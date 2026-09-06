// Package agent implements the agent runtime layer (Ring 2).
package agent

import (
	"context"
	"fmt"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// AgentTool wraps an Agent to make it callable as a Tool.
// This enables the "agents as tools" pattern where agents can delegate to other agents.
type AgentTool struct {
	agent *Agent
}

// NewAgentTool creates a new AgentTool wrapper around an agent.
func NewAgentTool(agent *Agent) *AgentTool {
	return &AgentTool{
		agent: agent,
	}
}

// Name returns the agent's name as the tool name.
func (t *AgentTool) Name() string {
	return t.agent.definition.ID
}

// Description returns the agent's description.
func (t *AgentTool) Description() string {
	if t.agent.definition.Description != "" {
		return t.agent.definition.Description
	}
	return fmt.Sprintf("Delegate task to %s agent (model: %s, provider: %s)",
		t.agent.definition.Name,
		t.agent.definition.Model,
		t.agent.definition.Provider)
}

// Parameters returns the JSON schema for invoking this agent as a tool.
// Agent tools accept a task and optional context.
func (t *AgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task to delegate to this agent",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Optional additional context for the task",
			},
		},
		"required": []string{"task"},
	}
}

// Execute runs the agent with the given parameters.
// This is called when another agent invokes this agent as a tool.
func (t *AgentTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Validate parameters
	if err := t.Validate(params); err != nil {
		return tools.NewErrorResult(err), nil // Return error as tool result, not Go error
	}

	// Extract task parameter
	task, _ := params["task"].(string) // Already validated

	// Extract optional context
	additionalContext := ""
	if ctxVal, ok := params["context"]; ok {
		if ctxStr, ok := ctxVal.(string); ok {
			additionalContext = ctxStr
		}
	}

	// Build full message with context if provided
	message := task
	if additionalContext != "" {
		message = fmt.Sprintf("%s\n\nAdditional context:\n%s", task, additionalContext)
	}

	// Create execution request
	req := ExecuteRequest{
		Message: message,
		Context: params, // Pass all params as context
	}

	// Execute the agent
	resp, err := t.agent.Execute(ctx, req)
	if err != nil {
		// Categorize error - if it's already an SDK error, preserve category
		return tools.NewErrorResult(err), nil
	}

	// Create tool result from agent response
	result := tools.NewToolResult(resp.Message)
	result.DurationMS = resp.Duration.Milliseconds()

	// Add metadata about the agent execution
	result.Metadata["agent_id"] = t.agent.definition.ID
	result.Metadata["agent_name"] = t.agent.definition.Name
	result.Metadata["turn_count"] = resp.TurnCount
	result.Metadata["tokens_used"] = resp.TokensUsed
	result.Metadata["cost_usd"] = resp.CostUSD
	result.Metadata["finish_reason"] = string(resp.FinishReason)

	return result, nil
}

// Validate checks if the parameters are valid for this agent tool.
func (t *AgentTool) Validate(params map[string]any) error {
	if params == nil {
		return sdkerr.Permanent("agent_tool.invalid_params", "parameters cannot be nil")
	}

	// Check required "task" parameter
	taskVal, ok := params["task"]
	if !ok {
		return sdkerr.Permanent("agent_tool.missing_task", "required parameter 'task' is missing")
	}

	if taskVal == nil {
		return sdkerr.Permanent("agent_tool.nil_task", "parameter 'task' cannot be nil")
	}

	task, ok := taskVal.(string)
	if !ok {
		return sdkerr.Permanent("agent_tool.invalid_task_type",
			fmt.Sprintf("parameter 'task' must be string, got %T", taskVal))
	}

	if task == "" {
		return sdkerr.Permanent("agent_tool.empty_task", "parameter 'task' cannot be empty")
	}

	// Validate optional "context" parameter if provided
	if ctxVal, ok := params["context"]; ok && ctxVal != nil {
		if _, ok := ctxVal.(string); !ok {
			return sdkerr.Permanent("agent_tool.invalid_context_type",
				fmt.Sprintf("parameter 'context' must be string, got %T", ctxVal))
		}
	}

	return nil
}

// IsIdempotent returns false because agents make decisions and may not be deterministic.
// The same task may produce different results based on model behavior, time, etc.
func (t *AgentTool) IsIdempotent() bool {
	return false
}

// RequiresPermission returns the permissions needed to invoke this agent.
// This includes all permissions that the agent's tools require.
func (t *AgentTool) RequiresPermission() []tools.Permission {
	// Calculate permissions from the agent's tool registry
	// This ensures sub-agents only get permissions for tools they actually have
	permissionSet := make(map[tools.Permission]bool)

	// Get the agent's tool registry
	registry := t.agent.ToolRegistry()
	if registry == nil {
		return []tools.Permission{}
	}

	// Collect permissions from all tools in the registry
	for _, toolName := range registry.List() {
		tool, err := registry.Get(toolName)
		if err != nil {
			continue
		}

		// Add this tool's required permissions
		if pt, ok := tool.(tools.PermissionedTool); ok {
			for _, perm := range pt.RequiresPermission() {
				permissionSet[perm] = true
			}
		}
	}

	// Convert map to slice
	permissions := make([]tools.Permission, 0, len(permissionSet))
	for perm := range permissionSet {
		permissions = append(permissions, perm)
	}

	return permissions
}

// SupportedContentTypes returns content types this agent can produce.
// Agents typically produce text, but may also produce images/PDFs if they use vision tools.
func (t *AgentTool) SupportedContentTypes() []tools.ContentType {
	contentTypes := []tools.ContentType{tools.ContentTypeText}

	// If agent supports vision, it may produce image content
	if t.agent.definition.Capabilities != nil && t.agent.definition.Capabilities.SupportsVision {
		contentTypes = append(contentTypes, tools.ContentTypeImage)
	}

	return contentTypes
}

// OptimizationHints provides guidance for efficient agent tool use.
func (t *AgentTool) OptimizationHints() *tools.OptimizationHints {
	// Agents are expensive operations that should be executed sequentially
	hints := &tools.OptimizationHints{
		PreferSequential:  true,            // Don't run multiple agents in parallel
		EstimatedDuration: 5 * time.Second, // Agents take longer than simple tools
		CanBatch:          false,           // Agents can't be batched
		BatchSize:         0,
		Priority:          50,    // Medium priority
		MinimalLatency:    false, // Agents are not minimal latency
		Cacheable:         false, // Agents are not deterministic
		CacheTTL:          0,
	}

	// Adjust hints based on agent capabilities
	if t.agent.definition.Capabilities != nil {
		if t.agent.definition.Capabilities.Timeout > 0 {
			hints.EstimatedDuration = t.agent.definition.Capabilities.Timeout
		}
	}

	return hints
}

// Agent returns the underlying agent instance.
// This is useful for accessing agent-specific methods or metadata.
func (t *AgentTool) Agent() *Agent {
	return t.agent
}
