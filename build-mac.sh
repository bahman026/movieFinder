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

echo "Done: dist/MovieFinder.app ($(du -sh dist/MovieFinder.app | cut -f1)), a macOS $goarch app"
