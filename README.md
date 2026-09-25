# aituner

Benchmark a machine for local AI, tune it, and find out which models it can actually run well.

aituner opens a local web UI. **Every launch starts at the Hardware home page and runs detection again**, then walks the main path:

1. **Hardware**: everything about the machine, detected directly. Click **Next: downloads**.
2. **Downloads**: canirun.ai-ranked, MLX-optimised models that fit this machine (community "unrestricted" variants included by default). Each shows how your
   memory budget is used once it loads (weights, runtime, and the room left for KV cache), as a grid or list. **Add to queue** moves a model into the
   **download queue** at the top of the page; downloads run one at a time, in order, verified file by file and resumable. The download folder is
   configurable (default `~/Models`). MLX for Mac is installed from here if it is missing.
3. **Setup**: once a model is downloaded. It installs or updates MLX for Mac, starts the model, shows the connection details, and has a card per tool
   (**Claude Code, VS Code, Neovim, Vim + tmux**, or any OpenAI-compatible tool). Choosing a tool shows exactly what will be installed and changed, asks once,
   then installs, configures and verifies it, so you can open your project and start coding. Each tool gets its own isolated profile: your own editor
   settings and dotfiles are never modified.

An optional **performance track** (Benchmark, Tune, Re-run, Report) measures memory bandwidth, GPU compute and LLM speed (MLX and Ollama), shows a reviewable
diff of system changes (nothing is applied until you approve it), and re-runs for a before/after comparison that separates real change from noise. Its numbers
are pinned across the top of every view (from the last measured run until this launch has its own), and they add speed estimates to the model list.

**Status:** macOS on Apple Silicon. Linux is designed for (`internal/platform`) but not implemented; the launcher and
binary say so plainly instead of pretending. See [`SPECS/SPEC.md`](SPECS/SPEC.md) for the design, measured results and
known gaps, and [`TASKS.md`](TASKS.md) for progress.

## Run it

```sh
./run_aituner.sh
```

The launcher is idempotent. Each run it installs anything missing and upgrades outdated Homebrew-managed tools to
the latest stable (Go, Node, Python 3.14; set `AITUNER_SKIP_UPDATES=1` to skip upgrades), rebuilds only what changed,
then starts aituner. It needs [Homebrew](https://brew.sh) and never uses `sudo`.

aituner shows a small terminal UI (status, the URL, a live log; `o` opens the browser, `q` quits) and opens your browser.
Useful flags: `--no-tui` (headless), `--no-open`, `--port N`, `--version`.

MLX for Mac installs into an isolated Python environment (`mlx`, `mlx-lm`) under
`~/Library/Application Support/aituner/` and downloads two small benchmark models (about 4 GB in total, only if
missing). It lists exactly what it will fetch and asks first.

## What it changes on your machine

Only what you approve in step 3, and each change can be reverted from the UI:

| Change | Needs admin | Persists |
|---|---|---|
| GPU wired memory limit (`sysctl iogpu.wired_limit_mb`), offered only if it gains at least 1 GiB over what macOS already allows | yes (native macOS prompt) | until reboot, or opt in to a LaunchDaemon |
| Ollama flash attention and q8 KV cache (`launchctl setenv`, restarts Ollama.app) | no | until logout, or opt in to a LaunchAgent |

Tuning is judged by the re-run. A change that does not help, or makes things slower, is reported as such; on the
reference machine the Ollama change measured about 9% slower and the tool said so.

## Connect your editor

The model runs in `mlx_lm.server` on an internal port. Editors talk to aituner's **gateway** (`127.0.0.1:8747`), which needs an API
key, only forwards a safe set of request fields (so a client can never make the server load another model), and speaks both the
OpenAI API and Anthropic's Messages API (which Claude Code needs). Note that Anthropic does not support routing Claude Code to
non-Claude models: it works, but small local models are slow on its large prompts and unreliable at tool use.

| Tool | What "Set up" does |
|---|---|
| Claude Code | `aituner-claude` launcher (env vars for one run; `~/.claude` untouched); installs via `brew install --cask claude-code` if missing |
| VS Code | isolated profile with Continue and the Claude Code extension; launcher `aituner-code` |
| Neovim | `NVIM_APPNAME=aituner-nvim` profile with lazy.nvim + CodeCompanion; launcher `aituner-nvim` |
| Vim + tmux | Homebrew Vim (system Vim has no Python) + vim-ai, tmux layout with a chat pane and the model log; sources your `~/.vimrc` read-only |

## Security model

aituner can change system settings, so the local server is locked down:

- Binds `127.0.0.1` only. A random 256-bit token is created per launch and exchanged once for an `HttpOnly`,
  `SameSite=Strict` cookie. All `/api` routes require it.
- `Host` allow-list (DNS rebinding) and `Origin` check on every state-changing request; strict CSP; no CORS.
- The API accepts change **keys** only. Values are recomputed server-side from measurements, and privileged scripts
  are built from constants and integers and checked against a character allow-list.
- Outbound traffic is limited to canirun.ai and huggingface.co over HTTPS, with timeouts, size caps and retries.
- Model downloads are safetensors/config/tokenizer only, never `*.py` or pickle; `trust_remote_code` is never enabled.
  Repo names from third parties are validated before they can appear in a copy-paste command.
- Data lives in a `0700` directory; the database, lock and log files are `0600`. Only one aituner can run at a time. State-changing requests are audit-logged.
- The download folder must be inside your home directory or on an external drive, with no hidden or `~/Library` paths; only models the
  recommender offered can be downloaded, and only weights/config/tokenizer files are fetched.

`SPECS/SPEC.md` §10 has the full model. Note the open decision D1: SQLite has no Row Level Security, so tenant
isolation is enforced in the data layer instead.

## Develop

```sh
go vet ./... && go test -race -count=1 ./...      # unit + real-hardware tests
AITUNER_LIVE=1 go test -count=1 -v ./...          # also hits canirun.ai / Hugging Face and runs the full benchmark
cd web && npm run check && npm run build          # Svelte diagnostics, then build into internal/webui/dist
```

Layout: `cmd/aituner` (entry point, TUI) · `internal/store` (SQLite, tenant-scoped) · `internal/platform` (OS seam,
macOS detection) · `internal/bench` (suites) · `internal/tune` (plan/apply/revert) · `internal/reco` (recommendations) ·
`internal/api` (HTTP, SSE, phase gates) · `web/` (Svelte PWA).

## Uninstall

Quit aituner, then delete `~/Library/Application Support/aituner/`. Downloaded models stay in your models folder (default `~/Models`); delete them yourself if you no longer want them. If you applied persistence options, revert them
in the UI first (or remove `/Library/LaunchDaemons/ai.aituner.wiredlimit.plist` and
`~/Library/LaunchAgents/ai.aituner.ollama-env.plist`). Cached models live in `~/.cache/huggingface`.
