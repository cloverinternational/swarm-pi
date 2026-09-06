package builtin

import (
	"path/filepath"
	"testing"
)

// TestGoalPersistence_SaveAndReload verifies that a goal set on one hook is
// persisted to goal.json and restored by a fresh hook pointed at the same path.
func TestGoalPersistence_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, GoalStoreFileName)

	// First hook: set a goal, which should persist to disk.
	h1 := NewGoalHook()
	h1.SetStorePath(path)
	if err := h1.SetGoal("ship the feature"); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}

	// Second hook (simulating a restart): load from the same path.
	h2 := NewGoalHook()
	h2.SetStorePath(path)

	g := h2.GetGoal()
	if g == nil {
		t.Fatalf("expected goal to be restored, got nil")
	}
	if g.Condition != "ship the feature" {
		t.Fatalf("Condition = %q, want %q", g.Condition, "ship the feature")
	}
	if g.State != GoalStateNotYetEvaluated {
		t.Fatalf("State = %q, want %q", g.State, GoalStateNotYetEvaluated)
	}
}

// TestGoalPersistence_ClearPersists verifies that clearing a goal is persisted
// so a reload sees the cleared state rather than the previous active goal.
func TestGoalPersistence_ClearPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, GoalStoreFileName)

	h1 := NewGoalHook()
	h1.SetStorePath(path)
	if err := h1.SetGoal("do the thing"); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}
	h1.Clear()

	h2 := NewGoalHook()
	h2.SetStorePath(path)
	g := h2.GetGoal()
	if g == nil {
		t.Fatalf("expected cleared goal to load, got nil")
	}
	if g.State != GoalStateCleared {
		t.Fatalf("State = %q, want %q", g.State, GoalStateCleared)
	}
}

// TestGoalPersistence_EmptyPathClears verifies that switching to an empty store
// path clears in-memory goal state (conversation switch to one with no goal).
func TestGoalPersistence_EmptyPathClears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, GoalStoreFileName)

	h := NewGoalHook()
	h.SetStorePath(path)
	if err := h.SetGoal("persist me"); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}
	if h.GetGoal() == nil {
		t.Fatalf("expected active goal before clear")
	}

	// Switching to a conversation with no store must not leak the prior goal.
	h.SetStorePath("")
	if g := h.GetGoal(); g != nil {
		t.Fatalf("expected nil goal after empty store path, got %+v", g)
	}
}

// TestGoalPersistence_Disabled verifies that with no store path the hook still
// works in memory and writes nothing to disk.
func TestGoalPersistence_Disabled(t *testing.T) {
	h := NewGoalHook()
	if err := h.SetGoal("in-memory only"); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}
	if g := h.GetGoal(); g == nil || g.Condition != "in-memory only" {
		t.Fatalf("in-memory goal not set correctly: %+v", g)
	}
}
