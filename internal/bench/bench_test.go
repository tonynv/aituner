package bench

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tonynv/aituner/internal/platform"
)

func TestMedianAndSpread(t *testing.T) {
	if Median([]float64{3, 1, 2}) != 2 || Median([]float64{4, 1, 2, 3}) != 2.5 || Median(nil) != 0 {
		t.Fatal("median wrong")
	}
	if got := SpreadPct([]float64{95, 100, 105}); math.Abs(got-5) > 1e-9 {
		t.Fatalf("spread %v", got)
	}
	if SpreadPct([]float64{7}) != 0 {
		t.Fatal("single trial must have zero spread")
	}
}

func mk(name string, trials ...float64) Metric {
	m := Metric{Suite: "llm", Engine: "mlx", Name: name, Unit: "tok/s", Trials: trials}
	m.Finalize()
	return m
}

func TestCompareVerdicts(t *testing.T) {
	before := []Metric{mk("generation_tps", 100, 101, 102), mk("prompt_tps", 1000, 1000, 1000), mk("peak_memory", 2.4, 2.5, 2.5), mk("gone", 5)}
	after := []Metric{mk("generation_tps", 110, 111, 112), mk("prompt_tps", 1010, 1005, 1015), mk("peak_memory", 2.5, 2.5, 2.5)}
	rows := map[string]Row{}
	for _, r := range Compare(before, after) {
		rows[r.Metric] = r
	}
	if rows["generation_tps"].Verdict != "faster" {
		t.Fatalf("expected faster: %+v", rows["generation_tps"])
	}
	if rows["prompt_tps"].Verdict != "within_noise" { // +1% is under the 2% floor
		t.Fatalf("expected within_noise: %+v", rows["prompt_tps"])
	}
	if rows["peak_memory"].Verdict != "info" {
		t.Fatalf("expected info: %+v", rows["peak_memory"])
	}
	if _, ok := rows["gone"]; ok {
		t.Fatal("metric missing after must not be compared")
	}
	slower := Compare([]Metric{mk("generation_tps", 100, 100, 100)}, []Metric{mk("generation_tps", 80, 80, 80)})
	if slower[0].Verdict != "slower" {
		t.Fatalf("%+v", slower[0])
	}
}

func TestCompareUsesNoiseOfBothRuns(t *testing.T) {
	// 8% delta but trials span +-10%: not evidence
	r := Compare([]Metric{mk("generation_tps", 90, 100, 110)}, []Metric{mk("generation_tps", 98, 108, 118)})
	if r[0].Verdict != "within_noise" {
		t.Fatalf("%+v", r[0])
	}
}

// Real measurement on this machine: plausible bounds only (no fixed number is claimed).
func TestMemBandwidthLive(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("throughput is not measurable in -short or -race mode")
	}
	cp, rd, err := MemBandwidth(context.Background(), 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp) != 2 || len(rd) != 2 || Median(cp) < 5 || Median(rd) < 5 || Median(cp) > 2000 {
		t.Fatalf("implausible: copy=%v read=%v", cp, rd)
	}
	t.Logf("copy=%v read=%v GB/s", cp, rd)
}

func TestBenchPromptDefeatsCache(t *testing.T) {
	// the marker is at the very start, so no shared prefix can be served from Ollama's prompt cache
	if benchPrompt(1)[:10] == benchPrompt(2)[:10] {
		t.Fatal("prompts must differ at the start")
	}
}

// Full orchestrated benchmark on the real machine. Opt-in (downloads models): AITUNER_LIVE=1 go test -run Live -v
func TestRunLiveFull(t *testing.T) {
	if os.Getenv("AITUNER_LIVE") != "1" {
		t.Skip("set AITUNER_LIVE=1 to run the full live benchmark")
	}
	dir, err := platform.Current().DataDir()
	if err != nil {
		t.Fatal(err)
	}
	o := Options{
		MLX:         MLX{Python: filepath.Join(dir, "venv", "bin", "python"), Dir: filepath.Join(dir, "runtime")},
		Ollama:      NewOllama(),
		Threads:     8,
		Trials:      3,
		FetchModels: true,
	}
	res, err := Run(context.Background(), o, func(e Event) { t.Logf("[%s] %s", e.Level, e.Message) })
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Metrics {
		t.Logf("%-8s %-7s %-16s %10.2f %-6s trials=%v", m.Suite, m.Engine, m.Name, m.Value, m.Unit, m.Trials)
	}
	t.Logf("skipped=%v probe=%+v", res.Skipped, res.Probe)
	if len(res.Metrics) < 10 {
		t.Fatalf("too few metrics: %d", len(res.Metrics))
	}
}
