package anthropic

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestBuildOrderedBlocksPreservesBlockIndex verifies that pending blocks keyed
// by block_index are emitted in ascending order, with tool_use blocks linked
// to the matching finalized ToolCall (post-name-normalization).
func TestBuildOrderedBlocksPreservesBlockIndex(t *testing.T) {
	pending := map[int]*pendingBlock{
		0: mkText("hello "),
		1: mkToolUse("call_123", "X"),
		2: mkText("world"),
	}
	toolCalls := []conversation.ToolCall{
		{ID: "call_123", Name: "X", Parameters: map[string]any{"a": 1}},
	}

	got := buildOrderedBlocks(pending, toolCalls)
	if len(got) != 3 {
		t.Fatalf("expected 3 blocks, got %d: %+v", len(got), got)
	}
	if got[0].Type != conversation.BlockTypeContent || got[0].Content != "hello " || got[0].Sequence != 0 {
		t.Errorf("block 0: %+v", got[0])
	}
	if got[1].Type != conversation.BlockTypeToolCall || got[1].ToolCall == nil || got[1].ToolCall.ID != "call_123" || got[1].Sequence != 1 {
		t.Errorf("block 1: %+v", got[1])
	}
	if got[1].ToolCall.Parameters["a"] != 1 {
		t.Errorf("tool_use block must link to finalized ToolCall with parsed params; got %+v", got[1].ToolCall)
	}
	if got[2].Type != conversation.BlockTypeContent || got[2].Content != "world" || got[2].Sequence != 2 {
		t.Errorf("block 2: %+v", got[2])
	}
}

// TestBuildOrderedBlocksInterleavesThinkingAndToolUse verifies extended-thinking
// scenarios where Anthropic emits thinking → tool_use → text interleaved.
func TestBuildOrderedBlocksInterleavesThinkingAndToolUse(t *testing.T) {
	pending := map[int]*pendingBlock{
		0: mkThinking("reasoning"),
		1: mkToolUse("t1", "Search"),
		2: mkText("answer"),
	}
	toolCalls := []conversation.ToolCall{{ID: "t1", Name: "Search"}}

	got := buildOrderedBlocks(pending, toolCalls)
	if len(got) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(got))
	}
	if got[0].Type != conversation.BlockTypeThinking || got[0].Content != "reasoning" {
		t.Errorf("expected thinking block first: %+v", got[0])
	}
	if got[1].Type != conversation.BlockTypeToolCall {
		t.Errorf("expected tool_call second: %+v", got[1])
	}
	if got[2].Type != conversation.BlockTypeContent || got[2].Content != "answer" {
		t.Errorf("expected content third: %+v", got[2])
	}
}

// TestBuildOrderedBlocksSortsSparseIndices ensures block_index values don't
// need to be contiguous — Anthropic reserves the right to emit sparse indices.
func TestBuildOrderedBlocksSortsSparseIndices(t *testing.T) {
	pending := map[int]*pendingBlock{
		5: mkText("second"),
		0: mkText("first"),
	}

	got := buildOrderedBlocks(pending, nil)
	if len(got) != 2 || got[0].Content != "first" || got[1].Content != "second" {
		t.Fatalf("sparse indices not ordered correctly: %+v", got)
	}
	if got[0].Sequence != 0 || got[1].Sequence != 5 {
		t.Errorf("sequence should mirror block_index: %d, %d", got[0].Sequence, got[1].Sequence)
	}
}

// TestBuildOrderedBlocksDropsEmpty skips text/thinking blocks with no content
// (empty content_block_stop without deltas).
func TestBuildOrderedBlocksDropsEmpty(t *testing.T) {
	pending := map[int]*pendingBlock{
		0: {typ: "text"},     // empty textBuf
		1: {typ: "thinking"}, // empty textBuf
		2: mkText("real content"),
	}

	got := buildOrderedBlocks(pending, nil)
	if len(got) != 1 || got[0].Content != "real content" {
		t.Fatalf("empty blocks should be dropped: %+v", got)
	}
}

// TestBuildOrderedBlocksDropsUnfinalizedToolUse skips tool_use blocks that
// never matched a finalized ToolCall (malformed JSON, stream interruption).
func TestBuildOrderedBlocksDropsUnfinalizedToolUse(t *testing.T) {
	pending := map[int]*pendingBlock{
		0: mkToolUse("t1", "X"),
		1: mkText("fallback"),
	}
	// toolCalls is empty — tool_use at index 0 was never finalized.
	got := buildOrderedBlocks(pending, nil)
	if len(got) != 1 || got[0].Content != "fallback" {
		t.Fatalf("unfinalized tool_use should be dropped: %+v", got)
	}
}

// TestBuildOrderedBlocksEmpty returns nil when pending is empty (legacy path).
func TestBuildOrderedBlocksEmpty(t *testing.T) {
	if got := buildOrderedBlocks(nil, nil); got != nil {
		t.Fatalf("empty input must return nil, got %+v", got)
	}
	if got := buildOrderedBlocks(map[int]*pendingBlock{}, nil); got != nil {
		t.Fatalf("empty map must return nil, got %+v", got)
	}
}

// TestProcessSSEStreamEmitsOrderedBlocks drives an end-to-end SSE stream
// through processSSEStream and asserts that the final chunk carries
// OrderedBlocks matching provider-native block_index order.
func TestProcessSSEStreamEmitsOrderedBlocks(t *testing.T) {
	// Minimal Provider — no network calls are issued; processSSEStream only
	// reads from the io.Reader we pass in.
	p, err := New(Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Fake SSE stream with interleaved text → tool_use → text.
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_x","model":"claude","usage":{"input_tokens":10}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi "}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool_abc","name":"MyTool"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"k\":1}"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"done"}}`,
		`data: {"type":"content_block_stop","index":2}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
		``, // trailing newline
	}, "\n")

	chunks := make(chan provider.StreamChunk, 32)
	go func() {
		defer close(chunks)
		if err := p.processSSEStream(context.Background(), strings.NewReader(sse), chunks); err != nil {
			t.Errorf("processSSEStream: %v", err)
		}
	}()

	var finalChunk provider.StreamChunk
	for c := range chunks {
		if c.Done {
			finalChunk = c
		}
	}

	if !finalChunk.Done {
		t.Fatalf("expected a Done chunk")
	}
	if len(finalChunk.OrderedBlocks) != 3 {
		t.Fatalf("expected 3 ordered blocks, got %d: %+v", len(finalChunk.OrderedBlocks), finalChunk.OrderedBlocks)
	}

	ob := finalChunk.OrderedBlocks
	if ob[0].Type != conversation.BlockTypeContent || ob[0].Content != "hi " || ob[0].Sequence != 0 {
		t.Errorf("block 0: %+v", ob[0])
	}
	if ob[1].Type != conversation.BlockTypeToolCall || ob[1].ToolCall == nil || ob[1].ToolCall.ID != "tool_abc" || ob[1].Sequence != 1 {
		t.Errorf("block 1: %+v", ob[1])
	}
	if ob[1].ToolCall.Parameters["k"] != float64(1) {
		t.Errorf("tool params not parsed: %+v", ob[1].ToolCall.Parameters)
	}
	if ob[2].Type != conversation.BlockTypeContent || ob[2].Content != "done" || ob[2].Sequence != 2 {
		t.Errorf("block 2: %+v", ob[2])
	}

	// Sanity: flat fields remain populated for backward compatibility.
	if len(finalChunk.ToolCalls) != 1 {
		t.Errorf("flat ToolCalls should still be populated: %+v", finalChunk.ToolCalls)
	}
}

func mkText(s string) *pendingBlock {
	pb := &pendingBlock{typ: "text"}
	pb.textBuf.WriteString(s)
	return pb
}

func mkThinking(s string) *pendingBlock {
	pb := &pendingBlock{typ: "thinking"}
	pb.textBuf.WriteString(s)
	return pb
}

func mkToolUse(id, name string) *pendingBlock {
	return &pendingBlock{typ: "tool_use", toolID: id, toolName: name}
}
