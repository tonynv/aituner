package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// vscode sets up an ISOLATED VS Code profile: its own user-data and extensions directories, so the user's normal VS Code
// (settings, extensions, sign-ins) is never touched. Two extensions are installed into it: Continue (chat, edit and apply
// with any OpenAI-compatible model) and the Claude Code extension.
type vscode struct{}

func (vscode) ID() string { return "vscode" }

var vscodeExtensions = []string{"Continue.continue", "anthropic.claude-code"} // both verified to install (marketplace ids)

func vsRoot(env Env) string        { return filepath.Join(env.ConfigDir, "vscode") }
func vsUserData(env Env) string    { return filepath.Join(vsRoot(env), "user-data") }
func vsExtDir(env Env) string      { return filepath.Join(vsRoot(env), "extensions") }
func vsContinue(env Env) string    { return filepath.Join(vsRoot(env), "continue") }
func vsSettings(env Env) string    { return filepath.Join(vsUserData(env), "User", "settings.json") }
func vsContinueCfg(env Env) string { return filepath.Join(vsContinue(env), "config.yaml") }
func vsLauncher(env Env) string    { return launcherPath(env, "aituner-code") }

// The Claude Code extension reads its environment from this setting (documented at code.claude.com: llm-gateway-connect).
func vsSettingsJSON(env Env) string {
	type kv struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	vars := []kv{
		{"ANTHROPIC_BASE_URL", env.RootURL}, {"ANTHROPIC_AUTH_TOKEN", env.Key}, {"ANTHROPIC_MODEL", env.Model},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", env.Model}, {"ANTHROPIC_DEFAULT_SONNET_MODEL", env.Model}, {"ANTHROPIC_DEFAULT_HAIKU_MODEL", env.Model},
		{"CLAUDE_CODE_SUBAGENT_MODEL", env.Model}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1"},
	}
	if env.ContextTokens > 0 {
		vars = append(vars, kv{"CLAUDE_CODE_MAX_CONTEXT_TOKENS", strconv.Itoa(env.ContextTokens)})
	}
	b, _ := json.MarshalIndent(map[string]any{"claudeCode.environmentVariables": vars}, "", "  ")
	return string(b) + "\n"
}

// Continue's config.yaml, in the same v1 schema as a hand-written config (name, version, schema, models).
func vsContinueYAML(env Env) string {
	var b strings.Builder
	b.WriteString("# " + Marker + ": Continue configuration for the local model (isolated CONTINUE_GLOBAL_DIR)\n")
	b.WriteString("name: aituner local\nversion: 1.0.0\nschema: v1\nmodels:\n")
	b.WriteString("  - name: " + yamlStr(env.Model) + "\n    provider: openai\n    model: " + yamlStr(env.Model) + "\n")
	b.WriteString("    apiBase: " + yamlStr(env.BaseURL) + "\n    apiKey: " + yamlStr(env.Key) + "\n")
	b.WriteString("    roles:\n      - chat\n      - edit\n      - apply\n")
	if env.ContextTokens > 0 {
		b.WriteString("    defaultCompletionOptions:\n      contextLength: " + strconv.Itoa(env.ContextTokens) + "\n")
	}
	return b.String()
}

// yamlStr quotes a scalar; JSON strings are valid YAML double-quoted scalars.
func yamlStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func vsLauncherScript(env Env) string {
	return `#!/usr/bin/env bash
# ` + Marker + `: vscode
# Opens an isolated VS Code (own settings and extensions) wired to the local model. Your normal VS Code is untouched.
set -euo pipefail
export PATH="$PATH:/opt/homebrew/bin:/usr/local/bin" # appended: never overrides the tools you already resolve
command -v code >/dev/null 2>&1 || { echo "aituner: the 'code' command was not found" >&2; exit 1; }
export CONTINUE_GLOBAL_DIR=` + shq(vsContinue(env)) + `
exec code --user-data-dir ` + shq(vsUserData(env)) + ` --extensions-dir ` + shq(vsExtDir(env)) + ` "${@:-$PWD}"
`
}

func (vscode) Plan(env Env) Plan {
	p := Plan{
		ID: "vscode", Title: "VS Code",
		Summary:      "An isolated VS Code window with Continue (chat, edit, apply) and the Claude Code extension, both using your local model.",
		Files:        []string{vsSettings(env), vsContinueCfg(env), vsLauncher(env)},
		WillNotTouch: []string{"your normal VS Code settings, extensions and sign-ins (this uses its own folders)", "~/.continue"},
	}
	if _, ok := env.Run.Look("code"); !ok {
		p.Steps = append(p.Steps, Step{Kind: "install", Title: "Install VS Code", Detail: "Homebrew cask visual-studio-code"})
		p.Commands = append(p.Commands, "brew install --cask visual-studio-code")
	} else {
		p.Steps = append(p.Steps, Step{Kind: "info", Title: "VS Code is already installed", Detail: "nothing to install"})
	}
	for _, id := range vscodeExtensions {
		p.Steps = append(p.Steps, Step{Kind: "install", Title: "Install the extension " + id, Detail: "into " + vsExtDir(env)})
		p.Commands = append(p.Commands, "code --user-data-dir "+vsUserData(env)+" --extensions-dir "+vsExtDir(env)+" --install-extension "+id)
	}
	p.Steps = append(p.Steps,
		Step{Kind: "write", Title: "Configure Continue and the Claude Code extension", Detail: "config.yaml under an isolated CONTINUE_GLOBAL_DIR, and claudeCode.environmentVariables in this profile's settings"},
		Step{Kind: "write", Title: "Create the launcher aituner-code"},
		Step{Kind: "info", Title: "Verify", Detail: "lists the installed extensions, parses both config files and probes the gateway"})
	p.Caveat = "Anthropic does not support routing Claude Code to non-Claude models; the Claude Code extension works with your local model but with the same limits as the terminal version. Continue works with any model."
	return p
}

func installedExtensions(env Env) map[string]bool {
	out := map[string]bool{}
	ents, _ := os.ReadDir(vsExtDir(env))
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		for _, id := range vscodeExtensions {
			if strings.HasPrefix(n, strings.ToLower(id)+"-") {
				out[id] = true
			}
		}
	}
	return out
}

func (vscode) Status(ctx context.Context, env Env) Status {
	_, has := env.Run.Look("code")
	exts := installedExtensions(env)
	st := Status{Installed: has, Files: []string{vsSettings(env), vsContinueCfg(env), vsLauncher(env)}, Launcher: vsLauncher(env)}
	st.Configured = isManaged(vsContinueCfg(env)) && isManaged(vsLauncher(env)) && fileExists(vsSettings(env)) && len(exts) == len(vscodeExtensions)
	return st
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (vscode) Setup(ctx context.Context, env Env, emit Emit) error {
	if err := ensureBrew(ctx, env, emit, "visual-studio-code", true, "code"); err != nil {
		return err
	}
	code, _ := env.Run.Look("code")
	for _, dir := range []string{vsUserData(env), vsExtDir(env), vsContinue(env), filepath.Dir(vsSettings(env))} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, id := range vscodeExtensions {
		emit("installing extension " + id)
		if err := env.Run.Run(ctx, emit, "", nil, code, "--user-data-dir", vsUserData(env), "--extensions-dir", vsExtDir(env), "--install-extension", id, "--force"); err != nil {
			return fmt.Errorf("could not install %s: %w", id, err)
		}
	}
	if _, err := writeOwned(vsSettings(env), vsSettingsJSON(env), 0o600); err != nil { // holds the key: owner-only
		return err
	}
	if _, err := writeManaged(vsContinueCfg(env), vsContinueYAML(env), 0o600); err != nil {
		return err
	}
	if _, err := writeManaged(vsLauncher(env), vsLauncherScript(env), 0o755); err != nil {
		return err
	}
	emit("wrote the profile settings, the Continue config and " + vsLauncher(env))

	// verify: extensions present, both config files parse, launcher is valid shell
	exts := installedExtensions(env)
	for _, id := range vscodeExtensions {
		if !exts[id] {
			return fmt.Errorf("verification failed: extension %s is not installed in the aituner profile", id)
		}
	}
	var probe map[string]any
	if raw, err := os.ReadFile(vsSettings(env)); err != nil || json.Unmarshal(raw, &probe) != nil {
		return fmt.Errorf("verification failed: the VS Code settings file is not valid JSON")
	}
	if py := filepath.Join(env.VenvBin, "python"); fileExists(py) {
		if err := env.Run.Run(ctx, emit, "", nil, py, "-c", "import sys,yaml; d=yaml.safe_load(open(sys.argv[1])); assert d['models'][0]['provider']=='openai' and d['models'][0]['apiBase']", vsContinueCfg(env)); err != nil {
			return fmt.Errorf("verification failed: the Continue config does not parse: %w", err)
		}
	}
	if bash, ok := env.Run.Look("bash"); ok {
		if err := env.Run.Run(ctx, emit, "", nil, bash, "-n", vsLauncher(env)); err != nil {
			return fmt.Errorf("verification failed: launcher syntax: %w", err)
		}
	}
	emit("Verified: both extensions are installed in the isolated profile and both configuration files are valid")
	if model, err := Probe(ctx, env, true); err != nil {
		emit("Note: end-to-end check skipped: " + err.Error())
	} else {
		emit("Verified: real requests through the gateway work for " + model)
	}
	return nil
}

func (vscode) Remove(ctx context.Context, env Env, emit Emit) error {
	if _, err := removeManaged(vsLauncher(env)); err != nil {
		return err
	}
	// the whole profile directory is aituner's own; it is safe to remove as one unit
	if isManaged(vsContinueCfg(env)) || !fileExists(vsContinueCfg(env)) {
		if err := os.RemoveAll(vsRoot(env)); err != nil {
			return err
		}
	}
	emit("removed the isolated VS Code profile and launcher (VS Code itself is left installed)")
	return nil
}

func (vscode) Launch(ctx context.Context, env Env, project string) (string, error) {
	if !isManaged(vsLauncher(env)) {
		return "", fmt.Errorf("set up VS Code first")
	}
	if err := env.Run.Run(ctx, func(string) {}, project, nil, vsLauncher(env), project); err != nil {
		return "", err
	}
	return shq(vsLauncher(env)) + " " + shq(project), nil
}
