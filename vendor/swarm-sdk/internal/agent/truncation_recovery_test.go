package agent_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	agent "github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// truncatingProvider scripts a sequence of responses and records every
// request it receives. Response 1 simulates a per-response output-token cap
// (FinishReasonLength) with NO complete tool calls — exactly what killed the
// skill-curator sub-agent mid-consolidation: its long SkillManage(create)
// call was truncated and the whole run aborted with zero work persisted.
type truncatingProvider struct {
	mu        sync.Mutex
	calls     int
	requests  []provider.ChatRequest
	responses []*provider.ChatResponse
}

func (p *truncatingProvider) Name() string { return "truncating-mock" }

func (p *truncatingProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		MaxContextWindow: 128_000,
		MaxOutputTokens:  4096,
	}
}

func (p *truncatingProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	idx := p.calls
	p.calls++
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}
	return p.responses[idx], nil
}

func (p *truncatingProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

// TestExecuteRecoversFromTruncatedResponse: one FinishReasonLength response
// must NOT terminate the agentic loop. The agent should be told its output
// was truncated and be given another turn (Hermes runs its curator fork with
// max_iterations=9999 for exactly this reason).
func TestExecuteRecoversFromTruncatedResponse(t *testing.T) {
	p := &truncatingProvider{
		responses: []*provider.ChatResponse{
			{
				Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "partial output that got cut o"},
				FinishReason: provider.FinishReasonLength,
				Usage:        &conversation.TokenUsage{Input: 10, Output: 4096},
			},
			{
				Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "recovered and finished the task"},
				FinishReason: provider.FinishReasonStop,
				Usage:        &conversation.TokenUsage{Input: 20, Output: 12},
			},
		},
	}

	ag, err := agent.Build().Provider(p).Model("mock-model").Create()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := ag.Execute(context.Background(), agent.ExecuteRequest{Message: "do the work"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if resp.Message != "recovered and finished the task" {
		t.Errorf("final message = %q, want the post-recovery response", resp.Message)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (truncated turn + recovery turn)", p.calls)
	}

	// The recovery request must keep the loop agentic: it should carry a
	// truncation notice, not the terminal "provide your final response"
	// summarization notice (which strips tools and ends the run).
	last := p.requests[1]
	var noticeFound bool
	for _, m := range last.Messages {
		if m == nil {
			continue
		}
		if strings.Contains(m.Content, "truncated") {
			noticeFound = true
		}
		if strings.Contains(m.Content, "You MUST now provide a final response") {
			t.Errorf("recovery turn used the terminal summarization notice — loop was ended, not recovered")
		}
	}
	if !noticeFound {
		t.Error("recovery request carries no truncation notice for the model")
	}
}

// TestExecuteTruncationRecoveryIsBounded: a provider that ALWAYS truncates
// must not loop forever — after a bounded number of consecutive truncations
// the agent falls back to the summarize-and-stop path.
func TestExecuteTruncationRecoveryIsBounded(t *testing.T) {
	p := &truncatingProvider{
		responses: []*provider.ChatResponse{
			{
				Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "always truncated"},
				FinishReason: provider.FinishReasonLength,
				Usage:        &conversation.TokenUsage{Input: 10, Output: 4096},
			},
		},
	}

	ag, err := agent.Build().Provider(p).Model("mock-model").Create()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resp, err := ag.Execute(context.Background(), agent.ExecuteRequest{Message: "do the work"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp == nil || resp.Message == "" {
		t.Error("expected a non-empty terminal message from the bounded fallback")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// 3 truncated turns + 1 summarization call is the expected ceiling.
	if p.calls > 5 {
		t.Errorf("provider calls = %d, want a small bounded number", p.calls)
	}
}
