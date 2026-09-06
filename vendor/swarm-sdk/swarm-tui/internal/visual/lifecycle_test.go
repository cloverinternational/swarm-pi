package visual

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndReadLockFile(t *testing.T) {
	dir := t.TempDir()
	lf := LockFilePath(dir)
	info := LockInfo{Port: 12345, PID: os.Getpid(), SessionID: "abc"}
	if err := WriteLockFile(lf, info); err != nil {
		t.Fatal(err)
	}

	got, err := ReadLockFile(lf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 12345 || got.PID != os.Getpid() || got.SessionID != "abc" {
		t.Errorf("got %+v", got)
	}
}

func TestIsStale_NoPID(t *testing.T) {
	dir := t.TempDir()
	lf := LockFilePath(dir)
	data, _ := json.Marshal(LockInfo{Port: 1, PID: 0})
	_ = os.MkdirAll(filepath.Dir(lf), 0o755)
	_ = os.WriteFile(lf, data, 0o644)
	if !IsStale(lf, 100) {
		t.Error("expected stale for PID 0")
	}
}

func TestIsStale_MissingFile(t *testing.T) {
	dir := t.TempDir()
	lf := filepath.Join(dir, "does-not-exist.json")
	if !IsStale(lf, 100) {
		t.Error("expected stale for missing file")
	}
}

func TestReapLockFile(t *testing.T) {
	dir := t.TempDir()
	lf := LockFilePath(dir)
	_ = WriteLockFile(lf, LockInfo{Port: 1, PID: os.Getpid()})
	if err := ReapLockFile(lf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lf); !os.IsNotExist(err) {
		t.Error("expected file removed")
	}
}
