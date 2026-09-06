package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeTempProvidersJSON creates a providers.json under a temp HOME containing an
// Anthropic provider entry with the given model IDs, and points HOME at it.
func writeTempProvidersJSON(t *testing.T, anthropicModelIDs []string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	models := make([]map[string]interface{}, 0, len(anthropicModelIDs))
	for _, id := range anthropicModelIDs {
		models = append(models, map[string]interface{}{"id": id, "display_name": id})
	}
	providers := []map[string]interface{}{
		{"name": "OpenAI", "type": "api_key", "models": []map[string]interface{}{}},
		{"name": "Anthropic", "type": "api_key", "models": models},
		{"name": "ClaudeCode", "type": "oauth", "models": models},
	}
	dir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, _ := json.MarshalIndent(providers, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), data, 0644); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}
	return home
}

func TestDiffNewAnthropicModels(t *testing.T) {
	prior := map[string]bool{
		"claude-opus-4-7":   true,
		"claude-sonnet-4-6": true,
	}
	fetched := []AnthropicModelInfo{
		{ID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5"},
		{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8"},
		{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7"},     // already known
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"}, // already known
	}

	added := diffNewAnthropicModels(prior, fetched)

	// Expect only the two genuinely new IDs, sorted.
	want := []string{"claude-opus-4-8", "claude-sonnet-5"}
	if len(added) != len(want) {
		t.Fatalf("expected %d new models, got %d: %v", len(want), len(added), added)
	}
	for i := range want {
		if added[i] != want[i] {
			t.Errorf("added[%d] = %q, want %q (full: %v)", i, added[i], want[i], added)
		}
	}
}

func TestReadAnthropicModelIDsFromProvidersJSON(t *testing.T) {
	writeTempProvidersJSON(t, []string{"claude-opus-4-7", "claude-haiku-4-5-20251001"})

	ids := readAnthropicModelIDsFromProvidersJSON()
	if !ids["claude-opus-4-7"] || !ids["claude-haiku-4-5-20251001"] {
		t.Fatalf("expected both stored IDs to be present, got: %v", ids)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 IDs, got %d: %v", len(ids), ids)
	}
}

func TestWriteAnthropicModelsToProvidersJSON(t *testing.T) {
	home := writeTempProvidersJSON(t, []string{"claude-opus-4-7"})

	models := []AnthropicModelInfo{
		{ID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5"},
		{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8"},
		{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7"},
	}
	n, err := writeAnthropicModelsToProvidersJSON(models)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 models written, got %d", n)
	}

	// Re-read and confirm the Anthropic entry now has the fetched models and
	// bookkeeping fields.
	data, err := os.ReadFile(filepath.Join(home, ".swarmos", "providers.json"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var providers []map[string]interface{}
	if err := json.Unmarshal(data, &providers); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, p := range providers {
		if name, _ := p["name"].(string); name != "Anthropic" {
			continue
		}
		found = true
		if p["source"] != "anthropic-oauth" {
			t.Errorf("source = %v, want anthropic-oauth", p["source"])
		}
		if _, ok := p["last_refreshed"].(string); !ok {
			t.Errorf("last_refreshed missing or not a string: %v", p["last_refreshed"])
		}
		ms, _ := p["models"].([]interface{})
		if len(ms) != 3 {
			t.Errorf("expected 3 models in Anthropic entry, got %d", len(ms))
		}
	}
	if !found {
		t.Fatal("Anthropic provider entry not found after write")
	}

	// The new-model diff computed against the pre-write snapshot should surface
	// exactly the two IDs that weren't there before.
	prior := map[string]bool{"claude-opus-4-7": true}
	added := diffNewAnthropicModels(prior, models)
	if len(added) != 2 {
		t.Errorf("expected 2 new models, got %d: %v", len(added), added)
	}
}
