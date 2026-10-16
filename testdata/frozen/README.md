# Frozen release entries

Entries signed by an earlier release of this library, kept so a wire-format
regression in how a stored, pre-signed entry is read, verified or inspected
fails the build instead of landing unnoticed. The golden vectors cannot catch
that on their own: `make vectors` regenerates them in the same commit as the
code, so they move together.

Each release has its own directory, `testdata/frozen/<release>/`, holding one
JSON file per entry. A file records the signed entry, the release's preimage and
payload for it, and the release's own `Inspect` output. `frozen_test.go` asserts
that the current code still decodes the entry to the same bytes, rebuilds the
same payload, verifies it, and inspects to the same structure.

The frozen set is **never regenerated** by a build or a `make` target. It is
committed by hand, once per release, and a check in `vectors-check` fails if a
run of `make vectors` changes anything under `testdata/frozen/`. It is
deliberately kept out of `testdata/vectors/`, which `make vectors` rewrites: a
set that moved with the generator would prove nothing about the release.
`TestFrozenSetIsSeparateFromVectors` enforces the separation.

## Adding a frozen set for a release

From a clean checkout of the current repository, with the release tag fetched:

```sh
git worktree add /tmp/soroauth-<release> <release>
cp -r scripts/frozengen /tmp/soroauth-<release>/cmd/frozengen
(cd /tmp/soroauth-<release> && \
  go run ./cmd/frozengen --release <release> \
    --out "$OLDPWD/testdata/frozen/<release>")
git worktree remove /tmp/soroauth-<release>
```

`scripts/frozengen` uses only API that existed in v0.1.0 (`Preimage`, `Payload`,
`AuthorizeEntry`, `WithDelegates`, `Inspect`, `NewEd25519Signer`, `ForAddress`,
`ParseAddress`), so it compiles against any release from v0.1.0 on. Running it
inside the release's worktree is what makes the entries that release's output
rather than the current code's.

Then commit the new directory with the test's `frozenRelease` constant pointed
at it, and open the pull request. Do not run `make vectors` afterwards: nothing
it writes belongs in this directory.
