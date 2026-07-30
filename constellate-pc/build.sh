#!/usr/bin/env bash
# Cross-compiles Constellate.exe for Windows from any machine with Go installed
# (Linux, macOS, or Windows with bash). No Windows toolchain is needed: the
# launcher is pure Go with cgo off.
#
#   ./build.sh              # both architectures
#   ./build.sh amd64        # just Intel/AMD 64-bit
set -euo pipefail

cd "$(dirname "$0")"
./fetch-web.sh

mkdir -p dist
arches=("${@:-amd64 arm64}")
read -r -a arches <<<"${arches[*]}"

for arch in "${arches[@]}"; do
  case "$arch" in
    amd64) suffix="" ;;         # the one almost everyone wants
    arm64) suffix="-arm64" ;;   # Snapdragon / Surface on ARM
    *) echo "unknown architecture: $arch" >&2; exit 1 ;;
  esac
  echo "Building dist/Constellate$suffix.exe (windows/$arch)..."
  # -H=windowsgui: no console window on double-click. -s -w: strip debug info.
  CGO_ENABLED=0 GOOS=windows GOARCH="$arch" \
    go build -trimpath -ldflags '-s -w -H=windowsgui' -o "dist/Constellate$suffix.exe" .
  # These two are console tools, so they keep their console and print normally.
  for tool in "Export-CodeSessions:./cmd/codesessions" "Make-Phone-Bundle:./cmd/bundle"; do
    name="${tool%%:*}"; pkg="${tool##*:}"
    echo "Building dist/$name$suffix.exe (windows/$arch)..."
    CGO_ENABLED=0 GOOS=windows GOARCH="$arch" \
      go build -trimpath -ldflags '-s -w' -o "dist/$name$suffix.exe" "$pkg"
  done
done

echo
ls -lh dist/
