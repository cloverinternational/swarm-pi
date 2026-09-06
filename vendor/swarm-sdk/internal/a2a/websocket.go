package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	// WebSocketPath is the WebSocket endpoint path for A2A connections
	WebSocketPath = "/ws"
	// WebSocketSubprotocol is the negotiated subprotocol for A2A
	WebSocketSubprotocol = "a2a-protocol-v1"
)

// WebSocketMessage wraps A2A messages for WebSocket transport
type WebSocketMessage struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     *WebSocketError `json:"error,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// WebSocketError represents an error in WebSocket transport
type WebSocketError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// WebSocketServer manages WebSocket connections for A2A peers
type WebSocketServer struct {
	mu       sync.RWMutex
	upgrader websocket.Upgrader
	listener net.Listener
	server   *http.Server

	// Connections keyed by peer handle
	connections map[string]*PeerConnection

	// Message handler for incoming requests
	requestHandler RequestHandler

	// sendMessageDelegate, when set, handles "SendMessage" method requests with
	// a protojson-encoded a2apb.SendMessageRequest payload. This is the unified
	// path matching the HTTP JSON-RPC transport.
	sendMessageDelegate func(ctx context.Context, req *a2apb.SendMessageRequest) (*a2apb.SendMessageResponse, *a2aError)

	logger observability.Logger

	// Diagnostic counters — atomic, surfaced via Stats() and the debug
	// control-socket payload. We add these because when an inbound DM
	// silently goes nowhere, "agent stayed idle" is not enough to tell
	// where in the WS pipeline the message was dropped.
	upgradeAttempts        atomic.Int64
	upgradeSuccess         atomic.Int64
	upgradeFailures        atomic.Int64
	messagesReceived       atomic.Int64 // ReadJSON succeeded
	messagesDispatched     atomic.Int64 // handleIncomingMessage entered
	requestMethodCount     atomic.Int64 // msg.Type == "request"
	sendMessageInvocations atomic.Int64 // sendMessageDelegate invoked
	sendMessageErrors      atomic.Int64 // delegate returned a2aError
	unknownTypeCount       atomic.Int64 // msg.Type not matched
	lastUpgradeErrorMu     sync.Mutex
	lastUpgradeError       string
	lastReceiveErrorMu     sync.Mutex
	lastReceiveError       string
}

// WebSocketServerStats is a read-only snapshot of the WS server's internal
// counters. Used for debug introspection — see Runtime.Debug().
type WebSocketServerStats struct {
	UpgradeAttempts        int64
	UpgradeSuccess         int64
	UpgradeFailures        int64
	MessagesReceived       int64
	MessagesDispatched     int64
	RequestMethodCount     int64
	SendMessageInvocations int64
	SendMessageErrors      int64
	UnknownTypeCount       int64
	LastUpgradeError       string
	LastReceiveError       string
	ActiveConnections      int
	HasDelegate            bool
	HasRequestHandler      bool
}

// Stats returns a snapshot of the WebSocket server's diagnostic counters.
// Safe to call concurrently with any other server operation.
func (ws *WebSocketServer) Stats() WebSocketServerStats {
	if ws == nil {
		return WebSocketServerStats{}
	}
	ws.mu.RLock()
	connCount := len(ws.connections)
	hasDelegate := ws.sendMessageDelegate != nil
	hasHandler := ws.requestHandler != nil
	ws.mu.RUnlock()

	ws.lastUpgradeErrorMu.Lock()
	lastUpErr := ws.lastUpgradeError
	ws.lastUpgradeErrorMu.Unlock()

	ws.lastReceiveErrorMu.Lock()
	lastRxErr := ws.lastReceiveError
	ws.lastReceiveErrorMu.Unlock()

	return WebSocketServerStats{
		UpgradeAttempts:        ws.upgradeAttempts.Load(),
		UpgradeSuccess:         ws.upgradeSuccess.Load(),
		UpgradeFailures:        ws.upgradeFailures.Load(),
		MessagesReceived:       ws.messagesReceived.Load(),
		MessagesDispatched:     ws.messagesDispatched.Load(),
		RequestMethodCount:     ws.requestMethodCount.Load(),
		SendMessageInvocations: ws.sendMessageInvocations.Load(),
		SendMessageErrors:      ws.sendMessageErrors.Load(),
		UnknownTypeCount:       ws.unknownTypeCount.Load(),
		LastUpgradeError:       lastUpErr,
		LastReceiveError:       lastRxErr,
		ActiveConnections:      connCount,
		HasDelegate:            hasDelegate,
		HasRequestHandler:      hasHandler,
	}
}

// PeerConnection represents a single peer's WebSocket connection
type PeerConnection struct {
	Handle     string
	Conn       *websocket.Conn
	WriteMu    sync.Mutex
	LastSeenAt time.Time
	Pending    chan *conversation.Message
	mu         sync.RWMutex
	closed     bool
}

// WebSocketClient handles outbound WebSocket connections
type WebSocketClient struct {
	dialer *websocket.Dialer
	mu     sync.RWMutex
	peers  map[string]*PeerConnection // cached connections
	logger observability.Logger
}

// NewWebSocketServer creates a new WebSocket server for A2A
func NewWebSocketServer(logger observability.Logger) *WebSocketServer {
	if logger == nil {
		logger = noop.NewLogger()
	}

	return &WebSocketServer{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// Allow loopback connections only
				host := r.Host
				if h, _, err := net.SplitHostPort(host); err == nil {
					host = h
				}
				return host == "127.0.0.1" || host == "::1" || host == "localhost"
			},
			Subprotocols:    []string{WebSocketSubprotocol},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		connections: make(map[string]*PeerConnection),
		logger:      logger,
	}
}

// RegisterOnMux registers the WebSocket upgrade handler on an existing HTTP mux.
// This avoids a separate listener and the port conflict that Start() causes when
// the HTTP server is already bound to the same address.
func (ws *WebSocketServer) RegisterOnMux(mux *http.ServeMux) {
	mux.HandleFunc(WebSocketPath, ws.handleWebSocket)
}

// Start begins listening for WebSocket connections on a NEW listener.
// Prefer RegisterOnMux when an HTTP server already exists on the same address.
func (ws *WebSocketServer) Start(listenAddress string) (string, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.server != nil {
		return "", fmt.Errorf("WebSocket server already started")
	}

	if listenAddress == "" {
		listenAddress = "127.0.0.1:0"
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return "", fmt.Errorf("failed to listen: %w", err)
	}

	ws.listener = listener

	mux := http.NewServeMux()
	mux.HandleFunc(WebSocketPath, ws.handleWebSocket)

	ws.server = &http.Server{
		Handler: mux,
	}

	go func() {
		if err := ws.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			ws.logger.Error(context.Background(), "websocket_server_error",
				observability.F("error", err.Error()))
		}
	}()

	actualAddr := listener.Addr().String()
	return actualAddr, nil
}

// Stop shuts down the WebSocket server
func (ws *WebSocketServer) Stop(ctx context.Context) error {
	ws.mu.Lock()

	// Close all peer connections
	for handle, conn := range ws.connections {
		conn.Close()
		delete(ws.connections, handle)
	}

	server := ws.server
	ws.server = nil
	ws.mu.Unlock()

	if server != nil {
		return server.Shutdown(ctx)
	}
	return nil
}

// handleWebSocket handles incoming WebSocket upgrade requests
func (ws *WebSocketServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract peer handle from query or header
	handle := r.URL.Query().Get("handle")
	if handle == "" {
		handle = r.Header.Get("X-A2A-Handle")
	}

	if handle == "" {
		http.Error(w, "missing peer handle", http.StatusBadRequest)
		return
	}

	// Validate version
	version := r.Header.Get("A2A-Version")
	if version != "" && version != ProtocolVersion {
		http.Error(w, "unsupported A2A version", http.StatusBadRequest)
		return
	}

	conn, err := ws.upgrader.Upgrade(w, r, nil)
	ws.upgradeAttempts.Add(1)
	if err != nil {
		ws.upgradeFailures.Add(1)
		ws.lastUpgradeErrorMu.Lock()
		ws.lastUpgradeError = err.Error()
		ws.lastUpgradeErrorMu.Unlock()
		ws.logger.Error(r.Context(), "websocket_upgrade_failed",
			observability.F("error", err.Error()),
			observability.F("handle", handle))
		return
	}
	ws.upgradeSuccess.Add(1)

	peerConn := &PeerConnection{
		Handle:     handle,
		Conn:       conn,
		LastSeenAt: time.Now(),
		Pending:    make(chan *conversation.Message, 100),
	}

	ws.mu.Lock()
	// Close existing connection if any
	if existing, ok := ws.connections[handle]; ok {
		existing.Close()
	}
	ws.connections[handle] = peerConn
	ws.mu.Unlock()

	ws.logger.Info(r.Context(), "websocket_peer_connected",
		observability.F("handle", handle),
		observability.F("remote", conn.RemoteAddr().String()))

	// Start handling messages
	go ws.handlePeerConnection(peerConn)
}

// handlePeerConnection manages the lifecycle of a peer connection
func (ws *WebSocketServer) handlePeerConnection(pc *PeerConnection) {
	defer func() {
		pc.Close()
		ws.mu.Lock()
		delete(ws.connections, pc.Handle)
		ws.mu.Unlock()
		ws.logger.Info(context.Background(), "websocket_peer_disconnected",
			observability.F("handle", pc.Handle))
	}()

	// Set read deadline and pong handler
	pc.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	pc.Conn.SetPongHandler(func(string) error {
		pc.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Start ping ticker
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// Handle outgoing messages
	go func() {
		for msg := range pc.Pending {
			if pc.IsClosed() {
				return
			}

			payload, err := json.Marshal(msg)
			if err != nil {
				ws.logger.Error(context.Background(), "websocket_marshal_error",
					observability.F("error", err.Error()))
				continue
			}

			wsMsg := WebSocketMessage{
				Type:      "message",
				Payload:   payload,
				Timestamp: time.Now(),
			}

			if err := pc.WriteJSON(wsMsg); err != nil {
				ws.logger.Error(context.Background(), "websocket_write_error",
					observability.F("error", err.Error()))
				return
			}
		}
	}()

	// Main read loop
	for {
		select {
		case <-pingTicker.C:
			if err := pc.WritePing(); err != nil {
				return
			}
		default:
			var msg WebSocketMessage
			if err := pc.Conn.ReadJSON(&msg); err != nil {
				ws.lastReceiveErrorMu.Lock()
				ws.lastReceiveError = err.Error()
				ws.lastReceiveErrorMu.Unlock()
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					ws.logger.Error(context.Background(), "websocket_read_error",
						observability.F("error", err.Error()),
						observability.F("handle", pc.Handle))
				}
				return
			}
			ws.messagesReceived.Add(1)

			pc.mu.Lock()
			pc.LastSeenAt = time.Now()
			pc.mu.Unlock()
			ws.handleIncomingMessage(pc, &msg)
		}
	}
}

// handleIncomingMessage processes messages from peers
func (ws *WebSocketServer) handleIncomingMessage(pc *PeerConnection, msg *WebSocketMessage) {
	ctx := context.Background()
	ws.messagesDispatched.Add(1)

	switch msg.Type {
	case "request":
		ws.requestMethodCount.Add(1)
		// Route based on method. The "SendMessage" method uses a protojson-encoded
		// a2apb.SendMessageRequest payload (unified with HTTP JSON-RPC). Other
		// methods fall back to the legacy InboundRequest JSON shape.
		ws.mu.RLock()
		delegate := ws.sendMessageDelegate
		handler := ws.requestHandler
		ws.mu.RUnlock()

		if msg.Method == "SendMessage" && delegate != nil {
			var req a2apb.SendMessageRequest
			if err := protojson.Unmarshal(msg.Payload, &req); err != nil {
				pc.sendError(msg.ID, -32700, "invalid SendMessage payload", err.Error())
				return
			}
			ws.sendMessageInvocations.Add(1)
			go func() {
				resp, rpcErr := delegate(ctx, &req)
				if rpcErr != nil {
					ws.sendMessageErrors.Add(1)
					pc.sendError(msg.ID, rpcErr.Code, rpcErr.Message, "")
					return
				}
				payload, marshalErr := protojson.Marshal(resp)
				if marshalErr != nil {
					pc.sendError(msg.ID, -32603, "response marshal failed", marshalErr.Error())
					return
				}
				_ = pc.WriteJSON(WebSocketMessage{
					Type:      "response",
					ID:        msg.ID,
					Payload:   payload,
					Timestamp: time.Now(),
				})
			}()
			return
		}

		if handler == nil {
			pc.sendError(msg.ID, -32000, "no request handler configured", "")
			return
		}

		var req InboundRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			pc.sendError(msg.ID, -32700, "invalid request payload", err.Error())
			return
		}

		// Process request asynchronously
		go func() {
			resp, err := handler(ctx, &req)
			if err != nil {
				pc.sendError(msg.ID, -32603, "request processing failed", err.Error())
				return
			}

			payload, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				pc.sendError(msg.ID, -32603, "failed to marshal response", marshalErr.Error())
				return
			}
			pc.WriteJSON(WebSocketMessage{
				Type:      "response",
				ID:        msg.ID,
				Payload:   payload,
				Timestamp: time.Now(),
			})
		}()

	case "ping":
		// Respond with pong
		pc.WriteJSON(WebSocketMessage{
			Type:      "pong",
			ID:        msg.ID,
			Timestamp: time.Now(),
		})

	case "status":
		// Status update from peer
		ws.logger.Debug(ctx, "websocket_status_update",
			observability.F("handle", pc.Handle),
			observability.F("status", string(msg.Payload)))

	default:
		ws.unknownTypeCount.Add(1)
		ws.logger.Warn(ctx, "websocket_unknown_message_type",
			observability.F("type", msg.Type),
			observability.F("handle", pc.Handle))
	}
}

// SetRequestHandler sets the handler for incoming requests
func (ws *WebSocketServer) SetRequestHandler(handler RequestHandler) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.requestHandler = handler
}

// SetSendMessageDelegate installs a handler for "SendMessage" method requests
// whose payload is a protojson-encoded a2apb.SendMessageRequest. This unifies
// the WebSocket transport with the HTTP JSON-RPC server (both call
// Runtime.handleSendMessage under the hood).
func (ws *WebSocketServer) SetSendMessageDelegate(fn func(ctx context.Context, req *a2apb.SendMessageRequest) (*a2apb.SendMessageResponse, *a2aError)) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.sendMessageDelegate = fn
}

// GetConnection returns an existing connection to a peer
func (ws *WebSocketServer) GetConnection(handle string) *PeerConnection {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.connections[handle]
}

// SendMessage sends a message to a connected peer
func (ws *WebSocketServer) SendMessage(handle string, msg *conversation.Message) error {
	ws.mu.RLock()
	conn, ok := ws.connections[handle]
	ws.mu.RUnlock()

	if !ok {
		return fmt.Errorf("peer not connected: %s", handle)
	}

	if conn.IsClosed() {
		return fmt.Errorf("peer connection closed: %s", handle)
	}

	select {
	case conn.Pending <- msg:
		return nil
	default:
		return fmt.Errorf("peer message queue full: %s", handle)
	}
}

// HasConnection checks if a peer is connected
func (ws *WebSocketServer) HasConnection(handle string) bool {
	ws.mu.RLock()
	conn, ok := ws.connections[handle]
	ws.mu.RUnlock()
	return ok && !conn.IsClosed()
}

// PeerConnection methods

func (pc *PeerConnection) WriteJSON(v any) error {
	pc.WriteMu.Lock()
	defer pc.WriteMu.Unlock()
	return pc.Conn.WriteJSON(v)
}

func (pc *PeerConnection) WritePing() error {
	pc.WriteMu.Lock()
	defer pc.WriteMu.Unlock()
	return pc.Conn.WriteMessage(websocket.PingMessage, nil)
}

func (pc *PeerConnection) sendError(id string, code int, message, details string) {
	pc.WriteJSON(WebSocketMessage{
		Type:      "error",
		ID:        id,
		Error:     &WebSocketError{Code: code, Message: message, Details: details},
		Timestamp: time.Now(),
	})
}

func (pc *PeerConnection) IsClosed() bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.closed
}

func (pc *PeerConnection) Close() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if !pc.closed {
		pc.closed = true
		close(pc.Pending)
		pc.Conn.Close()
	}
}

// WebSocketClient methods

// NewWebSocketClient creates a new WebSocket client
func NewWebSocketClient(logger observability.Logger) *WebSocketClient {
	if logger == nil {
		logger = noop.NewLogger()
	}

	return &WebSocketClient{
		dialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
			Subprotocols:     []string{WebSocketSubprotocol},
			ReadBufferSize:   1024,
			WriteBufferSize:  1024,
		},
		peers:  make(map[string]*PeerConnection),
		logger: logger,
	}
}

// Connect establishes a WebSocket connection to a peer
func (wc *WebSocketClient) Connect(ctx context.Context, handle, endpointURL string) (*PeerConnection, error) {
	// Check if already connected
	wc.mu.RLock()
	if existing, ok := wc.peers[handle]; ok && !existing.IsClosed() {
		wc.mu.RUnlock()
		return existing, nil
	}
	wc.mu.RUnlock()

	// Convert HTTP URL to WebSocket URL
	wsURL := convertToWebSocketURL(endpointURL)

	headers := http.Header{
		"X-A2A-Handle": []string{handle},
		"A2A-Version":  []string{ProtocolVersion},
	}

	conn, _, err := wc.dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", wsURL, err)
	}

	peerConn := &PeerConnection{
		Handle:     handle,
		Conn:       conn,
		LastSeenAt: time.Now(),
		Pending:    make(chan *conversation.Message, 100),
	}

	wc.mu.Lock()
	// Close existing if any
	if existing, ok := wc.peers[handle]; ok {
		existing.Close()
	}
	wc.peers[handle] = peerConn
	wc.mu.Unlock()

	wc.logger.Info(ctx, "websocket_client_connected",
		observability.F("handle", handle),
		observability.F("url", wsURL))

	// Start read loop
	go wc.handleClientConnection(peerConn)

	return peerConn, nil
}

// handleClientConnection manages a client-side connection
func (wc *WebSocketClient) handleClientConnection(pc *PeerConnection) {
	defer func() {
		pc.Close()
		wc.mu.Lock()
		delete(wc.peers, pc.Handle)
		wc.mu.Unlock()
		wc.logger.Info(context.Background(), "websocket_client_disconnected",
			observability.F("handle", pc.Handle))
	}()

	pc.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	pc.Conn.SetPongHandler(func(string) error {
		pc.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-pingTicker.C:
			if err := pc.WritePing(); err != nil {
				return
			}
		case msg := <-pc.Pending:
			if pc.IsClosed() {
				return
			}
			payload, _ := json.Marshal(msg)
			if err := pc.WriteJSON(WebSocketMessage{
				Type:      "message",
				Payload:   payload,
				Timestamp: time.Now(),
			}); err != nil {
				wc.logger.Error(context.Background(), "websocket_client_write_error",
					observability.F("error", err.Error()))
				return
			}
		default:
			var msg WebSocketMessage
			if err := pc.Conn.ReadJSON(&msg); err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					wc.logger.Error(context.Background(), "websocket_client_read_error",
						observability.F("error", err.Error()))
				}
				return
			}
			pc.mu.Lock()
			pc.LastSeenAt = time.Now()
			pc.mu.Unlock()

			// Handle incoming messages (responses, status updates)
			switch msg.Type {
			case "pong":
				// Pong received, connection alive
			case "response":
				wc.logger.Debug(context.Background(), "websocket_response_received",
					observability.F("handle", pc.Handle))
			case "message":
				wc.logger.Debug(context.Background(), "websocket_message_received",
					observability.F("handle", pc.Handle))
			}
		}
	}
}

// SendRequest sends a request to a peer and waits for response
func (wc *WebSocketClient) SendRequest(ctx context.Context, handle string, req *InboundRequest, timeout time.Duration) (*conversation.Message, error) {
	wc.mu.RLock()
	conn, ok := wc.peers[handle]
	wc.mu.RUnlock()

	if !ok || conn.IsClosed() {
		return nil, fmt.Errorf("peer not connected: %s", handle)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	msg := WebSocketMessage{
		Type:      "request",
		ID:        generateMessageID(),
		Method:    "SendMessage",
		Payload:   payload,
		Timestamp: time.Now(),
	}

	if err := conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Wait for response with timeout. ctx is intentionally discarded until the
	// response registry below is implemented; cancel still releases the timer.
	_, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// This is a simplified response handling - in production, use a response registry
	// For now, return a placeholder
	return &conversation.Message{}, nil
}

// Disconnect closes a peer connection
func (wc *WebSocketClient) Disconnect(handle string) error {
	wc.mu.Lock()
	conn, ok := wc.peers[handle]
	delete(wc.peers, handle)
	wc.mu.Unlock()

	if ok {
		conn.Close()
	}
	return nil
}

// Helper functions

func convertToWebSocketURL(httpURL string) string {
	// Strip trailing path from string-replacement fallback inputs so we always
	// land on the dedicated /ws upgrade endpoint regardless of what path the
	// peer's agent-card advertised (e.g. ".../rpc").
	stripPath := func(hostAndPath string) string {
		if before, _, ok := strings.Cut(hostAndPath, "/"); ok {
			return before
		}
		return hostAndPath
	}

	u, err := url.Parse(httpURL)
	if err != nil {
		// Fallback: string replacement. Strip any path so we don't append
		// /ws on top of an existing /rpc.
		if after, ok := strings.CutPrefix(httpURL, "http://"); ok {
			return "ws://" + stripPath(after) + WebSocketPath
		}
		if after, ok := strings.CutPrefix(httpURL, "https://"); ok {
			return "wss://" + stripPath(after) + WebSocketPath
		}
		return "ws://" + stripPath(httpURL) + WebSocketPath
	}

	// Switch scheme
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	}

	// Always force the WS upgrade path. Peer endpoints advertise the JSON-RPC
	// path (typically "/rpc") in their agent card; the WebSocket handler is
	// registered separately at WebSocketPath ("/ws") on the same mux, so we
	// must overwrite whatever path the card carried.
	u.Path = WebSocketPath
	// Clear any query/fragment from the RPC URL — the WS dialer adds its own
	// (handle, headers) and we don't want to leak RPC-only params upstream.
	u.RawQuery = ""
	u.Fragment = ""

	return u.String()
}

func generateMessageID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// Ensure gorilla/websocket is imported
// Add to go.mod if needed: github.com/gorilla/websocket v1.5.3
