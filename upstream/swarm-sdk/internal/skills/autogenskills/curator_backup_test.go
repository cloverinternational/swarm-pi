package autogenskills

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCuratorBackupRoundTripAndExcludesBackups(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "alpha", "SKILL.md"), "old")
	writeTestFile(t, filepath.Join(root, "alpha", "references", "note.md"), "support")
	writeTestFile(t, filepath.Join(root, "archive", "retired", "SKILL.md"), "retired")
	writeTestFile(t, filepath.Join(root, ".curator_state"), "{}")
	store, err := NewCuratorBackupStore(root, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := store.Snapshot("before")
	if err != nil {
		t.Fatal(err)
	}
	if snap.FileCount != 4 || snap.SourceDigest == "" {
		t.Fatalf("manifest = %#v", snap)
	}
	writeTestFile(t, filepath.Join(root, "alpha", "SKILL.md"), "new")
	writeTestFile(t, filepath.Join(root, "extra", "SKILL.md"), "extra")
	result, err := store.Rollback(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Safety.Reason != "pre-rollback:"+snap.ID {
		t.Fatalf("safety manifest = %#v", result.Safety)
	}
	assertTestFile(t, filepath.Join(root, "alpha", "SKILL.md"), "old")
	assertTestFile(t, filepath.Join(root, "alpha", "references", "note.md"), "support")
	if _, err := os.Stat(filepath.Join(root, "extra")); !os.IsNotExist(err) {
		t.Fatalf("extra survived rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".curator_backups", snap.ID)); err != nil {
		t.Fatalf("backup removed: %v", err)
	}
}

func TestCuratorBackupRetention(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a", "SKILL.md"), "x")
	store, _ := NewCuratorBackupStore(root, "", 2)
	n := 0
	store.Now = func() time.Time {
		n++
		return time.Date(2025, 1, 1, 0, 0, n, 0, time.UTC)
	}
	for range 4 {
		if _, err := store.Snapshot("test"); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(items))
	}
}

func TestCuratorBackupRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a", "SKILL.md"), "safe")
	store, _ := NewCuratorBackupStore(root, "", 5)
	snap, err := store.Snapshot("safe")
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(store.BackupDir, snap.ID, "skills.tar.gz")
	out, _ := os.Create(archive)
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../escaped", Mode: 0o600, Size: 4, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()
	_ = gz.Close()
	_ = out.Close()
	info, _ := os.Stat(archive)
	snap.SizeBytes = info.Size()
	raw, _ := json.MarshalIndent(snap, "", "  ")
	_ = os.WriteFile(filepath.Join(store.BackupDir, snap.ID, "manifest.json"), raw, 0o600)
	if _, err := store.Rollback(snap.ID); err == nil {
		t.Fatal("expected traversal error")
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escaped")); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote outside root: %v", err)
	}
	if _, err := store.Rollback("../bad"); err == nil {
		t.Fatal("expected invalid ID error")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertTestFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
}
