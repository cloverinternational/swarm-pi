package lifecycle

import "fmt"

// transitionRow is one row of ADR-005's single normative transition table.
// A row permits only the listed (From, To) pair for one of its Intents
// combined with one of its Reasons — multiple intents/reasons in a row are
// alternatives, not a requirement that all be satisfied simultaneously.
// Guards/evidence described in the ADR's table (for example "no active
// execution" or "intake is atomically closed before publication") are
// external preconditions this package does not observe directly; callers
// remain responsible for holding them before calling ValidateTransition.
type transitionRow struct {
	From    State
	To      State
	Intents []Intent
	Reasons []Reason
}

// transitionTable is THE single literal encoding of ADR-005's "Normative
// transition table". Every row here is legal; every (From, To) pair not
// present in this table is illegal regardless of intent or reason. This is
// the ONLY place transitions are enumerated — ValidateTransition and Next
// are both pure lookups over this slice.
var transitionTable = []transitionRow{
	{StateAbsent, StateStarting,
		[]Intent{IntentEnsure, IntentStart, IntentRecover},
		[]Reason{ReasonStartRequested, ReasonCandidateLaunched, ReasonSupervisorRestart}},
	{StateStarting, StateReady,
		[]Intent{IntentObserve, IntentRecover},
		[]Reason{ReasonReadinessProven}},
	{StateStarting, StateDegraded,
		[]Intent{IntentObserve, IntentRecover},
		[]Reason{ReasonReadinessTimeout, ReasonDependencyLost}},
	{StateStarting, StateStopping,
		[]Intent{IntentStop, IntentForceStop},
		[]Reason{ReasonOperatorStop, ReasonForcedTakeover}},
	{StateStarting, StateStopped,
		[]Intent{IntentObserve},
		[]Reason{ReasonProcessExited}},
	{StateReady, StateWorking,
		[]Intent{IntentAcceptWork},
		[]Reason{ReasonWorkAccepted}},
	{StateReady, StateDegraded,
		[]Intent{IntentObserve},
		[]Reason{ReasonDependencyLost, ReasonReadinessTimeout}},
	{StateReady, StateDraining,
		[]Intent{IntentDrain, IntentStop},
		[]Reason{ReasonOperatorDrain, ReasonOperatorStop}},
	{StateReady, StateUpgrading,
		[]Intent{IntentUpgrade},
		[]Reason{ReasonUpgradeRequested, ReasonBinaryStale}},
	{StateReady, StateStopped,
		[]Intent{IntentObserve},
		[]Reason{ReasonProcessExited}},
	{StateWorking, StateReady,
		[]Intent{IntentCompleteWork, IntentObserve},
		[]Reason{ReasonWorkCompleted, ReasonWorkFailed}},
	{StateWorking, StateDegraded,
		[]Intent{IntentObserve},
		[]Reason{ReasonDependencyLost, ReasonReadinessTimeout}},
	{StateWorking, StateDraining,
		[]Intent{IntentDrain, IntentStop},
		[]Reason{ReasonOperatorDrain, ReasonOperatorStop}},
	{StateWorking, StateStopped,
		[]Intent{IntentObserve},
		[]Reason{ReasonProcessExited}},
	{StateDegraded, StateReady,
		[]Intent{IntentRecover, IntentObserve},
		[]Reason{ReasonDependencyRecovered, ReasonReadinessProven}},
	{StateDegraded, StateWorking,
		[]Intent{IntentRecover, IntentObserve},
		[]Reason{ReasonDependencyRecovered, ReasonReadinessProven}},
	{StateDegraded, StateDraining,
		[]Intent{IntentDrain, IntentStop},
		[]Reason{ReasonOperatorDrain, ReasonOperatorStop}},
	{StateDegraded, StateStopping,
		[]Intent{IntentForceStop},
		[]Reason{ReasonForcedTakeover}},
	{StateDegraded, StateStopped,
		[]Intent{IntentObserve},
		[]Reason{ReasonProcessExited}},
	{StateDraining, StateStopping,
		[]Intent{IntentStop, IntentForceStop, IntentObserve},
		[]Reason{ReasonDrainComplete, ReasonDrainDeadline, ReasonForcedTakeover}},
	{StateDraining, StateStopped,
		[]Intent{IntentObserve},
		[]Reason{ReasonProcessExited}},
	{StateUpgrading, StateDraining,
		[]Intent{IntentDrain, IntentUpgrade},
		[]Reason{ReasonOperatorDrain, ReasonUpgradeRequested}},
	{StateUpgrading, StateStopping,
		[]Intent{IntentUpgrade, IntentForceStop},
		[]Reason{ReasonDrainComplete, ReasonDrainDeadline, ReasonForcedTakeover}},
	{StateUpgrading, StateStopped,
		[]Intent{IntentObserve},
		[]Reason{ReasonProcessExited}},
	{StateStopping, StateStopped,
		[]Intent{IntentObserve, IntentStop, IntentForceStop},
		[]Reason{ReasonProcessExited, ReasonForcedTakeover}},
	{StateStopped, StateAbsent,
		[]Intent{IntentCleanup, IntentObserve},
		[]Reason{ReasonCleanupComplete}},
}

func containsIntent(list []Intent, want Intent) bool {
	for _, i := range list {
		if i == want {
			return true
		}
	}
	return false
}

func containsReason(list []Reason, want Reason) bool {
	for _, r := range list {
		if r == want {
			return true
		}
	}
	return false
}

// rowFor returns the transitionTable row for (from, to), if any.
func rowFor(from, to State) (transitionRow, bool) {
	for _, row := range transitionTable {
		if row.From == from && row.To == to {
			return row, true
		}
	}
	return transitionRow{}, false
}

// ValidateTransition reports whether (from, to, intent, reason) is a legal
// row of ADR-005's normative transition table. Every (from, to) pair absent
// from the table is illegal regardless of intent or reason. When the pair
// is present, intent must be one of that row's permitted intents and reason
// must be one of that row's permitted reasons (independently — they are
// alternatives within their own column, not paired positionally).
//
// This function does not and cannot check the table's prose "Required
// guard/evidence" column (for example "no active execution", "intake is
// atomically closed before publication"); those are external preconditions
// the caller must hold before invoking a transition.
func ValidateTransition(from, to State, intent Intent, reason Reason) error {
	if !from.Valid() {
		return fmt.Errorf("lifecycle: invalid from-state %q", from)
	}
	if !to.Valid() {
		return fmt.Errorf("lifecycle: invalid to-state %q", to)
	}
	if !intent.Valid() {
		return fmt.Errorf("lifecycle: invalid intent %q", intent)
	}
	if !reason.Valid() {
		return fmt.Errorf("lifecycle: invalid reason %q", reason)
	}
	row, ok := rowFor(from, to)
	if !ok {
		return fmt.Errorf("lifecycle: illegal transition %s -> %s", from, to)
	}
	if !containsIntent(row.Intents, intent) {
		return fmt.Errorf("lifecycle: intent %q not permitted for %s -> %s", intent, from, to)
	}
	if !containsReason(row.Reasons, reason) {
		return fmt.Errorf("lifecycle: reason %q not permitted for %s -> %s", reason, from, to)
	}
	return nil
}

// Next returns every destination state the table permits from `from` for
// the given (intent, reason) pair. It is usually a single state; it returns
// more than one only where the table's intent/reason columns alone cannot
// disambiguate two rows sharing the same source state, intent, and reason
// set (for example `degraded` --recover/dependency_recovered--> `ready` or
// `working`, distinguished only by the table's prose guard on whether a
// retained active execution exists). Callers that must disambiguate such a
// case apply that guard themselves and then confirm the chosen destination
// with ValidateTransition. An empty result means the table permits no
// transition for these inputs.
func Next(from State, intent Intent, reason Reason) []State {
	var out []State
	for _, row := range transitionTable {
		if row.From != from {
			continue
		}
		if !containsIntent(row.Intents, intent) {
			continue
		}
		if !containsReason(row.Reasons, reason) {
			continue
		}
		out = append(out, row.To)
	}
	return out
}
