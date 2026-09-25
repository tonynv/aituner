package connect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// neovim sets up a self-contained profile launched with NVIM_APPNAME=aituner-nvim: its own config and plugin
// directories, so ~/.config/nvim is never read or written. It installs lazy.nvim and CodeCompanion (chat and inline
// editing against an OpenAI-compatible adapter), configured per the plugin's documented `openai_compatible` adapter.
type neovim struct{}

func (neovim) ID() string { return "neovim" }

const nvimAppName = "aituner-nvim"

func nvimConfigDir(env Env) string { return filepath.Join(env.Home, ".config", nvimAppName) }
func nvimInit(env Env) string      { return filepath.Join(nvimConfigDir(env), "init.lua") }
func nvimLauncher(env Env) string  { return launcherPath(env, "aituner-nvim") }
func nvimData(env Env) string      { return filepath.Join(env.Home, ".local", "share", nvimAppName) }
func nvimState(env Env) string     { return filepath.Join(env.Home, ".local", "state", nvimAppName) }

func luaStr(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }

func nvimInitLua(env Env) string {
	return `-- ` + Marker + `: Neovim profile for the local model (NVIM_APPNAME=` + nvimAppName + `)
-- Self-contained: your own ~/.config/nvim is not read or changed. To bring your settings in, add a line such as
--   dofile(vim.fn.expand("~/.config/nvim/lua/your_settings.lua"))
vim.g.mapleader = " "
vim.opt.number = true
vim.opt.termguicolors = true
vim.opt.mouse = "a"
vim.opt.clipboard = "unnamedplus"

local lazypath = vim.fn.stdpath("data") .. "/lazy/lazy.nvim"
if not (vim.uv or vim.loop).fs_stat(lazypath) then
  local out = vim.fn.system({ "git", "clone", "--filter=blob:none", "--branch=stable", "https://github.com/folke/lazy.nvim.git", lazypath })
  if vim.v.shell_error ~= 0 then error("could not install lazy.nvim: " .. out) end
end
vim.opt.rtp:prepend(lazypath)

require("lazy").setup({
  {
    "olimorris/codecompanion.nvim",
    dependencies = { "nvim-lua/plenary.nvim", "nvim-treesitter/nvim-treesitter" },
    opts = {
      adapters = {
        http = {
          aituner = function()
            return require("codecompanion.adapters").extend("openai_compatible", {
              name = "aituner",
              formatted_name = "aituner (local)",
              env = {
                url = ` + luaStr(env.RootURL) + `,
                api_key = ` + luaStr("cmd:cat "+shq(env.KeyFile)) + `,
                chat_url = "/v1/chat/completions",
              },
              schema = { model = { default = ` + luaStr(env.Model) + ` } },
            })
          end,
        },
      },
      interactions = {
        chat = { adapter = "aituner" },
        inline = { adapter = "aituner" },
        cmd = { adapter = "aituner" },
      },
    },
  },
}, { lockfile = vim.fn.stdpath("config") .. "/lazy-lock.json", install = { colorscheme = { "habamax" } }, checker = { enabled = false } })

vim.keymap.set({ "n", "v" }, "<leader>a", "<cmd>CodeCompanionChat Toggle<cr>", { desc = "AI chat" })
vim.keymap.set({ "n", "v" }, "<leader>i", ":CodeCompanion ", { desc = "AI inline edit" })
vim.keymap.set("v", "ga", "<cmd>CodeCompanionChat Add<cr>", { desc = "Add selection to AI chat" })
`
}

func nvimLauncherScript(env Env) string {
	return `#!/usr/bin/env bash
# ` + Marker + `: neovim
# Neovim with its own config (NVIM_APPNAME=` + nvimAppName + `) wired to the local model. ~/.config/nvim is untouched.
set -euo pipefail
export PATH="$PATH:/opt/homebrew/bin:/usr/local/bin" # appended: never overrides the tools you already resolve
command -v nvim >/dev/null 2>&1 || { echo "aituner: nvim was not found (brew install neovim)" >&2; exit 1; }
export NVIM_APPNAME=` + nvimAppName + `
exec nvim "$@"
`
}

func (neovim) Plan(env Env) Plan {
	p := Plan{
		ID: "neovim", Title: "Neovim",
		Summary:      "Neovim with CodeCompanion (AI chat and inline edits) using your local model, in its own profile.",
		Files:        []string{nvimInit(env), nvimLauncher(env)},
		WillNotTouch: []string{"~/.config/nvim and ~/.local/share/nvim (this uses NVIM_APPNAME=" + nvimAppName + ")"},
	}
	if _, ok := env.Run.Look("nvim"); !ok {
		p.Steps = append(p.Steps, Step{Kind: "install", Title: "Install Neovim", Detail: "Homebrew formula neovim"})
		p.Commands = append(p.Commands, "brew install neovim")
	} else {
		p.Steps = append(p.Steps, Step{Kind: "info", Title: "Neovim is already installed", Detail: "nothing to install"})
	}
	p.Steps = append(p.Steps,
		Step{Kind: "write", Title: "Write the profile", Detail: nvimInit(env) + ": lazy.nvim and CodeCompanion with an openai_compatible adapter"},
		Step{Kind: "install", Title: "Install the plugins", Detail: "downloads lazy.nvim, codecompanion.nvim, plenary.nvim, nvim-treesitter from GitHub"},
		Step{Kind: "write", Title: "Create the launcher aituner-nvim"},
		Step{Kind: "info", Title: "Verify", Detail: "starts Neovim headless, loads CodeCompanion and checks the aituner adapter resolves to your gateway"})
	p.Commands = append(p.Commands, "NVIM_APPNAME="+nvimAppName+` nvim --headless "+Lazy! sync" +qa`)
	return p
}

func (neovim) Status(ctx context.Context, env Env) Status {
	_, has := env.Run.Look("nvim")
	st := Status{Installed: has, Files: []string{nvimInit(env), nvimLauncher(env)}, Launcher: nvimLauncher(env)}
	_, err := os.Stat(filepath.Join(nvimData(env), "lazy", "codecompanion.nvim"))
	st.Configured = isManaged(nvimInit(env)) && isManaged(nvimLauncher(env)) && err == nil
	return st
}

func (neovim) Setup(ctx context.Context, env Env, emit Emit) error {
	if err := ensureBrew(ctx, env, emit, "neovim", false, "nvim"); err != nil {
		return err
	}
	nvim, _ := env.Run.Look("nvim")
	if _, err := writeManaged(nvimInit(env), nvimInitLua(env), 0o600); err != nil { // contains the path to the key file, not the key
		return err
	}
	if _, err := writeManaged(nvimLauncher(env), nvimLauncherScript(env), 0o755); err != nil {
		return err
	}
	emit("wrote " + nvimInit(env) + " and " + nvimLauncher(env))
	nenv := []string{"NVIM_APPNAME=" + nvimAppName}
	emit("installing plugins (first run downloads them from GitHub)")
	if err := env.Run.Run(ctx, emit, "", nenv, nvim, "--headless", "+Lazy! sync", "+qa"); err != nil {
		return fmt.Errorf("plugin installation failed: %w", err)
	}
	// verify: the plugin loads and the adapter resolves to the gateway with the model we configured
	check := `local ok, cc = pcall(require, "codecompanion")
if not ok then io.stderr:write("VERIFY_FAIL codecompanion did not load: " .. tostring(cc) .. "\n") vim.cmd("cquit 1") end
local cfg = require("codecompanion.config")
local a = cfg.adapters and cfg.adapters.http and cfg.adapters.http.aituner
if not a then io.stderr:write("VERIFY_FAIL the aituner adapter is not configured\n") vim.cmd("cquit 2") end
local ok2, ad = pcall(require("codecompanion.adapters").resolve, "aituner")
if not ok2 or type(ad) ~= "table" then io.stderr:write("VERIFY_FAIL the adapter does not resolve: " .. tostring(ad) .. "\n") vim.cmd("cquit 3") end
io.stderr:write("VERIFY_OK adapter=" .. tostring(ad.name) .. " url=" .. tostring(ad.env and ad.env.url) .. " model=" .. tostring(ad.schema and ad.schema.model and ad.schema.model.default) .. "\n")`
	var verified strings.Builder
	if err := env.Run.Run(ctx, func(l string) { verified.WriteString(l + "\n"); emit(l) }, "", nenv, nvim, "--headless", "-c", "lua "+strings.ReplaceAll(check, "\n", " "), "-c", "qa"); err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}
	if !strings.Contains(verified.String(), "VERIFY_OK") || !strings.Contains(verified.String(), env.RootURL) {
		return fmt.Errorf("verification failed: the adapter did not resolve to %s (%s)", env.RootURL, strings.TrimSpace(verified.String()))
	}
	emit("Verified: CodeCompanion loads and its aituner adapter points at your gateway")
	if model, err := Probe(ctx, env, true); err != nil {
		emit("Note: end-to-end check skipped: " + err.Error())
	} else {
		emit("Verified: real requests through the gateway work for " + model)
	}
	return nil
}

func (neovim) Remove(ctx context.Context, env Env, emit Emit) error {
	if _, err := removeManaged(nvimLauncher(env)); err != nil {
		return err
	}
	if isManaged(nvimInit(env)) {
		for _, d := range []string{nvimConfigDir(env), nvimData(env), nvimState(env), filepath.Join(env.Home, ".cache", nvimAppName)} {
			if err := os.RemoveAll(d); err != nil {
				return err
			}
		}
	} else if fileExists(nvimInit(env)) {
		return fmt.Errorf("%w: %s (left in place)", ErrForeignFile, nvimInit(env))
	}
	emit("removed the aituner Neovim profile and launcher (Neovim itself is left installed)")
	return nil
}

func (neovim) Launch(ctx context.Context, env Env, project string) (string, error) {
	if !isManaged(nvimLauncher(env)) {
		return "", fmt.Errorf("set up Neovim first")
	}
	return openTerminal(ctx, env, "neovim", nvimLauncher(env), project)
}
