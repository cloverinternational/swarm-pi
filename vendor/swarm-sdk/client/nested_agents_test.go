package client

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestRegisterNestedAgentToolsUsesParentRegistry(t *testing.T) {
	logger := noop.NewLogger()
	tracer := noop.NewTracer()
	providerRegistry := provider.NewSimpleRegistry(logger)
	parent := tools.NewSimpleRegistry(logger, tracer)

	parentTool := &testNestedAgentTool{name: "parent-only"}
	if err := parent.Register(parentTool); err != nil {
		t.Fatal(err)
	}

	err := registerNestedAgentTools(logger, tracer, providerRegistry, parent, provider.Config{
		Name:  "openai",
		Model: "test-model",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("registerNestedAgentTools() error = %v", err)
	}

	for _, name := range []string{
		"Task",
		"Subagent",
		"BackgroundTask",
		"TaskOutput",
		"wait_for_agent",
		"multi_agent_wait",
		"parent-only",
	} {
		if !parent.IsRegistered(name) {
			t.Errorf("parent registry is missing %q", name)
		}
	}
}

func TestNewNestedAgentsIsOptIn(t *testing.T) {
	workspace := t.TempDir()
	makeClient := func(nested bool) *Client {
		opts := []Option{
			WithoutAutoConfig(),
			WithProviderString("openai", "gpt-4o-mini"),
			WithAPIKey("test-key"),
			WithWorkspace(workspace),
		}
		if nested {
			opts = append(opts, WithNestedAgents())
		}
		c, err := New(opts...)
		if err != nil {
			t.Fatalf("New(nested=%v): %v", nested, err)
		}
		return c
	}

	plain := makeClient(false)
	if plain.AgentToolRegistry().IsRegistered("Subagent") {
		t.Error("default client unexpectedly exposes Subagent")
	}
	_ = plain.Close()

	nested := makeClient(true)
	defer nested.Close()
	for _, name := range []string{"Task", "Subagent", "BackgroundTask", "TaskOutput", "wait_for_agent", "multi_agent_wait"} {
		if !nested.AgentToolRegistry().IsRegistered(name) {
			t.Errorf("nested client is missing %q", name)
		}
	}
}

type testNestedAgentTool struct{ name string }

func (t *testNestedAgentTool) Name() string        { return t.name }
func (t *testNestedAgentTool) Description() string { return "test tool" }
func (t *testNestedAgentTool) Parameters() any     { return map[string]any{} }
func (t *testNestedAgentTool) Execute(context.Context, map[string]any) (*tools.ToolResult, error) {
	return &tools.ToolResult{}, nil
}
