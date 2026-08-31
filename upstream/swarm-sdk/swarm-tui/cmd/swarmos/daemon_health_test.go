// cmd/swarmos/daemon_health_test.go
//
// Covers the ADR-005 readiness split introduced in daemon_health.go:
// /readyz reports false/non-ready before any self-probe evidence exists
// and true once selfProbeReady has legally transitioned the daemon to
// `ready`; identity mismatch never proves readiness; the 15s
// timeout->degraded/readiness_timeout path; and the already-exited
// ->stopped/process_exited path. Per Phase 03 CONTRACT.md, these tests
// prove the negative cases (readiness NOT reported before self-probe;
// illegal/mismatched evidence never proves readiness), not only the happy
// path.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
)

// resetSelfProbeSeams restores selfProbeReady's injectable clock/sleep/
// timeout seams to their real implementations, returning a cleanup func.
// Tests must defer the returned func so a fake clock or shrunk timeout
// never leaks into another test. Mirrors resetStopSeams's pattern in
// global_daemon_test.go.
func resetSelfProbeSeams(t *testing.T) (restore func()) {
	t.Helper()
	origSleep := daemonSelfProbeSleepFunc
	origNow := daemonSelfProbeNowFunc
	origTimeout := readinessSelfProbeTimeout
	return func() {
		daemonSelfProbeSleepFunc = origSleep
		daemonSelfProbeNowFunc = origNow
		readinessSelfProbeTimeout = origTimeout
	}
}

// selfProbeTestState builds a daemonState with a fixed, known identity so
// probe evidence can be constructed to deliberately match or mismatch it.
func selfProbeTestState() *daemonState {
	s := newDaemonState("test-daemon", "test-model", "/tmp/ws")
	s.instanceToken = "instance-token"
	s.processStart = "process-start"
	s.executable = "/test/swarmos"
	return s
}

func matchingEvidence(s *daemonState) daemonHealthEvidence {
	return daemonHealthEvidence{
		Handle:        s.handle,
		PID:           os.Getpid(),
		InstanceToken: s.instanceToken,
		ProcessStart:  s.processStart,
		Executable:    s.executable,
	}
}

// ── newDaemonState / daemonState.transition ─────────────────────────────

func TestNewDaemonState_StartsInStarting(t *testing.T) {
	s := newDaemonState("h", "m", "/ws")
	status, _, _ := s.snapshot()
	if status != lifecycle.StateStarting {
		t.Fatalf("newDaemonState status = %v, want %v (ADR-005: never ready before self-probe)",
			status, lifecycle.StateStarting)
	}
}

func TestDaemonStateTransition_RejectsIllegalTransition(t *testing.T) {
	s := selfProbeTestState() // status: starting
	if err := s.transition(lifecycle.StateWorking, lifecycle.IntentAcceptWork, lifecycle.ReasonWorkAccepted); err == nil {
		t.Fatal("expected illegal starting->working transition to be rejected")
	}
	status, _, _ := s.snapshot()
	if status != lifecycle.StateStarting {
		t.Fatalf("status mutated despite illegal transition: got %v, want unchanged %v", status, lifecycle.StateStarting)
	}
}

func TestDaemonStateTransition_AcceptsLegalTransition(t *testing.T) {
	s := selfProbeTestState()
	if err := s.transition(lifecycle.StateReady, lifecycle.IntentObserve, lifecycle.ReasonReadinessProven); err != nil {
		t.Fatalf("expected legal starting->ready transition to succeed: %v", err)
	}
	status, _, _ := s.snapshot()
	if status != lifecycle.StateReady {
		t.Fatalf("status = %v, want %v", status, lifecycle.StateReady)
	}
}

// ── /readyz ───────────────────────────────────────────────────────────

func TestDaemonReadyzHandler_NotReadyBeforeSelfProbe(t *testing.T) {
	state := selfProbeTestState()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	daemonReadyzHandler(state)(rec, req)

	if rec.Code != 503 {
		t.Fatalf("status code = %d, want 503 before any self-probe", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["ready"] != false {
		t.Errorf("ready = %v, want false", body["ready"])
	}
	if body["status"] != lifecycle.StateStarting.String() {
		t.Errorf("status = %v, want %v", body["status"], lifecycle.StateStarting.String())
	}
}

func TestDaemonReadyzHandler_ReadyAfterSuccessfulSelfProbe(t *testing.T) {
	state := selfProbeTestState()
	defer resetSelfProbeSeams(t)()

	evidence := matchingEvidence(state)
	probe := func() (daemonHealthEvidence, error) { return evidence, nil }
	if err := selfProbeReady(context.Background(), state, probe, func() bool { return true }); err != nil {
		t.Fatalf("selfProbeReady: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	daemonReadyzHandler(state)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status code = %d, want 200 after successful self-probe", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["ready"] != true {
		t.Errorf("ready = %v, want true", body["ready"])
	}
	if body["status"] != lifecycle.StateReady.String() {
		t.Errorf("status = %v, want %v", body["status"], lifecycle.StateReady.String())
	}
	if body["handle"] != state.handle {
		t.Errorf("handle = %v, want %v", body["handle"], state.handle)
	}
	if body["instance_token"] != state.instanceToken {
		t.Errorf("instance_token = %v, want %v", body["instance_token"], state.instanceToken)
	}
}

// ── selfProbeReady negative cases (Phase 03 CONTRACT.md requires proving
//    these, not only the happy path) ─────────────────────────────────────

func TestSelfProbeReady_IdentityMismatchNeverProvesReady(t *testing.T) {
	state := selfProbeTestState()
	defer resetSelfProbeSeams(t)()

	readinessSelfProbeTimeout = 30 * time.Millisecond
	fakeNow := time.Now()
	daemonSelfProbeNowFunc = func() time.Time { return fakeNow }
	daemonSelfProbeSleepFunc = func(d time.Duration) { fakeNow = fakeNow.Add(d) }

	mismatched := daemonHealthEvidence{Handle: "someone-else-daemon", PID: 999999}
	probe := func() (daemonHealthEvidence, error) { return mismatched, nil }

	err := selfProbeReady(context.Background(), state, probe, func() bool { return true })
	if err == nil {
		t.Fatal("expected selfProbeReady to fail when probe evidence identity never matches")
	}
	status, _, _ := state.snapshot()
	if status == lifecycle.StateReady {
		t.Fatalf("status must never become ready from mismatched identity evidence, got %v", status)
	}
	if status != lifecycle.StateDegraded {
		t.Fatalf("status = %v, want degraded once the identity-mismatched self-probe times out", status)
	}
}

func TestSelfProbeReady_TimeoutTransitionsToDegraded(t *testing.T) {
	state := selfProbeTestState()
	defer resetSelfProbeSeams(t)()

	readinessSelfProbeTimeout = 30 * time.Millisecond
	fakeNow := time.Now()
	daemonSelfProbeNowFunc = func() time.Time { return fakeNow }
	daemonSelfProbeSleepFunc = func(d time.Duration) { fakeNow = fakeNow.Add(d) }

	attempts := 0
	probe := func() (daemonHealthEvidence, error) {
		attempts++
		return daemonHealthEvidence{}, fmt.Errorf("connection refused")
	}
	err := selfProbeReady(context.Background(), state, probe, func() bool { return true })
	if err == nil {
		t.Fatal("expected selfProbeReady to report a timeout error")
	}
	status, _, _ := state.snapshot()
	if status != lifecycle.StateDegraded {
		t.Fatalf("status = %v, want degraded (readiness_timeout) after a still-live candidate exceeds the deadline", status)
	}
	if attempts == 0 {
		t.Error("expected at least one probe attempt before the deadline was reached")
	}
}

func TestSelfProbeReady_AlreadyExitedTransitionsToStopped(t *testing.T) {
	state := selfProbeTestState()
	defer resetSelfProbeSeams(t)()

	probe := func() (daemonHealthEvidence, error) { return daemonHealthEvidence{}, fmt.Errorf("connection refused") }
	err := selfProbeReady(context.Background(), state, probe, func() bool { return false })
	if err == nil {
		t.Fatal("expected selfProbeReady to report an error for an already-exited candidate")
	}
	status, _, _ := state.snapshot()
	if status != lifecycle.StateStopped {
		t.Fatalf("status = %v, want stopped (process_exited) when the candidate is found already exited", status)
	}
}

func TestSelfProbeReady_RespectsContextCancellation(t *testing.T) {
	state := selfProbeTestState()
	defer resetSelfProbeSeams(t)()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	probe := func() (daemonHealthEvidence, error) { return daemonHealthEvidence{}, fmt.Errorf("connection refused") }
	err := selfProbeReady(ctx, state, probe, func() bool { return true })
	if err == nil {
		t.Fatal("expected selfProbeReady to return the context's cancellation error")
	}
	status, _, _ := state.snapshot()
	if status != lifecycle.StateStarting {
		t.Fatalf("status = %v, want unchanged %v on immediate cancellation (no self-probe evidence was ever obtained)",
			status, lifecycle.StateStarting)
	}
}

func TestSelfProbeReady_EventualSuccessAfterInitialFailures(t *testing.T) {
	state := selfProbeTestState()
	defer resetSelfProbeSeams(t)()

	readinessSelfProbeTimeout = 5 * time.Second
	daemonSelfProbeSleepFunc = func(time.Duration) {} // don't actually sleep in the test

	attempts := 0
	evidence := matchingEvidence(state)
	probe := func() (daemonHealthEvidence, error) {
		attempts++
		if attempts < 3 {
			return daemonHealthEvidence{}, fmt.Errorf("not ready yet")
		}
		return evidence, nil
	}
	if err := selfProbeReady(context.Background(), state, probe, func() bool { return true }); err != nil {
		t.Fatalf("selfProbeReady: %v", err)
	}
	status, _, _ := state.snapshot()
	if status != lifecycle.StateReady {
		t.Fatalf("status = %v, want ready once matching evidence eventually arrives", status)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want exactly 3 (fails twice, then succeeds)", attempts)
	}
}
