package reco

import (
	"testing"

	"github.com/tonynv/aituner/internal/hf"
)

func repos(ids ...string) []hf.Repo {
	var r []hf.Repo
	for _, id := range ids {
		r = append(r, hf.Repo{ID: id})
	}
	return r
}

func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"https://huggingface.co/lmstudio-community/Qwen3.6-35B-A3B-GGUF": "Qwen3.6-35B-A3B",
		"https://huggingface.co/Tongyi-MAI/Z-Image-Turbo":                "Z-Image-Turbo",
		"https://huggingface.co/x/Foo-MLX":                               "Foo",
		"https://huggingface.co/onlyone":                                 "",
	}
	for in, want := range cases {
		if got := BaseName(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

// Names below are real mlx-community / community repos returned by the Hub API for Qwen3.6 searches.
func TestMatchMLXAcceptsOnlyPlainQuants(t *testing.T) {
	rs := repos(
		"mlx-community/Qwen3.6-35B-A3B-4bit",
		"mlx-community/Qwen3.6-35B-A3B-8bit",
		"mlx-community/Qwen3.6-35B-A3B-OptiQ-4bit",                                  // different quant scheme
		"mlx-community/Qwen3.6-35B-A3B-OptiQ-4bit-REAP-19B",                         // pruned
		"mlx-community/Huihui-Qwen3.6-35B-A3B-Claude-4.7-Opus-abliterated-mlx-8bit", // fine-tune
		"mlx-community/Qwen3.6-27B-4bit",                                            // different size
		"someone/Qwen3.6-35B-A3B-4bit",                                              // not mlx-community
	)
	got := MatchMLX("Qwen3.6-35B-A3B", rs)
	if len(got) != 2 || got[0].Bits != 4 || got[1].Bits != 8 {
		t.Fatalf("%+v", got)
	}
	if len(MatchMLX("Qwen3.6-27B", rs)) != 1 {
		t.Fatal("27B must match only its own quant")
	}
}

func TestMatchMLXInstructAndBF16(t *testing.T) {
	got := MatchMLX("Llama-3.2-3B", repos("mlx-community/Llama-3.2-3B-Instruct-4bit", "mlx-community/Llama-3.2-3B-Instruct-bf16", "mlx-community/Llama-3.2-3B-Instruct-4bit-DWQ"))
	if len(got) != 3 || got[1].Bits != 16 {
		t.Fatalf("%+v", got)
	}
}

func TestUnrestrictedVariant(t *testing.T) {
	yes := []string{
		"mlx-community/Huihui-Qwen3.6-35B-A3B-Claude-4.7-Opus-abliterated-mlx-8bit",
		"x/Qwen3.6-35B-A3B-uncensored-4bit",
	}
	no := []string{"mlx-community/Qwen3.6-35B-A3B-4bit", "x/Qwen3.5-9B-abliterated-4bit"}
	for _, id := range yes {
		if !IsUnrestrictedVariant("Qwen3.6-35B-A3B", hf.Repo{ID: id}) {
			t.Errorf("should match %s", id)
		}
	}
	for _, id := range no {
		if IsUnrestrictedVariant("Qwen3.6-35B-A3B", hf.Repo{ID: id}) {
			t.Errorf("should not match %s", id)
		}
	}
}

func TestActiveParams(t *testing.T) {
	if ActiveParamsBillions("Qwen3.6-35B-A3B") != 3 || ActiveParamsBillions("gemma4-26b-a4b-it") != 4 || ActiveParamsBillions("Llama-3.3-70B") != 0 {
		t.Fatal("active params parse")
	}
	for name, want := range map[string]int{
		"x-abliterated-MLX-4bit": 4, "x-8Bit": 8, "ornith-1.0-9b-abliterated-mxfp4-mlx": 4, "gemma-abliterix-MLX-4bit-nvfp4": 4,
		"Llama-abliterated-q8-mlx": 8, "gemma-MLX-2bit-int2-affine": 2, "Qwen3-8B-abliterated-v2-mxfp4": 4, "x": 0,
		"granite-4.1-8b-Abliterated-AND-Disinhibited-mxfp8-mlx": 8,
	} {
		if got := bitsFromName(name); got != want {
			t.Errorf("bitsFromName(%q)=%d want %d", name, got, want)
		}
	}
}

func TestFitStatusAndEstimate(t *testing.T) {
	budget := int64(27648) << 20 // 27 GiB
	if FitStatus(10<<30, budget) != "comfortable" || FitStatus(24<<30, budget) != "tight" || FitStatus(27<<30, budget) != "too_large" {
		t.Fatal("fit classification")
	}
	in := Input{GPUBandwidthGBs: 355, MLXGenTPS: 129.6, BenchModelBytes: 1_824_825_759}
	// the calibration must reproduce the benchmark model's own measured speed
	tps, eff := Estimate(in, in.BenchModelBytes, 1)
	if tps < 129.5 || tps > 129.7 || eff < 0.6 || eff > 0.7 {
		t.Fatalf("calibration: tps=%v eff=%v", tps, eff)
	}
	// twice the bytes per token => half the speed; MoE reading 1/10 of weights => 10x
	if t2, _ := Estimate(in, 2*in.BenchModelBytes, 1); t2 < 64 || t2 > 65.5 {
		t.Fatalf("scaling: %v", t2)
	}
	if t3, _ := Estimate(in, in.BenchModelBytes, 0.1); t3 < 1290 || t3 > 1300 {
		t.Fatalf("moe: %v", t3)
	}
	if v, _ := Estimate(Input{}, 1, 1); v != 0 {
		t.Fatal("must not invent an estimate without measurements")
	}
}

func TestPreferQuants(t *testing.T) {
	qs := []MLXQuant{{Bits: 16}, {Bits: 4}, {Bits: 8}, {Bits: 6}}
	budget := int64(27) << 30
	// big model: 4-bit first, bf16 last
	big := preferQuants(qs, budget, 27)
	if big[0].Bits != 4 || big[len(big)-1].Bits != 16 {
		t.Fatalf("big: %+v", big)
	}
	// small model (8 GB of weights at 1 byte/param < 30% of budget): 8-bit first
	small := preferQuants(qs, budget, 3)
	if small[0].Bits != 8 || small[1].Bits != 4 || small[len(small)-1].Bits != 16 {
		t.Fatalf("small: %+v", small)
	}
}

func TestSupportsGate(t *testing.T) {
	in := Input{SupportedModelTypes: map[string]bool{"llama": true}}
	if !in.supports("llama") || in.supports("diffusion_gemma") {
		t.Fatal("gate")
	}
	if !(Input{}).supports("anything") {
		t.Fatal("unchecked input must not block")
	}
}
