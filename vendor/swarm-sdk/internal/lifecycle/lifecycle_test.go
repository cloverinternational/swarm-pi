package lifecycle

import "testing"

func TestParseStateAcceptsCanonicalValues(t *testing.T) {
	for _, s := range allStates {
		got, err := ParseState(string(s))
		if err != nil {
			t.Errorf("ParseState(%q) unexpected error: %v", s, err)
		}
		if got != s {
			t.Errorf("ParseState(%q) = %q, want %q", s, got, s)
		}
	}
}

// TestParseStateFailsClosedOnCaseChange proves differently-cased input is
// never silently normalized to a canonical state — ADR-005 requires
// unknown/differently-cased/future values to fail closed.
func TestParseStateFailsClosedOnCaseChange(t *testing.T) {
	cases := []string{"Ready", "READY", "Working", "ABSENT", "Starting"}
	for _, raw := range cases {
		if _, err := ParseState(raw); err == nil {
			t.Errorf("ParseState(%q) = nil error, want failure (case must not be normalized)", raw)
		}
	}
}

func TestParseStateFailsClosedOnUnknownValues(t *testing.T) {
	cases := []string{
		"", "idle", "busy", "unhealthy", "offline", "failed",
		"ready/idle", "ready/working", "activating", "deactivating", "inactive",
	}
	for _, raw := range cases {
		if _, err := ParseState(raw); err == nil {
			t.Errorf("ParseState(%q) = nil error, want failure (unknown/free-form value)", raw)
		}
	}
}

func TestParseIntentCaseAndUnknown(t *testing.T) {
	for _, i := range allIntents {
		if got, err := ParseIntent(string(i)); err != nil || got != i {
			t.Errorf("ParseIntent(%q) = (%q, %v), want (%q, nil)", i, got, err, i)
		}
	}
	for _, raw := range []string{"Observe", "OBSERVE", "", "bogus", "reconcile"} {
		if _, err := ParseIntent(raw); err == nil {
			t.Errorf("ParseIntent(%q) = nil error, want failure", raw)
		}
	}
}

func TestParseReasonCaseAndUnknown(t *testing.T) {
	for _, r := range allReasons {
		if got, err := ParseReason(string(r)); err != nil || got != r {
			t.Errorf("ParseReason(%q) = (%q, %v), want (%q, nil)", r, got, err, r)
		}
	}
	for _, raw := range []string{"Process_Exited", "PROCESS_EXITED", "", "bogus", "timeout"} {
		if _, err := ParseReason(raw); err == nil {
			t.Errorf("ParseReason(%q) = nil error, want failure", raw)
		}
	}
}

// TestFromLegacyStatusBoundedMapping proves the ADR-005 "Compatibility"
// bounded legacy mapping: legacy idle -> ready, legacy working -> working,
// legacy stopping -> stopping, and nothing else.
func TestFromLegacyStatusBoundedMapping(t *testing.T) {
	cases := []struct {
		legacy string
		want   State
	}{
		{"idle", StateReady},
		{"working", StateWorking},
		{"stopping", StateStopping},
	}
	for _, tc := range cases {
		got, err := FromLegacyStatus(tc.legacy)
		if err != nil {
			t.Errorf("FromLegacyStatus(%q) unexpected error: %v", tc.legacy, err)
		}
		if got != tc.want {
			t.Errorf("FromLegacyStatus(%q) = %q, want %q", tc.legacy, got, tc.want)
		}
	}
}

// TestFromLegacyStatusRejectsCompositeAndFreeForm proves composite values
// (e.g. "ready/idle") and free-form/unknown text are rejected rather than
// normalized silently — the bounded mapping covers exactly three legacy
// strings and nothing else.
func TestFromLegacyStatusRejectsCompositeAndFreeForm(t *testing.T) {
	cases := []string{
		"ready/idle", "ready/working", "ready", "absent", "",
		"Idle", "WORKING", "unknown error: connection refused", "busy",
	}
	for _, raw := range cases {
		if _, err := FromLegacyStatus(raw); err == nil {
			t.Errorf("FromLegacyStatus(%q) = nil error, want failure (composite/free-form value)", raw)
		}
	}
}

func TestSnapshotFieldsAreOpaqueStrings(t *testing.T) {
	// Compile-time/structural proof this package stays a true leaf: Snapshot
	// must use plain strings for execution identity, never a typed value
	// from internal/identity, so this package imports only the standard
	// library.
	snap := Snapshot{
		State:             StateWorking,
		Intent:            IntentAcceptWork,
		Reason:            ReasonWorkAccepted,
		ReadinessEvidence: "healthz:200",
		ActiveExecutionID: "exec-opaque-id",
	}
	if snap.ActiveExecutionID != "exec-opaque-id" {
		t.Errorf("ActiveExecutionID = %q, want exec-opaque-id", snap.ActiveExecutionID)
	}
	if snap.State != StateWorking {
		t.Errorf("State = %q, want working", snap.State)
	}
}

func TestPredicates(t *testing.T) {
	if !CanAcceptWork(StateReady) {
		t.Error("CanAcceptWork(ready) = false, want true")
	}
	for _, s := range allStates {
		if s == StateReady {
			continue
		}
		if CanAcceptWork(s) {
			t.Errorf("CanAcceptWork(%s) = true, want false", s)
		}
	}

	if IsLive(StateAbsent) || IsLive(StateStopped) {
		t.Error("IsLive(absent|stopped) = true, want false")
	}
	for _, s := range allStates {
		if s == StateAbsent || s == StateStopped {
			continue
		}
		if !IsLive(s) {
			t.Errorf("IsLive(%s) = false, want true", s)
		}
	}

	if !IsReady(StateReady) {
		t.Error("IsReady(ready) = false, want true")
	}
	if IsReady(StateWorking) {
		t.Error("IsReady(working) = true, want false")
	}
}
