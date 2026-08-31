package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerror "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

const rateLimiterWindow = time.Minute

type modelRateLimitPolicy struct {
	Limit int
}

type modelRateLimitState struct {
	WindowStart time.Time
	Count       int
}

// modelRateLimiter enforces per provider/model request limits.
type modelRateLimiter struct {
	mu       sync.Mutex
	policies map[string]modelRateLimitPolicy
	states   map[string]modelRateLimitState
	now      func() time.Time
}

func newModelRateLimiter(config []commands.ModelRateLimitConfig, now func() time.Time) *modelRateLimiter {
	if now == nil {
		now = time.Now
	}
	policies := make(map[string]modelRateLimitPolicy)
	for _, entry := range config {
		providerName := strings.TrimSpace(entry.Provider)
		modelName := strings.TrimSpace(entry.Model)
		if providerName == "" || modelName == "" {
			continue
		}
		if !entry.Enabled {
			continue
		}
		if entry.RequestsPerMinute <= 0 {
			continue
		}
		limit := entry.RequestsPerMinute
		if entry.Burst > 0 {
			limit += entry.Burst
		}
		if limit <= 0 {
			continue
		}
		policies[commands.ModelRefKey(providerName, modelName)] = modelRateLimitPolicy{Limit: limit}
	}

	if len(policies) == 0 {
		return nil
	}
	return &modelRateLimiter{
		policies: policies,
		states:   make(map[string]modelRateLimitState),
		now:      now,
	}
}

func (l *modelRateLimiter) Allow(providerName, modelName string) error {
	if l == nil {
		return nil
	}

	key := commands.ModelRefKey(providerName, modelName)
	policy, ok := l.policies[key]
	if !ok || policy.Limit <= 0 {
		return nil
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	state := l.states[key]
	if state.WindowStart.IsZero() || now.Sub(state.WindowStart) >= rateLimiterWindow {
		state = modelRateLimitState{WindowStart: now, Count: 0}
	}

	if state.Count >= policy.Limit {
		retryAfter := rateLimiterWindow - now.Sub(state.WindowStart)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return sdkerror.Transient(
			"swarmos.local.rate_limited",
			fmt.Sprintf("rate limit exceeded for %s/%s", providerName, modelName),
			sdkerror.WithRetryAfter(retryAfter),
			sdkerror.WithAttr("provider", providerName),
			sdkerror.WithAttr("model", modelName),
			sdkerror.WithAttr("limit", policy.Limit),
		)
	}

	state.Count++
	l.states[key] = state
	return nil
}

// rateLimitedProvider enforces per-model limits before delegating to the wrapped provider.
type rateLimitedProvider struct {
	base         provider.Provider
	limiter      *modelRateLimiter
	providerName string
	model        string
}

func newRateLimitedProvider(base provider.Provider, limiter *modelRateLimiter, providerName, model string) provider.Provider {
	if base == nil || limiter == nil {
		return base
	}
	return &rateLimitedProvider{
		base:         base,
		limiter:      limiter,
		providerName: strings.TrimSpace(providerName),
		model:        strings.TrimSpace(model),
	}
}

func (p *rateLimitedProvider) Name() string {
	return p.base.Name()
}

func (p *rateLimitedProvider) Capabilities() provider.Capabilities {
	return p.base.Capabilities()
}

func (p *rateLimitedProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	if err := p.allow(req.Model); err != nil {
		return nil, err
	}
	return p.base.Chat(ctx, req)
}

func (p *rateLimitedProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if err := p.allow(req.Model); err != nil {
		return nil, err
	}
	return p.base.Stream(ctx, req)
}

func (p *rateLimitedProvider) LastProviderJSON() json.RawMessage {
	if debugProvider, ok := p.base.(provider.DebugProvider); ok {
		return debugProvider.LastProviderJSON()
	}
	return nil
}

// SetRawEventCallback forwards the raw-event callback to the wrapped provider so
// the Debug Inspector "Raw Events" tab receives events even when the concrete
// provider is wrapped for rate limiting.
func (p *rateLimitedProvider) SetRawEventCallback(callback provider.RawEventCallback) {
	if capable, ok := p.base.(rawEventCapable); ok {
		capable.SetRawEventCallback(callback)
	}
}

func (p *rateLimitedProvider) allow(reqModel string) error {
	modelName := p.model
	if modelName == "" {
		modelName = strings.TrimSpace(reqModel)
	}
	providerName := p.providerName
	if providerName == "" && p.base != nil {
		providerName = p.base.Name()
	}
	return p.limiter.Allow(providerName, modelName)
}
