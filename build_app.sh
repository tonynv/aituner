#!/usr/bin/env bash
# Builds aituner.app, a drag-to-Applications disk image and a zip (for the Homebrew cask) from this checkout.
# Idempotent: every run checks the toolchain (updating outdated Homebrew tools to the latest stable, like
# run_aituner.sh) and rebuilds from scratch into dist/.
#   ./build_app.sh             build dist/aituner.app, dist/aituner-<version>.dmg and .zip, and dist/SHA256SUMS
#   ./build_app.sh --install   also copy it to /Applications (replacing an older copy; quit aituner first)
# Signing: ad hoc (this Mac only) unless AITUNER_TEAM_ID is set: the keychain's "Developer ID Application" identity for
# that team is then used (AITUNER_SIGN_IDENTITY overrides with an explicit identity or SHA-1). Notarization (Developer ID only) runs when AITUNER_NOTARY_KEY (path to an App Store Connect API .p8),
# AITUNER_NOTARY_KEY_ID and AITUNER_NOTARY_ISSUER are set; the app and disk image are then stapled.
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
rm -rf "$app" "$work" "$out"/aituner*.dmg "$out"/aituner*.zip "$out/SHA256SUMS"
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

identity=${AITUNER_SIGN_IDENTITY:--}
if [ "$identity" = "-" ] && [ -n "${AITUNER_TEAM_ID:-}" ]; then
  # the certificate carries the enrolled entity's legal name; select it by team ID so no name lives in this repo
  identity=$(security find-identity -v -p codesigning | awk -v t="(${AITUNER_TEAM_ID})" '/Developer ID Application/ && index($0, t) { print $2; exit }')
  [ -n "$identity" ] || die "no Developer ID Application identity for team ${AITUNER_TEAM_ID} in the keychain"
fi
sign() { codesign --force --options runtime --timestamp$([ "$identity" = "-" ] && echo "=none") --sign "$identity" "$@"; }
if [ "$identity" = "-" ]; then say "signing (ad hoc, hardened runtime: runs on this Mac)"; else say "signing with Developer ID ${AITUNER_TEAM_ID:-$identity} (hardened runtime)"; fi
sign "$app/Contents/MacOS/aituner-server"
sign "$app"
codesign --verify --strict --deep "$app"

notarize() { # notarize <file>: submit to Apple and wait; fails the build if Apple rejects it
  xcrun notarytool submit "$1" --key "$AITUNER_NOTARY_KEY" --key-id "$AITUNER_NOTARY_KEY_ID" --issuer "$AITUNER_NOTARY_ISSUER" --wait --timeout 30m
}
notary=0
if [ -n "${AITUNER_NOTARY_KEY:-}" ]; then
  [ "$identity" != "-" ] || die "notarization needs a Developer ID identity (AITUNER_SIGN_IDENTITY)"
  [ -n "${AITUNER_NOTARY_KEY_ID:-}" ] && [ -n "${AITUNER_NOTARY_ISSUER:-}" ] || die "set AITUNER_NOTARY_KEY_ID and AITUNER_NOTARY_ISSUER"
  notary=1
  say "notarizing the app"
  ditto -c -k --keepParent "$app" "$work/notarize.zip"
  notarize "$work/notarize.zip"
  xcrun stapler staple "$app"
  xcrun stapler validate "$app"
fi

zip="$out/aituner-$short.zip"
dmg="$out/aituner-$short.dmg"
say "packaging"
ditto -c -k --keepParent "$app" "$zip"
mkdir -p "$work/dmg"
cp -R "$app" "$work/dmg/"
ln -s /Applications "$work/dmg/Applications"
hdiutil create -quiet -volname aituner -srcfolder "$work/dmg" -fs APFS -format UDZO -ov "$dmg"
if [ "$identity" != "-" ]; then
  codesign --force --timestamp --sign "$identity" "$dmg"
  if [ "$notary" = 1 ]; then say "notarizing the disk image"; notarize "$dmg"; xcrun stapler staple "$dmg"; fi
fi
(cd "$out" && shasum -a 256 "$(basename "$zip")" "$(basename "$dmg")" > SHA256SUMS)
rm -rf "$work"
say "built $app ($short, build $build, $(aituner_version)), $zip, $dmg$([ "$notary" = 1 ] && echo ", notarized")"

if [ "$install" = 1 ]; then
  pgrep -xq aituner-server && die "aituner is running: quit it (menu bar icon, Quit) and re-run with --install."
  say "installing to /Applications"
  rm -rf /Applications/aituner.app
  ditto "$app" /Applications/aituner.app
  say "installed /Applications/aituner.app"
fi
