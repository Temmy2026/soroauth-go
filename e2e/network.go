package e2e

import "strings"

// This file is deliberately not behind the e2e build tag. Everything in it is
// a pure decision — which network, which funding endpoint, which results file —
// and keeping it buildable without the tag means a normal `go test ./...` run
// exercises those decisions even though the scenarios themselves need a live
// network. The scenarios in the tagged files call in here so the choices are
// made in one place rather than branched on a flag at each call site.

// defaultRPCURL is the RPC the suite uses when SOROAUTH_RPC_URL is unset.
const defaultRPCURL = "https://soroban-testnet.stellar.org"

// publicTestnetFriendbot is the testnet friendbot, used only when the network
// advertises none of its own. A local network never falls back to it: funding a
// local address on public testnet would be a silent, confusing wrong.
const publicTestnetFriendbot = "https://friendbot.stellar.org/"

// networkKind is the coarse classification of the network the suite is pointed
// at. It is used for two decisions that must differ by network: whether a
// public explorer link exists, and whether a run may write the evidence-of-record
// results file.
type networkKind int

const (
	// networkOther is a network that is neither testnet nor a local standalone
	// one, for example futurenet.
	networkOther networkKind = iota
	// networkTestnet is Stellar testnet, the evidence of record for RESULTS.md.
	networkTestnet
	// networkLocal is a standalone network, as a quickstart container runs.
	networkLocal
)

// String names the kind for logs.
func (k networkKind) String() string {
	switch k {
	case networkTestnet:
		return "testnet"
	case networkLocal:
		return "local (standalone)"
	default:
		return "other"
	}
}

// classifyNetwork reads the passphrase the RPC reported and names the network.
//
// The quickstart standalone passphrase is "Standalone Network ; February 2017"
// and testnet's is "Test SDF Network ; September 2015"; matching on the stable
// part of each is enough, and anything else is left unclassified rather than
// guessed at.
func classifyNetwork(passphrase string) networkKind {
	switch {
	case strings.Contains(passphrase, "Standalone"):
		return networkLocal
	case strings.Contains(passphrase, "Test SDF Network"):
		return networkTestnet
	default:
		return networkOther
	}
}

// resolveRPCURL returns the RPC to use, preferring SOROAUTH_RPC_URL and falling
// back to testnet. Reading it through a getenv lets tests pass a fake
// environment rather than mutating the process's.
func resolveRPCURL(getenv func(string) string) string {
	if url := strings.TrimSpace(getenv("SOROAUTH_RPC_URL")); url != "" {
		return url
	}
	return defaultRPCURL
}

// explorerBaseURLFor returns the explorer prefix for a transaction hash, or ""
// when the network has no public explorer — a local quickstart has none, and a
// link into someone else's explorer would not resolve.
func explorerBaseURLFor(kind networkKind) string {
	if kind == networkTestnet {
		return "https://stellar.expert/explorer/testnet/tx/"
	}
	return ""
}

// resultsFileName is where a complete run of this network is recorded.
//
// Testnet is the evidence of record, so it owns RESULTS.md. Any other network —
// a local quickstart, a futurenet — is an iteration and writes RESULTS.local.md,
// so it can never overwrite the committed evidence with a run nobody agreed to.
func resultsFileName(kind networkKind) string {
	if kind == networkTestnet {
		return "RESULTS.md"
	}
	return "RESULTS.local.md"
}

// friendbotURL decides where to ask for a funded account.
//
// SOROAUTH_FRIENDBOT_URL wins when set. Otherwise the network's own advertised
// friendbot is used; a quickstart advertises http://localhost:8000/friendbot,
// so a local run funds locally. Only testnet-like networks fall back to the
// public testnet friendbot, and a local network gets "" so the caller can fund
// another way rather than reaching across to a public service.
func friendbotURL(kind networkKind, advertised string, getenv func(string) string) string {
	if override := strings.TrimSpace(getenv("SOROAUTH_FRIENDBOT_URL")); override != "" {
		return override
	}
	if advertised = strings.TrimSpace(advertised); advertised != "" {
		return advertised
	}
	if kind == networkTestnet || kind == networkOther {
		return publicTestnetFriendbot
	}
	return ""
}

// funderSeed returns the seed of an account that funds others, when the
// operator set one. It is the friendbot-free funding path: a local quickstart's
// root account, or any funded account, creates and funds the test accounts by
// transfer instead of asking a service.
func funderSeed(getenv func(string) string) string {
	return strings.TrimSpace(getenv("SOROAUTH_FUNDER_SEED"))
}
