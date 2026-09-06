package forge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveWorkspacePath_RelativeAnchorsOnWorkspace verifies that a relative
// path is resolved against the workspace root, not the process CWD. This is the
// core sub-agent bug: a sub-agent whose CWD differs from the workspace would
// otherwise read the wrong file.
func TestResolveWorkspacePath_RelativeAnchorsOnWorkspace(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, "agent")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sub, "exec.go")
	if err := os.WriteFile(target, []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveWorkspacePath(ws, "agent/exec.go")
	if err != nil {
		t.Fatalf("resolveWorkspacePath: %v", err)
	}
	if got != target {
		t.Errorf("relative resolution = %q, want %q", got, target)
	}
}

// TestResolveWorkspacePath_DedupesDuplicatedSegment reproduces the exact bug
// from the report: workspace root ends in "swarm-sdk" and the model passes a
// path that ALSO starts with "swarm-sdk", producing a doubled segment. The
// resolver must self-heal to the real file.
func TestResolveWorkspacePath_DedupesDuplicatedSegment(t *testing.T) {
	parent := t.TempDir()
	ws := filepath.Join(parent, "swarm-sdk") // workspace base == "swarm-sdk"
	realDir := filepath.Join(ws, "agent")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(realDir, "agent_execute.go")
	if err := os.WriteFile(realFile, []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Model passes "swarm-sdk/agent/agent_execute.go" — duplicated segment.
	got, err := resolveWorkspacePath(ws, "swarm-sdk/agent/agent_execute.go")
	if err != nil {
		t.Fatalf("resolveWorkspacePath: %v", err)
	}
	if got != realFile {
		t.Errorf("dedup resolution = %q, want %q (the de-duplicated real file)", got, realFile)
	}
}

// TestResolveWorkspacePath_AbsolutePassthrough verifies absolute paths are
// cleaned but not re-anchored.
func TestResolveWorkspacePath_AbsolutePassthrough(t *testing.T) {
	ws := t.TempDir()
	abs := filepath.Join(ws, "sub", "file.go")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveWorkspacePath(ws, abs)
	if err != nil {
		t.Fatalf("resolveWorkspacePath: %v", err)
	}
	if got != abs {
		t.Errorf("absolute passthrough = %q, want %q", got, abs)
	}
}

// TestResolveWorkspacePath_NewFileNoFalseHeal verifies that resolving a path
// for a not-yet-created file (Write/Patch use case) does NOT get mangled by the
// self-heal when no duplicated-segment alternative exists.
func TestResolveWorkspacePath_NewFileNoFalseHeal(t *testing.T) {
	ws := t.TempDir()
	want := filepath.Join(ws, "newpkg", "new_file.go") // does not exist yet

	got, err := resolveWorkspacePath(ws, "newpkg/new_file.go")
	if err != nil {
		t.Fatalf("resolveWorkspacePath: %v", err)
	}
	if got != want {
		t.Errorf("new-file resolution = %q, want %q", got, want)
	}
}

// TestResolveWorkspacePath_NoDedupWhenBothExist verifies that when the doubled
// path actually exists (a legitimately nested dir named like the workspace),
// we do NOT heal — the resolved path is left intact.
func TestResolveWorkspacePath_NoDedupWhenDoubledExists(t *testing.T) {
	parent := t.TempDir()
	ws := filepath.Join(parent, "swarm-sdk")
	doubled := filepath.Join(ws, "swarm-sdk", "agent")
	if err := os.MkdirAll(doubled, 0o755); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(doubled, "x.go")
	if err := os.WriteFile(realFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// This path genuinely exists at the doubled location; keep it.
	got, err := resolveWorkspacePath(ws, "swarm-sdk/agent/x.go")
	if err != nil {
		t.Fatalf("resolveWorkspacePath: %v", err)
	}
	if got != realFile {
		t.Errorf("resolution = %q, want %q (no false heal when doubled path exists)", got, realFile)
	}
}
