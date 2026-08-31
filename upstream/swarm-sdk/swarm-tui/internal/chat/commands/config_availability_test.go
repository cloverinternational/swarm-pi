package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

// isolateCredentialEnv points HOME at a temp dir and clears credential env
// vars so availability derivation observes only the fixtures a test writes.
func isolateCredentialEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", filepath.Join(home, ".swarm"))
	for _, v := range []string{
		"ANTHROPIC_API_KEY", "CLAUDE_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY",
		"XAI_API_KEY", "ZHIPU_API_KEY", "CURSOR_API_KEY",
	} {
		t.Setenv(v, "")
	}
	return home
}

func writeCredFixture(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func findProvider(t *testing.T, providers []ProviderConfig, name string) *ProviderConfig {
	t.Helper()
	for i := range providers {
		if strings.EqualFold(providers[i].Name, name) {
			return &providers[i]
		}
	}
	t.Fatalf("provider %q not found", name)
	return nil
}

// TestLoadProvidersInjectsCodexIntoExistingFile is the auto-update patch: an
// existing providers.json from any older install must gain the codex entry on
// load, typed oauth so the auth screen lists it.
func TestLoadProvidersInjectsCodexIntoExistingFile(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	raw := `[
      {"name":"ClaudeCode","display_name":"Claude Code (OAuth)","type":"oauth","api_type":"anthropic","available":true,"models":[]},
      {"name":"OpenAI","display_name":"OpenAI (API Key)","type":"api_key","api_type":"openai","available":false,"models":[]}
    ]`
	if err := os.WriteFile(filepath.Join(cm.configDir, "providers.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	cx := findProvider(t, got, "codex")
	if cx.Type != "oauth" || cx.APIType != "openai" {
		t.Errorf("codex entry = type %q api_type %q, want oauth/openai", cx.Type, cx.APIType)
	}
	if len(cx.Models) == 0 {
		t.Error("codex entry has no seed models — picker would show an empty provider")
	}
	// And the repaired file must be persisted so other consumers (desktop) see it.
	data, err := os.ReadFile(filepath.Join(cm.configDir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"codex"`) {
		t.Error("codex entry not persisted back to providers.json")
	}
}

// TestAvailabilityDerivedFromOpenAIOAuthToken reproduces the original bug:
// a stored OpenAI OAuth token must make codex Available — and must NOT light
// up the API-key OpenAI entry.
func TestAvailabilityDerivedFromOpenAIOAuthToken(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	if err := openai.StoreOAuthToken(&openai.OAuthToken{AccessToken: "tok"}); err != nil {
		t.Fatal(err)
	}

	got, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if !findProvider(t, got, "codex").Available {
		t.Error("codex not Available despite stored OpenAI OAuth token (the original bug)")
	}
	if findProvider(t, got, "OpenAI").Available {
		t.Error("OpenAI (API key) must not be Available from an OAuth token")
	}
}

// TestAvailabilityFlipsDownWhenTokenVanishes: derived availability is honest —
// a builtin OAuth entry whose token store is gone goes back to unavailable.
func TestAvailabilityFlipsDownWhenTokenVanishes(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	raw := `[
      {"name":"codex","display_name":"OpenAI Codex (OAuth)","type":"oauth","api_type":"openai","available":true,"models":[{"id":"gpt-5.5","display_name":"GPT-5.5"}]}
    ]`
	if err := os.WriteFile(filepath.Join(cm.configDir, "providers.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if findProvider(t, got, "codex").Available {
		t.Error("codex still Available with no stored token — stale flag not derived away")
	}
}

// TestAvailabilityCustomProviderNeverFlipsDown: we cannot enumerate every way
// a user supplies credentials to a custom provider, so a persisted
// available:true on a non-catalog entry is preserved.
func TestAvailabilityCustomProviderNeverFlipsDown(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	raw := `[
      {"name":"my-proxy","display_name":"My Proxy","type":"api_key","api_type":"openai-compatible","base_url":"http://localhost:9999/v1","available":true,"source":"custom","models":[{"id":"m","display_name":"M"}]}
    ]`
	if err := os.WriteFile(filepath.Join(cm.configDir, "providers.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if !findProvider(t, got, "my-proxy").Available {
		t.Error("custom provider flipped to unavailable — user intent must be preserved")
	}
}

// TestAvailabilityEnvKeysLightUpAPIKeyEntries: catalog-declared env vars make
// the corresponding API-key entries available, replacing the old xAI-only
// hardcoded check with catalog-driven derivation.
func TestAvailabilityEnvKeysLightUpAPIKeyEntries(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	t.Setenv("XAI_API_KEY", "xk")
	t.Setenv("ANTHROPIC_API_KEY", "ak")

	got, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if !findProvider(t, got, "xai-api").Available {
		t.Error("xai-api not Available despite XAI_API_KEY")
	}
	if !findProvider(t, got, "Anthropic").Available {
		t.Error("Anthropic not Available despite ANTHROPIC_API_KEY")
	}
	if !findProvider(t, got, "ClaudeCode").Available {
		t.Error("ClaudeCode must stay Available (DefaultAvailable)")
	}
	if findProvider(t, got, "codex").Available {
		t.Error("codex (OAuth) must not light up from env API keys")
	}
}

// TestAvailabilitySavedAPIKeyCountsForBuiltin: a key typed into settings
// (persisted on the entry itself) keeps the provider available.
func TestAvailabilitySavedAPIKeyCountsForBuiltin(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	raw := `[
      {"name":"OpenAI","display_name":"OpenAI (API Key)","type":"api_key","api_type":"openai","api_key":"sk-saved","available":false,"models":[]}
    ]`
	if err := os.WriteFile(filepath.Join(cm.configDir, "providers.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if !findProvider(t, got, "OpenAI").Available {
		t.Error("OpenAI not Available despite saved api_key on the entry")
	}
}

func TestMarkOAuthProviderAuthenticatedPersistsAvailabilityAndRefreshTime(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	raw := `[
	  {"name":"OpenAI","type":"api_key","api_type":"openai","available":false,"models":[]},
	  {"name":"codex","type":"oauth","api_type":"openai","available":false,"models":[]}
	]`
	if err := os.WriteFile(filepath.Join(cm.configDir, "providers.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := openai.StoreOAuthToken(&openai.OAuthToken{
		AccessToken: "valid-access",
		AccountID:   "account-id",
		ExpiresAt:   4102444800,
	}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if err := cm.MarkOAuthProviderAuthenticated("codex"); err != nil {
		t.Fatal(err)
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	codex := findProvider(t, providers, "codex")
	if !codex.Available {
		t.Error("codex remained unavailable")
	}
	refreshed, err := time.Parse(time.RFC3339, codex.LastRefreshed)
	if err != nil || refreshed.Before(before) {
		t.Errorf("codex last_refreshed = %q, err=%v", codex.LastRefreshed, err)
	}
	openAI := findProvider(t, providers, "OpenAI")
	if openAI.Available || openAI.LastRefreshed != "" {
		t.Errorf("API-key OpenAI was incorrectly marked authenticated: %+v", openAI)
	}
}

func TestMarkOAuthProviderAuthenticatedConcurrentUpdatesDoNotLoseFields(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	raw := `[
	  {"name":"codex","type":"oauth","api_type":"openai","available":false,"models":[]},
	  {"name":"test-oauth","type":"oauth","api_type":"custom","available":false,"models":[]}
	]`
	if err := os.WriteFile(filepath.Join(cm.configDir, "providers.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, providerName := range []string{"codex", "test-oauth"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- cm.MarkOAuthProviderAuthenticated(providerName)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	providers, err := cm.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, providerName := range []string{"codex", "test-oauth"} {
		provider := findProvider(t, providers, providerName)
		if provider.LastRefreshed == "" {
			t.Fatalf("%s lost concurrent last_refreshed update", providerName)
		}
	}
}
