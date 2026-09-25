package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ModelRun is one (context length, KV-cache precision) cell of a model benchmark.
type ModelRun struct {
	KVBits        int       `json:"kv_bits"` // 0 = fp16 KV cache
	PromptTokens  int       `json:"prompt_tokens"`
	PrefillTPS    float64   `json:"prefill_tps"` // median: how fast the prompt is read
	DecodeTPS     float64   `json:"decode_tps"`  // median: how fast tokens are written after that much context
	PrefillTrials []float64 `json:"prefill_trials"`
	DecodeTrials  []float64 `json:"decode_trials"`
	PeakGB        float64   `json:"peak_gb"`
}

// ModelResult is a full benchmark of one downloaded model.
type ModelResult struct {
	Repo   string     `json:"repo"`
	SizeGB float64    `json:"size_gb"`
	LoadS  float64    `json:"load_s"`
	Runs   []ModelRun `json:"runs"`
	At     int64      `json:"at"`              // unix ms
	Error  string     `json:"error,omitempty"` // this model failed; the others still ran
}

// Cell returns the run for a context length and KV precision, if measured.
func (r ModelResult) Cell(tokens, kvBits int) (ModelRun, bool) {
	for _, c := range r.Runs {
		if c.PromptTokens == tokens && c.KVBits == kvBits {
			return c, true
		}
	}
	return ModelRun{}, false
}

// ModelLengths are the context sizes measured: a short chat, a typical coding turn, and a long agentic session.
var ModelLengths = []int{1024, 4096, 16384}

// ModelBench measures one model directory with real code prompts: prefill and decode speed at several context
// lengths, with and without a quantised KV cache, plus load time and peak memory. The GPU must be otherwise idle.
func (m MLX) ModelBench(ctx context.Context, emit Emit, dir string) (ModelResult, error) {
	args := []string{"modelbench", "--model", dir, "--lengths"}
	for _, n := range ModelLengths {
		args = append(args, fmt.Sprint(n))
	}
	args = append(args, "--kv-bits", "0", "8", "--trials", "2", "--gen-tokens", "64")
	_, raws, err := m.run(ctx, emit, args...)
	if err != nil {
		return ModelResult{}, err
	}
	var out ModelResult
	for _, r := range raws {
		var l struct {
			Event        string    `json:"event"`
			LoadS        float64   `json:"load_s"`
			KVBits       int       `json:"kv_bits"`
			PromptTokens int       `json:"prompt_tokens"`
			Prefill      []float64 `json:"prefill_tps"`
			Decode       []float64 `json:"decode_tps"`
			PeakGB       float64   `json:"peak_gb"`
		}
		if json.Unmarshal(r, &l) != nil {
			continue
		}
		switch l.Event {
		case "loaded":
			out.LoadS = l.LoadS
		case "result":
			out.Runs = append(out.Runs, ModelRun{KVBits: l.KVBits, PromptTokens: l.PromptTokens, PrefillTPS: Median(l.Prefill), DecodeTPS: Median(l.Decode),
				PrefillTrials: l.Prefill, DecodeTrials: l.Decode, PeakGB: l.PeakGB})
		}
	}
	if len(out.Runs) == 0 {
		return ModelResult{}, errors.New("the benchmark produced no measurements")
	}
	sort.Slice(out.Runs, func(i, j int) bool {
		a, b := out.Runs[i], out.Runs[j]
		if a.KVBits != b.KVBits {
			return a.KVBits < b.KVBits
		}
		return a.PromptTokens < b.PromptTokens
	})
	return out, nil
}
