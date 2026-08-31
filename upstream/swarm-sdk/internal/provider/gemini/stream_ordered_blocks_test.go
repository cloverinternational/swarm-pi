package gemini

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestAppendGeminiOrderedBlocksInterleavesTextAndFunctionCall verifies that
// when a single SSE event's candidate.content.parts contains text followed
// by a function_call followed by more text, the accumulator records blocks
// in exactly that order — the bug this whole stack of PRs exists to fix.
func TestAppendGeminiOrderedBlocksInterleavesTextAndFunctionCall(t *testing.T) {
	resp := &GeminiResponse{
		Response: &VertexResponse{
			Candidates: []*Candidate{{
				Content: &Content{
					Parts: []*Part{
						{Text: "before "},
						{FunctionCall: &FunctionCall{Name: "Search", Args: map[string]any{"q": "x"}}},
						{Text: "after"},
					},
				},
			}},
		},
	}
	toolCalls := []conversation.ToolCall{
		{ID: "toolu_abc", Name: "Search", Parameters: map[string]any{"q": "x"}},
	}
	var acc []conversation.MessageBlock
	appendGeminiOrderedBlocks(&acc, resp, toolCalls)

	if len(acc) != 3 {
		t.Fatalf("expected 3 blocks, got %d: %+v", len(acc), acc)
	}
	if acc[0].Type != conversation.BlockTypeContent || acc[0].Content != "before " || acc[0].Sequence != 0 {
		t.Errorf("block 0: %+v", acc[0])
	}
	if acc[1].Type != conversation.BlockTypeToolCall || acc[1].ToolCall == nil || acc[1].ToolCall.ID != "toolu_abc" || acc[1].Sequence != 1 {
		t.Errorf("block 1: %+v", acc[1])
	}
	if acc[2].Type != conversation.BlockTypeContent || acc[2].Content != "after" || acc[2].Sequence != 2 {
		t.Errorf("block 2: %+v", acc[2])
	}
}

// TestAppendGeminiOrderedBlocksMergesConsecutiveText ensures streaming text
// deltas arriving as separate parts collapse into one content block rather
// than spawning many fragmented blocks. Tool_call boundaries must still
// start a new content run after them.
func TestAppendGeminiOrderedBlocksMergesConsecutiveText(t *testing.T) {
	// First event: two text parts in a row.
	resp1 := &GeminiResponse{
		Response: &VertexResponse{
			Candidates: []*Candidate{{
				Content: &Content{
					Parts: []*Part{
						{Text: "hel"},
						{Text: "lo "},
					},
				},
			}},
		},
	}
	// Second event: one more text part — should merge with previous.
	resp2 := &GeminiResponse{
		Response: &VertexResponse{
			Candidates: []*Candidate{{
				Content: &Content{Parts: []*Part{{Text: "world"}}},
			}},
		},
	}
	var acc []conversation.MessageBlock
	appendGeminiOrderedBlocks(&acc, resp1, nil)
	appendGeminiOrderedBlocks(&acc, resp2, nil)

	if len(acc) != 1 {
		t.Fatalf("expected 1 merged content block, got %d: %+v", len(acc), acc)
	}
	if acc[0].Type != conversation.BlockTypeContent || acc[0].Content != "hello world" {
		t.Errorf("merged block: %+v", acc[0])
	}
}

// TestAppendGeminiOrderedBlocksSeparatesThinkingAndContent ensures thought=true
// parts land in thinking blocks and don't merge with plain-text content
// blocks that appear adjacent to them.
func TestAppendGeminiOrderedBlocksSeparatesThinkingAndContent(t *testing.T) {
	resp := &GeminiResponse{
		Response: &VertexResponse{
			Candidates: []*Candidate{{
				Content: &Content{
					Parts: []*Part{
						{Text: "reasoning", Thought: true},
						{Text: "answer"},
					},
				},
			}},
		},
	}
	var acc []conversation.MessageBlock
	appendGeminiOrderedBlocks(&acc, resp, nil)

	if len(acc) != 2 {
		t.Fatalf("expected 2 blocks (thinking + content), got %d: %+v", len(acc), acc)
	}
	if acc[0].Type != conversation.BlockTypeThinking || acc[0].Content != "reasoning" {
		t.Errorf("block 0 should be thinking: %+v", acc[0])
	}
	if acc[1].Type != conversation.BlockTypeContent || acc[1].Content != "answer" {
		t.Errorf("block 1 should be content: %+v", acc[1])
	}
}

// TestAppendGeminiOrderedBlocksStartsNewContentAfterToolCall verifies that a
// text part arriving after a tool_call block does NOT merge into the
// pre-tool-call content block — the tool_call must remain between the two
// content runs, matching stream order.
func TestAppendGeminiOrderedBlocksStartsNewContentAfterToolCall(t *testing.T) {
	resp := &GeminiResponse{
		Response: &VertexResponse{
			Candidates: []*Candidate{{
				Content: &Content{
					Parts: []*Part{
						{Text: "pre"},
						{FunctionCall: &FunctionCall{Name: "Tool"}},
						{Text: "post"},
					},
				},
			}},
		},
	}
	tools := []conversation.ToolCall{{ID: "t1", Name: "Tool"}}
	var acc []conversation.MessageBlock
	appendGeminiOrderedBlocks(&acc, resp, tools)

	if len(acc) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(acc))
	}
	if acc[0].Content != "pre" || acc[2].Content != "post" {
		t.Errorf("text blocks must not merge across tool_call: %+v", acc)
	}
}

// TestAppendGeminiOrderedBlocksSkipsUnmatchedFunctionCall ensures we don't
// panic when eventToolCalls is shorter than the count of function_call
// parts (defensive against translation drift).
func TestAppendGeminiOrderedBlocksSkipsUnmatchedFunctionCall(t *testing.T) {
	resp := &GeminiResponse{
		Response: &VertexResponse{
			Candidates: []*Candidate{{
				Content: &Content{
					Parts: []*Part{
						{FunctionCall: &FunctionCall{Name: "A"}},
						{FunctionCall: &FunctionCall{Name: "B"}},
						{Text: "fallback"},
					},
				},
			}},
		},
	}
	// Only one tool call provided — second function_call part has no match.
	var acc []conversation.MessageBlock
	appendGeminiOrderedBlocks(&acc, resp, []conversation.ToolCall{{ID: "t1", Name: "A"}})

	// Expect: tool_call(A), content(fallback). The unmatched B is skipped.
	if len(acc) != 2 {
		t.Fatalf("expected 2 blocks (matched tool + fallback text), got %d: %+v", len(acc), acc)
	}
	if acc[0].Type != conversation.BlockTypeToolCall || acc[0].ToolCall.ID != "t1" {
		t.Errorf("block 0: %+v", acc[0])
	}
	if acc[1].Type != conversation.BlockTypeContent || acc[1].Content != "fallback" {
		t.Errorf("block 1: %+v", acc[1])
	}
}

// TestAppendGeminiOrderedBlocksEmptyResponse covers the nil/empty-guard paths
// so we don't accidentally crash when a malformed or usage-only event arrives.
func TestAppendGeminiOrderedBlocksEmptyResponse(t *testing.T) {
	var acc []conversation.MessageBlock
	appendGeminiOrderedBlocks(&acc, nil, nil)
	appendGeminiOrderedBlocks(&acc, &GeminiResponse{}, nil)
	appendGeminiOrderedBlocks(&acc, &GeminiResponse{Response: &VertexResponse{}}, nil)
	appendGeminiOrderedBlocks(&acc, &GeminiResponse{Response: &VertexResponse{Candidates: []*Candidate{{}}}}, nil)
	if len(acc) != 0 {
		t.Fatalf("empty/nil responses must not append: %+v", acc)
	}
}

// TestAppendGeminiOrderedBlocksDoesNotAliasToolCall verifies each emitted
// MessageBlock.ToolCall is a fresh copy so later mutation of the source
// slice can't change the recorded blocks.
func TestAppendGeminiOrderedBlocksDoesNotAliasToolCall(t *testing.T) {
	resp := &GeminiResponse{
		Response: &VertexResponse{
			Candidates: []*Candidate{{
				Content: &Content{Parts: []*Part{{FunctionCall: &FunctionCall{Name: "X"}}}},
			}},
		},
	}
	tools := []conversation.ToolCall{{ID: "t1", Name: "original"}}
	var acc []conversation.MessageBlock
	appendGeminiOrderedBlocks(&acc, resp, tools)
	tools[0].Name = "mutated"

	if acc[0].ToolCall.Name != "original" {
		t.Fatalf("ToolCall was aliased, not copied: got %q", acc[0].ToolCall.Name)
	}
}
