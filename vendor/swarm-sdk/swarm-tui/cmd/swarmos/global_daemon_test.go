package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/gateway"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	attserver "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/server"
)

func TestGlobalDaemonHandle_StableNonEmpty(t *testing.T) {
	if globalDaemonHandle == "" {
		t.Fatal("global daemon handle must not be empty (cron jobs target it)")
	}
}

func TestDaemonGatewayDefaultsToLAN(t *testing.T) {
	if gateway.IsLoopbackAddr(defaultDaemonGatewayAddr) {
		t.Fatalf("default daemon gateway %q must be LAN-discoverable", defaultDaemonGatewayAddr)
	}
	if defaultDaemonGatewayAddr != ":8787" {
		t.Fatalf("default daemon gateway %q, want :8787", defaultDaemonGatewayAddr)
	}
	unit := buildUnitFile("/tmp/swarm")
	if !strings.Contains(unit, "--gateway-addr "+defaultDaemonGatewayAddr) {
		t.Fatalf("systemd unit does not use LAN gateway default:\n%s", unit)
	}
}

func TestDaemonServeReachable(t *testing.T) {
	// An open listener is reachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	base := "http://" + ln.Addr().String()
	if !daemonServeReachable(base) {
		t.Errorf("expected %s to be reachable", base)
	}

	// A port that was just closed is not reachable.
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen2: %v", err)
	}
	dead := "http://" + ln2.Addr().String()
	ln2.Close()
	if daemonServeReachable(dead) {
		t.Errorf("expected %s to be unreachable after close", dead)
	}

	// Garbage / empty inputs are not reachable.
	if daemonServeReachable("not a url") {
		t.Error("garbage url must be unreachable")
	}
	if daemonServeReachable("") {
		t.Error("empty url must be unreachable")
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("the current process must report alive")
	}
	if processAlive(0) || processAlive(-1) {
		t.Error("non-positive pids must report not-alive")
	}
	// A pid that is almost certainly not a live process.
	if processAlive(1 << 30) {
		t.Error("absurdly high pid must report not-alive")
	}
}

// ── Manual-stop identity hardening (ADR-005 "Identity proof") ──────────────
//
// These tests exercise validateDaemonStopIdentity and stopDaemonBySignal
// directly, using withTempHome (daemon_lock_test.go) for filesystem
// isolation and the injectable processAliveFunc/signalProcessFunc/
// daemonStopSleepFunc seams (global_daemon.go) so nothing here spawns,
// signals, or sleeps against a real OS process — fully deterministic and
// safe under -race.

// daemonTestPeer builds a minimal, otherwise-valid daemon presence record
// for a given handle/pid.
func daemonTestPeer(handle string, pid int) *a2a.PeerPresence {
	return &a2a.PeerPresence{
		Handle:        handle,
		PID:           pid,
		Type:          a2a.PeerTypeDaemon,
		ServeURL:      "http://test-health",
		InstanceToken: "tok",
		ProcessStart:  "test-start",
		Executable:    "/test/swarmos",
	}
}

func syncPeerFromLock(t *testing.T, handle string, peer *a2a.PeerPresence) {
	t.Helper()
	id, err := readDaemonLockIdentity(daemonInstanceLockName(handle))
	if err != nil || id == nil {
		t.Fatalf("read instance identity: id=%+v err=%v", id, err)
	}
	peer.PID = id.PID
	peer.InstanceToken = id.Token
	peer.ProcessStart = id.ProcessStart
	peer.Executable = id.Executable
}

func installIdentitySeams(t *testing.T, peer *a2a.PeerPresence) {
	t.Helper()
	processEvidenceFunc = func(pid int) (string, string, string, error) {
		return peer.ProcessStart, peer.Executable, currentUID(t), nil
	}
	daemonHealthEvidenceFunc = func(string) (daemonHealthEvidence, error) {
		return daemonHealthEvidence{
			Handle:        peer.Handle,
			PID:           peer.PID,
			InstanceToken: peer.InstanceToken,
			ProcessStart:  peer.ProcessStart,
			Executable:    peer.Executable,
		}, nil
	}
}

// currentUID returns this process's user id string (os/user.Current().Uid),
// skipping the test if it cannot be resolved (some minimal/sandboxed CI
// environments lack /etc/passwd lookups even though the process itself is
// perfectly real).
func currentUID(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skipf("user.Current unavailable: %v", err)
	}
	return u.Uid
}

// resetStopSeams restores the four injectable seams stopDaemonBySignal uses
// (processAliveFunc, signalProcessFunc, daemonStopSleepFunc,
// daemonStopNowFunc) to their real implementations, returning a cleanup
// func. Tests must defer the returned func so an override in one test never
// leaks into the next.
func resetStopSeams(t *testing.T) (restore func()) {
	t.Helper()
	origAlive := processAliveFunc
	origSignal := signalProcessFunc
	origSleep := daemonStopSleepFunc
	origNow := daemonStopNowFunc
	origEvidence := processEvidenceFunc
	origHealth := daemonHealthEvidenceFunc
	return func() {
		processAliveFunc = origAlive
		signalProcessFunc = origSignal
		daemonStopSleepFunc = origSleep
		daemonStopNowFunc = origNow
		processEvidenceFunc = origEvidence
		daemonHealthEvidenceFunc = origHealth
	}
}

func TestValidateDaemonStopIdentity_NilPeer(t *testing.T) {
	withTempHome(t)
	if err := validateDaemonStopIdentity("h", nil); !errors.Is(err, errStopUnknownPeer) {
		t.Fatalf("err = %v, want errStopUnknownPeer", err)
	}
}

func TestValidateDaemonStopIdentity_WrongPeerType(t *testing.T) {
	withTempHome(t)
	peer := &a2a.PeerPresence{Handle: "h", PID: 111, Type: a2a.PeerTypeLocal}
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopWrongPeerType) {
		t.Fatalf("err = %v, want errStopWrongPeerType", err)
	}
}

func TestValidateDaemonStopIdentity_NoRecordedPID(t *testing.T) {
	withTempHome(t)
	peer := daemonTestPeer("h", 0)
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopNoRecordedPID) {
		t.Fatalf("err = %v, want errStopNoRecordedPID", err)
	}
}

func TestValidateDaemonStopIdentity_NoInstanceRecord(t *testing.T) {
	withTempHome(t)
	// A presence record with no corresponding instance-lock stamp at all —
	// e.g. a daemon from before this hardening, or a forged/copied presence
	// file. There is nothing to corroborate the pid against, so it must be
	// refused rather than trusted.
	peer := daemonTestPeer("h", 4242)
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopNoInstanceRecord) {
		t.Fatalf("err = %v, want errStopNoInstanceRecord", err)
	}
}

func TestValidateDaemonStopIdentity_MalformedInstanceRecord(t *testing.T) {
	withTempHome(t)
	writeRawLockFile(t, daemonInstanceLockName("h"), []byte("not json at all"))
	peer := daemonTestPeer("h", 4242)
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopMalformedRecord) {
		t.Fatalf("err = %v, want errStopMalformedRecord", err)
	}
}

func TestValidateDaemonStopIdentity_PIDMismatch(t *testing.T) {
	withTempHome(t)
	// The instance lock is genuinely held by pid 999, but presence claims a
	// different pid — a stale presence record or a reused pid number. This
	// is the exact "wrong/reused PID" case ADR-005 requires to be refused.
	writeLockIdentity(t, daemonInstanceLockName("h"), daemonLockIdentity{PID: 999, UID: currentUID(t), Token: "tok"})
	peer := daemonTestPeer("h", 111)
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopPIDMismatch) {
		t.Fatalf("err = %v, want errStopPIDMismatch", err)
	}
}

func TestValidateDaemonStopIdentity_WrongUser(t *testing.T) {
	withTempHome(t)
	writeLockIdentity(t, daemonInstanceLockName("h"), daemonLockIdentity{PID: 111, UID: "impossible-uid-999999", Token: "tok"})
	peer := daemonTestPeer("h", 111)
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopWrongUser) {
		t.Fatalf("err = %v, want errStopWrongUser", err)
	}
}

func TestValidateDaemonStopIdentity_BadSocketPath(t *testing.T) {
	withTempHome(t)
	defer resetStopSeams(t)()
	writeLockIdentity(t, daemonInstanceLockName("h"), daemonLockIdentity{PID: 111, UID: currentUID(t), Token: "tok"})
	peer := daemonTestPeer("h", 111)
	installIdentitySeams(t, peer)
	peer.ControlSocket = filepath.Join(t.TempDir(), "not-canonical.ctrl")
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopBadSocketPath) {
		t.Fatalf("err = %v, want errStopBadSocketPath", err)
	}
}

func TestValidateDaemonStopIdentity_SocketNotASocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("socket-type guard is unix-only (see validateDaemonStopIdentity)")
	}
	withTempHome(t)
	defer resetStopSeams(t)()
	writeLockIdentity(t, daemonInstanceLockName("h"), daemonLockIdentity{PID: 111, UID: currentUID(t), Token: "tok"})
	peer := daemonTestPeer("h", 111)
	installIdentitySeams(t, peer)
	peer.ControlSocket = expectedControlSocketPath("h")
	if err := os.MkdirAll(filepath.Dir(peer.ControlSocket), 0o755); err != nil {
		t.Fatalf("mkdir peers dir: %v", err)
	}
	// A crafted/foreign presence pointing ControlSocket at an ordinary file
	// must never be treated as an owned socket artifact.
	if err := os.WriteFile(peer.ControlSocket, []byte("not a socket"), 0o644); err != nil {
		t.Fatalf("write fake socket file: %v", err)
	}
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopBadSocketType) {
		t.Fatalf("err = %v, want errStopBadSocketType", err)
	}
}

// TestValidateDaemonStopIdentity_ValidManualFallback is the positive
// control: a real instance-lock hold (stamped by acquireDaemonLock, exactly
// as a real `swarmos daemon` process does at startup) plus a matching daemon
// presence and canonical, real, same-owner control socket must pass every
// guard.
func TestValidateDaemonStopIdentity_ValidManualFallback(t *testing.T) {
	withTempHome(t)
	defer resetStopSeams(t)()
	handle := "h"

	release, ok, err := acquireDaemonLock(daemonInstanceLockName(handle), false)
	if err != nil || !ok {
		t.Fatalf("acquire instance lock: ok=%v err=%v", ok, err)
	}
	defer release()

	peer := daemonTestPeer(handle, os.Getpid())
	syncPeerFromLock(t, handle, peer)
	installIdentitySeams(t, peer)
	sockPath := expectedControlSocketPath(handle)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		t.Fatalf("mkdir peers dir: %v", err)
	}
	if runtime.GOOS != "windows" {
		ln, lerr := net.Listen("unix", sockPath)
		if lerr != nil {
			t.Fatalf("listen unix socket: %v", lerr)
		}
		defer ln.Close()
	}
	peer.ControlSocket = sockPath

	if err := validateDaemonStopIdentity(handle, peer); err != nil {
		t.Fatalf("expected the valid manual fallback identity to pass, got: %v", err)
	}
}

// ── stopDaemonBySignal end-to-end (validation + injectable liveness/signal) ─

func TestStopDaemonBySignal_UnknownPeerNotFound(t *testing.T) {
	withTempHome(t)
	if err := stopDaemonBySignal("does-not-exist"); err == nil {
		t.Fatal("expected an error for a daemon with no presence record")
	}
}

func TestStopDaemonBySignal_DeniesWrongPeerType(t *testing.T) {
	withTempHome(t)
	handle := "h"
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, a2a.PeerPresence{Handle: handle, PID: 12345, Type: a2a.PeerTypeLocal}); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}
	defer resetStopSeams(t)()
	signalCalled := false
	signalProcessFunc = func(int, syscall.Signal) error { signalCalled = true; return nil }

	err := stopDaemonBySignal(handle)
	if !errors.Is(err, errStopWrongPeerType) {
		t.Fatalf("err = %v, want errStopWrongPeerType", err)
	}
	if signalCalled {
		t.Fatal("must never signal a non-daemon peer")
	}
}

func TestStopDaemonBySignal_DeniesWithoutInstanceRecord(t *testing.T) {
	withTempHome(t)
	handle := "h"
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, *daemonTestPeer(handle, 99999)); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}
	defer resetStopSeams(t)()
	signalCalled := false
	signalProcessFunc = func(int, syscall.Signal) error { signalCalled = true; return nil }
	processAliveFunc = func(int) bool { return true }

	err := stopDaemonBySignal(handle)
	if !errors.Is(err, errStopNoInstanceRecord) {
		t.Fatalf("err = %v, want errStopNoInstanceRecord", err)
	}
	if signalCalled {
		t.Fatal("must never signal when identity cannot be corroborated")
	}
}

func TestStopDaemonBySignal_DeniesPIDMismatch(t *testing.T) {
	withTempHome(t)
	handle := "h"
	release, ok, err := acquireDaemonLock(daemonInstanceLockName(handle), false)
	if err != nil || !ok {
		t.Fatalf("acquire instance lock: ok=%v err=%v", ok, err)
	}
	defer release()

	// Presence claims a pid different from the one actually holding the
	// instance lock (this test process) — stale presence / reused pid.
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, *daemonTestPeer(handle, os.Getpid()+999999)); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}
	defer resetStopSeams(t)()
	signalCalled := false
	signalProcessFunc = func(int, syscall.Signal) error { signalCalled = true; return nil }

	err = stopDaemonBySignal(handle)
	if !errors.Is(err, errStopPIDMismatch) {
		t.Fatalf("err = %v, want errStopPIDMismatch", err)
	}
	if signalCalled {
		t.Fatal("must never signal on pid mismatch")
	}
}

// TestStopDaemonBySignal_DeniesStaleLockAtPreSignalCheck proves the
// immediate pre-signal daemonInstanceLockHeld probe catches a stale record
// even when validateDaemonStopIdentity's earlier pid-match check passed:
// the instance lock's identity content matches presence, but nobody
// currently HOLDS that lock (the process that wrote it has already exited —
// its pid could already have been reused by an unrelated process). This is
// the TOCTOU-closing guard, exercised independently of the OS liveness
// probe (which is forced "alive" here) so it is proven to fire on its own.
func TestStopDaemonBySignal_DeniesStaleLockAtPreSignalCheck(t *testing.T) {
	withTempHome(t)
	handle := "h"
	writeLockIdentity(t, daemonInstanceLockName(handle), daemonLockIdentity{PID: 99999, UID: currentUID(t), Token: "tok"})
	peer := daemonTestPeer(handle, 99999)
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, *peer); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}
	defer resetStopSeams(t)()
	installIdentitySeams(t, peer)
	signalCalled := false
	signalProcessFunc = func(int, syscall.Signal) error { signalCalled = true; return nil }
	processAliveFunc = func(int) bool { return true } // OS still reports something at this (possibly reused) pid

	err := stopDaemonBySignal(handle)
	if !errors.Is(err, errStopStaleLock) {
		t.Fatalf("err = %v, want errStopStaleLock", err)
	}
	if signalCalled {
		t.Fatal("must never signal a stale/unheld instance lock record")
	}
}

// TestStopDaemonBySignal_DeniesNotLiveImmediatelyBeforeSignal proves the
// immediate pre-signal OS-liveness probe (distinct from the instance-lock
// probe above) independently refuses to signal when it finds the pid gone,
// even though the instance lock is still genuinely held.
func TestStopDaemonBySignal_DeniesNotLiveImmediatelyBeforeSignal(t *testing.T) {
	withTempHome(t)
	handle := "h"
	release, ok, err := acquireDaemonLock(daemonInstanceLockName(handle), false)
	if err != nil || !ok {
		t.Fatalf("acquire instance lock: ok=%v err=%v", ok, err)
	}
	defer release()

	peer := daemonTestPeer(handle, os.Getpid())
	syncPeerFromLock(t, handle, peer)
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, *peer); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}
	defer resetStopSeams(t)()
	signalCalled := false
	signalProcessFunc = func(int, syscall.Signal) error { signalCalled = true; return nil }
	processEvidenceFunc = func(int) (string, string, string, error) {
		return "", "", "", errors.New("gone")
	}

	err = stopDaemonBySignal(handle)
	if !errors.Is(err, errStopNotLiveBeforeSignal) {
		t.Fatalf("err = %v, want errStopNotLiveBeforeSignal", err)
	}
	if signalCalled {
		t.Fatal("must never signal when the immediate pre-signal liveness probe fails")
	}
}

// TestStopDaemonBySignal_ValidManualFallback_GracefulExit is the positive
// control for the full stop path: a real instance-lock hold plus a matching
// canonical daemon presence must be signalled exactly once (SIGTERM) and
// treated as successfully stopped when the injected liveness probe reports
// exit right away — no sleeping, no SIGKILL escalation needed.
func TestStopDaemonBySignal_ValidManualFallback_GracefulExit(t *testing.T) {
	withTempHome(t)
	handle := "h"

	release, ok, err := acquireDaemonLock(daemonInstanceLockName(handle), false)
	if err != nil || !ok {
		t.Fatalf("acquire instance lock: ok=%v err=%v", ok, err)
	}
	defer release()

	sockPath := expectedControlSocketPath(handle)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		t.Fatalf("mkdir peers dir: %v", err)
	}
	if runtime.GOOS != "windows" {
		ln, lerr := net.Listen("unix", sockPath)
		if lerr != nil {
			t.Fatalf("listen unix socket: %v", lerr)
		}
		defer ln.Close()
	}

	peer := daemonTestPeer(handle, os.Getpid())
	syncPeerFromLock(t, handle, peer)
	peer.ControlSocket = sockPath
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, *peer); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}

	defer resetStopSeams(t)()
	installIdentitySeams(t, peer)
	var sentSignals []syscall.Signal
	signalProcessFunc = func(pid int, sig syscall.Signal) error {
		sentSignals = append(sentSignals, sig)
		return nil
	}
	evidenceCalls := 0
	processEvidenceFunc = func(int) (string, string, string, error) {
		evidenceCalls++
		if evidenceCalls == 1 {
			return peer.ProcessStart, peer.Executable, currentUID(t), nil
		}
		return "", "", "", errors.New("exited")
	}
	daemonStopSleepFunc = func(time.Duration) {
		t.Fatal("must not sleep in a deterministic graceful-exit test")
	}

	if err := stopDaemonBySignal(handle); err != nil {
		t.Fatalf("expected the valid manual fallback to succeed, got: %v", err)
	}
	if len(sentSignals) != 1 || sentSignals[0] != syscall.SIGTERM {
		t.Fatalf("expected exactly one SIGTERM and no SIGKILL, got %v", sentSignals)
	}
}

// TestStopDaemonBySignal_EscalatesToSIGKILLAfterDeadline proves a daemon
// that never reports exit is escalated to SIGKILL after the poll loop, and
// that presence/socket artifacts are removed ONLY at that point — using the
// injectable sleep seam so the 5s deadline elapses without a real sleep.
func TestStopDaemonBySignal_EscalatesToSIGKILLAfterDeadline(t *testing.T) {
	withTempHome(t)
	handle := "h"

	release, ok, err := acquireDaemonLock(daemonInstanceLockName(handle), false)
	if err != nil || !ok {
		t.Fatalf("acquire instance lock: ok=%v err=%v", ok, err)
	}
	defer release()

	peer := daemonTestPeer(handle, os.Getpid())
	syncPeerFromLock(t, handle, peer)
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, *peer); err != nil {
		t.Fatalf("JoinSwarm: %v", err)
	}

	defer resetStopSeams(t)()
	installIdentitySeams(t, peer)
	var sentSignals []syscall.Signal
	signalProcessFunc = func(pid int, sig syscall.Signal) error {
		sentSignals = append(sentSignals, sig)
		return nil
	}
	processEvidenceFunc = func(int) (string, string, string, error) {
		if len(sentSignals) >= 2 {
			return "", "", "", errors.New("exited after kill")
		}
		return peer.ProcessStart, peer.Executable, currentUID(t), nil
	}
	// Advance a fake clock in lockstep with the (also faked) poll sleep, so
	// the loop's real 5s deadline elapses instantly instead of the test
	// burning 5 real wall-clock seconds in a busy loop.
	fakeNow := time.Now()
	daemonStopNowFunc = func() time.Time { return fakeNow }
	daemonStopSleepFunc = func(d time.Duration) {
		fakeNow = fakeNow.Add(d)
	}

	if err := stopDaemonBySignal(handle); err != nil {
		t.Fatalf("expected SIGKILL escalation to still report success, got: %v", err)
	}
	if len(sentSignals) != 2 || sentSignals[0] != syscall.SIGTERM || sentSignals[1] != syscall.SIGKILL {
		t.Fatalf("expected SIGTERM then SIGKILL, got %v", sentSignals)
	}

	// The daemon "never exited" from processAliveFunc's perspective, so the
	// escalation path's own cleanup should have cleared its presence.
	if peer, gerr := a2a.GetPeer(a2a.DefaultSwarmName, handle); gerr != nil || peer != nil {
		t.Fatalf("expected presence to be cleared after forced SIGKILL cleanup, got peer=%+v err=%v", peer, gerr)
	}
}

func setupValidStopPeer(t *testing.T, handle string) (*a2a.PeerPresence, func()) {
	t.Helper()
	release, ok, err := acquireDaemonLock(daemonInstanceLockName(handle), false)
	if err != nil || !ok {
		t.Fatalf("acquire instance lock: ok=%v err=%v", ok, err)
	}
	peer := daemonTestPeer(handle, os.Getpid())
	syncPeerFromLock(t, handle, peer)
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, *peer); err != nil {
		release()
		t.Fatalf("publish presence: %v", err)
	}
	return peer, release
}

func TestValidateDaemonStopIdentity_DeniesHealthEvidenceMismatch(t *testing.T) {
	withTempHome(t)
	defer resetStopSeams(t)()
	peer := daemonTestPeer("h", 111)
	writeLockIdentity(t, daemonInstanceLockName("h"), daemonLockIdentity{PID: 111})
	installIdentitySeams(t, peer)
	daemonHealthEvidenceFunc = func(string) (daemonHealthEvidence, error) {
		return daemonHealthEvidence{Handle: "replacement", PID: peer.PID, InstanceToken: peer.InstanceToken, ProcessStart: peer.ProcessStart, Executable: peer.Executable}, nil
	}
	if err := validateDaemonStopIdentity("h", peer); !errors.Is(err, errStopHealthMismatch) {
		t.Fatalf("err=%v, want health mismatch denial", err)
	}
}

func TestStopDaemonBySignal_TermSignalErrorPreservesPublication(t *testing.T) {
	withTempHome(t)
	defer resetStopSeams(t)()
	peer, release := setupValidStopPeer(t, "h")
	defer release()
	installIdentitySeams(t, peer)
	signalProcessFunc = func(int, syscall.Signal) error { return errors.New("injected TERM failure") }

	err := stopDaemonBySignal("h")
	if !errors.Is(err, errStopSignalFailed) {
		t.Fatalf("err=%v, want signal failure", err)
	}
	if got, _ := a2a.GetPeer(a2a.DefaultSwarmName, "h"); got == nil || got.InstanceToken != peer.InstanceToken {
		t.Fatalf("signal failure removed owned publication: %+v", got)
	}
}

func TestStopDaemonBySignal_PIDReusePreservesReplacement(t *testing.T) {
	withTempHome(t)
	defer resetStopSeams(t)()
	peer, release := setupValidStopPeer(t, "h")
	defer release()
	installIdentitySeams(t, peer)
	signaled := false
	signalProcessFunc = func(_ int, sig syscall.Signal) error {
		if sig == syscall.SIGTERM {
			signaled = true
			replacement := *peer
			replacement.InstanceToken = "replacement-token"
			replacement.ProcessStart = "replacement-start"
			if err := a2a.JoinSwarm(a2a.DefaultSwarmName, replacement); err != nil {
				t.Fatalf("publish replacement: %v", err)
			}
		}
		return nil
	}
	processEvidenceFunc = func(int) (string, string, string, error) {
		if signaled {
			return "replacement-start", peer.Executable, currentUID(t), nil
		}
		return peer.ProcessStart, peer.Executable, currentUID(t), nil
	}
	if err := stopDaemonBySignal("h"); err != nil {
		t.Fatal(err)
	}
	got, _ := a2a.GetPeer(a2a.DefaultSwarmName, "h")
	if got == nil || got.InstanceToken != "replacement-token" {
		t.Fatalf("PID reuse cleanup deleted replacement: %+v", got)
	}
}

func TestStopDaemonBySignal_ReplacementPublicationDeniesKill(t *testing.T) {
	withTempHome(t)
	defer resetStopSeams(t)()
	peer, release := setupValidStopPeer(t, "h")
	defer release()
	installIdentitySeams(t, peer)
	var signals []syscall.Signal
	signalProcessFunc = func(_ int, sig syscall.Signal) error {
		signals = append(signals, sig)
		return nil
	}
	fakeNow := time.Now()
	replaced := false
	daemonStopNowFunc = func() time.Time { return fakeNow }
	daemonStopSleepFunc = func(d time.Duration) {
		fakeNow = fakeNow.Add(d)
		if !replaced {
			replaced = true
			replacement := *peer
			replacement.InstanceToken = "new-token"
			if err := a2a.JoinSwarm(a2a.DefaultSwarmName, replacement); err != nil {
				t.Fatalf("publish replacement: %v", err)
			}
		}
	}
	err := stopDaemonBySignal("h")
	if !errors.Is(err, errStopTokenMismatch) {
		t.Fatalf("err=%v, want replacement-token denial", err)
	}
	if len(signals) != 1 || signals[0] != syscall.SIGTERM {
		t.Fatalf("replacement must deny KILL; signals=%v", signals)
	}
}

func TestStopDaemonBySignal_KillSignalErrorPreservesPublication(t *testing.T) {
	withTempHome(t)
	defer resetStopSeams(t)()
	peer, release := setupValidStopPeer(t, "h")
	defer release()
	installIdentitySeams(t, peer)
	fakeNow := time.Now()
	daemonStopNowFunc = func() time.Time { return fakeNow }
	daemonStopSleepFunc = func(d time.Duration) { fakeNow = fakeNow.Add(d) }
	signalProcessFunc = func(_ int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			return errors.New("injected KILL failure")
		}
		return nil
	}
	err := stopDaemonBySignal("h")
	if !errors.Is(err, errStopSignalFailed) {
		t.Fatalf("err=%v, want KILL signal failure", err)
	}
	if got, _ := a2a.GetPeer(a2a.DefaultSwarmName, "h"); got == nil || got.InstanceToken != peer.InstanceToken {
		t.Fatalf("KILL failure removed publication: %+v", got)
	}
}

// ─── Phase 01 R4: real socket-startup transaction coverage ─────────────────
//
// claimDaemonControlSocket and publishDaemonAttachEndpoints (daemon_cli.go)
// are the production startup transaction: stale-socket inspection/removal,
// bind, chmod, and socket identity capture (all inside the REAL
// AttachedServer.Listen — never modeled with an unrelated file write) plus
// the presence publication, joined to a2a.WithPeerHandleTransaction exactly
// as CONTRACT.md requires. The tests below exercise the real functions
// end-to-end against a real Unix socket and the real on-disk registry.

func testDaemonIdentity(token string) daemonLockIdentity {
	return daemonLockIdentity{
		PID:          os.Getpid(),
		UID:          "test-uid",
		Token:        token,
		ProcessStart: "process-start-" + token,
		Executable:   "/usr/bin/swarmos-test-" + token,
	}
}

// TestClaimDaemonControlSocketPublishesInstanceEvidenceAndBindsSocket is the
// single-process happy path: sdk.JoinSwarm's prerequisite write (simulated
// here directly, exactly as runDaemon performs it OUTSIDE the transaction)
// followed by claimDaemonControlSocket produces a dialable control socket
// and a presence record carrying the instance evidence and ControlSocket
// path — the same "final publication" shape CONTRACT.md requires.
func TestClaimDaemonControlSocketPublishesInstanceEvidenceAndBindsSocket(t *testing.T) {
	withTempHome(t)
	const handle = "claim-happy-path"
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, a2a.PeerPresence{
		Handle: handle, PID: os.Getpid(), Type: a2a.PeerTypeDaemon, Status: "idle",
	}); err != nil {
		t.Fatalf("prerequisite JoinSwarm: %v", err)
	}
	sockPath := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers", handle+".ctrl")
	ctrlSrv := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	t.Cleanup(ctrlSrv.Stop)

	identity := testDaemonIdentity("evidence-token")
	if err := claimDaemonControlSocket(ctrlSrv, handle, identity); err != nil {
		t.Fatalf("claimDaemonControlSocket: %v", err)
	}

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("claimed control socket was not dialable: %v", err)
	}
	_ = conn.Close()

	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil || peer == nil {
		t.Fatalf("GetPeer after claim: peer=%+v err=%v", peer, err)
	}
	if peer.InstanceToken != identity.Token || peer.ProcessStart != identity.ProcessStart || peer.Executable != identity.Executable {
		t.Fatalf("instance evidence not published: %+v", peer)
	}
	if peer.ControlSocket != sockPath {
		t.Fatalf("ControlSocket = %q, want %q", peer.ControlSocket, sockPath)
	}
}

// TestClaimDaemonControlSocketAmbiguousStaleSocketFailsWithoutMutation
// proves the conservative fail-closed contract: when an existing entry at
// the control-socket path is a real Unix socket but cannot be proven stale
// (dial fails with something other than ECONNREFUSED/ENOENT —
// removeStaleSocket's "ambiguous" branch, here forced deterministically with
// a real permission-denied dial rather than a fake), Listen() refuses to
// replace it, claimDaemonControlSocket never reaches the registry
// transaction's publish step, and the pre-existing presence is left
// completely untouched.
func TestClaimDaemonControlSocketAmbiguousStaleSocketFailsWithoutMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket permission probe")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses socket permission checks")
	}
	withTempHome(t)
	const handle = "claim-ambiguous-stale"
	original := a2a.PeerPresence{
		Handle: handle, PID: os.Getpid(), Type: a2a.PeerTypeDaemon, Status: "idle",
		InstanceToken: "original-token", ProcessStart: "original-start", Executable: "/usr/bin/original",
	}
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, original); err != nil {
		t.Fatalf("prerequisite JoinSwarm: %v", err)
	}
	sockPath := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers", handle+".ctrl")

	// A real bound-then-orphaned Unix socket, permission-tightened to 0000 so
	// a same-user dial fails with permission-denied — neither ECONNREFUSED
	// nor ENOENT, exactly the ambiguous case removeStaleSocket must refuse.
	orphan, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen orphan socket: %v", err)
	}
	orphan.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := orphan.Close(); err != nil {
		t.Fatalf("close orphan socket: %v", err)
	}
	if err := os.Chmod(sockPath, 0o000); err != nil {
		t.Fatalf("chmod orphan socket: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(sockPath) })

	ctrlSrv := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	t.Cleanup(ctrlSrv.Stop)
	err = claimDaemonControlSocket(ctrlSrv, handle, testDaemonIdentity("replacement-token"))
	if err == nil || !strings.Contains(err.Error(), "cannot prove existing socket is stale") {
		t.Fatalf("claimDaemonControlSocket() error = %v, want ambiguous-staleness refusal", err)
	}

	if info, serr := os.Lstat(sockPath); serr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("ambiguous socket was replaced/removed: info=%+v err=%v", info, serr)
	}
	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil || peer == nil || peer.InstanceToken != "original-token" {
		t.Fatalf("presence mutated after ambiguous-staleness refusal: peer=%+v err=%v", peer, err)
	}
}

// TestClaimDaemonControlSocketRollsBackOnHandleMismatchPublication proves
// the identity-checked Stop-before-release contract: claimDaemonControlSocket
// performs a REAL bind, then hits a genuine (not modeled/faked) publication
// failure — a forged presence file whose internal Handle field does not
// match the filename it lives under, which tx.Publish legitimately rejects —
// and must call the production ctrlSrv.Stop() to remove the just-bound
// socket before returning, so no bound-but-abandoned socket is left for a
// later same-handle attempt to trip over.
func TestClaimDaemonControlSocketRollsBackOnHandleMismatchPublication(t *testing.T) {
	withTempHome(t)
	const handle = "claim-rollback"
	peersDir := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers")
	if err := os.MkdirAll(peersDir, 0o700); err != nil {
		t.Fatalf("mkdir peers dir: %v", err)
	}
	forged := filepath.Join(peersDir, handle+".json")
	if err := os.WriteFile(forged, []byte(`{"handle":"someone-else","pid":1,"status":"idle"}`), 0o600); err != nil {
		t.Fatalf("forge mismatched presence: %v", err)
	}
	sockPath := filepath.Join(peersDir, handle+".ctrl")
	ctrlSrv := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	t.Cleanup(ctrlSrv.Stop)

	err := claimDaemonControlSocket(ctrlSrv, handle, testDaemonIdentity("rollback-token"))
	if err == nil || !strings.Contains(err.Error(), "does not match locked handle") {
		t.Fatalf("claimDaemonControlSocket() error = %v, want handle-mismatch publication failure", err)
	}
	if _, serr := os.Lstat(sockPath); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("bound socket was not rolled back after publication failure: %v", serr)
	}
	data, rerr := os.ReadFile(forged)
	if rerr != nil || !strings.Contains(string(data), "someone-else") {
		t.Fatalf("forged presence mutated despite rejected publish: data=%q err=%v", data, rerr)
	}
}

// TestClaimDaemonControlSocketRepeatedStartupReplacesStaleSocket proves a
// realistic restart sequence works end to end through the real transaction
// path: claim, Stop (as runDaemon's shutdown does), then claim again for the
// same handle — the second claim's Listen() must see the first claim's now
// genuinely stale (ECONNREFUSED) socket, replace it, and the final presence
// must reflect only the SECOND claim's instance evidence.
func TestClaimDaemonControlSocketRepeatedStartupReplacesStaleSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows registry removal deliberately fails closed")
	}
	withTempHome(t)
	const handle = "claim-repeated-startup"
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, a2a.PeerPresence{
		Handle: handle, PID: os.Getpid(), Type: a2a.PeerTypeDaemon, Status: "idle",
	}); err != nil {
		t.Fatalf("prerequisite JoinSwarm: %v", err)
	}
	sockPath := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers", handle+".ctrl")

	first := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	if err := claimDaemonControlSocket(first, handle, testDaemonIdentity("first")); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	first.Stop()
	if _, serr := os.Lstat(sockPath); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("first Stop did not remove its own socket: %v", serr)
	}

	second := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	t.Cleanup(second.Stop)
	if err := claimDaemonControlSocket(second, handle, testDaemonIdentity("second")); err != nil {
		t.Fatalf("second (repeated) claim: %v", err)
	}
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("repeated startup socket not dialable: %v", err)
	}
	_ = conn.Close()

	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil || peer == nil || peer.InstanceToken != "second" {
		t.Fatalf("repeated startup did not publish the SECOND claim's evidence: peer=%+v err=%v", peer, err)
	}
}

// TestPublishDaemonAttachEndpointsPreservesInstanceEvidence proves the
// "final presence publication containing ControlSocket and existing
// instance evidence" requirement: publishDaemonAttachEndpoints (run after
// claimDaemonControlSocket, exactly like runDaemon's step 5c) must read the
// instance evidence claimDaemonControlSocket already published via tx.Get()
// and carry it forward unchanged while adding Type/Workspace/ServeURL.
func TestPublishDaemonAttachEndpointsPreservesInstanceEvidence(t *testing.T) {
	withTempHome(t)
	const handle = "publish-endpoints"
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, a2a.PeerPresence{
		Handle: handle, PID: os.Getpid(), Type: a2a.PeerTypeDaemon, Status: "idle",
	}); err != nil {
		t.Fatalf("prerequisite JoinSwarm: %v", err)
	}
	sockPath := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers", handle+".ctrl")
	ctrlSrv := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	t.Cleanup(ctrlSrv.Stop)
	identity := testDaemonIdentity("endpoint-token")
	if err := claimDaemonControlSocket(ctrlSrv, handle, identity); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := publishDaemonAttachEndpoints(ctrlSrv, handle, "/workspace/root", "http://127.0.0.1:9999"); err != nil {
		t.Fatalf("publishDaemonAttachEndpoints: %v", err)
	}

	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil || peer == nil {
		t.Fatalf("GetPeer after final publish: peer=%+v err=%v", peer, err)
	}
	if peer.InstanceToken != identity.Token || peer.ProcessStart != identity.ProcessStart || peer.Executable != identity.Executable {
		t.Fatalf("final publish lost instance evidence: %+v", peer)
	}
	if peer.ControlSocket != sockPath {
		t.Fatalf("final publish lost ControlSocket: %+v", peer)
	}
	if peer.Type != a2a.PeerTypeDaemon || peer.Workspace != "/workspace/root" || peer.ServeURL != "http://127.0.0.1:9999" {
		t.Fatalf("final publish missing endpoint fields: %+v", peer)
	}
}

// TestClaimDaemonControlSocketIndependentHandlesDoNotSerialize is the
// single-process half of the lock-ordering contract: while one goroutine
// holds a real a2a.WithPeerHandleTransaction open for one handle (blocking
// on a channel, standing in for whatever else might hold that same handle's
// registry lock — conditional cleanup or another replacement attempt),
// claimDaemonControlSocket for a COMPLETELY DIFFERENT handle must complete
// promptly. Per-handle locks must never serialize unrelated handles, and
// AttachedServer.Listen()/Stop() (which only ever touch that server's own
// mutex) must never end up nested inside a DIFFERENT handle's still-open
// registry transaction in a way that could deadlock.
func TestClaimDaemonControlSocketIndependentHandlesDoNotSerialize(t *testing.T) {
	withTempHome(t)
	const heldHandle = "lock-order-held"
	const otherHandle = "lock-order-other"
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, a2a.PeerPresence{Handle: heldHandle, PID: os.Getpid(), Type: a2a.PeerTypeDaemon}); err != nil {
		t.Fatalf("JoinSwarm held handle: %v", err)
	}
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, a2a.PeerPresence{Handle: otherHandle, PID: os.Getpid(), Type: a2a.PeerTypeDaemon}); err != nil {
		t.Fatalf("JoinSwarm other handle: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	txErr := make(chan error, 1)
	go func() {
		txErr <- a2a.WithPeerHandleTransaction(a2a.DefaultSwarmName, heldHandle, func(tx *a2a.PeerHandleTransaction) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("held-handle transaction never entered")
	}
	defer func() {
		close(release)
		if err := <-txErr; err != nil {
			t.Fatalf("held-handle transaction: %v", err)
		}
	}()

	sockPath := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers", otherHandle+".ctrl")
	ctrlSrv := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
	t.Cleanup(ctrlSrv.Stop)

	claimDone := make(chan error, 1)
	go func() { claimDone <- claimDaemonControlSocket(ctrlSrv, otherHandle, testDaemonIdentity("independent")) }()
	select {
	case err := <-claimDone:
		if err != nil {
			t.Fatalf("independent-handle claim failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("independent-handle claim blocked behind an unrelated handle's registry transaction")
	}
}

// ─── deterministic real-path subprocess regression ─────────────────────────

// TestDaemonControlSocketCrossProcessHelper is the re-exec entry point used
// by startDaemonControlHelper below (mirrors internal/a2a's
// TestRegistryCrossProcessHelper pattern). It is a no-op under a normal `go
// test` run — it only does anything when SWARMOS_DAEMON_HELPER names a role,
// which the driving test sets when it re-execs this same test binary as a
// separate OS process.
func TestDaemonControlSocketCrossProcessHelper(t *testing.T) {
	role := os.Getenv("SWARMOS_DAEMON_HELPER")
	if role == "" {
		return
	}
	handle := os.Getenv("SWARMOS_DAEMON_HANDLE")
	switch role {
	case "hold":
		// Stands in for "old conditional cleanup holding the lock": a REAL
		// a2a.WithPeerHandleTransaction for handle, which is the exact same
		// per-handle registry lock RemovePeerIfInstance (conditional cleanup)
		// and claimDaemonControlSocket (replacement startup) both use.
		err := a2a.WithPeerHandleTransaction(a2a.DefaultSwarmName, handle, func(tx *a2a.PeerHandleTransaction) error {
			fmt.Println("HOLD_ENTERED")
			var releaseByte [1]byte
			if _, err := os.Stdin.Read(releaseByte[:]); err != nil {
				fmt.Fprintf(os.Stderr, "wait for hold release: %v\n", err)
				os.Exit(2)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "hold: %v\n", err)
			os.Exit(2)
		}
		fmt.Println("HOLD_DONE")
	case "claim":
		// The REAL replacement startup path: a genuine AttachedServer.Listen
		// bind (never modeled with an unrelated file write) plus the real
		// instance-evidence publication, through the exact production
		// function runDaemon calls.
		sockPath := os.Getenv("SWARMOS_DAEMON_SOCK")
		ctrlSrv := attserver.NewAttached(nil, nil, 80, 24, nil, sockPath)
		identity := daemonLockIdentity{
			PID:          os.Getpid(),
			UID:          "helper-uid",
			Token:        "new-token",
			ProcessStart: "new-process-start",
			Executable:   "/usr/bin/swarmos-test-new",
		}
		if err := claimDaemonControlSocket(ctrlSrv, handle, identity); err != nil {
			fmt.Fprintf(os.Stderr, "claim: %v\n", err)
			os.Exit(2)
		}
		// This line can only be reached after claimDaemonControlSocket has
		// fully returned — i.e. after WithPeerHandleTransaction released the
		// per-handle lock, which (per this test's setup) only happens once
		// the "hold" helper's transaction above has released it. Printing it
		// is therefore proof the replacement could not report socket-bound /
		// publication completion any earlier.
		fmt.Println("CLAIM_DONE")
		var releaseByte [1]byte
		if _, err := os.Stdin.Read(releaseByte[:]); err != nil {
			fmt.Fprintf(os.Stderr, "wait for claim release: %v\n", err)
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown helper role %q\n", role)
		os.Exit(2)
	}
}

func startDaemonControlHelper(t *testing.T, home, role, handle, sockPath string) (*exec.Cmd, *bufio.Reader, func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonControlSocketCrossProcessHelper$")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"SWARMOS_DAEMON_HELPER="+role,
		"SWARMOS_DAEMON_HANDLE="+handle,
		"SWARMOS_DAEMON_SOCK="+sockPath,
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

func readDaemonHelperLine(t *testing.T, reader *bufio.Reader, role string) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read %s helper handshake: %v", role, err)
	}
	return strings.TrimSpace(line)
}

func waitDaemonHelper(t *testing.T, cmd *exec.Cmd, role string) {
	t.Helper()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%s helper failed: %v", role, err)
	}
}

// TestCrossProcessDaemonControlSocketClaimSerializesWithHeldTransaction is
// the deterministic real-path subprocess regression Phase 01 CONTRACT.md
// requires: an "old conditional cleanup" stand-in process opens (and holds)
// a REAL a2a.WithPeerHandleTransaction for the handle; while it holds the
// lock, a second process runs the REAL replacement startup path
// (claimDaemonControlSocket — genuine AttachedServer.Listen bind, never an
// unrelated file write standing in for socket creation) and must not be able
// to report completion (its CLAIM_DONE handshake line) until the first
// process releases. After release, the test requires BOTH a dialable
// replacement control socket AND a matching new-token presence JSON — proof
// the replacement actually ran to completion for real, not merely that its
// process exited.
func TestCrossProcessDaemonControlSocketClaimSerializesWithHeldTransaction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows registry removal deliberately fails closed")
	}
	home, err := os.MkdirTemp("", "swh")
	if err != nil {
		t.Fatalf("create short temp home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	const handle = "cross-process-claim"
	if err := a2a.JoinSwarm(a2a.DefaultSwarmName, a2a.PeerPresence{
		Handle: handle, PID: 1, Type: a2a.PeerTypeDaemon, Status: "idle",
		InstanceToken: "old-token", ProcessStart: "old-process-start", Executable: "/usr/bin/old",
	}); err != nil {
		t.Fatalf("prerequisite JoinSwarm: %v", err)
	}
	sockPath := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers", handle+".ctrl")

	holder, holderOut, releaseHolder := startDaemonControlHelper(t, home, "hold", handle, sockPath)
	if line := readDaemonHelperLine(t, holderOut, "hold"); line != "HOLD_ENTERED" {
		t.Fatalf("hold handshake = %q, want HOLD_ENTERED", line)
	}

	claimant, claimantOut, releaseClaimant := startDaemonControlHelper(t, home, "claim", handle, sockPath)
	claimDone := make(chan string, 1)
	go func() {
		line, _ := claimantOut.ReadString('\n')
		claimDone <- strings.TrimSpace(line)
	}()
	select {
	case line := <-claimDone:
		t.Fatalf("replacement startup reported completion while old cleanup still held the lock: %q", line)
	case <-time.After(250 * time.Millisecond):
	}

	releaseHolder()
	if line := readDaemonHelperLine(t, holderOut, "hold"); line != "HOLD_DONE" {
		t.Fatalf("hold completion = %q, want HOLD_DONE", line)
	}
	waitDaemonHelper(t, holder, "hold")

	select {
	case line := <-claimDone:
		if line != "CLAIM_DONE" {
			t.Fatalf("replacement completion = %q, want CLAIM_DONE", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement startup never completed after old cleanup released the lock")
	}

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("replacement control socket was not dialable: %v", err)
	}
	_ = conn.Close()

	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil || peer == nil || peer.InstanceToken != "new-token" {
		t.Fatalf("replacement's new-token presence not published: peer=%+v err=%v", peer, err)
	}
	if peer.ControlSocket != sockPath {
		t.Fatalf("replacement presence ControlSocket = %q, want %q", peer.ControlSocket, sockPath)
	}

	releaseClaimant()
	waitDaemonHelper(t, claimant, "claim")
}
