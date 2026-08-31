package hooks

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// mockSDKProvider implements SDKProvider for testing.
type mockSDKProvider struct {
	permissionChecker *tools.InteractivePermissionChecker
	toolExecutor      func(ctx context.Context, name string, params map[string]any) string
}

func (m *mockSDKProvider) Logger() observability.Logger {
	return observability.NewNopLogger()
}

func (m *mockSDKProvider) Tracer() observability.Tracer {
	return nil
}

func (m *mockSDKProvider) PermissionChecker() *tools.InteractivePermissionChecker {
	return m.permissionChecker
}

func (m *mockSDKProvider) SetHooksToolExecutor(executor func(ctx context.Context, name string, params map[string]any) string) {
	m.toolExecutor = executor
}

func (m *mockSDKProvider) ExecuteHooksMessageWithDetails(ctx context.Context, userMessage string, history []*conversation.Message, systemPrompt string, providerTools []provider.Tool) (*HooksAgentResult, error) {
	return &HooksAgentResult{Content: "mock response"}, nil
}

func TestHooksAssistant_ToolsAlwaysAllowed(t *testing.T) {
	// Hooks no longer require permissions - they're always allowed
	// Individual hooks can be enabled/disabled via their config
	checker := tools.NewInteractivePermissionChecker(tools.DefaultPermissionConfig(), nil)
	sdk := &mockSDKProvider{
		permissionChecker: checker,
	}

	hookTools := NewHookTools(NewHooksConfig(), nil, t.TempDir(), nil)
	_ = NewHooksAssistant(sdk, hookTools, "")

	output := sdk.toolExecutor(context.Background(), "list_hooks", map[string]any{})
	if strings.Contains(output, "permission denied") {
		t.Fatalf("expected hooks tool to run without permission check, got: %s", output)
	}
}
