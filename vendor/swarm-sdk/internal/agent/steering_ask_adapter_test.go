package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSteeringAskAdapter_CallsInner(t *testing.T) {
	var gotQ, gotU string
	var gotTimeout time.Duration
	a := NewSteeringAskAdapter(func(ctx context.Context, q, u string, ttl time.Duration) (string, error) {
		gotQ = q
		gotU = u
		gotTimeout = ttl
		return "yes", nil
	})

	answer, err := a.Ask(context.Background(), "Continue?", "high", 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer != "yes" {
		t.Errorf("expected answer='yes', got %q", answer)
	}
	if gotQ != "Continue?" {
		t.Errorf("expected question='Continue?', got %q", gotQ)
	}
	if gotU != "high" {
		t.Errorf("expected urgency='high', got %q", gotU)
	}
	if gotTimeout != 30*time.Second {
		t.Errorf("expected timeout=30s, got %v", gotTimeout)
	}
}

func TestSteeringAskAdapter_NilFn(t *testing.T) {
	a := NewSteeringAskAdapter(nil)
	answer, err := a.Ask(context.Background(), "q", "low", 5*time.Second)
	if err != nil {
		t.Errorf("nil fn should not error, got %v", err)
	}
	if answer != "" {
		t.Errorf("nil fn should return empty answer, got %q", answer)
	}
}

func TestSteeringAskAdapter_NilReceiver(t *testing.T) {
	var a *SteeringAskAdapter
	answer, err := a.Ask(context.Background(), "q", "low", 5*time.Second)
	if err != nil {
		t.Errorf("nil receiver should not error, got %v", err)
	}
	if answer != "" {
		t.Errorf("nil receiver should return empty answer, got %q", answer)
	}
}

func TestSteeringAskAdapter_PropagatesError(t *testing.T) {
	wantErr := errors.New("user-cancelled")
	a := NewSteeringAskAdapter(func(ctx context.Context, q, u string, ttl time.Duration) (string, error) {
		return "", wantErr
	})

	_, err := a.Ask(context.Background(), "q", "low", 5*time.Second)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected wantErr, got %v", err)
	}
}

func TestSteeringAskAdapter_SatisfiesInterface(t *testing.T) {
	var p AskUserPusher = NewSteeringAskAdapter(func(ctx context.Context, q, u string, ttl time.Duration) (string, error) {
		return "ok", nil
	})
	answer, err := p.Ask(context.Background(), "q", "low", 5*time.Second)
	if err != nil || answer != "ok" {
		t.Errorf("interface dispatch broken: answer=%q err=%v", answer, err)
	}
}

func TestSteeringAskAdapter_Concurrent(t *testing.T) {
	var mu sync.Mutex
	var calls int
	a := NewSteeringAskAdapter(func(ctx context.Context, q, u string, ttl time.Duration) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return "ack", nil
	})

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			_, _ = a.Ask(context.Background(), "q", "low", 5*time.Second)
		})
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 16 {
		t.Fatalf("expected 16 calls, got %d", calls)
	}
}

func TestAskUserPusherFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := AskUserPusherFromContext(ctx); ok {
		t.Fatal("empty ctx should not carry a pusher")
	}

	p := NewSteeringAskAdapter(func(ctx context.Context, q, u string, ttl time.Duration) (string, error) {
		return "answer", nil
	})

	ctx2 := WithAskUserPusher(ctx, p)
	got, ok := AskUserPusherFromContext(ctx2)
	if !ok {
		t.Fatal("expected pusher on ctx2")
	}
	// Round-trip the call.
	answer, err := got.Ask(ctx2, "q", "low", 5*time.Second)
	if err != nil || answer != "answer" {
		t.Errorf("round-trip broken: answer=%q err=%v", answer, err)
	}

	// Original ctx untouched.
	if _, ok := AskUserPusherFromContext(ctx); ok {
		t.Fatal("WithAskUserPusher mutated parent ctx")
	}
}
