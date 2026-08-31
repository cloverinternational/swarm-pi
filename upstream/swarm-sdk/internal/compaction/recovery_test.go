package compaction

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type summaryCapProvider struct {
	request provider.ChatRequest
}

func (*summaryCapProvider) Name() string { return "summary-cap" }
func (*summaryCapProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{MaxContextWindow: 32_000, MaxOutputTokens: 2_048}
}
func (p *summaryCapProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.request = req
	return &provider.ChatResponse{
		Message: &conversation.Message{Role: conversation.RoleAssistant, Content: boundedTestSummary},
	}, nil
}
func (*summaryCapProvider) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

func TestSummaryBudgetCapsUnknownFallbackAt128K(t *testing.T) {
	service := NewService(CompactionConfig{
		ContextLimit:     1_000_000,
		SummaryMaxTokens: 30_000,
	})
	got := service.summaryInputBudget("summary prompt")
	maxWant := 128_000 - 30_000 - summaryRequestSafetyTokens
	if got > maxWant {
		t.Fatalf("summary input budget = %d, exceeds conservative 128K fallback budget %d", got, maxWant)
	}
}

const boundedTestSummary = `## Previous session state

The user asked for a bounded compaction implementation. The agent inspected the context pipeline, preserved complete tool call groups, and identified summary overflow as the primary reliability issue.

Key decisions: keep a stable conversation identity, retain the full transcript, send only the active generation to providers, and stop before unsafe requests. The next step is to verify the implementation with focused tests and race detection.`

func TestSummarizeBoundedChunksEveryRequestWithinBudget(t *testing.T) {
	var calls int
	service := NewService(CompactionConfig{
		ContextLimit:     50_000,
		SummaryMaxTokens: 4_000,
		SummarizeFunc: func(_ context.Context, messages []*conversation.Message, prompt string) (string, error) {
			calls++
			budget := serviceBudgetForTest(50_000, 4_000, prompt)
			if got := estimateMessagesTokens(messages); got > budget {
				t.Fatalf("request %d estimated at %d tokens, budget %d", calls, got, budget)
			}
			return boundedTestSummary, nil
		},
	})
	messages := []*conversation.Message{
		{ID: "one", Role: conversation.RoleUser, Content: strings.Repeat("a", 60_000)},
		{ID: "two", Role: conversation.RoleAssistant, Content: strings.Repeat("b", 60_000)},
		{ID: "three", Role: conversation.RoleUser, Content: strings.Repeat("c", 60_000)},
	}

	summary, method, attempts, err := service.summarizeBounded(
		context.Background(),
		messages,
		"Create the final passive summary.",
		&CompactionContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if method != "chunked" {
		t.Fatalf("method = %q, want chunked", method)
	}
	if calls < 2 || attempts != calls {
		t.Fatalf("calls=%d attempts=%d, want matching multi-call reduction", calls, attempts)
	}
	if len(summary) < 200 {
		t.Fatalf("summary too short: %d", len(summary))
	}
}

func serviceBudgetForTest(contextLimit, summaryMax int, prompt string) int {
	return contextLimit - summaryMax - EstimateTokens(prompt) - summaryRequestSafetyTokens
}

func TestChunkMessageGroupsKeepsToolCallAndResultTogether(t *testing.T) {
	assistant := &conversation.Message{
		ID:   "assistant-tool",
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{{
			ID:   "call-1",
			Name: "Read",
		}},
	}
	toolResult := &conversation.Message{
		ID:   "tool-result",
		Role: conversation.RoleTool,
		ToolResults: []conversation.ToolResult{{
			CallID: "call-1",
			Name:   "Read",
			Output: "result",
		}},
	}
	chunks := chunkMessageGroups(
		[]*conversation.Message{
			{ID: "large-user", Role: conversation.RoleUser, Content: strings.Repeat("x", 8_000)},
			assistant,
			toolResult,
		},
		1_000,
	)

	for _, chunk := range chunks {
		assistantIndex, resultIndex := -1, -1
		for i, msg := range chunk {
			if msg == assistant {
				assistantIndex = i
			}
			if msg == toolResult {
				resultIndex = i
			}
		}
		if assistantIndex >= 0 || resultIndex >= 0 {
			if assistantIndex < 0 || resultIndex != assistantIndex+1 {
				t.Fatalf("tool group split across chunk: %#v", chunk)
			}
		}
	}
}

func TestCompactionUsesDeterministicRecoveryWhenSummarizersFail(t *testing.T) {
	service := NewService(CompactionConfig{
		ContextLimit: 50_000,
		SummarizeFunc: func(context.Context, []*conversation.Message, string) (string, error) {
			return "", errors.New("all summary models unavailable")
		},
	})
	conv := &conversation.Conversation{
		CurrentContextSize: 40_000,
		Messages: []*conversation.Message{
			{ID: "user", Role: conversation.RoleUser, Content: "Please fix compaction and preserve the work."},
			{ID: "assistant", Role: conversation.RoleAssistant, Content: "I inspected the threshold and persistence paths."},
		},
	}
	compCtx := &CompactionContext{ConversationJSONPath: "/tmp/conversation.json"}

	result, err := service.CompactWithContext(context.Background(), conv, false, compCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != nil {
		t.Fatalf("deterministic recovery returned result error: %v", result.Error)
	}
	if !result.Compacted || result.RecoveryMethod != "deterministic" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Summary, "Full transcript: /tmp/conversation.json") {
		t.Fatalf("summary lacks transcript pointer: %s", result.Summary)
	}
	if len(result.RecoveredFiles) != 0 {
		t.Fatalf("deterministic recovery restored large files: %#v", result.RecoveredFiles)
	}
	active := service.BuildCompactedMessagesWithContext(result, compCtx, conv.Messages...)
	if len(active) == 0 {
		t.Fatal("deterministic recovery produced no active messages")
	}
	if strings.Contains(active[0].Content, "Continue the conversation") {
		t.Fatalf("boundary contains imperative continuation: %s", active[0].Content)
	}
	for _, msg := range active {
		if msg.Role == conversation.RoleAssistant && strings.Contains(msg.Content, "I'm continuing") {
			t.Fatalf("compacted history contains synthetic continuation acknowledgement: %s", msg.Content)
		}
		if strings.Contains(msg.Content, "Please continue the conversation") {
			t.Fatalf("compacted history contains imperative continuation: %s", msg.Content)
		}
		if strings.Contains(msg.Content, "## Restored Files") {
			t.Fatalf("deterministic recovery restored large file block: %s", msg.Content)
		}
	}
	if got := estimateMessagesTokens(active); got >= 40_000 {
		t.Fatalf("deterministic active context estimate = %d, want below 40000", got)
	}
}

func TestCompactionMicroStageDoesNotMutateArchivedTranscript(t *testing.T) {
	msg := makeMessageWithToolResults([]conversation.ToolResult{
		makeToolResult("Read", "call-1", strings.Repeat("one", 2_000)),
		makeToolResult("Read", "call-2", strings.Repeat("two", 2_000)),
		makeToolResult("Read", "call-3", strings.Repeat("three", 2_000)),
		makeToolResult("Read", "call-4", strings.Repeat("four", 2_000)),
		makeToolResult("Read", "call-5", strings.Repeat("five", 2_000)),
	})
	original := msg.ToolResults[0].Output
	service := NewService(CompactionConfig{
		ContextLimit: 50_000,
		SummarizeFunc: func(context.Context, []*conversation.Message, string) (string, error) {
			return boundedTestSummary, nil
		},
	})
	conv := &conversation.Conversation{
		CurrentContextSize: 40_000,
		Messages:           []*conversation.Message{msg},
	}

	if _, err := service.CompactWithContext(context.Background(), conv, false, &CompactionContext{}); err != nil {
		t.Fatal(err)
	}
	if msg.ToolResults[0].Output != original {
		t.Fatal("micro-compaction mutated the durable pre-commit transcript")
	}
}

func TestFitCompactedMessagesPrioritizesSummaryAndAnchors(t *testing.T) {
	summary := &conversation.Message{Role: conversation.RoleUser, Content: strings.Repeat("summary ", 100)}
	files := &conversation.Message{Role: conversation.RoleUser, Content: "## Restored Files\n" + strings.Repeat("file ", 8_000)}
	tasks := &conversation.Message{Role: conversation.RoleUser, Content: "## Current Tasks\n- verify compaction"}

	fitted, estimate, trimmed := FitCompactedMessagesToBudget(
		[]*conversation.Message{summary, files, tasks},
		1_000,
	)
	if !trimmed {
		t.Fatal("expected oversized restoration to be trimmed")
	}
	if estimate > 1_000 {
		t.Fatalf("fitted estimate = %d, budget 1000", estimate)
	}
	if len(fitted) != 2 || fitted[0].Content != summary.Content || fitted[1].Content != tasks.Content {
		t.Fatalf("fitted messages did not prioritize summary/tasks: %#v", fitted)
	}
}

func TestSummarizeWithProviderCapsOutputToCapability(t *testing.T) {
	prov := &summaryCapProvider{}
	if _, err := SummarizeWithProvider(
		context.Background(),
		prov,
		"small-model",
		[]*conversation.Message{{Role: conversation.RoleUser, Content: "work"}},
		"summarize",
		nil,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if prov.request.MaxTokens == nil || *prov.request.MaxTokens != 2_048 {
		t.Fatalf("max tokens = %v, want provider cap 2048", prov.request.MaxTokens)
	}
}

func TestFitCompactedMessagesNonASCIINeverExceedsBudget(t *testing.T) {
	summary := &conversation.Message{
		Role:    conversation.RoleUser,
		Content: strings.Repeat("🧠", 10_000),
	}
	fitted, estimate, _ := FitCompactedMessagesToBudget(
		[]*conversation.Message{summary},
		500,
	)
	if len(fitted) == 0 || estimate > 500 {
		t.Fatalf("fitted=%d estimate=%d, want non-empty <= 500", len(fitted), estimate)
	}
}

func TestFileAccessSnapshotConcurrentWithRecording(t *testing.T) {
	service := NewService(DefaultConfig(128_000))
	conv := &conversation.Conversation{}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			service.RecordFileAccess("/tmp/file.go", i%2 == 0)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			service.SnapshotToConversation(conv, &CompactionContext{})
			_ = service.GetFileAccess()
		}
	}()
	wg.Wait()
}
