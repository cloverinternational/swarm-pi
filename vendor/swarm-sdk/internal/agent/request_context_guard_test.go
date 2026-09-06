package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

type requestGuardProvider struct {
	requests []provider.ChatRequest
}

func (p *requestGuardProvider) Name() string { return "request-guard" }

func (p *requestGuardProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{MaxContextWindow: 100_000}
}

func (p *requestGuardProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return &provider.ChatResponse{
		Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "done"},
		FinishReason: provider.FinishReasonStop,
		Usage:        &conversation.TokenUsage{Input: 1_000, Output: 10},
	}, nil
}

func (*requestGuardProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

type emergencyGuardProvider struct {
	requests []provider.ChatRequest
	retryErr error
}

func (p *emergencyGuardProvider) Name() string { return "emergency-guard" }

func (p *emergencyGuardProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{MaxContextWindow: 100_000}
}

func (p *emergencyGuardProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) == 1 {
		return nil, errors.New("context_length_exceeded")
	}
	if p.retryErr != nil {
		return nil, p.retryErr
	}
	return &provider.ChatResponse{
		Message:      &conversation.Message{Role: conversation.RoleAssistant, Content: "recovered"},
		FinishReason: provider.FinishReasonStop,
		Usage:        &conversation.TokenUsage{Input: 500, Output: 10},
	}, nil
}

func (*emergencyGuardProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

func TestCurrentRequestTokensPreservesRealUsageWithoutGrowth(t *testing.T) {
	req := provider.ChatRequest{
		Messages:     []*conversation.Message{{Role: conversation.RoleUser, Content: "hello"}},
		SystemPrompt: "system",
		Tools:        []provider.Tool{{Name: "read", Description: "read a file"}},
	}
	a := &Agent{
		inputTokens:         42_000,
		lastRequestEstimate: estimateProviderRequestTokens(req),
	}

	if got := a.currentRequestTokens(req); got != 42_000 {
		t.Fatalf("current request tokens = %d, want real usage 42000", got)
	}
}

func TestCurrentRequestTokensAddsOnlyUnobservedRequestGrowth(t *testing.T) {
	previous := provider.ChatRequest{
		Messages:     []*conversation.Message{{Role: conversation.RoleUser, Content: "hello"}},
		SystemPrompt: "system",
		Tools:        []provider.Tool{{Name: "read", Description: "read a file"}},
	}
	current := previous
	current.Messages = append(current.Messages, &conversation.Message{
		Role: conversation.RoleTool,
		ToolResults: []conversation.ToolResult{{
			Name:   "read",
			Output: strings.Repeat("large tool output ", 2_000),
		}},
	})
	current.SystemPrompt += strings.Repeat(" policy", 500)
	current.Tools = append(current.Tools, provider.Tool{
		Name:        "large_tool",
		Description: strings.Repeat("schema description ", 500),
		Parameters:  map[string]any{"description": strings.Repeat("parameter ", 500)},
	})

	previousEstimate := estimateProviderRequestTokens(previous)
	currentEstimate := estimateProviderRequestTokens(current)
	a := &Agent{inputTokens: 40_000, lastRequestEstimate: previousEstimate}

	want := 40_000 + currentEstimate - previousEstimate
	if got := a.currentRequestTokens(current); got != want {
		t.Fatalf("current request tokens = %d, want %d", got, want)
	}
}

func TestCurrentRequestTokensWithoutBaselineUsesLargerEvidence(t *testing.T) {
	req := provider.ChatRequest{
		Messages: []*conversation.Message{{
			Role:    conversation.RoleUser,
			Content: strings.Repeat("request ", 4_000),
		}},
	}
	estimated := estimateProviderRequestTokens(req)

	if got := (&Agent{inputTokens: estimated + 100}).currentRequestTokens(req); got != estimated+100 {
		t.Fatalf("historical real usage lost: got %d want %d", got, estimated+100)
	}
	if got := (&Agent{inputTokens: 1}).currentRequestTokens(req); got != estimated {
		t.Fatalf("first request estimate lost: got %d want %d", got, estimated)
	}
}

func TestExecuteLoopCompactsInjectedGrowthBeforeProvider(t *testing.T) {
	prov := &requestGuardProvider{}
	injected := false
	compactCalls := 0
	a := &Agent{
		definition:  &Definition{ID: "guard", Name: "guard", Model: "test"},
		provider:    prov,
		toolReg:     tools.NewRegistry(),
		logger:      noop.NewLogger(),
		tracer:      noop.NewTracer(),
		inputTokens: 10_000,
		messageInjector: func() []string {
			if injected {
				return nil
			}
			injected = true
			return []string{strings.Repeat("injected context ", 12_000)}
		},
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
				compactCalls++
				return []*conversation.Message{{
					Role:    conversation.RoleUser,
					Content: "compacted context",
				}}, nil
			},
		},
	}
	messages := []*conversation.Message{{Role: conversation.RoleUser, Content: "initial"}}
	a.lastRequestEstimate = estimateProviderRequestTokens(a.buildProviderRequest(messages))

	if _, _, err := a.executeLoop(context.Background(), messages, 2); err != nil {
		t.Fatalf("executeLoop: %v", err)
	}
	if compactCalls != 1 {
		t.Fatalf("CompactFunc calls = %d, want 1", compactCalls)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(prov.requests))
	}
	if got := prov.requests[0].Messages; len(got) != 1 || got[0].Content != "compacted context" {
		t.Fatalf("provider received stale request: %#v", got)
	}
}

func TestEmergencyCompactionUsesSharedPersistenceAndRebuiltRequest(t *testing.T) {
	prov := &emergencyGuardProvider{}
	persistCalls := 0
	doneUpdates := 0
	a := &Agent{
		definition:  &Definition{ID: "emergency", Name: "emergency", Model: "test"},
		provider:    prov,
		toolReg:     tools.NewRegistry(),
		logger:      noop.NewLogger(),
		tracer:      noop.NewTracer(),
		inputTokens: 10_000,
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdFixedTokens,
				Value: 90_000,
			},
			CompactFunc: func(
				context.Context,
				[]*conversation.Message,
				*compaction.CompactionContext,
			) ([]*conversation.Message, error) {
				return []*conversation.Message{{
					Role:    conversation.RoleUser,
					Content: "emergency summary",
				}}, nil
			},
		},
		compactionPersistCallback: func(
			context.Context,
			string,
			[]*conversation.Message,
			string,
			int,
		) error {
			persistCalls++
			return nil
		},
	}
	a.intermediateCallback = func(_ context.Context, update IntermediateUpdate) error {
		if _, ok := update.(CompactionDoneUpdate); ok {
			doneUpdates++
		}
		return nil
	}
	messages := []*conversation.Message{{
		Role:    conversation.RoleUser,
		Content: strings.Repeat("large original context ", 2_000),
	}}
	a.lastRequestEstimate = estimateProviderRequestTokens(a.buildProviderRequest(messages))

	if _, _, err := a.executeLoop(context.Background(), messages, 2); err != nil {
		t.Fatalf("executeLoop: %v", err)
	}
	if persistCalls != 1 || doneUpdates != 1 {
		t.Fatalf("persist calls=%d done updates=%d, want 1/1", persistCalls, doneUpdates)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d, want initial plus one retry", len(prov.requests))
	}
	got := prov.requests[1].Messages
	if len(got) != 1 || got[0].Content != "emergency summary" {
		t.Fatalf("retry used stale request: %#v", got)
	}
}

func TestRunInLoopCompactionDoesNotPublishFailedPersistence(t *testing.T) {
	persistErr := errors.New("durable store unavailable")
	doneUpdates := 0
	original := &conversation.Message{
		Role:    conversation.RoleUser,
		Content: strings.Repeat("original context ", 2_000),
	}
	messages := []*conversation.Message{original}
	a := &Agent{
		logger:      noop.NewLogger(),
		inputTokens: 12_000,
		compactionPersistCallback: func(
			context.Context,
			string,
			[]*conversation.Message,
			string,
			int,
		) error {
			return persistErr
		},
		intermediateCallback: func(_ context.Context, update IntermediateUpdate) error {
			if _, ok := update.(CompactionDoneUpdate); ok {
				doneUpdates++
			}
			return nil
		},
	}

	_, err := a.runInLoopCompaction(
		context.Background(),
		&messages,
		12_000,
		90_000,
		func(context.Context, []*conversation.Message, *compaction.CompactionContext) ([]*conversation.Message, error) {
			return []*conversation.Message{{
				Role:    conversation.RoleUser,
				Content: "compacted context",
			}}, nil
		},
	)
	if !errors.Is(err, persistErr) {
		t.Fatalf("runInLoopCompaction error = %v, want persistence cause", err)
	}
	if len(messages) != 1 || messages[0] != original || messages[0].Content != original.Content {
		t.Fatalf("failed persistence changed live messages: %#v", messages)
	}
	if a.inputTokens != 12_000 {
		t.Fatalf("inputTokens = %d, want unchanged 12000", a.inputTokens)
	}
	if doneUpdates != 0 {
		t.Fatalf("CompactionDoneUpdate count = %d, want 0", doneUpdates)
	}
}

func TestRunInLoopCompactionIsolatesMutatingCompactorFailure(t *testing.T) {
	compactErr := errors.New("compactor failed after mutation")
	original := &conversation.Message{
		Role:    conversation.RoleUser,
		Content: strings.Repeat("original context ", 2_000),
		Metadata: map[string]any{
			"nested": map[string]any{"value": "original"},
		},
		ToolCalls: []conversation.ToolCall{{
			Name:       "read",
			Parameters: map[string]any{"path": "original.txt"},
		}},
	}
	messages := []*conversation.Message{original}
	a := &Agent{logger: noop.NewLogger()}

	_, err := a.runInLoopCompaction(
		context.Background(),
		&messages,
		12_000,
		90_000,
		func(_ context.Context, candidate []*conversation.Message, _ *compaction.CompactionContext) ([]*conversation.Message, error) {
			candidate[0].Content = "mutated"
			candidate[0].Metadata["nested"].(map[string]any)["value"] = "mutated"
			candidate[0].ToolCalls[0].Parameters["path"] = "mutated.txt"
			return nil, compactErr
		},
	)
	if !errors.Is(err, compactErr) {
		t.Fatalf("runInLoopCompaction error = %v, want compactor cause", err)
	}
	if messages[0] != original || original.Content == "mutated" {
		t.Fatalf("compactor mutated live message: %#v", original)
	}
	if got := original.Metadata["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("nested metadata = %v, want original", got)
	}
	if got := original.ToolCalls[0].Parameters["path"]; got != "original.txt" {
		t.Fatalf("tool parameter = %v, want original.txt", got)
	}
}

func TestRunInLoopCompactionRejectsCyclicMetadataBeforeCompactor(t *testing.T) {
	cyclic := make(map[string]any)
	cyclic["self"] = cyclic
	messages := []*conversation.Message{{
		Role:     conversation.RoleUser,
		Content:  "original",
		Metadata: cyclic,
	}}
	compactCalls := 0
	a := &Agent{logger: noop.NewLogger()}

	_, err := a.runInLoopCompaction(
		context.Background(),
		&messages,
		12_000,
		90_000,
		func(context.Context, []*conversation.Message, *compaction.CompactionContext) ([]*conversation.Message, error) {
			compactCalls++
			return nil, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "clone compaction messages") {
		t.Fatalf("runInLoopCompaction error = %v, want clone failure", err)
	}
	if compactCalls != 0 {
		t.Fatalf("CompactFunc calls = %d, want 0", compactCalls)
	}
}

func TestExecuteLoopRechecksHardLimitAfterRebuildingCompactedRequest(t *testing.T) {
	prov := &requestGuardProvider{}
	a := &Agent{
		definition: &Definition{ID: "rebuild-guard", Name: "rebuild-guard", Model: "test"},
		provider:   prov,
		toolReg:    tools.NewRegistry(),
		logger:     noop.NewLogger(),
		tracer:     noop.NewTracer(),
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdFixedTokens,
				Value: 10_000,
			},
			CompactFunc: func(
				context.Context,
				[]*conversation.Message,
				*compaction.CompactionContext,
			) ([]*conversation.Message, error) {
				return []*conversation.Message{{
					Role:    conversation.RoleUser,
					Content: "compacted context",
				}}, nil
			},
		},
		ephemeralSystemFn: func(messages []*conversation.Message) string {
			if len(messages) == 1 && messages[0].Content == "compacted context" {
				return strings.Repeat("expanded request overhead ", 100_000)
			}
			return ""
		},
	}
	messages := []*conversation.Message{{
		Role:    conversation.RoleUser,
		Content: strings.Repeat("large original context ", 8_000),
	}}

	_, _, err := a.executeLoop(context.Background(), messages, 2)
	var pressureErr *ContextPressureError
	if !errors.As(err, &pressureErr) {
		t.Fatalf("executeLoop error = %v, want ContextPressureError", err)
	}
	if pressureErr.Stage != "post_compaction_guard" {
		t.Fatalf("pressure stage = %q, want post_compaction_guard", pressureErr.Stage)
	}
	if len(prov.requests) != 0 {
		t.Fatalf("provider received %d oversized requests, want 0", len(prov.requests))
	}
}

func TestEmergencyCompactionReturnsRetryCancellation(t *testing.T) {
	prov := &emergencyGuardProvider{retryErr: context.DeadlineExceeded}
	a := &Agent{
		definition: &Definition{ID: "retry-cancel", Name: "retry-cancel", Model: "test"},
		provider:   prov,
		toolReg:    tools.NewRegistry(),
		logger:     noop.NewLogger(),
		tracer:     noop.NewTracer(),
		autoCompactionConfig: AutoCompactionConfig{
			EnableAutoCompaction: true,
			Threshold: AutoCompactionThreshold{
				Mode:  AutoCompactionThresholdFixedTokens,
				Value: 90_000,
			},
			CompactFunc: func(
				context.Context,
				[]*conversation.Message,
				*compaction.CompactionContext,
			) ([]*conversation.Message, error) {
				return []*conversation.Message{{
					Role:    conversation.RoleUser,
					Content: "emergency summary",
				}}, nil
			},
		},
	}
	messages := []*conversation.Message{{
		Role:    conversation.RoleUser,
		Content: strings.Repeat("original context ", 2_000),
	}}

	_, _, err := a.executeLoop(context.Background(), messages, 2)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("executeLoop error = %v, want context deadline cause", err)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d, want initial plus one retry", len(prov.requests))
	}
}
