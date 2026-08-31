package a2a

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMergeExistingAttachSurfacesPreservesSameProcessEndpoints(t *testing.T) {
	next := &PeerPresence{Handle: "tui", EndpointURL: "http://127.0.0.1:9000/rpc"}
	existing := &PeerPresence{
		Handle:        "tui",
		PID:           42,
		ControlSocket: "/tmp/tui.ctrl",
		ServeURL:      "http://127.0.0.1:9001",
	}

	mergeExistingAttachSurfaces(next, existing, 42)

	if next.ControlSocket != existing.ControlSocket {
		t.Fatalf("ControlSocket = %q, want %q", next.ControlSocket, existing.ControlSocket)
	}
	if next.ServeURL != existing.ServeURL {
		t.Fatalf("ServeURL = %q, want %q", next.ServeURL, existing.ServeURL)
	}
	if next.EndpointURL != "http://127.0.0.1:9000/rpc" {
		t.Fatalf("runtime EndpointURL was overwritten: %q", next.EndpointURL)
	}
}

func TestMergeExistingAttachSurfacesRejectsStaleProcess(t *testing.T) {
	next := &PeerPresence{Handle: "tui"}
	existing := &PeerPresence{
		Handle:        "tui",
		PID:           41,
		ControlSocket: "/tmp/stale.ctrl",
		ServeURL:      "http://127.0.0.1:9001",
	}

	mergeExistingAttachSurfaces(next, existing, 42)

	if next.ControlSocket != "" || next.ServeURL != "" {
		t.Fatalf("stale attach surfaces were preserved: %+v", next)
	}
}

// TestJoinSwarm_MergeExistingAttachSurfaces_RoundTrip is an end-to-end,
// filesystem-backed version of the two pure-function tests above: it proves
// the full JoinSwarm → GetPeer → mergeExistingAttachSurfaces round trip
// still preserves same-process attach surfaces (control socket, serve URL)
// after the hardening in this phase (handle validation, secured 0700/0600
// directories/files, atomic publication) — a stale-vs-live PID boundary is
// exactly the "stale record" scenario the runtime depends on this for.
func TestJoinSwarm_MergeExistingAttachSurfaces_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	pid := os.Getpid()
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{
		Handle:        "tui-live",
		PID:           pid,
		ControlSocket: filepath.Join(t.TempDir(), "tui.ctrl"),
		ServeURL:      "http://127.0.0.1:9001",
	}); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}

	existing, err := GetPeer(DefaultSwarmName, "tui-live")
	if err != nil {
		t.Fatalf("GetPeer: %v", err)
	}
	if existing == nil {
		t.Fatalf("GetPeer returned nil for a just-joined peer")
	}

	next := &PeerPresence{Handle: "tui-live", EndpointURL: "http://127.0.0.1:9000/rpc"}
	mergeExistingAttachSurfaces(next, existing, pid)

	if next.ControlSocket != existing.ControlSocket {
		t.Errorf("ControlSocket not preserved across disk round trip: got %q, want %q", next.ControlSocket, existing.ControlSocket)
	}
	if next.ServeURL != existing.ServeURL {
		t.Errorf("ServeURL not preserved across disk round trip: got %q, want %q", next.ServeURL, existing.ServeURL)
	}
}

// TestGetPeer_FailsClosedOnUnsafeSymlinkedEntry proves GetPeer (used by
// mergeExistingAttachSurfaces at runtime start to decide whether to preserve
// an existing attach surface) treats an unsafe, symlink-substituted presence
// file exactly as "not found" rather than an error that might be mishandled
// by a caller — unsafe legacy/hostile state must fail closed, never be
// silently trusted as a live peer's attach surface.
func TestGetPeer_FailsClosedOnUnsafeSymlinkedEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"handle":"tui-live","control_socket":"/tmp/attacker.ctrl"}`), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	peersDir := filepath.Join(SwarmPath(DefaultSwarmName), "peers")
	if err := os.MkdirAll(peersDir, 0o700); err != nil {
		t.Fatalf("mkdir peers: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(peersDir, "tui-live.json")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := GetPeer(DefaultSwarmName, "tui-live")
	if err != nil {
		t.Fatalf("GetPeer must fail closed (nil, nil), not error, for unsafe state: %v", err)
	}
	if got != nil {
		t.Fatalf("GetPeer must never surface a symlink-substituted presence file as a real peer: %+v", got)
	}
}

func TestRemovePeerIfInstancePreservesReplacementPublication(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const handle = "replacement"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, PID: 1, Type: PeerTypeDaemon, InstanceToken: "new"}); err != nil {
		t.Fatal(err)
	}
	removed, err := RemovePeerIfInstance(DefaultSwarmName, handle, "old")
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("cleanup for an old token removed replacement publication")
	}
	got, err := GetPeer(DefaultSwarmName, handle)
	if err != nil || got == nil || got.InstanceToken != "new" {
		t.Fatalf("replacement lost: peer=%+v err=%v", got, err)
	}
}

func TestRegistryPermissionTighteningFailureFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fchmod seam")
	}
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(SwarmPath("chmod-failure"), "peers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "peer.json")
	if err := os.WriteFile(path, []byte(`{"handle":"peer"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	original := registryFchmod
	registryFchmod = func(int, uint32) error { return errors.New("injected chmod failure") }
	t.Cleanup(func() { registryFchmod = original })
	if _, err := safeReadFile(path); err == nil || !containsError(err, "injected chmod failure") {
		t.Fatalf("unsafe broad-mode file was accepted after chmod failure: %v", err)
	}
}

func containsError(err error, text string) bool {
	return err != nil && strings.Contains(err.Error(), text)
}

func TestRegistryCrossProcessHelper(t *testing.T) {
	role := os.Getenv("A2A_REGISTRY_HELPER")
	if role == "" {
		return
	}
	handle := os.Getenv("A2A_REGISTRY_HANDLE")
	switch role {
	case "cleanup":
		registryConditionalCleanupAfterRead = func() {
			fmt.Println("CLEANUP_ENTERED")
			var release [1]byte
			if _, err := os.Stdin.Read(release[:]); err != nil {
				fmt.Fprintf(os.Stderr, "wait for cleanup release: %v\n", err)
				os.Exit(2)
			}
		}
		removed, err := RemovePeerIfInstance(DefaultSwarmName, handle, "old")
		if err != nil || !removed {
			fmt.Fprintf(os.Stderr, "conditional cleanup: removed=%v err=%v\n", removed, err)
			os.Exit(2)
		}
		fmt.Println("CLEANUP_DONE")
	case "writer":
		realAcquire := registryAcquireHandleLock
		registryAcquireHandleLock = func(path string) (func() error, error) {
			fmt.Println("WRITER_ATTEMPTING_LOCK")
			return realAcquire(path)
		}
		err := JoinSwarm(DefaultSwarmName, PeerPresence{
			Handle:        handle,
			PID:           os.Getpid(),
			Type:          PeerTypeDaemon,
			InstanceToken: "new",
			ControlSocket: filepath.Join(filepath.Dir(filepath.Join(SwarmPath(DefaultSwarmName), "peers", handle+".json")), handle+".ctrl"),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "publish replacement: %v\n", err)
			os.Exit(2)
		}
		ctrlPath := filepath.Join(SwarmPath(DefaultSwarmName), "peers", handle+".ctrl")
		if err := os.WriteFile(ctrlPath, []byte("replacement-resource"), filePerm); err != nil {
			fmt.Fprintf(os.Stderr, "publish replacement resource: %v\n", err)
			os.Exit(2)
		}
		fmt.Println("WRITER_PUBLISHED")
	case "tx-hold":
		// Enters a WithPeerHandleTransaction for handle and blocks inside the
		// callback until released over stdin, proving (from the outside, via
		// a second helper process) that a same-handle transaction elsewhere
		// cannot proceed while this one is open.
		err := WithPeerHandleTransaction(DefaultSwarmName, handle, func(tx *PeerHandleTransaction) error {
			fmt.Println("TX_ENTERED")
			var release [1]byte
			if _, err := os.Stdin.Read(release[:]); err != nil {
				fmt.Fprintf(os.Stderr, "wait for tx-hold release: %v\n", err)
				os.Exit(2)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "tx-hold: %v\n", err)
			os.Exit(2)
		}
		fmt.Println("TX_DONE")
	case "tx-attempt":
		// Attempts a WithPeerHandleTransaction for handle, announcing entry
		// (proving it actually got the lock, not just that the process
		// started) so a driving test can distinguish "blocked before
		// entering" from "entered and finished quickly".
		err := WithPeerHandleTransaction(DefaultSwarmName, handle, func(tx *PeerHandleTransaction) error {
			fmt.Println("TX_ENTERED")
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "tx-attempt: %v\n", err)
			os.Exit(2)
		}
		fmt.Println("TX_DONE")
	default:
		fmt.Fprintf(os.Stderr, "unknown helper role %q\n", role)
		os.Exit(2)
	}
}

func startRegistryHelper(t *testing.T, home, role, handle string) (*exec.Cmd, *bufio.Reader, func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRegistryCrossProcessHelper$")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"A2A_REGISTRY_HELPER="+role,
		"A2A_REGISTRY_HANDLE="+handle,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	release := func() {
		if _, err := stdin.Write([]byte{1}); err != nil {
			t.Fatalf("release %s helper: %v (stderr: %s)", role, err, stderr.String())
		}
		_ = stdin.Close()
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, bufio.NewReader(stdout), release
}

func readHelperLine(t *testing.T, reader *bufio.Reader, role string) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read %s helper handshake: %v", role, err)
	}
	return strings.TrimSpace(line)
}

func waitRegistryHelper(t *testing.T, cmd *exec.Cmd, role string) {
	t.Helper()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%s helper failed: %v", role, err)
	}
}

func TestCrossProcessConditionalCleanupSerializesReplacementWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows registry removal deliberately fails closed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	const handle = "serialized"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, PID: 1, Type: PeerTypeDaemon, InstanceToken: "old"}); err != nil {
		t.Fatal(err)
	}
	ctrlPath := filepath.Join(SwarmPath(DefaultSwarmName), "peers", handle+".ctrl")
	if err := os.WriteFile(ctrlPath, []byte("old-resource"), filePerm); err != nil {
		t.Fatal(err)
	}

	cleanup, cleanupOut, releaseCleanup := startRegistryHelper(t, home, "cleanup", handle)
	if line := readHelperLine(t, cleanupOut, "cleanup"); line != "CLEANUP_ENTERED" {
		t.Fatalf("cleanup handshake = %q, want CLEANUP_ENTERED", line)
	}

	writer, writerOut, _ := startRegistryHelper(t, home, "writer", handle)
	if line := readHelperLine(t, writerOut, "writer"); line != "WRITER_ATTEMPTING_LOCK" {
		t.Fatalf("writer handshake = %q, want WRITER_ATTEMPTING_LOCK", line)
	}
	writerPublished := make(chan string, 1)
	go func() {
		line, _ := writerOut.ReadString('\n')
		writerPublished <- strings.TrimSpace(line)
	}()
	select {
	case line := <-writerPublished:
		t.Fatalf("replacement writer completed while cleanup held the cross-process lock: %q", line)
	case <-time.After(250 * time.Millisecond):
	}
	releaseCleanup()
	if line := readHelperLine(t, cleanupOut, "cleanup"); line != "CLEANUP_DONE" {
		t.Fatalf("cleanup completion = %q, want CLEANUP_DONE", line)
	}
	waitRegistryHelper(t, cleanup, "cleanup")
	select {
	case line := <-writerPublished:
		if line != "WRITER_PUBLISHED" {
			t.Fatalf("writer completion = %q, want WRITER_PUBLISHED", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement writer did not complete after cleanup released the lock")
	}
	waitRegistryHelper(t, writer, "writer")

	got, err := GetPeer(DefaultSwarmName, handle)
	if err != nil || got == nil || got.InstanceToken != "new" {
		t.Fatalf("serialized replacement not retained: peer=%+v err=%v", got, err)
	}
	resource, err := os.ReadFile(ctrlPath)
	if err != nil || string(resource) != "replacement-resource" {
		t.Fatalf("replacement resource lost: content=%q err=%v", resource, err)
	}
}

func TestRegistryLockAcquisitionFailureDoesNotMutateState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const handle = "lock-failure"
	original := PeerPresence{
		Handle:        handle,
		PID:           1,
		Type:          PeerTypeDaemon,
		Status:        "original-status",
		InstanceToken: "old",
	}
	if err := JoinSwarm(DefaultSwarmName, original); err != nil {
		t.Fatal(err)
	}
	ctrlPath := filepath.Join(SwarmPath(DefaultSwarmName), "peers", handle+".ctrl")
	if err := os.WriteFile(ctrlPath, []byte("original-resource"), filePerm); err != nil {
		t.Fatal(err)
	}

	realAcquire := registryAcquireHandleLock
	registryAcquireHandleLock = func(string) (func() error, error) {
		return nil, errors.New("injected registry lock failure")
	}
	t.Cleanup(func() { registryAcquireHandleLock = realAcquire })

	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, InstanceToken: "new"}); !containsError(err, "injected registry lock failure") {
		t.Fatalf("publication did not fail closed on lock failure: %v", err)
	}
	if err := UpdateStatus(DefaultSwarmName, handle, "mutated-status", "mutated-task"); !containsError(err, "injected registry lock failure") {
		t.Fatalf("status writer did not fail closed on lock failure: %v", err)
	}
	removed, err := RemovePeerIfInstance(DefaultSwarmName, handle, "old")
	if removed || !containsError(err, "injected registry lock failure") {
		t.Fatalf("cleanup did not fail closed on lock failure: removed=%v err=%v", removed, err)
	}

	got, err := GetPeer(DefaultSwarmName, handle)
	if err != nil || got == nil || got.InstanceToken != "old" || got.Status != "original-status" {
		t.Fatalf("presence mutated after lock failures: peer=%+v err=%v", got, err)
	}
	resource, err := os.ReadFile(ctrlPath)
	if err != nil || string(resource) != "original-resource" {
		t.Fatalf("resource mutated after lock failures: content=%q err=%v", resource, err)
	}
}

// TestPeerHandleTransactionGetPublishRoundTrip proves the basic single-
// process contract: Get() reads the current on-disk presence through the
// held lock, and Publish() writes a new presence for the SAME handle via the
// already-locked atomic writer — a plain round trip with no lock contention
// involved, exercising the happy path the cross-process tests below build on.
func TestPeerHandleTransactionGetPublishRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const handle = "tx-roundtrip"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, PID: 1, Status: "initial"}); err != nil {
		t.Fatal(err)
	}

	err := WithPeerHandleTransaction(DefaultSwarmName, handle, func(tx *PeerHandleTransaction) error {
		current, err := tx.Get()
		if err != nil {
			return err
		}
		if current == nil || current.Status != "initial" {
			return fmt.Errorf("unexpected transaction Get() result: %+v", current)
		}
		current.Status = "updated-in-transaction"
		return tx.Publish(*current)
	})
	if err != nil {
		t.Fatalf("WithPeerHandleTransaction: %v", err)
	}

	got, err := GetPeer(DefaultSwarmName, handle)
	if err != nil || got == nil || got.Status != "updated-in-transaction" {
		t.Fatalf("transaction publish not observed: peer=%+v err=%v", got, err)
	}
}

// TestPeerHandleTransactionPublishRejectsHandleMismatch proves Publish fails
// closed — and never touches disk — when a callback tries to publish a
// presence for a handle other than the one the transaction's lock actually
// covers. Without this check a careless (or exploited) callback could reuse
// a *PeerHandleTransaction to write under a completely different handle's
// path while holding a lock that never actually serialized against that
// other handle's writers.
func TestPeerHandleTransactionPublishRejectsHandleMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const handle = "tx-mismatch"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, PID: 1, Status: "original"}); err != nil {
		t.Fatal(err)
	}

	err := WithPeerHandleTransaction(DefaultSwarmName, handle, func(tx *PeerHandleTransaction) error {
		return tx.Publish(PeerPresence{Handle: "someone-else", Status: "hijacked"})
	})
	if err == nil || !containsError(err, "does not match locked handle") {
		t.Fatalf("mismatched-handle publish did not fail closed: %v", err)
	}

	got, err := GetPeer(DefaultSwarmName, handle)
	if err != nil || got == nil || got.Status != "original" {
		t.Fatalf("locked handle mutated despite rejected mismatched publish: peer=%+v err=%v", got, err)
	}
	other, err := GetPeer(DefaultSwarmName, "someone-else")
	if err != nil || other != nil {
		t.Fatalf("mismatched handle must never be written: peer=%+v err=%v", other, err)
	}
}

// TestPeerHandleTransactionLockFailureDoesNotInvokeCallback proves
// WithPeerHandleTransaction fails closed exactly like every other writer
// that goes through withRegistryHandle: when the stable OS lock cannot be
// acquired, the callback must never run (so it can never mutate or observe
// partially-locked state), and pre-existing on-disk presence must be
// unchanged.
func TestPeerHandleTransactionLockFailureDoesNotInvokeCallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const handle = "tx-lock-failure"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, PID: 1, Status: "original"}); err != nil {
		t.Fatal(err)
	}

	realAcquire := registryAcquireHandleLock
	registryAcquireHandleLock = func(string) (func() error, error) {
		return nil, errors.New("injected transaction lock failure")
	}
	t.Cleanup(func() { registryAcquireHandleLock = realAcquire })

	callbackRan := false
	err := WithPeerHandleTransaction(DefaultSwarmName, handle, func(tx *PeerHandleTransaction) error {
		callbackRan = true
		return nil
	})
	if !containsError(err, "injected transaction lock failure") {
		t.Fatalf("WithPeerHandleTransaction did not fail closed on lock failure: %v", err)
	}
	if callbackRan {
		t.Fatal("callback ran despite a lock-acquisition failure")
	}

	got, err := GetPeer(DefaultSwarmName, handle)
	if err != nil || got == nil || got.Status != "original" {
		t.Fatalf("presence mutated after transaction lock failure: peer=%+v err=%v", got, err)
	}
}

// TestPeerHandleTransactionCallbackFailurePropagatesAndReleasesLock proves
// two things at once: (1) a callback error is returned verbatim to the
// WithPeerHandleTransaction caller (fail-closed propagation — errors are
// never swallowed), and (2) the lock is still released afterward — a second,
// independent transaction for the SAME handle must be able to proceed
// immediately, proving a failed callback never leaks the lock.
func TestPeerHandleTransactionCallbackFailurePropagatesAndReleasesLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const handle = "tx-callback-failure"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, PID: 1, Status: "original"}); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("injected callback failure")
	err := WithPeerHandleTransaction(DefaultSwarmName, handle, func(tx *PeerHandleTransaction) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("callback error not propagated verbatim: %v", err)
	}

	// The lock must have been released: a follow-up transaction for the same
	// handle must succeed without blocking.
	entered := false
	if err := WithPeerHandleTransaction(DefaultSwarmName, handle, func(tx *PeerHandleTransaction) error {
		entered = true
		return nil
	}); err != nil {
		t.Fatalf("follow-up transaction failed after prior callback error: %v", err)
	}
	if !entered {
		t.Fatal("follow-up transaction callback never ran; lock appears leaked")
	}
}

// TestCrossProcessPeerHandleTransactionSerializesSameHandle is the
// subprocess-handshake proof that WithPeerHandleTransaction genuinely
// serializes two different OS processes on the same handle: process A opens
// a transaction and blocks inside its callback until released; process B
// attempts a transaction for the SAME handle and must not announce entry
// until A releases. This is the deterministic-barrier equivalent (via the
// TX_ENTERED handshake line, not a sleep/timing ratio) of the existing
// TestCrossProcessConditionalCleanupSerializesReplacementWriter, extended to
// cover the new transaction API directly rather than only the pre-existing
// cleanup/writer pair.
func TestCrossProcessPeerHandleTransactionSerializesSameHandle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows registry removal deliberately fails closed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	const handle = "tx-serialized"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: handle, PID: 1, Status: "original"}); err != nil {
		t.Fatal(err)
	}

	holder, holderOut, releaseHolder := startRegistryHelper(t, home, "tx-hold", handle)
	if line := readHelperLine(t, holderOut, "tx-hold"); line != "TX_ENTERED" {
		t.Fatalf("tx-hold handshake = %q, want TX_ENTERED", line)
	}

	attempt, attemptOut, _ := startRegistryHelper(t, home, "tx-attempt", handle)
	attemptEntered := make(chan string, 1)
	go func() {
		line, _ := attemptOut.ReadString('\n')
		attemptEntered <- strings.TrimSpace(line)
	}()
	select {
	case line := <-attemptEntered:
		t.Fatalf("second same-handle transaction entered while the first was still open: %q", line)
	case <-time.After(250 * time.Millisecond):
	}

	releaseHolder()
	if line := readHelperLine(t, holderOut, "tx-hold"); line != "TX_DONE" {
		t.Fatalf("tx-hold completion = %q, want TX_DONE", line)
	}
	waitRegistryHelper(t, holder, "tx-hold")

	select {
	case line := <-attemptEntered:
		if line != "TX_ENTERED" {
			t.Fatalf("second transaction handshake = %q, want TX_ENTERED", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second same-handle transaction never entered after the first released")
	}
	if line := readHelperLine(t, attemptOut, "tx-attempt"); line != "TX_DONE" {
		t.Fatalf("tx-attempt completion = %q, want TX_DONE", line)
	}
	waitRegistryHelper(t, attempt, "tx-attempt")
}

// TestCrossProcessPeerHandleTransactionDifferentHandleIsIndependent proves
// the flip side: while process A holds an open transaction for one handle,
// a second process opening a transaction for a DIFFERENT handle must not be
// blocked by A at all — per-handle locks (registryHandleMutex is keyed by
// path, and the stable OS lock file is per-handle) must not serialize
// unrelated handles against each other.
func TestCrossProcessPeerHandleTransactionDifferentHandleIsIndependent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows registry removal deliberately fails closed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	const heldHandle = "tx-independent-held"
	const otherHandle = "tx-independent-other"
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: heldHandle, PID: 1, Status: "original"}); err != nil {
		t.Fatal(err)
	}
	if err := JoinSwarm(DefaultSwarmName, PeerPresence{Handle: otherHandle, PID: 1, Status: "original"}); err != nil {
		t.Fatal(err)
	}

	holder, holderOut, releaseHolder := startRegistryHelper(t, home, "tx-hold", heldHandle)
	if line := readHelperLine(t, holderOut, "tx-hold"); line != "TX_ENTERED" {
		t.Fatalf("tx-hold handshake = %q, want TX_ENTERED", line)
	}

	other, otherOut, _ := startRegistryHelper(t, home, "tx-attempt", otherHandle)
	if line := readHelperLine(t, otherOut, "tx-attempt"); line != "TX_ENTERED" {
		t.Fatalf("independent-handle transaction did not enter promptly while an unrelated handle was locked: %q", line)
	}
	if line := readHelperLine(t, otherOut, "tx-attempt"); line != "TX_DONE" {
		t.Fatalf("independent-handle transaction completion = %q, want TX_DONE", line)
	}
	waitRegistryHelper(t, other, "tx-attempt")

	releaseHolder()
	if line := readHelperLine(t, holderOut, "tx-hold"); line != "TX_DONE" {
		t.Fatalf("tx-hold completion = %q, want TX_DONE", line)
	}
	waitRegistryHelper(t, holder, "tx-hold")
}
