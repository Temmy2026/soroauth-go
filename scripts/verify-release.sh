#!/usr/bin/env bash
#
# verify-release.sh verifies the supply-chain evidence attached to a soroauth
# release the way a consumer can, with no key of their own:
#
#   * every released binary against its detached Sigstore bundle
#     (cosign verify-blob, keyless) and against the build provenance
#     attestation GitHub stores for it (gh attestation verify);
#   * the SPDX SBOM attached next to each binary against its own attestation,
#     and the attached file against the attested predicate;
#   * the container image digest the release names, the same two ways.
#
# With --tamper-demo it copies one verified binary, flips a single byte, and
# requires BOTH verification paths to reject the copy. A tampered binary that
# still verifies means the gate does not bite, so that is a non-zero exit: the
# demo is negative evidence, not a formality.
#
# Keyless is the whole point: the certificate behind a bundle is issued against
# the release workflow's GitHub OIDC identity, so nothing in this repository or
# in CI holds a long-lived signing key. The default identity policy is the
# upstream release workflow at a tag. A run of the verification workflow on a
# branch (see .github/workflows/verify-release.yml, input self_test) passes
# --signer-workflow and --identity-regexp instead.
#
# Usage:
#   scripts/verify-release.sh                        # latest release, downloaded
#   scripts/verify-release.sh --tag v0.2.0           # one tag, downloaded
#   scripts/verify-release.sh --dir release-assets   # already-downloaded assets
#   scripts/verify-release.sh --dir release-assets --tamper-demo
#   scripts/verify-release.sh --dir self-test-dist \
#     --repo OWNER/REPO \
#     --signer-workflow OWNER/REPO/.github/workflows/verify-release.yml \
#     --identity-regexp '^https://github.com/OWNER/REPO/.github/workflows/verify-release.yml@refs/'
#
# --dir verifies an existing directory (--tag is then ignored); without it the
# assets are downloaded with `gh release download` into a temporary directory.
# The image digest is read from $dir/soroauth-image-digest.txt when the release
# carries it, or from --image-digest; with neither, image verification is
# skipped with an explicit message. --image overrides the image name
# (default ghcr.io/OWNER/REPO).
#
# Requires cosign and gh on PATH, and gh must be authenticated
# (`gh auth login` or GH_TOKEN); attestation lookup reads GitHub's API.
#
# Exit status: 0 everything verified; 1 a verification failed, a required
# asset was missing, or --tamper-demo saw a tampered copy verify; 2 usage.

set -euo pipefail

issuer='https://token.actions.githubusercontent.com'
digest_asset='soroauth-image-digest.txt'
spdx_predicate_base='https://spdx.dev/Document'

repo='soroauth/soroauth-go'
signer_workflow=''
identity_regexp=''
tag=''
dir=''
image=''
image_digest=''
tamper=0
workdir=''
scratch=''

binaries=()

usage() {
  sed -n '/^# Usage:/,/^# Exit status:/p' "$0" | sed 's/^# \{0,1\}//'
}

die() {
  echo "verify-release: $*" >&2
  exit 1
}

on_exit() {
  local d
  for d in "$workdir" "$scratch"; do
    if [ -n "$d" ] && [ -d "$d" ]; then
      rm -rf "$d"
    fi
  done
}
trap on_exit EXIT

# scratch_dir prints (and creates once) a private directory for the predicate
# files and the tampered copy, cleaned up on exit.
scratch_dir() {
  if [ -z "$scratch" ]; then
    scratch="$(mktemp -d)"
  fi
  printf '%s\n' "$scratch"
}

# run prints a command and runs it, so the transcript names what verified what.
run() {
  printf '+ %s\n' "$*" >&2
  "$@"
}

# must_reject runs a command that is expected to fail, printing the command and
# its output either way. Returns 0 when the command failed (the expectation)
# and 1 when it succeeded.
must_reject() {
  local out status=0
  printf '+ %s\n' "$*" >&2
  out="$("$@" 2>&1)" || status=$?
  if [ -n "$out" ]; then
    printf '%s\n' "$out"
  fi
  if [ "$status" -eq 0 ]; then
    return 1
  fi
  echo "  (rejected: exit status ${status})"
  return 0
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --repo)
        [ $# -ge 2 ] || die "--repo needs a value"
        repo="$2"
        shift 2
        ;;
      --signer-workflow)
        [ $# -ge 2 ] || die "--signer-workflow needs a value"
        signer_workflow="$2"
        shift 2
        ;;
      --identity-regexp)
        [ $# -ge 2 ] || die "--identity-regexp needs a value"
        identity_regexp="$2"
        shift 2
        ;;
      --tag)
        [ $# -ge 2 ] || die "--tag needs a value"
        tag="$2"
        shift 2
        ;;
      --dir)
        [ $# -ge 2 ] || die "--dir needs a value"
        dir="$2"
        shift 2
        ;;
      --image)
        [ $# -ge 2 ] || die "--image needs a value"
        image="$2"
        shift 2
        ;;
      --image-digest)
        [ $# -ge 2 ] || die "--image-digest needs a value"
        image_digest="$2"
        shift 2
        ;;
      --tamper-demo)
        tamper=1
        shift
        ;;
      -h | --help)
        usage
        exit 0
        ;;
      -*)
        die "unknown flag '$1' (see --help)"
        ;;
      *)
        [ -z "$dir" ] || die "more than one directory given ('$dir' and '$1')"
        dir="$1"
        shift
        ;;
    esac
  done

  case "$repo" in
    */*) ;;
    *) die "--repo must be OWNER/REPO, got '$repo'" ;;
  esac
  [ -n "$signer_workflow" ] || signer_workflow="${repo}/.github/workflows/release.yml"
  # The certificate identity is a URL: https://github.com/<workflow>@<ref>.
  # A signed release runs from a tag, so the release policy ends there.
  [ -n "$identity_regexp" ] || identity_regexp="^https://github.com/${signer_workflow}@refs/tags/"

  case "$image_digest" in
    '') ;;
    *@*)
      # Accept a full image reference as well as a bare digest.
      image="${image_digest%@*}"
      image_digest="${image_digest##*@}"
      ;;
    *) ;;
  esac
  [ -n "$image" ] || image="ghcr.io/${repo}"
}

preflight() {
  command -v cosign >/dev/null 2>&1 ||
    die "cosign is not installed (or not on PATH). Install it from https://docs.sigstore.dev/cosign/system_config/installation/ and re-run; verification is nothing without it."
  command -v gh >/dev/null 2>&1 ||
    die "gh is not installed (or not on PATH). Install it from https://cli.github.com/ and re-run; verification is nothing without it."
  if ! gh auth status >/dev/null 2>&1; then
    die "gh is not authenticated. Run 'gh auth login', or set GH_TOKEN, so attestations can be read from GitHub's API."
  fi
}

# resolve_dir fills $dir with the directory of release assets to verify,
# downloading the release into a temporary directory when none was given.
resolve_dir() {
  if [ -n "$dir" ]; then
    return 0
  fi
  workdir="$(mktemp -d)"
  dir="$workdir"
  if [ -z "$tag" ]; then
    tag="$(gh release view --repo "$repo" --json tagName --jq .tagName)" ||
      die "cannot determine the latest release of $repo"
    [ -n "$tag" ] || die "$repo has no published release"
  fi
  echo "verify-release: downloading the assets of ${repo}@${tag}" >&2
  run gh release download "$tag" --repo "$repo" --dir "$dir" ||
    die "downloading the release assets of ${tag} failed"
}

# collect_binaries fills the binaries array with soroauth-<os>-<arch>[.exe]
# files, excluding the SBOMs, bundles and bookkeeping files next to them.
collect_binaries() {
  [ -d "$dir" ] || die "$dir is not a directory"
  local f name
  for f in "$dir"/soroauth-*; do
    [ -f "$f" ] || continue
    name="$(basename "$f")"
    case "$name" in
      *.sigstore.json | *.spdx.json | *.json | *.txt | *.1) continue ;;
    esac
    binaries+=("$f")
  done
  if [ "${#binaries[@]}" -eq 0 ]; then
    die "no release binaries found in $dir: expected soroauth-<os>-<arch>[.exe] next to their .sigstore.json bundles and .spdx.json SBOMs"
  fi
  # Stable order so runs and logs are comparable.
  mapfile -t binaries < <(printf '%s\n' "${binaries[@]}" | sort)
}

# sbom_predicate_type reads the SPDX version out of the attached SBOM and
# builds the predicate type actions/attest uses for it, e.g.
#   "spdxVersion": "SPDX-2.3"  ->  https://spdx.dev/Document/v2.3
# The attached file is the same document the action was given, so this is the
# type the attestation must carry; nothing is assumed about syft's default.
sbom_predicate_type() {
  local sbom="$1" version
  # `|| true` keeps a non-matching grep from ending the script at this
  # assignment under set -e; the check below is what reports the problem.
  version="$(
    grep -o '"spdxVersion"[[:space:]]*:[[:space:]]*"SPDX-[0-9][0-9.]*"' "$sbom" |
      head -n1 |
      sed 's/.*"SPDX-\([0-9][0-9.]*\)".*/\1/' || true
  )"
  [ -n "$version" ] ||
    die "$sbom does not declare an spdxVersion; it is not an SPDX JSON document"
  printf '%s/v%s\n' "$spdx_predicate_base" "$version"
}

# verify_sbom verifies the SBOM attestation for $bin and proves the attached
# file IS the attested document: the predicate GitHub returns must match the
# file exactly (canonicalised), or a consumer downloading the SBOM would get
# something nobody signed.
verify_sbom() {
  local bin="$1" sbom="$2" predicate_type attested canonical_file canonical_attested
  local scratch_path extract
  command -v jq >/dev/null 2>&1 ||
    die "jq is required to compare an attached SBOM with its attested predicate"
  predicate_type="$(sbom_predicate_type "$sbom")"
  scratch_path="$(scratch_dir)"
  attested="$scratch_path/attested-$(basename "$sbom")"
  extract=(gh attestation verify "$bin"
    --repo "$repo"
    --signer-workflow "$signer_workflow"
    --predicate-type "$predicate_type"
    --format json
    --jq '.[0].verificationResult.statement.predicate')
  printf '+ %s > %s\n' "${extract[*]}" "$attested" >&2
  "${extract[@]}" >"$attested" ||
    die "the SBOM attestation for $bin (predicate ${predicate_type}) did not verify"
  [ -s "$attested" ] ||
    die "gh returned no SBOM predicate for $bin; the attestation did not carry the SPDX document"
  case "$(head -c 20 "$attested")" in
    null*)
      die "gh returned no SBOM predicate for $bin; the attestation did not carry the SPDX document"
      ;;
  esac
  canonical_file="$attested.file.json"
  canonical_attested="$attested.attested.json"
  jq -S . "$sbom" >"$canonical_file" || die "cannot parse $sbom as JSON"
  jq -S . "$attested" >"$canonical_attested" ||
    die "cannot parse the attested predicate for $bin as JSON"
  if ! diff -u "$canonical_attested" "$canonical_file" >"$attested.diff"; then
    cat "$attested.diff" >&2
    die "$sbom differs from the SBOM predicate attested for $bin"
  fi
  echo "  SBOM $(basename "$sbom") matches the attested predicate (${predicate_type})"
}

# verify_binary verifies one binary: its Sigstore bundle and its provenance
# attestation, and the SBOM the release carries next to it.
verify_binary() {
  local bin="$1"
  local bundle="$bin.sigstore.json"
  local sbom="$bin.spdx.json"
  [ -f "$bundle" ] ||
    die "$(basename "$bin") has no $(basename "$bundle") next to it: this release predates the signing scheme or an asset is missing, so there is nothing to verify"
  echo "== $(basename "$bin")"
  run cosign verify-blob \
    --bundle "$bundle" \
    --certificate-identity-regexp "$identity_regexp" \
    --certificate-oidc-issuer "$issuer" \
    "$bin"
  run gh attestation verify "$bin" \
    --repo "$repo" \
    --signer-workflow "$signer_workflow"
  [ -f "$sbom" ] ||
    die "$(basename "$bin") has no $(basename "$sbom") next to it; an attested SBOM is part of the release"
  verify_sbom "$bin" "$sbom"
  echo "  verified: $(basename "$bin")"
}

# verify_image verifies the container image digest the release names. Skipped,
# with a message, when the release carries no digest and none was supplied.
verify_image() {
  if [ -z "$image_digest" ] && [ -f "$dir/$digest_asset" ]; then
    image_digest="$(tr -d '[:space:]' <"$dir/$digest_asset")"
    case "$image_digest" in
      *@*)
        image="${image_digest%@*}"
        image_digest="${image_digest##*@}"
        ;;
    esac
  fi
  if [ -z "$image_digest" ]; then
    echo "== container image: skipped (no $digest_asset in $dir and none supplied)"
    return 0
  fi
  case "$image_digest" in
    sha256:*) ;;
    *) die "image digest '$image_digest' is not a sha256 digest" ;;
  esac
  # The digest, never a tag: a tag can be moved between pushes, the digest
  # names exactly the bytes the release attested and signed.
  echo "== ${image}@${image_digest}"
  run cosign verify \
    --certificate-identity-regexp "$identity_regexp" \
    --certificate-oidc-issuer "$issuer" \
    "${image}@${image_digest}"
  run gh attestation verify "oci://${image}@${image_digest}" \
    --repo "$repo" \
    --signer-workflow "$signer_workflow"
  echo "  verified: ${image}@${image_digest}"
}

# tamper_demo copies a verified binary, flips one byte, and requires both
# verification paths to reject the copy. The byte is complemented (XOR 0xFF),
# so it is guaranteed to differ, and the copy is compared back to the original
# to prove the flip actually landed.
tamper_demo() {
  local bin="$1"
  local bundle="$bin.sigstore.json"
  local name size offset old new tampered
  name="$(basename "$bin")"
  tampered="$(scratch_dir)/$name"
  cp "$bin" "$tampered"
  size="$(wc -c <"$tampered" | tr -d '[:space:]')"
  [ "$size" -gt 0 ] || die "cannot tamper with an empty file: $name"
  offset=$((size / 2))
  old="$(dd if="$tampered" bs=1 skip="$offset" count=1 2>/dev/null | od -An -tu1 | tr -d ' \n' || true)"
  [ -n "$old" ] || die "cannot read byte ${offset} of $name"
  new=$((old ^ 255))
  # A zero byte cannot travel through a shell variable, so if the complement is
  # zero write the equally-different byte 1 instead.
  if [ "$new" -eq 0 ]; then
    new=1
  fi
  printf '%b' "\\$(printf '%03o' "$new")" |
    dd of="$tampered" bs=1 seek="$offset" count=1 conv=notrunc 2>/dev/null
  if cmp -s "$bin" "$tampered"; then
    die "the tamper demo changed nothing in $name; fix the demo before trusting it"
  fi
  echo "== tamper demo: $name with byte ${offset} flipped (${old} -> ${new})"
  if must_reject cosign verify-blob \
    --bundle "$bundle" \
    --certificate-identity-regexp "$identity_regexp" \
    --certificate-oidc-issuer "$issuer" \
    "$tampered"; then
    echo "  OK: the Sigstore bundle rejected the tampered copy"
  else
    die "cosign VERIFIED the tampered copy of $name: the signature gate does not bite"
  fi
  if must_reject gh attestation verify "$tampered" \
    --repo "$repo" \
    --signer-workflow "$signer_workflow"; then
    echo "  OK: the provenance attestation rejected the tampered copy"
  else
    die "gh attestation verify VERIFIED the tampered copy of $name: the provenance gate does not bite"
  fi
}

main() {
  local bin
  parse_args "$@"
  preflight
  resolve_dir
  collect_binaries
  echo "verify-release: policy: repo=${repo} signer-workflow=${signer_workflow}" >&2
  echo "verify-release: policy: certificate-identity-regexp=${identity_regexp}" >&2
  echo "verify-release: policy: certificate-oidc-issuer=${issuer}" >&2
  for bin in "${binaries[@]}"; do
    verify_binary "$bin"
  done
  if [ "$tamper" -eq 1 ]; then
    tamper_demo "${binaries[0]}"
  fi
  verify_image
  echo "OK: ${#binaries[@]} release binaries in ${dir} verified against ${repo}"
}

main "$@"
