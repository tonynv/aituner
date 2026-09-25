package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tonynv/aituner/internal/download"
	"github.com/tonynv/aituner/internal/store"
)

// Clearing: "models" deletes the models aituner downloaded (folders carrying its completion marker, nothing else in the
// models folder); "reports" deletes every run, benchmark result, tuning record and model benchmark. GET previews exactly
// what would go; POST does it, after confirmation.

type resetModel struct {
	Repo  string `json:"repo"`
	Dest  string `json:"dest"`
	Bytes int64  `json:"bytes"`
}

type resetPreview struct {
	ModelsDir      string       `json:"models_dir"`
	Models         []resetModel `json:"models"`
	Runs           int          `json:"runs"`
	MeasuredRuns   int          `json:"measured_runs"`
	ReportsBlocked string       `json:"reports_blocked,omitempty"` // why reports cannot be cleared now
	Busy           string       `json:"busy,omitempty"`            // why nothing can be cleared now
}

func (s *Server) resetPreview(r *http.Request) (resetPreview, error) {
	p := resetPreview{Models: []resetModel{}}
	root, _, err := s.modelsDir(r.Context())
	if err != nil {
		return p, err
	}
	p.ModelsDir = root
	for _, it := range s.dl.List(root) {
		if it.State == download.State_Done {
			p.Models = append(p.Models, resetModel{Repo: it.Repo, Dest: it.Dest, Bytes: it.BytesTotal})
		}
	}
	runs, err := s.tn.ListRuns(r.Context(), 100000)
	if err != nil {
		return p, err
	}
	p.Runs = len(runs)
	for _, run := range runs {
		if rs, err := s.tn.Results(r.Context(), run.ID); err == nil && len(rs) > 0 {
			p.MeasuredRuns++
		}
		if cs, err := s.tn.TuneChanges(r.Context(), run.ID); err == nil {
			for _, c := range cs {
				if c.RevertedAt == nil {
					p.ReportsBlocked = "A tuning change is still applied. Revert it in Tune first: its record is what undoes it."
				}
			}
		}
	}
	if j := s.jobs.info(); j != nil && j.Running {
		p.Busy = "A " + j.Kind + " job is running. Wait for it or cancel it first."
	} else if s.dl.Active() {
		p.Busy = "A download is in progress. Cancel it first."
	}
	return p, nil
}

func (s *Server) handleResetPreview(w http.ResponseWriter, r *http.Request) {
	p, err := s.resetPreview(r)
	if err != nil {
		writeErr(w, 500, "reset", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type resetReq struct {
	Models  bool `json:"models"`
	Reports bool `json:"reports"`
	Confirm bool `json:"confirm"`
}

type resetResult struct {
	ModelsDeleted []string `json:"models_deleted"`
	BytesFreed    int64    `json:"bytes_freed"`
	RunsDeleted   int64    `json:"runs_deleted"`
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	var req resetReq
	if !decode(w, r, &req) {
		return
	}
	if !req.Models && !req.Reports {
		writeErr(w, http.StatusBadRequest, "nothing", "choose models, reports or both")
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusBadRequest, "needs_confirmation", "review what will be deleted and confirm")
		return
	}
	p, err := s.resetPreview(r)
	if err != nil {
		writeErr(w, 500, "reset", err.Error())
		return
	}
	if p.Busy != "" {
		writeErr(w, http.StatusConflict, "busy", p.Busy)
		return
	}
	if req.Reports && p.ReportsBlocked != "" {
		writeErr(w, http.StatusConflict, "changes_applied", p.ReportsBlocked)
		return
	}
	res := resetResult{ModelsDeleted: []string{}}
	if req.Models {
		s.cfg.Log("audit: clear models")
		if st := s.serve.Status(); st.ModelDir != "" {
			for _, m := range p.Models {
				if m.Dest == st.ModelDir { // it is about to be deleted: stop serving it first
					s.stopGateway()
					s.serve.Stop()
				}
			}
		}
		for _, m := range p.Models {
			if err := removeModel(p.ModelsDir, m); err != nil {
				writeErr(w, 500, "delete_failed", m.Repo+": "+err.Error())
				return
			}
			res.ModelsDeleted = append(res.ModelsDeleted, m.Repo)
			res.BytesFreed += m.Bytes
		}
	}
	if req.Reports {
		s.cfg.Log("audit: clear reports")
		n, err := s.tn.ClearReports(r.Context(), "bench_meta", modelBenchSource)
		if errors.Is(err, store.ErrChangesApplied) {
			writeErr(w, http.StatusConflict, "changes_applied", err.Error())
			return
		}
		if err != nil {
			writeErr(w, 500, "db", err.Error())
			return
		}
		res.RunsDeleted = n
		if err := s.ensureRun(r.Context(), true); err != nil { // this launch needs a run to work in
			writeErr(w, 500, "db", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// removeModel deletes one downloaded model folder, only after re-checking that it is a real directory (not a
// symlink) directly at <root>/<org>/<name> and still carries aituner's marker for that repo.
func removeModel(root string, m resetModel) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	dest := filepath.Clean(m.Dest)
	parts := strings.Split(m.Repo, "/")
	if len(parts) != 2 || dest != filepath.Join(root, parts[0], parts[1]) {
		return errors.New("not a model folder aituner manages")
	}
	fi, err := os.Lstat(dest)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("not a plain folder")
	}
	real, err := filepath.EvalSymlinks(dest)
	if err != nil || !strings.HasPrefix(real, rootReal+string(os.PathSeparator)) {
		return errors.New("outside the models folder")
	}
	if _, ok := download.Complete(dest, m.Repo); !ok {
		return errors.New("no aituner download marker")
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(dest)) // the org folder, only if now empty
	return nil
}
