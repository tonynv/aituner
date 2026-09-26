package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonynv/aituner/internal/connect"
)

// fake is a connect.Runner that records commands. running says whether pgrep finds Ollama.
type fake struct {
	have    map[string]string
	cmds    []string
	running bool
	brewHas map[string]bool // "cask ollama", "formula ollama", "formula macmon"
}

func (f *fake) Look(name string) (string, bool) { p, ok := f.have[name]; return p, ok }
func (f *fake) Run(_ context.Context, _ connect.Emit, _ string, _ []string, name string, args ...string) error {
	line := filepath.Base(name) + " " + strings.Join(args, " ")
	f.cmds = append(f.cmds, line)
	switch {
	case filepath.Base(name) == "pgrep":
		if f.running {
			return nil
		}
		return errors.New("no process")
	case filepath.Base(name) == "brew" && len(args) == 3 && args[0] == "list":
		if f.brewHas[strings.TrimPrefix(args[1], "--")+" "+args[2]] {
			return nil
		}
		return errors.New("not installed")
	}
	return nil
}

func TestFindOllamaKinds(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".ollama", "models"), 0o755)
	os.WriteFile(filepath.Join(home, ".ollama", "models", "blob"), make([]byte, 2000), 0o644)
	f := &fake{have: map[string]string{"brew": "/opt/homebrew/bin/brew", "ollama": "/opt/homebrew/bin/ollama"}, brewHas: map[string]bool{"formula ollama": true}}
	o := FindOllama(context.Background(), f, home, filepath.Join(home, "no-link"))
	if o.Kind != "brew-formula" || o.Path != "/opt/homebrew/bin/ollama" || o.ModelsGB <= 0 || o.CLILink {
		t.Fatalf("%+v", o)
	}
	f.brewHas = map[string]bool{"cask ollama": true}
	if o := FindOllama(context.Background(), f, home, ""); o.Kind != "brew-cask" {
		t.Fatalf("%+v", o)
	}
}

func TestRemoveOllamaAppGoesToTrashWithModels(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "Applications", "Ollama.app")
	os.MkdirAll(filepath.Join(app, "Contents"), 0o755)
	models := filepath.Join(dir, "home", ".ollama")
	os.MkdirAll(models, 0o755)
	trash := filepath.Join(dir, "home", ".Trash")
	f := &fake{have: map[string]string{}}
	o := Ollama{Kind: "app", Path: app, Models: models}
	if err := RemoveOllama(context.Background(), f, o, true, trash, func(string) {}); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{app, models} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s still there", gone)
		}
	}
	for _, in := range []string{"Ollama.app", ".ollama"} {
		if _, err := os.Stat(filepath.Join(trash, in)); err != nil {
			t.Fatalf("%s not in the Trash: %v", in, err)
		}
	}
	if !strings.Contains(strings.Join(f.cmds, "\n"), "killall -TERM Ollama") {
		t.Fatalf("Ollama was not stopped first: %v", f.cmds)
	}
	// a second copy with the same name gets a new name in the Trash instead of replacing the first
	os.MkdirAll(filepath.Join(app, "Contents"), 0o755)
	if err := RemoveOllama(context.Background(), f, o, false, trash, func(string) {}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(trash)
	if len(entries) != 3 {
		t.Fatalf("trash: %v", entries)
	}
	if err := RemoveOllama(context.Background(), f, Ollama{Kind: "app", Path: filepath.Join(dir, "Other.app")}, false, trash, func(string) {}); err == nil {
		t.Fatal("moved something that is not Ollama.app")
	}
}

func TestRemoveOllamaBrewAndMacmonAndMLX(t *testing.T) {
	f := &fake{have: map[string]string{"brew": "/opt/homebrew/bin/brew"}}
	if err := RemoveOllama(context.Background(), f, Ollama{Kind: "brew-cask"}, false, t.TempDir(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMacmon(context.Background(), f, func(string) {}); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(f.cmds, "\n")
	if !strings.Contains(all, "brew uninstall --cask ollama") || !strings.Contains(all, "brew uninstall macmon") {
		t.Fatalf("%s", all)
	}
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "venv", "bin"), 0o755)
	if dst, err := RemoveMLX(data, filepath.Join(t.TempDir(), ".Trash")); err != nil || filepath.Base(dst) != "venv" {
		t.Fatalf("%s %v", dst, err)
	}
	if _, err := RemoveMLX(data, t.TempDir()); err == nil {
		t.Fatal("removed an environment that is not there")
	}
}
