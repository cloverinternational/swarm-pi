package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/ws"
)

// Bounds and timing shared by the JSON-RPC WebSocket transport. Package
// variables let tests shrink them to exercise caps, slow-client handling, and
// shutdown deterministically without waiting out production-sized timeouts.
var (
	// maxWSMessageBytes bounds a single inbound WebSocket message via
	// gorilla/websocket's SetReadLimit, which fails the read (and closes the
	// connection with a 1009 "message too big" close code) once exceeded —
	// before the oversized message is fully buffered.
	maxWSMessageBytes int64 = 4 << 20 // 4 MiB, matching the HTTP transport cap.

	// wsPongWait bounds how long a connection may go without a received
	// Pong before it is considered dead and the read loop is unblocked with
	// a deadline error. It is refreshed on every Pong. A slow-but-alive
	// client that keeps answering pings stays connected indefinitely; a
	// client that stops responding (crashed, network partition, or
	// deliberately withholding Pongs to hold the connection open) is
	// dropped instead of leaking the connection's goroutines forever.
	wsPongWait = 60 * time.Second

	// wsWriteWait bounds a single ping control-frame write.
	wsWriteWait = 10 * time.Second

	// maxWSInFlightPerConn bounds the number of concurrently executing
	// method dispatches for one WebSocket connection. Each inbound request
	// still runs on its own goroutine so the connection stays responsive to
	// concurrent calls, but an unbounded number of in-flight goroutines per
	// connection is a resource-exhaustion vector for a single misbehaving
	// or malicious peer. Once the bound is reached, acquiring a slot blocks
	// the read loop — deliberate backpressure rather than an unbounded
	// queue of buffered requests.
	maxWSInFlightPerConn = 64

	// maxWSOutboundQueue bounds the number of pending outbound
	// notifications ("client.event"/"client.update") buffered per
	// WebSocket connection between the goroutines that produce them
	// (Subscribe/SubscribeUpdates callbacks, which may run concurrently
	// with each other and with in-flight request handling) and the single
	// per-connection writer goroutine that actually calls the
	// mutex-protected ws.Conn write methods.
	//
	// Policy on a full queue (slow/stuck reader): DROP THE NEWEST
	// notification (the one currently being enqueued) and increment
	// wsConn.dropped, leaving everything already queued untouched and the
	// connection open. This mirrors serve/sse.go's existing drop-on-full
	// precedent exactly ("Buffer full — drop the event... choose to drop
	// rather than block to keep the agent loop fast"): a non-blocking
	// channel send with a default branch, same bounded memory guarantee,
	// same choice to keep the producer (the event/update source) from
	// ever blocking on a stuck peer. Notifications are already understood
	// to be best-effort/lossy for a slow consumer (see writeNotification's
	// doc comment on dropping unencodable payloads); dropping under queue
	// pressure is a natural extension of that same contract rather than a
	// new one, so closing the connection instead was rejected here as
	// unnecessarily harsh for a merely-slow (not necessarily dead) peer —
	// the existing wsPongWait/ping machinery already closes a genuinely
	// dead connection independently of this queue.
	maxWSOutboundQueue = 256
)

// defaultUpgrader fails closed on a browser-presented cross-origin
// WebSocket handshake (see defaultCheckOrigin) while allowing the existing
// bearer-capable non-browser compatibility path. Production deployments
// that need a different policy — for example validating against a
// configured allowlist rather than same-origin — may still override it via
// Mux.wsUpgrader (see the unexported upgrader() accessor below).
var defaultUpgrader = ws.NewServerUpgrader(ws.WithCheckOrigin(defaultCheckOrigin))

// defaultCheckOrigin fails closed for a browser-presented cross-origin
// WebSocket handshake to prevent cross-site WebSocket hijacking, mirroring
// ADR-007's Origin-check requirement for sensitive HTTP/SSE/WebSocket
// routes. It does not perform authentication: that remains Mux.applyAuth's
// job (bearer/session — the seam owned by worker 2), applied before Upgrade
// is ever called. A missing Origin header is treated as a non-browser
// client (CLI, desktop shell, service-to-service caller) since only a
// browser attaches Origin automatically and unforgeably; such clients rely
// on the bearer/session check for identity, not on network origin.
func defaultCheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// wsUpgrader is the configured upgrader; lazily falls back to defaultUpgrader.
func (m *Mux) upgrader() *ws.ServerUpgrader {
	if m.wsUpgrader == nil {
		return defaultUpgrader
	}
	return m.wsUpgrader
}

// wsConn wraps a ws.Conn for JSON-RPC message handling.
// It provides a writeJSON method for sending responses.
type wsConn struct {
	conn *ws.Conn

	// outCh is the bounded outbound notification queue (see
	// maxWSOutboundQueue). Exactly one consumer goroutine (runWriter)
	// reads from it and calls the mutex-protected conn.WriteMessage, so
	// concurrent notification producers never call the underlying write
	// methods directly/concurrently themselves — they enqueue here
	// instead, preserving FIFO delivery order among notifications that
	// were successfully enqueued.
	outCh chan []byte

	// dropped counts notifications discarded because outCh was full at
	// enqueue time (see maxWSOutboundQueue's drop-newest-on-full
	// policy). Read via droppedCount; primarily for tests/diagnostics.
	dropped uint64
}

// newWSConn constructs a wsConn with its bounded outbound queue ready to
// use. Callers must start runWriter (in its own goroutine, tracked by the
// connection's wait group) before any notification is enqueued, or
// notifications will simply accumulate up to maxWSOutboundQueue and then
// begin dropping per policy until a writer is started.
func newWSConn(conn *ws.Conn) *wsConn {
	return &wsConn{conn: conn, outCh: make(chan []byte, maxWSOutboundQueue)}
}

// droppedCount returns the number of notifications discarded so far
// because the outbound queue was full (see maxWSOutboundQueue).
func (c *wsConn) droppedCount() uint64 {
	return atomic.LoadUint64(&c.dropped)
}

// runWriter is the single per-connection consumer of outCh: it is the only
// goroutine that ever calls writeRaw for a queued notification, so
// concurrent notification producers (multiple Subscribe/SubscribeUpdates
// callbacks firing at once) never race each other on the connection's
// write path even though ws.Conn's own mutex would already prevent a torn
// frame — this additionally preserves FIFO ordering among notifications
// that made it into the queue. It returns (terminating the goroutine, so
// callers must track it and it never leaks) as soon as ctx is cancelled or
// a write fails (peer gone / connection closing), whichever comes first.
func (c *wsConn) runWriter(ctx context.Context) {
	for {
		select {
		case b, ok := <-c.outCh:
			if !ok {
				return
			}
			if err := c.writeRaw(b); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// writeRaw sends an already-bounded-encoded JSON-RPC frame verbatim. Callers
// must have produced payload via encodeBoundedJSON/boundedEncodeResponse —
// this method performs no encoding of its own.
func (c *wsConn) writeRaw(payload []byte) error {
	return c.conn.WriteMessage(ws.TextMessage, payload)
}

// writeResponse bounded-encodes resp (falling back to the fixed
// response_too_large/response_encoding_error envelope on failure — see
// boundedEncodeResponse) and sends it. Used for the small, trusted,
// fixed-shape Response values built directly by this file (parse errors,
// unknown method, bad jsonrpc version); the arbitrary-result Response built
// from a dispatched method's own output is instead encoded inside the
// dispatch child itself (see handleWSRequest) so it stays under the global
// dispatch limiter until encoding actually completes.
func (c *wsConn) writeResponse(ctx context.Context, resp Response) error {
	return c.writeRaw(boundedEncodeResponse(ctx, resp))
}

// writeNotification bounded-encodes n and sends it. Unlike writeResponse, a
// notification that does not fit or cannot be safely encoded is dropped
// rather than replaced with a fabricated envelope: client.event/
// client.update notifications have no id for an error reply to correlate
// against, and the connection must stay open and usable for the next event
// rather than injecting a shape the client does not expect for that method
// name.
//
// The encoded frame is enqueued onto the connection's bounded outbound
// queue (outCh / maxWSOutboundQueue) rather than written directly: this
// method may be called concurrently by multiple notification sources
// (the auto-subscribe Subscribe callback and, per in-flight streaming
// request, a SubscribeUpdates callback), and routing all of them through
// one queue feeding one consumer goroutine (runWriter) is what preserves
// FIFO order and keeps every producer non-blocking regardless of how slow
// the peer's reader is. If the queue is full, the new notification is
// dropped and c.dropped is incremented — see maxWSOutboundQueue's doc
// comment for the full policy rationale.
func (c *wsConn) writeNotification(ctx context.Context, n Notification) error {
	b, err := encodeBoundedJSON(ctx, n)
	if err != nil {
		return nil
	}
	select {
	case c.outCh <- b:
		return nil
	default:
		atomic.AddUint64(&c.dropped, 1)
		return nil
	}
}

// WebSocketHandler returns an http.Handler that upgrades to WebSocket and
// speaks JSON-RPC 2.0 bidirectionally:
//
//   - Client → Server: Request envelopes; the server dispatches and sends a
//     matching Response.
//   - Server → Client: Notifications.  "client.event" carries
//     Subscribe events (auto-installed for the duration of the connection);
//     "client.update" carries per-call IntermediateUpdates correlated by id
//     for streaming methods.
//
// One subscription per connection.  Closing the WebSocket unsubscribes.
func (m *Mux) WebSocketHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := m.applyAuth(w, r)
		if !ok {
			return
		}

		// Install the write deadline before attempting the upgrade itself:
		// a failed handshake (bad Origin, wrong method, missing headers,
		// ...) makes Upgrade write an HTTP error response synchronously on
		// w, and that write must be bounded exactly like every other
		// early-denial path in this package. Fail closed (never attempt the
		// upgrade) if w cannot support a write deadline.
		if !setResponseWriteDeadline(w) {
			return
		}
		conn, err := m.upgrader().Upgrade(w, r, nil)
		if err != nil {
			// Upgrade has already written the response on error.
			return
		}
		defer conn.Close()

		wsConn := newWSConn(conn)
		// Derive the connection context before registering callbacks so
		// they capture an immutable context interface rather than racing a
		// later reassignment. defer cancel remains below defer wg.Wait so
		// LIFO shutdown still cancels supervisors before draining them.
		ctx, cancel := context.WithCancel(ctx)

		// Bound the size of a single inbound message and require forward
		// progress from the peer: the read deadline is refreshed on every
		// received Pong, so a genuinely idle-but-alive client stays
		// connected, while a client that stops responding is closed instead
		// of pinning the connection and its goroutines forever.
		underlying := conn.Underlying()
		underlying.SetReadLimit(maxWSMessageBytes)
		_ = underlying.SetReadDeadline(time.Now().Add(wsPongWait))
		underlying.SetPongHandler(func(string) error {
			return underlying.SetReadDeadline(time.Now().Add(wsPongWait))
		})

		// Auto-subscribe: every connection receives Subscribe events as
		// "client.event" notifications.  TS clients can opt out by ignoring
		// them.
		unsub := m.client.Subscribe(func(ev client.Event) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			env := EncodeEvent(ev)
			_ = wsConn.writeNotification(ctx, Notification{
				Jsonrpc: jsonrpcVersion,
				Method:  "client.event",
				Params:  env,
			})
			return nil
		})
		defer unsub()

		var wg sync.WaitGroup
		defer wg.Wait()

		// ctx's cancel must run (via defer) BEFORE wg.Wait unblocks any
		// handler goroutine that is only waiting on ctx.Done() — declaring
		// this defer after wg's means it executes first during unwind
		// (defers are LIFO), so cancellation always precedes the drain.
		// Reversing this order is a deadlock: wg.Wait would block forever
		// waiting for a handler that only exits on cancellation.
		defer cancel()

		// connClosed lets the shutdown watcher below exit without leaking
		// once the read loop returns on its own (client-initiated close or
		// a read error), and lets the watcher force-close the connection to
		// unblock a pending conn.ReadMessage() when ctx is cancelled from
		// outside this handler — e.g. the inbound HTTP request context
		// ending during server shutdown. Without the watcher, a blocked
		// read loop would leak until the peer eventually disconnects on its
		// own, and shutdown would not "drain" the connection at all.
		connClosed := make(chan struct{})
		defer close(connClosed)

		// Both housekeeping goroutines below are tracked by wg (not a bare
		// "go func(){}()") so that "defer wg.Wait()" genuinely drains them
		// before this handler returns. Leaving them untracked was a real
		// bug: the handler function (and therefore net/http's ServeHTTP,
		// and therefore httptest.Server.Close()) could return/complete
		// while the ping ticker or shutdown watcher was still briefly
		// alive, racing the *next* connection's read of the same
		// package-level tuning variables (maxWSMessageBytes, wsPongWait,
		// ...) against a concurrently running test mutating them — exactly
		// the kind of shutdown goroutine leak this hardening pass exists
		// to close.
		wg.Go(func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-connClosed:
			}
		})

		// Single per-connection outbound writer: the only goroutine that
		// dequeues wsConn.outCh and calls the mutex-protected
		// conn.WriteMessage for a queued notification (see runWriter's doc
		// comment and maxWSOutboundQueue). Tracked by wg like the other two
		// housekeeping goroutines so "defer wg.Wait()" genuinely drains it
		// — it terminates as soon as ctx is cancelled (cancel() runs before
		// wg.Wait() per the LIFO ordering explained above), so it can never
		// leak past this handler returning.
		wg.Go(func() {
			wsConn.runWriter(ctx)
		})

		// Periodic ping refreshes the slow-client detection above and
		// surfaces a half-open connection. WriteControl is safe to call
		// concurrently with WriteJSON/WriteMessage — gorilla/websocket
		// documents the Close and WriteControl methods as safe alongside
		// all other connection methods — so pings do not need wsConn's
		// write mutex. Only ordinary data frames must be serialized through
		// one writer, which wsConn.writeRaw (via the shared ws.Conn mutex)
		// already guarantees.
		wg.Go(func() {
			ticker := time.NewTicker((wsPongWait * 9) / 10)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := underlying.WriteControl(ws.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
						return
					}
				case <-ctx.Done():
					return
				}
			}
		})

		// sem bounds concurrently in-flight method dispatches for this
		// connection; see maxWSInFlightPerConn.
		sem := make(chan struct{}, maxWSInFlightPerConn)

		// Per-connection update fan-out: while a streaming method is in
		// flight we tap SubscribeUpdates and forward as "client.update"
		// notifications carrying the request id for correlation.
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var req Request
			if err := json.Unmarshal(msg, &req); err != nil {
				_ = wsConn.writeResponse(ctx, makeErrorResponse(nil, NewError(ErrParseError, err.Error(), nil)))
				continue
			}
			if req.Jsonrpc != "" && req.Jsonrpc != jsonrpcVersion {
				_ = wsConn.writeResponse(ctx, makeErrorResponse(req.ID, NewError(ErrInvalidRequest, "jsonrpc must be 2.0", req.Jsonrpc)))
				continue
			}

			// Acquire an in-flight slot before spawning; blocks (bounded
			// backpressure) rather than spawning unboundedly, and still
			// observes shutdown so a full semaphore cannot itself leak the
			// read loop.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}

			// Each tracked goroutine is a cancellation-responsive supervisor.
			// Method code itself runs in a result-only child which never owns
			// the connection. If method code ignores cancellation, the
			// supervisor still returns and releases its per-connection slot;
			// at most maxWSInFlightPerConn abandoned children can originate
			// from this connection.
			wg.Go(func() {
				defer func() { <-sem }()
				m.handleWSRequest(ctx, wsConn, req)
			})
		}
	})
}

// handleWSRequest supervises a single Request envelope received over the
// WebSocket, optionally tapping SubscribeUpdates for streaming methods. The
// supervisor is tracked by the connection wait group; the result-only method
// child is deliberately not tracked because arbitrary method code may ignore
// cancellation forever. Only this supervisor may write the child's result.
func (m *Mux) handleWSRequest(ctx context.Context, conn *wsConn, req Request) {
	method, ok := m.Lookup(req.Method)
	if !ok {
		if !req.IsNotification() {
			_ = conn.writeResponse(ctx, makeErrorResponse(req.ID, NewError(ErrMethodNotFound, "method not found", req.Method)))
		}
		return
	}

	// For streaming methods, install a temporary update tap that forwards
	// IntermediateUpdates as "client.update" notifications correlated to
	// this request id.
	var unsub func()
	if method.Streams {
		unsub = m.client.SubscribeUpdates(func(_ context.Context, u agent.IntermediateUpdate) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			_ = conn.writeNotification(ctx, Notification{
				Jsonrpc: jsonrpcVersion,
				Method:  "client.update",
				Params: map[string]any{
					"id":     json.RawMessage(req.ID),
					"update": EncodeUpdate(u),
				},
			})
			return nil
		})
		defer func() {
			if unsub != nil {
				unsub()
			}
		}()
	}

	resultCh := make(chan dispatchResult, 1)
	go func() {
		limiter := m.dispatchLimiter()
		if !limiter.acquire(ctx) {
			return
		}
		defer limiter.release()

		result, dispatchErr := m.Dispatch(ctx, req.Method, req.Params)
		if req.IsNotification() {
			resultCh <- dispatchResult{}
			return
		}

		var response Response
		if dispatchErr != nil {
			response = makeErrorResponse(req.ID, dispatchErr)
		} else {
			response = makeResponse(req.ID, result)
		}
		// Bounded encoding happens inside the limiter-held child, exactly
		// like the HTTP transport (see http.go), so an expensive-to-encode
		// result stays under the global dispatch capacity until encoding
		// actually completes rather than escaping it the moment Dispatch
		// returns.
		resultCh <- dispatchResult{payload: boundedEncodeResponse(ctx, response)}
	}()

	var outcome dispatchResult
	select {
	case outcome = <-resultCh:
	case <-ctx.Done():
		// The child owns no connection reference and its result channel has
		// capacity one, so a permanently blocked or late-returning handler
		// cannot delay shutdown or emit a response after cancellation.
		return
	}
	if ctx.Err() != nil {
		return
	}

	if req.IsNotification() {
		return
	}
	_ = conn.writeRaw(outcome.payload)
}
