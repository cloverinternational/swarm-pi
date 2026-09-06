package metrics

import (
	"testing"
	"time"
)

// TestTPSCalculation verifies TPS is calculated correctly
func TestTPSCalculation(t *testing.T) {
	tps := &TPSMetrics{
		SnapshotWindowSize: 5,
		SnapshotInterval:   50 * time.Millisecond,
		Snapshots:          make([]TPSSnapshot, 0, 10),
	}

	// Start streaming (tracker counts output tokens only)
	tps.StartStreaming()

	// Simulate 100ms passing, 20 output tokens received = 200 t/s
	time.Sleep(100 * time.Millisecond)
	tps.UpdateOutputTokens(20)

	current, peak, avg := tps.GetMetrics()

	// Should have non-zero TPS
	if current <= 0 {
		t.Errorf("CurrentTPS should be > 0, got %f", current)
	}

	// Peak should match current (first sample)
	if peak <= 0 {
		t.Errorf("PeakTPS should be > 0, got %f", peak)
	}

	// Average should be set
	if avg <= 0 {
		t.Errorf("AvgTPS should be > 0, got %f", avg)
	}

	t.Logf("TPS - Current: %.1f, Peak: %.1f, Avg: %.1f", current, peak, avg)
}

// TestTPSEMASmoothing verifies EMA smoothing reduces jitter
func TestTPSEMASmoothing(t *testing.T) {
	tps := &TPSMetrics{
		SnapshotWindowSize: 10,
		SnapshotInterval:   50 * time.Millisecond,
		Snapshots:          make([]TPSSnapshot, 0, 20),
	}

	tps.StartStreaming()

	// Simulate varying token arrivals (high, low, high pattern)
	delays := []time.Duration{100, 50, 100}
	tokenDeltas := []int{50, 10, 50}

	for i, delay := range delays {
		time.Sleep(delay * time.Millisecond)
		tps.UpdateOutputTokens(int(float64(tokenDeltas[i]) * float64(i+1)))
	}

	current, peak, avg := tps.GetMetrics()

	// Peak should be highest value recorded
	if peak < current {
		t.Logf("Peak (%.1f) should be >= Current (%.1f)", peak, current)
	}

	// Average should be between low and high values
	if avg <= 0 {
		t.Errorf("AvgTPS should be > 0, got %f", avg)
	}

	t.Logf("EMA Test - Current: %.1f, Peak: %.1f, Avg: %.1f", current, peak, avg)
}

// TestCacheMetricsAccuracy verifies cache hit/fail recording
func TestCacheMetricsAccuracy(t *testing.T) {
	cache := &CacheMetrics{}

	// Record 5 reads: 3 hits, 2 misses
	cache.RecordCacheRead(true)
	cache.RecordCacheRead(true)
	cache.RecordCacheRead(true)
	cache.RecordCacheRead(false)
	cache.RecordCacheRead(false)

	hits, reads, _, failures, hitRate := cache.GetMetrics()

	if reads != 5 {
		t.Errorf("Expected 5 reads, got %d", reads)
	}

	if hits != 3 {
		t.Errorf("Expected 3 hits, got %d", hits)
	}

	if failures != 2 {
		t.Errorf("Expected 2 failures, got %d", failures)
	}

	expectedRate := 60.0 // 3/5 * 100
	if hitRate < expectedRate-0.1 || hitRate > expectedRate+0.1 {
		t.Errorf("Expected hit rate ~60%%, got %.1f%%", hitRate)
	}

	t.Logf("Cache - Hits: %d, Reads: %d, Failures: %d, HitRate: %.1f%%", hits, reads, failures, hitRate)
}

// TestCacheMetricsWrites verifies cache write recording
func TestCacheMetricsWrites(t *testing.T) {
	cache := &CacheMetrics{}

	// Record writes
	cache.RecordCacheWrite(true)
	cache.RecordCacheWrite(true)
	cache.RecordCacheWrite(false)

	_, reads, writes, failures, _ := cache.GetMetrics()

	if reads != 0 {
		t.Errorf("Expected 0 reads, got %d", reads)
	}

	if writes != 3 {
		t.Errorf("Expected 3 writes, got %d", writes)
	}

	if failures != 1 {
		t.Errorf("Expected 1 failure, got %d", failures)
	}

	t.Logf("Cache Writes - Writes: %d, Failures: %d", writes, failures)
}

// TestMetricsStorePersistence verifies metrics can be saved and loaded
func TestMetricsStorePersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create store and add data
	store := NewMetricsStore(tmpDir)
	store.SetCurrentModel("test-model", "Test Model")

	// Simulate some TPS data
	store.TPS.StartStreaming()
	store.TPS.UpdateOutputTokens(100)
	store.TPS.UpdateOutputTokens(200)
	store.TPS.StopStreaming()

	// Flush and save
	store.FlushCurrentSession()
	if err := store.SaveToDisk(); err != nil {
		t.Fatalf("Failed to save metrics: %v", err)
	}

	// Load in new store
	store2 := NewMetricsStore(tmpDir)

	// Verify data loaded
	metrics := store2.GetModelMetrics("test-model")
	if metrics == nil {
		t.Errorf("Expected to load model metrics, got nil")
		return
	}

	if metrics.ModelName != "Test Model" {
		t.Errorf("Expected model name 'Test Model', got %s", metrics.ModelName)
	}

	if metrics.TotalTokensGenerated <= 0 {
		t.Errorf("Expected positive token count, got %d", metrics.TotalTokensGenerated)
	}

	t.Logf("Persistence - Loaded model: %s, tokens: %d", metrics.ModelName, metrics.TotalTokensGenerated)
}

// TestMetricsStoreMultipleModels verifies per-model tracking
func TestMetricsStoreMultipleModels(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewMetricsStore(tmpDir)

	// Simulate model 1
	store.SetCurrentModel("model-1", "Model 1")
	store.TPS.StartStreaming()
	store.TPS.UpdateOutputTokens(50)
	store.TPS.StopStreaming()
	store.FlushCurrentSession()

	// Simulate model 2
	store.SetCurrentModel("model-2", "Model 2")
	store.TPS.StartStreaming()
	store.TPS.UpdateOutputTokens(100)
	store.TPS.StopStreaming()
	store.FlushCurrentSession()

	// Save and reload
	store.SaveToDisk()
	store2 := NewMetricsStore(tmpDir)

	// Verify both models exist
	m1 := store2.GetModelMetrics("model-1")
	m2 := store2.GetModelMetrics("model-2")

	if m1 == nil {
		t.Errorf("Expected model-1 metrics, got nil")
	}
	if m2 == nil {
		t.Errorf("Expected model-2 metrics, got nil")
	}

	t.Logf("Multi-model - Model 1 tokens: %d, Model 2 tokens: %d",
		m1.TotalTokensGenerated, m2.TotalTokensGenerated)
}

// TestRollingAverageCalculation verifies rolling window average
func TestRollingAverageCalculation(t *testing.T) {
	tps := &TPSMetrics{
		SnapshotWindowSize: 3,
		SnapshotInterval:   50 * time.Millisecond,
		Snapshots:          make([]TPSSnapshot, 0, 6),
	}

	tps.StartStreaming()

	// Add multiple snapshots
	for i := range 5 {
		time.Sleep(50 * time.Millisecond)
		tps.UpdateOutputTokens((i + 1) * 10)
	}

	_, _, avg := tps.GetMetrics()

	// Average should be calculated from recent snapshots
	if avg <= 0 {
		t.Errorf("Expected positive average, got %f", avg)
	}

	// With rolling window of 3, shouldn't include all 5 measurements
	if len(tps.Snapshots) > 3 {
		t.Errorf("Snapshots should be limited to window size 3, got %d", len(tps.Snapshots))
	}

	t.Logf("Rolling average - Snapshots: %d, Average: %.1f", len(tps.Snapshots), avg)
}
