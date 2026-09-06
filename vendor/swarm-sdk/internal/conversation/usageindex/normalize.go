package usageindex

import (
	"strconv"
	"strings"
	"time"
)

// rawTokens mirrors the persisted `tokens` object on an assistant message.
type rawTokens struct {
	Input         int64
	Output        int64
	Total         int64
	CacheCreation int64
	CacheRead     int64
	// Creation5m/Creation1h mirror tokens.cache_creation_5m /
	// cache_creation_1h, the per-TTL split written by the Anthropic provider.
	// Absent (zero) on older records and on providers without prompt caching.
	Creation5m int64
	Creation1h int64
}

// rawCacheMetrics mirrors `metadata.cache_metrics`, the older place cache
// numbers were recorded before they moved onto the tokens object.
type rawCacheMetrics struct {
	Creation   int64
	Creation5m int64
	Creation1h int64
	Read       int64
}

func (c rawCacheMetrics) creationTokens() int64 {
	if c.Creation > 0 {
		return c.Creation
	}
	return nonNegative(c.Creation5m) + nonNegative(c.Creation1h)
}

func (c rawCacheMetrics) any() bool {
	return c.creationTokens() > 0 || c.Read > 0
}

// normalize converts one persisted response into disjoint token buckets.
//
// Two persisted layouts exist and they disagree about tokens.input:
//
//	tokens.cache_creation / tokens.cache_read present  -> input EXCLUDES cache
//	metadata.cache_metrics only                        -> input ALREADY INCLUDES cache
//
// Adding cache tokens to input unconditionally would double count the second
// layout; ignoring metadata.cache_metrics (what the previous scanner did) makes
// its cache activity invisible. Deciding per response is the only correct
// option, and the chosen layout is recorded so tests can assert it.
func normalize(tokens rawTokens, cache rawCacheMetrics) Response {
	var response Response
	response.Output = nonNegative(tokens.Output)
	input := nonNegative(tokens.Input)
	tokenCacheWrite := nonNegative(tokens.CacheCreation)
	tokenCacheRead := nonNegative(tokens.CacheRead)

	switch {
	case tokenCacheWrite+tokenCacheRead > 0:
		response.Schema = CacheSchemaTokens
		response.CacheWrite = tokenCacheWrite
		response.CacheRead = tokenCacheRead
		response.FreshInput = input
	case cache.any():
		response.Schema = CacheSchemaMetadata
		response.CacheWrite = cache.creationTokens()
		response.CacheRead = nonNegative(cache.Read)
		// The cache tokens are already inside `input` here, so the fresh part
		// is whatever is left over once they are removed.
		response.FreshInput = nonNegative(input - response.CacheWrite - response.CacheRead)
	default:
		response.Schema = CacheSchemaNone
		response.FreshInput = input
	}

	// Prefer the per-TTL split carried on `tokens` (current layout). Fall back
	// to metadata.cache_metrics for legacy records that predate it. Without
	// this, current-schema rows always report a zero split and the cache-break
	// detector silently assumes the 5m default even when 1h was requested.
	response.CacheWrite5m = nonNegative(tokens.Creation5m)
	if response.CacheWrite5m == 0 {
		response.CacheWrite5m = nonNegative(cache.Creation5m)
	}
	response.CacheWrite1h = nonNegative(tokens.Creation1h)
	if response.CacheWrite1h == 0 {
		response.CacheWrite1h = nonNegative(cache.Creation1h)
	}

	counted := response.FreshInput + response.CacheWrite + response.CacheRead + response.Output
	// Providers can report a total covering dimensions not broken out above
	// (Gemini thought tokens, for example). Keep the remainder instead of
	// dropping it, so the provider's own total is preserved exactly.
	response.Other = nonNegative(nonNegative(tokens.Total) - counted)
	response.Total = counted + response.Other
	return response
}

// dedupKey reproduces the identity rules the previous scanner used, so counts
// stay comparable: message IDs are globally unique, timestamp-derived keys are
// only unique within a fork lineage, and everything older falls back to the
// conversation copy it was found in.
func dedupKey(id, timestamp, conversationID string, index int, tokens rawTokens) (string, bool) {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		return "id:" + trimmed, false
	}
	if trimmed := strings.TrimSpace(timestamp); trimmed != "" {
		return "timestamp:" + trimmed +
			"|input:" + strconv.FormatInt(tokens.Input, 10) +
			"|output:" + strconv.FormatInt(tokens.Output, 10) +
			"|total:" + strconv.FormatInt(tokens.Total, 10) +
			"|cache_creation:" + strconv.FormatInt(tokens.CacheCreation, 10) +
			"|cache_read:" + strconv.FormatInt(tokens.CacheRead, 10), true
	}
	return "conversation:" + conversationID + "|index:" + strconv.Itoa(index), false
}

func parseTimestamp(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
