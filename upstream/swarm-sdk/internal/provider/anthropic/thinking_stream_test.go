package anthropic

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestProcessSSEStreamEmitsThinkingBeforeText(t *testing.T) {
	p, err := New(Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_x","model":"claude","usage":{"input_tokens":10}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason "}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"carefully"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	chunks := make(chan provider.StreamChunk, 16)
	go func() {
		defer close(chunks)
		if err := p.processSSEStream(context.Background(), strings.NewReader(sse), chunks); err != nil {
			t.Errorf("processSSEStream: %v", err)
		}
	}()

	var thinking strings.Builder
	var eventKinds []string
	var final provider.StreamChunk
	for chunk := range chunks {
		if chunk.Thinking != "" {
			thinking.WriteString(chunk.Thinking)
			eventKinds = append(eventKinds, "thinking")
		}
		if chunk.Delta != "" {
			eventKinds = append(eventKinds, "text")
		}
		if chunk.Done {
			final = chunk
		}
	}
	if got := thinking.String(); got != "reason carefully" {
		t.Fatalf("streamed thinking = %q, want %q", got, "reason carefully")
	}
	if len(eventKinds) != 3 || eventKinds[0] != "thinking" || eventKinds[1] != "thinking" || eventKinds[2] != "text" {
		t.Fatalf("event order = %v, want [thinking thinking text]", eventKinds)
	}
	if final.Thinking != "" {
		t.Fatalf("final chunk repeated already-streamed thinking: %q", final.Thinking)
	}
	if len(final.OrderedBlocks) != 2 || final.OrderedBlocks[0].Type != conversation.BlockTypeThinking || final.OrderedBlocks[0].Content != "reason carefully" || final.OrderedBlocks[1].Type != conversation.BlockTypeContent {
		t.Fatalf("final ordered blocks = %+v", final.OrderedBlocks)
	}
}
