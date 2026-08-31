package chat

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/codex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// stubStreamProvider fails with err until failuresLeft hits zero, then streams
// one chunk successfully.
type stubStreamProvider struct {
	name         string
	failuresLeft *atomic.Int32
	err          error
}

func (s *stubStreamProvider) Name() string                        { return s.name }
func (s *stubStreamProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (s *stubStreamProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, errors.New("not used")
}
func (s *stubStreamProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if s.failuresLeft.Add(-1) >= 0 {
		return nil, s.err
	}
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Delta: "ok", Done: true}
	close(ch)
	return ch, nil
}

func rateLimitedErr() error {
	return sdkerr.Transient("codex.rate_limited", "usage_limit_reached", sdkerr.WithRetryAfter(time.Second))
}

func newTestRotator(t *testing.T, inner provider.Provider, hooks accountRotationHooks, rebuilt provider.Provider) (*accountRotatingProvider, *[]providerEventMsg) {
	t.Helper()
	events := &[]providerEventMsg{}
	p := &accountRotatingProvider{
		providerName: "codex",
		rebuild: func() (provider.Provider, error) {
			if rebuilt == nil {
				return inner, nil
			}
			return rebuilt, nil
		},
		notify: func(m providerEventMsg) { *events = append(*events, m) },
		hooks:  hooks,
		inner:  inner,
	}
	return p, events
}

// TestRotationOnRateLimit: a typed rate-limit error with 2 stacked accounts
// rotates, rebuilds the inner provider, retries, and emits an account_rotate
// event.
func TestRotationOnRateLimit(t *testing.T) {
	var failures atomic.Int32
	failures.Store(1)
	failing := &stubStreamProvider{name: "codex", failuresLeft: &failures, err: rateLimitedErr()}

	rotated := 0
	hooks := accountRotationHooks{
		rotate: func(string) (string, string, int, error) {
			rotated++
			return "a@x.com", "b@x.com", 2, nil
		},
		stackedAccounts: func(string) int { return 2 },
		loadSnapshots:   func() (map[string]codex.RateLimitSnapshot, error) { return nil, nil },
		now:             time.Now,
	}
	p, events := newTestRotator(t, failing, hooks, failing)

	ch, err := p.Stream(context.Background(), provider.ChatRequest{Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("Stream after rotation: %v", err)
	}
	for range ch {
	}
	if rotated != 1 {
		t.Errorf("rotated %d times, want 1", rotated)
	}
	found := false
	for _, e := range *events {
		if e.Type == "account_rotate" && e.FromProvider == "a@x.com" && e.ToProvider == "b@x.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("no account_rotate event; events = %+v", *events)
	}
}

// TestNoRotationForNonRateLimit: ordinary errors pass through untouched.
func TestNoRotationForNonRateLimit(t *testing.T) {
	var failures atomic.Int32
	failures.Store(10)
	failing := &stubStreamProvider{name: "codex", failuresLeft: &failures, err: errors.New("boom")}
	rotated := 0
	hooks := accountRotationHooks{
		rotate:          func(string) (string, string, int, error) { rotated++; return "", "", 2, nil },
		stackedAccounts: func(string) int { return 2 },
		loadSnapshots:   func() (map[string]codex.RateLimitSnapshot, error) { return nil, nil },
		now:             time.Now,
	}
	p, _ := newTestRotator(t, failing, hooks, nil)
	if _, err := p.Stream(context.Background(), provider.ChatRequest{}); err == nil {
		t.Fatal("expected error")
	}
	if rotated != 0 {
		t.Errorf("rotated on non-rate-limit error")
	}
}

// TestSingleAccountEmitsStackHint: with one account, no rotation — but the
// user gets told stacking exists.
func TestSingleAccountEmitsStackHint(t *testing.T) {
	var failures atomic.Int32
	failures.Store(10)
	failing := &stubStreamProvider{name: "codex", failuresLeft: &failures, err: rateLimitedErr()}
	hooks := accountRotationHooks{
		rotate:          func(string) (string, string, int, error) { t.Fatal("must not rotate"); return "", "", 0, nil },
		stackedAccounts: func(string) int { return 1 },
		loadSnapshots:   func() (map[string]codex.RateLimitSnapshot, error) { return nil, nil },
		now:             time.Now,
	}
	p, events := newTestRotator(t, failing, hooks, nil)
	if _, err := p.Stream(context.Background(), provider.ChatRequest{}); err == nil {
		t.Fatal("expected error to propagate")
	}
	found := false
	for _, e := range *events {
		if e.Type == "usage_warning" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected stack-hint usage_warning; events = %+v", *events)
	}
}

// TestRotationThrottled: two rate limits inside the throttle window rotate
// only once.
func TestRotationThrottled(t *testing.T) {
	var failures atomic.Int32
	failures.Store(10) // always failing
	failing := &stubStreamProvider{name: "codex", failuresLeft: &failures, err: rateLimitedErr()}
	rotated := 0
	base := time.Now()
	hooks := accountRotationHooks{
		rotate:          func(string) (string, string, int, error) { rotated++; return "a", "b", 2, nil },
		stackedAccounts: func(string) int { return 2 },
		loadSnapshots:   func() (map[string]codex.RateLimitSnapshot, error) { return nil, nil },
		now:             func() time.Time { return base },
	}
	p, _ := newTestRotator(t, failing, hooks, failing)
	_, _ = p.Stream(context.Background(), provider.ChatRequest{})
	_, _ = p.Stream(context.Background(), provider.ChatRequest{})
	if rotated != 1 {
		t.Errorf("rotated %d times within throttle window, want 1", rotated)
	}
}

// TestUsageThresholdWarnings: crossing 75/90 fires once per window per reset
// period, and re-fires for a new reset period.
func TestUsageThresholdWarnings(t *testing.T) {
	usageWarningsFired.Range(func(k, _ any) bool { usageWarningsFired.Delete(k); return true })

	snap := codex.RateLimitSnapshot{
		CapturedAt: time.Now(),
		AccountID:  "acct_1",
		Primary:    &codex.RateLimitWindow{UsedPercent: 92, WindowMinutes: 10080, ResetAt: 1000},
		Secondary:  &codex.RateLimitWindow{UsedPercent: 10, WindowMinutes: 300},
	}
	hooks := accountRotationHooks{
		rotate:          func(string) (string, string, int, error) { return "", "", 2, nil },
		stackedAccounts: func(string) int { return 1 },
		loadSnapshots: func() (map[string]codex.RateLimitSnapshot, error) {
			return map[string]codex.RateLimitSnapshot{"acct_1": snap}, nil
		},
		now: time.Now,
	}
	p, events := newTestRotator(t, &stubStreamProvider{name: "codex", failuresLeft: &atomic.Int32{}}, hooks, nil)

	p.checkUsageThresholds()
	warnings := func() int {
		n := 0
		for _, e := range *events {
			if e.Type == "usage_warning" {
				n++
			}
		}
		return n
	}
	// 92% crosses both 75 and 90 for the weekly window → two warnings.
	if got := warnings(); got != 2 {
		t.Fatalf("warnings = %d, want 2 (75%% and 90%% thresholds); events=%+v", got, *events)
	}
	// Same snapshot again: deduped.
	p.checkUsageThresholds()
	if got := warnings(); got != 2 {
		t.Errorf("warnings after repeat = %d, want still 2", got)
	}
	// New reset period (window reset) → warns again.
	snap.Primary.ResetAt = 2000
	p.checkUsageThresholds()
	if got := warnings(); got != 4 {
		t.Errorf("warnings after new reset period = %d, want 4", got)
	}
	// Single account at ≥90%: the hint to stack another account is present.
	hintFound := false
	for _, e := range *events {
		if e.Type == "usage_warning" && strings.Contains(e.Error, "/auth") {
			hintFound = true
		}
	}
	if !hintFound {
		t.Error("expected stack-another-account hint in a ≥90% warning")
	}
}
