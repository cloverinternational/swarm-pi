package forge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
)

// effectByPath finds the single effect for path, failing the test otherwise.
func effectByPath(t *testing.T, o *toolout.Outcome, path string) toolout.FileEffect {
	t.Helper()
	if o == nil {
		t.Fatal("tool result carried no typed Outcome")
	}
	for _, e := range o.Effects {
		if e.Path == path {
			return e
		}
	}
	t.Fatalf("no effect for %q; got %v", path, o.Paths())
	return toolout.FileEffect{}
}

// TestApplyPatchOutcomeCoversEveryOperationKind asserts that a multi-file
// transaction reports one typed effect per committed change, with the real
// absolute paths and byte counts — the data a consumer would otherwise have
// to recover by parsing the V4A patch envelope out of the tool's parameters.
func TestApplyPatchOutcomeCoversEveryOperationKind(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.txt"), "hello\nworld\n", 0o640)
	mustWriteFile(t, filepath.Join(root, "obsolete.txt"), "old\n", 0o600)

	patch := `*** Begin Patch
*** Update File: a.txt
@@
 hello
-world
+swarm
*** Add File: nested/b.txt
+new
+file
*** Delete File: obsolete.txt
*** End Patch`
	result, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	outcome := result.Outcome
	if outcome == nil {
		t.Fatal("apply_patch reported no typed Outcome")
	}
	if !outcome.Succeeded() {
		t.Fatalf("status = %q, want success", outcome.Status)
	}
	if len(outcome.Effects) != 3 {
		t.Fatalf("effects = %v, want 3", outcome.Paths())
	}

	updated := effectByPath(t, outcome, filepath.Join(root, "a.txt"))
	if updated.Op != toolout.FileOpUpdate {
		t.Fatalf("a.txt op = %q, want %q", updated.Op, toolout.FileOpUpdate)
	}
	if updated.BytesWritten != int64(len("hello\nswarm\n")) {
		t.Fatalf("a.txt bytes = %d, want %d", updated.BytesWritten, len("hello\nswarm\n"))
	}

	added := effectByPath(t, outcome, filepath.Join(root, "nested", "b.txt"))
	if added.Op != toolout.FileOpCreate {
		t.Fatalf("b.txt op = %q, want %q", added.Op, toolout.FileOpCreate)
	}
	if added.BytesWritten != int64(len("new\nfile\n")) {
		t.Fatalf("b.txt bytes = %d, want %d", added.BytesWritten, len("new\nfile\n"))
	}

	deleted := effectByPath(t, outcome, filepath.Join(root, "obsolete.txt"))
	if deleted.Op != toolout.FileOpDelete {
		t.Fatalf("obsolete.txt op = %q, want %q", deleted.Op, toolout.FileOpDelete)
	}
	if deleted.BytesWritten != 0 {
		t.Fatalf("a delete reported %d bytes written, want 0", deleted.BytesWritten)
	}
}

// TestApplyPatchOutcomeRecordsMoveSource asserts a "Move to" reports both
// where the content landed and where it came from. Recording only the
// destination would silently lose the fact that the source path is gone —
// which is exactly the kind of loss this typed outcome exists to prevent.
func TestApplyPatchOutcomeRecordsMoveSource(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "old.txt"), "one\n", 0o644)

	patch := `*** Begin Patch
*** Update File: old.txt
*** Move to: new.txt
@@
-one
+two
*** End Patch`
	result, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome == nil || len(result.Outcome.Effects) != 1 {
		t.Fatalf("expected exactly one effect, got %v", result.Outcome.Paths())
	}
	effect := result.Outcome.Effects[0]
	if effect.Op != toolout.FileOpMove {
		t.Fatalf("op = %q, want %q", effect.Op, toolout.FileOpMove)
	}
	if want := filepath.Join(root, "new.txt"); effect.Path != want {
		t.Fatalf("path = %q, want %q", effect.Path, want)
	}
	if want := filepath.Join(root, "old.txt"); effect.FromPath != want {
		t.Fatalf("from_path = %q, want %q", effect.FromPath, want)
	}
}

// TestEditToolOutcomeReportsRealPath covers the hidden Edit adapter, whose
// file_path parameter is only a *request*: the committed path is resolved and
// canonicalised by the tool, and that resolved path is what the outcome must
// report.
func TestEditToolOutcomeReportsRealPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "edit_me.txt")
	mustWriteFile(t, path, "alpha\nbeta\n", 0o644)

	result, err := NewFSPatch(root).Execute(context.Background(), map[string]any{
		"file_path":  "edit_me.txt",
		"old_string": "beta",
		"new_string": "gamma",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome == nil {
		t.Fatal("Edit reported no typed Outcome")
	}
	paths := result.Outcome.Paths()
	if len(paths) != 1 || paths[0] != path {
		t.Fatalf("paths = %v, want [%s]", paths, path)
	}
	if result.Outcome.Effects[0].Op != toolout.FileOpUpdate {
		t.Fatalf("op = %q, want %q", result.Outcome.Effects[0].Op, toolout.FileOpUpdate)
	}
	if want := int64(len("alpha\ngamma\n")); result.Outcome.BytesWritten() != want {
		t.Fatalf("bytes written = %d, want %d", result.Outcome.BytesWritten(), want)
	}
}
