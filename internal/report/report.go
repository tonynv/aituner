package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/platform"
	"github.com/tonynv/aituner/internal/store"
)

type Stage struct {
	Metrics  []bench.Metric `json:"metrics"`
	Derived  []Derived      `json:"derived"`
	Warnings []string       `json:"warnings"`
	Skipped  []string       `json:"skipped"`
}

type Report struct {
	GeneratedAt int64              `json:"generated_at"`
	Aituner     string             `json:"aituner_version"`
	Run         store.Run          `json:"run"`
	Hardware    json.RawMessage    `json:"hardware"`
	Stages      map[string]Stage   `json:"stages"`
	Compare     []bench.Row        `json:"compare"`
	Changes     []store.TuneChange `json:"tune_changes"`
	Versions    map[string]string  `json:"versions"`
}

// MetricsFrom rebuilds bench metrics for one stage from stored results.
func MetricsFrom(rs []store.Result, stage string) []bench.Metric {
	out := []bench.Metric{}
	for _, r := range rs {
		if r.Stage != stage {
			continue
		}
		m := bench.Metric{Suite: r.Suite, Engine: r.Engine, Name: r.Metric, Unit: r.Unit, Value: r.Value}
		_ = json.Unmarshal(r.Trials, &m.Trials)
		var v map[string]string
		if json.Unmarshal(r.Versions, &v) == nil {
			m.Versions = v
		}
		m.Label = bench.LabelFor(m.Suite, m.Engine, m.Name)
		out = append(out, m)
	}
	return out
}

// Headline is the small set of numbers shown in the pinned bar and run history.
type Headline struct {
	GPUFP16   float64 `json:"gpu_fp16_tflops"`
	GPUBW     float64 `json:"gpu_bandwidth_gbs"`
	MLXGen    float64 `json:"mlx_generation_tps"`
	MLXPrompt float64 `json:"mlx_prompt_tps"`
	OllamaGen float64 `json:"ollama_generation_tps"`
	CPUCopyBW float64 `json:"cpu_copy_gbs"`
}

func HeadlineOf(ms []bench.Metric) Headline {
	v := func(suite, engine, name string) float64 {
		m, _ := get(ms, suite, engine, name)
		return m.Value
	}
	return Headline{GPUFP16: v("gpu", "mlx", "matmul_fp16"), GPUBW: v("gpu", "mlx", "mem_bandwidth"), MLXGen: v("llm", "mlx", "generation_tps"),
		MLXPrompt: v("llm", "mlx", "prompt_tps"), OllamaGen: v("llm", "ollama", "generation_tps"), CPUCopyBW: v("memory", "cpu", "copy_bandwidth")}
}

// Build assembles a report for a run. meta gives per-stage warnings/skips (stored with the results).
func Build(run store.Run, machine json.RawMessage, rs []store.Result, changes []store.TuneChange, meta map[string]StageMeta, in Inputs, version string) Report {
	r := Report{GeneratedAt: time.Now().UnixMilli(), Aituner: version, Run: run, Hardware: machine, Stages: map[string]Stage{}, Changes: changes, Versions: map[string]string{}}
	for _, stage := range []string{"baseline", "tuned"} {
		ms := MetricsFrom(rs, stage)
		if len(ms) == 0 {
			continue
		}
		mt := meta[stage]
		r.Stages[stage] = Stage{Metrics: ms, Derived: Derive(ms, in), Warnings: nz(mt.Warnings), Skipped: nz(mt.Skipped)}
		for _, m := range ms {
			for k, v := range m.Versions {
				r.Versions[k] = v
			}
		}
	}
	if b, t := r.Stages["baseline"], r.Stages["tuned"]; len(b.Metrics) > 0 && len(t.Metrics) > 0 {
		r.Compare = bench.Compare(b.Metrics, t.Metrics)
	}
	if r.Changes == nil {
		r.Changes = []store.TuneChange{}
	}
	return r
}

type StageMeta struct {
	Warnings []string `json:"warnings"`
	Skipped  []string `json:"skipped"`
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func fnum(v float64) string {
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f", v)
	case v >= 10:
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

func cell(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s) }

// Markdown renders a readable report.
func Markdown(r Report) string {
	var b strings.Builder
	var hw platform.Hardware
	_ = json.Unmarshal(r.Hardware, &hw)
	fmt.Fprintf(&b, "# aituner benchmark report\n\n")
	fmt.Fprintf(&b, "- Run: `%s`  (%s, %s)\n", r.Run.ID, r.Run.Phase, time.UnixMilli(r.Run.CreatedAt).Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "- Machine: %s (%s), %s, %d CPU cores (%dP+%dE), %d GPU cores, %.0f GB %s\n", hw.Model.Name, hw.Model.Identifier, hw.CPU.Chip,
		hw.CPU.Cores, hw.CPU.PerformanceCores, hw.CPU.EfficiencyCores, hw.GPU.Cores, float64(hw.Memory.TotalBytes)/(1<<30), hw.Memory.Type)
	fmt.Fprintf(&b, "- OS: %s %s (%s)\n", hw.OS.Name, hw.OS.Version, hw.OS.Build)
	keys := make([]string, 0, len(r.Versions))
	for k := range r.Versions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %s\n", k, r.Versions[k])
	}
	b.WriteString("\n")
	for _, stage := range []string{"baseline", "tuned"} {
		st, ok := r.Stages[stage]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "## %s results\n\n| Measurement | Result | Spread | Trials |\n|---|---:|---:|---:|\n", strings.ToUpper(stage[:1])+stage[1:])
		for _, m := range st.Metrics {
			fmt.Fprintf(&b, "| %s | %s %s | %.1f%% | %d |\n", cell(m.Label), fnum(m.Value), m.Unit, bench.SpreadPct(m.Trials), len(m.Trials))
		}
		if len(st.Derived) > 0 {
			b.WriteString("\n**Derived**\n\n| Figure | Value | How |\n|---|---:|---|\n")
			for _, d := range st.Derived {
				fmt.Fprintf(&b, "| %s | %s %s | %s |\n", cell(d.Label), fnum(d.Value), d.Unit, cell(d.Formula))
			}
		}
		for _, w := range st.Warnings {
			fmt.Fprintf(&b, "\n> Warning: %s\n", w)
		}
		b.WriteString("\n")
	}
	if len(r.Changes) > 0 {
		b.WriteString("## Tuning applied\n\n")
		for _, c := range r.Changes {
			state := "applied"
			if c.RevertedAt != nil {
				state = "reverted"
			}
			fmt.Fprintf(&b, "- `%s`: %s -> %s (%s)\n", c.Key, c.Before, c.After, state)
		}
		b.WriteString("\n")
	}
	if len(r.Compare) > 0 {
		b.WriteString("## Before and after\n\n| Measurement | Before | After | Change | Verdict |\n|---|---:|---:|---:|---|\n")
		for _, c := range r.Compare {
			fmt.Fprintf(&b, "| %s | %s | %s | %+.1f%% | %s (noise +-%.1f%%) |\n", cell(bench.LabelFor(c.Suite, c.Engine, c.Metric)), fnum(c.Before.Median), fnum(c.After.Median), c.DeltaPct, c.Verdict, c.NoisePct)
		}
	}
	return b.String()
}

// CSV renders one row per metric per stage, with every trial.
func CSV(r Report) (string, error) {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	if err := w.Write([]string{"stage", "suite", "engine", "metric", "label", "unit", "median", "spread_pct", "trials"}); err != nil {
		return "", err
	}
	for _, stage := range []string{"baseline", "tuned"} {
		for _, m := range r.Stages[stage].Metrics {
			tr := make([]string, len(m.Trials))
			for i, v := range m.Trials {
				tr[i] = fmt.Sprintf("%.4f", v)
			}
			if err := w.Write([]string{stage, m.Suite, m.Engine, m.Name, m.Label, m.Unit, fmt.Sprintf("%.4f", m.Value), fmt.Sprintf("%.2f", bench.SpreadPct(m.Trials)), strings.Join(tr, ";")}); err != nil {
				return "", err
			}
		}
	}
	w.Flush()
	return sb.String(), w.Error()
}
