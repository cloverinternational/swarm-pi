package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/profile"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/agent/mocks"
	tmocks "github.com/Swarm-Code/mono/swarm-sdk/tests/mocks"
)

// ============================================================
// Bridge Configuration Tests
// ============================================================

func TestNewBridge_Empty(t *testing.T) {
	bridge, err := NewBridge()
	if err != nil {
		t.Fatalf("NewBridge with empty options should succeed: %v", err)
	}

	if bridge == nil {
		t.Fatal("NewBridge returned nil")
	}
}

func TestNewBridge_WithDefaults(t *testing.T) {
	config := BridgeConfig{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-3-opus",
	}

	bridge, err := NewBridgeFromConfig(config)
	if err != nil {
		t.Fatalf("NewBridge failed: %v", err)
	}

	provider, model := bridge.GetProvider()
	if provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", provider, "anthropic")
	}
	if model != "claude-3-opus" {
		t.Errorf("Model = %q, want %q", model, "claude-3-opus")
	}
}

func TestBridge_CancelExecution_NoCancelFunc(t *testing.T) {
	config := BridgeConfig{}
	bridge, _ := NewBridgeFromConfig(config)

	// Should not panic when no cancel function is set
	err := bridge.CancelExecution()
	if err != nil {
		t.Errorf("CancelExecution should succeed: %v", err)
	}
}

func TestBridge_GetToolRegistry_NilRegistry(t *testing.T) {
	config := BridgeConfig{}
	bridge, _ := NewBridgeFromConfig(config)

	registry := bridge.GetToolRegistry()
	if registry.ToolCount != 0 {
		t.Errorf("ToolCount = %d, want 0", registry.ToolCount)
	}
}

func TestBridge_GetConversationManager(t *testing.T) {
	config := BridgeConfig{}
	bridge, _ := NewBridgeFromConfig(config)

	manager := bridge.GetConversationManager()
	if manager == nil {
		t.Error("GetConversationManager should not return nil")
	}
}

func TestConvertCoreMessage_PreservesImageMetadata(t *testing.T) {
	msg := core.NewUserMessage("")
	msg.Metadata = map[string]any{
		"images": []map[string]string{
			{
				"type":       "base64",
				"media_type": "image/png",
				"data":       "aGVsbG8=",
				"name":       "clipboard.png",
			},
		},
	}

	sdkMsg := convertCoreMessage(msg)
	images, err := vision.ExtractImagesFromMetadata(sdkMsg.Metadata)
	if err != nil {
		t.Fatalf("ExtractImagesFromMetadata failed: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("image count = %d, want 1", len(images))
	}
	if images[0].MediaType != "image/png" {
		t.Fatalf("media type = %q, want %q", images[0].MediaType, "image/png")
	}
}

func TestConvertSDKMessage_PreservesImageMetadata(t *testing.T) {
	msg := &conversation.Message{
		ID:        "msg-1",
		Role:      conversation.RoleUser,
		Content:   "",
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"images": []map[string]string{
				{
					"type":       "base64",
					"media_type": "image/png",
					"data":       "aGVsbG8=",
				},
			},
		},
	}

	coreMsg := convertSDKMessage(msg)
	if coreMsg.Metadata == nil {
		t.Fatal("expected metadata to be preserved")
	}
	if len(coreMsg.Attachments) != 1 {
		t.Fatalf("attachment count = %d, want 1", len(coreMsg.Attachments))
	}
}

// ============================================================
// Agent Factory Tests
// ============================================================

func TestBridge_ExecuteMessage_UsesAgentFactoryWithTools(t *testing.T) {
	logger := mocks.NewMockLogger()
	tracer := mocks.NewMockTracer()
	registry := tools.NewSimpleRegistry(logger, tracer)
	provReg := provider.NewSimpleRegistry(logger)

	if err := registry.Register(mockTool{}); err != nil {
		t.Fatalf("Register mock tool failed: %v", err)
	}

	errCh := make(chan error, 1)
	reportErr := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	bridge, err := NewBridgeFromConfig(BridgeConfig{
		ToolRegistry: registry,
		AgentFactory: func(ctx context.Context, model string) (*agent.Agent, error) {
			return nil, fmt.Errorf("unexpected AgentFactory call")
		},
		AgentFactoryWithTools: func(ctx context.Context, model string, toolRegistry tools.Registry) (*agent.Agent, error) {
			if toolRegistry != registry {
				reportErr(fmt.Errorf("tool registry mismatch"))
			}
			if !toolRegistry.IsRegistered("mock_tool") {
				reportErr(fmt.Errorf("mock_tool not registered"))
			}

			def := &agent.Definition{
				ID:        "test-agent",
				Provider:  "mock",
				Model:     "mock-model",
				ToolHints: []string{"*"},
				Capabilities: &agent.Capabilities{
					SupportsTools: true,
					MaxTurns:      1,
				},
			}
			prov := nonStreamingProvider{inner: mocks.NewMockProviderWithToolCalls("mock", "mock-model")}
			ag, err := agent.New(agent.Config{
				Definition:       def,
				Provider:         prov,
				ProviderRegistry: provReg,
				ToolRegistry:     toolRegistry,
				Logger:           logger,
				Tracer:           tracer,
				Auditor:          &tmocks.MockAuditor{},
			})
			if err != nil {
				return nil, err
			}
			if err := ag.Initialize(); err != nil {
				return nil, err
			}
			return ag, nil
		},
	})
	if err != nil {
		t.Fatalf("NewBridge failed: %v", err)
	}

	updates, err := bridge.ExecuteMessage(context.Background(), "", "hello", "mock-model")
	if err != nil {
		t.Fatalf("ExecuteMessage failed: %v", err)
	}

	var sawToolCall bool
	var sawToolResult bool
	for update := range updates {
		switch update.Type {
		case core.UpdateToolCall:
			sawToolCall = true
		case core.UpdateToolResult:
			sawToolResult = true
		}
	}

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}

	if !sawToolCall {
		t.Errorf("expected tool call update")
	}
	if !sawToolResult {
		t.Errorf("expected tool result update")
	}
}

func TestBridge_ExecuteMessage_UsesAgentFactoryWhenNoToolFactory(t *testing.T) {
	logger := mocks.NewMockLogger()
	tracer := mocks.NewMockTracer()
	registry := tools.NewSimpleRegistry(logger, tracer)
	provReg := provider.NewSimpleRegistry(logger)

	var factoryCalls int32
	bridge, err := NewBridgeFromConfig(BridgeConfig{
		ToolRegistry: registry,
		AgentFactory: func(ctx context.Context, model string) (*agent.Agent, error) {
			atomic.AddInt32(&factoryCalls, 1)

			def := &agent.Definition{
				ID:       "test-agent",
				Provider: "mock",
				Model:    "mock-model",
			}
			prov := mocks.NewMockProvider("mock")
			ag, err := agent.New(agent.Config{
				Definition:       def,
				Provider:         prov,
				ProviderRegistry: provReg,
				ToolRegistry:     registry,
				Logger:           logger,
				Tracer:           tracer,
				Auditor:          &tmocks.MockAuditor{},
			})
			if err != nil {
				return nil, err
			}
			if err := ag.Initialize(); err != nil {
				return nil, err
			}
			return ag, nil
		},
	})
	if err != nil {
		t.Fatalf("NewBridge failed: %v", err)
	}

	updates, err := bridge.ExecuteMessage(context.Background(), "", "hello", "mock-model")
	if err != nil {
		t.Fatalf("ExecuteMessage failed: %v", err)
	}
	for range updates {
	}

	if got := atomic.LoadInt32(&factoryCalls); got != 1 {
		t.Errorf("AgentFactory calls = %d, want 1", got)
	}
}

func TestBridge_ExecuteMessage_ModeFiltering(t *testing.T) {
	logger := mocks.NewMockLogger()
	tracer := mocks.NewMockTracer()
	registry := tools.NewSimpleRegistry(logger, tracer)
	provReg := provider.NewSimpleRegistry(logger)

	if err := registry.Register(mockTool{}); err != nil {
		t.Fatalf("Register mock tool failed: %v", err)
	}

	prov := &toolRecordingProvider{name: "mock"}

	bridge, err := NewBridgeFromConfig(BridgeConfig{
		ToolRegistry: registry,
		AgentFactoryWithTools: func(ctx context.Context, model string, toolRegistry tools.Registry) (*agent.Agent, error) {
			def := &agent.Definition{
				ID:        "test-agent",
				Provider:  "mock",
				Model:     "mock-model",
				ToolHints: []string{"*"},
				Capabilities: &agent.Capabilities{
					SupportsTools: true,
				},
			}
			ag, err := agent.New(agent.Config{
				Definition:       def,
				Provider:         prov,
				ProviderRegistry: provReg,
				ToolRegistry:     toolRegistry,
				Logger:           logger,
				Tracer:           tracer,
				Auditor:          &tmocks.MockAuditor{},
			})
			if err != nil {
				return nil, err
			}
			if err := ag.Initialize(); err != nil {
				return nil, err
			}
			return ag, nil
		},
	})
	if err != nil {
		t.Fatalf("NewBridge failed: %v", err)
	}

	cases := []struct {
		name      string
		modeID    string
		wantTools int
		wantName  string
	}{
		{name: "chat_filters_all_tools", modeID: mode.ModeChat, wantTools: 0},
		// PLAN mode is an approval ceremony, not a tool-authorization phase.
		// It exposes the ordinary tool surface and leaves task, permission,
		// workspace, credential, and safety policy unchanged.
		{name: "plan_exposes_ordinary_tools", modeID: mode.ModePlan, wantTools: 1, wantName: "mock_tool"},
		{name: "act_allows_tools", modeID: mode.ModeAct, wantTools: 1, wantName: "mock_tool"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov.reset()

			updates, err := bridge.ExecuteMessageWithMode(context.Background(), "", "hello", "mock-model", tc.modeID)
			if err != nil {
				t.Fatalf("ExecuteMessageWithMode failed: %v", err)
			}
			for range updates {
			}

			names := prov.toolNames()
			if len(names) != tc.wantTools {
				t.Fatalf("tool count = %d, want %d (mode %s)", len(names), tc.wantTools, tc.modeID)
			}
			if tc.wantTools > 0 && names[0] != tc.wantName {
				t.Fatalf("tool name = %q, want %q (mode %s)", names[0], tc.wantName, tc.modeID)
			}
		})
	}
}

type mockTool struct{}

func (mockTool) Name() string {
	return "mock_tool"
}

func (mockTool) Description() string {
	return "Test tool for bridge execution."
}

func (mockTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"test": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"test"},
	}
}

func (mockTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	return tools.NewToolResult("ok"), nil
}

func (mockTool) Validate(params map[string]any) error {
	return nil
}

func (mockTool) IsIdempotent() bool {
	return true
}

func (mockTool) RequiresPermission() []tools.Permission {
	return nil
}

func (mockTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

func (mockTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

type toolRecordingProvider struct {
	name      string
	mu        sync.Mutex
	lastTools []provider.Tool
}

func (p *toolRecordingProvider) Name() string {
	return p.name
}

func (p *toolRecordingProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming: false,
	}
}

func (p *toolRecordingProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	p.lastTools = append([]provider.Tool(nil), req.Tools...)
	p.mu.Unlock()

	return &provider.ChatResponse{
		Message: &conversation.Message{
			Role:      conversation.RoleAssistant,
			Content:   "ok",
			Timestamp: time.Now(),
		},
		FinishReason: provider.FinishReasonStop,
	}, nil
}

func (p *toolRecordingProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func (p *toolRecordingProvider) toolNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	names := make([]string, len(p.lastTools))
	for i, tool := range p.lastTools {
		names[i] = tool.Name
	}
	return names
}

func (p *toolRecordingProvider) reset() {
	p.mu.Lock()
	p.lastTools = nil
	p.mu.Unlock()
}

type nonStreamingProvider struct {
	inner provider.Provider
}

func (p nonStreamingProvider) Name() string {
	return p.inner.Name()
}

func (p nonStreamingProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.inner.Chat(ctx, req)
}

func (p nonStreamingProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return p.inner.Stream(ctx, req)
}

func (p nonStreamingProvider) Capabilities() provider.Capabilities {
	caps := p.inner.Capabilities()
	caps.Streaming = false
	return caps
}

// ============================================================
// ConversationManagerAdapter Tests
// ============================================================

func TestConversationManagerAdapter_Create_NilManager(t *testing.T) {
	adapter := &conversationManagerAdapter{
		manager: nil,
	}

	ctx := context.Background()
	id, err := adapter.Create(ctx, core.CreateConversationOptions{})
	if err != nil {
		t.Fatalf("Create with nil manager failed: %v", err)
	}
	if id != "" {
		t.Errorf("ID = %q, want empty with nil manager", id)
	}
}

func TestConversationManagerAdapter_Load_NilManager(t *testing.T) {
	adapter := &conversationManagerAdapter{
		manager: nil,
	}

	ctx := context.Background()
	state, err := adapter.Load(ctx, "test-id")
	if err != nil {
		t.Fatalf("Load with nil manager failed: %v", err)
	}
	if state != nil {
		t.Error("State should be nil with nil manager")
	}
}

func TestConversationManagerAdapter_List_NilStorage(t *testing.T) {
	adapter := &conversationManagerAdapter{
		storage: nil,
	}

	ctx := context.Background()
	summaries, err := adapter.List(ctx, core.ListConversationsOptions{})
	if err != nil {
		t.Fatalf("List with nil storage failed: %v", err)
	}
	if summaries != nil {
		t.Error("Summaries should be nil with nil storage")
	}
}

func TestConversationManagerAdapter_Delete_NilManager(t *testing.T) {
	adapter := &conversationManagerAdapter{
		manager: nil,
	}

	ctx := context.Background()
	err := adapter.Delete(ctx, "test-id")
	if err != nil {
		t.Errorf("Delete with nil manager failed: %v", err)
	}
}

func TestConversationManagerAdapter_Save_NilManager(t *testing.T) {
	adapter := &conversationManagerAdapter{
		manager: nil,
	}

	ctx := context.Background()
	state := &core.ConversationState{ID: "test"}
	err := adapter.Save(ctx, state)
	if err != nil {
		t.Errorf("Save with nil manager failed: %v", err)
	}
}

// ============================================================
// State Update Type Tests
// ============================================================

func TestStateUpdateTypes(t *testing.T) {
	// Verify all update types are valid
	updateTypes := []core.UpdateType{
		core.UpdateContentDelta,
		core.UpdateThinking,
		core.UpdateMessageComplete,
		core.UpdateMessageAdd,
		core.UpdateToolCall,
		core.UpdateToolResult,
		core.UpdateToolChunk,
		core.UpdateStreamStart,
		core.UpdateStreamEnd,
		core.UpdateTokenCount,
		core.UpdateConvStatus,
		core.UpdateConvList,
		core.UpdateError,
		core.UpdateHookExecution,
		core.UpdateSubAgent,
		core.UpdateModelChange,
		core.UpdateProviderChange,
	}

	for _, ut := range updateTypes {
		update := core.NewStateUpdate(ut, nil)
		if update.Type != ut {
			t.Errorf("Update type = %v, want %v", update.Type, ut)
		}
		if update.EventType() != string(ut) {
			t.Errorf("EventType() = %q, want %q", update.EventType(), string(ut))
		}
	}
}

// ============================================================
// Payload Type Tests
// ============================================================

func TestContentDeltaPayload(t *testing.T) {
	payload := core.ContentDeltaPayload{
		Content: "Hello, world!",
		Append:  true,
	}

	if payload.Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", payload.Content, "Hello, world!")
	}
	if !payload.Append {
		t.Error("Append should be true")
	}
}

func TestThinkingPayload(t *testing.T) {
	payload := core.ThinkingPayload{
		Content: "Let me think...",
		Append:  true,
	}

	if payload.Content != "Let me think..." {
		t.Errorf("Content = %q, want %q", payload.Content, "Let me think...")
	}
}

func TestToolCallPayload(t *testing.T) {
	payload := core.ToolCallPayload{
		ID:         "call-123",
		Name:       "bash",
		Parameters: map[string]any{"command": "ls -la"},
	}

	if payload.ID != "call-123" {
		t.Errorf("ID = %q, want %q", payload.ID, "call-123")
	}
	if payload.Name != "bash" {
		t.Errorf("Name = %q, want %q", payload.Name, "bash")
	}
	if payload.Parameters["command"] != "ls -la" {
		t.Errorf("Parameters[command] = %v, want %q", payload.Parameters["command"], "ls -la")
	}
}

func TestToolResultPayload(t *testing.T) {
	payload := core.ToolResultPayload{
		CallID:   "call-123",
		Output:   "file1.txt\nfile2.txt",
		Error:    "",
		ExitCode: 0,
	}

	if payload.CallID != "call-123" {
		t.Errorf("CallID = %q, want %q", payload.CallID, "call-123")
	}
	if payload.Output != "file1.txt\nfile2.txt" {
		t.Errorf("Output = %q, want %q", payload.Output, "file1.txt\nfile2.txt")
	}
}

func TestToolChunkPayload(t *testing.T) {
	payload := core.ToolChunkPayload{
		CallID: "call-123",
		Chunk:  "streaming output...",
		Stream: "stdout",
	}

	if payload.CallID != "call-123" {
		t.Errorf("CallID = %q, want %q", payload.CallID, "call-123")
	}
	if payload.Stream != "stdout" {
		t.Errorf("Stream = %q, want %q", payload.Stream, "stdout")
	}
}

func TestTokenCountPayload(t *testing.T) {
	payload := core.TokenCountPayload{
		InputTokens:  1000,
		OutputTokens: 500,
		TotalTokens:  1500,
		CacheRead:    100,
		CacheCreate:  50,
	}

	if payload.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", payload.InputTokens)
	}
	if payload.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", payload.TotalTokens)
	}
}

func TestHookExecutionPayload(t *testing.T) {
	payload := core.HookExecutionPayload{
		HookName: "pre-bash",
		ToolName: "bash",
		Phase:    "before",
		Success:  true,
		Output:   "hook output",
		Blocked:  false,
		Error:    "",
	}

	if payload.HookName != "pre-bash" {
		t.Errorf("HookName = %q, want %q", payload.HookName, "pre-bash")
	}
	if payload.Phase != "before" {
		t.Errorf("Phase = %q, want %q", payload.Phase, "before")
	}
}

func TestSubAgentPayload(t *testing.T) {
	payload := core.SubAgentPayload{
		AgentName: "researcher",
		AgentID:   "agent-123",
		Status:    "running",
		Message:   "Searching...",
	}

	if payload.AgentName != "researcher" {
		t.Errorf("AgentName = %q, want %q", payload.AgentName, "researcher")
	}
	if payload.Status != "running" {
		t.Errorf("Status = %q, want %q", payload.Status, "running")
	}
}

func TestModelChangePayload(t *testing.T) {
	payload := core.ModelChangePayload{
		Model:         "claude-3-opus",
		Provider:      "anthropic",
		ContextWindow: 200000,
	}

	if payload.Model != "claude-3-opus" {
		t.Errorf("Model = %q, want %q", payload.Model, "claude-3-opus")
	}
	if payload.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", payload.ContextWindow)
	}
}

func TestErrorPayload(t *testing.T) {
	payload := core.ErrorPayload{
		Message: "Something went wrong",
		Code:    "execution_failed",
	}

	if payload.Message != "Something went wrong" {
		t.Errorf("Message = %q, want %q", payload.Message, "Something went wrong")
	}
	if payload.Code != "execution_failed" {
		t.Errorf("Code = %q, want %q", payload.Code, "execution_failed")
	}
}

// ============================================================
// Core Message Role Tests
// ============================================================

func TestMessageRoles(t *testing.T) {
	tests := []struct {
		role core.MessageRole
		name string
	}{
		{core.RoleUser, "user"},
		{core.RoleAssistant, "assistant"},
		{core.RoleSystem, "system"},
		{core.RoleTool, "tool"},
	}

	for _, tt := range tests {
		if string(tt.role) != tt.name {
			t.Errorf("Role %v = %q, want %q", tt.role, string(tt.role), tt.name)
		}
	}
}

// ============================================================
// ToolRegistryInfo Tests
// ============================================================

func TestToolRegistryInfo(t *testing.T) {
	info := core.ToolRegistryInfo{
		ToolCount: 5,
		ToolNames: []string{"bash", "read", "write", "glob", "grep"},
		Tools: []core.ToolInfo{
			{Name: "bash", Description: "Execute bash commands"},
			{Name: "read", Description: "Read files"},
		},
	}

	if info.ToolCount != 5 {
		t.Errorf("ToolCount = %d, want 5", info.ToolCount)
	}
	if len(info.ToolNames) != 5 {
		t.Errorf("ToolNames count = %d, want 5", len(info.ToolNames))
	}
	if len(info.Tools) != 2 {
		t.Errorf("Tools count = %d, want 2", len(info.Tools))
	}
}

// ============================================================
// Conversation State Tests
// ============================================================

func TestConversationState(t *testing.T) {
	state := &core.ConversationState{
		ID:       "conv-123",
		Messages: []core.Message{},
		Status:   core.StatusIdle,
		Metadata: map[string]any{"key": "value"},
	}

	if state.ID != "conv-123" {
		t.Errorf("ID = %q, want %q", state.ID, "conv-123")
	}
	if state.Status != core.StatusIdle {
		t.Errorf("Status = %v, want %v", state.Status, core.StatusIdle)
	}
}

func TestConversationStatus(t *testing.T) {
	statuses := []core.ConversationStatus{
		core.StatusIdle,
		core.StatusThinking,
		core.StatusStreaming,
		core.StatusToolUse,
		core.StatusComplete,
		core.StatusError,
	}

	expected := []string{
		"idle", "thinking", "streaming", "tool_use", "complete", "error",
	}

	for i, status := range statuses {
		if string(status) != expected[i] {
			t.Errorf("Status %v = %q, want %q", status, string(status), expected[i])
		}
	}
}

// ============================================================
// ConversationSummary Tests
// ============================================================

func TestConversationSummary(t *testing.T) {
	summary := core.ConversationSummary{
		ID:           "conv-123",
		Title:        "Test Conversation",
		Preview:      "This is a preview...",
		MessageCount: 10,
		TotalTokens:  5000,
		Status:       "idle",
	}

	if summary.ID != "conv-123" {
		t.Errorf("ID = %q, want %q", summary.ID, "conv-123")
	}
	if summary.MessageCount != 10 {
		t.Errorf("MessageCount = %d, want 10", summary.MessageCount)
	}
	if summary.TotalTokens != 5000 {
		t.Errorf("TotalTokens = %d, want 5000", summary.TotalTokens)
	}
}

// ============================================================
// SwitchProfile Tests (028-profile-ipc-integration)
// ============================================================

func TestBridge_SwitchProfile_Success(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test profiles file
	testProfiles := profile.ProfilesConfig{
		Profiles: []profile.AgentProfile{
			{
				ID:   "profile-1",
				Name: "Profile 1",
				Pointers: map[profile.ModelAlias]profile.ModelPointer{
					profile.AliasMain: {Provider: "openai", Model: "gpt-4"},
				},
			},
			{
				ID:   "profile-2",
				Name: "Profile 2",
				Pointers: map[profile.ModelAlias]profile.ModelPointer{
					profile.AliasMain: {Provider: "anthropic", Model: "claude-3"},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(testProfiles, "", "  ")
	os.WriteFile(filepath.Join(tmpDir, "agent_profiles.json"), data, 0644)

	store := profile.NewProfileStore(tmpDir)

	bridge, err := NewBridgeFromConfig(BridgeConfig{
		ProfileStore:    store,
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4",
	})
	if err != nil {
		t.Fatalf("NewBridge failed: %v", err)
	}

	// Switch to profile-2
	err = bridge.SwitchProfile("profile-2")
	if err != nil {
		t.Fatalf("SwitchProfile failed: %v", err)
	}

	// Verify provider/model changed to profile-2's main alias
	provider, model := bridge.GetProvider()
	if provider != "anthropic" {
		t.Errorf("Expected provider 'anthropic', got '%s'", provider)
	}
	if model != "claude-3" {
		t.Errorf("Expected model 'claude-3', got '%s'", model)
	}
}

func TestBridge_SwitchProfile_ProfileNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	// Create empty profiles file
	testProfiles := profile.ProfilesConfig{
		Profiles: []profile.AgentProfile{
			{
				ID:   "existing-profile",
				Name: "Existing",
				Pointers: map[profile.ModelAlias]profile.ModelPointer{
					profile.AliasMain: {Provider: "openai", Model: "gpt-4"},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(testProfiles, "", "  ")
	os.WriteFile(filepath.Join(tmpDir, "agent_profiles.json"), data, 0644)

	store := profile.NewProfileStore(tmpDir)

	bridge, _ := NewBridgeFromConfig(BridgeConfig{
		ProfileStore: store,
	})

	err := bridge.SwitchProfile("nonexistent-profile")
	if err == nil {
		t.Fatal("Expected error for nonexistent profile")
	}

	// Verify error message mentions profile not found
	if !strings.Contains(err.Error(), "profile") {
		t.Errorf("Error should mention 'profile', got: %s", err.Error())
	}
}

func TestBridge_SwitchProfile_NoMainAlias(t *testing.T) {
	tmpDir := t.TempDir()

	// Create profile without main alias
	testProfiles := profile.ProfilesConfig{
		Profiles: []profile.AgentProfile{
			{
				ID:   "no-main-profile",
				Name: "No Main",
				Pointers: map[profile.ModelAlias]profile.ModelPointer{
					// Only steering alias, no main
					profile.AliasSteering: {Provider: "openai", Model: "gpt-4"},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(testProfiles, "", "  ")
	os.WriteFile(filepath.Join(tmpDir, "agent_profiles.json"), data, 0644)

	store := profile.NewProfileStore(tmpDir)

	bridge, _ := NewBridgeFromConfig(BridgeConfig{
		ProfileStore: store,
	})

	err := bridge.SwitchProfile("no-main-profile")
	if err == nil {
		t.Fatal("Expected error for profile without main alias")
	}

	// Verify error message mentions main alias
	if !strings.Contains(err.Error(), "main") {
		t.Errorf("Error should mention 'main' alias, got: %s", err.Error())
	}
}

func TestBridge_SwitchProfile_NilProfileStore(t *testing.T) {
	bridge, _ := NewBridgeFromConfig(BridgeConfig{
		ProfileStore: nil, // No profile store
	})

	err := bridge.SwitchProfile("any-profile")
	if err == nil {
		t.Fatal("Expected error when profile store is nil")
	}
}

func TestBridge_SwitchProfile_PreservesOtherState(t *testing.T) {
	tmpDir := t.TempDir()

	testProfiles := profile.ProfilesConfig{
		Profiles: []profile.AgentProfile{
			{
				ID:   "profile-1",
				Name: "Profile 1",
				Pointers: map[profile.ModelAlias]profile.ModelPointer{
					profile.AliasMain: {Provider: "newprovider", Model: "newmodel"},
				},
			},
		},
	}

	data, _ := json.MarshalIndent(testProfiles, "", "  ")
	os.WriteFile(filepath.Join(tmpDir, "agent_profiles.json"), data, 0644)

	store := profile.NewProfileStore(tmpDir)

	// Create bridge with initial state
	bridge, _ := NewBridgeFromConfig(BridgeConfig{
		ProfileStore:    store,
		DefaultProvider: "initialprovider",
		DefaultModel:    "initialmodel",
	})

	// Verify initial state
	provider, model := bridge.GetProvider()
	if provider != "initialprovider" {
		t.Errorf("Initial provider = %q, want 'initialprovider'", provider)
	}
	if model != "initialmodel" {
		t.Errorf("Initial model = %q, want 'initialmodel'", model)
	}

	// Switch profile
	err := bridge.SwitchProfile("profile-1")
	if err != nil {
		t.Fatalf("SwitchProfile failed: %v", err)
	}

	// Verify state changed
	provider, model = bridge.GetProvider()
	if provider != "newprovider" {
		t.Errorf("After switch provider = %q, want 'newprovider'", provider)
	}
	if model != "newmodel" {
		t.Errorf("After switch model = %q, want 'newmodel'", model)
	}
}
