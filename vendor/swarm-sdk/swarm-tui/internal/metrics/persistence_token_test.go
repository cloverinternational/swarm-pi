package metrics

import (
	"path/filepath"
	"testing"
	"time"
)

// TestFlush_ZeroStartTime_DoesNotCorruptDuration reproduces the production bug
// observed in ~/.config/swarm-tui/metrics/model_metrics.json where
// TotalStreamingSecs accumulated to ≈9.22e9 (=MaxInt64 / 1e9 nanoseconds).
//
// Pre-fix: flushCurrentSessionLocked called time.Since(time.Time{}) when
// StartStreaming had not been invoked, overflowing time.Duration and polluting
// stats forever.
//
// Post-fix: flushCurrentSessionLocked returns early when StreamStartTime is
// zero — duration stays at 0, no corruption.
func TestFlush_ZeroStartTime_DoesNotCorruptDuration(t *testing.T) {
	dir := t.TempDir()
	s := NewMetricsStore(dir)
	s.SetCurrentModel("claude-sonnet-4-6", "Sonnet 4.6")

	// Do NOT call StartStreaming. Then flush.
	s.FlushCurrentSession()

	got := s.ModelStats["claude-sonnet-4-6"]
	if got == nil {
		t.Fatal("model entry missing")
	}
	if got.TotalStreamingSecs != 0 {
		t.Errorf("TotalStreamingSecs after flush w/o StartStreaming: got %v, want 0", got.TotalStreamingSecs)
	}
	if got.SessionCount != 0 {
		t.Errorf("SessionCount: got %d, want 0 (no real session ran)", got.SessionCount)
	}
}

// TestFlush_ClampsAbsurdDurations protects against any future regression where
// a non-zero but absurd StreamStartTime sneaks through. The cap is
// maxReasonableSessionSecs.
func TestFlush_ClampsAbsurdDurations(t *testing.T) {
	dir := t.TempDir()
	s := NewMetricsStore(dir)
	s.SetCurrentModel("claude-haiku-4-5", "Haiku 4.5")

	// Fabricate a StartStreaming time from 100 years ago.
	s.TPS.StreamStartTime = time.Now().Add(-100 * 365 * 24 * time.Hour)
	s.TPS.LastUpdateTime = s.TPS.StreamStartTime
	s.TPS.TokensAtStart = 0
	s.TPS.CurrentTokens = 100

	s.FlushCurrentSession()

	got := s.ModelStats["claude-haiku-4-5"]
	if got.TotalStreamingSecs > maxReasonableSessionSecs {
		t.Errorf("TotalStreamingSecs not clamped: got %v, max=%d", got.TotalStreamingSecs, maxReasonableSessionSecs)
	}
	if got.TotalTokensGenerated != 100 {
		t.Errorf("TotalTokensGenerated: got %d, want 100", got.TotalTokensGenerated)
	}
}

// TestRecordTurnTokens_DirectAttribution exercises the new authoritative path.
// Even with no StartStreaming/UpdateTokens at all, tokens land correctly.
func TestRecordTurnTokens_DirectAttribution(t *testing.T) {
	dir := t.TempDir()
	s := NewMetricsStore(dir)

	s.RecordTurnTokens("claude-sonnet-4-6", "Sonnet 4.6", 27809, 153, 1200*time.Millisecond)
	s.RecordTurnTokens("claude-sonnet-4-6", "Sonnet 4.6", 8100, 42, 800*time.Millisecond)

	got := s.ModelStats["claude-sonnet-4-6"]
	if got == nil {
		t.Fatal("model stats missing after RecordTurnTokens")
	}
	wantTokens := int64(27809 + 153 + 8100 + 42)
	if got.TotalTokensGenerated != wantTokens {
		t.Errorf("TotalTokensGenerated: got %d, want %d", got.TotalTokensGenerated, wantTokens)
	}
	if got.SessionCount != 2 {
		t.Errorf("SessionCount: got %d, want 2", got.SessionCount)
	}
	wantSecs := 2.0
	if got.TotalStreamingSecs < wantSecs-0.1 || got.TotalStreamingSecs > wantSecs+0.1 {
		t.Errorf("TotalStreamingSecs: got %v, want ~%v", got.TotalStreamingSecs, wantSecs)
	}
}

// TestRepairCorruptedDurations rescues files that were poisoned by the old bug
// before the guards were in place.
func TestRepairCorruptedDurations(t *testing.T) {
	dir := t.TempDir()
	s := NewMetricsStore(dir)

	// Mirror the production poison: 14 sessions, ~600 tokens, 1.29e11 seconds.
	s.ModelStats["claude-sonnet-4-6"] = &ModelMetrics{
		ModelID:              "claude-sonnet-4-6",
		ModelName:            "Sonnet 4.6",
		TotalTokensGenerated: 600,
		TotalStreamingSecs:   1.29e11, // ~4095 years
		SessionCount:         14,
	}
	s.ModelStats["clean"] = &ModelMetrics{
		ModelID:              "clean",
		TotalTokensGenerated: 1000,
		TotalStreamingSecs:   42.0, // valid
		SessionCount:         3,
	}

	n := s.RepairCorruptedDurations()
	if n != 1 {
		t.Errorf("repaired count: got %d, want 1", n)
	}
	if s.ModelStats["claude-sonnet-4-6"].TotalStreamingSecs != 0 {
		t.Errorf("poisoned entry not zeroed: %v", s.ModelStats["claude-sonnet-4-6"].TotalStreamingSecs)
	}
	if s.ModelStats["clean"].TotalStreamingSecs != 42.0 {
		t.Errorf("clean entry mistakenly repaired: %v", s.ModelStats["clean"].TotalStreamingSecs)
	}
	// Token totals must NOT be touched by the repair — the user's lifetime
	// counts are preserved even when duration is reset.
	if s.ModelStats["claude-sonnet-4-6"].TotalTokensGenerated != 600 {
		t.Errorf("repair clobbered TotalTokensGenerated: got %d", s.ModelStats["claude-sonnet-4-6"].TotalTokensGenerated)
	}
}

// TestFlush_NegativeDelta_TreatedAsZero — TPS path used to attribute negative
// session tokens (CurrentTokens < TokensAtStart), corrupting future averages.
// Now negative deltas clamp to zero.
func TestFlush_NegativeDelta_TreatedAsZero(t *testing.T) {
	dir := t.TempDir()
	s := NewMetricsStore(dir)
	s.SetCurrentModel("m", "M")

	s.TPS.StreamStartTime = time.Now().Add(-1 * time.Second)
	s.TPS.LastUpdateTime = time.Now()
	s.TPS.TokensAtStart = 30000
	s.TPS.CurrentTokens = 100 // upstream caller reset the running counter
	s.FlushCurrentSession()

	got := s.ModelStats["m"]
	if got.TotalTokensGenerated != 0 {
		t.Errorf("negative delta should clamp to 0; got %d", got.TotalTokensGenerated)
	}
}

// Sanity: persistence round-trip still works after the new field set.
func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewMetricsStore(dir)
	s.RecordTurnTokens("m", "Model", 100, 50, time.Second)
	if err := s.SaveToDisk(); err != nil {
		t.Fatal(err)
	}

	s2 := NewMetricsStore(dir)
	got := s2.ModelStats["m"]
	if got == nil {
		t.Fatal("did not reload model")
	}
	if got.TotalTokensGenerated != 150 {
		t.Errorf("round-trip totals: got %d, want 150", got.TotalTokensGenerated)
	}
	_ = filepath.Join // keep "filepath" import in case of future asserts
}
