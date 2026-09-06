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

func TestBuildRequestRequestsReasoningSummary(t *testing.T) {
	payload, err := buildRequest(provider.ChatRequest{
		Model:           "gpt-5.6-sol",
		ReasoningEffort: provider.ReasoningEffortHigh,
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning payload = %#v", payload["reasoning"])
	}
	if reasoning["effort"] != provider.ReasoningEffortHigh || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning payload = %#v", reasoning)
	}
}

func TestStreamEmitsReasoningSummaryBeforeTextWithoutDuplicates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"plan \"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"carefully\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.done\",\"text\":\"plan carefully\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"plan carefully\"}],\"encrypted_content\":\"secret\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2},\"output\":[{\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"plan carefully\"}]},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	prov, err := New(Config{AccessToken: "token", AccountID: "acct", BaseURL: srv.URL, Tracer: &testTracer{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := prov.Stream(context.Background(), provider.ChatRequest{Model: "gpt-5.6-sol", SystemPrompt: "sys"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var thinking, text string
	var eventKinds []string
	var final provider.StreamChunk
	for chunk := range stream {
		if chunk.Thinking != "" {
			thinking += chunk.Thinking
			eventKinds = append(eventKinds, "thinking")
		}
		if chunk.Delta != "" {
			text += chunk.Delta
			eventKinds = append(eventKinds, "text")
		}
		if chunk.Done {
			final = chunk
		}
	}
	if thinking != "plan carefully" || text != "answer" {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
	if len(eventKinds) != 3 || eventKinds[0] != "thinking" || eventKinds[1] != "thinking" || eventKinds[2] != "text" {
		t.Fatalf("event order = %v", eventKinds)
	}
	if final.Thinking != "" {
		t.Fatalf("final chunk duplicated summary: %q", final.Thinking)
	}
	if len(final.OrderedBlocks) != 2 || final.OrderedBlocks[0].Type != conversation.BlockTypeThinking || final.OrderedBlocks[0].Content != "plan carefully" || final.OrderedBlocks[1].Type != conversation.BlockTypeContent || final.OrderedBlocks[1].Content != "answer" {
		t.Fatalf("ordered blocks = %+v", final.OrderedBlocks)
	}
}

func TestStreamEmitsReasoningTextFromCurrentResponsesEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"inspect \"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"carefully\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_text.done\",\"text\":\"inspect carefully\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"content\":[{\"type\":\"reasoning_text\",\"text\":\"inspect carefully\"}]}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2},\"output\":[{\"type\":\"reasoning\",\"content\":[{\"type\":\"reasoning_text\",\"text\":\"inspect carefully\"}]},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	prov, err := New(Config{AccessToken: "token", AccountID: "acct", BaseURL: srv.URL, Tracer: &testTracer{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := prov.Stream(context.Background(), provider.ChatRequest{Model: "gpt-5.6-terra", SystemPrompt: "sys"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var thinking, text string
	var final provider.StreamChunk
	for chunk := range stream {
		thinking += chunk.Thinking
		text += chunk.Delta
		if chunk.Done {
			final = chunk
		}
	}
	if thinking != "inspect carefully" || text != "answer" {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
	if len(final.OrderedBlocks) != 2 || final.OrderedBlocks[0].Type != conversation.BlockTypeThinking || final.OrderedBlocks[0].Content != "inspect carefully" {
		t.Fatalf("ordered blocks = %+v", final.OrderedBlocks)
	}
}
