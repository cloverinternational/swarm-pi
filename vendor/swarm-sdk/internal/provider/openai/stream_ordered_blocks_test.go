package openai

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestBuildOpenAIOrderedBlocksEmpty(t *testing.T) {
	if got := buildOpenAIOrderedBlocks("", "", nil); got != nil {
		t.Fatalf("empty input must return nil, got %+v", got)
	}
}

func TestBuildOpenAIOrderedBlocksContentOnly(t *testing.T) {
	got := buildOpenAIOrderedBlocks("hello", "", nil)
	if len(got) != 1 || got[0].Type != conversation.BlockTypeContent || got[0].Content != "hello" {
		t.Fatalf("content-only: %+v", got)
	}
}

func TestBuildOpenAIOrderedBlocksReasoningThenContent(t *testing.T) {
	got := buildOpenAIOrderedBlocks("answer", "reasoning", nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(got))
	}
	if got[0].Type != conversation.BlockTypeThinking || got[0].Content != "reasoning" || got[0].Sequence != 0 {
		t.Errorf("block 0 must be thinking first: %+v", got[0])
	}
	if got[1].Type != conversation.BlockTypeContent || got[1].Content != "answer" || got[1].Sequence != 1 {
		t.Errorf("block 1 must be content second: %+v", got[1])
	}
}

func TestBuildOpenAIOrderedBlocksWithToolCalls(t *testing.T) {
	toolCalls := []conversation.ToolCall{
		{ID: "call_0", Name: "A"},
		{ID: "call_1", Name: "B"},
	}
	got := buildOpenAIOrderedBlocks("preamble", "", toolCalls)
	if len(got) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(got))
	}
	if got[0].Type != conversation.BlockTypeContent || got[0].Content != "preamble" {
		t.Errorf("block 0 content: %+v", got[0])
	}
	if got[1].Type != conversation.BlockTypeToolCall || got[1].ToolCall.ID != "call_0" || got[1].Sequence != 1 {
		t.Errorf("block 1: %+v", got[1])
	}
	if got[2].Type != conversation.BlockTypeToolCall || got[2].ToolCall.ID != "call_1" || got[2].Sequence != 2 {
		t.Errorf("block 2: %+v", got[2])
	}
}

// TestBuildOpenAIOrderedBlocksDoesNotAliasToolCalls verifies each emitted
// MessageBlock.ToolCall is a fresh copy, so later mutation of the source
// slice can't change the recorded blocks.
func TestBuildOpenAIOrderedBlocksDoesNotAliasToolCalls(t *testing.T) {
	toolCalls := []conversation.ToolCall{{ID: "call_0", Name: "original"}}
	got := buildOpenAIOrderedBlocks("", "", toolCalls)
	toolCalls[0].Name = "mutated"
	if got[0].ToolCall.Name != "original" {
		t.Fatalf("ToolCall was aliased, not copied: got %q", got[0].ToolCall.Name)
	}
}
