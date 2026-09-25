#!/usr/bin/env bash
# Builds aituner.app and a drag-to-Applications disk image (dist/aituner.dmg) from this checkout.
# Idempotent: every run checks the toolchain (updating outdated Homebrew tools to the latest stable, like
# run_aituner.sh), rebuilds from scratch into dist/, and signs the app for this Mac (ad hoc).
#   ./build_app.sh             build dist/aituner.app and dist/aituner.dmg
#   ./build_app.sh --install   also copy it to /Applications (replacing an older copy; quit aituner first)
set -euo pipefail
cd "$(dirname "$0")"
. scripts/preflight.sh

install=0
case "${1:-}" in
  "") ;;
  --install) install=1 ;;
  *) die "unknown option: $1 (use --install or nothing)" ;;
esac

xcrun --find swiftc >/dev/null 2>&1 || die "the Xcode Command Line Tools are required: run xcode-select --install, then re-run."
ensure_toolchain
build_web

out=dist
app=$out/aituner.app
work=$out/.work
rm -rf "$app" "$work" "$out/aituner.dmg"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$work"

build_aituner "$app/Contents/MacOS/aituner-server"

say "building the app shell"
xcrun swiftc -O -target arm64-apple-macos14.0 -o "$app/Contents/MacOS/aituner" macos/App.swift

say "rendering the icon"
xcrun swiftc -O -o "$work/make_icon" macos/make_icon.swift
"$work/make_icon" web/public/icon.svg "$work/aituner.iconset"
iconutil -c icns -o "$app/Contents/Resources/aituner.icns" "$work/aituner.iconset"

# CFBundleShortVersionString must be numeric: the latest vX.Y.Z tag, else the web package version.
short=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null | sed 's/^v//' || true)
[ -n "$short" ] || short=$(node -p "require('./web/package.json').version")
build=$(git rev-list --count HEAD 2>/dev/null || echo 1)
sed -e "s/@SHORT_VERSION@/$short/" -e "s/@BUILD@/$build/" macos/Info.plist > "$app/Contents/Info.plist"
plutil -lint -s "$app/Contents/Info.plist"

say "signing (ad hoc, hardened runtime)"
codesign --force --options runtime --sign - "$app/Contents/MacOS/aituner-server"
codesign --force --options runtime --sign - "$app"
codesign --verify --strict --deep "$app"

say "making the disk image"
mkdir -p "$work/dmg"
cp -R "$app" "$work/dmg/"
ln -s /Applications "$work/dmg/Applications"
hdiutil create -quiet -volname aituner -srcfolder "$work/dmg" -fs APFS -format UDZO -ov "$out/aituner.dmg"
rm -rf "$work"
say "built $app ($short, build $build, $(aituner_version)) and $out/aituner.dmg"

if [ "$install" = 1 ]; then
  pgrep -xq aituner-server && die "aituner is running: quit it (menu bar icon, Quit) and re-run with --install."
  say "installing to /Applications"
  rm -rf /Applications/aituner.app
  ditto "$app" /Applications/aituner.app
  say "installed /Applications/aituner.app"
fi
