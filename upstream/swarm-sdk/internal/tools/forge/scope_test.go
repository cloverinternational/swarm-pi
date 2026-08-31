package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateScope_AllowsPathInsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := validateScope("pkg", ws, "fs_search.invalid_path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(resolved, "pkg") {
		t.Fatalf("resolved=%q", resolved)
	}
}

func TestValidateScope_RejectsPathOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir() // sibling temp dir, same parent
	_, err := validateScope(outside, ws, "fs_search.invalid_path")
	if err == nil {
		t.Fatal("expected error for out-of-workspace path")
	}
	if !strings.Contains(err.Error(), "outside the agent workspace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateScope_BlocksSystemDirs(t *testing.T) {
	ws := t.TempDir()
	// Even if a user somehow asks for /sys, we refuse before ever shelling
	// out to rg. (We don't require the dir to exist for the check.)
	for _, sys := range []string{"/sys", "/proc/1", "/nix/store/foo", "/dev/null"} {
		_, err := validateScope(sys, ws, "fs_search.invalid_path")
		if err == nil {
			t.Fatalf("expected block for %q", sys)
		}
		if !strings.Contains(err.Error(), "blocked system directory") {
			t.Fatalf("%q: unexpected error: %v", sys, err)
		}
	}
}

// TestValidateScope_EmptyWorkspaceAllowsOutsidePath pins the exact contract that
// the swarm-tui --allow-all-paths flag relies on: when workspace == "" the forge
// tools are trusted/unrestricted (only blocklist-gated). If this invariant ever
// changes, --allow-all-paths would silently stop working (Bug 3 / review C1).
func TestValidateScope_EmptyWorkspaceAllowsOutsidePath(t *testing.T) {
	outside := t.TempDir() // an arbitrary absolute path, no workspace configured

	resolved, err := validateScope(outside, "", "fs_write.invalid_path")
	if err != nil {
		t.Fatalf("empty workspace must allow any path, got error: %v", err)
	}
	if resolved == "" {
		t.Fatal("expected a resolved path, got empty")
	}

	// Blocklisted system dirs must STILL be refused even with no workspace.
	if _, err := validateScope("/proc/1", "", "fs_write.invalid_path"); err == nil {
		t.Fatal("blocked system dir must be refused even with empty workspace")
	}
}

func TestValidateScope_RelativePathResolvesAgainstWorkspace(t *testing.T) {
	ws := t.TempDir()
	resolved, err := validateScope(".", ws, "fs_search.invalid_path")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wsResolved, _ := resolveScopePath(ws, "")
	if resolved != wsResolved {
		t.Fatalf("want %q, got %q", wsResolved, resolved)
	}
}

func TestValidateScope_EmptyPathTreatedAsWorkspace(t *testing.T) {
	ws := t.TempDir()
	resolved, err := validateScope("", ws, "fs_search.invalid_path")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wsResolved, _ := resolveScopePath(ws, "")
	if resolved != wsResolved {
		t.Fatalf("want %q, got %q", wsResolved, resolved)
	}
}
