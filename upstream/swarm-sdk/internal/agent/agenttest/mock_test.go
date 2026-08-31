package agenttest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent/agenttest"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestNewAgent_SimpleResponse(t *testing.T) {
	ag := agenttest.NewAgent(t, "The answer is 42.")

	result, err := ag.Run(context.Background(), "What is 6×7?")
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !strings.Contains(result.Message, "42") {
		t.Errorf("unexpected response: %q", result.Message)
	}
}

func TestNewAgent_MultipleResponses(t *testing.T) {
	ag := agenttest.NewAgent(t, "First response.", "Second response.")

	r1, err := ag.Run(context.Background(), "first")
	if err != nil {
		t.Fatalf("first Run() error: %v", err)
	}
	if r1.Message != "First response." {
		t.Errorf("r1.Message = %q, want %q", r1.Message, "First response.")
	}

	// Second Run consumes the next scripted response.
	r2, err := ag.Run(context.Background(), "second")
	if err != nil {
		t.Fatalf("second Run() error: %v", err)
	}
	if r2.Message != "Second response." {
		t.Errorf("r2.Message = %q, want %q", r2.Message, "Second response.")
	}
}

func TestMockProvider_ToolCallCount(t *testing.T) {
	mock := agenttest.NewMockProvider().
		ThenCallTool("my_tool", map[string]any{"x": 1}).
		ThenRespondWith("Done.")

	ag, err := agent.New(agent.Config{
		Definition: &agent.Definition{
			ID:       "tc-test",
			Provider: "mock",
			Model:    "mock",
		},
		Provider: mock,
		Tools:    []tools.Tool{&echoTool{}},
	})
	if err != nil {
		t.Fatalf("agent.New error: %v", err)
	}

	if _, err := ag.Run(context.Background(), "call the tool"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if got := mock.ToolCallCount("my_tool"); got != 1 {
		t.Errorf("ToolCallCount(my_tool) = %d, want 1", got)
	}
	if got := mock.TotalCalls(); got < 2 {
		t.Errorf("TotalCalls() = %d, want ≥2 (tool-call turn + final turn)", got)
	}
}

func TestMockProvider_CallLog(t *testing.T) {
	mock := agenttest.NewMockProvider().RespondWith("hi")

	ag, err := agent.New(agent.Config{
		Definition: &agent.Definition{ID: "log-test", Provider: "mock", Model: "mock"},
		Provider:   mock,
	})
	if err != nil {
		t.Fatalf("agent.New error: %v", err)
	}
	if _, err := ag.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	log := mock.CallLog()
	if len(log) == 0 {
		t.Error("CallLog() returned empty slice, want at least one entry")
	}
	if !strings.Contains(log[0], "call[0]") {
		t.Errorf("unexpected log entry: %q", log[0])
	}
}

func TestMockProvider_ExhaustedSteps_DoesNotError(t *testing.T) {
	// A mock with no scripted steps should return an empty stop response,
	// not an error. The agent must be able to exit naturally.
	mock := agenttest.NewMockProvider()
	ag, err := agent.New(agent.Config{
		Definition: &agent.Definition{ID: "empty-mock", Provider: "mock", Model: "mock"},
		Provider:   mock,
	})
	if err != nil {
		t.Fatalf("agent.New error: %v", err)
	}
	result, err := ag.Run(context.Background(), "anything")
	if err != nil {
		t.Fatalf("Run() on exhausted mock should not error, got: %v", err)
	}
	_ = result
}

// echoTool is a minimal tools.Tool for use in tests — executes instantly.
type echoTool struct{}

func (e *echoTool) Name() string        { return "my_tool" }
func (e *echoTool) Description() string { return "echo tool for tests" }
func (e *echoTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (e *echoTool) Execute(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
	return tools.NewToolResult("executed"), nil
}
