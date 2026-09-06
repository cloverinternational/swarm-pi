package gemini

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestTranslateRequestSamplingControlsPreserveZeroAndOmitNil(t *testing.T) {
	zeroP := 0.0
	zeroK := 0

	got := TranslateRequest(provider.ChatRequest{TopP: &zeroP, TopK: &zeroK}, "project", "session")
	config := got.Request.GenerationConfig
	if config.TopP == nil || *config.TopP != 0 || config.TopK == nil || *config.TopK != 0 {
		t.Fatalf("explicit zero sampling controls were not preserved: topP=%v topK=%v", config.TopP, config.TopK)
	}

	got = TranslateRequest(provider.ChatRequest{}, "project", "session")
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	request := body["request"].(map[string]any)
	generationConfig := request["generationConfig"].(map[string]any)
	if _, ok := generationConfig["topP"]; ok {
		t.Error("nil TopP must be omitted")
	}
	if _, ok := generationConfig["topK"]; ok {
		t.Error("nil TopK must be omitted")
	}
}
