# aituner

Benchmark a machine for local AI, tune it, and find out which models it can actually run well.

aituner opens a local web UI and walks through five steps, in order:

1. **Hardware**: everything about the machine, detected directly. No model suggestions yet.
2. **Benchmark**: real measurements of memory bandwidth, GPU compute and LLM speed (MLX and Ollama).
3. **Tune**: a reviewable diff of system changes. Nothing is applied until you approve it.
4. **Re-run**: the same benchmark again, with a before/after comparison that separates real change from noise.
5. **Models**: only now does it ask [canirun.ai](https://www.canirun.ai) what fits, resolve each model to an
   Apple-Silicon (MLX) build that loads on your machine, and estimate its speed from what you measured. Community
   "unrestricted" (abliterated/uncensored) variants are included by default and can be switched off.

The server enforces this order. Asking for recommendations early returns `409 wrong_phase`.

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

The first benchmark installs an isolated Python environment (`mlx`, `mlx-lm`) under
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
- Data lives in a `0700` directory; the database and lock file are `0600`. Only one aituner can run at a time.

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

Quit aituner, then delete `~/Library/Application Support/aituner/`. If you applied persistence options, revert them
in the UI first (or remove `/Library/LaunchDaemons/ai.aituner.wiredlimit.plist` and
`~/Library/LaunchAgents/ai.aituner.ollama-env.plist`). Cached models live in `~/.cache/huggingface`.
