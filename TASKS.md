# TASKS

Legend: [x] done, [~] partial, [ ] not done. Build order: DataStore -> API -> UI.

## Research / design
- [x] Clone repo (was empty), probe reference hardware and toolchain
- [x] Verify canirun.ai API (live), mlx-lm benchmark interface, Ollama env vars + stats fields, HF MLX API, mflux
- [x] SPEC written and pushed (SPECS/SPEC.md), updated to reality after build

## Build (branch build/v0.1)
- [x] T1 Datastore: SQLite, migrations, tenant-scoped repo, phase state machine + isolation tests
- [x] T2 Platform interface + macOS detection (hardware, runtimes, thermal, wired limit); real-capture fixture + live test
- [x] T3 API: token/Host/Origin/CSP, phase gates, single-job runner + SSE, crash recovery (12+ tests)
- [x] T4 Benchmarks: CPU memory (Go), GPU + LLM (MLX), Ollama; comparison with noise band; runtime setup
- [x] T5 Tuning: measured plan, per-change opt-in, hardened admin runner, verified apply/revert, rollback on failure
- [x] T6 canirun.ai + Hugging Face clients, recommendation pipeline (MLX resolution, arch gate, calibrated speed, unrestricted variants)
- [x] T7 Svelte 5 PWA: 5 steps, dark/light, diff review, comparison, recommendations, mobile
- [x] T8 Bubble Tea v2 TUI + run_aituner.sh preflight
- [~] T9 End-to-end on this Mac: full flow verified in Chrome (Playwright) incl. real benchmark, real Ollama tune apply + revert,
      light and dark, 390px mobile. NOT verified: admin-approved wired-limit apply (needs a human at the dialog).
- [ ] T10 Linux implementation
- [ ] Release gates (manager / UI-UX / security / QA agents) not run: no release requested

## Open
- D1 RLS deviation sign-off; PWA install + offline shell not exercised in a real install; Ollama env persistence
