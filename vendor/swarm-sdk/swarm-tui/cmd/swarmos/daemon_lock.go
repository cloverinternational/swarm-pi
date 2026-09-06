package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Advisory file locks for the global-daemon lifecycle.
//
// Two locks make the daemon a proper single instance:
//
//   - the SPAWN lock (acquireDaemonLock in ensureGlobalDaemon): serializes
//     concurrent "ensure" callers (two TUIs starting at once) so only one of
//     them probes-and-spawns; the loser waits and finds the winner's daemon.
//   - the INSTANCE lock (held for the daemon process's lifetime): a second
//     `swarmos daemon` with the same handle fails fast instead of fighting
//     over the presence file and gateway port.
//
// OS advisory locks die with the process, so a forcibly terminated daemon never
// leaves a stale lock behind — unlike pidfiles.
//
// Identity corroboration: every successful acquire additionally stamps the
// lock file with a daemonLockIdentity record (holder PID, owning user id, a
// fresh random token, and the acquisition time). For the instance lock this
// record is written by the daemon process itself at startup and lives for
// exactly as long as the daemon holds the lock — a forcibly killed or exited
// daemon releases the OS lock immediately, so a later probe that can acquire
// the same lock proves the record is stale/free instead of trusting whatever
// PID happens to be written in it. Manual stop (see global_daemon.go) uses
// this to corroborate a presence record's PID against the process that is
// ACTUALLY holding the handle's instance lock right now, instead of trusting
// PID (and PID reuse) or presence content alone.

// daemonLockDir returns ~/.swarmos/locks, creating it if needed.
func daemonLockDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for lock dir: %w", err)
	}
	dir := filepath.Join(home, ".swarmos", "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create lock dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("tighten lock dir: %w", err)
	}
	return dir, nil
}

// daemonLockIdentity is the corroboration record stamped into a lock file by
// whichever process currently holds it. It is NOT a substitute for the lock
// itself — the identity fields are self-reported by the holder — but reading
// it is only trusted by callers in combination with daemonInstanceLockHeld
// proving that the SAME lock is still exclusively held right now, which an
// unrelated or spoofing process cannot fake (the OS enforces the flock).
type daemonLockIdentity struct {
	PID               int    `json:"pid"`
	UID               string `json:"uid"`
	Token             string `json:"token"`
	StartedAtUnixNano int64  `json:"started_at_unix_nano"`
	ProcessStart      string `json:"process_start"`
	Executable        string `json:"executable"`
}

// writeDaemonLockIdentity stamps f (already truncated-and-locked by the
// caller) with a fresh identity record for THIS process. Best-effort: a
// failure here does not invalidate the lock itself, it only means later
// identity corroboration will find no/garbled record and fail closed.
func writeDaemonLockIdentity(f *os.File) (*daemonLockIdentity, error) {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return nil, fmt.Errorf("generate lock token: %w", err)
	}
	start, executable, uid, err := processEvidenceFunc(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("read lock holder process evidence: %w", err)
	}
	identity := daemonLockIdentity{
		PID:               os.Getpid(),
		UID:               uid,
		Token:             hex.EncodeToString(tok),
		StartedAtUnixNano: time.Now().UnixNano(),
		ProcessStart:      start,
		Executable:        executable,
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("marshal lock identity: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return nil, fmt.Errorf("truncate lock file: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("seek lock file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return nil, fmt.Errorf("write lock identity: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	return &identity, nil
}

// readDaemonLockIdentity reads the identity record stamped by whoever last
// held name's lock. Returns (nil, nil) when the lock file does not exist or
// has never been stamped (no evidence either way — callers must treat this
// as "cannot corroborate", not as "corroborated absent"). Returns a non-nil
// error for a lock file that exists but contains unparseable or impossible
// content (fail closed: a malformed record must never be trusted).
func readDaemonLockIdentity(name string) (*daemonLockIdentity, error) {
	dir, err := daemonLockDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".lock")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read lock %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}
	var identity daemonLockIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, fmt.Errorf("malformed lock identity %s: %w", path, err)
	}
	if identity.PID <= 0 {
		return nil, fmt.Errorf("malformed lock identity %s: non-positive pid %d", path, identity.PID)
	}
	if identity.Token == "" || identity.ProcessStart == "" || identity.Executable == "" || identity.UID == "" {
		return nil, fmt.Errorf("malformed lock identity %s: incomplete instance evidence", path)
	}
	return &identity, nil
}

// daemonInstanceLockHeld reports whether name's lock is currently held by a
// live process, WITHOUT writing to the file (a plain probe, unlike
// acquireDaemonLock). It never blocks:
//
//   - held=true  — a non-blocking acquire failed because another open file
//     description (necessarily a different, still-live process for the
//     instance lock's whole-lifetime usage pattern) holds it.
//   - held=false — the probe itself acquired the lock (nobody held it), and
//     immediately released it again. Any identity record already in the file
//     is therefore stale/free and must not be trusted for a destructive
//     decision.
//
// This is the "immediate pre-signal liveness" check: called right before a
// manual SIGTERM/SIGKILL, it closes the TOCTOU window between validating a
// presence record and actually signalling the PID it names.
func daemonInstanceLockHeld(name string) (held bool, err error) {
	dir, err := daemonLockDir()
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, fmt.Errorf("open lock %s: %w", path, err)
	}
	defer f.Close()

	ok, err := lockDaemonFile(f, false)
	if err != nil {
		return false, fmt.Errorf("probe lock %s: %w", path, err)
	}
	if !ok {
		return true, nil // held by a live process
	}
	_ = unlockDaemonFile(f) // we only acquired to prove it was free; release it
	return false, nil
}

// acquireDaemonLock takes the named advisory lock. When block is true it
// waits for the holder to release; when false it fails immediately with
// ok=false if the lock is held elsewhere. The returned release func closes
// the lock (also released automatically if the process dies).
func acquireDaemonLock(name string, block bool) (release func(), ok bool, err error) {
	release, _, ok, err = acquireDaemonLockWithIdentity(name, block)
	return release, ok, err
}

func acquireDaemonLockWithIdentity(name string, block bool) (release func(), identity *daemonLockIdentity, ok bool, err error) {
	dir, err := daemonLockDir()
	if err != nil {
		return nil, nil, false, err
	}
	path := filepath.Join(dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, false, fmt.Errorf("open lock %s: %w", path, err)
	}

	ok, err = lockDaemonFile(f, block)
	if err != nil {
		_ = f.Close()
		return nil, nil, false, fmt.Errorf("lock %s: %w", path, err)
	}
	if !ok {
		_ = f.Close()
		return nil, nil, false, nil
	}

	// Stamp holder identity so a later stop/takeover decision (see
	// global_daemon.go) can corroborate a presence record's PID against
	// whoever is ACTUALLY holding this lock right now. Best-effort: a
	// stamping failure does not fail the acquire — it only means identity
	// corroboration later finds no usable record and fails closed.
	identity, err = writeDaemonLockIdentity(f)
	if err != nil {
		_ = unlockDaemonFile(f)
		_ = f.Close()
		return nil, nil, false, err
	}

	return func() {
		_ = unlockDaemonFile(f)
		_ = f.Close()
	}, identity, true, nil
}

// daemonInstanceLockName returns the instance-lock name for a daemon handle.
func daemonInstanceLockName(handle string) string {
	return "daemon-instance-" + handle
}

// daemonSpawnLockName is the shared lock serializing ensureGlobalDaemon.
const daemonSpawnLockName = "daemon-spawn"

var processEvidenceFunc = readProcessEvidence
