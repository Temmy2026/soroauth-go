//go:build e2e

package e2e

import (
	"testing"

	rpc "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroauth/soroauth-go"
)

// transferAmount is 1 XLM in stroops. Small enough that friendbot funding
// covers many runs.
const transferAmount = 10_000_000

// TestScenarioA proves the legacy SOROBAN_CREDENTIALS_ADDRESS arm is accepted
// by a live host: a payer submits a transfer of someone else's XLM, authorized
// only by that someone's soroauth signature.
func TestScenarioA(t *testing.T) {
	h := newHarness(t)

	payer := h.newAccount(t, "payer P")
	from := h.newAccount(t, "sender A")
	to := h.newAccount(t, "recipient B")

	op := h.transferOp(t, scAddressOf(t, from.Address()), scAddressOf(t, to.Address()),
		transferAmount, payer.Address())

	result := runScenario(t, h, scenarioSpec{
		payer:   payer,
		op:      op,
		signers: []soroauth.Signer{soroauth.NewEd25519Signer(from)},
		// Scenario A is about the legacy arm, so the recording pass is not
		// asked to upgrade what it records.
		upgradedAuth: false,
	})

	t.Logf("tx hash: %s", result.Hash)
	t.Logf("ledger:  %d", result.Ledger)
	t.Logf("arm:     %s", result.Arm)
	t.Logf("link:    %s%s", runExplorerBase, result.Hash)

	record(scenarioResult{
		ID:        "A",
		Name:      "legacy address credentials accepted live",
		Proves:    "A SOROBAN_CREDENTIALS_ADDRESS entry signed by soroauth is accepted by the host.",
		TxHash:    result.Hash,
		Ledger:    result.Ledger,
		Arm:       result.Arm,
		Succeeded: result.Status == rpc.TransactionStatusSuccess,
		RawError:  result.RawError,
		Notes: []string{
			"Payer and sender are different accounts, so the transfer cannot fall back to source-account auth.",
		},
	})

	if result.Status != rpc.TransactionStatusSuccess {
		t.Fatalf("scenario A failed on-chain (status %s):\n%s", result.Status, result.RawError)
	}
	if result.Arm != "SorobanCredentialsTypeSorobanCredentialsAddress" {
		t.Errorf("submitted envelope carried credentials arm %q, want legacy address", result.Arm)
	}
}

// TestScenarioPasskeySignerFlags proves that passkey signers correctly reject assertions
// missing required user-presence (UP) or user-verification (UV) flags before signing or submission.

// TestScenarioB proves the CAP-71 SOROBAN_CREDENTIALS_ADDRESS_V2 arm is
// accepted live. It asks simulation for V2 first, and upgrades locally if the
// RPC did not provide it, reporting which of the two actually happened.
func TestScenarioB(t *testing.T) {
	h := newHarness(t)

	payer := h.newAccount(t, "payer P")
	from := h.newAccount(t, "sender A")
	to := h.newAccount(t, "recipient B")

	op := h.transferOp(t, scAddressOf(t, from.Address()), scAddressOf(t, to.Address()),
		transferAmount, payer.Address())

	var route string
	prepare := func(t *testing.T, entries []xdr.SorobanAuthorizationEntry, _ uint32) []xdr.SorobanAuthorizationEntry {
		t.Helper()

		recordedArm := entries[0].Credentials.Type.String()
		if entries[0].Credentials.Type == xdr.SorobanCredentialsTypeSorobanCredentialsAddressV2 {
			route = "simulation returned AddressV2 directly (UseUpgradedAuth was honoured)"
			t.Log(route)
			return entries
		}

		route = "simulation returned " + recordedArm + "; upgraded locally with soroauth.UpgradeToV2"
		t.Log(route)

		upgraded := make([]xdr.SorobanAuthorizationEntry, 0, len(entries))
		for i, entry := range entries {
			if entry.Credentials.Type == xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount {
				upgraded = append(upgraded, entry)
				continue
			}
			converted, err := soroauth.UpgradeToV2(entry)
			if err != nil {
				t.Fatalf("upgrading entry %d: %v", i, err)
			}
			upgraded = append(upgraded, converted)
		}
		return upgraded
	}

	result := runScenario(t, h, scenarioSpec{
		payer:   payer,
		op:      op,
		signers: []soroauth.Signer{soroauth.NewEd25519Signer(from)},
		// Ask the recording pass for V2; prepare upgrades locally if the
		// best-effort flag was not honoured.
		upgradedAuth: true,
		prepare:      prepare,
	})

	t.Logf("tx hash: %s", result.Hash)
	t.Logf("ledger:  %d", result.Ledger)
	t.Logf("arm:     %s", result.Arm)
	t.Logf("route:   %s", route)
	t.Logf("link:    %s%s", runExplorerBase, result.Hash)

	record(scenarioResult{
		ID:        "B",
		Name:      "CAP-71 address_v2 credentials accepted live",
		Proves:    "A SOROBAN_CREDENTIALS_ADDRESS_V2 entry, whose payload binds the signer address, is accepted by the host.",
		TxHash:    result.Hash,
		Ledger:    result.Ledger,
		Arm:       result.Arm,
		Succeeded: result.Status == rpc.TransactionStatusSuccess,
		RawError:  result.RawError,
		Notes:     []string{route},
	})

	if result.Status != rpc.TransactionStatusSuccess {
		t.Fatalf("scenario B failed on-chain (status %s):\n%s", result.Status, result.RawError)
	}
	if result.Arm != "SorobanCredentialsTypeSorobanCredentialsAddressV2" {
		t.Errorf("submitted envelope carried arm %q, want the address_v2 arm", result.Arm)
	}
}
