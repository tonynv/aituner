package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/download"
	"github.com/tonynv/aituner/internal/gateway"
	"github.com/tonynv/aituner/internal/reco"
	"github.com/tonynv/aituner/internal/serve"
)

// DefaultGatewayPort is stable on purpose: editor configurations written by "Set up" embed it.
const DefaultGatewayPort = 8747

func gatewayPort() int {
	if v := os.Getenv("AITUNER_GATEWAY_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 1023 && p < 65536 {
			return p
		}
	}
	return DefaultGatewayPort
}

// Close stops everything aituner started: the gateway and the model server.
func (s *Server) Close() {
	s.stopGateway()
	s.serve.Stop()
}

func (s *Server) startGateway() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gw != nil {
		return nil
	}
	port := gatewayPort()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("the gateway port %d is in use by another program; free it or set AITUNER_GATEWAY_PORT", port)
	}
	g := gateway.New(s.gwKey, s.serve.Backend, s.cfg.Log)
	g.Info = func() (string, int) { // evaluated per request: the model starts after the gateway is created
		st := s.serve.Status()
		if c := s.contextFor(context.Background(), st.ModelDir, st.Spec.KVBits); c != nil && c.Known {
			return st.Repo, c.Tokens
		}
		return st.Repo, 0
	}
	srv := &http.Server{Handler: g.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 5 * time.Minute}
	s.gw, s.gwPort = srv, port
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.cfg.Log("gateway: " + err.Error())
		}
	}()
	return nil
}

func (s *Server) stopGateway() {
	s.mu.Lock()
	srv := s.gw
	s.gw, s.gwPort = nil, 0
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if srv.Shutdown(ctx) != nil {
			_ = srv.Close()
		}
	}
}

// ServeInfo is what editors need to connect.
type ServeInfo struct {
	Port    int    `json:"port"`
	BaseURL string `json:"base_url"` // OpenAI-compatible base (…/v1)
	Root    string `json:"root_url"` // Anthropic base (no /v1)
	Model   string `json:"model"`
}

func (s *Server) gatewayInfo() (ServeInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gw == nil {
		return ServeInfo{}, false
	}
	root := fmt.Sprintf("http://127.0.0.1:%d", s.gwPort)
	return ServeInfo{Port: s.gwPort, BaseURL: root + "/v1", Root: root, Model: s.serve.Status().Repo}, true
}

type serveResp struct {
	Runtime   runtimeInfo  `json:"runtime"`
	Server    serve.Status `json:"server"`
	Gateway   *ServeInfo   `json:"gateway"`
	KeyHint   string       `json:"key_hint"`
	Context   *ctxInfo     `json:"context,omitempty"`
	Models    []serveModel `json:"models"`
	ModelsDir string       `json:"models_dir"`
}

type runtimeInfo struct {
	Ready  bool   `json:"ready"`
	MLX    string `json:"mlx_version"`
	MLXLM  string `json:"mlx_lm_version"`
	Path   string `json:"venv_path"`
	Python string `json:"python_version"`
}

type serveModel struct {
	Repo    string  `json:"repo"`
	Dest    string  `json:"dest"`
	SizeGB  float64 `json:"size_gb"`
	Running bool    `json:"running"`
}

// ctxInfo is how much conversation the running model can hold: the KV cache room left in the memory budget, capped by
// the model's own limit. Editors that need to be told the window (Claude Code) are configured from this.
type ctxInfo struct {
	Tokens   int  `json:"tokens"`
	ModelMax int  `json:"model_max"`
	KVBits   int  `json:"kv_bits"`
	Known    bool `json:"known"`
}

func keyHint(k string) string {
	if len(k) < 8 {
		return ""
	}
	return "…" + k[len(k)-4:]
}

// downloadedModels lists completed downloads in the models folder.
func (s *Server) downloadedModels(ctx context.Context) ([]serveModel, string) {
	root, _, err := s.modelsDir(ctx)
	if err != nil {
		return []serveModel{}, ""
	}
	running := s.serve.Status()
	out := []serveModel{}
	for _, it := range s.dl.List(root) {
		if it.State != download.State_Done {
			continue
		}
		out = append(out, serveModel{Repo: it.Repo, Dest: it.Dest, SizeGB: float64(it.BytesTotal) / 1e9,
			Running: (running.State == serve.StateRunning || running.State == serve.StateStarting) && running.ModelDir == it.Dest})
	}
	return out, root
}

// contextFor computes the usable context for a model folder from its local config.json and the memory budget.
func (s *Server) contextFor(ctx context.Context, dir string, kvBits int) *ctxInfo {
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil
	}
	p, err := reco.KVFromConfig(raw)
	if err != nil {
		return nil
	}
	var weights int64
	if n, ok := download.Complete(dir, s.serve.Status().Repo); ok {
		weights = n
	} else if n, ok := download.Complete(dir, filepath.Base(filepath.Dir(dir))+"/"+filepath.Base(dir)); ok {
		weights = n
	}
	budget := int64(s.budgetGBNow(ctx) * (1 << 30))
	if weights <= 0 || budget <= 0 {
		return &ctxInfo{Known: false, KVBits: kvBits}
	}
	fit, err := p.Fit(weights, budget)
	if err != nil || !fit.Known {
		return &ctxInfo{Known: false, KVBits: kvBits, ModelMax: p.MaxContext}
	}
	t := fit.TokensF16
	if kvBits == 8 || kvBits == 4 {
		t = fit.Tokens8Bit
	}
	return &ctxInfo{Tokens: t, ModelMax: p.MaxContext, KVBits: kvBits, Known: true}
}

// budgetGBNow is the current GPU-usable memory (Metal's recommended working set, or a larger explicit wired limit).
func (s *Server) budgetGBNow(ctx context.Context) float64 {
	hw := s.hardware()
	if hw == nil {
		return 0
	}
	run, err := s.tn.LatestRun(ctx)
	if err != nil {
		return 0
	}
	return s.budgetGB(ctx, run.ID, hw)
}

func (s *Server) handleServeStatus(w http.ResponseWriter, r *http.Request) {
	hw := s.hardware()
	resp := serveResp{Server: s.serve.Status(), KeyHint: keyHint(s.gwKey), Models: []serveModel{}}
	if hw != nil {
		resp.Runtime = runtimeInfo{Ready: hw.Software.MLX.Ready, MLX: hw.Software.MLX.MLXVersion(), MLXLM: hw.Software.MLX.MLXLM, Path: hw.Software.MLX.VenvPath, Python: hw.Software.Python.Version}
	}
	if gi, ok := s.gatewayInfo(); ok {
		resp.Gateway = &gi
	}
	resp.Models, resp.ModelsDir = s.downloadedModels(r.Context())
	if st := resp.Server; st.State == serve.StateRunning || st.State == serve.StateStarting {
		resp.Context = s.contextFor(r.Context(), st.ModelDir, st.Spec.KVBits)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleServeKey reveals the gateway key. It is a separate, explicit request so the key never rides along in polled state.
func (s *Server) handleServeKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"key": s.gwKey})
}

func (s *Server) handleServeLogs(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	lines := s.serve.Logs(after)
	if lines == nil {
		lines = []serve.LogLine{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// handleServeRuntime installs or updates MLX for Mac (mlx, mlx-lm) into aituner's own environment.
func (s *Server) handleServeRuntime(w http.ResponseWriter, r *http.Request) {
	if !decode(w, r, &struct{}{}) { // no parameters: any field is an error
		return
	}
	hw := s.hardware()
	if hw == nil {
		writeErr(w, http.StatusNotImplemented, "platform_unsupported", s.unsupportedMsg())
		return
	}
	if st := s.serve.Status().State; st == serve.StateRunning || st == serve.StateStarting {
		writeErr(w, http.StatusConflict, "model_running", "stop the running model before updating MLX")
		return
	}
	py, ver := hw.Software.Python.Path, hw.Software.Python.Version
	err := s.jobs.start(s.ctx, "runtime", func(ctx context.Context, emit bench.Emit) error {
		_, err := bench.EnsureRuntime(ctx, emit, s.cfg.DataDir, py, ver)
		if err == nil {
			emit(bench.Event{Level: "info", Message: "MLX is installed and up to date", Progress: 1})
		}
		return err
	}, func(error) { _ = s.refreshHW(context.Background()) })
	if err != nil {
		writeErr(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

type serveStartReq struct {
	Repo      string `json:"repo"`
	MaxTokens int    `json:"max_tokens"`
	KVBits    int    `json:"kv_bits"`
}

func (s *Server) handleServeStart(w http.ResponseWriter, r *http.Request) {
	var req serveStartReq
	if !decode(w, r, &req) {
		return
	}
	if !reco.ValidRepo(req.Repo) {
		writeErr(w, http.StatusBadRequest, "bad_repo", "invalid repository id")
		return
	}
	if j := s.jobs.info(); j != nil && j.Running {
		writeErr(w, http.StatusConflict, "busy", "a benchmark, tuning or install job is running; wait for it to finish")
		return
	}
	if !s.hardwareReady(w) {
		return
	}
	hw := s.hardware()
	if !hw.Software.MLX.Ready {
		writeErr(w, http.StatusConflict, "no_runtime", "MLX is not installed yet: install it first")
		return
	}
	// only a completed download in the models folder can be served, never an arbitrary path
	models, _ := s.downloadedModels(r.Context())
	var dir string
	for _, m := range models {
		if m.Repo == req.Repo {
			dir = m.Dest
		}
	}
	if dir == "" {
		writeErr(w, http.StatusNotFound, "not_downloaded", "that model is not downloaded")
		return
	}
	spec := serve.Spec{Bin: filepath.Join(hw.Software.MLX.VenvPath, "bin", "mlx_lm.server"), ModelDir: dir, Repo: req.Repo, MaxTokens: req.MaxTokens, KVBits: req.KVBits}
	if err := s.startGateway(); err != nil {
		writeErr(w, http.StatusConflict, "gateway_port", err.Error())
		return
	}
	s.cfg.Log("audit: serve start repo=" + req.Repo)
	if err := s.serve.Start(s.ctx, spec); err != nil {
		s.stopGateway()
		code := http.StatusBadRequest
		if errors.Is(err, serve.ErrRunning) {
			code = http.StatusConflict
		}
		writeErr(w, code, "cannot_serve", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, s.serve.Status())
}

func (s *Server) hardwareReady(w http.ResponseWriter) bool {
	if s.hardware() == nil {
		writeErr(w, http.StatusNotImplemented, "platform_unsupported", s.unsupportedMsg())
		return false
	}
	return true
}

func (s *Server) handleServeStop(w http.ResponseWriter, r *http.Request) {
	s.cfg.Log("audit: serve stop")
	s.stopGateway()
	s.serve.Stop()
	writeJSON(w, http.StatusOK, s.serve.Status())
}
