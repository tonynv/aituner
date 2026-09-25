package report

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tonynv/aituner/internal/store"
)

func rs(stage string, vals map[string]float64) []store.Result {
	var out []store.Result
	for k, v := range vals {
		p := strings.Split(k, "/")
		tr, _ := json.Marshal([]float64{v * 0.99, v, v * 1.01})
		ve, _ := json.Marshal(map[string]string{"mlx": "0.32.2"})
		out = append(out, store.Result{Stage: stage, Suite: p[0], Engine: p[1], Metric: p[2], Value: v, Unit: "tok/s", Trials: tr, Versions: ve})
	}
	return out
}

func sample() Report {
	all := append(rs("baseline", map[string]float64{"llm/mlx/generation_tps": 130, "gpu/mlx/mem_bandwidth": 355}),
		rs("tuned", map[string]float64{"llm/mlx/generation_tps": 131, "gpu/mlx/mem_bandwidth": 356})...)
	hw := json.RawMessage(`{"model":{"name":"Mac Studio","identifier":"Mac13,1"},"cpu":{"chip":"Apple M1 Max","cores":10,"performance_cores":8,"efficiency_cores":2},"gpu":{"cores":24},"memory":{"total_bytes":34359738368,"type":"LPDDR5"},"os":{"name":"macOS","version":"26.5.1","build":"25F80"}}`)
	run := store.Run{ID: "abc123", Phase: "tuned_done", CreatedAt: 1790000000000}
	meta := map[string]StageMeta{"baseline": {Warnings: []string{"a | b warning"}}}
	return Build(run, hw, all, []store.TuneChange{{Key: "ollama.env", Before: "unset", After: "1"}}, meta, Inputs{RefBandwidthGBs: 400, BenchModelBytes: 1_824_825_759}, "test")
}

func TestBuildHasBothStagesComparisonAndLabels(t *testing.T) {
	r := sample()
	if len(r.Stages["baseline"].Metrics) != 2 || len(r.Stages["tuned"].Metrics) != 2 || len(r.Compare) != 2 {
		t.Fatalf("%+v", r)
	}
	for _, m := range r.Stages["baseline"].Metrics {
		if m.Label == "" || m.Label == m.Key() {
			t.Errorf("metric %s has no human label", m.Key())
		}
	}
	if r.Versions["mlx"] != "0.32.2" {
		t.Fatalf("versions: %v", r.Versions)
	}
	if h := HeadlineOf(r.Stages["tuned"].Metrics); h.MLXGen != 131 || h.GPUBW != 356 {
		t.Fatalf("%+v", h)
	}
}

func TestBuildWithOnlyBaselineHasNoComparison(t *testing.T) {
	r := Build(store.Run{}, nil, rs("baseline", map[string]float64{"llm/mlx/generation_tps": 130}), nil, nil, Inputs{}, "t")
	if len(r.Compare) != 0 || len(r.Stages) != 1 || r.Changes == nil {
		t.Fatalf("%+v", r)
	}
}

func TestMarkdownIsReadableAndEscapesTables(t *testing.T) {
	md := Markdown(sample())
	for _, want := range []string{"# aituner benchmark report", "Mac Studio (Mac13,1)", "Apple M1 Max", "## Baseline results", "## Tuned results", "| MLX text generation, 512-token context | 130 tok/s", "## Before and after", "ollama.env", "- mlx: 0.32.2\n", "GPU memory bandwidth utilisation", "Warning: a | b warning"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q\n%s", want, md)
		}
	}
	if strings.Contains(md, `\n`) {
		t.Error("literal backslash-n leaked into the output")
	}
}

func TestCSVParsesBackWithAllTrials(t *testing.T) {
	out, err := CSV(sample())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil || len(rows) != 5 { // header + 2 metrics x 2 stages
		t.Fatalf("%v rows=%d\n%s", err, len(rows), out)
	}
	if rows[0][0] != "stage" || len(strings.Split(rows[1][8], ";")) != 3 {
		t.Fatalf("%v", rows[1])
	}
}
