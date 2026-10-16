# Frozen wasm parity regressions

An empty directory until there is something to freeze. When the wasm parity
harness (`wasm/parity.mjs`) reports a divergence between
`wasm/dist/soroauth.wasm` and the native build, do not edit the failing case and
do not change the expectation: copy the case here and commit it, so it is
replayed on every run.

A case uses the same schema as a differential case
(`testdata/differential/corpus/*.json`): `unsigned_entry_xdr`,
`network_passphrase`, `valid_until_ledger`, `preimage_xdr`, `payload_hex`. The
harness replays every `.json` file in this directory alongside the differential
corpus. Files that are not `.json` are ignored, which is why this README can
live here.

`cmd/gencorpus` does not touch this directory, so a frozen case survives
regeneration of the seeds and the recorded expectations.
