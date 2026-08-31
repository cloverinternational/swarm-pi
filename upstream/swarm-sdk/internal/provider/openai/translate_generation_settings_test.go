package openai

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestTranslateRequestTopPPreservesZeroAndOmitsNil(t *testing.T) {
	zero := 0.0
	got, err := TranslateRequest(provider.ChatRequest{Model: "gpt-4o", TopP: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if got.TopP == nil || *got.TopP != 0 {
		t.Fatalf("explicit zero TopP was not preserved: %v", got.TopP)
	}

	got, err = TranslateRequest(provider.ChatRequest{Model: "gpt-4o"})
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
	if _, ok := body["top_p"]; ok {
		t.Error("nil TopP must be omitted")
	}
}
