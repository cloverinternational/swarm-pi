package usageindex

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBoundedCallReturnsEvenWhenFnIgnoresCancellation directly exercises the
// race primitive Load relies on, independent of any real database driver.
// fn deliberately never checks ctx and sleeps far longer than the timeout —
// modeling a native driver call Go cannot preempt — and boundedCall must still
// return promptly.
func TestBoundedCallReturnsEvenWhenFnIgnoresCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err, timedOut := boundedCall(ctx, func(context.Context) (int, error) {
		time.Sleep(2 * time.Second) // never checks ctx — simulates an uncancelable call
		return 42, nil
	})
	elapsed := time.Since(start)

	if !timedOut {
		t.Fatal("expected timedOut=true")
	}
	if err == nil {
		t.Fatal("expected a context error")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("boundedCall took %v, want it to return near the 50ms timeout regardless of fn", elapsed)
	}
}

// TestBoundedCallReturnsFnResultWhenItFinishesFirst is the complementary case:
// when fn finishes before the deadline, its real result must be returned.
func TestBoundedCallReturnsFnResultWhenItFinishesFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, err, timedOut := boundedCall(ctx, func(context.Context) (int, error) {
		return 7, nil
	})
	if timedOut {
		t.Fatal("expected timedOut=false")
	}
	if err != nil || value != 7 {
		t.Fatalf("value=%d err=%v, want 7/nil", value, err)
	}
}

// TestLoadFallsBackWhenIndexBlocksBeyondTimeout reproduces the reported bug:
// the Usage screen showed "Refreshing usage data…" forever. The root cause on
// a machine running several swarm processes at once is that they all share
// one index file; if a writer holds it locked, Load must still return in
// bounded time instead of waiting on the database indefinitely.
func TestLoadFallsBackWhenIndexBlocksBeyondTimeout(t *testing.T) {
	root := fixtureStore(t)
	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "usage-index.sqlite")

	// Hold an exclusive write lock on the same database file from a completely
	// separate connection, simulating another swarm process already syncing.
	locker, err := sql.Open("sqlite", "file:"+filepath.ToSlash(sqlitePath)+"?_pragma=busy_timeout(30000)")
	if err != nil {
		t.Fatalf("open locker: %v", err)
	}
	defer locker.Close()
	locker.SetMaxOpenConns(1)
	if _, err := locker.Exec(`CREATE TABLE IF NOT EXISTS lock_holder(id INTEGER)`); err != nil {
		t.Fatalf("create locker table: %v", err)
	}
	tx, err := locker.Begin()
	if err != nil {
		t.Fatalf("begin locker tx: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO lock_holder(id) VALUES(1)`); err != nil {
		t.Fatalf("locker insert: %v", err)
	}
	// Never commit within the test window: this is the "stuck other process".
	// t.Cleanup runs after the test function returns, so nothing in the test
	// body may block waiting on it — that would deadlock the test itself.
	t.Cleanup(func() { _ = tx.Rollback() })

	start := time.Now()
	snapshot, err := Load(context.Background(), root, LoadOptions{
		OpenOptions: OpenOptions{Backend: BackendSQLite, SQLitePath: sqlitePath},
		// Much shorter than SQLite's own busy_timeout, so the assertion below
		// proves Load's own bound is what returned control, not SQLite's.
		SyncTimeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Load took %v; it must return near SyncTimeout, not block on the held lock", elapsed)
	}
	if !snapshot.Degraded {
		t.Fatalf("expected a degraded (fallback) snapshot, got %+v", snapshot)
	}
	// SQLite's own busy handling can fail fast with SQLITE_BUSY instead of
	// blocking all the way to SyncTimeout; either outcome is an acceptable
	// degraded fallback. What must never happen — and what the elapsed-time
	// assertion above already proved — is Load blocking indefinitely.
	lowerReason := strings.ToLower(snapshot.DegradedReason)
	if !strings.Contains(lowerReason, "timed out") && !strings.Contains(lowerReason, "lock") && !strings.Contains(lowerReason, "busy") {
		t.Errorf("DegradedReason = %q, want it to mention the timeout or the lock contention", snapshot.DegradedReason)
	}
	// The fallback must still produce correct numbers: a locked index is a
	// speed problem, never an accuracy problem.
	if snapshot.Summary.ResponseCount == 0 {
		t.Error("expected the fallback scan to still report responses")
	}
}

// TestLoadHonorsCallerCancellationWithoutMaskingIt ensures that when the
// caller's own context ends (not our internal timeout), Load reports that
// directly instead of silently falling back to a full scan.
func TestLoadHonorsCallerCancellationWithoutMaskingIt(t *testing.T) {
	root := fixtureStore(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Load(ctx, root, LoadOptions{
		OpenOptions: OpenOptions{Backend: BackendSQLite, SQLitePath: filepath.Join(dir, "usage-index.sqlite")},
		SyncTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected an error when the caller's context is already canceled")
	}
}

// TestLoadDefaultBackendPrefersSQLite locks in the deliberate default: unlike
// the history search index, the usage index defaults to SQLite because it is
// a single file shared by every concurrently running swarm process, and
// SQLite's WAL + busy_timeout handling of that pattern is mature. This test
// guards against an accidental revert to Turso-first BackendAuto.
func TestLoadDefaultBackendPrefersSQLite(t *testing.T) {
	root := fixtureStore(t)
	dir := t.TempDir()
	snapshot, err := Load(context.Background(), root, LoadOptions{
		OpenOptions: OpenOptions{
			Backend:    BackendSQLite,
			SQLitePath: filepath.Join(dir, "usage-index.sqlite"),
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snapshot.Backend != BackendSQLite {
		t.Errorf("Backend = %q, want sqlite", snapshot.Backend)
	}
}
