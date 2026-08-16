#!/usr/bin/env bash
# Builds MovieFinder.app for macOS and drops it in ./dist
# Usage: ./build-mac.sh [arm64|amd64]        (defaults to this Mac's own type)
#
# Unlike the Windows and Linux builds, this one does NOT use Docker. Fyne needs
# CGO against the macOS Cocoa frameworks, and those come from the macOS SDK,
# which only exists on a Mac — a Linux container cannot cross-compile them. So
# this script uses the native Go toolchain instead.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$root"

# Which chip to build for. arm64 = Apple silicon (M1 and later), amd64 = Intel.
case "${1:-$(uname -m)}" in
    arm64 | aarch64) goarch=arm64 ;;
    x86_64 | amd64) goarch=amd64 ;;
    *)
        echo "unknown architecture: ${1:-$(uname -m)} (use arm64 or amd64)" >&2
        exit 1
        ;;
esac

if ! command -v go > /dev/null 2>&1; then
    echo "Go is not installed. Run:  brew install go" >&2
    exit 1
fi
if ! xcode-select -p > /dev/null 2>&1; then
    echo "Xcode Command Line Tools are missing. Run:  xcode-select --install" >&2
    exit 1
fi

mkdir -p dist

echo "Building MovieFinder for darwin/$goarch..."
CGO_ENABLED=1 GOOS=darwin GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w" -o dist/MovieFinder ./cmd/moviefinder

# The fyne packager rewrites FyneApp.toml in place (it bumps Build and drops
# empty fields), which would show up as an unwanted change in git. Keep a copy
# and restore it however this script exits.
backup="$root/.FyneApp.toml.bak"
cp FyneApp.toml "$backup"
restore() { [ -f "$backup" ] && mv -f "$backup" "$root/FyneApp.toml"; }
trap restore EXIT

# Wrap the binary in a .app bundle so it gets its icon and name in the Dock.
# --exe reuses the binary built above rather than compiling a second time.
rm -rf dist/MovieFinder.app MovieFinder.app
go run fyne.io/tools/cmd/fyne@latest package \
    -os darwin --exe dist/MovieFinder --icon Icon.png \
    --name MovieFinder --app-id com.moviefinder.app

mv MovieFinder.app dist/

# Re-sign the finished bundle. The linker ad-hoc signs the bare executable, but
# that signature does not cover the .app wrapper around it, so `codesign -v`
# reports "code has no resources but signature indicates they must be present".
# macOS requires a valid signature on Apple silicon, so an invalid one is not a
# warning — the app is rejected outright as "damaged and can't be opened", which
# only shows up once someone else opens it. Ad-hoc (-s -) is enough to make the
# bundle internally consistent; it is not an Apple Developer ID, so Gatekeeper
# still asks the user to approve the app the first time.
codesign --force --deep -s - dist/MovieFinder.app
codesign --verify --strict dist/MovieFinder.app

# A .app is a directory, so it cannot be attached to a chat app or emailed as
# is. ditto (not zip) is what preserves bundle symlinks and metadata, and keeps
# the signature valid through the round trip.
zip="MovieFinder-mac-$goarch.zip"
rm -f "dist/$zip"
(cd dist && ditto -c -k --sequesterRsrc --keepParent MovieFinder.app "$zip")

echo "Done: dist/MovieFinder.app ($(du -sh dist/MovieFinder.app | cut -f1)), a macOS $goarch app"
echo "To share: dist/$zip ($(du -sh "dist/$zip" | cut -f1))"
