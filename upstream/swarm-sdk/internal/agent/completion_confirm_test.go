package agent_test

import (
	"context"
	"sync"
	"testing"

	agent "github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type completionConfirmProvider struct {
	mu       sync.Mutex
	calls    int
	requests []provider.ChatRequest
	reason   provider.FinishReason
}

func (p *completionConfirmProvider) Name() string { return "completion-confirm-mock" }

func (p *completionConfirmProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{MaxContextWindow: 128_000, MaxOutputTokens: 4096}
}

func (p *completionConfirmProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	return &provider.ChatResponse{
		Message: &conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: "complete",
		},
		FinishReason: p.reason,
		Usage:        &conversation.TokenUsage{Input: 10, Output: 1},
	}, nil
}

func (p *completionConfirmProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func newCompletionConfirmAgent(t *testing.T, p provider.Provider, enabled bool, max int) *agent.Agent {
	t.Helper()
	ag, err := agent.New(agent.Config{
		Definition: &agent.Definition{
			ID:       "completion-confirm-test",
			Name:     "Completion Confirm Test",
			Provider: p.Name(),
			Model:    "mock-model",
		},
		Provider:             p,
		CompletionConfirm:    enabled,
		CompletionConfirmMax: max,
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return ag
}

func TestCompletionConfirmFiresExactlyConfiguredMaximum(t *testing.T) {
	p := &completionConfirmProvider{reason: provider.FinishReasonStop}
	ag := newCompletionConfirmAgent(t, p, true, 2)

	var injected int
	ag.SetMessageCallback(func(_ context.Context, msg *conversation.Message) error {
		if msg.Role == conversation.RoleUser && msg.Content != "do the work" {
			injected++
		}
		return nil
	})

	resp, err := ag.Execute(context.Background(), agent.ExecuteRequest{
		Message:  "do the work",
		MaxTurns: 1,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Message != "complete" {
		t.Fatalf("response = %q, want complete", resp.Message)
	}
	if p.calls != 3 {
		t.Fatalf("provider calls = %d, want 3", p.calls)
	}
	if injected != 2 {
		t.Fatalf("persisted confirmation messages = %d, want 2", injected)
	}

	for i := 1; i < len(p.requests); i++ {
		last := p.requests[i].Messages[len(p.requests[i].Messages)-1]
		if last.Role != conversation.RoleUser || last.Content == "" {
			t.Fatalf("request %d does not end with the confirmation user message: %+v", i+1, last)
		}
	}
}

func TestCompletionConfirmDisabledReturnsImmediately(t *testing.T) {
	p := &completionConfirmProvider{reason: provider.FinishReasonStop}
	ag := newCompletionConfirmAgent(t, p, false, 3)

	var injected int
	ag.SetMessageCallback(func(_ context.Context, msg *conversation.Message) error {
		if msg.Role == conversation.RoleUser && msg.Content != "do the work" {
			injected++
		}
		return nil
	})

	if _, err := ag.Execute(context.Background(), agent.ExecuteRequest{Message: "do the work"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls)
	}
	if injected != 0 {
		t.Fatalf("persisted confirmation messages = %d, want 0", injected)
	}
}

func TestCompletionConfirmHandlesEmptyToolCallFinish(t *testing.T) {
	p := &completionConfirmProvider{reason: provider.FinishReasonToolCalls}
	ag := newCompletionConfirmAgent(t, p, true, 1)

	resp, err := ag.Execute(context.Background(), agent.ExecuteRequest{Message: "do the work"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Message != "complete" {
		t.Fatalf("response = %q, want complete", resp.Message)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", p.calls)
	}
}
