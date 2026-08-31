package chat

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// EnableCaching enables prompt caching with specified TTL
func (sdk *SDKIntegration) EnableCaching(ttl string) {
	sdk.cachingEnabled = true
	if ttl == "5m" || ttl == "1h" {
		sdk.cacheTTL = ttl
	} else {
		sdk.cacheTTL = "5m" // Default if invalid
	}

	sdk.logger.Info(context.Background(), "sdk.caching.enabled",
		observability.F("ttl", sdk.cacheTTL),
		observability.F("enabled", sdk.cachingEnabled),
	)
}

// DisableCaching disables prompt caching
func (sdk *SDKIntegration) DisableCaching() {
	sdk.cachingEnabled = false

	sdk.logger.Info(context.Background(), "caching.disabled")
}

// ToggleCaching toggles prompt caching on/off
func (sdk *SDKIntegration) ToggleCaching() bool {
	sdk.cachingEnabled = !sdk.cachingEnabled

	if sdk.cachingEnabled {
		sdk.logger.Info(context.Background(), "sdk.caching.toggled_on",
			observability.F("ttl", sdk.cacheTTL),
			observability.F("enabled", sdk.cachingEnabled),
		)
	} else {
		sdk.logger.Info(context.Background(), "sdk.caching.toggled_off",
			observability.F("enabled", sdk.cachingEnabled),
		)
	}

	return sdk.cachingEnabled
}

// IsCachingEnabled returns whether prompt caching is enabled
func (sdk *SDKIntegration) IsCachingEnabled() bool {
	return sdk.cachingEnabled
}

// GetCacheTTL returns the current cache TTL
func (sdk *SDKIntegration) GetCacheTTL() string {
	return sdk.cacheTTL
}

// GetCacheMetrics returns the last cache metrics
func (sdk *SDKIntegration) GetCacheMetrics() map[string]int {
	return sdk.lastCacheMetrics
}

// GetCacheHitRate returns the cache hit rate percentage
func (sdk *SDKIntegration) GetCacheHitRate() float64 {
	return sdk.cacheHitRate
}

// GetTotalCacheStats returns total cache creation and read counts
func (sdk *SDKIntegration) GetTotalCacheStats() (creation, read int) {
	return sdk.totalCacheCreation, sdk.totalCacheRead
}

// UpdateCacheMetrics updates cache metrics from streaming responses.
// This is called from the TUI after streaming completes to track cache hits.
func (sdk *SDKIntegration) UpdateCacheMetrics(ctx context.Context, metadata map[string]any) {
	if sdk == nil || metadata == nil {
		return
	}

	// Extract cache_metrics from metadata
	cacheMetricsRaw, ok := metadata["cache_metrics"]
	if !ok {
		return
	}

	// Handle both map[string]int and map[string]interface{} types
	var cacheMetrics map[string]int
	switch cm := cacheMetricsRaw.(type) {
	case map[string]int:
		cacheMetrics = cm
	case map[string]any:
		cacheMetrics = make(map[string]int)
		for k, v := range cm {
			if intVal, ok := v.(int); ok {
				cacheMetrics[k] = intVal
			}
		}
	default:
		logDebug("[SDK-CACHE] Unknown cache metrics type: %T", cacheMetricsRaw)
		return
	}

	if len(cacheMetrics) == 0 {
		return
	}

	sdk.lastCacheMetrics = cacheMetrics

	// Calculate totals (supports both legacy and new format)
	cacheCreation := cacheMetrics["cache_creation_tokens"]
	cacheCreation5m := cacheMetrics["cache_creation_5m_tokens"]
	cacheCreation1h := cacheMetrics["cache_creation_1h_tokens"]
	cacheRead := cacheMetrics["cache_read_tokens"]

	totalCreation := cacheCreation + cacheCreation5m + cacheCreation1h

	// Update session totals
	sdk.totalCacheCreation += totalCreation
	sdk.totalCacheRead += cacheRead

	// Calculate session hit rate
	totalCacheActivity := sdk.totalCacheCreation + sdk.totalCacheRead
	if totalCacheActivity > 0 {
		sdk.cacheHitRate = float64(sdk.totalCacheRead) / float64(totalCacheActivity) * 100
	}

	// Update global stats in cache manager
	if sdk.cacheManager != nil {
		if stats, ok := sdk.cacheManager.GetStats().(*CacheStats); ok {
			stats.TotalRequests++
			stats.TotalCreationTokens += int64(totalCreation)
			stats.TotalReadTokens += int64(cacheRead)

			// Calculate global hit rate
			totalGlobalActivity := stats.TotalCreationTokens + stats.TotalReadTokens
			if totalGlobalActivity > 0 {
				stats.TotalHitRate = float64(stats.TotalReadTokens) / float64(totalGlobalActivity) * 100
			}

			// Persist stats
			if err := sdk.cacheManager.SaveStats(); err != nil {
				logDebug("[SDK-CACHE] Failed to save stats: %v", err)
			}
		}
	}

	logDebug("[SDK-CACHE] Updated from streaming: creation=%d read=%d hitRate=%.1f%%",
		totalCreation, cacheRead, sdk.cacheHitRate)
}
