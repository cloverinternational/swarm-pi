package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

const (
	defaultRuntimeProvider = "ClaudeCode"
	defaultRuntimeModel    = "claude-opus-4-20250514"
)

type providerBuildDeps struct {
	Logger           observability.Logger
	Tracer           observability.Tracer
	RawDebug         bool
	EventCallback    func(providerEventMsg)
	EndpointOverride *ProviderEndpointOverride
}

type providerBuilderFunc func(providerName, model string, deps providerBuildDeps) (provider.Provider, string, string, bool, error)
type reliabilityConfigLoaderFunc func() (*commands.SwarmOSConfig, error)

func defaultSDKProviderBuilder(providerName, model string, deps providerBuildDeps) (provider.Provider, string, string, bool, error) {
	return buildProviderWithOverride(providerName, model, deps.Logger, deps.Tracer, deps.RawDebug, deps.EndpointOverride)
}

func loadSwarmOSConfig() (*commands.SwarmOSConfig, error) {
	cm, err := commands.NewConfigManager()
	if err != nil {
		return nil, err
	}
	return cm.LoadConfig()
}

// modelPinnedProvider ensures a specific provider/model pair receives requests with the expected model ID.
// This allows orchestrator fallback entries to use different models safely.
type modelPinnedProvider struct {
	base  provider.Provider
	model string
}

func newModelPinnedProvider(base provider.Provider, model string) provider.Provider {
	if base == nil {
		return nil
	}
	if strings.TrimSpace(model) == "" {
		return base
	}
	return &modelPinnedProvider{base: base, model: strings.TrimSpace(model)}
}

func (p *modelPinnedProvider) Name() string {
	return p.base.Name()
}

func (p *modelPinnedProvider) Capabilities() provider.Capabilities {
	return p.base.Capabilities()
}

func (p *modelPinnedProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	req.Model = p.model
	return p.base.Chat(ctx, req)
}

func (p *modelPinnedProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	req.Model = p.model
	return p.base.Stream(ctx, req)
}

func (p *modelPinnedProvider) LastProviderJSON() json.RawMessage {
	if debugProvider, ok := p.base.(provider.DebugProvider); ok {
		return debugProvider.LastProviderJSON()
	}
	return nil
}

// SetRawEventCallback forwards the raw-event callback to the wrapped provider so
// the Debug Inspector "Raw Events" tab receives events even when the concrete
// provider is wrapped for model pinning.
func (p *modelPinnedProvider) SetRawEventCallback(callback provider.RawEventCallback) {
	if capable, ok := p.base.(rawEventCapable); ok {
		capable.SetRawEventCallback(callback)
	}
}

func buildReliabilityProviderStack(
	primary provider.Provider,
	runtimeProvider string,
	runtimeModel string,
	retry commands.RetrySettingsConfig,
	chain *fallback.Chain,
	buildFallback func(providerName, model string) (provider.Provider, error),
	wrap func(provider.Provider) provider.Provider,
	limiter *modelRateLimiter,
	logger observability.Logger,
	eventCallback func(providerEventMsg),
) (provider.Provider, error) {
	if primary == nil {
		return nil, fmt.Errorf("primary provider is required")
	}
	if wrap == nil {
		wrap = func(p provider.Provider) provider.Provider { return p }
	}

	entries := resolveReliabilityChainEntries(runtimeProvider, runtimeModel, chain)
	if len(entries) == 0 {
		return wrap(primary), nil
	}

	decorateEntryProvider := func(base provider.Provider, entry fallback.ModelRef) provider.Provider {
		decorated := wrap(base)
		// Stacked-account rotation sits inside the rate limiter/model pin: on a
		// subscription rate limit it swaps the active OAuth account and retries
		// with a freshly built provider (the factory re-reads the token store).
		var rebuild func() (provider.Provider, error)
		if buildFallback != nil {
			entryCopy := entry
			rebuild = func() (provider.Provider, error) {
				fresh, err := buildFallback(entryCopy.Provider, entryCopy.Model)
				if err != nil {
					return nil, err
				}
				return wrap(fresh), nil
			}
		}
		decorated = maybeWrapAccountRotation(decorated, entry.Provider, rebuild, eventCallback)
		decorated = newRateLimitedProvider(decorated, limiter, entry.Provider, entry.Model)
		return newModelPinnedProvider(decorated, entry.Model)
	}

	primaryProvider := decorateEntryProvider(primary, entries[0])
	if !retry.Enabled {
		return primaryProvider, nil
	}

	retry = applyRetryDefaults(retry)
	builder := provider.NewOrchestratorBuilder().
		WithMaxRetriesPerProvider(retry.MaxRetriesPerProvider).
		WithRotateOnRateLimit(retry.RotateOnRateLimit).
		WithRetryAfterFallback(time.Duration(retry.RetryAfterFallbackMs) * time.Millisecond).
		WithLogger(logger)

	// Set callbacks for UI feedback
	if eventCallback != nil {
		builder.OnRetry(func(ctx context.Context, info provider.RetryInfo) {
			eventCallback(providerEventMsg{
				Type:         "retry",
				ProviderName: info.ProviderName,
				Attempt:      info.Attempt,
				MaxAttempts:  info.MaxAttempts,
				Error:        info.Error.Error(),
				WaitDuration: info.WaitDuration,
			})
		})
		builder.OnProviderSwitch(func(ctx context.Context, from, to string, reason error) {
			eventCallback(providerEventMsg{
				Type:         "switch",
				FromProvider: from,
				ToProvider:   to,
				Reason:       reason,
			})
		})
	}

	builder.AddProvider(reliabilityEntryName(entries[0], 0), primaryProvider, retry.MaxRetriesPerProvider)

	for idx, entry := range entries[1:] {
		if buildFallback == nil {
			continue
		}
		fallbackProvider, err := buildFallback(entry.Provider, entry.Model)
		if err != nil {
			if logger != nil {
				logger.Warn(context.Background(), "reliability.fallback_provider_skipped",
					observability.F("provider", entry.Provider),
					observability.F("model", entry.Model),
					observability.F("error", err.Error()),
				)
			}
			continue
		}
		builder.AddProvider(reliabilityEntryName(entry, idx+1), decorateEntryProvider(fallbackProvider, entry), retry.MaxRetriesPerProvider)
	}

	orchestrator, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build reliability orchestrator: %w", err)
	}
	return orchestrator, nil
}

func buildRuntimeProviderStack(
	primary provider.Provider,
	runtimeProvider string,
	runtimeModel string,
	deps providerBuildDeps,
	configLoader reliabilityConfigLoaderFunc,
	providerBuilder providerBuilderFunc,
	wrap func(provider.Provider) provider.Provider,
	now func() time.Time,
) (provider.Provider, error) {
	if providerBuilder == nil {
		providerBuilder = defaultSDKProviderBuilder
	}
	if configLoader == nil {
		configLoader = loadSwarmOSConfig
	}
	if now == nil {
		now = time.Now
	}

	retry := commands.DefaultRetrySettings()
	chain := fallback.NewChain(runtimeProvider, runtimeModel)
	var limiter *modelRateLimiter

	cfg, err := configLoader()
	if err != nil {
		retry.Enabled = false
		if deps.Logger != nil {
			deps.Logger.Warn(context.Background(), "reliability.config_load_failed",
				observability.F("error", err.Error()),
				observability.F("fallback_mode", "direct_provider"),
			)
		}
	} else if cfg != nil {
		retry = cfg.GetRetrySettings()
		if deps.EndpointOverride == nil {
			chain = cfg.GetChatFallbackChain()
			limiter = newModelRateLimiter(cfg.GetModelRateLimits(), now)
		}
	}

	return buildReliabilityProviderStack(
		primary,
		runtimeProvider,
		runtimeModel,
		retry,
		chain,
		func(providerName, model string) (provider.Provider, error) {
			prov, _, _, _, buildErr := providerBuilder(providerName, model, deps)
			if buildErr != nil {
				return nil, buildErr
			}
			return prov, nil
		},
		wrap,
		limiter,
		deps.Logger,
		deps.EventCallback,
	)
}

func applyRetryDefaults(settings commands.RetrySettingsConfig) commands.RetrySettingsConfig {
	defaults := commands.DefaultRetrySettings()
	if settings.MaxRetriesPerProvider <= 0 {
		settings.MaxRetriesPerProvider = defaults.MaxRetriesPerProvider
	}
	if settings.RetryAfterFallbackMs <= 0 {
		settings.RetryAfterFallbackMs = defaults.RetryAfterFallbackMs
	}
	return settings
}

func resolveReliabilityChainEntries(runtimeProvider, runtimeModel string, chain *fallback.Chain) []fallback.ModelRef {
	providerName := strings.TrimSpace(runtimeProvider)
	modelName := strings.TrimSpace(runtimeModel)

	if providerName == "" && chain != nil && !chain.Primary.IsEmpty() {
		providerName = strings.TrimSpace(chain.Primary.Provider)
	}
	if modelName == "" && chain != nil && !chain.Primary.IsEmpty() {
		modelName = strings.TrimSpace(chain.Primary.Model)
	}
	if providerName == "" {
		providerName = defaultRuntimeProvider
	}
	if modelName == "" {
		modelName = defaultRuntimeModel
	}

	entries := []fallback.ModelRef{{Provider: providerName, Model: modelName}}
	seen := map[string]struct{}{
		modelRefKey(providerName, modelName): {},
	}

	if chain == nil {
		return entries
	}

	for _, entry := range chain.All() {
		entryProvider := strings.TrimSpace(entry.Provider)
		entryModel := strings.TrimSpace(entry.Model)
		if entryProvider == "" || entryModel == "" {
			continue
		}
		// Never add a known non-chat-completion model (legacy completions,
		// image, audio, embedding, moderation) as a fallback attempt: it
		// always 400s, and if it's the last entry that failure would abort
		// the whole run instead of the explicit caller-requested model
		// simply succeeding or failing on its own. See Swarm-Code/mono#66.
		if fallback.IsKnownNonChatModel(entryModel) {
			continue
		}
		key := modelRefKey(entryProvider, entryModel)
		if _, exists := seen[key]; exists {
			continue
		}
		entries = append(entries, fallback.ModelRef{Provider: entryProvider, Model: entryModel})
		seen[key] = struct{}{}
	}

	return entries
}

func modelRefKey(providerName, model string) string {
	return strings.ToLower(strings.TrimSpace(providerName)) + "::" + strings.ToLower(strings.TrimSpace(model))
}

func reliabilityEntryName(ref fallback.ModelRef, index int) string {
	providerName := provider.NormalizeProviderName(ref.Provider)
	modelName := strings.TrimSpace(ref.Model)
	if modelName == "" {
		return fmt.Sprintf("%s[%d]", providerName, index)
	}
	return fmt.Sprintf("%s/%s[%d]", providerName, modelName, index)
}
