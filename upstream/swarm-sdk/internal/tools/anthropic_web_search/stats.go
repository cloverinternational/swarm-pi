// stats.go - Usage Statistics Functions
// Part of the Claude Web Search Go implementation
// Reverse engineered from Claude Code v2.1.25

package anthropic_web_search

import (
	"context"
	"time"
)

// ============================================================================
// USAGE STATISTICS - Mirrors BxA (getWebSearchRequests) and related functions
// ============================================================================

// GetModelUsage returns usage for a specific model
func (c *Client) ModelUsage(model string) *ModelUsage {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	if usage, exists := c.modelUsage[model]; exists {
		// Return a copy to prevent race conditions
		return &ModelUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			WebSearchRequests:        usage.WebSearchRequests,
			CostUSD:                  usage.CostUSD,
			ContextWindow:            usage.ContextWindow,
			MaxOutputTokens:          usage.MaxOutputTokens,
		}
	}
	return nil
}

// GetAllModelUsage returns usage for all models
func (c *Client) AllModelUsage() map[string]*ModelUsage {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	result := make(map[string]*ModelUsage)
	for model, usage := range c.modelUsage {
		result[model] = &ModelUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			WebSearchRequests:        usage.WebSearchRequests,
			CostUSD:                  usage.CostUSD,
			ContextWindow:            usage.ContextWindow,
			MaxOutputTokens:          usage.MaxOutputTokens,
		}
	}
	return result
}

// GetTotalWebSearchRequests returns total web search requests across all models
// This mirrors the BxA function from the binary:
//
//	function BxA() {
//	  return C8T(Object.values(XA.modelUsage), "webSearchRequests");
//	}
func (c *Client) TotalWebSearchRequests() int {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	total := 0
	for _, usage := range c.modelUsage {
		total += usage.WebSearchRequests
	}
	return total
}

// GetTotalInputTokens returns total input tokens across all models
// Mirrors TmT function from binary
func (c *Client) TotalInputTokens() int {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	total := 0
	for _, usage := range c.modelUsage {
		total += usage.InputTokens
	}
	return total
}

// GetTotalOutputTokens returns total output tokens across all models
// Mirrors RmT function from binary
func (c *Client) TotalOutputTokens() int {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	total := 0
	for _, usage := range c.modelUsage {
		total += usage.OutputTokens
	}
	return total
}

// GetTotalCacheReadTokens returns total cache read tokens across all models
// Mirrors AxA function from binary
func (c *Client) TotalCacheReadTokens() int {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	total := 0
	for _, usage := range c.modelUsage {
		total += usage.CacheReadInputTokens
	}
	return total
}

// GetTotalCacheCreatedTokens returns total cache created tokens across all models
// Mirrors DxA function from binary
func (c *Client) TotalCacheCreatedTokens() int {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	total := 0
	for _, usage := range c.modelUsage {
		total += usage.CacheCreationInputTokens
	}
	return total
}

// GetTotalCost returns total cost across all models
// Mirrors LI function from binary
func (c *Client) TotalCost() float64 {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	total := 0.0
	for _, usage := range c.modelUsage {
		total += usage.CostUSD
	}
	return total
}

// ResetUsage resets all usage statistics
// Mirrors iZT function from binary
func (c *Client) ResetUsage() {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()

	c.modelUsage = make(map[string]*ModelUsage)

	c.searchMu.Lock()
	c.searchCount = 0
	c.lastSearches = make([]time.Time, 0)
	c.searchMu.Unlock()
}

// UsageSummary represents a summary of usage statistics
type UsageSummary struct {
	TotalInputTokens        int                    `json:"totalInputTokens"`
	TotalOutputTokens       int                    `json:"totalOutputTokens"`
	TotalCacheReadTokens    int                    `json:"totalCacheReadTokens"`
	TotalCacheCreatedTokens int                    `json:"totalCacheCreatedTokens"`
	TotalWebSearchRequests  int                    `json:"totalWebSearchRequests"`
	TotalCostUSD            float64                `json:"totalCostUSD"`
	ModelBreakdown          map[string]*ModelUsage `json:"modelBreakdown"`
	SessionSearchCount      int                    `json:"sessionSearchCount"`
	RateLimitRemaining      int                    `json:"rateLimitRemaining"`
}

// GetUsageSummary returns a complete usage summary
func (c *Client) UsageSummary() *UsageSummary {
	c.usageMu.RLock()
	defer c.usageMu.RUnlock()

	summary := &UsageSummary{
		ModelBreakdown: make(map[string]*ModelUsage),
	}

	for model, usage := range c.modelUsage {
		summary.TotalInputTokens += usage.InputTokens
		summary.TotalOutputTokens += usage.OutputTokens
		summary.TotalCacheReadTokens += usage.CacheReadInputTokens
		summary.TotalCacheCreatedTokens += usage.CacheCreationInputTokens
		summary.TotalWebSearchRequests += usage.WebSearchRequests
		summary.TotalCostUSD += usage.CostUSD

		summary.ModelBreakdown[model] = &ModelUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			WebSearchRequests:        usage.WebSearchRequests,
			CostUSD:                  usage.CostUSD,
			ContextWindow:            usage.ContextWindow,
			MaxOutputTokens:          usage.MaxOutputTokens,
		}
	}

	// Add rate limit info
	c.searchMu.Lock()
	summary.SessionSearchCount = c.searchCount
	summary.RateLimitRemaining = c.config.MaxPerSession - c.searchCount
	c.searchMu.Unlock()

	return summary
}

// GetWebSearchStats returns statistics specifically about web search usage
type WebSearchStats struct {
	TotalRequests     int     `json:"totalRequests"`
	SessionCount      int     `json:"sessionCount"`
	SessionRemaining  int     `json:"sessionRemaining"`
	MinuteCount       int     `json:"minuteCount"`
	MinuteRemaining   int     `json:"minuteRemaining"`
	EstimatedCost     float64 `json:"estimatedCost"`
	AveragePerMessage float64 `json:"averagePerMessage"`
}

// GetWebSearchStats returns detailed web search statistics
func (c *Client) WebSearchStats() *WebSearchStats {
	c.usageMu.RLock()
	totalRequests := 0
	for _, usage := range c.modelUsage {
		totalRequests += usage.WebSearchRequests
	}
	c.usageMu.RUnlock()

	c.searchMu.Lock()
	defer c.searchMu.Unlock()

	// Count recent searches (within last minute)
	now := time.Now()
	oneMinuteAgo := now.Add(-time.Minute)
	minuteCount := 0
	for _, t := range c.lastSearches {
		if t.After(oneMinuteAgo) {
			minuteCount++
		}
	}

	// Calculate average (avoid division by zero)
	messageCount := len(c.lastSearches)
	var avg float64
	if messageCount > 0 {
		avg = float64(totalRequests) / float64(messageCount)
	}

	return &WebSearchStats{
		TotalRequests:     totalRequests,
		SessionCount:      c.searchCount,
		SessionRemaining:  c.config.MaxPerSession - c.searchCount,
		MinuteCount:       minuteCount,
		MinuteRemaining:   c.config.MaxPerMinute - minuteCount,
		EstimatedCost:     float64(totalRequests) * 0.01, // $0.01 per search estimate
		AveragePerMessage: avg,
	}
}

// HasRemainingSearchCapacity checks if there's capacity for more searches
func (c *Client) HasRemainingSearchCapacity() bool {
	c.searchMu.Lock()
	defer c.searchMu.Unlock()

	// Check session limit
	if c.searchCount >= c.config.MaxPerSession {
		return false
	}

	// Check per-minute rate limit
	now := time.Now()
	oneMinuteAgo := now.Add(-time.Minute)
	recentCount := 0
	for _, t := range c.lastSearches {
		if t.After(oneMinuteAgo) {
			recentCount++
		}
	}

	return recentCount < c.config.MaxPerMinute
}

// WaitForSearchCapacity waits until search capacity is available or context is done
func (c *Client) WaitForSearchCapacity(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if c.HasRemainingSearchCapacity() {
				return nil
			}
		}
	}
}
