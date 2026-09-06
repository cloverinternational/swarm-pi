package a2a

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/gorilla/websocket"
)

const (
	// DefaultHubPort is the TCP port for cross-machine hub connections
	DefaultHubPort = 9900
	// HubPingInterval is the keepalive interval
	HubPingInterval = 30 * time.Second
	// HubPongTimeout is how long to wait for pong before disconnecting
	HubPongTimeout = 90 * time.Second
	// ReconnectDelay is base delay before retry
	ReconnectDelay = 2 * time.Second
)

// HubMessage types (pi-link style)
type HubMessageType string

const (
	HubMsgRegister       HubMessageType = "register"
	HubMsgWelcome        HubMessageType = "welcome"
	HubMsgTerminalJoined HubMessageType = "terminal_joined"
	HubMsgTerminalLeft   HubMessageType = "terminal_left"
	HubMsgChat           HubMessageType = "chat"
	HubMsgPromptRequest  HubMessageType = "prompt_request"
	HubMsgPromptResponse HubMessageType = "prompt_response"
	HubMsgStatusUpdate   HubMessageType = "status_update"
	HubMsgError          HubMessageType = "error"
)

// HubStatus represents agent state (pi-link style)
type HubStatus struct {
	Kind  string `json:"kind"`           // "idle", "thinking", "tool"
	Since int64  `json:"since"`          // Unix timestamp
	Tool  string `json:"tool,omitempty"` // Tool name if kind="tool"
	Task  string `json:"task,omitempty"` // Current task description
}

// HubMessage is the envelope for all hub messages
type HubMessage struct {
	Type      HubMessageType       `json:"type"`
	From      string               `json:"from,omitempty"`
	To        string               `json:"to,omitempty"`
	ID        string               `json:"id,omitempty"`
	Content   string               `json:"content,omitempty"`
	Prompt    string               `json:"prompt,omitempty"`
	Response  string               `json:"response,omitempty"`
	Error     string               `json:"error,omitempty"`
	Terminals []string             `json:"terminals,omitempty"`
	Status    HubStatus            `json:"status"`
	Statuses  map[string]HubStatus `json:"statuses,omitempty"`
	Cwd       string               `json:"cwd,omitempty"`
	Cwds      map[string]string    `json:"cwds,omitempty"`
	Timestamp int64                `json:"timestamp"`
}

// HubClient represents a connected peer (hub perspective)
type HubClient struct {
	Handle   string
	Conn     *websocket.Conn
	Cwd      string
	Status   HubStatus
	JoinedAt time.Time
	WriteMu  sync.Mutex
	mu       sync.RWMutex
	closed   bool
}

// WorkspaceHub manages peer connections for a single workspace
// Only one hub exists per workspace across all local binaries
type WorkspaceHub struct {
	workspace string
	handle    string // this instance's own handle
	endpoint  string // unix:/path or tcp://host:port

	// Hub state (in-memory only, like pi-link)
	mu         sync.RWMutex
	clients    map[*HubClient]struct{} // All connected clients
	localPeers map[string]*HubClient   // Hub-mode: handle → directly connected client (always non-nil)
	knownPeers map[string]struct{}     // Client-mode: handle → presence in upstream hub's peer set
	statuses   map[string]HubStatus    // Handle → status
	cwds       map[string]string       // Handle → working dir

	// connMu protects isClient, clientConn, server, listener, endpoint
	connMu  sync.Mutex
	selfCwd string // this instance's working directory, sent on connect

	// Server state
	server   *http.Server
	listener net.Listener
	upgrader websocket.Upgrader

	// Client state (when connecting as client)
	clientConn *websocket.Conn
	isClient   bool

	// Callbacks
	OnMessage    func(from, content string)
	OnPeerJoined func(handle string)
	OnPeerLeft   func(handle string)
	OnPrompt     func(from, prompt string) (string, error)

	logger   observability.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewWorkspaceHub creates a new hub instance (does not start it)
func NewWorkspaceHub(workspace string, logger observability.Logger) *WorkspaceHub {
	if logger == nil {
		logger = noop.NewLogger()
	}

	return &WorkspaceHub{
		workspace:  workspace,
		clients:    make(map[*HubClient]struct{}),
		localPeers: make(map[string]*HubClient),
		knownPeers: make(map[string]struct{}),
		statuses:   make(map[string]HubStatus),
		cwds:       make(map[string]string),
		logger:     logger,
		stopCh:     make(chan struct{}),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// Allow local connections, including Unix socket ("unix" or empty host)
				host := r.Host
				if h, _, err := net.SplitHostPort(host); err == nil {
					host = h
				}
				return host == "127.0.0.1" || host == "::1" || host == "localhost" || host == "unix" || host == ""
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
}

// SetCwd sets this instance's working directory, advertised to hub on connect.
// Call before StartOrConnect.
func (h *WorkspaceHub) SetCwd(cwd string) {
	h.connMu.Lock()
	h.selfCwd = cwd
	h.connMu.Unlock()
}

// SocketPath returns the Unix socket path for this workspace
func (h *WorkspaceHub) SocketPath() string {
	// Hash workspace for safe filename
	hash := sha256.Sum256([]byte(h.workspace))
	hashStr := hex.EncodeToString(hash[:8])
	return filepath.Join(os.TempDir(), fmt.Sprintf("swarm-%s.sock", hashStr))
}

// StartOrConnect implements pi-link style discovery:
// 1. Try to connect as client to existing hub (Unix socket first, then TCP)
// 2. If fails, become the hub
func (h *WorkspaceHub) StartOrConnect(ctx context.Context, handle string) error {
	h.connMu.Lock()
	defer h.connMu.Unlock()

	// Store this instance's own handle so hub-mode sends can use it
	h.handle = handle

	// Try Unix socket first (same machine, fastest)
	sockPath := h.SocketPath()
	if _, err := os.Stat(sockPath); err == nil {
		if err := h.connectClient(ctx, "unix", sockPath, handle); err == nil {
			h.endpoint = "unix:" + sockPath
			h.isClient = true
			h.logger.Info(ctx, "hub.connected_unix",
				observability.F("handle", handle),
				observability.F("socket", sockPath))
			return nil
		}
		// Socket exists but can't connect - might be stale
		os.Remove(sockPath)
	}

	// Try TCP port (cross-machine or fallback)
	if err := h.connectClient(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", DefaultHubPort), handle); err == nil {
		h.endpoint = fmt.Sprintf("tcp://127.0.0.1:%d", DefaultHubPort)
		h.isClient = true
		h.logger.Info(ctx, "hub.connected_tcp",
			observability.F("handle", handle),
			observability.F("port", DefaultHubPort))
		return nil
	}

	// Become the hub
	if err := h.startHub(ctx, sockPath); err != nil {
		return fmt.Errorf("failed to start hub: %w", err)
	}

	h.isClient = false
	h.endpoint = "unix:" + sockPath
	h.logger.Info(ctx, "hub.became_hub",
		observability.F("handle", handle),
		observability.F("socket", sockPath))

	return nil
}

// startHub starts listening as the hub on Unix socket
func (h *WorkspaceHub) startHub(ctx context.Context, sockPath string) error {
	// Remove stale socket if exists
	os.Remove(sockPath)

	// Create Unix socket listener
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		// Fallback to TCP
		listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", DefaultHubPort))
		if err != nil {
			return fmt.Errorf("failed to listen: %w", err)
		}
	}

	h.listener = listener

	// Set socket permissions
	if _, ok := listener.Addr().(*net.UnixAddr); ok {
		os.Chmod(sockPath, 0777)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWebSocket)

	h.server = &http.Server{
		Handler: mux,
	}

	go func() {
		if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			h.logger.Error(ctx, "hub_server_error",
				observability.F("error", err.Error()))
		}
	}()

	return nil
}

// connectClient connects as a client to an existing hub
func (h *WorkspaceHub) connectClient(ctx context.Context, network, address, handle string) error {
	var dialer websocket.Dialer

	var conn *websocket.Conn
	var err error

	// Include cwd in query so the hub knows our working directory at connect time
	cwdParam := ""
	if h.selfCwd != "" {
		cwdParam = "&cwd=" + h.selfCwd
	}

	if network == "unix" {
		// Custom dialer for Unix socket
		dialer.NetDial = func(network, addr string) (net.Conn, error) {
			return net.Dial("unix", address)
		}
		conn, _, err = dialer.Dial("ws://unix/ws?handle="+handle+cwdParam, nil)
	} else {
		conn, _, err = dialer.Dial(fmt.Sprintf("ws://%s/ws?handle=%s%s", address, handle, cwdParam), nil)
	}

	if err != nil {
		return err
	}

	h.clientConn = conn

	// Start client read loop
	go h.clientReadLoop(conn, handle)

	return nil
}

// Stop shuts down the hub or client connection
func (h *WorkspaceHub) Stop(ctx context.Context) error {
	h.stopOnce.Do(func() {
		close(h.stopCh)

		h.connMu.Lock()
		isClient := h.isClient
		clientConn := h.clientConn
		server := h.server
		h.connMu.Unlock()

		if isClient && clientConn != nil {
			clientConn.Close()
		}

		if !isClient && server != nil {
			// Close all client connections
			h.mu.Lock()
			for client := range h.clients {
				client.Close()
			}
			h.clients = make(map[*HubClient]struct{})
			h.mu.Unlock()

			server.Shutdown(ctx)

			// Clean up socket
			if sockPath := h.SocketPath(); sockPath != "" {
				os.Remove(sockPath)
			}
		}
	})

	return nil
}

// handleWebSocket handles incoming WebSocket connections (hub side)
func (h *WorkspaceHub) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract handle and optional cwd from query
	handle := r.URL.Query().Get("handle")
	if handle == "" {
		http.Error(w, "missing handle", http.StatusBadRequest)
		return
	}
	cwd := r.URL.Query().Get("cwd")

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error(ctx, "hub_upgrade_failed",
			observability.F("error", err.Error()),
			observability.F("handle", handle))
		return
	}

	client := &HubClient{
		Handle:   handle,
		Conn:     conn,
		Cwd:      cwd,
		JoinedAt: time.Now(),
		Status:   HubStatus{Kind: "idle", Since: time.Now().Unix()},
	}

	// Send welcome BEFORE registering so the new peer does not appear
	// in their own terminal list (only existing peers are listed).
	h.sendWelcome(client)

	// Register client (close any stale connection for this handle)
	h.mu.Lock()
	if existing, ok := h.localPeers[handle]; ok {
		existing.Close()
	}
	h.clients[client] = struct{}{}
	h.localPeers[handle] = client
	h.statuses[handle] = client.Status
	if cwd != "" {
		h.cwds[handle] = cwd
	}
	h.mu.Unlock()

	h.logger.Info(ctx, "hub_peer_connected",
		observability.F("handle", handle),
		observability.F("count", len(h.localPeers)))

	// Broadcast join to all peers (new peer is now registered, so
	// the terminals list will correctly include them)
	h.broadcastTerminalJoined(handle, cwd)
	// Also fire on hub-process itself (not in h.clients)
	if h.OnPeerJoined != nil {
		h.OnPeerJoined(handle)
	}

	// Start handling messages
	h.hubReadLoop(client)

	// Cleanup on disconnect
	h.mu.Lock()
	delete(h.clients, client)
	delete(h.localPeers, handle)
	delete(h.statuses, handle)
	delete(h.cwds, handle)
	h.mu.Unlock()

	h.logger.Info(ctx, "hub_peer_disconnected",
		observability.F("handle", handle),
		observability.F("count", len(h.localPeers)))

	// Broadcast leave to all peers
	h.broadcastTerminalLeft(handle)
	// Also fire on hub-process itself (not in h.clients)
	if h.OnPeerLeft != nil {
		h.OnPeerLeft(handle)
	}
}

// hubReadLoop handles messages from a connected client (hub perspective)
func (h *WorkspaceHub) hubReadLoop(client *HubClient) {
	defer client.Close()

	// Set read deadline and pong handler
	client.Conn.SetReadDeadline(time.Now().Add(HubPongTimeout))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(HubPongTimeout))
		return nil
	})

	// Start ping ticker
	pingTicker := time.NewTicker(HubPingInterval)
	defer pingTicker.Stop()

	// Ping loop
	go func() {
		for range pingTicker.C {
			if client.IsClosed() {
				return
			}
			client.WriteMu.Lock()
			err := client.Conn.WriteMessage(websocket.PingMessage, nil)
			client.WriteMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	// Read loop
	for {
		select {
		case <-h.stopCh:
			return
		default:
		}

		var msg HubMessage
		if err := client.Conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Error(context.Background(), "hub_read_error",
					observability.F("error", err.Error()),
					observability.F("handle", client.Handle))
			}
			return
		}

		client.mu.Lock()
		client.Conn.SetReadDeadline(time.Now().Add(HubPongTimeout))
		client.mu.Unlock()

		// Handle message
		h.handleHubMessage(client, msg)
	}
}

// clientReadLoop handles messages from the hub (client perspective)
func (h *WorkspaceHub) clientReadLoop(conn *websocket.Conn, handle string) {
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(HubPongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(HubPongTimeout))
		return nil
	})

	pingTicker := time.NewTicker(HubPingInterval)
	defer pingTicker.Stop()

	go func() {
		for range pingTicker.C {
			err := conn.WriteMessage(websocket.PingMessage, nil)
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-h.stopCh:
			return
		default:
		}

		var msg HubMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Error(context.Background(), "client_read_error",
					observability.F("error", err.Error()))
			}
			// Trigger reconnect
			go h.scheduleReconnect(handle)
			return
		}

		conn.SetReadDeadline(time.Now().Add(HubPongTimeout))
		h.handleClientMessage(msg)
	}
}

// scheduleReconnect attempts to reconnect after a delay (like pi-link)
func (h *WorkspaceHub) scheduleReconnect(handle string) {
	jitter := time.Duration(rand.Intn(3)) * time.Second
	select {
	case <-h.stopCh:
		// Hub is shutting down; do not reconnect
		return
	case <-time.After(ReconnectDelay + jitter):
	}

	ctx := context.Background()
	if err := h.StartOrConnect(ctx, handle); err != nil {
		h.logger.Error(ctx, "hub_reconnect_failed",
			observability.F("error", err.Error()),
			observability.F("handle", handle))
	}
}

// handleHubMessage processes a message from a client (hub side)
func (h *WorkspaceHub) handleHubMessage(client *HubClient, msg HubMessage) {
	switch msg.Type {
	case HubMsgRegister:
		// Client sent an explicit register message with its CWD
		if msg.Cwd != "" {
			h.mu.Lock()
			client.Cwd = msg.Cwd
			h.cwds[client.Handle] = msg.Cwd
			h.mu.Unlock()
		}

	case HubMsgStatusUpdate:
		h.mu.Lock()
		h.statuses[client.Handle] = msg.Status
		client.Status = msg.Status
		if msg.Cwd != "" {
			h.cwds[client.Handle] = msg.Cwd
			client.Cwd = msg.Cwd
		}
		h.mu.Unlock()

		// Broadcast status to all other clients
		h.broadcastStatusUpdate(client.Handle, msg.Status, msg.Cwd)

	case HubMsgChat:
		// Route to target or broadcast
		if msg.To == "" || msg.To == "*" {
			// Broadcast (exclude sender) — also deliver to hub process itself
			h.broadcastChat(client.Handle, msg.Content, client.Handle)
			if h.OnMessage != nil {
				h.OnMessage(client.Handle, msg.Content)
			}
		} else if msg.To == h.handle {
			// DM addressed to the hub process itself — deliver via callback
			if h.OnMessage != nil {
				h.OnMessage(client.Handle, msg.Content)
			}
		} else {
			// Direct message to another connected client
			h.sendToClient(msg.To, HubMessage{
				Type:    HubMsgChat,
				From:    client.Handle,
				Content: msg.Content,
			})
		}

	case HubMsgPromptRequest:
		if h.OnPrompt != nil {
			go func() {
				response, err := h.OnPrompt(client.Handle, msg.Prompt)
				errStr := ""
				if err != nil {
					errStr = err.Error()
				}
				h.sendToClient(client.Handle, HubMessage{
					Type:     HubMsgPromptResponse,
					To:       client.Handle,
					ID:       msg.ID,
					Response: response,
					Error:    errStr,
				})
			}()
		}

	case HubMsgPromptResponse:
		// Route to waiting client
		h.sendToClient(msg.To, msg)
	}
}

// handleClientMessage processes a message from the hub (client side)
func (h *WorkspaceHub) handleClientMessage(msg HubMessage) {
	switch msg.Type {
	case HubMsgWelcome:
		// Populate local state from welcome payload
		h.mu.Lock()
		for _, t := range msg.Terminals {
			h.knownPeers[t] = struct{}{}
		}
		maps.Copy(h.statuses, msg.Statuses)
		maps.Copy(h.cwds, msg.Cwds)
		h.mu.Unlock()

	case HubMsgTerminalJoined:
		h.mu.Lock()
		h.knownPeers[msg.From] = struct{}{}
		if msg.Cwd != "" {
			h.cwds[msg.From] = msg.Cwd
		}
		h.mu.Unlock()
		if h.OnPeerJoined != nil {
			h.OnPeerJoined(msg.From)
		}

	case HubMsgTerminalLeft:
		h.mu.Lock()
		delete(h.knownPeers, msg.From)
		delete(h.statuses, msg.From)
		delete(h.cwds, msg.From)
		h.mu.Unlock()
		if h.OnPeerLeft != nil {
			h.OnPeerLeft(msg.From)
		}

	case HubMsgChat:
		if h.OnMessage != nil {
			h.OnMessage(msg.From, msg.Content)
		}

	case HubMsgStatusUpdate:
		h.mu.Lock()
		h.statuses[msg.From] = msg.Status
		if msg.Cwd != "" {
			h.cwds[msg.From] = msg.Cwd
		}
		h.mu.Unlock()
	}
}

// Broadcasting methods

func (h *WorkspaceHub) sendWelcome(client *HubClient) {
	h.mu.RLock()
	terminals := make([]string, 0, len(h.localPeers))
	statuses := make(map[string]HubStatus)
	cwds := make(map[string]string)

	for handle, c := range h.localPeers {
		terminals = append(terminals, handle)
		statuses[handle] = c.Status
		if c.Cwd != "" {
			cwds[handle] = c.Cwd
		}
	}
	h.mu.RUnlock()

	msg := HubMessage{
		Type:      HubMsgWelcome,
		From:      "hub",
		To:        client.Handle,
		Terminals: terminals,
		Statuses:  statuses,
		Cwds:      cwds,
		Timestamp: time.Now().Unix(),
	}

	client.WriteMu.Lock()
	client.Conn.WriteJSON(msg)
	client.WriteMu.Unlock()
}

func (h *WorkspaceHub) broadcastTerminalJoined(handle, cwd string) {
	h.mu.RLock()
	terminals := make([]string, 0, len(h.localPeers))
	for peer := range h.localPeers {
		terminals = append(terminals, peer)
	}
	h.mu.RUnlock()

	msg := HubMessage{
		Type:      HubMsgTerminalJoined,
		From:      handle,
		Terminals: terminals,
		Cwd:       cwd,
		Timestamp: time.Now().Unix(),
	}

	h.broadcast(msg, handle)
}

func (h *WorkspaceHub) broadcastTerminalLeft(handle string) {
	h.mu.RLock()
	terminals := make([]string, 0, len(h.localPeers))
	for peer := range h.localPeers {
		terminals = append(terminals, peer)
	}
	h.mu.RUnlock()

	msg := HubMessage{
		Type:      HubMsgTerminalLeft,
		From:      handle,
		Terminals: terminals,
		Timestamp: time.Now().Unix(),
	}

	h.broadcast(msg, "")
}

func (h *WorkspaceHub) broadcastStatusUpdate(handle string, status HubStatus, cwd string) {
	msg := HubMessage{
		Type:      HubMsgStatusUpdate,
		From:      handle,
		Status:    status,
		Cwd:       cwd,
		Timestamp: time.Now().Unix(),
	}
	h.broadcast(msg, "")
}

func (h *WorkspaceHub) broadcastChat(from, content, exclude string) {
	msg := HubMessage{
		Type:      HubMsgChat,
		From:      from,
		Content:   content,
		Timestamp: time.Now().Unix(),
	}
	h.broadcast(msg, exclude)
}

func (h *WorkspaceHub) broadcast(msg HubMessage, exclude string) {
	h.mu.RLock()
	clients := make([]*HubClient, 0, len(h.clients))
	for client := range h.clients {
		if client.Handle != exclude {
			clients = append(clients, client)
		}
	}
	h.mu.RUnlock()

	for _, client := range clients {
		if client.IsClosed() {
			continue
		}
		client.WriteMu.Lock()
		if !client.IsClosed() { // double-check under write lock
			client.Conn.WriteJSON(msg)
		}
		client.WriteMu.Unlock()
	}
}

func (h *WorkspaceHub) sendToClient(handle string, msg HubMessage) {
	h.mu.RLock()
	client, ok := h.localPeers[handle]
	h.mu.RUnlock()

	if !ok {
		return
	}

	client.WriteMu.Lock()
	client.Conn.WriteJSON(msg)
	client.WriteMu.Unlock()
}

// Client methods

// SendChat sends a chat message. Works in both client and hub mode.
func (h *WorkspaceHub) SendChat(to, content string) error {
	h.connMu.Lock()
	isClient := h.isClient
	clientConn := h.clientConn
	selfHandle := h.handle
	h.connMu.Unlock()

	if isClient {
		if clientConn == nil {
			return fmt.Errorf("not connected to hub")
		}
		msg := HubMessage{
			Type:    HubMsgChat,
			To:      to,
			Content: content,
		}
		return clientConn.WriteJSON(msg)
	}

	// Hub mode: deliver directly to connected clients
	if to == "" || to == "*" {
		h.broadcastChat(selfHandle, content, "")
		// Also deliver to hub-process itself (not in h.clients)
		if h.OnMessage != nil {
			h.OnMessage(selfHandle, content)
		}
	} else if to == selfHandle {
		// DM to hub's own handle — deliver via callback (not in h.handles)
		if h.OnMessage != nil {
			h.OnMessage(selfHandle, content)
		}
	} else {
		h.sendToClient(to, HubMessage{
			Type:    HubMsgChat,
			From:    selfHandle,
			Content: content,
		})
	}
	return nil
}

// SendStatusUpdate sends status update. Works in both client and hub mode.
func (h *WorkspaceHub) SendStatusUpdate(status HubStatus, cwd string) error {
	h.connMu.Lock()
	isClient := h.isClient
	clientConn := h.clientConn
	selfHandle := h.handle
	h.connMu.Unlock()

	if isClient {
		if clientConn == nil {
			return fmt.Errorf("not connected to hub")
		}
		// Mirror own status locally so GetPeerStatus is immediately consistent
		h.mu.Lock()
		h.statuses[selfHandle] = status
		if cwd != "" {
			h.cwds[selfHandle] = cwd
		}
		h.mu.Unlock()
		msg := HubMessage{
			Type:   HubMsgStatusUpdate,
			Status: status,
			Cwd:    cwd,
		}
		return clientConn.WriteJSON(msg)
	}

	// Hub mode: update own status and broadcast to all clients
	h.mu.Lock()
	h.statuses[selfHandle] = status
	if cwd != "" {
		h.cwds[selfHandle] = cwd
	}
	h.mu.Unlock()
	h.broadcastStatusUpdate(selfHandle, status, cwd)
	return nil
}

// GetPeers returns list of connected peers
func (h *WorkspaceHub) GetPeers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	peers := make([]string, 0, len(h.localPeers)+len(h.knownPeers))
	for handle := range h.localPeers {
		peers = append(peers, handle)
	}
	for handle := range h.knownPeers {
		peers = append(peers, handle)
	}
	return peers
}

// GetPeerStatus returns a peer's status
func (h *WorkspaceHub) GetPeerStatus(handle string) (HubStatus, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	status, ok := h.statuses[handle]
	return status, ok
}

// IsHub returns true if this instance is the hub
func (h *WorkspaceHub) IsHub() bool {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	return !h.isClient
}

// IsConnected returns true if connected to hub
func (h *WorkspaceHub) IsConnected() bool {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.isClient {
		return h.clientConn != nil
	}
	return h.server != nil
}

// Close closes the client connection
func (c *HubClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		c.closed = true
		c.Conn.Close()
	}
}

// IsClosed returns true if the connection is closed
func (c *HubClient) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// WriteJSON sends a JSON message to the client
func (c *HubClient) WriteJSON(v any) error {
	c.WriteMu.Lock()
	defer c.WriteMu.Unlock()
	return c.Conn.WriteJSON(v)
}
