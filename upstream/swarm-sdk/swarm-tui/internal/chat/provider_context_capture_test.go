package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type contextCaptureTestProvider struct {
	name     string
	chatFn   func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error)
	streamFn func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error)
}

func (p *contextCaptureTestProvider) Name() string { return p.name }
func (p *contextCaptureTestProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Streaming: true}
}
func (p *contextCaptureTestProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	if p.chatFn != nil {
		return p.chatFn(ctx, req)
	}
	return &provider.ChatResponse{}, nil
}
func (p *contextCaptureTestProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if p.streamFn != nil {
		return p.streamFn(ctx, req)
	}
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func contextCaptureRequest(runID, model, content string) provider.ChatRequest {
	return provider.ChatRequest{
		Model: model,
		Metadata: map[string]any{
			"context_run_id": runID,
		},
		Messages: []*conversation.Message{{
			Role:    conversation.RoleUser,
			Content: content,
		}},
	}
}

func TestProviderContextCaptureChatPairsSameCallUsage(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	base := &contextCaptureTestProvider{
		name: "fake",
		chatFn: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
			if req.Messages[0].Content != "paired payload" {
				t.Fatalf("unexpected request: %+v", req)
			}
			return &provider.ChatResponse{Usage: &conversation.TokenUsage{
				Input: 20, CacheCreation: 3, CacheRead: 7, Output: 5, Total: 35,
			}}, nil
		},
	}
	wrapped := newContextInjectingProviderWithCapture(base, nil, store)

	if _, err := wrapped.Chat(context.Background(), contextCaptureRequest("run-chat", "m1", "paired payload")); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	calls := store.take("run-chat")
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.ID != "run-chat/call-1" || call.Path != "chat" || call.Provider != "fake" || call.Model != "m1" {
		t.Fatalf("identity mismatch: %+v", call)
	}
	if call.Usage == nil || call.Usage.InputTokens != 30 || call.Usage.OutputTokens != 5 {
		t.Fatalf("usage mismatch: %+v", call.Usage)
	}
	if call.Report.Format != "canonical-final" || len(call.Report.Messages.Components) != 1 {
		t.Fatalf("report mismatch: %+v", call.Report)
	}
	if again := store.take("run-chat"); len(again) != 0 {
		t.Fatalf("take did not drain run: %+v", again)
	}
}

func TestProviderContextCaptureStreamKeepsLatestUsage(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	base := &contextCaptureTestProvider{
		name: "streamer",
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			ch := make(chan provider.StreamChunk, 2)
			ch <- provider.StreamChunk{Usage: &conversation.TokenUsage{Input: 10, Output: 1}}
			ch <- provider.StreamChunk{Done: true, Usage: &conversation.TokenUsage{Input: 25, Output: 4, Total: 29}}
			close(ch)
			return ch, nil
		},
	}
	wrapped := newContextInjectingProviderWithCapture(base, nil, store)
	ch, err := wrapped.Stream(context.Background(), contextCaptureRequest("run-stream", "m2", "stream"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}

	calls := store.take("run-stream")
	if len(calls) != 1 || calls[0].Outcome != contextCallOutcomeDone {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Usage == nil || calls[0].Usage.InputTokens != 25 || calls[0].Usage.OutputTokens != 4 {
		t.Fatalf("latest usage not retained: %+v", calls[0].Usage)
	}
}

func TestProviderContextCaptureStreamPreservesTailAfterDone(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	base := &contextCaptureTestProvider{
		name: "streamer",
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			ch := make(chan provider.StreamChunk, 2)
			ch <- provider.StreamChunk{Done: true, Delta: "done", Usage: &conversation.TokenUsage{Input: 10}}
			ch <- provider.StreamChunk{Delta: "tail"}
			close(ch)
			return ch, nil
		},
	}
	wrapped := newContextInjectingProviderWithCapture(base, nil, store)
	ch, err := wrapped.Stream(context.Background(), contextCaptureRequest("run-tail", "m", "stream"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var deltas []string
	for chunk := range ch {
		deltas = append(deltas, chunk.Delta)
	}
	if got := strings.Join(deltas, ","); got != "done,tail" {
		t.Fatalf("forwarded deltas = %q, want done,tail", got)
	}
	if calls := store.take("run-tail"); len(calls) != 1 || calls[0].Outcome != contextCallOutcomeDone {
		t.Fatalf("capture = %+v", calls)
	}
}

func TestProviderContextCaptureStreamUsesTailUsageAndError(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	base := &contextCaptureTestProvider{
		name: "streamer",
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			ch := make(chan provider.StreamChunk, 2)
			ch <- provider.StreamChunk{Done: true, Usage: &conversation.TokenUsage{Input: 10}}
			ch <- provider.StreamChunk{
				Error: errors.New("late stream error"),
				Usage: &conversation.TokenUsage{Input: 20, Output: 2, Total: 22},
			}
			close(ch)
			return ch, nil
		},
	}
	wrapped := newContextInjectingProviderWithCapture(base, nil, store)
	ch, err := wrapped.Stream(context.Background(), contextCaptureRequest("run-tail-error", "m", "stream"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var chunks int
	for range ch {
		chunks++
	}
	if chunks != 2 {
		t.Fatalf("forwarded chunks = %d, want 2", chunks)
	}
	calls := store.take("run-tail-error")
	if len(calls) != 1 || calls[0].Outcome != contextCallOutcomeError || calls[0].Error == "" {
		t.Fatalf("capture outcome = %+v", calls)
	}
	if calls[0].Usage == nil || calls[0].Usage.InputTokens != 20 || calls[0].Usage.OutputTokens != 2 {
		t.Fatalf("tail usage was not retained: %+v", calls[0].Usage)
	}
}

func TestProviderContextCaptureStreamPreservesNilSource(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	base := &contextCaptureTestProvider{
		name: "nil-stream",
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			return nil, nil
		},
	}
	wrapped := newContextInjectingProviderWithCapture(base, nil, store)
	ch, err := wrapped.Stream(context.Background(), contextCaptureRequest("run-nil", "m", "stream"))
	if err != nil || ch != nil {
		t.Fatalf("Stream = (%v, %v), want (nil, nil)", ch, err)
	}
	if calls := store.take("run-nil"); len(calls) != 1 || calls[0].Outcome != contextCallOutcomeClosed {
		t.Fatalf("capture = %+v", calls)
	}
}

func TestProviderContextCaptureFinalizesErrorBeforeForwarding(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	source := make(chan provider.StreamChunk, 1)
	source <- provider.StreamChunk{Error: errors.New("stream failed")}
	close(source)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := &contextCaptureTestProvider{
		name: "error-stream",
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			return source, nil
		},
	}
	wrapped := newContextInjectingProviderWithCapture(base, nil, store)
	ch, err := wrapped.Stream(ctx, contextCaptureRequest("run-stream-error", "m", "stream"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	chunk := <-ch
	if chunk.Error == nil {
		t.Fatal("expected forwarded error chunk")
	}
	calls := store.take("run-stream-error")
	if len(calls) != 1 || calls[0].Outcome != contextCallOutcomeError {
		t.Fatalf("error was not finalized before forwarding: %+v", calls)
	}
}

func TestProviderContextCaptureRecordsErrorsAndClosedStreams(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	base := &contextCaptureTestProvider{
		name: "failer",
		chatFn: func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
			return nil, errors.New("chat failed")
		},
		streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			ch := make(chan provider.StreamChunk)
			close(ch)
			return ch, nil
		},
	}
	wrapped := newContextInjectingProviderWithCapture(base, nil, store)
	_, _ = wrapped.Chat(context.Background(), contextCaptureRequest("run-errors", "m", "chat"))
	ch, err := wrapped.Stream(context.Background(), contextCaptureRequest("run-errors", "m", "stream"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}

	calls := store.take("run-errors")
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2: %+v", len(calls), calls)
	}
	if calls[0].Outcome != contextCallOutcomeError || calls[0].Error == "" || calls[0].Usage != nil {
		t.Fatalf("chat error capture = %+v", calls[0])
	}
	if calls[1].Outcome != contextCallOutcomeClosed || calls[1].Usage != nil {
		t.Fatalf("closed stream capture = %+v", calls[1])
	}
}

func TestProviderContextCaptureIgnoresMissingRunID(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	candidate := store.begin(provider.ChatRequest{Model: "m"}, "fake", "chat")
	if candidate != nil {
		t.Fatalf("candidate = %+v, want nil", candidate)
	}
	if calls := store.take(""); len(calls) != 0 {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestProviderContextCaptureConcurrentRunsStayIsolated(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	const runs = 20
	var wg sync.WaitGroup
	for i := 0; i < runs; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			runID := fmt.Sprintf("run-%d", i)
			for call := 0; call < 3; call++ {
				candidate := store.begin(contextCaptureRequest(runID, "m", "payload"), "fake", "chat")
				candidate.finish(&conversation.TokenUsage{Input: i + call + 1}, contextCallOutcomeDone, nil)
			}
		}()
	}
	wg.Wait()

	for i := 0; i < runs; i++ {
		runID := fmt.Sprintf("run-%d", i)
		calls := store.take(runID)
		if len(calls) != 3 {
			t.Fatalf("%s calls = %d, want 3", runID, len(calls))
		}
		for j, call := range calls {
			if call.RunID != runID || call.Ordinal != j+1 {
				t.Fatalf("%s call %d = %+v", runID, j, call)
			}
		}
	}
}

func TestSDKIntegrationFlushesOnlyRequestedContextRun(t *testing.T) {
	t.Parallel()
	store := newProviderContextCaptureStore()
	for _, runID := range []string{"run-a", "run-b"} {
		candidate := store.begin(contextCaptureRequest(runID, "m", "payload"), "fake", "chat")
		candidate.finish(&conversation.TokenUsage{Input: 10}, contextCallOutcomeDone, nil)
	}
	screen := NewDebugScreen()
	sdk := &SDKIntegration{contextCapture: store, debugScreen: screen}

	sdk.flushProviderContextCaptures("run-a")
	if len(screen.contextCalls) != 1 || screen.contextCalls[0].RunID != "run-a" {
		t.Fatalf("flushed calls = %+v, want only run-a", screen.contextCalls)
	}
	if remaining := store.take("run-b"); len(remaining) != 1 || remaining[0].RunID != "run-b" {
		t.Fatalf("run-b was lost or cross-flushed: %+v", remaining)
	}
}
