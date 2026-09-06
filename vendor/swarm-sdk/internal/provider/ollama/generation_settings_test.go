package ollama

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestBuildOllamaRequestSamplingControlsPreserveZeroAndOmitNil(t *testing.T) {
	zeroP := 0.0
	zeroK := 0
	p := New(nil)

	got := p.buildOllamaRequest(provider.ChatRequest{TopP: &zeroP, TopK: &zeroK}, false)
	if got.Options == nil || got.Options.TopP == nil || *got.Options.TopP != 0 ||
		got.Options.TopK == nil || *got.Options.TopK != 0 {
		t.Fatalf("explicit zero sampling controls were not preserved: %+v", got.Options)
	}

	got = p.buildOllamaRequest(provider.ChatRequest{}, false)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["options"]; ok {
		t.Error("options must be omitted when all controls are nil")
	}
}
