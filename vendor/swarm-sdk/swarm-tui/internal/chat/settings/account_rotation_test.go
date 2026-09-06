package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

func writeAccountsFixture(t *testing.T, home string, accounts []authAccount) {
	t.Helper()
	data, err := json.Marshal(authAccountFile{Accounts: accounts})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".swarmos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".swarmos", "tui_accounts.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func openaiTokenData(t *testing.T, access string) json.RawMessage {
	t.Helper()
	td, err := json.Marshal(openai.OAuthToken{AccessToken: access})
	if err != nil {
		t.Fatal(err)
	}
	return td
}

// TestRotateToNextAccount: rotating the codex row advances to the next
// OpenAI-family account (wrapping), flips IsActive, and pushes the new
// account's token into the SDK store.
func TestRotateToNextAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountsFixture(t, home, []authAccount{
		{ID: "a1", Provider: "OpenAI", Email: "a@x.com", IsActive: true, AddedAt: 1, TokenData: openaiTokenData(t, "tok-a")},
		{ID: "a2", Provider: "OpenAI", Email: "b@x.com", IsActive: false, AddedAt: 2, TokenData: openaiTokenData(t, "tok-b")},
		{ID: "zz", Provider: "ClaudeCode", IsActive: true, AddedAt: 3},
	})

	from, to, total, err := RotateToNextAccount("codex")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if total != 2 || from == nil || to == nil || from.ID != "a1" || to.ID != "a2" {
		t.Fatalf("rotate = from %+v to %+v total %d", from, to, total)
	}
	// Active flag flipped in the accounts file.
	accts := AccountsForProvider("codex")
	for _, a := range accts {
		if a.ID == "a2" && !a.IsActive {
			t.Error("a2 not active after rotation")
		}
		if a.ID == "a1" && a.IsActive {
			t.Error("a1 still active after rotation")
		}
	}
	// SDK store now holds a2's token.
	tok, err := openai.GetStoredOAuthToken()
	if err != nil || tok == nil || tok.AccessToken != "tok-b" {
		t.Errorf("SDK store token = %+v err=%v, want tok-b", tok, err)
	}
	// Rotating again wraps back to a1.
	_, to2, _, err := RotateToNextAccount("codex")
	if err != nil || to2.ID != "a1" {
		t.Fatalf("second rotate to %+v err=%v, want a1", to2, err)
	}
}

// TestRotateToNextAccountSingleAccount: nothing to rotate to.
func TestRotateToNextAccountSingleAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountsFixture(t, home, []authAccount{
		{ID: "a1", Provider: "OpenAI", IsActive: true, AddedAt: 1, TokenData: openaiTokenData(t, "tok-a")},
	})
	if _, _, total, err := RotateToNextAccount("codex"); err == nil {
		t.Errorf("expected error with a single account (total=%d)", total)
	}
}

// TestReconcileSDKTokensIntoAccounts: a stored SDK OAuth token with no
// matching account (the state observed after a login that bypassed the
// account registry) is imported as the family's first stacked account.
func TestReconcileSDKTokensIntoAccounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := openai.StoreOAuthToken(&openai.OAuthToken{AccessToken: "live-tok", AccountID: "acct_123"}); err != nil {
		t.Fatal(err)
	}

	imported := ReconcileSDKTokensIntoAccounts()
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}
	accts := AccountsForProvider("codex")
	if len(accts) != 1 || !accts[0].IsActive || accts[0].Provider != "OpenAI" {
		t.Fatalf("accounts after import = %+v", accts)
	}
	var tok openai.OAuthToken
	if err := json.Unmarshal(accts[0].TokenData, &tok); err != nil || tok.AccessToken != "live-tok" {
		t.Errorf("imported token = %+v err=%v", tok, err)
	}
	// Idempotent: second call imports nothing.
	if again := ReconcileSDKTokensIntoAccounts(); again != 0 {
		t.Errorf("second reconcile imported %d, want 0", again)
	}
}
