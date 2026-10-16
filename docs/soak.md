# Batch signing soak

`TestSoakBatchSigning` (`soak_test.go`, build tag `soak`) signs batches of the
committed golden vectors continuously, from several goroutines, for a
caller-chosen duration. It is the long-running counterpart to the rest of the
suite, not a replacement for it: `go test -race` proves the batch signing path
and the pooled buffers in [`internal/xdrcopy`](../internal/xdrcopy) are correct
under contention for the length of a short run, and `TestCopyConcurrentReuse`
pins the short version of the pool contract, while this test watches the steady
state a service actually runs in — no leaked goroutines, no unbounded heap, and
no pool buffer handed to two callers — round after round.

## What one round does

Every round, four goroutines call `AuthorizeBatch` over every group of golden
vectors that can share one call — the same network passphrase, expiration ledger
and ordered signer list — until one second of signing has passed. Each call
signs at least `-soak-batch` entries (default 16) by repeating a group's
vectors, so the same credential node is signed in several positions of one batch
at once: that is the shape a pooled buffer handed to two callers needs to show
itself. After the workers join, and before the next round:

- every signed entry is compared byte for byte with its vector's recorded
  `signed_entry_xdr`, on every call, not a sample;
- the in-memory corpus is re-marshalled and compared with the recorded
  `unsigned_entry_xdr`, so a write through a caller's input is named as that
  rather than blamed on the signature;
- `runtime.GC()` runs, then `runtime.NumGoroutine` and
  `runtime.MemStats.HeapAlloc` are compared with the baseline taken after the
  first (warm-up) round.

One line goes to stdout per round, so CI logs carry the evidence. A real line
from the run below:

```text
soak: round 163: signed=648 goroutines=2 (baseline 2, +0) heap=569816 B (baseline 536192, +33624 B) elapsed=6m30s
```

A breach of either tolerance stops the test on that round with the numbers it
broke, and a byte mismatch stops it naming the vector and the first byte that
moved.

## Tolerances and why they are what they are

| Assertion  | Tolerance        | Measured in the run below                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ---------- | ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Goroutines | baseline + 1     | delta exactly 0 in all 259 rounds. `wg.Wait` returns when the last worker calls `Done`, not when it has left the scheduler, so one unit of slack keeps an exiting worker from flaking a ten-minute run; a leaked worker is +4 and persists, and a leaked `AuthorizeBatch` goroutine is larger still, so both still trip it on the next round.                                                                                                                                                                     |
| Live heap  | baseline + 4 MiB | max delta +33,624 B (0.03 MiB), min delta +16,128 B, with no upward trend over 258 measured rounds. The headroom is GC retention: the buffers the pools happen to hold, goroutine stacks and the test's own short-lived allocations. An absolute MiB figure is used rather than a percentage so a small baseline does not buy a small tolerance. A leak that retains even one signed entry per round — a round signs hundreds of entries — adds hundreds of kilobytes per round and crosses 4 MiB within the run. |

Both constants live in `soak_test.go` with the same reasoning in their comments.

## The reported run

|                     |                                                                                        |
| ------------------- | -------------------------------------------------------------------------------------- |
| Date                | 2026-10-15 (run started 23:15:22 UTC, ended 23:25:25 UTC)                              |
| Machine             | 2 CPUs, `INTEL(R) XEON(R) PLATINUM 8573C` (`nproc`, `/proc/cpuinfo`)                   |
| Go                  | `go1.27.1 linux/amd64`                                                                 |
| Command             | `go test -race -tags soak -run TestSoakBatchSigning -soak-duration 10m -timeout 20m .` |
| Result              | `PASS`, exit 0, `ok github.com/soroauth/soroauth-go 602.141s`                          |
| Rounds              | 259 (1 warm-up + 258 measured)                                                         |
| Entries signed      | 648 per round, every round; 167,832 in total                                           |
| Baseline            | 2 goroutines, 536,192 B live heap                                                      |
| Goroutines observed | 2 in every measured round (delta 0, max 0)                                             |
| Heap observed       | 552,320 B to 569,816 B; max delta +33,624 B at round 163                               |

The golden-vector byte check ran on every round: every signed entry of every
`AuthorizeBatch` call — all 167,832 — was compared with its vector's recorded
`signed_entry_xdr`, and the corpus was checked against `unsigned_entry_xdr`
before each round. The heap stayed inside a 17 KB band (552,320–569,816 B) for
the whole run (a linear fit over the 258 measured rounds is +8.4 B/round, which
is noise, not growth), and the goroutine count never left its baseline.

## Reproduce it

```sh
make soak                # 2m local smoke, race detector on
make soak SOAKTIME=10m   # the nightly budget

# or directly, which is exactly what the reported run above used:
go test -race -tags soak -run TestSoakBatchSigning -soak-duration 10m -timeout 20m .
```

`-timeout` must stay above `-soak-duration`: Go's default test timeout is 10m,
and the test would be killed just before a ten-minute soak finished. The `soak`
job in [`.github/workflows/fuzz.yml`](../.github/workflows/fuzz.yml) runs that
ten-minute command nightly, on every push to `main`, and on demand, and files an
issue when it fails. `-soak-batch` raises or lowers the minimum entries per
call; without `-race` a round simply fits more passes into its second, which is
the only number on the line that changes.

## What it does not prove

The soak replays committed vectors: it can show that the pools and the batch
path do not corrupt bytes or retain memory over time, not that unseen inputs
sign correctly. That is what the [fuzz targets](fuzzing.md), the parity
harnesses and the [e2e suite](../e2e/README.md) are for. It is also not a
performance gate — the round lines are evidence, not benchmarks — and it does
not prove anything about the live network, which no offline test can.
