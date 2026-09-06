package conversation

import (
	"testing"
)

// TestAddEditWithPromptID_AppendsToLedger asserts the canonical behaviour
// of an edit that produces a different prompt id: the ledger gains an
// entry, the Edit record links the transition, and the message's
// canonical id is the new one.
func TestAddEditWithPromptID_AppendsToLedger(t *testing.T) {
	m := &Message{
		ID:        "msg-1",
		Role:      RoleUser,
		Content:   "first version",
		PromptIDs: []string{"conv-x:aaaaaaaaaaaa"},
	}

	// Simulate caller: capture previous content, swap, mint id, record.
	prev := m.Content
	m.Content = "second version"
	m.AddEditWithPromptID("user", "fixed typo", prev, "conv-x:bbbbbbbbbbbb")

	if got := m.CanonicalPromptID(); got != "conv-x:bbbbbbbbbbbb" {
		t.Fatalf("canonical prompt id should be the new one, got %q", got)
	}
	if len(m.PromptIDs) != 2 {
		t.Fatalf("expected ledger length 2, got %d", len(m.PromptIDs))
	}
	if m.PromptIDs[0] != "conv-x:aaaaaaaaaaaa" {
		t.Fatalf("original id should remain at index 0, got %q", m.PromptIDs[0])
	}

	if len(m.EditHistory) != 1 {
		t.Fatalf("expected one edit record, got %d", len(m.EditHistory))
	}
	e := m.EditHistory[0]
	if e.PreviousPromptID != "conv-x:aaaaaaaaaaaa" {
		t.Errorf("Edit.PreviousPromptID wrong: %q", e.PreviousPromptID)
	}
	if e.NewPromptID != "conv-x:bbbbbbbbbbbb" {
		t.Errorf("Edit.NewPromptID wrong: %q", e.NewPromptID)
	}
	if e.PreviousContent != "first version" {
		t.Errorf("Edit.PreviousContent should snapshot pre-edit text, got %q", e.PreviousContent)
	}
}

// TestAddEditWithPromptID_NoOpWhenIDMatches asserts that an edit whose
// canonicalised content produces the SAME prompt id (e.g. the user only
// adjusted whitespace) records the Edit but does not pollute the ledger
// with a duplicate.
func TestAddEditWithPromptID_NoOpWhenIDMatches(t *testing.T) {
	m := &Message{
		ID:        "msg-1",
		Role:      RoleUser,
		Content:   "do the thing",
		PromptIDs: []string{"conv-x:aaaaaaaaaaaa"},
	}

	prev := m.Content
	m.Content = "  do the thing  " // canonicalisation collapses whitespace
	m.AddEditWithPromptID("user", "whitespace cleanup", prev, "conv-x:aaaaaaaaaaaa")

	if len(m.PromptIDs) != 1 {
		t.Fatalf("expected ledger unchanged when id matches, got len %d", len(m.PromptIDs))
	}
	if len(m.EditHistory) != 1 {
		t.Fatalf("Edit should still be recorded, got %d", len(m.EditHistory))
	}
}

// TestAddEditWithPromptID_FreshLedger covers the case where a message has
// no prior PromptIDs (e.g. a legacy message getting its first id stamped
// post-hoc via an edit). Previous-id is empty; new-id appends as the
// first entry.
func TestAddEditWithPromptID_FreshLedger(t *testing.T) {
	m := &Message{
		ID:      "msg-legacy",
		Role:    RoleUser,
		Content: "legacy content",
	}

	m.AddEditWithPromptID("system", "backfill prompt id", m.Content, "conv-x:newminted000")

	if got := m.CanonicalPromptID(); got != "conv-x:newminted000" {
		t.Fatalf("expected first ledger entry to become canonical, got %q", got)
	}
	if len(m.EditHistory) != 1 {
		t.Fatalf("expected one edit record, got %d", len(m.EditHistory))
	}
	if m.EditHistory[0].PreviousPromptID != "" {
		t.Errorf("legacy backfill should report PreviousPromptID empty, got %q",
			m.EditHistory[0].PreviousPromptID)
	}
}

// TestClone_PreservesPromptIDs asserts the deep-copy invariant: the
// clone has its own PromptIDs slice (not aliased with the source).
func TestClone_PreservesPromptIDs(t *testing.T) {
	m := &Message{
		ID:        "msg-1",
		Role:      RoleUser,
		Content:   "x",
		PromptIDs: []string{"conv-x:aaaaaaaaaaaa", "conv-x:bbbbbbbbbbbb"},
	}

	clone := m.Clone()
	if len(clone.PromptIDs) != 2 {
		t.Fatalf("clone lost PromptIDs entries, got %d", len(clone.PromptIDs))
	}
	clone.PromptIDs[0] = "MUTATED"
	if m.PromptIDs[0] != "conv-x:aaaaaaaaaaaa" {
		t.Fatalf("clone should not alias source slice, source mutated to %q", m.PromptIDs[0])
	}
}

// TestCanonicalPromptID_EmptyLedger returns "" rather than panicking for
// messages that were never minted (assistant/tool messages, legacy data).
func TestCanonicalPromptID_EmptyLedger(t *testing.T) {
	m := &Message{ID: "msg-1", Role: RoleAssistant}
	if got := m.CanonicalPromptID(); got != "" {
		t.Fatalf("empty ledger should return \"\", got %q", got)
	}
}
