package builtin

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// MockLogger is a simple logger for testing
type MockLogger struct {
	logs []string
}

func (m *MockLogger) Info(ctx context.Context, msg string, fields ...observability.Field) {
	m.logs = append(m.logs, "INFO: "+msg)
}

func (m *MockLogger) Debug(ctx context.Context, msg string, fields ...observability.Field) {
	m.logs = append(m.logs, "DEBUG: "+msg)
}

func (m *MockLogger) Warn(ctx context.Context, msg string, fields ...observability.Field) {
	m.logs = append(m.logs, "WARN: "+msg)
}

func (m *MockLogger) Error(ctx context.Context, msg string, fields ...observability.Field) {
	m.logs = append(m.logs, "ERROR: "+msg)
}

func (m *MockLogger) Trace(ctx context.Context, msg string, fields ...observability.Field) {
	m.logs = append(m.logs, "TRACE: "+msg)
}

func (m *MockLogger) Log(ctx context.Context, level observability.Level, msg string, fields ...observability.Field) {
	m.logs = append(m.logs, msg)
}

// TestLocalFindingsHookFilter tests the event filtering logic
func TestLocalFindingsHookFilter(t *testing.T) {
	// Create hook with nil cache (we'll test filtering only)
	hook, err := NewLocalFindingsHook(nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}
	defer hook.Close()

	// Should match AfterTool events with capture_finding=true
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "test",
		},
		Metadata: map[string]any{
			"capture_finding": true,
		},
	}
	if !hook.Filter(event) {
		t.Error("Should filter EventToolAfterExecute with capture_finding=true")
	}

	// Should NOT match if capture_finding is false
	event.Metadata["capture_finding"] = false
	if hook.Filter(event) {
		t.Error("Should NOT filter when capture_finding=false")
	}

	// Should NOT match if capture_finding is missing AND tool is not in autoCapture list
	hook.SetAutoCapture([]string{"only_this_tool"})
	event.Metadata = nil
	if hook.Filter(event) {
		t.Error("Should NOT filter when metadata is nil and tool not in autoCapture list")
	}
	// Reset autoCapture for remaining tests
	hook.SetAutoCapture(nil)

	// Should NOT match other event types
	event.Type = hooks.EventToolBeforeExecute
	event.Metadata = map[string]any{"capture_finding": true}
	if hook.Filter(event) {
		t.Error("Should NOT filter EventToolBeforeExecute")
	}
}

// TestLocalFindingsHookNameAndPriority
func TestLocalFindingsHookNameAndPriority(t *testing.T) {
	hook, err := NewLocalFindingsHook(nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}
	defer hook.Close()

	if hook.Name() != "local-findings" {
		t.Errorf("Expected name 'local-findings', got '%s'", hook.Name())
	}

	if hook.Priority() != 85 {
		t.Errorf("Expected priority 85, got %d", hook.Priority())
	}
}

// TestLocalFindingsHookCapture tests actual finding capture
func TestLocalFindingsHookCapture(t *testing.T) {
	tmpDir := t.TempDir()
	config := findings.Config{LocalCacheDir: tmpDir}
	cache, err := findings.NewFileCache(config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	logger := &MockLogger{}
	hook, err := NewLocalFindingsHook(cache, nil, logger)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}
	defer hook.Close()

	// Wait for directory creation
	time.Sleep(50 * time.Millisecond)

	// Create event with analysis metadata
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "evaluate",
			"tool_input": map[string]any{
				"dataset": "chat",
			},
			"tool_output": map[string]any{
				"success":  true,
				"accuracy": 0.95,
				"pgr":      0.87,
			},
		},
		Metadata: map[string]any{
			"capture_finding":  true,
			"finding_tags":     []string{"w2s", "evaluation"},
			"finding_priority": 75,
		},
		AgentID:        "test-agent-123",
		ConversationID: "conv-456",
	}

	ctx := context.Background()
	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}

	// Should continue
	if result.Action != hooks.ActionContinue {
		t.Errorf("Expected ActionContinue, got %v", result.Action)
	}

	// Wait for async write
	time.Sleep(200 * time.Millisecond)

	// Verify finding was written to cache
	// List all findings
	list, err := cache.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("Failed to list findings: %v", err)
	}

	if len(list) != 1 {
		t.Errorf("Expected 1 finding in cache, got %d", len(list))
	}

	if len(list) == 1 {
		finding := list[0]

		if finding.ToolName != "evaluate" {
			t.Errorf("Expected tool name 'evaluate', got '%s'", finding.ToolName)
		}

		if finding.AgentID != "test-agent-123" {
			t.Errorf("Expected agent ID 'test-agent-123', got '%s'", finding.AgentID)
		}

		// Check tags
		if len(finding.Tags) == 0 {
			t.Error("Expected tags to be set")
		}

		hasW2STag := slices.Contains(finding.Tags, "w2s")
		if !hasW2STag {
			t.Errorf("Expected 'w2s' tag, got: %v", finding.Tags)
		}

		// Check priority
		if finding.Metadata.Priority != 75.0 {
			t.Errorf("Expected priority 75.0, got %f", finding.Metadata.Priority)
		}

		// Check success
		if !finding.Metadata.Success {
			t.Error("Expected success=true")
		}
	}

	// Check log was written
	foundLog := slices.Contains(logger.logs, "INFO: Captured finding")
	if !foundLog {
		t.Errorf("Expected log message 'Captured finding', got: %v", logger.logs)
	}
}

// TestLocalFindingsHookContextSummary tests context summary generation
func TestLocalFindingsHookContextSummary(t *testing.T) {
	tmpDir := t.TempDir()
	config := findings.Config{LocalCacheDir: tmpDir}
	cache, err := findings.NewFileCache(config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	hook, err := NewLocalFindingsHook(cache, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}
	defer hook.Close()

	time.Sleep(50 * time.Millisecond)

	// Test with command input
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "execute_shell",
			"tool_input": map[string]any{
				"command": "go test ./...",
			},
			"tool_output": map[string]any{
				"success": true,
			},
		},
		Metadata: map[string]any{
			"capture_finding": true,
		},
	}

	ctx := context.Background()
	_, err = hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	list, err := cache.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("Failed to list: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("Expected 1 finding, got %d", len(list))
	}

	// Check context summary includes command
	if list[0].ContextSummary == "" {
		t.Error("ContextSummary should not be empty")
	}

	// Test with file path input
	event.Data["tool_name"] = "read_file"
	event.Data["tool_input"] = map[string]any{
		"file_path": "/path/to/config.yaml",
	}
	event.Data["tool_output"] = map[string]any{
		"content": "some content",
	}

	_, err = hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	list, err = cache.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("Failed to list: %v", err)
	}

	if len(list) != 2 {
		t.Errorf("Expected 2 findings, got %d", len(list))
	}
}

// TestLocalFindingsHookErrorHandling tests error cases
func TestLocalFindingsHookErrorHandling(t *testing.T) {
	// Test 1: Double-close should not panic
	tmpDir := t.TempDir()
	config := findings.Config{LocalCacheDir: tmpDir}
	cache, err := findings.NewFileCache(config)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	logger := &MockLogger{}
	hook, err := NewLocalFindingsHook(cache, nil, logger)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}

	// Close the cache first, then close the hook (which closes cache again).
	// This must not panic thanks to sync.Once guard.
	cache.Close()
	hook.Close()

	// Test 2: OnEvent with nil cache should fail-open
	hook2, err := NewLocalFindingsHook(&errorCache{}, nil, logger)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}
	defer hook2.Close()

	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name":   "test",
			"tool_output": map[string]any{"success": true},
		},
		Metadata: map[string]any{
			"capture_finding": true,
		},
	}

	ctx := context.Background()
	result, err := hook2.OnEvent(ctx, event)

	// Should NOT return error (fail-open), but should include message
	if err != nil {
		t.Errorf("Should fail-open, but got error: %v", err)
	}

	if result.Action != hooks.ActionContinue {
		t.Errorf("Expected ActionContinue even on error, got %v", result.Action)
	}

	// Should have error message from the failing cache
	if result.Message == "" {
		t.Error("Should include error message in result")
	}
}

// errorCache is a Cache implementation that always returns errors on Write.
type errorCache struct{}

func (e *errorCache) Write(_ context.Context, _ findings.Finding) error {
	return fmt.Errorf("simulated cache error")
}
func (e *errorCache) Read(_ context.Context, _ string) (findings.Finding, error) {
	return findings.Finding{}, fmt.Errorf("simulated cache error")
}
func (e *errorCache) Query(_ context.Context, _ findings.FindingQuery) ([]findings.FindingResult, error) {
	return nil, fmt.Errorf("simulated cache error")
}
func (e *errorCache) List(_ context.Context, _, _ int) ([]findings.Finding, error) {
	return nil, fmt.Errorf("simulated cache error")
}
func (e *errorCache) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("simulated cache error")
}
func (e *errorCache) Close() error { return nil }

// TestLocalFindingsHookTagExtraction tests tag extraction from metadata
func TestLocalFindingsHookTagExtraction(t *testing.T) {
	hook, err := NewLocalFindingsHook(nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}
	defer hook.Close()

	// Test with no tags
	tags := hook.extractTags(nil)
	if len(tags) != 1 || tags[0] != "auto-captured" {
		t.Errorf("Expected [auto-captured] for nil metadata, got: %v", tags)
	}

	// Test with provided tags
	metadata := map[string]any{
		"finding_tags": []string{"w2s", "evaluation"},
	}
	tags = hook.extractTags(metadata)

	// Should have provided tags + auto-captured
	hasW2S := false
	hasAuto := false
	for _, tag := range tags {
		if tag == "w2s" {
			hasW2S = true
		}
		if tag == "auto-captured" {
			hasAuto = true
		}
	}

	if !hasW2S {
		t.Errorf("Expected 'w2s' tag, got: %v", tags)
	}

	if !hasAuto {
		t.Errorf("Expected 'auto-captured' tag, got: %v", tags)
	}
}

// TestLocalFindingsHookPriorityExtraction tests priority extraction
func TestLocalFindingsHookPriorityExtraction(t *testing.T) {
	hook, err := NewLocalFindingsHook(nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}
	defer hook.Close()

	// Test default priority
	priority := hook.extractPriority(nil)
	if priority != 50.0 {
		t.Errorf("Expected default priority 50.0, got %f", priority)
	}

	// Test float64 priority
	metadata := map[string]any{
		"finding_priority": 75.5,
	}
	priority = hook.extractPriority(metadata)
	if priority != 75.5 {
		t.Errorf("Expected priority 75.5, got %f", priority)
	}

	// Test int priority (backward compatibility)
	metadata = map[string]any{
		"finding_priority": 80,
	}
	priority = hook.extractPriority(metadata)
	if priority != 80.0 {
		t.Errorf("Expected priority 80.0, got %f", priority)
	}
}

// TestLocalFindingsHookSetPriority tests priority adjustment
func TestLocalFindingsHookSetPriority(t *testing.T) {
	hook, err := NewLocalFindingsHook(nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create hook: %v", err)
	}
	defer hook.Close()

	if hook.Priority() != 85 {
		t.Errorf("Expected initial priority 85, got %d", hook.Priority())
	}

	hook.SetPriority(88)
	if hook.Priority() != 88 {
		t.Errorf("Expected adjusted priority 88, got %d", hook.Priority())
	}
}

// BenchmarkLocalFindingsHook benchmarks hook execution
func BenchmarkLocalFindingsHook(b *testing.B) {
	tmpDir := b.TempDir()
	config := findings.Config{LocalCacheDir: tmpDir}
	cache, err := findings.NewFileCache(config)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}

	hook, err := NewLocalFindingsHook(cache, nil, nil)
	if err != nil {
		b.Fatalf("Failed to create hook: %v", err)
	}
	defer hook.Close()

	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "evaluate",
			"tool_input": map[string]any{
				"dataset": "chat",
			},
			"tool_output": map[string]any{
				"success": true,
				"pgr":     0.87,
			},
		},
		Metadata: map[string]any{
			"capture_finding": true,
		},
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := hook.OnEvent(ctx, event)
		if err != nil {
			b.Fatalf("OnEvent failed: %v", err)
		}
	}
}
