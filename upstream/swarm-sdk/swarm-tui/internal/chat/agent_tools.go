package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// AgentTools provides tools for managing custom agents via AI assistant
type AgentTools struct {
	agentsSettings *settings.AgentsSettings
}

// NewAgentTools creates a new agent tools instance
func NewAgentTools(agentsSettings *settings.AgentsSettings) *AgentTools {
	return &AgentTools{
		agentsSettings: agentsSettings,
	}
}

// GetTools returns all available agent management tools
func (at *AgentTools) GetTools() []tools.Tool {
	return []tools.Tool{
		&CreateAgentTool{agentsSettings: at.agentsSettings},
		&UpdateAgentTool{agentsSettings: at.agentsSettings},
		&DeleteAgentTool{agentsSettings: at.agentsSettings},
		&ListAgentsTool{agentsSettings: at.agentsSettings},
		&SetDefaultAgentTool{agentsSettings: at.agentsSettings},
		&CloneAgentTool{agentsSettings: at.agentsSettings},
		&ConfigureToolsTool{agentsSettings: at.agentsSettings},
		&ConfigureHooksTool{agentsSettings: at.agentsSettings},
		&SetCapabilitiesTool{agentsSettings: at.agentsSettings},
	}
}

// CreateAgentTool creates a new custom agent
type CreateAgentTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *CreateAgentTool) Name() string {
	return "create_agent"
}

func (t *CreateAgentTool) Description() string {
	return "Create a new custom agent with specified configuration. Returns the created agent details."
}

func (t *CreateAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "Unique agent ID in kebab-case (e.g., 'code-reviewer', 'security-auditor')",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Display name for the agent",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Brief description of what the agent does",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "AI provider (e.g., 'anthropic', 'openai', 'openrouter')",
				"default":     "anthropic",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model ID (e.g., 'claude-sonnet-4-20250514', 'gpt-4')",
				"default":     "claude-sonnet-4-20250514",
			},
			"system_prompt": map[string]any{
				"type":        "string",
				"description": "System prompt defining agent behavior and capabilities",
			},
			"tools": map[string]any{
				"type":        "array",
				"description": "List of tool names, or ['*'] for all tools",
				"items": map[string]any{
					"type": "string",
				},
				"default": []string{"*"},
			},
			"hooks": map[string]any{
				"type":        "array",
				"description": "List of hook names to attach to this agent",
				"items": map[string]any{
					"type": "string",
				},
				"default": []string{},
			},
			"temperature": map[string]any{
				"type":        "number",
				"description": "Temperature for response generation (0.0-1.0)",
				"default":     0.7,
			},
			"max_turns": map[string]any{
				"type":        "integer",
				"description": "Maximum conversation turns (0 = unlimited)",
				"default":     20,
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (minimum: 900 = 15 minutes)",
				"default":     900,
			},
			"color": map[string]any{
				"type":        "string",
				"description": "UI color for the agent (hex format)",
			},
			"icon": map[string]any{
				"type":        "string",
				"description": "Emoji icon for the agent",
			},
		},
		"required": []string{"id", "name", "provider", "model"},
	}
}

func (t *CreateAgentTool) Validate(params map[string]any) error        { return nil }
func (t *CreateAgentTool) IsIdempotent() bool                          { return false }
func (t *CreateAgentTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *CreateAgentTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *CreateAgentTool) RequiresPermission() []tools.Permission      { return nil }

func (t *CreateAgentTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Extract parameters
	id, _ := params["id"].(string)
	name, _ := params["name"].(string)
	description, _ := params["description"].(string)
	provider, _ := params["provider"].(string)
	model, _ := params["model"].(string)
	systemPrompt, _ := params["system_prompt"].(string)

	// Default provider and model
	if provider == "" {
		provider = "anthropic"
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// Parse tools array
	toolsList := []string{"*"}
	if toolsRaw, ok := params["tools"].([]any); ok {
		toolsList = make([]string, 0, len(toolsRaw))
		for _, t := range toolsRaw {
			if ts, ok := t.(string); ok {
				toolsList = append(toolsList, ts)
			}
		}
	}

	// Parse hooks array
	hooksList := []string{}
	if hooksRaw, ok := params["hooks"].([]any); ok {
		hooksList = make([]string, 0, len(hooksRaw))
		for _, h := range hooksRaw {
			if hs, ok := h.(string); ok {
				hooksList = append(hooksList, hs)
			}
		}
	}

	// Parse capabilities
	temperature := 0.7
	if temp, ok := params["temperature"].(float64); ok {
		temperature = temp
	}
	maxTurns := 0 // Unlimited by default (agent summarizes on limit if set)
	if mt, ok := params["max_turns"].(float64); ok {
		maxTurns = int(mt)
	}
	timeout := 900
	if to, ok := params["timeout"].(float64); ok {
		timeout = int(to)
		if timeout < 900 {
			timeout = 900
		}
	}

	color, _ := params["color"].(string)
	icon, _ := params["icon"].(string)

	// Create agent entry
	agent := settings.CustomAgentEntry{
		ID:           id,
		Name:         name,
		Description:  description,
		Provider:     provider,
		Model:        model,
		SystemPrompt: systemPrompt,
		Tools:        toolsList,
		Hooks:        hooksList,
		Capabilities: &settings.AgentCapabilities{
			MaxTokens:   0, // Use default/auto-compact naturally
			Temperature: temperature,
			MaxTurns:    maxTurns,
			Timeout:     timeout,
		},
		Builtin: false,
		Color:   color,
		Icon:    icon,
	}

	// Create agent
	if err := t.agentsSettings.CreateAgent(agent); err != nil {
		return tools.NewToolResult(fmt.Sprintf("Error creating agent: %v", err)), nil
	}

	// Return success with agent details
	agentJSON, _ := json.MarshalIndent(agent, "", "  ")
	return tools.NewToolResult(fmt.Sprintf("✓ Agent created successfully:\n%s", string(agentJSON))), nil
}

// UpdateAgentTool updates an existing agent
type UpdateAgentTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *UpdateAgentTool) Name() string {
	return "update_agent"
}

func (t *UpdateAgentTool) Description() string {
	return "Update an existing agent's configuration. Only specified fields will be updated."
}

func (t *UpdateAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to update",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "New display name",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "New description",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "New provider",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "New model ID",
			},
			"system_prompt": map[string]any{
				"type":        "string",
				"description": "New system prompt",
			},
			"tools": map[string]any{
				"type":        "array",
				"description": "New tools list",
				"items": map[string]any{
					"type": "string",
				},
			},
			"hooks": map[string]any{
				"type":        "array",
				"description": "New hooks list",
				"items": map[string]any{
					"type": "string",
				},
			},
			"temperature": map[string]any{
				"type":        "number",
				"description": "New temperature value",
			},
			"max_turns": map[string]any{
				"type":        "integer",
				"description": "New max_turns value",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "New timeout value",
			},
			"color": map[string]any{
				"type":        "string",
				"description": "New UI color",
			},
			"icon": map[string]any{
				"type":        "string",
				"description": "New emoji icon",
			},
		},
		"required": []string{"id"},
	}
}

func (t *UpdateAgentTool) Validate(params map[string]any) error        { return nil }
func (t *UpdateAgentTool) IsIdempotent() bool                          { return false }
func (t *UpdateAgentTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *UpdateAgentTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *UpdateAgentTool) RequiresPermission() []tools.Permission      { return nil }

func (t *UpdateAgentTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	id, _ := params["id"].(string)

	// Get existing agent
	existing := t.agentsSettings.GetAgent(id)
	if existing == nil {
		return tools.NewToolResult(fmt.Sprintf("Error: Agent '%s' not found", id)), nil
	}

	// Create updated agent (start with existing values)
	updated := *existing

	// Update fields if provided
	if name, ok := params["name"].(string); ok {
		updated.Name = name
	}
	if desc, ok := params["description"].(string); ok {
		updated.Description = desc
	}
	if provider, ok := params["provider"].(string); ok {
		updated.Provider = provider
	}
	if model, ok := params["model"].(string); ok {
		updated.Model = model
	}
	if sysPrompt, ok := params["system_prompt"].(string); ok {
		updated.SystemPrompt = sysPrompt
	}
	if color, ok := params["color"].(string); ok {
		updated.Color = color
	}
	if icon, ok := params["icon"].(string); ok {
		updated.Icon = icon
	}

	// Update tools if provided
	if toolsRaw, ok := params["tools"].([]any); ok {
		updated.Tools = make([]string, 0, len(toolsRaw))
		for _, t := range toolsRaw {
			if ts, ok := t.(string); ok {
				updated.Tools = append(updated.Tools, ts)
			}
		}
	}

	// Update hooks if provided
	if hooksRaw, ok := params["hooks"].([]any); ok {
		updated.Hooks = make([]string, 0, len(hooksRaw))
		for _, h := range hooksRaw {
			if hs, ok := h.(string); ok {
				updated.Hooks = append(updated.Hooks, hs)
			}
		}
	}

	// Update capabilities if provided
	if updated.Capabilities == nil {
		updated.Capabilities = &settings.AgentCapabilities{}
	}
	if temp, ok := params["temperature"].(float64); ok {
		updated.Capabilities.Temperature = temp
	}
	if mt, ok := params["max_turns"].(float64); ok {
		updated.Capabilities.MaxTurns = int(mt)
	}
	if to, ok := params["timeout"].(float64); ok {
		t := int(to)
		if t < 900 {
			t = 900
		}
		updated.Capabilities.Timeout = t
	}

	// Update agent
	if err := t.agentsSettings.UpdateAgent(id, updated); err != nil {
		return tools.NewToolResult(fmt.Sprintf("Error updating agent: %v", err)), nil
	}

	// Return success
	agentJSON, _ := json.MarshalIndent(updated, "", "  ")
	return tools.NewToolResult(fmt.Sprintf("✓ Agent updated successfully:\n%s", string(agentJSON))), nil
}

// DeleteAgentTool deletes an agent
type DeleteAgentTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *DeleteAgentTool) Name() string {
	return "delete_agent"
}

func (t *DeleteAgentTool) Description() string {
	return "Delete a custom agent. Built-in agents cannot be deleted."
}

func (t *DeleteAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to delete",
			},
		},
		"required": []string{"id"},
	}
}

func (t *DeleteAgentTool) Validate(params map[string]any) error        { return nil }
func (t *DeleteAgentTool) IsIdempotent() bool                          { return false }
func (t *DeleteAgentTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *DeleteAgentTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *DeleteAgentTool) RequiresPermission() []tools.Permission      { return nil }

func (t *DeleteAgentTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	id, _ := params["id"].(string)

	if err := t.agentsSettings.DeleteAgent(id); err != nil {
		return tools.NewToolResult(fmt.Sprintf("Error deleting agent: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("✓ Agent '%s' deleted successfully", id)), nil
}

// ListAgentsTool lists all agents
type ListAgentsTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *ListAgentsTool) Name() string {
	return "list_agents"
}

func (t *ListAgentsTool) Description() string {
	return "List all available agents with their configuration"
}

func (t *ListAgentsTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListAgentsTool) Validate(params map[string]any) error        { return nil }
func (t *ListAgentsTool) IsIdempotent() bool                          { return true }
func (t *ListAgentsTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ListAgentsTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *ListAgentsTool) RequiresPermission() []tools.Permission      { return nil }

func (t *ListAgentsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	agents := t.agentsSettings.GetAgents()
	defaultID := t.agentsSettings.GetDefaultAgentID()

	var lines []string
	lines = append(lines, fmt.Sprintf("Total agents: %d", len(agents)))
	lines = append(lines, fmt.Sprintf("Default agent: %s", defaultID))
	lines = append(lines, "")

	for _, agent := range agents {
		isDefault := agent.ID == defaultID
		prefix := " "
		if isDefault {
			prefix = "✓"
		}

		builtinTag := ""
		if agent.Builtin {
			builtinTag = " [built-in]"
		}

		lines = append(lines, fmt.Sprintf("%s %s - %s%s", prefix, agent.ID, agent.Name, builtinTag))
		if agent.Description != "" {
			lines = append(lines, fmt.Sprintf("  Description: %s", agent.Description))
		}
		lines = append(lines, fmt.Sprintf("  Provider: %s, Model: %s", agent.Provider, agent.Model))
		if len(agent.Tools) > 0 {
			toolsStr := strings.Join(agent.Tools, ", ")
			if len(toolsStr) > 80 {
				toolsStr = toolsStr[:77] + "..."
			}
			lines = append(lines, fmt.Sprintf("  Tools: %s", toolsStr))
		}
		if len(agent.Hooks) > 0 {
			lines = append(lines, fmt.Sprintf("  Hooks: %s", strings.Join(agent.Hooks, ", ")))
		}
		if agent.Capabilities != nil {
			lines = append(lines, fmt.Sprintf("  Capabilities: temp=%.1f, turns=%d, timeout=%ds",
				agent.Capabilities.Temperature,
				agent.Capabilities.MaxTurns,
				agent.Capabilities.Timeout))
		}
		lines = append(lines, "")
	}

	return tools.NewToolResult(strings.Join(lines, "\n")), nil
}

// SetDefaultAgentTool sets the default agent
type SetDefaultAgentTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *SetDefaultAgentTool) Name() string {
	return "set_default_agent"
}

func (t *SetDefaultAgentTool) Description() string {
	return "Set the default agent for new conversations"
}

func (t *SetDefaultAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to set as default",
			},
		},
		"required": []string{"id"},
	}
}

func (t *SetDefaultAgentTool) Validate(params map[string]any) error        { return nil }
func (t *SetDefaultAgentTool) IsIdempotent() bool                          { return false }
func (t *SetDefaultAgentTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *SetDefaultAgentTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *SetDefaultAgentTool) RequiresPermission() []tools.Permission      { return nil }

func (t *SetDefaultAgentTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	id, _ := params["id"].(string)

	if err := t.agentsSettings.SetDefaultAgent(id); err != nil {
		return tools.NewToolResult(fmt.Sprintf("Error setting default agent: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("✓ Default agent set to '%s'", id)), nil
}

// CloneAgentTool clones an existing agent
type CloneAgentTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *CloneAgentTool) Name() string {
	return "clone_agent"
}

func (t *CloneAgentTool) Description() string {
	return "Clone an existing agent with a new ID and optional modifications"
}

func (t *CloneAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to clone",
			},
			"new_id": map[string]any{
				"type":        "string",
				"description": "ID for the new cloned agent",
			},
			"new_name": map[string]any{
				"type":        "string",
				"description": "Optional new name for the cloned agent",
			},
		},
		"required": []string{"source_id", "new_id"},
	}
}

func (t *CloneAgentTool) Validate(params map[string]any) error        { return nil }
func (t *CloneAgentTool) IsIdempotent() bool                          { return false }
func (t *CloneAgentTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *CloneAgentTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *CloneAgentTool) RequiresPermission() []tools.Permission      { return nil }

func (t *CloneAgentTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	sourceID, _ := params["source_id"].(string)
	newID, _ := params["new_id"].(string)
	newName, _ := params["new_name"].(string)

	// Get source agent
	source := t.agentsSettings.GetAgent(sourceID)
	if source == nil {
		return tools.NewToolResult(fmt.Sprintf("Error: Agent '%s' not found", sourceID)), nil
	}

	// Clone agent
	cloned := *source
	cloned.ID = newID
	cloned.Builtin = false

	if newName != "" {
		cloned.Name = newName
	} else {
		cloned.Name = source.Name + " (copy)"
	}

	// Create cloned agent
	if err := t.agentsSettings.CreateAgent(cloned); err != nil {
		return tools.NewToolResult(fmt.Sprintf("Error cloning agent: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("✓ Agent '%s' cloned to '%s'", sourceID, newID)), nil
}

// ConfigureToolsTool configures tools for an agent
type ConfigureToolsTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *ConfigureToolsTool) Name() string {
	return "configure_tools"
}

func (t *ConfigureToolsTool) Description() string {
	return "Configure which tools an agent can use. Use ['*'] for all tools or specify individual tool names."
}

func (t *ConfigureToolsTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to configure",
			},
			"tools": map[string]any{
				"type":        "array",
				"description": "List of tool names, or ['*'] for all tools",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
		"required": []string{"id", "tools"},
	}
}

func (t *ConfigureToolsTool) Validate(params map[string]any) error        { return nil }
func (t *ConfigureToolsTool) IsIdempotent() bool                          { return false }
func (t *ConfigureToolsTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ConfigureToolsTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *ConfigureToolsTool) RequiresPermission() []tools.Permission      { return nil }

func (t *ConfigureToolsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	id, _ := params["id"].(string)

	// Parse tools
	toolsList := []string{}
	if toolsRaw, ok := params["tools"].([]any); ok {
		for _, tool := range toolsRaw {
			if ts, ok := tool.(string); ok {
				toolsList = append(toolsList, ts)
			}
		}
	}

	// Get existing agent
	existing := t.agentsSettings.GetAgent(id)
	if existing == nil {
		return tools.NewToolResult(fmt.Sprintf("Error: Agent '%s' not found", id)), nil
	}

	// Update tools
	updated := *existing
	updated.Tools = toolsList

	if err := t.agentsSettings.UpdateAgent(id, updated); err != nil {
		return tools.NewToolResult(fmt.Sprintf("Error configuring tools: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("✓ Tools configured for agent '%s': %s", id, strings.Join(toolsList, ", "))), nil
}

// ConfigureHooksTool configures hooks for an agent
type ConfigureHooksTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *ConfigureHooksTool) Name() string {
	return "configure_hooks"
}

func (t *ConfigureHooksTool) Description() string {
	return "Configure which event hooks are attached to an agent"
}

func (t *ConfigureHooksTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to configure",
			},
			"hooks": map[string]any{
				"type":        "array",
				"description": "List of hook names to attach",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
		"required": []string{"id", "hooks"},
	}
}

func (t *ConfigureHooksTool) Validate(params map[string]any) error        { return nil }
func (t *ConfigureHooksTool) IsIdempotent() bool                          { return false }
func (t *ConfigureHooksTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ConfigureHooksTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *ConfigureHooksTool) RequiresPermission() []tools.Permission      { return nil }

func (t *ConfigureHooksTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	id, _ := params["id"].(string)

	// Parse hooks
	hooksList := []string{}
	if hooksRaw, ok := params["hooks"].([]any); ok {
		for _, hook := range hooksRaw {
			if hs, ok := hook.(string); ok {
				hooksList = append(hooksList, hs)
			}
		}
	}

	// Get existing agent
	existing := t.agentsSettings.GetAgent(id)
	if existing == nil {
		return tools.NewToolResult(fmt.Sprintf("Error: Agent '%s' not found", id)), nil
	}

	// Update hooks
	updated := *existing
	updated.Hooks = hooksList

	if err := t.agentsSettings.UpdateAgent(id, updated); err != nil {
		return tools.NewToolResult(fmt.Sprintf("Error configuring hooks: %v", err)), nil
	}

	hooksStr := "none"
	if len(hooksList) > 0 {
		hooksStr = strings.Join(hooksList, ", ")
	}
	return tools.NewToolResult(fmt.Sprintf("✓ Hooks configured for agent '%s': %s", id, hooksStr)), nil
}

// SetCapabilitiesTool sets agent capabilities
type SetCapabilitiesTool struct {
	agentsSettings *settings.AgentsSettings
}

func (t *SetCapabilitiesTool) Name() string {
	return "set_capabilities"
}

func (t *SetCapabilitiesTool) Description() string {
	return "Set capabilities (temperature, max_turns, timeout) for an agent"
}

func (t *SetCapabilitiesTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to configure",
			},
			"temperature": map[string]any{
				"type":        "number",
				"description": "Temperature (0.0-1.0)",
			},
			"max_turns": map[string]any{
				"type":        "integer",
				"description": "Maximum conversation turns (0 = unlimited)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds",
			},
		},
		"required": []string{"id"},
	}
}

func (t *SetCapabilitiesTool) Validate(params map[string]any) error        { return nil }
func (t *SetCapabilitiesTool) IsIdempotent() bool                          { return false }
func (t *SetCapabilitiesTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *SetCapabilitiesTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *SetCapabilitiesTool) RequiresPermission() []tools.Permission      { return nil }

func (t *SetCapabilitiesTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	id, _ := params["id"].(string)

	// Get existing agent
	existing := t.agentsSettings.GetAgent(id)
	if existing == nil {
		return tools.NewToolResult(fmt.Sprintf("Error: Agent '%s' not found", id)), nil
	}

	// Update capabilities
	updated := *existing
	if updated.Capabilities == nil {
		updated.Capabilities = &settings.AgentCapabilities{}
	}

	if temp, ok := params["temperature"].(float64); ok {
		updated.Capabilities.Temperature = temp
	}
	if mt, ok := params["max_turns"].(float64); ok {
		updated.Capabilities.MaxTurns = int(mt)
	}
	if to, ok := params["timeout"].(float64); ok {
		t := int(to)
		if t < 900 {
			t = 900
		}
		updated.Capabilities.Timeout = t
	}

	if err := t.agentsSettings.UpdateAgent(id, updated); err != nil {
		return tools.NewToolResult(fmt.Sprintf("Error setting capabilities: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("✓ Capabilities updated for agent '%s': temp=%.1f, turns=%d, timeout=%ds",
		id,
		updated.Capabilities.Temperature,
		updated.Capabilities.MaxTurns,
		updated.Capabilities.Timeout)), nil
}
