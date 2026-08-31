package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProviderConfigHTTPMaxRetriesRoundTrip(t *testing.T) {
	zero := 0
	original := ProviderConfig{
		Name:           "plexus",
		DisplayName:    "Plexus Gateway",
		Type:           "api_key",
		APIType:        "openai-compatible",
		BaseURL:        "http://tailnet-plexus:4000/v1",
		HTTPMaxRetries: &zero,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded ProviderConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.HTTPMaxRetries == nil || *decoded.HTTPMaxRetries != 0 {
		t.Fatalf("HTTPMaxRetries = %v, want pointer to zero", decoded.HTTPMaxRetries)
	}
}

func TestLoadProvidersPreservesSelfHostedPlexusEndpoint(t *testing.T) {
	isolateCredentialEnv(t)
	cm := newTestConfigManager(t)
	customURL := "http://tailnet-plexus:4000/v1"
	raw := `[{"name":"plexus","display_name":"Plexus Gateway","type":"api_key","api_type":"openai-compatible","base_url":"` + customURL + `","http_max_retries":0,"available":false,"models":[]}]`
	if err := os.WriteFile(filepath.Join(cm.configDir, "providers.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	providers, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	plexus := findProvider(t, providers, "plexus")
	if plexus.BaseURL != customURL {
		t.Fatalf("BaseURL = %q, want preserved %q", plexus.BaseURL, customURL)
	}
	if plexus.HTTPMaxRetries == nil || *plexus.HTTPMaxRetries != 0 {
		t.Fatalf("HTTPMaxRetries = %v, want pointer to zero", plexus.HTTPMaxRetries)
	}

	persisted, err := os.ReadFile(filepath.Join(cm.configDir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsJSONFieldValue(persisted, "base_url", customURL) {
		t.Fatalf("persisted providers.json lost custom Plexus URL: %s", persisted)
	}
}

func containsJSONFieldValue(data []byte, field, value string) bool {
	var providers []map[string]any
	if err := json.Unmarshal(data, &providers); err != nil {
		return false
	}
	for _, provider := range providers {
		if provider[field] == value {
			return true
		}
	}
	return false
}
