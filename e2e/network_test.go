package e2e

import "testing"

// fakeEnv makes a getenv from a map, so these tests never touch the process
// environment.
func fakeEnv(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestClassifyNetwork(t *testing.T) {
	cases := []struct {
		name       string
		passphrase string
		want       networkKind
	}{
		{"testnet", "Test SDF Network ; September 2015", networkTestnet},
		{"local quickstart", "Standalone Network ; February 2017", networkLocal},
		{"futurenet", "Test SDF Future Network ; October 2022", networkOther},
		{"empty", "", networkOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyNetwork(tc.passphrase); got != tc.want {
				t.Errorf("classifyNetwork(%q) = %v, want %v", tc.passphrase, got, tc.want)
			}
		})
	}
}

func TestResolveRPCURL(t *testing.T) {
	if got := resolveRPCURL(fakeEnv(map[string]string{})); got != defaultRPCURL {
		t.Errorf("with no SOROAUTH_RPC_URL got %q, want the testnet default %q", got, defaultRPCURL)
	}
	local := "http://localhost:8000/rpc"
	if got := resolveRPCURL(fakeEnv(map[string]string{"SOROAUTH_RPC_URL": local})); got != local {
		t.Errorf("got %q, want %q", got, local)
	}
	// Whitespace-only must not be treated as a URL.
	if got := resolveRPCURL(fakeEnv(map[string]string{"SOROAUTH_RPC_URL": "   "})); got != defaultRPCURL {
		t.Errorf("a blank SOROAUTH_RPC_URL should fall back to the default, got %q", got)
	}
}

func TestExplorerBaseURLFor(t *testing.T) {
	if got := explorerBaseURLFor(networkTestnet); got == "" {
		t.Error("testnet should have an explorer prefix")
	}
	for _, kind := range []networkKind{networkLocal, networkOther} {
		if got := explorerBaseURLFor(kind); got != "" {
			t.Errorf("explorerBaseURLFor(%v) = %q, want empty", kind, got)
		}
	}
}

// TestResultsFileName pins the rule that keeps a local run from overwriting the
// committed testnet evidence.
func TestResultsFileName(t *testing.T) {
	if got := resultsFileName(networkTestnet); got != "RESULTS.md" {
		t.Errorf("testnet results file = %q, want RESULTS.md", got)
	}
	for _, kind := range []networkKind{networkLocal, networkOther} {
		if got := resultsFileName(kind); got != "RESULTS.local.md" {
			t.Errorf("resultsFileName(%v) = %q, want RESULTS.local.md", kind, got)
		}
	}
}

func TestFriendbotURL(t *testing.T) {
	cases := []struct {
		name       string
		kind       networkKind
		advertised string
		env        map[string]string
		want       string
	}{
		{
			name:       "testnet uses its advertised friendbot",
			kind:       networkTestnet,
			advertised: "https://friendbot-testnet.stellar.org/",
			want:       "https://friendbot-testnet.stellar.org/",
		},
		{
			name:       "local uses its advertised friendbot, not the public one",
			kind:       networkLocal,
			advertised: "http://localhost:8000/friendbot",
			want:       "http://localhost:8000/friendbot",
		},
		{
			name: "testnet falls back to the public friendbot when none advertised",
			kind: networkTestnet,
			want: publicTestnetFriendbot,
		},
		{
			name: "local never falls back to the public friendbot",
			kind: networkLocal,
			want: "",
		},
		{
			name:       "an explicit override wins",
			kind:       networkLocal,
			advertised: "http://localhost:8000/friendbot",
			env:        map[string]string{"SOROAUTH_FRIENDBOT_URL": "http://localhost:9000/friendbot"},
			want:       "http://localhost:9000/friendbot",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := friendbotURL(tc.kind, tc.advertised, fakeEnv(tc.env))
			if got != tc.want {
				t.Errorf("friendbotURL(%v, %q) = %q, want %q", tc.kind, tc.advertised, got, tc.want)
			}
		})
	}
}

func TestFunderSeed(t *testing.T) {
	if got := funderSeed(fakeEnv(map[string]string{})); got != "" {
		t.Errorf("with no SOROAUTH_FUNDER_SEED got %q, want empty", got)
	}
	if got := funderSeed(fakeEnv(map[string]string{"SOROAUTH_FUNDER_SEED": " SABC "})); got != "SABC" {
		t.Errorf("got %q, want the trimmed seed %q", got, "SABC")
	}
}
