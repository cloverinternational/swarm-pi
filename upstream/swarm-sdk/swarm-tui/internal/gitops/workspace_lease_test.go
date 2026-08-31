package gitops

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceLeaseCountsAndCloses(t *testing.T) {
	stateDir := t.TempDir()
	workspace := t.TempDir()
	lease, err := registerWorkspaceLease(stateDir, workspace)
	if err != nil {
		t.Fatalf("registerWorkspaceLease: %v", err)
	}
	if got := countActiveWorkspaceSessions(stateDir, workspace, time.Now()); got != 1 {
		t.Fatalf("active sessions = %d, want 1", got)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := countActiveWorkspaceSessions(stateDir, workspace, time.Now()); got != 0 {
		t.Fatalf("active sessions after close = %d, want 0", got)
	}
}

func TestWorkspaceLeasePrunesStaleAndInvalidRecords(t *testing.T) {
	stateDir := t.TempDir()
	workspace := t.TempDir()
	canonical, _ := filepath.Abs(workspace)
	dir := filepath.Join(stateDir, workspaceLeaseKey(canonical))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "stale.json")
	if err := os.WriteFile(stale, []byte(`{"pid":1,"execution_root":"`+canonical+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-workspaceLeaseMaxAge - time.Second)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if got := countActiveWorkspaceSessions(stateDir, workspace, time.Now()); got != 0 {
		t.Fatalf("active sessions = %d, want 0", got)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale lease was not pruned: %v", err)
	}
}
