package lifecycle

import "testing"

// allStates enumerates the closed state list for exhaustive iteration.
// Duplicated here deliberately (rather than reused from a package var) so
// this test independently proves the package's closed membership rather
// than trusting its own internal list.
var allStates = []State{
	StateAbsent, StateStarting, StateReady, StateWorking, StateDegraded,
	StateDraining, StateStopping, StateStopped, StateUpgrading,
}

var allIntents = []Intent{
	IntentEnsure, IntentStart, IntentAcceptWork, IntentCompleteWork,
	IntentObserve, IntentRecover, IntentDrain, IntentStop, IntentForceStop,
	IntentUpgrade, IntentCleanup,
}

var allReasons = []Reason{
	ReasonStartRequested, ReasonCandidateLaunched, ReasonReadinessProven,
	ReasonReadinessTimeout, ReasonDependencyLost, ReasonDependencyRecovered,
	ReasonWorkAccepted, ReasonWorkCompleted, ReasonWorkFailed,
	ReasonOperatorDrain, ReasonOperatorStop, ReasonUpgradeRequested,
	ReasonBinaryStale, ReasonDrainComplete, ReasonDrainDeadline,
	ReasonForcedTakeover, ReasonProcessExited, ReasonCleanupComplete,
	ReasonSupervisorRestart,
}

func TestClosedVocabularyCounts(t *testing.T) {
	if len(allStates) != 9 {
		t.Fatalf("closed state list has %d members, want 9", len(allStates))
	}
	if len(allIntents) != 11 {
		t.Fatalf("closed intent list has %d members, want 11", len(allIntents))
	}
	if len(allReasons) != 19 {
		t.Fatalf("closed reason list has %d members, want 19", len(allReasons))
	}
	if len(transitionTable) != 26 {
		t.Fatalf("transition table has %d rows, want 26 (one literal row per ADR-005 table row)", len(transitionTable))
	}
}

// TestTransitionTableRowsAreLegal is the table-driven test iterating EVERY
// row of the literal transition table: for every (intent, reason)
// alternative the row lists, ValidateTransition must accept it. This proves
// the table encodes at least everything ADR-005 permits.
func TestTransitionTableRowsAreLegal(t *testing.T) {
	for _, row := range transitionTable {
		for _, intent := range row.Intents {
			for _, reason := range row.Reasons {
				t.Run(string(row.From)+"->"+string(row.To)+"/"+string(intent)+"/"+string(reason), func(t *testing.T) {
					if err := ValidateTransition(row.From, row.To, intent, reason); err != nil {
						t.Errorf("ValidateTransition(%s, %s, %s, %s) = %v, want nil", row.From, row.To, intent, reason, err)
					}
				})
			}
		}
	}
}

// TestIllegalPairsExhaustive is the generated/enumerated check ADR-005's
// Verification section requires: every (from, to) state pair NOT explicitly
// present in the transition table must be rejected for EVERY intent/reason
// combination, proving illegality does not depend on which intent or reason
// happens to be supplied.
func TestIllegalPairsExhaustive(t *testing.T) {
	checked := 0
	for _, from := range allStates {
		for _, to := range allStates {
			if _, ok := rowFor(from, to); ok {
				continue // legal pair — covered by TestTransitionTableRowsAreLegal
			}
			for _, intent := range allIntents {
				for _, reason := range allReasons {
					checked++
					if err := ValidateTransition(from, to, intent, reason); err == nil {
						t.Fatalf("ValidateTransition(%s, %s, %s, %s) = nil, want error (pair not in transition table)", from, to, intent, reason)
					}
				}
			}
		}
	}
	// 9*9=81 total pairs, 26 legal, so 55 illegal pairs * 11 intents * 19
	// reasons must all be exercised and rejected.
	wantChecked := (len(allStates)*len(allStates) - len(transitionTable)) * len(allIntents) * len(allReasons)
	if checked != wantChecked {
		t.Fatalf("checked %d illegal (from,to,intent,reason) combinations, want %d", checked, wantChecked)
	}
}

// TestProcessExitedExhaustive proves ADR-005's "Unexpected process exit"
// requirement: every candidate/live state — starting, ready, working,
// degraded, draining, upgrading, and stopping — carries a direct
// observe/process_exited row straight to stopped.
func TestProcessExitedExhaustive(t *testing.T) {
	candidateOrLive := []State{
		StateStarting, StateReady, StateWorking, StateDegraded,
		StateDraining, StateUpgrading, StateStopping,
	}
	for _, from := range candidateOrLive {
		t.Run(string(from), func(t *testing.T) {
			if err := ValidateTransition(from, StateStopped, IntentObserve, ReasonProcessExited); err != nil {
				t.Errorf("ValidateTransition(%s, stopped, observe, process_exited) = %v, want nil", from, err)
			}
		})
	}
}

// TestStoppingAlsoAcceptsForcedTakeover proves the one process_exited
// destination row (`stopping` -> `stopped`) that additionally allows a
// forced-takeover completion, per the table's stop/force_stop/observe cell.
func TestStoppingAlsoAcceptsForcedTakeover(t *testing.T) {
	if err := ValidateTransition(StateStopping, StateStopped, IntentForceStop, ReasonForcedTakeover); err != nil {
		t.Errorf("ValidateTransition(stopping, stopped, force_stop, forced_takeover) = %v, want nil", err)
	}
}

func TestNextReturnsTableDestinations(t *testing.T) {
	got := Next(StateAbsent, IntentStart, ReasonStartRequested)
	if len(got) != 1 || got[0] != StateStarting {
		t.Fatalf("Next(absent, start, start_requested) = %v, want [starting]", got)
	}

	// degraded --recover/dependency_recovered--> {ready, working} is
	// deliberately ambiguous by intent/reason alone (the table disambiguates
	// only via the prose guard on retained active execution), so Next must
	// surface both candidates rather than silently picking one.
	got = Next(StateDegraded, IntentRecover, ReasonDependencyRecovered)
	if len(got) != 2 {
		t.Fatalf("Next(degraded, recover, dependency_recovered) = %v, want 2 candidates (ready, working)", got)
	}

	if got := Next(StateReady, IntentStop, ReasonBinaryStale); len(got) != 0 {
		t.Fatalf("Next(ready, stop, binary_stale) = %v, want no legal destination", got)
	}
}
