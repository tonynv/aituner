//go:build darwin

package tune

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AgentSpec describes the per-user LaunchAgent that re-applies the Ollama environment at login.
// Fields are injectable so tests can exercise the real launchctl path without touching real settings.
type AgentSpec struct {
	Label string
	Dir   string      // directory for the plist (normally ~/Library/LaunchAgents)
	Env   [][2]string // variables to setenv at login
}

func DefaultAgent() AgentSpec {
	home, _ := os.UserHomeDir()
	return AgentSpec{
		Label: "ai.aituner.ollama-env",
		Dir:   filepath.Join(home, "Library", "LaunchAgents"),
		Env:   [][2]string{{"OLLAMA_FLASH_ATTENTION", "1"}, {"OLLAMA_KV_CACHE_TYPE", "q8_0"}},
	}
}

func (a AgentSpec) path() string { return filepath.Join(a.Dir, a.Label+".plist") }

func (a AgentSpec) domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Installed reports whether the agent's plist exists.
func (a AgentSpec) Installed() bool {
	_, err := os.Stat(a.path())
	return err == nil
}

// script is run by /bin/sh at login; built only from our constant variable names and values.
func (a AgentSpec) script() string {
	s := ""
	for i, kv := range a.Env {
		if i > 0 {
			s += "; "
		}
		s += fmt.Sprintf("/bin/launchctl setenv %s %s", kv[0], kv[1])
	}
	return s
}

func plutil(ctx context.Context, args ...string) error {
	if out, err := exec.CommandContext(ctx, "/usr/bin/plutil", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("plutil %v: %w: %s", args, err, out)
	}
	return nil
}

// Install writes the plist with plutil (exec argv, no shell), lints it, and loads it into the user domain.
func (a AgentSpec) Install(ctx context.Context) error {
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		return err
	}
	p := a.path()
	_ = os.Remove(p)
	steps := [][]string{
		{"-create", "xml1", p},
		{"-insert", "Label", "-string", a.Label, p},
		{"-insert", "ProgramArguments", "-array", p},
		{"-insert", "ProgramArguments.0", "-string", "/bin/sh", p},
		{"-insert", "ProgramArguments.1", "-string", "-c", p},
		{"-insert", "ProgramArguments.2", "-string", a.script(), p},
		{"-insert", "RunAtLoad", "-bool", "YES", p},
	}
	for _, s := range steps {
		if err := plutil(ctx, s...); err != nil {
			_ = os.Remove(p)
			return err
		}
	}
	if err := plutil(ctx, "-lint", p); err != nil {
		_ = os.Remove(p)
		return err
	}
	_ = exec.CommandContext(ctx, "/bin/launchctl", "bootout", a.domain()+"/"+a.Label).Run() // ignore: may not be loaded
	if out, err := exec.CommandContext(ctx, "/bin/launchctl", "bootstrap", a.domain(), p).CombinedOutput(); err != nil {
		_ = os.Remove(p)
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, out)
	}
	if err := exec.CommandContext(ctx, "/bin/launchctl", "print", a.domain()+"/"+a.Label).Run(); err != nil {
		return errors.New("verification failed: agent is not loaded")
	}
	// bootstrap loads the job but launchd does not fire RunAtLoad for a job added to a live session,
	// so run it now and verify it really applies the variables (at login launchd runs it by itself).
	if out, err := exec.CommandContext(ctx, "/bin/launchctl", "kickstart", "-k", a.domain()+"/"+a.Label).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl kickstart: %w: %s", err, out)
	}
	return a.waitEnv(ctx)
}

func (a AgentSpec) waitEnv(ctx context.Context) error {
	deadline := time.Now().Add(6 * time.Second)
	for {
		ok := true
		for _, kv := range a.Env {
			out, _ := guiLaunchctl(ctx, "getenv", kv[0]).Output()
			if strings.TrimSpace(string(out)) != kv[1] {
				ok = false
			}
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("verification failed: the agent ran but the variables are not set")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Remove unloads and deletes the agent. It is safe to call when nothing is installed.
func (a AgentSpec) Remove(ctx context.Context) error {
	_ = exec.CommandContext(ctx, "/bin/launchctl", "bootout", a.domain()+"/"+a.Label).Run()
	if err := os.Remove(a.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if a.Installed() {
		return errors.New("verification failed: plist still present")
	}
	return nil
}

// RemoveOllamaAgent removes aituner's Ollama settings LaunchAgent, if it is installed (for when Ollama is removed).
func RemoveOllamaAgent(ctx context.Context) error {
	if a := DefaultAgent(); a.Installed() {
		return a.Remove(ctx)
	}
	return nil
}
