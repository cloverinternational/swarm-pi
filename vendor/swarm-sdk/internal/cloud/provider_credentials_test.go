package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

func TestClientSyncHostedProviderCredentialUsesStoredOpenAIOAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := openai.StoreOAuthToken(&openai.OAuthToken{
		AccessToken:  "openai-access-token",
		RefreshToken: "openai-refresh-token",
		APIKey:       "openai-oauth-api-key",
		ExpiresAt:    time.Now().Add(10 * time.Minute).Unix(),
		AccountID:    "acct_123",
	}); err != nil {
		t.Fatalf("StoreOAuthToken: %v", err)
	}

	var gotAuth string
	var gotBody struct {
		Provider  string         `json:"provider"`
		Token     string         `json:"token"`
		Source    string         `json:"source"`
		ExpiresAt string         `json:"expires_at"`
		Metadata  map[string]any `json:"metadata"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/provider-credentials" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"credential": map[string]any{
				"provider": gotBody.Provider,
			},
		})
	}))
	defer server.Close()

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken: "cloud-access-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(10 * time.Minute).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	client := NewClient(&CloudConfig{APIBaseURL: server.URL}, tm, server.Client())
	if err := client.SyncHostedProviderCredential(context.Background(), "openai"); err != nil {
		t.Fatalf("SyncHostedProviderCredential: %v", err)
	}

	if gotAuth != "Bearer cloud-access-token" {
		t.Fatalf("Authorization header = %q, want bearer cloud token", gotAuth)
	}
	if gotBody.Provider != "openai" {
		t.Fatalf("provider = %q", gotBody.Provider)
	}
	if gotBody.Token != "openai-oauth-api-key" {
		t.Fatalf("token = %q", gotBody.Token)
	}
	if gotBody.Source != "oauth" {
		t.Fatalf("source = %q", gotBody.Source)
	}
	if gotBody.Metadata["account_id"] != "acct_123" {
		t.Fatalf("account_id = %#v", gotBody.Metadata["account_id"])
	}
	if gotBody.Metadata["token_kind"] != "api_key" {
		t.Fatalf("token_kind = %#v", gotBody.Metadata["token_kind"])
	}
}

func TestResolveHostedOpenAICredentialUsesOAuthAccessTokenWhenAPIKeyMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := openai.StoreOAuthToken(&openai.OAuthToken{
		AccessToken:  "openai-oauth-access-token",
		RefreshToken: "openai-refresh-token",
		APIKey:       "",
		ExpiresAt:    time.Now().Add(10 * time.Minute).Unix(),
		AccountID:    "acct_123",
	}); err != nil {
		t.Fatalf("StoreOAuthToken: %v", err)
	}

	request, err := resolveHostedOpenAICredential(context.Background())
	if err != nil {
		t.Fatalf("resolveHostedOpenAICredential: %v", err)
	}
	if request.Token != "openai-oauth-access-token" {
		t.Fatalf("token = %q", request.Token)
	}
	if request.Metadata["account_id"] != "acct_123" {
		t.Fatalf("account_id = %#v", request.Metadata["account_id"])
	}
	if request.Metadata["token_kind"] != "access_token" {
		t.Fatalf("token_kind = %#v", request.Metadata["token_kind"])
	}
	if request.ExpiresAt == "" {
		t.Fatalf("expected expires_at for access token sync")
	}
}

func TestResolveHostedOpenAICredentialFallsBackToTUIAccounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Isolate the SwarmOS root so only this test's tui_accounts.json is read
	// (canonical registry location: ~/.swarm/tui_accounts.json).
	t.Setenv("SWARM_HOME", home)

	tuiAccounts := map[string]any{
		"version": "1",
		"accounts": []map[string]any{
			{
				"id":        "openai_test",
				"provider":  "OpenAI",
				"is_active": true,
				"added_at":  time.Now().Unix(),
				"token_data": map[string]any{
					"access_token":  "fallback-access-token",
					"refresh_token": "fallback-refresh-token",
					"api_key":       "fallback-api-key",
					"expires_at":    time.Now().Add(10 * time.Minute).Unix(),
					"account_id":    "acct_fallback",
				},
			},
		},
	}
	data, err := json.Marshal(tuiAccounts)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(paths.In("tui_accounts.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	request, err := resolveHostedOpenAICredential(context.Background())
	if err != nil {
		t.Fatalf("resolveHostedOpenAICredential: %v", err)
	}
	if request.Token != "fallback-api-key" {
		t.Fatalf("token = %q", request.Token)
	}
	if request.Metadata["account_id"] != "acct_fallback" {
		t.Fatalf("account_id = %#v", request.Metadata["account_id"])
	}
	if request.Metadata["token_kind"] != "api_key" {
		t.Fatalf("token_kind = %#v", request.Metadata["token_kind"])
	}
}

func TestStageHostedProviderOAuthFilesCopiesTUIAccounts(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), "source")
	targetHome := filepath.Join(t.TempDir(), "target")
	sourceDir := filepath.Join(sourceHome, ".swarm")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "tui_accounts.json"), []byte(`{"version":"1","accounts":[]}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := stageHostedProviderOAuthFiles(targetHome, sourceHome); err != nil {
		t.Fatalf("stageHostedProviderOAuthFiles: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetHome, ".swarm", "tui_accounts.json")); err != nil {
		t.Fatalf("expected staged tui_accounts.json: %v", err)
	}
}
