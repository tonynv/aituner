package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/connect"
	"github.com/tonynv/aituner/internal/serve"
)

func (s *Server) connectRunner() connect.Runner {
	if s.cfg.Connect != nil {
		return s.cfg.Connect
	}
	return connect.ExecRunner{}
}

// connectEnv builds the integration environment from aituner's own state. running is false when no model is being served.
func (s *Server) connectEnv(ctx context.Context) (env connect.Env, running bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return env, false, err
	}
	hw := s.hardware()
	env = connect.Env{
		Home: home, ConfigDir: filepath.Join(home, ".config", "aituner"), BinDir: filepath.Join(home, ".local", "bin"),
		DataDir: s.cfg.DataDir, KeyFile: filepath.Join(s.cfg.DataDir, "gateway.key"), Key: s.gwKey,
		LogFile: filepath.Join(s.cfg.DataDir, "model-server.log"), Run: s.connectRunner(),
		Port: gatewayPort(), RootURL: fmt.Sprintf("http://127.0.0.1:%d", gatewayPort()), BaseURL: fmt.Sprintf("http://127.0.0.1:%d/v1", gatewayPort()),
	}
	if hw != nil {
		env.VenvBin = filepath.Join(hw.Software.MLX.VenvPath, "bin")
	}
	st := s.serve.Status()
	if gi, ok := s.gatewayInfo(); ok && st.State == serve.StateRunning {
		env.Port, env.RootURL, env.BaseURL, env.Model = gi.Port, gi.Root, gi.BaseURL, st.Repo
		if c := s.contextFor(ctx, st.ModelDir, st.Spec.KVBits); c != nil && c.Known {
			env.ContextTokens = c.Tokens
		}
		running = true
	}
	return env, running, nil
}

type connectItem struct {
	ID     string         `json:"id"`
	Plan   connect.Plan   `json:"plan"`
	Status connect.Status `json:"status"`
}

func (s *Server) handleConnectList(w http.ResponseWriter, r *http.Request) {
	env, running, err := s.connectEnv(r.Context())
	if err != nil {
		writeErr(w, 500, "connect", err.Error())
		return
	}
	items := []connectItem{}
	for _, i := range connect.All() {
		items = append(items, connectItem{ID: i.ID(), Plan: i.Plan(env), Status: i.Status(r.Context(), env)})
	}
	home, _ := os.UserHomeDir()
	writeJSON(w, http.StatusOK, map[string]any{
		"model_running": running, "model": env.Model, "base_url": env.BaseURL, "root_url": env.RootURL, "context_tokens": env.ContextTokens,
		"key_file": env.KeyFile, "bin_dir": env.BinDir, "project_default": home, "curl": connect.CurlExample(env), "items": items,
	})
}

type connectReq struct {
	ID      string `json:"id"`
	Confirm bool   `json:"confirm"`
	Project string `json:"project"`
}

func (s *Server) integration(w http.ResponseWriter, id string) connect.Integration {
	i := connect.ByID(id)
	if i == nil {
		writeErr(w, http.StatusNotFound, "unknown_integration", "unknown integration")
	}
	return i
}

func (s *Server) handleConnectSetup(w http.ResponseWriter, r *http.Request) {
	var req connectReq
	if !decode(w, r, &req) {
		return
	}
	integ := s.integration(w, req.ID)
	if integ == nil {
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusBadRequest, "needs_confirmation", "review the plan and confirm: setup installs software and writes files")
		return
	}
	env, running, err := s.connectEnv(r.Context())
	if err != nil {
		writeErr(w, 500, "connect", err.Error())
		return
	}
	if !running {
		writeErr(w, http.StatusConflict, "no_model", "start a model first: the editor configuration embeds the model and the gateway address")
		return
	}
	s.cfg.Log("audit: connect setup " + req.ID)
	err = s.jobs.start(s.ctx, "connect", func(ctx context.Context, emit bench.Emit) error {
		emit(bench.Event{Level: "info", Message: "Setting up " + integ.Plan(env).Title})
		if err := integ.Setup(ctx, env, func(m string) { emit(bench.Event{Level: "info", Message: m}) }); err != nil {
			return err
		}
		emit(bench.Event{Level: "info", Message: "Done", Progress: 1})
		return nil
	}, nil)
	if err != nil {
		writeErr(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleConnectRemove(w http.ResponseWriter, r *http.Request) {
	var req connectReq
	if !decode(w, r, &req) {
		return
	}
	integ := s.integration(w, req.ID)
	if integ == nil {
		return
	}
	env, _, err := s.connectEnv(r.Context())
	if err != nil {
		writeErr(w, 500, "connect", err.Error())
		return
	}
	s.cfg.Log("audit: connect remove " + req.ID)
	err = s.jobs.start(s.ctx, "connect", func(ctx context.Context, emit bench.Emit) error {
		return integ.Remove(ctx, env, func(m string) { emit(bench.Event{Level: "info", Message: m}) })
	}, nil)
	if err != nil {
		writeErr(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleConnectLaunch(w http.ResponseWriter, r *http.Request) {
	var req connectReq
	if !decode(w, r, &req) {
		return
	}
	integ := s.integration(w, req.ID)
	if integ == nil {
		return
	}
	env, running, err := s.connectEnv(r.Context())
	if err != nil {
		writeErr(w, 500, "connect", err.Error())
		return
	}
	if !running {
		writeErr(w, http.StatusConflict, "no_model", "start a model first")
		return
	}
	project, err := connect.ValidateProject(env.Home, req.Project)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project", err.Error())
		return
	}
	s.cfg.Log("audit: connect launch " + req.ID + " project=" + project)
	cmd, err := integ.Launch(r.Context(), env, project)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot_launch", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "opened", "command": cmd})
}
