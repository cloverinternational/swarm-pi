package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRouterRefreshPreservesGenerationOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	temperature := 0.3
	topP := 0.8
	topK := 40
	initial := []map[string]any{{
		"name": "OpenRouter",
		"models": []map[string]any{{
			"id":               "qwen/qwen3",
			"display_name":     "Old name",
			"temperature":      temperature,
			"max_tokens":       65536,
			"top_p":            topP,
			"top_k":            topK,
			"thinking_enabled": true,
			"thinking_budget":  8192,
		}},
	}}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "providers.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = writeOpenRouterModelsToProvidersJSON([]OpenRouterModelInfo{{
		ID:            "qwen/qwen3",
		Name:          "Qwen 3 refreshed",
		ContextLength: 131072,
		Description:   "Fresh catalog metadata",
	}}, "test")
	if err != nil {
		t.Fatal(err)
	}

	updatedData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var updated []map[string]any
	if err := json.Unmarshal(updatedData, &updated); err != nil {
		t.Fatal(err)
	}
	model := updated[0]["models"].([]any)[0].(map[string]any)
	for key, want := range map[string]float64{
		"temperature": temperature,
		"max_tokens":  65536,
		"top_p":       topP,
		"top_k":       float64(topK),
	} {
		if got, ok := model[key].(float64); !ok || got != want {
			t.Fatalf("%s = %#v, want %v", key, model[key], want)
		}
	}
	if got := model["display_name"]; got != "Qwen 3 refreshed" {
		t.Fatalf("display_name = %#v", got)
	}
	if got := model["context_window"]; got != float64(131072) {
		t.Fatalf("context_window = %#v", got)
	}
}
