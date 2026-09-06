package main

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
)

func TestDecideDaemonAction(t *testing.T) {
	p := &a2a.PeerPresence{Handle: globalDaemonHandle, PID: 1234}

	cases := []struct {
		name       string
		presence   *a2a.PeerPresence
		healthy    bool
		ready      bool
		pidAlive   bool
		staleBin   bool
		busy       bool
		wantIntent lifecycle.Intent
		wantReason lifecycle.Reason
		wantWait   bool
	}{
		// daemonActionSpawn maps to ADR-005's absent->starting row: intent
		// `start`, reason `start_requested`.
		{"no presence at all", nil, false, false, false, false, false, lifecycle.IntentStart, lifecycle.ReasonStartRequested, false},
		// daemonActionUse is a pure observation — no transition occurred, so
		// it carries `observe` and no reason code. ready=true here models a
		// candidate that has actually self-proven readiness via /readyz
		// (ADR-005: liveness alone is never enough to authorize reuse).
		{"healthy AND ready current daemon", p, true, true, true, false, false, lifecycle.IntentObserve, "", false},
		// busy alone (independent of the instantaneous ready probe result —
		// a working daemon's own /readyz reports ready=false, since
		// lifecycle.IsReady excludes `working`) is sufficient prior-
		// readiness evidence: only ready->working/degraded->working rows
		// reach `working` at all.
		{"healthy busy daemon (ready probe false, busy proves prior readiness)", p, true, false, true, false, true, lifecycle.IntentObserve, "", false},
		// ADR-005 fix under test: healthy (live, /healthz answers) but NOT
		// yet self-proven ready and NOT busy — still `starting` (or
		// `degraded`) — must never be treated as usable-ready merely
		// because liveness passed. This is the exact case the historical
		// bug got wrong.
		{"healthy but NOT yet self-probed ready (still starting) must wait, not reuse", p, true, false, true, false, false, lifecycle.IntentObserve, "", true},
		// daemonActionUpgrade maps to ADR-005's ready->upgrading row: intent
		// `upgrade`, reason `binary_stale`. Requires ready=true (idle, not
		// busy) to even reach the staleness check.
		{"healthy idle ready daemon on stale binary", p, true, true, true, true, false, lifecycle.IntentUpgrade, lifecycle.ReasonBinaryStale, false},
		// Active work pins the decision to `observe` (use): upgrade remains
		// pending without terminating work, per ADR-005's upgrade section.
		{"healthy BUSY daemon on stale binary waits", p, true, false, true, true, true, lifecycle.IntentObserve, "", false},
		// daemonActionTakeover maps to a forced-takeover completion: intent
		// `force_stop`, reason `forced_takeover`. healthy=false short-
		// circuits before the readiness check is ever consulted.
		{"wedged: alive but unhealthy", p, false, false, true, false, false, lifecycle.IntentForceStop, lifecycle.ReasonForcedTakeover, false},
		{"wedged stale busy still takeover", p, false, false, true, true, true, lifecycle.IntentForceStop, lifecycle.ReasonForcedTakeover, false},
		{"crashed: dead pid, leftover presence", p, false, false, false, false, false, lifecycle.IntentStart, lifecycle.ReasonStartRequested, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideDaemonAction(tc.presence, tc.healthy, tc.ready, tc.pidAlive, tc.staleBin, tc.busy)
			if got.Intent != tc.wantIntent || got.Reason != tc.wantReason || got.Wait != tc.wantWait {
				t.Errorf("decideDaemonAction() = {Intent:%q Reason:%q Wait:%v}, want {Intent:%q Reason:%q Wait:%v}",
					got.Intent, got.Reason, got.Wait, tc.wantIntent, tc.wantReason, tc.wantWait)
			}
		})
	}
}

func TestDaemonBinaryStale(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	older := now.Add(-1 * time.Hour)

	if daemonBinaryStale(nil, now) {
		t.Error("nil presence must never be stale")
	}
	if daemonBinaryStale(&a2a.PeerPresence{}, now) {
		t.Error("presence without recorded mtime must never be stale (pre-upgrade daemons)")
	}
	if daemonBinaryStale(&a2a.PeerPresence{BinaryModTime: now}, time.Time{}) {
		t.Error("unknown current mtime must never be stale")
	}
	if daemonBinaryStale(&a2a.PeerPresence{BinaryModTime: now}, now) {
		t.Error("matching mtimes are not stale")
	}
	if !daemonBinaryStale(&a2a.PeerPresence{BinaryModTime: older}, now) {
		t.Error("differing mtimes must be stale")
	}
}
