package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// AgentsAssistant is an AI assistant for creating and managing custom agents.
// It uses the SDK's ExecuteAgentsMessage to reuse the same OAuth-configured provider.
type AgentsAssistant struct {
	sdk               *SDKIntegration
	agentTools        *AgentTools
	history           []*conversation.Message
	systemPrompt      string
	agentInstructions string // Prepended to user messages (OAuth requires minimal system prompt)
	tools             []provider.Tool
}

// NewAgentsAssistant creates a new agents assistant that reuses the SDK's provider.
func NewAgentsAssistant(sdk *SDKIntegration, agentTools *AgentTools, oauthPrefix string) *AgentsAssistant {
	// IMPORTANT: For OAuth, the system prompt must be ONLY the OAuth prefix
	// Any additional instructions must be provided as a user message in the conversation
	// This matches how the main chat works with OAuth
	systemPrompt := oauthPrefix
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI assistant."
	}

	// Agent instructions will be prepended to the first user message
	agentInstructions := `You are an agents configuration assistant. Help users create, manage, and customize AI agents.
This system is compatible with custom agent configurations.

Agent Components:
- ID: kebab-case identifier (e.g., "code-reviewer", "security-auditor", "doc-writer")
- Name: Display name for the agent
- Description: Brief description of what the agent does
- Provider: anthropic, openai, openrouter, etc.
- Model: claude-sonnet-4-20250514, gpt-4, etc.
- System Prompt: Instructions defining agent behavior and capabilities
- Tools: Available tools - use ["*"] for all tools, or specify individual names
- Hooks: Event hooks to attach to the agent
- Capabilities:
  * temperature: Randomness 0.0-1.0 (default: 0.7)
  * max_turns: Maximum conversation turns, 0=unlimited (default: 20)
  * timeout: Timeout in seconds (default: 900, minimum: 900 = 15 minutes)
- Color: UI color in hex format (e.g., "#6366F1")
- Icon: Emoji icon for the agent (e.g., "🤖", "🔍", "🔬")

Built-in Agents (cannot be deleted):
- general-assistant: Multipurpose AI assistant
- code-reviewer: Code review and analysis specialist
- research-agent: Exploration and information gathering
- background-worker: Long-running task executor

Common Use Cases:
1. Security Auditor: Code review tools, low temp (0.3), security-focused prompt
2. Documentation Writer: File access tools, creative temp (0.8), writing-focused prompt
3. Test Generator: Code analysis + bash tools, structured prompt for test creation
4. Bug Hunter: Full tool access, thorough exploration prompt, high timeout
5. API Designer: File + bash tools, structured thinking prompt
6. Refactoring Agent: Code editing tools, careful prompt with patterns

Tool Categories:
- File operations: file_read, file_write, list_dir
- Search: grep
- Execution: bash
- All tools: Use ["*"] in tools array

Be concise. Use the provided tools to create/modify agents and confirm what was done.

User request: `

	// Build provider.Tool definitions from AgentTools
	var providerTools []provider.Tool
	for _, tool := range agentTools.GetTools() {
		params := tool.Parameters()
		paramsMap, ok := params.(map[string]any)
		if !ok {
			continue
		}
		providerTools = append(providerTools, provider.Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  paramsMap,
		})
	}

	agentRegistry := tools.NewSimpleRegistry(sdk.logger, sdk.tracer)
	if sdk.permissionChecker != nil {
		agentRegistry.SetPermissionChecker(sdk.permissionChecker)
	}
	for _, tool := range agentTools.GetTools() {
		if err := agentRegistry.Register(tool); err != nil {
			logDebug("Failed to register agent tool: %v", err)
		}
	}

	// Set up the tool executor in SDK
	// This executor checks BOTH the agents registry and the SDK's main registry
	// This allows both agent management tools AND MCP management tools to work
	mainRegistry := sdk.GetToolRegistry()
	sdk.SetAgentsToolExecutor(func(ctx context.Context, name string, params map[string]any) string {
		// Try agents registry first
		result, err := agentRegistry.Execute(ctx, name, params)
		if err == nil {
			if result != nil && result.Output != "" {
				return result.Output
			}
			if result != nil && len(result.Content) > 0 {
				return fmt.Sprintf("%v", result.Content[0])
			}
			return "Tool executed successfully"
		}

		// If not found in agents registry, try main registry (for MCP tools, etc.)
		if mainRegistry != nil {
			exec := tools.NewExecutor(mainRegistry, nil)
			result, err = exec.Execute(ctx, name, params)
			if err == nil {
				if result != nil && result.Output != "" {
					return result.Output
				}
				if result != nil && len(result.Content) > 0 {
					return fmt.Sprintf("%v", result.Content[0])
				}
				return "Tool executed successfully"
			}
		}

		return fmt.Sprintf("Error: %v", err)
	})

	return &AgentsAssistant{
		sdk:               sdk,
		agentTools:        agentTools,
		history:           make([]*conversation.Message, 0),
		systemPrompt:      systemPrompt,
		agentInstructions: agentInstructions,
		tools:             providerTools,
	}
}

// Chat sends a message and gets a response using the SDK's provider.
func (aa *AgentsAssistant) Chat(ctx context.Context, userMessage string) (string, error) {
	result, err := aa.ChatWithDetails(ctx, userMessage)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// ChatWithDetails sends a message and returns detailed response including tool calls.
func (aa *AgentsAssistant) ChatWithDetails(ctx context.Context, userMessage string) (*AgentsAgentResponse, error) {
	if aa.sdk == nil {
		return nil, fmt.Errorf("agents assistant not properly initialized")
	}

	// For OAuth, we MUST use minimal system prompt
	// Agent instructions are prepended to the first user message only
	messageToSend := userMessage
	if len(aa.history) == 0 && aa.agentInstructions != "" {
		messageToSend = aa.agentInstructions + userMessage
	}

	// Execute using SDK's detailed method (reuses OAuth-configured provider)
	sdkResult, err := aa.sdk.ExecuteAgentsMessageWithDetails(ctx, messageToSend, aa.history, aa.systemPrompt, aa.tools)
	if err != nil {
		// Try fallback to secondary model if primary fails
		logDebug("[AgentsAssistant] Primary model failed: %v, attempting fallback", err)
		fallbackResult, fallbackErr := aa.sdk.ExecuteAgentsMessageWithDetailsFallback(ctx, messageToSend, aa.history, aa.systemPrompt, aa.tools)
		if fallbackErr != nil {
			// Both failed, return original error
			return &AgentsAgentResponse{Error: err.Error()}, err
		}
		// Fallback succeeded
		logDebug("[AgentsAssistant] Fallback model succeeded")
		sdkResult = fallbackResult
		err = nil // Clear error since fallback worked
	}

	// Convert SDK result to AgentsAgentResponse
	response := &AgentsAgentResponse{
		Content:    sdkResult.Content,
		ToolCalls:  make([]AgentToolCall, 0, len(sdkResult.ToolCalls)),
		TotalTurns: sdkResult.TotalTurns,
	}

	for _, tc := range sdkResult.ToolCalls {
		agentTC := AgentToolCall{
			Name:      tc.Name,
			Input:     tc.Input,
			InputJSON: tc.InputJSON,
			Result:    tc.Result,
			Success:   tc.Success,
			Duration:  tc.Duration,
			PreHooks:  make([]AgentHookExecution, 0, len(tc.PreHooks)),
			PostHooks: make([]AgentHookExecution, 0, len(tc.PostHooks)),
		}
		// Copy pre-hooks
		for _, ph := range tc.PreHooks {
			agentTC.PreHooks = append(agentTC.PreHooks, AgentHookExecution(ph))
		}
		// Copy post-hooks
		for _, ph := range tc.PostHooks {
			agentTC.PostHooks = append(agentTC.PostHooks, AgentHookExecution(ph))
		}
		response.ToolCalls = append(response.ToolCalls, agentTC)
	}

	// Add the ORIGINAL user message to history (not with instructions prepended)
	aa.history = append(aa.history, &conversation.Message{
		ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
		Role:      conversation.RoleUser,
		Content:   userMessage,
		Timestamp: time.Now(),
	})

	// Add response to history (include tool call info)
	if sdkResult.Content != "" {
		msg := &conversation.Message{
			ID:        fmt.Sprintf("assistant-%d", time.Now().UnixNano()),
			Role:      conversation.RoleAssistant,
			Content:   sdkResult.Content,
			Timestamp: time.Now(),
		}
		// Store tool calls in message for history display
		for _, tc := range sdkResult.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, conversation.ToolCall{
				ID:         fmt.Sprintf("tc-%d", time.Now().UnixNano()),
				Name:       tc.Name,
				Parameters: tc.Input,
			})
		}
		aa.history = append(aa.history, msg)
	}

	return response, nil
}

// ClearHistory clears the conversation history.
func (aa *AgentsAssistant) ClearHistory() {
	aa.history = make([]*conversation.Message, 0)
}

// GetHistory returns the conversation history.
func (aa *AgentsAssistant) GetHistory() []*conversation.Message {
	return aa.history
}

// AgentsAgentResponse contains structured response data from the agents assistant
type AgentsAgentResponse struct {
	Content    string          `json:"content"`     // Final assistant response (may include markdown)
	ToolCalls  []AgentToolCall `json:"tool_calls"`  // All tool calls made during this turn
	TotalTurns int             `json:"total_turns"` // Number of LLM turns (including tool result handling)
	Error      string          `json:"error,omitempty"`
}

// QuickCreate creates an agent from a simple description without full chat.
func (aa *AgentsAssistant) QuickCreate(ctx context.Context, description string) (string, error) {
	prompt := fmt.Sprintf(`Create an agent based on this description: "%s"

Use the create_agent tool. Choose appropriate:
- id (kebab-case, descriptive like "security-auditor")
- name (display name)
- description (what it does)
- provider (anthropic recommended)
- model (claude-sonnet-4-20250514 for general use, opus for complex tasks)
- system_prompt (clear instructions on behavior)
- tools (["*"] for all, or specific tools)
- capabilities (adjust temperature, timeout based on use case)
- color and icon (for UI)

After creating, briefly confirm what was created.`, description)

	return aa.Chat(ctx, prompt)
}

// SuggestAgents suggests useful agent configurations based on common needs.
func (aa *AgentsAssistant) SuggestAgents(ctx context.Context) (string, error) {
	prompt := `Based on common use cases, suggest 3 useful custom agents the user might want to create:

1. A specialized agent for their workflow
2. A productivity-focused agent
3. A quality assurance agent

For each, explain what it does and ask if they'd like you to create it.`

	return aa.Chat(ctx, prompt)
}

// CreateExampleAgent creates a sample agent for demonstration.
func (aa *AgentsAssistant) CreateExampleAgent(ctx context.Context, agentType string) (string, error) {
	var prompt string

	switch strings.ToLower(agentType) {
	case "security":
		prompt = `Create a security auditor agent with these specifications:
- ID: "security-auditor"
- Name: "Security Auditor"
- Description: "Analyzes code for security vulnerabilities and best practices"
- Provider: anthropic
- Model: claude-sonnet-4-20250514
- System Prompt: Focus on security analysis, OWASP top 10, common vulnerabilities
- Tools: ["file_read", "grep", "", "list_dir"]
- Temperature: 0.3 (precise, consistent)
- Max Tokens: 8192
- Icon: "🔒"
- Color: "#EF4444"

Create this agent now and confirm.`

	case "documentation":
		prompt = `Create a documentation writer agent with these specifications:
- ID: "doc-writer"
- Name: "Documentation Writer"
- Description: "Creates comprehensive documentation for code and projects"
- Provider: anthropic
- Model: claude-sonnet-4-20250514
- System Prompt: Focus on clear, comprehensive documentation with examples
- Tools: ["*"]
- Temperature: 0.8 (creative, varied output)
- Max Tokens: 16384
- Icon: "📝"
- Color: "#3B82F6"

Create this agent now and confirm.`

	case "tester":
		prompt = `Create a test generator agent with these specifications:
- ID: "test-generator"
- Name: "Test Generator"
- Description: "Generates comprehensive unit and integration tests"
- Provider: anthropic
- Model: claude-sonnet-4-20250514
- System Prompt: Focus on comprehensive test coverage, edge cases, mocking
- Tools: ["file_read", "file_write", "bash", "grep"]
- Temperature: 0.5
- Max Tokens: 12288
- Icon: "🧪"
- Color: "#10B981"

Create this agent now and confirm.`

	default:
		return "", fmt.Errorf("unknown agent type: %s (use security, documentation, or tester)", agentType)
	}

	return aa.Chat(ctx, prompt)
}

// GetToolCount returns the number of registered tools
func (aa *AgentsAssistant) GetToolCount() int {
	return len(aa.tools)
}

// ListTools returns the names of all registered tools
func (aa *AgentsAssistant) ListTools() []string {
	var names []string
	for _, tool := range aa.tools {
		names = append(names, tool.Name)
	}
	return names
}

// IsInitialized returns true if the assistant was properly initialized
func (aa *AgentsAssistant) IsInitialized() bool {
	return aa.sdk != nil && len(aa.tools) > 0
}

// GetLastError returns formatted error details for debugging
func (aa *AgentsAssistant) GetLastError(err error) string {
	if err == nil {
		return ""
	}
	// Extract useful info from error
	errStr := err.Error()
	if strings.Contains(errStr, "oauth") || strings.Contains(errStr, "OAuth") {
		return i18n.T("final.assistant.oauth_error", errStr)
	}
	if strings.Contains(errStr, "401") || strings.Contains(errStr, "unauthorized") {
		return i18n.T("final.assistant.authentication_error", errStr)
	}
	if strings.Contains(errStr, "400") {
		return i18n.T("final.assistant.request_error", errStr)
	}
	return errStr
}
