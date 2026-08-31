package chat

import (
	"fmt"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// TestFindOrCreateSubAgentBlock_SingleAgent verifies that a single sub-agent's
// updates are all routed to the same block.
func TestFindOrCreateSubAgentBlock_SingleAgent(t *testing.T) {
	msg := &Message{Role: "assistant"}

	// First update creates the block.
	sa1 := findOrCreateSubAgentBlock(msg, "agent-1", "Explorer", 0)
	if sa1 == nil {
		t.Fatal("expected non-nil SubAgentDisplay")
	}
	if sa1.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want %q", sa1.AgentID, "agent-1")
	}
	if len(msg.OrderedBlocks) != 1 {
		t.Fatalf("OrderedBlocks len = %d, want 1", len(msg.OrderedBlocks))
	}

	// Second update finds the existing block.
	sa2 := findOrCreateSubAgentBlock(msg, "agent-1", "Explorer", 1)
	if sa2 != sa1 {
		t.Error("second call should return the same block pointer")
	}
	if len(msg.OrderedBlocks) != 1 {
		t.Fatalf("OrderedBlocks len = %d, want 1 (no duplicate)", len(msg.OrderedBlocks))
	}
}

// TestFindOrCreateSubAgentBlock_ParallelAgents verifies that N parallel
// sub-agents each get their own block and never merge.
func TestFindOrCreateSubAgentBlock_ParallelAgents(t *testing.T) {
	for _, n := range []int{2, 3, 5, 10} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			msg := &Message{Role: "assistant"}

			// Add N Task tool calls first (simulating parallel dispatch).
			for i := range n {
				msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
					Type: "tool_call",
					ToolCall: &ToolCallDisplay{
						ID:   fmt.Sprintf("call-%d", i),
						Name: "Task",
						Parameters: map[string]any{
							"task": fmt.Sprintf("task-%d", i),
						},
					},
					Sequence: i,
				})
			}

			// Create N sub-agent blocks (simulating interleaved creation).
			blocks := make([]*SubAgentDisplay, n)
			for i := range n {
				blocks[i] = findOrCreateSubAgentBlock(msg, fmt.Sprintf("agent-%d", i), "Worker", i+n)
			}

			// Verify we got N distinct blocks.
			seen := make(map[*SubAgentDisplay]bool)
			for i, b := range blocks {
				if b == nil {
					t.Fatalf("block[%d] is nil", i)
				}
				if seen[b] {
					t.Fatalf("block[%d] is a duplicate of an earlier block", i)
				}
				seen[b] = true
				if b.AgentID != fmt.Sprintf("agent-%d", i) {
					t.Errorf("block[%d].AgentID = %q, want %q", i, b.AgentID, fmt.Sprintf("agent-%d", i))
				}
			}

			// Count sub_agent_activity blocks in OrderedBlocks.
			saCount := 0
			for _, b := range msg.OrderedBlocks {
				if b.Type == "sub_agent_activity" {
					saCount++
				}
			}
			if saCount != n {
				t.Errorf("sub_agent_activity count = %d, want %d", saCount, n)
			}

			// Interleaved updates should route to the correct block.
			for round := range 3 {
				for i := range n {
					found := findOrCreateSubAgentBlock(msg, fmt.Sprintf("agent-%d", i), "Worker", 100+round*n+i)
					if found != blocks[i] {
						t.Errorf("round %d: agent-%d routed to wrong block", round, i)
					}
				}
			}

			// Verify no new blocks were created by the interleaved lookups.
			saCount2 := 0
			for _, b := range msg.OrderedBlocks {
				if b.Type == "sub_agent_activity" {
					saCount2++
				}
			}
			if saCount2 != n {
				t.Errorf("after interleaved lookups: sub_agent_activity count = %d, want %d", saCount2, n)
			}
		})
	}
}

// TestFindOrCreateSubAgentBlock_SameNameDifferentID tests that agents with the
// same name but different IDs get separate blocks.
func TestFindOrCreateSubAgentBlock_SameNameDifferentID(t *testing.T) {
	msg := &Message{Role: "assistant"}

	sa1 := findOrCreateSubAgentBlock(msg, "id-1", "Explorer", 0)
	sa2 := findOrCreateSubAgentBlock(msg, "id-2", "Explorer", 1)
	sa3 := findOrCreateSubAgentBlock(msg, "id-3", "Explorer", 2)

	if sa1 == sa2 || sa2 == sa3 || sa1 == sa3 {
		t.Fatal("agents with different IDs but same name must not share blocks")
	}
}

// TestFindOrCreateSubAgentBlock_EmptyAgentID tests the fallback matching when
// the SDK does not provide an AgentID (backward compat).
func TestFindOrCreateSubAgentBlock_EmptyAgentID(t *testing.T) {
	msg := &Message{Role: "assistant"}

	// With empty IDs and same name, they should share a block (legacy behavior).
	sa1 := findOrCreateSubAgentBlock(msg, "", "Explorer", 0)
	sa2 := findOrCreateSubAgentBlock(msg, "", "Explorer", 1)
	if sa1 != sa2 {
		t.Error("empty-ID agents with same name should share a block (legacy compat)")
	}

	// But a named agent should NOT match an agent with an ID.
	sa3 := findOrCreateSubAgentBlock(msg, "real-id", "Explorer", 2)
	if sa3 == sa1 {
		t.Error("agent with ID should not match agent without ID")
	}
}

// TestFindOrCreateSubAgentBlock_TaskInstructionMatching tests that each new
// sub-agent block gets the correct task instruction from an unclaimed Task call.
func TestFindOrCreateSubAgentBlock_TaskInstructionMatching(t *testing.T) {
	msg := &Message{Role: "assistant"}

	// Add 3 Task tool calls.
	for i := range 3 {
		msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
			Type: "tool_call",
			ToolCall: &ToolCallDisplay{
				ID:   fmt.Sprintf("call-%d", i),
				Name: "Task",
				Parameters: map[string]any{
					"task": fmt.Sprintf("Do thing %d", i),
				},
			},
		})
	}

	// Create sub-agents one by one; each should claim a different Task call.
	sa1 := findOrCreateSubAgentBlock(msg, "a1", "Worker", 10)
	sa2 := findOrCreateSubAgentBlock(msg, "a2", "Worker", 11)
	sa3 := findOrCreateSubAgentBlock(msg, "a3", "Worker", 12)

	// Each must have a non-empty, unique task instruction.
	instructions := map[string]bool{
		sa1.TaskInstruction: true,
		sa2.TaskInstruction: true,
		sa3.TaskInstruction: true,
	}
	if len(instructions) != 3 {
		t.Errorf("expected 3 unique task instructions, got %d: [%q, %q, %q]",
			len(instructions), sa1.TaskInstruction, sa2.TaskInstruction, sa3.TaskInstruction)
	}
}

// TestProcessSubAgentInnerUpdate_AllTypes tests that all inner update types
// are correctly dispatched.
func TestProcessSubAgentInnerUpdate_AllTypes(t *testing.T) {
	sa := &SubAgentDisplay{AgentID: "test", AgentName: "Test", Blocks: []MessageBlock{}}

	// Thinking
	processSubAgentInnerUpdate(sa, agent.ThinkingUpdate{Content: "hmm", Append: false})
	if len(sa.Blocks) != 1 || sa.Blocks[0].Type != "thinking" {
		t.Fatal("thinking block not created")
	}

	// Append thinking
	processSubAgentInnerUpdate(sa, agent.ThinkingUpdate{Content: " more", Append: true})
	if len(sa.Blocks) != 1 {
		t.Fatal("appended thinking should not create new block")
	}
	if sa.Blocks[0].GetContent() != "hmm more" {
		t.Errorf("thinking content = %q, want %q", sa.Blocks[0].GetContent(), "hmm more")
	}

	// Content
	processSubAgentInnerUpdate(sa, agent.ContentUpdate{Content: "hello", Append: false})
	if len(sa.Blocks) != 2 || sa.Blocks[1].Type != "content" {
		t.Fatal("content block not created")
	}

	// Append content
	processSubAgentInnerUpdate(sa, agent.ContentUpdate{Content: " world", Append: true})
	if len(sa.Blocks) != 2 {
		t.Fatal("appended content should not create new block")
	}
	if sa.Blocks[1].GetContent() != "hello world" {
		t.Errorf("content = %q, want %q", sa.Blocks[1].GetContent(), "hello world")
	}

	// Tool call
	processSubAgentInnerUpdate(sa, agent.ToolCallUpdate{ID: "tc1", Name: "Read", Parameters: map[string]any{"path": "/tmp"}})
	if len(sa.Blocks) != 3 || sa.Blocks[2].Type != "tool_call" {
		t.Fatal("tool_call block not created")
	}
	if sa.Blocks[2].ToolCall.Name != "Read" {
		t.Errorf("tool name = %q, want Read", sa.Blocks[2].ToolCall.Name)
	}

	// Tool output chunk (streaming — creates a new tool_result)
	processSubAgentInnerUpdate(sa, agent.ToolOutputChunk{ID: "tc1", Chunk: "line1\n"})
	if len(sa.Blocks) != 4 || sa.Blocks[3].Type != "tool_result" {
		t.Fatal("streaming tool_result not created")
	}
	if sa.Blocks[3].ToolResult.CallID != "tc1" {
		t.Errorf("tool_result callID = %q, want tc1", sa.Blocks[3].ToolResult.CallID)
	}

	// Another chunk appends to the existing result
	processSubAgentInnerUpdate(sa, agent.ToolOutputChunk{ID: "tc1", Chunk: "line2\n"})
	if len(sa.Blocks) != 4 {
		t.Fatal("streaming chunk should append, not create new block")
	}
	if sa.Blocks[3].ToolResult.GetOutput() != "line1\nline2\n" {
		t.Errorf("streamed output = %q", sa.Blocks[3].ToolResult.GetOutput())
	}

	// Final tool result overwrites the streaming one
	processSubAgentInnerUpdate(sa, agent.ToolResultUpdate{ID: "tc1", Output: "final output", Error: nil})
	if len(sa.Blocks) != 4 {
		t.Fatal("final result should update existing block")
	}
	if sa.Blocks[3].ToolResult.GetOutput() != "final output" {
		t.Errorf("final output = %q, want %q", sa.Blocks[3].ToolResult.GetOutput(), "final output")
	}
	if sa.Blocks[3].ToolResult.IsStreaming {
		t.Error("final result should not be streaming")
	}
}

// TestProcessSubAgentInnerUpdate_NestedSubAgent tests recursive sub-agent handling.
func TestProcessSubAgentInnerUpdate_NestedSubAgent(t *testing.T) {
	parent := &SubAgentDisplay{AgentID: "parent", AgentName: "Parent", Blocks: []MessageBlock{}}

	// Nested sub-agent update
	processSubAgentInnerUpdate(parent, agent.SubAgentUpdate{
		AgentID:   "child-1",
		AgentName: "Child",
		Update:    agent.ContentUpdate{Content: "nested content", Append: false},
	})

	// Should create a sub_agent_activity block
	if len(parent.Blocks) != 1 || parent.Blocks[0].Type != "sub_agent_activity" {
		t.Fatal("nested sub-agent block not created")
	}
	nested := parent.Blocks[0].SubAgentActivity
	if nested.AgentID != "child-1" {
		t.Errorf("nested AgentID = %q, want child-1", nested.AgentID)
	}
	if len(nested.Blocks) != 1 || nested.Blocks[0].GetContent() != "nested content" {
		t.Fatal("nested content not set")
	}

	// Another update to the same child should reuse the block
	processSubAgentInnerUpdate(parent, agent.SubAgentUpdate{
		AgentID:   "child-1",
		AgentName: "Child",
		Update:    agent.ContentUpdate{Content: " more", Append: true},
	})
	if len(parent.Blocks) != 1 {
		t.Fatal("second nested update should not create new block")
	}
	if nested.Blocks[0].GetContent() != "nested content more" {
		t.Errorf("appended nested content = %q", nested.Blocks[0].GetContent())
	}

	// Different child
	processSubAgentInnerUpdate(parent, agent.SubAgentUpdate{
		AgentID:   "child-2",
		AgentName: "Child",
		Update:    agent.ContentUpdate{Content: "other child", Append: false},
	})
	if len(parent.Blocks) != 2 {
		t.Fatal("different child should create new block")
	}
}

// TestFindUnmatchedTaskInstruction verifies correct Task claim logic.
func TestFindUnmatchedTaskInstruction(t *testing.T) {
	msg := &Message{Role: "assistant"}

	// No Task calls → empty
	if inst := findUnmatchedTaskInstruction(msg); inst != "" {
		t.Errorf("expected empty, got %q", inst)
	}

	// Add a Task call
	msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
		Type: "tool_call",
		ToolCall: &ToolCallDisplay{
			ID:         "tc-1",
			Name:       "Task",
			Parameters: map[string]any{"task": "first task"},
		},
	})

	// Should find the unclaimed task
	if inst := findUnmatchedTaskInstruction(msg); inst != "first task" {
		t.Errorf("expected %q, got %q", "first task", inst)
	}

	// Claim it by adding a sub-agent block after it
	msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
		Type:             "sub_agent_activity",
		SubAgentActivity: &SubAgentDisplay{AgentID: "a1"},
	})

	// Now it's claimed → should return empty
	if inst := findUnmatchedTaskInstruction(msg); inst != "" {
		t.Errorf("expected empty after claim, got %q", inst)
	}

	// Add another Task call
	msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
		Type: "tool_call",
		ToolCall: &ToolCallDisplay{
			ID:         "tc-2",
			Name:       "Task",
			Parameters: map[string]any{"task": "second task"},
		},
	})

	// Should find the new unclaimed one
	if inst := findUnmatchedTaskInstruction(msg); inst != "second task" {
		t.Errorf("expected %q, got %q", "second task", inst)
	}
}
