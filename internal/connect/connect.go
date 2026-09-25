// Package connect installs and configures editors and tools to use the model aituner serves. Every integration is
// additive and reversible: it writes only to directories aituner owns, never modifies the user's own dotfiles or
// settings, and refuses to overwrite any file it did not create (files carry a marker line).
package connect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Marker identifies files written by aituner. It is checked before any overwrite or removal.
const Marker = "aituner-managed"

// Emit receives progress lines for the UI's live log.
type Emit func(msg string)

// Runner runs external commands. Only tool code calls it, with fixed argv (never a shell string); the recording
// implementation used in unit tests makes orchestration checkable without installing anything.
type Runner interface {
	Run(ctx context.Context, emit Emit, dir string, env []string, name string, args ...string) error
	Look(name string) (string, bool)
}

// Env is everything an integration needs to know. It is filled from aituner's own state, never from user text.
type Env struct {
	Home          string
	ConfigDir     string // ~/.config/aituner
	BinDir        string // ~/.local/bin
	DataDir       string
	KeyFile       string // gateway.key
	Key           string
	Port          int
	RootURL       string // http://127.0.0.1:PORT      (Anthropic base)
	BaseURL       string // http://127.0.0.1:PORT/v1   (OpenAI base)
	Model         string // model id clients send (the repo id)
	ContextTokens int
	VenvBin       string
	LogFile       string
	Run           Runner
	VimPaths      []string // where to look for a Vim with Python support (default: Homebrew locations)
}

type Step struct {
	Kind   string `json:"kind"` // install | write | info
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

type Plan struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Steps        []Step   `json:"steps"`
	Commands     []string `json:"commands"` // exact external commands, shown before anything runs
	Files        []string `json:"files"`    // files aituner will create or update
	WillNotTouch []string `json:"will_not_touch"`
	Caveat       string   `json:"caveat,omitempty"`
}

type Status struct {
	Installed  bool     `json:"installed"`  // the tool itself is present
	Configured bool     `json:"configured"` // aituner's configuration for it is in place
	Version    string   `json:"version,omitempty"`
	Files      []string `json:"files"`
	Launcher   string   `json:"launcher,omitempty"` // command that starts it
}

type Integration interface {
	ID() string
	Plan(env Env) Plan
	Status(ctx context.Context, env Env) Status
	Setup(ctx context.Context, env Env, emit Emit) error
	Remove(ctx context.Context, env Env, emit Emit) error
	// Launch opens the tool for a project folder and returns the command line that does so.
	Launch(ctx context.Context, env Env, project string) (string, error)
}

// All returns every integration, in display order.
func All() []Integration {
	return []Integration{claudeCode{}, vscode{}, neovim{}, vimTmux{}, openaiGeneric{}}
}

func ByID(id string) Integration {
	for _, i := range All() {
		if i.ID() == id {
			return i
		}
	}
	return nil
}

// ---- files ----------------------------------------------------------------------------------------------------

var ErrForeignFile = errors.New("a file that aituner did not create is in the way")

func isManaged(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < 5 && sc.Scan(); i++ {
		if strings.Contains(sc.Text(), Marker) {
			return true
		}
	}
	return false
}

// writeManaged writes content atomically. It refuses to replace an existing file that lacks the marker, so a user's own
// script or config is never overwritten. It reports whether the content changed.
func writeManaged(path, content string, mode os.FileMode) (bool, error) {
	if !strings.Contains(content, Marker) {
		return false, fmt.Errorf("internal error: %s would be written without the %s marker", path, Marker)
	}
	if _, err := os.Lstat(path); err == nil {
		if !isManaged(path) {
			return false, fmt.Errorf("%w: %s (move it away, or remove it, and set up again)", ErrForeignFile, path)
		}
		if old, err := os.ReadFile(path); err == nil && string(old) == content {
			return false, nil
		}
	}
	return true, writeAtomic(path, []byte(content), mode)
}

// writeOwned writes a file inside a directory aituner exclusively owns (no marker needed, e.g. JSON settings).
func writeOwned(path, content string, mode os.FileMode) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return false, nil
	}
	return true, writeAtomic(path, []byte(content), mode)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeManaged deletes a file only if aituner created it.
func removeManaged(path string) (bool, error) {
	if _, err := os.Lstat(path); err != nil {
		return false, nil
	}
	if !isManaged(path) {
		return false, fmt.Errorf("%w: %s (left in place)", ErrForeignFile, path)
	}
	return true, os.Remove(path)
}

// shq single-quotes a string for a POSIX shell.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func launcherPath(env Env, name string) string { return filepath.Join(env.BinDir, name) }

// ---- gateway probes: real requests, used as verification ----------------------------------------------------------

type modelsResp struct {
	Data []struct {
		ID            string `json:"id"`
		ContextWindow int    `json:"context_window"`
	} `json:"data"`
}

// Probe sends real requests through the gateway with the key: the model list, then a tiny generation in OpenAI format
// and in Anthropic format. It returns the model id the gateway reports.
func Probe(ctx context.Context, env Env, generate bool) (string, error) {
	cl := &http.Client{Timeout: 90 * time.Second}
	get := func(path string) ([]byte, int, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, env.RootURL+path, nil)
		req.Header.Set("Authorization", "Bearer "+env.Key)
		resp, err := cl.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return b, resp.StatusCode, nil
	}
	post := func(path, hdr, body string) (int, string, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, env.RootURL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(hdr, map[string]string{"Authorization": "Bearer " + env.Key, "x-api-key": env.Key}[hdr])
		resp, err := cl.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, string(b), nil
	}
	h, code, err := get("/health")
	if err != nil {
		return "", fmt.Errorf("the gateway is not reachable at %s: start a model in aituner first", env.RootURL)
	}
	if code != 200 || !bytes.Contains(h, []byte(`"model_running":true`)) {
		return "", errors.New("the gateway is up but no model is running: start one in aituner first")
	}
	mb, code, err := get("/v1/models")
	if err != nil || code != 200 {
		return "", fmt.Errorf("the gateway rejected the key or failed (%d)", code)
	}
	var mr modelsResp
	if json.Unmarshal(mb, &mr) != nil || len(mr.Data) == 0 {
		return "", errors.New("the gateway returned no model")
	}
	model := mr.Data[0].ID
	if !generate {
		return model, nil
	}
	if code, body, err := post("/v1/chat/completions", "Authorization", `{"messages":[{"role":"user","content":"Reply with one word."}],"max_tokens":8}`); err != nil || code != 200 {
		return model, fmt.Errorf("OpenAI-format request failed (%d): %s", code, trim(body))
	}
	if code, body, err := post("/v1/messages", "x-api-key", `{"model":"x","max_tokens":8,"messages":[{"role":"user","content":"Reply with one word."}]}`); err != nil || code != 200 || !strings.Contains(body, `"type":"message"`) {
		return model, fmt.Errorf("Anthropic-format request failed (%d): %s", code, trim(body))
	}
	return model, nil
}

func trim(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// ---- running commands ---------------------------------------------------------------------------------------------

// ExecRunner runs real commands, streaming output to emit.
type ExecRunner struct{}

func (ExecRunner) Look(name string) (string, bool) {
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", filepath.Join(os.Getenv("HOME"), ".local", "bin")} {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, true
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return "", false
}

func (ExecRunner) Run(ctx context.Context, emit Emit, dir string, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_INSTALL_CLEANUP=1", "NONINTERACTIVE=1")
	cmd.Env = append(cmd.Env, env...)
	cmd.WaitDelay = 5 * time.Second
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	var tail []string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if l == "" {
			continue
		}
		emit(l)
		tail = append(tail, l)
		if len(tail) > 6 {
			tail = tail[1:]
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s failed: %w: %s", filepath.Base(name), err, strings.Join(tail, " | "))
	}
	return nil
}

// ensureBrew installs a Homebrew formula (or cask) unless the command it provides is already present.
func ensureBrew(ctx context.Context, env Env, emit Emit, formula string, cask bool, provides string) error {
	if p, ok := env.Run.Look(provides); ok {
		emit(fmt.Sprintf("%s is already installed (%s)", provides, p))
		return nil
	}
	brew, ok := env.Run.Look("brew")
	if !ok {
		return fmt.Errorf("%s is not installed and Homebrew was not found; install Homebrew from https://brew.sh or install %s yourself", provides, formula)
	}
	args := []string{"install"}
	if cask {
		args = append(args, "--cask")
	}
	args = append(args, formula)
	emit("brew " + strings.Join(args, " "))
	if err := env.Run.Run(ctx, emit, "", nil, brew, args...); err != nil {
		return err
	}
	if _, ok := env.Run.Look(provides); !ok {
		return fmt.Errorf("installed %s but %s is still not on this machine's PATH", formula, provides)
	}
	return nil
}

// ---- opening a terminal -------------------------------------------------------------------------------------------

// openTerminal runs a launcher in a new Terminal.app window for a project folder. It writes a small .command file and
// opens it with `open -a Terminal`, which needs no Automation permission (unlike AppleScript).
func openTerminal(ctx context.Context, env Env, name, launcher, project string) (string, error) {
	script := "#!/bin/bash\n# " + Marker + ": launch " + name + "\ncd " + shq(project) + " || exit 1\nexec " + shq(launcher) + "\n"
	path := filepath.Join(env.ConfigDir, "launch", name+".command")
	if err := writeAtomic(path, []byte(script), 0o700); err != nil {
		return "", err
	}
	open, _ := env.Run.Look("open")
	if open == "" {
		open = "/usr/bin/open"
	}
	if err := env.Run.Run(ctx, func(string) {}, "", nil, open, "-a", "Terminal", path); err != nil {
		return "", err
	}
	return "cd " + shq(project) + " && " + shq(launcher), nil
}

// ---- project folder -----------------------------------------------------------------------------------------------

// ValidateProject checks a folder the user wants to open: absolute, inside their home directory or on an external drive,
// an existing directory.
func ValidateProject(home, p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	if p == "" || !filepath.IsAbs(p) || strings.ContainsAny(p, "\x00\n\r") {
		return "", errors.New("enter an absolute project folder path")
	}
	p = filepath.Clean(p)
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", errors.New("that folder does not exist")
	}
	homeReal, _ := filepath.EvalSymlinks(home)
	if !strings.HasPrefix(real+"/", homeReal+"/") && !strings.HasPrefix(real, "/Volumes/") {
		return "", errors.New("the project must be inside your home directory or on an external drive")
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", errors.New("that path is not a folder")
	}
	return real, nil
}
