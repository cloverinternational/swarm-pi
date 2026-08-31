package a2a

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Runtime coordinates per-session A2A discovery, transport, client calls, and conversation projection.
type Runtime struct {
	backend        Backend
	hooks          HookEmitter
	logger         observability.Logger
	tracer         observability.Tracer
	client         *Client
	peer           PeerIdentity
	card           *a2apb.AgentCard
	rpcPath        string
	ttlSeconds     int
	listenAddress  string
	baseURL        string
	projectionSink ProjectionSink
	swarmName      string // NEW: swarm name for filesystem-based discovery

	mux      *http.ServeMux
	server   *http.Server
	listener net.Listener

	requestHandler RequestHandler

	// inboundMsgCb is called when an inbound A2A message arrives from a peer.
	// It runs before the requestHandler so the TUI (or any host) can allocate
	// a conversation and wire the source context before the agent executes.
	// Set via SetInboundMessageCallback.
	inboundMsgCb InboundMessageCallback

	stopOnce  sync.Once
	startOnce sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup

	mu                    sync.Mutex
	pending               []*conversation.Message
	currentConversationID string
	taskWatchers          map[string]map[chan *a2apb.StreamResponse]struct{}
	taskCancels           map[string]context.CancelFunc

	// Swarm status tracking
	swarmStatus     AgentSwarmStatus
	swarmCallback   SwarmCallback
	swarmEventQueue chan SwarmEvent

	// WebSocket transport for real-time peer communication
	wsServer *WebSocketServer
	wsClient *WebSocketClient
	wsPool   *ConnectionPool

	// peerMutes tracks Phase 3 halt_peer_loop effects. Inbound DMs from
	// muted peers are dropped (returned as completed tasks) before the
	// request handler runs.
	peerMutes *PeerMuteStore
}

// NewRuntime constructs a phase-1 A2A runtime.
func NewRuntime(cfg Config, backend Backend, logger observability.Logger, tracer observability.Tracer, sink ProjectionSink) (*Runtime, error) {
	if logger == nil {
		logger = noop.NewLogger()
	}
	if tracer == nil {
		tracer = noop.NewTracer()
	}
	handle := NormalizeHandle(cfg.Handle)
	if handle == "" {
		return nil, fmt.Errorf("a2a handle is required")
	}
	scopeKey := NormalizeScopeKey(cfg.ProjectID, cfg.WorkspacePath)
	if scopeKey == "" {
		return nil, fmt.Errorf("a2a workspace/project scope is required")
	}
	workspacePath := cfg.WorkspacePath
	if workspacePath != "" {
		if abs, err := filepath.Abs(workspacePath); err == nil {
			workspacePath = filepath.Clean(abs)
		}
	}
	if backend == nil {
		// Always use global registry - all peers discover each other
		// via single shared registry. Workspace separation via scope_key.
		registryPath := GlobalRegistryPath()
		var err error
		backend, err = NewSQLiteBackend(SQLiteConfig{Path: registryPath})
		if err != nil {
			return nil, err
		}
	}

	ttl := cfg.PresenceTTL
	if ttl <= 0 {
		ttl = 15 * time.Second
	}

	return &Runtime{
		backend:        backend,
		hooks:          cfg.Hooks,
		logger:         logger,
		tracer:         tracer,
		client:         NewClient(nil),
		ttlSeconds:     int(ttl / time.Second),
		projectionSink: sink,
		rpcPath:        defaultRPCPath(cfg.RPCPath),
		listenAddress:  strings.TrimSpace(cfg.ListenAddress),
		baseURL:        strings.TrimSpace(cfg.BaseURL),
		swarmName:      cfg.SwarmName,
		peer: PeerIdentity{
			Handle:         handle,
			SessionID:      defaultSessionID(cfg.SessionID),
			ConversationID: strings.TrimSpace(cfg.ConversationID),
			WorkspacePath:  workspacePath,
			ProjectID:      strings.TrimSpace(cfg.ProjectID),
			ScopeKey:       scopeKey,
			Metadata:       cloneMap(cfg.Metadata),
		},
		currentConversationID: strings.TrimSpace(cfg.ConversationID),
		card:                  buildAgentCard(cfg.Descriptor, cfg.CardOverrides, ""),
		stopCh:                make(chan struct{}),
		taskWatchers:          make(map[string]map[chan *a2apb.StreamResponse]struct{}),
		taskCancels:           make(map[string]context.CancelFunc),
		swarmStatus: AgentSwarmStatus{
			Handle: handle,
			Model:  cfg.Descriptor.ProviderName,
			Status: SwarmStatusIdle,
		},
		swarmEventQueue: make(chan SwarmEvent, 100),
		wsServer:        NewWebSocketServer(logger),
		wsClient:        NewWebSocketClient(logger),
		wsPool:          NewConnectionPool(logger),
		peerMutes:       NewPeerMuteStore(),
	}, nil
}

func mergeExistingAttachSurfaces(next, existing *PeerPresence, currentPID int) {
	if next == nil || existing == nil || existing.PID != currentPID {
		return
	}
	if existing.ControlSocket != "" {
		next.ControlSocket = existing.ControlSocket
	}
	if existing.ServeURL != "" {
		next.ServeURL = existing.ServeURL
	}
	next.InstanceToken = existing.InstanceToken
	next.ProcessStart = existing.ProcessStart
	next.Executable = existing.Executable
}

// Start starts the local A2A server, publishes discovery state, and begins heartbeats.
func (r *Runtime) Start(ctx context.Context) error {
	var startErr error
	r.startOnce.Do(func() {
		if err := r.startServer(); err != nil {
			startErr = err
			return
		}

		// Register with SQLite backend (kept for compatibility)
		lease, err := r.backend.RegisterPeer(ctx, r.peer, r.ttlSeconds)
		if err != nil {
			startErr = err
			return
		}
		r.peer.RegisteredAt = lease.RenewedAt
		r.peer.LastSeenAt = lease.RenewedAt
		r.peer.ExpiresAt = lease.ExpiresAt

		// Also register with filesystem-based discovery (NEW)
		swarmName := r.swarmName
		if swarmName == "" {
			swarmName = DefaultSwarmName
		}
		presence := PeerPresence{
			Handle:         r.peer.Handle,
			Name:           r.peer.Handle,
			PID:            0, // Will be set by JoinSwarm
			EndpointURL:    r.peer.EndpointURL,
			CardURL:        r.peer.CardURL,
			Workspace:      r.peer.WorkspacePath,
			Status:         "idle",
			ConversationID: r.currentConversationID,
			Type:           PeerTypeLocal,
		}
		// A TUI can bind and advertise its local control socket before the
		// asynchronous SDK runtime is ready. Preserve those same-process attach
		// surfaces when the runtime publishes its richer A2A presence; replacing
		// the file wholesale here used to make Inspect disappear at bootstrap.
		if existing, err := GetPeer(swarmName, r.peer.Handle); err == nil {
			mergeExistingAttachSurfaces(&presence, existing, os.Getpid())
		}
		if err := JoinSwarm(swarmName, presence); err != nil {
			// Log but don't fail - filesystem discovery is supplementary
			r.logger.Warn(ctx, "a2a.filesystem_join_failed", observability.F("error", err.Error()))
		}

		r.emitHook(ctx, hooks.EventA2APeerAnnounced, map[string]any{
			"session_id":   r.peer.SessionID,
			"handle":       r.peer.Handle,
			"scope_key":    r.peer.ScopeKey,
			"endpoint_url": r.peer.EndpointURL,
			"protocol":     "a2a",
		})

		r.wg.Add(1)
		go r.heartbeatLoop()
	})
	return startErr
}

func (r *Runtime) startServer() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.server != nil {
		return nil
	}

	listenAddress := r.listenAddress
	if listenAddress == "" {
		listenAddress = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen a2a server: %w", err)
	}
	baseURL := r.baseURL
	if baseURL == "" {
		baseURL = "http://" + dialableHostPort(ln.Addr().String())
	}
	r.peer.EndpointURL = strings.TrimRight(baseURL, "/") + r.rpcPath
	r.peer.CardURL = strings.TrimRight(baseURL, "/") + AgentCardPath
	r.card.SupportedInterfaces[0].Url = r.peer.EndpointURL

	mux := http.NewServeMux()
	mux.HandleFunc(AgentCardPath, r.serveAgentCard)
	mux.HandleFunc(r.rpcPath, r.serveRPC)

	r.listener = ln
	r.mux = mux
	r.server = &http.Server{
		Handler: mux,
	}

	r.wg.Go(func() {
		if err := r.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			r.logger.Warn(context.Background(), "a2a.server_failed", observability.F("error", err.Error()))
		}
	})

	// Mount WebSocket handler on the existing HTTP mux (no separate listener)
	if r.wsServer != nil {
		r.wsServer.RegisterOnMux(mux)
		r.wsServer.SetRequestHandler(r.requestHandler)
		// Route protojson SendMessage requests through the unified handler so
		// WebSocket and HTTP RPC peers exercise identical logic.
		r.wsServer.SetSendMessageDelegate(r.handleSendMessage)
		r.logger.Info(context.Background(), "a2a.websocket_registered",
			observability.F("path", WebSocketPath))
	}

	return nil
}

// Stop terminates the local server and background goroutines.
func (r *Runtime) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.mu.Lock()
		server := r.server
		r.mu.Unlock()
		if server != nil {
			_ = server.Shutdown(ctx)
		}
		// Stop WebSocket server
		if r.wsServer != nil {
			_ = r.wsServer.Stop(ctx)
		}
		// Close WebSocket connection pool
		if r.wsPool != nil {
			r.wsPool.Close()
		}
		r.wg.Wait()
	})
}

// Close stops the runtime, removes peer from swarm, and closes the underlying backend.
func (r *Runtime) Close() error {
	// Remove from filesystem-based discovery before stopping
	swarmName := r.swarmName
	if swarmName == "" {
		swarmName = DefaultSwarmName
	}
	_ = LeaveSwarm(swarmName, r.peer.Handle)

	r.Stop()
	return r.backend.Close()
}

// Peer returns the local published peer identity.
func (r *Runtime) Peer() PeerIdentity { return r.peer }

// DebugSnapshot is a read-only view of the runtime's live internals. It is
// intended for diagnostic tooling (e.g. `swarmos swarm attach <handle> debug`)
// and is safe to call concurrently with normal runtime operation. The fields
// are best-effort point-in-time samples — they may change before the caller
// reads the next field.
type DebugSnapshot struct {
	Handle               string
	SessionID            string
	EndpointURL          string
	WorkspacePath        string
	SwarmStatus          string
	SwarmCurrentTask     string
	SwarmModel           string
	ActiveConversationID string
	PendingMessageCount  int
	ActiveTaskIDs        []string
	// WS server diagnostics (zero if no WS server, e.g. tests).
	WSStats WebSocketServerStats
}

// Debug returns a read-only snapshot of the runtime's live state. Used by
// hosts that want to expose A2A internals over a debug surface — see
// AttachedServer's TypeGetA2ADebug handler.
func (r *Runtime) Debug() DebugSnapshot {
	if r == nil {
		return DebugSnapshot{}
	}
	r.mu.Lock()
	snap := DebugSnapshot{
		Handle:               r.peer.Handle,
		SessionID:            r.peer.SessionID,
		EndpointURL:          r.peer.EndpointURL,
		WorkspacePath:        r.peer.WorkspacePath,
		SwarmStatus:          string(r.swarmStatus.Status),
		SwarmCurrentTask:     r.swarmStatus.CurrentTask,
		SwarmModel:           r.swarmStatus.Model,
		ActiveConversationID: r.currentConversationID,
		PendingMessageCount:  len(r.pending),
		ActiveTaskIDs:        make([]string, 0, len(r.taskCancels)),
	}
	for id := range r.taskCancels {
		snap.ActiveTaskIDs = append(snap.ActiveTaskIDs, id)
	}
	wsServer := r.wsServer
	r.mu.Unlock()
	if wsServer != nil {
		snap.WSStats = wsServer.Stats()
	}
	return snap
}

// LookupBindingByRemoteTaskID exposes the backend's binding lookup so hosts
// (e.g. the TUI request handler) can correlate an inbound reply message
// (carrying referenceTaskIds) to the local conversation that originally
// produced the corresponding outbound task. Returns (nil, nil) when no
// binding exists — callers should fall back to peer-handle resolution.
func (r *Runtime) LookupBindingByRemoteTaskID(ctx context.Context, remoteTaskID string) (*RemoteTaskBinding, error) {
	if r == nil || r.backend == nil {
		return nil, nil
	}
	return r.backend.LookupBindingByRemoteTaskID(ctx, r.peer.SessionID, remoteTaskID)
}

// dialableHostPort rewrites a wildcard/unspecified bind host (e.g. "[::]:PORT"
// or "0.0.0.0:PORT") to a dialable loopback host, preserving the port. A
// listener bound to the unspecified address reports its Addr() with that
// wildcard host, which is NOT routable by clients — peers that recorded
// "http://[::]:PORT/rpc" as their endpoint_url could never be dialed. Only the
// host is normalized; an already-concrete host is returned unchanged.
func dialableHostPort(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// Address returns the actual listening address (includes dynamically assigned port if port was 0)
func (r *Runtime) Address() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener != nil {
		return r.listener.Addr().String()
	}
	return r.listenAddress
}

// Card returns the generated Agent Card.
func (r *Runtime) Card() *a2apb.AgentCard { return r.card }

// SetRequestHandler installs the local inbound request handler used by SendMessage/streaming.
func (r *Runtime) SetRequestHandler(handler RequestHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestHandler = handler
}

// InboundMessageCallback is called when an inbound A2A message arrives from a
// peer, before the requestHandler executes.  The host (TUI or conductor) can
// use this to allocate a conversation, update UI state, or tag the execution
// context with the peer's source information.
//
// The callback receives:
//   - peer: identity of the sender
//   - conversationID: the conversation this message targets (may be empty)
type InboundMessageCallback func(peer PeerIdentity, conversationID string)

// SetInboundMessageCallback installs a callback that fires on every inbound
// A2A message, before the requestHandler runs.  Pass nil to clear.
func (r *Runtime) SetInboundMessageCallback(cb InboundMessageCallback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inboundMsgCb = cb
}

// UpdateConversationID updates the main conversation target used for projections.
func (r *Runtime) UpdateConversationID(ctx context.Context, conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	r.mu.Lock()
	r.currentConversationID = conversationID
	r.peer.ConversationID = conversationID
	r.mu.Unlock()
	if r.logger != nil {
		r.logger.Info(ctx, "a2a.runtime.update_conversation",
			observability.F("session_id", r.peer.SessionID),
			observability.F("conversation_id", conversationID),
		)
	}
	return r.backend.UpdatePeerConversation(ctx, r.peer.SessionID, conversationID)
}

// TakePendingMessages drains projected peer messages for the agent execute loop.
func (r *Runtime) TakePendingMessages() []*conversation.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) == 0 {
		return nil
	}
	out := make([]*conversation.Message, len(r.pending))
	copy(out, r.pending)
	r.pending = nil
	return out
}

// EnqueueMessage adds a message to the pending queue for processing.
// This is used to inject peer messages like user messages.
func (r *Runtime) EnqueueMessage(msg *conversation.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, msg)
}

// BuiltinTools returns the A2A toolset for a network-enabled agent.
func (r *Runtime) BuiltinTools() []Tool {
	return []Tool{
		newListAgentsTool(r),
		newFetchAgentCardTool(r),
		newSendMessageTool(r, false),
		newSendMessageTool(r, true),
		newGetTaskTool(r),
		newListTasksTool(r),
		newCancelTaskTool(r),
		newSubscribeTaskTool(r),
	}
}

// ListAgents returns active peer sessions in the same workspace/project scope.
func (r *Runtime) ListAgents(ctx context.Context) ([]PeerIdentity, error) {
	if err := r.Start(ctx); err != nil {
		return nil, err
	}
	return r.backend.ListPeers(ctx, r.peer.ScopeKey)
}

func (r *Runtime) isSelfEndpoint(targetEndpoint string) bool {
	return strings.EqualFold(
		strings.TrimRight(strings.TrimSpace(targetEndpoint), "/"),
		strings.TrimRight(strings.TrimSpace(r.peer.EndpointURL), "/"),
	)
}

// FetchAgentCard retrieves a peer's published card using its base/card URL.
func (r *Runtime) FetchAgentCard(ctx context.Context, cardBaseURL string) (*a2apb.AgentCard, error) {
	return r.client.FetchAgentCard(ctx, strings.TrimRight(cardBaseURL, "/"))
}

// SendMessage dispatches an A2A SendMessage call and projects the response into the local conversation.
func (r *Runtime) SendMessage(ctx context.Context, targetEndpoint string, req *a2apb.SendMessageRequest, extensions []string) (*a2apb.SendMessageResponse, error) {
	if err := r.Start(ctx); err != nil {
		return nil, err
	}
	if r.isSelfEndpoint(targetEndpoint) {
		return nil, fmt.Errorf("a2a self-send is not supported")
	}
	if req == nil {
		return nil, fmt.Errorf("a2a send request is required")
	}
	req = protoCloneSendRequest(req)
	ensureLocalMetadata(req.GetMessage(), r.peer)
	resp, err := r.client.SendMessage(ctx, targetEndpoint, req, extensions)
	if err != nil {
		return nil, err
	}
	if err := r.projectSendMessageResponse(ctx, targetEndpoint, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// SendStreamingMessage dispatches a streaming A2A request and projects all remote events.
func (r *Runtime) SendStreamingMessage(ctx context.Context, targetEndpoint string, req *a2apb.SendMessageRequest, extensions []string) ([]*a2apb.StreamResponse, error) {
	if err := r.Start(ctx); err != nil {
		return nil, err
	}
	if r.isSelfEndpoint(targetEndpoint) {
		return nil, fmt.Errorf("a2a self-send is not supported")
	}
	req = protoCloneSendRequest(req)
	ensureLocalMetadata(req.GetMessage(), r.peer)
	events := make([]*a2apb.StreamResponse, 0)
	err := r.client.SendStreamingMessage(ctx, targetEndpoint, req, extensions, func(event *a2apb.StreamResponse) error {
		events = append(events, protoCloneStreamResponse(event))
		return r.projectStreamResponse(ctx, targetEndpoint, event)
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

// GetTask fetches a task from a remote peer and projects the resulting state snapshot.
func (r *Runtime) GetTask(ctx context.Context, targetEndpoint string, req *a2apb.GetTaskRequest, extensions []string) (*a2apb.Task, error) {
	task, err := r.client.GetTask(ctx, targetEndpoint, req, extensions)
	if err != nil {
		return nil, err
	}
	if task != nil {
		if err := r.projectTaskSnapshot(ctx, targetEndpoint, task); err != nil {
			return nil, err
		}
	}
	return task, nil
}

// ListTasks fetches a page of tasks from a remote peer.
func (r *Runtime) ListTasks(ctx context.Context, targetEndpoint string, req *a2apb.ListTasksRequest, extensions []string) (*a2apb.ListTasksResponse, error) {
	return r.client.ListTasks(ctx, targetEndpoint, req, extensions)
}

// CancelTask cancels a remote task and projects the updated snapshot.
func (r *Runtime) CancelTask(ctx context.Context, targetEndpoint string, req *a2apb.CancelTaskRequest, extensions []string) (*a2apb.Task, error) {
	task, err := r.client.CancelTask(ctx, targetEndpoint, req, extensions)
	if err != nil {
		return nil, err
	}
	if task != nil {
		if err := r.projectTaskSnapshot(ctx, targetEndpoint, task); err != nil {
			return nil, err
		}
	}
	return task, nil
}

// SubscribeToTask follows a remote task stream and projects all updates.
func (r *Runtime) SubscribeToTask(ctx context.Context, targetEndpoint string, req *a2apb.SubscribeToTaskRequest, extensions []string) ([]*a2apb.StreamResponse, error) {
	events := make([]*a2apb.StreamResponse, 0)
	err := r.client.SubscribeToTask(ctx, targetEndpoint, req, extensions, func(event *a2apb.StreamResponse) error {
		events = append(events, protoCloneStreamResponse(event))
		return r.projectStreamResponse(ctx, targetEndpoint, event)
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (r *Runtime) serveAgentCard(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := validateServiceHeaders(req.Header); err != nil {
		writeJSONRPCError(w, nil, err)
		return
	}
	raw, err := marshalProto(r.card)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentJSON)
	w.Header().Set("Cache-Control", "max-age=30")
	w.Header().Set("ETag", fmt.Sprintf(`"%s"`, r.card.GetVersion()))
	_, _ = w.Write(raw)
}

func (r *Runtime) serveRPC(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := validateServiceHeaders(req.Header); err != nil {
		writeJSONRPCError(w, nil, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 2<<20))
	if err != nil {
		writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
			JSONRPC: jsonRPCVersion,
			Error:   &jsonRPCError{Code: -32700, Message: "Invalid JSON payload"},
		})
		return
	}
	var envelope jsonRPCRequest
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
			JSONRPC: jsonRPCVersion,
			Error:   &jsonRPCError{Code: -32700, Message: "Invalid JSON payload"},
		})
		return
	}
	if envelope.JSONRPC != jsonRPCVersion || strings.TrimSpace(envelope.Method) == "" {
		writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      envelope.ID,
			Error:   &jsonRPCError{Code: -32600, Message: "Request payload validation error"},
		})
		return
	}

	switch envelope.Method {
	case "SendMessage":
		msg := &a2apb.SendMessageRequest{}
		if err := unmarshalProto(envelope.Params, msg); err != nil {
			writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
				JSONRPC: jsonRPCVersion,
				ID:      envelope.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "Invalid parameters"},
			})
			return
		}
		resp, rpcErr := r.handleSendMessage(req.Context(), msg)
		if rpcErr != nil {
			writeJSONRPCError(w, envelope.ID, rpcErr)
			return
		}
		writeJSONRPCResult(w, envelope.ID, resp)
	case "SendStreamingMessage":
		msg := &a2apb.SendMessageRequest{}
		if err := unmarshalProto(envelope.Params, msg); err != nil {
			writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
				JSONRPC: jsonRPCVersion,
				ID:      envelope.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "Invalid parameters"},
			})
			return
		}
		if !r.card.GetCapabilities().GetStreaming() {
			writeJSONRPCError(w, envelope.ID, unsupportedOperationError("Streaming is not supported"))
			return
		}
		r.serveStreamingSend(req.Context(), w, envelope.ID, msg)
	case "GetTask":
		msg := &a2apb.GetTaskRequest{}
		if err := unmarshalProto(envelope.Params, msg); err != nil {
			writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
				JSONRPC: jsonRPCVersion,
				ID:      envelope.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "Invalid parameters"},
			})
			return
		}
		task, rpcErr := r.handleGetTask(req.Context(), msg)
		if rpcErr != nil {
			writeJSONRPCError(w, envelope.ID, rpcErr)
			return
		}
		writeJSONRPCResult(w, envelope.ID, task)
	case "ListTasks":
		msg := &a2apb.ListTasksRequest{}
		if err := unmarshalProto(envelope.Params, msg); err != nil {
			writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
				JSONRPC: jsonRPCVersion,
				ID:      envelope.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "Invalid parameters"},
			})
			return
		}
		resp, rpcErr := r.handleListTasks(req.Context(), msg)
		if rpcErr != nil {
			writeJSONRPCError(w, envelope.ID, rpcErr)
			return
		}
		writeJSONRPCResult(w, envelope.ID, resp)
	case "CancelTask":
		msg := &a2apb.CancelTaskRequest{}
		if err := unmarshalProto(envelope.Params, msg); err != nil {
			writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
				JSONRPC: jsonRPCVersion,
				ID:      envelope.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "Invalid parameters"},
			})
			return
		}
		task, rpcErr := r.handleCancelTask(req.Context(), msg)
		if rpcErr != nil {
			writeJSONRPCError(w, envelope.ID, rpcErr)
			return
		}
		writeJSONRPCResult(w, envelope.ID, task)
	case "SubscribeToTask":
		msg := &a2apb.SubscribeToTaskRequest{}
		if err := unmarshalProto(envelope.Params, msg); err != nil {
			writeJSONRPCResponse(w, http.StatusBadRequest, jsonRPCResponse{
				JSONRPC: jsonRPCVersion,
				ID:      envelope.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "Invalid parameters"},
			})
			return
		}
		if !r.card.GetCapabilities().GetStreaming() {
			writeJSONRPCError(w, envelope.ID, unsupportedOperationError("Streaming is not supported"))
			return
		}
		r.serveSubscribeTask(req.Context(), w, envelope.ID, msg)
	case "GetExtendedAgentCard":
		writeJSONRPCError(w, envelope.ID, unsupportedOperationError("Extended agent card is not supported"))
	case "CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig", "ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig":
		writeJSONRPCError(w, envelope.ID, pushNotificationUnsupportedError())
	default:
		writeJSONRPCResponse(w, http.StatusNotFound, jsonRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      envelope.ID,
			Error:   &jsonRPCError{Code: -32601, Message: "Method not found"},
		})
	}
}

func (r *Runtime) handleSendMessage(ctx context.Context, req *a2apb.SendMessageRequest) (*a2apb.SendMessageResponse, *a2aError) {
	if req.GetMessage() == nil || strings.TrimSpace(req.GetMessage().GetMessageId()) == "" {
		return nil, &a2aError{Code: -32602, Message: "Invalid parameters", HTTPStatus: http.StatusBadRequest}
	}
	peer := r.resolveRemotePeer(ctx, req.GetMessage())

	// Phase 3 steering: drop inbound DMs from muted peers. We return a
	// completed task with a status note so the sender sees clean completion
	// (not a protocol error). Muting is non-destructive on the wire.
	if r.peerMutes != nil && r.peerMutes.IsPeerMuted(peer.Handle) {
		return r.mutedPeerResponse(req, peer.Handle), nil
	}

	projected := r.projectPeerMessageFromA2A(peer, r.resolveInboundConversationID(req), req.GetMessage(), "message")
	if r.shouldAutoPersistInboundProjection() {
		if err := r.persistProjection(ctx, projected, r.projectionKeyForMessage(req.GetMessage()), false); err != nil {
			return nil, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError}
		}
	}

	// Inspect swarm metadata to choose the inbound handling path.
	swarmType := swarmTypeFromMessage(req.GetMessage())
	isReply := len(req.GetMessage().GetReferenceTaskIds()) > 0

	// A swarm DM should always be async — the sender does not wait for the
	// recipient's agent to finish. The recipient ACKs immediately with a
	// Task{state: WORKING}, then sends its reply back as a reverse-DM when
	// the agent completes. See plan.md for the full design.
	//
	// A reply (any message with referenceTaskIds) is also async: the
	// recipient projects it into the conversation but does not run its agent
	// (the L1 loop guard is enforced by the TUI request handler — here we
	// just don't block the sender). We still mark it as task-mode so the
	// recipient returns immediately.
	//
	// Broadcasts are inject-only: the recipient projects but does not execute.
	// The async path returns the task immediately, then spawnAsyncTask runs
	// executeInbound which delegates to the requestHandler. The TUI request
	// handler treats broadcast and reply messages as projection-only.
	forceAsync := swarmType == SwarmTypeDM || swarmType == SwarmTypeBroadcast || isReply

	shouldTask := forceAsync || req.GetConfiguration().GetReturnImmediately() || strings.TrimSpace(req.GetMessage().GetTaskId()) != ""
	if !shouldTask {
		reply, rpcErr := r.executeInbound(ctx, peer, req, projected)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return &a2apb.SendMessageResponse{
			Payload: &a2apb.SendMessageResponse_Message{Message: reply},
		}, nil
	}

	task := r.newTaskFromRequest(req)
	if err := r.backend.SaveTask(ctx, r.peer.SessionID, task); err != nil {
		return nil, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError}
	}
	r.emitHook(ctx, hooks.EventA2ATaskCreated, map[string]any{
		"protocol":   "a2a",
		"task_id":    task.GetId(),
		"context_id": task.GetContextId(),
		"event":      "task_created",
	})

	// Async path: spawn the task and return the WORKING snapshot immediately.
	// This is the default for DMs, replies, broadcasts, and any request with
	// ReturnImmediately set. The legacy "wait for completion" path below is
	// kept for backward-compat with non-swarm callers that explicitly set
	// taskId without ReturnImmediately.
	if forceAsync || req.GetConfiguration().GetReturnImmediately() {
		r.spawnAsyncTask(peer, req, projected, task)
		return &a2apb.SendMessageResponse{
			Payload: &a2apb.SendMessageResponse_Task{Task: task},
		}, nil
	}

	reply, rpcErr := r.executeInbound(ctx, peer, req, projected)
	if rpcErr != nil {
		return nil, rpcErr
	}
	completed := r.completeTask(task, reply)
	if err := r.backend.SaveTask(ctx, r.peer.SessionID, completed); err != nil {
		return nil, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError}
	}
	return &a2apb.SendMessageResponse{
		Payload: &a2apb.SendMessageResponse_Task{Task: completed},
	}, nil
}

// mutedPeerResponse synthesises a completed task to return when an inbound
// DM is dropped due to a steering-installed peer mute. The sender sees a
// clean task completion rather than a protocol error.
func (r *Runtime) mutedPeerResponse(req *a2apb.SendMessageRequest, peerHandle string) *a2apb.SendMessageResponse {
	task := r.newTaskFromRequest(req)
	notice := fmt.Sprintf("[steering] inbound DMs from %q are muted", peerHandle)
	if reason := r.peerMutes.MuteReason(peerHandle); reason != "" {
		notice += ": " + reason
	}
	statusMsg := statusMessage(task.GetContextId(), task.GetId(), notice)
	completed := r.setTaskState(task, a2apb.TaskState_TASK_STATE_COMPLETED, statusMsg)
	return &a2apb.SendMessageResponse{
		Payload: &a2apb.SendMessageResponse_Task{Task: completed},
	}
}

// swarmTypeFromMessage extracts the SwarmTypeKey metadata value from an A2A
// message. Returns "" if the metadata is absent or the key is not set.
func swarmTypeFromMessage(msg *a2apb.Message) string {
	if msg == nil || msg.GetMetadata() == nil {
		return ""
	}
	raw, ok := msg.GetMetadata().AsMap()[SwarmTypeKey]
	if !ok {
		return ""
	}
	s, _ := raw.(string)
	return strings.TrimSpace(s)
}

// fromHandleFromMessage extracts the sender's swarm handle from an A2A
// message's metadata. The sender's runtime stamps "from_handle" on every
// swarm DM and broadcast (see SendDM / Broadcast). Returns "" if absent.
func fromHandleFromMessage(msg *a2apb.Message) string {
	if msg == nil || msg.GetMetadata() == nil {
		return ""
	}
	m := msg.GetMetadata().AsMap()
	if raw, ok := m["from_handle"]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	// Fall back to the MetadataHandleKey ("swarm.handle") if from_handle is
	// missing — older senders may have stamped that one instead.
	if raw, ok := m[MetadataHandleKey]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// snapshotSwarmStatus returns the current SwarmStatus and CurrentTask, used
// by spawnAsyncTask to restore state after a transient "working" tag.
func (r *Runtime) snapshotSwarmStatus() (SwarmStatus, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.swarmStatus.Status, r.swarmStatus.CurrentTask
}

// swarmTaskDescription builds the human-readable CurrentTask string shown to
// peers in `swarm_list_peers` while this agent is handling an inbound message.
func (r *Runtime) swarmTaskDescription(swarmType, fromHandle string) string {
	switch swarmType {
	case SwarmTypeDM:
		if fromHandle != "" {
			return fmt.Sprintf("handling DM from %s", fromHandle)
		}
		return "handling inbound DM"
	case SwarmTypeBroadcast:
		if fromHandle != "" {
			return fmt.Sprintf("received broadcast from %s", fromHandle)
		}
		return "received broadcast"
	default:
		return "handling inbound A2A request"
	}
}

// sendReplyBack pushes the agent's reply to the original DM sender as a
// fresh A2A SendMessage request. The reply carries referenceTaskIds=[origTaskID]
// so the sender's runtime can correlate it to the local conversation that
// originated the DM (see RemoteTaskBinding / LookupBindingByRemoteTaskID).
//
// Runs in its own goroutine — the caller (spawnAsyncTask) has already
// returned the WORKING task to the original peer, so we're free to take
// our time delivering the reply.
//
// Failures are logged and silently swallowed: the inbox copy already exists
// (SendDM writes to inbox before sending over WS), so the sender's UI will
// eventually see the reply even if the live delivery fails.
func (r *Runtime) sendReplyBack(toHandle, replyText, origTaskID string) {
	if strings.TrimSpace(toHandle) == "" || strings.TrimSpace(replyText) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.sendReplyDM(ctx, toHandle, replyText, origTaskID); err != nil {
		r.logger.Warn(ctx, "a2a.reply_back_failed",
			observability.F("to_handle", toHandle),
			observability.F("orig_task_id", origTaskID),
			observability.F("error", err.Error()),
		)
	}
}

func (r *Runtime) serveStreamingSend(ctx context.Context, w http.ResponseWriter, id any, req *a2apb.SendMessageRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONRPCError(w, id, unsupportedOperationError("Streaming is not supported by this server"))
		return
	}
	w.Header().Set("Content-Type", contentSSE)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	peer := r.resolveRemotePeer(ctx, req.GetMessage())
	projected := r.projectPeerMessageFromA2A(peer, r.resolveInboundConversationID(req), req.GetMessage(), "message")
	if r.shouldAutoPersistInboundProjection() {
		if err := r.persistProjection(ctx, projected, r.projectionKeyForMessage(req.GetMessage()), false); err != nil {
			writeSSEError(w, flusher, id, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError})
			return
		}
	}

	task := r.newTaskFromRequest(req)
	if err := r.backend.SaveTask(ctx, r.peer.SessionID, task); err != nil {
		writeSSEError(w, flusher, id, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError})
		return
	}
	_ = writeSSEEvent(w, flusher, id, &a2apb.StreamResponse{Payload: &a2apb.StreamResponse_Task{Task: task}})

	working := r.setTaskState(task, a2apb.TaskState_TASK_STATE_WORKING, statusMessage(task.GetContextId(), task.GetId(), "Task is working"))
	_ = r.backend.SaveTask(ctx, r.peer.SessionID, working)
	_ = writeSSEEvent(w, flusher, id, &a2apb.StreamResponse{
		Payload: &a2apb.StreamResponse_StatusUpdate{
			StatusUpdate: &a2apb.TaskStatusUpdateEvent{
				TaskId:    working.GetId(),
				ContextId: working.GetContextId(),
				Status:    working.GetStatus(),
			},
		},
	})

	reply, rpcErr := r.executeInbound(ctx, peer, req, projected)
	if rpcErr != nil {
		failed := r.setTaskState(working, a2apb.TaskState_TASK_STATE_FAILED, statusMessage(working.GetContextId(), working.GetId(), rpcErr.Message))
		_ = r.backend.SaveTask(ctx, r.peer.SessionID, failed)
		writeSSEError(w, flusher, id, rpcErr)
		return
	}
	_ = writeSSEEvent(w, flusher, id, &a2apb.StreamResponse{Payload: &a2apb.StreamResponse_Message{Message: reply}})
	completed := r.completeTask(working, reply)
	_ = r.backend.SaveTask(ctx, r.peer.SessionID, completed)
	_ = writeSSEEvent(w, flusher, id, &a2apb.StreamResponse{Payload: &a2apb.StreamResponse_Task{Task: completed}})
}

func (r *Runtime) handleGetTask(ctx context.Context, req *a2apb.GetTaskRequest) (*a2apb.Task, *a2aError) {
	task, err := r.backend.GetTask(ctx, r.peer.SessionID, req.GetId())
	if err != nil {
		return nil, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError}
	}
	if task == nil {
		return nil, taskNotFoundError(req.GetId())
	}
	return trimTask(task, req.HistoryLength, true), nil
}

func (r *Runtime) handleListTasks(ctx context.Context, req *a2apb.ListTasksRequest) (*a2apb.ListTasksResponse, *a2aError) {
	var after *time.Time
	if ts := req.GetStatusTimestampAfter(); ts != nil {
		value := ts.AsTime().UTC()
		after = &value
	}
	tasks, nextToken, total, err := r.backend.ListTasks(ctx, r.peer.SessionID, TaskListFilter{
		ContextID:            req.GetContextId(),
		Status:               req.GetStatus(),
		PageSize:             int(req.GetPageSize()),
		PageToken:            req.GetPageToken(),
		HistoryLength:        req.HistoryLength,
		StatusTimestampAfter: after,
		IncludeArtifacts:     req.GetIncludeArtifacts(),
	})
	if err != nil {
		return nil, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError}
	}
	return &a2apb.ListTasksResponse{
		Tasks:         tasks,
		NextPageToken: nextToken,
		PageSize:      int32(len(tasks)),
		TotalSize:     int32(total),
	}, nil
}

func (r *Runtime) handleCancelTask(ctx context.Context, req *a2apb.CancelTaskRequest) (*a2apb.Task, *a2aError) {
	task, err := r.backend.GetTask(ctx, r.peer.SessionID, req.GetId())
	if err != nil {
		return nil, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError}
	}
	if task == nil {
		return nil, taskNotFoundError(req.GetId())
	}
	if isTerminalTaskState(task.GetStatus().GetState()) {
		return nil, taskNotCancelableError(req.GetId())
	}
	r.mu.Lock()
	cancel := r.taskCancels[task.GetId()]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	task = r.setTaskState(task, a2apb.TaskState_TASK_STATE_CANCELED, statusMessage(task.GetContextId(), task.GetId(), "Task canceled"))
	if err := r.backend.SaveTask(ctx, r.peer.SessionID, task); err != nil {
		return nil, &a2aError{Code: -32603, Message: "Internal error", HTTPStatus: http.StatusInternalServerError}
	}
	r.broadcast(task.GetId(), &a2apb.StreamResponse{
		Payload: &a2apb.StreamResponse_StatusUpdate{
			StatusUpdate: &a2apb.TaskStatusUpdateEvent{
				TaskId:    task.GetId(),
				ContextId: task.GetContextId(),
				Status:    task.GetStatus(),
			},
		},
	})
	return task, nil
}

func (r *Runtime) serveSubscribeTask(ctx context.Context, w http.ResponseWriter, id any, req *a2apb.SubscribeToTaskRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONRPCError(w, id, unsupportedOperationError("Streaming is not supported by this server"))
		return
	}
	task, rpcErr := r.handleGetTask(ctx, &a2apb.GetTaskRequest{Id: req.GetId()})
	if rpcErr != nil {
		writeJSONRPCError(w, id, rpcErr)
		return
	}
	if isTerminalTaskState(task.GetStatus().GetState()) {
		writeJSONRPCError(w, id, unsupportedOperationError("SubscribeToTask is not supported for terminal tasks"))
		return
	}

	w.Header().Set("Content-Type", contentSSE)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	_ = writeSSEEvent(w, flusher, id, &a2apb.StreamResponse{
		Payload: &a2apb.StreamResponse_Task{Task: task},
	})

	ch := make(chan *a2apb.StreamResponse, 16)
	r.addWatcher(task.GetId(), ch)
	defer r.removeWatcher(task.GetId(), ch)

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-ch:
			if event == nil {
				return
			}
			if err := writeSSEEvent(w, flusher, id, event); err != nil {
				return
			}
			if payload := event.GetTask(); payload != nil && isTerminalTaskState(payload.GetStatus().GetState()) {
				return
			}
			if payload := event.GetStatusUpdate(); payload != nil && isTerminalTaskState(payload.GetStatus().GetState()) {
				return
			}
		}
	}
}

func (r *Runtime) executeInbound(ctx context.Context, peer PeerIdentity, req *a2apb.SendMessageRequest, projected *conversation.Message) (*a2apb.Message, *a2aError) {
	r.mu.Lock()
	handler := r.requestHandler
	cb := r.inboundMsgCb
	conversationID := r.currentConversationID
	r.mu.Unlock()
	source := "runtime.current"
	if conversationID == "" {
		conversationID = projected.Metadata["conversation_id"].(string)
		source = "projected.metadata"
	}
	if r.logger != nil {
		r.logger.Info(ctx, "a2a.runtime.execute_inbound",
			observability.F("session_id", r.peer.SessionID),
			observability.F("peer_handle", peer.Handle),
			observability.F("conversation_id", conversationID),
			observability.F("conversation_source", source),
			observability.F("has_handler", handler != nil),
		)
	}
	if handler == nil {
		return buildAgentMessage(r.peer, req.GetMessage().GetContextId(), req.GetMessage().GetTaskId(), "Message received"), nil
	}
	// Notify host before executing so it can allocate a conversation / update UI.
	if cb != nil {
		cb(peer, conversationID)
	}
	reply, err := handler(ctx, &InboundRequest{
		Peer:             peer,
		ConversationID:   conversationID,
		Request:          protoCloneSendRequest(req),
		ProjectedMessage: projected.Clone(),
	})
	if err != nil {
		return nil, &a2aError{Code: -32603, Message: err.Error(), HTTPStatus: http.StatusInternalServerError}
	}
	return conversationMessageToA2A(r.peer, req.GetMessage().GetContextId(), req.GetMessage().GetTaskId(), reply), nil
}

func (r *Runtime) spawnAsyncTask(peer PeerIdentity, req *a2apb.SendMessageRequest, projected *conversation.Message, task *a2apb.Task) {
	taskCtx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.taskCancels[task.GetId()] = cancel
	r.mu.Unlock()

	// Capture metadata before spawning the goroutine so the reverse-DM path
	// below has a stable view of who sent the original message.
	swarmType := swarmTypeFromMessage(req.GetMessage())
	isReply := len(req.GetMessage().GetReferenceTaskIds()) > 0
	fromHandle := fromHandleFromMessage(req.GetMessage())

	r.wg.Go(func() {
		defer cancel()
		defer func() {
			r.mu.Lock()
			delete(r.taskCancels, task.GetId())
			r.mu.Unlock()
		}()

		// Bracket the inbound execution with swarm status updates so any peer
		// running `swarm_list_peers` sees this agent as "working" with a
		// human-readable task description while it's processing the message.
		// Status returns to "idle" on every exit path (success, error, panic).
		prevStatus, prevTask := r.snapshotSwarmStatus()
		workingTask := r.swarmTaskDescription(swarmType, fromHandle)
		_, _ = r.UpdateStatus(taskCtx, SwarmStatusWorking, workingTask)
		defer func() {
			// Restore previous status on exit. If the agent was already
			// "working" on something else when this DM arrived (shouldn't
			// happen — agent is single-flight — but defensive), restore it.
			_, _ = r.UpdateStatus(taskCtx, prevStatus, prevTask)
		}()

		working := r.setTaskState(task, a2apb.TaskState_TASK_STATE_WORKING, statusMessage(task.GetContextId(), task.GetId(), "Task is working"))
		_ = r.backend.SaveTask(taskCtx, r.peer.SessionID, working)
		r.broadcast(task.GetId(), &a2apb.StreamResponse{
			Payload: &a2apb.StreamResponse_StatusUpdate{
				StatusUpdate: &a2apb.TaskStatusUpdateEvent{
					TaskId:    working.GetId(),
					ContextId: working.GetContextId(),
					Status:    working.GetStatus(),
				},
			},
		})

		reply, rpcErr := r.executeInbound(taskCtx, peer, req, projected)
		if rpcErr != nil {
			failed := r.setTaskState(working, a2apb.TaskState_TASK_STATE_FAILED, statusMessage(working.GetContextId(), working.GetId(), rpcErr.Message))
			_ = r.backend.SaveTask(taskCtx, r.peer.SessionID, failed)
			r.broadcast(task.GetId(), &a2apb.StreamResponse{
				Payload: &a2apb.StreamResponse_StatusUpdate{
					StatusUpdate: &a2apb.TaskStatusUpdateEvent{
						TaskId:    failed.GetId(),
						ContextId: failed.GetContextId(),
						Status:    failed.GetStatus(),
					},
				},
			})
			return
		}

		completed := r.completeTask(working, reply)
		_ = r.backend.SaveTask(taskCtx, r.peer.SessionID, completed)
		r.broadcast(task.GetId(), &a2apb.StreamResponse{Payload: &a2apb.StreamResponse_Message{Message: reply}})
		r.broadcast(task.GetId(), &a2apb.StreamResponse{Payload: &a2apb.StreamResponse_Task{Task: completed}})

		// Reverse-DM: when this peer finishes processing an inbound swarm DM,
		// send the assistant's reply back to the originator as a new DM with
		// referenceTaskIds=[task.id] so the sender can correlate it to the
		// original conversation. Skipped when:
		//   - This wasn't a DM (broadcasts, raw RPC, etc.)
		//   - The inbound itself was a reply (loop guard)
		//   - We don't know who sent it (no from_handle)
		//   - The agent produced no reply text
		if swarmType == SwarmTypeDM && !isReply && fromHandle != "" {
			replyText, _ := flattenMessageContent(reply)
			if strings.TrimSpace(replyText) != "" {
				go r.sendReplyBack(fromHandle, replyText, task.GetId())
			}
		}
	})
}

func (r *Runtime) projectSendMessageResponse(ctx context.Context, targetEndpoint string, resp *a2apb.SendMessageResponse) error {
	if resp == nil {
		return nil
	}
	switch payload := resp.Payload.(type) {
	case *a2apb.SendMessageResponse_Message:
		return r.projectPeerA2AMessage(ctx, targetEndpoint, payload.Message)
	case *a2apb.SendMessageResponse_Task:
		return r.projectTaskSnapshot(ctx, targetEndpoint, payload.Task)
	default:
		return nil
	}
}

func (r *Runtime) projectStreamResponse(ctx context.Context, targetEndpoint string, event *a2apb.StreamResponse) error {
	if event == nil {
		return nil
	}
	switch payload := event.Payload.(type) {
	case *a2apb.StreamResponse_Task:
		return r.projectTaskSnapshot(ctx, targetEndpoint, payload.Task)
	case *a2apb.StreamResponse_Message:
		return r.projectPeerA2AMessage(ctx, targetEndpoint, payload.Message)
	case *a2apb.StreamResponse_StatusUpdate:
		return r.projectTaskStatusUpdate(ctx, targetEndpoint, payload.StatusUpdate)
	case *a2apb.StreamResponse_ArtifactUpdate:
		return r.projectTaskArtifactUpdate(ctx, targetEndpoint, payload.ArtifactUpdate)
	default:
		return nil
	}
}

func (r *Runtime) projectTaskSnapshot(ctx context.Context, targetEndpoint string, task *a2apb.Task) error {
	if task == nil {
		return nil
	}
	if task.GetId() != "" {
		_ = r.backend.SaveRemoteBinding(ctx, RemoteTaskBinding{
			LocalSessionID:  r.peer.SessionID,
			ConversationID:  r.currentConversation(),
			RemoteEndpoint:  targetEndpoint,
			RemoteTaskID:    task.GetId(),
			RemoteContextID: task.GetContextId(),
		})
	}
	if status := task.GetStatus(); status != nil {
		if status.GetMessage() != nil {
			if err := r.projectPeerA2AMessage(ctx, targetEndpoint, status.GetMessage()); err != nil {
				return err
			}
		} else {
			msg := &conversation.Message{
				ID:        uuid.NewString(),
				Timestamp: time.Now().UTC(),
				Role:      conversation.RolePeer,
				Content:   fmt.Sprintf("A2A task %s is %s", task.GetId(), strings.TrimPrefix(task.GetStatus().GetState().String(), "TASK_STATE_")),
				A2A: &conversation.A2AMetadata{
					EndpointURL:        targetEndpoint,
					ContextID:          task.GetContextId(),
					TaskID:             task.GetId(),
					TaskState:          task.GetStatus().GetState().String(),
					StreamingEventType: "task",
				},
			}
			if err := r.persistProjection(ctx, msg, fmt.Sprintf("task:%s:%s", task.GetId(), task.GetStatus().GetState().String()), true); err != nil {
				return err
			}
		}
	}
	for _, historyMsg := range task.GetHistory() {
		if historyMsg.GetRole() == a2apb.Role_ROLE_AGENT {
			if err := r.projectPeerA2AMessage(ctx, targetEndpoint, historyMsg); err != nil {
				return err
			}
		}
	}
	for _, artifact := range task.GetArtifacts() {
		if err := r.projectArtifact(ctx, targetEndpoint, task.GetContextId(), task.GetId(), artifact, false, true); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) projectTaskStatusUpdate(ctx context.Context, targetEndpoint string, event *a2apb.TaskStatusUpdateEvent) error {
	if event == nil {
		return nil
	}
	if event.GetStatus().GetMessage() != nil {
		return r.projectPeerA2AMessage(ctx, targetEndpoint, event.GetStatus().GetMessage())
	}
	msg := &conversation.Message{
		ID:        uuid.NewString(),
		Timestamp: time.Now().UTC(),
		Role:      conversation.RolePeer,
		Content:   fmt.Sprintf("A2A task %s is %s", event.GetTaskId(), strings.TrimPrefix(event.GetStatus().GetState().String(), "TASK_STATE_")),
		A2A: &conversation.A2AMetadata{
			EndpointURL:        targetEndpoint,
			ContextID:          event.GetContextId(),
			TaskID:             event.GetTaskId(),
			TaskState:          event.GetStatus().GetState().String(),
			StreamingEventType: "status_update",
		},
	}
	return r.persistProjection(ctx, msg, fmt.Sprintf("status:%s:%s:%d", event.GetTaskId(), event.GetStatus().GetState().String(), event.GetStatus().GetTimestamp().GetSeconds()), true)
}

func (r *Runtime) projectTaskArtifactUpdate(ctx context.Context, targetEndpoint string, event *a2apb.TaskArtifactUpdateEvent) error {
	if event == nil {
		return nil
	}
	return r.projectArtifact(ctx, targetEndpoint, event.GetContextId(), event.GetTaskId(), event.GetArtifact(), event.GetAppend(), event.GetLastChunk())
}

func (r *Runtime) projectArtifact(ctx context.Context, targetEndpoint, contextID, taskID string, artifact *a2apb.Artifact, appendMode, lastChunk bool) error {
	if artifact == nil {
		return nil
	}
	msg := &conversation.Message{
		ID:        uuid.NewString(),
		Timestamp: time.Now().UTC(),
		Role:      conversation.RolePeer,
		Content:   flattenArtifactText(artifact),
		A2A: &conversation.A2AMetadata{
			EndpointURL:        targetEndpoint,
			ContextID:          contextID,
			TaskID:             taskID,
			ArtifactID:         artifact.GetArtifactId(),
			ArtifactName:       artifact.GetName(),
			StreamingEventType: "artifact_update",
			ArtifactAppend:     appendMode,
			ArtifactLastChunk:  lastChunk,
		},
	}
	msg.A2A.References = append(msg.A2A.References, artifactReferences(artifact)...)
	return r.persistProjection(ctx, msg, fmt.Sprintf("artifact:%s:%s:%t:%t", taskID, artifact.GetArtifactId(), appendMode, lastChunk), true)
}

func (r *Runtime) projectPeerA2AMessage(ctx context.Context, targetEndpoint string, msg *a2apb.Message) error {
	if msg == nil {
		return nil
	}
	projected := r.projectPeerMessageFromA2A(r.resolveMessageAuthor(targetEndpoint, msg), r.currentConversation(), msg, "message")
	return r.persistProjection(ctx, projected, r.projectionKeyForMessage(msg), true)
}

func (r *Runtime) projectPeerMessageFromA2A(peer PeerIdentity, conversationID string, msg *a2apb.Message, eventType string) *conversation.Message {
	content, refs := flattenMessageContent(msg)
	if conversationID == "" {
		conversationID = r.currentConversation()
	}
	return &conversation.Message{
		ID:        ensureID(""),
		Timestamp: time.Now().UTC(),
		Role:      conversation.RolePeer,
		Content:   content,
		Metadata: map[string]any{
			"conversation_id": conversationID,
		},
		A2A: &conversation.A2AMetadata{
			RemoteAgentHandle:  peer.Handle,
			RemoteAgentSession: peer.SessionID,
			EndpointURL:        peer.EndpointURL,
			CardURL:            peer.CardURL,
			MessageID:          msg.GetMessageId(),
			ContextID:          msg.GetContextId(),
			TaskID:             msg.GetTaskId(),
			ReferenceTaskIDs:   append([]string(nil), msg.GetReferenceTaskIds()...),
			StreamingEventType: eventType,
			References:         refs,
		},
	}
}

func (r *Runtime) persistProjection(ctx context.Context, msg *conversation.Message, key string, dedupe bool) error {
	if msg == nil {
		return nil
	}
	conversationID := r.currentConversation()
	if raw, ok := msg.Metadata["conversation_id"].(string); ok && strings.TrimSpace(raw) != "" {
		conversationID = raw
	}
	if strings.TrimSpace(conversationID) == "" {
		conversationID = r.peer.ConversationID
	}
	if dedupe && strings.TrimSpace(key) != "" && strings.TrimSpace(conversationID) != "" {
		created, err := r.backend.RecordProjection(ctx, r.peer.SessionID, conversationID, key)
		if err != nil {
			return err
		}
		if !created {
			return nil
		}
	}
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]any)
	}
	msg.Metadata["conversation_id"] = conversationID
	persisted := false
	if r.projectionSink != nil {
		ok, err := r.projectionSink(ctx, msg)
		if err != nil {
			return err
		}
		persisted = ok
	}
	if persisted {
		msg.Metadata[MessagePersistedMetadataKey] = true
		peerHandle := ""
		if msg.A2A != nil {
			peerHandle = msg.A2A.RemoteAgentHandle
		}
		r.emitHook(ctx, hooks.EventA2AMessageProjected, map[string]any{
			"conversation_id": conversationID,
			"message_id":      msg.A2A.MessageID,
			"task_id":         msg.A2A.TaskID,
			"context_id":      msg.A2A.ContextID,
			"peer_handle":     peerHandle,
		})
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, msg)
	peerHandle := ""
	if msg.A2A != nil {
		peerHandle = msg.A2A.RemoteAgentHandle
	}
	r.emitHook(ctx, hooks.EventA2AMessageProjected, map[string]any{
		"conversation_id": conversationID,
		"message_id":      msg.A2A.MessageID,
		"task_id":         msg.A2A.TaskID,
		"context_id":      msg.A2A.ContextID,
		"peer_handle":     peerHandle,
	})
	return nil
}

func (r *Runtime) resolveInboundConversationID(req *a2apb.SendMessageRequest) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(r.currentConversationID) != "" {
		return r.currentConversationID
	}
	if strings.TrimSpace(r.peer.ConversationID) != "" {
		return r.peer.ConversationID
	}
	if contextID := strings.TrimSpace(req.GetMessage().GetContextId()); contextID != "" {
		return "a2a:" + contextID
	}
	return "a2a:" + ensureID("")
}

func (r *Runtime) shouldAutoPersistInboundProjection() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requestHandler == nil
}

func (r *Runtime) resolveRemotePeer(ctx context.Context, msg *a2apb.Message) PeerIdentity {
	if msg == nil {
		return PeerIdentity{Handle: "peer"}
	}
	handle, _ := msg.GetMetadata().AsMap()[MetadataHandleKey].(string)
	sessionID, _ := msg.GetMetadata().AsMap()[MetadataSessionIDKey].(string)
	peer := PeerIdentity{
		Handle:      firstNonEmpty(handle, "peer"),
		SessionID:   firstNonEmpty(sessionID, "unknown"),
		ScopeKey:    r.peer.ScopeKey,
		EndpointURL: "",
	}
	if sessionID != "" {
		if registered, err := r.backend.GetPeer(ctx, sessionID); err == nil && registered != nil {
			peer = *registered
		}
	}
	return peer
}

func (r *Runtime) resolveMessageAuthor(targetEndpoint string, msg *a2apb.Message) PeerIdentity {
	metadata := map[string]any(nil)
	if msg != nil && msg.GetMetadata() != nil {
		metadata = msg.GetMetadata().AsMap()
	}
	handle, _ := metadata[MetadataHandleKey].(string)
	sessionID, _ := metadata[MetadataSessionIDKey].(string)
	return PeerIdentity{
		Handle:      firstNonEmpty(handle, "peer"),
		SessionID:   firstNonEmpty(sessionID, "unknown"),
		EndpointURL: targetEndpoint,
	}
}

func (r *Runtime) currentConversation() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.currentConversationID
}

func (r *Runtime) heartbeatLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(time.Duration(r.ttlSeconds) * time.Second / 2)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			// Use filesystem-based discovery to update last_seen_at timestamp
			swarmName := r.swarmName
			if swarmName == "" {
				swarmName = DefaultSwarmName
			}
			if err := TouchPeer(swarmName, r.peer.Handle); err != nil {
				r.logger.Warn(context.Background(), "a2a.peer_touch_failed", observability.F("error", err.Error()))
			}
		}
	}
}

func (r *Runtime) addWatcher(taskID string, ch chan *a2apb.StreamResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	watchers := r.taskWatchers[taskID]
	if watchers == nil {
		watchers = make(map[chan *a2apb.StreamResponse]struct{})
		r.taskWatchers[taskID] = watchers
	}
	watchers[ch] = struct{}{}
}

func (r *Runtime) removeWatcher(taskID string, ch chan *a2apb.StreamResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if watchers := r.taskWatchers[taskID]; watchers != nil {
		delete(watchers, ch)
		if len(watchers) == 0 {
			delete(r.taskWatchers, taskID)
		}
	}
}

func (r *Runtime) broadcast(taskID string, event *a2apb.StreamResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.taskWatchers[taskID] {
		select {
		case ch <- protoCloneStreamResponse(event):
		default:
		}
	}
}

func (r *Runtime) newTaskFromRequest(req *a2apb.SendMessageRequest) *a2apb.Task {
	contextID := strings.TrimSpace(req.GetMessage().GetContextId())
	if contextID == "" {
		contextID = uuid.NewString()
	}
	taskID := strings.TrimSpace(req.GetMessage().GetTaskId())
	if taskID == "" {
		taskID = uuid.NewString()
	}
	clientMessage := protoCloneMessage(req.GetMessage())
	clientMessage.ContextId = contextID
	clientMessage.TaskId = taskID
	return &a2apb.Task{
		Id:        taskID,
		ContextId: contextID,
		Status: &a2apb.TaskStatus{
			State:     a2apb.TaskState_TASK_STATE_SUBMITTED,
			Message:   statusMessage(contextID, taskID, "Task submitted"),
			Timestamp: timestamppb.New(time.Now().UTC()),
		},
		History: []*a2apb.Message{clientMessage},
		Metadata: mapToStruct(map[string]any{
			MetadataHandleKey:    r.peer.Handle,
			MetadataSessionIDKey: r.peer.SessionID,
		}),
	}
}

func (r *Runtime) setTaskState(task *a2apb.Task, state a2apb.TaskState, message *a2apb.Message) *a2apb.Task {
	clone := protoCloneTask(task)
	clone.Status = &a2apb.TaskStatus{
		State:     state,
		Message:   message,
		Timestamp: timestamppb.New(time.Now().UTC()),
	}
	return clone
}

func (r *Runtime) completeTask(task *a2apb.Task, reply *a2apb.Message) *a2apb.Task {
	clone := protoCloneTask(task)
	clone.History = append(clone.History, protoCloneMessage(reply))
	clone.Status = &a2apb.TaskStatus{
		State:     a2apb.TaskState_TASK_STATE_COMPLETED,
		Message:   protoCloneMessage(reply),
		Timestamp: timestamppb.New(time.Now().UTC()),
	}
	return clone
}

func (r *Runtime) projectionKeyForMessage(msg *a2apb.Message) string {
	if msg == nil {
		return ""
	}
	return "message:" + msg.GetMessageId()
}

func (r *Runtime) emitHook(ctx context.Context, eventType string, payload map[string]any) {
	if r.hooks == nil {
		return
	}
	_, _ = r.hooks.Emit(ctx, hooks.Event{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      payload,
		Metadata:  map[string]any{"source": "a2a.runtime"},
	})
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, id any, event *a2apb.StreamResponse) error {
	result, err := marshalProto(event)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(jsonRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", envelope); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, id any, err *a2aError) {
	envelope, _ := json.Marshal(jsonRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &jsonRPCError{
			Code:    err.Code,
			Message: err.Message,
		},
	})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", envelope)
	flusher.Flush()
}

func isTerminalTaskState(state a2apb.TaskState) bool {
	switch state {
	case a2apb.TaskState_TASK_STATE_COMPLETED,
		a2apb.TaskState_TASK_STATE_FAILED,
		a2apb.TaskState_TASK_STATE_CANCELED,
		a2apb.TaskState_TASK_STATE_REJECTED:
		return true
	default:
		return false
	}
}

func statusMessage(contextID, taskID, content string) *a2apb.Message {
	return &a2apb.Message{
		MessageId: uuid.NewString(),
		ContextId: contextID,
		TaskId:    taskID,
		Role:      a2apb.Role_ROLE_AGENT,
		Parts: []*a2apb.Part{{
			Content:   &a2apb.Part_Text{Text: content},
			MediaType: "text/plain",
		}},
	}
}

func buildAgentMessage(peer PeerIdentity, contextID, taskID, content string) *a2apb.Message {
	msg := statusMessage(contextID, taskID, content)
	ensurePeerMetadata(msg, peer)
	return msg
}

func conversationMessageToA2A(peer PeerIdentity, contextID, taskID string, msg *conversation.Message) *a2apb.Message {
	if msg == nil {
		return buildAgentMessage(peer, contextID, taskID, "")
	}
	out := &a2apb.Message{
		MessageId: firstNonEmpty(msg.ID, uuid.NewString()),
		ContextId: contextID,
		TaskId:    taskID,
		Role:      a2apb.Role_ROLE_AGENT,
		Parts: []*a2apb.Part{{
			Content:   &a2apb.Part_Text{Text: msg.Content},
			MediaType: "text/plain",
		}},
	}
	ensurePeerMetadata(out, peer)
	return out
}

func ensurePeerMetadata(msg *a2apb.Message, peer PeerIdentity) {
	if msg == nil {
		return
	}
	metadata := map[string]any{}
	if msg.GetMetadata() != nil {
		metadata = msg.GetMetadata().AsMap()
	}
	metadata[MetadataHandleKey] = peer.Handle
	metadata[MetadataSessionIDKey] = peer.SessionID
	if peer.ConversationID != "" {
		metadata[MetadataConversationIDKey] = peer.ConversationID
	}
	msg.Metadata = mapToStruct(metadata)
}

func ensureLocalMetadata(msg *a2apb.Message, peer PeerIdentity) {
	ensurePeerMetadata(msg, peer)
	if strings.TrimSpace(msg.GetContextId()) == "" {
		msg.ContextId = uuid.NewString()
	}
}

func flattenMessageContent(msg *a2apb.Message) (string, []conversation.Reference) {
	if msg == nil {
		return "", nil
	}
	lines := make([]string, 0, len(msg.GetParts()))
	refs := make([]conversation.Reference, 0)
	for _, part := range msg.GetParts() {
		switch content := part.GetContent().(type) {
		case *a2apb.Part_Text:
			if strings.TrimSpace(content.Text) != "" {
				lines = append(lines, content.Text)
			}
		case *a2apb.Part_Data:
			raw, _ := json.MarshalIndent(structValueToAny(content.Data), "", "  ")
			if len(raw) > 0 {
				lines = append(lines, string(raw))
			}
		case *a2apb.Part_Url:
			refs = append(refs, conversation.Reference{
				Type:  "url",
				Value: content.Url,
				Label: part.GetFilename(),
				Metadata: map[string]any{
					"media_type": part.GetMediaType(),
				},
			})
		case *a2apb.Part_Raw:
			refs = append(refs, conversation.Reference{
				Type:  "raw",
				Value: base64.StdEncoding.EncodeToString(content.Raw),
				Label: part.GetFilename(),
				Metadata: map[string]any{
					"media_type": part.GetMediaType(),
					"bytes":      len(content.Raw),
				},
			})
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n")), refs
}

func flattenArtifactText(artifact *a2apb.Artifact) string {
	if artifact == nil {
		return ""
	}
	lines := make([]string, 0, len(artifact.GetParts())+1)
	if artifact.GetName() != "" {
		lines = append(lines, fmt.Sprintf("Artifact: %s", artifact.GetName()))
	}
	for _, part := range artifact.GetParts() {
		switch content := part.GetContent().(type) {
		case *a2apb.Part_Text:
			if strings.TrimSpace(content.Text) != "" {
				lines = append(lines, content.Text)
			}
		case *a2apb.Part_Data:
			raw, _ := json.MarshalIndent(structValueToAny(content.Data), "", "  ")
			if len(raw) > 0 {
				lines = append(lines, string(raw))
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

func artifactReferences(artifact *a2apb.Artifact) []conversation.Reference {
	if artifact == nil {
		return nil
	}
	refs := make([]conversation.Reference, 0)
	for _, part := range artifact.GetParts() {
		switch content := part.GetContent().(type) {
		case *a2apb.Part_Url:
			refs = append(refs, conversation.Reference{
				Type:  "url",
				Value: content.Url,
				Label: firstNonEmpty(part.GetFilename(), artifact.GetName()),
			})
		case *a2apb.Part_Raw:
			refs = append(refs, conversation.Reference{
				Type:  "raw",
				Value: base64.StdEncoding.EncodeToString(content.Raw),
				Label: firstNonEmpty(part.GetFilename(), artifact.GetName()),
				Metadata: map[string]any{
					"media_type": part.GetMediaType(),
					"bytes":      len(content.Raw),
				},
			})
		}
	}
	return refs
}

func structValueToAny(value *structpb.Value) any {
	if value == nil {
		return nil
	}
	return value.AsInterface()
}

func mapToStruct(values map[string]any) *structpb.Struct {
	if len(values) == 0 {
		return nil
	}
	out, err := structpb.NewStruct(values)
	if err != nil {
		return nil
	}
	return out
}

func protoCloneTask(task *a2apb.Task) *a2apb.Task {
	if task == nil {
		return nil
	}
	return trimTask(task, nil, true)
}

func protoCloneMessage(msg *a2apb.Message) *a2apb.Message {
	if msg == nil {
		return nil
	}
	return proto.Clone(msg).(*a2apb.Message)
}

func protoCloneSendRequest(req *a2apb.SendMessageRequest) *a2apb.SendMessageRequest {
	if req == nil {
		return nil
	}
	clone := proto.Clone(req).(*a2apb.SendMessageRequest) //nolint:copylocks
	clone.Message = protoCloneMessage(req.GetMessage())
	if req.GetMetadata() != nil {
		clone.Metadata = mapToStruct(req.GetMetadata().AsMap())
	}
	if cfg := req.GetConfiguration(); cfg != nil {
		cfgClone := proto.Clone(cfg).(*a2apb.SendMessageConfiguration) //nolint:copylocks
		cfgClone.AcceptedOutputModes = append([]string(nil), cfg.GetAcceptedOutputModes()...)
		if cfg.HistoryLength != nil {
			value := *cfg.HistoryLength
			cfgClone.HistoryLength = &value
		}
		clone.Configuration = cfgClone
	}
	return clone
}

func protoCloneStreamResponse(resp *a2apb.StreamResponse) *a2apb.StreamResponse {
	if resp == nil {
		return nil
	}
	raw, _ := marshalProto(resp)
	out := &a2apb.StreamResponse{}
	_ = unmarshalProto(raw, out)
	return out
}

// ============================================================================
// Swarm Chatroom Interface Implementation
// ============================================================================

// SetSwarmCallback sets the callback for swarm events.
func (r *Runtime) SetSwarmCallback(cb SwarmCallback) {
	r.mu.Lock()
	r.swarmCallback = cb
	r.mu.Unlock()
}

// SwarmEvents returns the channel for swarm events (for polling).
func (r *Runtime) SwarmEvents() <-chan SwarmEvent {
	return r.swarmEventQueue
}

// emitSwarmEvent emits a swarm event to the callback and/or event queue.
func (r *Runtime) emitSwarmEvent(event SwarmEvent) {
	event.Timestamp = time.Now()

	// Try callback first
	r.mu.Lock()
	cb := r.swarmCallback
	r.mu.Unlock()

	if cb != nil {
		cb(event)
	}

	// Also push to queue (non-blocking)
	select {
	case r.swarmEventQueue <- event:
	default:
		// Queue full, drop oldest
		select {
		case <-r.swarmEventQueue:
			r.swarmEventQueue <- event
		default:
		}
	}
}

// ListPeers returns all connected peer agents with their status.
func (r *Runtime) ListPeers(ctx context.Context) (*SwarmListPeersResult, error) {
	// Use filesystem-based discovery
	swarmName := r.swarmName
	if swarmName == "" {
		swarmName = DefaultSwarmName
	}

	peerPresences, err := ListPeers(swarmName)
	if err != nil {
		return nil, fmt.Errorf("list peers: %w", err)
	}

	result := &SwarmListPeersResult{
		Peers: make([]SwarmPeerInfo, 0, len(peerPresences)),
	}

	for _, p := range peerPresences {
		if p.Handle == r.peer.Handle {
			continue // Skip self
		}

		// Parse swarm status from peer
		status := SwarmStatusIdle
		if p.Status != "" {
			status = SwarmStatus(p.Status)
		}

		result.Peers = append(result.Peers, SwarmPeerInfo{
			Handle:      p.Handle,
			Model:       p.Model,
			Status:      status,
			CurrentTask: p.CurrentTask,
			RunningTime: "", // Not stored in presence
			Endpoint:    p.EndpointURL,
		})
	}

	// Add self status
	r.mu.Lock()
	selfStatus := r.swarmStatus
	r.mu.Unlock()
	selfStatus.RunningTime = selfStatus.RunningTimeDuration()
	result.Self = &selfStatus

	return result, nil
}

// SendDM sends a direct message to a specific peer over WebSocket.
//
// Async-first semantics: SendDM does NOT wait for the recipient's agent to
// produce a reply. The recipient ACKs immediately with a Task{state: WORKING}
// (because the request carries ReturnImmediately=true). When the agent
// finishes, it sends its reply back as a *reverse-DM* — a fresh inbound A2A
// message that lands in the original conversation via referenceTaskIds. See
// plan.md and spawnAsyncTask for the full picture.
//
// Transport behaviour:
//   - Always writes the message to the peer's inbox first (offline durability).
//   - Then attempts delivery via the pooled WebSocket connection using
//     SendRequest with a short timeout, so we can capture the task ACK and
//     populate the result's TaskID. If the ACK times out, the message is
//     still delivered (the wsPool's read loop will eventually receive the
//     peer's task response and route it as a normal inbound).
//   - On WS failure, the inbox copy still wins and the result reports
//     Status="queued" with QueuedReason set to the WS error.
//   - On WS success, returns Status="sent" with TaskID populated when the
//     peer's ACK arrived in time.
func (r *Runtime) SendDM(ctx context.Context, peerHandle, message string) (*SwarmDMResult, error) {
	return r.sendDMInternal(ctx, peerHandle, message, nil)
}

// sendReplyDM is the reverse-DM path used by spawnAsyncTask: the recipient
// completed its agent execution and is now sending the reply back to the
// original DM sender. The only difference from SendDM is the referenceTaskIds,
// which let the sender's runtime correlate this reply to the original
// outbound task via RemoteTaskBinding.
func (r *Runtime) sendReplyDM(ctx context.Context, peerHandle, message, origTaskID string) error {
	var refs []string
	if strings.TrimSpace(origTaskID) != "" {
		refs = []string{origTaskID}
	}
	_, err := r.sendDMInternal(ctx, peerHandle, message, refs)
	return err
}

// sendDMInternal is the shared implementation behind SendDM and sendReplyDM.
// referenceTaskIds is non-nil only for the reverse-DM path.
func (r *Runtime) sendDMInternal(ctx context.Context, peerHandle, message string, referenceTaskIds []string) (*SwarmDMResult, error) {
	swarmName := r.swarmName
	if swarmName == "" {
		swarmName = DefaultSwarmName
	}

	targetPeer, err := GetPeer(swarmName, peerHandle)
	if err != nil {
		return nil, fmt.Errorf("get peer: %w", err)
	}
	if targetPeer == nil {
		return nil, fmt.Errorf("peer %q not found", peerHandle)
	}

	// Always durably enqueue to inbox first — offline-safe delivery.
	inboxErr := SendMessage(swarmName, peerHandle, r.peer.Handle, message)

	// Build the A2A request payload (same shape as the HTTP path used).
	r.mu.Lock()
	selfStatus := r.swarmStatus
	r.mu.Unlock()

	msgID := fmt.Sprintf("dm-%s", uuid.New().String()[:8])
	a2aMsg := &a2apb.Message{
		MessageId: msgID,
		Role:      a2apb.Role_ROLE_USER,
		Parts: []*a2apb.Part{
			{Content: &a2apb.Part_Text{Text: message}},
		},
		Metadata: mapToStruct(map[string]any{
			SwarmTypeKey:      SwarmTypeDM,
			MetadataHandleKey: r.peer.Handle,
			"from_handle":     r.peer.Handle,
			"from_model":      selfStatus.Model,
			"from_task":       selfStatus.CurrentTask,
			"from_status":     string(selfStatus.Status),
		}),
		ReferenceTaskIds: referenceTaskIds,
	}
	req := &a2apb.SendMessageRequest{
		Message: a2aMsg,
		// ReturnImmediately tells the recipient to ACK with Task{state: WORKING}
		// and process the message asynchronously. This is what makes peer
		// messaging non-blocking even when the recipient's agent takes minutes.
		Configuration: &a2apb.SendMessageConfiguration{
			ReturnImmediately: true,
		},
	}

	reqPayload, err := protojson.Marshal(req)
	if err != nil {
		if inboxErr == nil {
			return &SwarmDMResult{
				Status:       "queued",
				QueuedReason: fmt.Sprintf("marshal failed: %v", err),
				PeerHandle:   peerHandle,
			}, nil
		}
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	wsMsg := WebSocketMessage{
		Type:      "request",
		ID:        msgID,
		Method:    "SendMessage",
		Payload:   reqPayload,
		Timestamp: time.Now(),
	}

	if r.wsPool == nil {
		// No WS pool (test/embedded mode). Inbox copy is the delivery.
		if inboxErr != nil {
			return nil, fmt.Errorf("inbox enqueue failed: %w", inboxErr)
		}
		return &SwarmDMResult{
			Status:     "queued",
			PeerHandle: peerHandle,
		}, nil
	}

	// Try a short-timeout SendRequest to capture the peer's Task ACK. We
	// limit this to a couple of seconds because the recipient's
	// handleSendMessage returns the Task{state: WORKING} synchronously for
	// async-path requests (no agent execution involved at this stage).
	// If the ACK doesn't arrive in time, we still consider the message
	// delivered — the WS write itself succeeded, and the recipient is now
	// processing it.
	ackCtx, ackCancel := context.WithTimeout(ctx, 3*time.Second)
	defer ackCancel()
	resp, sendErr := r.wsPool.SendRequest(ackCtx, peerHandle, targetPeer.EndpointURL, wsMsg)
	if sendErr != nil {
		// Distinguish "couldn't deliver" from "delivered but no ACK in time".
		// A context-deadline error means the WS write succeeded but the
		// peer's response didn't come back fast enough. The message is
		// still en route — treat as sent without a TaskID.
		if ackCtx.Err() == context.DeadlineExceeded {
			r.logger.Debug(ctx, "a2a.send_dm_ack_timeout",
				observability.F("peer", peerHandle),
				observability.F("msg_id", msgID))
			return &SwarmDMResult{
				Status:     "sent",
				PeerHandle: peerHandle,
				PeerStatus: targetPeer.Status,
				PeerTask:   targetPeer.CurrentTask,
			}, nil
		}
		r.logger.Warn(ctx, "a2a.send_dm_ws_failed",
			observability.F("peer", peerHandle),
			observability.F("error", sendErr.Error()))
		if inboxErr == nil {
			return &SwarmDMResult{
				Status:       "queued",
				QueuedReason: fmt.Sprintf("peer unreachable: %v", sendErr),
				PeerHandle:   peerHandle,
			}, nil
		}
		return nil, fmt.Errorf("send message to peer: %w", sendErr)
	}

	// Parse the peer's response to extract the TaskID. The payload is a
	// protojson-encoded SendMessageResponse with payload kind = Task.
	result := &SwarmDMResult{
		Status:     "sent",
		PeerHandle: peerHandle,
		PeerStatus: targetPeer.Status,
		PeerTask:   targetPeer.CurrentTask,
	}
	if resp != nil && len(resp.Payload) > 0 {
		var sendResp a2apb.SendMessageResponse
		if err := protojson.Unmarshal(resp.Payload, &sendResp); err == nil {
			if task := sendResp.GetTask(); task != nil {
				result.TaskID = task.GetId()
			}
		}
	}
	return result, nil
}

// Broadcast sends a message to all connected peers.
func (r *Runtime) Broadcast(ctx context.Context, message string) (*SwarmBroadcastResult, error) {
	// Use filesystem-based discovery to get peers
	swarmName := r.swarmName
	if swarmName == "" {
		swarmName = DefaultSwarmName
	}

	peers, err := ListPeers(swarmName)
	if err != nil {
		return nil, fmt.Errorf("list peers: %w", err)
	}

	// Build message with swarm metadata
	r.mu.Lock()
	selfStatus := r.swarmStatus
	r.mu.Unlock()

	msg := &a2apb.Message{
		MessageId: fmt.Sprintf("broadcast-%s", uuid.New().String()[:8]),
		Role:      a2apb.Role_ROLE_USER,
		Parts: []*a2apb.Part{
			{
				Content: &a2apb.Part_Text{Text: message},
			},
		},
		Metadata: mapToStruct(map[string]any{
			SwarmTypeKey:      SwarmTypeBroadcast,
			MetadataHandleKey: r.peer.Handle, // Required for resolveRemotePeer
			"from_handle":     r.peer.Handle,
			"from_model":      selfStatus.Model,
			"from_task":       selfStatus.CurrentTask,
			"from_status":     string(selfStatus.Status),
		}),
	}

	req := &a2apb.SendMessageRequest{
		Message: msg,
		// Broadcasts are async/inject-only — recipients project the message
		// into their conversation but do not auto-execute their agent. See
		// plan.md "Phase D: scaling fixes" and the TUI request handler's
		// L1+broadcast guard for details.
		Configuration: &a2apb.SendMessageConfiguration{
			ReturnImmediately: true,
		},
	}

	// Marshal once; reuse the payload for every peer.
	reqPayload, marshalErr := protojson.Marshal(req)
	if marshalErr != nil {
		return nil, fmt.Errorf("marshal broadcast request: %w", marshalErr)
	}

	// Fan-out in parallel — sequential delivery to N peers with up to 3s WS
	// timeout each makes broadcast latency O(N×3s) in the worst case.
	// Parallelising keeps it O(3s) regardless of peer count.
	var (
		wg          sync.WaitGroup
		recipientMu sync.Mutex
		recipients  int
	)
	for _, p := range peers {
		if p.Handle == r.peer.Handle {
			continue // Skip self
		}
		peer := p // shadow for goroutine closure

		// Write to inbox synchronously for offline durability. Inbox writes
		// are fast (append-only NDJSON, see discovery.go) so we don't pay
		// the network-latency cost here.
		_ = SendMessage(swarmName, peer.Handle, r.peer.Handle, message)

		if r.wsPool == nil {
			continue
		}

		wg.Go(func() {
			wsMsg := WebSocketMessage{
				Type:      "request",
				ID:        fmt.Sprintf("bcast-%s-%s", uuid.New().String()[:8], peer.Handle),
				Method:    "SendMessage",
				Payload:   reqPayload,
				Timestamp: time.Now(),
			}
			if err := r.wsPool.SendFireAndForget(ctx, peer.Handle, peer.EndpointURL, wsMsg); err != nil {
				r.logger.Warn(ctx, "swarm.broadcast_failed",
					observability.F("peer", peer.Handle),
					observability.F("error", err.Error()))
				return
			}
			recipientMu.Lock()
			recipients++
			recipientMu.Unlock()
		})
	}
	wg.Wait()

	return &SwarmBroadcastResult{
		Status:     "sent",
		Recipients: recipients,
	}, nil
}

// UpdateStatus updates this agent's status visible to peers.
func (r *Runtime) UpdateStatus(ctx context.Context, status SwarmStatus, task string) (*SwarmUpdateStatusResult, error) {
	r.mu.Lock()
	previousTask := r.swarmStatus.CurrentTask
	previousStarted := r.swarmStatus.TaskStarted

	// Update status
	r.swarmStatus.Status = status
	r.swarmStatus.CurrentTask = task

	// Reset timer if task changed
	if task != previousTask || status == SwarmStatusIdle {
		r.swarmStatus.TaskStarted = time.Now()
	} else if previousStarted.IsZero() {
		r.swarmStatus.TaskStarted = time.Now()
	}

	runningTime := r.swarmStatus.RunningTimeDuration()
	r.mu.Unlock()

	// Update peer metadata for discovery
	if r.peer.Metadata == nil {
		r.peer.Metadata = make(map[string]any)
	}
	r.peer.Metadata["swarm_status"] = string(status)
	r.peer.Metadata["swarm_task"] = task
	r.peer.Metadata["swarm_running_time"] = runningTime

	// Update status in filesystem-based discovery
	swarmName := r.swarmName
	if swarmName == "" {
		swarmName = DefaultSwarmName
	}
	if err := UpdateStatus(swarmName, r.peer.Handle, string(status), task); err != nil {
		// Log but don't fail - status update is best-effort
		r.logger.Warn(ctx, "a2a.update_status_failed", observability.F("error", err.Error()))
	}

	return &SwarmUpdateStatusResult{
		Status:       "updated",
		PreviousTask: previousTask,
		RunningTime:  runningTime,
	}, nil
}

// GetSwarmStatus returns the current swarm status.
func (r *Runtime) GetSwarmStatus() AgentSwarmStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := r.swarmStatus
	status.RunningTime = status.RunningTimeDuration()
	return status
}

// SetSwarmModel sets the model name for the agent.
func (r *Runtime) SetSwarmModel(model string) {
	r.mu.Lock()
	r.swarmStatus.Model = model
	r.mu.Unlock()
}

// handleSwarmMessage processes incoming messages that have swarm metadata.
func (r *Runtime) handleSwarmMessage(msg *a2apb.Message) bool {
	metadata := msg.GetMetadata().AsMap()
	swarmType, _ := metadata["swarm_type"].(string)
	if swarmType == "" {
		return false // Not a swarm message
	}

	fromHandle, _ := metadata["from_handle"].(string)
	fromModel, _ := metadata["from_model"].(string)
	fromTask, _ := metadata["from_task"].(string)

	// Extract text content
	var text string
	for _, part := range msg.GetParts() {
		if t := part.GetText(); t != "" {
			text = t
			break
		}
	}

	switch swarmType {
	case "dm":
		r.emitSwarmEvent(SwarmEvent{
			Type:      SwarmEventDM,
			From:      fromHandle,
			FromModel: fromModel,
			FromTask:  fromTask,
			Message:   text,
		})
		return true
	case "broadcast":
		r.emitSwarmEvent(SwarmEvent{
			Type:      SwarmEventBroadcast,
			From:      fromHandle,
			FromModel: fromModel,
			FromTask:  fromTask,
			Message:   text,
		})
		return true
	}

	return false
}

// WebSocket connection management methods

// ConnectToPeerWebSocket establishes a WebSocket connection to a peer
func (r *Runtime) ConnectToPeerWebSocket(ctx context.Context, handle string, endpointURL string) error {
	if r.wsClient == nil {
		return fmt.Errorf("websocket client not initialized")
	}
	_, err := r.wsClient.Connect(ctx, handle, endpointURL)
	return err
}

// SendWebSocketMessage sends a message to a peer via WebSocket
func (r *Runtime) SendWebSocketMessage(handle string, msg *conversation.Message) error {
	if r.wsServer == nil {
		return fmt.Errorf("websocket server not initialized")
	}
	return r.wsServer.SendMessage(handle, msg)
}

// HasWebSocketConnection checks if a peer is connected via WebSocket
func (r *Runtime) HasWebSocketConnection(handle string) bool {
	if r.wsServer == nil {
		return false
	}
	return r.wsServer.HasConnection(handle)
}

// DisconnectWebSocketPeer closes WebSocket connection to a peer
func (r *Runtime) DisconnectWebSocketPeer(handle string) error {
	if r.wsClient == nil {
		return nil
	}
	return r.wsClient.Disconnect(handle)
}
