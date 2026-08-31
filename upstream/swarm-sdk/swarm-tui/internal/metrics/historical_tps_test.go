package metrics

import (
	"testing"
	"time"
)

// TestRecordTurnTokensOutputBasedAvg verifies HistoricalAvgTPS is computed
// from OUTPUT tokens over streaming seconds — input tokens (the re-counted
// conversation context) must not inflate the generation rate.
func TestRecordTurnTokensOutputBasedAvg(t *testing.T) {
	s := NewMetricsStore(t.TempDir())

	// 30k input context, 500 output tokens over 10s → 50 tok/s, NOT 3050.
	s.RecordTurnTokens("m1", "Model One", 30000, 500, 10*time.Second)

	avg, _ := s.GetModelHistoricalTPS("m1")
	if avg != 50 {
		t.Errorf("HistoricalAvgTPS = %.2f, want 50 (output-only rate)", avg)
	}

	// Legacy total-throughput field keeps input+output (usage tab semantics).
	s.mu.RLock()
	total := s.ModelStats["m1"].TotalTokensGenerated
	s.mu.RUnlock()
	if total != 30500 {
		t.Errorf("TotalTokensGenerated = %d, want 30500 (legacy throughput total)", total)
	}

	// Second turn accumulates: +1000 output over 10s → (500+1000)/(10+10) = 75.
	s.RecordTurnTokens("m1", "Model One", 35000, 1000, 10*time.Second)
	avg, _ = s.GetModelHistoricalTPS("m1")
	if avg != 75 {
		t.Errorf("after second turn HistoricalAvgTPS = %.2f, want 75", avg)
	}
}

// TestRecordTurnTokensZeroDurationOrOutput verifies turns with no usable
// duration or no output don't poison the generation-rate history.
func TestRecordTurnTokensZeroDurationOrOutput(t *testing.T) {
	s := NewMetricsStore(t.TempDir())

	s.RecordTurnTokens("m1", "Model One", 1000, 100, 0)            // no duration
	s.RecordTurnTokens("m1", "Model One", 1000, 0, 10*time.Second) // no output
	if avg, _ := s.GetModelHistoricalTPS("m1"); avg != 0 {
		t.Errorf("HistoricalAvgTPS = %.2f, want 0 (no valid samples)", avg)
	}

	// A valid turn then establishes the rate cleanly.
	s.RecordTurnTokens("m1", "Model One", 1000, 200, 4*time.Second)
	if avg, _ := s.GetModelHistoricalTPS("m1"); avg != 50 {
		t.Errorf("HistoricalAvgTPS = %.2f, want 50", avg)
	}
}

// TestLoadResetsLegacyInflatedAvg verifies entries persisted before the
// output-only fields existed get their inflated averages zeroed on load.
func TestLoadResetsLegacyInflatedAvg(t *testing.T) {
	dir := t.TempDir()

	s := NewMetricsStore(dir)
	s.mu.Lock()
	s.ModelStats["legacy"] = &ModelMetrics{
		ModelID:              "legacy",
		HistoricalAvgTPS:     4920, // garbage: input+output over seconds
		TotalTokensGenerated: 306658,
		TotalStreamingSecs:   62,
		SessionCount:         26,
	}
	s.mu.Unlock()
	if err := s.SaveToDisk(); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded := NewMetricsStore(dir)
	if avg, _ := reloaded.GetModelHistoricalTPS("legacy"); avg != 0 {
		t.Errorf("legacy inflated avg survived load: %.2f, want 0", avg)
	}

	// Entries with valid output-only fields keep their (recomputed) average.
	s2 := NewMetricsStore(dir)
	s2.RecordTurnTokens("fresh", "Fresh", 1000, 600, 12*time.Second)
	if err := s2.SaveToDisk(); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	reloaded2 := NewMetricsStore(dir)
	if avg, _ := reloaded2.GetModelHistoricalTPS("fresh"); avg != 50 {
		t.Errorf("fresh avg after reload = %.2f, want 50", avg)
	}
}
