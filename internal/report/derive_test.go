package report

import (
	"math"
	"strings"
	"testing"

	"github.com/tonynv/aituner/internal/bench"
)

func m(suite, engine, name string, trials ...float64) bench.Metric {
	x := bench.Metric{Suite: suite, Engine: engine, Name: name, Trials: trials}
	x.Finalize()
	return x
}

// Numbers are the ones measured on the reference machine (Mac Studio M1 Max).
func refMetrics() []bench.Metric {
	sustained := make([]float64, 20)
	for i := range sustained {
		sustained[i] = 7.0
	}
	return []bench.Metric{
		m("gpu", "mlx", "mem_bandwidth", 355),
		m("llm", "mlx", "generation_tps", 129.6),
		m("llm", "mlx", "prefill_1024_tps", 1000),
		m("llm", "mlx", "prefill_4096_tps", 920),
		m("llm", "mlx", "decode_4096_tps", 103),
		m("llm", "ollama", "generation_tps", 82.3),
		m("gpu", "mlx", "sustained_matmul_fp16", sustained...),
	}
}

func find(d []Derived, key string) (Derived, bool) {
	for _, x := range d {
		if x.Key == key {
			return x, true
		}
	}
	return Derived{}, false
}

func TestDeriveOnReferenceNumbers(t *testing.T) {
	d := Derive(refMetrics(), Inputs{RefBandwidthGBs: 400, BenchModelBytes: 1_824_825_759})
	check := func(key string, want, tol float64) {
		t.Helper()
		x, ok := find(d, key)
		if !ok || math.Abs(x.Value-want) > tol {
			t.Errorf("%s = %+v (ok=%v), want %.2f", key, x.Value, ok, want)
		}
		if ok && x.Formula == "" {
			t.Errorf("%s has no formula", key)
		}
	}
	check("gpu_bw_utilization", 88.75, 0.01) // 355/400
	check("decode_roofline_tps", 194.5, 0.2) // 355e9 / 1.8248e9
	check("decode_roofline_pct", 66.6, 0.2)  // 129.6 / 194.5
	check("ttft_1024_ms", 1024, 0.01)        // 1024 tok / 1000 tok/s
	check("ttft_4096_ms", 4096/920.0*1000, 0.01)
	check("long_context_retained", 103/129.6*100, 0.01)
	check("mlx_vs_ollama", 129.6/82.3, 0.001)
	if x, _ := find(d, "sustained_throttle_pct"); x.Value != 0 || x.Level != "ok" {
		t.Errorf("flat series must be ok: %+v", x)
	}
	if _, ok := find(d, "ttft_256_ms"); ok {
		t.Error("no prefill_256 metric, so no ttft_256")
	}
}

func TestDeriveSkipsWhatItCannotCompute(t *testing.T) {
	d := Derive(refMetrics(), Inputs{}) // offline: no reference bandwidth, no model size
	if _, ok := find(d, "gpu_bw_utilization"); ok {
		t.Error("utilisation needs a reference")
	}
	if _, ok := find(d, "decode_roofline_pct"); ok {
		t.Error("roofline needs the model size")
	}
	if len(Derive(nil, Inputs{})) != 0 {
		t.Error("no metrics, no insight")
	}
}

func TestThrottlePct(t *testing.T) {
	// 9 samples: first third mean 7.0, last third mean 5.5 -> 21.4% slower
	drop, ok := ThrottlePct([]float64{7, 7, 7, 6.5, 6.5, 6, 5.5, 5.5, 5.5})
	if !ok || math.Abs(drop-(7.0-5.5)/7.0*100) > 1e-9 {
		t.Fatalf("%v %v", drop, ok)
	}
	if _, ok := ThrottlePct([]float64{7, 7, 7}); ok {
		t.Fatal("too few samples must not be judged")
	}
	if _, ok := ThrottlePct([]float64{0, 0, 0, 0, 0, 0}); ok {
		t.Fatal("zero baseline cannot be judged")
	}
	if d, _ := ThrottlePct([]float64{5, 5, 5, 6, 6, 6}); d >= 0 {
		t.Fatalf("speeding up is negative drift, got %v", d)
	}
	series := []float64{7, 7, 7, 6.8, 6.5, 6.2, 6, 5.8, 5.6}
	d := Derive([]bench.Metric{m("gpu", "mlx", "sustained_matmul_fp16", series...)}, Inputs{})
	if x, _ := find(d, "sustained_throttle_pct"); x.Level != "warn" || !strings.Contains(x.Note, "throttling") {
		t.Fatalf("a falling series must warn: %+v", x)
	}
}
