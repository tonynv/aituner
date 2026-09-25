package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/connect"
	"github.com/tonynv/aituner/internal/modeldir"
	"github.com/tonynv/aituner/internal/monitor"
	"github.com/tonynv/aituner/internal/serve"
)

// service is one tool or service aituner uses, for the sidebar: installed, and active (doing work now).
type service struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Detail    string `json:"detail"`
}

func (s *Server) services() []service {
	hw := s.hardware()
	var out []service
	st := s.serve.Status()
	serving := st.State == serve.StateRunning || st.State == serve.StateStarting
	mlx := service{ID: "mlx", Name: "MLX", Detail: "not installed"}
	if hw != nil && hw.Software.MLX.Ready {
		mlx.Installed, mlx.Detail = true, "mlx "+hw.Software.MLX.MLX
	}
	mlx.Active = serving
	out = append(out, mlx)

	model := service{ID: "model", Name: "Model server", Installed: mlx.Installed, Active: st.State == serve.StateRunning, Detail: "stopped"}
	if serving {
		model.Detail = st.State + " · " + modelBase(st.Repo)
	}
	out = append(out, model)

	gw := service{ID: "gateway", Name: "Gateway", Installed: true, Detail: "off"}
	if gi, ok := s.gatewayInfo(); ok {
		gw.Active, gw.Detail = true, fmt.Sprintf("127.0.0.1:%d", gi.Port)
	}
	out = append(out, gw)

	mm := service{ID: "macmon", Name: "macmon", Detail: "not installed"}
	if _, ok := monitor.FindMacmon(); ok {
		mm.Installed, mm.Detail = true, "idle"
		if running, src := s.mon.Active(); running && src == monitor.SourceMacmon {
			mm.Active, mm.Detail = true, "sampling"
		}
	}
	out = append(out, mm)

	ol := service{ID: "ollama", Name: "Ollama", Detail: "not installed (optional)"}
	if hw != nil && hw.Software.Ollama.Installed {
		ol.Installed, ol.Active, ol.Detail = true, hw.Software.Ollama.Running, "installed"
		if ol.Active {
			ol.Detail = "running"
		}
	}
	return append(out, ol)
}

func modelBase(repo string) string {
	for i := len(repo) - 1; i >= 0; i-- {
		if repo[i] == '/' {
			return repo[i+1:]
		}
	}
	return repo
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"services": s.services()})
}

// ---- bootstrap: install or update everything aituner needs, in one confirmed job -------------------------------------

type bootstrapStep struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Action string   `json:"action"` // create | install | update | none | unavailable
	Detail string   `json:"detail"`
	Items  []string `json:"items,omitempty"` // e.g. the folders to create, one per entry
}

var stepVerb = map[string]string{"create": "creating", "install": "installing", "update": "updating", "none": "already installed", "unavailable": "not available"}

func (s *Server) bootstrapPlan(ctx context.Context) ([]bootstrapStep, string) {
	hw := s.hardware()
	if hw == nil {
		return nil, s.unsupportedMsg()
	}
	steps := []bootstrapStep{}
	if missing := s.missingFolders(ctx); len(missing) > 0 {
		steps = append(steps, bootstrapStep{ID: "folders", Name: "Folders", Action: "create", Detail: fmt.Sprintf("%d folder(s) that do not exist yet", len(missing)), Items: missing})
	}
	mlx := bootstrapStep{ID: "mlx", Name: "MLX and mlx-lm", Action: "install",
		Detail: "a private Python environment in aituner's app data, then pip install -U mlx mlx-lm"}
	if hw.Software.MLX.Ready {
		mlx.Action, mlx.Detail = "update", "mlx "+hw.Software.MLX.MLX+", mlx-lm "+hw.Software.MLX.MLXLM+": pip install -U to the latest"
	}
	if hw.Software.Python.Path == "" {
		mlx.Action, mlx.Detail = "unavailable", "Python 3 was not found (run_aituner.sh installs python@3.14 with Homebrew)"
	}
	steps = append(steps, mlx)
	env, _, err := s.connectEnv(ctx)
	if err != nil {
		return steps, err.Error()
	}
	for _, m := range connect.TerminalMonitors() {
		if m.ID != "macmon" { // macmon feeds the live stats; the other monitors stay optional (Monitor tab)
			continue
		}
		st := bootstrapStep{ID: m.ID, Name: m.Name, Action: "install", Detail: "brew install " + m.Formula + " (live power, temperature and GPU stats, no admin)"}
		if m.Installed(env) {
			st.Action, st.Detail = "none", "installed"
		} else if _, ok := env.Run.Look("brew"); !ok {
			st.Action, st.Detail = "unavailable", "Homebrew is not installed (https://brew.sh)"
		}
		steps = append(steps, st)
	}
	return steps, ""
}

// missingFolders lists the configured models, reports and knowledge base folders that do not exist yet.
func (s *Server) missingFolders(ctx context.Context) []string {
	st := s.storage(ctx)
	var out []string
	for _, f := range []folderInfo{st.Models, st.Reports, st.Knowledge} {
		if f.Dir != "" && f.Problem == "" && !f.Info.Exists {
			out = append(out, f.Dir)
		}
	}
	return out
}

func (s *Server) handleBootstrapPlan(w http.ResponseWriter, r *http.Request) {
	steps, problem := s.bootstrapPlan(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"steps": steps, "problem": problem})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusBadRequest, "needs_confirmation", "review what bootstrap installs and confirm")
		return
	}
	steps, problem := s.bootstrapPlan(r.Context())
	if problem != "" {
		writeErr(w, http.StatusConflict, "unavailable", problem)
		return
	}
	if st := s.serve.Status().State; st == serve.StateRunning || st == serve.StateStarting {
		writeErr(w, http.StatusConflict, "model_running", "stop the running model first: MLX cannot be updated while it is in use")
		return
	}
	env, _, err := s.connectEnv(r.Context())
	if err != nil {
		writeErr(w, 500, "connect", err.Error())
		return
	}
	hw := s.hardware()
	py, ver := hw.Software.Python.Path, hw.Software.Python.Version
	s.cfg.Log("audit: bootstrap")
	err = s.jobs.start(s.ctx, "bootstrap", func(ctx context.Context, emit bench.Emit) error {
		say := func(m string) { emit(bench.Event{Level: "info", Message: m}) }
		for i, st := range steps {
			say(fmt.Sprintf("[%d/%d] %s: %s", i+1, len(steps), st.Name, stepVerb[st.Action]))
			switch {
			case st.Action == "none":
			case st.Action == "unavailable":
				emit(bench.Event{Level: "warn", Message: "  skipped: " + st.Detail})
			case st.ID == "folders":
				for _, d := range s.missingFolders(ctx) {
					if err := modeldir.Ensure(d); err != nil {
						return fmt.Errorf("create %s: %w", d, err)
					}
					say("  created " + d)
				}
			case st.ID == "mlx":
				if _, err := bench.EnsureRuntime(ctx, emit, s.cfg.DataDir, py, ver); err != nil {
					return fmt.Errorf("MLX: %w", err)
				}
			default:
				if m := connect.TerminalMonitorByID(st.ID); m != nil {
					if err := m.Install(ctx, env, say); err != nil {
						return fmt.Errorf("%s: %w", st.Name, err)
					}
				}
			}
			emit(bench.Event{Level: "info", Message: "  done", Progress: float64(i+1) / float64(len(steps))})
		}
		say("Bootstrap complete")
		return nil
	}, func(error) { _ = s.refreshHW(context.Background()) })
	if err != nil {
		writeErr(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}
