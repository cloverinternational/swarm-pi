package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAnthropicModelsToProvidersJSONPreservesModelSettings(t *testing.T) {
	home := writeTempProvidersJSON(t, []string{"claude-opus-4-7", "custom-claude"})
	path := filepath.Join(home, ".swarmos", "providers.json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	var providers []map[string]interface{}
	if err := json.Unmarshal(data, &providers); err != nil {
		t.Fatalf("unmarshal seed: %v", err)
	}
	for _, provider := range providers {
		name, _ := provider["name"].(string)
		if !isAnthropicProviderName(name) {
			continue
		}
		models, _ := provider["models"].([]interface{})
		for _, raw := range models {
			model, _ := raw.(map[string]interface{})
			if model["id"] == "claude-opus-4-7" {
				model["thinking_enabled"] = true
				model["thinking_budget"] = float64(8192)
				model["thinking_effort"] = "high"
				model["context_window"] = float64(1_000_000)
				model["custom_user_field"] = "keep-me"
			}
		}
	}
	data, _ = json.MarshalIndent(providers, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("rewrite seed: %v", err)
	}

	_, err = writeAnthropicModelsToProvidersJSON([]AnthropicModelInfo{
		{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8"},
		{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7 refreshed"},
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	data, _ = os.ReadFile(path)
	if err := json.Unmarshal(data, &providers); err != nil {
		t.Fatalf("unmarshal refreshed: %v", err)
	}
	for _, provider := range providers {
		name, _ := provider["name"].(string)
		if !isAnthropicProviderName(name) {
			continue
		}
		models, _ := provider["models"].([]interface{})
		byID := make(map[string]map[string]interface{}, len(models))
		for _, raw := range models {
			model, _ := raw.(map[string]interface{})
			id, _ := model["id"].(string)
			byID[id] = model
		}
		preserved := byID["claude-opus-4-7"]
		if preserved["display_name"] != "Claude Opus 4.7 refreshed" || preserved["thinking_enabled"] != true || preserved["thinking_budget"] != float64(8192) || preserved["thinking_effort"] != "high" || preserved["context_window"] != float64(1_000_000) || preserved["custom_user_field"] != "keep-me" {
			t.Errorf("%s refreshed model lost settings: %+v", name, preserved)
		}
		if _, ok := byID["custom-claude"]; !ok {
			t.Errorf("%s refresh dropped user model", name)
		}
		if _, ok := byID["claude-opus-4-8"]; !ok {
			t.Errorf("%s refresh omitted fetched model", name)
		}
	}
}
