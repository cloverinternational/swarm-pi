package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestApplyPatchMultiFileTransaction(t *testing.T) {
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
	wantSummary := "Success. Updated the following files:\nA nested/b.txt\nM a.txt\nD obsolete.txt\n"
	if result.Output != wantSummary {
		t.Fatalf("unexpected Codex summary: %q, want %q", result.Output, wantSummary)
	}
	assertFileContent(t, filepath.Join(root, "a.txt"), "hello\nswarm\n")
	assertFileContent(t, filepath.Join(root, "nested", "b.txt"), "new\nfile\n")
	if _, err := os.Stat(filepath.Join(root, "obsolete.txt")); !os.IsNotExist(err) {
		t.Fatalf("obsolete file still exists: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode changed: got %o", info.Mode().Perm())
	}
}

func TestApplyPatchPreservesCRLF(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "windows.txt")
	mustWriteFile(t, path, "one\r\ntwo\r\n", 0o600)
	patch := `*** Begin Patch
*** Update File: windows.txt
@@
 one
-two
+changed
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "one\r\nchanged\r\n")
}

func TestApplyPatchUsesCanonicalRootForSymlinkedWorkspace(t *testing.T) {
	parent := t.TempDir()
	realWorkspace := filepath.Join(parent, "real")
	if err := os.MkdirAll(realWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	workspaceLink := filepath.Join(parent, "workspace-link")
	if err := os.Symlink(realWorkspace, workspaceLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(realWorkspace, "mode.txt")
	mustWriteFile(t, path, "before\n", 0o660)
	if err := os.Chmod(path, 0o660); err != nil {
		t.Fatal(err)
	}
	patch := `*** Begin Patch
*** Update File: mode.txt
@@
-before
+after
*** End Patch`

	if _, err := NewApplyPatchTool(workspaceLink).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "after\n")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("root-anchored atomic write changed mode under umask: got %o, want 660", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(realWorkspace, ".swarm", "snapshots")); err != nil {
		t.Fatalf("snapshots were not rooted in canonical workspace: %v", err)
	}
}

func TestApplyPatchPreflightPreventsPartialMutation(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.txt"), "before\n", 0o600)

	patch := `*** Begin Patch
*** Update File: a.txt
@@
-before
+after
*** Update File: missing.txt
@@
-missing
+changed
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err == nil {
		t.Fatal("expected missing-file error")
	}
	assertFileContent(t, filepath.Join(root, "a.txt"), "before\n")
}

func TestApplyPatchRejectsAmbiguousFuzzyContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "ambiguous.go")
	original := "func a() {\n\treturn\n}\n\nfunc b() {\n\treturn\n}\n"
	mustWriteFile(t, path, original, 0o600)
	patch := `*** Begin Patch
*** Update File: ambiguous.go
@@
-    return
+    panic("changed")
*** End Patch`
	_, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "ambiguous") {
		t.Fatalf("expected ambiguous-context error, got %v", err)
	}
	if !strings.Contains(err.Error(), "lines 2, 6") {
		t.Fatalf("ambiguous error should identify candidate lines, got %v", err)
	}
	assertFileContent(t, path, original)
}

func TestApplyPatchMovesFileAndTracksUndoSnapshots(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "old.txt"), "old\n", 0o600)
	patch := `*** Begin Patch
*** Update File: old.txt
*** Move to: dir/new.txt
@@
-old
+new
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old path still exists: %v", err)
	}
	assertFileContent(t, filepath.Join(root, "dir", "new.txt"), "new\n")
	entries, err := os.ReadDir(filepath.Join(root, ".swarm", "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 4 { // .gitignore plus source/destination snapshot metadata.
		t.Fatalf("expected move snapshots, got %d entries", len(entries))
	}
	snapshotDir := filepath.Join(root, ".swarm", "snapshots")
	dirInfo, err := os.Stat(snapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("snapshot directory mode = %o, want 700", dirInfo.Mode().Perm())
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bak") {
			info, err := entry.Info()
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("snapshot %s mode = %o, want 600", entry.Name(), info.Mode().Perm())
			}
		}
	}
}

func TestFailedLegacyEditDoesNotCreateSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	mustWriteFile(t, path, "actual\n", 0o600)
	_, err := NewFSPatch(root).Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "missing",
		"new_string": "replacement",
	})
	if err == nil {
		t.Fatal("expected exact replacement failure")
	}
	entries, readErr := os.ReadDir(filepath.Join(root, ".swarm", "snapshots"))
	if readErr == nil && len(entries) != 0 {
		t.Fatalf("failed edit created %d snapshot artifacts", len(entries))
	}
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
}

func TestApplyPatchRollsBackEarlierWritesOnCommitFailure(t *testing.T) {
	root := t.TempDir()
	tool := NewApplyPatchTool(root)
	writes := 0
	tool.writeFileFn = func(path, content string, mode os.FileMode) error {
		writes++
		if writes == 2 {
			return errors.New("injected write failure")
		}
		return writeFileAtomic(path, content, mode)
	}
	patch := `*** Begin Patch
*** Add File: first.txt
+first
*** Add File: second.txt
+second
*** End Patch`
	if _, err := tool.Execute(context.Background(), map[string]any{"input": patch}); err == nil {
		t.Fatal("expected injected commit failure")
	}
	for _, name := range []string{"first.txt", "second.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived rollback: %v", name, err)
		}
	}
}

func TestApplyPatchRejectsBackwardEOFHunkWithoutPanic(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	mustWriteFile(t, path, "first\nmiddle\nlast\n", 0o600)
	patch := `*** Begin Patch
*** Update File: file.txt
@@
-last
+done
@@
 first
*** End of File
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err == nil {
		t.Fatal("expected backward EOF hunk rejection")
	}
	assertFileContent(t, path, "first\nmiddle\nlast\n")
}

func TestApplyPatchRejectsSnapshotDirSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".swarm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".swarm", "snapshots")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(root, "secret.txt")
	mustWriteFile(t, path, "safe\n", 0o600)
	patch := `*** Begin Patch
*** Update File: secret.txt
@@
-safe
+edited
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err == nil {
		t.Fatal("expected snapshot symlink escape rejection")
	}
	assertFileContent(t, path, "safe\n")
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("snapshot escaped workspace: %d entries written outside", len(entries))
	}
}

func TestApplyPatchRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "secret.txt"), "safe\n", 0o600)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	patch := `*** Begin Patch
*** Update File: escape/secret.txt
@@
-safe
+escaped
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
	assertFileContent(t, filepath.Join(outside, "secret.txt"), "safe\n")
}

func TestApplyPatchAllowsExplicitlyApprovedOutsidePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	planPath := filepath.Join(outside, "plan.md")
	ctx := tools.WithApprovedPaths(context.Background(), planPath)
	patch := fmt.Sprintf(`*** Begin Patch
*** Add File: %s
+# Approved plan
*** End Patch`, planPath)

	if _, err := NewApplyPatchTool(root).Execute(ctx, map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, planPath, "# Approved plan\n")
}

func TestApplyPatchRejectsIntroducedConflictMarkers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	mustWriteFile(t, path, "safe\n", 0o600)
	patch := `*** Begin Patch
*** Update File: file.txt
@@
-safe
+<<<<<<< ours
+left
+=======
+right
+>>>>>>> theirs
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err == nil {
		t.Fatal("expected conflict marker rejection")
	}
	assertFileContent(t, path, "safe\n")
}

func TestRegisterFileEditingToolsExposesOnlyApplyPatch(t *testing.T) {
	registry := tools.NewRegistry()
	if err := RegisterFileEditingTools(registry, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	visible := registry.List()
	if len(visible) != 1 || visible[0] != "apply_patch" {
		t.Fatalf("visible mutation tools = %v, want [apply_patch]", visible)
	}
	for _, legacy := range []string{"Edit", "Write"} {
		if _, err := registry.Get(legacy); err != nil {
			t.Fatalf("hidden legacy tool %s is not executable: %v", legacy, err)
		}
	}
}

func TestUnifiedMutationSchemaReducesPromptSurface(t *testing.T) {
	root := t.TempDir()
	contractSize := func(tool tools.Tool) int {
		schema, err := json.Marshal(tool.Parameters())
		if err != nil {
			t.Fatal(err)
		}
		return len(tool.Name()) + len(tool.Description()) + len(schema)
	}
	legacy := contractSize(NewFSWrite(root)) + contractSize(NewFSPatch(root)) + contractSize(NewApplyPatchTool(root))
	unified := contractSize(NewApplyPatchTool(root))
	if unified >= legacy {
		t.Fatalf("unified schema grew: unified=%d legacy=%d", unified, legacy)
	}
	t.Logf("mutation tool contract bytes: legacy=%d unified=%d reduction=%.1f%%", legacy, unified, 100*(1-float64(unified)/float64(legacy)))
}

func TestApplyPatchMatchesPinnedCodexEnvelopeAndNewlineSemantics(t *testing.T) {
	root := t.TempDir()
	patch := `<<'EOF'
 *** Begin Patch
  *** Add File: nested/new.txt
+hello
 *** End Patch
EOF`

	result, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Success. Updated the following files:\nA nested/new.txt\n" {
		t.Fatalf("unexpected Codex summary: %q", result.Output)
	}
	assertFileContent(t, filepath.Join(root, "nested", "new.txt"), "hello\n")
}

func TestApplyPatchCodexOverwriteAndRepeatedOperationParity(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "duplicate.txt"), "old content\n", 0o600)
	mustWriteFile(t, filepath.Join(root, "repeat.txt"), "one\ntwo\nthree\n", 0o640)

	patch := `*** Begin Patch
*** Add File: duplicate.txt
+new content
*** Update File: repeat.txt
@@
-one
+ONE
*** Update File: repeat.txt
@@
-three
+THREE
*** End Patch`
	result, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Success. Updated the following files:\nA duplicate.txt\nM repeat.txt\nM repeat.txt\n" {
		t.Fatalf("unexpected Codex summary: %q", result.Output)
	}
	assertFileContent(t, filepath.Join(root, "duplicate.txt"), "new content\n")
	assertFileContent(t, filepath.Join(root, "repeat.txt"), "ONE\ntwo\nTHREE\n")
	info, err := os.Stat(filepath.Join(root, "duplicate.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("overwrite widened mode: got %o", info.Mode().Perm())
	}
}

func TestApplyPatchCodexMoveOverwritesDestinationTransactionally(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "old", "name.txt"), "from\n", 0o640)
	mustWriteFile(t, filepath.Join(root, "renamed", "name.txt"), "existing\n", 0o600)

	patch := `*** Begin Patch
*** Update File: old/name.txt
*** Move to: renamed/name.txt
@@
-from
+new
*** End Patch`
	result, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Success. Updated the following files:\nM renamed/name.txt\n" {
		t.Fatalf("unexpected Codex summary: %q", result.Output)
	}
	if _, err := os.Stat(filepath.Join(root, "old", "name.txt")); !os.IsNotExist(err) {
		t.Fatalf("move source still exists: %v", err)
	}
	assertFileContent(t, filepath.Join(root, "renamed", "name.txt"), "new\n")
}

func TestApplyPatchCodexUnicodeFuzzAndFinalNewline(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "typography.txt")
	mustWriteFile(t, path, "quote: “hello”", 0o600)

	patch := `*** Begin Patch
*** Update File: typography.txt
@@
-quote: "hello"
+quote: "updated"
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "quote: \"updated\"\n")
}

func TestApplyPatchAllowsSeparatorWithoutCompleteConflictBlock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.tex")
	mustWriteFile(t, path, "before\n", 0o600)
	separator := strings.Repeat("=", 7)
	patch := fmt.Sprintf(`*** Begin Patch
*** Update File: report.tex
@@
-before
+before
+%s
+after
*** End Patch`, separator)

	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "before\n"+separator+"\nafter\n")
}

func TestApplyPatchRollbackRestoresOverwrittenFile(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "existing.txt")
	mustWriteFile(t, original, "original\n", 0o600)
	tool := NewApplyPatchTool(root)
	writes := 0
	tool.writeFileFn = func(path, content string, mode os.FileMode) error {
		writes++
		if writes == 2 {
			return errors.New("injected second write failure")
		}
		return writeFileAtomic(path, content, mode)
	}
	patch := `*** Begin Patch
*** Add File: existing.txt
+overwritten
*** Add File: second.txt
+second
*** End Patch`

	if _, err := tool.Execute(context.Background(), map[string]any{"input": patch}); err == nil {
		t.Fatal("expected injected commit failure")
	}
	assertFileContent(t, original, "original\n")
	if _, err := os.Stat(filepath.Join(root, "second.txt")); !os.IsNotExist(err) {
		t.Fatalf("second file survived failed transaction: %v", err)
	}
}

func TestApplyPatchRepeatedOperationsCreateOneUndoBoundaryPerPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "repeat.txt")
	mustWriteFile(t, path, "one\ntwo\n", 0o600)
	patch := `*** Begin Patch
*** Update File: repeat.txt
@@
-one
+ONE
*** Update File: repeat.txt
@@
-two
+TWO
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(root, ".swarm", "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	backups := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bak") {
			backups++
		}
	}
	if backups != 1 {
		t.Fatalf("repeated operations created %d backups, want one transaction-level undo boundary", backups)
	}
}

func mustWriteFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}

func TestApplyPatchAnchorCanReplaceAnchorLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "anchor.go")
	mustWriteFile(t, path, "func first() {}\n\nfunc target() {\n\told()\n}\n", 0o600)
	patch := `*** Begin Patch
*** Update File: anchor.go
@@ func target()
-func target() {
+func renamed() {
` + " \told()\n" + ` }
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "func first() {}\n\nfunc renamed() {\n\told()\n}\n")
}

func TestApplyPatchSupportsUniqueMultilineAnchor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "multiline.go")
	mustWriteFile(t, path, "type First struct{}\nfunc configure() { old() }\n\ntype Widget struct{}\nfunc configure() { old() }\n", 0o600)
	patch := `*** Begin Patch
*** Update File: multiline.go
@@ type Widget struct{}\nfunc configure()
 type Widget struct{}
-func configure() { old() }
+func configure() { newValue() }
*** End Patch`
	if _, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "type First struct{}\nfunc configure() { old() }\n\ntype Widget struct{}\nfunc configure() { newValue() }\n")
}

func TestApplyPatchRejectsAmbiguousPartialLineAnchor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "partial.go")
	original := "func alphaHandler() {}\nfunc betaHandler() {}\n"
	mustWriteFile(t, path, original, 0o600)
	patch := `*** Begin Patch
*** Update File: partial.go
@@ Handler
-func alphaHandler() {}
+func changed() {}
*** End Patch`
	_, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch})
	if err == nil || !strings.Contains(err.Error(), "ambiguous @@ anchor at lines 1, 2") {
		t.Fatalf("expected candidate-line ambiguity, got %v", err)
	}
	assertFileContent(t, path, original)
}
