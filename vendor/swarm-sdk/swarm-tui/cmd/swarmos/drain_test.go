// cmd/swarmos/drain_test.go
//
// Covers Phase 07.C's local drain-transition wiring (CONTRACT.md section 3):
// daemon_cli.go's shutdown sequence must transition the in-process
// *daemonState into lifecycle.StateDraining through the existing
// daemonState.transition/tryTransitionLocked choke point BEFORE the process
// begins its bounded HTTP drain/advertises `stopping` — never a bare
// `state.status = ...` field write, and never a second invented transition
// helper (both explicitly forbidden by CONTRACT.md section 3). These tests
// assert the transition via daemonState's own exposed status
// (state.snapshot(), the same accessor daemon_health_test.go uses), not
// merely "no panic", per the phase's real-test requirement.
package main

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
)

// TestBeginDaemonDrain_ReadyToDraining proves the ordinary case: a daemon
// sitting `ready` (idle, no accepted work) legally reaches `draining` via
// beginDaemonDrain, which is exactly the ADR-005 `ready -> draining` row
// (IntentDrain/ReasonOperatorDrain) applied through daemonState.transition.
func TestBeginDaemonDrain_ReadyToDraining(t *testing.T) {
	s := selfProbeTestState() // status: starting
	if err := s.transition(lifecycle.StateReady, lifecycle.IntentObserve, lifecycle.ReasonReadinessProven); err != nil {
		t.Fatalf("precondition starting->ready: %v", err)
	}

	status, _, _ := s.snapshot()
	if status != lifecycle.StateReady {
		t.Fatalf("precondition: status = %v, want %v", status, lifecycle.StateReady)
	}

	if err := beginDaemonDrain(s); err != nil {
		t.Fatalf("beginDaemonDrain(ready): %v", err)
	}

	status, _, _ = s.snapshot()
	if status != lifecycle.StateDraining {
		t.Fatalf("status after beginDaemonDrain = %v, want %v (ADR-005 ready -> draining)", status, lifecycle.StateDraining)
	}
}

// TestBeginDaemonDrain_WorkingToDraining proves the row-not-candidate lookup
// works for the second live pre-drain source state too: a daemon with an
// active execution (`working`) also legally reaches `draining` (accepted
// work is given its bounded completion opportunity per ADR-005, not
// discarded).
func TestBeginDaemonDrain_WorkingToDraining(t *testing.T) {
	s := selfProbeTestState()
	if err := s.transition(lifecycle.StateReady, lifecycle.IntentObserve, lifecycle.ReasonReadinessProven); err != nil {
		t.Fatalf("precondition starting->ready: %v", err)
	}
	s.setWorking("some task")
	status, _, _ := s.snapshot()
	if status != lifecycle.StateWorking {
		t.Fatalf("precondition: status = %v, want %v", status, lifecycle.StateWorking)
	}

	if err := beginDaemonDrain(s); err != nil {
		t.Fatalf("beginDaemonDrain(working): %v", err)
	}

	status, _, _ = s.snapshot()
	if status != lifecycle.StateDraining {
		t.Fatalf("status after beginDaemonDrain = %v, want %v (ADR-005 working -> draining)", status, lifecycle.StateDraining)
	}
}

// TestBeginDaemonDrain_StartingIsIllegalAndLeavesStatusUnchanged proves the
// negative case ADR-005 requires: a candidate still `starting` (intake was
// never opened; no `starting -> draining` row exists in the normative
// table) legally REFUSES the drain transition, and — because
// tryTransitionLocked only mutates state.status on a validated success —
// status is left exactly as it was, never silently forced into `draining`.
func TestBeginDaemonDrain_StartingIsIllegalAndLeavesStatusUnchanged(t *testing.T) {
	s := selfProbeTestState() // status: starting (newDaemonState's initial state)
	status, _, _ := s.snapshot()
	if status != lifecycle.StateStarting {
		t.Fatalf("precondition: status = %v, want %v", status, lifecycle.StateStarting)
	}

	if err := beginDaemonDrain(s); err == nil {
		t.Fatal("expected beginDaemonDrain(starting) to be refused (no starting->draining row in ADR-005's table)")
	}

	status, _, _ = s.snapshot()
	if status != lifecycle.StateStarting {
		t.Fatalf("status mutated despite illegal transition: got %v, want unchanged %v", status, lifecycle.StateStarting)
	}
}

// TestBeginDaemonDrain_NilStateErrors proves the nil-state guard so a
// mis-wired call path fails loudly with an error rather than panicking.
func TestBeginDaemonDrain_NilStateErrors(t *testing.T) {
	if err := beginDaemonDrain(nil); err == nil {
		t.Fatal("expected beginDaemonDrain(nil) to error")
	}
}

// TestBeginDaemonDrain_UsesTheSingleChokePoint proves this wiring reuses
// daemonState.transition rather than mutating state.status directly:
// calling beginDaemonDrain twice in a row from `ready` must succeed once
// and then fail the second time (draining -> draining is not a table row —
// every unlisted (from, to) pair, including a self-pair, is illegal), which
// could only be true if the real validated choke point is in use.
func TestBeginDaemonDrain_UsesTheSingleChokePoint(t *testing.T) {
	s := selfProbeTestState()
	if err := s.transition(lifecycle.StateReady, lifecycle.IntentObserve, lifecycle.ReasonReadinessProven); err != nil {
		t.Fatalf("precondition starting->ready: %v", err)
	}
	if err := beginDaemonDrain(s); err != nil {
		t.Fatalf("first beginDaemonDrain(ready): %v", err)
	}
	if err := beginDaemonDrain(s); err == nil {
		t.Fatal("expected second beginDaemonDrain (draining->draining) to be refused: no such row in ADR-005's table")
	}
	status, _, _ := s.snapshot()
	if status != lifecycle.StateDraining {
		t.Fatalf("status after refused repeat transition = %v, want unchanged %v", status, lifecycle.StateDraining)
	}
}
