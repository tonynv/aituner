package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/connect"
	"github.com/tonynv/aituner/internal/monitor"
)

func (s *Server) ramTotal() int64 {
	if hw := s.hardware(); hw != nil {
		return hw.Memory.TotalBytes
	}
	return 0
}

type monitorTool struct {
	connect.TerminalMonitor
	Installed bool `json:"installed"`
}

type monitorModel struct {
	State     string `json:"state"`
	Repo      string `json:"repo"`
	RSSBytes  int64  `json:"rss_bytes"`
	StartedAt int64  `json:"started_at,omitempty"`
}

type monitorResp struct {
	Source  string           `json:"source"` // macmon or builtin; empty until the first sample
	Samples []monitor.Sample `json:"samples"`
	Model   monitorModel     `json:"model"`
	Tools   []monitorTool    `json:"tools,omitempty"`
}

// handleMonitor returns live samples newer than ?since= (the full 10-minute history for 0) and the model server's state.
// Polling it keeps sampling on; ?tools=1 also lists the terminal monitors.
func (s *Server) handleMonitor(w http.ResponseWriter, r *http.Request) {
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if r.URL.Query().Get("since") == "" {
		since, err = 0, nil
	}
	if err != nil || since < 0 {
		writeErr(w, http.StatusBadRequest, "bad_since", "since must be a sample sequence number")
		return
	}
	s.mon.Want(s.ctx)
	var resp monitorResp
	resp.Samples, resp.Source = s.mon.Since(since)
	st := s.serve.Status()
	resp.Model = monitorModel{State: st.State, Repo: st.Repo, RSSBytes: st.RSSBytes, StartedAt: st.StartedAt}
	if r.URL.Query().Get("tools") == "1" {
		env, _, err := s.connectEnv(r.Context())
		if err != nil {
			writeErr(w, 500, "connect", err.Error())
			return
		}
		for _, m := range connect.TerminalMonitors() {
			resp.Tools = append(resp.Tools, monitorTool{TerminalMonitor: m, Installed: m.Installed(env)})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type monitorToolReq struct {
	ID      string `json:"id"`
	Action  string `json:"action"` // install | open
	Confirm bool   `json:"confirm"`
}

// handleMonitorTool installs a terminal monitor (a job, after confirmation) or opens it in Terminal.
func (s *Server) handleMonitorTool(w http.ResponseWriter, r *http.Request) {
	var req monitorToolReq
	if !decode(w, r, &req) {
		return
	}
	m := connect.TerminalMonitorByID(req.ID)
	if m == nil {
		writeErr(w, http.StatusNotFound, "unknown_tool", "unknown monitor")
		return
	}
	env, _, err := s.connectEnv(r.Context())
	if err != nil {
		writeErr(w, 500, "connect", err.Error())
		return
	}
	switch req.Action {
	case "open":
		s.cfg.Log("audit: monitor open " + m.ID)
		if err := m.Open(r.Context(), env); err != nil {
			writeErr(w, http.StatusConflict, "monitor", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "opened"})
	case "install":
		if !req.Confirm {
			writeErr(w, http.StatusBadRequest, "needs_confirmation", "confirm: this runs brew install "+m.Formula)
			return
		}
		s.cfg.Log("audit: monitor install " + m.ID)
		err := s.jobs.start(s.ctx, "monitor", func(ctx context.Context, emit bench.Emit) error {
			if err := m.Install(ctx, env, func(l string) { emit(bench.Event{Level: "info", Message: l}) }); err != nil {
				return err
			}
			emit(bench.Event{Level: "info", Message: m.Name + " installed", Progress: 1})
			return nil
		}, nil)
		if err != nil {
			writeErr(w, http.StatusConflict, "busy", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	default:
		writeErr(w, http.StatusBadRequest, "bad_action", "action must be install or open")
	}
}
