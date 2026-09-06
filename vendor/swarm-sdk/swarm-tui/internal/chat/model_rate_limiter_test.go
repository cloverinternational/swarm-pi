package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	chatsettings "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

type fakeLimiterClock struct {
	now time.Time
}

func (c *fakeLimiterClock) Now() time.Time {
	return c.now
}

func (c *fakeLimiterClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestModelRateLimiterBlocksWhenLimitExceeded(t *testing.T) {
	clock := &fakeLimiterClock{now: time.Unix(1700000000, 0)}
	limiter := newModelRateLimiter([]commands.ModelRateLimitConfig{
		{
			Provider:          "OpenAI",
			Model:             "gpt-5.1",
			RequestsPerMinute: 2,
			Enabled:           true,
		},
	}, clock.Now)

	if err := limiter.Allow("OpenAI", "gpt-5.1"); err != nil {
		t.Fatalf("first request unexpectedly blocked: %v", err)
	}
	if err := limiter.Allow("OpenAI", "gpt-5.1"); err != nil {
		t.Fatalf("second request unexpectedly blocked: %v", err)
	}
	err := limiter.Allow("OpenAI", "gpt-5.1")
	if err == nil {
		t.Fatalf("third request should be blocked by limiter")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected rate limit error, got: %v", err)
	}
}

func TestModelRateLimiterCountersAreIndependentPerKey(t *testing.T) {
	clock := &fakeLimiterClock{now: time.Unix(1700000000, 0)}
	limiter := newModelRateLimiter([]commands.ModelRateLimitConfig{
		{Provider: "OpenAI", Model: "gpt-5.1", RequestsPerMinute: 1, Enabled: true},
		{Provider: "Anthropic", Model: "claude-sonnet-4-20250514", RequestsPerMinute: 1, Enabled: true},
	}, clock.Now)

	if err := limiter.Allow("OpenAI", "gpt-5.1"); err != nil {
		t.Fatalf("openai first request blocked: %v", err)
	}
	if err := limiter.Allow("Anthropic", "claude-sonnet-4-20250514"); err != nil {
		t.Fatalf("anthropic first request blocked: %v", err)
	}

	if err := limiter.Allow("OpenAI", "gpt-5.1"); err == nil {
		t.Fatalf("openai second request should be blocked")
	}
	if err := limiter.Allow("Anthropic", "claude-sonnet-4-20250514"); err == nil {
		t.Fatalf("anthropic second request should be blocked")
	}
}

func TestModelRateLimiterAllowsAfterWindowReset(t *testing.T) {
	clock := &fakeLimiterClock{now: time.Unix(1700000000, 0)}
	limiter := newModelRateLimiter([]commands.ModelRateLimitConfig{
		{Provider: "OpenAI", Model: "gpt-5.1", RequestsPerMinute: 1, Enabled: true},
	}, clock.Now)

	if err := limiter.Allow("OpenAI", "gpt-5.1"); err != nil {
		t.Fatalf("first request blocked: %v", err)
	}
	if err := limiter.Allow("OpenAI", "gpt-5.1"); err == nil {
		t.Fatalf("second request should be blocked in same window")
	}

	clock.Advance(time.Minute + time.Second)
	if err := limiter.Allow("OpenAI", "gpt-5.1"); err != nil {
		t.Fatalf("request after window reset should be allowed: %v", err)
	}
}

func TestModelRateLimiterNoPolicyAndDisabledPolicyAreNoOp(t *testing.T) {
	clock := &fakeLimiterClock{now: time.Unix(1700000000, 0)}
	noPolicyLimiter := newModelRateLimiter(nil, clock.Now)
	for range 10 {
		if err := noPolicyLimiter.Allow("Gemini", "gemini-2.5-pro"); err != nil {
			t.Fatalf("no-policy limiter should never block, got: %v", err)
		}
	}

	disabledPolicyLimiter := newModelRateLimiter([]commands.ModelRateLimitConfig{
		{
			Provider:          "OpenAI",
			Model:             "gpt-5.1",
			RequestsPerMinute: 0,
		},
	}, clock.Now)
	for range 10 {
		if err := disabledPolicyLimiter.Allow("OpenAI", "gpt-5.1"); err != nil {
			t.Fatalf("disabled/invalid policy should not block, got: %v", err)
		}
	}
}

func TestBuildRuntimeProviderStackAppliesRateLimiting(t *testing.T) {
	clock := &fakeLimiterClock{now: time.Unix(1700000000, 0)}
	primary := &testChatProvider{
		name: "openai",
		chatFn: func(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
			return &provider.ChatResponse{}, nil
		},
	}

	stack, err := buildRuntimeProviderStack(
		primary,
		"OpenAI",
		"gpt-5.1",
		providerBuildDeps{},
		func() (*commands.SwarmOSConfig, error) {
			return &commands.SwarmOSConfig{
				RetrySettings: &commands.RetrySettingsConfig{
					Enabled:               false,
					MaxRetriesPerProvider: 3,
					RetryAfterFallbackMs:  1000,
				},
				ModelRateLimits: []commands.ModelRateLimitConfig{
					{
						Provider:          "OpenAI",
						Model:             "gpt-5.1",
						RequestsPerMinute: 1,
						Enabled:           true,
					},
				},
			}, nil
		},
		nil,
		nil,
		func() time.Time { return clock.Now() },
	)
	if err != nil {
		t.Fatalf("buildRuntimeProviderStack failed: %v", err)
	}

	if _, err := stack.Chat(context.Background(), provider.ChatRequest{Model: "ignored"}); err != nil {
		t.Fatalf("first request should pass: %v", err)
	}
	if _, err := stack.Chat(context.Background(), provider.ChatRequest{Model: "ignored"}); err == nil {
		t.Fatalf("second request should be blocked by rate limiter")
	}
}

func persistLocalRuntimeReliabilityBaseline(t *testing.T) *commands.ConfigManager {
	t.Helper()

	commands.InvalidateConfigCache()
	t.Cleanup(commands.InvalidateConfigCache)

	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager failed: %v", err)
	}

	_, err = cm.UpdateConfig(func(cfg *commands.SwarmOSConfig) {
		cfg.CurrentProvider = "OpenAI"
		cfg.CurrentModel = "gpt-5.1"
		cfg.SetChatFallbackChain(fallback.NewChain("OpenAI", "gpt-5.1"))
		cfg.SetRetrySettings(commands.RetrySettingsConfig{
			Enabled:               false,
			MaxRetriesPerProvider: 1,
			RotateOnRateLimit:     false,
			RetryAfterFallbackMs:  1000,
		})
	})
	if err != nil {
		t.Fatalf("failed to persist baseline reliability config: %v", err)
	}

	return cm
}

func TestRuntimeProviderStackUsesPersistedRateLimitSettings(t *testing.T) {
	// Phase-05 behavior spec: rate limits saved through settings must be enforced in runtime.
	t.Setenv("HOME", t.TempDir())
	cm := persistLocalRuntimeReliabilityBaseline(t)

	reliability := chatsettings.NewReliabilitySettings(cm)
	if err := reliability.AddRateLimit(commands.ModelRateLimitConfig{
		Provider:          "OpenAI",
		Model:             "gpt-5.1",
		RequestsPerMinute: 1,
		Enabled:           true,
	}); err != nil {
		t.Fatalf("AddRateLimit failed: %v", err)
	}
	if err := reliability.Save(); err != nil {
		t.Fatalf("Save reliability settings failed: %v", err)
	}

	clock := &fakeLimiterClock{now: time.Unix(1700001000, 0)}
	primary := &testChatProvider{
		name: "openai",
		chatFn: func(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
			return &provider.ChatResponse{}, nil
		},
	}

	stack, err := buildRuntimeProviderStack(
		primary,
		"OpenAI",
		"gpt-5.1",
		providerBuildDeps{},
		loadSwarmOSConfig,
		nil,
		nil,
		func() time.Time { return clock.Now() },
	)
	if err != nil {
		t.Fatalf("buildRuntimeProviderStack failed: %v", err)
	}

	if _, err := stack.Chat(context.Background(), provider.ChatRequest{Model: "ignored"}); err != nil {
		t.Fatalf("first request should pass: %v", err)
	}
	if _, err := stack.Chat(context.Background(), provider.ChatRequest{Model: "ignored"}); err == nil {
		t.Fatalf("second request should be blocked by persisted rate limiter")
	}
}

func TestRuntimeProviderStackSkipsDisabledPersistedRateLimitSettings(t *testing.T) {
	// Phase-05 behavior spec: disabled rate limits must persist through reload and not be enforced.
	t.Setenv("HOME", t.TempDir())
	cm := persistLocalRuntimeReliabilityBaseline(t)

	reliability := chatsettings.NewReliabilitySettings(cm)
	limit := commands.ModelRateLimitConfig{
		Provider:          "OpenAI",
		Model:             "gpt-5.1",
		RequestsPerMinute: 1,
		Enabled:           true,
	}
	if err := reliability.AddRateLimit(limit); err != nil {
		t.Fatalf("AddRateLimit failed: %v", err)
	}

	limit.Enabled = false
	if err := reliability.UpdateRateLimit(0, limit); err != nil {
		t.Fatalf("UpdateRateLimit failed: %v", err)
	}
	if err := reliability.Save(); err != nil {
		t.Fatalf("Save reliability settings failed: %v", err)
	}

	reloaded := chatsettings.NewReliabilitySettings(cm)
	limits := reloaded.GetModelRateLimits()
	if len(limits) != 1 || limits[0].Enabled {
		t.Fatalf("expected one disabled rate limit after reload, got %+v", limits)
	}

	clock := &fakeLimiterClock{now: time.Unix(1700002000, 0)}
	primary := &testChatProvider{
		name: "openai",
		chatFn: func(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
			return &provider.ChatResponse{}, nil
		},
	}

	stack, err := buildRuntimeProviderStack(
		primary,
		"OpenAI",
		"gpt-5.1",
		providerBuildDeps{},
		loadSwarmOSConfig,
		nil,
		nil,
		func() time.Time { return clock.Now() },
	)
	if err != nil {
		t.Fatalf("buildRuntimeProviderStack failed: %v", err)
	}

	if _, err := stack.Chat(context.Background(), provider.ChatRequest{Model: "ignored"}); err != nil {
		t.Fatalf("first request should pass with disabled limit: %v", err)
	}
	if _, err := stack.Chat(context.Background(), provider.ChatRequest{Model: "ignored"}); err != nil {
		t.Fatalf("disabled persisted rate limit should not block second request: %v", err)
	}
}
