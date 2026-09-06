package hooks

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

// HooksAssistant is an AI assistant for creating and managing hooks.
// It uses the SDK's ExecuteHooksMessage to reuse the same OAuth-configured provider.
type HooksAssistant struct {
	sdk              SDKProvider
	hookTools        *HookTools
	history          []*conversation.Message
	systemPrompt     string
	hookInstructions string // Prepended to user messages (OAuth requires minimal system prompt)
	tools            []provider.Tool
}

// NewHooksAssistant creates a new hooks assistant that reuses the SDK's provider.
func NewHooksAssistant(sdk SDKProvider, hookTools *HookTools, oauthPrefix string) *HooksAssistant {
	// IMPORTANT: For OAuth, the system prompt must be ONLY the OAuth prefix
	// Any additional instructions must be provided as a user message in the conversation
	// This matches how the main chat works with OAuth
	systemPrompt := oauthPrefix
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI assistant."
	}

	// Hook instructions will be prepended to the first user message
	hookInstructions := `You are a hooks configuration assistant. Help users create, manage, and test event hooks.
This system supports both Claude Code and Gemini CLI hook naming conventions.

**CRITICAL: ALWAYS validate tool_matcher patterns before creating hooks!**

AVAILABLE TOOLS:
- list_available_tools: Show all registered tool names (USE THIS FIRST!)
- validate_hook: Test tool_matcher pattern before creating
- create_hook: Create a new hook (auto-validates tool_matcher)
- update_hook: Update an existing hook
- list_hooks: List all hooks
- get_hook: Get hook details
- enable_hook / disable_hook: Toggle hook status
- test_hook: Test a hook with simulated event
- list_hook_events: Show all event types

TOOL NAMES ARE CASE-SENSITIVE AND SNAKE_CASE:
⚠️  file_write (NOT 'Write' or 'write')
⚠️  apply_patch (NOT 'Edit' or 'edit')
⚠️  file_read (NOT 'Read')
⚠️  Bash (capitalized, NOT 'bash')
⚠️  mcp_<server>_<tool> (for MCP tools)

WORKFLOW FOR CREATING HOOKS:
1. Use 'list_available_tools' to see actual tool names
2. Use 'validate_hook' to test your tool_matcher pattern
3. Only then use 'create_hook' with validated pattern
4. Use 'enable_hook' to activate the hook

EVENT TYPES (use any naming convention):

Session Events:
- session.start / SessionStart: Session begins
- session.end / SessionEnd: Session ends

Agent Events:
- user.prompt_submit / UserPromptSubmit / BeforeAgent: Before processing prompt
- agent.stop / Stop / AfterAgent: Agent completes
- subagent.stop / SubagentStop: Subagent stops

Tool Events:
- tool.before_execute / PreToolUse / BeforeTool: Before tool runs (can block)
- tool.after_execute / PostToolUse / AfterTool: After tool completes

Model Events (Gemini CLI):
- BeforeModel: Before LLM request
- AfterModel: After LLM response
- BeforeToolSelection: Before tool selection

Other:
- compact.before / PreCompact / PreCompress: Before compaction
- notification / Notification: For notifications

TOOL MATCHER EXAMPLES:
✅ CORRECT: "file_write|apply_patch" (matches file modifications)
✅ CORRECT: "Bash" (matches Bash tool)
✅ CORRECT: "file_.*" (matches all file_* tools)
✅ CORRECT: "mcp_.*" (matches all MCP tools)
❌ WRONG: "Write|Edit" (these tool names don't exist)
❌ WRONG: "bash" (lowercase won't match 'Bash')

ENVIRONMENT VARIABLES:
- CLAUDE_PROJECT_DIR: Project directory
- SWARMOS_TOOL_NAME: Tool being executed
- SWARMOS_TOOL_PARAMS: Parameters as JSON
- SWARMOS_TOOL_FILE_PATH: File path (if applicable)
- SWARMOS_HOOK_EVENT: Event type

HOOK ACTIONS:
- "continue": Always continue (logging/observability)
- "block_exit2": Block on exit code 2 (Claude Code default - recommended)
- "block": Block on any non-zero exit
- "block_on_output": Block if stdout produced

EXIT CODES: 0=success, 1=non-blocking error, 2=blocking error

DEFAULTS: timeout=60s, action=block_exit2, priority=50

CONFIG LOCATIONS (loaded in order):
1. .claude/settings.json (project - highest priority)
2. ~/.claude/settings.json (user)
3. ~/.swarmos/hooks.json (system)

Be concise. Use the appropriate tool and confirm the result.

User request: `

	// Build provider.Tool definitions from HookTools
	var providerTools []provider.Tool
	for _, tool := range hookTools.GetTools() {
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

	hookRegistry := tools.NewSimpleRegistry(sdk.Logger(), sdk.Tracer())
	if pc := sdk.PermissionChecker(); pc != nil {
		hookRegistry.SetPermissionChecker(pc)
	}
	for _, tool := range hookTools.GetTools() {
		if err := hookRegistry.Register(tool); err != nil {
			logDebug("Failed to register hook tool: %v", err)
		}
	}

	// Set up the tool executor in SDK
	sdk.SetHooksToolExecutor(func(ctx context.Context, name string, params map[string]any) string {
		result, err := hookRegistry.Execute(ctx, name, params)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		if result != nil && result.Output != "" {
			return result.Output
		}
		if result != nil && len(result.Content) > 0 {
			return fmt.Sprintf("%v", result.Content[0])
		}
		return "Tool executed successfully"
	})

	return &HooksAssistant{
		sdk:              sdk,
		hookTools:        hookTools,
		history:          make([]*conversation.Message, 0),
		systemPrompt:     systemPrompt,
		hookInstructions: hookInstructions,
		tools:            providerTools,
	}
}

// Chat sends a message and gets a response using the SDK's provider.
func (ha *HooksAssistant) Chat(ctx context.Context, userMessage string) (string, error) {
	result, err := ha.ChatWithDetails(ctx, userMessage)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// ChatWithDetails sends a message and returns detailed response including tool calls.
func (ha *HooksAssistant) ChatWithDetails(ctx context.Context, userMessage string) (*HooksAgentResponse, error) {
	if ha.sdk == nil {
		return nil, fmt.Errorf("hooks assistant not properly initialized")
	}

	// For OAuth, we MUST use minimal system prompt
	// Hook instructions are prepended to the first user message only
	messageToSend := userMessage
	if len(ha.history) == 0 && ha.hookInstructions != "" {
		messageToSend = ha.hookInstructions + userMessage
	}

	// Execute using SDK's detailed method (reuses OAuth-configured provider)
	sdkResult, err := ha.sdk.ExecuteHooksMessageWithDetails(ctx, messageToSend, ha.history, ha.systemPrompt, ha.tools)
	if err != nil {
		return &HooksAgentResponse{Error: err.Error()}, err
	}

	// Convert SDK result to HooksAgentResponse
	response := &HooksAgentResponse{
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
	ha.history = append(ha.history, &conversation.Message{
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
		ha.history = append(ha.history, msg)
	}

	return response, nil
}

// ClearHistory clears the conversation history.
func (ha *HooksAssistant) ClearHistory() {
	ha.history = make([]*conversation.Message, 0)
}

// GetHistory returns the conversation history.
func (ha *HooksAssistant) GetHistory() []*conversation.Message {
	return ha.history
}

// HooksAgentResponse contains structured response data from the hooks agent
type HooksAgentResponse struct {
	Content    string          `json:"content"`     // Final assistant response (may include markdown)
	ToolCalls  []AgentToolCall `json:"tool_calls"`  // All tool calls made during this turn
	TotalTurns int             `json:"total_turns"` // Number of LLM turns (including tool result handling)
	Error      string          `json:"error,omitempty"`
}

// AgentHookExecution represents a hook that ran during tool execution
type AgentHookExecution struct {
	HookName string `json:"hook_name"`
	Phase    string `json:"phase"` // "pre" or "post"
	Success  bool   `json:"success"`
	Blocked  bool   `json:"blocked"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// AgentToolCall represents a tool call with its result
type AgentToolCall struct {
	Name      string               `json:"name"`
	Input     map[string]any       `json:"input"`
	InputJSON string               `json:"input_json"` // Pretty-printed JSON for display
	Result    string               `json:"result"`
	Success   bool                 `json:"success"`
	Duration  time.Duration        `json:"duration"`
	PreHooks  []AgentHookExecution `json:"pre_hooks,omitempty"`
	PostHooks []AgentHookExecution `json:"post_hooks,omitempty"`
}

// ChatMessage represents a message in the hooks assistant chat.
type ChatMessage struct {
	Role      string         `json:"role"` // "user", "assistant", "tool"
	Content   string         `json:"content"`
	ToolCalls []ChatToolCall `json:"tool_calls,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// ChatToolCall represents a tool call in the chat.
type ChatToolCall struct {
	Name   string         `json:"name"`
	Input  map[string]any `json:"input"`
	Result string         `json:"result,omitempty"`
}

// FormatHistory formats the conversation history for display.
func (ha *HooksAssistant) FormatHistory() []ChatMessage {
	var messages []ChatMessage

	for _, msg := range ha.history {
		cm := ChatMessage{
			Role:      string(msg.Role),
			Content:   msg.Content,
			Timestamp: msg.Timestamp,
		}

		// Add tool calls if present
		for _, tc := range msg.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, ChatToolCall{
				Name:  tc.Name,
				Input: tc.Parameters,
			})
		}

		messages = append(messages, cm)
	}

	return messages
}

// QuickCreate creates a hook from a simple description without full chat.
func (ha *HooksAssistant) QuickCreate(ctx context.Context, description string) (string, error) {
	prompt := fmt.Sprintf(`Create a hook based on this description: "%s"

Use the create_hook tool to create it. Choose appropriate:
- name (kebab-case, descriptive)
- event_patterns (use tool.before_execute or tool.after_execute for tool hooks)
- tool_matcher (if filtering specific tools like "Bash" or "Edit|Write")
- command (shell command - use $CLAUDE_PROJECT_DIR, $TOOL_NAME, etc.)
- priority (99 for security, 50 for general)
- action (block_exit2 recommended - exit 2 blocks, other codes don't)
- timeout (60s default)

After creating, briefly confirm what was created.`, description)

	return ha.Chat(ctx, prompt)
}

// SuggestHooks suggests hooks based on the user's workflow.
func (ha *HooksAssistant) SuggestHooks(ctx context.Context) (string, error) {
	prompt := `Based on common use cases, suggest 3 useful hooks the user might want to create:

1. A logging/observability hook
2. A security hook
3. A notification hook

For each, explain what it does and ask if they'd like you to create it.`

	return ha.Chat(ctx, prompt)
}

// CreateDebugHook creates a simple debug logging hook for testing.
func (ha *HooksAssistant) CreateDebugHook(ctx context.Context) (string, error) {
	prompt := `Create a simple debug hook with these specifications:
- Name: "debug-logger"
- Description: "Logs all tool executions to a debug file for testing"
- Events: tool.before_execute, tool.after_execute
- Command: echo "[$(date +%H:%M:%S)] $HOOK_EVENT_TYPE: $TOOL_NAME in $CLAUDE_PROJECT_DIR" >> ~/.swarmos/hook_debug.log
- Action: continue (never block - just log)
- Priority: 90
- Timeout: 60s

Create this hook now and confirm when done.`

	return ha.Chat(ctx, prompt)
}

// CreateBlockingHook creates a sample hook that blocks certain operations.
func (ha *HooksAssistant) CreateBlockingHook(ctx context.Context, pattern string) (string, error) {
	prompt := fmt.Sprintf(`Create a security hook that blocks operations matching this pattern: "%s"

Specifications:
- Name: "security-blocker"
- Description: "Blocks operations matching dangerous patterns"
- Events: tool.before_execute (or PreToolUse)
- tool_matcher: Use if pattern is a tool name like "Bash" or "rm|mv"
- Command: Check if pattern matches, exit 2 to block (Claude Code semantics)
- Action: block_exit2 (exit code 2 = block)
- Priority: 99 (highest - security)
- Timeout: 60s

Create this hook and explain what it will block.`, pattern)

	return ha.Chat(ctx, prompt)
}

// GetToolCount returns the number of registered tools
func (ha *HooksAssistant) GetToolCount() int {
	return len(ha.tools)
}

// ListTools returns the names of all registered tools
func (ha *HooksAssistant) ListTools() []string {
	var names []string
	for _, tool := range ha.tools {
		names = append(names, tool.Name)
	}
	return names
}

// IsInitialized returns true if the assistant was properly initialized
func (ha *HooksAssistant) IsInitialized() bool {
	return ha.sdk != nil && len(ha.tools) > 0
}

// GetLastError returns formatted error details for debugging
func (ha *HooksAssistant) GetLastError(err error) string {
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
