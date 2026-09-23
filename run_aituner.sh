#!/usr/bin/env bash
# aituner preflight + launcher. Idempotent: every run checks the toolchain, updates outdated
# Homebrew-managed tools to the latest stable, rebuilds only what changed, then starts aituner.
# It never uses sudo. Extra arguments are passed to aituner (e.g. --no-tui, --no-open, --port N).
# Set AITUNER_SKIP_UPDATES=1 to install what is missing but leave existing tools at their versions.
set -euo pipefail
cd "$(dirname "$0")"

say()  { printf '==> %s\n' "$*"; }
die()  { printf 'aituner: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = "Darwin" ] || die "$(uname -s) is not supported yet; aituner currently supports macOS on Apple Silicon."
[ "$(uname -m)" = "arm64" ]  || die "Apple Silicon (arm64) is required; MLX does not run on Intel Macs."

command -v brew >/dev/null 2>&1 || [ -x /opt/homebrew/bin/brew ] || die "Homebrew is required. Install it from https://brew.sh, then re-run."
eval "$(/opt/homebrew/bin/brew shellenv 2>/dev/null || brew shellenv)"

# ensure_formula <formula> <command-that-must-exist>
# Installs it if missing; upgrades it if Homebrew manages it and it is outdated.
ensure_formula() {
  local formula=$1 cmd=$2
  if ! command -v "$cmd" >/dev/null 2>&1; then
    say "installing $formula"
    brew install "$formula"
  elif [ "${AITUNER_SKIP_UPDATES:-0}" != "1" ] && brew list --formula "$formula" >/dev/null 2>&1 && [ -n "$(brew outdated --formula --quiet "$formula")" ]; then
    say "updating $formula to the latest stable"
    brew upgrade "$formula"
  fi
}

ensure_formula go go
ensure_formula node node
ensure_formula python@3.14 python3.14

command -v python3.14 >/dev/null 2>&1 || die "python3.14 not found after install"
say "toolchain: $(go version | cut -d' ' -f3), node $(node --version), $(python3.14 --version)"

# Web UI: rebuild when sources, lockfile or config are newer than the build.
stamp=internal/webui/dist/index.html
if [ ! -f "$stamp" ] || [ -n "$(find web/src web/index.html web/public web/package.json web/package-lock.json web/vite.config.js -newer "$stamp" -type f 2>/dev/null | head -1)" ]; then
  say "building web UI"
  (cd web
   if [ ! -d node_modules ] || [ package-lock.json -nt node_modules ]; then npm ci --no-audit --no-fund; fi
   npm run build --silent)
fi

# Go binary: `go build` is incremental, so this is cheap when nothing changed.
say "building aituner"
mkdir -p bin
go build -o bin/aituner ./cmd/aituner

command -v ollama >/dev/null 2>&1 && say "ollama: found" || say "ollama: not installed (optional; MLX benchmarks still work)"

say "starting aituner"
exec bin/aituner "$@"
