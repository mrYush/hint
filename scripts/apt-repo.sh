#!/usr/bin/env bash
# Build a signed, static apt repository from the .deb files goreleaser
# produced (WP0.14). The output directory is what gets served over HTTPS;
# release.yml publishes it to the gh-pages branch.
#
# usage: scripts/apt-repo.sh <debs-dir> <out-dir>
#
# Environment:
#   APT_SIGNING_KEY         ASCII-armored private key (required)
#   APT_SIGNING_PASSPHRASE  passphrase of that key (optional)
#
# The repository holds only the debs passed in, so each run replaces the
# previous index: apt always sees exactly one (the current) version.
set -euo pipefail

debs_dir=${1:?usage: apt-repo.sh <debs-dir> <out-dir>}
out_dir=${2:?usage: apt-repo.sh <debs-dir> <out-dir>}
: "${APT_SIGNING_KEY:?APT_SIGNING_KEY is not set}"

suite=stable
component=main
archs=(amd64 arm64)

shopt -s nullglob
debs=("$debs_dir"/*.deb)
if ((${#debs[@]} == 0)); then
  echo "no .deb files in $debs_dir" >&2
  exit 1
fi

rm -rf "$out_dir"
mkdir -p "$out_dir/pool/$component"
cp "${debs[@]}" "$out_dir/pool/$component/"

export GNUPGHOME
GNUPGHOME=$(mktemp -d)
trap 'rm -rf "$GNUPGHOME"' EXIT
chmod 700 "$GNUPGHOME"
gpg_opts=(--batch --yes --pinentry-mode loopback)
if [[ -n ${APT_SIGNING_PASSPHRASE:-} ]]; then
  gpg_opts+=(--passphrase "$APT_SIGNING_PASSPHRASE")
else
  gpg_opts+=(--passphrase "")
fi
gpg "${gpg_opts[@]}" --import <<<"$APT_SIGNING_KEY" 2>/dev/null

cd "$out_dir"

for arch in "${archs[@]}"; do
  bin_dir=dists/$suite/$component/binary-$arch
  mkdir -p "$bin_dir"
  # -a limits the index to one architecture; the pool holds both.
  apt-ftparchive --arch "$arch" packages "pool/$component" >"$bin_dir/Packages"
  gzip -9nk "$bin_dir/Packages"
done

apt-ftparchive \
  -o "APT::FTPArchive::Release::Origin=hint" \
  -o "APT::FTPArchive::Release::Label=hint" \
  -o "APT::FTPArchive::Release::Suite=$suite" \
  -o "APT::FTPArchive::Release::Codename=$suite" \
  -o "APT::FTPArchive::Release::Architectures=${archs[*]}" \
  -o "APT::FTPArchive::Release::Components=$component" \
  release "dists/$suite" >"dists/$suite/Release"

gpg "${gpg_opts[@]}" --clearsign -o "dists/$suite/InRelease" "dists/$suite/Release"
gpg "${gpg_opts[@]}" --detach-sign -o "dists/$suite/Release.gpg" "dists/$suite/Release"

# Public half: binary keyring for signed-by=, armored copy for humans.
gpg --batch --export >hint.gpg
gpg --batch --armor --export >hint.asc

# Pages would otherwise run Jekyll over the tree.
touch .nojekyll
