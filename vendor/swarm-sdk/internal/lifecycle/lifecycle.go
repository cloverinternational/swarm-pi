// Package lifecycle encodes ADR-005's closed daemon lifecycle vocabulary:
// the nine states, eleven intents, and closed reason-code set a daemon
// authority uses to describe and validate its own transitions. It is a
// dependency-free leaf package (standard library only) so every other
// package — identity, presence/a2a, gateway, and the swarmos CLI adapters —
// can depend on it without creating an import cycle.
//
// See docs/architecture/swarm-attach/adr-005-lifecycle-authority.md for the
// normative decision this package encodes byte-for-byte. Nothing in this
// package invents a state, intent, or reason beyond what that ADR lists;
// unknown, differently-cased, or free-form values must fail closed rather
// than silently normalize.
package lifecycle

import (
	"fmt"
	"time"
)

// State is one of ADR-005's nine closed, lower-case, serialized lifecycle
// states. No other value is a valid protocol state.
type State string

// The closed lifecycle state list, exactly as ADR-005 "Closed lifecycle
// vocabulary" defines it. Order here matches the ADR's prose ordering.
const (
	StateAbsent    State = "absent"
	StateStarting  State = "starting"
	StateReady     State = "ready"
	StateWorking   State = "working"
	StateDegraded  State = "degraded"
	StateDraining  State = "draining"
	StateStopping  State = "stopping"
	StateStopped   State = "stopped"
	StateUpgrading State = "upgrading"
)

// String returns the canonical lower-case wire value.
func (s State) String() string { return string(s) }

// validStates is the closed membership set backing State.Valid and
// ParseState. Keeping it as a map (rather than a repeated switch) means
// there is exactly one place the nine states are enumerated for validation.
var validStates = map[State]bool{
	StateAbsent:    true,
	StateStarting:  true,
	StateReady:     true,
	StateWorking:   true,
	StateDegraded:  true,
	StateDraining:  true,
	StateStopping:  true,
	StateStopped:   true,
	StateUpgrading: true,
}

// Valid reports whether s is one of the nine closed lifecycle states.
func (s State) Valid() bool { return validStates[s] }

// ParseState decodes raw into a closed State, failing closed on unknown,
// missing, differently-cased, or composite values (for example "ready/idle")
// rather than normalizing them. Callers that need the bounded legacy
// mapping ("idle"/"working"/"stopping") must call FromLegacyStatus instead;
// ParseState never guesses at a legacy synonym.
func ParseState(raw string) (State, error) {
	s := State(raw)
	if !s.Valid() {
		return "", fmt.Errorf("lifecycle: unknown state %q", raw)
	}
	return s, nil
}

// Intent is what an authorized actor asks the lifecycle state machine to
// do. It is not itself a state and does not prove a transition occurred.
type Intent string

// The closed intent vocabulary, exactly as ADR-005 "Intents and reason
// codes" defines it.
const (
	IntentEnsure       Intent = "ensure"
	IntentStart        Intent = "start"
	IntentAcceptWork   Intent = "accept_work"
	IntentCompleteWork Intent = "complete_work"
	IntentObserve      Intent = "observe"
	IntentRecover      Intent = "recover"
	IntentDrain        Intent = "drain"
	IntentStop         Intent = "stop"
	IntentForceStop    Intent = "force_stop"
	IntentUpgrade      Intent = "upgrade"
	IntentCleanup      Intent = "cleanup"
)

// String returns the canonical lower-case wire value.
func (i Intent) String() string { return string(i) }

var validIntents = map[Intent]bool{
	IntentEnsure:       true,
	IntentStart:        true,
	IntentAcceptWork:   true,
	IntentCompleteWork: true,
	IntentObserve:      true,
	IntentRecover:      true,
	IntentDrain:        true,
	IntentStop:         true,
	IntentForceStop:    true,
	IntentUpgrade:      true,
	IntentCleanup:      true,
}

// Valid reports whether i is one of the eleven closed intents.
func (i Intent) Valid() bool { return validIntents[i] }

// ParseIntent decodes raw into a closed Intent, failing closed on unknown or
// differently-cased values.
func ParseIntent(raw string) (Intent, error) {
	i := Intent(raw)
	if !i.Valid() {
		return "", fmt.Errorf("lifecycle: unknown intent %q", raw)
	}
	return i, nil
}

// Reason is a stable machine reason code recording why a validated
// transition occurred. It is separate from Intent: an observe intent can
// produce dependency_lost, and a stop intent can produce operator_stop.
type Reason string

// The closed reason-code set, exactly as ADR-005 "Intents and reason codes"
// defines it.
const (
	ReasonStartRequested      Reason = "start_requested"
	ReasonCandidateLaunched   Reason = "candidate_launched"
	ReasonReadinessProven     Reason = "readiness_proven"
	ReasonReadinessTimeout    Reason = "readiness_timeout"
	ReasonDependencyLost      Reason = "dependency_lost"
	ReasonDependencyRecovered Reason = "dependency_recovered"
	ReasonWorkAccepted        Reason = "work_accepted"
	ReasonWorkCompleted       Reason = "work_completed"
	ReasonWorkFailed          Reason = "work_failed"
	ReasonOperatorDrain       Reason = "operator_drain"
	ReasonOperatorStop        Reason = "operator_stop"
	ReasonUpgradeRequested    Reason = "upgrade_requested"
	ReasonBinaryStale         Reason = "binary_stale"
	ReasonDrainComplete       Reason = "drain_complete"
	ReasonDrainDeadline       Reason = "drain_deadline"
	ReasonForcedTakeover      Reason = "forced_takeover"
	ReasonProcessExited       Reason = "process_exited"
	ReasonCleanupComplete     Reason = "cleanup_complete"
	ReasonSupervisorRestart   Reason = "supervisor_restart"
)

// String returns the canonical lower-case wire value.
func (r Reason) String() string { return string(r) }

var validReasons = map[Reason]bool{
	ReasonStartRequested:      true,
	ReasonCandidateLaunched:   true,
	ReasonReadinessProven:     true,
	ReasonReadinessTimeout:    true,
	ReasonDependencyLost:      true,
	ReasonDependencyRecovered: true,
	ReasonWorkAccepted:        true,
	ReasonWorkCompleted:       true,
	ReasonWorkFailed:          true,
	ReasonOperatorDrain:       true,
	ReasonOperatorStop:        true,
	ReasonUpgradeRequested:    true,
	ReasonBinaryStale:         true,
	ReasonDrainComplete:       true,
	ReasonDrainDeadline:       true,
	ReasonForcedTakeover:      true,
	ReasonProcessExited:       true,
	ReasonCleanupComplete:     true,
	ReasonSupervisorRestart:   true,
}

// Valid reports whether r is one of the closed reason codes.
func (r Reason) Valid() bool { return validReasons[r] }

// ParseReason decodes raw into a closed Reason, failing closed on unknown or
// differently-cased values.
func ParseReason(raw string) (Reason, error) {
	r := Reason(raw)
	if !r.Valid() {
		return "", fmt.Errorf("lifecycle: unknown reason %q", raw)
	}
	return r, nil
}

// Snapshot bundles one observation of a daemon's lifecycle exactly as
// ADR-005 describes: "Every observation joins the daemon instance identity,
// authority mode, lifecycle state, intent, reason code, readiness evidence,
// active execution identity when any, and timestamp." This package is a
// leaf and must not depend on internal/identity, so ActiveExecutionID is a
// plain opaque string rather than that package's typed identity — callers
// that have a typed execution identity pass its string form.
type Snapshot struct {
	// State is the lifecycle state this observation reports.
	State State
	// Intent is what was asked for; it does not by itself prove State
	// changed (a repeated/no-op intent is recorded as an observation, not a
	// fabricated transition).
	Intent Intent
	// Reason records why the transition (or observation) occurred.
	Reason Reason
	// ReadinessEvidence is orthogonal, implementation-defined evidence (for
	// example which self-probes passed) supporting State; it never replaces
	// State with prose and may be empty when not applicable.
	ReadinessEvidence string
	// ActiveExecutionID is the opaque identity of the accepted execution
	// driving a `working` state, if any. Empty when there is none.
	ActiveExecutionID string
	// ObservedAt is when this snapshot was produced.
	ObservedAt time.Time
}

// FromLegacyStatus implements ADR-005's bounded legacy presence-status
// mapping used only during migration: legacy "idle" maps to StateReady,
// legacy "working" maps to StateWorking, and legacy "stopping" maps to
// StateStopping. Every other value — including composite values such as
// "ready/idle" or "ready/working", free-form errors, and any other
// unrecognized text — is rejected rather than silently normalized, per the
// ADR's "Compatibility" section.
func FromLegacyStatus(legacy string) (State, error) {
	switch legacy {
	case "idle":
		return StateReady, nil
	case "working":
		return StateWorking, nil
	case "stopping":
		return StateStopping, nil
	default:
		return "", fmt.Errorf("lifecycle: unsupported legacy status %q", legacy)
	}
}
