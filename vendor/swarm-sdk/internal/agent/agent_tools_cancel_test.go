package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

type cancellationTestProvider struct{}

func (cancellationTestProvider) Name() string { return "cancel-test" }

func (cancellationTestProvider) Chat(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, nil
}

func (cancellationTestProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func (cancellationTestProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		FunctionCalling:      true,
		SupportsSystemPrompt: true,
		SupportedModels:      []string{"cancel-test"},
		MaxContextWindow:     128_000,
		MaxOutputTokens:      4_096,
	}
}

type cancellationTestTool struct {
	name string

	mu    sync.Mutex
	calls int
}

func (t *cancellationTestTool) Name() string        { return t.name }
func (t *cancellationTestTool) Description() string { return "records whether it executed" }
func (t *cancellationTestTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *cancellationTestTool) Execute(context.Context, map[string]any) (*tools.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	return tools.NewToolResult("executed"), nil
}
func (t *cancellationTestTool) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

type cancellationTestHooks struct {
	blockedTool string
}

var _ HooksManager = (*cancellationTestHooks)(nil)

func (h *cancellationTestHooks) EmitToolBeforeExecute(_ context.Context, toolName string, _ map[string]any) ([]HookResult, error) {
	if toolName == h.blockedTool {
		return []HookResult{{
			HookName: "policy",
			Blocked:  true,
			Output:   "change the request and retry",
		}}, nil
	}
	return nil, nil
}

func (*cancellationTestHooks) EmitToolAfterExecute(context.Context, string, map[string]any, any, error) []HookResult {
	return nil
}

func (*cancellationTestHooks) EmitProviderResponse(context.Context, string, string, int, int, int64) {
}

func TestHookBlockCancelsBatchWithoutToolErrors(t *testing.T) {
	before := &cancellationTestTool{name: "before"}
	blocked := &cancellationTestTool{name: "blocked"}
	after := &cancellationTestTool{name: "after"}

	ag, err := New(Config{
		Definition: &Definition{ID: "cancel-test", Provider: "cancel-test", Model: "cancel-test"},
		Provider:   cancellationTestProvider{},
		Tools:      []tools.Tool{before, blocked, after},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ag.SetHooksManager(&cancellationTestHooks{blockedTool: blocked.Name()})

	messages, err := ag.executeToolsWithParallelism(context.Background(), &conversation.Message{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{
			{ID: "call-before", Name: before.Name(), Parameters: map[string]any{}},
			{ID: "call-blocked", Name: blocked.Name(), Parameters: map[string]any{}},
			{ID: "call-after", Name: after.Name(), Parameters: map[string]any{}},
		},
	})
	if err != nil {
		t.Fatalf("executeToolsWithParallelism: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want one tool-result message", len(messages))
	}
	if got := len(messages[0].ToolResults); got != 3 {
		t.Fatalf("tool results = %d, want one result per tool call", got)
	}
	for _, tool := range []*cancellationTestTool{before, blocked, after} {
		if got := tool.callCount(); got != 0 {
			t.Errorf("%s calls = %d, want batch execution halted", tool.Name(), got)
		}
	}

	results := messages[0].ToolResults
	for _, result := range results {
		if result.Error != nil {
			t.Errorf("%s result error = %+v, want non-error policy outcome", result.Name, result.Error)
		}
		if result.Output == "" {
			t.Errorf("%s result output is empty, want model-visible non-execution reason", result.Name)
		}
	}
	if got, want := results[1].Output, "Tool 'blocked' blocked by hook: change the request and retry"; got != want {
		t.Errorf("blocked tool output = %q, want %q", got, want)
	}
}
