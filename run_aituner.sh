#!/usr/bin/env bash
# aituner preflight + launcher. Idempotent: every run checks the toolchain, updates outdated
# Homebrew-managed tools to the latest stable, rebuilds only what changed, then starts aituner.
# It never uses sudo. Extra arguments are passed to aituner (e.g. --no-tui, --no-open, --port N).
# Set AITUNER_SKIP_UPDATES=1 to install what is missing but leave existing tools at their versions.
set -euo pipefail
cd "$(dirname "$0")"
. scripts/preflight.sh

ensure_toolchain
build_web
build_aituner bin/aituner

command -v ollama >/dev/null 2>&1 && say "ollama: found" || say "ollama: not installed (optional; MLX benchmarks still work)"

say "starting aituner"
exec bin/aituner "$@"
