// Package client — source_probe_test.go
//
// Internal probe tests for the unified event bus source-tagging machinery:
//   - WithSource / sourceFromContext round-trip
//   - InjectEvent carries EventSource to Subscribe handlers
//   - InjectUpdate propagates EventSource through the agent bridge
//   - Default source (SourceLocal) applied when context carries none
package client

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// newBusClient returns a bare *Client wired as an event bus only.
// sessStopped defaults to false so InjectEvent/InjectUpdate work immediately.
func newBusClient(t *testing.T) *Client {
	t.Helper()
	c := &Client{}
	c.SetSessionState("act", "", "")
	return c
}

// ─── WithSource / sourceFromContext ──────────────────────────────────────────

func TestWithSource_RoundTrip(t *testing.T) {
	want := EventSource{
		Kind:        SourcePeer,
		PeerHandle:  "peer-xyz",
		AgentID:     "agent-001",
		ConvID:      "conv-abc",
		WorkspaceID: "/workspace/issue-7",
	}
	ctx := WithSource(context.Background(), want)
	got := sourceFromContext(ctx)

	if got.Kind != want.Kind {
		t.Errorf("Kind: got %q, want %q", got.Kind, want.Kind)
	}
	if got.PeerHandle != want.PeerHandle {
		t.Errorf("PeerHandle: got %q, want %q", got.PeerHandle, want.PeerHandle)
	}
	if got.AgentID != want.AgentID {
		t.Errorf("AgentID: got %q, want %q", got.AgentID, want.AgentID)
	}
	if got.ConvID != want.ConvID {
		t.Errorf("ConvID: got %q, want %q", got.ConvID, want.ConvID)
	}
	if got.WorkspaceID != want.WorkspaceID {
		t.Errorf("WorkspaceID: got %q, want %q", got.WorkspaceID, want.WorkspaceID)
	}
}

func TestSourceFromContext_NilContext(t *testing.T) {
	//lint:ignore SA1012 deliberately passing nil to verify nil-context handling
	got := sourceFromContext(nil)
	if got.Kind != "" {
		t.Errorf("nil context: Kind = %q, want empty string", got.Kind)
	}
}

func TestSourceFromContext_NoValueReturnsZero(t *testing.T) {
	got := sourceFromContext(context.Background())
	if got != (EventSource{}) {
		t.Errorf("plain context: got %+v, want zero EventSource", got)
	}
}

// ─── InjectEvent ─────────────────────────────────────────────────────────────

// TestInjectEvent_SourcePeer verifies that a peer-sourced event reaches all
// Subscribe handlers with the exact EventSource set by the caller.
func TestInjectEvent_SourcePeer(t *testing.T) {
	c := newBusClient(t)

	done := make(chan Event, 1)
	c.Subscribe(func(ev Event) error {
		if ev.Kind == EventPeerJoined {
			done <- ev
		}
		return nil
	})

	c.InjectEvent(Event{
		Kind: EventPeerJoined,
		Source: EventSource{
			Kind:        SourcePeer,
			PeerHandle:  "laptop-a1b2",
			WorkspaceID: "/work/issue-42",
		},
		Payload: PeerJoinedPayload{Handle: "laptop-a1b2", EndpointURL: "http://localhost:9000"},
	})

	select {
	case ev := <-done:
		if ev.Source.Kind != SourcePeer {
			t.Errorf("Source.Kind = %q, want %q", ev.Source.Kind, SourcePeer)
		}
		if ev.Source.PeerHandle != "laptop-a1b2" {
			t.Errorf("Source.PeerHandle = %q, want laptop-a1b2", ev.Source.PeerHandle)
		}
		if ev.Source.WorkspaceID != "/work/issue-42" {
			t.Errorf("Source.WorkspaceID = %q, want /work/issue-42", ev.Source.WorkspaceID)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for EventPeerJoined")
	}
}

// TestInjectEvent_MultipleKinds verifies that different event kinds all carry
// their EventSource correctly through the same bus.
func TestInjectEvent_MultipleKinds(t *testing.T) {
	c := newBusClient(t)

	received := make(chan Event, 10)
	c.Subscribe(func(ev Event) error {
		received <- ev
		return nil
	})

	testCases := []Event{
		{Kind: EventPeerJoined, Source: EventSource{Kind: SourcePeer, PeerHandle: "p1"}, Payload: PeerJoinedPayload{Handle: "p1"}},
		{Kind: EventPeerLeft, Source: EventSource{Kind: SourcePeer, PeerHandle: "p2"}, Payload: PeerLeftPayload{Handle: "p2"}},
		{Kind: EventTaskDispatched, Source: EventSource{Kind: SourcePeer, PeerHandle: "w1"}, Payload: TaskDispatchedPayload{IssueID: "7", PeerHandle: "w1"}},
		{Kind: EventTaskCompleted, Source: EventSource{Kind: SourcePeer, PeerHandle: "w1"}, Payload: TaskCompletedPayload{IssueID: "7", PeerHandle: "w1"}},
		{Kind: EventError, Source: EventSource{Kind: SourceBackground}, Payload: ErrorPayload{Message: "bg agent lost conn"}},
	}

	for _, ev := range testCases {
		c.InjectEvent(ev)
	}

	got := make(map[EventKind]Event)
	deadline := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case ev := <-received:
			got[ev.Kind] = ev
			if len(got) == len(testCases) {
				break loop
			}
		case <-deadline:
			break loop
		}
	}

	if len(got) != len(testCases) {
		t.Fatalf("received %d events, want %d", len(got), len(testCases))
	}

	// Spot-check a few.
	if got[EventPeerJoined].Source.PeerHandle != "p1" {
		t.Errorf("PeerJoined handle: got %q, want p1", got[EventPeerJoined].Source.PeerHandle)
	}
	if got[EventError].Source.Kind != SourceBackground {
		t.Errorf("Error source kind: got %q, want %q", got[EventError].Source.Kind, SourceBackground)
	}
	if got[EventTaskDispatched].Source.PeerHandle != "w1" {
		t.Errorf("TaskDispatched handle: got %q, want w1", got[EventTaskDispatched].Source.PeerHandle)
	}
}

// TestInjectEvent_TimestampAutoSet verifies that InjectEvent auto-sets At when
// the caller leaves it zero.
func TestInjectEvent_TimestampAutoSet(t *testing.T) {
	c := newBusClient(t)
	before := time.Now()

	done := make(chan Event, 1)
	c.Subscribe(func(ev Event) error {
		if ev.Kind == EventPeerLeft {
			done <- ev
		}
		return nil
	})

	c.InjectEvent(Event{Kind: EventPeerLeft, Payload: PeerLeftPayload{Handle: "x"}})

	select {
	case ev := <-done:
		if ev.At.IsZero() {
			t.Error("At is zero — InjectEvent did not auto-stamp the timestamp")
		}
		if ev.At.Before(before) {
			t.Errorf("At %v is before test start %v", ev.At, before)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

// TestInjectEvent_MultipleSubscribers verifies fan-out to N subscribers.
func TestInjectEvent_MultipleSubscribers(t *testing.T) {
	const N = 5
	c := newBusClient(t)

	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		c.Subscribe(func(ev Event) error {
			if ev.Kind == EventPeerStatus {
				wg.Done()
			}
			return nil
		})
	}

	c.InjectEvent(Event{
		Kind:    EventPeerStatus,
		Source:  EventSource{Kind: SourcePeer, PeerHandle: "broadcast-peer"},
		Payload: PeerStatusPayload{Handle: "broadcast-peer", Status: "working"},
	})

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("fan-out to %d subscribers did not complete in time", N)
	}
}

// ─── InjectUpdate ─────────────────────────────────────────────────────────────

// TestInjectUpdate_SourcePropagation verifies that InjectUpdate routes through
// the agent bridge and delivers an EventAgent event with the correct source.
func TestInjectUpdate_SourcePropagation(t *testing.T) {
	c := newBusClient(t)
	c.startAgentBridge() // wire SubscribeUpdates → dispatchEvent → Subscribe

	done := make(chan Event, 1)
	c.Subscribe(func(ev Event) error {
		if ev.Kind == EventAgent {
			done <- ev
		}
		return nil
	})

	src := EventSource{
		Kind:       SourcePeer,
		PeerHandle: "remote-worker",
		ConvID:     "conv-999",
	}
	update := agent.ContentUpdate{Content: "hello from remote peer"}

	if err := c.InjectUpdate(context.Background(), src, update); err != nil {
		t.Fatalf("InjectUpdate: %v", err)
	}

	select {
	case ev := <-done:
		if ev.Kind != EventAgent {
			t.Errorf("Kind = %q, want %q", ev.Kind, EventAgent)
		}
		if ev.Source.Kind != SourcePeer {
			t.Errorf("Source.Kind = %q, want %q", ev.Source.Kind, SourcePeer)
		}
		if ev.Source.PeerHandle != "remote-worker" {
			t.Errorf("Source.PeerHandle = %q, want remote-worker", ev.Source.PeerHandle)
		}
		if ev.Source.ConvID != "conv-999" {
			t.Errorf("Source.ConvID = %q, want conv-999", ev.Source.ConvID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for EventAgent from InjectUpdate")
	}
}

// TestInjectUpdate_DefaultsToLocal verifies that when InjectUpdate is called
// with an empty EventSource, the bridge fills in SourceLocal.
func TestInjectUpdate_DefaultsToLocal(t *testing.T) {
	c := newBusClient(t)
	c.startAgentBridge()

	done := make(chan Event, 1)
	c.Subscribe(func(ev Event) error {
		if ev.Kind == EventAgent {
			done <- ev
		}
		return nil
	})

	// Pass an empty source — bridge should default to SourceLocal.
	if err := c.InjectUpdate(context.Background(), EventSource{}, agent.ContentUpdate{Content: "local"}); err != nil {
		t.Fatalf("InjectUpdate: %v", err)
	}

	select {
	case ev := <-done:
		if ev.Source.Kind != SourceLocal {
			t.Errorf("Source.Kind = %q, want %q (default)", ev.Source.Kind, SourceLocal)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for EventAgent")
	}
}

// TestInjectUpdate_BackgroundSource verifies SourceBackground is preserved.
func TestInjectUpdate_BackgroundSource(t *testing.T) {
	c := newBusClient(t)
	c.startAgentBridge()

	done := make(chan Event, 1)
	c.Subscribe(func(ev Event) error {
		if ev.Kind == EventAgent {
			done <- ev
		}
		return nil
	})

	src := EventSource{Kind: SourceBackground, AgentID: "bg-007"}
	if err := c.InjectUpdate(context.Background(), src, agent.ContentUpdate{Content: "bg update"}); err != nil {
		t.Fatalf("InjectUpdate: %v", err)
	}

	select {
	case ev := <-done:
		if ev.Source.Kind != SourceBackground {
			t.Errorf("Source.Kind = %q, want %q", ev.Source.Kind, SourceBackground)
		}
		if ev.Source.AgentID != "bg-007" {
			t.Errorf("Source.AgentID = %q, want bg-007", ev.Source.AgentID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for EventAgent")
	}
}
