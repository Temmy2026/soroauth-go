# Documentation

This is the map. Every document in the repository is listed here with the
question it answers, so a newcomer does not have to guess which of the README,
the architecture note and the guides is the one they need. The repository root
[README](../README.md) links here.

## Start here

| Document                                  | Answers                                                                                                         |
| ----------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| [README](../README.md)                    | What soroauth is, how to install it, and a 20-line quickstart for signing a simulated entry.                    |
| [ARCHITECTURE](../ARCHITECTURE.md)        | How the signing flow works, what each file is responsible for, and the CAP-71-01 delegate model.                |
| [CONTRIBUTING](../CONTRIBUTING.md)        | How to set up, what the tests expect, the commit format, and what a pull request has to pass.                   |
| [SECURITY](../SECURITY.md)                | How to report a vulnerability privately, what counts as critical, and the unaudited status of the library.      |
| [security-analysis](security-analysis.md) | What static analysis and dependency review run, how findings are triaged, and why the pull-request cap is five. |
| [CHANGELOG](../CHANGELOG.md)              | What changed in each release.                                                                                   |
| [faq](faq.md)                             | Why soroauth behaves as it does: the recurring design questions, each with its source.                          |

## Guides

| Document                                        | Answers                                                                                                            |
| ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| [labels](labels.md)                             | What each complexity and area label means, who applies it, and who has the final say on complexity.                |
| [signers](signers.md)                           | Which signer to use for a given threat model, and what evidence backs each one.                                    |
| [passkeys](passkeys.md)                         | How to sign for a WebAuthn / passkey smart wallet, end to end.                                                     |
| [migrating](migrating.md)                       | How to replace hand-rolled auth-entry signing with soroauth and verify it produced identical bytes.                |
| [multi-party-signing](multi-party-signing.md)   | How three parties sign one delegate tree in sequence, and why the expiration cannot change once anyone has signed. |
| [verification-limits](verification-limits.md)   | What `soroauth verify` can and cannot establish offline.                                                           |
| [verification-service](verification-service.md) | How to run the HTTP verification service, and what it will and will not do.                                        |
| [sdk-support](sdk-support.md)                   | Which `go-stellar-sdk` versions are supported and how the pin moves.                                               |
| [browser](browser.md)                           | How to use soroauth from a browser page: installing and loading the wasm module, and the passkey flow.             |

## Reference

| Document                                      | Answers                                                                                                   |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| [analyzer-heuristics](analyzer-heuristics.md) | What each risk-analyzer heuristic catches and what it misses.                                             |
| [glossary](glossary.md)                       | What credential arm, preimage, payload, nonce and delegate ordering mean, with the CAP that defines each. |
| [FIXTURES](FIXTURES.md)                       | What each e2e fixture contract exercises, and why none is a product.                                      |
| [ISSUE_BACKLOG](ISSUE_BACKLOG.md)             | The written-up work that is not done yet; `WAVE9_BACKLOG.json` is the newer Wave's source of truth.       |
| [diagrams](diagrams/README.md)                | The rendered signing-flow and delegate-tree diagrams, and their committed sources.                        |
| [UPSTREAM_PROPOSAL](../UPSTREAM_PROPOSAL.md)  | The proposal to add a minimal auth-entry signing helper to `go-stellar-sdk` itself.                       |

## Testing and evidence

| Document                                                             | Answers                                                                                     |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| [e2e/README](../e2e/README.md)                                       | What each live testnet scenario proves, and how the two-pass simulation flow works.         |
| [e2e/RESULTS](../e2e/RESULTS.md)                                     | The transaction hashes and observed credential arms from a real testnet run.                |
| [fuzzing](fuzzing.md)                                                | How the fuzz targets run continuously, and how a crash becomes a committed regression seed. |
| [parity](parity.md)                                                  | What agreement across implementations does and does not establish.                          |
| [differential fuzzing](../testdata/differential/README.md)           | How random entries are checked across the Go, JS and Python implementations.                |
| [reproducibility](reproducibility.md)                                | How a released binary is rebuilt byte-for-byte, and what that does and does not cover.      |
| [frozen divergences](../testdata/differential/regressions/README.md) | The regression corpus of cases the implementations once disagreed on.                       |
| [Python parity](../testdata/parity-python/README.md)                 | How the vectors are recomputed with the Python `stellar-sdk`.                               |
| [Rust parity](../testdata/parity-rust/README.md)                     | How the vectors are recomputed with the `stellar-xdr` crate the host itself uses.           |

## Sub-project docs

These live next to the code they describe.

| Document                                                          | Answers                                                                       |
| ----------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| [wasm](../wasm/README.md)                                         | What the js/wasm signing core exposes and how it is built.                    |
| [wasm/ts](../wasm/ts/README.md)                                   | How to use the `@soroauth/wasm` TypeScript wrapper.                           |
| [examples/browser-passkey](../examples/browser-passkey/README.md) | How to run the browser demo that signs with a passkey and submits to testnet. |
| [examples/remote-signer](../examples/remote-signer/README.md)     | How to run the reference HTTP signing server and its client together.         |

## Repository meta

| Document                                                     | Answers                                                                  |
| ------------------------------------------------------------ | ------------------------------------------------------------------------ |
| [pull request template](../.github/pull_request_template.md) | What every pull request must state, including the wire-format checklist. |
| [CLAUDE.md](../CLAUDE.md)                                    | The original build specification for this project, kept for reference.   |
