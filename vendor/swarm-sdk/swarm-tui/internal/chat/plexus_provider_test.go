package chat

import "testing"

func TestResolveAPITypeFromNamePlexus(t *testing.T) {
	got := resolveAPITypeFromName("Plexus")
	if got.APIType != "openai-compatible" {
		t.Fatalf("APIType = %q, want openai-compatible", got.APIType)
	}
	if got.DefaultBaseURL != "http://localhost:4000/v1" {
		t.Fatalf("DefaultBaseURL = %q", got.DefaultBaseURL)
	}
}
