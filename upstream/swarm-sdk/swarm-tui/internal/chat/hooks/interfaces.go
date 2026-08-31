package hooks

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// SDKProvider defines the minimal interface that hooks_assistant needs from the SDK integration.
// This breaks the circular dependency: hooks/ defines the interface, chat/ implements it.
type SDKProvider interface {
	// Logger returns the SDK logger
	Logger() observability.Logger
	// Tracer returns the SDK tracer
	Tracer() observability.Tracer
	// PermissionChecker returns the tool permission checker (may be nil)
	PermissionChecker() *tools.InteractivePermissionChecker
	// SetHooksToolExecutor sets the function that executes hooks tools
	SetHooksToolExecutor(executor func(ctx context.Context, name string, params map[string]any) string)
	// ExecuteHooksMessageWithDetails executes a hooks chat message and returns detailed results
	ExecuteHooksMessageWithDetails(ctx context.Context, userMessage string, history []*conversation.Message, systemPrompt string, providerTools []provider.Tool) (*HooksAgentResult, error)
}
