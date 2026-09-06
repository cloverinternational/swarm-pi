package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func secureTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestHostLockLifetimeExclusion(t *testing.T) {
	path := filepath.Join(secureTestDir(t), "host.lock")
	first, err := AcquireHostLock(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AcquireHostLock(path)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, ErrHostLocked) {
		_ = first.Close()
		t.Fatalf("second acquisition error = %v, want ErrHostLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireHostLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHostLockRejectsSymlink(t *testing.T) {
	dir := secureTestDir(t)
	target := filepath.Join(dir, "target")
	if err := osWriteFile(target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "host.lock")
	if err := makeSymlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	lock, err := AcquireHostLock(link)
	if lock != nil {
		_ = lock.Close()
	}
	if err == nil {
		t.Fatal("AcquireHostLock followed a symlink")
	}
}
