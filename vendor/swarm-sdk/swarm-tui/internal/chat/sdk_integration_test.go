package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestGetOpenAIAuth_RegistryTokenWithEmptyAccountID verifies that getOpenAIAuth
// returns an error when the account registry returns a token without AccountID.
// This is a regression test for the bug where empty AccountID would be passed
// to the Codex provider, causing "account does not have permissions" errors.
func TestGetOpenAIAuth_RegistryTokenWithEmptyAccountID(t *testing.T) {
	// Save original HOME and restore after test
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)

	// Create temp directory for test
	tempDir, err := os.MkdirTemp("", "tui-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Set HOME to temp directory so tryLoadAccountFromRegistry finds our test file
	os.Setenv("HOME", tempDir)

	// Create account registry structure
	accountsDir := filepath.Join(tempDir, ".swarmos", "accounts", "openai")
	if err := os.MkdirAll(accountsDir, 0755); err != nil {
		t.Fatalf("failed to create accounts dir: %v", err)
	}

	// Create account file with AccessToken but empty/missing AccountID
	// This simulates the bug condition
	token := genericOAuthToken{
		AccessToken: "test-access-token",
		AccountID:   "", // Empty AccountID - the bug condition
		IDToken:     "", // No IDToken either, so extraction will also fail
	}
	tokenData, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("failed to marshal token: %v", err)
	}

	tokenPath := filepath.Join(accountsDir, "account-test.json")
	if err := os.WriteFile(tokenPath, tokenData, 0600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}

	// Call getOpenAIAuth - should return error for empty AccountID
	ctx := context.Background()
	auth, err := getOpenAIAuth(ctx, "oauth", "openai")

	// BUG: Currently this returns success with empty accountID
	// EXPECTED (after fix): Should return error or fall through to other auth methods
	//
	// This test verifies the fix - if accountID is empty, it should NOT return
	// a successful auth from the registry path
	if err == nil && auth.accountID == "" && auth.accessToken != "" {
		t.Errorf("BUG: getOpenAIAuth returned auth with empty accountID: %+v", auth)
		t.Error("Expected: error or fallthrough when accountID is empty")
	}

	// If we got an error mentioning accountID or credentials, that's the expected fix behavior
	if err != nil {
		// This is expected after the fix
		t.Logf("Got expected error: %v", err)
	}
}

// TestGetOpenAIAuth_RegistryTokenWithValidAccountID verifies normal operation
// when the account registry has a valid token with AccountID.
func TestGetOpenAIAuth_RegistryTokenWithValidAccountID(t *testing.T) {
	// Save original HOME and restore after test
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)

	// Create temp directory for test
	tempDir, err := os.MkdirTemp("", "tui-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Set HOME to temp directory
	os.Setenv("HOME", tempDir)

	// Create account registry structure
	accountsDir := filepath.Join(tempDir, ".swarmos", "accounts", "openai")
	if err := os.MkdirAll(accountsDir, 0755); err != nil {
		t.Fatalf("failed to create accounts dir: %v", err)
	}

	// Create account file with valid AccessToken AND AccountID
	token := genericOAuthToken{
		AccessToken: "test-access-token",
		AccountID:   "test-account-id", // Valid AccountID
	}
	tokenData, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("failed to marshal token: %v", err)
	}

	tokenPath := filepath.Join(accountsDir, "account-test.json")
	if err := os.WriteFile(tokenPath, tokenData, 0600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}

	// Call getOpenAIAuth - should succeed with valid accountID
	ctx := context.Background()
	auth, err := getOpenAIAuth(ctx, "oauth", "openai")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if auth.accountID != "test-account-id" {
		t.Errorf("expected accountID 'test-account-id', got '%s'", auth.accountID)
	}

	if auth.accessToken != "test-access-token" {
		t.Errorf("expected accessToken 'test-access-token', got '%s'", auth.accessToken)
	}
}

func TestNormalizeCerebrasBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty uses default", "", "https://api.cerebras.ai/v1"},
		{"root without v1", "https://api.cerebras.ai", "https://api.cerebras.ai/v1"},
		{"with v1", "https://api.cerebras.ai/v1", "https://api.cerebras.ai/v1"},
		{"with trailing slash", "https://api.cerebras.ai/v1/", "https://api.cerebras.ai/v1"},
		{"with chat path", "https://api.cerebras.ai/v1/chat/completions", "https://api.cerebras.ai/v1"},
		{"with chat path trailing slash", "https://api.cerebras.ai/v1/chat/completions/", "https://api.cerebras.ai/v1"},
		{"custom base missing v1", "https://staging.cerebras.ai/api", "https://staging.cerebras.ai/api/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCerebrasBaseURL(tt.in)
			if got != tt.want {
				t.Fatalf("normalizeCerebrasBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeOpenAIOAuthBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty uses provider default", "", ""},
		{"api root", "https://api.openai.com", ""},
		{"api v1", "https://api.openai.com/v1", ""},
		{"api v1 trailing slash", "https://api.openai.com/v1/", ""},
		{"api responses path", "https://api.openai.com/v1/responses", ""},
		{"api chat completions path", "https://api.openai.com/v1/chat/completions", ""},
		{"codex base", "https://chatgpt.com/backend-api/codex", "https://chatgpt.com/backend-api/codex"},
		{"codex responses path", "https://chatgpt.com/backend-api/codex/responses", "https://chatgpt.com/backend-api/codex"},
		{"custom base preserved", "https://example.com/custom-codex", "https://example.com/custom-codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOpenAIOAuthBaseURL(tt.in)
			if got != tt.want {
				t.Fatalf("normalizeOpenAIOAuthBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseStringMapJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "valid object with mixed scalar values",
			in:   `{"flag":true,"count":3,"name":"codex"}`,
			want: map[string]string{
				"flag":  "true",
				"count": "3",
				"name":  "codex",
			},
		},
		{
			name: "invalid json",
			in:   `{"a":`,
			want: nil,
		},
		{
			name: "empty",
			in:   "",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStringMapJSON(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseStringMapJSON(%q)=%v want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveCodexPassthrough_MergeConfigAndEnv(t *testing.T) {
	t.Setenv(codexQueryParamsEnv, `{"alpha":"env","beta":2}`)
	t.Setenv(codexHTTPHeadersEnv, `{"x-experiment":"1"}`)

	cfg := &ProviderConfig{
		CodexQueryParams: map[string]string{
			"alpha": "provider",
			"gamma": "keep",
		},
		CodexHTTPHeaders: map[string]string{
			"x-origin": "swarm",
		},
	}

	query, headers := resolveCodexPassthrough(cfg)

	wantQuery := map[string]string{
		"alpha": "env",
		"beta":  "2",
		"gamma": "keep",
	}
	wantHeaders := map[string]string{
		"x-origin":     "swarm",
		"x-experiment": "1",
	}

	if !reflect.DeepEqual(query, wantQuery) {
		t.Fatalf("query passthrough mismatch: got %v want %v", query, wantQuery)
	}
	if !reflect.DeepEqual(headers, wantHeaders) {
		t.Fatalf("header passthrough mismatch: got %v want %v", headers, wantHeaders)
	}
}
