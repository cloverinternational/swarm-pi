// Package lifecycle — status.go
//
// This file defines the ONE canonical status/snapshot DTO that every
// surface which already exposes daemon status (healthz/readyz payload,
// presence display, CLI `swarm status`/`swarm list`, and the
// `client.snapshot` RPC / `A2ADebugResponse`) is meant to format from, per
// Phase 03's readiness-status-registry contract and ADR-005's "Readiness,
// publication, and adapters" section.
//
// StatusView wraps the existing Phase 02 Snapshot (state, intent, reason,
// evidence) exactly as-is and adds a JSON-serializable shape, a
// String()/Summary() human formatter, and a DisplayLabel — a clearly
// separate, explicitly-derived presentation field. Per ADR-005: "Friendly
// labels are explicitly derived display state and never round-trip as
// protocol state." Nothing in this file ever assigns a DisplayLabel value
// back into the canonical State field; DeriveDisplayLabel is a pure
// function that only reads a Snapshot and returns a DisplayLabel, it never
// mutates or returns a State.
//
// This file stays a leaf: it imports only the standard library plus this
// package's own Phase 02 types (State/Intent/Reason/Snapshot from
// lifecycle.go). It must never import internal/a2a, internal/identity,
// internal/lan, or any swarm-tui package — see package-boundaries.md.
package lifecycle

import (
	"fmt"
	"time"
)

// DisplayLabel is an explicitly derived, human-friendly presentation value
// for status surfaces (CLI, presence, UI, logs). It is NEVER a valid
// protocol State and must never be written back into StatusView.State or
// any Snapshot.State field — ADR-005's "Readiness, publication, and
// adapters" section is explicit that friendly labels "never round-trip as
// protocol state." Keeping this as a distinct Go type (rather than reusing
// State) makes that distinction impossible to blur by accident: nothing in
// this package accepts a DisplayLabel anywhere a State is expected.
type DisplayLabel string

// The closed set of friendly display labels this package derives, exactly
// as ADR-005's "Readiness, publication, and adapters" section documents by
// example. DisplayUnknown is this package's own fallback for evidence that
// does not (yet) support a confident friendly label — it is not an ADR-005
// value itself, only a safe default for callers that must render something.
const (
	// DisplayIdle is derived from state=ready with no active execution.
	DisplayIdle DisplayLabel = "idle"
	// DisplayBusy is derived from state=working.
	DisplayBusy DisplayLabel = "busy"
	// DisplayUnhealthy is derived from state=degraded.
	DisplayUnhealthy DisplayLabel = "unhealthy"
	// DisplayOffline is derived from state=absent or state=stopped, plus
	// discovery evidence (this package cannot itself perform discovery, so
	// the caller supplies that evidence as a bool — see DeriveDisplayLabel).
	DisplayOffline DisplayLabel = "offline"
	// DisplayRestartPending is derived from state=working plus a pending
	// upgrade intent.
	DisplayRestartPending DisplayLabel = "restart pending"
	// DisplayUnknown is returned when the available evidence does not
	// support any of the ADR-005-documented labels above (for example an
	// empty/zero Snapshot, or state=absent/stopped without discovery
	// evidence). It is a safe fallback, not a protocol value.
	DisplayUnknown DisplayLabel = "unknown"
)

// String returns the display label text.
func (d DisplayLabel) String() string { return string(d) }

// StatusView is the canonical status/snapshot DTO. It carries the exact
// Phase 02 Snapshot fields (state, intent, reason, readiness evidence,
// active execution identity, observation time) in a JSON-serializable
// shape with canonical lower-case wire values (e.g. "ready", not "Ready"),
// plus a separately-named DisplayLabel field holding the derived friendly
// presentation value. State and DisplayLabel are deliberately different Go
// fields (and DisplayLabel is a distinct Go type from State) so a caller —
// or a JSON round-trip — cannot confuse or merge them.
type StatusView struct {
	// State is the canonical ADR-005 lifecycle state. Machine-readable,
	// closed-vocabulary, lower-case. Never overwritten by DisplayLabel.
	State State `json:"state"`
	// Intent is what was asked for in the observation this view reports.
	// Empty when not applicable.
	Intent Intent `json:"intent,omitempty"`
	// Reason records why the transition (or observation) occurred. Empty
	// when not applicable.
	Reason Reason `json:"reason,omitempty"`
	// ReadinessEvidence is orthogonal, implementation-defined evidence
	// supporting State (for example which self-probes passed). Never
	// replaces State with prose.
	ReadinessEvidence string `json:"readiness_evidence,omitempty"`
	// ActiveExecutionID is the opaque identity of the accepted execution
	// driving a `working` state, if any. Empty when there is none.
	ActiveExecutionID string `json:"active_execution_id,omitempty"`
	// ObservedAt is when the underlying Snapshot was produced.
	ObservedAt time.Time `json:"observed_at"`

	// DisplayLabel is the explicitly derived, human-friendly presentation
	// value (idle/busy/unhealthy/offline/"restart pending"/unknown) for
	// CLI/UI/log rendering. It is display-only: never a valid State value
	// and never round-tripped back into State by any function in this
	// file.
	DisplayLabel DisplayLabel `json:"display_label"`
}

// Snapshot returns the embedded Phase 02 Snapshot this view was built from,
// without the DisplayLabel — the inverse of NewStatusView (minus the
// derivation inputs, which are not recoverable from the view alone). This
// lets a caller that received a StatusView over the wire recover the plain
// Snapshot for further ADR-005 transition validation without re-deriving
// display state.
func (v StatusView) Snapshot() Snapshot {
	return Snapshot{
		State:             v.State,
		Intent:            v.Intent,
		Reason:            v.Reason,
		ReadinessEvidence: v.ReadinessEvidence,
		ActiveExecutionID: v.ActiveExecutionID,
		ObservedAt:        v.ObservedAt,
	}
}

// Summary returns a bounded, human-readable one-line status description
// suitable for CLI/log use, e.g. `idle [ready]` or
// `unhealthy [degraded] reason=dependency_lost`. The canonical State always
// appears in brackets alongside the friendly DisplayLabel so a reader never
// loses the machine-readable value behind the presentation text.
func (v StatusView) Summary() string {
	label := v.DisplayLabel
	if label == "" {
		label = DisplayUnknown
	}
	state := v.State
	if state == "" {
		state = "unknown"
	}
	s := fmt.Sprintf("%s [%s]", label, state)
	if v.Reason != "" {
		s += fmt.Sprintf(" reason=%s", v.Reason)
	}
	return s
}

// String implements fmt.Stringer as an alias for Summary, so a StatusView
// prints sensibly with %v/%s and in log lines without an explicit
// .Summary() call.
func (v StatusView) String() string {
	return v.Summary()
}

// NewStatusView builds the canonical StatusView from a Phase 02 Snapshot
// plus the two pieces of orthogonal evidence this leaf package cannot
// derive on its own:
//
//   - hasDiscoveryEvidence reports whether an external discovery/presence
//     source (which this package must not import — see package-boundaries.md)
//     has evidence bearing on the "offline" label for an absent/stopped
//     daemon. Without it, an absent/stopped state alone is reported as
//     DisplayUnknown rather than a confident "offline".
//   - pendingUpgrade reports whether an `upgrade` intent is currently
//     pending against a `working` daemon (ADR-005: an upgrade against
//     active work "remains a pending intent while state stays working").
//     It is a distinct input from Snapshot.Intent because Snapshot.Intent
//     records what was asked for in THIS observation, while a pending
//     upgrade may have been requested in an earlier observation and is
//     still awaiting the work to complete.
//
// NewStatusView is pure: it never mutates snap and never assigns the
// derived label into the returned view's State field.
func NewStatusView(snap Snapshot, hasDiscoveryEvidence, pendingUpgrade bool) StatusView {
	return StatusView{
		State:             snap.State,
		Intent:            snap.Intent,
		Reason:            snap.Reason,
		ReadinessEvidence: snap.ReadinessEvidence,
		ActiveExecutionID: snap.ActiveExecutionID,
		ObservedAt:        snap.ObservedAt,
		DisplayLabel:      DeriveDisplayLabel(snap, hasDiscoveryEvidence, pendingUpgrade),
	}
}

// NewStatusViewFromLegacy builds a StatusView from a legacy free-form
// presence status string using ONLY the bounded FromLegacyStatus mapping
// already defined in lifecycle.go (legacy "idle"/"working"/"stopping").
// This exists so every caller that must adapt legacy presence text — the
// CLI included — goes through the SAME mapping rather than each writing its
// own ad hoc translation table. Unrecognized legacy values are returned as
// the same error FromLegacyStatus produces; callers must not fabricate a
// fallback state for them.
func NewStatusViewFromLegacy(legacyStatus string, hasDiscoveryEvidence, pendingUpgrade bool) (StatusView, error) {
	st, err := FromLegacyStatus(legacyStatus)
	if err != nil {
		return StatusView{}, err
	}
	snap := Snapshot{State: st, ObservedAt: time.Now()}
	return NewStatusView(snap, hasDiscoveryEvidence, pendingUpgrade), nil
}

// DeriveDisplayLabel is the pure friendly-label derivation function.
// Per ADR-005's "Readiness, publication, and adapters" section, EXACTLY:
//
//   - "idle" from state=ready with no active execution (ActiveExecutionID
//     empty; per the ADR's closed vocabulary, `ready` already means idle
//     and able to accept work, so a ready observation is always reported
//     as idle regardless of any stray ActiveExecutionID value — `working`
//     is the state that means an execution is active).
//   - "busy" from state=working.
//   - "unhealthy" from state=degraded.
//   - "offline" from state=absent or state=stopped, plus discovery
//     evidence (hasDiscoveryEvidence=true). Without discovery evidence an
//     absent/stopped observation is reported as DisplayUnknown — this
//     leaf package cannot itself confirm discovery has lost the peer.
//   - "restart pending" from state=working plus a pending upgrade intent
//     (pendingUpgrade=true); this takes priority over the plain "busy"
//     label for a working+pending-upgrade daemon.
//
// Every other closed State (starting, draining, stopping, upgrading, or
// the zero value) falls back to DisplayUnknown for the zero value, or to a
// DisplayLabel holding that state's own lower-case text otherwise — a
// readable default that invents no new protocol state and is never
// confused with one of the five ADR-005-documented labels above.
//
// DeriveDisplayLabel is pure: it only reads snap and the two evidence
// flags, and never writes anywhere. It has no way to modify a State value
// even by mistake — it does not receive a pointer and returns only a
// DisplayLabel, never a State.
func DeriveDisplayLabel(snap Snapshot, hasDiscoveryEvidence, pendingUpgrade bool) DisplayLabel {
	switch snap.State {
	case StateReady:
		return DisplayIdle
	case StateWorking:
		if pendingUpgrade {
			return DisplayRestartPending
		}
		return DisplayBusy
	case StateDegraded:
		return DisplayUnhealthy
	case StateAbsent, StateStopped:
		if hasDiscoveryEvidence {
			return DisplayOffline
		}
		return DisplayUnknown
	case "":
		return DisplayUnknown
	default:
		// starting, draining, stopping, upgrading: no ADR-005-documented
		// friendly label exists for these; fall back to the state's own
		// text rather than fabricating one of the five reserved labels.
		return DisplayLabel(snap.State.String())
	}
}
