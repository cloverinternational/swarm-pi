package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Orchestrator manages multiple providers with automatic retry and fallback.
// It implements the Provider interface, allowing it to be used transparently
// wherever a single provider is expected.
//
// Features:
// - Per-provider retry with exponential backoff
// - Respect for provider-specified RetryAfter delays
// - Automatic rotation to fallback providers on persistent failures
// - Configurable callbacks for UI feedback on retry/rotation events
type Orchestrator struct {
	providers    []ProviderEntry
	config       OrchestratorConfig
	logger       observability.Logger
	currentIndex int
	mu           sync.RWMutex
}

// ProviderEntry represents a provider with its configuration.
type ProviderEntry struct {
	// Name is a human-readable name for the provider (e.g., "gemini", "anthropic")
	Name string

	// Provider is the actual provider implementation
	Provider Provider

	// MaxRetries is the maximum number of retries for this provider before rotating.
	// If 0, uses OrchestratorConfig.MaxRetriesPerProvider.
	MaxRetries int
}

// OrchestratorConfig configures the orchestrator behavior.
type OrchestratorConfig struct {
	// MaxRetriesPerProvider is the default max retries before rotating to next provider.
	// Can be overridden per-provider in ProviderEntry.MaxRetries.
	// Default: 3
	MaxRetriesPerProvider int

	// RotateOnRateLimit if true, immediately rotates to next provider on rate limit
	// instead of waiting. If false, waits for RetryAfter duration.
	// Default: false
	RotateOnRateLimit bool

	// RetryAfterFallback is the default retry delay when provider doesn't specify one.
	// Default: 30 seconds
	RetryAfterFallback time.Duration

	// OnRetry is called before each retry attempt. Can be used for UI feedback.
	OnRetry RetryCallback

	// OnProviderSwitch is called when rotating to a different provider.
	OnProviderSwitch ProviderSwitchCallback

	// OnExhausted is called when all providers have been exhausted.
	OnExhausted ExhaustedCallback

	// Logger for internal logging. Optional.
	Logger observability.Logger
}

// RetryCallback is invoked before each retry attempt.
type RetryCallback func(ctx context.Context, info RetryInfo)

// RetryInfo contains information about a retry attempt.
type RetryInfo struct {
	// ProviderName is the name of the provider being retried
	ProviderName string

	// Attempt is the current attempt number (1-indexed)
	Attempt int

	// MaxAttempts is the maximum attempts for this provider
	MaxAttempts int

	// Error is the error that triggered the retry
	Error error

	// WaitDuration is how long we'll wait before retrying
	WaitDuration time.Duration

	// IsRateLimited indicates if this is a rate limit error
	IsRateLimited bool
}

// ProviderSwitchCallback is invoked when switching to a different provider.
type ProviderSwitchCallback func(ctx context.Context, from, to string, reason error)

// ExhaustedInfo contains rich context about why all providers were exhausted.
// It is passed to ExhaustedCallback so that callers (e.g. a TUI) can build
// a meaningful "switch profile?" prompt without re-parsing raw errors.
type ExhaustedInfo struct {
	// ProviderName is the last provider that was attempted.
	ProviderName string

	// AllProviders lists every provider that was tried, in order.
	AllProviders []string

	// LastError is the final error returned by the last provider.
	LastError error

	// AllErrors contains one error per provider attempt (may contain duplicates
	// when the same provider is retried multiple times).
	AllErrors []error

	// IsRateLimit is true when the dominant failure mode was a 429 rate-limit.
	IsRateLimit bool

	// TotalAttempts is the total number of individual request attempts made
	// across all providers (retries included).
	TotalAttempts int

	// ElapsedTime is the total wall-clock time spent across all attempts.
	ElapsedTime time.Duration
}

// ExhaustedCallback is invoked when all providers have been exhausted.
type ExhaustedCallback func(ctx context.Context, info ExhaustedInfo)

// NewOrchestrator creates a new Orchestrator with the given configuration.
func NewOrchestrator(providers []ProviderEntry, config OrchestratorConfig) (*Orchestrator, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("at least one provider is required")
	}

	// Apply defaults
	if config.MaxRetriesPerProvider == 0 {
		config.MaxRetriesPerProvider = 4
	}
	if config.RetryAfterFallback == 0 {
		config.RetryAfterFallback = 30 * time.Second
	}

	return &Orchestrator{
		providers: providers,
		config:    config,
		logger:    config.Logger,
	}, nil
}

// Name returns the name of the current provider.
func (o *Orchestrator) Name() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.providers[o.currentIndex].Name
}

// Capabilities returns the capabilities of the current provider.
func (o *Orchestrator) Capabilities() Capabilities {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.providers[o.currentIndex].Provider.Capabilities()
}

// Chat executes a chat request with retry and fallback logic.
func (o *Orchestrator) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return o.executeWithFallback(ctx, func(p Provider) (*ChatResponse, error) {
		return p.Chat(ctx, req)
	})
}

// Stream executes a streaming chat request with retry and fallback logic.
func (o *Orchestrator) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	// For streaming, we can't easily retry mid-stream, so we only retry on initial connection
	var lastErr error
	var allErrors []error
	var triedProviders []string
	totalAttempts := 0
	rateLimitCount := 0
	start := time.Now()
	startIndex := o.getCurrentIndex()

	for attempts := 0; attempts < len(o.providers); attempts++ {
		currentProvider := o.getProviderAt((startIndex + attempts) % len(o.providers))
		entry := o.providers[(startIndex+attempts)%len(o.providers)]
		triedProviders = append(triedProviders, entry.Name)

		maxRetries := entry.MaxRetries
		if maxRetries == 0 {
			maxRetries = o.config.MaxRetriesPerProvider
		}

		for retry := 0; retry <= maxRetries; retry++ {
			totalAttempts++
			chunks, err := currentProvider.Stream(ctx, req)
			if err == nil {
				return chunks, nil
			}

			lastErr = err
			allErrors = append(allErrors, err)
			if o.isRateLimitError(err) {
				rateLimitCount++
			}

			// Check if we should retry
			if !o.shouldRetry(err) {
				break
			}

			// Check for rate limit with immediate rotation
			if o.isRateLimitError(err) && o.config.RotateOnRateLimit {
				o.logRetry(ctx, entry.Name, retry+1, maxRetries+1, err, 0, true)
				break // Break to rotate
			}

			// Calculate wait duration
			waitDuration := o.getRetryDelay(err)

			// Notify callback
			o.notifyRetry(ctx, entry.Name, retry+1, maxRetries+1, err, waitDuration)

			// Wait before retry
			if retry < maxRetries {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(waitDuration):
				}
			}
		}

		// Rotate to next provider
		if attempts < len(o.providers)-1 {
			nextEntry := o.providers[(startIndex+attempts+1)%len(o.providers)]
			o.notifySwitch(ctx, entry.Name, nextEntry.Name, lastErr)
		}
	}

	// All providers exhausted
	o.notifyExhausted(ctx, ExhaustedInfo{
		ProviderName:  triedProviders[len(triedProviders)-1],
		AllProviders:  triedProviders,
		LastError:     lastErr,
		AllErrors:     allErrors,
		IsRateLimit:   len(allErrors) > 0 && rateLimitCount == len(allErrors),
		TotalAttempts: totalAttempts,
		ElapsedTime:   time.Since(start),
	})
	return nil, fmt.Errorf("all providers exhausted: %w", lastErr)
}

// executeWithFallback executes a request with retry and fallback logic.
func (o *Orchestrator) executeWithFallback(ctx context.Context, fn func(Provider) (*ChatResponse, error)) (*ChatResponse, error) {
	var allErrors []error
	var triedProviders []string
	totalAttempts := 0
	rateLimitCount := 0
	start := time.Now()
	startIndex := o.getCurrentIndex()

	for providerAttempt := 0; providerAttempt < len(o.providers); providerAttempt++ {
		providerIndex := (startIndex + providerAttempt) % len(o.providers)
		entry := o.providers[providerIndex]
		currentProvider := entry.Provider
		triedProviders = append(triedProviders, entry.Name)

		maxRetries := entry.MaxRetries
		if maxRetries == 0 {
			maxRetries = o.config.MaxRetriesPerProvider
		}

		var lastErr error
		for retry := 0; retry <= maxRetries; retry++ {
			totalAttempts++
			resp, err := fn(currentProvider)
			if err == nil {
				// Update current index to this provider (it succeeded)
				o.setCurrentIndex(providerIndex)
				return resp, nil
			}

			lastErr = err
			allErrors = append(allErrors, err)
			if o.isRateLimitError(err) {
				rateLimitCount++
			}

			// Check if we should retry this provider
			if !o.shouldRetry(err) {
				o.logPermanentError(ctx, entry.Name, err)
				break // Don't retry permanent errors
			}

			// Check for rate limit with immediate rotation option
			if o.isRateLimitError(err) && o.config.RotateOnRateLimit && providerAttempt < len(o.providers)-1 {
				o.logRetry(ctx, entry.Name, retry+1, maxRetries+1, err, 0, true)
				break // Break inner loop to rotate to next provider
			}

			// Don't wait/retry if this was the last attempt
			if retry >= maxRetries {
				break
			}

			// Calculate wait duration
			waitDuration := o.getRetryDelay(err)

			// Notify retry callback
			o.notifyRetry(ctx, entry.Name, retry+1, maxRetries+1, err, waitDuration)

			// Wait before retry
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitDuration):
			}
		}

		// Rotate to next provider if available
		if providerAttempt < len(o.providers)-1 {
			nextEntry := o.providers[(providerIndex+1)%len(o.providers)]
			o.notifySwitch(ctx, entry.Name, nextEntry.Name, lastErr)
		}
	}

	// All providers exhausted — build rich info for the callback.
	lastErr := error(nil)
	if len(allErrors) > 0 {
		lastErr = allErrors[len(allErrors)-1]
	}
	lastName := ""
	if len(triedProviders) > 0 {
		lastName = triedProviders[len(triedProviders)-1]
	}
	o.notifyExhausted(ctx, ExhaustedInfo{
		ProviderName:  lastName,
		AllProviders:  triedProviders,
		LastError:     lastErr,
		AllErrors:     allErrors,
		IsRateLimit:   len(allErrors) > 0 && rateLimitCount == len(allErrors),
		TotalAttempts: totalAttempts,
		ElapsedTime:   time.Since(start),
	})

	if lastErr != nil {
		return nil, fmt.Errorf("all providers exhausted: %w", lastErr)
	}
	return nil, fmt.Errorf("all providers exhausted")
}

// shouldRetry determines if an error should trigger a retry.
func (o *Orchestrator) shouldRetry(err error) bool {
	if err == nil {
		return false
	}
	return sdkerr.IsRetryable(err) || sdkerr.IsTransient(err)
}

// isRateLimitError checks if an error is specifically a rate limit error.
// Delegates to the canonical sdkerr.IsRateLimitError helper.
func (o *Orchestrator) isRateLimitError(err error) bool {
	return sdkerr.IsRateLimitError(err)
}

// getRetryDelay returns the delay before retrying.
func (o *Orchestrator) getRetryDelay(err error) time.Duration {
	if retryAfter := sdkerr.GetRetryAfter(err); retryAfter > 0 {
		return retryAfter
	}
	return o.config.RetryAfterFallback
}

// getCurrentIndex returns the current provider index thread-safely.
func (o *Orchestrator) getCurrentIndex() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.currentIndex
}

// setCurrentIndex sets the current provider index thread-safely.
func (o *Orchestrator) setCurrentIndex(index int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.currentIndex = index
}

// getProviderAt returns the provider at the given index.
// Thread-safe: uses read lock to access providers slice.
func (o *Orchestrator) getProviderAt(index int) Provider {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.providers[index].Provider
}

// notifyRetry calls the retry callback if configured.
func (o *Orchestrator) notifyRetry(ctx context.Context, name string, attempt, maxAttempts int, err error, wait time.Duration) {
	o.logRetry(ctx, name, attempt, maxAttempts, err, wait, o.isRateLimitError(err))

	if o.config.OnRetry != nil {
		o.config.OnRetry(ctx, RetryInfo{
			ProviderName:  name,
			Attempt:       attempt,
			MaxAttempts:   maxAttempts,
			Error:         err,
			WaitDuration:  wait,
			IsRateLimited: o.isRateLimitError(err),
		})
	}
}

// notifySwitch calls the provider switch callback if configured.
func (o *Orchestrator) notifySwitch(ctx context.Context, from, to string, reason error) {
	if o.logger != nil {
		o.logger.Info(ctx, "orchestrator.provider_switch",
			observability.F("from", from),
			observability.F("to", to),
			observability.F("reason", reason.Error()),
		)
	}

	if o.config.OnProviderSwitch != nil {
		o.config.OnProviderSwitch(ctx, from, to, reason)
	}
}

// notifyExhausted calls the exhausted callback if configured.
func (o *Orchestrator) notifyExhausted(ctx context.Context, info ExhaustedInfo) {
	if o.logger != nil {
		o.logger.Warn(ctx, "orchestrator.all_providers_exhausted",
			observability.F("provider", info.ProviderName),
			observability.F("error_count", len(info.AllErrors)),
			observability.F("total_attempts", info.TotalAttempts),
			observability.F("is_rate_limit", info.IsRateLimit),
			observability.F("elapsed_ms", info.ElapsedTime.Milliseconds()),
		)
	}

	if o.config.OnExhausted != nil {
		o.config.OnExhausted(ctx, info)
	}
}

// logRetry logs a retry attempt.
func (o *Orchestrator) logRetry(ctx context.Context, name string, attempt, maxAttempts int, err error, wait time.Duration, isRateLimit bool) {
	if o.logger == nil {
		return
	}

	fields := []observability.Field{
		{Key: "provider", Value: name},
		{Key: "attempt", Value: attempt},
		{Key: "max_attempts", Value: maxAttempts},
		{Key: "error", Value: err.Error()},
		{Key: "wait_ms", Value: wait.Milliseconds()},
		{Key: "is_rate_limit", Value: isRateLimit},
	}

	o.logger.Info(ctx, "orchestrator.retry", fields...)
}

// logPermanentError logs a permanent error that won't be retried.
func (o *Orchestrator) logPermanentError(ctx context.Context, name string, err error) {
	if o.logger == nil {
		return
	}

	o.logger.Warn(ctx, "orchestrator.permanent_error",
		observability.F("provider", name),
		observability.F("error", err.Error()),
		observability.F("error_type", sdkerr.GetType(err)),
	)
}

// GetCurrentProvider returns the currently active provider.
func (o *Orchestrator) CurrentProvider() Provider {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.providers[o.currentIndex].Provider
}

// GetCurrentProviderName returns the name of the currently active provider.
func (o *Orchestrator) CurrentProviderName() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.providers[o.currentIndex].Name
}

// ProviderCount returns the number of configured providers.
func (o *Orchestrator) ProviderCount() int {
	return len(o.providers)
}

// Ensure Orchestrator implements Provider interface
var _ Provider = (*Orchestrator)(nil)
