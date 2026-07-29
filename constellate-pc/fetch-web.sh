#!/usr/bin/env bash
# Downloads the Constellate web app into ./web so it can be embedded in the
# executable. The upstream project is MIT licensed; no copy of it is kept in
# this repository, it is fetched at build time at the pinned revision below.
set -euo pipefail

REPO="https://github.com/JCarterJohnson/constellate.git"
# Pinned so a build is reproducible. Bump deliberately, then re-test.
REV="31508ea5630b4404409b468fbab249126ab6c18f"

cd "$(dirname "$0")"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "Fetching Constellate web app at ${REV:0:12}..."
git init -q "$work"
git -C "$work" remote add origin "$REPO"
if ! git -C "$work" fetch -q --depth 1 origin "$REV"; then
  # Some servers refuse fetch-by-sha; fall back to the default branch.
  echo "Pinned revision unavailable by sha, fetching default branch instead." >&2
  git -C "$work" fetch -q --depth 1 origin HEAD
fi
git -C "$work" checkout -q FETCH_HEAD

# Fixes we carry against upstream. These are deliberately tiny and each patch
# file explains itself; git apply fails loudly if upstream has moved, which is
# the signal to re-check the fix rather than ship a silently unpatched build.
shopt -s nullglob
for patch in patches/*.patch; do
  echo "Applying $(basename "$patch")..."
  git -C "$work" apply "$(pwd)/$patch"
done
shopt -u nullglob

rm -rf web
mkdir -p web/vendor
cp "$work/index.html" web/index.html
cp "$work/vendor/three.min.js" web/vendor/three.min.js
cp "$work/LICENSE" web/UPSTREAM-LICENSE
git -C "$work" rev-parse HEAD > web/UPSTREAM-REVISION

echo "Web app staged in ./web (upstream revision $(cat web/UPSTREAM-REVISION))."
