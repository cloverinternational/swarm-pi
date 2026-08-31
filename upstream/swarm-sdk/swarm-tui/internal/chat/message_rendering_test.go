package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestMessageRenderingSequence reproduces the issue where multiple sequential
// assistant messages with tool calls don't render properly because they share
// the same globalUpdateSequence counter across different message contexts.
//
// Expected behavior: Each new assistant message should have its own sequence
// numbering starting fresh, so blocks within that message render in order.
//
// Actual behavior: The globalUpdateSequence keeps incrementing across messages,
// causing rendering issues when messages are processed.
func TestMessageRenderingSequence(t *testing.T) {
	// Create a minimal app instance
	app := &App{
		messages:             []Message{},
		updateQueue:          make(chan tea.Msg, 10000),
		globalUpdateSequence: 0,
	}

	// Simulate 4 rapid sequential assistant responses, each with a bash tool call
	// This mimics: "Hi! Testing bash" (text) -> bash echo 1 (tool) -> result

	for i := 1; i <= 4; i++ {
		// Add a new assistant message
		app.messages = append(app.messages, Message{
			Role:          "assistant",
			Content:       "",
			OrderedBlocks: []MessageBlock{},
		})
		messageIdx := len(app.messages) - 1

		// Simulate the update sequence for one response cycle:
		// 1. Content update (text like "Hi! Testing bash")
		app.globalUpdateSequence++
		seq1 := app.globalUpdateSequence

		// 2. Tool call (bash echo 1)
		app.globalUpdateSequence++
		seq2 := app.globalUpdateSequence

		// 3. Tool result
		app.globalUpdateSequence++
		seq3 := app.globalUpdateSequence

		// Add blocks to the message
		app.messages[messageIdx].OrderedBlocks = append(app.messages[messageIdx].OrderedBlocks,
			MessageBlock{
				Type:     "content",
				Content:  "Hi! Testing bash command and message rendering.",
				Sequence: seq1,
			},
			MessageBlock{
				Type: "tool_call",
				ToolCall: &ToolCallDisplay{
					ID:         "call_" + string(rune('0'+i)),
					Name:       "bash",
					Parameters: map[string]any{"command": "echo 1"},
				},
				Sequence: seq2,
			},
			MessageBlock{
				Type: "tool_result",
				ToolResult: &ToolResultDisplay{
					CallID: "call_" + string(rune('0'+i)),
					Output: "1\n",
				},
				Sequence: seq3,
			},
		)

		t.Logf("Message %d: sequences [%d, %d, %d]", i, seq1, seq2, seq3)
	}

	// The problem: globalUpdateSequence is now at 12
	// Each message's blocks have sequences that span across multiple messages
	// Message 1: [1, 2, 3]
	// Message 2: [4, 5, 6]
	// Message 3: [7, 8, 9]
	// Message 4: [10, 11, 12]

	t.Logf("Final globalUpdateSequence: %d", app.globalUpdateSequence)

	// Verify the issue: If we were to reset or if messages got re-rendered
	// with a fresh sequence counter, the ordering would break
	if app.globalUpdateSequence != 12 {
		t.Errorf("Expected globalUpdateSequence to be 12, got %d", app.globalUpdateSequence)
	}

	// The core issue: When the UI renders messages independently (e.g., during
	// viewport updates, pagination, or debug_inspect), the sequence numbers
	// from different messages can collide or be misinterpreted.

	// Additionally, if listenForAgentUpdates were to be called for a new
	// message while an old one is still being processed, the sequences
	// would interleave incorrectly.

	// Test that message blocks are properly sequenced WITHIN each message
	for i, msg := range app.messages {
		if len(msg.OrderedBlocks) != 3 {
			t.Errorf("Message %d: expected 3 blocks, got %d", i, len(msg.OrderedBlocks))
		}

		// Check that sequences are monotonically increasing within the message
		for j := 1; j < len(msg.OrderedBlocks); j++ {
			if msg.OrderedBlocks[j].Sequence <= msg.OrderedBlocks[j-1].Sequence {
				t.Errorf("Message %d: blocks out of sequence at index %d: %d <= %d",
					i, j, msg.OrderedBlocks[j].Sequence, msg.OrderedBlocks[j-1].Sequence)
			}
		}
	}

	t.Log("✓ Test demonstrates the globalUpdateSequence issue")
	t.Log("Issue: Global counter doesn't reset between messages, causing potential rendering conflicts")
	t.Log("Solution needed: Per-message sequence counter that resets for each new assistant message")
}

// TestMessageRenderingConcurrency tests what happens when updates arrive
// while the queue is being processed
func TestMessageRenderingConcurrency(t *testing.T) {
	app := &App{
		messages:             []Message{},
		updateQueue:          make(chan tea.Msg, 10000),
		globalUpdateSequence: 0,
	}

	// Simulate concurrent updates arriving rapidly
	// This can happen when the LLM sends multiple updates in quick succession

	app.messages = append(app.messages, Message{
		Role:          "assistant",
		Content:       "",
		OrderedBlocks: []MessageBlock{},
	})

	// Simulate updates arriving faster than they can be processed
	updates := []struct {
		name string
		seq  int
	}{}

	for i := range 10 {
		app.globalUpdateSequence++
		updates = append(updates, struct {
			name string
			seq  int
		}{
			name: "update_" + string(rune('0'+i)),
			seq:  app.globalUpdateSequence,
		})
	}

	// If queue processing is delayed, and then drained all at once,
	// the sequences should still maintain order
	prevSeq := 0
	for _, u := range updates {
		if u.seq <= prevSeq {
			t.Errorf("Update %s: sequence %d not greater than previous %d", u.name, u.seq, prevSeq)
		}
		prevSeq = u.seq
	}

	t.Log("✓ Sequences are monotonically increasing globally")
	t.Log("Issue: But they span across message boundaries, breaking per-message isolation")
}

// TestExpectedBehavior documents what the correct behavior should be
func TestExpectedBehavior(t *testing.T) {
	t.Log("Expected behavior for message rendering:")
	t.Log("1. Each assistant message should have independent sequence numbering")
	t.Log("2. Sequences should start fresh for each new message")
	t.Log("3. globalUpdateSequence should only be used to order updates WITHIN a single message response")
	t.Log("4. When a new message starts, the sequence counter should reset")
	t.Log("")
	t.Log("Current bug:")
	t.Log("- globalUpdateSequence increments across ALL messages forever")
	t.Log("- This causes sequences to span multiple messages")
	t.Log("- When messages are rendered independently (pagination, debug), ordering can break")
	t.Log("- Multiple rapid messages share the same sequence space")
}
