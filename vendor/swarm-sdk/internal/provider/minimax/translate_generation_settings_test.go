package minimax

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestTranslateRequestGenerationSettingsPreserveZeroAndOmitNil(t *testing.T) {
	zero := 0.0
	got, _, err := translateRequest(provider.ChatRequest{
		Temperature: &zero,
		TopP:        &zero,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Temperature == nil || *got.Temperature != 0 || got.TopP == nil || *got.TopP != 0 {
		t.Fatalf("explicit zero generation settings were not preserved: temperature=%v top_p=%v", got.Temperature, got.TopP)
	}

	got, _, err = translateRequest(provider.ChatRequest{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["temperature"]; ok {
		t.Error("nil Temperature must be omitted")
	}
	if _, ok := body["top_p"]; ok {
		t.Error("nil TopP must be omitted")
	}
}
