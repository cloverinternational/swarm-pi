package chat

import (
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/subagent"
)

// findOrCreateSubAgentBlock locates an existing SubAgentDisplay block that matches
// the given agentID/agentName, or creates a new one.
//
// Matching strategy (in priority order):
//
//  1. AgentID exact match — the SDK assigns a globally-unique ID to every sub-agent.
//     This is the primary discriminator for parallel agents and is always preferred.
//
//  2. AgentName match when BOTH sides have empty AgentID — this covers older SDK
//     versions or code paths (e.g. delegate_task.go before the AgentID fix) where
//     the ID was not propagated. To avoid false merges with named agents that DO
//     have an ID, we only match when both the incoming update and the existing block
//     have an empty AgentID.
//
// If no match is found, a new sub_agent_activity block is appended to the message.
// The new block is wired to the first unmatched Task tool call (if any) to display
// the task instruction in the sub-agent box header.
//
// This function is safe for any number of parallel sub-agents (no hard-coded limits).
func findOrCreateSubAgentBlock(msg *Message, agentID, agentName string, sequence int) *SubAgentDisplay {
	// ── Phase 1: Search ALL existing blocks for a match ──────────────────
	//
	// We iterate forwards so that earlier blocks are found first, which
	// preserves deterministic ordering when the same agent sends multiple
	// sequential updates.
	for i := range msg.OrderedBlocks {
		block := &msg.OrderedBlocks[i]
		if block.Type != "sub_agent_activity" || block.SubAgentActivity == nil {
			continue
		}
		sa := block.SubAgentActivity

		// Primary match: unique AgentID (non-empty on both sides).
		if agentID != "" && sa.AgentID == agentID {
			logDebug("[SubAgentDispatch] Matched by AgentID: agent_id=%s name=%s blockIdx=%d",
				agentID, agentName, i)
			return sa
		}

		// Fallback match: both sides have no AgentID and names match.
		// This is a weaker signal, but is the best we can do if the SDK
		// did not populate AgentID (e.g. older builds, edge-case paths).
		if agentID == "" && sa.AgentID == "" && sa.AgentName == agentName {
			logDebug("[SubAgentDispatch] Matched by AgentName: name=%s (no agent_id) blockIdx=%d",
				agentName, i)
			return sa
		}
	}

	// ── Phase 2: Create a new block ─────────────────────────────────────
	taskInstruction := findUnmatchedTaskInstruction(msg)

	newBlock := &SubAgentDisplay{
		AgentID:         agentID,
		AgentName:       agentName,
		TaskInstruction: taskInstruction,
		Blocks:          make([]MessageBlock, 0, 16), // pre-alloc for streaming
		StartTime:       time.Now(),
		SpinnerVerb:     subagent.SampleSpinnerVerb(agentName),
		CompletionVerb:  subagent.SampleCompletionVerb(agentName),
	}
	msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
		Type:             "sub_agent_activity",
		SubAgentActivity: newBlock,
		Sequence:         sequence,
	})

	saCount := 0
	for _, b := range msg.OrderedBlocks {
		if b.Type == "sub_agent_activity" {
			saCount++
		}
	}
	logDebug("[SubAgentDispatch] CREATED sub_agent_activity #%d: agent_id=%s name=%s task=%q totalBlocks=%d status=spawned",
		saCount, agentID, agentName, truncateForLogSA(taskInstruction, 80), len(msg.OrderedBlocks))

	return newBlock
}

// findUnmatchedTaskInstruction scans ordered blocks to find a Task tool call
// that does not yet have a corresponding sub_agent_activity block.
//
// For parallel agents, multiple Task calls exist in the same message. We
// pair them with sub-agent blocks in order: the first sub-agent block claims
// the first Task call, the second claims the second, etc. This works
// regardless of whether the blocks are interleaved or all appended at the end.
func findUnmatchedTaskInstruction(msg *Message) string {
	// Collect all Task call IDs in order of appearance.
	var taskCalls []string                      // ordered Task call IDs
	taskInstructions := make(map[string]string) // callID → task param

	for _, block := range msg.OrderedBlocks {
		if block.Type == "tool_call" && block.ToolCall != nil &&
			(block.ToolCall.Name == "Subagent" || block.ToolCall.Name == "Task") {
			taskCalls = append(taskCalls, block.ToolCall.ID)
			if taskParam, ok := block.ToolCall.Parameters["task"].(string); ok {
				taskInstructions[block.ToolCall.ID] = taskParam
			}
		}
	}

	if len(taskCalls) == 0 {
		return ""
	}

	// Count existing sub-agent blocks — each one "claims" a Task call in order.
	existingSubAgents := 0
	for _, block := range msg.OrderedBlocks {
		if block.Type == "sub_agent_activity" && block.SubAgentActivity != nil {
			existingSubAgents++
		}
	}

	// The next unclaimed Task call is at index = existingSubAgents.
	// If all are claimed, return empty.
	if existingSubAgents >= len(taskCalls) {
		return ""
	}

	return taskInstructions[taskCalls[existingSubAgents]]
}

// processSubAgentInnerUpdate dispatches an IntermediateUpdate into the correct
// inner block type (thinking, content, tool_call, tool_result, tool_output_chunk).
//
// Each update is appended to (or merged with) the sub-agent's nested Blocks slice.
// This function handles all update types from the SDK, including streaming chunks.
func processSubAgentInnerUpdate(sa *SubAgentDisplay, update agent.IntermediateUpdate) {
	// HeartbeatUpdate is a pure liveness signal — it must NOT append a block
	// or count as a real event. The renderer uses LastHeartbeat to suppress
	// the "no progress callback — broken" warning when streaming is wired
	// but currently idle (e.g. long thinking block, slow provider).
	if hb, ok := update.(agent.HeartbeatUpdate); ok {
		_ = hb
		sa.LastHeartbeat = time.Now()
		return
	}
	// Anything else counts as a real event for liveness purposes.
	sa.EventCount++

	switch inner := update.(type) {

	// ── Thinking ────────────────────────────────────────────────────────
	case agent.ThinkingUpdate:
		if inner.Append && len(sa.Blocks) > 0 && sa.Blocks[len(sa.Blocks)-1].Type == "thinking" {
			sa.Blocks[len(sa.Blocks)-1].AppendContent(inner.Content)
		} else {
			sa.Blocks = append(sa.Blocks, MessageBlock{
				Type:    "thinking",
				Content: inner.Content,
			})
		}
		logDebug("[SubAgentInner] thinking: agent=%s len=%d", sa.AgentName, len(inner.Content))

	// ── Content ─────────────────────────────────────────────────────────
	case agent.ContentUpdate:
		if inner.Append && len(sa.Blocks) > 0 && sa.Blocks[len(sa.Blocks)-1].Type == "content" {
			sa.Blocks[len(sa.Blocks)-1].AppendContent(inner.Content)
		} else {
			sa.Blocks = append(sa.Blocks, MessageBlock{
				Type:    "content",
				Content: inner.Content,
			})
		}
		logDebug("[SubAgentInner] content: agent=%s len=%d append=%v", sa.AgentName, len(inner.Content), inner.Append)

	// ── Tool Call ───────────────────────────────────────────────────────
	case agent.ToolCallUpdate:
		sa.Blocks = append(sa.Blocks, MessageBlock{
			Type: "tool_call",
			ToolCall: &ToolCallDisplay{
				ID:         inner.ID,
				Name:       inner.Name,
				Parameters: inner.Parameters,
			},
		})
		logDebug("[SubAgentInner] tool_call: agent=%s tool=%s id=%s", sa.AgentName, inner.Name, inner.ID)

	// ── Tool Result (final) ─────────────────────────────────────────────
	case agent.ToolResultUpdate:
		errStr := ""
		if inner.Error != nil {
			errStr = inner.Error.Error()
		}
		// Try to update an existing streaming result for this call ID.
		updated := false
		for i := len(sa.Blocks) - 1; i >= 0; i-- {
			b := &sa.Blocks[i]
			if b.Type == "tool_result" && b.ToolResult != nil && b.ToolResult.CallID == inner.ID {
				b.ToolResult.SetOutput(inner.Output)
				b.ToolResult.Error = errStr
				b.ToolResult.IsStreaming = false
				updated = true
				break
			}
		}
		if !updated {
			sa.Blocks = append(sa.Blocks, MessageBlock{
				Type: "tool_result",
				ToolResult: &ToolResultDisplay{
					CallID: inner.ID,
					Output: inner.Output,
					Error:  errStr,
				},
			})
		}
		logDebug("[SubAgentInner] tool_result: agent=%s id=%s outLen=%d err=%q",
			sa.AgentName, inner.ID, len(inner.Output), errStr)

	// ── Tool Output Chunk (streaming) ───────────────────────────────────
	case agent.ToolOutputChunk:
		// Find existing streaming result or create a new one.
		found := false
		for i := len(sa.Blocks) - 1; i >= 0; i-- {
			b := &sa.Blocks[i]
			if b.Type == "tool_result" && b.ToolResult != nil && b.ToolResult.CallID == inner.ID {
				b.ToolResult.AppendOutput(inner.Chunk)
				found = true
				break
			}
		}
		if !found {
			sa.Blocks = append(sa.Blocks, MessageBlock{
				Type: "tool_result",
				ToolResult: &ToolResultDisplay{
					CallID:      inner.ID,
					Output:      inner.Chunk,
					IsStreaming: true,
				},
			})
		}

	// ── Assistant Message (per-turn finalization) ────────────────────────
	// Mirrors the top-level handler in app_update.go so nested sub-agents
	// also accumulate live token/turn counts. Without this, deep agent
	// hierarchies show "0 tokens" until completion.
	case agent.AssistantMessageUpdate:
		if inner.Turn > sa.TurnCount {
			sa.TurnCount = inner.Turn
		}
		if inner.InputTokens > 0 || inner.OutputTokens > 0 {
			sa.TokenCount = inner.InputTokens + inner.OutputTokens
		}
		logDebug("[SubAgentInner] assistant message: agent=%s turn=%d in=%d out=%d finish=%s",
			sa.AgentName, inner.Turn, inner.InputTokens, inner.OutputTokens, inner.FinishReason)

	// ── Token Count (per-API-call authoritative counts) ───────────────────
	case agent.TokenCountUpdate:
		if inner.Turn > sa.TurnCount {
			sa.TurnCount = inner.Turn
		}
		if inner.InputTokens > 0 || inner.OutputTokens > 0 {
			sa.TokenCount = inner.InputTokens + inner.OutputTokens
		}

	// ── Nested sub-agent (sub-sub-agent) ────────────────────────────────
	case agent.SubAgentUpdate:
		// Recursively handle nested sub-agent updates by creating/finding
		// a nested SubAgentDisplay within this sub-agent's blocks.
		nested := findOrCreateNestedSubAgent(sa, inner.AgentID, inner.AgentName)
		processSubAgentInnerUpdate(nested, inner.Update)
		logDebug("[SubAgentInner] nested sub-agent: parent=%s child=%s (id=%s)",
			sa.AgentName, inner.AgentName, inner.AgentID)

	// ── Completion (terminal event) ──────────────────────────────────────
	case agent.SubAgentCompleteUpdate:
		// Mark this sub-agent as complete so the renderer collapses its
		// transcript into a one-line "Done (…)" summary. Fields are frozen
		// at this moment; no further updates should arrive for this agent.
		sa.IsComplete = true
		sa.EndTime = time.Now()
		if inner.TurnCount > sa.TurnCount {
			sa.TurnCount = inner.TurnCount
		}
		sa.ToolUseCount = inner.ToolUseCount
		if inner.TokensUsed > sa.TokenCount {
			sa.TokenCount = inner.TokensUsed
		}
		logDebug("[SubAgentInner] complete: agent=%s turns=%d tools=%d tokens=%d duration=%s err=%q",
			sa.AgentName, inner.TurnCount, inner.ToolUseCount, inner.TokensUsed, inner.Duration, inner.Error)

	default:
		logDebug("[SubAgentInner] unhandled update type: agent=%s type=%T", sa.AgentName, update)
	}
}

// findOrCreateNestedSubAgent finds or creates a SubAgentDisplay nested within
// another sub-agent's blocks. This supports arbitrarily deep agent hierarchies.
func findOrCreateNestedSubAgent(parent *SubAgentDisplay, agentID, agentName string) *SubAgentDisplay {
	// Search existing nested blocks
	for i := range parent.Blocks {
		b := &parent.Blocks[i]
		if b.Type != "sub_agent_activity" || b.SubAgentActivity == nil {
			continue
		}
		sa := b.SubAgentActivity
		if agentID != "" && sa.AgentID == agentID {
			return sa
		}
		if agentID == "" && sa.AgentID == "" && sa.AgentName == agentName {
			return sa
		}
	}

	// Create new nested block
	nested := &SubAgentDisplay{
		AgentID:   agentID,
		AgentName: agentName,
		Blocks:    make([]MessageBlock, 0, 8),
	}
	parent.Blocks = append(parent.Blocks, MessageBlock{
		Type:             "sub_agent_activity",
		SubAgentActivity: nested,
	})
	logDebug("[SubAgentDispatch] Created nested sub-agent: parent=%s child=%s (id=%s)",
		parent.AgentName, agentName, agentID)
	return nested
}

// truncateForLogSA truncates a string for sub-agent debug log output.
func truncateForLogSA(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
