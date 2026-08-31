package profiles

import (
	"testing"
)

func TestLoadBuiltinProfiles_IncludesWafer(t *testing.T) {
	registry, err := LoadBuiltinProfiles()
	if err != nil {
		t.Fatalf("failed to load builtin profiles: %v", err)
	}

	// Verify wafer profile exists
	profile, ok := registry.Get("wafer")
	if !ok {
		t.Error("wafer profile not found in registry")
	}

	// Verify profile fields
	if profile.Name != "wafer" {
		t.Errorf("expected profile name 'wafer', got %q", profile.Name)
	}

	if profile.DisplayName != "Wafer.ai" {
		t.Errorf("expected display name 'Wafer.ai', got %q", profile.DisplayName)
	}

	if profile.BaseURL != "https://pass.wafer.ai/v1" {
		t.Errorf("expected base URL 'https://pass.wafer.ai/v1', got %q", profile.BaseURL)
	}

	if profile.Auth.Type != "bearer" {
		t.Errorf("expected auth type 'bearer', got %q", profile.Auth.Type)
	}

	if !profile.Features.ChatCompletion {
		t.Error("expected ChatCompletion to be true")
	}

	if !profile.Features.Streaming {
		t.Error("expected Streaming to be true")
	}

	if !profile.Features.FunctionCalling {
		t.Error("expected FunctionCalling to be true")
	}
}
