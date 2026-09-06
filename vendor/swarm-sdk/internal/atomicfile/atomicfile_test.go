package atomicfile

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWriteRoundTrip(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "file.json")
	want := []byte(`{"hello":"world"}`)

	if err := Write(target, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip mismatch: got %q want %q", got, want)
	}
	// No leftover temp files in the directory.
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 file, found %d", len(entries))
	}
}

func TestWriteRefusesEmpty(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	target := filepath.Join(t.TempDir(), "empty.json")

	if err := Write(target, nil); err == nil {
		t.Fatal("expected error writing empty data, got nil")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target should not exist after refused write: %v", err)
	}

	// AllowEmpty permits it.
	if err := Write(target, nil, AllowEmpty()); err != nil {
		t.Fatalf("Write with AllowEmpty: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target should exist after AllowEmpty write: %v", err)
	}
}

func TestWritePermsAre0600(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	target := filepath.Join(t.TempDir(), "secret.json")
	if err := Write(target, []byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 0600", perm)
	}
}

func TestWithPermOption(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	target := filepath.Join(t.TempDir(), "pub.json")
	if err := Write(target, []byte("x"), WithPerm(0o644)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, _ := os.Stat(target)
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("perm = %o, want 0644", perm)
	}
}

func TestWithLockSerializes(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	target := filepath.Join(t.TempDir(), "locked.json")

	var mu sync.Mutex
	inside := 0
	maxInside := 0
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = WithLock(target, func() error {
				mu.Lock()
				inside++
				if inside > maxInside {
					maxInside = inside
				}
				mu.Unlock()

				time.Sleep(2 * time.Millisecond)

				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
		}()
	}
	wg.Wait()

	if maxInside != 1 {
		t.Fatalf("WithLock did not serialize: max concurrent = %d, want 1", maxInside)
	}
}

func TestBackupRetentionKeepsNAndPrunesZeroByte(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	src := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	// Create 5 backups, keeping newest 3. Sleep so modtimes differ.
	for i := 0; i < 5; i++ {
		if err := Backup(src, 3); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
		time.Sleep(3 * time.Millisecond)
	}

	// Inject a 0-byte corrupt backup that must be pruned.
	zero := filepath.Join(backupsDirForTest(t), "providers.json.zzzz.bak")
	if err := os.WriteFile(zero, nil, 0o600); err != nil {
		t.Fatalf("seed zero: %v", err)
	}

	// One more backup triggers a prune pass.
	if err := Backup(src, 3); err != nil {
		t.Fatalf("final Backup: %v", err)
	}

	entries, _ := os.ReadDir(backupsDirForTest(t))
	count := 0
	for _, e := range entries {
		info, _ := e.Info()
		if info.Size() == 0 {
			t.Fatalf("0-byte backup was not pruned: %s", e.Name())
		}
		count++
	}
	if count != 3 {
		t.Fatalf("retention: kept %d backups, want 3", count)
	}
}

// backupsDirForTest returns the backups dir under the current SWARM_HOME.
func backupsDirForTest(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("SWARM_HOME"), "backups")
}
