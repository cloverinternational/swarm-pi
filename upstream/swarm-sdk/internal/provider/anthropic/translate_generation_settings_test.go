package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestTranslateRequestSamplingControlsPreserveZeroAndOmitNil(t *testing.T) {
	zeroP := 0.0
	zeroK := 0

	got, err := TranslateRequest(provider.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []*conversation.Message{{Role: conversation.RoleUser, Content: "hello"}},
		TopP:     &zeroP,
		TopK:     &zeroK,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.TopP == nil || *got.TopP != 0 || got.TopK == nil || *got.TopK != 0 {
		t.Fatalf("explicit zero sampling controls were not preserved: top_p=%v top_k=%v", got.TopP, got.TopK)
	}

	got, err = TranslateRequest(provider.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []*conversation.Message{{Role: conversation.RoleUser, Content: "hello"}},
	})
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
	if _, ok := body["top_k"]; ok {
		t.Error("nil TopK must be omitted")
	}
}
