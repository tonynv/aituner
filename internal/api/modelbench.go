package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/reco"
	"github.com/tonynv/aituner/internal/serve"
	"github.com/tonynv/aituner/internal/store"
)

const modelBenchSource = "modelbench"

type modelBenchReq struct {
	Repos []string `json:"repos"` // empty = every downloaded model
}

// modelBenchItem pairs a downloaded model with its latest stored benchmark, if any.
type modelBenchItem struct {
	Repo   string             `json:"repo"`
	SizeGB float64            `json:"size_gb"`
	Result *bench.ModelResult `json:"result,omitempty"`
}

// handleModelBenchStart benchmarks downloaded models one after another. Only models in the models folder can be
// measured, and never while a model is being served: the GPU has to be idle for the numbers to mean anything.
func (s *Server) handleModelBenchStart(w http.ResponseWriter, r *http.Request) {
	var req modelBenchReq
	if !decode(w, r, &req) {
		return
	}
	if !s.hardwareReady(w) {
		return
	}
	mlx, ready := s.mlxRuntime()
	if !ready {
		writeErr(w, http.StatusConflict, "no_runtime", "MLX is not installed yet: install it first")
		return
	}
	if st := s.serve.Status().State; st != serve.StateStopped && st != serve.StateError {
		writeErr(w, http.StatusConflict, "model_running", "stop the running model first: a benchmark needs the GPU to itself")
		return
	}
	models, _ := s.downloadedModels(r.Context())
	want := map[string]bool{}
	for _, repo := range req.Repos {
		if !reco.ValidRepo(repo) {
			writeErr(w, http.StatusBadRequest, "bad_repo", "invalid repository id")
			return
		}
		want[repo] = true
	}
	var todo []serveModel
	for _, m := range models {
		if len(want) == 0 || want[m.Repo] {
			todo = append(todo, m)
			delete(want, m.Repo)
		}
	}
	if len(want) > 0 {
		writeErr(w, http.StatusNotFound, "not_downloaded", "one of those models is not downloaded")
		return
	}
	if len(todo) == 0 {
		writeErr(w, http.StatusConflict, "nothing_to_benchmark", "download a model first")
		return
	}
	s.cfg.Log(fmt.Sprintf("audit: model benchmark requested models=%d", len(todo)))
	err := s.jobs.start(s.ctx, "modelbench", func(ctx context.Context, emit bench.Emit) error {
		for i, m := range todo {
			base := float64(i) / float64(len(todo))
			emit(bench.Event{Level: "info", Suite: "modelbench", Message: fmt.Sprintf("[%d/%d] %s", i+1, len(todo), m.Repo), Progress: base})
			sub := func(e bench.Event) {
				e.Progress = base + e.Progress/float64(len(todo))
				emit(e)
			}
			res, err := mlx.ModelBench(ctx, sub, m.Dest)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			res.Repo, res.SizeGB, res.At = m.Repo, m.SizeGB, time.Now().UnixMilli()
			if err != nil {
				res.Error = lastLine(err.Error())
				emit(bench.Event{Level: "warn", Suite: "modelbench", Message: m.Repo + ": " + err.Error()})
			}
			b, _ := json.Marshal(res)
			if err := s.tn.CachePut(ctx, modelBenchSource, m.Repo, b); err != nil {
				return err
			}
		}
		return nil
	}, func(error) {})
	if err != nil {
		writeErr(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleModelBenchList(w http.ResponseWriter, r *http.Request) {
	models, _ := s.downloadedModels(r.Context())
	items := make([]modelBenchItem, 0, len(models))
	for _, m := range models {
		it := modelBenchItem{Repo: m.Repo, SizeGB: m.SizeGB}
		if e, err := s.tn.CacheGet(r.Context(), modelBenchSource, m.Repo); err == nil {
			var res bench.ModelResult
			if json.Unmarshal(e.Body, &res) == nil {
				it.Result = &res
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			writeErr(w, 500, "db", err.Error())
			return
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// lastLine is the final non-empty line of an error: for a Python failure that is the exception, not the traceback.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 300 {
		l = l[:300] + "…"
	}
	return l
}
