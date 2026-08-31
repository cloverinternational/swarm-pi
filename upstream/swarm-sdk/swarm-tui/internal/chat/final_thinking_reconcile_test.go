package chat

import (
	"errors"
	"testing"
)

func TestReconcileAssistantThinkingFromComplete(t *testing.T) {
	messages := []Message{{Role: "assistant"}}
	if !reconcileAssistantThinkingFromComplete(messages, "reason carefully", nil) {
		t.Fatal("expected terminal thinking to repair empty assistant placeholder")
	}
	if messages[0].Thinking != "reason carefully" {
		t.Fatalf("Thinking = %q", messages[0].Thinking)
	}
	if len(messages[0].OrderedBlocks) != 1 || messages[0].OrderedBlocks[0].Type != "thinking" || messages[0].OrderedBlocks[0].Content != "reason carefully" {
		t.Fatalf("OrderedBlocks = %+v", messages[0].OrderedBlocks)
	}
	if reconcileAssistantThinkingFromComplete(messages, "reason carefully", nil) {
		t.Fatal("identical terminal thinking must be idempotent")
	}
}

func TestReconcileAssistantThinkingFromCompleteRepairsPartialBlock(t *testing.T) {
	messages := []Message{{
		Role:     "assistant",
		Thinking: "reason ",
		OrderedBlocks: []MessageBlock{{
			Type:     "thinking",
			Content:  "reason ",
			Sequence: 3,
		}},
	}}
	if !reconcileAssistantThinkingFromComplete(messages, "reason carefully", nil) {
		t.Fatal("expected terminal thinking to repair partial stream")
	}
	if messages[0].Thinking != "reason carefully" || messages[0].OrderedBlocks[0].Content != "reason carefully" || messages[0].OrderedBlocks[0].Sequence != 3 {
		t.Fatalf("repaired message = %+v", messages[0])
	}
}

func TestReconcileAssistantThinkingFromCompletePreservesPriorTurn(t *testing.T) {
	messages := []Message{{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{Type: "thinking", Content: "first turn", Sequence: 1},
			{Type: "tool_call", Sequence: 2},
			{Type: "tool_result", Sequence: 3},
		},
	}}
	if !reconcileAssistantThinkingFromComplete(messages, "second turn", nil) {
		t.Fatal("expected a new terminal thinking block")
	}
	blocks := messages[0].OrderedBlocks
	if len(blocks) != 4 || blocks[0].Content != "first turn" || blocks[3].Type != "thinking" || blocks[3].Content != "second turn" || blocks[3].Sequence != 4 {
		t.Fatalf("turn-separated blocks = %+v", blocks)
	}
}

func TestReconcileAssistantThinkingFromCompleteSkipsInvalidInputs(t *testing.T) {
	if reconcileAssistantThinkingFromComplete([]Message{{Role: "assistant"}}, "thinking", errors.New("boom")) {
		t.Fatal("must skip on error")
	}
	if reconcileAssistantThinkingFromComplete([]Message{{Role: "assistant"}}, "   ", nil) {
		t.Fatal("must skip empty thinking")
	}
	if reconcileAssistantThinkingFromComplete([]Message{{Role: "user"}}, "thinking", nil) {
		t.Fatal("must skip non-assistant message")
	}
}
