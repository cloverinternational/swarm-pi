package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type outputBudgetProvider struct{}

func (*outputBudgetProvider) Name() string { return "output-budget" }
func (*outputBudgetProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{MaxContextWindow: 100_000, MaxOutputTokens: 20_000}
}
func (*outputBudgetProvider) Chat(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, nil
}
func (*outputBudgetProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

func TestBlockingLimitReservesOutputBudget(t *testing.T) {
	a := &Agent{provider: &outputBudgetProvider{}}
	if got, want := a.getBlockingLimit(), 77_000; got != want {
		t.Fatalf("blocking limit = %d, want %d", got, want)
	}
}

// TestContextPressureHardLimitBlocksWithoutCompactor asserts that notification
// alone never permits an unsafe provider request.
func TestContextPressureHardLimitBlocksWithoutCompactor(t *testing.T) {
	a := &Agent{
		configuredContextWindow: 100_000,
		inputTokens:             98_000,
		logger:                  observability.NewNopLogger(),
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
		},
	}
	var updates []IntermediateUpdate
	a.intermediateCallback = func(_ context.Context, update IntermediateUpdate) error {
		updates = append(updates, update)
		return nil
	}
	messages := []*conversation.Message{{Role: conversation.RoleUser, Content: "work"}}
	notified := false

	_, err := a.checkContextPressure(context.Background(), &messages, a.inputTokens, &notified)

	var pressureErr *ContextPressureError
	if !errors.As(err, &pressureErr) {
		t.Fatalf("error = %v, want *ContextPressureError", err)
	}
	if len(updates) != 1 || updates[0].UpdateType() != "compaction_needed" {
		t.Fatalf("updates = %#v, want one compaction_needed", updates)
	}
}

func TestContextPressurePropagatesCompactionFailure(t *testing.T) {
	want := errors.New("summary backend unavailable")
	a := &Agent{
		configuredContextWindow: 100_000,
		inputTokens:             98_000,
		logger:                  observability.NewNopLogger(),
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
			CompactFunc: func(
				context.Context,
				[]*conversation.Message,
				*compaction.CompactionContext,
			) ([]*conversation.Message, error) {
				return nil, want
			},
		},
	}
	messages := []*conversation.Message{{Role: conversation.RoleUser, Content: "work"}}
	notified := false

	_, err := a.checkContextPressure(context.Background(), &messages, a.inputTokens, &notified)

	var pressureErr *ContextPressureError
	if !errors.As(err, &pressureErr) || !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped compaction failure", err)
	}
	if pressureErr.Stage != "compaction_failed" {
		t.Fatalf("stage = %q, want compaction_failed", pressureErr.Stage)
	}
}

func TestContextPressureThresholdSignalAllowsSafeTurnBoundary(t *testing.T) {
	a := &Agent{
		configuredContextWindow: 100_000,
		inputTokens:             50_000,
		logger:                  observability.NewNopLogger(),
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdFixedTokens,
				Value: 40_000,
			},
		},
	}
	var updates int
	a.intermediateCallback = func(_ context.Context, update IntermediateUpdate) error {
		if update.UpdateType() == "compaction_needed" {
			updates++
		}
		return nil
	}
	messages := []*conversation.Message{{Role: conversation.RoleUser, Content: "work"}}
	notified := false

	if _, err := a.checkContextPressure(context.Background(), &messages, a.inputTokens, &notified); err != nil {
		t.Fatalf("safe threshold returned error: %v", err)
	}
	if updates != 1 || !notified {
		t.Fatalf("updates=%d notified=%v, want one/true", updates, notified)
	}
}

func TestContextPressureRejectsUnchangedCompactorOutput(t *testing.T) {
	messages := []*conversation.Message{{Role: conversation.RoleUser, Content: "unchanged"}}
	a := &Agent{
		configuredContextWindow: 100_000,
		inputTokens:             98_000,
		logger:                  observability.NewNopLogger(),
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdFixedTokens,
				Value: 40_000,
			},
			CompactFunc: func(
				context.Context,
				[]*conversation.Message,
				*compaction.CompactionContext,
			) ([]*conversation.Message, error) {
				return messages, nil
			},
		},
	}
	notified := false
	_, err := a.checkContextPressure(context.Background(), &messages, a.inputTokens, &notified)
	var pressureErr *ContextPressureError
	if !errors.As(err, &pressureErr) || !strings.Contains(err.Error(), "unchanged") {
		t.Fatalf("error = %v, want unchanged-context pressure error", err)
	}
}

func TestContextPressureRejectsSemanticallyUnchangedClone(t *testing.T) {
	messages := []*conversation.Message{{ID: "same", Role: conversation.RoleUser, Content: "unchanged"}}
	a := &Agent{
		configuredContextWindow: 100_000,
		inputTokens:             98_000,
		logger:                  observability.NewNopLogger(),
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
			Threshold:            AutoCompactionThreshold{Mode: AutoCompactionThresholdFixedTokens, Value: 40_000},
			CompactFunc: func(
				context.Context,
				[]*conversation.Message,
				*compaction.CompactionContext,
			) ([]*conversation.Message, error) {
				return []*conversation.Message{messages[0].Clone()}, nil
			},
		},
	}
	notified := false
	_, err := a.checkContextPressure(context.Background(), &messages, a.inputTokens, &notified)
	if err == nil || !strings.Contains(err.Error(), "unchanged") {
		t.Fatalf("error = %v, want structural unchanged rejection", err)
	}
}

func TestContextPressurePreservesFixedRequestOverhead(t *testing.T) {
	messages := []*conversation.Message{{
		ID:      "history",
		Role:    conversation.RoleUser,
		Content: strings.Repeat("history ", 100),
	}}
	a := &Agent{
		provider:    &outputBudgetProvider{},
		inputTokens: 80_000,
		logger:      observability.NewNopLogger(),
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
			CompactFunc: func(
				context.Context,
				[]*conversation.Message,
				*compaction.CompactionContext,
			) ([]*conversation.Message, error) {
				return []*conversation.Message{{
					ID:      "summary",
					Role:    conversation.RoleUser,
					Content: "small summary",
				}}, nil
			},
		},
	}
	notified := false
	_, err := a.checkContextPressure(context.Background(), &messages, a.inputTokens, &notified)
	if err == nil || !strings.Contains(err.Error(), "remains unsafe") {
		t.Fatalf("error = %v, want fixed-overhead unsafe rejection", err)
	}
}
