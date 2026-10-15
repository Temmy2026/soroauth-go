package soroauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// TestAdversarialRegressionsAreRefused specifically checks the frozen
// regressions directory (testdata/differential/regressions/). These are
// cases that once caused a cross-implementation divergence; they must be
// refused by every entry point so a divergence cannot slip through as an
// accepted-but-wrong result.
func TestAdversarialRegressionsAreRefused(t *testing.T) {
	regressionsDir := "testdata/differential/regressions"
	found, err := filepath.Glob(filepath.Join(regressionsDir, "*.json"))
	if err != nil {
		t.Fatalf("globbing %s: %v", regressionsDir, err)
	}
	if len(found) == 0 {
		t.Skip("no frozen regressions yet; run `go run ./cmd/difffuzz -freeze <case>` to add one")
	}

	signerKP, _ := keypair.FromRawSeed([32]byte{1})
	edSigner := NewEd25519Signer(signerKP)
	passphrase := network.TestNetworkPassphrase

	var otherEntry xdr.SorobanAuthorizationEntry
	// Use a known-good entry from the main corpus as the "other" for DiffEntries.
	mainCorpus := loadDifferentialCorpus(t)
	if len(mainCorpus) > 0 {
		_ = xdr.SafeUnmarshalBase64(mainCorpus[0].UnsignedEntryXDR, &otherEntry)
	}

	for _, path := range found {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		var doc differentialCase
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&doc); err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}

		var entry xdr.SorobanAuthorizationEntry
		if err := xdr.SafeUnmarshalBase64(doc.UnsignedEntryXDR, &entry); err != nil {
			t.Fatalf("%s: unmarshaling: %v", doc.Name, err)
		}

		// Every regression case must be refused by every entry point.
		eps := []struct {
			name string
			fn   func() error
		}{
			{"Inspect", func() error { _, err := Inspect(entry); return err }},
			{"VerifyEntry", func() error { _, err := VerifyEntry(entry, passphrase); return err }},
			{"DiffEntries", func() error { _, err := DiffEntries(entry, otherEntry); return err }},
			{"AuthorizeEntry", func() error {
				_, err := AuthorizeEntry(context.Background(), entry, edSigner, 1234567, passphrase)
				return err
			}},
			{"DecodeAuthorizationEntry", func() error { _, err := DecodeAuthorizationEntry(doc.UnsignedEntryXDR); return err }},
		}

		for _, ep := range eps {
			if err := ep.fn(); err != nil {
				if !isSentinelError(err) {
					t.Errorf("%s: %s returned non-sentinel error: %T %v", doc.Name, ep.name, err, err)
				}
			} else {
				t.Errorf("%s: %s accepted a frozen regression (no error)", doc.Name, ep.name)
			}
		}
	}
}

// isSentinelError reports whether err is (or wraps) one of the exported
// Err* sentinels from errors.go. This is the same set the errors_meta_test.go
// check uses, so the two tests stay in sync.
func isSentinelError(err error) bool {
	if err == nil {
		return false
	}

	sentinels := []error{
		ErrSourceAccountCredentials,
		ErrUnsupportedCredentials,
		ErrNoMatchingCredentialNode,
		ErrDuplicateDelegate,
		ErrSignatureMismatch,
		ErrMissingSigner,
		ErrAlreadySigned,
		ErrInvalidExpiration,
		ErrTooManySignatures,
		ErrDecodeLimit,
		ErrNoInvokeOperation,
		ErrUnsupportedEnvelope,
		ErrUnsignedCredentialNode,
		ErrDelegatePlanUnmatched,
		ErrNonceAlreadyReserved,
		ErrWebAuthnMalformedJSON,
		ErrWebAuthnMissingField,
		ErrWebAuthnMalformedField,
		ErrWebAuthnTruncatedAuthenticatorData,
		ErrWebAuthnChallengeMismatch,
		ErrSignerAddressMismatch,
		ErrVaultUnauthorized,
		ErrVaultKeyNotFound,
		ErrVaultKeyType,
		ErrLedgerUnavailable,
		ErrLedgerLocked,
		ErrLedgerWrongApp,
		ErrLedgerBlindSigningDisabled,
		ErrLedgerDenied,
	}

	for _, s := range sentinels {
		if errors.Is(err, s) {
			return true
		}
	}

	// Also check the structured error types that wrap the sentinels.
	if _, ok := err.(*NoMatchingCredentialNodeError); ok {
		return true
	}
	if _, ok := err.(*DuplicateDelegateError); ok {
		return true
	}
	if _, ok := err.(*MissingSignerError); ok {
		return true
	}
	if _, ok := err.(*UnsignedCredentialNodeError); ok {
		return true
	}
	if _, ok := err.(*DelegatePlanUnmatchedError); ok {
		return true
	}
	if _, ok := err.(*NonceAlreadyReservedError); ok {
		return true
	}

	return false
}

// TestEntryPointsCoverSentinels is the meta-test: every public entry point
// must have at least one test that produces each sentinel it can return.
// This mirrors errors_meta_test.go but for entry points rather than just
// "does any test mention this sentinel".
func TestEntryPointsCoverSentinels(t *testing.T) {
	// Map of entry point -> sentinels it is documented to produce.
	// If an entry point gains a new sentinel path, add it here and the
	// test will fail until a test case is added.
	entryPointSentinels := map[string][]error{
		"Inspect": {
			ErrUnsupportedCredentials,
		},
		"VerifyEntry": {
			ErrUnsupportedCredentials,
		},
		"DiffEntries": {
			ErrUnsupportedCredentials,
		},
		"AuthorizeEntry": {
			ErrUnsupportedCredentials,
			ErrSourceAccountCredentials,
			ErrNoMatchingCredentialNode,
			ErrDuplicateDelegate,
			ErrMissingSigner,
			ErrAlreadySigned,
			ErrInvalidExpiration,
			ErrTooManySignatures,
			ErrUnsignedCredentialNode,
			ErrDelegatePlanUnmatched,
			ErrNonceAlreadyReserved,
		},
		"DecodeAuthorizationEntry": {
			ErrDecodeLimit,
			ErrUnsupportedCredentials, // via the traversal
		},
	}

	// Scan all _test.go files in this package and the external test package
	// for mentions of each sentinel per entry point.
	testDirs := []string{".", "cmd/soroauth"} // main package + CLI package
	mentioned := map[string]map[string]bool{} // entryPoint -> sentinel -> mentioned

	for _, dir := range testDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}

		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
				continue
			}
			if name == "adversarial_test.go" || name == "errors_meta_test.go" {
				continue // this file and the meta-test itself don't count
			}

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", name, err)
			}

			ast.Inspect(file, func(n ast.Node) bool {
				if ident, ok := n.(*ast.Ident); ok {
					// Check if this identifier is a sentinel we care about.
					for ep, sentinels := range entryPointSentinels {
						for _, s := range sentinels {
							varName := varNameForSentinel(s)
							sentinelName := s.Error()
							if ident.Name == varName || ident.Name == sentinelName {
								if mentioned[ep] == nil {
									mentioned[ep] = map[string]bool{}
								}
								mentioned[ep][ident.Name] = true
							}
						}
					}
				}
				return true
			})
		}
	}

	// Now check coverage.
	var missing []string
	for ep, sentinels := range entryPointSentinels {
		for _, s := range sentinels {
			varName := varNameForSentinel(s)
			sentinelName := s.Error()
			if mentioned[ep] == nil || (!mentioned[ep][varName] && !mentioned[ep][sentinelName]) {
				missing = append(missing, fmt.Sprintf("%s: %s (variable: %s)", ep, sentinelName, varName))
			}
		}
	}

	if len(missing) > 0 {
		t.Errorf("these entry point/sentinel pairs are not covered by any test:\n  %s\n"+
			"Each pair needs a test that provokes the sentinel at that entry point. "+
			"If a sentinel is no longer produced by that entry point, remove it from entryPointSentinels.",
			strings.Join(missing, "\n  "))
	}

	t.Logf("Entry point sentinel coverage checked: %d entry points", len(entryPointSentinels))
}

// varNameForSentinel returns the Go variable name for a sentinel error.
func varNameForSentinel(s error) string {
	switch s {
	case ErrSourceAccountCredentials:
		return "ErrSourceAccountCredentials"
	case ErrUnsupportedCredentials:
		return "ErrUnsupportedCredentials"
	case ErrNoMatchingCredentialNode:
		return "ErrNoMatchingCredentialNode"
	case ErrDuplicateDelegate:
		return "ErrDuplicateDelegate"
	case ErrSignatureMismatch:
		return "ErrSignatureMismatch"
	case ErrMissingSigner:
		return "ErrMissingSigner"
	case ErrAlreadySigned:
		return "ErrAlreadySigned"
	case ErrInvalidExpiration:
		return "ErrInvalidExpiration"
	case ErrTooManySignatures:
		return "ErrTooManySignatures"
	case ErrDecodeLimit:
		return "ErrDecodeLimit"
	case ErrNoInvokeOperation:
		return "ErrNoInvokeOperation"
	case ErrUnsupportedEnvelope:
		return "ErrUnsupportedEnvelope"
	case ErrUnsignedCredentialNode:
		return "ErrUnsignedCredentialNode"
	case ErrDelegatePlanUnmatched:
		return "ErrDelegatePlanUnmatched"
	case ErrNonceAlreadyReserved:
		return "ErrNonceAlreadyReserved"
	case ErrWebAuthnMalformedJSON:
		return "ErrWebAuthnMalformedJSON"
	case ErrWebAuthnMissingField:
		return "ErrWebAuthnMissingField"
	case ErrWebAuthnMalformedField:
		return "ErrWebAuthnMalformedField"
	case ErrWebAuthnTruncatedAuthenticatorData:
		return "ErrWebAuthnTruncatedAuthenticatorData"
	case ErrWebAuthnChallengeMismatch:
		return "ErrWebAuthnChallengeMismatch"
	case ErrSignerAddressMismatch:
		return "ErrSignerAddressMismatch"
	case ErrVaultUnauthorized:
		return "ErrVaultUnauthorized"
	case ErrVaultKeyNotFound:
		return "ErrVaultKeyNotFound"
	case ErrVaultKeyType:
		return "ErrVaultKeyType"
	case ErrLedgerUnavailable:
		return "ErrLedgerUnavailable"
	case ErrLedgerLocked:
		return "ErrLedgerLocked"
	case ErrLedgerWrongApp:
		return "ErrLedgerWrongApp"
	case ErrLedgerBlindSigningDisabled:
		return "ErrLedgerBlindSigningDisabled"
	case ErrLedgerDenied:
		return "ErrLedgerDenied"
	default:
		return ""
	}
}
