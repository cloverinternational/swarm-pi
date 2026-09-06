package judge

import (
	"context"
	"testing"
)

// parseLLM is the deterministic, testable core of Layer 2 (the network call is
// exercised by the integration probe, not here). These assert real parsing
// behavior against real strings and would fail if the tolerant parser broke.

func TestParseLLM_CleanJSON(t *testing.T) {
	got := parseLLM(`{"verifies_behavior":3,"would_fail_if_broken":true,"mock_overuse":false,"reasoning":"covers main path"}`)
	if got == nil {
		t.Fatal("expected parse, got nil")
	}
	if got.VerifiesBehavior != 3 || !got.WouldFailIfBroken || got.MockOveruse {
		t.Errorf("unexpected fields: %+v", got)
	}
}

func TestParseLLM_FencedAndChatty(t *testing.T) {
	raw := "Here is my judgement:\n```json\n{\"verifies_behavior\":1,\"would_fail_if_broken\":false,\"mock_overuse\":true,\"reasoning\":\"only tests the mock\"}\n```\nHope that helps!"
	got := parseLLM(raw)
	if got == nil {
		t.Fatal("expected parse from chatty/fenced reply, got nil")
	}
	if got.VerifiesBehavior != 1 || got.WouldFailIfBroken || !got.MockOveruse {
		t.Errorf("unexpected fields: %+v", got)
	}
}

func TestParseLLM_ClampsScore(t *testing.T) {
	got := parseLLM(`{"verifies_behavior":9,"would_fail_if_broken":true}`)
	if got == nil {
		t.Fatal("expected parse, got nil")
	}
	if got.VerifiesBehavior != 3 {
		t.Errorf("expected score clamped to 3, got %d", got.VerifiesBehavior)
	}
}

func TestParseLLM_GarbageReturnsNil(t *testing.T) {
	for _, raw := range []string{"", "I cannot help with that", "not json at all"} {
		if got := parseLLM(raw); got != nil {
			t.Errorf("parseLLM(%q) = %+v, want nil", raw, got)
		}
	}
}

func TestNoopJudge_AlwaysNil(t *testing.T) {
	var j Judge = NoopJudge{}
	if j.Evaluate(context.Background(), AnalyzedTest{TestName: "X"}, "src") != nil {
		t.Error("NoopJudge must return nil")
	}
}

func TestNewSDKJudge_Validation(t *testing.T) {
	if _, err := NewSDKJudge(SDKJudgeConfig{}); err == nil {
		t.Error("expected error for missing factory")
	}
}
