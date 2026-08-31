package agent_test

// thinking_streaming_bug_test.go — reproduces the SDK bug where ThinkingUpdate
// is not emitted during streaming, causing thinking to not render in new TUI chats.
//
// BUG SUMMARY (two-layer failure):
//
// Layer 1 — SDK (this file tests):
//   In streamChat() (agent_execute_chain.go), when a StreamChunk arrives with
//   Thinking != "", no callIntermediateCallback(ThinkingUpdate{}) is made.
//   The chunk is silently accumulated. ThinkingUpdate is only fired later from
//   executeLoop(), AFTER streamChat returns — by which time all ContentUpdate
//   events have already been emitted. This means:
//     • ThinkingEvent arrives AFTER all TextEvents on the agent.Stream() channel.
//     • The LLM's actual order (thinking → text) is reversed.
//
// Layer 2 — TUI (app_update.go, NOT tested here):
//   The agentThinkingMsg and agentHookExecutionMsg handlers add blocks to
//   OrderedBlocks but never call lastMsg.MarkDirty(). updateViewportIncremental()
//   skips dirty-checking and uses cached pre-rendered content, so the blocks are
//   invisible until the user navigates away and back or a full re-render fires.
//   Old conversations render fine because thinking is stored in the message and
//   rendered statically on load.

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// thinkingFirstProvider is a streaming mock that sends:
//
//	chunk 0: thinking-only chunk (Thinking = "let me reason...")
//	chunk 1: text chunk          (Delta    = "Hello!")
//	chunk 2: done chunk          (Done     = true, FinishReason = Stop)
//
// This mirrors the real Anthropic API ordering where thinking_delta events
// arrive before text_delta events. The SDK's Anthropic provider accumulates
// thinking_delta into accumulatedThinking and only forwards it in the final
// message_stop chunk, but a provider-agnostic streaming consumer (streamChat)
// must handle thinking chunks arriving at any point in the stream.
type thinkingFirstProvider struct{}

func (p *thinkingFirstProvider) Name() string { return "thinking-first-mock" }

func (p *thinkingFirstProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:        true, // forces agent to use streamChat, not Chat
		MaxContextWindow: 128_000,
		MaxOutputTokens:  4096,
	}
}

func (p *thinkingFirstProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "Hello!", Thinking: "let me reason..."},
		FinishReason: provider.FinishReasonStop,
		Usage:        &conversation.TokenUsage{Input: 10, Output: 1},
	}, nil
}

func (p *thinkingFirstProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 4)
	go func() {
		defer close(ch)
		// Chunk 0: thinking arrives before text — matches Anthropic's actual ordering.
		// streamChat() SHOULD fire ThinkingUpdate here. Currently it does NOT.
		ch <- provider.StreamChunk{
			Thinking: "let me reason...",
		}
		// Chunk 1: text arrives after thinking.
		// streamChat() correctly fires ContentUpdate here.
		ch <- provider.StreamChunk{
			Delta: "Hello!",
		}
		// Chunk 2: stream ends.
		ch <- provider.StreamChunk{
			FinishReason: provider.FinishReasonStop,
			Done:         true,
			Usage:        &conversation.TokenUsage{Input: 10, Output: 1},
		}
	}()
	return ch, nil
}

// TestStream_ThinkingEvent_order_is_wrong_due_to_deferred_emission demonstrates
// that ThinkingEvent arrives AFTER TextEvent on the agent.Stream() channel, even
// though the provider's stream sends the thinking chunk BEFORE the text chunk.
//
// The root cause is in streamChat() (agent_execute_chain.go lines ~239-247):
//
//	if chunk.Thinking != "" {
//	    accumulatedThinking = chunk.Thinking  // ← accumulated, never forwarded
//	    streamHadNonContent = true
//	    // MISSING: a.callIntermediateCallback(ctx, ThinkingUpdate{Content: chunk.Thinking})
//	}
//
// ThinkingUpdate is only emitted from executeLoop() after streamChat returns:
//
//	if resp.Message.Thinking != "" {
//	    a.callIntermediateCallback(ctx, ThinkingUpdate{Content: resp.Message.Thinking})
//	}
//
// This test FAILS with the bug present.
// When the bug is fixed, ThinkingEvent will arrive BEFORE TextEvent.
func TestStream_ThinkingEvent_order_is_wrong_due_to_deferred_emission(t *testing.T) {
	ag, err := agent.Build().
		Provider(&thinkingFirstProvider{}).
		Model("thinking-model").
		Create()
	if err != nil {
		t.Fatalf("Build().Create(): %v", err)
	}

	type eventKind string
	const (
		kindThinking eventKind = "thinking"
		kindText     eventKind = "text"
		kindDone     eventKind = "done"
	)

	var seq []eventKind
	for ev := range ag.Stream(context.Background(), "test prompt") {
		switch ev.(type) {
		case agent.ThinkingEvent:
			seq = append(seq, kindThinking)
		case agent.TextEvent:
			seq = append(seq, kindText)
		case agent.DoneEvent:
			seq = append(seq, kindDone)
		case agent.ErrorEvent:
			t.Fatalf("unexpected ErrorEvent")
		}
	}

	// Find first occurrence of each event kind.
	firstIdx := func(k eventKind) int {
		for i, s := range seq {
			if s == k {
				return i
			}
		}
		return -1
	}

	thinkingIdx := firstIdx(kindThinking)
	textIdx := firstIdx(kindText)

	if thinkingIdx < 0 {
		t.Fatalf("ThinkingEvent was never received; full sequence: %v", seq)
	}
	if textIdx < 0 {
		t.Fatalf("TextEvent was never received; full sequence: %v", seq)
	}

	// WANT: thinkingIdx < textIdx  (thinking arrives before text — correct order)
	// BUG:  thinkingIdx > textIdx  (thinking arrives after text — wrong order)
	//
	// The provider stream sends chunk 0 (thinking) before chunk 1 (text).
	// streamChat processes them in order but only fires ContentUpdate for chunk 1.
	// ThinkingUpdate is deferred to executeLoop, which runs after all chunks.
	// Result: TextEvent at index 0, ThinkingEvent at index 1+.
	if thinkingIdx > textIdx {
		t.Errorf(
			"BUG: ThinkingEvent at index %d, first TextEvent at index %d — wrong order.\n"+
				"Full event sequence: %v\n\n"+
				"EXPECTED sequence: [thinking, text, done]\n"+
				"ACTUAL   sequence: %v\n\n"+
				"ROOT CAUSE: streamChat() in agent_execute_chain.go never calls\n"+
				"  callIntermediateCallback(ctx, ThinkingUpdate{Content: chunk.Thinking})\n"+
				"when processing a chunk with Thinking != \"\". Instead, ThinkingUpdate is\n"+
				"fired by executeLoop() after streamChat() returns, placing it after all\n"+
				"ContentUpdate/TextEvent emissions.\n\n"+
				"FIX: In streamChat(), add:\n"+
				"  if chunk.Thinking != \"\" {\n"+
				"      a.callIntermediateCallback(ctx, ThinkingUpdate{\n"+
				"          Content: chunk.Thinking,\n"+
				"          Append:  accumulatedThinking != \"\",\n"+
				"      })\n"+
				"  }",
			thinkingIdx, textIdx, seq, seq,
		)
	}
}

// TestStream_ThinkingUpdate_not_fired_from_streamChat_for_thinking_chunk
// verifies the lower-level invariant: for every StreamChunk that carries
// Thinking content, exactly one ThinkingUpdate intermediate callback must be
// fired while streamChat is still processing that chunk (i.e., before any
// ContentUpdate for a later text chunk).
//
// This test uses Execute() + SetIntermediateCallback() directly, so it avoids
// the agent.Stream() wrapper and inspects the raw intermediate-update sequence.
//
// This test FAILS with the bug present.
func TestStream_ThinkingUpdate_not_fired_from_streamChat_for_thinking_chunk(t *testing.T) {
	ag, err := agent.Build().
		Provider(&thinkingFirstProvider{}).
		Model("thinking-model").
		Create()
	if err != nil {
		t.Fatalf("Build().Create(): %v", err)
	}

	// Capture updates with their sequence numbers so ordering is unambiguous.
	type taggedUpdate struct {
		seq  int
		kind string
	}
	var updates []taggedUpdate
	counter := 0

	ag.SetIntermediateCallback(func(_ context.Context, u agent.IntermediateUpdate) error {
		counter++
		updates = append(updates, taggedUpdate{seq: counter, kind: u.UpdateType()})
		return nil
	})

	_, err = ag.Execute(context.Background(), agent.ExecuteRequest{Message: "test"})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}

	// Determine positions of first ThinkingUpdate and first ContentUpdate.
	thinkingSeq := -1
	contentSeq := -1
	for _, u := range updates {
		if u.kind == "thinking" && thinkingSeq < 0 {
			thinkingSeq = u.seq
		}
		if u.kind == "content" && contentSeq < 0 {
			contentSeq = u.seq
		}
	}

	if thinkingSeq < 0 {
		t.Fatalf("ThinkingUpdate was never emitted; updates: %v", updates)
	}

	// The thinking chunk (chunk 0) is processed by streamChat before the text
	// chunk (chunk 1). A correct implementation fires ThinkingUpdate immediately
	// when the thinking chunk is processed, so thinkingSeq < contentSeq.
	//
	// With the bug: streamChat fires ContentUpdate for the text chunk but never
	// fires ThinkingUpdate for the thinking chunk. ThinkingUpdate is only emitted
	// by executeLoop after streamChat returns, so thinkingSeq > contentSeq.
	if contentSeq > 0 && thinkingSeq > contentSeq {
		t.Errorf(
			"BUG: ThinkingUpdate (seq=%d) fired AFTER first ContentUpdate (seq=%d).\n"+
				"Update log: %v\n\n"+
				"streamChat() sees the thinking chunk (Thinking != \"\") before the text\n"+
				"chunk (Delta != \"\"), so it must emit ThinkingUpdate before ContentUpdate.\n\n"+
				"Currently streamChat() silently accumulates chunk.Thinking without calling\n"+
				"callIntermediateCallback, deferring ThinkingUpdate to executeLoop.\n\n"+
				"File: swarm-sdk/agent/agent_execute_chain.go, func streamChat(), ~line 239",
			thinkingSeq, contentSeq, updates,
		)
	}
}
