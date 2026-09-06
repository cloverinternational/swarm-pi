// Package repair provides history repair utilities for conversation messages.
//
// It corrects two classes of structural invariant violations that arise when
// connections drop or tool execution is interrupted mid-turn:
//
//  1. Orphaned leading messages — the first message in the slice is not a
//     user or system message (API contract violation for most providers).
//
//  2. Orphaned tool calls — an assistant message contains tool_use blocks
//     whose tool_result counterparts are absent from subsequent messages.
//
// Both repairs are pure functions: they accept a slice, return a new slice,
// and carry all diagnostic information in a RepairReport.  No I/O, no
// logging, no side-effects.  Callers may discard the report when unneeded.
package repair

import (
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// RepairReport summarises the mutations performed by RepairHistory.
type RepairReport struct {
	// LeadingMessagesDropped is the number of orphaned non-user/system messages
	// removed from the front of the slice.
	LeadingMessagesDropped int

	// OrphanedToolCallsFixed is the number of tool_use blocks for which a
	// synthetic tool_result was synthesised.
	OrphanedToolCallsFixed int
}

// RepairHistory runs all repair passes over msgs and returns the corrected
// slice together with a RepairReport describing every mutation.
//
// The input slice is never modified in place; a new slice is returned.
// If no repairs are needed the original slice is returned unchanged and
// Report fields are zero.
func RepairHistory(msgs []*conversation.Message) ([]*conversation.Message, RepairReport) {
	var report RepairReport

	msgs, dropped := repairLeadingOrphanedMessages(msgs)
	report.LeadingMessagesDropped = dropped

	msgs, fixed := repairOrphanedToolCalls(msgs)
	report.OrphanedToolCallsFixed = fixed

	return msgs, report
}

// repairLeadingOrphanedMessages strips non-user/system messages from the
// front of the slice.  The Anthropic (and most provider) API contract
// requires the first message to have role "user" or "system".
//
// Returns the (possibly truncated) slice and the number of messages dropped.
func repairLeadingOrphanedMessages(msgs []*conversation.Message) ([]*conversation.Message, int) {
	dropped := 0
	for len(msgs) > 0 &&
		msgs[0].Role != conversation.RoleUser &&
		msgs[0].Role != conversation.RoleSystem {
		msgs = msgs[1:]
		dropped++
	}
	return msgs, dropped
}

// repairOrphanedToolCalls ensures every tool_use block in an assistant
// message has a matching tool_result somewhere later in the slice.
//
// For each orphaned tool call it synthesises an error ToolResult and inserts
// it immediately after the assistant message that issued the tool call.  If
// the next message already carries tool results (partial repair scenario) the
// synthetic results are appended to that message rather than creating a new one.
//
// Returns the repaired slice and the total number of orphaned calls fixed.
func repairOrphanedToolCalls(msgs []*conversation.Message) ([]*conversation.Message, int) {
	if len(msgs) == 0 {
		return msgs, 0
	}

	// Build a set of all tool_result call IDs present in the slice.
	toolResultIDs := make(map[string]bool, len(msgs))
	for _, msg := range msgs {
		for _, tr := range msg.ToolResults {
			toolResultIDs[tr.CallID] = true
		}
	}

	// Identify orphaned tool calls and the index of the assistant message
	// that contains them.
	type orphan struct {
		msgIndex int
		call     conversation.ToolCall
	}
	var orphans []orphan

	for i, msg := range msgs {
		if msg.Role != conversation.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if !toolResultIDs[tc.ID] {
				orphans = append(orphans, orphan{msgIndex: i, call: tc})
			}
		}
	}

	if len(orphans) == 0 {
		return msgs, 0
	}

	// Group by message index so we can insert one synthetic message per
	// assistant message.
	byMsgIndex := make(map[int][]conversation.ToolCall, len(orphans))
	for _, o := range orphans {
		byMsgIndex[o.msgIndex] = append(byMsgIndex[o.msgIndex], o.call)
	}

	// Rebuild the slice, inserting synthetic tool-result messages as needed.
	repaired := make([]*conversation.Message, 0, len(msgs)+len(byMsgIndex))
	for i, msg := range msgs {
		repaired = append(repaired, msg)

		orphanedCalls, ok := byMsgIndex[i]
		if !ok {
			continue
		}

		// If the next existing message already has tool results, append to it.
		if i+1 < len(msgs) && len(msgs[i+1].ToolResults) > 0 {
			for _, tc := range orphanedCalls {
				msgs[i+1].ToolResults = append(msgs[i+1].ToolResults, syntheticResult(tc))
			}
			continue
		}

		// Otherwise create a new tool-result message.
		var results []conversation.ToolResult
		for _, tc := range orphanedCalls {
			results = append(results, syntheticResult(tc))
		}
		repaired = append(repaired, &conversation.Message{
			ID:          fmt.Sprintf("repair_%d", time.Now().UnixNano()),
			Timestamp:   time.Now(),
			Role:        conversation.RoleTool,
			Content:     "",
			ToolResults: results,
		})
	}

	return repaired, len(orphans)
}

// syntheticResult builds an error ToolResult for a tool call that was
// interrupted before its result was recorded.
func syntheticResult(tc conversation.ToolCall) conversation.ToolResult {
	return conversation.ToolResult{
		CallID: tc.ID,
		Name:   tc.Name, // required by Gemini API (function_response.name)
		Output: fmt.Sprintf(
			"Error: Tool call '%s' failed or was interrupted. "+
				"The connection may have dropped or the tool execution was not completed.",
			tc.Name,
		),
		Error: &conversation.ToolError{
			Type:    "tool.interrupted",
			Message: "Tool call was interrupted or failed to complete",
		},
	}
}
