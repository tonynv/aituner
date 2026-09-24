package reco

import (
	"encoding/json"
	"errors"
	"math"
)

// KVProfile is how a model's KV cache grows, derived from its config.json (never guessed).
//
// Full-attention layers grow linearly with context; sliding-window layers stop growing once the context
// exceeds the window; linear-attention / convolution layers keep a small constant state that is ignored.
// If a model's config does not give enough to compute this honestly, Known is false and Reason says why.
type KVProfile struct {
	Known         bool    `json:"known"`
	Reason        string  `json:"reason,omitempty"`
	PerTokenBytes float64 `json:"per_token_bytes"` // fp16 KV bytes added per context token (full-attention layers)
	FixedBytes    float64 `json:"fixed_bytes"`     // fp16 KV bytes from sliding-window layers once the window is full
	MaxContext    int     `json:"max_context"`     // model's max_position_embeddings (0 = not stated)
	FullLayers    int     `json:"full_layers"`
	SlidingLayers int     `json:"sliding_layers"`
	OtherLayers   int     `json:"other_layers"` // conv / linear-attention layers (constant state, not counted)
}

const (
	bytesFP16 = 2
	// KV quantised to 8 bits in groups of 64 stores an fp16 scale and bias per group: (8 + 2*16/64) bits per element.
	kv8Ratio = (8.0 + 2*16.0/64.0) / 16.0
	// runtime + activation overhead kept out of the KV budget
	runtimeOverheadBytes = 1 << 30
)

func num(m map[string]any, k string) (float64, bool) {
	v, ok := m[k].(float64)
	return v, ok && v > 0
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// KVFromConfig computes the profile from a Hugging Face config.json (text_config is used when nested).
func KVFromConfig(raw []byte) (KVProfile, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return KVProfile{}, err
	}
	c := top
	if t, ok := top["text_config"].(map[string]any); ok {
		c = t
	}
	if _, mla := c["kv_lora_rank"]; mla {
		return KVProfile{Reason: "multi-head latent attention: cache layout is implementation-specific"}, nil
	}
	layers, ok := num(c, "num_hidden_layers")
	if !ok {
		return KVProfile{Reason: "config has no num_hidden_layers"}, nil
	}
	heads, ok := num(c, "num_attention_heads")
	if !ok {
		return KVProfile{Reason: "config has no num_attention_heads"}, nil
	}
	kvHeads := heads
	if v, ok := num(c, "num_key_value_heads"); ok {
		kvHeads = v
	}
	headDim, ok := num(c, "head_dim")
	if !ok {
		hs, ok2 := num(c, "hidden_size")
		if !ok2 {
			return KVProfile{Reason: "config has neither head_dim nor hidden_size"}, nil
		}
		headDim = hs / heads
	}
	p := KVProfile{}
	if mp, ok := num(c, "max_position_embeddings"); ok {
		p.MaxContext = int(mp)
	}
	perLayerTok := func(kvh, hd float64) float64 { return 2 * kvh * hd * bytesFP16 } // K and V, per token per layer

	types, _ := c["layer_types"].([]any)
	if len(types) == 0 {
		// no per-layer detail: assume every layer keeps a full-length cache. This can only over-estimate KV
		// use (a sliding-window model would need less), so the context we report is a safe lower bound.
		p.Known, p.FullLayers = true, int(layers)
		p.PerTokenBytes = layers * perLayerTok(kvHeads, headDim)
		return p, nil
	}
	if float64(len(types)) != layers {
		return KVProfile{Reason: "layer_types length does not match num_hidden_layers"}, nil
	}
	window, _ := num(c, "sliding_window")
	gHeads, gDim := kvHeads, headDim
	if v, ok := num(c, "num_global_key_value_heads"); ok {
		gHeads = v
	}
	if v, ok := num(c, "global_head_dim"); ok {
		gDim = v
	}
	for _, t := range types {
		switch s, _ := t.(string); s {
		case "full_attention":
			p.FullLayers++
			p.PerTokenBytes += perLayerTok(gHeads, gDim)
		case "sliding_attention":
			if window <= 0 {
				return KVProfile{Reason: "sliding_attention layers but no sliding_window in config"}, nil
			}
			p.SlidingLayers++
			p.FixedBytes += perLayerTok(kvHeads, headDim) * window
		default:
			p.OtherLayers++
		}
	}
	p.Known = true
	return p, nil
}

// KVFit is what a profile means for a specific model size inside a specific memory budget.
type KVFit struct {
	Known       bool    `json:"known"`
	Reason      string  `json:"reason,omitempty"`
	WeightsGB   float64 `json:"weights_gb"`
	RoomGB      float64 `json:"room_gb"` // budget left for KV cache after weights and runtime overhead
	BudgetGB    float64 `json:"budget_gb"`
	TokensF16   int     `json:"tokens_fp16"` // context tokens that fit with an fp16 KV cache
	Tokens8Bit  int     `json:"tokens_8bit"` // ... with an 8-bit quantised KV cache
	MaxContext  int     `json:"max_context"` // the model's own limit (0 = unknown)
	BytesPerTok int64   `json:"bytes_per_token_fp16"`
	FixedBytes  int64   `json:"fixed_bytes"` // sliding-window layers: constant KV cost once the window is full
	OverheadGB  float64 `json:"overhead_gb"` // runtime/activation memory kept out of the KV budget
	Note        string  `json:"note,omitempty"`
}

func tokensFor(room float64, p KVProfile, scale float64) int {
	room -= p.FixedBytes * scale
	if room <= 0 {
		return 0
	}
	var t float64
	if p.PerTokenBytes == 0 {
		t = math.MaxInt32 // nothing grows with context: bounded only by the model's own limit
	} else {
		t = room / (p.PerTokenBytes * scale)
	}
	if p.MaxContext > 0 && t > float64(p.MaxContext) {
		t = float64(p.MaxContext)
	}
	if t > math.MaxInt32 {
		t = math.MaxInt32
	}
	return int(t)
}

// Fit places a model of weightsBytes into budgetBytes and reports the KV cache room that remains.
func (p KVProfile) Fit(weightsBytes, budgetBytes int64) (KVFit, error) {
	if budgetBytes <= 0 || weightsBytes <= 0 {
		return KVFit{}, errors.New("budget and weights must be positive")
	}
	f := KVFit{Known: p.Known, Reason: p.Reason, MaxContext: p.MaxContext,
		WeightsGB: float64(weightsBytes) / (1 << 30), BudgetGB: float64(budgetBytes) / (1 << 30), OverheadGB: float64(runtimeOverheadBytes) / (1 << 30)}
	room := float64(budgetBytes - weightsBytes - runtimeOverheadBytes)
	if room < 0 {
		room = 0
	}
	f.RoomGB = room / (1 << 30)
	if !p.Known {
		return f, nil
	}
	f.BytesPerTok = int64(p.PerTokenBytes)
	f.FixedBytes = int64(p.FixedBytes)
	f.TokensF16 = tokensFor(room, p, 1)
	f.Tokens8Bit = tokensFor(room, p, kv8Ratio)
	if p.OtherLayers > 0 {
		f.Note = "Hybrid model: its linear-attention/convolution layers keep a small constant state that is not counted."
	}
	return f, nil
}
