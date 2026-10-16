package soroauth

import (
	"context"
	"fmt"
	"testing"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Per-arm signing-path benchmarks (issue #365).
//
// scripts/checkbench used to gate the signing path with a single aggregate
// number per benchmark. One number hides a regression in one credential arm
// behind an improvement in another, and the delegates arm — the one that scales
// with tree size — is exactly the arm whose cost a legacy improvement can mask.
// These benchmarks split verification and diffing by arm, and add a delegate
// tree parameterised by node count so super-linear growth is visible, so
// testdata/bench/budgets.json can carry a separate baseline per arm and
// operation. The signing arm itself is BenchmarkAuthorizeEntry/{legacy,v2,
// delegates} in bench_signing_test.go.
//
// Every budget this file feeds names the operation and the arm, which is what
// lets checkbench print "signing/delegates" rather than an opaque benchmark
// name when a baseline is exceeded.

// benchCredentialArms is the three address arms a signing, verification or diff
// benchmark runs over, in a fixed order so budget names are stable.
var benchCredentialArms = []struct {
	name string
	arm  xdr.SorobanCredentialsType
}{
	{"legacy", xdr.SorobanCredentialsTypeSorobanCredentialsAddress},
	{"v2", xdr.SorobanCredentialsTypeSorobanCredentialsAddressV2},
	{"delegates", xdr.SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates},
}

// benchSignedEntryForArm builds the arm's entry and signs every node it has, so
// the verification benchmark measures a fully signed entry rather than a walk
// that stops at the first unsigned node.
func benchSignedEntryForArm(b *testing.B, arm xdr.SorobanCredentialsType) xdr.SorobanAuthorizationEntry {
	b.Helper()
	ctx := context.Background()

	entry := entryForArm(b, arm, 42)
	topSigner := NewEd25519Signer(benchKeypair(b, "soroauth-preimage-signer"))
	signed, err := AuthorizeEntry(ctx, entry, topSigner, testValidUntilLedger, network.TestNetworkPassphrase)
	if err != nil {
		b.Fatalf("signing the top-level node: %v", err)
	}
	if arm == xdr.SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates {
		delegateSigner := NewEd25519Signer(benchKeypair(b, "soroauth-preimage-delegate"))
		signed, err = AuthorizeEntry(ctx, signed, delegateSigner, testValidUntilLedger,
			network.TestNetworkPassphrase, ForAddress(delegateSigner.Address()))
		if err != nil {
			b.Fatalf("signing the delegate node: %v", err)
		}
	}
	return signed
}

// BenchmarkVerifyEntry measures offline verification of a fully signed entry on
// each signing arm, so a regression in the delegates walk cannot hide behind
// the legacy path. Budgeted as operation=verification in
// testdata/bench/budgets.json.
func BenchmarkVerifyEntry(b *testing.B) {
	for _, arm := range benchCredentialArms {
		b.Run(arm.name, func(b *testing.B) {
			entry := benchSignedEntryForArm(b, arm.arm)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := VerifyEntry(entry, network.TestNetworkPassphrase); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDiffEntries measures DiffEntries on each signing arm, over two
// entries that differ in a payload-affecting field (the nonce), so the diff has
// real work to report. Budgeted as operation=diffing.
func BenchmarkDiffEntries(b *testing.B) {
	for _, arm := range benchCredentialArms {
		b.Run(arm.name, func(b *testing.B) {
			a := entryForArm(b, arm.arm, 42)
			c := entryForArm(b, arm.arm, 43)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := DiffEntries(a, c); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// benchDelegateTreeSizes is the node counts the tree benchmark walks: a single
// node, then a geometric spread up to the deepest nesting WithDelegates accepts
// (63; the guard also visits the empty level below the leaf). The spread is what
// makes super-linear growth visible — per-node cost should stay flat.
var benchDelegateTreeSizes = []int{1, 4, 16, 63}

// BenchmarkDelegateTree signs a delegate chain of a given node count and is
// parameterised by that count, so the per-node cost of the recursive walk and
// the deep copy is visible at every size. Budgeted as operation=signing with
// arm=delegates_tree, one baseline per node count.
func BenchmarkDelegateTree(b *testing.B) {
	for _, nodes := range benchDelegateTreeSizes {
		b.Run(fmt.Sprintf("nodes_%d", nodes), func(b *testing.B) {
			entry, leafKP, leafAddr := benchDelegateChain(b, nodes)
			signer := NewEd25519Signer(leafKP)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := AuthorizeEntry(ctx, entry, signer, testValidUntilLedger,
					network.TestNetworkPassphrase, ForAddress(leafAddr)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
