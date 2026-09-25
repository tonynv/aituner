package connect

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

type claudeCode struct{}

func (claudeCode) ID() string { return "claude" }

func claudeLauncherPath(env Env) string { return launcherPath(env, "aituner-claude") }

// The launcher reads the gateway key and the model's real context window at run time, so it stays correct when a
// different model is served later. It sets environment variables for one process only: Claude Code's own settings and
// login (~/.claude) are never modified.
func claudeLauncher(env Env) string {
	return `#!/usr/bin/env bash
# ` + Marker + `: claude
# Runs Claude Code against the local model served by aituner. Your normal ` + "`claude`" + ` command, its login and
# ~/.claude/settings.json are not touched: everything here applies to this one process.
set -euo pipefail
# If this was started from inside another Claude Code session (aituner itself may have been), that session's markers and
# messaging credentials would be inherited: Claude Code then disables transcript saving, and the identity would leak into this one.
unset CLAUDECODE CLAUDE_PID CLAUDE_EFFORT CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_SESSION_ID CLAUDE_CODE_SESSION_ATTENDED \
  CLAUDE_CODE_BRIDGE_SESSION_ID CLAUDE_CODE_MESSAGING_SOCKET CLAUDE_CODE_MESSAGING_TOKEN CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_EXECPATH
export PATH="$PATH:/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin" # appended: never overrides the tools you already resolve
KEY_FILE=` + shq(env.KeyFile) + `
BASE=` + shq(env.RootURL) + `
VENV_PY=` + shq(filepath.Join(env.VenvBin, "python")) + `

fail() { echo "aituner: $1" >&2; exit 1; }
[ -r "$KEY_FILE" ] || fail "gateway key not found. Open aituner and start a model first."
command -v claude >/dev/null 2>&1 || fail "Claude Code is not installed (brew install --cask claude-code)."
KEY=$(tr -d '\n' < "$KEY_FILE")
curl -fsS -m 3 "$BASE/health" 2>/dev/null | grep -q '"model_running":true' || fail "no model is being served. Start one in the aituner Run tab."
INFO=$(curl -fsS -m 5 -H "x-api-key: $KEY" "$BASE/v1/models") || fail "the gateway rejected the key."
read -r MODEL CTX <<<"$("$VENV_PY" -c 'import json,sys; d=json.load(sys.stdin)["data"][0]; print(d["id"], d.get("context_window") or 0)' <<<"$INFO")"

export ANTHROPIC_BASE_URL="$BASE"
export ANTHROPIC_AUTH_TOKEN="$KEY"
export ANTHROPIC_MODEL="$MODEL"
export ANTHROPIC_DEFAULT_OPUS_MODEL="$MODEL" ANTHROPIC_DEFAULT_SONNET_MODEL="$MODEL" ANTHROPIC_DEFAULT_HAIKU_MODEL="$MODEL"
export CLAUDE_CODE_SUBAGENT_MODEL="$MODEL"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
# tell Claude Code the truth about this model's window (it cannot look it up for a local model)
if [ "${CTX:-0}" -gt 0 ]; then export CLAUDE_CODE_MAX_CONTEXT_TOKENS="$CTX"; fi
echo "aituner: Claude Code -> local model $MODEL (context ${CTX:-unknown} tokens)" >&2
# Claude Code's normal request is ~27,000 tokens (its tool list, skills, MCP servers, CLAUDE.md and hooks), and a local model
# reads prompts at a few hundred tokens/s, so every cold turn would take over a minute. --bare sends ~1,700 tokens (Bash, Read
# and Edit only; no hooks, skills, MCP, CLAUDE.md, auto-memory) and the attribution setting keeps commit/PR text free of
# co-author lines. Set AITUNER_CLAUDE_FULL=1 for the complete Claude Code experience (slow on a local model).
if [ "${AITUNER_CLAUDE_FULL:-0}" = 1 ]; then
  exec claude "$@"
fi
exec claude --bare --settings '{"attribution":{"commit":"","pr":""}}' "$@"
`
}

func (c claudeCode) Plan(env Env) Plan {
	p := Plan{
		ID: "claude", Title: "Claude Code",
		Summary:      "Run Claude Code against your local model through aituner's gateway.",
		Files:        []string{claudeLauncherPath(env)},
		WillNotTouch: []string{"~/.claude/settings.json", "your Claude login and subscription", "your normal `claude` command"},
		Caveat:       "Anthropic does not support routing Claude Code to non-Claude models. It works, but local models handle Claude Code's large prompts (about 15,000 tokens every turn) and tool use far less reliably than Claude does: expect slow first replies and occasional wrong tool calls. Larger coding models work best.",
	}
	if _, ok := env.Run.Look("claude"); !ok {
		p.Steps = append(p.Steps, Step{Kind: "install", Title: "Install Claude Code", Detail: "Homebrew cask claude-code (Anthropic's documented install method)"})
		p.Commands = append(p.Commands, "brew install --cask claude-code")
	} else {
		p.Steps = append(p.Steps, Step{Kind: "info", Title: "Claude Code is already installed", Detail: "nothing to install"})
	}
	p.Steps = append(p.Steps,
		Step{Kind: "write", Title: "Create the launcher aituner-claude", Detail: "sets ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, the model names and the model's real context window for one run"},
		Step{Kind: "info", Title: "Verify", Detail: "checks the launcher and, if a model is running, sends a real request in Anthropic format through the gateway"})
	return p
}

func (claudeCode) Status(ctx context.Context, env Env) Status {
	st := Status{Files: []string{claudeLauncherPath(env)}, Launcher: claudeLauncherPath(env)}
	if p, ok := env.Run.Look("claude"); ok {
		st.Installed = true
		if out, err := exec.CommandContext(ctx, p, "--version").Output(); err == nil {
			st.Version = string(out)
		}
	}
	st.Configured = isManaged(claudeLauncherPath(env))
	return st
}

func (claudeCode) Setup(ctx context.Context, env Env, emit Emit) error {
	if err := ensureBrew(ctx, env, emit, "claude-code", true, "claude"); err != nil {
		return err
	}
	path := claudeLauncherPath(env)
	changed, err := writeManaged(path, claudeLauncher(env), 0o755)
	if err != nil {
		return err
	}
	emit(map[bool]string{true: "wrote " + path, false: path + " is already up to date"}[changed])
	if bash, ok := env.Run.Look("bash"); ok {
		if err := env.Run.Run(ctx, emit, "", nil, bash, "-n", path); err != nil {
			return fmt.Errorf("the generated launcher has a syntax error: %w", err)
		}
	}
	if claude, ok := env.Run.Look("claude"); ok {
		if err := env.Run.Run(ctx, emit, "", nil, claude, "--version"); err != nil {
			return fmt.Errorf("claude is installed but does not run: %w", err)
		}
	}
	if model, err := Probe(ctx, env, true); err != nil {
		emit("Note: end-to-end check skipped: " + err.Error())
		emit("Everything is installed; start a model in the Run tab, then use " + path)
	} else {
		emit("Verified: OpenAI-format and Anthropic-format requests both work through the gateway for " + model)
	}
	return nil
}

func (claudeCode) Remove(ctx context.Context, env Env, emit Emit) error {
	ok, err := removeManaged(claudeLauncherPath(env))
	if err != nil {
		return err
	}
	if ok {
		emit("removed " + claudeLauncherPath(env) + " (Claude Code itself is left installed)")
	}
	return nil
}

func (claudeCode) Launch(ctx context.Context, env Env, project string) (string, error) {
	if !isManaged(claudeLauncherPath(env)) {
		return "", fmt.Errorf("set up Claude Code first")
	}
	return openTerminal(ctx, env, "claude", claudeLauncherPath(env), project)
}
