// Package tools inspects and removes the third-party tools aituner works with: Ollama (optional; aituner never installs
// it), macmon, and aituner's own MLX environment. Removal moves apps and data to the Trash where it can, so it can be
// undone from Finder.
package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tonynv/aituner/internal/connect"
)

// OllamaApp is where the Ollama download installs itself.
const OllamaApp = "/Applications/Ollama.app"

// Ollama describes how Ollama is installed here.
type Ollama struct {
	Kind     string  `json:"kind"` // app | brew-formula | brew-cask | "" (not installed)
	Path     string  `json:"path"`
	Models   string  `json:"models"` // ~/.ollama
	ModelsGB float64 `json:"models_gb"`
	CLILink  bool    `json:"cli_link"` // Ollama.app's /usr/local/bin/ollama link exists
}

func dirBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// BrewPrefixes are Homebrew's install prefixes (Apple Silicon, then Intel). Homebrew records a formula as
// <prefix>/Cellar/<name> and a cask as <prefix>/Caskroom/<name>: checking those is instant, unlike `brew list`.
var BrewPrefixes = []string{"/opt/homebrew", "/usr/local"}

func brewHas(kind, name string) bool {
	for _, p := range BrewPrefixes {
		if fi, err := os.Stat(filepath.Join(p, kind, name)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// FindOllama inspects the Ollama install. home is the user's home folder.
func FindOllama(ctx context.Context, r connect.Runner, home, cliLink string) Ollama {
	o := Ollama{Models: filepath.Join(home, ".ollama")}
	if fi, err := os.Stat(o.Models); err == nil && fi.IsDir() {
		o.ModelsGB = float64(dirBytes(o.Models)) / 1e9
	}
	if t, err := os.Readlink(cliLink); err == nil && strings.HasPrefix(t, OllamaApp+"/") {
		o.CLILink = true
	}
	switch {
	case brewHas("Caskroom", "ollama"):
		o.Kind, o.Path = "brew-cask", OllamaApp
		return o
	case brewHas("Cellar", "ollama"):
		o.Kind = "brew-formula"
		o.Path, _ = r.Look("ollama")
		return o
	}
	if fi, err := os.Stat(OllamaApp); err == nil && fi.IsDir() {
		o.Kind, o.Path = "app", OllamaApp
	}
	return o
}

// StopOllama stops Ollama: the menu bar app (which stops its server), a Homebrew service, or a bare `ollama serve`.
func StopOllama(ctx context.Context, r connect.Runner, o Ollama, emit connect.Emit) error {
	nop := func(string) {}
	switch o.Kind {
	case "brew-formula":
		if brew, ok := r.Look("brew"); ok {
			_ = r.Run(ctx, emit, "", nil, brew, "services", "stop", "ollama")
		}
	default:
		_ = r.Run(ctx, nop, "", nil, "/usr/bin/killall", "-TERM", "Ollama")
	}
	_ = r.Run(ctx, nop, "", nil, "/usr/bin/pkill", "-TERM", "-x", "ollama")
	for i := 0; i < 50; i++ { // up to 10 s for it to exit
		if r.Run(ctx, nop, "", nil, "/usr/bin/pgrep", "-x", "ollama") != nil && r.Run(ctx, nop, "", nil, "/usr/bin/pgrep", "-x", "Ollama") != nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("Ollama did not stop")
}

// ToTrash moves path into trash (the user's ~/.Trash), renaming it if the name is taken. It never deletes.
func ToTrash(path, trash string) (string, error) {
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return "", err
	}
	base := filepath.Base(path)
	dst := filepath.Join(trash, base)
	if _, err := os.Lstat(dst); err == nil {
		ext := filepath.Ext(base)
		dst = filepath.Join(trash, fmt.Sprintf("%s %s%s", strings.TrimSuffix(base, ext), time.Now().Format("15.04.05"), ext))
	}
	return dst, os.Rename(path, dst)
}

// RemoveOllama stops Ollama and removes it: Homebrew uninstalls Homebrew installs; the downloaded app is moved to the
// Trash. With models, ~/.ollama goes to the Trash too. The root-owned CLI link is left to the caller (admin prompt).
func RemoveOllama(ctx context.Context, r connect.Runner, o Ollama, models bool, trash string, emit connect.Emit) error {
	if err := StopOllama(ctx, r, o, emit); err != nil {
		emit("warning: " + err.Error())
	}
	switch o.Kind {
	case "brew-cask", "brew-formula":
		brew, ok := r.Look("brew")
		if !ok {
			return fmt.Errorf("Homebrew was not found")
		}
		args := []string{"uninstall"}
		if o.Kind == "brew-cask" {
			args = append(args, "--cask")
		}
		if err := r.Run(ctx, emit, "", nil, brew, append(args, "ollama")...); err != nil {
			return err
		}
	case "app":
		if filepath.Base(o.Path) != "Ollama.app" {
			return fmt.Errorf("refusing to move %q: not Ollama.app", o.Path)
		}
		dst, err := ToTrash(o.Path, trash)
		if err != nil {
			return fmt.Errorf("move Ollama.app to the Trash: %w", err)
		}
		emit("moved Ollama.app to the Trash (" + dst + ")")
	default:
		return fmt.Errorf("Ollama is not installed")
	}
	if models {
		if fi, err := os.Stat(o.Models); err == nil && fi.IsDir() {
			dst, err := ToTrash(o.Models, trash)
			if err != nil {
				return fmt.Errorf("move %s to the Trash: %w", o.Models, err)
			}
			emit("moved Ollama's models to the Trash (" + dst + ")")
		}
	}
	return nil
}

// MacmonFromBrew reports whether macmon was installed with Homebrew (the only way aituner installs it).
func MacmonFromBrew() bool { return brewHas("Cellar", "macmon") }

// RemoveMacmon uninstalls macmon with Homebrew.
func RemoveMacmon(ctx context.Context, r connect.Runner, emit connect.Emit) error {
	brew, ok := r.Look("brew")
	if !ok {
		return fmt.Errorf("Homebrew was not found")
	}
	return r.Run(ctx, emit, "", nil, brew, "uninstall", "macmon")
}

// RemoveMLX moves aituner's private MLX environment (<data>/venv) to the Trash; Bootstrap reinstalls it.
func RemoveMLX(dataDir, trash string) (string, error) {
	venv := filepath.Join(dataDir, "venv")
	fi, err := os.Lstat(venv)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("the MLX environment is not installed")
	}
	return ToTrash(venv, trash)
}
