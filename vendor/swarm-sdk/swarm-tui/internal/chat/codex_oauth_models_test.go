package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/codex"
)

// writeTempCodexProvidersJSON creates a providers.json under a temp HOME with
// an OpenAI ChatGPT-OAuth entry holding the given model IDs plus an unrelated
// API-key entry, and points HOME at it.
func writeTempCodexProvidersJSON(t *testing.T, oauthModelIDs []string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	models := make([]map[string]interface{}, 0, len(oauthModelIDs))
	for _, id := range oauthModelIDs {
		models = append(models, map[string]interface{}{"id": id, "display_name": id})
	}
	providers := []map[string]interface{}{
		{"name": "OpenAI", "type": "oauth", "api_type": "openai", "models": models},
		{"name": "Anthropic", "type": "api_key", "api_type": "anthropic", "models": []map[string]interface{}{
			{"id": "claude-opus-4-8", "display_name": "Claude Opus 4.8"},
		}},
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

func readProvidersFixture(t *testing.T, home string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".swarmos", "providers.json"))
	if err != nil {
		t.Fatalf("read providers.json: %v", err)
	}
	var providers []map[string]interface{}
	if err := json.Unmarshal(data, &providers); err != nil {
		t.Fatalf("parse providers.json: %v", err)
	}
	return providers
}

func i64ptr(v int64) *int64 { return &v }

func catalogFixtureModels() []codex.CatalogModel {
	return []codex.CatalogModel{
		{
			Slug:          "gpt-5.6-sol",
			DisplayName:   "GPT-5.6-Sol",
			Visibility:    "list",
			Priority:      1,
			ContextWindow: i64ptr(372000),
			SupportedReasoningLevels: []codex.ReasoningLevel{
				{Effort: "low"}, {Effort: "medium"}, {Effort: "high"},
				{Effort: "xhigh"}, {Effort: "max"}, {Effort: "ultra"},
			},
			UseResponsesLite: true,
			BaseInstructions: "You are Codex.",
		},
		{
			Slug:          "gpt-5.5",
			DisplayName:   "GPT-5.5",
			Visibility:    "list",
			Priority:      7,
			ContextWindow: i64ptr(272000),
			SupportedReasoningLevels: []codex.ReasoningLevel{
				{Effort: "low"}, {Effort: "medium"}, {Effort: "high"}, {Effort: "xhigh"},
			},
		},
		{
			Slug:        "codex-auto-review",
			DisplayName: "Codex Auto Review",
			Visibility:  "hide",
			Priority:    43,
		},
	}
}

func TestWriteCodexModelsToProvidersJSON(t *testing.T) {
	home := writeTempCodexProvidersJSON(t, []string{"gpt-4o", "gpt-5.1-codex-max"})

	n, err := writeCodexModelsToProvidersJSON(catalogFixtureModels())
	if err != nil {
		t.Fatalf("writeCodexModelsToProvidersJSON: %v", err)
	}
	if n != 2 {
		t.Errorf("wrote %d listed models, want 2", n)
	}

	providers := readProvidersFixture(t, home)
	var openaiModels []interface{}
	for _, p := range providers {
		if name, _ := p["name"].(string); name == "OpenAI" {
			openaiModels, _ = p["models"].([]interface{})
			if src, _ := p["source"].(string); src != "codex-oauth" {
				t.Errorf("source = %q, want codex-oauth", src)
			}
		}
		if name, _ := p["name"].(string); name == "Anthropic" {
			models, _ := p["models"].([]interface{})
			if len(models) != 1 {
				t.Errorf("Anthropic entry must be untouched, got %d models", len(models))
			}
		}
	}

	// 2 listed catalog models first + 2 preserved pre-existing models.
	if len(openaiModels) != 4 {
		t.Fatalf("OpenAI entry has %d models, want 4: %v", len(openaiModels), openaiModels)
	}
	first := openaiModels[0].(map[string]interface{})
	if first["id"] != "gpt-5.6-sol" {
		t.Errorf("first model = %v, want gpt-5.6-sol (priority order)", first["id"])
	}
	if first["context_window"].(float64) != float64(provider.DefaultContextWindowCap) {
		t.Errorf("context_window = %v", first["context_window"])
	}
	efforts, _ := first["reasoning_efforts"].([]interface{})
	// ultra collapses into max → low, medium, high, xhigh, max.
	if len(efforts) != 5 || efforts[len(efforts)-1] != "max" {
		t.Errorf("reasoning_efforts = %v, want 5 entries ending in max", efforts)
	}
	if first["supports_reasoning_effort"] != true {
		t.Errorf("supports_reasoning_effort = %v", first["supports_reasoning_effort"])
	}

	// Hidden model excluded; preserved models appended.
	ids := make([]string, 0, len(openaiModels))
	for _, mv := range openaiModels {
		m := mv.(map[string]interface{})
		ids = append(ids, m["id"].(string))
	}
	for _, id := range ids {
		if id == "codex-auto-review" {
			t.Error("hidden-visibility model must not be written")
		}
	}
	if ids[2] != "gpt-4o" || ids[3] != "gpt-5.1-codex-max" {
		t.Errorf("pre-existing models not preserved in order: %v", ids)
	}
}

func TestWriteCodexModelsNoOAuthEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".swarmos")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "providers.json"), []byte(`[{"name":"Anthropic","type":"api_key","models":[]}]`), 0644)

	if _, err := writeCodexModelsToProvidersJSON(catalogFixtureModels()); err == nil {
		t.Fatal("expected error when no OpenAI-OAuth entry exists")
	}
}

func TestDiffNewCodexModels(t *testing.T) {
	prior := map[string]bool{"gpt-5.5": true}
	added := diffNewCodexModels(prior, catalogFixtureModels())
	// gpt-5.6-sol is new and listed; codex-auto-review is hidden so excluded.
	if len(added) != 1 || added[0] != "gpt-5.6-sol" {
		t.Errorf("added = %v, want [gpt-5.6-sol]", added)
	}
}

func TestWriteCodexCatalogPrompts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeCodexCatalogPrompts(catalogFixtureModels())

	data, err := os.ReadFile(filepath.Join(home, ".swarmos", "codex_prompt_catalog_gpt-5.6-sol.md"))
	if err != nil {
		t.Fatalf("catalog prompt not written: %v", err)
	}
	if string(data) != "You are Codex." {
		t.Errorf("prompt content = %q", string(data))
	}
	// Models without base instructions must not produce files.
	if _, err := os.Stat(filepath.Join(home, ".swarmos", "codex_prompt_catalog_gpt-5.5.md")); !os.IsNotExist(err) {
		t.Error("model without base_instructions must not get a prompt file")
	}
}

// TestConcurrentProvidersJSONWriters verifies the shared mutex prevents the
// historical last-writer-wins race: codex and anthropic refreshers running
// concurrently must both land their models.
func TestConcurrentProvidersJSONWriters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	providers := []map[string]interface{}{
		{"name": "OpenAI", "type": "oauth", "api_type": "openai", "models": []map[string]interface{}{}},
		{"name": "Anthropic", "type": "api_key", "api_type": "anthropic", "models": []map[string]interface{}{}},
	}
	dir := filepath.Join(home, ".swarmos")
	os.MkdirAll(dir, 0700)
	data, _ := json.MarshalIndent(providers, "", "  ")
	os.WriteFile(filepath.Join(dir, "providers.json"), data, 0644)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 25 {
			writeCodexModelsToProvidersJSON(catalogFixtureModels())
		}
	}()
	go func() {
		defer wg.Done()
		for range 25 {
			writeAnthropicModelsToProvidersJSON([]AnthropicModelInfo{
				{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8"},
			})
		}
	}()
	wg.Wait()

	final := readProvidersFixture(t, home)
	for _, p := range final {
		name, _ := p["name"].(string)
		models, _ := p["models"].([]interface{})
		if name == "OpenAI" && len(models) == 0 {
			t.Error("OpenAI models lost to concurrent writer")
		}
		if name == "Anthropic" && len(models) == 0 {
			t.Error("Anthropic models lost to concurrent writer")
		}
	}
}
