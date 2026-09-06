package settings

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func TestPlexusConnectPersistsUpdatedKeyAndActivatesAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const secret = "sk-plexus-test-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"swarm-fast"},{"id":"swarm-main"}]}`))
	}))
	defer srv.Close()

	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err := cm.SaveProviders([]commands.ProviderConfig{{
		Name: "plexus", DisplayName: "Plexus Gateway", Type: "api_key",
		APIType: "openai-compatible", BaseURL: srv.URL + "/v1", APIKey: "old-plexus-key",
		HTTPMaxRetries: &zero, Models: []commands.ModelConfig{},
	}}); err != nil {
		t.Fatal(err)
	}

	m := NewModelSettings("openai", "unused")
	m.SetConfigManager(cm)
	m.providers = loadProviders()
	for i := range m.providers {
		if strings.EqualFold(m.providers[i].Name, "plexus") {
			m.selectedProvider = i
			break
		}
	}
	m.formAPIEndpoint = srv.URL + "/v1"
	m.formAPIKey = secret

	cmd := m.beginPlexusSync()
	if cmd == nil {
		t.Fatal("Plexus sync command was not created")
	}
	if m.plexusConnected {
		t.Fatal("Plexus connected before async command completed")
	}
	msg, ok := cmd().(plexusSyncResultMsg)
	if !ok {
		t.Fatal("unexpected Plexus sync message")
	}
	m.applyPlexusSyncResult(msg)
	if !m.plexusConnected {
		t.Fatalf("Plexus did not connect: %s", m.plexusStatusText)
	}
	if len(m.providers[m.selectedProvider].Models) != 2 {
		t.Fatalf("authorized aliases = %d, want 2", len(m.providers[m.selectedProvider].Models))
	}

	providers, err := cm.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	var plexus commands.ProviderConfig
	for _, provider := range providers {
		if strings.EqualFold(provider.Name, "plexus") {
			plexus = provider
			break
		}
	}
	if plexus.APIKey != secret {
		t.Fatal("updated Plexus key was not persisted in provider config")
	}
	if plexus.APIKeySecretRef != "" {
		t.Fatalf("unexpected secret ref = %q", plexus.APIKeySecretRef)
	}
	data, err := os.ReadFile(cm.GetConfigPath("providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), secret) {
		t.Fatal("providers.json does not contain the updated Plexus key")
	}
	info, err := os.Stat(cm.GetConfigPath("providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("providers.json mode = %o, want 600", got)
	}

	var selectedProvider, selectedModel string
	m.SetOnModelSelect(func(provider, model string) {
		selectedProvider, selectedModel = provider, model
	})
	m.plexusDefaultAlias = "swarm-main"
	m.usePlexusNow()
	if selectedProvider != "plexus" || selectedModel != "swarm-main" {
		t.Fatalf("selected %s/%s, want plexus/swarm-main", selectedProvider, selectedModel)
	}
}

func TestPlexusIgnoresStaleAsyncCredentialResult(t *testing.T) {
	m := NewModelSettings("openai", "unused")
	m.plexusRequestGeneration = 1
	m.plexusField = 1
	if !m.handlePlexusProviderKey("x") {
		t.Fatal("direct key input was not handled")
	}
	if m.plexusRequestGeneration != 2 {
		t.Fatal("direct key input did not invalidate the in-flight request")
	}
	storeCalls := 0
	m.SetVaultCredentialCallbacks(
		func(string, string) (string, error) {
			storeCalls++
			return "provider-plexus-api-key", nil
		},
		nil,
		nil,
	)
	m.applyPlexusSyncResult(plexusSyncResultMsg{
		generation: 1,
		persistKey: true,
		key:        "stale-secret",
		models:     []commands.ModelConfig{{ID: "swarm-main"}},
	})
	if storeCalls != 0 {
		t.Fatal("stale async result mutated the Vault")
	}
}

func TestPlexusDisconnectRestoresConfigWhenVaultDeleteFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	original := commands.ProviderConfig{
		Name: "plexus", DisplayName: "Plexus Gateway", Type: "api_key",
		APIType: "openai-compatible", BaseURL: "http://plexus.test/v1",
		HTTPMaxRetries: &zero, APIKeySecretRef: "provider-plexus-api-key",
		Available: true, Models: []commands.ModelConfig{{ID: "swarm-main"}},
	}
	if err := cm.SaveProviders([]commands.ProviderConfig{original}); err != nil {
		t.Fatal(err)
	}
	m := NewModelSettings("plexus", "swarm-main")
	m.SetConfigManager(cm)
	m.providers = loadProviders()
	for i := range m.providers {
		if strings.EqualFold(m.providers[i].Name, "plexus") {
			m.selectedProvider = i
			break
		}
	}
	m.formAPIEndpoint = original.BaseURL
	m.SetVaultCredentialCallbacks(
		nil,
		func(string) (string, error) { return "secret", nil },
		func(string) error { return os.ErrPermission },
	)
	m.disconnectPlexus()

	providers, err := cm.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range providers {
		if strings.EqualFold(provider.Name, "plexus") {
			if provider.APIKeySecretRef != original.APIKeySecretRef || !provider.Available || len(provider.Models) != 1 {
				t.Fatalf("Plexus config was not restored: %+v", provider)
			}
			return
		}
	}
	t.Fatal("Plexus provider disappeared during rollback")
}

func TestNewPlexusProviderDoesNotPersistInvalidKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.SaveProviders([]commands.ProviderConfig{}); err != nil {
		t.Fatal(err)
	}
	m := NewModelSettings("openai", "unused")
	m.SetConfigManager(cm)
	m.providers = []commands.Provider{}
	m.formProviderType = "Plexus Gateway"
	m.formDisplayName = "Plexus Gateway"
	m.formAuthType = "api_key"
	m.formAPIEndpoint = srv.URL + "/v1"
	m.formAPIKey = "invalid-plexus-key"

	m.saveNewProvider()

	providers, err := cm.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range providers {
		if strings.EqualFold(provider.Name, plexusProviderName) &&
			(provider.APIKey == "invalid-plexus-key" || provider.BaseURL == srv.URL+"/v1") {
			t.Fatalf("invalid Plexus credentials were persisted")
		}
	}
}

func TestPlexusFieldsStayDisabledAfterInvalidKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cm, _ := commands.NewConfigManager()
	zero := 0
	_ = cm.SaveProviders([]commands.ProviderConfig{{
		Name: "plexus", DisplayName: "Plexus Gateway", Type: "api_key",
		APIType: "openai-compatible", BaseURL: srv.URL + "/v1", HTTPMaxRetries: &zero,
	}})
	m := NewModelSettings("openai", "unused")
	m.SetConfigManager(cm)
	m.providers = loadProviders()
	for i := range m.providers {
		if strings.EqualFold(m.providers[i].Name, "plexus") {
			m.selectedProvider = i
			break
		}
	}
	m.formAPIEndpoint = srv.URL + "/v1"
	m.formAPIKey = "bad-key"
	m.SetVaultCredentialCallbacks(
		func(string, string) (string, error) { return "unused", nil },
		nil,
		nil,
	)
	cmd := m.beginPlexusSync()
	if cmd == nil {
		t.Fatal("Plexus sync command was not created")
	}
	msg := cmd().(plexusSyncResultMsg)
	m.applyPlexusSyncResult(msg)
	if m.plexusConnected {
		t.Fatal("invalid key enabled Plexus fields")
	}
	if !m.plexusStatusErr {
		t.Fatal("invalid key did not produce an error state")
	}
}
