package forge

import (
	"context"
	"path/filepath"
	"testing"
)

// TestApplyPatchOutcome_PopulatesBlobHashesFromInMemoryContent asserts
// committedEffects fills PreBlobSHA1/PostBlobSHA1 from the same
// oldContent/newContent strings it already uses for BytesWritten — never a
// fresh read of the file this tool just wrote. The fixture hash below was
// produced by real `git hash-object` (see internal/bench/effect_test.go's
// fixture comment for the exact commands: `printf 'hello world\n' > f.txt &&
// git hash-object f.txt`), so this also cross-checks that forge's
// independent gitBlobSHA1 implementation agrees with bench's blobHash for
// identical content — a divergence between the two would fail here even if
// each package's own unit test stayed green in isolation.
func TestApplyPatchOutcome_PopulatesBlobHashesFromInMemoryContent(t *testing.T) {
	const helloWorldHash = "3b18e512dba79e4c8300dd08aeb37f8e728b8dad"

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.txt"), "hello world\n", 0o644)
	mustWriteFile(t, filepath.Join(root, "delete_me.txt"), "hello world\n", 0o644)

	patch := `*** Begin Patch
*** Update File: a.txt
@@
-hello world
+updated content
*** Add File: b.txt
+hello world
*** Delete File: delete_me.txt
*** End Patch`

	result, err := NewApplyPatchTool(root).Execute(context.Background(), map[string]any{"input": patch})
	if err != nil {
		t.Fatal(err)
	}
	outcome := result.Outcome
	if outcome == nil || len(outcome.Effects) != 3 {
		t.Fatalf("expected 3 effects, got %v", outcome.Paths())
	}

	updated := effectByPath(t, outcome, filepath.Join(root, "a.txt"))
	if updated.PreBlobSHA1 != helloWorldHash {
		t.Errorf("a.txt PreBlobSHA1 = %q, want %q (hash of its real prior content)", updated.PreBlobSHA1, helloWorldHash)
	}
	if updated.PostBlobSHA1 == "" || updated.PostBlobSHA1 == updated.PreBlobSHA1 {
		t.Errorf("a.txt PostBlobSHA1 = %q, want a non-empty hash distinct from PreBlobSHA1", updated.PostBlobSHA1)
	}

	added := effectByPath(t, outcome, filepath.Join(root, "b.txt"))
	if added.PreBlobSHA1 != "" {
		t.Errorf("b.txt (create) PreBlobSHA1 = %q, want empty — there is no prior content to hash", added.PreBlobSHA1)
	}
	if added.PostBlobSHA1 != helloWorldHash {
		t.Errorf("b.txt PostBlobSHA1 = %q, want %q", added.PostBlobSHA1, helloWorldHash)
	}

	deleted := effectByPath(t, outcome, filepath.Join(root, "delete_me.txt"))
	if deleted.PreBlobSHA1 != helloWorldHash {
		t.Errorf("delete_me.txt PreBlobSHA1 = %q, want %q — proves what was deleted", deleted.PreBlobSHA1, helloWorldHash)
	}
	if deleted.PostBlobSHA1 != "" {
		t.Errorf("delete_me.txt (delete) PostBlobSHA1 = %q, want empty — nothing survives to hash", deleted.PostBlobSHA1)
	}

	if got := gitBlobSHA1("hello world\n"); got != helloWorldHash {
		t.Fatalf("gitBlobSHA1 fixture mismatch: got %q want %q", got, helloWorldHash)
	}
}
