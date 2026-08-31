package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

func TestCreateProvider_UsesProvidersJSONAPIKey(t *testing.T) {
	// Isolate the SwarmOS root so createProvider reads only this test's
	// providers.json (canonical: ~/.swarm/config/providers.json).
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
			"models": []map[string]any{
				{"id": "grok-4.20-multi-agent-0309"},
			},
		},
	}
	data, err := json.Marshal(providers)
	if err != nil {
		t.Fatalf("marshal providers.json: %v", err)
	}
	if err := os.WriteFile(paths.ProvidersFile(), data, 0o600); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}

	prov, err := createProvider(&Config{
		Provider: "xai",
		Model:    "grok-4.20-multi-agent-0309",
	}, noop.NewLogger(), noop.NewTracer())
	if err != nil {
		t.Fatalf("createProvider() error = %v", err)
	}
	if prov == nil {
		t.Fatal("createProvider() returned nil provider")
	}
	if got := prov.Name(); got != "xai" {
		t.Fatalf("createProvider() provider name = %q, want %q", got, "xai")
	}
}
