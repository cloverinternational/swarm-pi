package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/mock"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

type delayedMockProvider struct {
	*mock.Provider
	release <-chan struct{}
}

func (p *delayedMockProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	select {
	case <-p.release:
		return p.Provider.Stream(ctx, req)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newDelayedBackgroundAgent(t *testing.T, id string, delay time.Duration) *agent.BackgroundAgent {
	t.Helper()
	release := make(chan struct{})
	prov := &delayedMockProvider{Provider: mock.NewProvider(), release: release}
	ag, err := agent.New(agent.Config{
		Definition:   &agent.Definition{ID: id, Provider: "mock", Model: "mock-model"},
		Provider:     prov,
		ToolRegistry: tools.NewSimpleRegistry(noop.NewLogger(), noop.NewTracer()),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	bg, err := agent.NewBackgroundAgent(agent.BackgroundAgentConfig{
		Agent: ag, Logger: noop.NewLogger(), Tracer: noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewBackgroundAgent: %v", err)
	}
	if err := bg.Start(context.Background(), "delayed task"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(bg.Cancel)
	go func() {
		time.Sleep(delay)
		close(release)
	}()
	return bg
}

// mockBackgroundAgentManager is a test double for BackgroundAgentManager
type mockBackgroundAgentManager struct {
	agents map[string]*agent.BackgroundAgent
}

func newMockBackgroundAgentManager() *mockBackgroundAgentManager {
	return &mockBackgroundAgentManager{
		agents: make(map[string]*agent.BackgroundAgent),
	}
}

func (m *mockBackgroundAgentManager) Add(bg *agent.BackgroundAgent, task string) error {
	m.agents[bg.AgentID()] = bg
	return nil
}

func (m *mockBackgroundAgentManager) Get(agentID string) (*agent.BackgroundAgent, error) {
	bg, ok := m.agents[agentID]
	if !ok {
		return nil, errors.New("agent not found")
	}
	return bg, nil
}

func (m *mockBackgroundAgentManager) List() []BackgroundAgentInfo {
	return nil
}

func (m *mockBackgroundAgentManager) Cancel(agentID string) error {
	return nil
}

func (m *mockBackgroundAgentManager) Remove(agentID string) error {
	delete(m.agents, agentID)
	return nil
}

func TestWaitForAgent_MissingAgentID(t *testing.T) {
	mgr := newMockBackgroundAgentManager()
	tool, err := NewWaitForAgentTool(WaitForAgentConfig{
		BGManager: mgr,
		Logger:    noop.NewLogger(),
		Tracer:    noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("Failed to create tool: %v", err)
	}

	_, err = tool.Execute(context.Background(), map[string]any{
		"agent_id": "", // Missing agent_id
	})

	if err == nil {
		t.Error("Expected error for missing agent_id, got nil")
	}
}

func TestWaitForAgent_AgentNotFound(t *testing.T) {
	mgr := newMockBackgroundAgentManager()
	tool, err := NewWaitForAgentTool(WaitForAgentConfig{
		BGManager: mgr,
		Logger:    noop.NewLogger(),
		Tracer:    noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("Failed to create tool: %v", err)
	}

	_, err = tool.Execute(context.Background(), map[string]any{
		"agent_id": "non-existent-agent",
	})

	if err == nil {
		t.Error("Expected error for non-existent agent, got nil")
	}
}

func TestIsTerminalStatus(t *testing.T) {
	tests := []struct {
		status   agent.BackgroundAgentStatus
		expected bool
	}{
		{agent.StatusCompleted, true},
		{agent.StatusFailed, true},
		{agent.StatusCancelled, true},
		{agent.StatusRunning, false},
		{agent.StatusPending, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := isTerminalStatus(tt.status)
			if result != tt.expected {
				t.Errorf("isTerminalStatus(%s) = %v, want %v", tt.status, result, tt.expected)
			}
		})
	}
}

func TestWaitForAgentTool_Description(t *testing.T) {
	tool := &WaitForAgentTool{}
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
	if tool.Name() != "wait_for_agent" {
		t.Errorf("Expected name 'wait_for_agent', got '%s'", tool.Name())
	}
}

func TestWaitForAgentTool_IsIdempotent(t *testing.T) {
	tool := &WaitForAgentTool{}
	if tool.IsIdempotent() {
		t.Error("wait_for_agent should not be idempotent")
	}
}

func TestWaitForAgentTool_SupportsParallel(t *testing.T) {
	tool := &WaitForAgentTool{}
	if tool.SupportsParallel() {
		t.Error("wait_for_agent should not support parallel execution")
	}
}

// TestWaitForAgent_Timeout tests that the tool returns timeout error when agent doesn't complete
func TestWaitForAgent_Timeout(t *testing.T) {
	mgr := newMockBackgroundAgentManager()

	tool, err := NewWaitForAgentTool(WaitForAgentConfig{
		BGManager: mgr,
		Logger:    noop.NewLogger(),
		Tracer:    noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("Failed to create tool: %v", err)
	}

	// Use a very short timeout for testing
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = tool.Execute(ctx, map[string]any{
		"agent_id":        "test-agent",
		"timeout_seconds": 1,
	})

	// Should get an error (either timeout or agent not found)
	if err == nil {
		t.Error("Expected error for timeout or missing agent")
	}
}

func TestWaitForAgent_ExpiredParentDeadlineDoesNotConsumeWaitBudget(t *testing.T) {
	mgr := newMockBackgroundAgentManager()
	bg := newDelayedBackgroundAgent(t, "delayed-agent", 40*time.Millisecond)
	_ = mgr.Add(bg, "task")
	rawTool, err := NewWaitForAgentTool(WaitForAgentConfig{BGManager: mgr})
	if err != nil {
		t.Fatalf("NewWaitForAgentTool: %v", err)
	}

	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result, err := rawTool.Execute(expired, map[string]any{
		"agent_id":        "delayed-agent",
		"timeout_seconds": 1,
	})
	if err != nil {
		t.Fatalf("wait inherited expired framework deadline: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("result is not structured JSON: %v\n%s", err, result.Output)
	}
	if got["status"] != string(agent.StatusCompleted) {
		t.Fatalf("status = %v, want completed; output=%s", got["status"], result.Output)
	}
}

func TestWaitForAgent_ExplicitTargetCancellationIsStructured(t *testing.T) {
	mgr := newMockBackgroundAgentManager()
	bg := newDelayedBackgroundAgent(t, "cancelled-agent", time.Hour)
	_ = mgr.Add(bg, "task")
	rawTool, err := NewWaitForAgentTool(WaitForAgentConfig{BGManager: mgr})
	if err != nil {
		t.Fatalf("NewWaitForAgentTool: %v", err)
	}
	bg.Cancel()

	result, err := rawTool.Execute(context.Background(), map[string]any{
		"agent_id":        "cancelled-agent",
		"timeout_seconds": 0,
	})
	if err != nil {
		t.Fatalf("explicit target cancellation returned generic error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("result is not structured JSON: %v\n%s", err, result.Output)
	}
	if got["status"] != string(agent.StatusCancelled) {
		t.Fatalf("status = %v, want cancelled; output=%s", got["status"], result.Output)
	}
}

func TestWaitForAgent_TimeoutPresenceSemantics(t *testing.T) {
	tests := []struct {
		name       string
		raw        map[string]any
		wantBudget float64
	}{
		{
			name:       "omitted defaults to 600 seconds",
			raw:        map[string]any{"agent_id": "waiting-agent"},
			wantBudget: 600,
		},
		{
			name: "explicit zero disables timeout",
			raw: map[string]any{
				"agent_id":        "waiting-agent",
				"timeout_seconds": 0,
			},
			wantBudget: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := newMockBackgroundAgentManager()
			bg := newDelayedBackgroundAgent(t, "waiting-agent", time.Hour)
			_ = mgr.Add(bg, "task")
			rawTool, err := NewWaitForAgentTool(WaitForAgentConfig{BGManager: mgr})
			if err != nil {
				t.Fatalf("NewWaitForAgentTool: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result, err := rawTool.Execute(ctx, tt.raw)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
				t.Fatalf("result is not structured JSON: %v\n%s", err, result.Output)
			}
			if got["wait_status"] != "cancelled" {
				t.Fatalf("wait_status = %v, want cancelled; output=%s", got["wait_status"], result.Output)
			}
			if got["timeout_seconds"] != tt.wantBudget {
				t.Fatalf("timeout_seconds = %v, want %v; output=%s", got["timeout_seconds"], tt.wantBudget, result.Output)
			}
		})
	}
}

func TestWaitForAgent_NegativeTimeoutIsInvalid(t *testing.T) {
	mgr := newMockBackgroundAgentManager()
	rawTool, err := NewWaitForAgentTool(WaitForAgentConfig{BGManager: mgr})
	if err != nil {
		t.Fatalf("NewWaitForAgentTool: %v", err)
	}

	_, err = rawTool.Execute(context.Background(), map[string]any{
		"agent_id":        "unused",
		"timeout_seconds": -1,
	})
	if err == nil {
		t.Fatal("negative timeout must be rejected")
	}
}

func TestWaitForAgent_CustomCauseCancellationStopsUnlimitedWaitWithoutCancellingTarget(t *testing.T) {
	mgr := newMockBackgroundAgentManager()
	bg := newDelayedBackgroundAgent(t, "custom-cause", time.Hour)
	_ = mgr.Add(bg, "task")
	rawTool, err := NewWaitForAgentTool(WaitForAgentConfig{BGManager: mgr})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	resultCh := make(chan *tools.ToolResult, 1)
	go func() {
		result, _ := rawTool.Execute(ctx, map[string]any{
			"agent_id":        "custom-cause",
			"timeout_seconds": 0,
		})
		resultCh <- result
	}()
	cancel(errors.New("session shutdown"))
	select {
	case result := <-resultCh:
		var got map[string]any
		if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
			t.Fatal(err)
		}
		if got["wait_status"] != "cancelled" {
			t.Fatalf("wait_status = %v, want cancelled", got["wait_status"])
		}
	case <-time.After(time.Second):
		t.Fatal("custom-cause cancellation did not stop unlimited wait")
	}
	if bg.Status() != agent.StatusRunning {
		t.Fatalf("wait cancellation changed target status to %s", bg.Status())
	}
	bg.Cancel()
}
