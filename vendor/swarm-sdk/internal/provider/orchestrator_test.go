package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// mockProvider is a test provider that can be configured to return specific responses/errors
type mockProvider struct {
	name         string
	responses    []*ChatResponse
	errors       []error
	callCount    int32
	streamChunks []StreamChunk
}

func newMockProvider(name string) *mockProvider {
	return &mockProvider{name: name}
}

func (m *mockProvider) WithResponse(resp *ChatResponse) *mockProvider {
	m.responses = append(m.responses, resp)
	return m
}

func (m *mockProvider) WithError(err error) *mockProvider {
	m.errors = append(m.errors, err)
	return m
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Capabilities() Capabilities {
	return Capabilities{Streaming: true}
}

func (m *mockProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	idx := int(atomic.AddInt32(&m.callCount, 1)) - 1

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.responses) && m.responses[idx] != nil {
		return m.responses[idx], nil
	}
	return &ChatResponse{}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	idx := int(atomic.AddInt32(&m.callCount, 1)) - 1

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}

	ch := make(chan StreamChunk, 1)
	go func() {
		defer close(ch)
		for _, chunk := range m.streamChunks {
			select {
			case ch <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (m *mockProvider) CallCount() int {
	return int(atomic.LoadInt32(&m.callCount))
}

func TestOrchestrator_SingleProvider_Success(t *testing.T) {
	provider := newMockProvider("test").WithResponse(&ChatResponse{})

	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("test", provider, 3).
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	_, err = orchestrator.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if provider.CallCount() != 1 {
		t.Errorf("expected 1 call, got %d", provider.CallCount())
	}
}

func TestOrchestrator_SingleProvider_Retry_TransientError(t *testing.T) {
	transientErr := sdkerr.Transient("test.transient", "temporary failure",
		sdkerr.WithRetryAfter(10*time.Millisecond),
	)

	provider := newMockProvider("test").
		WithError(transientErr).      // First call fails
		WithError(transientErr).      // Second call fails
		WithResponse(&ChatResponse{}) // Third call succeeds

	var retryCount int
	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("test", provider, 3).
		WithRetryAfterFallback(10 * time.Millisecond).
		OnRetry(func(ctx context.Context, info RetryInfo) {
			retryCount++
		}).
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	_, err = orchestrator.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}

	if provider.CallCount() != 3 {
		t.Errorf("expected 3 calls (2 retries + 1 success), got %d", provider.CallCount())
	}

	if retryCount != 2 {
		t.Errorf("expected 2 retry callbacks, got %d", retryCount)
	}
}

func TestOrchestrator_SingleProvider_NoPermanentErrorRetry(t *testing.T) {
	permanentErr := sdkerr.Permanent("test.auth_failed", "invalid credentials")

	provider := newMockProvider("test").WithError(permanentErr)

	var retryCount int
	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("test", provider, 3).
		OnRetry(func(ctx context.Context, info RetryInfo) {
			retryCount++
		}).
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	_, err = orchestrator.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error for permanent failure")
	}

	if provider.CallCount() != 1 {
		t.Errorf("expected 1 call (no retry for permanent), got %d", provider.CallCount())
	}

	if retryCount != 0 {
		t.Errorf("expected 0 retry callbacks for permanent error, got %d", retryCount)
	}
}

func TestOrchestrator_MultipleProviders_Fallback(t *testing.T) {
	transientErr := sdkerr.Transient("test.transient", "provider 1 failed",
		sdkerr.WithRetryAfter(10*time.Millisecond),
	)

	provider1 := newMockProvider("provider1").
		WithError(transientErr).
		WithError(transientErr).
		WithError(transientErr).
		WithError(transientErr) // Always fails

	provider2 := newMockProvider("provider2").
		WithResponse(&ChatResponse{}) // Succeeds

	var switchCount int
	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("provider1", provider1, 2).
		AddProvider("provider2", provider2, 2).
		WithRetryAfterFallback(10 * time.Millisecond).
		OnProviderSwitch(func(ctx context.Context, from, to string, reason error) {
			switchCount++
			if from != "provider1" || to != "provider2" {
				t.Errorf("unexpected switch from %s to %s", from, to)
			}
		}).
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	_, err = orchestrator.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("expected success after fallback, got error: %v", err)
	}

	if switchCount != 1 {
		t.Errorf("expected 1 provider switch, got %d", switchCount)
	}

	if provider2.CallCount() < 1 {
		t.Errorf("expected provider2 to be called at least once")
	}
}

func TestOrchestrator_RateLimitWithRotation(t *testing.T) {
	rateLimitErr := sdkerr.Transient("gemini.rate_limited", "rate limit exceeded",
		sdkerr.WithRetryAfter(60*time.Second),
	)

	provider1 := newMockProvider("gemini").WithError(rateLimitErr)
	provider2 := newMockProvider("anthropic").WithResponse(&ChatResponse{})

	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("gemini", provider1, 3).
		AddProvider("anthropic", provider2, 3).
		WithRotateOnRateLimit(true). // Immediately rotate on rate limit
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	start := time.Now()
	_, err = orchestrator.Chat(context.Background(), ChatRequest{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success after rotation, got error: %v", err)
	}

	// Should have rotated immediately without waiting 60s
	if elapsed > 1*time.Second {
		t.Errorf("expected immediate rotation, but took %v", elapsed)
	}

	if provider1.CallCount() != 1 {
		t.Errorf("expected provider1 called once before rotation, got %d", provider1.CallCount())
	}

	if provider2.CallCount() != 1 {
		t.Errorf("expected provider2 called once, got %d", provider2.CallCount())
	}
}

func TestOrchestrator_RateLimitWithWait(t *testing.T) {
	rateLimitErr := sdkerr.Transient("gemini.rate_limited", "rate limit exceeded",
		sdkerr.WithRetryAfter(50*time.Millisecond),
	)

	provider := newMockProvider("gemini").
		WithError(rateLimitErr).
		WithResponse(&ChatResponse{})

	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("gemini", provider, 3).
		WithRotateOnRateLimit(false). // Wait for retry instead of rotating
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	start := time.Now()
	_, err = orchestrator.Chat(context.Background(), ChatRequest{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success after wait, got error: %v", err)
	}

	// Should have waited at least 50ms
	if elapsed < 50*time.Millisecond {
		t.Errorf("expected to wait at least 50ms, but only took %v", elapsed)
	}

	if provider.CallCount() != 2 {
		t.Errorf("expected 2 calls (1 fail + 1 success), got %d", provider.CallCount())
	}
}

func TestOrchestrator_AllProvidersExhausted(t *testing.T) {
	transientErr := sdkerr.Transient("test.transient", "always fails",
		sdkerr.WithRetryAfter(10*time.Millisecond),
	)

	provider1 := newMockProvider("provider1")
	provider2 := newMockProvider("provider2")

	// Both providers always fail
	for range 10 {
		provider1.WithError(transientErr)
		provider2.WithError(transientErr)
	}

	var exhaustedCalled bool
	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("provider1", provider1, 1).
		AddProvider("provider2", provider2, 1).
		WithRetryAfterFallback(10 * time.Millisecond).
		OnExhausted(func(ctx context.Context, info ExhaustedInfo) {
			exhaustedCalled = true
		}).
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	_, err = orchestrator.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers exhausted")
	}

	if !exhaustedCalled {
		t.Error("expected exhausted callback to be called")
	}
}

func TestOrchestrator_ContextCancellation(t *testing.T) {
	transientErr := sdkerr.Transient("test.transient", "always fails",
		sdkerr.WithRetryAfter(1*time.Second),
	)

	provider := newMockProvider("test").WithError(transientErr).WithError(transientErr)

	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("test", provider, 5).
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = orchestrator.Chat(ctx, ChatRequest{})
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded error, got: %v", err)
	}
}

func TestOrchestratorBuilder_Validation(t *testing.T) {
	t.Run("empty provider name", func(t *testing.T) {
		_, err := NewOrchestratorBuilder().
			AddProvider("", newMockProvider("test"), 3).
			Build()
		if err == nil {
			t.Error("expected error for empty provider name")
		}
	})

	t.Run("nil provider", func(t *testing.T) {
		_, err := NewOrchestratorBuilder().
			AddProvider("test", nil, 3).
			Build()
		if err == nil {
			t.Error("expected error for nil provider")
		}
	})

	t.Run("no providers", func(t *testing.T) {
		_, err := NewOrchestratorBuilder().Build()
		if err == nil {
			t.Error("expected error when no providers")
		}
	})

	t.Run("duplicate names", func(t *testing.T) {
		_, err := NewOrchestratorBuilder().
			AddProvider("test", newMockProvider("test1"), 3).
			AddProvider("test", newMockProvider("test2"), 3).
			Build()
		if err == nil {
			t.Error("expected error for duplicate provider names")
		}
	})

	t.Run("negative max retries", func(t *testing.T) {
		_, err := NewOrchestratorBuilder().
			AddProvider("test", newMockProvider("test"), -1).
			Build()
		if err == nil {
			t.Error("expected error for negative max retries")
		}
	})
}

func TestOrchestrator_GetCurrentProviderName(t *testing.T) {
	provider1 := newMockProvider("provider1")
	provider2 := newMockProvider("provider2")

	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("provider1", provider1, 3).
		AddProvider("provider2", provider2, 3).
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	if orchestrator.CurrentProviderName() != "provider1" {
		t.Errorf("expected current provider to be 'provider1', got '%s'", orchestrator.CurrentProviderName())
	}
}

func TestOrchestrator_ProviderCount(t *testing.T) {
	provider1 := newMockProvider("provider1")
	provider2 := newMockProvider("provider2")

	orchestrator, err := NewOrchestratorBuilder().
		AddProvider("provider1", provider1, 3).
		AddProvider("provider2", provider2, 3).
		Build()
	if err != nil {
		t.Fatalf("failed to build orchestrator: %v", err)
	}

	if orchestrator.ProviderCount() != 2 {
		t.Errorf("expected 2 providers, got %d", orchestrator.ProviderCount())
	}
}
