#!/usr/bin/env bash
#
# Rebuild every released target from a git ref and write its checksums.
#
# This is the documented command a third party runs to check a release: check
# out the tag, run this script, and compare the checksums it writes against the
# `checksums.txt` attached to the GitHub release. If they match, the published
# binaries are exactly what this source produces, and nothing was changed in
# between.
#
# Usage:
#
#	scripts/reproduce.sh <git-ref> [outdir]
#
# <git-ref> is a tag (e.g. v0.5.0) or any commit-ish. [outdir] defaults to
# ./reproduced. The script writes one binary per target and a checksums.txt
# under [outdir].
#
# The build metadata is derived from the ref, never from the wall clock: the
# timestamp is the commit's committer date, so the same ref always stamps the
# same bytes. See docs/reproducibility.md for the full list of sources of
# non-determinism this neutralises.
#
# Exit status: 0 on success, 1 on a usage or build error.
set -euo pipefail

usage() {
	echo "usage: $(basename "$0") <git-ref> [outdir]" >&2
	exit 1
}

[ "$#" -ge 1 ] || usage
ref="$1"
out="${2:-$(pwd)/reproduced}"

# Every command below has to run at the repository root: `go build` resolves
# ./cmd/soroauth relative to the working directory, and the release workflow
# builds from the checkout root.
cd "$(git rev-parse --show-toplevel)"

# The commit the ref points at. For an annotated tag this is the commit, not
# the tag object, which is what the release workflow stamps.
commit="$(git rev-list -n1 "$ref")"

# The version string stamped into the binary. When the ref is exactly a tag,
# that tag is the version; otherwise fall back to `git describe`, which is a
# function of the ref and the reachable tags alone.
if version="$(git describe --tags --exact-match "$ref" 2>/dev/null)"; then
	:
else
	version="$(git describe --tags --always "$ref")"
fi

build_date="$(git show -s --format=%cI "$commit")"
export SOURCE_DATE_EPOCH
SOURCE_DATE_EPOCH="$(git show -s --format=%ct "$commit")"

# macOS has no sha256sum; shasum -a 256 writes the same "hash  file" line.
if command -v sha256sum >/dev/null 2>&1; then
	checksum() { sha256sum "$@"; }
elif command -v shasum >/dev/null 2>&1; then
	checksum() { shasum -a 256 "$@"; }
else
	echo "need sha256sum or shasum to checksum the build output" >&2
	exit 1
fi

mkdir -p "$out"
out="$(cd "$out" && pwd)"

# The same five targets the release workflow builds. If this list changes, the
# matrix in .github/workflows/release.yml must change with it.
targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64"

for target in $targets; do
	goos="${target%%/*}"
	goarch="${target##*/}"
	asset="soroauth_${goos}_${goarch}"
	if [ "$goos" = "windows" ]; then
		asset="${asset}.exe"
	fi

	echo "building ${asset} (${version}, ${commit}, ${build_date})"
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -buildvcs=false \
		-ldflags="-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${build_date}" \
		-o "$out/$asset" ./cmd/soroauth
done

# Sorted by filename so the file is byte-stable regardless of build order.
(
	cd "$out"
	# shellcheck disable=SC2012 # the names are known and space-free
	checksum soroauth_* | sort -k2 >checksums.txt
)

echo "wrote $out/checksums.txt"
