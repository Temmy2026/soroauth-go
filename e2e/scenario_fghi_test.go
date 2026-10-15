//go:build e2e

package e2e

import (
	"fmt"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	rpc "github.com/stellar/go-stellar-sdk/protocols/rpc"

	"github.com/soroauth/soroauth-go"
)

// The scenarios in this file put the two delegate-window fixtures in front of a
// live host. They go through delegatesFlow below: the wrapping, the per-address
// signing and the two simulation passes are identical, and the only thing that
// changes is which account contract the transfer is authorized by and what its
// __check_auth decides.

// delegatesFlow adapts the delegate helper's original signature to runScenario,
// the one runner every scenario uses since #196 unified the three copied flows.
//
// These four scenarios were written against that original signature
// (t, h, payer, contract, to, delegatesToAttach, signers, expectFailure). Rather
// than restore a second copy of the record/sign/enforce/submit flow — exactly
// what #196 removed — this turns the arguments into a scenarioSpec and lets
// runScenario do the work, so there stays one place that decides how a scenario
// runs.
func delegatesFlow(
	t *testing.T,
	h *harness,
	payer *keypair.Full,
	contract string,
	to *keypair.Full,
	delegatesToAttach []string,
	signers []*keypair.Full,
	expectFailure bool,
) submission {
	t.Helper()

	op := h.transferOp(t, scAddressOf(t, contract), scAddressOf(t, to.Address()),
		transferAmount, payer.Address())

	flowSigners := make([]soroauth.Signer, 0, len(signers))
	for _, kp := range signers {
		flowSigners = append(flowSigners, soroauth.NewEd25519Signer(kp))
	}

	return runScenario(t, h, scenarioSpec{
		payer:         payer,
		op:            op,
		upgradedAuth:  true,
		signers:       flowSigners,
		prepare:       prepareDelegates(contract, delegatesToAttach),
		expectFailure: expectFailure,
	})
}

// TestScenarioF proves a session key inside its window is accepted by a live
// host: a contract account holding XLM transfers it, authorized only by a
// G-account session key whose own ledger window contains the ledger the
// transaction executes in.
func TestScenarioF(t *testing.T) {
	h := newHarness(t)

	payer := h.newAccount(t, "payer P")
	sessionSigner := h.newAccount(t, "session key K")
	to := h.newAccount(t, "recipient B")

	// The window opens at the ledger the fixture is deployed in and closes
	// well after the run, so every pass below is inside it. ValidFrom is read
	// before the deploy, which takes several ledgers, so the deployed account's
	// window has already opened by the time anything is authorized against it.
	now := h.latestLedger(t)
	windowFrom := now
	windowUntil := now + 2000

	deployed := h.deploySessionKeys(t, payer, []sessionKey{{
		Address:    sessionSigner.Address(),
		ValidFrom:  windowFrom,
		ValidUntil: windowUntil,
	}})
	h.fundContract(t, payer, deployed.ContractAddress, transferAmount*5)

	result := delegatesFlow(t, h, payer, deployed.ContractAddress, to,
		[]string{sessionSigner.Address()},
		[]*keypair.Full{sessionSigner},
		false)

	t.Logf("contract:  %s", deployed.ContractAddress)
	t.Logf("deploy tx: %s", deployed.CreateTxHash)
	t.Logf("tx hash:   %s", result.Hash)
	t.Logf("ledger:    %d", result.Ledger)
	t.Logf("arm:       %s", result.Arm)
	t.Logf("link:      %s%s", runExplorerBase, result.Hash)

	record(scenarioResult{
		ID:        "F",
		Name:      "an in-window session key is accepted live",
		Proves:    "A CAP-71 delegates entry whose only delegate is a G-account session key, inside the ledger window the session-keys contract stored for it, is accepted by the host.",
		TxHash:    result.Hash,
		Ledger:    result.Ledger,
		Arm:       result.Arm,
		Succeeded: result.Status == rpc.TransactionStatusSuccess,
		RawError:  result.RawError,
		Notes: []string{
			"session-keys contract: " + deployed.ContractAddress,
			"wasm upload tx: " + deployed.UploadTxHash,
			"contract deploy tx: " + deployed.CreateTxHash,
			"wasm hash: " + deployed.WasmHash,
			"Registered session key: " + sessionSigner.Address(),
			fmt.Sprintf("Session window: ledgers %d to %d, both inclusive.", windowFrom, windowUntil),
			"The window is checked by the contract, in __check_auth, against env.ledger().sequence(); it is not part of the protocol.",
		},
	})

	if result.Status != rpc.TransactionStatusSuccess {
		t.Fatalf("scenario F failed on-chain (status %s):\n%s", result.Status, result.RawError)
	}
	if result.Arm != "SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates" {
		t.Errorf("submitted envelope carried arm %q, want the delegates arm", result.Arm)
	}
}

// TestScenarioG proves the host refuses what it should: the same flow as F,
// pointed at a session key whose window has already closed. The contract's
// __check_auth returns SessionExpired, and this test asserts that exact
// contract error code rather than merely that the transaction failed.
//
// The entry's own signature_expiration_ledger is set by delegatesFlow to
// roughly a thousand ledgers in the future, so the host's own expiration check
// — which runs only after __check_auth has returned Ok — is not what refuses
// this transaction. The refusal is the contract's, on the contract's clock.
func TestScenarioG(t *testing.T) {
	h := newHarness(t)

	payer := h.newAccount(t, "payer P")
	sessionSigner := h.newAccount(t, "expired session key K")
	to := h.newAccount(t, "recipient B")

	// A window that closed before the fixture was even deployed.
	now := h.latestLedger(t)
	windowFrom := now - 100
	windowUntil := now - 50

	deployed := h.deploySessionKeys(t, payer, []sessionKey{{
		Address:    sessionSigner.Address(),
		ValidFrom:  windowFrom,
		ValidUntil: windowUntil,
	}})
	h.fundContract(t, payer, deployed.ContractAddress, transferAmount*5)

	result := delegatesFlow(t, h, payer, deployed.ContractAddress, to,
		[]string{sessionSigner.Address()},
		[]*keypair.Full{sessionSigner},
		true)

	t.Logf("contract:  %s", deployed.ContractAddress)
	t.Logf("deploy tx: %s", deployed.CreateTxHash)
	t.Logf("tx hash:   %s", result.Hash)
	t.Logf("status:    %s", result.Status)
	t.Logf("arm:       %s", result.Arm)
	t.Logf("RAW ERROR:\n%s", result.RawError)

	record(scenarioResult{
		ID:              "G",
		Name:            "the host rejects an expired session key",
		Proves:          "A delegates entry naming a session key whose ledger window has closed is refused by the contract's __check_auth with its SessionExpired error.",
		TxHash:          result.Hash,
		Ledger:          result.Ledger,
		Arm:             result.Arm,
		Succeeded:       result.Status != rpc.TransactionStatusSuccess,
		ExpectRejection: true,
		RawError:        result.RawError,
		Notes: []string{
			"session-keys contract: " + deployed.ContractAddress,
			"contract deploy tx: " + deployed.CreateTxHash,
			"Registered session key: " + sessionSigner.Address(),
			fmt.Sprintf("Session window: ledgers %d to %d, closed before deployment.", windowFrom, windowUntil),
			"SessionKeyError::SessionExpired is contract error #4 in e2e/contracts/session-keys/src/lib.rs.",
			"The entry's own signature_expiration_ledger is still in the future; the host checks that after __check_auth, so the refusal reported here is the contract's.",
		},
	})

	if result.Status == rpc.TransactionStatusSuccess {
		t.Fatal("the host accepted an expired session key; scenario G proves nothing if this succeeds")
	}
	if result.RawError == "" {
		t.Error("no raw error was captured, so there is nothing to report")
	}

	// The transaction must have been refused by the account contract with
	// SessionExpired (#4), not for some unrelated reason such as running out
	// of instructions before __check_auth was ever reached.
	const sessionExpired = 4
	assertContractErrorCode(t, result.Diagnostics, sessionExpired,
		"SessionKeyError::SessionExpired")
}

// TestScenarioH proves a threshold account accepts exactly M signatures: a
// 2-of-3 account with two of its three signers attached and signed transfers
// its XLM.
func TestScenarioH(t *testing.T) {
	h := newHarness(t)

	payer := h.newAccount(t, "payer P")
	s1 := h.newAccount(t, "signer S1")
	s2 := h.newAccount(t, "signer S2")
	s3 := h.newAccount(t, "signer S3")
	to := h.newAccount(t, "recipient B")

	deployed := h.deployThresholdAccount(t, payer,
		[]string{s1.Address(), s2.Address(), s3.Address()}, 2)
	h.fundContract(t, payer, deployed.ContractAddress, transferAmount*5)

	// Exactly M of N: S3 is registered but not attached, and does not sign.
	result := delegatesFlow(t, h, payer, deployed.ContractAddress, to,
		[]string{s1.Address(), s2.Address()},
		[]*keypair.Full{s1, s2},
		false)

	t.Logf("contract:  %s", deployed.ContractAddress)
	t.Logf("deploy tx: %s", deployed.CreateTxHash)
	t.Logf("tx hash:   %s", result.Hash)
	t.Logf("ledger:    %d", result.Ledger)
	t.Logf("arm:       %s", result.Arm)
	t.Logf("link:      %s%s", runExplorerBase, result.Hash)

	record(scenarioResult{
		ID:        "H",
		Name:      "an M-of-N account accepts exactly M signatures",
		Proves:    "A 2-of-3 threshold account is authorized by exactly two signed delegates, so AuthorizeAll does not need to sign every delegate for the account to accept.",
		TxHash:    result.Hash,
		Ledger:    result.Ledger,
		Arm:       result.Arm,
		Succeeded: result.Status == rpc.TransactionStatusSuccess,
		RawError:  result.RawError,
		Notes: []string{
			"threshold-account contract: " + deployed.ContractAddress,
			"wasm upload tx: " + deployed.UploadTxHash,
			"contract deploy tx: " + deployed.CreateTxHash,
			"wasm hash: " + deployed.WasmHash,
			"Registered signers (N=3): " + s1.Address() + ", " + s2.Address() + ", " + s3.Address(),
			"Attached and signed (M=2): " + s1.Address() + ", " + s2.Address(),
			"S3 was not attached and did not sign. This is the case RequireAllSigned (issue #103) would forbid.",
		},
	})

	if result.Status != rpc.TransactionStatusSuccess {
		t.Fatalf("scenario H failed on-chain (status %s):\n%s", result.Status, result.RawError)
	}
	if result.Arm != "SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates" {
		t.Errorf("submitted envelope carried arm %q, want the delegates arm", result.Arm)
	}
}

// TestScenarioI proves the host refuses what it should: the same 2-of-3
// account, with M-1 signers attached. The contract's __check_auth returns
// InsufficientSignatures, and this test asserts that exact contract error code
// rather than merely that the transaction failed.
func TestScenarioI(t *testing.T) {
	h := newHarness(t)

	payer := h.newAccount(t, "payer P")
	s1 := h.newAccount(t, "signer S1")
	s2 := h.newAccount(t, "signer S2")
	s3 := h.newAccount(t, "signer S3")
	to := h.newAccount(t, "recipient B")

	deployed := h.deployThresholdAccount(t, payer,
		[]string{s1.Address(), s2.Address(), s3.Address()}, 2)
	h.fundContract(t, payer, deployed.ContractAddress, transferAmount*5)

	// M-1: one signer attached and signed, against a threshold of two.
	result := delegatesFlow(t, h, payer, deployed.ContractAddress, to,
		[]string{s1.Address()},
		[]*keypair.Full{s1},
		true)

	t.Logf("contract:  %s", deployed.ContractAddress)
	t.Logf("deploy tx: %s", deployed.CreateTxHash)
	t.Logf("tx hash:   %s", result.Hash)
	t.Logf("status:    %s", result.Status)
	t.Logf("arm:       %s", result.Arm)
	t.Logf("RAW ERROR:\n%s", result.RawError)

	record(scenarioResult{
		ID:              "I",
		Name:            "an M-of-N account rejects M-1 signatures",
		Proves:          "A 2-of-3 threshold account with one signed delegate attached is refused by the contract's __check_auth with its InsufficientSignatures error.",
		TxHash:          result.Hash,
		Ledger:          result.Ledger,
		Arm:             result.Arm,
		Succeeded:       result.Status != rpc.TransactionStatusSuccess,
		ExpectRejection: true,
		RawError:        result.RawError,
		Notes: []string{
			"threshold-account contract: " + deployed.ContractAddress,
			"contract deploy tx: " + deployed.CreateTxHash,
			"Registered signers (N=3): " + s1.Address() + ", " + s2.Address() + ", " + s3.Address(),
			"Attached and signed (M-1=1): " + s1.Address(),
			"ThresholdError::InsufficientSignatures is contract error #3 in e2e/contracts/threshold-account/src/lib.rs.",
		},
	})

	if result.Status == rpc.TransactionStatusSuccess {
		t.Fatal("the host accepted M-1 signatures; scenario I proves nothing if this succeeds")
	}
	if result.RawError == "" {
		t.Error("no raw error was captured, so there is nothing to report")
	}

	// The transaction must have been refused by the account contract with
	// InsufficientSignatures (#3), not for some unrelated reason such as
	// running out of instructions before __check_auth was ever reached.
	const insufficientSignatures = 3
	assertContractErrorCode(t, result.Diagnostics, insufficientSignatures,
		"ThresholdError::InsufficientSignatures")
}

// assertContractErrorCode fails the test unless the contract error code wanted
// appears in the host's diagnostics, naming the variant it stands for.
func assertContractErrorCode(t *testing.T, diagnostics []string, wanted uint32, name string) {
	t.Helper()

	codes := contractErrorCodes(t, diagnostics)
	for _, code := range codes {
		if code == wanted {
			t.Logf("contract error code %d (%s) confirmed in the host diagnostics", wanted, name)
			return
		}
	}
	t.Errorf("the host reported contract error codes %v, want %d (%s); "+
		"the transaction failed, but not for the reason this scenario is about",
		codes, wanted, name)
}
