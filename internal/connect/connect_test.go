package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recorder is a Runner for orchestration tests: it records commands, reports which tools "exist", and performs the
// filesystem side effects a real install would (so verification steps can run). Real installs are covered by the
// live tests (AITUNER_LIVE_INSTALL=1).
type recorder struct {
	have   map[string]string
	cmds   []string
	env    Env
	failOn string
	// vimrcErrors makes Vim exit non-zero when it loads the user-sourcing profile, as a real vimrc with an error does
	vimrcErrors bool
}

func (r *recorder) Look(name string) (string, bool) {
	p, ok := r.have[name]
	return p, ok
}

func (r *recorder) Run(ctx context.Context, emit Emit, dir string, env []string, name string, args ...string) error {
	line := filepath.Base(name) + " " + strings.Join(args, " ")
	r.cmds = append(r.cmds, line)
	if r.failOn != "" && strings.Contains(line, r.failOn) {
		return errors.New("simulated failure")
	}
	base := filepath.Base(name)
	switch {
	case base == "code" && contains(args, "--install-extension"):
		id := args[len(args)-2]
		if !contains(args, "--force") {
			id = args[len(args)-1]
		}
		os.MkdirAll(filepath.Join(vsExtDir(r.env), strings.ToLower(id)+"-1.0.0"), 0o755)
	case base == "git" && len(args) > 0 && args[0] == "clone":
		dest := args[len(args)-1]
		os.MkdirAll(filepath.Join(dest, "plugin"), 0o755)
		os.MkdirAll(filepath.Join(dest, ".git"), 0o755)
		os.WriteFile(filepath.Join(dest, "plugin", "vim-ai.vim"), []byte(`" plugin`), 0o644)
	case base == "vim" && contains(args, "-es"):
		defer func() {}()
		for i, a := range args {
			if a == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "redir!") {
				out := strings.Fields(args[i+1])[2]
				os.WriteFile(out, []byte("2 1\n"), 0o644)
			}
		}
		if r.vimrcErrors && len(args) > 1 && args[1] == vimRC(r.env) {
			return errors.New("vim failed: exit status 1") // the user's own vimrc has an error; the aituner part still loaded
		}
	case base == "nvim" && contains(args, "--headless") && len(args) > 2 && strings.HasPrefix(args[1], "+Lazy"):
		os.MkdirAll(filepath.Join(nvimData(r.env), "lazy", "codecompanion.nvim"), 0o755)
	case base == "nvim" && contains(args, "-c"):
		emit("VERIFY_OK adapter=aituner url=" + r.env.RootURL + " model=" + r.env.Model + " key=resolved")
	}
	return nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func testEnv(t *testing.T, have map[string]string) (Env, *recorder) {
	t.Helper()
	home := t.TempDir()
	env := Env{
		Home: home, ConfigDir: filepath.Join(home, ".config", "aituner"), BinDir: filepath.Join(home, ".local", "bin"),
		DataDir: filepath.Join(home, "data"), KeyFile: filepath.Join(home, "data", "gateway.key"), Key: "aituner-testkey-0123456789abcdef",
		Port: 8747, RootURL: "http://127.0.0.1:8747", BaseURL: "http://127.0.0.1:8747/v1", Model: "mlx-community/Test-4bit", ContextTokens: 32768,
		VenvBin: filepath.Join(home, "venv", "bin"), LogFile: filepath.Join(home, "data", "model-server.log"),
	}
	os.MkdirAll(env.DataDir, 0o700)
	os.WriteFile(env.KeyFile, []byte(env.Key+"\n"), 0o600)
	r := &recorder{have: have, env: env}
	env.Run = r
	return env, r
}

var allTools = map[string]string{"claude": "/x/claude", "code": "/x/code", "nvim": "/x/nvim", "tmux": "/x/tmux", "git": "/usr/bin/git", "brew": "/x/brew", "bash": "/bin/bash", "open": "/usr/bin/open"}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.Type()&fs.ModeSymlink != 0 {
			tgt, _ := os.Readlink(p)
			out[rel] = "symlink->" + tgt
		} else if d.IsDir() {
			out[rel] = "dir"
		} else {
			b, _ := os.ReadFile(p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

// The user's own dotfiles and settings must be byte-for-byte unchanged by every setup, and new files may only appear in
// aituner's own locations.
func TestSetupNeverTouchesTheUsersOwnFilesAndStaysInsideItsOwnDirectories(t *testing.T) {
	env, rec := testEnv(t, allTools)
	home := env.Home
	// a home directory like the reference machine's: dotfiles that are symlinks into a git-tracked repo
	dot := filepath.Join(home, "github", "dotfiles")
	os.MkdirAll(dot, 0o755)
	os.WriteFile(filepath.Join(dot, "vimrc"), []byte("set number\n\" my vimrc\n"), 0o644)
	os.WriteFile(filepath.Join(dot, "tmux.conf"), []byte("set -g prefix C-a\n"), 0o644)
	os.Symlink(filepath.Join(dot, "vimrc"), filepath.Join(home, ".vimrc"))
	os.Symlink(filepath.Join(dot, "tmux.conf"), filepath.Join(home, ".tmux.conf"))
	os.MkdirAll(filepath.Join(home, ".config", "nvim"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "nvim", "init.lua"), []byte("-- my nvim config\n"), 0o644)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"mine"}`), 0o644)
	os.MkdirAll(filepath.Join(home, ".continue"), 0o755)
	os.WriteFile(filepath.Join(home, ".continue", "config.yaml"), []byte("name: mine\n"), 0o600)
	os.MkdirAll(filepath.Join(home, "Library", "Application Support", "Code", "User"), 0o755)
	os.WriteFile(filepath.Join(home, "Library", "Application Support", "Code", "User", "settings.json"), []byte(`{"editor.fontSize":13}`), 0o644)
	os.MkdirAll("/tmp", 0o755)
	brewVimPath := "" // brewVim looks at fixed paths; make the test independent of the machine's Homebrew
	_ = brewVimPath

	before := snapshot(t, home)
	for _, i := range All() {
		if i.ID() == "vim-tmux" {
			continue // needs Homebrew's Vim at a fixed path; covered separately below
		}
		if err := i.Setup(context.Background(), env, func(string) {}); err != nil {
			t.Fatalf("%s: %v", i.ID(), err)
		}
	}
	after := snapshot(t, home)
	for path, content := range before {
		if after[path] != content {
			t.Errorf("the user's file %s was modified", path)
		}
	}
	allowed := []string{".config/aituner", ".config/aituner-nvim", ".local/bin/aituner-", ".local/share/aituner-nvim"}
	for path := range after {
		if _, existed := before[path]; existed {
			continue
		}
		ok := false
		for _, a := range allowed {
			if strings.HasPrefix(path, a) {
				ok = true
			}
		}
		// parent directories of allowed locations (.config, .local, .local/bin) are fine
		if path == ".config" || path == ".local" || path == ".local/bin" || path == ".local/share" {
			ok = true
		}
		if !ok {
			t.Errorf("setup created %s outside aituner's own directories", path)
		}
	}
	_ = rec
}

func TestSetupRefusesToOverwriteAForeignFileAndRemoveLeavesItAlone(t *testing.T) {
	env, _ := testEnv(t, allTools)
	os.MkdirAll(env.BinDir, 0o755)
	mine := launcherPath(env, "aituner-claude")
	os.WriteFile(mine, []byte("#!/bin/sh\necho my own script\n"), 0o755)
	err := claudeCode{}.Setup(context.Background(), env, func(string) {})
	if !errors.Is(err, ErrForeignFile) {
		t.Fatalf("want ErrForeignFile, got %v", err)
	}
	if b, _ := os.ReadFile(mine); !strings.Contains(string(b), "my own script") {
		t.Fatal("the user's script was overwritten")
	}
	if err := (claudeCode{}).Remove(context.Background(), env, func(string) {}); !errors.Is(err, ErrForeignFile) {
		t.Fatalf("remove must refuse a foreign file: %v", err)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatal("the user's script was deleted")
	}
}

func TestSetupIsIdempotentAndRemoveRestoresTheState(t *testing.T) {
	env, _ := testEnv(t, allTools)
	ctx, emit := context.Background(), func(string) {}
	for _, i := range []Integration{claudeCode{}, vscode{}, neovim{}, openaiGeneric{}} {
		if err := i.Setup(ctx, env, emit); err != nil {
			t.Fatalf("%s: %v", i.ID(), err)
		}
		first := snapshot(t, env.Home)
		if err := i.Setup(ctx, env, emit); err != nil {
			t.Fatalf("%s again: %v", i.ID(), err)
		}
		if second := snapshot(t, env.Home); fmt.Sprint(first) != fmt.Sprint(second) {
			t.Errorf("%s: running Setup twice changed files", i.ID())
		}
		if st := i.Status(ctx, env); !st.Configured {
			t.Errorf("%s: not configured after Setup: %+v", i.ID(), st)
		}
		if err := i.Remove(ctx, env, emit); err != nil {
			t.Fatalf("%s remove: %v", i.ID(), err)
		}
		if st := i.Status(ctx, env); st.Configured {
			t.Errorf("%s: still configured after Remove", i.ID())
		}
	}
	// nothing of ours is left in the launcher directory
	if ents, _ := os.ReadDir(env.BinDir); len(ents) != 0 {
		t.Errorf("launchers left behind: %v", ents)
	}
}

func TestPlanNamesInstallCommandsOnlyForWhatIsMissing(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"brew": "/x/brew"}) // nothing installed
	cmds := func(i Integration) string { return strings.Join(i.Plan(env).Commands, "; ") }
	if c := cmds(claudeCode{}); !strings.Contains(c, "brew install --cask claude-code") {
		t.Errorf("claude: %s", c)
	}
	if c := cmds(vscode{}); !strings.Contains(c, "brew install --cask visual-studio-code") || !strings.Contains(c, "--install-extension Continue.continue") || !strings.Contains(c, "--install-extension anthropic.claude-code") {
		t.Errorf("vscode: %s", c)
	}
	if c := cmds(neovim{}); !strings.Contains(c, "brew install neovim") || !strings.Contains(c, "Lazy! sync") {
		t.Errorf("neovim: %s", c)
	}
	env2, _ := testEnv(t, allTools)
	if c := cmds2(env2, claudeCode{}); c != "" {
		t.Errorf("claude is installed, no install command expected: %s", c)
	}
	for _, i := range All() {
		p := i.Plan(env)
		if p.Title == "" || p.Summary == "" || len(p.Steps) == 0 || len(p.WillNotTouch) == 0 {
			t.Errorf("%s: incomplete plan %+v", i.ID(), p)
		}
	}
	if c := (claudeCode{}).Plan(env).Caveat; !strings.Contains(c, "does not support") {
		t.Error("the Claude Code plan must state Anthropic's position")
	}
}

func cmds2(env Env, i Integration) string { return strings.Join(i.Plan(env).Commands, "; ") }

func TestFailedInstallStopsSetupWithoutLeavingLaunchers(t *testing.T) {
	env, rec := testEnv(t, map[string]string{"brew": "/x/brew", "bash": "/bin/bash"})
	rec.failOn = "install --cask claude-code"
	if err := (claudeCode{}).Setup(context.Background(), env, func(string) {}); err == nil {
		t.Fatal("a failed install must fail the setup")
	}
	if fileExists(launcherPath(env, "aituner-claude")) {
		t.Fatal("no launcher may be written when the install failed")
	}
	env3, _ := testEnv(t, map[string]string{}) // no brew, no tool
	if err := (neovim{}).Setup(context.Background(), env3, func(string) {}); err == nil || !strings.Contains(err.Error(), "Homebrew") {
		t.Fatalf("missing Homebrew must be explained: %v", err)
	}
}

// ---- generated scripts really work --------------------------------------------------------------------------------

// awkward values: spaces, quotes and shell metacharacters in every path and name
func nastyEnv(t *testing.T) Env {
	env, _ := testEnv(t, allTools)
	env.Home = filepath.Join(env.Home, `o'brien & co $HOME`)
	env.ConfigDir = filepath.Join(env.Home, ".config", "aituner")
	env.BinDir = filepath.Join(env.Home, ".local", "bin")
	env.DataDir = filepath.Join(env.Home, "Application Support", "aituner")
	os.MkdirAll(env.DataDir, 0o700)
	env.KeyFile = filepath.Join(env.DataDir, "gateway.key")
	os.WriteFile(env.KeyFile, []byte(env.Key+"\n"), 0o600)
	env.LogFile = filepath.Join(env.DataDir, "model-server.log")
	env.VenvBin = filepath.Join(env.Home, "venv", "bin")
	env.Model = "mlx-community/Model's-4bit"
	return env
}

func TestEveryGeneratedScriptIsValidShellEvenWithNastyPaths(t *testing.T) {
	env := nastyEnv(t)
	scripts := map[string]string{
		"claude": claudeLauncher(env), "code": vsLauncherScript(env), "nvim": nvimLauncherScript(env), "vim": vimLauncherScript(env, "/opt/homebrew/bin/vim"),
		"tmux": tmuxLauncherScript(env, "/opt/homebrew/bin/vim"), "chat": chatLauncherScript(env),
	}
	for name, s := range scripts {
		if !strings.Contains(s, Marker) {
			t.Errorf("%s lacks the managed marker", name)
		}
		p := filepath.Join(t.TempDir(), name+".sh")
		os.WriteFile(p, []byte(s), 0o755)
		if out, err := exec.Command("/bin/bash", "-n", p).CombinedOutput(); err != nil {
			t.Errorf("%s: bash -n: %v\n%s", name, err, out)
		}
	}
	if shq("a'b") != `'a'\''b'` {
		t.Fatalf("shq: %s", shq("a'b"))
	}
	// the other file formats must be valid too
	var settings map[string]any
	if err := json.Unmarshal([]byte(vsSettingsJSON(env)), &settings); err != nil {
		t.Errorf("settings json: %v", err)
	}
	if !strings.Contains(vsContinueYAML(env), `"mlx-community/Model's-4bit"`) {
		t.Errorf("yaml must quote the model id: %s", vsContinueYAML(env))
	}
	if !strings.Contains(vimrcContent(env), `'mlx-community/Model''s-4bit'`) {
		t.Errorf("vim string quoting: %s", vimrcContent(env))
	}
	if !strings.Contains(nvimInitLua(env), `"mlx-community/Model's-4bit"`) {
		t.Errorf("lua string quoting")
	}
}

func TestChatScriptIsValidPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	p := filepath.Join(t.TempDir(), "chat.py")
	os.WriteFile(p, []byte(chatScript), 0o644)
	if out, err := exec.Command(py, "-m", "py_compile", p).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Runs the REAL launcher in a real bash against a live (stand-in) gateway, with a stand-in `claude` that reports its
// environment: proves the launcher reads the key file, the model and the context window and exports the right variables.
func TestClaudeLauncherExportsTheRightEnvironment(t *testing.T) {
	env := nastyEnv(t)
	py := ""
	for _, c := range []string{"/opt/homebrew/bin/python3", "/usr/local/bin/python3"} { // not /usr/bin/python3: that is an Xcode shim
		if _, err := os.Stat(c); err == nil {
			py = c
		}
	}
	if py == "" {
		t.Skip("no Homebrew python3")
	}
	os.MkdirAll(env.VenvBin, 0o755)
	os.Symlink(py, filepath.Join(env.VenvBin, "python"))
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			w.Write([]byte(`{"status":"ok","model_running":true}`))
		case r.URL.Path == "/v1/models" && r.Header.Get("x-api-key") == env.Key:
			w.Write([]byte(`{"data":[{"id":"the-model","context_window":65536}]}`))
		default:
			w.WriteHeader(401)
		}
	}))
	defer gw.Close()
	env.RootURL = gw.URL
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nenv | grep -E '^(ANTHROPIC|CLAUDE)' | sort\necho \"ARGS:$*\"\n"), 0o755)
	launcher := filepath.Join(t.TempDir(), "aituner-claude")
	os.WriteFile(launcher, []byte(claudeLauncher(env)), 0o755)
	cmd := exec.Command("/bin/bash", launcher, "--flag", "value")
	cmd.Env = []string{"HOME=" + env.Home, "PATH=" + bin + ":/usr/bin:/bin", // the stand-in claude comes first; the launcher must not reorder it
		"CLAUDECODE=1", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CODE_SESSION_ID=abc", "CLAUDE_CODE_MESSAGING_TOKEN=secret", "CLAUDE_CODE_BRIDGE_SESSION_ID=x"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s := string(out)
	for _, want := range []string{"ANTHROPIC_BASE_URL=" + gw.URL, "ANTHROPIC_AUTH_TOKEN=" + env.Key, "ANTHROPIC_MODEL=the-model", "ANTHROPIC_DEFAULT_HAIKU_MODEL=the-model",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS=65536", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ARGS:--flag value"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	for _, leaked := range []string{"CLAUDECODE=", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_BRIDGE_SESSION_ID"} {
		if strings.Contains(s, leaked) {
			t.Errorf("inherited session variable %s reached claude:\n%s", leaked, s)
		}
	}
	// no model running: a clear message, not a broken launch
	gw2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"model_running":false}`)) }))
	defer gw2.Close()
	env.RootURL = gw2.URL
	os.WriteFile(launcher, []byte(claudeLauncher(env)), 0o755)
	cmd = exec.Command("/bin/bash", launcher)
	cmd.Env = []string{"HOME=" + env.Home, "PATH=" + bin + ":/usr/bin:/bin"}
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "no model is being served") {
		t.Fatalf("expected a clear refusal, got err=%v out=%s", err, out)
	}
}

func TestValidateProject(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "code", "app"), 0o755)
	os.WriteFile(filepath.Join(home, "file.txt"), []byte("x"), 0o644)
	realHome, _ := filepath.EvalSymlinks(home)
	if got, err := ValidateProject(home, "~/code/app"); err != nil || got != filepath.Join(realHome, "code", "app") {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "relative/dir", "/etc", "/tmp", filepath.Join(home, "missing"), filepath.Join(home, "file.txt"), "~/code/../../etc", "/etc/passwd\n"} {
		if got, err := ValidateProject(home, bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestProbeExplainsEachFailure(t *testing.T) {
	env, _ := testEnv(t, allTools)
	env.RootURL = "http://127.0.0.1:1" // nothing listens
	if _, err := Probe(context.Background(), env, false); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("%v", err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"model_running":false}`)) }))
	defer down.Close()
	env.RootURL = down.URL
	if _, err := Probe(context.Background(), env, false); err == nil || !strings.Contains(err.Error(), "no model is running") {
		t.Fatalf("%v", err)
	}
	badKey := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"model_running":true}`))
			return
		}
		w.WriteHeader(401)
	}))
	defer badKey.Close()
	env.RootURL = badKey.URL
	if _, err := Probe(context.Background(), env, false); err == nil || !strings.Contains(err.Error(), "rejected the key") {
		t.Fatalf("%v", err)
	}
}

func TestVimTmuxSetupWritesAnIsolatedProfileThatSourcesTheUsersVimrcReadOnly(t *testing.T) {
	env, rec := testEnv(t, allTools)
	fake := filepath.Join(t.TempDir(), "vim")
	os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755)
	env.VimPaths = []string{fake}
	os.WriteFile(filepath.Join(env.Home, ".vimrc"), []byte("set number\n"), 0o644)
	before := snapshot(t, env.Home)
	if err := (vimTmux{}).Setup(context.Background(), env, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(env.Home, ".vimrc")); string(b) != "set number\n" {
		t.Fatal("the user's ~/.vimrc was modified")
	}
	rc, _ := os.ReadFile(vimRC(env))
	for _, want := range []string{Marker, "source ~/.vimrc", "packloadall", env.BaseURL + "/chat/completions", env.KeyFile, "vim_ai_chat", "vim_ai_edit", "vim_ai_complete"} {
		if !strings.Contains(string(rc), want) {
			t.Errorf("vimrc lacks %q", want)
		}
	}
	tm, _ := os.ReadFile(tmuxLauncher(env))
	for _, want := range []string{"pane_id", "split-window", "model_running", "switch-client", env.LogFile} {
		if !strings.Contains(string(tm), want) {
			t.Errorf("tmux launcher lacks %q", want)
		}
	}
	if strings.Contains(string(tm), ".0") && strings.Contains(string(tm), "-t \"$SESSION\":code.") {
		t.Error("tmux targets must be pane ids, not indexes (pane-base-index varies per user)")
	}
	joined := strings.Join(rec.cmds, "\n")
	if !strings.Contains(joined, "git clone --depth 1 https://github.com/madox2/vim-ai.git") || strings.Contains(joined, "brew install vim") {
		t.Errorf("commands:\n%s", joined)
	}
	for path := range snapshot(t, env.Home) {
		if _, existed := before[path]; !existed && !strings.HasPrefix(path, ".config") && !strings.HasPrefix(path, ".local") {
			t.Errorf("created %s outside aituner's directories", path)
		}
	}
	// running Setup again updates the plugin instead of failing on the existing clone
	if err := (vimTmux{}).Setup(context.Background(), env, func(string) {}); err != nil {
		t.Fatalf("second setup: %v", err)
	}
	if !strings.Contains(strings.Join(rec.cmds, "\n"), "git pull --ff-only") {
		t.Error("an existing plugin clone must be updated, not re-cloned")
	}
	if err := (vimTmux{}).Remove(context.Background(), env, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if fileExists(vimRC(env)) || fileExists(tmuxLauncher(env)) {
		t.Fatal("remove left files behind")
	}
	if b, _ := os.ReadFile(filepath.Join(env.Home, ".vimrc")); string(b) != "set number\n" {
		t.Fatal("remove touched the user's vimrc")
	}
}

// Found on the reference machine: the user's own ~/.vimrc has an error (missing colour scheme), which makes silent Vim
// exit 1. That must be reported as a note, never fail aituner's setup, but a broken aituner part must still fail.
func TestVimSetupToleratesErrorsInTheUsersOwnVimrcButNotInItsOwnPart(t *testing.T) {
	env, rec := testEnv(t, allTools)
	fake := filepath.Join(t.TempDir(), "vim")
	os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755)
	env.VimPaths = []string{fake}
	rec.vimrcErrors = true
	var log []string
	if err := (vimTmux{}).Setup(context.Background(), env, func(m string) { log = append(log, m) }); err != nil {
		t.Fatalf("a problem in the user's vimrc must not fail the setup: %v", err)
	}
	if !strings.Contains(strings.Join(log, "\n"), "your own ~/.vimrc") {
		t.Fatalf("the user must be told about it:\n%s", strings.Join(log, "\n"))
	}
	// and a strict-part failure (no :AI command) still fails
	env2, _ := testEnv(t, allTools)
	env2.VimPaths = []string{fake}
	broken := &recorder{have: allTools, env: env2}
	env2.Run = brokenVim{broken}
	if err := (vimTmux{}).Setup(context.Background(), env2, func(string) {}); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("a missing :AI command must fail verification: %v", err)
	}
}

// brokenVim behaves like a Vim where the plugin did not load.
type brokenVim struct{ *recorder }

func (b brokenVim) Run(ctx context.Context, emit Emit, dir string, env []string, name string, args ...string) error {
	if filepath.Base(name) == "vim" && contains(args, "-es") {
		for i, a := range args {
			if a == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "redir!") {
				os.WriteFile(strings.Fields(args[i+1])[2], []byte("0 1\n"), 0o644)
			}
		}
		return nil
	}
	return b.recorder.Run(ctx, emit, dir, env, name, args...)
}

func TestAtomicWritesLeaveNoTempFilesAndNeverFollowAPlantedSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	os.WriteFile(victim, []byte("precious"), 0o600)
	target := filepath.Join(dir, "launcher")
	// an attacker who guesses a fixed "<path>.tmp" name and plants a symlink there must gain nothing
	os.Symlink(victim, target+".tmp")
	for i := 0; i < 3; i++ {
		if _, err := writeManaged(target, "#!/bin/sh\n# "+Marker+"\necho "+fmt.Sprint(i)+"\n", 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("a planted symlink redirected the write: %q", b)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") && e.Name() != "launcher.tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	sec := filepath.Join(dir, "secret")
	writeOwned(sec, "key", 0o600)
	if fi, _ := os.Stat(sec); fi.Mode().Perm() != 0o600 {
		t.Fatalf("secrets must be owner-only: %v", fi.Mode().Perm())
	}
}
