package openai

import (
	"testing"
)

// reasoningOnlyResponse builds a response where the model produced only
// reasoning_content and no content — the shape reasoning models (kimi, GLM,
// o-series via compatible endpoints) return when generation stops before the
// final answer is emitted.
func reasoningOnlyResponse(finishReason string) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Model:   "kimi-k2p6",
		Created: 1781208393,
		Choices: []Choice{
			{
				Index: 0,
				Message: OpenAIMessage{
					Role:             "assistant",
					Content:          "",
					ReasoningContent: "The user wants a commit message. Let me look at the diff...",
				},
				FinishReason: finishReason,
			},
		},
	}
}

// When generation was truncated (finish_reason "length"), the reasoning is an
// unfinished chain-of-thought — substituting it as content hands garbage to
// every consumer (observed: sac committed raw CoT as a commit message).
// Instead the response must carry the SDK's established truncation placeholder
// (translate.go handles finish_reason=length for empty content), with the
// partial reasoning preserved in Thinking/metadata for debugging.
func TestTranslateResponse_TruncatedReasoningNotSubstitutedAsContent(t *testing.T) {
	resp, err := TranslateResponse(reasoningOnlyResponse("length"))
	if err != nil {
		t.Fatalf("TranslateResponse: %v", err)
	}
	msg := resp.Message
	if msg.Content != "[Response truncated due to token limit]" {
		t.Errorf("want explicit truncation placeholder, got: %q", msg.Content)
	}
	if msg.Thinking == "" {
		t.Errorf("reasoning lost: Thinking is empty")
	}
}

// A complete (finish_reason "stop") reasoning-only response keeps the
// long-standing substitution so reasoning-only models don't return empty
// messages.
func TestTranslateResponse_CompleteReasoningOnlyStillSubstituted(t *testing.T) {
	resp, err := TranslateResponse(reasoningOnlyResponse("stop"))
	if err != nil {
		t.Fatalf("TranslateResponse: %v", err)
	}
	msg := resp.Message
	if msg.Content == "" {
		t.Errorf("reasoning-only response with finish_reason=stop should keep content substitution")
	}
}
