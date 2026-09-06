package chat

import (
	"testing"
)

func TestSDKProfileRegistry_LookupWafer(t *testing.T) {
	// The SDK registry should contain wafer with correct base URL
	result := lookupProviderInSDKRegistry("wafer")
	if !result.Found {
		t.Fatal("Expected wafer to be found in SDK registry")
	}

	if result.BaseURL != "https://pass.wafer.ai/v1" {
		t.Errorf("Expected wafer base URL 'https://pass.wafer.ai/v1', got '%s'", result.BaseURL)
	}

	if result.APIType != "openai-compatible" {
		t.Errorf("Expected wafer apiType 'openai-compatible', got '%s'", result.APIType)
	}
}

func TestSDKProfileRegistry_LookupWaferAlias(t *testing.T) {
	// wafer.ai should resolve to wafer profile
	result := lookupProviderInSDKRegistry("wafer.ai")
	if !result.Found {
		t.Fatal("Expected wafer.ai alias to resolve in SDK registry")
	}

	if result.BaseURL != "https://pass.wafer.ai/v1" {
		t.Errorf("Expected wafer base URL 'https://pass.wafer.ai/v1', got '%s'", result.BaseURL)
	}
}

func TestSDKProfileRegistry_LookupCerebras(t *testing.T) {
	// The SDK registry should contain cerebras
	result := lookupProviderInSDKRegistry("cerebras")
	if !result.Found {
		t.Fatal("Expected cerebras to be found in SDK registry")
	}

	if result.BaseURL != "https://api.cerebras.ai/v1" {
		t.Errorf("Expected cerebras base URL 'https://api.cerebras.ai/v1', got '%s'", result.BaseURL)
	}

	if result.APIType != "openai-compatible" {
		t.Errorf("Expected cerebras apiType 'openai-compatible', got '%s'", result.APIType)
	}
}

func TestSDKProfileRegistry_LookupFireworks(t *testing.T) {
	// The SDK registry should contain fireworks
	result := lookupProviderInSDKRegistry("fireworks")
	if !result.Found {
		t.Fatal("Expected fireworks to be found in SDK registry")
	}

	if result.BaseURL != "https://api.fireworks.ai/inference/v1" {
		t.Errorf("Expected fireworks base URL 'https://api.fireworks.ai/inference/v1', got '%s'", result.BaseURL)
	}
}

func TestSDKProfileRegistry_LookupOpenAI(t *testing.T) {
	// OpenAI should be distinguished from openai-compatible
	result := lookupProviderInSDKRegistry("openai")
	if !result.Found {
		t.Fatal("Expected openai to be found in SDK registry")
	}

	if result.APIType != "openai" {
		t.Errorf("Expected openai apiType 'openai', got '%s'", result.APIType)
	}
}

func TestSDKProfileRegistry_LookupUnknown(t *testing.T) {
	// Unknown provider should not be found
	result := lookupProviderInSDKRegistry("unknown-provider-xyz")
	if result.Found {
		t.Error("Expected unknown provider to not be found in SDK registry")
	}
}

func TestBackfillProviderConfigFromRegistry(t *testing.T) {
	// Test backfilling a legacy config without api_type
	cfg := &ProviderConfig{
		Name: "wafer",
	}

	updated := backfillProviderConfigFromRegistry(cfg, "wafer")
	if !updated {
		t.Fatal("Expected config to be updated by backfill")
	}

	if cfg.APIType != "openai-compatible" {
		t.Errorf("Expected backfilled apiType 'openai-compatible', got '%s'", cfg.APIType)
	}

	if cfg.BaseURL != "https://pass.wafer.ai/v1" {
		t.Errorf("Expected backfilled base URL 'https://pass.wafer.ai/v1', got '%s'", cfg.BaseURL)
	}
}

func TestBackfillProviderConfig_NoUpdateWhenAlreadySet(t *testing.T) {
	// Test that backfill doesn't overwrite existing values
	cfg := &ProviderConfig{
		Name:    "wafer",
		APIType: "anthropic", // Wrong, but explicitly set
		BaseURL: "https://custom.url/v1",
	}

	updated := backfillProviderConfigFromRegistry(cfg, "wafer")
	if updated {
		t.Error("Expected no update when apiType is already set")
	}

	if cfg.APIType != "anthropic" {
		t.Errorf("Expected apiType to remain 'anthropic', got '%s'", cfg.APIType)
	}
}
