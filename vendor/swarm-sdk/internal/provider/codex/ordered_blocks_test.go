package codex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestStream_EmitsOrderedBlocksForInterleavedTextAndToolCall drives a Codex
// SSE stream with text delta → function_call → text delta and asserts the
// final chunk carries OrderedBlocks in that native order with stable
// sequence numbers.
func TestStream_EmitsOrderedBlocksForInterleavedTextAndToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer does not support flushing")
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
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
	if len(finalChunk.OrderedBlocks) != 3 {
		t.Fatalf("expected 3 ordered blocks, got %d: %+v", len(finalChunk.OrderedBlocks), finalChunk.OrderedBlocks)
	}

	ob := finalChunk.OrderedBlocks
	if ob[0].Type != conversation.BlockTypeContent || ob[0].Content != "hello " || ob[0].Sequence != 0 {
		t.Errorf("block 0: %+v", ob[0])
	}
	if ob[1].Type != conversation.BlockTypeToolCall || ob[1].ToolCall == nil || ob[1].ToolCall.ID != "call_1" || ob[1].Sequence != 1 {
		t.Errorf("block 1: %+v", ob[1])
	}
	if ob[1].ToolCall.Parameters["command"] != "pwd" {
		t.Errorf("tool call parameters not propagated: %+v", ob[1].ToolCall.Parameters)
	}
	if ob[2].Type != conversation.BlockTypeContent || ob[2].Content != "world" || ob[2].Sequence != 2 {
		t.Errorf("block 2: %+v", ob[2])
	}

	// Sanity: flat ToolCalls slice remains populated for backward-compat.
	if len(finalChunk.ToolCalls) != 1 {
		t.Errorf("flat ToolCalls not populated: %+v", finalChunk.ToolCalls)
	}
}

// TestStream_OrderedBlocksUpsertPropagatesFinalizedArguments ensures that
// when output_item.added announces a tool call with placeholder args and
// output_item.done arrives later with finalized args, the tool_call block
// in OrderedBlocks reflects the finalized args — not the placeholder.
func TestStream_OrderedBlocksUpsertPropagatesFinalizedArguments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer does not support flushing")
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"bash\",\"arguments\":\"{}\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{}}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	prov, err := New(Config{
		AccessToken: "token",
		AccountID:   "acct",
		BaseURL:     srv.URL,
		Tracer:      &testTracer{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model:        "gpt-5.2-codex",
		SystemPrompt: "sys",
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
	if len(finalChunk.OrderedBlocks) != 1 {
		t.Fatalf("expected 1 tool_call block, got %d", len(finalChunk.OrderedBlocks))
	}
	tc := finalChunk.OrderedBlocks[0].ToolCall
	if tc == nil {
		t.Fatalf("ToolCall nil on block: %+v", finalChunk.OrderedBlocks[0])
	}
	if tc.Parameters["command"] != "pwd" {
		t.Errorf("ordered block should reflect finalized arguments, got %+v", tc.Parameters)
	}
}
