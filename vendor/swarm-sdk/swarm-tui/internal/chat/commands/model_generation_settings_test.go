package commands

import (
	"encoding/json"
	"testing"
)

func TestModelGenerationSettingsRoundTrip(t *testing.T) {
	zeroFloat := 0.0
	zeroInt := 0
	original := ModelConfig{
		ID:          "explicit-zero",
		Temperature: &zeroFloat,
		MaxTokens:   8192,
		TopP:        &zeroFloat,
		TopK:        &zeroInt,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ModelConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Temperature == nil || *decoded.Temperature != 0 {
		t.Fatalf("temperature explicit zero lost: %#v", decoded.Temperature)
	}
	if decoded.TopP == nil || *decoded.TopP != 0 {
		t.Fatalf("top_p explicit zero lost: %#v", decoded.TopP)
	}
	if decoded.TopK == nil || *decoded.TopK != 0 {
		t.Fatalf("top_k explicit zero lost: %#v", decoded.TopK)
	}
	if decoded.MaxTokens != 8192 {
		t.Fatalf("max_tokens = %d, want 8192", decoded.MaxTokens)
	}

	info := ModelInfoFromConfig(decoded)
	restored := info.ApplyToConfig(ModelConfig{Reasoning: true, ThinkingEffort: "high"})
	if restored.Temperature == nil || *restored.Temperature != 0 ||
		restored.TopP == nil || *restored.TopP != 0 ||
		restored.TopK == nil || *restored.TopK != 0 ||
		restored.MaxTokens != 8192 {
		t.Fatalf("ModelConfig <-> ModelInfo conversion lost overrides: %#v", restored)
	}
	if !restored.Reasoning || restored.ThinkingEffort != "high" {
		t.Fatalf("conversion lost catalog/reasoning metadata: %#v", restored)
	}
}

func TestInheritedModelGenerationSettingsOmitted(t *testing.T) {
	data, err := json.Marshal(ModelConfig{ID: "inherited"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"temperature", "max_tokens", "top_p", "top_k"} {
		var values map[string]json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			t.Fatal(err)
		}
		if _, ok := values[key]; ok {
			t.Errorf("inherited %s should be omitted: %s", key, data)
		}
	}
}
