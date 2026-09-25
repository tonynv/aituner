package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/platform"
	"github.com/tonynv/aituner/internal/report"
	"github.com/tonynv/aituner/internal/store"
)

var runIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// stageMetas reads the per-stage warnings and skips stored with a run's benchmark results.
func (s *Server) stageMetas(ctx context.Context, runID string) map[string]report.StageMeta {
	out := map[string]report.StageMeta{}
	for _, stage := range []string{"baseline", "tuned"} {
		if e, err := s.tn.CacheGet(ctx, "bench_meta", runID+"|"+stage); err == nil {
			var m benchMeta
			if json.Unmarshal(e.Body, &m) == nil {
				out[stage] = report.StageMeta{Warnings: m.Warnings, Skipped: m.Skipped}
			}
		}
	}
	return out
}

// budgetGB is the GPU-usable memory as of the last benchmark of this run (Metal's recommended working set,
// or an explicit wired limit if larger). 0 when no benchmark has run yet.
func (s *Server) budgetGB(ctx context.Context, runID string, hw *platform.Hardware) float64 {
	var best int64
	for _, stage := range []string{"tuned", "baseline"} {
		if e, err := s.tn.CacheGet(ctx, "bench_meta", runID+"|"+stage); err == nil {
			var m benchMeta
			if json.Unmarshal(e.Body, &m) == nil && m.Probe.MaxRecommendedWorkingSetB > 0 {
				best = m.Probe.MaxRecommendedWorkingSetB
				break
			}
		}
	}
	if hw != nil {
		if w := hw.Memory.WiredLimitMB << 20; w > best {
			best = w
		}
	}
	return float64(best) / (1 << 30)
}

// reportInputs gathers the reference figures for derived insight. Both are best-effort and cached: offline, the
// figures that need them are simply omitted from the report.
func (s *Server) reportInputs(ctx context.Context, hw *platform.Hardware) report.Inputs {
	var in report.Inputs
	if hw == nil {
		return in
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	key := fmt.Sprintf("canirun.ref|%d|%s", hw.Memory.TotalBytes>>30, hw.GPU.Name)
	if e, err := s.tn.CacheGet(ctx, "reco", key); err == nil {
		_ = json.Unmarshal(e.Body, &in.RefBandwidthGBs)
	}
	if in.RefBandwidthGBs == 0 {
		r, err := s.cfg.CanIRun.Recommend(ctx, canirun.Hardware{RAMGb: int(hw.Memory.TotalBytes >> 30), GPU: &canirun.GPU{Name: hw.GPU.Name}}, "code", 1)
		if err == nil && r.Hardware.MemoryBandwidthGbps > 0 {
			in.RefBandwidthGBs = r.Hardware.MemoryBandwidthGbps
			b, _ := json.Marshal(in.RefBandwidthGBs)
			_ = s.tn.CachePut(ctx, "reco", key, b)
		}
	}
	if n, err := s.engine.RepoBytes(ctx, bench.BenchModelMLX); err == nil {
		in.BenchModelBytes = n
	}
	return in
}

// runOrLatest resolves the ?run= parameter (default: the latest run), validating its shape first.
func (s *Server) runOrLatest(w http.ResponseWriter, r *http.Request, param string) (store.Run, bool) {
	id := r.URL.Query().Get(param)
	if id == "" {
		run, err := s.tn.LatestRun(r.Context())
		if err != nil {
			writeErr(w, http.StatusNotFound, "no_run", "there is no run yet")
			return store.Run{}, false
		}
		return run, true
	}
	if !runIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad_run", "invalid run id")
		return store.Run{}, false
	}
	run, err := s.tn.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no_run", "run not found")
		return store.Run{}, false
	}
	return run, true
}

func (s *Server) buildReport(ctx context.Context, run store.Run) (report.Report, error) {
	m, err := s.tn.GetMachine(ctx, run.MachineID)
	if err != nil {
		return report.Report{}, err
	}
	rs, err := s.tn.Results(ctx, run.ID)
	if err != nil {
		return report.Report{}, err
	}
	changes, _ := s.tn.TuneChanges(ctx, run.ID)
	var hw *platform.Hardware
	_ = json.Unmarshal(m.Snapshot, &hw)
	return report.Build(run, m.Snapshot, rs, changes, s.stageMetas(ctx, run.ID), s.reportInputs(ctx, hw), s.cfg.Version), nil
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if s.hardware() == nil {
		writeErr(w, http.StatusNotImplemented, "platform_unsupported", s.unsupportedMsg())
		return
	}
	run, ok := s.runOrLatest(w, r, "run")
	if !ok {
		return
	}
	rep, err := s.buildReport(r.Context(), run)
	if err != nil {
		writeErr(w, 500, "report", err.Error())
		return
	}
	if len(rep.Stages) == 0 {
		writeErr(w, http.StatusConflict, "no_results", "this run has no benchmark results yet")
		return
	}
	name := "aituner-report-" + run.ID[:8]
	attach := func(ext string) {
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+"."+ext+`"`)
		}
	}
	switch f := r.URL.Query().Get("format"); f {
	case "", "json":
		attach("json")
		writeJSON(w, http.StatusOK, rep)
	case "md":
		attach("md")
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, report.Markdown(rep))
	case "csv":
		out, err := report.CSV(rep)
		if err != nil {
			writeErr(w, 500, "report", err.Error())
			return
		}
		attach("csv")
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, out)
	default:
		writeErr(w, http.StatusBadRequest, "bad_format", "format must be json, md or csv")
	}
}

type runSummary struct {
	ID        string          `json:"id"`
	Phase     string          `json:"phase"`
	CreatedAt int64           `json:"created_at"`
	Machine   string          `json:"machine"`
	Stage     string          `json:"stage"` // which stage the headline is from: tuned if present, else baseline
	Headline  report.Headline `json:"headline"`
	Changes   int             `json:"changes_applied"`
}

// finalMetrics is a run's most complete result set: tuned if it exists, else baseline.
func finalMetrics(rs []store.Result) ([]bench.Metric, string) {
	if t := report.MetricsFrom(rs, "tuned"); len(t) > 0 {
		return t, "tuned"
	}
	return report.MetricsFrom(rs, "baseline"), "baseline"
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.tn.ListRuns(r.Context(), 30)
	if err != nil {
		writeErr(w, 500, "db", err.Error())
		return
	}
	out := make([]runSummary, 0, len(runs))
	for _, run := range runs {
		sum := runSummary{ID: run.ID, Phase: run.Phase, CreatedAt: run.CreatedAt}
		if m, err := s.tn.GetMachine(r.Context(), run.MachineID); err == nil {
			var hw platform.Hardware
			if json.Unmarshal(m.Snapshot, &hw) == nil {
				sum.Machine = hw.Model.Name + ", " + hw.CPU.Chip
			}
		}
		if rs, err := s.tn.Results(r.Context(), run.ID); err == nil {
			ms, stage := finalMetrics(rs)
			if len(ms) > 0 {
				sum.Stage, sum.Headline = stage, report.HeadlineOf(ms)
			}
		}
		if cs, err := s.tn.TuneChanges(r.Context(), run.ID); err == nil {
			for _, c := range cs {
				if c.RevertedAt == nil {
					sum.Changes++
				}
			}
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

// handleCompare compares the final results of two runs (b relative to a).
func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	a, ok := s.runOrLatest(w, r, "a")
	if !ok {
		return
	}
	if r.URL.Query().Get("b") == "" {
		writeErr(w, http.StatusBadRequest, "bad_run", "b is required")
		return
	}
	b, ok := s.runOrLatest(w, r, "b")
	if !ok {
		return
	}
	ra, _ := s.tn.Results(r.Context(), a.ID)
	rb, _ := s.tn.Results(r.Context(), b.ID)
	ma, sa := finalMetrics(ra)
	mb, sb := finalMetrics(rb)
	if len(ma) == 0 || len(mb) == 0 {
		writeErr(w, http.StatusConflict, "no_results", "both runs need benchmark results")
		return
	}
	rows := bench.Compare(ma, mb)
	for i := range rows {
		rows[i].Label = bench.LabelFor(rows[i].Suite, rows[i].Engine, rows[i].Metric)
	}
	writeJSON(w, http.StatusOK, map[string]any{"a": a.ID, "b": b.ID, "a_stage": sa, "b_stage": sb, "rows": rows})
}
