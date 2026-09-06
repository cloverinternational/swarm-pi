package main

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

// Point the lock dir at a temp HOME so tests never touch the real
// ~/.swarmos/locks (and never collide with a live daemon).
//
// Deliberately NOT t.TempDir(): that nests under a path containing the full
// (sometimes long) test/subtest name, e.g.
// /tmp/TestSomeVeryDescriptiveName/001. Presence control sockets live at
// <HOME>/.swarm/swarms/default/peers/<handle>.ctrl, and unix domain socket
// paths are capped at sizeof(sockaddr_un.sun_path) (108 bytes on Linux) — a
// long descriptive test name pushes that over the limit and net.Listen
// fails with "invalid argument" for reasons that have nothing to do with
// the behavior under test. A short, fixed-width prefix keeps every derived
// path well under that limit regardless of test name length.
func withTempHome(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "swh")
	if err != nil {
		t.Fatalf("create short temp home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
}

// writeRawLockFile writes data verbatim to name's lock file, bypassing
// acquireDaemonLock entirely. Used to construct malformed or foreign
// identity records that a real acquire would never produce, so denial paths
// can be tested deterministically without needing a second live process.
func writeRawLockFile(t *testing.T, name string, data []byte) {
	t.Helper()
	dir, err := daemonLockDir()
	if err != nil {
		t.Fatalf("daemonLockDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".lock"), data, 0o644); err != nil {
		t.Fatalf("write raw lock file: %v", err)
	}
}

// writeLockIdentity JSON-encodes id and writes it as name's lock file
// content, bypassing acquireDaemonLock so tests can stamp an arbitrary
// (including intentionally wrong) holder identity.
func writeLockIdentity(t *testing.T, name string, id daemonLockIdentity) {
	t.Helper()
	if id.UID == "" {
		if u, err := user.Current(); err == nil {
			id.UID = u.Uid
		}
	}
	if id.Token == "" {
		id.Token = "tok"
	}
	if id.ProcessStart == "" {
		id.ProcessStart = "test-start"
	}
	if id.Executable == "" {
		id.Executable = "/test/swarmos"
	}
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	writeRawLockFile(t, name, data)
}

func TestAcquireDaemonLock_Exclusive(t *testing.T) {
	withTempHome(t)

	release1, ok, err := acquireDaemonLock("test-lock", false)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}

	// A second non-blocking acquire (separate file description) must fail
	// while the first is held.
	release2, ok2, err2 := acquireDaemonLock("test-lock", false)
	if err2 != nil {
		t.Fatalf("second acquire errored: %v", err2)
	}
	if ok2 {
		release2()
		t.Fatal("second acquire succeeded while lock was held")
	}

	// After release, the lock is available again.
	release1()
	release3, ok3, err3 := acquireDaemonLock("test-lock", false)
	if err3 != nil || !ok3 {
		t.Fatalf("reacquire after release: ok=%v err=%v", ok3, err3)
	}
	release3()
}

func TestAcquireDaemonLock_DistinctNamesIndependent(t *testing.T) {
	withTempHome(t)

	r1, ok, err := acquireDaemonLock("lock-a", false)
	if err != nil || !ok {
		t.Fatalf("lock-a: ok=%v err=%v", ok, err)
	}
	defer r1()

	r2, ok2, err2 := acquireDaemonLock("lock-b", false)
	if err2 != nil || !ok2 {
		t.Fatalf("lock-b should be independent of lock-a: ok=%v err=%v", ok2, err2)
	}
	r2()
}

func TestDaemonLockDirCreated(t *testing.T) {
	withTempHome(t)

	dir, err := daemonLockDir()
	if err != nil {
		t.Fatalf("daemonLockDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("lock dir not created: %v", err)
	}
	if filepath.Base(dir) != "locks" {
		t.Errorf("unexpected lock dir: %s", dir)
	}
}

// TestAcquireDaemonLock_StampsIdentity proves acquireDaemonLock stamps a
// self-identity record (holder pid, owning user id, a fresh token, and an
// acquisition timestamp) into the lock file on every successful acquire —
// this is the "daemon instance token" that a later manual-stop identity
// check corroborates a presence record's pid against, instead of trusting
// pid/presence content alone (ADR-005 "Identity proof").
func TestAcquireDaemonLock_StampsIdentity(t *testing.T) {
	withTempHome(t)

	release, ok, err := acquireDaemonLock("identity-stamp", false)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	defer release()

	id, err := readDaemonLockIdentity("identity-stamp")
	if err != nil {
		t.Fatalf("readDaemonLockIdentity: %v", err)
	}
	if id == nil {
		t.Fatal("expected a stamped identity record while the lock is held")
	}
	if id.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", id.PID, os.Getpid())
	}
	if id.Token == "" {
		t.Error("expected a non-empty token")
	}
	if id.StartedAtUnixNano == 0 {
		t.Error("expected a non-zero acquisition timestamp")
	}
	if id.ProcessStart == "" {
		t.Error("expected non-empty OS process-start evidence")
	}
	if id.Executable == "" {
		t.Error("expected non-empty executable evidence")
	}
	if u, uerr := user.Current(); uerr == nil && id.UID != u.Uid {
		t.Errorf("UID = %q, want %q", id.UID, u.Uid)
	}
}

// TestAcquireDaemonLock_ReacquireRestampsIdentity proves a fresh acquire
// after release overwrites the previous holder's identity (new token, same
// pid since it's the same test process) rather than leaving stale content
// behind — a later stop decision must always see the CURRENT holder.
func TestAcquireDaemonLock_ReacquireRestampsIdentity(t *testing.T) {
	withTempHome(t)

	release1, ok, err := acquireDaemonLock("identity-restamp", false)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	first, err := readDaemonLockIdentity("identity-restamp")
	if err != nil || first == nil {
		t.Fatalf("read first identity: id=%+v err=%v", first, err)
	}
	release1()

	release2, ok, err := acquireDaemonLock("identity-restamp", false)
	if err != nil || !ok {
		t.Fatalf("second acquire: ok=%v err=%v", ok, err)
	}
	defer release2()
	second, err := readDaemonLockIdentity("identity-restamp")
	if err != nil || second == nil {
		t.Fatalf("read second identity: id=%+v err=%v", second, err)
	}
	if first.Token == second.Token {
		t.Error("expected a fresh token on reacquire, got the same one")
	}
}

// TestReadDaemonLockIdentity_MissingReturnsNilNil proves a lock name that
// was never acquired reports (nil, nil) — "no evidence either way" — never a
// synthesized identity and never an error that a caller might mishandle.
func TestReadDaemonLockIdentity_MissingReturnsNilNil(t *testing.T) {
	withTempHome(t)

	id, err := readDaemonLockIdentity("never-acquired")
	if err != nil {
		t.Fatalf("unexpected error for a never-acquired lock: %v", err)
	}
	if id != nil {
		t.Fatalf("expected nil identity for a never-acquired lock, got %+v", id)
	}
}

// TestReadDaemonLockIdentity_MalformedFailsClosed proves a lock file whose
// content is not valid JSON is reported as an error, never silently ignored
// or treated as "no record" — a malformed record must never be trusted.
func TestReadDaemonLockIdentity_MalformedFailsClosed(t *testing.T) {
	withTempHome(t)
	writeRawLockFile(t, "malformed", []byte("{not valid json"))

	id, err := readDaemonLockIdentity("malformed")
	if err == nil {
		t.Fatal("expected an error for malformed lock content")
	}
	if id != nil {
		t.Fatalf("expected nil identity alongside the error, got %+v", id)
	}
}

// TestReadDaemonLockIdentity_NonPositivePIDFailsClosed proves a lock record
// with an impossible pid (<=0) is rejected as malformed rather than trusted.
func TestReadDaemonLockIdentity_NonPositivePIDFailsClosed(t *testing.T) {
	withTempHome(t)
	writeLockIdentity(t, "bad-pid", daemonLockIdentity{PID: 0, UID: "1000", Token: "x"})

	if _, err := readDaemonLockIdentity("bad-pid"); err == nil {
		t.Fatal("expected an error for a non-positive recorded pid")
	}

	writeLockIdentity(t, "bad-pid", daemonLockIdentity{PID: -7, UID: "1000", Token: "x"})
	if _, err := readDaemonLockIdentity("bad-pid"); err == nil {
		t.Fatal("expected an error for a negative recorded pid")
	}
}

// TestDaemonInstanceLockHeld proves the non-mutating probe correctly reports
// held=false before anything acquires the lock, held=true while an open file
// description holds it, and held=false again after release — and that
// probing a free lock does NOT stamp any identity into the file (the probe
// must have no side effects on content, only ever testing then releasing).
func TestDaemonInstanceLockHeld(t *testing.T) {
	withTempHome(t)

	held, err := daemonInstanceLockHeld("held-probe")
	if err != nil {
		t.Fatalf("probe before acquire: %v", err)
	}
	if held {
		t.Fatal("expected held=false before anything acquires the lock")
	}
	if id, rerr := readDaemonLockIdentity("held-probe"); rerr != nil || id != nil {
		t.Fatalf("probe must not stamp identity into a free lock: id=%+v err=%v", id, rerr)
	}

	release, ok, err := acquireDaemonLock("held-probe", false)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}

	held, err = daemonInstanceLockHeld("held-probe")
	if err != nil {
		t.Fatalf("probe while held: %v", err)
	}
	if !held {
		t.Fatal("expected held=true while the lock is held by a live process")
	}

	release()

	held, err = daemonInstanceLockHeld("held-probe")
	if err != nil {
		t.Fatalf("probe after release: %v", err)
	}
	if held {
		t.Fatal("expected held=false after the holder released it")
	}
}
