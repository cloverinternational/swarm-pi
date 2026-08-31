// Package serve — eventlog_test.go
//
// Covers EventLog's ring-buffer retention/eviction behavior, sequence
// monotonicity (including for an event Kind this package's
// payloadForEvent discriminator switch does not recognize — ADR-004
// Compatibility's "an unknown event kind under a compatible major can be
// ignored or preserved while still advancing the sequence cursor"),
// Attach/Detach/Events/Latest/Retention correctness, and SubscribeFrom's
// atomic replay+live-tail registration (including its gap detection)
// under concurrent readers. Run with -race per CONTRACT.md's verify step.
package serve

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// waitForSeq polls l.Latest() until it reaches at least want or the test
// times out. Event recording happens asynchronously off c.Subscribe's
// dispatch, so tests that inject via c.SetMode/c.InjectEvent must
// synchronize on the resulting sequence number rather than assuming
// same-goroutine ordering.
func waitForSeq(t *testing.T, l *EventLog, want uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if l.Latest() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("EventLog never reached sequence %d (latest=%d)", want, l.Latest())
}

// listenerCount returns the number of currently registered SubscribeFrom
// live-tail listeners. Test-only introspection (accesses the unexported
// listeners map directly, safe because this file is package serve, not
// serve_test) used to deterministically wait for a connection's listener
// to be registered before a test injects an event that connection must
// observe live — see waitForListenerCount's doc comment for why this
// matters and sse_replay_test.go for its primary use.
func (l *EventLog) listenerCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.listeners)
}

// waitForListenerCount polls until l has at least want registered
// SubscribeFrom listeners or the test times out.
//
// Why this is needed: SSEHandler (sse.go) writes its HTTP response
// headers and an initial ": stream-open" comment BEFORE calling
// EventLog.SubscribeFrom to register its live-tail listener (so proxies
// that buffer until the first body bytes still see the connection open
// promptly). That means an HTTP client's Get/Do call can return — and a
// test can proceed to inject an event the connection is meant to observe
// live — before the server-side listener actually exists yet. A
// connection with a resume cursor self-synchronizes (its replay frames,
// which are only ever written after SubscribeFrom registers the
// listener, prove registration happened once the test has read them), but
// a plain tail-only (no cursor) connection has no such signal on the
// wire, so tests that immediately inject a "must be seen live" event
// after opening one must wait on this instead.
func waitForListenerCount(t *testing.T, l *EventLog, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if l.listenerCount() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("EventLog never reached %d registered listener(s) (have %d)", want, l.listenerCount())
}

// injectN synchronously records n events on l (attached to c) and blocks
// until EventLog has recorded all of them, so callers get a deterministic
// buffer state before asserting on it.
func injectN(t *testing.T, c *client.Client, l *EventLog, n int) {
	t.Helper()
	start := l.Latest()
	for i := 0; i < n; i++ {
		c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "dark"}})
	}
	waitForSeq(t, l, start+uint64(n))
}

// ─── Retention / eviction ──────────────────────────────────────────────────

func TestEventLog_RingBufferRetentionAndEviction(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(3)
	l.Attach(c)
	defer l.Detach()

	injectN(t, c, l, 5) // capacity 3, so seq 0 and 1 must be evicted.

	events, latest := l.Events(0)
	if latest != 5 {
		t.Fatalf("latest = %d, want 5", latest)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 retained events, got %d", len(events))
	}
	wantSeqs := []uint64{2, 3, 4}
	for i, ev := range events {
		if ev.Seq != wantSeqs[i] {
			t.Fatalf("events[%d].Seq = %d, want %d", i, ev.Seq, wantSeqs[i])
		}
	}

	oldest, latestRet, ok := l.Retention()
	if !ok {
		t.Fatal("Retention() ok = false after recording events")
	}
	if oldest != 2 {
		t.Fatalf("Retention oldest = %d, want 2 (0 and 1 must have been evicted)", oldest)
	}
	if latestRet != 5 {
		t.Fatalf("Retention latest = %d, want 5", latestRet)
	}
}

func TestEventLog_RetentionEmptyBeforeAnyEvent(t *testing.T) {
	l := NewEventLog(4)
	if _, _, ok := l.Retention(); ok {
		t.Fatal("Retention() ok = true on a fresh EventLog with no recorded events")
	}
	if events, latest := l.Events(0); events != nil || latest != 0 {
		t.Fatalf("Events(0) on empty log = (%v, %d), want (nil, 0)", events, latest)
	}
	if l.Latest() != 0 {
		t.Fatalf("Latest() = %d, want 0", l.Latest())
	}
}

func TestEventLog_DefaultCapacityAppliedForZeroOrNegative(t *testing.T) {
	for _, capacity := range []int{0, -1, -100} {
		l := NewEventLog(capacity)
		if cap(l.buf) != 1024 {
			t.Fatalf("NewEventLog(%d): buf cap = %d, want default 1024", capacity, cap(l.buf))
		}
	}
}

// ─── Sequence monotonicity ──────────────────────────────────────────────────

func TestEventLog_SequenceMonotonic(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(1024)
	start := l.Attach(c)
	defer l.Detach()
	if start != 0 {
		t.Fatalf("Attach on a fresh EventLog returned %d, want 0", start)
	}

	// Subscribe for live tail BEFORE injecting so this test observes every
	// sequence number including 0. Events(after) is deliberately exclusive
	// of `after` itself (see eventsAfterLocked's "> after" search), so seq
	// 0 specifically can never be recovered via Events(0) — that half of
	// monotonicity (starting at 0) is checked via the live channel instead;
	// the second half (Events' cursor semantics) is checked afterward.
	_, ch, cancel, _, _, hasGap := l.SubscribeFrom(nil)
	defer cancel()
	if hasGap {
		t.Fatal("unexpected gap on a fresh subscribe")
	}

	const n = 50
	injectN(t, c, l, n)

	seen := make([]uint64, 0, n)
	for len(seen) < n {
		select {
		case entry := <-ch:
			seen = append(seen, entry.Seq)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out collecting live events, got %d/%d", len(seen), n)
		}
	}
	for i, seq := range seen {
		if seq != uint64(i) {
			t.Fatalf("live seq[%d] = %d, want %d (sequence must be contiguous, no gaps, starting at 0)", i, seq, i)
		}
	}

	// Events(after) semantics: strictly greater than `after`, so Events(0)
	// excludes seq 0 and returns seq 1..49 (n-1 events) — the other half of
	// the monotonicity contract, exercised via the ring-buffer read path
	// rather than the live-tail channel above.
	events, latest := l.Events(0)
	if latest != n {
		t.Fatalf("latest = %d, want %d", latest, n)
	}
	if len(events) != n-1 {
		t.Fatalf("expected %d events strictly after seq 0, got %d", n-1, len(events))
	}
	for i, ev := range events {
		want := uint64(i + 1)
		if ev.Seq != want {
			t.Fatalf("events[%d].Seq = %d, want %d (sequence must be contiguous, no gaps)", i, ev.Seq, want)
		}
	}
}

// TestEventLog_UnknownKindStillAdvancesSequence proves ADR-004
// Compatibility's rule verbatim: "An unknown event kind under a
// compatible major can be ignored or preserved while still advancing the
// sequence cursor." A Kind the package's payloadForEvent discriminator
// switch has never heard of must still get a monotonic sequence number,
// must still occupy a ring-buffer slot (participating in retention/
// eviction exactly like any recognized kind), and must not corrupt
// encoding of the events recorded before or after it.
func TestEventLog_UnknownKindStillAdvancesSequence(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(1024)
	l.Attach(c)
	defer l.Detach()

	// Subscribed live (rather than reading back via Events(0), which is
	// exclusive of seq 0 — see TestEventLog_SequenceMonotonic) so this test
	// can assert on seq 0 (the first ThemeChanged) through seq 2 (the
	// second) without an off-by-one.
	_, ch, cancel, _, _, hasGap := l.SubscribeFrom(nil)
	defer cancel()
	if hasGap {
		t.Fatal("unexpected gap")
	}

	c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "light"}})
	c.InjectEvent(client.Event{Kind: client.EventKind("a_kind_this_package_has_never_heard_of"), Payload: map[string]any{"anything": "goes"}})
	c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "dark"}})

	events := make([]loggedEvent, 0, 3)
	for len(events) < 3 {
		select {
		case entry := <-ch:
			events = append(events, entry)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out, got %d/3 live events", len(events))
		}
	}
	if latest := l.Latest(); latest != 3 {
		t.Fatalf("latest = %d, want 3", latest)
	}
	for i, ev := range events {
		if ev.Seq != uint64(i) {
			t.Fatalf("events[%d].Seq = %d, want %d — unknown kind must not skip or duplicate a sequence number", i, ev.Seq, i)
		}
	}
	if events[1].Kind != "a_kind_this_package_has_never_heard_of" {
		t.Fatalf("events[1].Kind = %q, want the unknown kind preserved verbatim", events[1].Kind)
	}
	// The unknown-kind event's payload must still round-trip as valid JSON
	// (preserved, not dropped or replaced with a fabricated shape) — see
	// stream.go's payloadForEvent default case.
	var mid struct {
		Kind    string         `json:"kind"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(events[1].Data, &mid); err != nil {
		t.Fatalf("unknown-kind event did not marshal to valid JSON: %v (data=%s)", err, events[1].Data)
	}
	if mid.Payload["anything"] != "goes" {
		t.Fatalf("unknown-kind event's payload was not preserved: %+v", mid.Payload)
	}
	// The events on either side of the unknown-kind one must still be
	// intact and correctly typed.
	var first, third struct {
		Kind    string `json:"kind"`
		Payload struct {
			Theme string `json:"theme"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(events[0].Data, &first); err != nil || first.Payload.Theme != "light" {
		t.Fatalf("event before the unknown kind was corrupted: err=%v data=%s", err, events[0].Data)
	}
	if err := json.Unmarshal(events[2].Data, &third); err != nil || third.Payload.Theme != "dark" {
		t.Fatalf("event after the unknown kind was corrupted: err=%v data=%s", err, events[2].Data)
	}
}

// TestEventLog_UnknownKindParticipatesInEviction proves the unknown-kind
// event is not special-cased out of ring-buffer retention: once enough
// events follow it, it is evicted exactly like any other entry would be.
func TestEventLog_UnknownKindParticipatesInEviction(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(2)
	l.Attach(c)
	defer l.Detach()

	c.InjectEvent(client.Event{Kind: client.EventKind("mystery_kind")})
	c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "a"}})
	c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "b"}})
	waitForSeq(t, l, 3)

	oldest, latest, ok := l.Retention()
	if !ok {
		t.Fatal("Retention ok = false")
	}
	if oldest != 1 {
		t.Fatalf("oldest = %d, want 1 (seq 0, the unknown-kind event, must have been evicted)", oldest)
	}
	if latest != 3 {
		t.Fatalf("latest = %d, want 3", latest)
	}
}

// ─── Attach / Detach ────────────────────────────────────────────────────────

func TestEventLog_AttachDetach(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(1024)
	l.Attach(c)

	injectN(t, c, l, 2)
	if l.Latest() != 2 {
		t.Fatalf("Latest() = %d, want 2 before Detach", l.Latest())
	}

	l.Detach()
	// Give the (now unsubscribed) dispatch bus a moment to prove it does NOT
	// deliver further events to l.
	c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "x"}})
	time.Sleep(20 * time.Millisecond)
	if l.Latest() != 2 {
		t.Fatalf("Latest() = %d after Detach, want unchanged 2 (Detach must stop recording)", l.Latest())
	}

	// Detach is idempotent.
	l.Detach()
}

func TestEventLog_StreamIDStableAcrossCalls(t *testing.T) {
	l := NewEventLog(4)
	id1 := l.StreamID()
	id2 := l.StreamID()
	if id1 == "" {
		t.Fatal("StreamID() returned empty string")
	}
	if id1 != id2 {
		t.Fatalf("StreamID() changed across calls: %q vs %q", id1, id2)
	}

	other := NewEventLog(4)
	if other.StreamID() == l.StreamID() {
		t.Fatal("two independent EventLogs got the same StreamID")
	}
}

// ─── Concurrent readers (-race) ─────────────────────────────────────────────

// TestEventLog_ConcurrentReaders drives many concurrent Events/Latest/
// Retention readers against a single producer goroutine appending events,
// and must be run with -race (see CONTRACT.md's verify step) to prove
// EventLog's locking is sound: no torn reads of the ring buffer, no data
// race on next/streamID/listeners.
func TestEventLog_ConcurrentReaders(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(64)
	l.Attach(c)
	defer l.Detach()

	const producedEvents = 200
	const readerGoroutines = 16

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < readerGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = l.Events(0)
				_ = l.Latest()
				_, _, _ = l.Retention()
				replay, ch, cancel, _, _, hasGap := l.SubscribeFrom(nil)
				if !hasGap {
					select {
					case <-ch:
					default:
					}
				}
				_ = replay
				cancel()
			}
		}()
	}

	for i := 0; i < producedEvents; i++ {
		c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "concurrent"}})
	}
	waitForSeq(t, l, producedEvents)
	close(stop)
	wg.Wait()

	if l.Latest() != producedEvents {
		t.Fatalf("Latest() = %d, want %d", l.Latest(), producedEvents)
	}
}

// ─── SubscribeFrom: replay + live-tail + gap (unit level; HTTP-level
// coverage lives in sse_replay_test.go) ─────────────────────────────────────

func TestEventLog_SubscribeFrom_NilCursorIsTailOnly(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(1024)
	l.Attach(c)
	defer l.Detach()

	injectN(t, c, l, 3) // seq 0,1,2 already recorded before subscribing.

	replay, ch, cancel, _, _, hasGap := l.SubscribeFrom(nil)
	defer cancel()
	if hasGap {
		t.Fatal("nil cursor must never report a gap")
	}
	if len(replay) != 0 {
		t.Fatalf("nil cursor (tail-only) must replay nothing, got %d events", len(replay))
	}

	c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "live"}})
	select {
	case entry := <-ch:
		if entry.Seq != 3 {
			t.Fatalf("live entry Seq = %d, want 3", entry.Seq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live-tail delivery after nil-cursor subscribe")
	}
}

func TestEventLog_SubscribeFrom_ReplayThenLiveNoDuplication(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(1024)
	l.Attach(c)
	defer l.Detach()

	injectN(t, c, l, 5) // seq 0..4

	after := uint64(2)
	replay, ch, cancel, _, latest, hasGap := l.SubscribeFrom(&after)
	defer cancel()
	if hasGap {
		t.Fatal("unexpected gap")
	}
	if latest != 5 {
		t.Fatalf("latest = %d, want 5", latest)
	}
	if len(replay) != 2 {
		t.Fatalf("expected 2 replayed events (seq 3,4), got %d", len(replay))
	}
	if replay[0].Seq != 3 || replay[1].Seq != 4 {
		t.Fatalf("replay seqs = [%d,%d], want [3,4]", replay[0].Seq, replay[1].Seq)
	}

	c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "live"}})
	select {
	case entry := <-ch:
		if entry.Seq != 5 {
			t.Fatalf("live entry Seq = %d, want 5 (no duplication/skip across the replay→live handoff)", entry.Seq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live-tail delivery after resume")
	}
}

func TestEventLog_SubscribeFrom_GapWhenCursorPredatesRetention(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(3) // small capacity forces eviction quickly.
	l.Attach(c)
	defer l.Detach()

	injectN(t, c, l, 10) // seq 0..9, only 7,8,9 retained (cap 3).

	after := uint64(0) // long evicted.
	replay, ch, cancel, oldest, latest, hasGap := l.SubscribeFrom(&after)
	defer cancel()
	if !hasGap {
		t.Fatal("expected hasGap = true for a cursor far older than retained history")
	}
	if replay != nil {
		t.Fatalf("expected nil replay on gap, got %d events", len(replay))
	}
	if ch != nil {
		t.Fatal("expected nil channel on gap (no listener should be registered)")
	}
	if oldest != 7 {
		t.Fatalf("oldest = %d, want 7", oldest)
	}
	if latest != 10 {
		t.Fatalf("latest = %d, want 10", latest)
	}
}

func TestEventLog_SubscribeFrom_NoGapAtExactRetentionBoundary(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(3)
	l.Attach(c)
	defer l.Detach()

	injectN(t, c, l, 10) // retained: seq 7,8,9; oldest=7.

	after := uint64(6) // == oldest-1: fully contiguous with what's retained.
	replay, ch, cancel, _, _, hasGap := l.SubscribeFrom(&after)
	defer cancel()
	if hasGap {
		t.Fatal("cursor == oldest-1 must NOT be reported as a gap (nothing was actually missed)")
	}
	if len(replay) != 3 {
		t.Fatalf("expected 3 replayed events (seq 7,8,9), got %d", len(replay))
	}
	_ = ch
}

func TestEventLog_SubscribeFrom_ConcurrentSubscribersIndependent(t *testing.T) {
	c := &client.Client{}
	l := NewEventLog(1024)
	l.Attach(c)
	defer l.Detach()

	injectN(t, c, l, 3) // seq 0,1,2

	after0 := uint64(0)
	after1 := uint64(1)
	replayA, chA, cancelA, _, _, gapA := l.SubscribeFrom(&after0)
	defer cancelA()
	replayB, chB, cancelB, _, _, gapB := l.SubscribeFrom(&after1)
	defer cancelB()
	if gapA || gapB {
		t.Fatal("unexpected gap")
	}
	if len(replayA) != 2 || replayA[0].Seq != 1 || replayA[1].Seq != 2 {
		t.Fatalf("replayA = %+v, want seq [1,2]", replayA)
	}
	if len(replayB) != 1 || replayB[0].Seq != 2 {
		t.Fatalf("replayB = %+v, want seq [2]", replayB)
	}

	c.InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "live"}})

	for name, ch := range map[string]<-chan loggedEvent{"A": chA, "B": chB} {
		select {
		case entry := <-ch:
			if entry.Seq != 3 {
				t.Fatalf("subscriber %s got seq %d, want 3", name, entry.Seq)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %s never received the live event (cross-talk or lost delivery)", name)
		}
	}
}
