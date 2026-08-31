package findings

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestFindingCreation tests basic Finding struct creation
func TestFindingCreation(t *testing.T) {
	finding := Finding{
		FindingID:      uuid.New().String(),
		ToolName:       "evaluate",
		ToolInput:      map[string]any{"dataset": "chat"},
		ToolOutput:     map[string]any{"accuracy": 0.95, "pgr": 0.87},
		Timestamp:      time.Now(),
		AgentID:        "agent-test-123",
		ConversationID: "conv-456",
		ContextSummary: "Evaluating W2S approach on chat dataset",
		Tags:           []string{"w2s", "chat", "evaluation"},
		Metadata: FindingMetadata{
			DurationMs: 1234,
			TokensUsed: 500,
			Success:    true,
			Priority:   75,
			Source:     "local",
		},
	}

	if finding.FindingID == "" {
		t.Error("Finding should have auto-generated ID")
	}

	if finding.ToolName != "evaluate" {
		t.Errorf("Expected tool name 'evaluate', got '%s'", finding.ToolName)
	}

	if len(finding.Tags) != 3 {
		t.Errorf("Expected 3 tags, got %d", len(finding.Tags))
	}
}

// TestFileCacheWriteRead tests basic cache operations
func TestFileCacheWriteRead(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{
		LocalCacheDir: tmpDir,
	}

	cache, err := NewFileCache(config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer cache.Close()

	// Create a finding
	finding := Finding{
		FindingID:      uuid.New().String(),
		ToolName:       "test_tool",
		Timestamp:      time.Now(),
		AgentID:        "test-agent",
		ContextSummary: "Test finding",
		Metadata: FindingMetadata{
			Success: true,
		},
	}

	// Write to cache
	ctx := context.Background()
	if err := cache.Write(ctx, finding); err != nil {
		t.Fatalf("Failed to write finding: %v", err)
	}

	// Wait for async background writer to flush
	time.Sleep(100 * time.Millisecond)

	// Read back
	read, err := cache.Read(ctx, finding.FindingID)
	if err != nil {
		t.Fatalf("Failed to read finding: %v", err)
	}

	if read.FindingID != finding.FindingID {
		t.Errorf("Finding ID mismatch: expected %s, got %s", finding.FindingID, read.FindingID)
	}

	if read.ToolName != finding.ToolName {
		t.Errorf("Tool name mismatch: expected %s, got %s", finding.ToolName, read.ToolName)
	}
}

// TestFileCacheQuery tests query functionality
func TestFileCacheQuery(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{LocalCacheDir: tmpDir}
	cache, err := NewFileCache(config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()

	// Create multiple findings
	findings := []Finding{
		{
			FindingID:      uuid.New().String(),
			ToolName:       "evaluate",
			Timestamp:      time.Now(),
			AgentID:        "agent-1",
			ContextSummary: "First evaluation",
			Tags:           []string{"w2s", "chat"},
			Metadata:       FindingMetadata{Success: true, Priority: 80},
		},
		{
			FindingID:      uuid.New().String(),
			ToolName:       "evaluate",
			Timestamp:      time.Now(),
			AgentID:        "agent-2",
			ContextSummary: "Second evaluation",
			Tags:           []string{"w2s", "math"},
			Metadata:       FindingMetadata{Success: false, Priority: 90},
		},
		{
			FindingID:      uuid.New().String(),
			ToolName:       "build",
			Timestamp:      time.Now().Add(-time.Hour),
			AgentID:        "agent-1",
			ContextSummary: "Build task",
			Tags:           []string{"build", "compile"},
			Metadata:       FindingMetadata{Success: true, Priority: 50},
		},
	}

	for _, f := range findings {
		if err := cache.Write(ctx, f); err != nil {
			t.Fatalf("Failed to write finding: %v", err)
		}
	}

	// Wait a moment for async writes
	time.Sleep(100 * time.Millisecond)

	// Test query by tool name
	results, err := cache.Query(ctx, FindingQuery{ToolName: "evaluate"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 evaluate results, got %d", len(results))
	}

	// Test query by agent
	results, err = cache.Query(ctx, FindingQuery{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 agent-1 results, got %d", len(results))
	}

	// Test query by tags
	results, err = cache.Query(ctx, FindingQuery{Tags: []string{"w2s"}})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 w2s results, got %d", len(results))
	}

	// Test query with limit
	results, err = cache.Query(ctx, FindingQuery{Limit: 2})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results with limit, got %d", len(results))
	}
}

// TestFileCacheDelete tests deletion
func TestFileCacheDelete(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{LocalCacheDir: tmpDir}
	cache, err := NewFileCache(config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()

	// Create finding
	finding := Finding{
		FindingID:      uuid.New().String(),
		ToolName:       "delete_test",
		Timestamp:      time.Now(),
		AgentID:        "test-agent",
		ContextSummary: "To be deleted",
		Metadata:       FindingMetadata{Success: true},
	}

	if err := cache.Write(ctx, finding); err != nil {
		t.Fatalf("Failed to write finding: %v", err)
	}

	// Wait for write
	time.Sleep(100 * time.Millisecond)

	// Verify it exists
	_, err = cache.Read(ctx, finding.FindingID)
	if err != nil {
		t.Fatalf("Finding should exist before delete: %v", err)
	}

	// Delete
	if err := cache.Delete(ctx, finding.FindingID); err != nil {
		t.Fatalf("Failed to delete finding: %v", err)
	}

	// Verify it's gone
	_, err = cache.Read(ctx, finding.FindingID)
	if err == nil {
		t.Error("Finding should not exist after delete")
	}
}

// TestFileCacheList tests pagination
func TestFileCacheList(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{LocalCacheDir: tmpDir}
	cache, err := NewFileCache(config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()

	// Create 5 findings
	for range 5 {
		f := Finding{
			FindingID:      uuid.New().String(),
			ToolName:       "list_test",
			Timestamp:      time.Now(),
			AgentID:        "test-agent",
			ContextSummary: "List test finding",
			Metadata:       FindingMetadata{Success: true},
		}
		if err := cache.Write(ctx, f); err != nil {
			t.Fatalf("Failed to write finding: %v", err)
		}
	}

	// Wait for writes
	time.Sleep(200 * time.Millisecond)

	// List with limit
	list, err := cache.List(ctx, 3, 0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(list) != 3 {
		t.Errorf("Expected 3 results with limit=3, got %d", len(list))
	}
}

// TestInMemoryIndex tests the semantic index
func TestInMemoryIndex(t *testing.T) {
	index := NewInMemoryIndex()
	defer index.Close()

	ctx := context.Background()

	// Index a finding
	finding := Finding{
		FindingID:      uuid.New().String(),
		ToolName:       "evaluate",
		Timestamp:      time.Now(),
		AgentID:        "test-agent",
		ContextSummary: "Test evaluation",
		Tags:           []string{"w2s", "evaluation"},
		Metadata:       FindingMetadata{Success: true},
	}

	if err := index.Index(ctx, finding); err != nil {
		t.Fatalf("Failed to index finding: %v", err)
	}

	// Search (simple implementation does text matching)
	results, err := index.SemanticSearch(ctx, "evaluate", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// In the simple implementation, we expect at least 1 result
	// because the tool name matches exactly
	if len(results) < 1 {
		t.Errorf("Expected at least 1 result, got %d", len(results))
	}

	// Delete and verify
	if err := index.Delete(ctx, finding.FindingID); err != nil {
		t.Fatalf("Failed to delete from index: %v", err)
	}

	results, err = index.SemanticSearch(ctx, "evaluate", 10)
	if err != nil {
		t.Fatalf("Search after delete failed: %v", err)
	}

	// After delete, the finding should not be found
	found := false
	for _, r := range results {
		if r.Finding.FindingID == finding.FindingID {
			found = true
			break
		}
	}
	if found {
		t.Error("Finding should not be found after delete")
	}
}

// TestConfigLoadSave tests configuration persistence
func TestConfigLoadSave(t *testing.T) {
	tmpDir := t.TempDir()

	config := Config{
		LocalCacheDir:       tmpDir,
		MaxCacheSizeMB:      500,
		RetentionDays:       60,
		EnableSemanticIndex: true,
		SyncEndpoint:        "https://findings.swarm.example.com",
		SyncEnabled:         true,
		AutoCaptureTools:    []string{"evaluate", "build"},
	}

	// Save config
	if err := SaveConfig(tmpDir, config); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Load config
	loaded, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if loaded.MaxCacheSizeMB != config.MaxCacheSizeMB {
		t.Errorf("MaxCacheSizeMB mismatch: expected %d, got %d", config.MaxCacheSizeMB, loaded.MaxCacheSizeMB)
	}

	if loaded.RetentionDays != config.RetentionDays {
		t.Errorf("RetentionDays mismatch: expected %d, got %d", config.RetentionDays, loaded.RetentionDays)
	}

	if loaded.EnableSemanticIndex != config.EnableSemanticIndex {
		t.Errorf("EnableSemanticIndex mismatch: expected %v, got %v", config.EnableSemanticIndex, loaded.EnableSemanticIndex)
	}
}

// TestQueryTimeRange tests time-based filtering
func TestQueryTimeRange(t *testing.T) {
	tmpDir := t.TempDir()
	config := Config{LocalCacheDir: tmpDir}
	cache, err := NewFileCache(config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()
	now := time.Now()

	// Create findings at different times
	findings := []Finding{
		{
			FindingID:      uuid.New().String(),
			ToolName:       "old",
			Timestamp:      now.Add(-48 * time.Hour),
			AgentID:        "test",
			ContextSummary: "Old finding",
			Metadata:       FindingMetadata{Success: true},
		},
		{
			FindingID:      uuid.New().String(),
			ToolName:       "recent",
			Timestamp:      now.Add(-2 * time.Hour),
			AgentID:        "test",
			ContextSummary: "Recent finding",
			Metadata:       FindingMetadata{Success: true},
		},
		{
			FindingID:      uuid.New().String(),
			ToolName:       "very_recent",
			Timestamp:      now,
			AgentID:        "test",
			ContextSummary: "Very recent finding",
			Metadata:       FindingMetadata{Success: true},
		},
	}

	for _, f := range findings {
		if err := cache.Write(ctx, f); err != nil {
			t.Fatalf("Failed to write finding: %v", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	// Query for recent findings (last 3 hours)
	results, err := cache.Query(ctx, FindingQuery{
		TimeRange: &TimeRange{
			Start: now.Add(-3 * time.Hour),
			End:   now.Add(time.Hour), // Future to include all
		},
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Should get 2 recent findings (not the 48-hour-old one)
	if len(results) != 2 {
		t.Errorf("Expected 2 recent findings, got %d", len(results))
	}
}

// TestDefaultConfig tests default configuration
func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.LocalCacheDir == "" {
		t.Error("LocalCacheDir should have a default")
	}

	if config.MaxCacheSizeMB <= 0 {
		t.Error("MaxCacheSizeMB should be positive")
	}

	if config.RetentionDays <= 0 {
		t.Error("RetentionDays should be positive")
	}

	if !config.EnableSemanticIndex {
		t.Error("EnableSemanticIndex should be true by default")
	}
}

// TestNoOpIndex tests the no-op index
func TestNoOpIndex(t *testing.T) {
	index := NewNoOpIndex()
	ctx := context.Background()

	finding := Finding{
		FindingID: "test-id",
		ToolName:  "test",
	}

	// All operations should succeed but do nothing
	if err := index.Index(ctx, finding); err != nil {
		t.Errorf("NoOpIndex.Index should not error: %v", err)
	}

	results, err := index.SemanticSearch(ctx, "query", 10)
	if err != nil {
		t.Errorf("NoOpIndex.SemanticSearch should not error: %v", err)
	}

	if len(results) != 0 {
		t.Error("NoOpIndex should return empty results")
	}

	if err := index.Delete(ctx, "test-id"); err != nil {
		t.Errorf("NoOpIndex.Delete should not error: %v", err)
	}

	if err := index.Close(); err != nil {
		t.Errorf("NoOpIndex.Close should not error: %v", err)
	}
}

// BenchmarkCacheWrite benchmarks cache write operations
func BenchmarkCacheWrite(b *testing.B) {
	tmpDir := b.TempDir()
	config := Config{LocalCacheDir: tmpDir}
	cache, err := NewFileCache(config)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()

	finding := Finding{
		FindingID:      uuid.New().String(),
		ToolName:       "benchmark",
		Timestamp:      time.Now(),
		AgentID:        "bench-agent",
		ContextSummary: "Benchmark finding",
		Metadata:       FindingMetadata{Success: true},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		finding.FindingID = uuid.New().String()
		if err := cache.Write(ctx, finding); err != nil {
			b.Fatalf("Write failed: %v", err)
		}
	}
}
