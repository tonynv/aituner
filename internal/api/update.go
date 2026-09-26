package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/connect"
	"github.com/tonynv/aituner/internal/httpx"
	"github.com/tonynv/aituner/internal/update"
)

// Software update. A release build checks GitHub 30 s after launch and then daily (unless turned off), tells the app
// shell when a newer version appears (it prompts natively; the web UI shows a banner), and installs it on request:
// verified download, then a helper swaps the app after aituner quits and relaunches it. Homebrew installs are updated
// with brew instead, in Terminal; development and terminal builds never replace themselves.

const (
	settingUpdateAuto = "update_auto" // "off" disables automatic checks
	settingUpdateSkip = "update_skip" // a version the user chose to skip
	updateEvery       = 24 * time.Hour
)

type updater struct {
	mu        sync.Mutex
	client    *httpx.Client
	checked   time.Time
	latest    *update.Release
	err       string
	announced string // the version the app shell was last told about
}

type updateStatus struct {
	Current   string          `json:"current"`         // this build's version ("0.1.0"), or its build string
	Release   bool            `json:"release"`         // this is a tagged release build
	Latest    *update.Release `json:"latest"`          // nil until checked, or when GitHub has no release
	Available bool            `json:"available"`       // a newer release exists
	Skipped   string          `json:"skipped"`         // version the user chose to skip
	Auto      bool            `json:"auto"`            // automatic daily checks
	Checked   int64           `json:"checked_at"`      // ms; 0 = never
	Error     string          `json:"error,omitempty"` // last check's failure
	Method    string          `json:"method"`          // app | homebrew | manual
	Why       string          `json:"why,omitempty"`   // why manual
}

// updateMethod decides how this copy is updated.
func (s *Server) updateMethod() (method, why string) {
	bundle, ok := update.BundleOf(s.cfg.Executable)
	switch {
	case !ok || s.cfg.AppPID == 0:
		return "manual", "aituner is running from a terminal build: download the new version from GitHub, or pull and run ./build_app.sh."
	case update.Homebrew(bundle):
		return "homebrew", ""
	}
	if _, rel := update.Current(s.cfg.Version); !rel {
		return "manual", "this is a development build; it is not replaced automatically."
	}
	if !update.Writable(bundle) {
		return "manual", "the folder aituner is installed in is not writable; install the new version from GitHub."
	}
	return "app", ""
}

func (s *Server) updateStatus(ctx context.Context) updateStatus {
	cur, rel := update.Current(s.cfg.Version)
	st := updateStatus{Current: cur, Release: rel}
	if !rel {
		st.Current = s.cfg.Version
	}
	auto, _ := s.tn.GetSetting(ctx, settingUpdateAuto)
	st.Auto = auto != "off"
	st.Skipped, _ = s.tn.GetSetting(ctx, settingUpdateSkip)
	s.upd.mu.Lock()
	st.Latest, st.Error = s.upd.latest, s.upd.err
	if !s.upd.checked.IsZero() {
		st.Checked = s.upd.checked.UnixMilli()
	}
	s.upd.mu.Unlock()
	st.Available = rel && st.Latest != nil && update.Newer(st.Latest.Version, cur)
	st.Method, st.Why = s.updateMethod()
	return st
}

// CheckForUpdate asks GitHub for the latest release. A manual check always reports back to the app shell ("update",
// "uptodate" or "update-error"); an automatic one announces a new version once, unless the user skipped it.
func (s *Server) CheckForUpdate(ctx context.Context, manual bool) updateStatus {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rel, err := update.Latest(ctx, s.upd.client)
	s.upd.mu.Lock()
	s.upd.checked, s.upd.latest, s.upd.err = time.Now(), rel, ""
	if err != nil {
		s.upd.err = err.Error()
	}
	s.upd.mu.Unlock()
	st := s.updateStatus(ctx)
	notify := s.cfg.Notify
	if notify == nil {
		return st
	}
	switch {
	case st.Error != "" && manual:
		notify("update-error " + st.Error)
	case st.Available && (manual || st.Skipped != st.Latest.Version):
		s.upd.mu.Lock()
		first := s.upd.announced != st.Latest.Version
		s.upd.announced = st.Latest.Version
		s.upd.mu.Unlock()
		if manual || first {
			notify("update " + st.Latest.Version)
		}
	case manual:
		notify("uptodate " + st.Current)
	}
	return st
}

// updateLoop checks 30 s after launch and then daily, for release builds with automatic checks on.
func (s *Server) updateLoop() {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		if a, _ := s.tn.GetSetting(s.ctx, settingUpdateAuto); a != "off" {
			s.CheckForUpdate(s.ctx, false)
		}
		t.Reset(updateEvery)
	}
}

// NotifyApp sends a line to the app shell, when aituner runs under it.
func (s *Server) NotifyApp(line string) {
	if s.cfg.Notify != nil {
		s.cfg.Notify(line)
	}
}

// SkipUpdate records a version the user does not want to be offered again.
func (s *Server) SkipUpdate(ctx context.Context, version string) error {
	if version != "" {
		if _, ok := update.Current("v" + version); !ok {
			return errors.New("invalid version")
		}
	}
	return s.tn.SetSetting(ctx, settingUpdateSkip, version)
}

// InstallUpdate starts installing the latest release (a job). For the app it downloads and verifies the release,
// starts the swap helper and asks the app shell to quit; for Homebrew it runs the upgrade in Terminal.
func (s *Server) InstallUpdate(ctx context.Context) error {
	st := s.updateStatus(ctx)
	if !st.Available {
		return errors.New("no newer release is available")
	}
	rel := st.Latest
	if st.Method == "manual" {
		return errors.New(st.Why)
	}
	if st.Method == "homebrew" {
		env, _, err := s.connectEnv(ctx)
		if err != nil {
			return err
		}
		brew, ok := env.Run.Look("brew")
		if !ok {
			return errors.New("Homebrew was not found")
		}
		s.cfg.Log("audit: update via homebrew to " + rel.Version)
		// brew quits aituner itself (the cask's uninstall quit), upgrades, then aituner is opened again
		return connect.RunInTerminal(ctx, env, "aituner-update", []string{brew, "upgrade", "--cask", "aituner"}, []string{"/usr/bin/open", "-a", "aituner"})
	}
	bundle, _ := update.BundleOf(s.cfg.Executable)
	s.cfg.Log("audit: update to " + rel.Version)
	return s.jobs.start(s.ctx, "update", func(ctx context.Context, emit bench.Emit) error {
		say := func(m string, p float64) { emit(bench.Event{Level: "info", Message: m, Progress: p}) }
		root := filepath.Join(s.cfg.DataDir, "update")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		dir, err := os.MkdirTemp(root, rel.Version+"-")
		if err != nil {
			return err
		}
		say("Downloading aituner "+rel.Version, 0.1)
		app, err := update.Fetch(ctx, s.upd.client, update.ExecRunner, rel, dir)
		if err != nil {
			return err
		}
		say("Checking the signature and notarization", 0.7)
		if err := update.Verify(ctx, update.ExecRunner, app, update.BundleID, rel.Version, update.TeamID); err != nil {
			return fmt.Errorf("the download is not a genuine aituner release (%v); nothing was changed", err)
		}
		script := filepath.Join(dir, "install.sh")
		if err := os.WriteFile(script, []byte(update.Script(s.cfg.AppPID, app, bundle, "/usr/bin/open")), 0o700); err != nil {
			return err
		}
		logf, err := os.OpenFile(filepath.Join(s.cfg.DataDir, "update.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer logf.Close()
		cmd := exec.Command("/bin/bash", script)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survives aituner quitting
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		say("Verified. aituner restarts with version "+rel.Version, 1)
		if s.cfg.Quit != nil {
			time.AfterFunc(1500*time.Millisecond, s.cfg.Quit) // let the UI show the message first
		}
		return nil
	}, nil)
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updateStatus(r.Context()))
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !decode(w, r, &struct{}{}) {
		return
	}
	writeJSON(w, http.StatusOK, s.CheckForUpdate(r.Context(), false))
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Auto *bool   `json:"auto"`
		Skip *string `json:"skip"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Auto != nil {
		v := ""
		if !*req.Auto {
			v = "off"
		}
		if err := s.tn.SetSetting(r.Context(), settingUpdateAuto, v); err != nil {
			writeErr(w, 500, "db", err.Error())
			return
		}
	}
	if req.Skip != nil {
		if err := s.SkipUpdate(r.Context(), *req.Skip); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_version", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, s.updateStatus(r.Context()))
}

func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusBadRequest, "needs_confirmation", "confirm the update")
		return
	}
	if st := s.serve.Status().State; st != "stopped" && st != "error" {
		writeErr(w, http.StatusConflict, "model_running", "stop the running model first: updating restarts aituner")
		return
	}
	if err := s.InstallUpdate(r.Context()); err != nil {
		writeErr(w, http.StatusConflict, "update", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}
