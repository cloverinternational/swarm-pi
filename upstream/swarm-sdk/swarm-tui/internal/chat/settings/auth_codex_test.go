package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

// TestStartFlowRoutesCodexToOpenAIDeviceFlow locks in that the codex catalog
// row starts the OpenAI device flow (shared token store) instead of falling
// into the "Unknown OAuth provider" error branch.
func TestStartFlowRoutesCodexToOpenAIDeviceFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &AuthSettings{viewState: "list"}
	cmd := a.startFlow(authProviderEntry{Name: "codex", DisplayName: "OpenAI Codex (OAuth)"})
	if a.flowState == "error" {
		t.Fatalf("startFlow(codex) errored: %s", a.flowError)
	}
	if cmd == nil {
		t.Fatal("startFlow(codex) returned no command")
	}
	if a.flowState != "openai_code" {
		t.Errorf("flowState = %q, want openai_code (OpenAI device flow)", a.flowState)
	}
}

// TestAccountsForProviderMatchesWireFamily: OAuth accounts are stored under
// the historical label "OpenAI"; they must surface on the codex row (same wire
// family), and must NOT surface on unrelated rows.
func TestAccountsForProviderMatchesWireFamily(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := map[string]any{"accounts": []map[string]any{
		{"id": "1", "provider": "OpenAI", "is_active": true},
		{"id": "2", "provider": "ClaudeCode", "is_active": true},
	}}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".swarmos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".swarmos", "tui_accounts.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	codexAccts := accountsForProvider("codex")
	if len(codexAccts) != 1 || codexAccts[0].Provider != "OpenAI" {
		t.Errorf("accountsForProvider(codex) = %+v, want the OpenAI account", codexAccts)
	}
	claudeAccts := accountsForProvider("ClaudeCode")
	if len(claudeAccts) != 1 || claudeAccts[0].Provider != "ClaudeCode" {
		t.Errorf("accountsForProvider(ClaudeCode) = %+v, want only the ClaudeCode account", claudeAccts)
	}
	if got := accountsForProvider("xai"); len(got) != 0 {
		t.Errorf("accountsForProvider(xai) = %+v, want none", got)
	}
}

func TestOpenAIAccountRefreshGateHonorsCancellation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := saveAuthAccountFile(authAccountFile{
		Version: "1",
		Accounts: []authAccount{{
			ID:       "codex-1",
			Provider: "OpenAI",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	openAIAccountRefreshGate <- struct{}{}
	defer func() { <-openAIAccountRefreshGate }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := RefreshAccountTokenByIDContext(ctx, "codex-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("RefreshAccountTokenByIDContext error = %v, want context.Canceled", err)
	}
}

func TestAuthSettingsOpenAICompletionUsesCredentialChangedCallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &AuthSettings{viewState: "list"}
	a.SetCredentialChangedCallback(func(provider string) error {
		if provider != "codex" {
			t.Fatalf("provider = %q, want codex", provider)
		}
		return errors.New("live provider rejected credential")
	})

	a.HandleMsg(authOpenAIResultMsg{token: &openai.OAuthToken{
		AccessToken: "access-token",
		AccountID:   "account-id",
	}})

	if a.flowState != "error" {
		t.Fatalf("flowState = %q, want error", a.flowState)
	}
	if a.flowError != "live provider rejected credential" {
		t.Fatalf("flowError = %q", a.flowError)
	}
}
