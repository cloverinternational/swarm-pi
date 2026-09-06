package repair_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/repair"
)

// helpers

func userMsg(content string) *conversation.Message {
	return &conversation.Message{
		ID:        "u1",
		Timestamp: time.Now(),
		Role:      conversation.RoleUser,
		Content:   content,
	}
}

func assistantMsg(content string, calls ...conversation.ToolCall) *conversation.Message {
	return &conversation.Message{
		ID:        "a1",
		Timestamp: time.Now(),
		Role:      conversation.RoleAssistant,
		Content:   content,
		ToolCalls: calls,
	}
}

func toolResultMsg(results ...conversation.ToolResult) *conversation.Message {
	return &conversation.Message{
		ID:          "t1",
		Timestamp:   time.Now(),
		Role:        conversation.RoleTool,
		ToolResults: results,
	}
}

func toolCall(id, name string) conversation.ToolCall {
	return conversation.ToolCall{ID: id, Name: name}
}

func toolResult(callID string) conversation.ToolResult {
	return conversation.ToolResult{CallID: callID, Output: "ok"}
}

// ── Pass 1: orphaned leading messages ────────────────────────────────────────

func TestRepairHistory_NoOrphanedLeading_Unchanged(t *testing.T) {
	msgs := []*conversation.Message{userMsg("hello"), assistantMsg("hi")}
	out, report := repair.RepairHistory(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if report.LeadingMessagesDropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", report.LeadingMessagesDropped)
	}
}

func TestRepairHistory_DropsOrphanedLeadingAssistant(t *testing.T) {
	msgs := []*conversation.Message{
		assistantMsg("stray"),
		userMsg("hello"),
		assistantMsg("hi"),
	}
	out, report := repair.RepairHistory(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages after strip, got %d", len(out))
	}
	if out[0].Role != conversation.RoleUser {
		t.Fatalf("expected first message to be user, got %s", out[0].Role)
	}
	if report.LeadingMessagesDropped != 1 {
		t.Fatalf("expected 1 dropped, got %d", report.LeadingMessagesDropped)
	}
}

func TestRepairHistory_PreservesSystemLeading(t *testing.T) {
	sys := &conversation.Message{Role: conversation.RoleSystem, Content: "you are helpful"}
	msgs := []*conversation.Message{sys, userMsg("hi")}
	out, report := repair.RepairHistory(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if report.LeadingMessagesDropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", report.LeadingMessagesDropped)
	}
}

func TestRepairHistory_EmptySlice(t *testing.T) {
	out, report := repair.RepairHistory(nil)
	if len(out) != 0 {
		t.Fatalf("expected empty slice, got %d", len(out))
	}
	if report.LeadingMessagesDropped != 0 || report.OrphanedToolCallsFixed != 0 {
		t.Fatalf("expected zero report on empty slice")
	}
}

// ── Pass 2: orphaned tool calls ───────────────────────────────────────────────

func TestRepairHistory_NoOrphanedToolCalls_Unchanged(t *testing.T) {
	tc := toolCall("id1", "bash")
	msgs := []*conversation.Message{
		userMsg("run it"),
		assistantMsg("ok", tc),
		toolResultMsg(toolResult("id1")),
		assistantMsg("done"),
	}
	out, report := repair.RepairHistory(msgs)
	if len(out) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(out))
	}
	if report.OrphanedToolCallsFixed != 0 {
		t.Fatalf("expected 0 fixed, got %d", report.OrphanedToolCallsFixed)
	}
}

func TestRepairHistory_SynthesizesOrphanedToolCall(t *testing.T) {
	tc := toolCall("id1", "bash")
	msgs := []*conversation.Message{
		userMsg("run it"),
		assistantMsg("ok", tc),
		// no tool result message — connection dropped
	}
	out, report := repair.RepairHistory(msgs)
	// should insert synthetic tool result after the assistant message
	if len(out) != 3 {
		t.Fatalf("expected 3 messages (user+assistant+synthetic), got %d", len(out))
	}
	if out[2].Role != conversation.RoleTool {
		t.Fatalf("expected synthetic message role=tool, got %s", out[2].Role)
	}
	if len(out[2].ToolResults) != 1 {
		t.Fatalf("expected 1 synthetic result, got %d", len(out[2].ToolResults))
	}
	if out[2].ToolResults[0].CallID != "id1" {
		t.Fatalf("expected CallID=id1, got %s", out[2].ToolResults[0].CallID)
	}
	if !strings.Contains(out[2].ToolResults[0].Output, "interrupted") {
		t.Fatalf("expected error output mentioning 'interrupted', got: %s", out[2].ToolResults[0].Output)
	}
	if report.OrphanedToolCallsFixed != 1 {
		t.Fatalf("expected 1 fixed, got %d", report.OrphanedToolCallsFixed)
	}
}

func TestRepairHistory_AppendsToExistingToolResultMsg(t *testing.T) {
	tc1 := toolCall("id1", "bash")
	tc2 := toolCall("id2", "read_file")
	// id2 has a result but id1 is orphaned — next msg already has some results
	msgs := []*conversation.Message{
		userMsg("run"),
		assistantMsg("ok", tc1, tc2),
		toolResultMsg(toolResult("id2")), // id1 missing
	}
	out, report := repair.RepairHistory(msgs)
	// no new message inserted; synthetic result appended to existing tool msg
	if len(out) != 3 {
		t.Fatalf("expected 3 messages (no new insertion), got %d", len(out))
	}
	if len(out[2].ToolResults) != 2 {
		t.Fatalf("expected 2 tool results (1 real + 1 synthetic), got %d", len(out[2].ToolResults))
	}
	if report.OrphanedToolCallsFixed != 1 {
		t.Fatalf("expected 1 fixed, got %d", report.OrphanedToolCallsFixed)
	}
}

func TestRepairHistory_MultipleOrphansAcrossMessages(t *testing.T) {
	tc1 := toolCall("id1", "bash")
	tc2 := toolCall("id2", "read_file")
	msgs := []*conversation.Message{
		userMsg("run"),
		assistantMsg("first call", tc1),
		// no result for tc1
		assistantMsg("second call", tc2),
		// no result for tc2
	}
	out, report := repair.RepairHistory(msgs)
	// two synthetic messages should be inserted (one after each assistant msg):
	// user + assistant1 + synthetic1 + assistant2 + synthetic2 = 5
	if len(out) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(out))
	}
	if report.OrphanedToolCallsFixed != 2 {
		t.Fatalf("expected 2 fixed, got %d", report.OrphanedToolCallsFixed)
	}
}
