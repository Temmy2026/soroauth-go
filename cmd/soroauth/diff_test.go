package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroauth/soroauth-go"
)

// diffReport mirrors soroauth.EntryDiff for decoding the CLI's JSON, so the
// test reads what a consumer of the command would actually receive rather than
// the in-process value.
type diffReport struct {
	Identical         bool     `json:"identical"`
	PayloadChanged    bool     `json:"payload_changed"`
	SignaturesChanged bool     `json:"signatures_changed"`
	StaleSignatures   []string `json:"stale_signatures"`
	Changes           []struct {
		Kind           string `json:"kind"`
		Path           string `json:"path"`
		From           string `json:"from"`
		To             string `json:"to"`
		AffectsPayload bool   `json:"affects_payload"`
	} `json:"changes"`
	Summary string `json:"summary"`
	Note    string `json:"note"`
}

// staleEntry returns the signed entry of a vector with its expiration moved and
// every signature left alone — the edit that makes a signature stale. No
// subcommand can produce this, which is the point: it is what arrives from
// somewhere else and what diff exists to catch.
func staleEntry(t *testing.T, v vectorFile) string {
	t.Helper()
	var entry xdr.SorobanAuthorizationEntry
	if err := xdr.SafeUnmarshalBase64(v.SignedEntryXDR, &entry); err != nil {
		t.Fatalf("decoding the signed entry: %v", err)
	}
	switch entry.Credentials.Type {
	case xdr.SorobanCredentialsTypeSorobanCredentialsAddress:
		entry.Credentials.Address.SignatureExpirationLedger = xdr.Uint32(v.ValidUntilLedger + 1)
	case xdr.SorobanCredentialsTypeSorobanCredentialsAddressV2:
		entry.Credentials.AddressV2.SignatureExpirationLedger = xdr.Uint32(v.ValidUntilLedger + 1)
	case xdr.SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates:
		entry.Credentials.AddressWithDelegates.AddressCredentials.SignatureExpirationLedger = xdr.Uint32(v.ValidUntilLedger + 1)
	default:
		t.Fatalf("vector %s is not an address arm", v.Name)
	}
	encoded, err := xdr.MarshalBase64(entry)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return encoded
}

func diffJSON(t *testing.T, before, after string) diffReport {
	t.Helper()
	stdout, _, _ := runCLI(t, "diff", "--before", before, "--after", after, "--json")
	var got diffReport
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("the output is not valid JSON: %v\n%s", err, stdout)
	}
	return got
}

func TestDiffReportsAnUpgrade(t *testing.T) {
	v := loadVector(t, "legacy_single_testnet")

	var legacy xdr.SorobanAuthorizationEntry
	if err := xdr.SafeUnmarshalBase64(v.UnsignedEntryXDR, &legacy); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	upgraded, err := soroauth.UpgradeToV2(legacy)
	if err != nil {
		t.Fatalf("upgrading: %v", err)
	}
	encoded, err := xdr.MarshalBase64(upgraded)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	stdout, _, err := runCLI(t, "diff", "--before", v.UnsignedEntryXDR, "--after", encoded)
	if err != nil {
		t.Fatalf("diff returned an error: %v", err)
	}
	if !strings.Contains(stdout, "payload changed") {
		t.Errorf("the report does not say the payload changed:\n%s", stdout)
	}
	// The payload-affecting marker must survive into the text output, since
	// that is the whole signal in the human report.
	if !strings.Contains(stdout, "! arm") {
		t.Errorf("the arm change is not marked as payload-affecting:\n%s", stdout)
	}

	got := diffJSON(t, v.UnsignedEntryXDR, encoded)
	if !got.PayloadChanged || got.Identical {
		t.Errorf("report = %+v, want a payload change", got)
	}
	if len(got.StaleSignatures) != 0 {
		t.Errorf("StaleSignatures = %v; neither entry is signed", got.StaleSignatures)
	}
}

// TestDiffExitsNonZeroOnStaleSignatures is the contract a CI script depends on:
// the report is printed in full, and the exit code carries the verdict.
func TestDiffExitsNonZeroOnStaleSignatures(t *testing.T) {
	v := loadVector(t, "v2_single_testnet")
	tampered := staleEntry(t, v)

	stdout, _, err := runCLI(t, "diff", "--before", v.SignedEntryXDR, "--after", tampered)
	if err == nil {
		t.Fatal("diff exited zero despite a stale signature")
	}
	if code := ExitCode(err); code != ExitVerificationFailed {
		t.Errorf("exit code = %d, want %d", code, ExitVerificationFailed)
	}
	if !strings.Contains(stdout, "PAYLOAD CHANGED") {
		t.Errorf("the verdict is missing from stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "stale signatures") {
		t.Errorf("the stale-signature section is missing:\n%s", stdout)
	}

	got := diffJSON(t, v.SignedEntryXDR, tampered)
	if len(got.StaleSignatures) != 1 || got.StaleSignatures[0] != "credentials" {
		t.Errorf("StaleSignatures = %v, want [credentials]", got.StaleSignatures)
	}
	if got.SignaturesChanged {
		t.Error("SignaturesChanged = true; the signature bytes were not touched")
	}
}

// TestDiffExitsZeroWhenNothingWentStale separates the exit code from "the
// entries differ": a difference is not a failure, only an invalidated signature
// is.
func TestDiffExitsZeroWhenNothingWentStale(t *testing.T) {
	v := loadVector(t, "v2_single_testnet")

	if _, _, err := runCLI(t, "diff", "--before", v.UnsignedEntryXDR, "--after", v.SignedEntryXDR); err != nil {
		t.Fatalf("diff exited non-zero for a plain signing step: %v", err)
	}
	if _, _, err := runCLI(t, "diff", "--before", v.SignedEntryXDR, "--after", v.SignedEntryXDR); err != nil {
		t.Fatalf("diff exited non-zero for identical entries: %v", err)
	}
}

func TestDiffIdenticalEntries(t *testing.T) {
	v := loadVector(t, "delegates_unsorted_with_nested")

	got := diffJSON(t, v.UnsignedEntryXDR, v.UnsignedEntryXDR)
	if !got.Identical {
		t.Error("identical = false for an entry against itself")
	}
	if len(got.Changes) != 0 {
		t.Errorf("Changes = %+v, want none", got.Changes)
	}
	if got.Note == "" {
		t.Error("Note is empty; a diff with no payload change must still say the passphrase was not compared")
	}
}

func TestDiffUsageErrors(t *testing.T) {
	v := loadVector(t, "v2_single_testnet")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing before",
			args: []string{"diff", "--after", v.UnsignedEntryXDR},
			want: "--before or --before-entries is required",
		},
		{
			name: "missing after",
			args: []string{"diff", "--before", v.UnsignedEntryXDR},
			want: "--after or --after-entries is required",
		},
		{
			name: "both from stdin",
			args: []string{"diff", "--before", "-", "--after", "-"},
			want: "only one of --before and --after can be read from stdin",
		},
		{
			name: "malformed before names the flag it was given, not --entry",
			args: []string{"diff", "--before", "not-base64", "--after", v.UnsignedEntryXDR},
			want: "--before",
		},
		{
			name: "malformed after names the flag it was given, not --entry",
			args: []string{"diff", "--before", v.UnsignedEntryXDR, "--after", "not-base64"},
			want: "--after",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runCLI(t, tt.args...)
			if err == nil {
				t.Fatalf("diff accepted %v", tt.args)
			}
			if code := ExitCode(err); code != ExitUsageError {
				t.Errorf("exit code = %d, want %d", code, ExitUsageError)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want it to contain %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "--entry") {
				t.Errorf("err = %q names --entry, a flag diff does not have", err)
			}
		})
	}
}

func TestDiffJSONFailureStaysOnStdout(t *testing.T) {
	v := loadVector(t, "v2_single_testnet")

	stdout, stderr, err := runCLI(t, "diff", "--before", "not-base64", "--after", v.UnsignedEntryXDR, "--json")
	if err == nil {
		t.Fatal("diff --json accepted a malformed entry")
	}
	var out struct {
		Error string `json:"error"`
	}
	if jsonErr := json.Unmarshal([]byte(stdout), &out); jsonErr != nil {
		t.Fatalf("stdout is not a single JSON object: %v\n%s", jsonErr, stdout)
	}
	if out.Error == "" {
		t.Errorf("the JSON error object has no error field: %s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr is not empty in JSON mode: %q", stderr)
	}
}

// TestDiffNeverEchoesItsInput holds diff to the same rule as every other
// subcommand: an entry may be a signed one, so no error path repeats the blob
// into a terminal scrollback or a CI log.
func TestDiffNeverEchoesItsInput(t *testing.T) {
	const blob = "AAAAAQAAAAAAAAAYbm90LWEtcmVhbC1lbnRyeQ"

	_, stderr, err := runCLI(t, "diff", "--before", blob, "--after", blob)
	if err == nil {
		t.Skip("the blob decoded; this test needs one that does not")
	}
	if strings.Contains(err.Error(), blob) || strings.Contains(stderr, blob) {
		t.Errorf("the input was echoed back: %v / %q", err, stderr)
	}
}

func TestDiffIsListedInUsage(t *testing.T) {
	stdout, _, err := runCLI(t, "help")
	if err != nil {
		t.Fatalf("help returned an error: %v", err)
	}
	if !strings.Contains(stdout, "diff ") {
		t.Errorf("diff is not listed in the usage text:\n%s", stdout)
	}
}
