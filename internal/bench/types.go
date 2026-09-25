// Package bench runs the real measurements (SPEC §6): CPU memory bandwidth in Go, GPU and LLM workloads via
// MLX in the aituner venv, and LLM inference through a running Ollama. Nothing here is simulated.
package bench

import (
	"sort"
	"strings"
)

// Metric is one measured quantity with all of its trials.
type Metric struct {
	Suite    string            `json:"suite"`
	Engine   string            `json:"engine"`
	Name     string            `json:"metric"`
	Unit     string            `json:"unit"`
	Trials   []float64         `json:"trials"`
	Value    float64           `json:"value"` // median of Trials
	Versions map[string]string `json:"versions,omitempty"`
	Model    string            `json:"model,omitempty"`
	Label    string            `json:"label"`
}

func (m *Metric) Finalize() {
	m.Value = Median(m.Trials)
	m.Label = LabelFor(m.Suite, m.Engine, m.Name)
}

// IsSeries reports whether the metric's "trials" are a time series (one sample per second), where variation over time is
// the signal (throttling) rather than measurement noise.
func (m Metric) IsSeries() bool { return strings.HasPrefix(m.Name, "sustained_") }

func (m Metric) Key() string { return m.Suite + "/" + m.Engine + "/" + m.Name }

// Event is progress/log output streamed to the UI.
type Event struct {
	Level    string  `json:"level"` // info | warn | error | trial
	Suite    string  `json:"suite,omitempty"`
	Message  string  `json:"message"`
	Progress float64 `json:"progress"` // 0..1 overall
}

type Emit func(Event)

func Median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	if len(c)%2 == 1 {
		return c[len(c)/2]
	}
	return (c[len(c)/2-1] + c[len(c)/2]) / 2
}

// SpreadPct is the relative half-range of the trials around their median, in percent: the noise yardstick used
// to decide whether a before/after difference means anything. With five or more trials the single lowest and
// highest are set aside first, so one disturbed trial (another app grabbing the GPU) does not inflate the noise
// estimate; the median already ignores it. UnstablePct reports the untrimmed figure for warnings.
func SpreadPct(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	if len(c) >= 5 {
		c = c[1 : len(c)-1]
	}
	return halfRangePct(c)
}

// UnstablePct is the untrimmed relative half-range, used to flag disturbed measurements.
func UnstablePct(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return halfRangePct(c)
}

// halfRangePct expects sorted input.
func halfRangePct(c []float64) float64 {
	if len(c) < 2 {
		return 0
	}
	m := Median(c)
	if m == 0 {
		return 0
	}
	return (c[len(c)-1] - c[0]) / 2 / m * 100
}

// UnstableThresholdPct: beyond this untrimmed spread a measurement is reported as disturbed.
const UnstableThresholdPct = 15.0

var labels = map[string]string{
	"memory/cpu/copy_bandwidth":     "CPU memory copy bandwidth",
	"memory/cpu/read_bandwidth":     "CPU memory read bandwidth",
	"gpu/mlx/matmul_fp16":           "GPU compute, fp16",
	"gpu/mlx/matmul_fp32":           "GPU compute, fp32",
	"gpu/mlx/sustained_matmul_fp16": "GPU compute, sustained (per second)",
	"gpu/mlx/mem_bandwidth":         "GPU memory bandwidth",
	"llm/mlx/prompt_tps":            "MLX prompt processing, 512 tokens",
	"llm/mlx/generation_tps":        "MLX text generation, 512-token context",
	"llm/mlx/peak_memory":           "MLX peak memory",
	"llm/mlx/prefill_256_tps":       "MLX prefill, 256-token prompt",
	"llm/mlx/prefill_1024_tps":      "MLX prefill, 1024-token prompt",
	"llm/mlx/prefill_4096_tps":      "MLX prefill, 4096-token prompt",
	"llm/mlx/decode_4096_tps":       "MLX text generation, 4096-token context",
	"llm/ollama/prompt_tps":         "Ollama prompt processing",
	"llm/ollama/generation_tps":     "Ollama text generation",
}

// LabelFor is the human name of a metric; unknown metrics fall back to their key so nothing is ever unlabeled.
func LabelFor(suite, engine, name string) string {
	k := suite + "/" + engine + "/" + name
	if l, ok := labels[k]; ok {
		return l
	}
	return k
}
