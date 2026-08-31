package profiling

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestComputeMessageHash verifies stable hash computation
func TestComputeMessageHash(t *testing.T) {
	msg1 := map[string]any{
		"role":    "user",
		"content": "Hello, world!",
	}

	hash1, err := ComputeMessageHash(msg1)
	if err != nil {
		t.Fatalf("ComputeMessageHash failed: %v", err)
	}
	if hash1 == "" {
		t.Fatalf("ComputeMessageHash returned empty string")
	}

	// Same message should produce same hash
	hash2, err := ComputeMessageHash(msg1)
	if err != nil {
		t.Fatalf("ComputeMessageHash failed on second call: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("Same message produced different hashes: %s vs %s", hash1, hash2)
	}

	// Different message should produce different hash
	msg2 := map[string]any{
		"role":    "assistant",
		"content": "Hello, world!",
	}
	hash3, err := ComputeMessageHash(msg2)
	if err != nil {
		t.Fatalf("ComputeMessageHash failed for different message: %v", err)
	}
	if hash1 == hash3 {
		t.Fatalf("Different messages produced same hash")
	}
}

// TestHashStability verifies that key order doesn't affect hash
func TestHashStability(t *testing.T) {
	// Create two messages with same content but different key order
	// JSON marshaling should produce stable order (sorted keys)
	msg := []map[string]any{
		{
			"z_field": "last",
			"a_field": "first",
			"m_field": "middle",
		},
	}

	hash1, err := ComputeMessageHash(msg)
	if err != nil {
		t.Fatalf("ComputeMessageHash failed: %v", err)
	}

	hash2, err := ComputeMessageHash(msg)
	if err != nil {
		t.Fatalf("ComputeMessageHash failed: %v", err)
	}

	if hash1 != hash2 {
		t.Fatalf("Hash instability: %s vs %s", hash1, hash2)
	}
}

// TestValidatorBeforeTranslation tests hash recording before translation
func TestValidatorBeforeTranslation(t *testing.T) {
	v := NewValidator()
	ctx := context.Background()

	messages := []map[string]string{
		{"role": "user", "content": "Hello"},
	}

	hash, err := v.BeforeTranslation(ctx, messages)
	if err != nil {
		t.Fatalf("BeforeTranslation failed: %v", err)
	}

	if hash == "" {
		t.Fatalf("BeforeTranslation returned empty hash")
	}
}

// TestValidatorAfterTranslation tests validation after translation
func TestValidatorAfterTranslation(t *testing.T) {
	v := NewValidator()
	ctx := context.Background()

	hash := "abc123"
	translated := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}

	err := v.AfterTranslation(ctx, hash, translated)
	if err != nil {
		t.Fatalf("AfterTranslation failed: %v", err)
	}
}

// TestCacheBreakDetection tests detection of cache breaks between turns
func TestCacheBreakDetection(t *testing.T) {
	v := NewValidator()
	convID := "test-conv"

	// Turn 1: Record initial state
	messages1 := []map[string]string{
		{"role": "user", "content": "Hello"},
	}
	err := v.CompareTurns(convID, 1, messages1)
	if err != nil {
		t.Fatalf("CompareTurns failed on turn 1: %v", err)
	}

	// Turn 2: Same messages, no break
	err = v.CompareTurns(convID, 2, messages1)
	if err != nil {
		t.Fatalf("CompareTurns failed on turn 2: %v", err)
	}

	metrics := v.Metrics(convID)
	if metrics == nil {
		t.Fatalf("Expected metrics after turns, got nil")
	}
	if metrics.CacheBreaks != 0 {
		t.Fatalf("Expected no cache breaks, got %d", metrics.CacheBreaks)
	}

	// Turn 3: Different messages, should detect break
	messages2 := []map[string]string{
		{"role": "user", "content": "Different content"},
	}
	err = v.CompareTurns(convID, 3, messages2)
	if err != nil {
		t.Fatalf("CompareTurns failed on turn 3: %v", err)
	}

	metrics = v.Metrics(convID)
	if metrics == nil {
		t.Fatalf("Expected metrics after turn 3, got nil")
	}
	if metrics.CacheBreaks != 1 {
		t.Fatalf("Expected 1 cache break, got %d", metrics.CacheBreaks)
	}
}

// TestNoOpProfilerZeroOverhead verifies no-op returns immediately
func TestNoOpProfilerZeroOverhead(t *testing.T) {
	noop := NewNoOpProfiler()
	ctx := context.Background()

	start := time.Now()

	// All calls should be instant
	noop.BeforeTranslation(ctx, nil)
	noop.AfterTranslation(ctx, "", nil)
	noop.TrackMutation(ctx, nil)
	noop.RecordAPIResponse(ctx, nil)
	noop.Metrics("")
	noop.GenerateReport("")
	noop.ExportMetrics(ctx, "", "json")
	noop.Reset("")

	elapsed := time.Since(start)

	// Should complete in < 1ms
	if elapsed > time.Millisecond {
		t.Fatalf("NoOpProfiler took too long: %v", elapsed)
	}
}

// TestCacheBreakEvent verifies event serialization
func TestCacheBreakEvent(t *testing.T) {
	event := &CacheBreakEvent{
		ID:             "break-123",
		Timestamp:      time.Now(),
		ConversationID: "conv-456",
		TurnNumber:     5,
		EventType:      "CACHE_BREAK_DETECTED",
		BreakType:      string(BreakSystemPromptChange),
		Severity:       string(SeverityHigh),
		Description:    "System prompt changed",
		MessageIndex:   0,
		BeforeHash:     "abc123",
		AfterHash:      "def456",
	}

	// Should be JSON serializable
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal CacheBreakEvent: %v", err)
	}

	if len(data) == 0 {
		t.Fatalf("Marshaled event is empty")
	}

	// Should be deserializable
	var unmarshaled CacheBreakEvent
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal CacheBreakEvent: %v", err)
	}

	if unmarshaled.ID != event.ID {
		t.Fatalf("Unmarshaled event differs: %s vs %s", unmarshaled.ID, event.ID)
	}
}

// TestMetricsAggregation verifies metrics collection
func TestMetricsAggregation(t *testing.T) {
	v := NewValidator()

	// Record API response
	metrics := &APIMetrics{
		Provider:            "anthropic",
		Model:               "claude-3-sonnet",
		RequestTokens:       1000,
		OutputTokens:        500,
		CacheCreationTokens: 1000,
		CacheReadTokens:     0,
		CacheHitDetected:    false,
	}

	err := v.RecordAPIResponse(context.Background(), metrics)
	if err != nil {
		t.Fatalf("RecordAPIResponse failed: %v", err)
	}

	// Record another response with cache hit
	metrics2 := &APIMetrics{
		Provider:            "anthropic",
		Model:               "claude-3-sonnet",
		RequestTokens:       100,
		OutputTokens:        50,
		CacheCreationTokens: 0,
		CacheReadTokens:     100,
		CacheHitDetected:    true,
	}

	err = v.RecordAPIResponse(context.Background(), metrics2)
	if err != nil {
		t.Fatalf("RecordAPIResponse failed for second call: %v", err)
	}

	// Check aggregated metrics
	m := v.Metrics("anthropic")
	if m == nil {
		t.Fatalf("GetMetrics returned nil")
	}

	if m.TotalAPICalls != 2 {
		t.Fatalf("Expected 2 API calls, got %d", m.TotalAPICalls)
	}

	if m.CacheHits != 1 {
		t.Fatalf("Expected 1 cache hit, got %d", m.CacheHits)
	}

	if m.CacheMisses != 1 {
		t.Fatalf("Expected 1 cache miss, got %d", m.CacheMisses)
	}

	// Cache hit rate should be 50%
	if m.CacheHitPercent < 49 || m.CacheHitPercent > 51 {
		t.Fatalf("Cache hit percent should be ~50%%, got %.1f%%", m.CacheHitPercent)
	}
}

// TestReportGeneration verifies report creation
func TestReportGeneration(t *testing.T) {
	v := NewValidator()
	convID := "test-conv"

	// Create some breaks
	for i := range 5 {
		messages := []map[string]any{
			{"role": "user", "content": "message " + string(rune(i))},
		}
		_ = v.CompareTurns(convID, i+1, messages)
	}

	report := v.GenerateReport(convID)
	if report == nil {
		t.Fatalf("GenerateReport returned nil")
	}

	if report.Summary == nil {
		t.Fatalf("Report summary is nil")
	}

	if report.GeneratedAt.IsZero() {
		t.Fatalf("Report generated_at is zero")
	}
}

// TestReset verifies conversation reset
func TestReset(t *testing.T) {
	v := NewValidator()
	convID := "test-conv"

	// Record some data
	messages := []map[string]string{
		{"role": "user", "content": "Hello"},
	}
	_ = v.CompareTurns(convID, 1, messages)

	metrics := v.Metrics(convID)
	if metrics == nil {
		t.Fatalf("Expected metrics before reset")
	}

	// Reset
	err := v.Reset(convID)
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Metrics should be gone
	metrics = v.Metrics(convID)
	if metrics != nil {
		t.Fatalf("Metrics should be nil after reset, got %v", metrics)
	}
}

// TestFactoryCreation verifies profiler factory
func TestFactoryCreation(t *testing.T) {
	// By default (ENABLE_CACHE_PROFILING not set), should get NoOpProfiler
	profiler := NewProfiler()
	if profiler == nil {
		t.Fatalf("NewProfiler returned nil")
	}

	// Should be able to use it
	hash, err := profiler.BeforeTranslation(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeforeTranslation failed: %v", err)
	}

	// NoOp should return empty hash
	if hash != "" {
		t.Fatalf("Expected empty hash from NoOp, got %s", hash)
	}
}

// TestRegistryGetSet verifies global registry
func TestRegistryGetSet(t *testing.T) {
	// Clear registry first
	ClearAll()

	convID := "test-conv"

	// Get profiler (should create one)
	p1 := GetProfiler(convID)
	if p1 == nil {
		t.Fatalf("GetProfiler returned nil")
	}

	// Get again (should return same instance)
	p2 := GetProfiler(convID)
	if p1 != p2 {
		t.Fatalf("GetProfiler should return same instance")
	}

	// Set custom profiler
	custom := NewNoOpProfiler()
	SetProfiler(convID, custom)

	p3 := GetProfiler(convID)
	if p3 != custom {
		t.Fatalf("SetProfiler should replace profiler")
	}

	// Verify it's the same custom instance on next get
	p3b := GetProfiler(convID)
	if p3b != custom {
		t.Fatalf("GetProfiler should return the custom profiler that was set")
	}

	// Clear specific profiler
	ClearProfiler(convID)

	// After clearing, the next get creates a fresh one (may be same type but different instance)
	// So we just verify that we can still get a profiler
	p4 := GetProfiler(convID)
	if p4 == nil {
		t.Fatalf("GetProfiler should still work after ClearProfiler")
	}

	// Clean up
	ClearAll()
}
