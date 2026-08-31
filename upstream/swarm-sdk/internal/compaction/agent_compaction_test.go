package compaction

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type agentCompactionProvider struct {
	err             error
	maxOutputTokens int
}

func (*agentCompactionProvider) Name() string { return "agent-compaction-test" }

func (p *agentCompactionProvider) Capabilities() provider.Capabilities {
	maxOutput := p.maxOutputTokens
	if maxOutput == 0 {
		maxOutput = 8_192
	}
	return provider.Capabilities{
		MaxContextWindow: 200_000,
		MaxOutputTokens:  maxOutput,
	}
}

func (p *agentCompactionProvider) Chat(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &provider.ChatResponse{
		Message: &conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: boundedTestSummary,
		},
	}, nil
}

func (*agentCompactionProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

func TestAgentPostCompactBudgetIncludesOutputReservationAndMargin(t *testing.T) {
	got := agentPostCompactBudget(AgentCompactionConfig{
		Provider:     &agentCompactionProvider{maxOutputTokens: 20_000},
		ContextLimit: 128_000,
	})
	if want := 95_000; got != want {
		t.Fatalf("agentPostCompactBudget() = %d, want %d", got, want)
	}
}

func TestCreateCompactFuncBuildsActiveGeneration(t *testing.T) {
	result, err := CreateCompactFunc(AgentCompactionConfig{
		Provider:     &agentCompactionProvider{},
		Model:        "test-model",
		ContextLimit: 200_000,
	})
	if err != nil {
		t.Fatalf("CreateCompactFunc: %v", err)
	}

	original := []*conversation.Message{
		{ID: "user", Role: conversation.RoleUser, Content: strings.Repeat("request ", 2_000)},
		{ID: "assistant", Role: conversation.RoleAssistant, Content: strings.Repeat("response ", 2_000)},
	}
	compacted, err := result.CompactFunc(context.Background(), original, &CompactionContext{})
	if err != nil {
		t.Fatalf("CompactFunc: %v", err)
	}
	if len(compacted) == 0 {
		t.Fatal("CompactFunc returned no active messages")
	}
	if reflect.DeepEqual(compacted, original) {
		t.Fatal("CompactFunc returned the unchanged transcript")
	}
	if got, max := EstimateMessagesTokens(compacted), 160_000; got > max {
		t.Fatalf("compacted generation estimated at %d tokens, exceeds budget %d", got, max)
	}
}

func TestCreateCompactFuncContinuesWithDeterministicRecovery(t *testing.T) {
	summaryErr := errors.New("summary backend unavailable")
	result, err := CreateCompactFunc(AgentCompactionConfig{
		Provider:     &agentCompactionProvider{err: summaryErr},
		Model:        "test-model",
		ContextLimit: 200_000,
	})
	if err != nil {
		t.Fatalf("CreateCompactFunc: %v", err)
	}

	compacted, err := result.CompactFunc(context.Background(), []*conversation.Message{
		{ID: "user", Role: conversation.RoleUser, Content: "Preserve the current implementation state."},
	}, &CompactionContext{})
	if err != nil {
		t.Fatalf("deterministic recovery should keep the agent running: %v", err)
	}
	if len(compacted) == 0 {
		t.Fatal("deterministic recovery returned no active messages")
	}
	if !strings.Contains(compacted[0].Content, "deterministic recovery") {
		t.Fatalf("active generation does not identify deterministic recovery: %q", compacted[0].Content)
	}
	if !strings.Contains(compacted[0].Content, summaryErr.Error()) {
		t.Fatalf("active generation does not preserve summary failure: %q", compacted[0].Content)
	}
}
