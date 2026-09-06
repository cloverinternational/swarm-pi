package metrics

import (
	"time"
)

// The tracker follows the industry-standard tokens/sec definition: OUTPUT
// tokens over generation time. Prompt/input tokens are never part of the
// rate — they are processed during prefill, not generated. Decode rate is
// measured from the first observed output token (the TTFT boundary) so prompt
// processing doesn't dilute the figure; when the whole response arrives as a
// single chunk (diffusion models), the end-to-end time is the only thing
// observable and is used as the fallback.

// StartStreaming initializes tracking for a new response. Counters reset to
// zero output tokens.
func (t *TPSMetrics) StartStreaming() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.StreamStartTime = time.Now()
	t.LastUpdateTime = t.StreamStartTime
	t.FirstTokenTime = time.Time{}
	t.TokensAtStart = 0
	t.CurrentTokens = 0
	t.Snapshots = t.Snapshots[:0] // Clear previous snapshots
	t.CurrentTPS = 0
	t.PeakTPS = 0
	t.AverageTPS = 0
}

// UpdateOutputTokens records the cumulative OUTPUT token count observed so
// far in this response and updates the live rate. Call it with output tokens
// only — never input+output.
func (t *TPSMetrics) UpdateOutputTokens(cumOutputTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	// First output observed: mark the TTFT boundary so decode-rate math can
	// exclude prompt processing time.
	if t.FirstTokenTime.IsZero() && cumOutputTokens > 0 {
		t.FirstTokenTime = now
	}

	elapsed := now.Sub(t.LastUpdateTime)

	// Debounce: only calculate if interval has passed
	if elapsed < t.SnapshotInterval {
		if cumOutputTokens > t.CurrentTokens {
			t.CurrentTokens = cumOutputTokens
		}
		return
	}

	tokensDelta := cumOutputTokens - t.CurrentTokens
	if tokensDelta <= 0 {
		return
	}

	// Calculate instantaneous TPS
	instantTPS := float64(tokensDelta) / elapsed.Seconds()

	// Create snapshot
	snapshot := TPSSnapshot{
		Timestamp:   now,
		TokensDelta: tokensDelta,
		TPS:         instantTPS,
	}

	// Add to rolling window
	t.Snapshots = append(t.Snapshots, snapshot)
	if len(t.Snapshots) > t.SnapshotWindowSize {
		t.Snapshots = t.Snapshots[1:] // Remove oldest
	}

	// Update current TPS with exponential moving average (smoothing)
	// This reduces jitter while still responding to changes
	alpha := 0.3 // Smoothing factor (0.3 = 30% new, 70% previous)
	if t.CurrentTPS == 0 {
		t.CurrentTPS = instantTPS
	} else {
		t.CurrentTPS = alpha*instantTPS + (1-alpha)*t.CurrentTPS
	}

	// Update peak TPS
	if t.CurrentTPS > t.PeakTPS {
		t.PeakTPS = t.CurrentTPS
	}

	// Calculate rolling average from snapshot window
	t.AverageTPS = t.calculateRollingAverage()

	// Update state
	t.CurrentTokens = cumOutputTokens
	t.LastUpdateTime = now
}

// calculateRollingAverage computes average of snapshot window (must be locked)
func (t *TPSMetrics) calculateRollingAverage() float64 {
	if len(t.Snapshots) == 0 {
		return 0
	}

	var sum float64
	for _, snap := range t.Snapshots {
		sum += snap.TPS
	}
	return sum / float64(len(t.Snapshots))
}

// generationSecondsLocked returns the time spent generating output: from the
// first observed output token to the last update (decode time). When only a
// single chunk was observed (first == last, e.g. diffusion models), it falls
// back to the end-to-end time since StreamStartTime. Must be called locked.
func (t *TPSMetrics) generationSecondsLocked() float64 {
	if !t.FirstTokenTime.IsZero() {
		if secs := t.LastUpdateTime.Sub(t.FirstTokenTime).Seconds(); secs > 0 {
			return secs
		}
	}
	if t.StreamStartTime.IsZero() {
		return 0
	}
	return t.LastUpdateTime.Sub(t.StreamStartTime).Seconds()
}

// GenerationSeconds is the exported, locking variant of generationSecondsLocked.
func (t *TPSMetrics) GenerationSeconds() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.generationSecondsLocked()
}

// StopStreaming finalizes the response: the session average becomes total
// output tokens over generation time.
func (t *TPSMetrics) StopStreaming() {
	t.mu.Lock()
	defer t.mu.Unlock()

	totalTokens := float64(t.CurrentTokens)
	genSecs := t.generationSecondsLocked()

	if genSecs > 0 && totalTokens > 0 {
		finalAvg := totalTokens / genSecs
		// Blend with existing average (don't override completely)
		alpha := 0.5
		if t.AverageTPS == 0 {
			t.AverageTPS = finalAvg
		} else {
			t.AverageTPS = alpha*finalAvg + (1-alpha)*t.AverageTPS
		}
	}
}

// IsStreaming returns whether we're currently tracking a stream
func (t *TPSMetrics) IsStreaming() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return !t.StreamStartTime.IsZero() && time.Since(t.LastUpdateTime) < 2*time.Second
}
