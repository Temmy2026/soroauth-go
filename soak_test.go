//go:build soak

package soroauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// soakDuration is how long TestSoakBatchSigning keeps signing. The default is
// short on purpose: a local run is a smoke that proves the loop starts and its
// assertions hold, not a replacement for the CI budget. The scheduled job in
// .github/workflows/fuzz.yml passes 10m.
var soakDuration = flag.Duration("soak-duration", 2*time.Minute,
	"how long TestSoakBatchSigning keeps signing batches")

// soakBatch is the smallest number of entries each AuthorizeBatch call signs.
// Every recorded vector's entries are repeated up to that size, so one call
// signs the same credential node several times concurrently and a buffer shared
// between two callers has somewhere to show itself. The default is what the
// reported run used; the scheduled job leaves it alone.
var soakBatch = flag.Int("soak-batch", 16,
	"minimum entries per AuthorizeBatch call (each vector's entries repeat up to this size)")

const (
	// soakWorkers is how many goroutines call AuthorizeBatch concurrently.
	// Each call itself spawns one goroutine per entry and signs through a
	// bounded semaphore (batch_sign.go), so the total in flight is this many
	// plus that pool's workers. Four is deliberate: the machine this was
	// written on has two CPUs and the CI runner has two to four, so four
	// callers keep every core contended without making the test's wall-clock
	// cost depend on the number of cores.
	soakWorkers = 4

	// soakRoundBudget is how long each round spends signing before the
	// workers are joined and the round's measurements are taken. One second
	// keeps a ten-minute run near ten-minute-sized logs: one line per round.
	soakRoundBudget = time.Second

	// soakGoroutineHeadroom is how many goroutines above the warm-up
	// baseline are tolerated when a round has joined its workers. The
	// reference runs (docs/soak.md) measured a delta of exactly 0 in every
	// round, so the tolerance is the smallest non-zero slack that keeps the
	// assertion sensitive: wg.Wait returns when the last worker calls Done,
	// not when it has left the scheduler, so one worker observed mid-exit
	// must not fail a ten-minute run. A leaked worker is +4 and persists
	// across rounds, and a leaked pool goroutine is larger still; both trip
	// this on the round after they start.
	soakGoroutineHeadroom = 1

	// soakHeapHeadroomBytes is how much live heap above the warm-up baseline
	// is tolerated after a runtime.GC, expressed as an absolute MiB figure
	// rather than a percentage so a tiny baseline does not buy a tiny
	// tolerance. It is GC retention headroom: the reported run measured a
	// maximum delta of 33,624 B (0.03 MiB) over 258 measured rounds, with no
	// upward trend, so 4 MiB leaves room for the buffers the pools happened
	// to retain on a busier CI runner while still being small next to
	// anything that grows. A leak that retains one signed entry per round
	// adds hundreds of kilobytes per round (a round signs hundreds of
	// entries) and crosses this quickly, where a steady state stays flat
	// across rounds. See docs/soak.md for the measurement.
	soakHeapHeadroomBytes = 4 << 20
)

// soakCase is one recorded golden vector prepared for the batch path: the
// unsigned entry, the exact bytes the reference implementation recorded for it
// once signed, and — so corruption of the shared corpus can be told apart from
// corruption of a result — the unsigned bytes it decoded from.
type soakCase struct {
	name        string
	entry       xdr.SorobanAuthorizationEntry
	rawUnsigned []byte
	wantSigned  []byte
}

// soakGroup is the set of vectors that one AuthorizeBatch call can sign: they
// share a network passphrase, an expiration ledger and the ordered list of
// signers their recorded steps call for. Batching across those boundaries
// would not reproduce the recorded bytes, so the groups are kept apart instead
// of bending the expectations.
type soakGroup struct {
	name       string
	passphrase string
	validUntil uint32
	signers    []Signer
	cases      []soakCase

	// batch is the group's cases repeated up to the -soak-batch size. It is
	// built once: the workers hand the same slice to AuthorizeBatch call
	// after call, which is what a service doing steady traffic looks like.
	batch []xdr.SorobanAuthorizationEntry
}

// buildSoakCorpus loads every committed golden vector and arranges the ones
// the batch path can sign into per-call groups. Every vector is included: the
// batch signs each arm exactly as AuthorizeEntry does, including source-account
// pass-through (which is exactly what its recorded signed bytes are).
func buildSoakCorpus(t *testing.T) []soakGroup {
	t.Helper()

	vectors := loadVectors(t)
	groups := make(map[string]*soakGroup)
	order := make([]string, 0, len(vectors))

	for _, v := range vectors {
		rawUnsigned, err := base64.StdEncoding.DecodeString(v.UnsignedEntryXDR)
		if err != nil {
			t.Fatalf("soak: %s: decoding the recorded unsigned entry: %v", v.Name, err)
		}
		var entry xdr.SorobanAuthorizationEntry
		if err := xdr.SafeUnmarshalBase64(v.UnsignedEntryXDR, &entry); err != nil {
			t.Fatalf("soak: %s: decoding the unsigned entry: %v", v.Name, err)
		}
		wantSigned, err := base64.StdEncoding.DecodeString(v.SignedEntryXDR)
		if err != nil {
			t.Fatalf("soak: %s: decoding the recorded signed entry: %v", v.Name, err)
		}

		signers := make([]Signer, 0, len(v.Steps))
		labels := make([]string, 0, len(v.Steps))
		for _, step := range v.Steps {
			signers = append(signers, signerForStep(t, step))
			labels = append(labels, step.SignerLabel)
		}

		// The key is everything AuthorizeBatch's contract depends on. Two
		// vectors with the same key need the same call shape, so they share
		// one group — and only then, because a signer the vector did not
		// record would write a signature the reference never wrote.
		key := v.NetworkPassphrase + "\x00" +
			strconv.FormatUint(uint64(v.ValidUntilLedger), 10) + "\x00" +
			strings.Join(labels, "\x00")
		group, ok := groups[key]
		if !ok {
			group = &soakGroup{name: v.Name}
			groups[key] = group
			order = append(order, key)
		}
		group.passphrase = v.NetworkPassphrase
		group.validUntil = v.ValidUntilLedger
		group.signers = signers
		group.cases = append(group.cases, soakCase{
			name:        v.Name,
			entry:       entry,
			rawUnsigned: rawUnsigned,
			wantSigned:  wantSigned,
		})
	}

	out := make([]soakGroup, 0, len(order))
	for _, key := range order {
		group := groups[key]
		if len(group.cases) > 1 {
			group.name = fmt.Sprintf("%s (+%d more)", group.name, len(group.cases)-1)
		}

		size := len(group.cases)
		if *soakBatch > size {
			size = (*soakBatch + len(group.cases) - 1) / len(group.cases) * len(group.cases)
		}
		group.batch = make([]xdr.SorobanAuthorizationEntry, size)
		for i := range group.batch {
			group.batch[i] = group.cases[i%len(group.cases)].entry
		}

		out = append(out, *group)
	}
	if len(out) == 0 {
		t.Fatal("soak: no golden vectors loaded; the soak would sign nothing")
	}
	return out
}

// caseAt returns the case whose expected bytes belong at position i of the
// group's batch, wrapping the same way buildSoakCorpus repeated the cases.
func (g *soakGroup) caseAt(i int) *soakCase {
	return &g.cases[i%len(g.cases)]
}

// soakMeasure flushes the garbage collector so HeapAlloc reports live memory
// and not collection debt, then reads the two numbers a round is asserted
// against.
func soakMeasure() (goroutines int, heapBytes uint64) {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return runtime.NumGoroutine(), stats.HeapAlloc
}

// soakWorker signs every group once per pass until the round's deadline runs
// out, or until another worker has reported a failure (stop). It checks every
// signed entry against the vector's recorded bytes immediately, so a pooled
// buffer handed to two callers is caught on the pass it happens rather than
// after the round.
func soakWorker(
	ctx context.Context,
	groups []soakGroup,
	deadline time.Time,
	stop func() bool,
	note func(string),
) int {
	signed := 0
	for !stop() && time.Now().Before(deadline) {
		for gi := range groups {
			group := &groups[gi]
			out, err := AuthorizeBatch(ctx, group.batch, group.signers, group.validUntil, group.passphrase)
			if err != nil {
				note(fmt.Sprintf("soak: %s: AuthorizeBatch returned an unexpected error: %v", group.name, err))
				return signed
			}
			if len(out) != len(group.batch) {
				note(fmt.Sprintf("soak: %s: AuthorizeBatch returned %d entries for %d inputs",
					group.name, len(out), len(group.batch)))
				return signed
			}
			for i := range out {
				c := group.caseAt(i)
				got, err := out[i].MarshalBinary()
				if err != nil {
					note(fmt.Sprintf("soak: %s (batch position %d): marshalling the signed entry: %v",
						c.name, i, err))
					return signed
				}
				if !bytes.Equal(got, c.wantSigned) {
					note(soakByteMismatch(c, got))
					return signed
				}
			}
			signed += len(out)
		}
	}
	return signed
}

// soakByteMismatch names the vector whose signed entry moved and the first
// byte that moved, which is what a buffer shared between two callers looks
// like: the same vector sitting in two positions of one batch and coming back
// with different bytes.
func soakByteMismatch(c *soakCase, got []byte) string {
	offset := len(c.wantSigned)
	if len(got) < offset {
		offset = len(got)
	}
	for i := 0; i < offset; i++ {
		if c.wantSigned[i] != got[i] {
			offset = i
			break
		}
	}

	// A little context around the first differing byte, bounded so a large
	// entry does not flood the test log.
	show := func(b []byte) string {
		lo, hi := offset-8, offset+8
		if lo < 0 {
			lo = 0
		}
		if hi > len(b) {
			hi = len(b)
		}
		if lo > hi {
			lo = hi
		}
		return fmt.Sprintf("%x", b[lo:hi])
	}

	return fmt.Sprintf(
		"soak: %s: signed entry differs from the golden vector: first difference at byte %d (%d bytes returned, %d recorded)\n want %s\n  got %s",
		c.name, offset, len(got), len(c.wantSigned), show(c.wantSigned), show(got))
}

// soakCorpusIsPristine asserts that the in-memory corpus still marshals to the
// exact bytes every vector recorded. The corpus entries are shared by every
// worker, so a bug that wrote through a caller's input would corrupt the
// source of the next round's expectations; this fails naming the vector
// instead of letting the byte comparison above blame the signature.
func soakCorpusIsPristine(t *testing.T, groups []soakGroup) {
	t.Helper()
	for gi := range groups {
		for ci := range groups[gi].cases {
			c := &groups[gi].cases[ci]
			raw, err := c.entry.MarshalBinary()
			if err != nil {
				t.Fatalf("soak: %s: marshalling the corpus entry: %v", c.name, err)
			}
			if !bytes.Equal(raw, c.rawUnsigned) {
				t.Fatalf("soak: %s: the corpus entry no longer matches its recorded unsigned XDR: %d bytes now, %d recorded",
					c.name, len(raw), len(c.rawUnsigned))
			}
		}
	}
}

// soakRound signs for one round, joins every worker, and returns how many
// entries were signed. Any byte mismatch, error or surprise shape stops the
// whole test rather than a worker.
func soakRound(t *testing.T, groups []soakGroup) int {
	t.Helper()

	soakCorpusIsPristine(t, groups)

	var mu sync.Mutex
	failMsg := ""
	note := func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		if failMsg == "" {
			failMsg = msg
		}
	}
	stop := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return failMsg != ""
	}

	deadline := time.Now().Add(soakRoundBudget)
	var signed atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < soakWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signed.Add(int64(soakWorker(context.Background(), groups, deadline, stop, note)))
		}()
	}
	wg.Wait()

	if failMsg != "" {
		t.Fatal(failMsg)
	}
	return int(signed.Load())
}

// TestSoakBatchSigning is the long-running counterpart to the rest of the
// suite, and is not compiled without the soak build tag.
//
// `go test -race` proves the batch signing path and the pooled buffers in
// internal/xdrcopy are correct under contention for the length of one test
// run; TestCopyConcurrentReuse pins the short version of the pool contract in
// its own package. Neither says anything about an hour of service: a goroutine
// that leaks, a heap that grows, or a pooled buffer that is eventually handed
// to two callers at once only shows up over repetition. This test provides
// that repetition — several goroutines signing batches of the committed
// golden vectors, round after round — and after every round it asserts that
//
//   - every signed entry is byte-identical to the vector's recorded
//     SignedEntryXDR (bytes, not just memory: pool corruption is the failure
//     being hunted, and only the bytes catch it),
//   - the goroutine count is back to the baseline taken after the warm-up
//     round, and
//   - live heap (runtime.MemStats.HeapAlloc, after a runtime.GC) is back
//     within the documented headroom of that same baseline.
//
// The test is deliberately not a replacement for either of those short
// checks; it is the same properties watched for much longer. See docs/soak.md
// for the reported run and the measurements behind the tolerances.
func TestSoakBatchSigning(t *testing.T) {
	start := time.Now()
	end := start.Add(*soakDuration)
	groups := buildSoakCorpus(t)

	var baseGoroutines int
	var baseHeap uint64
	totalSigned := 0
	round := 0
	// The first round always runs: it is the warm-up, and the baseline it
	// leaves behind is what every later round is measured against. After it,
	// a round starts only while the requested duration is still running.
	for round == 0 || time.Now().Before(end) {
		round++
		signed := soakRound(t, groups)
		totalSigned += signed
		goroutines, heap := soakMeasure()

		if round == 1 {
			baseGoroutines, baseHeap = goroutines, heap
			fmt.Printf("soak: warm-up round 1: signed=%d goroutines=%d (baseline) heap=%d B (baseline)\n",
				signed, goroutines, heap)
			continue
		}

		fmt.Printf("soak: round %d: signed=%d goroutines=%d (baseline %d, %+d) heap=%d B (baseline %d, %+d B) elapsed=%s\n",
			round, signed,
			goroutines, baseGoroutines, goroutines-baseGoroutines,
			heap, baseHeap, int64(heap)-int64(baseHeap),
			time.Since(start).Round(time.Second))

		if goroutines > baseGoroutines+soakGoroutineHeadroom {
			t.Errorf("soak: round %d: %d goroutines, baseline %d, headroom %d: goroutines did not return to the baseline",
				round, goroutines, baseGoroutines, soakGoroutineHeadroom)
			return
		}
		if heap > baseHeap+soakHeapHeadroomBytes {
			t.Errorf("soak: round %d: heap %d B, baseline %d B, headroom %d B: live heap did not return to the baseline",
				round, heap, baseHeap, soakHeapHeadroomBytes)
			return
		}
	}

	if round < 2 {
		t.Fatalf("soak: -soak-duration %s ended before any measured round followed the warm-up", *soakDuration)
	}
	fmt.Printf("soak: done: %d rounds, %d entries signed, %s\n", round, totalSigned, time.Since(start).Round(time.Second))
}
