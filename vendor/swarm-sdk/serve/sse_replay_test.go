// Package serve — sse_replay_test.go
//
// HTTP-level coverage for SSEHandler's Last-Event-ID/?after= resume
// support: exact missed-event replay, the explicit typed gap event when a
// cursor predates EventLog's retained history, the no-resume-cursor
// backward-compat smoke test, and independent replay+tail for concurrent
// connections with no cross-talk. Low-level EventLog.SubscribeFrom
// coverage (retention math, gap-boundary edge cases, concurrent readers)
// lives in eventlog_test.go; this file only exercises the wire protocol
// SSEHandler builds on top of it.
package serve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// sseFrame is one parsed "id:"/"event:"/"data:" SSE record.
type sseFrame struct {
	ID    string
	Event string
	Data  string
}

// sseFrameReader incrementally parses SSE frames off an http.Response
// body using the same line-accumulation algorithm real EventSource
// implementations use (see swarm-sdk/swarm-tui/internal/chat/
// attach_sse.go's startSSELoop, which this mirrors at the parsing level).
type sseFrameReader struct {
	sc              *bufio.Scanner
	id, event, data string
}

func newSSEFrameReader(body *http.Response) *sseFrameReader {
	sc := bufio.NewScanner(body.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &sseFrameReader{sc: sc}
}

// next blocks until a complete frame is parsed, the stream ends (ok=false),
// or the scanner errors (ok=false).
func (r *sseFrameReader) next() (sseFrame, bool) {
	for r.sc.Scan() {
		line := r.sc.Text()
		if line == "" {
			if r.data == "" && r.event == "" && r.id == "" {
				continue
			}
			f := sseFrame{ID: r.id, Event: r.event, Data: r.data}
			r.id, r.event, r.data = "", "", ""
			return f, true
		}
		if strings.HasPrefix(line, ":") {
			continue // comment (e.g. ": stream-open")
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch field {
		case "id":
			r.id = value
		case "event":
			r.event = value
		case "data":
			if r.data == "" {
				r.data = value
			} else {
				r.data += "\n" + value
			}
		}
	}
	return sseFrame{}, false
}

// nextNamed blocks until a frame whose Event is one of wantEvents arrives
// (skipping any others — e.g. this package emits no keepalive frames
// today, but a future one should not break these tests), or times out.
func nextNamed(t *testing.T, r *sseFrameReader, timeout time.Duration, wantEvents ...string) sseFrame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		type result struct {
			f  sseFrame
			ok bool
		}
		ch := make(chan result, 1)
		go func() { f, ok := r.next(); ch <- result{f, ok} }()
		select {
		case res := <-ch:
			if !res.ok {
				t.Fatalf("SSE stream ended before a %v frame arrived", wantEvents)
			}
			for _, want := range wantEvents {
				if res.f.Event == want {
					return res.f
				}
			}
			// Not the frame we wanted — keep scanning within the deadline.
		case <-time.After(timeout):
			t.Fatalf("timed out waiting for a %v frame", wantEvents)
		}
	}
	t.Fatalf("timed out waiting for a %v frame", wantEvents)
	return sseFrame{}
}

func decodeThemePayload(t *testing.T, f sseFrame) string {
	t.Helper()
	var env struct {
		Sequence *uint64 `json:"sequence"`
		Payload  struct {
			Theme string `json:"theme"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(f.Data), &env); err != nil {
		t.Fatalf("decode frame data: %v (data=%s)", err, f.Data)
	}
	return env.Payload.Theme
}

// ─── No resume cursor: backward-compat smoke test ──────────────────────────

// TestSSEHandler_NoResumeCursorTailOnly proves a plain connection (no
// Last-Event-ID header, no ?after= query parameter) behaves exactly as
// before this task: no replay of anything produced before the connection
// opened, live events only, in order. CONTRACT.md: "no-resume-cursor
// connects tail-only as before".
func TestSSEHandler_NoResumeCursorTailOnly(t *testing.T) {
	m := newTestMux()
	srv := httptest.NewServer(m.SSEHandler())
	defer srv.Close()

	// Force eventLogFor(m)'s lazy creation/attach synchronously (see the
	// identical comment in TestSSEHandler_ConcurrentConnectionsNoCrossTalk)
	// so the "before-connect" event below is actually recorded into the
	// ring buffer — otherwise this test would trivially pass for the wrong
	// reason (no EventLog existing yet to have recorded it at all) instead
	// of proving a no-cursor connection deliberately skips buffered history
	// it could have replayed.
	elog := eventLogFor(m)

	// Produced BEFORE any connection exists — must never be replayed to a
	// no-cursor connection, even though EventLog above has genuinely
	// buffered it (proven by waitForSeq below).
	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "before-connect"}})
	waitForSeq(t, elog, 1)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	r := newSSEFrameReader(resp)

	// See waitForListenerCount's doc comment (eventlog_test.go): a plain
	// tail-only connection gives the client no wire signal that its live
	// listener is registered yet, unlike a resuming connection whose
	// replay frames prove it.
	waitForListenerCount(t, elog, 1)

	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "after-connect"}})

	f := nextNamed(t, r, 3*time.Second, "theme_changed")
	if got := decodeThemePayload(t, f); got != "after-connect" {
		t.Fatalf("first observed event theme = %q, want %q (the before-connect event must not have been replayed)", got, "after-connect")
	}
	if f.ID == "" {
		t.Fatal("expected a non-empty SSE id: line so a browser EventSource can resume via Last-Event-ID")
	}
}

// ─── Resume: exact missed-event replay ──────────────────────────────────────

func testResume(t *testing.T, useHeader bool) {
	t.Helper()
	m := newTestMux()
	srv := httptest.NewServer(m.SSEHandler())
	defer srv.Close()

	// Force eventLogFor(m)'s lazy creation/attach synchronously (see the
	// identical comment in TestSSEHandler_GapEventWhenCursorPredatesRetention)
	// and so waitForListenerCount below has a stable *EventLog to poll.
	elog := eventLogFor(m)

	// First connection: observe a couple of live events, remember the
	// last-seen sequence, then disconnect.
	resp1, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	r1 := newSSEFrameReader(resp1)

	// resp1 has no resume cursor (tail-only) — wait for its listener to be
	// registered before injecting the event it must observe live; see
	// waitForListenerCount's doc comment for why this is needed.
	waitForListenerCount(t, elog, 1)

	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "seen-1"}})
	f1 := nextNamed(t, r1, 3*time.Second, "theme_changed")
	if got := decodeThemePayload(t, f1); got != "seen-1" {
		t.Fatalf("got %q, want seen-1", got)
	}
	lastSeenID := f1.ID
	resp1.Body.Close() // disconnect

	// While disconnected, three more events are produced — these are what
	// the reconnect must replay, in order, exactly once each.
	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "missed-1"}})
	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "missed-2"}})
	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "missed-3"}})

	// Give the (now-detached) first connection's cancel a moment so its
	// EventLog listener is deregistered before we assert on the second
	// connection's isolated view — not strictly required for correctness
	// (each SubscribeFrom listener is independent either way) but avoids
	// flakiness from two listeners racing to drain a shared test timeline.
	time.Sleep(20 * time.Millisecond)

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if useHeader {
		req.Header.Set("Last-Event-ID", lastSeenID)
	} else {
		req.URL.RawQuery = "after=" + lastSeenID
	}
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("resume get: %v", err)
	}
	defer resp2.Body.Close()
	r2 := newSSEFrameReader(resp2)

	wantThemes := []string{"missed-1", "missed-2", "missed-3"}
	for i, want := range wantThemes {
		f := nextNamed(t, r2, 3*time.Second, "theme_changed")
		if got := decodeThemePayload(t, f); got != want {
			t.Fatalf("replayed event %d theme = %q, want %q (exact missed events must replay in order)", i, got, want)
		}
	}

	// After replay, live tail continues seamlessly with no duplication and
	// no gap.
	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "live-after-resume"}})
	f := nextNamed(t, r2, 3*time.Second, "theme_changed")
	if got := decodeThemePayload(t, f); got != "live-after-resume" {
		t.Fatalf("post-replay live theme = %q, want live-after-resume", got)
	}
}

func TestSSEHandler_ResumeViaAfterQueryParam(t *testing.T) {
	testResume(t, false)
}

func TestSSEHandler_ResumeViaLastEventIDHeader(t *testing.T) {
	testResume(t, true)
}

// ─── Gap: cursor predates retained history ──────────────────────────────────

// TestSSEHandler_GapEventWhenCursorPredatesRetention forces EventLog past
// its default ring-buffer capacity (NewEventLog(0) defaults to 1024) so
// early sequence numbers are genuinely evicted, then reconnects with a
// cursor from that evicted range and asserts the server emits a single
// typed "event: gap" frame — never a silent skip to the oldest retained
// event, never a silently dropped/errored request — carrying the exact
// requested/oldest/latest cursor fields.
func TestSSEHandler_GapEventWhenCursorPredatesRetention(t *testing.T) {
	m := newTestMux()
	srv := httptest.NewServer(m.SSEHandler())
	defer srv.Close()

	// Force eventLogFor(m)'s lazy creation/attach synchronously before
	// producing any events. eventLogFor is idempotent (keyed by *Mux), so
	// this is the exact same *EventLog every SSEHandler connection below
	// will use — calling it directly here (rather than relying on a
	// connection's handler goroutine to reach its own eventLogFor call
	// first) avoids a race where events injected immediately after opening
	// a connection could be recorded before or after that goroutine has
	// actually attached, depending on scheduling.
	elog := eventLogFor(m)

	const flood = 1100 // comfortably past the default 1024 capacity.
	for i := 0; i < flood; i++ {
		m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: fmt.Sprintf("flood-%d", i)}})
	}
	waitForSeq(t, elog, flood)

	oldest, latest, ok := elog.Retention()
	if !ok {
		t.Fatal("expected Retention() ok = true after flooding events")
	}
	if oldest == 0 {
		t.Fatal("expected eviction to have occurred (oldest should be > 0 after flooding past capacity)")
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.URL.RawQuery = "after=0" // long evicted.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("resume get: %v", err)
	}
	defer resp.Body.Close()
	r := newSSEFrameReader(resp)

	f := nextNamed(t, r, 3*time.Second, EventKindGap)
	var env struct {
		Kind    string     `json:"kind"`
		Payload GapPayload `json:"payload"`
	}
	if err := json.Unmarshal([]byte(f.Data), &env); err != nil {
		t.Fatalf("decode gap frame: %v (data=%s)", err, f.Data)
	}
	if env.Payload.Requested != 0 {
		t.Fatalf("gap payload Requested = %d, want 0", env.Payload.Requested)
	}
	if env.Payload.Oldest != oldest {
		t.Fatalf("gap payload Oldest = %d, want %d", env.Payload.Oldest, oldest)
	}
	if env.Payload.Latest < latest {
		t.Fatalf("gap payload Latest = %d, want >= %d", env.Payload.Latest, latest)
	}

	// The gap connection ends after the gap frame — no replay, no live
	// tail — since the client must make an explicit recovery decision.
	if _, ok := r.next(); ok {
		t.Fatal("expected the connection to end after the gap frame, but more data followed")
	}
}

// ─── Concurrent connections: independent replay+tail, no cross-talk ────────

// TestSSEHandler_ConcurrentConnectionsNoCrossTalk opens several concurrent
// SSE connections — some resuming from different cursors, one tail-only —
// and proves each sees exactly its own expected sequence with no event
// duplicated or delivered to the wrong connection.
func TestSSEHandler_ConcurrentConnectionsNoCrossTalk(t *testing.T) {
	m := newTestMux()
	srv := httptest.NewServer(m.SSEHandler())
	defer srv.Close()

	// Force eventLogFor(m)'s lazy creation/attach synchronously before
	// producing any events — see the identical comment in
	// TestSSEHandler_GapEventWhenCursorPredatesRetention for why this must
	// happen before injecting anything rather than relying on a
	// connection's handler goroutine to reach its own eventLogFor call.
	elog := eventLogFor(m)

	for i := 0; i < 5; i++ {
		m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: fmt.Sprintf("seed-%d", i)}})
	}
	waitForSeq(t, elog, 5)
	_, latest, _ := elog.Retention()

	type conn struct {
		name string
		resp *http.Response
		r    *sseFrameReader
		want []string // theme values this connection must observe, in order
	}

	mkReq := func(after string) *http.Request {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		if after != "" {
			req.URL.RawQuery = "after=" + after
		}
		return req
	}

	// Events(after)/SubscribeFrom(after) are exclusive of `after` itself
	// (see eventlog.go's eventsAfterLocked), so "replay seed-4 onward" is
	// after=latest-2 (excludes seed-3, includes seed-4) and "replay
	// seed-2 onward" is after=latest-4 (excludes seed-1, includes
	// seed-2..seed-4).
	respA, err := http.DefaultClient.Do(mkReq(fmt.Sprintf("%d", latest-2))) // resume from seed-4 onward
	if err != nil {
		t.Fatalf("connA: %v", err)
	}
	respB, err := http.DefaultClient.Do(mkReq(fmt.Sprintf("%d", latest-4))) // resume from seed-2 onward
	if err != nil {
		t.Fatalf("connB: %v", err)
	}
	respC, err := http.DefaultClient.Do(mkReq("")) // tail-only, no replay
	if err != nil {
		t.Fatalf("connC: %v", err)
	}

	conns := []*conn{
		{name: "A", resp: respA, r: newSSEFrameReader(respA), want: []string{"seed-4", "live-1", "live-2"}},
		{name: "B", resp: respB, r: newSSEFrameReader(respB), want: []string{"seed-2", "seed-3", "seed-4", "live-1", "live-2"}},
		{name: "C", resp: respC, r: newSSEFrameReader(respC), want: []string{"live-1", "live-2"}},
	}
	defer func() {
		for _, c := range conns {
			c.resp.Body.Close()
		}
	}()

	// connC has no resume cursor (tail-only) and so — unlike connA/connB,
	// whose replay frames prove their listener is already registered once
	// read — gives the test no wire signal that its live listener exists
	// yet. Wait for all three listeners before injecting the live events
	// every connection below must observe; see waitForListenerCount's doc
	// comment (eventlog_test.go) for the full explanation.
	waitForListenerCount(t, elog, 3)

	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "live-1"}})
	m.Client().InjectEvent(client.Event{Kind: client.EventThemeChanged, Payload: client.ThemePayload{Theme: "live-2"}})

	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func(c *conn) {
			defer wg.Done()
			for i, want := range c.want {
				f := nextNamed(t, c.r, 5*time.Second, "theme_changed")
				if got := decodeThemePayload(t, f); got != want {
					t.Errorf("conn %s frame %d theme = %q, want %q", c.name, i, got, want)
				}
			}
		}(c)
	}
	wg.Wait()
}
