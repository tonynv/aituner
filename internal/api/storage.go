package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tonynv/aituner/internal/connect"
	"github.com/tonynv/aituner/internal/modeldir"
	"github.com/tonynv/aituner/internal/report"
)

// Storage: every place aituner keeps data on disk, in one view. Folders the user can move (models, reports, knowledge
// base) follow the
// same policy (modeldir.Resolve: inside the home folder or on an external drive, no hidden or ~/Library paths). The
// app data folder (database, logs) is fixed by the platform.

// movable folders besides models (which keeps its own setting and endpoint): setting key and default under $HOME
var folderKinds = map[string]struct{ setting, home string }{
	"reports":   {"reports_dir", "Reports"},
	"knowledge": {"knowledge_dir", "KnowledgeBase"},
}

type folderInfo struct {
	Dir       string        `json:"dir"`
	Default   string        `json:"default"`
	IsDefault bool          `json:"is_default"`
	Info      modeldir.Info `json:"info"`
	Problem   string        `json:"problem,omitempty"`
}

type storageResp struct {
	Models    folderInfo `json:"models"`
	Reports   folderInfo `json:"reports"`
	Knowledge folderInfo `json:"knowledge"`
	Data      struct {
		Dir      string `json:"dir"`
		DBBytes  int64  `json:"db_bytes"`
		LogBytes int64  `json:"log_bytes"`
	} `json:"data"`
}

// folderDir returns a movable folder (reports, knowledge): the saved setting, or ~/<default>. Nothing is created here.
func (s *Server) folderDir(ctx context.Context, kind string) (folderInfo, error) {
	k := folderKinds[kind]
	home, err := os.UserHomeDir()
	if err != nil {
		return folderInfo{}, err
	}
	def := filepath.Join(home, k.home)
	saved, err := s.tn.GetSetting(ctx, k.setting)
	if err != nil {
		return folderInfo{}, err
	}
	fi := folderInfo{Default: def, IsDefault: saved == "", Dir: saved}
	if fi.IsDefault {
		fi.Dir = def
	}
	p, err := modeldir.Resolve(fi.Dir)
	if err != nil {
		fi.Problem = err.Error()
		return fi, err
	}
	fi.Dir, fi.Info = p, modeldir.Stat(p)
	return fi, nil
}

func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return 0
}

func (s *Server) storage(ctx context.Context) storageResp {
	var resp storageResp
	ms := s.settings(ctx)
	resp.Models = folderInfo{Dir: ms.ModelsDir, Default: ms.DefaultDir, IsDefault: ms.IsDefault, Info: ms.Info, Problem: ms.Problem}
	resp.Reports, _ = s.folderDir(ctx, "reports")
	resp.Knowledge, _ = s.folderDir(ctx, "knowledge")
	resp.Data.Dir = s.cfg.DataDir
	for _, f := range []string{"aituner.db", "aituner.db-wal", "aituner.db-shm"} {
		resp.Data.DBBytes += fileSize(filepath.Join(s.cfg.DataDir, f))
	}
	for _, f := range []string{"aituner.log", "aituner.log.1", "model-server.log"} {
		resp.Data.LogBytes += fileSize(filepath.Join(s.cfg.DataDir, f))
	}
	return resp
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.storage(r.Context()))
}

type putFolderReq struct {
	Dir string `json:"dir"`
}

// handlePutFolder (PUT /api/v1/storage/{kind}) validates, creates and proves the folder writable, then saves it;
// empty resets to the default (which is created later, when Bootstrap or a save needs it).
func (s *Server) handlePutFolder(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	k, ok := folderKinds[kind]
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown_folder", "folder must be reports or knowledge")
		return
	}
	var req putFolderReq
	if !decode(w, r, &req) {
		return
	}
	value := ""
	if req.Dir != "" {
		p, err := modeldir.Resolve(req.Dir)
		if err == nil {
			err = modeldir.Ensure(p)
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_folder", err.Error())
			return
		}
		value = p
	}
	s.cfg.Log("audit: " + kind + " folder set")
	if err := s.tn.SetSetting(r.Context(), k.setting, value); err != nil {
		writeErr(w, 500, "db", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.storage(r.Context()))
}

type saveReportReq struct {
	Run string `json:"run"`
}

// handleSaveReport writes the report (Markdown, CSV, JSON) into the reports folder and returns the paths.
func (s *Server) handleSaveReport(w http.ResponseWriter, r *http.Request) {
	var req saveReportReq
	if !decode(w, r, &req) {
		return
	}
	if req.Run != "" && !runIDRe.MatchString(req.Run) {
		writeErr(w, http.StatusBadRequest, "bad_run", "invalid run id")
		return
	}
	q := r.URL.Query()
	q.Set("run", req.Run)
	r.URL.RawQuery = q.Encode()
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
		writeErr(w, http.StatusConflict, "no_results", "this run has no benchmark results")
		return
	}
	dir, err := s.folderDir(r.Context(), "reports")
	if err == nil {
		err = modeldir.Ensure(dir.Dir)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_folder", err.Error())
		return
	}
	csv, err := report.CSV(rep)
	if err != nil {
		writeErr(w, 500, "report", err.Error())
		return
	}
	js, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		writeErr(w, 500, "report", err.Error())
		return
	}
	base := fmt.Sprintf("aituner-report-%s-%s", time.UnixMilli(run.CreatedAt).Format("20060102-1504"), run.ID[:8])
	paths := []string{}
	for ext, body := range map[string][]byte{"md": []byte(report.Markdown(rep)), "csv": []byte(csv), "json": js} {
		p := filepath.Join(dir.Dir, base+"."+ext)
		if err := connect.WriteAtomic(p, body, 0o644); err != nil {
			writeErr(w, 500, "write", err.Error())
			return
		}
		paths = append(paths, p)
	}
	s.cfg.Log("audit: report saved " + run.ID)
	writeJSON(w, http.StatusOK, map[string]any{"dir": dir.Dir, "files": paths})
}

type revealReq struct {
	Which string `json:"which"` // models | reports | knowledge | data
}

// handleReveal shows one of aituner's own folders in Finder. The path comes from settings, never from the request.
func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	var req revealReq
	if !decode(w, r, &req) {
		return
	}
	st := s.storage(r.Context())
	dir := map[string]string{"models": st.Models.Dir, "reports": st.Reports.Dir, "knowledge": st.Knowledge.Dir, "data": st.Data.Dir}[req.Which]
	if dir == "" {
		writeErr(w, http.StatusBadRequest, "bad_folder", "which must be models, reports, knowledge or data")
		return
	}
	if _, err := os.Stat(dir); err != nil {
		writeErr(w, http.StatusConflict, "missing_folder", "this folder does not exist yet: Bootstrap in Setup creates it, after you confirm")
		return
	}
	if err := s.cfg.Open(dir); err != nil {
		writeErr(w, 500, "open", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "opened"})
}
