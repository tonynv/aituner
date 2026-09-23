# TASKS

Legend: [x] done, [~] in progress, [ ] next. Build order: DataStore → API → UI.

## Research / design
- [x] Clone repo (was empty), probe reference hardware and toolchain
- [x] Verify canirun.ai API (live), mlx-lm benchmark interface, Ollama env vars + stats fields, HF MLX API
- [x] SPEC written (SPECS/SPEC.md)

## Build
- [ ] T1 Go module, store (SQLite, migrations, tenant-scoped repo) + isolation tests
- [ ] T2 platform interface + darwin detect (hardware, runtimes, thermal, wired limit)
- [ ] T3 API server: auth/host/origin middleware, phase gate, SSE, /hardware
- [ ] T4 benchmark suites (mem bw, MLX GPU, MLX LLM, Ollama LLM) + runtime setup
- [ ] T5 tuning engine (wired limit, persist daemon, Ollama env) + diff/apply/revert
- [ ] T6 canirun + HF clients, recommendation pipeline (post-tune only)
- [ ] T7 Svelte UI (5 steps, dark/light, PWA, mobile)
- [ ] T8 Bubble Tea TUI + run_aituner.sh preflight
- [ ] T9 End-to-end run on this Mac; Playwright UI QA (dark/light/mobile)
- [ ] T10 Linux implementation (future)

## Open
- D1 RLS deviation sign-off; image-gen runtime choice [TBD]
