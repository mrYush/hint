#!/bin/sh
set -eu
export GOTOOLCHAIN=go1.26.10

revision=53264f88ad6900abab31c8e50e1281c0381ff45f
repo=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
mkdir -p "${1:?Usage: build-hint.sh OUTPUT_DIR}"
output=$(cd "$1" && pwd)
source=$(mktemp -d)
trap 'rm -rf "$source"' EXIT
git -C "$repo" archive "$revision" > "$source/source.tar"
tar -xf "$source/source.tar" -C "$source"
cd "$source"
for arch in amd64 arm64; do
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -buildvcs=false \
        -ldflags "-X main.version=harbor-baseline -X main.commit=$revision" \
        -o "$output/hint-linux-$arch" ./cmd/hint
done
cd "$output"
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum hint-linux-amd64 hint-linux-arm64 > SHA256SUMS
else
    shasum -a 256 hint-linux-amd64 hint-linux-arm64 > SHA256SUMS
fi
