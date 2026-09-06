package cloud

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigManager_LoadConfig_CreatesDefaultWithDeviceID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SWARMOS_CLOUD_REGION", "")
	t.Setenv("SWARMOS_CLOUD_ISSUER_URL", "")
	t.Setenv("SWARMOS_CLOUD_HOSTED_UI_URL", "")
	t.Setenv("SWARMOS_CLOUD_PUBLIC_CLIENT_ID", "")
	t.Setenv("SWARMOS_CLOUD_API_BASE_URL", "")
	t.Setenv("SWARMOS_CLOUD_API_FALLBACK_URL", "")

	cm, err := NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg == nil {
		t.Fatalf("LoadConfig returned nil config")
	}
	if cfg.DeviceID == "" {
		t.Fatalf("expected DeviceID to be set")
	}

	if cfg.Region != defaultRegion {
		t.Fatalf("expected default region %q, got %q", defaultRegion, cfg.Region)
	}
	if cfg.AuthIssuerURL != defaultAuthIssuerURL {
		t.Fatalf("expected default issuer %q, got %q", defaultAuthIssuerURL, cfg.AuthIssuerURL)
	}
	if cfg.HostedUIURL != defaultHostedUIURL {
		t.Fatalf("expected default hosted ui %q, got %q", defaultHostedUIURL, cfg.HostedUIURL)
	}
	if cfg.PublicClientID != defaultPublicClientID {
		t.Fatalf("expected default public client id %q, got %q", defaultPublicClientID, cfg.PublicClientID)
	}
	if cfg.APIBaseURL != defaultAPIBaseURL {
		t.Fatalf("expected default api base url %q, got %q", defaultAPIBaseURL, cfg.APIBaseURL)
	}
	if cfg.APIFallbackURL != defaultAPIFallbackURL {
		t.Fatalf("expected default api fallback url %q, got %q", defaultAPIFallbackURL, cfg.APIFallbackURL)
	}

	if _, statErr := os.Stat(cm.GetConfigPath()); statErr != nil {
		t.Fatalf("expected cloud.json to exist: %v", statErr)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	t.Setenv("SWARMOS_CLOUD_REGION", "test-region")
	t.Setenv("SWARMOS_CLOUD_ISSUER_URL", "https://issuer.example")
	t.Setenv("SWARMOS_CLOUD_HOSTED_UI_URL", "https://hosted.example")
	t.Setenv("SWARMOS_CLOUD_PUBLIC_CLIENT_ID", "client-123")
	t.Setenv("SWARMOS_CLOUD_API_BASE_URL", "https://api.example")
	t.Setenv("SWARMOS_CLOUD_API_FALLBACK_URL", "https://fallback.example")

	cfg := CloudConfig{
		Region:         "orig",
		AuthIssuerURL:  "orig",
		HostedUIURL:    "orig",
		PublicClientID: "orig",
		APIBaseURL:     "orig",
		APIFallbackURL: "orig",
	}
	ApplyEnvOverrides(&cfg)

	if cfg.Region != "test-region" {
		t.Fatalf("Region not overridden: %q", cfg.Region)
	}
	if cfg.AuthIssuerURL != "https://issuer.example" {
		t.Fatalf("AuthIssuerURL not overridden: %q", cfg.AuthIssuerURL)
	}
	if cfg.HostedUIURL != "https://hosted.example" {
		t.Fatalf("HostedUIURL not overridden: %q", cfg.HostedUIURL)
	}
	if cfg.PublicClientID != "client-123" {
		t.Fatalf("PublicClientID not overridden: %q", cfg.PublicClientID)
	}
	if cfg.APIBaseURL != "https://api.example" {
		t.Fatalf("APIBaseURL not overridden: %q", cfg.APIBaseURL)
	}
	if cfg.APIFallbackURL != "https://fallback.example" {
		t.Fatalf("APIFallbackURL not overridden: %q", cfg.APIFallbackURL)
	}
}

func TestConfigManager_LoadConfig_MigratesLegacyHostedUIConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SWARMOS_CLOUD_REGION", "")
	t.Setenv("SWARMOS_CLOUD_ISSUER_URL", "")
	t.Setenv("SWARMOS_CLOUD_HOSTED_UI_URL", "")
	t.Setenv("SWARMOS_CLOUD_PUBLIC_CLIENT_ID", "")
	t.Setenv("SWARMOS_CLOUD_API_BASE_URL", "")
	t.Setenv("SWARMOS_CLOUD_API_FALLBACK_URL", "")

	cm, err := NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	// Seed legacy hosted UI values into cloud.json.
	seed := CloudConfig{
		Region:         "us-east-2",
		AuthIssuerURL:  legacyAuthIssuerURL,
		HostedUIURL:    legacyHostedUIURL,
		PublicClientID: legacyPublicClientID,
		APIBaseURL:     "https://api.example",
		APIFallbackURL: "https://fallback.example",
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := os.WriteFile(cm.GetConfigPath(), raw, 0644); err != nil {
		t.Fatalf("write seed cloud.json: %v", err)
	}

	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.HostedUIURL != defaultHostedUIURL {
		t.Fatalf("expected HostedUIURL migrated to %q, got %q", defaultHostedUIURL, cfg.HostedUIURL)
	}
	if cfg.PublicClientID != defaultPublicClientID {
		t.Fatalf("expected PublicClientID migrated to %q, got %q", defaultPublicClientID, cfg.PublicClientID)
	}
	if cfg.AuthIssuerURL != defaultAuthIssuerURL {
		t.Fatalf("expected AuthIssuerURL migrated to %q, got %q", defaultAuthIssuerURL, cfg.AuthIssuerURL)
	}
	if cfg.DeviceID == "" {
		t.Fatalf("expected DeviceID to be set on migration")
	}

	// Verify the file was updated as well.
	updatedBytes, err := os.ReadFile(cm.GetConfigPath())
	if err != nil {
		t.Fatalf("read updated cloud.json: %v", err)
	}
	var updated CloudConfig
	if err := json.Unmarshal(updatedBytes, &updated); err != nil {
		t.Fatalf("unmarshal updated cloud.json: %v", err)
	}
	if updated.HostedUIURL != defaultHostedUIURL || updated.PublicClientID != defaultPublicClientID || updated.AuthIssuerURL != defaultAuthIssuerURL {
		t.Fatalf("expected cloud.json to be rewritten with migrated hosted UI config")
	}
	if updated.DeviceID == "" {
		t.Fatalf("expected cloud.json to include DeviceID after migration")
	}
}

func TestConfigManager_LoadConfig_FixesMismatchedLegacyPublicClientID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SWARMOS_CLOUD_REGION", "")
	t.Setenv("SWARMOS_CLOUD_ISSUER_URL", "")
	t.Setenv("SWARMOS_CLOUD_HOSTED_UI_URL", "")
	t.Setenv("SWARMOS_CLOUD_PUBLIC_CLIENT_ID", "")
	t.Setenv("SWARMOS_CLOUD_API_BASE_URL", "")
	t.Setenv("SWARMOS_CLOUD_API_FALLBACK_URL", "")

	cm, err := NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	seed := CloudConfig{
		HostedUIURL:    defaultHostedUIURL,
		PublicClientID: legacyPublicClientID,
		AuthIssuerURL:  defaultAuthIssuerURL,
		APIBaseURL:     defaultAPIBaseURL,
		APIFallbackURL: defaultAPIFallbackURL,
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cm.GetConfigPath()), 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(cm.GetConfigPath(), raw, 0644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.PublicClientID != defaultPublicClientID {
		t.Fatalf("expected PublicClientID fixed to %q, got %q", defaultPublicClientID, cfg.PublicClientID)
	}
}
