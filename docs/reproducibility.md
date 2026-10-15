# Reproducible builds

A release here ships five CLI binaries, one per target. This document is how
anyone — including someone who does not trust the CI that built them — rebuilds
those exact bytes from the tagged source and checks that they match. Provenance
says who built an artifact; reproducibility is what lets a stranger check what
was built.

## The build command

The one command is [`scripts/reproduce.sh`](../scripts/reproduce.sh). It builds
every target from a git ref with the deterministic flags below and writes a
`checksums.txt` beside the binaries:

```sh
scripts/reproduce.sh v0.5.0 reproduced   # <ref> [outdir], outdir defaults to ./reproduced
```

The targets are `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`,
and `windows/amd64`, exactly the matrix in
[`.github/workflows/release.yml`](../.github/workflows/release.yml). Inside,
each target is built with:

```sh
CGO_ENABLED=0 GOOS=<goos> GOARCH=<goarch> \
  go build -trimpath -buildvcs=false \
  -ldflags="-s -w -X main.version=<tag> -X main.commit=<commit> -X main.date=<commit date>" \
  -o soroauth_<goos>_<goarch> ./cmd/soroauth
```

## What makes it reproducible

Non-determinism has a small number of sources, and each is neutralised rather
than hoped about. If a rebuild stops matching, one of these was reintroduced.

| Source                            | Why it is non-deterministic                                                             | How it is neutralised                                                                                                                                              |
| --------------------------------- | --------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Absolute build paths              | Go records the directory it was built in                                                | `-trimpath`                                                                                                                                                        |
| VCS stamping                      | Go embeds the revision and a dirty bit by default                                       | `-buildvcs=false`                                                                                                                                                  |
| Build timestamp                   | `main.date` from the wall clock changes every run                                       | It is the tagged commit's committer date (`git show -s --format=%cI`), so the same tag always stamps the same value; `SOURCE_DATE_EPOCH` is set from the same time |
| Toolchain patch version           | `runtime.Version()` is compiled into the binary and a newer patch emits different bytes | The toolchain is pinned to `1.25.4`, the version `go.mod`'s `toolchain` directive names                                                                            |
| Host C toolchain                  | cgo pulls in the host's compiler and libc                                               | `CGO_ENABLED=0`                                                                                                                                                    |
| Module contents                   | A floating dependency could change between builds                                       | `go.sum` pins every module; `GOFLAGS` is not set, so module mode is the default                                                                                    |
| Build ID                          | Derived from the inputs, which are now fixed                                            | Left to Go; with the inputs fixed it is fixed. It is deliberately not overridden, which would defeat the build cache for no gain                                   |
| `git describe --dirty` in the CLI | A dirty worktree changes the version string                                             | The release stamps the tag, never `--dirty`; `scripts/reproduce.sh` uses `git describe` without `--dirty`                                                          |

## Verifying a release

Third-party steps, from a clean checkout of the repository:

```sh
# 1. Download the published checksums and the exact tag.
gh release download v0.5.0 --pattern checksums.txt --dir published
git fetch --tags

# 2. Rebuild from the tag with the documented command.
git checkout v0.5.0
scripts/reproduce.sh v0.5.0 reproduced

# 3. Compare. Any difference means the published bytes are not what the tag
#    builds; a match means they are.
diff -u <(sort -k2 published/checksums.txt) <(sort -k2 reproduced/checksums.txt)
```

CI runs these same steps automatically. The `reproduce` job in
[`.github/workflows/reproducible.yml`](../.github/workflows/reproducible.yml)
runs when a release is published, weekly on a schedule, and on demand against
any tag. It is a rebuild by a job separate from the one that built the release,
so it is an independent check rather than the builder confirming itself.

Locally, `make reproducible-check` builds every target twice from the same
commit and fails if the two checksum lists differ.

## What this does not cover

- **The container image.** It is built by `docker/build-push-action` from a base
  image, and layer timestamps and the base image digest are outside this
  command's control. The image is not claimed reproducible here; the released
  CLI binaries are.
- **The js/wasm signing core.** It is published to npm rather than attached to
  the GitHub release, so it is not one of the five binaries above. Its
  correctness is checked byte-for-byte against the golden vectors by
  `make wasm-check`, but cross-machine reproducibility of the `.wasm` bytes is
  not asserted by this document.
