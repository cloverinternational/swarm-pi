package forge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestFSUndoWalksBackThroughMultipleSnapshots proves issue #238: calling
// Undo repeatedly on a file with several snapshots must restore a DIFFERENT,
// progressively earlier state each time -- not keep re-restoring the same
// "latest" snapshot forever. The bug was that FSUndo picked the most recent
// .bak by embedded timestamp but never retired it, so a second call found
// the identical snapshot again and reported "Successfully reverted" while
// file bytes stayed unchanged.
func TestFSUndoWalksBackThroughMultipleSnapshots(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "file.txt")
	snapshotDir := filepath.Join(root, ".swarm", "snapshots")

	// v1 -> v2 -> v3, each transition snapshotting the PRIOR content via
	// apply_patch (the real production snapshot writer), so this test
	// exercises the exact snapshot format FSUndo consumes.
	mustWriteFile(t, target, "v1\n", 0o600)
	tool := NewApplyPatchTool(root)
	patchTo := func(from, to string) {
		patch := "*** Begin Patch\n*** Update File: file.txt\n@@\n-" + from + "\n+" + to + "\n*** End Patch"
		if _, err := tool.Execute(context.Background(), map[string]any{"input": patch}); err != nil {
			t.Fatalf("apply_patch %s->%s: %v", from, to, err)
		}
	}
	patchTo("v1", "v2")
	patchTo("v2", "v3")
	assertFileContent(t, target, "v3\n")

	undo := NewFSUndo(root, snapshotDir)

	// First Undo: v3 -> v2.
	if _, err := undo.Execute(context.Background(), map[string]any{"path": target}); err != nil {
		t.Fatalf("first Undo failed: %v", err)
	}
	assertFileContent(t, target, "v2\n")

	// Second Undo: v2 -> v1. This is the exact regression -- before the fix
	// this restored "v2\n" again (the same snapshot as the first call).
	if _, err := undo.Execute(context.Background(), map[string]any{"path": target}); err != nil {
		t.Fatalf("second Undo failed: %v", err)
	}
	assertFileContent(t, target, "v1\n")

	// Third Undo: no earlier snapshot exists for this file -- must fail
	// cleanly rather than claim another byte-changing success.
	if _, err := undo.Execute(context.Background(), map[string]any{"path": target}); err == nil {
		t.Fatal("expected fs_undo.no_snapshot once history is exhausted, got success")
	}
	assertFileContent(t, target, "v1\n")
}

// TestFSUndoSingleSnapshotSecondCallFails covers the near-miss from #238's
// acceptance criteria: with exactly one snapshot, the first Undo restores it
// and a second call must fail rather than silently no-op with a success
// message.
func TestFSUndoSingleSnapshotSecondCallFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "only.txt")
	snapshotDir := filepath.Join(root, ".swarm", "snapshots")

	mustWriteFile(t, target, "before\n", 0o600)
	tool := NewApplyPatchTool(root)
	patch := "*** Begin Patch\n*** Update File: only.txt\n@@\n-before\n+after\n*** End Patch"
	if _, err := tool.Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatalf("apply_patch: %v", err)
	}
	assertFileContent(t, target, "after\n")

	undo := NewFSUndo(root, snapshotDir)
	if _, err := undo.Execute(context.Background(), map[string]any{"path": target}); err != nil {
		t.Fatalf("first Undo failed: %v", err)
	}
	assertFileContent(t, target, "before\n")

	if _, err := undo.Execute(context.Background(), map[string]any{"path": target}); err == nil {
		t.Fatal("expected second Undo to fail with no earlier snapshot")
	}
	assertFileContent(t, target, "before\n")
}

// TestFSUndoConsumesNewFileSnapshot proves the "delete a newly created file"
// branch also retires its snapshot marker, so a second Undo on that same
// path reports no-snapshot rather than re-attempting the delete.
func TestFSUndoConsumesNewFileSnapshot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "brand_new.txt")
	snapshotDir := filepath.Join(root, ".swarm", "snapshots")

	tool := NewApplyPatchTool(root)
	patch := "*** Begin Patch\n*** Add File: brand_new.txt\n+hello\n*** End Patch"
	if _, err := tool.Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatalf("apply_patch add: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected new file to exist: %v", err)
	}

	undo := NewFSUndo(root, snapshotDir)
	if _, err := undo.Execute(context.Background(), map[string]any{"path": target}); err != nil {
		t.Fatalf("first Undo failed: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, stat err = %v", err)
	}

	if _, err := undo.Execute(context.Background(), map[string]any{"path": target}); err == nil {
		t.Fatal("expected second Undo to fail with no earlier snapshot")
	}
}
