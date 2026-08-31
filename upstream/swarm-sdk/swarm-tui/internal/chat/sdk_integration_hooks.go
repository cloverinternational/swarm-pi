package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/hooks"
)

func (sdk *SDKIntegration) ExecuteHooksMessage(ctx context.Context, userMessage string, history []*conversation.Message, systemPrompt string, tools []provider.Tool) (string, error) {
	if sdk.provider == nil {
		return "", fmt.Errorf("provider not initialized")
	}

	startTime := time.Now()

	// Build messages array: history + current user message
	messages := make([]*conversation.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, &conversation.Message{
		ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
		Role:      conversation.RoleUser,
		Content:   userMessage,
		Timestamp: time.Now(),
	})

	sdk.logger.Info(ctx, "hooks.chat.request",
		observability.F("message_count", len(messages)),
		observability.F("tool_count", len(tools)),
		observability.F("model", sdk.currentModel),
	)

	// Capture debug request for hooks chat
	if sdk.debugScreen != nil {
		// Build messages JSON
		messagesJSON := make([]map[string]any, 0)
		for _, msg := range messages {
			messagesJSON = append(messagesJSON, map[string]any{
				"role":    string(msg.Role),
				"content": msg.Content,
			})
		}

		// Build tools JSON
		toolsJSON := make([]map[string]any, 0)
		for _, tool := range tools {
			toolsJSON = append(toolsJSON, map[string]any{
				"name":         tool.Name,
				"description":  tool.Description,
				"input_schema": tool.Parameters,
			})
		}

		// Build request body
		reqBody := map[string]any{
			"model":      sdk.currentModel,
			"max_tokens": sdk.GetMaxTokens(),
			"messages":   messagesJSON,
			"system":     systemPrompt,
		}
		if len(toolsJSON) > 0 {
			reqBody["tools"] = toolsJSON
		}

		reqBodyJSON, _ := json.MarshalIndent(reqBody, "", "  ")

		// Get actual provider URL and headers
		debugURL, headers := sdk.getProviderDebugInfo()
		debugURL = debugURL + " (HOOKS CHAT)"

		// Add OAuth headers if applicable
		if sdk.isOAuth && provider.NormalizeProviderName(sdk.GetProviderName()) == "anthropic" {
			headers["anthropic-beta"] = "oauth-2025-04-20"
			headers["x-app"] = "cli"
		}

		// Capture canonical conversation JSON for hooks chat
		canonicalConv := map[string]any{
			"type":          "hooks_chat",
			"message_count": len(history),
			"messages":      history,
			"system_prompt": systemPrompt,
		}
		canonicalJSON := marshalForDebug(canonicalConv)

		debugReq := DebugRequest{
			MessageIndex:  len(history),
			Timestamp:     startTime,
			Method:        "POST",
			URL:           debugURL,
			Headers:       headers,
			Body:          string(reqBodyJSON),
			CanonicalJSON: canonicalJSON,
			Model:         sdk.currentModel,
			Metadata: map[string]any{
				"type":                "hooks_chat",
				"provider":            sdk.GetProviderName(),
				"is_oauth":            sdk.isOAuth,
				"system_prompt_len":   len(systemPrompt),
				"system_prompt_start": truncateString(systemPrompt, 100),
			},
		}
		sdk.debugScreen.AddRequest(debugReq)
	}

	// Create chat request using the same provider
	maxTokens := sdk.GetMaxTokens()
	req := provider.ChatRequest{
		Messages:     messages,
		Model:        sdk.currentModel,
		SystemPrompt: systemPrompt,
		Tools:        tools,
		MaxTokens:    &maxTokens,
	}

	// Execute using the provider (which has OAuth properly configured)
	resp, err := sdk.provider.Chat(ctx, req)
	if err != nil {
		sdk.logger.Error(ctx, "hooks.chat.failed",
			observability.F("error", err.Error()),
		)
		// Capture error in debug screen
		if sdk.debugScreen != nil && len(sdk.debugScreen.requests) > 0 {
			lastReq := &sdk.debugScreen.requests[len(sdk.debugScreen.requests)-1]
			lastReq.Error = err.Error()
			lastReq.Duration = time.Since(startTime)
			lastReq.ResponseCode = 400
		}
		return "", fmt.Errorf("hooks chat failed: %w", err)
	}

	// Handle tool calls if any (recursive execution)
	if resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
		return sdk.handleHooksToolCalls(ctx, resp.Message, messages, systemPrompt, tools)
	}

	// Return the response content
	if resp.Message != nil && resp.Message.Content != "" {
		sdk.logger.Info(ctx, "hooks.chat.completed",
			observability.F("response_length", len(resp.Message.Content)),
		)
		return resp.Message.Content, nil
	}

	return "", fmt.Errorf("empty response from hooks chat")
}

// handleHooksToolCalls executes tool calls and continues the conversation
func (sdk *SDKIntegration) handleHooksToolCalls(ctx context.Context, assistantMsg *conversation.Message, history []*conversation.Message, systemPrompt string, tools []provider.Tool) (string, error) {
	// Add assistant message with tool calls to history
	newHistory := make([]*conversation.Message, 0, len(history)+2)
	newHistory = append(newHistory, history...)
	newHistory = append(newHistory, assistantMsg)

	// Execute each tool call (tools are registered in the hooks tool registry)
	var toolResults []conversation.ToolResult
	for _, tc := range assistantMsg.ToolCalls {
		result := sdk.executeHooksTool(ctx, tc.Name, tc.Parameters)
		toolResults = append(toolResults, conversation.ToolResult{
			CallID: tc.ID,
			Output: result,
		})
	}

	// Add tool results message
	toolResultMsg := &conversation.Message{
		ID:          fmt.Sprintf("tool-%d", time.Now().UnixNano()),
		Role:        conversation.RoleTool,
		Timestamp:   time.Now(),
		ToolResults: toolResults,
	}
	newHistory = append(newHistory, toolResultMsg)

	// Continue conversation with tool results
	maxTokens := sdk.GetMaxTokens()
	req := provider.ChatRequest{
		Messages:     newHistory,
		Model:        sdk.currentModel,
		SystemPrompt: systemPrompt,
		Tools:        tools,
		MaxTokens:    &maxTokens,
	}

	resp, err := sdk.provider.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("hooks follow-up failed: %w", err)
	}

	// Check for more tool calls (recursive)
	if resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
		return sdk.handleHooksToolCalls(ctx, resp.Message, newHistory, systemPrompt, tools)
	}

	if resp.Message != nil && resp.Message.Content != "" {
		return resp.Message.Content, nil
	}

	return "", fmt.Errorf("empty follow-up response")
}

// executeHooksTool executes a single hooks tool - called by SDK for hooks chat
func (sdk *SDKIntegration) executeHooksTool(ctx context.Context, name string, params map[string]any) string {
	// This will be set by the app when initializing hooks
	if sdk.hooksToolExecutor != nil {
		return sdk.hooksToolExecutor(ctx, name, params)
	}
	return fmt.Sprintf("Tool executor not configured for: %s", name)
}

// SetHooksToolExecutor sets the function that executes hooks tools
func (sdk *SDKIntegration) SetHooksToolExecutor(executor func(ctx context.Context, name string, params map[string]any) string) {
	sdk.hooksToolExecutor = executor
}

// ExecuteHooksMessageWithDetails executes a hooks chat message and returns detailed results
// including all tool calls made during the conversation turn.
func (sdk *SDKIntegration) ExecuteHooksMessageWithDetails(ctx context.Context, userMessage string, history []*conversation.Message, systemPrompt string, tools []provider.Tool) (*hooks.HooksAgentResult, error) {
	if sdk.provider == nil {
		return nil, fmt.Errorf("provider not initialized")
	}

	result := &hooks.HooksAgentResult{
		ToolCalls:  []hooks.HooksAgentToolCall{},
		TotalTurns: 0,
	}

	// Build messages array: history + current user message
	messages := make([]*conversation.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, &conversation.Message{
		ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
		Role:      conversation.RoleUser,
		Content:   userMessage,
		Timestamp: time.Now(),
	})

	// Recursive function to handle tool calls
	var executeWithToolHandling func(msgs []*conversation.Message) (string, error)
	executeWithToolHandling = func(msgs []*conversation.Message) (string, error) {
		result.TotalTurns++

		maxTokens := sdk.GetMaxTokens()
		req := provider.ChatRequest{
			Messages:     msgs,
			Model:        sdk.currentModel,
			SystemPrompt: systemPrompt,
			Tools:        tools,
			MaxTokens:    &maxTokens,
		}

		resp, err := sdk.provider.Chat(ctx, req)
		if err != nil {
			return "", fmt.Errorf("hooks chat failed: %w", err)
		}

		// Handle tool calls if any
		if resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
			// Add assistant message with tool calls
			newHistory := make([]*conversation.Message, 0, len(msgs)+2)
			newHistory = append(newHistory, msgs...)
			newHistory = append(newHistory, resp.Message)

			// Execute each tool call and track results (with hook execution)
			var toolResults []conversation.ToolResult
			for _, tc := range resp.Message.ToolCalls {
				toolStart := time.Now()

				// Pretty-print the input JSON
				inputJSON, _ := json.MarshalIndent(tc.Parameters, "", "  ")

				// Create tool call record
				toolCallRecord := hooks.HooksAgentToolCall{
					Name:      tc.Name,
					Input:     tc.Parameters,
					InputJSON: string(inputJSON),
					PreHooks:  []hooks.HooksAgentHookExecution{},
					PostHooks: []hooks.HooksAgentHookExecution{},
				}

				// Run pre-hooks if hooks manager is available
				var blocked bool
				if sdk.hooksManager != nil {
					preResults, preErr := sdk.hooksManager.EmitToolBeforeExecute(ctx, tc.Name, tc.Parameters)
					for _, hr := range preResults {
						toolCallRecord.PreHooks = append(toolCallRecord.PreHooks, hooks.HooksAgentHookExecution{
							HookName: hr.HookName,
							Phase:    "pre",
							Success:  hr.Success,
							Blocked:  hr.Blocked,
							Output:   hr.Output,
							Error:    hr.Error,
						})
						// Broadcast to TUI for visibility. Wrapped in recover in case
						// the channel was closed concurrently by MarkComplete when the
						// agent is being torn down.
						func() {
							defer func() { _ = recover() }()
							if sdk.localUpdateChan != nil {
								select {
								case sdk.localUpdateChan <- agent.HookExecutionUpdate{
									HookName:   hr.HookName,
									ToolName:   tc.Name,
									ToolCallID: tc.ID,
									Phase:      "before",
									Success:    hr.Success,
									Output:     hr.Output,
									Blocked:    hr.Blocked,
									Error:      hr.Error,
								}:
								default:
								}
							}
						}()
						if hr.Blocked {
							blocked = true
						}
					}
					if preErr != nil && blocked {
						toolCallRecord.Result = fmt.Sprintf("Blocked by hook: %v", preErr)
						toolCallRecord.Success = false
					}
				}

				// Execute tool if not blocked
				var toolResult string
				if !blocked {
					toolResult = sdk.executeHooksTool(ctx, tc.Name, tc.Parameters)
					toolCallRecord.Result = toolResult
					toolCallRecord.Success = !strings.HasPrefix(toolResult, "Error:")
				}

				// Run post-hooks if hooks manager is available
				if sdk.hooksManager != nil && !blocked {
					var execErr error
					if !toolCallRecord.Success {
						execErr = fmt.Errorf("%s", toolResult)
					}
					postResults := sdk.hooksManager.EmitToolAfterExecute(ctx, tc.Name, tc.Parameters, toolResult, execErr)
					for _, hr := range postResults {
						toolCallRecord.PostHooks = append(toolCallRecord.PostHooks, hooks.HooksAgentHookExecution{
							HookName: hr.HookName,
							Phase:    "post",
							Success:  hr.Success,
							Blocked:  hr.Blocked,
							Output:   hr.Output,
							Error:    hr.Error,
						})
						// Broadcast to TUI for visibility. Wrapped in recover in case
						// the channel was closed concurrently by MarkComplete when the
						// agent is being torn down.
						func() {
							defer func() { _ = recover() }()
							if sdk.localUpdateChan != nil {
								select {
								case sdk.localUpdateChan <- agent.HookExecutionUpdate{
									HookName:   hr.HookName,
									ToolName:   tc.Name,
									ToolCallID: tc.ID,
									Phase:      "after",
									Success:    hr.Success,
									Output:     hr.Output,
									Blocked:    hr.Blocked,
									Error:      hr.Error,
								}:
								default:
								}
							}
						}()
					}
				}

				toolCallRecord.Duration = time.Since(toolStart)
				result.ToolCalls = append(result.ToolCalls, toolCallRecord)

				toolResults = append(toolResults, conversation.ToolResult{
					CallID: tc.ID,
					Output: toolCallRecord.Result,
				})
			}

			// Add tool results message
			toolResultMsg := &conversation.Message{
				ID:          fmt.Sprintf("tool-%d", time.Now().UnixNano()),
				Role:        conversation.RoleTool,
				Timestamp:   time.Now(),
				ToolResults: toolResults,
			}
			newHistory = append(newHistory, toolResultMsg)

			// Continue conversation with tool results
			return executeWithToolHandling(newHistory)
		}

		// No more tool calls, return final content
		if resp.Message != nil && resp.Message.Content != "" {
			return resp.Message.Content, nil
		}

		return "", fmt.Errorf("empty response from hooks chat")
	}

	content, err := executeWithToolHandling(messages)
	if err != nil {
		return nil, err
	}

	result.Content = content
	return result, nil
}

// ============================================================================
// AGENTS ASSISTANT METHODS
// ============================================================================

// agentsToolExecutor is the function that executes agents tools
// Similar to hooksToolExecutor but for agent management
var agentsToolExecutor func(ctx context.Context, name string, params map[string]any) string

// ExecuteAgentsMessage executes an agents chat message using the same provider as the main chat.
// This reuses the OAuth configuration from the main provider.
func (sdk *SDKIntegration) ExecuteAgentsMessage(ctx context.Context, userMessage string, history []*conversation.Message, systemPrompt string, tools []provider.Tool) (string, error) {
	if sdk.provider == nil {
		return "", fmt.Errorf("provider not initialized")
	}

	startTime := time.Now()

	// Build messages array: history + current user message
	messages := make([]*conversation.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, &conversation.Message{
		ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
		Role:      conversation.RoleUser,
		Content:   userMessage,
		Timestamp: time.Now(),
	})

	sdk.logger.Info(ctx, "agents.chat.request",
		observability.F("message_count", len(messages)),
		observability.F("tool_count", len(tools)),
		observability.F("model", sdk.currentModel),
	)

	// Create chat request using the same provider
	maxTokens := sdk.GetMaxTokens()
	req := provider.ChatRequest{
		Messages:     messages,
		Model:        sdk.currentModel,
		SystemPrompt: systemPrompt,
		Tools:        tools,
		MaxTokens:    &maxTokens,
	}

	// Execute using the provider (which has OAuth properly configured)
	resp, err := sdk.provider.Chat(ctx, req)
	if err != nil {
		sdk.logger.Error(ctx, "agents.chat.failed",
			observability.F("error", err.Error()),
		)
		return "", fmt.Errorf("agents chat failed: %w", err)
	}

	// Handle tool calls if any (recursive execution)
	if resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
		return sdk.handleAgentsToolCalls(ctx, resp.Message, messages, systemPrompt, tools)
	}

	// Return the response content
	if resp.Message != nil && resp.Message.Content != "" {
		sdk.logger.Info(ctx, "agents.chat.completed",
			observability.F("response_length", len(resp.Message.Content)),
			observability.F("duration", time.Since(startTime).String()),
		)
		return resp.Message.Content, nil
	}

	return "", fmt.Errorf("empty response from agents chat")
}

// handleAgentsToolCalls executes tool calls and continues the conversation for agents assistant
func (sdk *SDKIntegration) handleAgentsToolCalls(ctx context.Context, assistantMsg *conversation.Message, history []*conversation.Message, systemPrompt string, tools []provider.Tool) (string, error) {
	// Add assistant message with tool calls to history
	newHistory := make([]*conversation.Message, 0, len(history)+2)
	newHistory = append(newHistory, history...)
	newHistory = append(newHistory, assistantMsg)

	// Execute each tool call (tools are registered in the agents tool registry)
	var toolResults []conversation.ToolResult
	for _, tc := range assistantMsg.ToolCalls {
		result := sdk.executeAgentsTool(ctx, tc.Name, tc.Parameters)
		toolResults = append(toolResults, conversation.ToolResult{
			CallID: tc.ID,
			Output: result,
		})
	}

	// Add tool results message
	toolResultMsg := &conversation.Message{
		ID:          fmt.Sprintf("tool-%d", time.Now().UnixNano()),
		Role:        conversation.RoleTool,
		Timestamp:   time.Now(),
		ToolResults: toolResults,
	}
	newHistory = append(newHistory, toolResultMsg)

	// Continue conversation with tool results
	maxTokens := sdk.GetMaxTokens()
	req := provider.ChatRequest{
		Messages:     newHistory,
		Model:        sdk.currentModel,
		SystemPrompt: systemPrompt,
		Tools:        tools,
		MaxTokens:    &maxTokens,
	}

	resp, err := sdk.provider.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("agents follow-up failed: %w", err)
	}

	// Check for more tool calls (recursive)
	if resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
		return sdk.handleAgentsToolCalls(ctx, resp.Message, newHistory, systemPrompt, tools)
	}

	if resp.Message != nil && resp.Message.Content != "" {
		return resp.Message.Content, nil
	}

	return "", fmt.Errorf("empty follow-up response")
}

// executeAgentsTool executes a single agents tool - called by SDK for agents chat
func (sdk *SDKIntegration) executeAgentsTool(ctx context.Context, name string, params map[string]any) string {
	// This will be set by the app when initializing agents assistant
	if agentsToolExecutor != nil {
		return agentsToolExecutor(ctx, name, params)
	}
	return fmt.Sprintf("Tool executor not configured for: %s", name)
}

// SetAgentsToolExecutor sets the function that executes agents tools
func (sdk *SDKIntegration) SetAgentsToolExecutor(executor func(ctx context.Context, name string, params map[string]any) string) {
	agentsToolExecutor = executor
}

// workflowToolRegistry holds the registered workflow tools
var workflowToolRegistry tools.Registry

// ExecuteWorkflowAssistantMessage executes a workflow assistant message with its specialized tools
func (sdk *SDKIntegration) ExecuteWorkflowAssistantMessage(ctx context.Context, systemPrompt string, history []*conversation.Message, providerTools []provider.Tool, toolRegistry tools.Registry) (string, error) {
	if sdk.provider == nil {
		return "", fmt.Errorf("provider not initialized")
	}

	// Store the tool registry for use in tool execution
	workflowToolRegistry = toolRegistry

	startTime := time.Now()

	sdk.logger.Info(ctx, "workflow.assistant.request",
		observability.F("message_count", len(history)),
		observability.F("tool_count", len(providerTools)),
		observability.F("model", sdk.currentModel),
	)

	// Create chat request
	maxTokens := sdk.GetMaxTokens()
	req := provider.ChatRequest{
		Messages:     history,
		Model:        sdk.currentModel,
		SystemPrompt: systemPrompt,
		Tools:        providerTools,
		MaxTokens:    &maxTokens,
	}

	// Execute using the provider
	resp, err := sdk.provider.Chat(ctx, req)
	if err != nil {
		sdk.logger.Error(ctx, "workflow.assistant.failed",
			observability.F("error", err.Error()),
		)
		return "", fmt.Errorf("workflow assistant failed: %w", err)
	}

	// Handle tool calls if any
	if resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
		return sdk.handleWorkflowToolCalls(ctx, resp.Message, history, systemPrompt, providerTools)
	}

	// Return the response content
	if resp.Message != nil && resp.Message.Content != "" {
		sdk.logger.Info(ctx, "workflow.assistant.completed",
			observability.F("response_length", len(resp.Message.Content)),
			observability.F("duration", time.Since(startTime).String()),
		)
		return resp.Message.Content, nil
	}

	return "", fmt.Errorf("empty response from workflow assistant")
}

// handleWorkflowToolCalls executes tool calls and continues the conversation for workflow assistant
func (sdk *SDKIntegration) handleWorkflowToolCalls(ctx context.Context, assistantMsg *conversation.Message, history []*conversation.Message, systemPrompt string, providerTools []provider.Tool) (string, error) {
	// Add assistant message with tool calls to history
	newHistory := make([]*conversation.Message, 0, len(history)+2)
	newHistory = append(newHistory, history...)
	newHistory = append(newHistory, assistantMsg)

	// Execute each tool call
	var toolResults []conversation.ToolResult
	for _, tc := range assistantMsg.ToolCalls {
		result := sdk.executeWorkflowTool(ctx, tc.Name, tc.Parameters)
		toolResults = append(toolResults, conversation.ToolResult{
			CallID: tc.ID,
			Output: result,
		})
	}

	// Add tool results message
	toolResultMsg := &conversation.Message{
		ID:          fmt.Sprintf("wf-tool-%d", time.Now().UnixNano()),
		Role:        conversation.RoleTool,
		Timestamp:   time.Now(),
		ToolResults: toolResults,
	}
	newHistory = append(newHistory, toolResultMsg)

	// Continue conversation with tool results
	maxTokens := sdk.GetMaxTokens()
	req := provider.ChatRequest{
		Messages:     newHistory,
		Model:        sdk.currentModel,
		SystemPrompt: systemPrompt,
		Tools:        providerTools,
		MaxTokens:    &maxTokens,
	}

	resp, err := sdk.provider.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("workflow follow-up failed: %w", err)
	}

	// Check for more tool calls (recursive)
	if resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
		return sdk.handleWorkflowToolCalls(ctx, resp.Message, newHistory, systemPrompt, providerTools)
	}

	if resp.Message != nil && resp.Message.Content != "" {
		return resp.Message.Content, nil
	}

	return "", fmt.Errorf("empty workflow follow-up response")
}

// executeWorkflowTool executes a single workflow tool
func (sdk *SDKIntegration) executeWorkflowTool(ctx context.Context, name string, params map[string]any) string {
	if workflowToolRegistry == nil {
		return fmt.Sprintf("Workflow tool registry not configured for: %s", name)
	}

	tool, err := workflowToolRegistry.Get(name)
	if err != nil {
		return fmt.Sprintf("Tool not found: %s", name)
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		return fmt.Sprintf("Tool execution error: %v", err)
	}

	if result.IsError {
		return fmt.Sprintf("Error: %s", result.Output)
	}

	return result.Output
}

// AgentsAgentResult contains the full result of an agents assistant execution
type AgentsAgentResult struct {
	Content    string                     `json:"content"`     // Final assistant response
	ToolCalls  []hooks.HooksAgentToolCall `json:"tool_calls"`  // All tool calls made (reusing hooks.HooksAgentToolCall type)
	TotalTurns int                        `json:"total_turns"` // Number of LLM turns
}

// ExecuteAgentsMessageWithDetails executes an agents chat message and returns detailed results
// including all tool calls made during the conversation turn.
func (sdk *SDKIntegration) ExecuteAgentsMessageWithDetails(ctx context.Context, userMessage string, history []*conversation.Message, systemPrompt string, tools []provider.Tool) (*AgentsAgentResult, error) {
	if sdk.provider == nil {
		return nil, fmt.Errorf("provider not initialized")
	}

	result := &AgentsAgentResult{
		ToolCalls:  []hooks.HooksAgentToolCall{},
		TotalTurns: 0,
	}

	// Build messages array: history + current user message
	messages := make([]*conversation.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, &conversation.Message{
		ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
		Role:      conversation.RoleUser,
		Content:   userMessage,
		Timestamp: time.Now(),
	})

	// Recursive function to handle tool calls
	var executeWithToolHandling func(msgs []*conversation.Message) (string, error)
	executeWithToolHandling = func(msgs []*conversation.Message) (string, error) {
		result.TotalTurns++

		maxTokens := sdk.GetMaxTokens()
		req := provider.ChatRequest{
			Messages:     msgs,
			Model:        sdk.currentModel,
			SystemPrompt: systemPrompt,
			Tools:        tools,
			MaxTokens:    &maxTokens,
		}

		resp, err := sdk.provider.Chat(ctx, req)
		if err != nil {
			return "", fmt.Errorf("agents chat failed: %w", err)
		}

		// Handle tool calls if any
		if resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
			// Add assistant message with tool calls
			newHistory := make([]*conversation.Message, 0, len(msgs)+2)
			newHistory = append(newHistory, msgs...)
			newHistory = append(newHistory, resp.Message)

			// Execute each tool call and track results
			var toolResults []conversation.ToolResult
			for _, tc := range resp.Message.ToolCalls {
				toolStart := time.Now()

				// Pretty-print the input JSON
				inputJSON, _ := json.MarshalIndent(tc.Parameters, "", "  ")

				// Create tool call record
				toolCallRecord := hooks.HooksAgentToolCall{
					Name:      tc.Name,
					Input:     tc.Parameters,
					InputJSON: string(inputJSON),
					PreHooks:  []hooks.HooksAgentHookExecution{},
					PostHooks: []hooks.HooksAgentHookExecution{},
				}

				// Execute the tool
				toolOutput := sdk.executeAgentsTool(ctx, tc.Name, tc.Parameters)
				toolCallRecord.Result = toolOutput
				toolCallRecord.Success = !strings.HasPrefix(toolOutput, "Error:")
				toolCallRecord.Duration = time.Since(toolStart)

				result.ToolCalls = append(result.ToolCalls, toolCallRecord)

				toolResults = append(toolResults, conversation.ToolResult{
					CallID: tc.ID,
					Output: toolCallRecord.Result,
				})
			}

			// Add tool results message
			toolResultMsg := &conversation.Message{
				ID:          fmt.Sprintf("tool-%d", time.Now().UnixNano()),
				Role:        conversation.RoleTool,
				Timestamp:   time.Now(),
				ToolResults: toolResults,
			}
			newHistory = append(newHistory, toolResultMsg)

			// Continue conversation with tool results
			return executeWithToolHandling(newHistory)
		}

		// No more tool calls, return final content
		if resp.Message != nil && resp.Message.Content != "" {
			return resp.Message.Content, nil
		}

		return "", fmt.Errorf("empty response from agents chat")
	}

	content, err := executeWithToolHandling(messages)
	if err != nil {
		return nil, err
	}

	result.Content = content
	return result, nil
}

// ExecuteAgentsMessageWithDetailsFallback is a fallback method that tries a secondary model
// if the primary fails. Uses claude-sonnet-4 as fallback.
func (sdk *SDKIntegration) ExecuteAgentsMessageWithDetailsFallback(ctx context.Context, userMessage string, history []*conversation.Message, systemPrompt string, tools []provider.Tool) (*AgentsAgentResult, error) {
	if sdk.provider == nil {
		return nil, fmt.Errorf("provider not initialized")
	}

	// Save current model
	originalModel := sdk.currentModel

	// Try fallback model (claude-sonnet-4)
	fallbackModel := "claude-sonnet-4-20250514"
	if originalModel == fallbackModel {
		// Already using fallback, try opus instead
		fallbackModel = "claude-opus-4-20250514"
	}

	logDebug("[AgentsSDK] Attempting fallback with model: %s (original: %s)", fallbackModel, originalModel)
	sdk.currentModel = fallbackModel

	// Try to execute with fallback model
	result, err := sdk.ExecuteAgentsMessageWithDetails(ctx, userMessage, history, systemPrompt, tools)

	// Restore original model
	sdk.currentModel = originalModel

	if err != nil {
		logDebug("[AgentsSDK] Fallback model also failed: %v", err)
		return nil, fmt.Errorf("fallback model %s also failed: %w", fallbackModel, err)
	}

	logDebug("[AgentsSDK] Fallback model %s succeeded", fallbackModel)
	return result, nil
}
