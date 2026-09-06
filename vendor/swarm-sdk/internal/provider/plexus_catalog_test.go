package provider

import "testing"

func TestBuiltinPlexusGatewayDefaults(t *testing.T) {
	plexus, ok := LookupBuiltinProvider("plexus")
	if !ok {
		t.Fatal("plexus missing from builtin provider catalog")
	}
	if plexus.APIType != "openai-compatible" {
		t.Fatalf("APIType = %q, want openai-compatible", plexus.APIType)
	}
	if plexus.AuthType != AuthAPIKey {
		t.Fatalf("AuthType = %q, want %q", plexus.AuthType, AuthAPIKey)
	}
	if plexus.BaseURL != "http://localhost:4000/v1" {
		t.Fatalf("BaseURL = %q", plexus.BaseURL)
	}
	if plexus.HTTPMaxRetries == nil || *plexus.HTTPMaxRetries != 0 {
		t.Fatalf("HTTPMaxRetries = %v, want pointer to zero", plexus.HTTPMaxRetries)
	}
}
