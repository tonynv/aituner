package api

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/tonynv/aituner/internal/download"
	"github.com/tonynv/aituner/internal/modeldir"
	"github.com/tonynv/aituner/internal/reco"
)

const settingModelsDir = "models_dir"

// rememberOffered records every repo the recommender just offered. Downloads may only fetch these, so the API
// is not a general "download anything from Hugging Face" endpoint.
func (s *Server) rememberOffered(out *reco.Output) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowed = map[string]bool{}
	for _, list := range out.Groups {
		for _, c := range list {
			if c.Runtime == "mlx" && c.Repo != "" {
				s.allowed[c.Repo] = true
			}
			for _, v := range c.Variants {
				s.allowed[v.Repo] = true
			}
		}
	}
}

func (s *Server) offered(repo string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allowed[repo]
}

// modelsDir returns the validated download root: the saved setting, or ~/Models.
func (s *Server) modelsDir(ctx context.Context) (path string, isDefault bool, err error) {
	saved, err := s.tn.GetSetting(ctx, settingModelsDir)
	if err != nil {
		return "", false, err
	}
	isDefault = saved == ""
	if isDefault {
		if saved, err = modeldir.Default(); err != nil {
			return "", false, err
		}
	}
	p, err := modeldir.Resolve(saved)
	return p, isDefault, err
}

type settingsResp struct {
	ModelsDir  string        `json:"models_dir"`
	IsDefault  bool          `json:"is_default"`
	DefaultDir string        `json:"default_dir"`
	Info       modeldir.Info `json:"info"`
	Problem    string        `json:"problem,omitempty"` // saved folder no longer passes the policy
}

func (s *Server) settings(ctx context.Context) settingsResp {
	def, _ := modeldir.Default()
	resp := settingsResp{DefaultDir: def}
	p, isDef, err := s.modelsDir(ctx)
	resp.IsDefault = isDef
	if err != nil {
		saved, _ := s.tn.GetSetting(ctx, settingModelsDir)
		resp.ModelsDir, resp.Problem = saved, err.Error()
		return resp
	}
	resp.ModelsDir, resp.Info = p, modeldir.Stat(p)
	return resp
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settings(r.Context()))
}

type putSettingsReq struct {
	ModelsDir string `json:"models_dir"`
}

// handlePutSettings validates the folder against the policy, creates it, proves it is writable, then saves it.
// An empty value resets to the default.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req putSettingsReq
	if !decode(w, r, &req) {
		return
	}
	if j := s.jobs.info(); (j != nil && j.Running) || s.dl.Active() {
		writeErr(w, http.StatusConflict, "busy", "wait for the running operation before changing the folder")
		return
	}
	value := ""
	if req.ModelsDir != "" {
		p, err := modeldir.Resolve(req.ModelsDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_folder", err.Error())
			return
		}
		if err := modeldir.Ensure(p); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_folder", err.Error())
			return
		}
		value = p
	}
	if err := s.tn.SetSetting(r.Context(), settingModelsDir, value); err != nil {
		writeErr(w, 500, "db", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.settings(r.Context()))
}

type downloadView struct {
	download.Status
	Run string `json:"run,omitempty"` // shell command to chat with the downloaded copy (done only)
}

type downloadsResp struct {
	ModelsDir string         `json:"models_dir"`
	Active    bool           `json:"active"`
	Items     []downloadView `json:"items"`
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	root, _, err := s.modelsDir(r.Context())
	resp := downloadsResp{Items: []downloadView{}, Active: s.dl.Active()}
	if err != nil { // saved folder is no longer valid: still report in-memory downloads, none from disk
		resp.Items = viewsOf(s.dl.List(""), "")
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.ModelsDir = root
	bin := ""
	if hw := s.hardware(); hw != nil && hw.Software.MLX.Ready {
		bin = filepath.Join(hw.Software.MLX.VenvPath, "bin")
	}
	resp.Items = viewsOf(s.dl.List(root), bin)
	writeJSON(w, http.StatusOK, resp)
}

func viewsOf(list []download.Status, bin string) []downloadView {
	out := make([]downloadView, 0, len(list))
	for _, st := range list {
		v := downloadView{Status: st}
		if st.State == download.State_Done {
			v.Run = download.ChatCommand(bin, st.Dest)
		}
		out = append(out, v)
	}
	return out
}

type downloadReq struct {
	Repo string `json:"repo"`
}

func (s *Server) handleStartDownload(w http.ResponseWriter, r *http.Request) {
	var req downloadReq
	if !decode(w, r, &req) {
		return
	}
	if !reco.ValidRepo(req.Repo) {
		writeErr(w, http.StatusBadRequest, "bad_repo", "invalid repository id")
		return
	}
	if !s.hardwareReady(w) {
		return
	}
	if !s.offered(req.Repo) {
		writeErr(w, http.StatusForbidden, "not_offered", "only models from the current recommendations can be downloaded")
		return
	}
	if j := s.jobs.info(); j != nil && j.Running {
		writeErr(w, http.StatusConflict, "busy", "a benchmark or tuning job is running; downloads would distort it")
		return
	}
	mlx, ready := s.mlxRuntime()
	if !ready {
		writeErr(w, http.StatusConflict, "no_runtime", "the MLX runtime is not installed yet; install it first")
		return
	}
	root, _, err := s.modelsDir(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_folder", "the saved models folder is not usable: "+err.Error())
		return
	}
	s.cfg.Log("audit: download requested repo=" + req.Repo + " dest_root=" + root)
	st, err := s.dl.Enqueue(s.ctx, mlx, req.Repo, root)
	switch err {
	case nil:
		writeJSON(w, http.StatusAccepted, st)
	case download.ErrBusy:
		writeErr(w, http.StatusConflict, "download_running", err.Error())
	default:
		writeErr(w, http.StatusBadRequest, "cannot_download", err.Error())
	}
}

func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	var req downloadReq
	if !decode(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": s.dl.Cancel(req.Repo)})
}
