package chat

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/attach"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/client"
)

// TestNewRealAttachScreenFactoryWiresRealFunctions proves that the factory
// returned by newRealAttachScreenFactory produces an AttachScreen with real,
// non-nil targetsFn/attachPeerFn/interjector — the concrete opposite of the
// empty NewAttachScreen(nil, nil, nil) fallback in app_keyboard.go that ran
// whenever AppOptions.AttachScreenFactory was left unset.
func TestNewRealAttachScreenFactoryWiresRealFunctions(t *testing.T) {
	factory := newRealAttachScreenFactory(nil, a2a.DefaultSwarmName)
	if factory == nil {
		t.Fatal("newRealAttachScreenFactory returned a nil factory")
	}

	screen := factory()
	if screen == nil {
		t.Fatal("factory() returned a nil AttachScreen")
	}
	if screen.targetsFn == nil {
		t.Fatal("expected a real targetsFn, got nil (screen behaves like the empty fallback)")
	}
	if screen.attachPeerFn == nil {
		t.Fatal("expected a real attachPeerFn, got nil (screen behaves like the empty fallback)")
	}
	// With a nil sdk, targetsFn must still be safe to call (attach.ListTargets
	// tolerates a nil sdkSource) and must not itself be the nil function that
	// NewAttachScreen(nil, nil, nil) would have supplied.
	if _, err := screen.targetsFn(); err != nil {
		// Peer discovery failing in the test sandbox is fine; a nil pointer
		// panic (the historical failure mode of an unwired factory) is not.
		t.Logf("targetsFn returned error (acceptable in sandboxed test env): %v", err)
	}
}

// TestBackgroundAgentInterjectorNilManager confirms the adapter fails
// loudly instead of panicking when no background agent manager is present
// (e.g. before SDK init completes).
func TestBackgroundAgentInterjectorNilManager(t *testing.T) {
	adapter := backgroundAgentInterjector{mgr: nil}
	err := adapter.Interject("some-agent", "hello")
	if err == nil {
		t.Fatal("expected an error for a nil background agent manager, got nil")
	}
	if !strings.Contains(err.Error(), "no background agent manager") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestBackgroundAgentInterjectorUnknownAgent confirms the adapter surfaces
// the manager's "not found" error for an unknown agent ID rather than
// silently dropping the interjection.
func TestBackgroundAgentInterjectorUnknownAgent(t *testing.T) {
	mgr := NewSDKBackgroundAgentManager()
	adapter := backgroundAgentInterjector{mgr: mgr}
	err := adapter.Interject("does-not-exist", "hello")
	if err == nil {
		t.Fatal("expected an error for an unknown agent ID, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("expected error to reference the missing agent id, got: %v", err)
	}
}

// TestAttachScreenPassesControlSocketNotID guards the fix for the WS3-era
// bug where the peer-attach call site passed Target.ID (the peer's display
// handle) instead of Target.ControlSocket (the actual dial target) to
// attachPeerFn. This test uses distinct ID/ControlSocket values, so it would
// fail against the old `s.attachPeerFn(t.ID)` call site.
func TestAttachScreenPassesControlSocketNotID(t *testing.T) {
	const wantSocket = "/tmp/peer.ctrl"
	targets := []attach.Target{
		{Kind: attach.TargetKindPeer, ID: "peer-handle", Name: "peer", ControlSocket: wantSocket, Steerable: true},
	}
	fake := &fakeAttachSession{frames: make(chan *client.Frame, 1), errors: make(chan error, 1)}

	var gotArg string
	s := NewAttachScreen(
		func() ([]attach.Target, error) { return targets, nil },
		func(arg string) (liveSession, error) {
			gotArg = arg
			return fake, nil
		},
		nil,
	)
	s.Update(key("enter"))
	if gotArg != wantSocket {
		t.Fatalf("attachPeerFn called with %q, want ControlSocket %q (Target.ID was %q)", gotArg, wantSocket, targets[0].ID)
	}
}
