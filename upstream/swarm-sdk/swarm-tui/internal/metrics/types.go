package metrics

import (
	"sync"
	"time"
)

// TPSSnapshot represents a point-in-time TPS measurement
type TPSSnapshot struct {
	Timestamp   time.Time `json:"timestamp"`
	TokensDelta int       `json:"tokens_delta"`
	TPS         float64   `json:"tps"`
}

// TPSMetrics tracks tokens-per-second for streaming sessions
type TPSMetrics struct {
	mu sync.RWMutex

	// Current streaming session. CurrentTokens counts OUTPUT tokens only —
	// the rate never includes prompt/input tokens (see tps_tracker.go).
	StreamStartTime time.Time     `json:"-"`
	FirstTokenTime  time.Time     `json:"-"` // TTFT boundary: first output observed
	LastUpdateTime  time.Time     `json:"-"`
	TokensAtStart   int           `json:"-"` // retained for persisted-layout compatibility; always 0
	CurrentTokens   int           `json:"-"`
	Snapshots       []TPSSnapshot `json:"-"`

	// Computed values (updated in real-time)
	CurrentTPS float64 `json:"current_tps"`
	PeakTPS    float64 `json:"peak_tps"`
	AverageTPS float64 `json:"average_tps"`

	// Configuration
	SnapshotWindowSize int           `json:"-"`
	SnapshotInterval   time.Duration `json:"-"`
}

// CacheMetrics tracks cache operation statistics
type CacheMetrics struct {
	mu sync.RWMutex

	// Current session metrics
	TotalReads       int64 `json:"total_reads"`
	TotalWrites      int64 `json:"total_writes"`
	SuccessfulReads  int64 `json:"successful_reads"`
	SuccessfulWrites int64 `json:"successful_writes"`
	FailedReads      int64 `json:"failed_reads"`
	FailedWrites     int64 `json:"failed_writes"`

	// Computed values
	HitRate     float64 `json:"hit_rate"`
	SuccessRate float64 `json:"success_rate"`

	// For animation (per-second deltas)
	ReadsThisSecond  int64     `json:"-"`
	WritesThisSecond int64     `json:"-"`
	LastResetTime    time.Time `json:"-"`
}

// ModelMetrics stores per-model historical statistics
type ModelMetrics struct {
	ModelID   string `json:"model_id"`
	ModelName string `json:"model_name"`

	// TPS history
	HistoricalPeakTPS    float64 `json:"historical_peak_tps"`
	HistoricalAvgTPS     float64 `json:"historical_avg_tps"`
	TotalTokensGenerated int64   `json:"total_tokens_generated"`
	TotalStreamingSecs   float64 `json:"total_streaming_seconds"`
	SessionCount         int     `json:"session_count"`

	// Generation-rate history (output only). HistoricalAvgTPS is computed from
	// these — the legacy TotalTokensGenerated/TotalStreamingSecs pair counts
	// input tokens too (re-counting the whole context every turn), which
	// inflated tok/s by orders of magnitude. The legacy fields are kept for
	// the usage tab's total-throughput display.
	TotalOutputTokens   int64   `json:"total_output_tokens"`
	OutputStreamingSecs float64 `json:"output_streaming_seconds"`

	// Cache history
	LifetimeCacheReads  int64 `json:"lifetime_cache_reads"`
	LifetimeCacheWrites int64 `json:"lifetime_cache_writes"`
	LifetimeCacheHits   int64 `json:"lifetime_cache_hits"`
	LifetimeCacheMisses int64 `json:"lifetime_cache_misses"`
	LifetimeFailedOps   int64 `json:"lifetime_failed_ops"`

	LastUpdated time.Time `json:"last_updated"`
}

// MetricsStore is the main metrics storage container
type MetricsStore struct {
	mu sync.RWMutex

	// Current session (in-memory only)
	CurrentModel string
	TPS          *TPSMetrics
	Cache        *CacheMetrics

	// Per-model historical data (persisted to disk)
	ModelStats map[string]*ModelMetrics

	// File path for persistence
	PersistPath string
}

// GetTPSMetrics returns current TPS metrics atomically
func (t *TPSMetrics) GetMetrics() (current, peak, avg float64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.CurrentTPS, t.PeakTPS, t.AverageTPS
}

// GetCacheMetrics returns cache metrics atomically
func (c *CacheMetrics) GetMetrics() (hits, reads, writes, failures int64, rate float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.SuccessfulReads, c.TotalReads, c.TotalWrites, c.FailedReads + c.FailedWrites, c.HitRate
}
