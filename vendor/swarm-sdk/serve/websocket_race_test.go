package serve

// websocket_race_test.go proves the bounded outbound notification queue
// added to wsConn (see websocket.go's maxWSOutboundQueue, wsConn.outCh,
// wsConn.runWriter) behaves correctly under three adversarial conditions:
//
//   - Concurrent producers: many goroutines calling writeNotification on
//     the same connection simultaneously must never produce an
//     interleaved/torn JSON frame on the wire (single-consumer runWriter
//     feeding the mutex-protected ws.Conn write serializes everything that
//     reaches the socket).
//   - A slow/stuck reader: the queue must stay bounded at exactly
//     maxWSOutboundQueue and enqueue must never block a producer — excess
//     notifications are dropped (drop-newest-on-full; see the constant's
//     doc comment) rather than buffered without limit.
//   - Goroutine lifecycle: the per-connection runWriter goroutine started
//     by WebSocketHandler must terminate (not leak) once the connection's
//     context is cancelled / the connection closes.
//
// These are in addition to (not a replacement for) the existing
// maxWSInFlightPerConn inbound-concurrency tests in mux_test.go, which are
// untouched by this file and must keep passing unweakened.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/ws"
)

// TestWSConn_ConcurrentNotificationBurst_NoTornFrames drives many
// goroutines calling writeNotification concurrently on one connection and
// asserts every frame that reaches the client is a complete,
// independently-parseable JSON value carrying exactly one distinct
// sequence number each — i.e. no interleaved/torn frame, and no
// duplicated/corrupted payload — proving the single-consumer runWriter
// goroutine correctly serializes concurrent producers rather than letting
// them race the connection's write path directly.
func TestWSConn_ConcurrentNotificationBurst_NoTornFrames(t *testing.T) {
	const n = 100 // well under maxWSOutboundQueue (256): nothing should drop.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	upgrader := ws.NewServerUpgrader()
	connReady := make(chan *wsConn, 1)
	handlerDone := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		sc := newWSConn(c)
		connReady <- sc

		writerDone := make(chan struct{})
		go func() {
			sc.runWriter(ctx)
			close(writerDone)
		}()

		<-handlerDone
		cancel()
		<-writerDone // prove runWriter actually terminates before the test moves on.
	}))
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"
	cliConn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cliConn.Close()

	sc := <-connReady

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			_ = sc.writeNotification(context.Background(), Notification{
				Jsonrpc: jsonrpcVersion,
				Method:  "client.event",
				Params:  map[string]any{"seq": seq},
			})
		}(i)
	}
	wg.Wait()

	if dropped := sc.droppedCount(); dropped != 0 {
		t.Fatalf("dropped = %d, want 0 (n=%d is under maxWSOutboundQueue=%d)", dropped, n, maxWSOutboundQueue)
	}

	seen := make(map[int]bool, n)
	_ = cliConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for len(seen) < n {
		_, msg, err := cliConn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v (got %d/%d frames)", err, len(seen), n)
		}
		var note struct {
			Jsonrpc string         `json:"jsonrpc"`
			Method  string         `json:"method"`
			Params  map[string]any `json:"params"`
		}
		// A torn/interleaved frame would either fail to unmarshal at all or
		// unmarshal into a structurally wrong/incomplete shape; either way
		// this catches it deterministically instead of trusting that a
		// merely non-erroring Unmarshal proves the bytes were intact.
		if err := json.Unmarshal(msg, &note); err != nil {
			t.Fatalf("frame did not parse as a complete, well-formed JSON value (torn frame?): %v; raw=%q", err, msg)
		}
		if note.Jsonrpc != jsonrpcVersion || note.Method != "client.event" {
			t.Fatalf("frame has wrong shape (torn/merged frame?): %q", msg)
		}
		seqF, ok := note.Params["seq"].(float64)
		if !ok {
			t.Fatalf("frame missing/malformed seq field (torn frame?): %q", msg)
		}
		seq := int(seqF)
		if seen[seq] {
			t.Fatalf("duplicate seq %d observed — frame corruption/merge", seq)
		}
		seen[seq] = true
	}

	close(handlerDone)
}

// TestWSConn_SlowReaderBoundedQueue proves that when nothing drains
// wsConn.outCh (the stuck-reader case: runWriter is never started here,
// standing in for a peer whose TCP receive window/OS buffers are full and
// therefore cannot make forward progress), producers enqueueing past
// maxWSOutboundQueue never block and the queue's length never exceeds the
// documented bound — excess notifications are dropped and counted rather
// than buffered without limit.
func TestWSConn_SlowReaderBoundedQueue(t *testing.T) {
	oldBound := maxWSOutboundQueue
	maxWSOutboundQueue = 8
	defer func() { maxWSOutboundQueue = oldBound }()
	bound := maxWSOutboundQueue

	// A real *ws.Conn is not needed to exercise the queue itself — outCh is
	// filled/drained independently of the underlying socket — so this test
	// only needs a wsConn value, not a live connection. nil is intentionally
	// unused: writeNotification never reaches c.conn while outCh has room
	// (that's the point of the queue sitting above the write), and once it
	// is full we only assert on the drop path, which also never touches
	// c.conn.
	sc := newWSConn(nil)

	const attempts = 50 // deliberately many more than bound
	var wg sync.WaitGroup
	done := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			_ = sc.writeNotification(context.Background(), Notification{
				Jsonrpc: jsonrpcVersion,
				Method:  "client.event",
				Params:  map[string]any{"seq": seq},
			})
		}(i)
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("producers blocked instead of dropping on a full queue — bound not enforced (unbounded growth risk)")
	}

	if got := len(sc.outCh); got != bound {
		t.Fatalf("outCh length = %d, want exactly the bound %d (queue must stay at capacity, not grow past it)", got, bound)
	}
	if cap(sc.outCh) != bound {
		t.Fatalf("outCh capacity = %d, want %d", cap(sc.outCh), bound)
	}
	wantDropped := uint64(attempts - bound)
	if got := sc.droppedCount(); got != wantDropped {
		t.Fatalf("droppedCount = %d, want %d (attempts=%d, bound=%d)", got, wantDropped, attempts, bound)
	}
}

// TestWSConn_GoroutineLeak_WriterTerminatesOnClose opens and closes many
// real WebSocket connections through the full WebSocketHandler and asserts
// runtime.NumGoroutine() settles back to (approximately) its baseline
// afterward — proving the per-connection runWriter goroutine (and the
// handler's other housekeeping goroutines) terminate on close/shutdown
// instead of leaking one goroutine per connection forever.
//
// Methodology: wsTestServer's drain() (defined in mux_test.go, same
// package) blocks until WebSocketHandler's internal sync.WaitGroup —
// which now also tracks runWriter, see websocket.go — has fully drained
// for that connection, so this test does not rely on a fixed sleep to
// "probably" catch a leak; it positively waits for the tracked goroutines
// to finish, then also settles briefly (with a GC) before the final
// NumGoroutine sample to let any untracked runtime bookkeeping goroutines
// (e.g. net/http's own connection teardown) quiesce, and asserts the
// after-count is within a small fixed tolerance of the before-count rather
// than exactly equal (background test-runner goroutines unrelated to this
// code are common and would otherwise make the assertion flaky).
func TestWSConn_GoroutineLeak_WriterTerminatesOnClose(t *testing.T) {
	const connections = 50
	const tolerance = 10 // generous fixed slack for unrelated runtime/test goroutines.

	m := newTestMux()
	srv, drain := wsTestServer(m.WebSocketHandler())
	defer srv.Close()

	wsURL, _ := url.Parse(srv.URL)
	wsURL.Scheme = "ws"

	settle := func() int {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		return runtime.NumGoroutine()
	}

	before := settle()

	for i := 0; i < connections; i++ {
		c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		c.Close()
		drain() // waits for this connection's handler goroutines (incl. runWriter) to fully unwind.
	}

	after := settle()

	if after > before+tolerance {
		t.Fatalf("goroutine count grew from %d to %d after opening/closing %d connections (tolerance %d) — possible goroutine leak (runWriter not terminating on close?)",
			before, after, connections, tolerance)
	}
}
