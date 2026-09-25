# aituner — SPEC

Status: v0.1 implemented on macOS (Apple Silicon); Linux not yet. Sections marked **[VERIFIED]** were checked against
official sources or run on the reference machine; **[UNVERIFIED]** are known gaps (see §15).
Keep this file current with reality (update in the same change as the code).

## 1. Goal

A local tool for people who want to run capable, unrestricted, local models (code, chat, image generation).
It answers, in order:

1. **What hardware is this?** (full capability detection, no model talk yet)
2. **How fast is it for AI, really?** (measured GPU / memory / real LLM inference — not spec-sheet numbers)
3. **What can be tuned?** (a reviewable diff of system changes, applied only on approval)
4. **Did tuning help?** (same benchmark re-run; before/after delta)
5. **Only now: what models can I run?** (canirun.ai fit data + macOS-optimised builds + unrestricted variants,
   sized against the *tuned* machine and calibrated by the *measured* results)

Reference machine: Mac Studio (Mac13,1), Apple M1 Max, 10 CPU cores (8P+2E), 24 GPU cores, 32 GB unified memory,
macOS 26.5.1, Metal 4. **[VERIFIED on this machine]**

### Non-goals (v0.1)
- Linux implementation (architecture is ready for it, §4; the UI must say "not yet supported", never fake it).
- Training / fine-tuning. Multi-machine. Cloud anything.
- Being a model runner/chat UI. We install/verify runtimes only as far as benchmarking and recommendation need.

## 2. User flow and hard gating

Single-page app, five steps. The **server** enforces order (UI gating is cosmetic):

| # | Step | Server state after | Notes |
|---|------|--------------------|-------|
| 1 | Hardware | `detected` | Launch opens browser here. **No model suggestions anywhere.** |
| 2 | Run benchmark (baseline) | `baseline_done` | Button. Shows what will be installed/downloaded first. |
| 3 | Tune: review diff | `tune_reviewed` | Per-change opt-in. Declining all is allowed. |
| 4 | Re-run benchmark + compare | `tuned_done` | Always re-runs, even with zero changes (then it doubles as a noise/variance control). |
| 5 | Recommendations | `tuned_done` | `GET /api/v1/recommendations` returns `409 wrong_phase` before `tuned_done`. Server phases: `detected`, `baseline_running`, `baseline_done`, `tune_reviewed`, `tuned_running`, `tuned_done`; a crash mid-run is recovered to the previous phase at startup. |

Every result is tied to a `run` (one pass through the flow) so history is kept and comparable.

## 3. Stack and conventions (per engineering standards)

- **Backend:** Go (latest stable; 1.27.1 on the reference machine, go.mod requires 1.27.1 **[VERIFIED]**). Pure-Go SQLite
  (`modernc.org/sqlite`, no CGO). Python only as a subprocess for MLX workloads (§6).
- **Frontend:** Svelte 5 + Vite SPA, static build embedded in the Go binary with `go:embed`. PWA (manifest +
  service worker), responsive, dark and light first-class, thin-line icons, 2–4px radii, no emoji.
- **Binary:** `aituner` is a **Bubble Tea v2 TUI** (`charm.land/bubbletea/v2` v2.0.9, latest stable **[VERIFIED]**; status, URL, live log, quit) that hosts the server and opens the
  browser. `--no-tui` for headless. Launcher **`run_aituner.sh`** is the preflight (§9).
- **No Docker, no Kubernetes, no third-party SaaS.** The only external services are the two public, read-only,
  unauthenticated sources the user asked for / that the models live on: canirun.ai and huggingface.co.
- **Build order:** DataStore → API → UI. No mocks: every feature is exercised against the real datastore, real API,
  and real hardware.

## 4. Architecture

```
cmd/aituner            main: flags, TUI, server lifecycle, browser open
internal/store         SQLite, migrations, tenant-scoped repository
internal/api           HTTP handlers, auth/host/origin middleware, SSE, phase gate
internal/platform      Platform interface + darwin impl (+ linux stub returning ErrUnsupported)
internal/detect        hardware/runtime detection (uses platform)
internal/bench         benchmark orchestrator + suites (memory bw in Go; GPU + LLM via MLX; Ollama)
internal/bench/py      embedded Python workloads (go:embed), run inside the aituner venv
internal/tune          Tunable interface: Current / Propose / Diff / Apply / Revert
internal/canirun       canirun.ai API client
internal/hf            Hugging Face Hub API client (MLX resolution, sizes, provenance)
internal/reco          recommendation pipeline (§7)
web/                   Svelte SPA
run_aituner.sh         preflight + build + launch
```

`platform.Platform` is the only place that knows about the OS: `Detect()`, `Tunables()`, `Runtimes()`,
`DataDir()`. The linux file returns `ErrUnsupported`; handlers translate that to a clear
`501 platform_unsupported`.

## 5. Datastore

SQLite in the OS data dir (`~/Library/Application Support/aituner/aituner.db`; XDG on Linux), `0700` dir / `0600`
files, WAL, foreign keys on, parameterised queries only, forward-only migrations.

Tables (all carry `tenant_id NOT NULL`): `tenants`, `machines` (hardware snapshot JSON), `runs` (phase/state),
`bench_results` (run, phase `baseline|tuned`, suite, engine, metric, value, unit, trials JSON, versions JSON),
`tune_changes` (key, before, after, applied_at, reverted_at), `reco_cache` (source, key, body, fetched_at).

> **DEVIATION D1 — Row Level Security.** The standards require RLS on every datastore. SQLite has no RLS, and
> this is a single-user local tool. Mitigation: every query goes through a `Store.ForTenant(id)` handle that
> injects `tenant_id` (no exported unscoped query path), `tenant_id` is part of every index/unique key, and a
> test asserts cross-tenant reads/writes are impossible. If real RLS is required, the alternative is a local
> PostgreSQL — heavier, contradicts "smallest moving-part count". **Needs owner sign-off.**

## 6. Benchmark (real measurements only)

Runs in `internal/bench`; progress streamed to the UI over SSE. Each suite does warm-up, N trials (default 3),
reports median and spread, and records thermal state and tool versions. Sleep is prevented for the duration
(`caffeinate -i` child). Benchmark model is **fixed** so before/after and machine-to-machine numbers compare.

| Suite | How | Metrics |
|---|---|---|
| Memory bandwidth | Go, multi-threaded streaming read/copy over a buffer far larger than cache | GB/s (measured) |
| GPU compute + memory | Embedded Python using MLX (`mlx`, Metal): fp16/bf16 matmul, large elementwise/copy | TFLOPS, GB/s |
| LLM — MLX | `mlx_lm` `benchmark` interface (`-p` prompt tokens, `-g` gen tokens, `-n` trials) against `mlx-community/Llama-3.2-3B-Instruct-4bit` (mlx-lm's own default) **[VERIFIED flags]** | prompt tok/s, gen tok/s, peak memory |
| LLM — Ollama (if installed/running) | `POST /api/generate` non-streaming; tok/s = `eval_count / eval_duration`, prefill from `prompt_eval_*` **[VERIFIED fields; 14B Q4 measured ≈22 tok/s cold-load excluded]** | prompt tok/s, gen tok/s |

Runtime setup (idempotent, done by preflight §9): a venv in the data dir from the Homebrew Python
(3.14.x present; `mlx` 0.32.x ships cp310–cp314 macOS arm64 wheels **[VERIFIED on PyPI]**), `pip install -U mlx
mlx-lm`. Versions are recorded with every result so an upgrade between runs is visible. Model download (~2 GB)
only after the user clicks Run and sees the list of what will be fetched.

## 7. Tuning (macOS)

A `Tunable` declares: key, how to read the current value, proposed value, **why / expected effect**, whether it
needs admin, whether it survives reboot, how to revert. Proposals are **capability-gated** — a tunable that this
machine does not support is not offered (e.g. High Power Mode is absent from this Mac Studio's `pmset -g cap`
**[VERIFIED]**, so it is not shown).

v0.1 tunables:

1. **GPU wired memory limit** — `sudo sysctl iogpu.wired_limit_mb=N`. Exists on this machine, `0` = macOS default
   **[VERIFIED]**; documented by mlx-lm for large models, macOS 15+ **[VERIFIED]**. Proposed
   `N = total_MB − reserve`, `reserve = max(5 GiB, 12.5% of RAM)` (32 GB → 27648 MB). **Only offered if it gains ≥ 1 GiB
   over what MLX reports Metal already allows** (`max_recommended_working_set_size`). Measured on the reference
   machine: default is already 25.0 GiB of 32, so the gain is +2 GiB — modest, and the UI says it will not speed up
   models that already fit. Volatile across reboot.
2. **Persist wired limit (opt-in)** — root LaunchDaemon `ai.aituner.wiredlimit`, built as root with Apple's
   `plutil` (no user-writable file is ever copied into `/Library`, no shell quoting) and loaded with
   `launchctl bootstrap`. Removed on revert.
3. **Ollama server env** — `OLLAMA_FLASH_ATTENTION=1`, `OLLAMA_KV_CACHE_TYPE=q8_0` via `launchctl setenv`
   (docs.ollama.com/faq **[VERIFIED]**), then Ollama.app is restarted with **SIGTERM + `open -a`**. AppleScript
   `quit` was tried first and fails with `-128 User canceled` (Automation permission) **[VERIFIED on this machine]**.
   User-level, no admin. **Measured on the reference machine: this made Ollama generation ~9% slower
   (82.7 → 75.0 tok/s) on the short-prompt benchmark**; the comparison reports it as `slower` and the UI offers Revert.

Rules: nothing is applied without an explicit per-change opt-in after the diff is shown (unified `-/+` view plus
table). Privileged changes use the native macOS admin prompt (`osascript … with administrator privileges`); aituner
never sees or stores the password. Every change is logged in `tune_changes` and is revertible from the UI. If an apply fails
part-way it is **rolled back automatically** so nothing is left half-applied and unrecorded. Tunables
must not claim a speedup — the re-run measures it and the comparison reports "no significant change" when the delta
is within measured trial spread.

## 8. Recommendations (unlocked after step 4)

Inputs: tuned effective GPU memory budget (post-change wired limit, else MLX's recommended working set), measured
bandwidth, measured tok/s.

1. **canirun.ai** — `POST https://www.canirun.ai/api/recommend` with the detected profile (`hardware.cpu`,
   `ramGb`, `gpu.name`, `useCase`, `limit` 1–25) and `GET /api/models`; documented in the project's README and
   source **[VERIFIED live: returns grade, status, quant, `vramRequiredGb`, `diskSizeGb`, HF `url`]**. Note the apex
   `canirun.ai` answers `307 → www.canirun.ai`; the client targets `www` directly. canirun.ai is GGUF/VRAM-centric
   and has no MLX or "unrestricted" signal, and it has no benchmark data — hence steps 2–4.
2. **macOS-optimised resolution** — for each candidate, look up a *plain quant* of the same base model in
   `mlx-community` via the Hugging Face API (fine-tunes/prunes are not accepted as "the same model"); sizes come from
   repo metadata **[VERIFIED]**. Candidates with no MLX build are dropped and counted ("no MLX build"). Quant preference:
   4-bit, 8-bit first for small models, bf16 last. **Architecture gate:** the repo's `config.model_type` must be one the
   *installed* mlx-lm can load (module list + `MODEL_REMAPPING`, read from the venv) — this removed e.g.
   `diffusion_gemma` **[VERIFIED live]**. Re-sized against the tuned budget with headroom (`max(1.5 GiB, 8%)`; ≤85% of
   budget = comfortable, ≤100% = tight).
3. **Speed estimate, calibrated** — `tok/s ≈ eff × measured_bandwidth / bytes_read_per_token` (active params for
   MoE), with `eff` calibrated from the benchmark model's measured tok/s. Always labelled *estimate*.
4. **Unrestricted profile (default, toggleable)** — open-weight, permissively licensed models plus community
   *abliterated/uncensored* MLX variants of the fitting families (HF search **[VERIFIED they exist]**). Each shows
   provenance (author, downloads, license, last update), safetensors-only (no pickle), and a plain note that these are
   third-party-modified, unaudited weights. `trust_remote_code` is never enabled by default.
5. **Categories** — code, chat/reasoning, image generation. Image runtime: **mflux** (`uv tool install --upgrade mflux`; supports Z-Image, FLUX.2, FLUX.1, Qwen-Image)
   **[VERIFIED against github.com/filipstrand/mflux README]**. Only models mflux lists are recommended; only the
   Z-Image-Turbo command is quoted (the one shown in the README); others link to the README. No speed estimate for images. Already-installed Ollama models
   are marked as installed.

6. **KV-cache meter** — for every MLX candidate and variant the model's real `config.json` is fetched from Hugging Face
   (cached) and the KV cache is computed **per layer type**: full-attention layers grow with context
   (`2 x kv_heads x head_dim x 2 B` per token per layer; `head_dim`/`kv_heads` from the config, global-layer overrides
   honoured), sliding-window layers cost a constant `window x ...` once full, linear-attention/convolution layers keep a
   small constant state that is **not counted** (noted in the UI). Multi-head-latent-attention and configs missing needed fields
   are reported as *unknown* with the reason, never guessed. Where a config has no `layer_types`, all layers are assumed full
   length, which can only under-state the context that fits. Room = `budget - weights - 1 GiB runtime overhead`; tokens that fit
   are computed for an fp16 KV cache and an 8-bit one (8 bits + fp16 scale/bias per 64-group), capped at the model's
   `max_position_embeddings`. The UI draws weights / runtime / KV room (and spare) to scale. **[VERIFIED]** against 6 real
   configs (dense, hybrid, sliding+global, conv hybrid; Llama-3.1-8B = 128 KiB/token exactly), fuzzed, and live on 12 models.
7. **Downloads** — the *Download* button saves a recommended MLX repo into the user's **models folder**
   (`<folder>/<org>/<name>`, default `~/Models`, configurable in the UI, stored per tenant in the datastore). Only repos the
   recommender just offered can be downloaded (no general fetch endpoint), only when recommendations are unlocked, never while a
   benchmark/tuning job runs (and benchmarks are refused while a download runs) so numbers stay valid. Files: weights, config,
   tokenizer only (`*.json *.safetensors *.model *.tiktoken *.txt *.jinja *.jsonl`; never `*.py`, pickle, `.bin`); gated and
   pickle-only repos refused; free space checked up front (`remaining + 2% + 512 MiB`); progress from bytes on disk; cancel keeps
   partial files and a later attempt **resumes**; on completion **every file is verified against Hugging Face's listed size** and a
   marker `.aituner-model.json` is written, so "downloaded" survives restarts. The UI then shows the local path and a command that
   runs the local copy. **[VERIFIED]**: a real download was loaded and generated text with mlx-lm from the folder; cancel/resume tested.
   **Folder policy** (`internal/modeldir`): absolute path, inside `$HOME` or under `/Volumes/<drive>/...`; symlinks resolved before the
   check; no hidden (dot) component, not `~/Library`, not the home or drive root, no control characters, <=1024 chars; the folder is
   created and proven writable before it is saved.

Memory budget = the larger of Metal's recommended working set and an explicit `iogpu.wired_limit_mb`, read from the
*current* system (a reverted tune is not credited). Lookups are cached 6 h in the datastore; stale cache is served if the
network fails; otherwise the UI shows the error — never invented data.

## 9. Launch and preflight — `run_aituner.sh`

Idempotent; re-run = update to latest stable. Installs missing tools and **upgrades outdated Homebrew-managed ones**
(Go, Node, Python 3.14; `AITUNER_SKIP_UPDATES=1` disables upgrades); requires Homebrew; rebuilds web (`npm ci` + Vite) only
when sources are newer and Go incrementally; then `exec bin/aituner`. The MLX venv (`pip install -U mlx mlx-lm`) is created by
the app on the user's first Run click, after consent. Never uses `sudo`. On Linux/Intel it exits with a clear message.

## 10. Security model (localhost tool that can change system settings)

- Bind `127.0.0.1` only. Fixed default port with fallback; port printed in TUI.
- Per-launch random 256-bit session token that **never appears in a URL, argv or the terminal**. The browser is handed a
  **single-use, 15-minute launch nonce** (`?t=`), exchanged once for an `HttpOnly`, `SameSite=Strict` cookie carrying the
  session token; replays, expired and evicted nonces (max 8 outstanding) set nothing. All `/api` requires the cookie
  (constant-time compare). The TUI mints a fresh link per `o` press.
- `Host` allow-list (`127.0.0.1:port`, `localhost:port`) to defeat DNS rebinding; `Origin` must match on
  non-GET; CSRF-safe by cookie + Origin check; strict CSP, `X-Content-Type-Options`, no CORS.
- Privileged actions only through §7's allow-listed tunables — no generic "run command" endpoint. Arguments are
  built from validated values, never shell-interpolated (`exec.Command` with argv).
- Outbound: canirun.ai and huggingface.co only, HTTPS, timeouts, response size caps, strict JSON decode.
- Downloads: safetensors only; refuse repos with pickle-only weights.
- No secrets in code. None needed today (public endpoints); any future token goes through `secret_mgr`.
- Dependencies pinned by lockfiles; CVE/outdated check before release.

## 11. UI

Five-step stepper, monochrome-forward, near-black dark / true-white light, mono + sans type, thin-line icons,
44px+ touch targets, safe-area aware, offline shell via service worker (API calls need the server; UI degrades
with a clear banner). Diff view for tuning; before/after table with delta and "within noise" marking; recommendation
cards grouped code / chat / image with fit grade, runtime, size, estimated tok/s, provenance and one-click
"copy install command" (we do not auto-install models). Accessible contrast (WCAG AA) in both themes.

## 12. Verification plan

Go unit tests (store isolation, phase gate, tunable diff/parse, canirun/hf clients against recorded-shape
fixtures *plus* live contract checks), then end-to-end on the real Mac: run the whole flow, real benchmarks, real
tuning apply + revert. UI verified in Chrome via Playwright in dark, light and a mobile viewport. Results and gaps
are reported honestly in `TASKS.md`.

## 13. Sources relied on

- canirun.ai project README + source: https://github.com/midudev/canirun.ai
- mlx-lm: https://github.com/ml-explore/mlx-lm (benchmark script, wired-limit guidance); PyPI `mlx`, `mlx-lm`
- Ollama FAQ (env vars, macOS `launchctl setenv`): https://docs.ollama.com/faq
- Hugging Face Hub API: https://huggingface.co/docs/hub/api
- Apple: `sysctl iogpu.*`, `pmset`, `system_profiler` (man pages / on-device output)

## 14. Open decisions for the owner

- D1 (above): SQLite + app-level tenant scoping instead of true RLS.
- Bubble Tea TUI wraps a web UI (standards say Go binaries are TUIs; product requires a browser UI) — both are provided.
- Licence for the repo (none chosen yet).

4. **Persist Ollama env (opt-in, user-level, no admin)** — per-user LaunchAgent `ai.aituner.ollama-env`, built with `plutil`
   argv (no shell), lint-checked, `launchctl bootstrap gui/<uid>`, then **`kickstart -k`** and verified by reading the
   variables back. (`bootstrap` alone does not fire RunAtLoad in a live session; found by a real launchctl test.)

## 14a. Reliability and robustness (implemented and tested)

- **Single instance:** `flock` on `aituner.lock` (`0600`, PID inside); a second launch refuses with the running PID.
- **Outbound resilience:** `internal/httpx` retries 429/502/503/504 and network errors (3 attempts, backoff, `Retry-After`
  capped at 10 s, body re-sent), context-aware, clear rate-limit message, allow-listed HTTPS only (redirects too).
- **Benchmark validity:** before/after each run the machine is checked (battery, thermal/speed-limit, load average, memory
  pressure) and warnings are stored with the results and shown in the UI. Five trials per metric; noise = half-range with the
  lowest/highest trial trimmed at n>=5 (median ignores outliers); any metric whose untrimmed spread exceeds 15% is flagged.
  Verified live: a busy machine (load 7.3/10 cores from the QA browser) was flagged.
- **Cancel / crash safety:** cancelling a benchmark returns the run to its previous phase, kills the Python and `caffeinate`
  children (verified with `pgrep`), and the run is retryable; a `*_running` run left by a crash is recovered at startup.
  A failed tuning apply is rolled back automatically.
- **Command safety:** HF repo ids are validated (`^[A-Za-z0-9][A-Za-z0-9._-]*/...`, no `..`) before they can appear in a
  copy-paste command; commands use the venv's absolute, single-quoted path.
- **PWA:** manifest + icons verified at declared sizes, service worker is network-first, never caches `/api/`, prunes stale
  hashed assets; offline reload shows the cached shell with a clear banner (all verified in Chrome).
- **Audit log:** every authenticated state-changing request (and every download request with repo and destination) is written to
  `aituner.log` (`0600`, moved aside at 5 MB) so any action can be attributed afterwards. The UI asks for confirmation before "New
  run" because it relocks the recommendations.
- **Fuzzing:** Go native fuzz targets for the command builder, name matchers, estimator, thermal/load parsers, system_profiler
  parser and the admin-script allow-list (fuzzing found and fixed an estimator returning negative speed for invalid input).

## 16. Phase 2: reporting, pinned stats, serve a model, connect your editor

Status: **implemented** (P1-P10 in TASKS.md); §16.5 lists what real runs revealed and what is still unverified. Everything
here follows the same rules: real measurements, nothing simulated, additive and reversible, never touching the user's own
dotfiles (`~/.vimrc` and `~/.tmux.conf` on the reference machine are symlinks into a git-tracked dotfiles repo).

### 16.1 Benchmark reporting and pinned stats

- **Richer suites** (all real, all with trials): prompt-length sweep for prefill (256 / 1024 / 4096 tokens), generation speed at
  two context depths (512 / 4096 tokens) so the cost of long context is visible, time-to-first-token derived from prefill,
  and a **sustained GPU run** (about 20 s of continuous matmul) reporting first-third vs last-third throughput to expose
  thermal throttling directly instead of inferring it from `pmset`.
- **Derived insight, not just numbers**: bandwidth utilisation (measured GPU GB/s vs canirun.ai's reference for the chip),
  and the **roofline** for LLM decoding, `bandwidth / model bytes`, with the measured tok/s shown as a percentage of it.
  Every derived figure states its formula and inputs.
- **Report tab** (available from the first completed benchmark): hardware and software versions, health warnings, every metric
  with all trials drawn as a strip/bar chart (SVG, both themes), before/after comparison with the noise band, tuning changes
  applied, run history with side-by-side comparison of any two runs, and **export** as JSON, Markdown and CSV
  (`GET /api/v1/report?run=<id>&format=json|md|csv`, authenticated, same-origin).
- **Pinned stats bar** on every tab: a sticky strip with the headline numbers (GPU fp16 TFLOPS, GPU bandwidth, MLX generation
  and prompt tok/s, Ollama generation tok/s, memory budget) and, once a re-run exists, the delta vs baseline; plus a health
  chip (thermal / power / load) and, while serving, the live model's status. Collapsible on phones; data comes from the run
  the user is viewing, never invented.

### 16.2 Serving a downloaded model

- **MLX for Mac**: the runtime tab shows the installed `mlx` / `mlx-lm` versions and an **Install / update MLX** action that
  runs the same idempotent installer used for benchmarking (`pip install --upgrade` into the aituner venv), with live log.
- **Model server** (`internal/serve`): starts `mlx_lm.server` on a loopback port for a *downloaded* model folder, supervised by
  aituner (start / stop / restart, health probe on `/health`, ring-buffered logs, memory shown). Flags exposed: max tokens,
  KV-cache quantisation (`--kv-bits`, `--kv-group-size`, `--quantized-kv-start`), prompt-cache size. `--trust-remote-code` is never
  passed. Verified in the installed mlx-lm: `mlx_lm.server` serves `/v1/chat/completions`, `/v1/completions`, `/v1/models`,
  `/health`, supports `tools` -> `tool_calls` through the tokenizer's tool parser, streaming, and `stream_options.include_usage`.
  It has **no authentication**, so it is bound to an internal loopback port and never exposed directly.
- **Gateway** (`internal/gateway`): the only listener editors talk to, `127.0.0.1` only, requires an API key (random,
  generated once, stored `0600` in the data dir, sent as `Authorization: Bearer` or `x-api-key`):
  - OpenAI-compatible: `/v1/chat/completions`, `/v1/completions`, `/v1/models` reverse-proxied to the model server (works with
    Continue, vim-ai, CodeCompanion, curl, anything OpenAI-compatible).
  - Anthropic Messages: `/v1/messages` (streaming and not) and `/v1/messages/count_tokens`, translated to and from the OpenAI
    format (system prompt, text / image / tool_use / tool_result blocks, tools and tool_choice, stop sequences, usage, the
    Anthropic SSE event sequence). Required by Claude Code, which sends only Anthropic format
    (`ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN`, per code.claude.com/docs llm-gateway-connect / llm-gateway-protocol **[VERIFIED]**).
  - **Caveat shown in the UI**: Anthropic states it does not support routing Claude Code to non-Claude models through any gateway
    **[VERIFIED in the official docs]**. It works technically; quality and tool-use reliability depend on the local model.
- Serving and benchmarking are mutually exclusive (a loaded model would distort measurements), as are downloads and benchmarks.

### 16.3 Connect an editor ("Set up" installs and configures it)

A **Run** tab unlocks once at least one model is downloaded. Each integration shows a plan (what will be installed, which files
are written, what is *not* touched), asks for one confirmation, runs as a job with live log, then **verifies** itself and reports
the result. Everything is additive: isolated config directories, marker-guarded files, never overwriting a file aituner did not
create, fully removable ("Remove" button). Installs use the tool's official method (Homebrew formula/cask, or the vendor's
documented installer) after the plan names the exact command.

| Integration | Install (only if missing) | Configuration (isolated) | Verification |
|---|---|---|---|
| **Claude Code** | already present here; else `brew install --cask claude-code` (official) | launcher `~/.local/bin/aituner-claude` (marker-guarded) exporting `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL` and the `ANTHROPIC_DEFAULT_*_MODEL` aliases, then `exec claude`; **`~/.claude/settings.json` is not modified**, so normal `claude` is unaffected | `claude --version`; a real `claude -p` round-trip through the gateway |
| **VS Code** | `code` CLI present; else cask `visual-studio-code` | an **isolated profile** (`--user-data-dir`, `--extensions-dir` under `~/.config/aituner/vscode`, `CONTINUE_GLOBAL_DIR` **[VERIFIED in Continue's source]**): extension **Continue** (`Continue.continue`) with a local OpenAI-compatible model, and the **Claude Code** extension with `claudeCode.environmentVariables`; launcher `aituner-code` | `code --list-extensions --extensions-dir ...`; config files parse |
| **Neovim** | `brew install neovim` | `NVIM_APPNAME=aituner-nvim` config with lazy.nvim + **CodeCompanion** (`openai_compatible` adapter, `interactions.chat/inline`) **[VERIFIED against the plugin's docs]**; launcher `aituner-nvim`; the user's `~/.config/nvim` is untouched | headless plugin sync, `:checkhealth`-style load, a real chat request through the adapter |
| **Vim + tmux** | `brew install vim` (system Vim is `-python3` **[VERIFIED]**; `vim-ai` needs python3) and `tmux` if missing | vim-ai cloned into an isolated pack dir; isolated vimrc that **sources the user's own vimrc read-only** then sets `endpoint_url` per command **[VERIFIED against vim-ai's README]**; `aituner-tmux` opens a session: Vim (main), server log, and a small chat pane | `vim -es` loads the plugin with python3; a real completion round-trip |
| **Any OpenAI-compatible tool** | none | shows base URL, key, model id, curl example | a real `curl` through the gateway |

"Open" starts the tool for a chosen project folder (Terminal.app for terminal tools, `code` for VS Code). The API key is shown
only in the local UI and written only to `0600` files under aituner's config dir.

### 16.4 Security additions

Gateway and model server bind loopback only; key required on every gateway request (constant-time compare); model server is not
reachable except through the gateway; setup jobs run only fixed, allow-listed commands (argv, no shell) with values from aituner's
own state (paths validated by `modeldir`, model ids by `ValidRepo`); launcher scripts are generated from constants plus quoted
paths and never overwrite foreign files; every setup action is audit-logged; removal restores the previous state exactly.

### 16.5 What real runs revealed (and how the design changed)

These were found by running the real tools, not by design; each has a regression test.

- **Benchmark validity.** Bandwidth measured after other GPU work swung 5-30% because earlier allocations fragment MLX's cache
  (a clean process is stable to ~1%; clearing the cache made it worse). Fix: bandwidth **first**, matmul next, the sustained run
  **last**, time-based warm-up. Trial spread across three back-to-back full runs: +-0.8%, +-2.9%, +-5.2%.
- **`mlx_lm.server` loads any model a request names** (and honours `draft_model`/adapter fields). The gateway therefore forwards
  requests only through a field allow-list and pins `model` to `default_model`; the server also runs with `HF_HUB_OFFLINE=1`.
  Verified live: a request naming another model fails and the server stays up on the original.
- **Tool calls are not reliably structured.** Real captures: Llama 3.1 returns `<|python_tag|>{...}` as plain text; Qwen2.5-Coder
  returns a fenced ```` ```json ```` block and leaks `<|im_end|>`. The gateway recovers calls from text (Hermes `<tool_call>`, python
  tag, fenced JSON, bare JSON) **only for names the client declared**, in streaming too (holding back only text that might be a
  call), and strips control tokens.
- **Claude Code specifics** (found by running the real CLI): it sends `system`-role entries *inside* `messages` (merged into one
  leading system message); it warns about an unknown model window unless `CLAUDE_CODE_MAX_CONTEXT_TOKENS` is set (the launcher sets
  it from the model's real context); it sends ~15K tokens per turn, so an 8B model needs about a minute to first token and
  answered a one-word request with an invalid tool call: a limit of small models, not of the plumbing. The UI says so.
- **A gateway created before the model starts** froze empty model info; model name and context window are now evaluated per request.
- **Process lifetime.** A killed harness left an orphaned `mlx_lm.server` holding GPU memory. The server now stops with aituner's
  context (quit or SIGTERM), a pidfile lets the next launch stop a stale one (only if the process is verifiably ours), and
  quitting aituner was verified to leave 0 model-server processes.
- **The user's own files are hostile territory.** The reference `~/.vimrc` has an error (missing colour scheme) that makes silent
  Vim exit 1; verification now checks aituner's part strictly without it and reports the user's error as a note. Launchers
  *append* to PATH so they never change which tool resolves.
- **Neovim adapter verification** resolves the plugin's adapter exactly as it does at request time, including the `cmd:cat <keyfile>`
  secret (URL, model and a 56-character key checked).

Verified end to end on the reference machine (real model, real installs): Claude Code launcher (env, model, context), VS Code
isolated profile (both extensions installed, configs valid), Neovim (`brew install neovim`, plugins synced, adapter resolves the key,
launch in Terminal with `NVIM_APPNAME`), Vim (real `:AI` round trip returned the model's reply), tmux layout on an isolated tmux
server, the terminal chat client, Anthropic- and OpenAI-format requests.

**Not verified:** the VS Code extensions' own chat UIs (Continue, Claude Code) were not driven; a CodeCompanion chat *inside*
Neovim was not driven (its adapter resolution, including the key, was); interactive tmux attach; a full Claude Code *agentic* session
with tool use on a large model. Expect small local models to be unreliable at agentic tool use.

## 15. Measured on the reference machine, and known gaps

Measured end to end through the real UI (Mac Studio M1 Max 32 GB, macOS 26.5.1, mlx 0.32.2, mlx-lm 0.31.3, Ollama 0.34.3):

| Metric | Result |
|---|---|
| GPU matmul fp16 / fp32 | 7.07 / 6.42 TFLOPS |
| GPU memory bandwidth (MLX) | 355 GB/s (canirun.ai reference: 400) |
| CPU memory copy / read (8 threads) | ~130 / ~122 GB/s |
| MLX Llama-3.2-3B-4bit prompt / generation | 1000 / 129.6 tok/s |
| Ollama llama3.2:3b prompt / generation | 884 / 82.3 tok/s (MLX generates ~57% faster) |
| Speed-estimate calibration | 67% of measured bandwidth |

Release-gate results (independent agents, all findings fixed or dispositioned): code review PASS, security PASS (no
critical/high; token-in-URL note fixed by single-use launch links), UI/UX NEEDS-WORK -> fixed (dark/light `--faint` contrast,
verified by computing WCAG ratios), dependencies PASS, QA 7/8 -> mobile comparison table fixed, manager audit: only the
owner-only items below remain. `govulncheck`: none; `npm audit`: 0; `go test -race`: pass.

Known gaps:
- **[UNVERIFIED]** Applying the wired-limit change (needs the macOS admin dialog) was not exercised: no human was present
  to approve it. Plan, script generation, allow-list and rollback logic are unit-tested; the real `sysctl`/LaunchDaemon
  path and whether Metal's working set then follows the new limit still need a human-approved run.
- The Ollama LaunchAgent is proven to install, run and remove under real launchctl, but its RunAtLoad firing on an actual
  fresh login has not been observed.
- Node 26.x is on Node's "Current" release line (not LTS). The owner's standard is latest stable and no downgrades, so it stays.
- Speed estimates are bandwidth models; MoE routing overhead and diffusion/vision architectures are not modelled.
- Recommendation install commands assume the user runs them from the aituner venv (`~/Library/Application Support/aituner/venv/bin`).
- Linux: not implemented (`platform` returns `ErrUnsupported`).
- D1 (SQLite without RLS) still awaits owner sign-off.

### 16.6 Prompt-cache bound (real-run finding)

`mlx_lm.server` keeps up to 10 reusable prompts and, by default, no byte limit. During a real Claude Code run (large, varying ~15K-token prompts) the log showed `Prompt Cache: 9 sequences, 19.28 GB` on a 32 GB machine. aituner now starts the server with `--prompt-cache-size 4` and `--prompt-cache-bytes` = RAM/8 clamped to 1-6 GiB (4 GiB on the reference machine). Verified live: with 7K-token distinct prompts the log shows `1 sequences, 2.17 GB` (older entries evicted). Known limitation: `message_start` carries `input_tokens: 0` because `mlx_lm.server` reports usage only in its final chunk; the final `message_delta` carries the real counts.
