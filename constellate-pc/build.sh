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
    amd64) out="dist/Constellate.exe" ;;        # the one almost everyone wants
    arm64) out="dist/Constellate-arm64.exe" ;;  # Snapdragon / Surface on ARM
    *) echo "unknown architecture: $arch" >&2; exit 1 ;;
  esac
  echo "Building $out (windows/$arch)..."
  # -H=windowsgui: no console window on double-click. -s -w: strip debug info.
  CGO_ENABLED=0 GOOS=windows GOARCH="$arch" \
    go build -trimpath -ldflags '-s -w -H=windowsgui' -o "$out" .
done

echo
ls -lh dist/
