package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/approval"
)

// Server implements the ACP agent-side server.
// It bridges the Swarm headless engine to the Agent Client Protocol,
// allowing editors like Zed to communicate with the Swarm agent.
type Server struct {
	engine   *core.Engine
	config   core.ConfigManager
	broker   *approval.ApprovalBroker
	reader   io.Reader
	writer   io.Writer
	sessions *sessionRegistry
	// clientCaps stores the capabilities negotiated during initialize.
	clientCaps ClientCapabilities
	// initialized is true once the initialize handshake is complete.
	initialized bool
	// initMu guards clientCaps and initialized.
	initMu sync.Mutex
	// writeMu serializes all writes to writer.
	writeMu sync.Mutex
	// activeMu guards the single in-flight session ID used for routing updates.
	activeMu        sync.Mutex
	activeSessionID string
	// outboundSeq provides monotonically increasing IDs for agent-initiated requests.
	outboundSeq atomic.Int64
	// pendingOutbound tracks channels waiting for responses to agent-initiated requests.
	pendingMu       sync.Mutex
	pendingOutbound map[int64]chan json.RawMessage

	// ── Enriched capabilities (optional; populated by NewServer) ─────────────
	// toolNames is the list of registered tool names for /tools.
	toolNames []string
	// agentOptions is the list of available agent profiles.
	agentOptions []AgentOption
	// providers is the ordered list of configured provider names.
	providers []string
	// providerModels maps provider name → its model list for the model dropdown.
	providerModels map[string][]ModelOptionValue
	// goalHook is the registered /goal stop-hook (may be nil if not wired).
	goalHook *builtin.GoalHook
}

// ServerConfig holds all dependencies and optional enrichment for NewServer.
type ServerConfig struct {
	Engine *core.Engine
	Config core.ConfigManager
	Broker *approval.ApprovalBroker
	Reader io.Reader
	Writer io.Writer
	// ToolNames is the list of registered tool names (shown by /tools).
	ToolNames []string
	// AgentOptions is the list of available agent profiles.
	AgentOptions []AgentOption
	// Providers is the ordered list of configured provider names.
	Providers []string
	// ProviderModels maps provider name → sorted model list.
	ProviderModels map[string][]ModelOptionValue
	// GoalHook is the registered /goal stop-hook used by the /goal command.
	GoalHook *builtin.GoalHook
}

// NewServer creates a new ACP server from the given config.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		engine:          cfg.Engine,
		config:          cfg.Config,
		broker:          cfg.Broker,
		reader:          cfg.Reader,
		writer:          cfg.Writer,
		sessions:        newSessionRegistry(),
		pendingOutbound: make(map[int64]chan json.RawMessage),
		toolNames:       cfg.ToolNames,
		agentOptions:    cfg.AgentOptions,
		providers:       cfg.Providers,
		providerModels:  cfg.ProviderModels,
		goalHook:        cfg.GoalHook,
	}
	if cfg.Engine != nil {
		cfg.Engine.SetRenderer(s)
		s.wireApprovalBroker()
	}
	return s
}

// ── core.Renderer implementation ─────────────────────────────────────────────

// OnStateUpdate is called by the engine whenever state changes.
// We translate the update into the appropriate ACP notification.
func (s *Server) OnStateUpdate(update core.StateUpdate) {
	s.routeEngineUpdate(update)
}

// GetCurrentState implements core.Renderer.
func (s *Server) GetCurrentState() *core.AppState {
	return s.engine.GetStatePtr()
}

// Flush implements core.Renderer — nothing to flush for line-delimited JSON.
func (s *Server) Flush() {}

// ── Start / read loop ─────────────────────────────────────────────────────────

// Start begins reading newline-delimited JSON-RPC messages from the reader and
// dispatching them. It blocks until the reader is closed or ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	reader := bufio.NewReader(s.reader)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if err == io.EOF && len(line) == 0 {
			return io.EOF
		}

		line = strings.TrimSpace(line)
		if line == "" {
			if err == io.EOF {
				return io.EOF
			}
			continue
		}

		// Parse the raw message to determine whether it is:
		//   (a) a request or notification from the client → dispatch as ACP method
		//   (b) a response from the client → route to a pending outbound request
		var raw struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Result  json.RawMessage `json:"result"`
			Error   *RPCError       `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			s.writeError(nil, ErrParseError, "parse error", err.Error())
			continue
		}

		// Responses from the client (to agent-initiated requests) have no Method.
		if raw.Method == "" {
			s.routeClientResponse(raw.ID, raw.Result, raw.Error)
			continue
		}

		req := &Request{
			JSONRPC: raw.JSONRPC,
			ID:      raw.ID,
			Method:  raw.Method,
			Params:  nil,
		}

		// Re-parse params from the original line.
		var withParams struct {
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &withParams); err != nil {
			s.writeError(raw.ID, ErrParseError, "failed to parse params", err.Error())
			continue
		}
		req.Params = withParams.Params

		// Dispatch in a goroutine so notifications are non-blocking and
		// long-running prompt turns do not block subsequent messages.
		go s.dispatch(ctx, req)
		if err == io.EOF {
			return io.EOF
		}
	}
}

// Stop cancels all active sessions.
func (s *Server) Stop() {
	s.sessions.mu.RLock()
	sessions := make([]*session, 0, len(s.sessions.sessions))
	for _, sess := range s.sessions.sessions {
		sessions = append(sessions, sess)
	}
	s.sessions.mu.RUnlock()

	for _, sess := range sessions {
		sess.cancel()
	}
}

// ── Dispatcher ────────────────────────────────────────────────────────────────

func (s *Server) dispatch(ctx context.Context, req *Request) {
	var (
		result any
		rpcErr *RPCError
	)

	switch req.Method {
	// ── Lifecycle ──────────────────────────────────────────────────────
	case "initialize":
		result, rpcErr = s.handleInitialize(req.Params)

	// ── Session management ──────────────────────────────────────────────
	case "session/new":
		result, rpcErr = s.handleSessionNew(ctx, req.Params)
	case "session/load":
		result, rpcErr = s.handleSessionLoad(ctx, req.Params)
	case "session/set_mode":
		result, rpcErr = s.handleSessionSetMode(req.Params)

	// ── Prompt execution ────────────────────────────────────────────────
	case "session/prompt":
		result, rpcErr = s.handleSessionPrompt(ctx, req.Params)

	// ── Config & discovery ──────────────────────────────────────────────
	case "session/set_config":
		result, rpcErr = s.handleSessionSetConfig(req.Params)
	case "session/set_config_option":
		result, rpcErr = s.handleSessionSetConfigOption(req.Params)
	case "slash_command/list":
		result, rpcErr = s.handleSlashCommandList()
	case "slash_command/run":
		result, rpcErr = s.handleSlashCommandRun(ctx, req.Params)

	// ── Notifications (no response) ────────────────────────────────────
	case "session/cancel":
		s.handleSessionCancel(req.Params)
		return

	default:
		if req.IsNotification() {
			return
		}
		rpcErr = &RPCError{Code: ErrMethodNotFound, Message: fmt.Sprintf("unknown method: %s", req.Method)}
	}

	if req.IsNotification() {
		return
	}

	if rpcErr != nil {
		s.writeError(req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
		return
	}
	s.writeResponse(req.ID, result)

	// Post-response notifications — sent after the JSON-RPC response so the
	// client processes the result before receiving the follow-up notification.
	switch req.Method {
	case "session/new":
		if r, ok := result.(*SessionNewResult); ok {
			s.sendAvailableCommandsUpdate(r.SessionID)
		}
	}
}

// ── initialize ────────────────────────────────────────────────────────────────

func (s *Server) handleInitialize(params json.RawMessage) (any, *RPCError) {
	var p InitializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid params", Data: err.Error()}
		}
	}

	// Record the client capabilities so we can honour them during prompts
	// (e.g. skip image content if the client can't display it).
	s.initMu.Lock()
	s.clientCaps = p.ClientCapabilities
	s.initialized = true
	s.initMu.Unlock()

	return &InitializeResult{
		ProtocolVersion:   ProtocolVersion(1),
		AgentCapabilities: agentCapabilities,
		AgentInfo:         agentInfo,
	}, nil
}

// ── session/new ───────────────────────────────────────────────────────────────

func (s *Server) handleSessionNew(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p SessionNewParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid params", Data: err.Error()}
		}
	}

	if s.engine == nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "engine not initialized"}
	}

	// Create a new conversation in the engine.
	convID, err := s.engine.CreateConversation(ctx, core.CreateConversationOptions{})
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "failed to create conversation", Data: err.Error()}
	}

	sess := newSession(convID, p.CWD)
	s.sessions.add(sess)

	cfgOpts := s.buildConfigOptions(sess)
	currentMode := s.currentModeID()

	return &SessionNewResult{
		SessionID: sess.id,
		Modes: SessionModes{
			CurrentModeID:  currentMode,
			AvailableModes: availableModes,
		},
		ConfigOptions: cfgOpts,
	}, nil
}

// ── session/load ──────────────────────────────────────────────────────────────

func (s *Server) handleSessionLoad(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p SessionLoadParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid params", Data: err.Error()}
	}

	if s.engine == nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "engine not initialized"}
	}

	// Try to resume an existing engine conversation by its ID.
	// If the conversation no longer exists, create a fresh one.
	convID := p.SessionID
	if _, err := s.engine.LoadConversation(ctx, convID); err != nil {
		// Conversation not found — create a new one and keep the requested ID
		// so the client's session reference stays valid.
		newID, cerr := s.engine.CreateConversation(ctx, core.CreateConversationOptions{})
		if cerr != nil {
			return nil, &RPCError{Code: ErrInternalError, Message: "failed to create conversation", Data: cerr.Error()}
		}
		convID = newID
	}

	sess := &session{
		id:     p.SessionID,
		convID: convID,
		cwd:    p.CWD,
	}
	// Remove any stale entry with this ID before re-registering.
	s.sessions.remove(p.SessionID)
	s.sessions.add(sess)

	currentMode := s.currentModeID()
	return &SessionLoadResult{
		SessionID: sess.id,
		Modes: SessionModes{
			CurrentModeID:  currentMode,
			AvailableModes: availableModes,
		},
		ConfigOptions: s.buildConfigOptions(sess),
	}, nil
}

// ── session/set_mode ──────────────────────────────────────────────────────────

func (s *Server) handleSessionSetMode(params json.RawMessage) (any, *RPCError) {
	var p SessionSetModeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid params", Data: err.Error()}
	}

	sess, ok := s.sessions.get(p.SessionID)
	if !ok {
		return nil, &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("unknown sessionId: %s", p.SessionID)}
	}

	validMode := false
	for _, mode := range availableModes {
		if mode.ID == p.ModeID {
			validMode = true
			break
		}
	}
	if !validMode {
		return nil, &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("unsupported modeId: %s", p.ModeID)}
	}

	sess.applyConfig(map[string]string{"mode": p.ModeID})

	// Map ACP mode ID → Swarm engine operating mode.
	engineMode := acpModeToEngineMode(p.ModeID)
	modeEvent := core.NewInputEvent(core.InputSetMode)
	modeEvent.Content = engineMode
	s.engine.SendEvent(modeEvent)

	// Confirm via a session/update so the editor UI can reflect the change.
	s.writeNotification("session/update", SessionUpdateParams{
		SessionID: sess.id,
		Update: SessionUpdate{
			SessionUpdateType: "current_mode_update",
			CurrentModeID:     p.ModeID,
		},
	})

	return &SessionSetModeResult{CurrentModeID: p.ModeID}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// currentModeID returns the current ACP mode ID based on the engine config.
func (s *Server) currentModeID() string {
	if s.config != nil {
		if cfg := s.config.GetConfig(); cfg != nil {
			switch cfg.DefaultMode {
			case "plan":
				return "architect"
			case "act", "auto":
				return "code"
			}
		}
	}
	return "code"
}

// acpModeToEngineMode maps an ACP mode ID to a Swarm engine operating mode.
func acpModeToEngineMode(modeID string) string {
	switch modeID {
	case "architect":
		return "plan"
	case "ask":
		return "act"
	case "code":
		return "act"
	default:
		return "act"
	}
}

// buildConfigOptions assembles the full set of ConfigOptions for a session,
// reflecting per-session overrides and falling back to global defaults.
func (s *Server) buildConfigOptions(sess *session) []ConfigOption {
	var opts []ConfigOption

	// Mode
	modeID := "code"
	if sess != nil {
		if _, _, _, m := sess.getConfig(); m != "" {
			modeID = m
		} else {
			modeID = s.currentModeID()
		}
	} else {
		modeID = s.currentModeID()
	}
	opts = append(opts, modeConfigOption(modeID))

	// Provider
	if len(s.providers) > 0 {
		provID := s.currentProvider(sess)
		opts = append(opts, providerConfigOption(provID, s.providers))
	}

	// Model
	currentModel := s.currentModel(sess)
	var providerModelList []ModelOptionValue
	if sess != nil {
		if prov, _, _, _ := sess.getConfig(); prov != "" && s.providerModels != nil {
			providerModelList = s.providerModels[prov]
		}
	}
	if providerModelList == nil && s.providerModels != nil {
		providerModelList = s.providerModels[s.currentProvider(sess)]
	}
	opts = append(opts, modelConfigOption(currentModel, providerModelList))

	// Agent
	if len(s.agentOptions) > 0 {
		agentID := s.currentAgentID(sess)
		opts = append(opts, agentConfigOption(agentID, s.agentOptions))
	}

	return opts
}

// ── session/set_config ────────────────────────────────────────────────────────

func (s *Server) handleSessionSetConfig(params json.RawMessage) (any, *RPCError) {
	var p SessionSetConfigParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid params", Data: err.Error()}
	}
	sess, ok := s.sessions.get(p.SessionID)
	if !ok {
		return nil, &RPCError{
			Code:    ErrInvalidParams,
			Message: fmt.Sprintf("unknown sessionId: %s", p.SessionID),
		}
	}
	if modeID, ok := p.Updates["mode"]; ok {
		valid := false
		for _, mode := range availableModes {
			if mode.ID == modeID {
				valid = true
				break
			}
		}
		if !valid {
			return nil, &RPCError{
				Code:    ErrInvalidParams,
				Message: fmt.Sprintf("unsupported modeId: %s", modeID),
			}
		}
	}

	changed := sess.applyConfig(p.Updates)

	if s.engine != nil {
		for _, field := range changed {
			switch field {
			case "model":
				ev := core.NewInputEvent(core.InputSetModel)
				ev.Content = p.Updates["model"]
				s.engine.SendEvent(ev)
			case "provider":
				ev := core.NewInputEvent(core.InputSetProvider)
				ev.Content = p.Updates["provider"]
				s.engine.SendEvent(ev)
			case "agent":
				s.engine.SetActiveAgent(p.Updates["agent"])
			case "mode":
				ev := core.NewInputEvent(core.InputSetMode)
				ev.Content = acpModeToEngineMode(p.Updates["mode"])
				s.engine.SendEvent(ev)
			}
		}
	}

	return &SessionSetConfigResult{ConfigOptions: s.buildConfigOptions(sess)}, nil
}

// ── session/set_config_option (ZED ACP compliant) ──────────────────────────

func (s *Server) handleSessionSetConfigOption(params json.RawMessage) (any, *RPCError) {
	var p SessionSetConfigOptionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid params", Data: err.Error()}
	}

	sess, ok := s.sessions.get(p.SessionID)
	if !ok {
		return nil, &RPCError{
			Code:    ErrInvalidParams,
			Message: fmt.Sprintf("unknown sessionId: %s", p.SessionID),
		}
	}

	// Convert value to string for internal storage.
	valueStr, ok := stringValue(p.Value)
	if !ok {
		return nil, &RPCError{
			Code:    ErrInvalidParams,
			Message: fmt.Sprintf("invalid value for configId %q: must be a string", p.ConfigID),
		}
	}

	// Validate configId.
	switch p.ConfigID {
	case "provider", "model", "agent", "mode":
		// valid
	default:
		return nil, &RPCError{
			Code:    ErrInvalidParams,
			Message: fmt.Sprintf("unknown configId: %s", p.ConfigID),
		}
	}

	changed := sess.applyConfig(map[string]string{p.ConfigID: valueStr})

	if s.engine != nil {
		for _, field := range changed {
			switch field {
			case "model":
				ev := core.NewInputEvent(core.InputSetModel)
				ev.Content = valueStr
				s.engine.SendEvent(ev)
			case "provider":
				ev := core.NewInputEvent(core.InputSetProvider)
				ev.Content = valueStr
				s.engine.SendEvent(ev)
			case "agent":
				s.engine.SetActiveAgent(valueStr)
			case "mode":
				ev := core.NewInputEvent(core.InputSetMode)
				ev.Content = acpModeToEngineMode(valueStr)
				s.engine.SendEvent(ev)
			}
		}
	}

	return &SessionSetConfigOptionResult{ConfigOptions: s.buildConfigOptions(sess)}, nil
}

// stringValue converts an any value to a string.
// Supports string, float64 (from JSON numbers), and bool.
func stringValue(v any) (string, bool) {
	switch val := v.(type) {
	case string:
		return val, true
	case float64:
		return fmt.Sprintf("%v", val), true
	case bool:
		return fmt.Sprintf("%v", val), true
	default:
		return "", false
	}
}

// ── session/prompt ────────────────────────────────────────────────────────────

func (s *Server) handleSessionPrompt(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p SessionPromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid params", Data: err.Error()}
	}

	sess, ok := s.sessions.get(p.SessionID)
	if !ok {
		return nil, &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("unknown sessionId: %s", p.SessionID)}
	}

	// Build the prompt text. Current ACP clients send `prompt`; retain the
	// legacy `content` fallback for older integrations.
	blocks := p.promptBlocks()
	if len(blocks) == 0 {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "prompt must contain at least one content block"}
	}
	content := buildPromptText(blocks)

	// Make sure the engine is working in the right conversation.
	s.engine.SendEvent(core.NewSwitchConvEvent(sess.convID))

	if !s.setActiveSession(sess.id) {
		return nil, &RPCError{Code: ErrInternalError, Message: "another prompt is already in flight"}
	}

	// Register the in-flight prompt turn so OnStateUpdate can route updates.
	promptCtx, promptCancel := context.WithCancel(ctx)
	doneCh, err := sess.startPrompt(promptCancel)
	if err != nil {
		s.clearActiveSession()
		promptCancel()
		return nil, &RPCError{Code: ErrInternalError, Message: err.Error()}
	}
	defer s.clearActiveSession()
	defer promptCancel()

	// Fire the message into the engine.
	ev := core.NewMessageEvent(content)
	ev.ConvID = sess.convID
	s.engine.SendEvent(ev)

	// Block until the stream ends or the context is cancelled.
	var result promptResult
	select {
	case result = <-doneCh:
	case <-promptCtx.Done():
		sess.finishPrompt(promptResult{stopReason: "cancelled"})
		result = promptResult{stopReason: "cancelled"}
	}

	if result.err != nil {
		// Per ACP spec, session/prompt must always return a stopReason.
		// Return "error" as the stop reason rather than a JSON-RPC error object.
		return &SessionPromptResult{StopReason: "error"}, nil
	}
	return &SessionPromptResult{StopReason: result.stopReason}, nil
}

// ── session/cancel ────────────────────────────────────────────────────────────

func (s *Server) handleSessionCancel(params json.RawMessage) {
	var p SessionCancelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	sess, ok := s.sessions.get(p.SessionID)
	if !ok {
		return
	}
	active := s.getActiveSession()
	if active == nil || active.id != sess.id {
		return
	}
	sess.cancel()
	if s.engine != nil {
		s.engine.SendEvent(core.NewInputEvent(core.InputCancel))
	}
}

// ── Active session tracking ───────────────────────────────────────────────────

func (s *Server) setActiveSession(id string) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.activeSessionID != "" {
		return false
	}
	s.activeSessionID = id
	return true
}

func (s *Server) clearActiveSession() {
	s.activeMu.Lock()
	s.activeSessionID = ""
	s.activeMu.Unlock()
}

func (s *Server) getActiveSession() *session {
	s.activeMu.Lock()
	id := s.activeSessionID
	s.activeMu.Unlock()
	if id == "" {
		return nil
	}
	sess, _ := s.sessions.get(id)
	return sess
}

// ── Engine update → ACP notification bridge ──────────────────────────────────

func (s *Server) routeEngineUpdate(update core.StateUpdate) {
	sess := s.getActiveSession()
	if sess == nil {
		return
	}

	switch update.Type {

	// ── Text streaming ─────────────────────────────────────────────────
	case core.UpdateContentDelta:
		payload, ok := update.Payload.(core.ContentDeltaPayload)
		if !ok {
			break
		}
		content, _ := json.Marshal(MessageContent{Type: "text", Text: payload.Content})
		s.sendSessionUpdate(sess.id, SessionUpdate{
			SessionUpdateType: "agent_message_chunk",
			Content:           content,
		})

	case core.UpdateThinking:
		payload, ok := update.Payload.(core.ThinkingPayload)
		if !ok {
			break
		}
		// Emit thinking content as agentThoughtChunk per ACP spec.
		content, _ := json.Marshal(MessageContent{Type: "text", Text: payload.Content})
		s.sendSessionUpdate(sess.id, SessionUpdate{
			SessionUpdateType: "agent_thought_chunk",
			Content:           content,
		})

	// ── Tool calls ────────────────────────────────────────────────────
	case core.UpdateToolCall:
		payload, ok := update.Payload.(core.ToolCallPayload)
		if !ok {
			break
		}
		var rawInput json.RawMessage
		if len(payload.Parameters) > 0 {
			rawInput, _ = json.Marshal(payload.Parameters)
		}
		s.sendSessionUpdate(sess.id, SessionUpdate{
			SessionUpdateType: "tool_call",
			ToolCallID:        payload.ID,
			Title:             payload.Name,
			Kind:              toolKindFromName(payload.Name),
			Status:            "pending",
			RawInput:          rawInput,
		})

	case core.UpdateToolResult:
		payload, ok := update.Payload.(core.ToolResultPayload)
		if !ok {
			break
		}
		// ACP v1 terminal statuses are "completed" and "failed".
		status := "completed"
		if payload.Error != "" {
			status = "failed"
		}
		var contentJSON json.RawMessage
		if payload.Output != "" {
			items := []ToolCallItem{{
				Type:    "content",
				Content: &MessageContent{Type: "text", Text: payload.Output},
			}}
			contentJSON, _ = json.Marshal(items)
		}
		s.sendSessionUpdate(sess.id, SessionUpdate{
			SessionUpdateType: "tool_call_update",
			ToolCallID:        payload.CallID,
			Status:            status,
			Content:           contentJSON,
		})

	// ── Hook execution (shown as a tool call to the editor) ───────────
	case core.UpdateHookExecution:
		payload, ok := update.Payload.(core.HookExecutionPayload)
		if !ok {
			break
		}
		hookID := payload.HookID
		if hookID == "" {
			hookID = "hook-" + payload.HookName
		}
		switch payload.Status {
		case "started", "running", "":
			s.sendSessionUpdate(sess.id, SessionUpdate{
				SessionUpdateType: "tool_call",
				ToolCallID:        hookID,
				Title:             fmt.Sprintf("Hook: %s", payload.HookName),
				Kind:              "execute",
				Status:            "pending",
			})
		default:
			status := "completed"
			if !payload.Success || payload.Error != "" {
				status = "failed"
			}
			s.sendSessionUpdate(sess.id, SessionUpdate{
				SessionUpdateType: "tool_call_update",
				ToolCallID:        hookID,
				Status:            status,
			})
		}

	// ── Sub-agent activity ────────────────────────────────────────────
	case core.UpdateSubAgent:
		payload, ok := update.Payload.(core.SubAgentPayload)
		if !ok {
			break
		}
		agentCallID := "subagent-" + payload.AgentID
		switch payload.Status {
		case "starting", "running":
			s.sendSessionUpdate(sess.id, SessionUpdate{
				SessionUpdateType: "tool_call",
				ToolCallID:        agentCallID,
				Title:             fmt.Sprintf("Agent: %s", payload.AgentName),
				Kind:              "execute",
				Status:            "pending",
			})
		case "complete", "done":
			s.sendSessionUpdate(sess.id, SessionUpdate{
				SessionUpdateType: "tool_call_update",
				ToolCallID:        agentCallID,
				Status:            "completed",
			})
		case "error", "failed":
			s.sendSessionUpdate(sess.id, SessionUpdate{
				SessionUpdateType: "tool_call_update",
				ToolCallID:        agentCallID,
				Status:            "failed",
			})
		}

	// ── Stream lifecycle ──────────────────────────────────────────────
	case core.UpdateStreamEnd:
		reason := "end_turn"
		sess.finishPrompt(promptResult{stopReason: reason})

	case core.UpdateError:
		if payload, ok := update.Payload.(core.ErrorPayload); ok {
			sess.finishPrompt(promptResult{stopReason: "refusal", err: fmt.Errorf("%s", payload.Message)})
		}

	// ── Token / context usage ──────────────────────────────────────────
	case core.UpdateTokenCount:
		payload, ok := update.Payload.(core.TokenCountPayload)
		if !ok {
			break
		}
		// Only emit when we have real token data; the ACP schema requires
		// both `used` and `size` to be present (non-omitted).
		if payload.TotalTokens == 0 {
			break
		}
		size := payload.ContextWindow
		if size == 0 {
			size = payload.EffectiveWindow
		}
		if size == 0 {
			break // size is required by the schema; skip if unknown
		}
		s.sendSessionUpdate(sess.id, SessionUpdate{
			SessionUpdateType: "usage_update",
			Used:              payload.TotalTokens,
			Size:              size,
		})
	}
}

// sendSessionUpdate emits a session/update notification to the editor.
func (s *Server) sendSessionUpdate(sessionID string, update SessionUpdate) {
	s.writeNotification("session/update", SessionUpdateParams{
		SessionID: sessionID,
		Update:    update,
	})
}

// sendAvailableCommandsUpdate emits an availableCommandsUpdate notification
// with the full list of slash commands the agent supports.
func (s *Server) sendAvailableCommandsUpdate(sessionID string) {
	cmds := make([]AvailableCommand, len(registeredSlashCommands))
	for i, c := range registeredSlashCommands {
		cmds[i] = AvailableCommand{Name: c.Name, Description: c.Description}
	}
	s.sendSessionUpdate(sessionID, SessionUpdate{
		SessionUpdateType: "available_commands_update",
		AvailableCommands: cmds,
	})
}

// ── Approval broker → ACP permission bridge ──────────────────────────────────

func (s *Server) wireApprovalBroker() {
	if s.broker == nil {
		return
	}

	s.broker.SetIPCSender(func(req *approval.PermissionRequest) error {
		sess := s.getActiveSession()
		sessionID := ""
		if sess != nil {
			sessionID = sess.id
		}

		// Map permission tool/target to ACP kind.
		kind := permissionKindFromTool(req.Tool, req.Permission)

		permParams := SessionRequestPermissionParams{
			SessionID: sessionID,
			ToolCall: PermissionToolCall{
				ToolCallID: req.RequestID,
				Title:      permissionTitle(req.Tool, req.Target),
				Kind:       kind,
				Status:     "pending",
			},
			Options: defaultPermissionOptions(),
		}

		respJSON, err := s.sendOutboundRequest("session/request_permission", permParams)
		if err != nil {
			return err
		}

		// Parse the client's response.
		var result SessionRequestPermissionResult
		if err := json.Unmarshal(respJSON, &result); err != nil {
			return err
		}

		// Map ACP option back to approval.Decision.
		var decision approval.Decision
		switch result.Outcome.OptionID {
		case "allow-always":
			decision = approval.DecisionApproveAlways
		case "allow-once":
			decision = approval.DecisionApproveOnce
		default:
			decision = approval.DecisionDeny
		}
		if result.Outcome.Outcome == "cancelled" {
			decision = approval.DecisionDenyStop
		}

		return s.broker.Respond(req.RequestID, decision)
	})
}

// sendOutboundRequest sends a JSON-RPC request from the agent to the editor and
// blocks until the editor responds. Returns the raw result JSON.
func (s *Server) sendOutboundRequest(method string, params any) (json.RawMessage, error) {
	id := s.outboundSeq.Add(1)
	ch := make(chan json.RawMessage, 1)

	s.pendingMu.Lock()
	s.pendingOutbound[id] = ch
	s.pendingMu.Unlock()

	msg := OutboundRequest{
		JSONRPC: "2.0",
		ID:      int(id),
		Method:  method,
		Params:  params,
	}
	if err := s.writeJSON(msg); err != nil {
		s.pendingMu.Lock()
		delete(s.pendingOutbound, id)
		s.pendingMu.Unlock()
		return nil, err
	}

	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()

	select {
	case result := <-ch:
		return result, nil
	case <-timer.C:
		s.pendingMu.Lock()
		delete(s.pendingOutbound, id)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for editor response to %s", method)
	}
}

// routeClientResponse delivers a response from the editor to the waiting outbound request.
func (s *Server) routeClientResponse(rawID json.RawMessage, result json.RawMessage, rpcErr *RPCError) {
	// Parse the integer ID.
	var id int64
	if err := json.Unmarshal(rawID, &id); err != nil {
		return
	}

	s.pendingMu.Lock()
	ch, ok := s.pendingOutbound[id]
	if ok {
		delete(s.pendingOutbound, id)
	}
	s.pendingMu.Unlock()

	if !ok {
		return
	}
	if rpcErr != nil {
		// On error treat as deny.
		ch <- json.RawMessage(`{"outcome":{"outcome":"cancelled"}}`)
		return
	}
	ch <- result
}

// ── Write helpers ─────────────────────────────────────────────────────────────

func (s *Server) writeResponse(id json.RawMessage, result any) {
	s.writeJSON(Response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) writeError(id json.RawMessage, code int, message string, data any) {
	s.writeJSON(Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message, Data: data},
	})
}

func (s *Server) writeNotification(method string, params any) {
	s.writeJSON(Notification{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *Server) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = fmt.Fprintln(s.writer, string(data))
	return err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// buildPromptText assembles a plain-text prompt from ACP prompt content items.
// Embedded resources are appended as fenced blocks with their URI as the language tag.
func buildPromptText(items []PromptContent) string {
	var sb strings.Builder
	for _, item := range items {
		switch item.Type {
		case "text":
			sb.WriteString(item.Text)
		case "resource":
			if item.Resource != nil {
				lang := mimeToLang(item.Resource.MimeType)
				sb.WriteString("\n\n```")
				sb.WriteString(lang)
				sb.WriteString(" ")
				sb.WriteString(item.Resource.URI)
				sb.WriteString("\n")
				sb.WriteString(item.Resource.Text)
				sb.WriteString("\n```")
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// mimeToLang converts a MIME type to a markdown fence language tag.
func mimeToLang(mime string) string {
	switch mime {
	case "text/x-python", "application/x-python":
		return "python"
	case "text/javascript", "application/javascript":
		return "javascript"
	case "text/typescript", "application/typescript":
		return "typescript"
	case "text/x-go", "application/x-go":
		return "go"
	case "text/x-rust":
		return "rust"
	case "text/x-c", "text/x-c++":
		return "c"
	case "application/json":
		return "json"
	case "text/html":
		return "html"
	case "text/css":
		return "css"
	case "text/x-sh", "application/x-sh":
		return "bash"
	default:
		return ""
	}
}

// toolKindFromName maps a tool name to an ACP kind hint.
func toolKindFromName(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "read") || strings.Contains(name, "view") || strings.Contains(name, "list"):
		return "read"
	case strings.Contains(name, "write") || strings.Contains(name, "edit") ||
		strings.Contains(name, "create") || strings.Contains(name, "patch"):
		return "write"
	case strings.Contains(name, "delete") || strings.Contains(name, "remove"):
		return "delete"
	case strings.Contains(name, "exec") || strings.Contains(name, "run") ||
		strings.Contains(name, "bash") || strings.Contains(name, "shell"):
		return "execute"
	default:
		return "read"
	}
}

// permissionKindFromTool maps a Swarm permission type to an ACP kind hint.
func permissionKindFromTool(tool, permission string) string {
	tool = strings.ToLower(tool)
	permission = strings.ToLower(permission)
	switch {
	case strings.Contains(permission, "write") || strings.Contains(permission, "edit"):
		return "write"
	case strings.Contains(permission, "exec") || strings.Contains(permission, "run"):
		return "execute"
	case strings.Contains(permission, "delete"):
		return "delete"
	default:
		return toolKindFromName(tool)
	}
}

// permissionTitle constructs a human-readable permission title.
func permissionTitle(tool, target string) string {
	if target != "" {
		return fmt.Sprintf("%s: %s", tool, target)
	}
	return tool
}
