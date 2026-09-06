package chat

import (
	"testing"
)

// TestInputHistory_WorkspaceIsolation is the regression test for the bug where
// pressing Up/Down in the chat box recalled prompts sent from a *different*
// working directory. History is persisted per-workspace; prompts added under
// one workspace root must never appear when a history is loaded for another.
func TestInputHistory_WorkspaceIsolation(t *testing.T) {
	// Hermetic config dir so we never touch the real ~/.config/swarmos.
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp) // os.UserConfigDir honors this on Linux
	t.Setenv("HOME", tmp)            // fallback on darwin/other

	const wsA = "/home/dev/project-a"
	const wsB = "/home/dev/project-b"

	// Send a prompt in workspace A and force a synchronous flush.
	histA := NewInputHistory(wsA)
	histA.Add("secret prompt from A")
	histA.saveInputHistoryToDisk()

	// A brand-new history for workspace B must NOT see A's prompt.
	histB := NewInputHistory(wsB)
	if got, ok := histB.NavigateUp(""); ok {
		t.Fatalf("workspace B leaked a prompt from workspace A: %q", got)
	}

	// Send a different prompt in B and flush.
	histB.Add("prompt from B")
	histB.saveInputHistoryToDisk()

	// Reloading A must still only see A's prompt (and never B's).
	histA2 := NewInputHistory(wsA)
	got, ok := histA2.NavigateUp("")
	if !ok {
		t.Fatalf("workspace A lost its own persisted prompt")
	}
	if got != "secret prompt from A" {
		t.Fatalf("workspace A got wrong/leaked prompt: %q", got)
	}
	// No second entry should exist in A.
	if _, ok := histA2.NavigateUp(got); ok {
		t.Fatalf("workspace A has more than its single prompt (cross-workspace leak)")
	}
}

// TestInputHistory_DistinctWorkspacesDistinctFiles guards the encoding: two
// different workspace roots must map to two different on-disk paths.
func TestInputHistory_DistinctWorkspacesDistinctFiles(t *testing.T) {
	pathA, err := getInputHistoryPath("/home/dev/project-a")
	if err != nil {
		t.Fatalf("getInputHistoryPath A: %v", err)
	}
	pathB, err := getInputHistoryPath("/home/dev/project-b")
	if err != nil {
		t.Fatalf("getInputHistoryPath B: %v", err)
	}
	if pathA == pathB {
		t.Fatalf("distinct workspaces share a history file: %q", pathA)
	}

	// Empty workspace falls back to the shared default bucket, and must be
	// distinct from any real workspace path.
	pathDefault, err := getInputHistoryPath("")
	if err != nil {
		t.Fatalf("getInputHistoryPath default: %v", err)
	}
	if pathDefault == pathA {
		t.Fatalf("default bucket collides with a real workspace path")
	}
}
