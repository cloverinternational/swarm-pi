package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/mock"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// fakeBGManager is a minimal BackgroundAgentManager that serves pre-registered
// background agents by ID. It is sufficient to drive multi_agent_wait, which
// only calls Get().
type fakeBGManager struct {
	agents map[string]*agent.BackgroundAgent
}

func (m *fakeBGManager) Add(bg *agent.BackgroundAgent, _ string) error {
	if m.agents == nil {
		m.agents = map[string]*agent.BackgroundAgent{}
	}
	m.agents[bg.AgentID()] = bg
	return nil
}

func (m *fakeBGManager) Get(id string) (*agent.BackgroundAgent, error) {
	if bg, ok := m.agents[id]; ok {
		return bg, nil
	}
	return nil, context.Canceled // any non-nil error; tool wraps it
}

func (m *fakeBGManager) List() []BackgroundAgentInfo { return nil }
func (m *fakeBGManager) Cancel(string) error         { return nil }
func (m *fakeBGManager) Remove(string) error         { return nil }

// newCompletedBGAgent builds a real BackgroundAgent backed by the mock provider,
// runs it to completion, and returns it already in a terminal state (Done closed).
func newCompletedBGAgent(t *testing.T, id string) *agent.BackgroundAgent {
	t.Helper()
	prov := mock.NewProvider()
	ag, err := agent.New(agent.Config{
		Definition:   &agent.Definition{ID: id, Provider: "mock", Model: "mock-model"},
		Provider:     prov,
		ToolRegistry: tools.NewSimpleRegistry(noop.NewLogger(), noop.NewTracer()),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	bg, err := agent.NewBackgroundAgent(agent.BackgroundAgentConfig{
		Agent:           ag,
		EventBufferSize: 16,
		Logger:          noop.NewLogger(),
		Tracer:          noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewBackgroundAgent: %v", err)
	}
	if err := bg.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-bg.Done() // run to terminal state
	return bg
}

// TestMultiAgentWait_WaitsViaDoneChannel is the regression guard for the bug
// where multi_agent_wait delegated to SubagentOutput's nonexistent "wait" action
// and ALWAYS failed with "invalid action: wait". The fixed tool waits on each
// agent's guaranteed Done() channel and returns a structured summary.
func TestMultiAgentWait_WaitsViaDoneChannel(t *testing.T) {
	mgr := &fakeBGManager{}
	a := newCompletedBGAgent(t, "agent-a")
	b := newCompletedBGAgent(t, "agent-b")
	_ = mgr.Add(a, "task a")
	_ = mgr.Add(b, "task b")

	tool, err := NewMultiAgentWaitTool(MultiAgentWaitConfig{
		BGManager: mgr,
		Logger:    noop.NewLogger(),
		Tracer:    noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewMultiAgentWaitTool: %v", err)
	}

	res, err := tool.Run(context.Background(), MultiAgentWaitParams{
		AgentIDs:       []string{"agent-a", "agent-b"},
		TimeoutSeconds: intPointer(10),
	})
	if err != nil {
		t.Fatalf("Run returned error (regression: tool used to always fail with 'invalid action: wait'): %v", err)
	}
	// Must NOT contain the old failure string.
	if strings.Contains(res.Output, "invalid action") {
		t.Fatalf("output contains the old 'invalid action' failure: %s", res.Output)
	}
	// Both agents must be reported.
	if !strings.Contains(res.Output, "agent-a") || !strings.Contains(res.Output, "agent-b") {
		t.Fatalf("expected both agent IDs in output, got: %s", res.Output)
	}
}

// TestMultiAgentWait_UnknownAgentFailsFast verifies a missing agent ID returns a
// clear error instead of blocking forever.
func TestMultiAgentWait_UnknownAgentFailsFast(t *testing.T) {
	mgr := &fakeBGManager{}
	tool, err := NewMultiAgentWaitTool(MultiAgentWaitConfig{
		BGManager: mgr,
		Logger:    noop.NewLogger(),
		Tracer:    noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewMultiAgentWaitTool: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_, err = tool.Run(context.Background(), MultiAgentWaitParams{
			AgentIDs:       []string{"does-not-exist"},
			TimeoutSeconds: intPointer(5),
		})
		close(done)
	}()

	select {
	case <-done:
		if err == nil {
			t.Fatal("expected error for unknown agent id, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected 'not found' error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked on unknown agent id instead of failing fast")
	}
}

func TestMultiAgentWait_ExpiredParentDeadlineDoesNotConsumeWaitBudget(t *testing.T) {
	mgr := &fakeBGManager{}
	a := newDelayedBackgroundAgent(t, "delayed-a", 30*time.Millisecond)
	b := newDelayedBackgroundAgent(t, "delayed-b", 50*time.Millisecond)
	_ = mgr.Add(a, "task a")
	_ = mgr.Add(b, "task b")
	tool, err := NewMultiAgentWaitTool(MultiAgentWaitConfig{
		BGManager: mgr, Logger: noop.NewLogger(), Tracer: noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewMultiAgentWaitTool: %v", err)
	}
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	result, err := tool.Run(expired, MultiAgentWaitParams{
		AgentIDs: []string{"delayed-a", "delayed-b"}, TimeoutSeconds: intPointer(1),
	})
	if err != nil {
		t.Fatalf("wait inherited expired framework deadline: %v", err)
	}
	var got struct {
		WaitStatus string           `json:"wait_status"`
		Agents     []map[string]any `json:"agents"`
	}
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("result is not structured JSON: %v\n%s", err, result.Output)
	}
	if got.WaitStatus != "completed" || len(got.Agents) != 2 {
		t.Fatalf("unexpected aggregate result: %+v\n%s", got, result.Output)
	}
}

func TestMultiAgentWait_ParentCancellationReturnsPerAgentStates(t *testing.T) {
	mgr := &fakeBGManager{}
	a := newDelayedBackgroundAgent(t, "running-a", time.Hour)
	b := newDelayedBackgroundAgent(t, "running-b", time.Hour)
	_ = mgr.Add(a, "task a")
	_ = mgr.Add(b, "task b")
	tool, err := NewMultiAgentWaitTool(MultiAgentWaitConfig{
		BGManager: mgr, Logger: noop.NewLogger(), Tracer: noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewMultiAgentWaitTool: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := tool.Run(ctx, MultiAgentWaitParams{
		AgentIDs: []string{"running-a", "running-b"}, TimeoutSeconds: intPointer(0),
	})
	if err != nil {
		t.Fatalf("parent cancellation returned generic error: %v", err)
	}
	var got struct {
		WaitStatus string           `json:"wait_status"`
		Agents     []map[string]any `json:"agents"`
	}
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("result is not structured JSON: %v\n%s", err, result.Output)
	}
	if got.WaitStatus != "cancelled" || len(got.Agents) != 2 {
		t.Fatalf("expected cancellation with both agent states: %+v\n%s", got, result.Output)
	}
	for _, state := range got.Agents {
		if state["status"] != string(agent.StatusRunning) {
			t.Fatalf("agent state = %v, want running; output=%s", state, result.Output)
		}
	}
	a.Cancel()
	b.Cancel()
}

func TestMultiAgentWait_TimeoutReturnsStatesWithoutCancellingAgents(t *testing.T) {
	mgr := &fakeBGManager{}
	a := newDelayedBackgroundAgent(t, "timeout-a", time.Hour)
	b := newDelayedBackgroundAgent(t, "timeout-b", time.Hour)
	_ = mgr.Add(a, "task a")
	_ = mgr.Add(b, "task b")
	tool, err := NewMultiAgentWaitTool(MultiAgentWaitConfig{
		BGManager: mgr, Logger: noop.NewLogger(), Tracer: noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewMultiAgentWaitTool: %v", err)
	}

	result, err := tool.Run(context.Background(), MultiAgentWaitParams{
		AgentIDs: []string{"timeout-a", "timeout-b"}, TimeoutSeconds: intPointer(1),
	})
	if err != nil {
		t.Fatalf("timeout returned generic error: %v", err)
	}
	var got struct {
		WaitStatus      string           `json:"wait_status"`
		Agents          []map[string]any `json:"agents"`
		AgentsCancelled bool             `json:"agents_cancelled"`
	}
	if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
		t.Fatalf("result is not structured JSON: %v\n%s", err, result.Output)
	}
	if got.WaitStatus != "timeout" || len(got.Agents) != 2 || got.AgentsCancelled {
		t.Fatalf("unexpected timeout result: %+v\n%s", got, result.Output)
	}
	if a.Status() != agent.StatusRunning || b.Status() != agent.StatusRunning {
		t.Fatalf("timeout cancelled target execution: %s/%s", a.Status(), b.Status())
	}
	a.Cancel()
	b.Cancel()
}

func TestMultiAgentWait_TimeoutPresenceSemantics(t *testing.T) {
	tests := []struct {
		name       string
		raw        map[string]any
		wantBudget float64
		wantError  bool
	}{
		{
			name:       "omitted defaults to 600",
			raw:        map[string]any{"agent_ids": []any{"waiting"}},
			wantBudget: 600,
		},
		{
			name:       "explicit zero is unlimited",
			raw:        map[string]any{"agent_ids": []any{"waiting"}, "timeout_seconds": 0},
			wantBudget: 0,
		},
		{
			name:       "positive owns timer",
			raw:        map[string]any{"agent_ids": []any{"waiting"}, "timeout_seconds": 7},
			wantBudget: 7,
		},
		{
			name:      "negative is invalid",
			raw:       map[string]any{"agent_ids": []any{"waiting"}, "timeout_seconds": -1},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mgr := newMockBackgroundAgentManager()
			bg := newDelayedBackgroundAgent(t, "waiting", time.Hour)
			_ = mgr.Add(bg, "task")
			tool, err := NewMultiAgentWaitTool(MultiAgentWaitConfig{
				BGManager: mgr,
				Logger:    noop.NewLogger(),
				Tracer:    noop.NewTracer(),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result, err := tool.Execute(ctx, test.raw)
			if test.wantError {
				if err == nil {
					t.Fatal("negative timeout succeeded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(result.Output), &got); err != nil {
				t.Fatal(err)
			}
			if got["timeout_seconds"] != test.wantBudget {
				t.Fatalf("timeout_seconds = %v, want %v", got["timeout_seconds"], test.wantBudget)
			}
			bg.Cancel()
		})
	}
}

func TestMultiAgentWait_CustomCauseCancellationDoesNotCancelTarget(t *testing.T) {
	mgr := newMockBackgroundAgentManager()
	bg := newDelayedBackgroundAgent(t, "custom-cause", time.Hour)
	_ = mgr.Add(bg, "task")
	tool, err := NewMultiAgentWaitTool(MultiAgentWaitConfig{
		BGManager: mgr,
		Logger:    noop.NewLogger(),
		Tracer:    noop.NewTracer(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	resultCh := make(chan *tools.ToolResult, 1)
	go func() {
		result, _ := tool.Execute(ctx, map[string]any{
			"agent_ids":       []any{"custom-cause"},
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
