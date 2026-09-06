package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestUniqueSubAgentID_WithToolCallID(t *testing.T) {
	t.Parallel()

	ctx := tools.WithToolCallID(context.Background(), "call_abc123")
	id := uniqueSubAgentID(ctx, "research")

	if !strings.HasPrefix(id, "research-") {
		t.Errorf("expected prefix 'research-', got %q", id)
	}
	if !strings.Contains(id, "call_abc123") {
		t.Errorf("expected ToolCallID in ID, got %q", id)
	}
}

func TestUniqueSubAgentID_WithoutToolCallID(t *testing.T) {
	t.Parallel()

	ctx := context.Background() // No ToolCallID
	id := uniqueSubAgentID(ctx, "code_formatter")

	if !strings.HasPrefix(id, "code_formatter-") {
		t.Errorf("expected prefix 'code_formatter-', got %q", id)
	}
	// Should contain a UUID fragment (8 hex chars after the prefix)
	suffix := strings.TrimPrefix(id, "code_formatter-")
	if len(suffix) < 8 {
		t.Errorf("UUID fallback too short: %q", suffix)
	}
}

func TestUniqueSubAgentID_Uniqueness(t *testing.T) {
	t.Parallel()

	// Even without ToolCallID, every call should produce a unique ID.
	ctx := context.Background()
	ids := make(map[string]bool)
	for i := range 100 {
		id := uniqueSubAgentID(ctx, "research")
		if ids[id] {
			t.Errorf("duplicate ID on iteration %d: %q", i, id)
		}
		ids[id] = true
	}
}

func TestUniqueSubAgentID_ParallelUniqueness(t *testing.T) {
	t.Parallel()

	// Two calls with same prefix and NO ToolCallID must produce different IDs.
	ctx := context.Background()
	id1 := uniqueSubAgentID(ctx, "research")
	id2 := uniqueSubAgentID(ctx, "research")

	if id1 == id2 {
		t.Errorf("BUG: both calls produced same ID %q — parallel sub-agents would merge in TUI", id1)
	}
}
