// Package hooks implements the event hook system.
// This file provides the HookSystem facade that orchestrates all components.
package hooks

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// HookSystem is the main facade for the hooks system.
// It orchestrates configuration loading, planning, execution, and aggregation.
type HookSystem struct {
	mu sync.RWMutex

	// Core components
	configHierarchy *ConfigHierarchy
	planner         *HookPlanner
	aggregator      *HookAggregator
	translator      Translator
	messageBus      *MessageBus
	eventHandler    *HookEventHandler

	// Session context
	sessionID string
	cwd       string

	// Configuration
	defaultTimeout time.Duration
	started        bool
}

// HookSystemConfig provides configuration for the hook system.
type HookSystemConfig struct {
	ProjectDir     string
	SessionID      string
	Cwd            string
	DefaultTimeout time.Duration
	BufferSize     int
}

// NewHookSystem creates a new hook system.
func NewHookSystem(config HookSystemConfig) *HookSystem {
	if config.DefaultTimeout <= 0 {
		config.DefaultTimeout = 60 * time.Second
	}
	if config.BufferSize <= 0 {
		config.BufferSize = 100
	}

	configHierarchy := NewConfigHierarchy(config.ProjectDir)
	planner := NewHookPlanner(configHierarchy)
	aggregator := NewHookAggregator()
	translator := NewDefaultTranslator()
	messageBus := NewMessageBus(config.BufferSize)

	system := &HookSystem{
		configHierarchy: configHierarchy,
		planner:         planner,
		aggregator:      aggregator,
		translator:      translator,
		messageBus:      messageBus,
		sessionID:       config.SessionID,
		cwd:             config.Cwd,
		defaultTimeout:  config.DefaultTimeout,
	}

	// Create event handler with the system
	system.eventHandler = NewHookEventHandler(system)

	return system
}

// Start initializes and starts the hook system.
func (s *HookSystem) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil
	}

	// Load configuration hierarchy
	if _, err := s.configHierarchy.Load(); err != nil {
		return fmt.Errorf("failed to load hook configuration: %w", err)
	}

	// Start message bus
	s.messageBus.Start()

	// Register event handler as subscriber
	s.messageBus.Subscribe(MsgHookExecutionRequest, s.eventHandler.HandleRequest)

	s.started = true
	return nil
}

// Stop shuts down the hook system.
func (s *HookSystem) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return
	}

	s.messageBus.Stop()
	s.started = false
}

// IsStarted returns whether the system is running.
func (s *HookSystem) IsStarted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.started
}

// Reload reloads the configuration hierarchy.
func (s *HookSystem) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.configHierarchy.Load()
	return err
}

// GetRegisteredHooks returns all registered hooks.
func (s *HookSystem) GetRegisteredHooks() []HookRegistryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.planner.GetAllHooks()
}

// FireEvent fires a hook event and returns the aggregated result.
func (s *HookSystem) FireEvent(ctx context.Context, eventName HookEventName, data map[string]any) (*AggregatedHookResult, error) {
	s.mu.RLock()
	if !s.started {
		s.mu.RUnlock()
		return &AggregatedHookResult{
			Success:     true,
			FinalOutput: &HookResponse{Decision: DecisionAllow},
		}, nil
	}
	s.mu.RUnlock()

	// Build event context for matching
	eventCtx := s.buildEventContext(eventName, data)

	// Create execution plan
	plan, err := s.planner.CreateExecutionPlan(eventName, eventCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create execution plan: %w", err)
	}

	// No hooks to execute
	if plan == nil || len(plan.Hooks) == 0 {
		return &AggregatedHookResult{
			Success:     true,
			FinalOutput: &HookResponse{Decision: DecisionAllow},
		}, nil
	}

	// Convert to hook input
	input, err := s.translator.ToHookInput(eventName, data, s.sessionID, s.cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to translate input: %w", err)
	}

	// Create request
	req := NewHookExecutionRequest(eventName, map[string]any{
		"input": input,
		"plan":  plan,
	}, eventCtx)

	// Send via message bus and wait for response
	resp, err := s.messageBus.Request(ctx, req, s.defaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("hook execution failed: %w", err)
	}

	if !resp.Success {
		return &AggregatedHookResult{
			Success:     false,
			FinalOutput: resp.Output,
			Errors:      []error{resp.Error},
		}, resp.Error
	}

	return &AggregatedHookResult{
		Success:     true,
		FinalOutput: resp.Output,
	}, nil
}

// buildEventContext builds the event context for matching hooks.
func (s *HookSystem) buildEventContext(eventName HookEventName, data map[string]any) *EventContext {
	ctx := &EventContext{}

	switch eventName {
	case EventBeforeTool, EventAfterTool:
		if toolName, ok := data["tool_name"].(string); ok {
			ctx.ToolName = toolName
		}
	case EventSessionStart:
		if source, ok := data["source"].(string); ok {
			ctx.Trigger = source
		}
	case EventSessionEnd:
		if reason, ok := data["reason"].(string); ok {
			ctx.Trigger = reason
		}
	case EventPreCompress:
		if trigger, ok := data["trigger"].(string); ok {
			ctx.Trigger = trigger
		}
	}

	return ctx
}

// Convenience methods for specific events

// FireSessionStart fires a session start event.
func (s *HookSystem) FireSessionStart(ctx context.Context, source SessionStartSource) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventSessionStart, map[string]any{
		"source": string(source),
	})
}

// FireSessionEnd fires a session end event.
func (s *HookSystem) FireSessionEnd(ctx context.Context, reason SessionEndReason) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventSessionEnd, map[string]any{
		"reason": string(reason),
	})
}

// FireBeforeAgent fires a before agent event.
func (s *HookSystem) FireBeforeAgent(ctx context.Context, prompt string) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventBeforeAgent, map[string]any{
		"prompt": prompt,
	})
}

// FireAfterAgent fires an after agent event.
func (s *HookSystem) FireAfterAgent(ctx context.Context, prompt, response string, stopHookActive bool) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventAfterAgent, map[string]any{
		"prompt":           prompt,
		"prompt_response":  response,
		"stop_hook_active": stopHookActive,
	})
}

// FireBeforeTool fires a before tool event.
func (s *HookSystem) FireBeforeTool(ctx context.Context, toolName string, toolInput map[string]any, mcpContext *MCPToolContext) (*AggregatedHookResult, error) {
	data := map[string]any{
		"tool_name":  toolName,
		"tool_input": toolInput,
	}
	if mcpContext != nil {
		data["mcp_context"] = map[string]any{
			"server_name": mcpContext.ServerName,
			"tool_name":   mcpContext.ToolName,
			"command":     mcpContext.Command,
			"args":        mcpContext.Args,
			"cwd":         mcpContext.Cwd,
			"url":         mcpContext.URL,
			"tcp":         mcpContext.TCP,
		}
	}
	return s.FireEvent(ctx, EventBeforeTool, data)
}

// FireAfterTool fires an after tool event.
func (s *HookSystem) FireAfterTool(ctx context.Context, toolName string, toolInput, toolResponse map[string]any, mcpContext *MCPToolContext) (*AggregatedHookResult, error) {
	data := map[string]any{
		"tool_name":     toolName,
		"tool_input":    toolInput,
		"tool_response": toolResponse,
	}
	if mcpContext != nil {
		data["mcp_context"] = map[string]any{
			"server_name": mcpContext.ServerName,
			"tool_name":   mcpContext.ToolName,
			"command":     mcpContext.Command,
			"args":        mcpContext.Args,
			"cwd":         mcpContext.Cwd,
			"url":         mcpContext.URL,
			"tcp":         mcpContext.TCP,
		}
	}
	return s.FireEvent(ctx, EventAfterTool, data)
}

// FireBeforeModel fires a before model event.
func (s *HookSystem) FireBeforeModel(ctx context.Context, llmRequest *LLMRequest) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventBeforeModel, map[string]any{
		"llm_request": llmRequest,
	})
}

// FireAfterModel fires an after model event.
func (s *HookSystem) FireAfterModel(ctx context.Context, llmRequest *LLMRequest, llmResponse *LLMResponse) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventAfterModel, map[string]any{
		"llm_request":  llmRequest,
		"llm_response": llmResponse,
	})
}

// FireBeforeToolSelection fires a before tool selection event.
func (s *HookSystem) FireBeforeToolSelection(ctx context.Context, llmRequest *LLMRequest) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventBeforeToolSelection, map[string]any{
		"llm_request": llmRequest,
	})
}

// FirePreCompress fires a pre-compress event.
func (s *HookSystem) FirePreCompress(ctx context.Context, trigger PreCompressTrigger) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventPreCompress, map[string]any{
		"trigger": string(trigger),
	})
}

// FireNotification fires a notification event.
func (s *HookSystem) FireNotification(ctx context.Context, notificationType NotificationType, message string, details map[string]any) (*AggregatedHookResult, error) {
	return s.FireEvent(ctx, EventNotification, map[string]any{
		"notification_type": string(notificationType),
		"message":           message,
		"details":           details,
	})
}

// SetSessionID updates the session ID.
func (s *HookSystem) SetSessionID(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = sessionID
}

// SetCwd updates the current working directory.
func (s *HookSystem) SetCwd(cwd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cwd = cwd
}

// GetTranslator returns the translator for external use.
func (s *HookSystem) GetTranslator() Translator {
	return s.translator
}

// GetPlanner returns the planner for external use.
func (s *HookSystem) GetPlanner() *HookPlanner {
	return s.planner
}

// GetAggregator returns the aggregator for external use.
func (s *HookSystem) GetAggregator() *HookAggregator {
	return s.aggregator
}
