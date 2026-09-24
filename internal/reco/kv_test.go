package reco

import (
	"math"
	"os"
	"strings"
	"testing"
)

// Fixtures are real config.json files from Hugging Face (mlx-community repos).
func cfg(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/configs/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestKVDenseModelsMatchKnownFigures(t *testing.T) {
	// Llama-3.1-8B: 32 layers x 2 (K,V) x 8 kv-heads x 128 dim x 2 bytes = 131072 B/token (128 KiB): the widely quoted figure
	for name, want := range map[string]float64{
		"Llama-3.1-8B-Instruct-4bit": 32 * 2 * 8 * 128 * 2,
		"Qwen3-8B-4bit":              36 * 2 * 8 * 128 * 2,
		"granite-4.1-8b-8bit":        40 * 2 * 8 * 128 * 2, // head_dim derived: hidden 4096 / 32 heads
	} {
		p, err := KVFromConfig(cfg(t, name))
		if err != nil || !p.Known || p.PerTokenBytes != want || p.FixedBytes != 0 {
			t.Errorf("%s: %+v err=%v want %v", name, p, err, want)
		}
	}
	if p, _ := KVFromConfig(cfg(t, "Llama-3.1-8B-Instruct-4bit")); p.MaxContext != 131072 {
		t.Fatalf("max context %d", p.MaxContext)
	}
}

func TestKVHybridLinearAttentionCountsOnlyFullLayers(t *testing.T) {
	p, err := KVFromConfig(cfg(t, "Qwen3.6-35B-A3B-4bit")) // nested text_config, 10 full + 30 linear layers
	if err != nil || !p.Known {
		t.Fatal(p, err)
	}
	if p.FullLayers != 10 || p.OtherLayers != 30 || p.SlidingLayers != 0 {
		t.Fatalf("layers: %+v", p)
	}
	if want := 10.0 * 2 * 2 * 256 * 2; p.PerTokenBytes != want { // 2 kv heads, head_dim 256
		t.Fatalf("per token %v want %v", p.PerTokenBytes, want)
	}
	if p.MaxContext != 262144 {
		t.Fatalf("max ctx %d", p.MaxContext)
	}
}

func TestKVSlidingWindowPlusGlobalLayers(t *testing.T) {
	p, err := KVFromConfig(cfg(t, "gemma-4-26b-a4b-it-4bit"))
	if err != nil || !p.Known {
		t.Fatal(p, err)
	}
	if p.FullLayers != 5 || p.SlidingLayers != 25 {
		t.Fatalf("layers: %+v", p)
	}
	// global layers: 2 kv heads x 512 dim; sliding layers: 8 kv heads x 256 dim, window 1024, cost is constant
	if want := 5.0 * 2 * 2 * 512 * 2; p.PerTokenBytes != want {
		t.Fatalf("per token %v want %v", p.PerTokenBytes, want)
	}
	if want := 25.0 * 2 * 8 * 256 * 2 * 1024; p.FixedBytes != want {
		t.Fatalf("fixed %v want %v", p.FixedBytes, want)
	}
}

func TestKVConvHybrid(t *testing.T) {
	p, err := KVFromConfig(cfg(t, "LFM2-24B-A2B-4bit"))
	if err != nil || !p.Known || p.FullLayers == 0 || p.OtherLayers == 0 || p.FullLayers+p.OtherLayers != 40 {
		t.Fatalf("%+v %v", p, err)
	}
	if want := float64(p.FullLayers) * 2 * 8 * 64 * 2; p.PerTokenBytes != want { // head_dim = 2048/32
		t.Fatalf("per token %v want %v", p.PerTokenBytes, want)
	}
}

func TestKVUnknownWhenConfigIsInsufficientOrExotic(t *testing.T) {
	for name, raw := range map[string]string{
		"mla":            `{"num_hidden_layers":4,"num_attention_heads":8,"kv_lora_rank":512}`,
		"no layers":      `{"num_attention_heads":8}`,
		"no heads":       `{"num_hidden_layers":4}`,
		"no dim":         `{"num_hidden_layers":4,"num_attention_heads":8}`,
		"types mismatch": `{"num_hidden_layers":4,"num_attention_heads":8,"hidden_size":64,"layer_types":["full_attention"]}`,
		"sliding no win": `{"num_hidden_layers":1,"num_attention_heads":8,"hidden_size":64,"layer_types":["sliding_attention"]}`,
	} {
		p, err := KVFromConfig([]byte(raw))
		if err != nil || p.Known || p.Reason == "" {
			t.Errorf("%s: must be unknown with a reason: %+v %v", name, p, err)
		}
	}
	if _, err := KVFromConfig([]byte(`not json`)); err == nil {
		t.Fatal("bad JSON must error")
	}
}

func TestKVFitArithmeticAndCaps(t *testing.T) {
	p, _ := KVFromConfig(cfg(t, "Llama-3.1-8B-Instruct-4bit"))
	gib := int64(1 << 30)
	// budget 25 GiB, weights 4.5 GiB, 1 GiB overhead: room 19.5 GiB, capped by the model's own 131072-token limit
	f, err := p.Fit(gib*9/2, 25*gib)
	if err != nil || !f.Known || math.Abs(f.RoomGB-19.5) > 1e-9 || f.TokensF16 != 131072 || f.Tokens8Bit != 131072 {
		t.Fatalf("%+v %v", f, err)
	}
	// tight: room 3 GiB -> 3 GiB / 128 KiB = 24576 tokens fp16; 8-bit stores ~53% as much, so ~1.88x more
	f, _ = p.Fit(4*gib, 8*gib)
	if f.TokensF16 != 24576 {
		t.Fatalf("fp16 tokens %d", f.TokensF16)
	}
	if want := int(3 * float64(gib) / (131072 * kv8Ratio)); f.Tokens8Bit != want || f.Tokens8Bit <= f.TokensF16 {
		t.Fatalf("8-bit tokens %d want %d", f.Tokens8Bit, want)
	}
	// no room at all
	if f, _ = p.Fit(7*gib, 8*gib); f.TokensF16 != 0 || f.RoomGB != 0 {
		t.Fatalf("no room: %+v", f)
	}
	// invalid input
	if _, err := p.Fit(0, gib); err == nil {
		t.Fatal("zero weights must error")
	}
}

func TestKVFitSlidingWindowFixedCostComesFirst(t *testing.T) {
	p, _ := KVFromConfig(cfg(t, "gemma-4-26b-a4b-it-4bit"))
	gib := int64(1 << 30)
	f, _ := p.Fit(15*gib, 25*gib) // room 9 GiB minus ~200 MiB fixed sliding-window cost
	want := int((9*float64(gib) - p.FixedBytes) / p.PerTokenBytes)
	if want > p.MaxContext {
		want = p.MaxContext
	}
	if f.TokensF16 != want {
		t.Fatalf("tokens %d want %d", f.TokensF16, want)
	}
	// fixed cost larger than the room: nothing fits
	if f, _ = p.Fit(24*gib+gib/2, 25*gib); f.TokensF16 != 0 {
		t.Fatalf("expected 0, got %d", f.TokensF16)
	}
}

func TestKVFitCarriesEverythingTheMeterNeeds(t *testing.T) {
	p, _ := KVFromConfig(cfg(t, "gemma-4-26b-a4b-it-4bit"))
	gib := int64(1 << 30)
	f, _ := p.Fit(14*gib, 25*gib)
	// weights + overhead + room must add back up to the budget, or the bar would not be drawn to scale
	if sum := f.WeightsGB + f.OverheadGB + f.RoomGB; math.Abs(sum-25) > 1e-9 {
		t.Fatalf("segments sum to %v, budget is 25", sum)
	}
	if f.FixedBytes != int64(p.FixedBytes) || f.FixedBytes == 0 || f.BytesPerTok != int64(p.PerTokenBytes) || f.OverheadGB != 1 {
		t.Fatalf("%+v", f)
	}
}

func TestKVFitUnknownStillReportsRoom(t *testing.T) {
	p, _ := KVFromConfig([]byte(`{"kv_lora_rank":1}`))
	f, _ := p.Fit(1<<30, 8<<30)
	if f.Known || f.TokensF16 != 0 || f.RoomGB < 5 || !strings.Contains(f.Reason, "latent") {
		t.Fatalf("%+v", f)
	}
}

func FuzzKVFromConfig(f *testing.F) {
	for _, n := range []string{"Llama-3.1-8B-Instruct-4bit", "gemma-4-26b-a4b-it-4bit"} {
		b, _ := os.ReadFile("testdata/configs/" + n + ".json")
		f.Add(b, int64(5<<30), int64(25<<30))
	}
	f.Add([]byte(`{"num_hidden_layers":-1,"num_attention_heads":0}`), int64(1), int64(1))
	f.Fuzz(func(t *testing.T, raw []byte, w, b int64) {
		p, err := KVFromConfig(raw)
		if err != nil {
			return
		}
		fit, err := p.Fit(w, b)
		if err != nil {
			return
		}
		if fit.TokensF16 < 0 || fit.Tokens8Bit < 0 || math.IsNaN(fit.RoomGB) || fit.RoomGB < 0 {
			t.Fatalf("invalid fit %+v from %+v", fit, p)
		}
		if p.MaxContext > 0 && fit.TokensF16 > p.MaxContext {
			t.Fatalf("exceeds model limit: %+v", fit)
		}
	})
}
