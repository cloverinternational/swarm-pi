package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestStream_EmitsOrderedBlocksForAccumulatedContent drives an Ollama
// newline-delimited JSON stream with several content deltas and asserts
// the final chunk exposes OrderedBlocks with a single merged content
// block carrying the full accumulated text.
func TestStream_EmitsOrderedBlocksForAccumulatedContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer does not support flushing")
		}
		fmt.Fprintln(w, `{"model":"llama3","message":{"role":"assistant","content":"hello "},"done":false}`)
		flusher.Flush()
		fmt.Fprintln(w, `{"model":"llama3","message":{"role":"assistant","content":"world"},"done":false}`)
		flusher.Flush()
		fmt.Fprintln(w, `{"model":"llama3","message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":5,"eval_count":9}`)
		flusher.Flush()
	}))
	defer srv.Close()

	cfg := &Config{
		BaseURL: srv.URL,
		Model:   "llama3",
	}
	prov := New(cfg)

	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model: "llama3",
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
	if len(finalChunk.OrderedBlocks) != 1 {
		t.Fatalf("expected 1 content block, got %d: %+v", len(finalChunk.OrderedBlocks), finalChunk.OrderedBlocks)
	}
	ob := finalChunk.OrderedBlocks[0]
	if ob.Type != conversation.BlockTypeContent || ob.Content != "hello world" || ob.Sequence != 0 {
		t.Errorf("block: %+v", ob)
	}
}

// TestStream_EmptyResponseEmitsNoOrderedBlocks guards against dropping a
// nil slice: when the stream ends without any text, OrderedBlocks should
// be empty/nil rather than a stray zero-content block.
func TestStream_EmptyResponseEmitsNoOrderedBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer does not support flushing")
		}
		fmt.Fprintln(w, `{"model":"llama3","message":{"role":"assistant","content":""},"done":true}`)
		flusher.Flush()
	}))
	defer srv.Close()

	prov := New(&Config{BaseURL: srv.URL, Model: "llama3"})
	stream, err := prov.Stream(context.Background(), provider.ChatRequest{
		Model: "llama3",
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
	if len(finalChunk.OrderedBlocks) != 0 {
		t.Fatalf("expected no ordered blocks for empty response, got %+v", finalChunk.OrderedBlocks)
	}
}
