// cmd/swarmos/global_daemon.go
//
// THE global background daemon for this machine.
//
// Product model (decided with the user): there is ONE background daemon per
// computer. Running `swarm` ensures it exists and renders it; closing the UI
// leaves it running silently (the engine, conversations, and agents persist in
// the daemon process). Cron jobs and scripts target it by its stable handle
// (`swarmos swarm task <handle> ...`). Stopping it is an EXPLICIT action
// (`swarmos swarm stop`) — the UI never kills it on close.
//
// This file is the lifecycle primitive: resolve / probe / spawn-detached /
// stop. Wiring it into the bare-`swarm` launch path is a separate step.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// globalDaemonHandle is the stable, per-user handle for the one background
// daemon that serves this computer. Stable so re-running `swarm` reattaches to
// the same engine and cron jobs have a fixed target.
const globalDaemonHandle = "swarmos-daemon"

// daemonServeReachable reports whether a daemon's serve interface at base
// (http://host:port) is accepting TCP connections — a cheap liveness probe that
// avoids a full RPC round-trip.
func daemonServeReachable(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", u.Host, 750*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// daemonHealthy performs a real health check against the daemon's /healthz
// endpoint. A TCP connect alone can pass while the process is wedged (a
// SIGSTOP'd or deadlocked daemon still completes handshakes on its listen
// backlog); an HTTP round-trip proves the serve loop is actually running.
// Daemons predating /healthz return 404 — treat that as healthy so a probe
// upgrade doesn't needlessly kill a working old daemon.
//
// LIVENESS ONLY (ADR-005 "Readiness, publication, and adapters"): a 200 (or
// legacy 404) here proves the serve loop answers, nothing more — it is
// never readiness or identity proof. Callers that need to know whether a
// candidate is actually usable-ready must use daemonReady (below), which
// requires a positive, identity-matched /readyz result instead.
func daemonHealthy(base string) bool {
	if base == "" {
		return false
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound
}

// globalDaemonStatus returns the live global daemon's presence (and true)
// only once it has itself self-proven READY per ADR-005 — a positive,
// identity-matched /readyz result (see daemonReady) — not merely a
// liveness-passing /healthz. Otherwise it returns (presence|nil, false). A
// stale presence whose serve interface is dead or wedged, or a live
// candidate that has bound its listener but not yet self-probed (still
// `starting`), both return (presence, false) so the caller never mistakes
// a not-yet-self-probed process for a usable, ready daemon. Pure liveness
// (any 200/404 on /healthz, no readiness/identity requirement) remains
// separately available via daemonHealthy for callers — such as the
// takeover-vs-spawn decision below — that only need "is a serve loop
// alive".
func globalDaemonStatus() (*a2a.PeerPresence, bool) {
	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, globalDaemonHandle)
	if err != nil || peer == nil || peer.ServeURL == "" {
		return nil, false
	}
	if !daemonReady(peer) {
		return peer, false
	}
	return peer, true
}

// daemonAction is what ensureGlobalDaemon decides to do after probing,
// expressed as an ADR-005 lifecycle Intent/Reason pair rather than an ad hoc
// action-name enum. It never claims a transition actually occurred — it
// only records what ensureGlobalDaemon is about to ask the chosen authority
// (systemd or the manual path) to do, and why. The four decision outcomes
// below preserve the exact takeover/spawn/upgrade/use behavior the previous
// untyped enum encoded; only their representation changed.
type daemonAction struct {
	// Intent is the ADR-005 closed intent this decision maps to.
	Intent lifecycle.Intent
	// Reason is the ADR-005 closed reason code explaining the decision.
	// Empty for daemonActionUse: reusing an already-healthy, current daemon
	// changes nothing, so — per ADR-005 — "an observation that does not
	// change state is not a transition" and carries no reason code.
	Reason lifecycle.Reason
	// Wait, when true, tells ensureGlobalDaemon this decision requires
	// polling for ADR-005 readiness evidence (bounded to the 15s readiness
	// window) before re-deciding, rather than immediately treating a live
	// candidate that simply has not yet self-probed as either reusable or
	// in need of takeover/spawn. This is a local reconciler-loop control
	// flag, not itself part of ADR-005's closed intent/reason vocabulary —
	// a pure `observe` produces neither a transition nor a fabricated
	// state change on its own.
	Wait bool
}

var (
	// daemonActionUse: a healthy, current daemon exists — return it. This is
	// a pure observation (no lifecycle transition), so it carries the
	// `observe` intent and no reason code.
	daemonActionUse = daemonAction{Intent: lifecycle.IntentObserve}
	// daemonActionWait: the candidate answers /healthz (live) but has not
	// yet self-proven readiness (still `starting`) and carries no `busy`
	// evidence that it was ready before. Also a pure observation — see
	// decideDaemonAction's doc for why liveness alone must never authorize
	// reuse per ADR-005.
	daemonActionWait = daemonAction{Intent: lifecycle.IntentObserve, Wait: true}
	// daemonActionSpawn: nothing usable exists — spawn fresh. Maps to
	// ADR-005's `absent -> starting` row (`start` intent, `start_requested`
	// reason).
	daemonActionSpawn = daemonAction{Intent: lifecycle.IntentStart, Reason: lifecycle.ReasonStartRequested}
	// daemonActionTakeover: a daemon process exists but its serve interface
	// is dead or wedged — kill it, then spawn fresh. Maps to ADR-005's
	// forced-takeover reason under a `force_stop` intent.
	daemonActionTakeover = daemonAction{Intent: lifecycle.IntentForceStop, Reason: lifecycle.ReasonForcedTakeover}
	// daemonActionUpgrade: a healthy daemon is running an outdated binary
	// and is idle — restart it so background work picks up the new code.
	// Maps to ADR-005's `ready -> upgrading` row (`upgrade` intent,
	// `binary_stale` reason) — "staleness maps to ReasonBinaryStale feeding
	// an upgrade intent".
	daemonActionUpgrade = daemonAction{Intent: lifecycle.IntentUpgrade, Reason: lifecycle.ReasonBinaryStale}
)

// decideDaemonAction is the pure decision core of ensureGlobalDaemon.
//
//	healthy    — serve interface answered the LIVENESS probe (/healthz
//	             200/legacy 404) — proves nothing about readiness or
//	             identity by itself
//	ready      — positive, identity-matched READINESS evidence (/readyz,
//	             see daemonReady): the candidate has itself self-probed and
//	             proven it is the presence-described instance
//	pidAlive   — presence PID refers to a live process
//	staleBin   — daemon's recorded binary mtime differs from the binary on disk
//	busy       — daemon reports status "working"
//
// ADR-005 fix: liveness (healthy) alone no longer authorizes reuse. A live
// candidate that has not yet self-probed (still `starting`) or has lost
// readiness (`degraded`) is never treated as usable-ready merely because
// its /healthz answered — unless `busy` independently proves it was ready
// before (only `ready -> working` and recovery rows reach `working`, so
// `busy` alone is sufficient prior-readiness evidence). Everything else
// about the takeover-vs-spawn liveness decision is unchanged.
func decideDaemonAction(presence *a2a.PeerPresence, healthy, ready, pidAlive, staleBin, busy bool) daemonAction {
	if presence == nil {
		return daemonActionSpawn
	}
	if !healthy {
		if pidAlive {
			return daemonActionTakeover // alive but wedged/unreachable
		}
		return daemonActionSpawn // crashed; presence is a leftover
	}
	if !ready && !busy {
		return daemonActionWait // live, but not yet self-probed ready
	}
	if staleBin && !busy {
		return daemonActionUpgrade
	}
	// Healthy and either current, or busy on an old binary (upgrade waits for
	// the next ensure once it goes idle — never kill in-flight work).
	return daemonActionUse
}

// binaryModTime returns the mtime of this process's executable ("" on error).
func binaryModTime() time.Time {
	self, err := os.Executable()
	if err != nil {
		return time.Time{}
	}
	info, err := os.Stat(self)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// daemonBinaryStale reports whether the daemon presence records a binary
// mtime that differs from the current binary on disk. Presences without a
// recorded mtime (pre-upgrade daemons) are never considered stale — no false
// restarts on fleets that haven't re-registered yet.
func daemonBinaryStale(presence *a2a.PeerPresence, current time.Time) bool {
	if presence == nil || presence.BinaryModTime.IsZero() || current.IsZero() {
		return false
	}
	return !presence.BinaryModTime.Equal(current)
}

// daemonLogPath returns <paths.Root()>/logs/daemon.log (created on demand),
// rotating the previous log to daemon.log.old when it exceeds 10MB. /tmp is
// deliberately NOT used: it is wiped on reboot and invisible next to the
// other swarmos logs.
//
// paths.Root() (~/.swarm, honoring SWARM_HOME) is this repo's single
// canonical on-disk root (see internal/paths/paths.go). This resolver
// previously hardcoded a divergent ~/.swarmos root, which split daemon logs
// away from every other swarmos on-disk artifact -- fixed here to join
// under the canonical root instead.
func daemonLogPath() string {
	dir := filepath.Join(paths.Root(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return filepath.Join(os.TempDir(), "swarmos-global-daemon.log")
	}
	logPath := filepath.Join(dir, "daemon.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() > 10*1024*1024 {
		_ = os.Rename(logPath, logPath+".old") // keep one generation
	}
	return logPath
}

// ensureGlobalDaemon makes sure the global background daemon is running,
// self-proven READY (ADR-005 — not merely healthy/live), and current, and
// returns its presence.
//
// Lifecycle decisions (see decideDaemonAction):
//   - ready + current binary          → reuse
//   - ready + stale binary + idle     → graceful restart (pick up new code)
//   - live but not yet self-probed    → wait (bounded 15s) for readiness
//   - process alive but serve wedged  → kill and respawn (takeover)
//   - dead / absent                   → spawn fresh
//
// The whole probe-decide-act sequence runs under an advisory spawn lock so
// concurrent callers (several TUIs starting at once) can't double-spawn: the
// first caller does the work, the rest block briefly and find its daemon.
func ensureGlobalDaemon(ctx context.Context, workspace string) (*a2a.PeerPresence, error) {
	// Fast path outside the lock: a ready, current daemon needs nothing.
	// globalDaemonStatus is readiness-aware (see its doc comment) — this can
	// never short-circuit on a candidate that is merely live/healthy but has
	// not yet self-probed.
	if peer, ok := globalDaemonStatus(); ok && !daemonBinaryStale(peer, binaryModTime()) {
		return peer, nil
	}

	release, _, lockErr := acquireDaemonLock(daemonSpawnLockName, true)
	if lockErr != nil {
		return nil, fmt.Errorf("acquire spawn lock: %w", lockErr)
	}
	defer release()

	// Re-probe under the lock — another caller may have just fixed things.
	presence, ready := globalDaemonStatus()
	if presence == nil {
		// GetPeer may have failed outright; retry once for the raw presence so
		// takeover can still see a dead process's record.
		presence, _ = a2a.GetPeer(a2a.DefaultSwarmName, globalDaemonHandle)
	}

	// healthy is the separate, pure-liveness signal (ADR-005: legacy
	// 200/404 on /healthz) decideDaemonAction still needs to distinguish
	// "crashed" (spawn) from "wedged" (takeover) — readiness (`ready` above)
	// answers a different question ("is it usable-ready") and must not be
	// conflated with it.
	healthy := presence != nil && daemonHealthy(presence.ServeURL)
	pidAlive := presence != nil && processAlive(presence.PID)
	staleBin := daemonBinaryStale(presence, binaryModTime())
	// presence.Status is the a2a package's own (as yet unmigrated) free-form
	// string field — Phase 02 explicitly defers switching its Go type to
	// lifecycle.State — but daemon_cli.go's writers already emit the
	// canonical ADR-005 wire value here, so comparing against
	// lifecycle.StateWorking's canonical string keeps this one comparison
	// vocabulary-consistent without touching internal/a2a.
	busy := presence != nil && presence.Status == lifecycle.StateWorking.String()

	action := decideDaemonAction(presence, healthy, ready, pidAlive, staleBin, busy)
	if action == daemonActionUse {
		return presence, nil
	}
	if action == daemonActionWait {
		// Live and answering /healthz, but not yet self-proven ready (still
		// `starting`) and no `busy` evidence it was ready before. ADR-005:
		// "Start and restart wait up to 15 seconds for readiness while
		// respecting caller cancellation" — poll for readiness instead of
		// immediately force-killing a candidate that may simply still be
		// mid self-probe (and which this reconciler did not itself launch).
		if peer, perr := waitGlobalDaemonReady(ctx, 15*time.Second); perr == nil {
			return peer, nil
		}
		// Readiness never arrived within the ADR-005 window: re-probe fresh
		// liveness/pid evidence — the candidate may have exited while this
		// reconciler waited, or may be genuinely wedged/degraded — and fall
		// through to the ordinary takeover/spawn decision below using that
		// fresh evidence rather than the (now stale) values captured above.
		presence, _ = a2a.GetPeer(a2a.DefaultSwarmName, globalDaemonHandle)
		healthy = presence != nil && daemonHealthy(presence.ServeURL)
		pidAlive = presence != nil && processAlive(presence.PID)
		switch {
		case !healthy && pidAlive:
			action = daemonActionTakeover
		case !healthy:
			action = daemonActionSpawn
		default:
			// Still answering /healthz but readiness never arrived: treat
			// like a wedged candidate so a fresh candidate gets the chance
			// to prove readiness cleanly.
			action = daemonActionTakeover
		}
	}

	// systemd-managed daemon: lifecycle goes through systemctl. A restart
	// re-execs the (updated) binary path, which is exactly what upgrade and
	// takeover need, and keeps the daemon supervised.
	if daemonManagedBySystemd() {
		var mgmtErr error
		if action == daemonActionTakeover {
			// The process is wedged — it cannot drain, so don't spend
			// TimeoutStopSec on a graceful stop. SIGKILL through systemd;
			// Restart=always respawns it immediately.
			mgmtErr = exec.Command("systemctl", "--user", "kill", "--signal=SIGKILL", serviceUnitName).Run()
		} else {
			mgmtErr = systemctlDaemon("restart")
		}
		if mgmtErr == nil {
			if peer, perr := waitGlobalDaemonReady(ctx, 15*time.Second); perr == nil {
				return peer, nil
			}
		}
		// systemctl path failed — fall through to the manual path below.
	}

	switch action {
	case daemonActionUpgrade:
		// Healthy but running old code and idle: restart it. stopDaemon is
		// graceful (SIGTERM first) so presence cleanup runs.
		if err := stopDaemon(globalDaemonHandle); err != nil {
			// Couldn't stop it — better a stale daemon than none.
			return presence, nil
		}

	case daemonActionTakeover:
		// Alive but wedged (serve dead / health probe hangs). SIGTERM may not
		// be deliverable (e.g. SIGSTOP'd), so stopDaemon's SIGKILL escalation
		// is what actually guarantees the takeover. If the validated stop
		// path refuses — for example because the pid cannot be corroborated
		// against the instance lock's holder (see
		// validateDaemonStopIdentity) — presence is cleared ONLY when the
		// recorded pid is INDEPENDENTLY confirmed dead right here; an
		// uncorroborated-but-still-alive pid is left untouched instead of
		// wiped, so a genuinely ambiguous process is never treated as safe
		// to erase.
		if err := stopDaemon(globalDaemonHandle); err != nil {
			if cur, gerr := a2a.GetPeer(a2a.DefaultSwarmName, globalDaemonHandle); gerr == nil && cur != nil && !processAlive(cur.PID) {
				_ = a2a.LeaveSwarm(a2a.DefaultSwarmName, globalDaemonHandle)
			}
		}

	case daemonActionSpawn:
		// Nothing usable — clear any leftover presence record before spawning
		// so pollers don't briefly see the dead record.
		if presence != nil {
			_ = a2a.LeaveSwarm(a2a.DefaultSwarmName, globalDaemonHandle)
		}
		// If the systemd unit is installed but stopped, start through it so
		// the daemon comes back supervised (Restart=always) rather than as an
		// orphan process.
		if daemonUnitInstalled() && systemctlDaemon("start") == nil {
			if peer, perr := waitGlobalDaemonReady(ctx, 15*time.Second); perr == nil {
				return peer, nil
			}
		}
	}

	return spawnGlobalDaemon(ctx, workspace)
}

// daemonUnitInstalled reports whether the swarm-daemon systemd user unit file
// exists (regardless of active state).
func daemonUnitInstalled() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	unitPath, err := systemdUserUnitPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(unitPath)
	return err == nil
}

// waitGlobalDaemonReady polls until the global daemon reports self-proven
// READY (ADR-005 — via the readiness-aware globalDaemonStatus, not mere
// liveness) or the timeout elapses.
func waitGlobalDaemonReady(ctx context.Context, timeout time.Duration) (*a2a.PeerPresence, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if peer, ok := globalDaemonStatus(); ok {
			return peer, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("global daemon did not become ready within %s", timeout)
}

// spawnGlobalDaemon starts a fresh detached daemon and polls until it is
// ready. Caller must hold the spawn lock.
func spawnGlobalDaemon(ctx context.Context, workspace string) (*a2a.PeerPresence, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate swarmos binary: %w", err)
	}
	if workspace == "" {
		if wd, werr := os.Getwd(); werr == nil {
			workspace = wd
		}
	}

	logPath := daemonLogPath()
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)

	cmd := exec.Command(self, "daemon",
		"--a2a-handle", globalDaemonHandle,
		"--a2a-listen", "127.0.0.1:0",
		"--workspace", workspace,
		// Start the gateway on explicit loopback. Phase 01 never turns a
		// wildcard/LAN address into authenticated control implicitly.
		"--gateway",
		"--gateway-addr", defaultDaemonGatewayAddr,
	)
	// Detach from the parent so the daemon outlives this process. Anchor its
	// working directory to the workspace (falling back to home) so it never
	// pins a transient launch directory against unmount/deletion.
	configureDetachedProcess(cmd)
	if workspace != "" {
		cmd.Dir = workspace
	} else if home, herr := os.UserHomeDir(); herr == nil {
		cmd.Dir = home
	}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn global daemon: %w", err)
	}
	pid := cmd.Process.Pid
	// We never Wait — the daemon is meant to outlive us. Release our child handle
	// so it is not left as a zombie when it eventually exits.
	_ = cmd.Process.Release()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if peer, ok := globalDaemonStatus(); ok {
			return peer, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("global daemon spawned (pid %d) but did not become reachable within 15s — see %s",
		pid, logPath)
}

// autoEnsureGlobalDaemon is the TUI-startup entry point: it retries
// ensureGlobalDaemon with backoff and reports the outcome through notify
// (level, message) so failures surface as a visible banner instead of dying
// in a debug log. Runs in its own goroutine; never blocks TUI startup.
func autoEnsureGlobalDaemon(workspace string, notify func(level, message string)) {
	if notify == nil {
		notify = func(string, string) {}
	}
	if workspace == "" {
		if wd, err := os.Getwd(); err == nil {
			workspace = wd
		}
	}

	// Fast path: already self-proven ready (ADR-005) and current — stay
	// silent, nothing changed. globalDaemonStatus is readiness-aware, so
	// this never mistakes a merely-live, still-starting candidate as done.
	if peer, ok := globalDaemonStatus(); ok && !daemonBinaryStale(peer, binaryModTime()) {
		return
	}

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		peer, err := ensureGlobalDaemon(ctx, workspace)
		cancel()
		if err == nil {
			notify("success", fmt.Sprintf("background daemon ready at %s (survives closing this UI)", peer.ServeURL))
			return
		}
		lastErr = err
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}
	notify("warning", fmt.Sprintf("background daemon failed to start: %v — background tasks and LAN gateway are unavailable (retry: swarmos swarm up)", lastErr))
}

// daemonManagedBySystemd reports whether the global daemon is currently
// running under the swarm-daemon systemd user unit. Lifecycle operations must
// go through systemctl in that case: the unit uses Restart=always, so a raw
// SIGTERM would just be resurrected 3 seconds later.
func daemonManagedBySystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "--user", "is-active", "--quiet", serviceUnitName).Run() == nil
}

// systemctlDaemon runs `systemctl --user <verb> swarm-daemon.service` with
// output discarded, returning its error.
func systemctlDaemon(verb string) error {
	return exec.Command("systemctl", "--user", verb, serviceUnitName).Run()
}

// Sentinel denial reasons for a manual (non-systemd) daemon stop. Every
// destructive path below (signal, presence/socket removal) wraps one of
// these with fmt.Errorf("...: %w", ...) so callers get a readable message
// while tests can assert the exact denial with errors.Is. ADR-005 "Identity
// proof": no destructive action may rely on a presence file or PID alone —
// each of these corresponds to one required, independently-failing guard.
var (
	// errStopUnknownPeer: no presence record exists for the handle at all.
	errStopUnknownPeer = errors.New("daemon presence not found")
	// errStopWrongPeerType: presence exists but is not typed as a daemon
	// peer — never signal a non-daemon peer's pid via the daemon stop path.
	errStopWrongPeerType = errors.New("presence peer type is not a daemon")
	// errStopNoRecordedPID: presence carries no usable pid.
	errStopNoRecordedPID = errors.New("presence has no recorded pid")
	// errStopNoInstanceRecord: the handle's instance lock has never been
	// stamped with a holder identity (see daemon_lock.go), so there is
	// nothing to corroborate the presence pid against.
	errStopNoInstanceRecord = errors.New("no instance lock identity record to corroborate against")
	// errStopMalformedRecord: the instance lock file exists but its content
	// could not be trusted (unparseable JSON, non-positive pid, ...).
	errStopMalformedRecord = errors.New("instance lock identity record is malformed")
	// errStopPIDMismatch: presence names a pid different from whoever is
	// actually holding the instance lock right now — the classic
	// wrong/reused-pid or stale-presence case.
	errStopPIDMismatch = errors.New("presence pid does not match the instance lock holder")
	// errStopWrongUser: the instance lock's stamped owning user id does not
	// match the user id running this stop — never signal another user's
	// process.
	errStopWrongUser = errors.New("instance lock is not owned by the current user")
	// errStopBadSocketPath: an advertised control socket path is not the
	// exact canonical path for this handle — malformed or foreign, so it
	// must never be treated as an owned artifact eligible for removal.
	errStopBadSocketPath = errors.New("control socket path is not canonical for this handle")
	// errStopBadSocketType: the path exists on disk but is not a socket —
	// refuse to touch it (guards against a crafted presence record pointing
	// ControlSocket at an arbitrary file).
	errStopBadSocketType = errors.New("control socket path does not refer to a socket")
	// errStopBadSocketOwner: the socket exists but is owned by a different
	// user than the one performing the stop.
	errStopBadSocketOwner = errors.New("control socket is not owned by the current user")
	// errStopStaleLock: immediately before signalling, the instance lock is
	// no longer held by anyone — the corroborated pid from validation has
	// already exited (and could be reused), so the presence record is now
	// stale. Never signal in this case.
	errStopStaleLock = errors.New("instance lock is no longer held; presence record is stale")
	// errStopNotLiveBeforeSignal: the immediate pre-signal liveness probe
	// found the pid already gone.
	errStopNotLiveBeforeSignal  = errors.New("process is not alive immediately before signalling")
	errStopMissingEvidence      = errors.New("presence is missing required instance evidence")
	errStopTokenMismatch        = errors.New("instance token evidence does not match")
	errStopProcessStartMismatch = errors.New("process-start evidence does not match")
	errStopExecutableMismatch   = errors.New("executable evidence does not match")
	errStopHealthMismatch       = errors.New("health evidence does not match presence")
	errStopSignalFailed         = errors.New("process signal failed")
	errStopExactExitTimeout     = errors.New("exact process instance did not exit before deadline")
)

type daemonHealthEvidence struct {
	Handle        string `json:"handle"`
	PID           int    `json:"pid"`
	InstanceToken string `json:"instance_token"`
	ProcessStart  string `json:"process_start"`
	Executable    string `json:"executable"`
	// Status is additive: the canonical ADR-005 lifecycle wire value
	// (daemon_cli.go's daemonHealthzHandler already emits it as "status")
	// this daemon reported alongside its identity. daemonReady's legacy
	// fallback (below) uses it to distinguish an identity-matched but
	// not-yet-ready daemon from one already known `ready`/`working`.
	Status string `json:"status"`
}

var daemonHealthEvidenceFunc = func(base string) (daemonHealthEvidence, error) {
	var evidence daemonHealthEvidence
	if base == "" {
		return evidence, fmt.Errorf("empty serve URL")
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		return evidence, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return evidence, fmt.Errorf("health status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&evidence); err != nil {
		return evidence, err
	}
	return evidence, nil
}

// daemonReadyEvidence mirrors daemonHealthEvidence but decodes a /readyz
// response's readiness boolean alongside its identity fields.
type daemonReadyEvidence struct {
	Ready         bool   `json:"ready"`
	Handle        string `json:"handle"`
	PID           int    `json:"pid"`
	InstanceToken string `json:"instance_token"`
	ProcessStart  string `json:"process_start"`
	Executable    string `json:"executable"`
}

// daemonReadyEvidenceFunc probes base+"/readyz" for readiness evidence,
// injectable so tests can exercise the readiness-aware decision path
// without a real HTTP listener. A daemon predating /readyz (any non-200,
// including the ADR-005 legacy-liveness 404) yields an error here — 404
// supplies neither readiness nor identity proof and must never be
// synthesized into a positive readiness evidence value.
var daemonReadyEvidenceFunc = func(base string) (daemonReadyEvidence, error) {
	var evidence daemonReadyEvidence
	if base == "" {
		return evidence, fmt.Errorf("empty serve URL")
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(base + "/readyz")
	if err != nil {
		return evidence, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return evidence, fmt.Errorf("readyz status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&evidence); err != nil {
		return evidence, err
	}
	return evidence, nil
}

// daemonReady reports whether presence's candidate has positively proven
// readiness AND its identity (handle/pid/instance token/process
// start/executable) matches presence — the readiness-aware evidence
// ADR-005 requires callers prefer over the old liveness-only /healthz
// 200/404 check before treating an existing candidate as usable-ready. A
// candidate that has not yet self-probed (still `starting`), one whose
// readiness evidence identity does not match presence, and a legacy
// pre-/readyz daemon (404) all report false here — none of them supplies
// readiness or identity proof, only (at most) liveness.
//
// Legacy compatibility fallback: a daemon predating the /readyz endpoint
// (post-identity but pre-Phase-03) has no /readyz to probe at all. Rather
// than treat such a daemon as perpetually unusable, this falls back to its
// identity-bearing /healthz response (daemonHealthEvidenceFunc) — but only
// accepts it as readiness proof when BOTH the full identity matches AND
// the reported canonical status is already `ready` or `working` (a
// canonical writer only ever reaches those states after its own successful
// self-probe). A bare legacy-liveness 404 on /healthz itself still yields
// an error from daemonHealthEvidenceFunc and therefore never satisfies
// this fallback either — 404 remains liveness-only proof, never readiness.
func daemonReady(presence *a2a.PeerPresence) bool {
	if presence == nil || presence.ServeURL == "" {
		return false
	}
	if evidence, err := daemonReadyEvidenceFunc(presence.ServeURL); err == nil {
		return evidence.Ready &&
			evidence.Handle == presence.Handle &&
			evidence.PID == presence.PID &&
			evidence.InstanceToken == presence.InstanceToken &&
			evidence.ProcessStart == presence.ProcessStart &&
			evidence.Executable == presence.Executable
	}
	evidence, err := daemonHealthEvidenceFunc(presence.ServeURL)
	if err != nil {
		return false
	}
	if evidence.Handle != presence.Handle || evidence.PID != presence.PID ||
		evidence.InstanceToken != presence.InstanceToken ||
		evidence.ProcessStart != presence.ProcessStart ||
		evidence.Executable != presence.Executable {
		return false
	}
	return evidence.Status == lifecycle.StateReady.String() || evidence.Status == lifecycle.StateWorking.String()
}

// expectedControlSocketPath returns the one canonical control-socket path a
// daemon with handle is allowed to advertise:
// <swarm peers dir>/<handle>.ctrl (see daemon_cli.go's ctrlSock). A presence
// record naming any other path is either malformed or describes a foreign
// artifact and must never be trusted for destructive cleanup.
func expectedControlSocketPath(handle string) string {
	return filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers", handle+".ctrl")
}

// validateDaemonStopIdentity performs the fail-closed identity proof ADR-005
// requires before any manual signal or artifact removal that targets a
// daemon by handle. Every one of the following must independently hold, or
// the daemon is not touched:
//
//  1. presence exists, is typed as a daemon peer, and carries a positive pid
//     (peer-type + basic-shape guard);
//  2. the handle's instance lock (held for the daemon process's entire
//     lifetime, see daemon_lock.go) currently carries a self-stamped
//     identity record whose pid equals presence.PID — this corroborates the
//     presence pid against the process ACTUALLY holding the daemon's own
//     instance lock right now, not merely a pid number that could since have
//     been reused by an unrelated process (daemon instance token / PID-reuse
//     guard);
//  3. that record's owning user matches the user performing the stop
//     (same-user ownership guard);
//  4. if a control socket path is advertised, it is the exact canonical path
//     for this handle, and — when something exists there on disk — it is
//     actually a socket owned by the current user (canonical-path +
//     ownership + type guard for any later removal).
//
// This function does NOT perform the immediate pre-signal liveness check;
// that must be repeated again right before the signal itself to close the
// TOCTOU window (see stopDaemonBySignal).
func validateDaemonStopIdentity(handle string, peer *a2a.PeerPresence) error {
	if peer == nil {
		return errStopUnknownPeer
	}
	if peer.Type != a2a.PeerTypeDaemon {
		return fmt.Errorf("%w: got %q", errStopWrongPeerType, peer.Type)
	}
	if peer.PID <= 0 {
		return errStopNoRecordedPID
	}
	if peer.Handle != handle || peer.InstanceToken == "" || peer.ProcessStart == "" || peer.Executable == "" || peer.ServeURL == "" {
		return errStopMissingEvidence
	}

	record, err := readDaemonLockIdentity(daemonInstanceLockName(handle))
	if err != nil {
		return fmt.Errorf("%w: %v", errStopMalformedRecord, err)
	}
	if record == nil {
		return errStopNoInstanceRecord
	}
	if record.PID != peer.PID {
		return fmt.Errorf("%w: presence pid %d, instance lock holder pid %d", errStopPIDMismatch, peer.PID, record.PID)
	}
	if record.Token != peer.InstanceToken {
		return errStopTokenMismatch
	}
	if record.ProcessStart != peer.ProcessStart {
		return errStopProcessStartMismatch
	}
	if record.Executable != peer.Executable {
		return errStopExecutableMismatch
	}
	if record.UID != "" {
		if u, uerr := user.Current(); uerr == nil && u.Uid != record.UID {
			return fmt.Errorf("%w: instance lock uid %q, current uid %q", errStopWrongUser, record.UID, u.Uid)
		}
	}
	start, executable, uid, err := processEvidenceFunc(peer.PID)
	if err != nil {
		return fmt.Errorf("%w: %v", errStopNotLiveBeforeSignal, err)
	}
	if start != peer.ProcessStart {
		return errStopProcessStartMismatch
	}
	if executable != peer.Executable {
		return errStopExecutableMismatch
	}
	if uid != record.UID {
		return errStopWrongUser
	}
	health, err := daemonHealthEvidenceFunc(peer.ServeURL)
	if err != nil {
		return fmt.Errorf("%w: %v", errStopHealthMismatch, err)
	}
	if health.Handle != handle || health.PID != peer.PID ||
		health.InstanceToken != peer.InstanceToken ||
		health.ProcessStart != peer.ProcessStart ||
		health.Executable != peer.Executable {
		return errStopHealthMismatch
	}

	if peer.ControlSocket != "" {
		expected := expectedControlSocketPath(handle)
		if peer.ControlSocket != expected {
			return fmt.Errorf("%w: got %q want %q", errStopBadSocketPath, peer.ControlSocket, expected)
		}
		// A missing socket file is not itself disqualifying — the daemon may
		// simply have not (re)created it, or it was already cleaned up. When
		// something IS there, though, it must be a socket we own.
		if info, serr := os.Lstat(peer.ControlSocket); serr == nil {
			if runtime.GOOS != "windows" && info.Mode()&os.ModeSocket == 0 {
				return fmt.Errorf("%w: %s", errStopBadSocketType, peer.ControlSocket)
			}
			if same, operr := sameOwnerAsSelf(peer.ControlSocket); operr == nil && !same {
				return fmt.Errorf("%w: %s", errStopBadSocketOwner, peer.ControlSocket)
			}
		}
	}

	return nil
}

func proveDaemonStopIdentity(handle, expectedToken string) (*a2a.PeerPresence, error) {
	peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle)
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return nil, errStopUnknownPeer
	}
	if expectedToken != "" && peer.InstanceToken != expectedToken {
		return nil, errStopTokenMismatch
	}
	if err := validateDaemonStopIdentity(handle, peer); err != nil {
		return nil, err
	}
	held, err := daemonInstanceLockHeld(daemonInstanceLockName(handle))
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, errStopStaleLock
	}
	return peer, nil
}

func exactProcessStillExists(peer *a2a.PeerPresence) bool {
	if peer == nil {
		return false
	}
	start, executable, _, err := processEvidenceFunc(peer.PID)
	return err == nil && start == peer.ProcessStart && executable == peer.Executable
}

func waitExactProcessExit(peer *a2a.PeerPresence, deadline time.Time) bool {
	for daemonStopNowFunc().Before(deadline) {
		if !exactProcessStillExists(peer) {
			return true
		}
		daemonStopSleepFunc(150 * time.Millisecond)
	}
	return !exactProcessStillExists(peer)
}

// stopDaemon stops a running daemon by handle: SIGTERM its process (its signal
// handler runs LeaveSwarm + socket cleanup), escalating to SIGKILL if it does
// not exit in time, then best-effort clears its presence. This is the explicit
// "kill"; the UI never invokes it on close. When the daemon is running under
// the systemd unit, the stop is delegated to systemctl so Restart=always does
// not immediately resurrect it.
func stopDaemon(handle string) error {
	if handle == globalDaemonHandle && daemonManagedBySystemd() {
		if err := systemctlDaemon("stop"); err == nil {
			// systemd delivered SIGTERM and waited (TimeoutStopSec); presence
			// cleanup ran in the daemon's own defer. Clear any leftovers.
			if peer, err := a2a.GetPeer(a2a.DefaultSwarmName, handle); err == nil && peer != nil && !processAlive(peer.PID) {
				_, _ = a2a.RemovePeerIfInstance(a2a.DefaultSwarmName, handle, peer.InstanceToken)
			}
			return nil
		}
		// systemctl failed — fall through to the direct signal path.
	}
	return stopDaemonBySignal(handle)
}

// stopDaemonBySignal is the raw signal-based stop used for daemons not
// managed by systemd (or when systemctl is unavailable/failing). Manual
// authority per ADR-005: identity is fully validated
// (validateDaemonStopIdentity) BEFORE any signal, and liveness plus
// instance-lock ownership are re-confirmed immediately before the signal
// itself. Wrong/reused pid, wrong peer type, token mismatch, wrong user,
// malformed paths, and stale records are refused without ever calling
// Signal or removing any artifact.
//
// Drain wiring note (Phase 07.C, CONTRACT.md section 3): ADR-005's
// `ready|working -> draining` row is a LOCAL, in-process lifecycle
// authority transition, validated through the target daemon's own
// *daemonState.transition (the single legal choke point,
// daemon_cli.go:~204-225). This function runs in a DIFFERENT process (the
// caller of `swarmos swarm stop`/`ensureGlobalDaemon`'s stop path) and only
// ever has a remote peer's presence record and PID — it holds no
// *daemonState value for the target daemon and cannot validate or mutate a
// state machine it does not own by reaching across a process boundary
// (doing so would also race the target's own goroutines touching state.mu).
// The correct local drain transition happens inside the SIGNALED daemon's
// own process, in its runDaemon shutdown sequence
// (daemon_cli.go's beginDaemonDrain, invoked on `<-ctx.Done()` — the
// signal.NotifyContext this SIGTERM below ultimately delivers) BEFORE that
// process advertises `stopping` or begins its bounded 5s HTTP drain. This
// function's only correct job on the caller side is exactly what it already
// does: prove identity, deliver the signal, and wait — never bypass that
// with a synthetic remote transition it cannot actually validate.
func stopDaemonBySignal(handle string) error {
	peer, err := proveDaemonStopIdentity(handle, "")
	if err != nil {
		return fmt.Errorf("refusing to signal daemon %q: %w", handle, err)
	}
	token := peer.InstanceToken
	if err := signalProcessFunc(peer.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("%w: SIGTERM pid %d: %v", errStopSignalFailed, peer.PID, err)
	}
	if waitExactProcessExit(peer, daemonStopNowFunc().Add(5*time.Second)) {
		_, _ = a2a.RemovePeerIfInstance(a2a.DefaultSwarmName, handle, token)
		return nil
	}

	peer, err = proveDaemonStopIdentity(handle, token)
	if err != nil {
		return fmt.Errorf("refusing SIGKILL for daemon %q: %w", handle, err)
	}
	if err := signalProcessFunc(peer.PID, syscall.SIGKILL); err != nil {
		return fmt.Errorf("%w: SIGKILL pid %d: %v", errStopSignalFailed, peer.PID, err)
	}
	if !waitExactProcessExit(peer, daemonStopNowFunc().Add(5*time.Second)) {
		return fmt.Errorf("%w: pid %d", errStopExactExitTimeout, peer.PID)
	}
	_, err = a2a.RemovePeerIfInstance(a2a.DefaultSwarmName, handle, token)
	return err
}

// runDaemonBackedTUI is the opt-in daemon-backed launch (--daemon / SWARM_DAEMON):
// ensure THE global background daemon, scope it to the current directory, then
// render it via the thin attach client. Detaching (Ctrl-C / :q) leaves the
// daemon — and its conversations and agents — running for cron jobs and
// background work. This is the same path that becomes the bare-`swarm` default
// once the full TUI renders over serve (AG-TUI).
func runDaemonBackedTUI(workspace string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	peer, err := ensureGlobalDaemon(ctx, workspace)
	cancel()
	if err != nil {
		return err
	}
	// Scope the global daemon to this directory so new conversations land here.
	if peer.ServeURL != "" && workspace != "" {
		_, _ = rpcCall(peer.ServeURL, "client.setWorkspace", map[string]any{"dir": workspace})
	}
	fmt.Printf("daemon-backed: rendering global daemon %q at %s (detach leaves it running)\n",
		peer.Handle, peer.ServeURL)
	return runAttachCLI([]string{peer.Handle})
}

// processAlive reports whether pid refers to a live process this user can see,
// using the signal-0 probe (nil = alive, ESRCH = gone). Thin wrapper around
// processAliveFunc (see below) so every existing call site stays unchanged.
func processAlive(pid int) bool {
	return processAliveFunc(pid)
}

// processAliveFunc is the real signal-0 liveness probe, injectable so
// deterministic tests can simulate a pid going from alive to exited (e.g.
// mid pre-signal / post-SIGTERM poll loop) without spawning or killing a
// real OS process. Tests MUST restore the original value (see
// global_daemon_test.go helpers) — this is process-wide mutable state.
var processAliveFunc = func(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc := os.Process{Pid: pid}
	return proc.Signal(syscall.Signal(0)) == nil
}

// signalProcessFunc sends sig to pid, injectable so deterministic tests can
// observe/simulate signal delivery (including failure) without touching a
// real OS process. Defaults to the real os.Process.Signal call used by
// stopDaemonBySignal's SIGTERM-then-SIGKILL escalation.
var signalProcessFunc = func(pid int, sig syscall.Signal) error {
	proc := os.Process{Pid: pid}
	return proc.Signal(sig)
}

// daemonStopSleepFunc is the poll-interval sleep used while waiting for a
// signalled daemon to exit gracefully, injectable so tests exercise the
// SIGTERM-wait-SIGKILL loop deterministically (via a fake processAliveFunc
// sequence) without ever actually sleeping in real time.
var daemonStopSleepFunc = time.Sleep

// daemonStopNowFunc supplies "now" for the SIGTERM-wait-SIGKILL deadline,
// injectable alongside daemonStopSleepFunc so a test can advance a fake
// clock in lockstep with fake sleeps and exercise the full 5s escalation
// window without ever waiting on the real wall clock (avoids a genuinely
// slow/flaky test while still proving the SIGKILL escalation path).
var daemonStopNowFunc = time.Now
