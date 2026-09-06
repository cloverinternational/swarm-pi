package attachclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeRawStream is a scripted RawEventStream: it yields the queued events in
// order, then returns errClosed (simulating the connection dropping).
type fakeRawStream struct {
	mu     sync.Mutex
	events []RawEvent
	i      int
	closed bool
}

var errStreamEnded = errors.New("fake stream ended")

func (s *fakeRawStream) Next(ctx context.Context) (RawEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.i >= len(s.events) {
		return RawEvent{}, errStreamEnded
	}
	ev := s.events[s.i]
	s.i++
	return ev, nil
}

func (s *fakeRawStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// fakeReconnectTransport lets tests script exactly what OpenEvents returns
// on each successive call (by cursor), and counts how many times it was
// invoked (for bounded-retry / detach-does-not-call-daemon assertions).
type fakeReconnectTransport struct {
	mu        sync.Mutex
	openCalls int
	// script is called once per OpenEvents invocation, in order.
	script func(call int, from Cursor) (RawEventStream, error)
}

func (t *fakeReconnectTransport) RPC(ctx context.Context, base, method string, params any) (json.RawMessage, error) {
	return nil, fmt.Errorf("fakeReconnectTransport: RPC not used in this test")
}

func (t *fakeReconnectTransport) OpenEvents(ctx context.Context, base string, from Cursor) (RawEventStream, error) {
	t.mu.Lock()
	t.openCalls++
	call := t.openCalls
	t.mu.Unlock()
	return t.script(call, from)
}

func (t *fakeReconnectTransport) calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.openCalls
}

func agentEventPayload(kind string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"kind": kind})
	return b
}

// ── disconnect/reconnect/replay: resume-from-acknowledged-cursor ──────────

func TestReconnect_ResumesFromAcknowledgedCursor(t *testing.T) {
	tr := &fakeReconnectTransport{}
	tr.script = func(call int, from Cursor) (RawEventStream, error) {
		switch call {
		case 1:
			if from.Sequence != 0 {
				t.Errorf("first connect: got from.Sequence=%d, want 0 (fresh start)", from.Sequence)
			}
			return &fakeRawStream{events: []RawEvent{
				{Kind: "stream_start", Cursor: Cursor{StreamID: "s1", Sequence: 1}, Payload: agentEventPayload("stream_start")},
				{Kind: "stream_end", Cursor: Cursor{StreamID: "s1", Sequence: 2}, Payload: agentEventPayload("stream_end")},
			}}, nil
		case 2:
			// Reconnect after the first stream "dropped": must resume
			// after the last ACKED cursor (sequence 2), not from scratch.
			if from.Sequence != 2 || from.StreamID != "s1" {
				t.Errorf("reconnect: got from=%+v, want {s1 2} (resume from acknowledged cursor)", from)
			}
			return &fakeRawStream{events: []RawEvent{
				{Kind: "stream_start", Cursor: Cursor{StreamID: "s1", Sequence: 3}, Payload: agentEventPayload("stream_start")},
			}}, nil
		default:
			// Block forever (until ctx cancelled) so the loop doesn't spin.
			<-context.Background().Done()
			return nil, errStreamEnded
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var acked []Cursor
	var ackedMu sync.Mutex
	onAck := func(c Cursor) {
		ackedMu.Lock()
		acked = append(acked, c)
		ackedMu.Unlock()
	}

	stream := newEventStream(ctx, tr, "http://daemon", Cursor{}, BackoffPolicy{Initial: time.Millisecond, Max: 5 * time.Millisecond, Multiplier: 2, MaxRetries: 20}, "", onAck)

	var got []Event
	for i := 0; i < 3; i++ {
		select {
		case ev := <-stream.Events:
			got = append(got, ev)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for event %d; got so far: %+v", i, got)
		}
	}
	stream.Close()

	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[2].Cursor.Sequence != 3 {
		t.Fatalf("third event cursor = %+v, want sequence 3 (post-reconnect resume)", got[2].Cursor)
	}
}

// ── explicit gap test: never silently skip ─────────────────────────────────

func TestReconnect_ExplicitGapReported(t *testing.T) {
	gapPayloadJSON, _ := json.Marshal(gapPayload{StreamID: "s1", RequestedAfter: 5, OldestRetained: 50})

	tr := &fakeReconnectTransport{}
	tr.script = func(call int, from Cursor) (RawEventStream, error) {
		if call == 1 {
			return &fakeRawStream{events: []RawEvent{
				{Kind: "gap", Cursor: Cursor{StreamID: "s1"}, Payload: gapPayloadJSON},
			}}, nil
		}
		<-context.Background().Done()
		return nil, errStreamEnded
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newEventStream(ctx, tr, "http://daemon", Cursor{}, BackoffPolicy{Initial: time.Millisecond, Max: 5 * time.Millisecond, Multiplier: 2, MaxRetries: 20}, "", nil)

	select {
	case ev := <-stream.Events:
		if ev.Kind != "gap" || ev.Gap == nil {
			t.Fatalf("got %+v, want an explicit Kind=\"gap\" Event with Gap set (never a silent skip)", ev)
		}
		if ev.Gap.OldestRetained != 50 || ev.Gap.RequestedAfter != 5 || ev.Gap.StreamID != "s1" {
			t.Fatalf("got gap %+v, want {s1 5 50}", ev.Gap)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for gap event")
	}
	stream.Close()
}

// ── bounded retry: backoff has a ceiling, does not retry forever ──────────

func TestReconnect_BoundedRetryHasCeiling(t *testing.T) {
	tr := &fakeReconnectTransport{}
	wantErr := errors.New("connection refused")
	tr.script = func(call int, from Cursor) (RawEventStream, error) {
		return nil, wantErr
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backoff := BackoffPolicy{Initial: time.Millisecond, Max: 2 * time.Millisecond, Multiplier: 2, MaxRetries: 3}
	stream := newEventStream(ctx, tr, "http://daemon", Cursor{}, backoff, "", nil)

	select {
	case err := <-stream.Errs:
		if err == nil {
			t.Fatal("got nil terminal error, want a bounded-retry-exhausted error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bounded retry to give up")
	}

	// Ensure the loop actually stopped calling OpenEvents (out channel closed).
	select {
	case _, ok := <-stream.Events:
		if ok {
			t.Fatal("expected Events channel to be closed after retry exhaustion")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Events channel to close")
	}

	calls := tr.calls()
	if calls != backoff.MaxRetries+1 {
		t.Fatalf("got %d OpenEvents call(s), want exactly MaxRetries+1=%d (bounded, not infinite)", calls, backoff.MaxRetries+1)
	}
	stream.Close()
}

func TestBackoffPolicy_NextIsBoundedByMax(t *testing.T) {
	b := BackoffPolicy{Initial: time.Millisecond, Max: 10 * time.Millisecond, Multiplier: 2}
	for attempt := 1; attempt <= 20; attempt++ {
		if d := b.next(attempt); d > b.Max {
			t.Fatalf("next(%d) = %v, want <= Max %v", attempt, d, b.Max)
		}
	}
}

// ── termination on Detach / ctx cancel: no further Transport calls ────────

func TestReconnect_CloseTerminatesLoopPromptly(t *testing.T) {
	tr := &fakeReconnectTransport{}
	block := make(chan struct{})
	tr.script = func(call int, from Cursor) (RawEventStream, error) {
		<-block // first connect just hangs until the test unblocks it
		return nil, errStreamEnded
	}

	ctx := context.Background()
	stream := newEventStream(ctx, tr, "http://daemon", Cursor{}, DefaultBackoffPolicy(), "", nil)

	// Close (this is what Detach calls) must unblock reconnectLoop's
	// transport.OpenEvents call via ctx cancellation and close Events.
	stream.Close()
	close(block)

	select {
	case _, ok := <-stream.Events:
		if ok {
			t.Fatal("expected Events channel to be closed after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Events channel to close after Close")
	}
}

// ── malformed event does not panic and is not treated as a gap ────────────

func TestDecodeEvent_MalformedPayloadDoesNotPanicOrGap(t *testing.T) {
	raw := RawEvent{Kind: "agent", Payload: json.RawMessage(`{not valid json`)}
	ev, gap := decodeEvent(raw)
	if gap != nil {
		t.Fatalf("got gap %+v, want nil for a malformed (non-gap) payload", gap)
	}
	if ev.Kind != "agent" {
		t.Fatalf("got Kind=%q, want the RawEvent.Kind fallback %q", ev.Kind, "agent")
	}
}

func TestDecodeEvent_GapPayload(t *testing.T) {
	payload, _ := json.Marshal(gapPayload{StreamID: "s9", RequestedAfter: 1, OldestRetained: 100})
	ev, gap := decodeEvent(RawEvent{Kind: "gap", Payload: payload})
	if gap == nil {
		t.Fatal("expected non-nil gap")
	}
	if ev.Kind != "" || ev.Agent.Type != "" || len(ev.Payload) != 0 || ev.Gap != nil {
		t.Fatalf("expected zero-value Event alongside a gap, got %+v", ev)
	}
	if gap.StreamID != "s9" || gap.RequestedAfter != 1 || gap.OldestRetained != 100 {
		t.Fatalf("got %+v, want {s9 1 100}", gap)
	}
}
