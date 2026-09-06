// Package hooks implements the event hook system.
// This file provides integration helpers for connecting the new hook system
// with existing TUI and SDK components.
package hooks

import (
	"context"
	"fmt"
	"time"
)

// Integration provides a bridge between the new HookSystem and existing components.
// It wraps the HookSystem and provides backward-compatible methods.
type Integration struct {
	system *HookSystem
}

// NewIntegration creates a new integration wrapper.
func NewIntegration(config HookSystemConfig) (*Integration, error) {
	system := NewHookSystem(config)
	if err := system.Start(); err != nil {
		return nil, fmt.Errorf("failed to start hook system: %w", err)
	}

	return &Integration{
		system: system,
	}, nil
}

// Close shuts down the integration.
func (i *Integration) Close() {
	if i.system != nil {
		i.system.Stop()
	}
}

// GetSystem returns the underlying hook system.
func (i *Integration) GetSystem() *HookSystem {
	return i.system
}

// ToolExecutionContext contains context for tool execution hooks.
type ToolExecutionContext struct {
	ToolName     string
	ToolInput    map[string]any
	ToolResponse map[string]any
	MCPContext   *MCPToolContext
}

// ToolHookResult represents the result of a tool hook execution.
type ToolHookResult struct {
	Allowed       bool
	Modified      bool
	SystemMessage string
	Reason        string
	AllResults    []*HookResponse
}

// EmitBeforeTool fires a BeforeTool event and returns whether to proceed.
func (i *Integration) EmitBeforeTool(ctx context.Context, toolCtx ToolExecutionContext) (*ToolHookResult, error) {
	result, err := i.system.FireBeforeTool(ctx, toolCtx.ToolName, toolCtx.ToolInput, toolCtx.MCPContext)
	if err != nil {
		return nil, err
	}

	hookResult := &ToolHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		if result.FinalOutput.Decision.IsBlocking() {
			hookResult.Allowed = false
			hookResult.Reason = result.FinalOutput.Reason
		}
		hookResult.SystemMessage = result.FinalOutput.SystemMessage
	}

	return hookResult, nil
}

// EmitAfterTool fires an AfterTool event.
func (i *Integration) EmitAfterTool(ctx context.Context, toolCtx ToolExecutionContext) (*ToolHookResult, error) {
	result, err := i.system.FireAfterTool(ctx, toolCtx.ToolName, toolCtx.ToolInput, toolCtx.ToolResponse, toolCtx.MCPContext)
	if err != nil {
		return nil, err
	}

	hookResult := &ToolHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		hookResult.SystemMessage = result.FinalOutput.SystemMessage
	}

	return hookResult, nil
}

// AgentContext contains context for agent hooks.
type AgentContext struct {
	Prompt         string
	Response       string
	StopHookActive bool
}

// AgentHookResult represents the result of an agent hook execution.
type AgentHookResult struct {
	Allowed        bool
	ModifiedPrompt string
	SystemMessage  string
	AdditionalCtx  string
	ShouldStop     bool
	StopReason     string
	AllResults     []*HookResponse
}

// EmitBeforeAgent fires a BeforeAgent event.
func (i *Integration) EmitBeforeAgent(ctx context.Context, agentCtx AgentContext) (*AgentHookResult, error) {
	result, err := i.system.FireBeforeAgent(ctx, agentCtx.Prompt)
	if err != nil {
		return nil, err
	}

	hookResult := &AgentHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		if result.FinalOutput.Decision.IsBlocking() {
			hookResult.Allowed = false
		}
		hookResult.SystemMessage = result.FinalOutput.SystemMessage
		hookResult.AdditionalCtx = result.FinalOutput.GetAdditionalContext()

		if !result.FinalOutput.ShouldContinue() {
			hookResult.ShouldStop = true
			hookResult.StopReason = result.FinalOutput.StopReason
		}
	}

	return hookResult, nil
}

// EmitAfterAgent fires an AfterAgent event.
func (i *Integration) EmitAfterAgent(ctx context.Context, agentCtx AgentContext) (*AgentHookResult, error) {
	result, err := i.system.FireAfterAgent(ctx, agentCtx.Prompt, agentCtx.Response, agentCtx.StopHookActive)
	if err != nil {
		return nil, err
	}

	hookResult := &AgentHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		hookResult.SystemMessage = result.FinalOutput.SystemMessage
		if !result.FinalOutput.ShouldContinue() {
			hookResult.ShouldStop = true
			hookResult.StopReason = result.FinalOutput.StopReason
		}
	}

	return hookResult, nil
}

// SessionHookResult represents the result of a session hook execution.
type SessionHookResult struct {
	Allowed       bool
	SystemMessage string
	AllResults    []*HookResponse
}

// EmitSessionStart fires a SessionStart event.
func (i *Integration) EmitSessionStart(ctx context.Context, source SessionStartSource) (*SessionHookResult, error) {
	result, err := i.system.FireSessionStart(ctx, source)
	if err != nil {
		return nil, err
	}

	hookResult := &SessionHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		if result.FinalOutput.Decision.IsBlocking() {
			hookResult.Allowed = false
		}
		hookResult.SystemMessage = result.FinalOutput.SystemMessage
	}

	return hookResult, nil
}

// EmitSessionEnd fires a SessionEnd event.
func (i *Integration) EmitSessionEnd(ctx context.Context, reason SessionEndReason) (*SessionHookResult, error) {
	result, err := i.system.FireSessionEnd(ctx, reason)
	if err != nil {
		return nil, err
	}

	hookResult := &SessionHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		hookResult.SystemMessage = result.FinalOutput.SystemMessage
	}

	return hookResult, nil
}

// ModelContext contains context for model hooks.
type ModelContext struct {
	Request  *LLMRequest
	Response *LLMResponse
}

// ModelHookResult represents the result of a model hook execution.
type ModelHookResult struct {
	Allowed          bool
	ModifiedRequest  *LLMRequest
	ModifiedResponse *LLMResponse
	SyntheticResp    *LLMResponse
	SystemMessage    string
	AllResults       []*HookResponse
}

// EmitBeforeModel fires a BeforeModel event.
func (i *Integration) EmitBeforeModel(ctx context.Context, modelCtx ModelContext) (*ModelHookResult, error) {
	result, err := i.system.FireBeforeModel(ctx, modelCtx.Request)
	if err != nil {
		return nil, err
	}

	hookResult := &ModelHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		if result.FinalOutput.Decision.IsBlocking() {
			hookResult.Allowed = false
		}
		hookResult.SystemMessage = result.FinalOutput.SystemMessage

		// Check for modified request or synthetic response
		if result.FinalOutput.HookSpecificOutput != nil {
			if modReq, ok := result.FinalOutput.HookSpecificOutput["llm_request"].(*LLMRequest); ok {
				hookResult.ModifiedRequest = modReq
			}
			if synthResp, ok := result.FinalOutput.HookSpecificOutput["llm_response"].(*LLMResponse); ok {
				hookResult.SyntheticResp = synthResp
			}
		}
	}

	return hookResult, nil
}

// EmitAfterModel fires an AfterModel event.
func (i *Integration) EmitAfterModel(ctx context.Context, modelCtx ModelContext) (*ModelHookResult, error) {
	result, err := i.system.FireAfterModel(ctx, modelCtx.Request, modelCtx.Response)
	if err != nil {
		return nil, err
	}

	hookResult := &ModelHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		hookResult.SystemMessage = result.FinalOutput.SystemMessage

		// Check for modified response
		if result.FinalOutput.HookSpecificOutput != nil {
			if modResp, ok := result.FinalOutput.HookSpecificOutput["llm_response"].(*LLMResponse); ok {
				hookResult.ModifiedResponse = modResp
			}
		}
	}

	return hookResult, nil
}

// ToolSelectionContext contains context for tool selection hooks.
type ToolSelectionContext struct {
	Request *LLMRequest
}

// ToolSelectionHookResult represents the result of a tool selection hook.
type ToolSelectionHookResult struct {
	Allowed       bool
	ToolConfig    *LLMToolConfig
	AllowedTools  []string
	SystemMessage string
	AllResults    []*HookResponse
}

// EmitBeforeToolSelection fires a BeforeToolSelection event.
func (i *Integration) EmitBeforeToolSelection(ctx context.Context, selCtx ToolSelectionContext) (*ToolSelectionHookResult, error) {
	result, err := i.system.FireBeforeToolSelection(ctx, selCtx.Request)
	if err != nil {
		return nil, err
	}

	hookResult := &ToolSelectionHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		if result.FinalOutput.Decision.IsBlocking() {
			hookResult.Allowed = false
		}
		hookResult.SystemMessage = result.FinalOutput.SystemMessage

		// Extract tool config
		if result.FinalOutput.HookSpecificOutput != nil {
			if toolConfig, ok := result.FinalOutput.HookSpecificOutput["toolConfig"].(map[string]any); ok {
				hookResult.ToolConfig = &LLMToolConfig{}
				if mode, ok := toolConfig["mode"].(string); ok {
					hookResult.ToolConfig.Mode = mode
				}
				if names, ok := toolConfig["allowedFunctionNames"].([]any); ok {
					for _, name := range names {
						if s, ok := name.(string); ok {
							hookResult.AllowedTools = append(hookResult.AllowedTools, s)
						}
					}
					hookResult.ToolConfig.AllowedFunctionNames = hookResult.AllowedTools
				}
			}
		}
	}

	return hookResult, nil
}

// PreCompressContext contains context for pre-compress hooks.
type PreCompressContext struct {
	Trigger PreCompressTrigger
}

// PreCompressHookResult represents the result of a pre-compress hook.
type PreCompressHookResult struct {
	Allowed       bool
	SystemMessage string
	AllResults    []*HookResponse
}

// EmitPreCompress fires a PreCompress event.
func (i *Integration) EmitPreCompress(ctx context.Context, compCtx PreCompressContext) (*PreCompressHookResult, error) {
	result, err := i.system.FirePreCompress(ctx, compCtx.Trigger)
	if err != nil {
		return nil, err
	}

	hookResult := &PreCompressHookResult{
		Allowed:    true,
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		if result.FinalOutput.Decision.IsBlocking() {
			hookResult.Allowed = false
		}
		hookResult.SystemMessage = result.FinalOutput.SystemMessage
	}

	return hookResult, nil
}

// NotificationContext contains context for notification hooks.
type NotificationContext struct {
	Type    NotificationType
	Message string
	Details map[string]any
}

// NotificationHookResult represents the result of a notification hook.
type NotificationHookResult struct {
	SuppressOutput bool
	SystemMessage  string
	AllResults     []*HookResponse
}

// EmitNotification fires a Notification event.
func (i *Integration) EmitNotification(ctx context.Context, notifCtx NotificationContext) (*NotificationHookResult, error) {
	result, err := i.system.FireNotification(ctx, notifCtx.Type, notifCtx.Message, notifCtx.Details)
	if err != nil {
		return nil, err
	}

	hookResult := &NotificationHookResult{
		AllResults: result.AllOutputs,
	}

	if result.FinalOutput != nil {
		hookResult.SystemMessage = result.FinalOutput.SystemMessage
		hookResult.SuppressOutput = result.FinalOutput.SuppressOutput
	}

	return hookResult, nil
}

// LegacyAdapter adapts the new hook system to the old event types.
// Use this for backward compatibility with existing code.
type LegacyAdapter struct {
	integration *Integration
}

// NewLegacyAdapter creates a new legacy adapter.
func NewLegacyAdapter(integration *Integration) *LegacyAdapter {
	return &LegacyAdapter{
		integration: integration,
	}
}

// EmitToolBeforeExecute emits the legacy tool.before_execute event.
// Returns (shouldProceed, systemMessage, error).
func (a *LegacyAdapter) EmitToolBeforeExecute(ctx context.Context, toolName string, params map[string]any) (bool, string, error) {
	result, err := a.integration.EmitBeforeTool(ctx, ToolExecutionContext{
		ToolName:  toolName,
		ToolInput: params,
	})
	if err != nil {
		return false, "", err
	}
	return result.Allowed, result.SystemMessage, nil
}

// EmitToolAfterExecute emits the legacy tool.after_execute event.
// Returns (systemMessage, error).
func (a *LegacyAdapter) EmitToolAfterExecute(ctx context.Context, toolName string, params map[string]any, response map[string]any) (string, error) {
	result, err := a.integration.EmitAfterTool(ctx, ToolExecutionContext{
		ToolName:     toolName,
		ToolInput:    params,
		ToolResponse: response,
	})
	if err != nil {
		return "", err
	}
	return result.SystemMessage, nil
}

// EmitUserPromptSubmit emits the legacy user.prompt_submit event.
// Returns (injectedContext, shouldProceed, error).
func (a *LegacyAdapter) EmitUserPromptSubmit(ctx context.Context, prompt string) (string, bool, error) {
	result, err := a.integration.EmitBeforeAgent(ctx, AgentContext{
		Prompt: prompt,
	})
	if err != nil {
		return "", false, err
	}
	return result.AdditionalCtx, result.Allowed, nil
}

// HookSystemFactory provides factory methods for creating hook systems.
type HookSystemFactory struct{}

// CreateDefault creates a default hook system for the given project directory.
func (f *HookSystemFactory) CreateDefault(projectDir string) (*Integration, error) {
	return NewIntegration(HookSystemConfig{
		ProjectDir:     projectDir,
		SessionID:      fmt.Sprintf("session_%d", time.Now().UnixNano()),
		Cwd:            projectDir,
		DefaultTimeout: 60 * time.Second,
		BufferSize:     100,
	})
}

// CreateWithSession creates a hook system with a specific session ID.
func (f *HookSystemFactory) CreateWithSession(projectDir, sessionID, cwd string) (*Integration, error) {
	return NewIntegration(HookSystemConfig{
		ProjectDir:     projectDir,
		SessionID:      sessionID,
		Cwd:            cwd,
		DefaultTimeout: 60 * time.Second,
		BufferSize:     100,
	})
}
