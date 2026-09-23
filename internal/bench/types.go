// Package bench runs the real measurements (SPEC §6): CPU memory bandwidth in Go, GPU and LLM workloads via
// MLX in the aituner venv, and LLM inference through a running Ollama. Nothing here is simulated.
package bench

import "sort"

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
}

func (m *Metric) Finalize() { m.Value = Median(m.Trials) }

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

// SpreadPct is the relative half-range of the trials around their median, in percent. It is the noise
// yardstick used to decide whether a before/after difference means anything.
func SpreadPct(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m := Median(v)
	if m == 0 {
		return 0
	}
	lo, hi := v[0], v[0]
	for _, x := range v {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	return (hi - lo) / 2 / m * 100
}
