#!/bin/sh
set -eu
repo=${1:?repository path}
sha=${2:?full release SHA}
test "$(git -C "$repo" rev-parse --verify "$sha^{commit}")" = "$sha"
stand=$(mktemp -d "${TMPDIR:-/tmp}/hint95-build.XXXXXX")
mkdir "$stand/source"
git -C "$repo" archive --output="$stand/source.tar" "$sha"
tar -xf "$stand/source.tar" -C "$stand/source"
cd "$stand/source"
export GOCACHE="$stand/go-cache"
CGO_ENABLED=0 go build -trimpath -o "$stand/hint" ./cmd/hint
{
  printf 'source_sha=%s\n' "$sha"
  go version
  go version -m "$stand/hint"
  if command -v sha256sum >/dev/null; then
    sha256sum "$stand/hint" "$stand/source.tar"
  else
    shasum -a 256 "$stand/hint" "$stand/source.tar"
  fi
} > "$stand/build.txt"
printf '%s\n' "$stand"
