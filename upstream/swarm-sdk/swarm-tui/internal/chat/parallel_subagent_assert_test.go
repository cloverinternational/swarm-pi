package chat

import (
	"fmt"
	"testing"
)

// buildAssertMsg builds a Message with Task tool_calls, optional sub_agent_activity
// blocks, and matching tool_results for testing assertParallelSubAgentsNotMerged.
func buildAssertMsg(calls []struct {
	callID string
	task   string
	result string // tool result output; include "async_launched" for background tasks
}, syncBlocks int) *Message {
	msg := &Message{Role: "assistant"}

	// tool_call blocks
	for _, c := range calls {
		msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
			Type: "tool_call",
			ToolCall: &ToolCallDisplay{
				ID:         c.callID,
				Name:       "Task",
				Parameters: map[string]any{"task": c.task},
			},
		})
	}

	// sub_agent_activity blocks (only for sync tasks)
	for i := range syncBlocks {
		msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
			Type: "sub_agent_activity",
			SubAgentActivity: &SubAgentDisplay{
				AgentID:   fmt.Sprintf("agent-%d", i),
				AgentName: fmt.Sprintf("Agent %d", i),
			},
		})
	}

	// tool_result blocks (one per call, in order)
	for _, c := range calls {
		msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
			Type: "tool_result",
			ToolResult: &ToolResultDisplay{
				CallID:   c.callID,
				ToolName: "Task",
				Output:   c.result,
			},
		})
	}

	return msg
}

// TestAssertParallelSubAgentsNotMerged_SyncAndAsyncMixed verifies that when one of
// two parallel Task calls returns async_launched the assertion does NOT panic.
// This is the production crash scenario: mixed sync+background Task calls.
func TestAssertParallelSubAgentsNotMerged_SyncAndAsyncMixed(t *testing.T) {
	msg := buildAssertMsg([]struct {
		callID string
		task   string
		result string
	}{
		{"call-sync", "Task one (sync)", `{"result":"done","tokens_used":100,"turns":2}`},
		{"call-bg", "Task two (bg)", `{"status":"async_launched","agent_id":"bg-abc","output_file":"/tmp/abc.output"}`},
	}, 1 /* only the sync task has a sub_agent_activity block */)

	// Must NOT panic — async_launched tasks are exempt from the block requirement
	assertParallelSubAgentsNotMerged(msg)
}

// TestAssertParallelSubAgentsNotMerged_BothAsync verifies that two async_launched
// Tasks with zero sub_agent_activity blocks do not trigger the assertion.
func TestAssertParallelSubAgentsNotMerged_BothAsync(t *testing.T) {
	msg := buildAssertMsg([]struct {
		callID string
		task   string
		result string
	}{
		{"call-bg-1", "Task one", `{"status":"async_launched","agent_id":"bg-1"}`},
		{"call-bg-2", "Task two", `{"status":"async_launched","agent_id":"bg-2"}`},
	}, 0)

	// Must NOT panic — all tasks were async
	assertParallelSubAgentsNotMerged(msg)
}

// TestAssertParallelSubAgentsNotMerged_BothSync_Correct verifies that two sync Tasks
// each with a dedicated sub_agent_activity block do not trigger the assertion.
func TestAssertParallelSubAgentsNotMerged_BothSync_Correct(t *testing.T) {
	msg := buildAssertMsg([]struct {
		callID string
		task   string
		result string
	}{
		{"call-1", "Task one", `{"result":"done","tokens_used":100}`},
		{"call-2", "Task two", `{"result":"done","tokens_used":200}`},
	}, 2 /* two blocks — one per sync task */)

	// Must NOT panic
	assertParallelSubAgentsNotMerged(msg)
}

// TestAssertParallelSubAgentsNotMerged_MergeBug verifies that the assertion
// degrades to logging instead of panicking when two sync Tasks are merged into
// one sub_agent_activity block.
func TestAssertParallelSubAgentsNotMerged_MergeBug(t *testing.T) {
	msg := buildAssertMsg([]struct {
		callID string
		task   string
		result string
	}{
		{"call-1", "Task one", `{"result":"done","tokens_used":100}`},
		{"call-2", "Task two", `{"result":"done","tokens_used":200}`},
	}, 1 /* only 1 block — two sync tasks merged into one: BUG */)

	assertParallelSubAgentsNotMerged(msg)
}

// TestAssertParallelSubAgentsNotMerged_SingleTask verifies that single-Task messages
// are skipped entirely (no parallel check needed).
func TestAssertParallelSubAgentsNotMerged_SingleTask(t *testing.T) {
	msg := buildAssertMsg([]struct {
		callID string
		task   string
		result string
	}{
		{"call-1", "Single task", `{"result":"done"}`},
	}, 1)

	// Must NOT panic — only one Task call
	assertParallelSubAgentsNotMerged(msg)
}
