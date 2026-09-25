package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/platform"
	"github.com/tonynv/aituner/internal/reco"
	"github.com/tonynv/aituner/internal/report"
	"github.com/tonynv/aituner/internal/serve"
	"github.com/tonynv/aituner/internal/store"
	"github.com/tonynv/aituner/internal/tune"
)

type Download struct {
	What string `json:"what"`
	Size string `json:"size"`
}

type BenchPlan struct {
	Downloads []Download `json:"downloads"`
	Stage     string     `json:"stage"` // baseline | tuned | ""
}

type benchMeta struct {
	Skipped  []string    `json:"skipped"`
	Warnings []string    `json:"warnings"`
	Probe    bench.Probe `json:"probe"`
}

type StateResp struct {
	Supported bool                       `json:"supported"`
	Platform  string                     `json:"platform"`
	Message   string                     `json:"message,omitempty"`
	Phase     string                     `json:"phase"`
	Run       *store.Run                 `json:"run,omitempty"`
	Hardware  *platform.Hardware         `json:"hardware,omitempty"`
	Job       *JobInfo                   `json:"job"`
	Baseline  []bench.Metric             `json:"baseline"`
	Tuned     []bench.Metric             `json:"tuned"`
	Compare   []bench.Row                `json:"compare"`
	Skipped   map[string][]string        `json:"skipped"`
	Warnings  map[string][]string        `json:"warnings"`
	Changes   []store.TuneChange         `json:"tune_changes"`
	BenchPlan BenchPlan                  `json:"bench_plan"`
	Headline  map[string]report.Headline `json:"headline"`
	// HeadlineFrom is set when this run has no measurements yet and the pinned numbers come from an earlier run
	// (its creation time, ms). A fresh launch starts a new run, so without this the pinned stats would be empty.
	HeadlineFrom int64         `json:"headline_from,omitempty"`
	BudgetGB     float64       `json:"budget_gb"`
	Serving      *servingBrief `json:"serving,omitempty"`
}

func toMetrics(rs []store.Result, stage string) []bench.Metric { return report.MetricsFrom(rs, stage) }

func (s *Server) runOr501(w http.ResponseWriter, r *http.Request) (store.Run, bool) {
	if s.hardware() == nil {
		writeErr(w, http.StatusNotImplemented, "platform_unsupported", s.unsupportedMsg())
		return store.Run{}, false
	}
	run, err := s.tn.LatestRun(r.Context())
	if err != nil {
		writeErr(w, 500, "no_run", err.Error())
		return store.Run{}, false
	}
	return run, true
}

func (s *Server) unsupportedMsg() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unsupported != "" {
		return s.unsupported
	}
	return "hardware could not be detected"
}

func hfCacheHas(repo string) bool {
	home := os.Getenv("HF_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		home = filepath.Join(h, ".cache", "huggingface")
	}
	_, err := os.Stat(filepath.Join(home, "hub", "models--"+strings.ReplaceAll(repo, "/", "--")))
	return err == nil
}

// benchPlan lists exactly what running the benchmark will install or download, shown before the user confirms.
func (s *Server) benchPlan(hw *platform.Hardware, phase string) BenchPlan {
	bp := BenchPlan{Downloads: []Download{}}
	switch phase {
	case store.PhaseDetected:
		bp.Stage = "baseline"
	case store.PhaseTuneReviewed:
		bp.Stage = "tuned"
	}
	if !hw.Software.MLX.Ready {
		bp.Downloads = append(bp.Downloads, Download{"Python environment with mlx and mlx-lm (from PyPI)", "about 200 MB"})
	}
	if !hfCacheHas(bench.BenchModelMLX) {
		bp.Downloads = append(bp.Downloads, Download{"MLX benchmark model " + bench.BenchModelMLX + " (Hugging Face)", "about 1.8 GB"})
	}
	if hw.Software.Ollama.Running {
		have := false
		for _, m := range hw.Software.Ollama.Models {
			if m.Name == bench.BenchModelOllama || strings.TrimSuffix(m.Name, ":latest") == bench.BenchModelOllama {
				have = true
			}
		}
		if !have {
			bp.Downloads = append(bp.Downloads, Download{"Ollama benchmark model " + bench.BenchModelOllama, "about 2.0 GB"})
		}
	}
	return bp
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	resp := StateResp{Platform: s.cfg.Platform.Name(), Job: s.jobs.info(),
		Baseline: []bench.Metric{}, Tuned: []bench.Metric{}, Compare: []bench.Row{}, Skipped: map[string][]string{}, Warnings: map[string][]string{}, Changes: []store.TuneChange{}}
	hw := s.hardware()
	if hw == nil {
		resp.Message = s.unsupportedMsg()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	run, err := s.tn.LatestRun(r.Context())
	if err != nil {
		writeErr(w, 500, "no_run", err.Error())
		return
	}
	rs, err := s.tn.Results(r.Context(), run.ID)
	if err != nil {
		writeErr(w, 500, "db", err.Error())
		return
	}
	resp.Supported, resp.Hardware, resp.Run, resp.Phase = true, hw, &run, run.Phase
	resp.Baseline, resp.Tuned = toMetrics(rs, "baseline"), toMetrics(rs, "tuned")
	if len(resp.Tuned) > 0 {
		resp.Compare = bench.Compare(resp.Baseline, resp.Tuned)
	}
	for _, stage := range []string{"baseline", "tuned"} {
		if e, err := s.tn.CacheGet(r.Context(), "bench_meta", run.ID+"|"+stage); err == nil {
			var m benchMeta
			if json.Unmarshal(e.Body, &m) == nil {
				resp.Skipped[stage] = m.Skipped
				resp.Warnings[stage] = m.Warnings
			}
		}
	}
	if cs, err := s.tn.TuneChanges(r.Context(), run.ID); err == nil {
		resp.Changes = cs
	}
	resp.BenchPlan = s.benchPlan(hw, run.Phase)
	resp.Headline = map[string]report.Headline{}
	if len(resp.Baseline) > 0 {
		resp.Headline["baseline"] = report.HeadlineOf(resp.Baseline)
	}
	if len(resp.Tuned) > 0 {
		resp.Headline["tuned"] = report.HeadlineOf(resp.Tuned)
	}
	if len(resp.Headline) == 0 {
		if prev, at := s.previousMeasured(r.Context(), run.ID); prev != nil {
			resp.Headline, resp.HeadlineFrom = prev, at
		}
	}
	resp.BudgetGB = s.budgetGB(r.Context(), run.ID, hw)
	if ss := s.serve.Status(); ss.State != serve.StateStopped {
		resp.Serving = &servingBrief{State: ss.State, Repo: ss.Repo}
	}
	writeJSON(w, http.StatusOK, resp)
}

// healthTTL is how long one live sample is reused: pollers see fresh numbers without each request spawning samplers.
const healthTTL = 2 * time.Second

// handleHealth reports the machine's live state (load, memory, GPU utilisation, thermal, power) for the menu bar view.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if time.Since(s.healthAt) > healthTTL {
		s.health, s.healthAt = platform.CheckHealth(r.Context()), time.Now()
	}
	writeJSON(w, http.StatusOK, s.health)
}

func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	if j := s.jobs.info(); j != nil && j.Running {
		writeErr(w, http.StatusConflict, "busy", ErrBusy.Error())
		return
	}
	if err := s.refreshHW(r.Context()); err != nil {
		writeErr(w, http.StatusNotImplemented, "platform_unsupported", s.unsupportedMsg())
		return
	}
	if err := s.ensureRun(r.Context(), false); err != nil {
		writeErr(w, 500, "db", err.Error())
		return
	}
	s.handleState(w, r)
}

func (s *Server) handleNewRun(w http.ResponseWriter, r *http.Request) {
	if j := s.jobs.info(); j != nil && j.Running {
		writeErr(w, http.StatusConflict, "busy", ErrBusy.Error())
		return
	}
	if err := s.refreshHW(r.Context()); err != nil {
		writeErr(w, http.StatusNotImplemented, "platform_unsupported", s.unsupportedMsg())
		return
	}
	if err := s.ensureRun(r.Context(), true); err != nil {
		writeErr(w, 500, "db", err.Error())
		return
	}
	s.handleState(w, r)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": s.jobs.stop()})
}

type benchReq struct {
	ConfirmDownloads bool `json:"confirm_downloads"`
}

func (s *Server) handleBenchmark(w http.ResponseWriter, r *http.Request) {
	var req benchReq
	if !decode(w, r, &req) {
		return
	}
	run, ok := s.runOr501(w, r)
	if !ok {
		return
	}
	var running, okPhase, failPhase, stage string
	switch run.Phase {
	case store.PhaseDetected:
		running, okPhase, failPhase, stage = store.PhaseBaselineRunning, store.PhaseBaselineDone, store.PhaseDetected, "baseline"
	case store.PhaseTuneReviewed:
		running, okPhase, failPhase, stage = store.PhaseTunedRunning, store.PhaseTunedDone, store.PhaseTuneReviewed, "tuned"
	default:
		writeErr(w, http.StatusConflict, "wrong_phase", fmt.Sprintf("cannot start a benchmark while the run is %q", run.Phase))
		return
	}
	if st := s.serve.Status().State; st == serve.StateStarting || st == serve.StateRunning || st == serve.StateStopping {
		writeErr(w, http.StatusConflict, "model_running", "a model is loaded: it would distort the benchmark and compete for GPU memory. Stop it first")
		return
	}
	if s.dl.Active() {
		writeErr(w, http.StatusConflict, "download_running", "a model download is running and would distort the benchmark; wait for it or cancel it")
		return
	}
	hw := s.hardware()
	if bp := s.benchPlan(hw, run.Phase); len(bp.Downloads) > 0 && !req.ConfirmDownloads {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "needs_confirmation", "message": "this run installs or downloads software; confirm to proceed", "downloads": bp.Downloads})
		return
	}
	from := run.Phase
	if err := s.tn.SetPhase(r.Context(), run.ID, from, running, ""); err != nil {
		writeErr(w, http.StatusConflict, "wrong_phase", "run changed state; refresh")
		return
	}
	runID := run.ID
	err := s.jobs.start(s.ctx, "benchmark", func(ctx context.Context, emit bench.Emit) error {
		return s.runBenchmark(ctx, emit, runID, stage)
	}, func(err error) {
		to, note := okPhase, ""
		if err != nil {
			to, note = failPhase, err.Error()
			if errors.Is(err, context.Canceled) {
				note = "Cancelled."
			}
		}
		if e := s.tn.SetPhase(context.Background(), runID, running, to, note); e != nil {
			s.cfg.Log("phase update failed: " + e.Error())
		}
		_ = s.refreshHW(context.Background())
	})
	if err != nil {
		_ = s.tn.SetPhase(r.Context(), runID, running, from, "")
		writeErr(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started", "stage": stage})
}

func (s *Server) runBenchmark(ctx context.Context, emit bench.Emit, runID, stage string) error {
	hw := s.hardware()
	mlx, err := bench.EnsureRuntime(ctx, emit, s.cfg.DataDir, hw.Software.Python.Path, hw.Software.Python.Version)
	if err != nil {
		return err
	}
	threads := hw.CPU.PerformanceCores
	if threads == 0 {
		threads = hw.CPU.Cores
	}
	res, err := bench.Run(ctx, bench.Options{MLX: mlx, Ollama: s.cfg.Ollama, Threads: threads, Trials: 5, FetchModels: true}, emit)
	if err != nil {
		return err
	}
	if err := s.tn.DeleteStageResults(ctx, runID, stage); err != nil {
		return err
	}
	for _, m := range res.Metrics {
		tr, _ := json.Marshal(m.Trials)
		ve, _ := json.Marshal(m.Versions)
		if err := s.tn.AddResult(ctx, runID, store.Result{Stage: stage, Suite: m.Suite, Engine: m.Engine, Metric: m.Name, Value: m.Value, Unit: m.Unit,
			Trials: tr, Versions: ve, Thermal: hw.Power.ThermalNote}); err != nil {
			return err
		}
	}
	meta, _ := json.Marshal(benchMeta{Skipped: res.Skipped, Warnings: res.Warnings, Probe: res.Probe})
	return s.tn.CachePut(ctx, "bench_meta", runID+"|"+stage, meta)
}

// mlxRuntime returns the venv runner if the runtime is installed.
func (s *Server) mlxRuntime() (bench.MLX, bool) {
	hw := s.hardware()
	if hw == nil || !hw.Software.MLX.Ready {
		return bench.MLX{}, false
	}
	return bench.MLX{Python: filepath.Join(hw.Software.MLX.VenvPath, "bin", "python"), Dir: filepath.Join(s.cfg.DataDir, "runtime")}, true
}

func (s *Server) tunePlan(ctx context.Context) (tune.Plan, error) {
	if err := s.refreshHW(ctx); err != nil {
		return tune.Plan{}, err
	}
	hw := s.hardware()
	var metal int64
	if m, ok := s.mlxRuntime(); ok {
		if p, err := m.Probe(ctx); err == nil {
			metal = p.MaxRecommendedWorkingSetB
		}
	}
	return tune.BuildPlan(tune.ReadEnv(ctx, hw.Memory.TotalBytes, metal, hw.Software.Ollama.Running)), nil
}

func (s *Server) handleTunePlan(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runOr501(w, r)
	if !ok {
		return
	}
	if run.Phase != store.PhaseBaselineDone {
		writeErr(w, http.StatusConflict, "wrong_phase", "tuning is available after the baseline benchmark, before it is reviewed")
		return
	}
	plan, err := s.tunePlan(r.Context())
	if err != nil {
		writeErr(w, 500, "plan", err.Error())
		return
	}
	if plan.Changes == nil {
		plan.Changes = []tune.Change{}
	}
	if plan.NotOffered == nil {
		plan.NotOffered = []tune.NotOffered{}
	}
	writeJSON(w, http.StatusOK, plan)
}

type applyReq struct {
	Keys []string `json:"keys"`
}

// handleTuneApply accepts only change KEYS. Values are recomputed server-side from the measured plan.
func (s *Server) handleTuneApply(w http.ResponseWriter, r *http.Request) {
	var req applyReq
	if !decode(w, r, &req) {
		return
	}
	run, ok := s.runOr501(w, r)
	if !ok {
		return
	}
	if run.Phase != store.PhaseBaselineDone {
		writeErr(w, http.StatusConflict, "wrong_phase", "tuning can only be applied once, after the baseline benchmark")
		return
	}
	plan, err := s.tunePlan(r.Context())
	if err != nil {
		writeErr(w, 500, "plan", err.Error())
		return
	}
	if err := plan.ValidateSelection(req.Keys); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_selection", err.Error())
		return
	}
	if len(req.Keys) == 0 { // declining every change is a valid outcome
		if err := s.tn.SetPhase(r.Context(), run.ID, store.PhaseBaselineDone, store.PhaseTuneReviewed, "no changes applied"); err != nil {
			writeErr(w, http.StatusConflict, "wrong_phase", "run changed state; refresh")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "no_changes"})
		return
	}
	selected := map[string]bool{}
	for _, k := range req.Keys {
		selected[k] = true
	}
	runID := run.ID
	err = s.jobs.start(s.ctx, "tune", func(ctx context.Context, emit bench.Emit) error {
		n := 0
		for _, c := range plan.Changes { // plan order respects dependencies
			if !selected[c.Key] {
				continue
			}
			emit(bench.Event{Level: "info", Message: "Applying: " + c.Title, Progress: float64(n) / float64(len(req.Keys))})
			if c.NeedsAdmin {
				emit(bench.Event{Level: "info", Message: "Waiting for macOS administrator approval (a system dialog is open)"})
			}
			if err := s.cfg.Runner.Apply(ctx, c); err != nil {
				// Apply may have changed the system before failing (e.g. env set, restart failed).
				// Undo it so nothing is left half-applied and unrecorded.
				emit(bench.Event{Level: "warn", Message: "Apply failed; rolling back " + c.Title})
				if rerr := s.cfg.Runner.Revert(context.WithoutCancel(ctx), c); rerr != nil {
					emit(bench.Event{Level: "error", Message: "Rollback also failed: " + rerr.Error()})
					return fmt.Errorf("%s: %w (rollback failed: %v)", c.Title, err, rerr)
				}
				return fmt.Errorf("%s: %w (rolled back)", c.Title, err)
			}
			if _, err := s.tn.AddTuneChange(ctx, runID, c.Key, c.Before, c.After); err != nil {
				return err
			}
			emit(bench.Event{Level: "info", Message: "Applied and verified: " + c.Title})
			n++
		}
		return nil
	}, func(err error) {
		if err == nil {
			if e := s.tn.SetPhase(context.Background(), runID, store.PhaseBaselineDone, store.PhaseTuneReviewed, ""); e != nil {
				s.cfg.Log("phase update failed: " + e.Error())
			}
		}
		_ = s.refreshHW(context.Background())
	})
	if err != nil {
		writeErr(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleTuneRevert(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runOr501(w, r)
	if !ok {
		return
	}
	changes, err := s.tn.TuneChanges(r.Context(), run.ID)
	if err != nil {
		writeErr(w, 500, "db", err.Error())
		return
	}
	var todo []store.TuneChange
	for i := len(changes) - 1; i >= 0; i-- { // reverse order
		if changes[i].RevertedAt == nil {
			todo = append(todo, changes[i])
		}
	}
	if len(todo) == 0 {
		writeErr(w, http.StatusConflict, "nothing_to_revert", "no applied changes to revert")
		return
	}
	err = s.jobs.start(s.ctx, "revert", func(ctx context.Context, emit bench.Emit) error {
		for _, c := range todo {
			emit(bench.Event{Level: "info", Message: "Reverting " + c.Key})
			if err := s.cfg.Runner.Revert(ctx, tune.Change{Key: c.Key, Before: c.Before, After: c.After}); err != nil {
				return fmt.Errorf("%s: %w", c.Key, err)
			}
			if err := s.tn.MarkReverted(ctx, c.ID); err != nil {
				return err
			}
			emit(bench.Event{Level: "info", Message: "Reverted " + c.Key})
		}
		return nil
	}, func(error) { _ = s.refreshHW(context.Background()) })
	if err != nil {
		writeErr(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func median(m []bench.Metric, suite, engine, name string) float64 {
	for _, x := range m {
		if x.Suite == suite && x.Engine == engine && x.Name == name {
			return x.Value
		}
	}
	return 0
}

// handleRecommendations produces model suggestions once hardware is detected. Speed estimates are added only
// when this run has measurements (tuned, else baseline); otherwise models are ranked on fit alone.
func (s *Server) handleRecommendations(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runOr501(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rs, err := s.tn.Results(ctx, run.ID)
	if err != nil {
		writeErr(w, 500, "db", err.Error())
		return
	}
	var bw, tps float64
	for _, stage := range []string{"tuned", "baseline"} {
		ms := toMetrics(rs, stage)
		if bw, tps = median(ms, "gpu", "mlx", "mem_bandwidth"), median(ms, "llm", "mlx", "generation_tps"); bw > 0 && tps > 0 {
			break
		}
		bw, tps = 0, 0
	}
	if err := s.refreshHW(ctx); err != nil {
		writeErr(w, 500, "detect", err.Error())
		return
	}
	hw := s.hardware()
	in := reco.Input{
		Hardware: canirun.Hardware{CPU: &canirun.CPU{Name: hw.CPU.Chip, Cores: hw.CPU.Cores, Threads: hw.CPU.Cores},
			RAMGb: int(hw.Memory.TotalBytes >> 30), GPU: &canirun.GPU{Name: hw.GPU.Name}},
		GPUBandwidthGBs: bw, MLXGenTPS: tps,
		Unrestricted: r.URL.Query().Get("unrestricted") != "0",
	}
	for _, m := range hw.Software.Ollama.Models {
		in.Installed = append(in.Installed, m.Name)
	}
	// budget reflects the CURRENT system (so a reverted tune is not credited): the larger of Metal's
	// recommended working set and an explicit iogpu.wired_limit_mb
	m, ready := s.mlxRuntime()
	if !ready {
		writeErr(w, http.StatusConflict, "no_runtime", "MLX runtime missing")
		return
	}
	probe, err := m.Probe(ctx)
	if err != nil {
		writeErr(w, 500, "probe", err.Error())
		return
	}
	in.BudgetBytes = probe.MaxRecommendedWorkingSetB
	if wired := hw.Memory.WiredLimitMB << 20; wired > in.BudgetBytes {
		in.BudgetBytes = wired
	}
	if in.BudgetBytes > hw.Memory.TotalBytes {
		in.BudgetBytes = hw.Memory.TotalBytes
	}
	in.MLXBinDir = filepath.Join(hw.Software.MLX.VenvPath, "bin")
	in.SupportedModelTypes = map[string]bool{}
	for _, t := range probe.SupportedModelTypes {
		in.SupportedModelTypes[t] = true
	}
	if bw > 0 {
		if in.BenchModelBytes, err = s.engine.RepoBytes(ctx, bench.BenchModelMLX); err != nil {
			writeErr(w, http.StatusBadGateway, "huggingface", "cannot read benchmark model size: "+err.Error())
			return
		}
	}
	out, err := s.engine.Recommend(ctx, in)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "upstream", err.Error())
		return
	}
	s.rememberOffered(out)
	writeJSON(w, http.StatusOK, out)
}

// servingBrief is the small piece of model-server state the pinned bar shows on every tab.
type servingBrief struct {
	State string `json:"state"`
	Repo  string `json:"repo"`
}

// previousMeasured returns the pinned headline of the newest earlier run that has measurements.
func (s *Server) previousMeasured(ctx context.Context, current string) (map[string]report.Headline, int64) {
	runs, err := s.tn.ListRuns(ctx, 500)
	if err != nil {
		return nil, 0
	}
	for _, r := range runs {
		if r.ID == current {
			continue
		}
		rs, err := s.tn.Results(ctx, r.ID)
		if err != nil {
			continue
		}
		h := map[string]report.Headline{}
		if m := toMetrics(rs, "baseline"); len(m) > 0 {
			h["baseline"] = report.HeadlineOf(m)
		}
		if m := toMetrics(rs, "tuned"); len(m) > 0 {
			h["tuned"] = report.HeadlineOf(m)
		}
		if len(h) > 0 {
			return h, r.CreatedAt
		}
	}
	return nil, 0
}
