package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const metricsFileName = "model_metrics.json"

type PersistenceData struct {
	Version    int                      `json:"version"`
	ModelStats map[string]*ModelMetrics `json:"model_stats"`
	UpdatedAt  time.Time                `json:"updated_at"`
}

// NewMetricsStore creates a new metrics store and loads persisted data
func NewMetricsStore(persistPath string) *MetricsStore {
	store := &MetricsStore{
		TPS: &TPSMetrics{
			SnapshotWindowSize: 10,
			SnapshotInterval:   100 * time.Millisecond,
			Snapshots:          make([]TPSSnapshot, 0, 20),
		},
		Cache: &CacheMetrics{
			LastResetTime: time.Now(),
		},
		ModelStats:  make(map[string]*ModelMetrics),
		PersistPath: persistPath,
	}

	// Load existing metrics from disk
	store.loadFromDisk()

	return store
}

// loadFromDisk reads metrics from disk
func (s *MetricsStore) loadFromDisk() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.PersistPath, metricsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No existing data, start fresh
		}
		return err
	}

	var persisted PersistenceData
	if err := json.Unmarshal(data, &persisted); err != nil {
		return err
	}

	s.ModelStats = persisted.ModelStats

	// Recompute HistoricalAvgTPS from the output-only fields. Entries written
	// before TotalOutputTokens existed carry averages computed from
	// input+output tokens (inflated by the whole context per turn) — zero
	// those rather than display nonsense; they rebuild on the next turn.
	for _, m := range s.ModelStats {
		if m == nil {
			continue
		}
		if m.OutputStreamingSecs > 0 {
			m.HistoricalAvgTPS = float64(m.TotalOutputTokens) / m.OutputStreamingSecs
		} else {
			m.HistoricalAvgTPS = 0
		}
	}
	return nil
}

// SaveToDisk persists metrics to disk
func (s *MetricsStore) SaveToDisk() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	persisted := PersistenceData{
		Version:    1,
		ModelStats: s.ModelStats,
		UpdatedAt:  time.Now(),
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(s.PersistPath, metricsFileName)

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// SetCurrentModel switches to tracking a different model
func (s *MetricsStore) SetCurrentModel(modelID, modelName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Flush previous model's session metrics
	if s.CurrentModel != "" && s.CurrentModel != modelID {
		s.flushCurrentSessionLocked()
	}

	s.CurrentModel = modelID

	// Ensure model entry exists in stats
	if _, exists := s.ModelStats[modelID]; !exists {
		s.ModelStats[modelID] = &ModelMetrics{
			ModelID:   modelID,
			ModelName: modelName,
		}
	}
}

// GetModelHistoricalTPS returns the persisted all-time average and peak TPS
// for a model. Unknown (or empty) model IDs return zeros.
func (s *MetricsStore) GetModelHistoricalTPS(modelID string) (avg, peak float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if stats, ok := s.ModelStats[modelID]; ok {
		return stats.HistoricalAvgTPS, stats.HistoricalPeakTPS
	}
	return 0, 0
}

// FlushCurrentSession saves session metrics to model history
func (s *MetricsStore) FlushCurrentSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushCurrentSessionLocked()
}

// maxReasonableSessionSecs caps a single session's duration contribution to
// stats.TotalStreamingSecs. Any value larger than this is treated as a clock /
// zero-time bug rather than real wall-clock streaming. 6 hours is far above any
// realistic single streaming session and well below the time.Duration overflow
// boundary (≈ 292 years) that previously polluted the file.
const maxReasonableSessionSecs = 6 * 60 * 60

// flushCurrentSessionLocked updates model stats with current session data (must be locked)
func (s *MetricsStore) flushCurrentSessionLocked() {
	if s.CurrentModel == "" {
		return
	}

	stats, exists := s.ModelStats[s.CurrentModel]
	if !exists || stats == nil {
		return
	}

	// Update TPS history
	if s.TPS.PeakTPS > stats.HistoricalPeakTPS {
		stats.HistoricalPeakTPS = s.TPS.PeakTPS
	}

	// Guard 1: StreamStartTime must be set. Calling flushCurrentSessionLocked
	// without a prior StartStreaming used to produce time.Since(time.Time{})
	// which overflows to ≈9.22e9 seconds (=MaxInt64 / 1e9 nanoseconds = 292
	// years), permanently corrupting TotalStreamingSecs. Skip those flushes.
	if s.TPS.StreamStartTime.IsZero() {
		return
	}

	// Guard 2: cap session duration. Even with a non-zero StreamStartTime, a
	// long-idle session followed by a flush could legitimately span hours;
	// anything beyond maxReasonableSessionSecs is treated as a clock issue
	// and clamped so it cannot corrupt the divisor of HistoricalAvgTPS.
	sessionDuration := time.Since(s.TPS.StreamStartTime).Seconds()
	if sessionDuration <= 0 {
		return
	}
	if sessionDuration > maxReasonableSessionSecs {
		sessionDuration = maxReasonableSessionSecs
	}

	// Guard 3: token count must be positive. The tracker counts output tokens
	// only (TokensAtStart is always 0 since the output-only refactor); treat
	// negative as 0 so a reset counter can't inflate the divisor.
	sessionTokens := max(s.TPS.CurrentTokens-s.TPS.TokensAtStart, 0)

	stats.TotalTokensGenerated += int64(sessionTokens)
	stats.TotalStreamingSecs += sessionDuration
	stats.SessionCount++

	// HistoricalAvgTPS is owned by RecordTurnTokens (output tokens over
	// streaming seconds). The legacy recompute here divided input+output by
	// double-counted seconds and produced inflated, meaningless tok/s.

	// Update cache history (cumulative)
	stats.LifetimeCacheReads += s.Cache.TotalReads
	stats.LifetimeCacheWrites += s.Cache.TotalWrites
	stats.LifetimeCacheHits += s.Cache.SuccessfulReads
	stats.LifetimeCacheMisses += s.Cache.FailedReads
	stats.LifetimeFailedOps += s.Cache.FailedReads + s.Cache.FailedWrites

	stats.LastUpdated = time.Now()
}

// RecordTurnTokens directly attributes an authoritative per-turn token count
// (input + output, as reported by the provider) to a model's lifetime stats.
// Bypasses the TPS delta path, which is brittle for non-streaming completions
// and for sessions where StartStreaming was called without UpdateTokens firing.
//
// duration is the wall-clock streaming time for this turn; pass 0 if unknown.
// SessionCount is incremented exactly once per call.
func (s *MetricsStore) RecordTurnTokens(modelID, modelName string, inputTokens, outputTokens int, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if modelID == "" {
		return
	}
	stats, exists := s.ModelStats[modelID]
	if !exists || stats == nil {
		stats = &ModelMetrics{ModelID: modelID, ModelName: modelName}
		s.ModelStats[modelID] = stats
	}
	if stats.ModelName == "" && modelName != "" {
		stats.ModelName = modelName
	}

	tokens := max(int64(inputTokens+outputTokens), 0)
	stats.TotalTokensGenerated += tokens

	secs := duration.Seconds()
	if secs > 0 && secs <= maxReasonableSessionSecs {
		stats.TotalStreamingSecs += secs
	}

	// Generation-rate history: output tokens over streaming seconds only.
	// Input tokens are processed, not generated — counting them inflated the
	// historical tok/s by the whole context size on every turn.
	if out := max(int64(outputTokens), 0); out > 0 && secs > 0 && secs <= maxReasonableSessionSecs {
		stats.TotalOutputTokens += out
		stats.OutputStreamingSecs += secs
	}

	stats.SessionCount++
	if stats.OutputStreamingSecs > 0 {
		stats.HistoricalAvgTPS = float64(stats.TotalOutputTokens) / stats.OutputStreamingSecs
	}
	stats.LastUpdated = time.Now()
}

// RepairCorruptedDurations rewrites TotalStreamingSecs to a sane value for any
// model whose persisted duration exceeds maxReasonableSessionSecs * SessionCount
// (a sign of the time.Since(zero) overflow bug fixed in flushCurrentSessionLocked).
// Returns the number of model entries repaired.
func (s *MetricsStore) RepairCorruptedDurations() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	repaired := 0
	for _, m := range s.ModelStats {
		if m == nil {
			continue
		}
		ceiling := float64(maxReasonableSessionSecs) * float64(max(m.SessionCount, 1))
		if m.TotalStreamingSecs > ceiling {
			m.TotalStreamingSecs = 0
			m.HistoricalAvgTPS = 0
			repaired++
		}
	}
	return repaired
}

// GetModelMetrics returns historical metrics for a model
func (s *MetricsStore) GetModelMetrics(modelID string) *ModelMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.ModelStats[modelID]
}

// GetAllModelMetrics returns a snapshot copy of all per-model historical metrics.
func (s *MetricsStore) GetAllModelMetrics() map[string]ModelMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]ModelMetrics, len(s.ModelStats))
	for k, v := range s.ModelStats {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}
