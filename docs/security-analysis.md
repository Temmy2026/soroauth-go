# Static security analysis and dependency review

This is the ledger for the security checks on a pull request: what runs, why the
pull-request check cap was raised to accommodate it, and how a finding is
triaged. A scanner whose findings nobody reads is worse than none, so the triage
below is the deliverable, not the workflow file alone.

## What runs

Both jobs live in
[`.github/workflows/security.yml`](../.github/workflows/security.yml) and pin
their actions by commit SHA, as every workflow here does.

| Job                 | Runs on                                                 | What it does                                                                                                                                                             |
| ------------------- | ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `codeql`            | `pull_request`, `push` to `main`, and a weekly schedule | CodeQL over the Go **security-and-quality** query suite. Uploads SARIF to GitHub code scanning, where findings are dismissed or resolved individually.                   |
| `dependency review` | `pull_request`                                          | Fails a pull request that introduces a dependency with a known advisory at severity `high` or above, and posts the dependency diff as a pull-request comment on failure. |

The scheduled `codeql` run is what catches a newly-published query finding a
problem in code that has not changed since the last run.

## Why the pull-request cap is five, not three

The repository deliberately capped pull requests at a short list so a
contributor does not face a wall of checks: `vet and test`,
`golden vectors are reproducible`, and `contracts build and test`, all in
`.github/workflows/ci.yml`. This change raises the cap to **five**, adding
`codeql` and `dependency review`.

The reasoning is explicit rather than incidental:

- A security gate that runs only after merge is not a gate. Dependency review in
  particular does nothing useful on a push to `main` — by then the vulnerable
  dependency is already merged.
- The two new checks are the only ones that changed. The three in `ci.yml` are
  untouched, and neither new job runs the test suite, so the review wait grows
  by the two security jobs and nothing else.
- Every other workflow here (links, spellcheck, ci-go, ci-parity, ci-wasm,
  ci-adapters) stays push-only for exactly the original reason. Only the
  security jobs are promoted to pull requests.

**To make them block.** A job only blocks a merge if the branch ruleset requires
it. Rulesets are configured on GitHub, not in this repository, so enabling
`codeql` and `dependency review` as required checks is a repository-settings
step that accompanies this change. Until then they report on the pull request
but do not hold the merge.

## Triage policy

Every finding is either **fixed**, or **dismissed with a written reason**. There
is no third disposition. The per-finding dismissal and its reason live on GitHub
code scanning (or the dependency-review comment); this file records the
recurring reasons and the decisions that span more than one finding, so a
reviewer reading a dismissal can see whether it was already considered.

## Initial triage

At the time this workflow landed, CodeQL had not yet run against the tree, so
its first run populates code scanning from a clean slate. To exercise the policy
and to leave the ledger non-empty, the findings the repository's existing local
static analysis already reports are triaged here — `golangci-lint` against
[`.golangci.yml`](../.golangci.yml), which `CONTRIBUTING.md` documents as a
local gate rather than a CI job. CodeQL findings are added under the same
policy.

These findings are pre-existing on `main`. None is a security defect, and none
is in the signing or key-handling paths. Each is dismissed, in the groups
`golangci-lint` sorts them into:

| Group                                                                                        | Count | Disposition | Reason                                                                                                                                                                                             |
| -------------------------------------------------------------------------------------------- | ----- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `errcheck`: unchecked `Close()` on a listener or HTTP response body in `examples/` and tests | 8     | Dismissed   | Teardown and illustrative code. The error is not actionable at the call site and handling it would add noise without changing behaviour.                                                           |
| `errorlint`: `==` comparison of a wrapped error in an example test                           | 1     | Dismissed   | The line is an example of comparing a sentinel; the wrapped case is asserted with `errors.Is` elsewhere in the same test.                                                                          |
| `staticcheck` `SA1019`: deprecated `crypto/elliptic` use in `secp256r1.go` and tests         | 6     | Dismissed   | SEC-1 uncompressed point encoding is the wire format. `crypto/ecdh` exposes the same bytes through a different API; migrating is separate work, and the current code is not a security regression. |
| `staticcheck` style hints (`S1011`, `S1009`, `SA9003`, `QF1012`)                             | 5     | Dismissed   | Style only, no behavioural effect. Folding them in would widen a security change into unrelated cleanup.                                                                                           |
| `unused`: plain `runSign` / `runPayload` / `runDelegates` / `readAssertionInput` wrappers    | 4     | Dismissed   | Dead convenience wrappers in `cmd/soroauth`; the dispatcher calls the `…WithStdin` variants. Removing them is unrelated cleanup and belongs in its own change.                                     |

## Reproducing locally

```sh
golangci-lint run ./...        # the baseline above
```

CodeQL itself runs in the `security` workflow; its results are visible under the
repository's **Security → Code scanning** tab.
