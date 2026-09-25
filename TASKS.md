# TASKS

Legend: [x] done, [~] partial, [ ] not done. Build order: DataStore -> API -> UI.

## Research / design
- [x] Clone repo, probe hardware/toolchain; verify canirun.ai API, mlx-lm, Ollama, HF API, mflux against official sources
- [x] SPEC written, pushed, and kept current with reality

## Build (branch build/v0.1, pushed)
- [x] T1 Datastore: SQLite, migrations, tenant-scoped repo, phase state machine, isolation tests
- [x] T2 Platform + macOS detection (real-capture fixture + live test); health checks (battery/thermal/load/memory)
- [x] T3 API: single-use launch links, token/Host/Origin/CSP, phase gates, single-job runner + SSE, crash recovery
- [x] T4 Benchmarks (memory, MLX GPU/LLM, Ollama), 5 trials, robust noise, disturbed-trial warnings
- [x] T5 Tuning: measured plan, opt-in per change, hardened admin runner, verified apply/revert, rollback, LaunchDaemon + Ollama LaunchAgent persistence
- [x] T6 canirun.ai + HF clients (retry/backoff), recommendations (MLX resolution, arch gate, calibrated speed, unrestricted variants, validated commands)
- [x] T7 Svelte 5 PWA: 5 steps, dark/light, diff review, comparison, models, mobile, offline shell
- [x] T8 Bubble Tea v2 TUI, run_aituner.sh preflight, single-instance lock, --version
- [x] T9 Quality: -race, govulncheck, npm audit, svelte-check, fuzzing, live contract tests, fault injection (cancel), PWA/offline in Chrome
- [x] T10 Release gates run by independent agents: code review, security, UI/UX, deps, manager, QA (findings fixed)
- [x] T12 KV-cache meter (per-layer-type KV from real configs), list/grid views, configurable models folder, verified resumable downloads, audit log, New-run confirmation
- [ ] T11 Linux implementation (out of scope for now; `platform` returns ErrUnsupported)

## Phase 2 (SPEC section 16), in order
- [x] P1 Richer benchmark suites (prompt sweep, context depth, TTFT, sustained/throttle) + derived roofline/utilisation; GPU test order/warm-up fixed
- [x] P2 Report view (honest charts, history, compare, export JSON/MD/CSV) + pinned stats bar on every tab
- [x] P3 Model server manager (mlx_lm.server, offline-only, pidfile/reaping, log file) + MLX install/update action
- [x] P4 Gateway: API key, model-pinned allow-listed OpenAI proxy, Anthropic Messages translation (streaming, tools, tolerant tool-call recovery), tested on real captures
- [x] P5 Run view (runtime, start/stop, connection details, logs, serving chip)
- [x] P6 Claude Code   - [x] P7 VS Code (isolated profile)   - [x] P8 Neovim (own NVIM_APPNAME)   - [x] P9 Vim + tmux   - [x] P10 generic OpenAI endpoint
- [ ] P11 Review gates (code, security, UI/UX, QA) on the new surfaces; docs

## Needs the owner
- D1: SQLite has no RLS (app-level tenant scoping + isolation test). Accept, or switch to local PostgreSQL?
- Approve the macOS admin dialog once to exercise the wired-limit apply (the only path not run end to end)
- Cut v0.1.0 (annotated tag, no attribution) and merge build/v0.1 into main when satisfied

## P12 Flow: detect -> downloads (queue) -> setup
- [x] Fresh run + detection on every launch; home page is Hardware
- [x] Recommendations and downloads available right after detection (no benchmark gate)
- [x] Server-side download queue (ordered, one at a time, remove/retry) + queue panel at top of Downloads
- [x] Shared MLX install card on Downloads and Setup; Setup unlocks after a download
- [x] Pinned stats fall back to the last measured run
- [ ] Re-run QA (tonynv-qa) on the new flow, dark/light, mobile viewport
