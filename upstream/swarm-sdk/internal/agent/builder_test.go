package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// stubProvider is a minimal provider.Provider for builder tests.
type stubProvider struct{ name string }

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "ok"},
		FinishReason: provider.FinishReasonStop,
	}, nil
}
func (s *stubProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Delta: "ok", Done: true, FinishReason: provider.FinishReasonStop}
	close(ch)
	return ch, nil
}
func (s *stubProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{SupportedModels: []string{"stub-model"}}
}

type noopTool struct{}

func (n *noopTool) Name() string        { return "noop" }
func (n *noopTool) Description() string { return "does nothing" }
func (n *noopTool) Parameters() any     { return map[string]any{} }
func (n *noopTool) Execute(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
	return tools.NewToolResult(""), nil
}

func TestBuilder_Create(t *testing.T) {
	p := &stubProvider{name: "stub"}

	ag, err := agent.Build().
		Provider(p).
		Model("stub-model").
		ID("test-agent").
		SystemPrompt("You are a test agent.").
		MaxTurns(5).
		Timeout(10 * time.Second).
		Tools(&noopTool{}).
		Create()

	if err != nil {
		t.Fatalf("Build().Create() returned error: %v", err)
	}
	if ag == nil {
		t.Fatal("Build().Create() returned nil agent")
	}
	if ag.ID() != "test-agent" {
		t.Errorf("agent ID = %q, want %q", ag.ID(), "test-agent")
	}

	// Verify the registered tool is accessible
	reg := ag.ToolRegistry()
	if _, err := reg.Get("noop"); err != nil {
		t.Errorf("tool 'noop' not in registry: %v", err)
	}
}

func TestBuilder_RequiresProvider(t *testing.T) {
	_, err := agent.Build().Model("stub-model").Create()
	if err == nil {
		t.Fatal("expected error when Provider is missing, got nil")
	}
}

func TestBuilder_RequiresModel(t *testing.T) {
	_, err := agent.Build().Provider(&stubProvider{name: "stub"}).Create()
	if err == nil {
		t.Fatal("expected error when Model is missing, got nil")
	}
}

func TestBuilder_DefaultID(t *testing.T) {
	p := &stubProvider{name: "myprovider"}
	ag, err := agent.Build().Provider(p).Model("stub-model").Create()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ag.ID() != "myprovider-agent" {
		t.Errorf("default ID = %q, want %q", ag.ID(), "myprovider-agent")
	}
}

func TestBuilder_MustCreate_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustCreate should have panicked for missing provider")
		}
	}()
	agent.Build().Model("stub-model").MustCreate()
}

func TestBuilder_Tools_RegisteredAtConstruction(t *testing.T) {
	p := &stubProvider{name: "stub"}
	ag, err := agent.Build().
		Provider(p).
		Model("stub-model").
		Tools(&noopTool{}).
		Create()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	list := ag.ToolRegistry().List()
	if len(list) == 0 {
		t.Error("expected at least one tool in registry, got 0")
	}
}
