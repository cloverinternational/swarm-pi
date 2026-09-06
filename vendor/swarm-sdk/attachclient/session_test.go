package attachclient

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/identity"
)

// TestNewSessionID_RoundTripsThroughIdentityPackage proves newSessionID
// (attachclient/client.go) now mints via identity.NewClientSessionID
// instead of the old ad hoc 8-random-byte "sess-<hex>" scheme: the
// returned string must parse successfully via
// identity.ParseClientSessionID and round-trip back to the identical
// string value (per CONTRACT.md section 3: "identity.ClientSessionID ...
// is the ONLY canonical session key").
func TestNewSessionID_RoundTripsThroughIdentityPackage(t *testing.T) {
	raw := newSessionID()
	if raw == "" {
		t.Fatal("newSessionID returned an empty string")
	}

	parsed, err := identity.ParseClientSessionID(raw)
	if err != nil {
		t.Fatalf("identity.ParseClientSessionID(%q) failed: %v — newSessionID must mint a real, canonical ClientSessionID, not an ad hoc string", raw, err)
	}
	if parsed.String() != raw {
		t.Fatalf("round trip mismatch: parsed.String() = %q, want %q", parsed.String(), raw)
	}
	if parsed.IsZero() {
		t.Fatal("parsed ClientSessionID reports IsZero() == true, want a valid non-zero identity")
	}
}

// TestNewSessionID_ProducesUniqueValues sanity-checks that repeated calls
// mint distinct, collision-free identities (identity.NewClientSessionID's
// own contract — see internal/identity/identity.go's 128-bit random
// token), exercised here at attachclient's call boundary.
func TestNewSessionID_ProducesUniqueValues(t *testing.T) {
	a := newSessionID()
	b := newSessionID()
	if a == b {
		t.Fatalf("two consecutive newSessionID() calls produced the same value %q", a)
	}
}

// TestConnect_SessionIDIsCanonicalClientSessionID exercises newSessionID
// through the real Connect path (not just calling the unexported function
// directly), proving the Session.ID a caller actually receives from
// Connect is itself a parseable identity.ClientSessionID.
func TestConnect_SessionIDIsCanonicalClientSessionID(t *testing.T) {
	tr := &fakeRPCTransport{}
	c := New(Deps{Transport: tr})

	sess, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := identity.ParseClientSessionID(sess.ID); err != nil {
		t.Fatalf("Connect returned Session.ID %q which does not parse as identity.ClientSessionID: %v", sess.ID, err)
	}
}

// TestConnect_TwoSessionsGetDistinctClientSessionIDs is the minimal
// two-session distinctness check at the attachclient layer: two separate
// Impl instances (representing two separate attachclient sessions, as two
// attached CLIs/TUIs would each construct) must never mint the same
// canonical ClientSessionID.
func TestConnect_TwoSessionsGetDistinctClientSessionIDs(t *testing.T) {
	c1 := New(Deps{Transport: &fakeRPCTransport{}})
	c2 := New(Deps{Transport: &fakeRPCTransport{}})

	sess1, err := c1.Connect(context.Background(), Target{BaseURL: "http://daemon"})
	if err != nil {
		t.Fatalf("connect c1: %v", err)
	}
	sess2, err := c2.Connect(context.Background(), Target{BaseURL: "http://daemon"})
	if err != nil {
		t.Fatalf("connect c2: %v", err)
	}
	if sess1.ID == sess2.ID {
		t.Fatalf("two independent Connect calls minted the same Session.ID %q", sess1.ID)
	}
}

// TestSubmit_MintsCanonicalAttemptAndExecutionIdentity proves Submit (the
// real "submit work" entry point) wires identity.NewAttemptID()/
// identity.NewExecutionID() at call time and surfaces both on the
// returned Execution rather than silently dropping them (CONTRACT.md,
// P07.B task 4).
func TestSubmit_MintsCanonicalAttemptAndExecutionIdentity(t *testing.T) {
	tr := &fakeRPCTransport{}
	c := New(Deps{Transport: tr})
	if _, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	exec, err := c.Submit(context.Background(), Work{Method: "client.sendMessage", Params: map[string]any{"message": "hi"}})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	if exec.AttemptID.IsZero() {
		t.Fatal("Execution.AttemptID is zero — Submit must mint a real identity.AttemptID via identity.NewAttemptID()")
	}
	if _, err := identity.ParseAttemptID(exec.AttemptID.String()); err != nil {
		t.Fatalf("Execution.AttemptID %q does not parse as identity.AttemptID: %v", exec.AttemptID, err)
	}

	if exec.CanonicalExecutionID.IsZero() {
		t.Fatal("Execution.CanonicalExecutionID is zero — Submit must mint a real identity.ExecutionID via identity.NewExecutionID()")
	}
	if _, err := identity.ParseExecutionID(exec.CanonicalExecutionID.String()); err != nil {
		t.Fatalf("Execution.CanonicalExecutionID %q does not parse as identity.ExecutionID: %v", exec.CanonicalExecutionID, err)
	}

	// The RPC actually sent to the daemon must carry both canonical
	// identities on the wire so a daemon-side consumer can thread them
	// further without a second round trip — "do not silently drop it".
	last, ok := tr.lastCall()
	if !ok {
		t.Fatal("expected an RPC call to have been recorded")
	}
	params, ok := last.params.(map[string]any)
	if !ok {
		t.Fatalf("expected map params, got %T", last.params)
	}
	if params["clientAttemptID"] != exec.AttemptID.String() {
		t.Fatalf("wire clientAttemptID = %v, want %q", params["clientAttemptID"], exec.AttemptID.String())
	}
	if params["canonicalExecutionID"] != exec.CanonicalExecutionID.String() {
		t.Fatalf("wire canonicalExecutionID = %v, want %q", params["canonicalExecutionID"], exec.CanonicalExecutionID.String())
	}
}

// TestSubmit_TwoCallsMintDistinctAttemptAndExecutionIdentity proves
// successive Submit calls each mint their OWN fresh AttemptID/ExecutionID
// (never reused/cached across calls).
func TestSubmit_TwoCallsMintDistinctAttemptAndExecutionIdentity(t *testing.T) {
	tr := &fakeRPCTransport{}
	c := New(Deps{Transport: tr})
	if _, err := c.Connect(context.Background(), Target{BaseURL: "http://daemon"}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	exec1, err := c.Submit(context.Background(), Work{Method: "client.sendMessage", Params: map[string]any{"message": "one"}})
	if err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	exec2, err := c.Submit(context.Background(), Work{Method: "client.sendMessage", Params: map[string]any{"message": "two"}})
	if err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	if exec1.AttemptID == exec2.AttemptID {
		t.Fatalf("two Submit calls minted the same AttemptID %q", exec1.AttemptID)
	}
	if exec1.CanonicalExecutionID == exec2.CanonicalExecutionID {
		t.Fatalf("two Submit calls minted the same CanonicalExecutionID %q", exec1.CanonicalExecutionID)
	}
}
