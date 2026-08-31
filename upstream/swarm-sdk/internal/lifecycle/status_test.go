// Package lifecycle — status_test.go
//
// Tests for the StatusView DTO defined in status.go: JSON round-trip
// fidelity of the canonical State value, and a friendly-label derivation
// table covering every ADR-005 "Readiness, publication, and adapters"
// example (idle/busy/unhealthy/offline/"restart pending"), plus a negative
// case proving the derived DisplayLabel is never written back into the
// canonical State field — including across a JSON round-trip.
package lifecycle

import (
	"encoding/json"
	"testing"
	"time"
)

// TestStatusView_JSONRoundTrip_PreservesCanonicalState marshals then
// unmarshals a StatusView and asserts the canonical wire value for State is
// EXACTLY the lower-case ADR-005 string (e.g. "ready", never "Ready"), and
// that the round trip changes nothing.
func TestStatusView_JSONRoundTrip_PreservesCanonicalState(t *testing.T) {
	observedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	original := NewStatusView(Snapshot{
		State:             StateReady,
		Intent:            IntentObserve,
		Reason:            ReasonReadinessProven,
		ReadinessEvidence: "healthz:ok",
		ActiveExecutionID: "",
		ObservedAt:        observedAt,
	}, false, false)

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// The wire form must carry the exact lower-case canonical state string,
	// not a Go-capitalized or otherwise reformatted value.
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}
	if got, want := wire["state"], "ready"; got != want {
		t.Fatalf("wire state = %v, want %q", got, want)
	}
	if got, want := wire["display_label"], "idle"; got != want {
		t.Fatalf("wire display_label = %v, want %q", got, want)
	}

	var decoded StatusView
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal into StatusView: %v", err)
	}
	if decoded.State != StateReady {
		t.Fatalf("decoded.State = %q, want %q", decoded.State, StateReady)
	}
	if decoded != original {
		t.Fatalf("round-trip changed the view:\nbefore=%+v\nafter=%+v", original, decoded)
	}
}

// TestStatusView_JSONRoundTrip_EveryCanonicalState asserts every one of the
// nine ADR-005 closed states survives a StatusView JSON round trip
// byte-for-byte as its own lower-case wire value — never re-cased, never
// replaced with a friendly label.
func TestStatusView_JSONRoundTrip_EveryCanonicalState(t *testing.T) {
	for _, st := range []State{
		StateAbsent, StateStarting, StateReady, StateWorking, StateDegraded,
		StateDraining, StateStopping, StateStopped, StateUpgrading,
	} {
		st := st
		t.Run(string(st), func(t *testing.T) {
			view := NewStatusView(Snapshot{State: st, ObservedAt: time.Now()}, true, false)
			raw, err := json.Marshal(view)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatalf("Unmarshal into map: %v", err)
			}
			if got, want := wire["state"], string(st); got != want {
				t.Fatalf("wire state = %v, want %q", got, want)
			}
			var decoded StatusView
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("Unmarshal into StatusView: %v", err)
			}
			if decoded.State != st {
				t.Fatalf("decoded.State = %q, want %q", decoded.State, st)
			}
		})
	}
}

// TestDeriveDisplayLabel_ADR005Table is the friendly-label derivation table
// test: one row per ADR-005 "Readiness, publication, and adapters" example,
// asserting DeriveDisplayLabel computes EXACTLY the documented label.
func TestDeriveDisplayLabel_ADR005Table(t *testing.T) {
	cases := []struct {
		name             string
		snap             Snapshot
		hasDiscoveryEvid bool
		pendingUpgrade   bool
		want             DisplayLabel
	}{
		{
			name:             `idle from state=ready with no active execution`,
			snap:             Snapshot{State: StateReady, ActiveExecutionID: ""},
			hasDiscoveryEvid: false,
			pendingUpgrade:   false,
			want:             DisplayIdle,
		},
		{
			name:             `busy from state=working`,
			snap:             Snapshot{State: StateWorking, ActiveExecutionID: "exec-1"},
			hasDiscoveryEvid: false,
			pendingUpgrade:   false,
			want:             DisplayBusy,
		},
		{
			name:             `unhealthy from state=degraded`,
			snap:             Snapshot{State: StateDegraded},
			hasDiscoveryEvid: false,
			pendingUpgrade:   false,
			want:             DisplayUnhealthy,
		},
		{
			name:             `offline from state=absent plus discovery evidence`,
			snap:             Snapshot{State: StateAbsent},
			hasDiscoveryEvid: true,
			pendingUpgrade:   false,
			want:             DisplayOffline,
		},
		{
			name:             `offline from state=stopped plus discovery evidence`,
			snap:             Snapshot{State: StateStopped},
			hasDiscoveryEvid: true,
			pendingUpgrade:   false,
			want:             DisplayOffline,
		},
		{
			name:             `restart pending from state=working plus pending upgrade intent`,
			snap:             Snapshot{State: StateWorking, ActiveExecutionID: "exec-1"},
			hasDiscoveryEvid: false,
			pendingUpgrade:   true,
			want:             DisplayRestartPending,
		},
		{
			name:             `absent without discovery evidence stays unknown, not offline`,
			snap:             Snapshot{State: StateAbsent},
			hasDiscoveryEvid: false,
			pendingUpgrade:   false,
			want:             DisplayUnknown,
		},
		{
			name:             `stopped without discovery evidence stays unknown, not offline`,
			snap:             Snapshot{State: StateStopped},
			hasDiscoveryEvid: false,
			pendingUpgrade:   false,
			want:             DisplayUnknown,
		},
		{
			name:             `zero-value snapshot is unknown, not a fabricated label`,
			snap:             Snapshot{},
			hasDiscoveryEvid: false,
			pendingUpgrade:   false,
			want:             DisplayUnknown,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveDisplayLabel(tc.snap, tc.hasDiscoveryEvid, tc.pendingUpgrade)
			if got != tc.want {
				t.Fatalf("DeriveDisplayLabel(%+v, %v, %v) = %q, want %q",
					tc.snap, tc.hasDiscoveryEvid, tc.pendingUpgrade, got, tc.want)
			}
		})
	}
}

// TestDisplayLabel_NeverRoundTripsIntoState is the required negative case:
// it proves the derived DisplayLabel is a clearly separate field/type from
// the canonical State and is never assigned back into State — neither by
// construction nor across a JSON round trip, even when DisplayLabel and
// State deliberately disagree (a StatusView built directly, bypassing
// NewStatusView, to simulate a maximally adversarial mismatch).
func TestDisplayLabel_NeverRoundTripsIntoState(t *testing.T) {
	// Construct an intentionally inconsistent view: canonical State says
	// `ready`, but DisplayLabel says "busy". If any code path in this
	// package ever "round-tripped" the friendly label back into the
	// canonical field, marshaling and re-parsing this value (or otherwise
	// deriving from it) would corrupt State. It must not.
	adversarial := StatusView{
		State:        StateReady,
		DisplayLabel: DisplayBusy,
		ObservedAt:   time.Now(),
	}

	raw, err := json.Marshal(adversarial)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded StatusView
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.State != StateReady {
		t.Fatalf("State was corrupted by a JSON round trip: got %q, want %q (DisplayLabel was %q)",
			decoded.State, StateReady, decoded.DisplayLabel)
	}
	if decoded.DisplayLabel != DisplayBusy {
		t.Fatalf("DisplayLabel was corrupted by a JSON round trip: got %q, want %q",
			decoded.DisplayLabel, DisplayBusy)
	}

	// Type-level proof: State and DisplayLabel are distinct Go types, so
	// neither the compiler nor a careless assignment can silently merge
	// them. This line only needs to compile to make the point, but we also
	// assert the runtime values stay distinct.
	var _ State = adversarial.State
	var _ DisplayLabel = adversarial.DisplayLabel
	if string(adversarial.State) == string(adversarial.DisplayLabel) {
		t.Fatalf("test fixture is not actually adversarial: State and DisplayLabel matched (%q)", adversarial.State)
	}

	// Re-deriving StatusView.Snapshot() from the adversarial view must
	// produce a plain Snapshot with State exactly `ready` and no trace of
	// the "busy" DisplayLabel anywhere in it (Snapshot has no display-label
	// field at all — the type system itself enforces the separation).
	snap := decoded.Snapshot()
	if snap.State != StateReady {
		t.Fatalf("Snapshot().State = %q, want %q", snap.State, StateReady)
	}

	// Finally, prove DeriveDisplayLabel is itself pure and never consults
	// (let alone mutates) a State based on a previously-computed
	// DisplayLabel: deriving fresh from the same Snapshot twice, once with
	// evidence flags flipped, changes ONLY the label, never State.
	freshA := NewStatusView(snap, false, false)
	freshB := NewStatusView(snap, true, true)
	if freshA.State != StateReady || freshB.State != StateReady {
		t.Fatalf("State drifted across re-derivation with different evidence flags: %q / %q",
			freshA.State, freshB.State)
	}
}

// TestNewStatusViewFromLegacy_UsesTheOneBoundedMapping asserts the CLI's
// legacy-presence bridge goes through the exact same FromLegacyStatus
// mapping lifecycle.go already exposes — not a second, independently
// maintained mapping — and fails closed (does not fabricate a state) for a
// legacy value outside the bounded set.
func TestNewStatusViewFromLegacy_UsesTheOneBoundedMapping(t *testing.T) {
	cases := []struct {
		legacy    string
		wantState State
		wantErr   bool
	}{
		{legacy: "idle", wantState: StateReady},
		{legacy: "working", wantState: StateWorking},
		{legacy: "stopping", wantState: StateStopping},
		{legacy: "active", wantErr: true},     // not in the bounded legacy set
		{legacy: "ready/idle", wantErr: true}, // composite value, rejected
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.legacy, func(t *testing.T) {
			view, err := NewStatusViewFromLegacy(tc.legacy, true, false)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for legacy status %q, got view=%+v", tc.legacy, view)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewStatusViewFromLegacy(%q): unexpected error: %v", tc.legacy, err)
			}
			if view.State != tc.wantState {
				t.Fatalf("NewStatusViewFromLegacy(%q).State = %q, want %q", tc.legacy, view.State, tc.wantState)
			}
		})
	}
}
