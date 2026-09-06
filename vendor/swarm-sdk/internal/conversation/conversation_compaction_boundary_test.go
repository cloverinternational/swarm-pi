package conversation

import (
	"encoding/json"
	"testing"
	"time"
)

func boundaryMessage(id string, role Role) *Message {
	return &Message{ID: id, Role: role, Content: id, Timestamp: time.Now()}
}

func TestActiveMessagesLegacyConversationUsesFullTranscript(t *testing.T) {
	conv := &Conversation{
		Messages: []*Message{
			boundaryMessage("user-1", RoleUser),
			boundaryMessage("assistant-1", RoleAssistant),
		},
	}

	active := conv.ActiveMessages()
	if len(active) != 2 || active[0].ID != "user-1" {
		t.Fatalf("legacy active messages = %#v, want full transcript", active)
	}
}

func TestAdvanceActiveContextRetainsTranscriptAndMovesBoundary(t *testing.T) {
	conv := &Conversation{
		Messages: []*Message{
			boundaryMessage("user-1", RoleUser),
			boundaryMessage("assistant-1", RoleAssistant),
		},
		CurrentContextSize: 42_000,
	}
	summary := boundaryMessage("summary-1", RoleUser)
	recovery := boundaryMessage("recovery-1", RoleUser)

	conv.AdvanceActiveContext([]*Message{summary, recovery}, "summary", 1_200)

	if conv.ID != "" {
		t.Fatalf("compaction unexpectedly changed conversation ID to %q", conv.ID)
	}
	if len(conv.Messages) != 4 {
		t.Fatalf("full transcript has %d messages, want 4", len(conv.Messages))
	}
	if got := conv.ActiveMessages(); len(got) != 2 || got[0].ID != "summary-1" {
		t.Fatalf("active messages = %#v, want compacted generation", got)
	}
	if conv.CompactionState.ActiveContextStart != 2 || conv.CompactionState.Generation != 1 {
		t.Fatalf("compaction state = %+v", conv.CompactionState)
	}
	if conv.CurrentContextSize != 1_200 {
		t.Fatalf("context size = %d, want 1200", conv.CurrentContextSize)
	}
	if !conv.CurrentContextSizeEstimated {
		t.Fatal("post-compaction context size should be marked estimated")
	}
	conv.UpdateContextSize(900)
	if conv.CurrentContextSize != 900 || conv.CurrentContextSizeEstimated {
		t.Fatalf("authoritative update did not clear estimate: size=%d estimated=%v",
			conv.CurrentContextSize, conv.CurrentContextSizeEstimated)
	}
}

func TestReplaceActiveMessagesPreservesArchive(t *testing.T) {
	archive := boundaryMessage("archived", RoleAssistant)
	summary := boundaryMessage("summary", RoleUser)
	conv := &Conversation{
		Messages: []*Message{archive, summary},
		CompactionState: &CompactionState{
			ActiveContextStart: 1,
			Generation:         1,
		},
	}
	repair := boundaryMessage("repair", RoleTool)

	conv.ReplaceActiveMessages([]*Message{summary, repair})

	if len(conv.Messages) != 3 || conv.Messages[0] != archive {
		t.Fatalf("archive was not preserved: %#v", conv.Messages)
	}
	if got := conv.ActiveMessages(); len(got) != 2 || got[1].ID != "repair" {
		t.Fatalf("active repair not spliced: %#v", got)
	}
}

func TestActiveContextBoundaryJSONRoundTrip(t *testing.T) {
	conv := &Conversation{
		Messages: []*Message{
			boundaryMessage("archived", RoleUser),
			boundaryMessage("summary", RoleUser),
		},
		CompactionState: &CompactionState{
			ActiveContextStart: 1,
			Generation:         3,
		},
	}

	data, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}
	var restored Conversation
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.ActiveContextStartIndex() != 1 || restored.CompactionState.Generation != 3 {
		t.Fatalf("restored boundary = %+v", restored.CompactionState)
	}
	if got := restored.ActiveMessages(); len(got) != 1 || got[0].ID != "summary" {
		t.Fatalf("restored active messages = %#v", got)
	}
}

func TestCorruptActiveContextBoundaryFailsClosed(t *testing.T) {
	conv := &Conversation{
		Messages: []*Message{boundaryMessage("archived", RoleUser)},
		CompactionState: &CompactionState{
			ActiveContextStart: 99,
		},
	}
	if got := conv.ActiveMessages(); len(got) != 0 {
		t.Fatalf("corrupt boundary exposed %d archived messages", len(got))
	}
}
