package metrics

import (
	"time"
)

// RecordCacheRead records a cache read attempt
func (c *CacheMetrics) RecordCacheRead(success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.TotalReads++
	c.ReadsThisSecond++

	if success {
		c.SuccessfulReads++
	} else {
		c.FailedReads++
	}

	c.updateHitRate()
}

// RecordCacheWrite records a cache write attempt
func (c *CacheMetrics) RecordCacheWrite(success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.TotalWrites++
	c.WritesThisSecond++

	if success {
		c.SuccessfulWrites++
	} else {
		c.FailedWrites++
	}

	c.updateSuccessRate()
}

// updateHitRate calculates hit rate (must be locked)
func (c *CacheMetrics) updateHitRate() {
	if c.TotalReads == 0 {
		c.HitRate = 0
	} else {
		c.HitRate = float64(c.SuccessfulReads) / float64(c.TotalReads) * 100.0
	}
}

// updateSuccessRate calculates overall success rate (must be locked)
func (c *CacheMetrics) updateSuccessRate() {
	totalOps := c.TotalReads + c.TotalWrites
	if totalOps == 0 {
		c.SuccessRate = 0
	} else {
		successOps := c.SuccessfulReads + c.SuccessfulWrites
		c.SuccessRate = float64(successOps) / float64(totalOps) * 100.0
	}
}

// ResetPerSecondMetrics resets the per-second counters (call once per second)
func (c *CacheMetrics) ResetPerSecondMetrics() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ReadsThisSecond = 0
	c.WritesThisSecond = 0
	c.LastResetTime = time.Now()
}

// GetCacheMetrics returns metrics atomically (deprecated - use GetMetrics on CacheMetrics)
func (c *CacheMetrics) GetCacheMetrics() (hits, reads, writes, failures int64, rate float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.SuccessfulReads, c.TotalReads, c.TotalWrites, c.FailedReads + c.FailedWrites, c.HitRate
}
