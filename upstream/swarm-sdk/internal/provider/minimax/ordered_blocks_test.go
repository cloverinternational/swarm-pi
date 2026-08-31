package minimax

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestStream_EmitsOrderedBlocksForThinkingThenContent drives a Minimax SSE
// stream with a thinking delta followed by content deltas and asserts the
// final chunk carries OrderedBlocks [thinking, content] with the correct
// accumulated text and monotonic sequence numbers.
func TestStream_EmitsOrderedBlocksForThinkingThenContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer does not support flushing")
		}
		events := []string{
			`event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"reasoning"}}

`,
			`event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello "}}

`,
			`event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"world"}}

`,
			`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

`,
			`event: message_stop
data: {"type":"message_stop"}

`,
		}
		for _, e := range events {
			fmt.Fprint(w, e)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	prov, err := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	}, noop.NewLogger(), noop.NewTracer())
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model: "MiniMax-M2.5",
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("stream setup failed: %v", err)
	}

	var finalChunk provider.StreamChunk
	for chunk := range stream {
		if chunk.Done {
			finalChunk = chunk
		}
	}
	if !finalChunk.Done {
		t.Fatalf("expected done chunk")
	}
	if len(finalChunk.OrderedBlocks) != 2 {
		t.Fatalf("expected 2 ordered blocks (thinking + merged content), got %d: %+v", len(finalChunk.OrderedBlocks), finalChunk.OrderedBlocks)
	}
	ob := finalChunk.OrderedBlocks
	if ob[0].Type != conversation.BlockTypeThinking || ob[0].Content != "reasoning" || ob[0].Sequence != 0 {
		t.Errorf("block 0: %+v", ob[0])
	}
	if ob[1].Type != conversation.BlockTypeContent || ob[1].Content != "hello world" || ob[1].Sequence != 1 {
		t.Errorf("block 1: %+v", ob[1])
	}
}
