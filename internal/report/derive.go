// Package report turns raw benchmark metrics into insight (derived figures with their formulas) and renders
// exportable reports. Every derived number states what it was computed from; nothing is estimated silently.
package report

import (
	"fmt"

	"github.com/tonynv/aituner/internal/bench"
)

type Derived struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit"`
	Formula string  `json:"formula"`
	Level   string  `json:"level"` // ok | warn | info
	Note    string  `json:"note,omitempty"`
}

type Inputs struct {
	RefBandwidthGBs float64 // canirun.ai's reference bandwidth for this chip; 0 = unknown
	BenchModelBytes int64   // size of the MLX benchmark model; 0 = unknown
}

func get(ms []bench.Metric, suite, engine, name string) (bench.Metric, bool) {
	for _, m := range ms {
		if m.Suite == suite && m.Engine == engine && m.Name == name {
			return m, true
		}
	}
	return bench.Metric{}, false
}

// ThrottlePct is how much sustained throughput fell between the first and last third of a per-second series.
// Positive = slowed down. Fewer than 6 samples cannot say anything.
func ThrottlePct(series []float64) (float64, bool) {
	n := len(series)
	if n < 6 {
		return 0, false
	}
	third := n / 3
	mean := func(v []float64) float64 {
		var s float64
		for _, x := range v {
			s += x
		}
		return s / float64(len(v))
	}
	first, last := mean(series[:third]), mean(series[n-third:])
	if first <= 0 {
		return 0, false
	}
	return (first - last) / first * 100, true
}

// Derive computes the insight figures available from the metrics (skipping any whose inputs are missing).
func Derive(ms []bench.Metric, in Inputs) []Derived {
	var out []Derived
	gpuBW, haveBW := get(ms, "gpu", "mlx", "mem_bandwidth")
	gen, haveGen := get(ms, "llm", "mlx", "generation_tps")

	if haveBW && in.RefBandwidthGBs > 0 {
		u := gpuBW.Value / in.RefBandwidthGBs * 100
		lvl := "ok"
		if u < 70 {
			lvl = "warn"
		}
		out = append(out, Derived{Key: "gpu_bw_utilization", Label: "GPU memory bandwidth utilisation", Value: u, Unit: "%", Level: lvl,
			Formula: fmt.Sprintf("measured %.0f GB/s / reference %.0f GB/s (canirun.ai) x 100", gpuBW.Value, in.RefBandwidthGBs)})
	}
	if haveBW && haveGen && in.BenchModelBytes > 0 {
		roof := gpuBW.Value * 1e9 / float64(in.BenchModelBytes)
		out = append(out,
			Derived{Key: "decode_roofline_tps", Label: "Decode roofline (bandwidth limit)", Value: roof, Unit: "tok/s", Level: "info",
				Formula: fmt.Sprintf("GPU bandwidth %.0f GB/s / model size %.2f GB: each generated token reads every weight once", gpuBW.Value, float64(in.BenchModelBytes)/1e9)},
			Derived{Key: "decode_roofline_pct", Label: "Decode speed vs roofline", Value: gen.Value / roof * 100, Unit: "%", Level: "info",
				Formula: fmt.Sprintf("measured %.0f tok/s / roofline %.0f tok/s x 100", gen.Value, roof)})
	}
	for _, p := range []struct {
		name string
		tok  int
	}{{"prefill_256_tps", 256}, {"prefill_1024_tps", 1024}, {"prefill_4096_tps", 4096}} {
		if m, ok := get(ms, "llm", "mlx", p.name); ok && m.Value > 0 {
			out = append(out, Derived{Key: fmt.Sprintf("ttft_%d_ms", p.tok), Label: fmt.Sprintf("Time to first token, %d-token prompt", p.tok), Value: float64(p.tok) / m.Value * 1000, Unit: "ms", Level: "info",
				Formula: fmt.Sprintf("%d prompt tokens / %.0f tok/s prefill", p.tok, m.Value)})
		}
	}
	if d, ok := get(ms, "llm", "mlx", "decode_4096_tps"); ok && haveGen && gen.Value > 0 {
		kept := d.Value / gen.Value * 100
		lvl := "info"
		if kept < 70 {
			lvl = "warn"
		}
		out = append(out, Derived{Key: "long_context_retained", Label: "Decode speed kept at 4K context", Value: kept, Unit: "%", Level: lvl,
			Formula: fmt.Sprintf("%.0f tok/s at 4096 tokens / %.0f tok/s at 512 tokens x 100", d.Value, gen.Value),
			Note:    "Longer contexts read a larger KV cache for every token, so decoding slows as the conversation grows."})
	}
	if s, ok := get(ms, "gpu", "mlx", "sustained_matmul_fp16"); ok {
		if drop, ok := ThrottlePct(s.Trials); ok {
			lvl, note := "ok", "No sustained slowdown: the GPU held its speed for the whole run."
			if drop > 5 {
				lvl, note = "warn", "Throughput fell during continuous load: the machine is throttling (heat or power). Check cooling and power mode."
			}
			out = append(out, Derived{Key: "sustained_throttle_pct", Label: "Sustained GPU slowdown", Value: drop, Unit: "%", Level: lvl,
				Formula: fmt.Sprintf("(mean of first third - mean of last third) / first third, over %d one-second samples", len(s.Trials)), Note: note})
		}
	}
	if o, ok := get(ms, "llm", "ollama", "generation_tps"); ok && haveGen && o.Value > 0 {
		out = append(out, Derived{Key: "mlx_vs_ollama", Label: "MLX vs Ollama generation speed", Value: gen.Value / o.Value, Unit: "x", Level: "info",
			Formula: fmt.Sprintf("MLX %.0f tok/s / Ollama %.0f tok/s (same-size 4-bit Llama 3.2 3B in each)", gen.Value, o.Value)})
	}
	return out
}
