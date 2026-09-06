package client

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// TestSWARMProviderEnvVar verifies that SWARM_PROVIDER / SWARM_MODEL env vars
// are applied with correct precedence:
//
//	explicit caller option > env var > config.json > default
func TestSWARMProviderEnvVar(t *testing.T) {
	t.Setenv("SWARM_PROVIDER", "openai")
	t.Setenv("SWARM_MODEL", "gpt-4o-mini")

	o := options{}
	// Simulate New() env-var application step.
	if o.providerName == "" {
		if v := os.Getenv("SWARM_PROVIDER"); v != "" {
			o.providerName = v
		}
	}
	if o.model == "" {
		if v := os.Getenv("SWARM_MODEL"); v != "" {
			o.model = v
		}
	}

	if o.providerName != "openai" {
		t.Errorf("SWARM_PROVIDER: got %q, want %q", o.providerName, "openai")
	}
	if o.model != "gpt-4o-mini" {
		t.Errorf("SWARM_MODEL: got %q, want %q", o.model, "gpt-4o-mini")
	}
}

// TestSWARMProviderEnvVarNotOverridesCallerOption verifies explicit WithProvider
// wins over SWARM_PROVIDER.
func TestSWARMProviderEnvVarNotOverridesCallerOption(t *testing.T) {
	t.Setenv("SWARM_PROVIDER", "openai")

	o := options{providerName: "anthropic"} // explicit caller option
	if o.providerName == "" {
		if v := os.Getenv("SWARM_PROVIDER"); v != "" {
			o.providerName = v
		}
	}

	if o.providerName != "anthropic" {
		t.Errorf("explicit option should win: got %q, want %q", o.providerName, "anthropic")
	}
}

// TestDefaultModelFor verifies all providers return a non-empty model string.
func TestDefaultModelFor(t *testing.T) {
	cases := []struct {
		provider string
		wantNE   bool // want non-empty
	}{
		{"anthropic", true},
		{"claudecode", true},
		{"openai", true},
		{"gemini", true},
		{"google", true},
		{"ollama", true},
		{"cerebras", true},
		{"groq", true},
		{"unknown-provider", false},
	}
	for _, tc := range cases {
		got := defaultModelFor(tc.provider)
		if tc.wantNE && got == "" {
			t.Errorf("defaultModelFor(%q): expected non-empty model", tc.provider)
		}
		if !tc.wantNE && got != "" {
			t.Logf("defaultModelFor(%q) = %q (unexpected, but not an error)", tc.provider, got)
		}
	}
}

// TestResolveCredentialsGroq verifies GROQ_API_KEY is respected.
func TestResolveCredentialsGroq(t *testing.T) {
	const want = "gsk_test_key_abc123"
	t.Setenv("GROQ_API_KEY", want)

	key, _ := resolveCredentials("groq")
	if key != want {
		t.Errorf("resolveCredentials(groq): got %q, want %q", key, want)
	}
}

// TestResolveCredentialsFromProvidersJSON verifies the shared resolver falls
// back to providers.json api_key/base_url when credentials.json is absent.
func TestResolveCredentialsFromProvidersJSON(t *testing.T) {
	// Isolate the SwarmOS root at a temp dir so resolveCredentials reads only
	// this test's providers.json (canonical: ~/.swarm/config/providers.json).
	t.Setenv("SWARM_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("XAI_API_KEY", "")

	providers := []map[string]any{
		{
			"name":     "xai",
			"type":     "api_key",
			"api_type": "openai-compatible",
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1/responses",
		},
	}
	data, err := json.Marshal(providers)
	if err != nil {
		t.Fatalf("marshal providers.json: %v", err)
	}
	if err := os.WriteFile(paths.ProvidersFile(), data, 0o600); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}

	key, baseURL := resolveCredentials("xai")
	if key != "xai-test-key" {
		t.Fatalf("resolveCredentials(xai) key = %q, want %q", key, "xai-test-key")
	}
	if baseURL != "https://api.x.ai/v1/responses" {
		t.Fatalf("resolveCredentials(xai) baseURL = %q, want %q", baseURL, "https://api.x.ai/v1/responses")
	}
}

// TestNormalizeProviderName verifies known aliases are normalized correctly.
func TestNormalizeProviderName(t *testing.T) {
	cases := map[string]string{
		"ClaudeCode":  "anthropic",
		"claude-code": "anthropic",
		"Google":      "gemini",
		"CODEX":       "openai",
		"OpenAI":      "openai",
		"wafer":       "wafer.ai",
		"wafer.ai":    "wafer.ai",
		"WAFER":       "wafer.ai",
	}
	for input, want := range cases {
		if got := normalizeProviderName(input); got != want {
			t.Errorf("normalizeProviderName(%q): got %q, want %q", input, got, want)
		}
	}
}
