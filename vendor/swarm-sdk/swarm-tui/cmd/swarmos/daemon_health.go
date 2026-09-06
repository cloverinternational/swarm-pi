// cmd/swarmos/daemon_health.go
//
// ADR-005 "Readiness, publication, and adapters" split: /healthz
// (daemon_cli.go's daemonHealthzHandler) remains pure LIVENESS — "is the
// serve loop alive" — unchanged in wire meaning. This file adds /readyz,
// which is READINESS: true only once this daemon process has itself
// completed a successful self-probe of its own required endpoint(s) and
// its in-memory lifecycle state is exactly `ready`. "Process existence, a
// held lock, presence publication, and a successful TCP handshake are
// liveness signals, not readiness."
//
// selfProbeReady below is the mechanism runDaemon (daemon_cli.go) invokes,
// BEFORE publishDaemonAttachEndpoints, to perform that self-probe and
// apply the resulting legal lifecycle transition:
//
//   - self-probe succeeds + identity matches -> starting -> ready
//     (observe/readiness_proven)
//   - 15s deadline elapses, candidate still live -> starting -> degraded
//     (observe/readiness_timeout)
//   - candidate found already exited            -> starting -> stopped
//     (observe/process_exited)
//
// This is deliberately a general, injectable helper (probe/live funcs)
// rather than something hard-wired to an HTTP round trip against this
// exact process, so daemon_health_test.go can exercise all three outcomes
// — including the already-exited path, which the current single
// production caller (self-probing its own always-live process) can never
// itself reach — deterministically and without a real 15-second wait.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// readinessSelfProbeTimeout is ADR-005's "Start and restart wait up to 15
// seconds for readiness while respecting caller cancellation" bound for a
// daemon's own startup self-probe (see selfProbeReady). A package var
// (rather than an inline literal) purely so daemon_health_test.go can
// shrink it for a deterministic, sub-second timeout test instead of
// spinning a real 15-second clock; production code never mutates it.
var readinessSelfProbeTimeout = 15 * time.Second

// readinessSelfProbePollInterval is how often selfProbeReady retries a
// failed self-probe attempt while waiting for readiness.
const readinessSelfProbePollInterval = 100 * time.Millisecond

// daemonSelfProbeSleepFunc is the poll-interval sleep selfProbeReady uses
// between failed probe attempts, injectable so tests exercise the
// timeout->degraded and still-polling paths deterministically without a
// real sleep. Mirrors the daemonStopSleepFunc seam already used by
// stopDaemonBySignal in global_daemon.go.
var daemonSelfProbeSleepFunc = time.Sleep

// daemonSelfProbeNowFunc supplies "now" for selfProbeReady's 15s deadline,
// injectable alongside daemonSelfProbeSleepFunc so a test can advance a
// fake clock in lockstep with fake sleeps and exercise the full timeout
// window without ever waiting on the real wall clock. Mirrors
// daemonStopNowFunc.
var daemonSelfProbeNowFunc = time.Now

// selfProbeReady performs ADR-005's required self-probe of a freshly bound
// daemon's own liveness endpoint before it may ever be observed as
// `ready`. probe is called repeatedly until it returns identity-bearing
// evidence that matches state's own recorded identity
// (daemonIdentityMatches), ctx is cancelled, or the ADR-005 15-second
// deadline elapses:
//
//   - success + matching identity    -> state.transition(ready, observe, readiness_proven)
//   - deadline elapses, live() true  -> state.transition(degraded, observe, readiness_timeout)
//   - live() false at any attempt    -> state.transition(stopped, observe, process_exited)
//
// live reports whether the underlying candidate process is still alive;
// for THIS process's own self-probe (the only production caller, wired in
// runDaemon) it is always true — the already-exited path exists so this
// generic self-probe helper is fully testable per Phase 03's CONTRACT.md
// requirement to prove the negative cases, not only the happy path, and so
// any future caller that self-probes a distinct candidate process (rather
// than itself) can reuse this exact function unchanged. Every returned
// error corresponds to a legal transition already having been attempted;
// a transition that itself fails validation (should never happen given the
// fixed `starting` precondition this is always invoked under) is reported
// via the wrapped error rather than silently swallowed.
func selfProbeReady(ctx context.Context, state *daemonState, probe func() (daemonHealthEvidence, error), live func() bool) error {
	if state == nil {
		return fmt.Errorf("daemon_health: selfProbeReady called with nil state")
	}
	deadline := daemonSelfProbeNowFunc().Add(readinessSelfProbeTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		evidence, err := probe()
		if err == nil && daemonIdentityMatches(state, evidence) {
			if terr := state.transition(lifecycle.StateReady, lifecycle.IntentObserve, lifecycle.ReasonReadinessProven); terr != nil {
				return fmt.Errorf("daemon_health: readiness proven but transition refused: %w", terr)
			}
			return nil
		}
		if !live() {
			_ = state.transition(lifecycle.StateStopped, lifecycle.IntentObserve, lifecycle.ReasonProcessExited)
			return fmt.Errorf("daemon_health: candidate exited before readiness was proven")
		}
		if !daemonSelfProbeNowFunc().Before(deadline) {
			_ = state.transition(lifecycle.StateDegraded, lifecycle.IntentObserve, lifecycle.ReasonReadinessTimeout)
			return fmt.Errorf("daemon_health: readiness self-probe timed out after %s", readinessSelfProbeTimeout)
		}
		daemonSelfProbeSleepFunc(readinessSelfProbePollInterval)
	}
}

// daemonIdentityMatches reports whether evidence — decoded from a
// self-probe's /healthz response — describes THIS daemon instance: same
// handle, pid, instance token, process-start, and executable already
// recorded on state. This is the "daemon instance identity/handle/PID
// internally consistent" half of readiness proof ADR-005 "Readiness,
// publication, and adapters" requires ("Readiness requires a successful
// /healthz response whose daemon instance identity, handle, and PID match
// presence"); the presence-matching half — comparing a DIFFERENT
// candidate process's evidence against ITS OWN presence record — is the
// caller-side concern in global_daemon.go's daemonReady, not this
// self-probe, which only ever proves "is THIS process ready".
func daemonIdentityMatches(state *daemonState, evidence daemonHealthEvidence) bool {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return evidence.Handle == state.handle &&
		evidence.PID == os.Getpid() &&
		evidence.InstanceToken == state.instanceToken &&
		evidence.ProcessStart == state.processStart &&
		evidence.Executable == state.executable
}

// daemonReadyzHandler serves GET /readyz: ADR-005 readiness, distinct from
// /healthz's pure liveness. `ready` is true only once this daemon process
// has itself completed a successful self-probe (selfProbeReady, invoked
// from runDaemon before publishDaemonAttachEndpoints) and its in-memory
// lifecycle state is exactly lifecycle.StateReady — never merely because
// the HTTP listener itself answers. Reports HTTP 503 (with `"ready":
// false` in the body) rather than 200 while not yet ready, so a plain
// status-code check already reflects readiness without decoding the body.
//
// Compatibility note: an OLDER daemon binary predating this handler simply
// 404s here, which callers (see global_daemon.go's daemonReady) must treat
// as legacy liveness only, never as readiness or identity proof.
func daemonReadyzHandler(state *daemonState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, _, _ := state.snapshot()
		ready := lifecycle.IsReady(status)
		w.Header().Set("Content-Type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":          ready,
			"status":         status.String(),
			"handle":         state.handle,
			"pid":            os.Getpid(),
			"instance_token": state.instanceToken,
			"process_start":  state.processStart,
			"executable":     state.executable,
			"version":        version.Version,
		})
	}
}
