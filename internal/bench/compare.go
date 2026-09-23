package bench

import "math"

// NoiseFloorPct is the minimum band below which a difference is never called real, even if trials were
// suspiciously identical: run-to-run drift (thermals, background load) below this is not evidence.
const NoiseFloorPct = 2.0

type Stat struct {
	Median    float64 `json:"median"`
	SpreadPct float64 `json:"spread_pct"`
	N         int     `json:"n"`
}

type Row struct {
	Key      string  `json:"key"`
	Suite    string  `json:"suite"`
	Engine   string  `json:"engine"`
	Metric   string  `json:"metric"`
	Unit     string  `json:"unit"`
	Before   Stat    `json:"before"`
	After    Stat    `json:"after"`
	DeltaPct float64 `json:"delta_pct"`
	NoisePct float64 `json:"noise_pct"`
	// Verdict: faster | slower | within_noise | info (memory use etc., no better/worse)
	Verdict string `json:"verdict"`
}

func stat(m Metric) Stat {
	return Stat{Median: m.Value, SpreadPct: SpreadPct(m.Trials), N: len(m.Trials)}
}

func isInfo(m Metric) bool { return m.Name == "peak_memory" }

// Compare pairs metrics by key and classifies each change against the measured noise of both runs.
func Compare(before, after []Metric) []Row {
	idx := map[string]Metric{}
	for _, m := range after {
		idx[m.Key()] = m
	}
	rows := []Row{}
	for _, b := range before {
		a, ok := idx[b.Key()]
		if !ok || b.Value == 0 {
			continue
		}
		r := Row{Key: b.Key(), Suite: b.Suite, Engine: b.Engine, Metric: b.Name, Unit: b.Unit, Before: stat(b), After: stat(a)}
		r.DeltaPct = (a.Value - b.Value) / b.Value * 100
		r.NoisePct = math.Max(NoiseFloorPct, math.Max(r.Before.SpreadPct, r.After.SpreadPct))
		switch {
		case isInfo(b):
			r.Verdict = "info"
		case math.Abs(r.DeltaPct) <= r.NoisePct:
			r.Verdict = "within_noise"
		case r.DeltaPct > 0:
			r.Verdict = "faster"
		default:
			r.Verdict = "slower"
		}
		rows = append(rows, r)
	}
	return rows
}
