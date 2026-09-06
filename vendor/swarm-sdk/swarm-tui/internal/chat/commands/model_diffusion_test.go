package commands

import (
	"encoding/json"
	"testing"
)

// TestModelConfigDiffusionRoundTrip verifies the diffusion flag survives the
// providers.json serialization round-trip and is omitted when false.
func TestModelConfigDiffusionRoundTrip(t *testing.T) {
	cfg := ModelConfig{
		ID:          "nvidia/diffusiongemma-26B-A4B-it-NVFP4",
		DisplayName: "DiffusionGemma 26B",
		Diffusion:   true,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back ModelConfig
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Diffusion {
		t.Error("diffusion flag lost in round-trip")
	}

	// omitempty: a non-diffusion model must not carry the key.
	plain, err := json.Marshal(ModelConfig{ID: "gpt-x"})
	if err != nil {
		t.Fatalf("marshal plain: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(plain, &asMap); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}
	if _, ok := asMap["diffusion"]; ok {
		t.Error("diffusion=false should be omitted from JSON")
	}
}
