package connect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// vimTmux sets up Vim with the vim-ai plugin and a tmux layout (Vim, a chat pane, the model server log). System Vim on
// macOS is built without Python, which vim-ai needs, so Homebrew's Vim is used through explicit paths (it does not replace
// /usr/bin/vim). The vimrc it creates sources the user's own ~/.vimrc first, read-only.
type vimTmux struct{}

func (vimTmux) ID() string { return "vim-tmux" }

func vimRoot(env Env) string { return filepath.Join(env.ConfigDir, "vim") }
func vimRC(env Env) string   { return filepath.Join(vimRoot(env), "vimrc") }
func vimPlugin(env Env) string {
	return filepath.Join(vimRoot(env), "pack", "aituner", "start", "vim-ai")
}
func vimLauncher(env Env) string  { return launcherPath(env, "aituner-vim") }
func tmuxLauncher(env Env) string { return launcherPath(env, "aituner-tmux") }
func chatLauncher(env Env) string { return launcherPath(env, "aituner-chat") }
func chatPy(env Env) string       { return filepath.Join(env.ConfigDir, "bin", "aituner_chat.py") }

// brewVim finds a Vim that has Python support (Homebrew's).
func brewVim(env Env) (string, bool) {
	paths := env.VimPaths
	if len(paths) == 0 {
		paths = []string{"/opt/homebrew/bin/vim", "/usr/local/bin/vim"}
	}
	for _, p := range paths {
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

const vimSourceUser = `if filereadable(expand('~/.vimrc'))
  source ~/.vimrc
endif
`

func vimrcContent(env Env) string { return vimrcWith(env, vimSourceUser) }

// vimrcWith builds the profile; verification uses a variant without the user's vimrc so it tests aituner's part strictly.
func vimrcWith(env Env, userPart string) string {
	return `" ` + Marker + `: Vim profile for the local model
" Your own ~/.vimrc is sourced first, read-only, so your normal setup applies. Then vim-ai is pointed at the local gateway.
set nocompatible
` + userPart + `set packpath^=` + vimRoot(env) + `
packloadall
let g:vim_ai_token_file_path = ` + vimStr(env.KeyFile) + `
let s:opts = {
\  'model': ` + vimStr(env.Model) + `,
\  'endpoint_url': ` + vimStr(env.BaseURL+"/chat/completions") + `,
\  'request_timeout': 300,
\}
let g:vim_ai_chat = {'options': s:opts}
let g:vim_ai_complete = {'options': s:opts}
let g:vim_ai_edit = {'options': s:opts}
`
}

func vimStr(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func vimLauncherScript(env Env, vim string) string {
	return `#!/usr/bin/env bash
# ` + Marker + `: vim
# Vim (with Python, for vim-ai) using the aituner profile. Your normal vim and ~/.vimrc are untouched.
exec ` + shq(vim) + ` -u ` + shq(vimRC(env)) + ` "$@"
`
}

func chatLauncherScript(env Env) string {
	return `#!/usr/bin/env bash
# ` + Marker + `: chat
export AITUNER_BASE=` + shq(env.BaseURL) + `
export AITUNER_KEY_FILE=` + shq(env.KeyFile) + `
export AITUNER_MODEL=` + shq(env.Model) + `
exec ` + shq(filepath.Join(env.VenvBin, "python")) + ` ` + shq(chatPy(env)) + ` "$@"
`
}

func tmuxLauncherScript(env Env, vim string) string {
	return `#!/usr/bin/env bash
# ` + Marker + `: tmux
# tmux session "aituner": Vim (left), a chat pane (top right) and the model server log (bottom right).
set -euo pipefail
export PATH="$PATH:/opt/homebrew/bin:/usr/local/bin" # appended: never overrides the tools you already resolve
command -v tmux >/dev/null 2>&1 || { echo "aituner: tmux was not found (brew install tmux)" >&2; exit 1; }
BASE=` + shq(env.RootURL) + `
curl -fsS -m 3 "$BASE/health" 2>/dev/null | grep -q '"model_running":true' || { echo "aituner: no model is being served. Start one in the aituner Run tab." >&2; exit 1; }
PROJECT="${1:-$PWD}"
SESSION=aituner
attach() { if [ -n "${TMUX:-}" ]; then tmux switch-client -t "$SESSION"; else tmux attach-session -t "$SESSION"; fi; }
if tmux has-session -t "$SESSION" 2>/dev/null; then attach; exit 0; fi
# pane ids, not indexes: users often change base-index / pane-base-index in their tmux config
P_VIM=$(tmux new-session -d -s "$SESSION" -n code -c "$PROJECT" -P -F '#{pane_id}' ` + shq(vimLauncher(env)) + `)
P_CHAT=$(tmux split-window -h -l 38% -t "$P_VIM" -c "$PROJECT" -P -F '#{pane_id}' ` + shq(chatLauncher(env)) + `)
tmux split-window -v -l 30% -t "$P_CHAT" -c "$PROJECT" ` + shq("tail -n 40 -F "+env.LogFile) + `
tmux select-pane -t "$P_VIM"
attach
`
}

func (vimTmux) Plan(env Env) Plan {
	p := Plan{
		ID: "vim-tmux", Title: "Vim + tmux",
		Summary:      "Vim with the vim-ai plugin (chat, edit and complete with your local model) inside a tmux layout with a chat pane and the model log.",
		Files:        []string{vimRC(env), vimPlugin(env), vimLauncher(env), tmuxLauncher(env), chatLauncher(env)},
		WillNotTouch: []string{"~/.vimrc and ~/.vim (only read: your vimrc is sourced first)", "~/.tmux.conf", "the system /usr/bin/vim"},
	}
	if _, ok := brewVim(env); !ok {
		p.Steps = append(p.Steps, Step{Kind: "install", Title: "Install Vim with Python support", Detail: "Homebrew formula vim (macOS's own Vim has no Python, which vim-ai needs). It does not replace /usr/bin/vim."})
		p.Commands = append(p.Commands, "brew install vim")
	} else {
		p.Steps = append(p.Steps, Step{Kind: "info", Title: "Homebrew Vim is already installed", Detail: "nothing to install"})
	}
	if _, ok := env.Run.Look("tmux"); !ok {
		p.Steps = append(p.Steps, Step{Kind: "install", Title: "Install tmux", Detail: "Homebrew formula tmux"})
		p.Commands = append(p.Commands, "brew install tmux")
	} else {
		p.Steps = append(p.Steps, Step{Kind: "info", Title: "tmux is already installed", Detail: "nothing to install"})
	}
	p.Steps = append(p.Steps,
		Step{Kind: "install", Title: "Install vim-ai", Detail: "git clone https://github.com/madox2/vim-ai into " + vimPlugin(env)},
		Step{Kind: "write", Title: "Write the profile and launchers", Detail: "vimrc (sources yours first), aituner-vim, aituner-tmux and a small terminal chat client"},
		Step{Kind: "info", Title: "Verify", Detail: "starts Vim headless: checks Python support and that the :AI command exists"})
	p.Commands = append(p.Commands, "git clone --depth 1 https://github.com/madox2/vim-ai.git "+vimPlugin(env))
	return p
}

func (vimTmux) Status(ctx context.Context, env Env) Status {
	_, hasVim := brewVim(env)
	_, hasTmux := env.Run.Look("tmux")
	st := Status{Installed: hasVim && hasTmux, Files: []string{vimRC(env), vimLauncher(env), tmuxLauncher(env)}, Launcher: tmuxLauncher(env)}
	st.Configured = isManaged(vimRC(env)) && isManaged(tmuxLauncher(env)) && fileExists(filepath.Join(vimPlugin(env), "plugin", "vim-ai.vim"))
	return st
}

func (vimTmux) Setup(ctx context.Context, env Env, emit Emit) error {
	if _, ok := brewVim(env); !ok {
		brew, hasBrew := env.Run.Look("brew")
		if !hasBrew {
			return fmt.Errorf("Homebrew was not found: install it from https://brew.sh (or install a Vim with Python support yourself)")
		}
		emit("brew install vim")
		if err := env.Run.Run(ctx, emit, "", nil, brew, "install", "vim"); err != nil {
			return err
		}
	}
	vim, ok := brewVim(env)
	if !ok {
		return fmt.Errorf("Homebrew's Vim was installed but not found in /opt/homebrew/bin or /usr/local/bin")
	}
	if err := ensureBrew(ctx, env, emit, "tmux", false, "tmux"); err != nil {
		return err
	}
	git, ok := env.Run.Look("git")
	if !ok {
		return fmt.Errorf("git is required to install the vim-ai plugin")
	}
	if fileExists(filepath.Join(vimPlugin(env), ".git")) {
		emit("updating vim-ai")
		if err := env.Run.Run(ctx, emit, vimPlugin(env), nil, git, "pull", "--ff-only"); err != nil {
			return fmt.Errorf("could not update vim-ai: %w", err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(vimPlugin(env)), 0o755); err != nil {
			return err
		}
		emit("cloning vim-ai")
		if err := env.Run.Run(ctx, emit, "", nil, git, "clone", "--depth", "1", "https://github.com/madox2/vim-ai.git", vimPlugin(env)); err != nil {
			return fmt.Errorf("could not install vim-ai: %w", err)
		}
	}
	files := []struct {
		path, content string
		mode          os.FileMode
	}{
		{vimRC(env), vimrcContent(env), 0o600},
		{vimLauncher(env), vimLauncherScript(env, vim), 0o755},
		{tmuxLauncher(env), tmuxLauncherScript(env, vim), 0o755},
		{chatLauncher(env), chatLauncherScript(env), 0o755},
	}
	for _, f := range files {
		if _, err := writeManaged(f.path, f.content, f.mode); err != nil {
			return err
		}
	}
	if _, err := writeManaged(chatPy(env), chatScript, 0o755); err != nil {
		return err
	}
	emit("wrote the Vim profile, aituner-vim, aituner-tmux and aituner-chat")

	// verify aituner's part strictly (without the user's vimrc, whose own errors are not ours to fail on)
	out := filepath.Join(env.ConfigDir, "vim", ".verify.out")
	strict := filepath.Join(env.ConfigDir, "vim", ".verify.vimrc")
	defer os.Remove(out)
	defer os.Remove(strict)
	if err := os.WriteFile(strict, []byte(vimrcWith(env, "")), 0o600); err != nil {
		return err
	}
	check := func(rc string) (string, error) {
		os.Remove(out)
		script := "redir! > " + out + " | silent echo exists(':AI') . ' ' . has('python3') | redir END"
		err := env.Run.Run(ctx, emit, "", nil, vim, "-u", rc, "-es", "-c", script, "-c", "qa!")
		b, _ := os.ReadFile(out)
		return strings.TrimSpace(string(b)), err
	}
	got, err := check(strict)
	if err != nil {
		return fmt.Errorf("verification failed: Vim did not start with the aituner settings: %w", err)
	}
	if f := strings.Fields(got); len(f) != 2 || f[0] != "2" || f[1] != "1" {
		return fmt.Errorf("verification failed: expected the :AI command (2) and Python (1), got %q", got)
	}
	emit("Verified: Vim with vim-ai starts, Python is available and the :AI commands exist")
	// then with the real profile, which also loads the user's own vimrc: its problems are reported, not blamed on us
	if got, err := check(vimRC(env)); err != nil {
		if f := strings.Fields(got); len(f) == 2 && f[0] == "2" && f[1] == "1" {
			emit("Note: Vim reported an error while loading your own ~/.vimrc (for example a missing colour scheme or plugin). aituner's part loaded fine; the same message appears in your normal Vim.")
		} else {
			return fmt.Errorf("verification failed: the aituner profile does not load with your vimrc: %w", err)
		}
	} else {
		emit("Verified: it also loads together with your own ~/.vimrc")
	}
	if bash, ok := env.Run.Look("bash"); ok {
		for _, f := range []string{tmuxLauncher(env), vimLauncher(env), chatLauncher(env)} {
			if err := env.Run.Run(ctx, emit, "", nil, bash, "-n", f); err != nil {
				return fmt.Errorf("verification failed: %s has a syntax error: %w", f, err)
			}
		}
	}
	if model, err := Probe(ctx, env, true); err != nil {
		emit("Note: end-to-end check skipped: " + err.Error())
	} else {
		emit("Verified: real requests through the gateway work for " + model)
	}
	return nil
}

func (vimTmux) Remove(ctx context.Context, env Env, emit Emit) error {
	for _, f := range []string{vimLauncher(env), tmuxLauncher(env), chatLauncher(env)} {
		if _, err := removeManaged(f); err != nil {
			return err
		}
	}
	if isManaged(vimRC(env)) || !fileExists(vimRC(env)) {
		_ = os.RemoveAll(vimRoot(env))
	}
	_ = os.Remove(chatPy(env))
	emit("removed the aituner Vim profile, launchers and the vim-ai copy (Vim and tmux themselves are left installed)")
	return nil
}

func (vimTmux) Launch(ctx context.Context, env Env, project string) (string, error) {
	if !isManaged(tmuxLauncher(env)) {
		return "", fmt.Errorf("set up Vim + tmux first")
	}
	return openTerminal(ctx, env, "vim-tmux", tmuxLauncher(env), project)
}
