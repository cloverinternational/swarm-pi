package mcptools

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// SDKProvider defines the minimal interface that MCPAssistant needs from the SDK integration.
// This breaks the circular dependency: mcptools/ defines the interface, chat/ implements it.
type SDKProvider interface {
	// GetToolRegistry returns the SDK tool registry for registering MCP management tools
	GetToolRegistry() tools.Registry
	// Logger returns the SDK logger
	Logger() observability.Logger
	// ExecuteAgentsMessage executes a chat message through the SDK provider and returns the response
	ExecuteAgentsMessage(ctx context.Context, userMessage string, history []*conversation.Message, systemPrompt string, providerTools []provider.Tool) (string, error)
}
