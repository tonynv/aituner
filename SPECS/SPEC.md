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

- **Backend:** Go (latest stable; 1.26.x on the reference machine **[VERIFIED]**). Pure-Go SQLite
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
- Per-launch random 256-bit token, delivered in the URL the tool opens, exchanged once for an `HttpOnly`,
  `SameSite=Strict` cookie; all `/api` requires it (constant-time compare).
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

Known gaps:
- **[UNVERIFIED]** Applying the wired-limit change (needs the macOS admin dialog) was not exercised: no human was present
  to approve it. Plan, script generation, allow-list and rollback logic are unit-tested; the real `sysctl`/LaunchDaemon
  path and whether Metal's working set then follows the new limit still need a human-approved run.
- Ollama env tuning is not persistent across logout/reboot (`launchctl setenv`); no LaunchAgent yet.
- Speed estimates are bandwidth models; MoE routing overhead and diffusion/vision architectures are not modelled.
- Recommendation install commands assume the user runs them from the aituner venv (`~/Library/Application Support/aituner/venv/bin`).
- Linux: not implemented (`platform` returns `ErrUnsupported`).
- D1 (SQLite without RLS) still awaits owner sign-off.
