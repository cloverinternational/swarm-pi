package mcptools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// MCPAssistant is an AI assistant for managing MCP servers.
// It uses the SDK's ExecuteAgentsMessage to reuse the same OAuth-configured provider.
type MCPAssistant struct {
	sdk             SDKProvider
	mcpTools        *MCPTools
	history         []*conversation.Message
	systemPrompt    string
	mcpInstructions string // Prepended to user messages (OAuth requires minimal system prompt)
	tools           []provider.Tool
}

// NewMCPAssistant creates a new MCP assistant that reuses the SDK's provider.
func NewMCPAssistant(sdk SDKProvider, mcpTools *MCPTools, oauthPrefix string) *MCPAssistant {
	// IMPORTANT: For OAuth, the system prompt must be ONLY the OAuth prefix
	// Any additional instructions must be provided as a user message in the conversation
	// This matches how the main chat works with OAuth
	systemPrompt := oauthPrefix
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI assistant."
	}

	// MCP instructions will be prepended to the first user message
	mcpInstructions := `You are an MCP (Model Context Protocol) server management assistant. Help users configure, manage, and troubleshoot MCP servers.

MCP Server Types:
- **stdio**: Launch server as subprocess (command-based)
  - Required: name, command
  - Optional: args, work_dir, env, timeout
  - Example: npx, uvx, docker, or any local executable

- **sse**: Server-Sent Events endpoint
  - Required: name, url
  - Optional: headers, env, timeout
  - Use for streaming HTTP connections

- **http**: Standard HTTP endpoint
  - Required: name, url
  - Optional: headers, env, timeout
  - Use for REST API-style connections

- **oauth**: OAuth 2.0 authenticated HTTP
  - Required: name, url, client_id
  - Optional: scopes, headers, env, timeout
  - Use for authenticated external services

Common MCP Servers:
1. **Filesystem** (stdio): File system access
   - Command: npx -y @modelcontextprotocol/server-filesystem /path/to/directory

2. **GitHub** (oauth): GitHub API access
   - Type: oauth, requires GitHub OAuth app setup

3. **Git** (stdio): Git repository operations
   - Command: npx -y @modelcontextprotocol/server-git --repository /path/to/repo

4. **Brave Search** (stdio): Web search
   - Command: npx -y @modelcontextprotocol/server-brave-search
   - Requires BRAVE_API_KEY environment variable

5. **Postgres** (stdio): PostgreSQL database access
   - Command: npx -y @modelcontextprotocol/server-postgres
   - Requires DATABASE_URL environment variable

6. **Slack** (oauth): Slack workspace integration
   - Type: oauth, requires Slack OAuth app

7. **Google Drive** (oauth): Google Drive access
   - Type: oauth, requires Google OAuth app

Best Practices:
- Test servers after adding with test_mcp_server tool
- Check server status regularly with get_mcp_server_status
- Disable unused servers to improve performance
- Use environment variables for API keys (never hardcode)
- Set appropriate timeouts (default: 30s, increase for slow servers)
- Configure specific tools if you don't need all tools from a server
- Use descriptive server names (e.g., "github-personal", "work-postgres")

Troubleshooting:
- If server won't connect: Check command/URL, verify dependencies installed
- If tools missing: Server might not be connected, check status
- If tools fail: Check server-specific requirements (API keys, permissions)
- For stdio: Ensure command is in PATH or use absolute path
- For OAuth: Verify client_id and scopes are correct

Be concise. Use the provided tools to manage MCP servers and confirm what was done.

User request: `

	// Build provider.Tool definitions from MCPTools
	var providerTools []provider.Tool
	for _, tool := range mcpTools.GetTools() {
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

	// Register MCP management tools in the SDK's main tool registry so they
	// can be executed by the agentsToolExecutor, but HIDE them so they don't
	// appear in the main chat agent's tool list. Without hiding, these 10
	// management tools pollute the LLM's context and cause it to invoke them
	// unprompted (e.g. calling list_mcp_server_tools to "check" available
	// tools after a task nudge).
	logger := sdk.Logger()
	toolRegistry := sdk.GetToolRegistry()
	if toolRegistry != nil {
		for _, tool := range mcpTools.GetTools() {
			if err := toolRegistry.Register(tool); err != nil {
				logger.Error(context.Background(), "Failed to register MCP tool",
					observability.F("tool", tool.Name()),
					observability.F("error", err.Error()))
			} else {
				// Hide from List() so the main agent doesn't see these tools
				if err := toolRegistry.HideTool(tool.Name()); err != nil {
					logger.Warn(context.Background(), "Failed to hide MCP management tool",
						observability.F("tool", tool.Name()),
						observability.F("error", err.Error()))
				}
				logger.Debug(context.Background(), "Registered MCP management tool (hidden from main agent)",
					observability.F("tool", tool.Name()))
			}
		}
	}

	return &MCPAssistant{
		sdk:             sdk,
		mcpTools:        mcpTools,
		history:         make([]*conversation.Message, 0),
		systemPrompt:    systemPrompt,
		mcpInstructions: mcpInstructions,
		tools:           providerTools,
	}
}

// SendMessage sends a message to the MCP assistant and gets a response.
func (ma *MCPAssistant) SendMessage(ctx context.Context, userMessage string) (string, error) {
	// For the first message, prepend the MCP instructions
	if len(ma.history) == 0 {
		userMessage = ma.mcpInstructions + userMessage
	}

	// Add user message to history
	ma.history = append(ma.history, &conversation.Message{
		Role:      conversation.RoleUser,
		Content:   userMessage,
		Timestamp: time.Now(),
	})

	// Execute using SDK's agents message execution (shares OAuth provider)
	result, err := ma.sdk.ExecuteAgentsMessage(ctx, userMessage, ma.history, ma.systemPrompt, ma.tools)
	if err != nil {
		return "", fmt.Errorf("failed to execute MCP assistant message: %w", err)
	}

	// Extract assistant response
	var assistantResponse strings.Builder
	if result != "" {
		assistantResponse.WriteString(result)
	}

	// Add assistant response to history
	ma.history = append(ma.history, &conversation.Message{
		Role:      conversation.RoleAssistant,
		Content:   assistantResponse.String(),
		Timestamp: time.Now(),
	})

	return strings.TrimSpace(assistantResponse.String()), nil
}

// Reset clears the conversation history
func (ma *MCPAssistant) Reset() {
	ma.history = make([]*conversation.Message, 0)
}

// GetHistory returns the conversation history
func (ma *MCPAssistant) GetHistory() []*conversation.Message {
	return ma.history
}
