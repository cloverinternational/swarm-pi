// Package a2a — wspool.go
//
// ConnectionPool manages per-peer WebSocket connections for A2A messaging.
// It provides:
//   - Lazy connection establishment (dial on first use)
//   - Connection reuse across multiple sends to the same peer
//   - Automatic reconnect with exponential backoff on transient failures
//   - Response correlation (match request ID → response)
//   - Health monitoring (ping/pong, dead connection eviction)
//   - Fire-and-forget Send for DM / Broadcast
//   - Optional Request/Response for wait-for-reply patterns
//
// Design rationale:
//   - One goroutine per connection (read loop) + a per-pool reaper.
//   - All Send operations are non-blocking on the caller — the read loop
//     pushes responses into per-request channels stored in respWaiters.
//   - On disconnect, the pool retries up to MaxReconnectAttempts with
//     exponential backoff (capped at MaxReconnectBackoff). Callers see
//     a transient error; the inbox fallback in Runtime.SendDM persists
//     the message for offline delivery.

package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/gorilla/websocket"
)

// Pool tunables. Exposed as variables (not constants) so tests can override.
var (
	// PoolDialTimeout is the maximum time to wait for a WebSocket handshake.
	PoolDialTimeout = 5 * time.Second
	// PoolSendTimeout is the maximum time to wait for a fire-and-forget send to succeed.
	PoolSendTimeout = 3 * time.Second
	// PoolRequestTimeout is the default timeout for a wait-for-reply request.
	PoolRequestTimeout = 10 * time.Second
	// PoolMaxReconnectAttempts is how many reconnect tries before giving up.
	PoolMaxReconnectAttempts = 3
	// PoolInitialReconnectBackoff is the starting backoff between reconnect tries.
	PoolInitialReconnectBackoff = 200 * time.Millisecond
	// PoolMaxReconnectBackoff caps exponential backoff growth.
	PoolMaxReconnectBackoff = 5 * time.Second
	// PoolHealthCheckInterval is how often the reaper sweeps for dead connections.
	PoolHealthCheckInterval = 30 * time.Second
	// PoolWriteWait is the WebSocket write deadline applied to every frame.
	PoolWriteWait = 5 * time.Second
)

// pooledConn wraps a PeerConnection with reconnect / lifecycle metadata.
type pooledConn struct {
	handle      string
	endpointURL string
	conn        *websocket.Conn
	writeMu     sync.Mutex
	closed      atomic.Bool

	// respWaiters maps a message ID → channel that the read loop signals.
	respWaiters   map[string]chan *WebSocketMessage
	respWaitersMu sync.Mutex

	// Connection attempt count; resets to 0 on a clean read.
	attempts atomic.Int32

	// Cancel func for the goroutine that runs the read loop.
	cancel context.CancelFunc
	// Done is closed when the read loop exits (after final reconnect attempt).
	done chan struct{}

	logger observability.Logger
}

// ConnectionPool manages WebSocket connections to A2A peers, keyed by handle.
type ConnectionPool struct {
	mu      sync.RWMutex
	conns   map[string]*pooledConn
	dialer  *websocket.Dialer
	logger  observability.Logger
	stopCh  chan struct{}
	stopped atomic.Bool
	wg      sync.WaitGroup

	// inboundHandler, when set, processes "request" messages received over a
	// pool-managed (client-side) connection. This enables full-duplex DMs.
	inboundHandlerMu sync.RWMutex
	inboundHandler   RequestHandler
}

// NewConnectionPool constructs a pool ready to dial peers.
func NewConnectionPool(logger observability.Logger) *ConnectionPool {
	if logger == nil {
		logger = noop.NewLogger()
	}
	p := &ConnectionPool{
		conns: make(map[string]*pooledConn),
		dialer: &websocket.Dialer{
			HandshakeTimeout: PoolDialTimeout,
			Subprotocols:     []string{WebSocketSubprotocol},
			ReadBufferSize:   4096,
			WriteBufferSize:  4096,
		},
		logger: logger,
		stopCh: make(chan struct{}),
	}
	p.wg.Add(1)
	go p.healthCheckLoop()
	return p
}

// SetInboundHandler installs a handler invoked when a peer sends a "request"
// message over a pool-managed connection. The pool is not the primary inbound
// server — that role belongs to WebSocketServer — but client-side connections
// can also receive requests over the bidirectional channel.
func (p *ConnectionPool) SetInboundHandler(h RequestHandler) {
	p.inboundHandlerMu.Lock()
	p.inboundHandler = h
	p.inboundHandlerMu.Unlock()
}

// Close shuts down the pool and all connections. Idempotent.
func (p *ConnectionPool) Close() {
	if !p.stopped.CompareAndSwap(false, true) {
		return
	}
	close(p.stopCh)

	p.mu.Lock()
	for _, pc := range p.conns {
		pc.closeConn()
	}
	p.conns = make(map[string]*pooledConn)
	p.mu.Unlock()

	p.wg.Wait()
}

// dial opens a fresh WebSocket connection to the peer. Used both by
// ensureConn (initial dial) and the reconnect path in readLoop.
func (p *ConnectionPool) dial(ctx context.Context, handle, endpointURL string) (*websocket.Conn, error) {
	wsURL := convertToWebSocketURL(endpointURL)
	dialCtx, cancel := context.WithTimeout(ctx, PoolDialTimeout)
	defer cancel()

	headers := http.Header{
		"X-A2A-Handle": []string{handle},
		"A2A-Version":  []string{ProtocolVersion},
	}

	conn, _, err := p.dialer.DialContext(dialCtx, wsURL, headers)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", wsURL, err)
	}
	return conn, nil
}

// ensureConn returns an active pooled connection for handle, dialing if absent.
func (p *ConnectionPool) ensureConn(ctx context.Context, handle, endpointURL string) (*pooledConn, error) {
	if p.stopped.Load() {
		return nil, fmt.Errorf("connection pool closed")
	}

	p.mu.RLock()
	pc, ok := p.conns[handle]
	p.mu.RUnlock()
	if ok && !pc.closed.Load() {
		return pc, nil
	}

	// Dial under write lock to avoid duplicate concurrent dials.
	p.mu.Lock()
	defer p.mu.Unlock()
	if pc, ok = p.conns[handle]; ok && !pc.closed.Load() {
		return pc, nil
	}

	conn, err := p.dial(ctx, handle, endpointURL)
	if err != nil {
		return nil, err
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	pc = &pooledConn{
		handle:      handle,
		endpointURL: endpointURL,
		conn:        conn,
		respWaiters: make(map[string]chan *WebSocketMessage),
		cancel:      runCancel,
		done:        make(chan struct{}),
		logger:      p.logger,
	}
	p.conns[handle] = pc

	p.wg.Add(1)
	go p.readLoop(runCtx, pc)

	p.logger.Info(ctx, "wspool.connected",
		observability.F("handle", handle),
		observability.F("url", convertToWebSocketURL(endpointURL)))
	return pc, nil
}

// SendFireAndForget sends a message without waiting for a peer reply.
// Returns an error if the connection cannot be established or the write fails.
// The caller is responsible for any fallback (e.g. inbox persistence).
func (p *ConnectionPool) SendFireAndForget(ctx context.Context, handle, endpointURL string, msg WebSocketMessage) error {
	pc, err := p.ensureConn(ctx, handle, endpointURL)
	if err != nil {
		return err
	}
	return pc.writeJSON(msg)
}

// SendRequest sends a request and waits for the matching response message.
// Uses the message's ID to correlate; if msg.ID is empty, generates one.
// Honors ctx cancellation and PoolRequestTimeout (whichever is sooner).
func (p *ConnectionPool) SendRequest(ctx context.Context, handle, endpointURL string, msg WebSocketMessage) (*WebSocketMessage, error) {
	if msg.ID == "" {
		msg.ID = generateMessageID()
	}
	pc, err := p.ensureConn(ctx, handle, endpointURL)
	if err != nil {
		return nil, err
	}

	respCh := make(chan *WebSocketMessage, 1)
	pc.respWaitersMu.Lock()
	pc.respWaiters[msg.ID] = respCh
	pc.respWaitersMu.Unlock()
	defer func() {
		pc.respWaitersMu.Lock()
		delete(pc.respWaiters, msg.ID)
		pc.respWaitersMu.Unlock()
	}()

	if err := pc.writeJSON(msg); err != nil {
		return nil, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, PoolRequestTimeout)
	defer cancel()

	select {
	case resp := <-respCh:
		if resp != nil && resp.Error != nil {
			return resp, fmt.Errorf("peer error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	case <-reqCtx.Done():
		return nil, fmt.Errorf("request %s timed out: %w", msg.ID, reqCtx.Err())
	}
}

// Disconnect closes a specific peer connection and removes it from the pool.
func (p *ConnectionPool) Disconnect(handle string) {
	p.mu.Lock()
	pc, ok := p.conns[handle]
	if ok {
		delete(p.conns, handle)
	}
	p.mu.Unlock()
	if ok {
		pc.closeConn()
	}
}

// HasConnection reports whether a live (non-closed) connection exists for handle.
func (p *ConnectionPool) HasConnection(handle string) bool {
	p.mu.RLock()
	pc, ok := p.conns[handle]
	p.mu.RUnlock()
	return ok && !pc.closed.Load()
}

// readLoop runs the per-connection read goroutine, with automatic reconnect
// on transient failures.
func (p *ConnectionPool) readLoop(ctx context.Context, pc *pooledConn) {
	defer p.wg.Done()
	defer close(pc.done)

	for {
		// Ping config: refresh deadline on each pong.
		pc.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		pc.conn.SetPongHandler(func(string) error {
			pc.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})

		pingDone := make(chan struct{})
		go p.pingTicker(pc, pingDone)

		var readErr error
		for {
			var msg WebSocketMessage
			if err := pc.conn.ReadJSON(&msg); err != nil {
				readErr = err
				break
			}
			pc.attempts.Store(0) // successful read resets reconnect counter
			p.routeIncoming(pc, &msg)
		}
		close(pingDone)

		// Determine if reconnect is warranted.
		if ctx.Err() != nil || pc.closed.Load() {
			return
		}

		attempts := pc.attempts.Add(1)
		if int(attempts) > PoolMaxReconnectAttempts {
			p.logger.Warn(ctx, "wspool.reconnect_exhausted",
				observability.F("handle", pc.handle),
				observability.F("attempts", attempts),
				observability.F("last_error", readErr.Error()))
			pc.closeConn()
			p.mu.Lock()
			delete(p.conns, pc.handle)
			p.mu.Unlock()
			return
		}

		backoff := computeBackoff(int(attempts))
		p.logger.Info(ctx, "wspool.reconnecting",
			observability.F("handle", pc.handle),
			observability.F("attempt", attempts),
			observability.F("backoff_ms", backoff.Milliseconds()),
			observability.F("last_error", readErr.Error()))

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		}

		// Redial. Bail if the new dial fails — caller's next Send will retry.
		newConn, err := p.dial(ctx, pc.handle, pc.endpointURL)
		if err != nil {
			p.logger.Warn(ctx, "wspool.redial_failed",
				observability.F("handle", pc.handle),
				observability.F("error", err.Error()))
			continue // try again next iteration (counts as another attempt)
		}

		pc.writeMu.Lock()
		_ = pc.conn.Close()
		pc.conn = newConn
		pc.writeMu.Unlock()

		p.logger.Info(ctx, "wspool.reconnected",
			observability.F("handle", pc.handle),
			observability.F("attempt", attempts))
	}
}

// routeIncoming dispatches a received message to the correct destination.
func (p *ConnectionPool) routeIncoming(pc *pooledConn, msg *WebSocketMessage) {
	switch msg.Type {
	case "response", "error":
		// Hold the mutex through the send. The waiter channel is buffered (1),
		// so the send is non-blocking, and holding the mutex makes it safe
		// against a concurrent closeConn that drains+closes all waiters.
		pc.respWaitersMu.Lock()
		ch, ok := pc.respWaiters[msg.ID]
		if ok {
			select {
			case ch <- msg:
			default:
				// waiter buffer full; drop (shouldn't happen with buffered ch)
			}
		}
		pc.respWaitersMu.Unlock()
	case "request":
		// Peer is asking us to handle a request over the same socket.
		p.inboundHandlerMu.RLock()
		h := p.inboundHandler
		p.inboundHandlerMu.RUnlock()
		if h == nil {
			pc.sendError(msg.ID, -32000, "no inbound handler", "")
			return
		}
		go p.processInbound(pc, msg, h)
	case "pong":
		// noop — pong handler already refreshed deadline
	case "message", "status":
		// Informational message; observable via logger.
		p.logger.Debug(context.Background(), "wspool.message",
			observability.F("handle", pc.handle),
			observability.F("type", msg.Type))
	default:
		p.logger.Warn(context.Background(), "wspool.unknown_type",
			observability.F("handle", pc.handle),
			observability.F("type", msg.Type))
	}
}

// processInbound runs the inbound handler and writes the response back.
func (p *ConnectionPool) processInbound(pc *pooledConn, msg *WebSocketMessage, h RequestHandler) {
	ctx := context.Background()
	var req InboundRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		pc.sendError(msg.ID, -32700, "invalid request payload", err.Error())
		return
	}
	resp, err := h(ctx, &req)
	if err != nil {
		pc.sendError(msg.ID, -32603, "request processing failed", err.Error())
		return
	}
	payload, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		pc.sendError(msg.ID, -32603, "response marshal failed", marshalErr.Error())
		return
	}
	_ = pc.writeJSON(WebSocketMessage{
		Type:      "response",
		ID:        msg.ID,
		Payload:   payload,
		Timestamp: time.Now(),
	})
}

// pingTicker periodically pings the peer to keep the connection alive.
func (p *ConnectionPool) pingTicker(pc *pooledConn, done <-chan struct{}) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			pc.writeMu.Lock()
			err := pc.conn.WriteControl(
				websocket.PingMessage,
				nil,
				time.Now().Add(PoolWriteWait),
			)
			pc.writeMu.Unlock()
			if err != nil {
				return // the read loop will catch the broken connection
			}
		case <-done:
			return
		}
	}
}

// healthCheckLoop sweeps for stale connections.
func (p *ConnectionPool) healthCheckLoop() {
	defer p.wg.Done()
	t := time.NewTicker(PoolHealthCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			p.mu.Lock()
			for h, pc := range p.conns {
				if pc.closed.Load() {
					delete(p.conns, h)
				}
			}
			p.mu.Unlock()
		case <-p.stopCh:
			return
		}
	}
}

// pooledConn helpers

func (pc *pooledConn) writeJSON(msg WebSocketMessage) error {
	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()
	if err := pc.conn.SetWriteDeadline(time.Now().Add(PoolWriteWait)); err != nil {
		return err
	}
	return pc.conn.WriteJSON(msg)
}

func (pc *pooledConn) sendError(id string, code int, message, details string) {
	_ = pc.writeJSON(WebSocketMessage{
		Type:      "error",
		ID:        id,
		Error:     &WebSocketError{Code: code, Message: message, Details: details},
		Timestamp: time.Now(),
	})
}

func (pc *pooledConn) closeConn() {
	if !pc.closed.CompareAndSwap(false, true) {
		return
	}
	if pc.cancel != nil {
		pc.cancel()
	}
	pc.writeMu.Lock()
	_ = pc.conn.Close()
	pc.writeMu.Unlock()

	// Drain any pending response waiters.
	pc.respWaitersMu.Lock()
	for id, ch := range pc.respWaiters {
		close(ch)
		delete(pc.respWaiters, id)
	}
	pc.respWaitersMu.Unlock()
}

// computeBackoff returns exponential backoff capped at PoolMaxReconnectBackoff.
func computeBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return PoolInitialReconnectBackoff
	}
	d := PoolInitialReconnectBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= PoolMaxReconnectBackoff {
			return PoolMaxReconnectBackoff
		}
	}
	return d
}
