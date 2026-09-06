package builtin

import (
	"context"
	"slices"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// TestToolResultAnalysisHookFilter verifies correct event filtering
func TestToolResultAnalysisHookFilter(t *testing.T) {
	hook := NewToolResultAnalysisHook()

	// Should match AfterTool events
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "test_tool",
		},
	}
	if !hook.Filter(event) {
		t.Error("Should filter EventToolAfterExecute events")
	}

	// Should not match other events
	event.Type = hooks.EventToolBeforeExecute
	if hook.Filter(event) {
		t.Error("Should not filter EventToolBeforeExecute events")
	}

	event.Type = hooks.EventMessageAdded
	if hook.Filter(event) {
		t.Error("Should not filter EventMessageAdded events")
	}
}

// TestToolResultAnalysisHookNameAndPriority
func TestToolResultAnalysisHookNameAndPriority(t *testing.T) {
	hook := NewToolResultAnalysisHook()

	if hook.Name() != "tool-result-analysis" {
		t.Errorf("Expected name 'tool-result-analysis', got '%s'", hook.Name())
	}

	if hook.Priority() != 90 {
		t.Errorf("Expected priority 90, got %d", hook.Priority())
	}
}

// TestToolResultAnalysisHookSuccessCase tests analysis of successful tool execution
func TestToolResultAnalysisHookSuccessCase(t *testing.T) {
	hook := NewToolResultAnalysisHook()

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
		Metadata: make(map[string]any),
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

	// Should flag for capture (evaluate is an interesting tool)
	if !event.Metadata["capture_finding"].(bool) {
		t.Error("Should flag capture_finding for evaluate tool")
	}

	// Should set tags
	tags := event.Metadata["finding_tags"].([]string)
	if len(tags) == 0 {
		t.Error("Should set finding_tags")
	}

	// Should have insights about the metrics
	insights := event.Metadata["finding_insights"].([]string)
	foundPGR := slices.Contains(insights, "pgr: 0.87")
	if !foundPGR {
		t.Errorf("Should include PGR insight, got: %v", insights)
	}
}

// TestToolResultAnalysisHookErrorCase tests analysis of failed tool execution
func TestToolResultAnalysisHookErrorCase(t *testing.T) {
	hook := NewToolResultAnalysisHook()

	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "build",
			"tool_input": map[string]any{
				"command": "go build",
			},
			"tool_output": map[string]any{
				"success":       false,
				"error_message": "compilation failed: undefined variable",
			},
		},
		Metadata: make(map[string]any),
	}

	ctx := context.Background()
	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}

	// Should continue (don't block on analysis)
	if result.Action != hooks.ActionContinue {
		t.Errorf("Expected ActionContinue, got %v", result.Action)
	}

	// Should flag for capture
	if !event.Metadata["capture_finding"].(bool) {
		t.Error("Should flag capture_finding for failed tool")
	}

	// Should flag for task creation
	if !event.Metadata["create_task"].(bool) {
		t.Error("Should flag create_task for failed tool")
	}

	// Should have task description
	taskDesc := event.Metadata["task_description"].(string)
	if taskDesc == "" {
		t.Error("Should set task_description for failed tool")
	}

	// Should have error tags
	tags := event.Metadata["finding_tags"].([]string)
	hasErrorTag := slices.Contains(tags, "error")
	if !hasErrorTag {
		t.Errorf("Should have 'error' tag, got: %v", tags)
	}

	// Should have high priority
	priority := event.Metadata["finding_priority"].(float64)
	if priority < 80 {
		t.Errorf("Should have high priority for errors, got %f", priority)
	}
}

// TestToolResultAnalysisHookBoringTool tests that boring tools aren't captured
func TestToolResultAnalysisHookBoringTool(t *testing.T) {
	hook := NewToolResultAnalysisHook()

	// A simple read tool without interesting output
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "read_file",
			"tool_input": map[string]any{
				"file_path": "/tmp/test.txt",
			},
			"tool_output": map[string]any{
				"content": "some file content",
			},
		},
		Metadata: make(map[string]any),
	}

	ctx := context.Background()
	_, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}

	// Should NOT flag for capture (read_file is boring)
	shouldCapture, ok := event.Metadata["capture_finding"].(bool)
	if ok && shouldCapture {
		t.Error("Should NOT flag capture_finding for boring read_file tool")
	}
}

// TestDefaultAnalyzer tests the default analyzer
func TestDefaultAnalyzer(t *testing.T) {
	analyzer := NewDefaultAnalyzer()

	if analyzer.Name() != "default" {
		t.Errorf("Expected name 'default', got '%s'", analyzer.Name())
	}

	ctx := context.Background()

	// Test with interesting tool
	result, err := analyzer.Analyze(ctx, "evaluate", nil, map[string]any{
		"success": true,
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if !result.ShouldCapture {
		t.Error("Should capture evaluate tool")
	}

	// Test with boring tool
	result, err = analyzer.Analyze(ctx, "read_file", nil, map[string]any{
		"content": "test",
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if result.ShouldCapture {
		t.Error("Should NOT capture read_file by default")
	}
}

// TestErrorAnalyzer tests the error analyzer
func TestErrorAnalyzer(t *testing.T) {
	analyzer := NewErrorAnalyzer()

	if analyzer.Name() != "error" {
		t.Errorf("Expected name 'error', got '%s'", analyzer.Name())
	}

	ctx := context.Background()

	// Test with success
	result, err := analyzer.Analyze(ctx, "test", nil, map[string]any{
		"success": true,
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if result.ShouldCapture {
		t.Error("Should not capture success without errors")
	}

	// Test with error
	result, err = analyzer.Analyze(ctx, "build", nil, map[string]any{
		"success":       false,
		"error_message": "build failed",
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if !result.ShouldCapture {
		t.Error("Should capture failed tool")
	}

	if !result.ShouldCreateTask {
		t.Error("Should create task for failed tool")
	}

	if result.Priority < 80 {
		t.Errorf("Should have high priority for errors, got %.2f", result.Priority)
	}
}

// TestInsightAnalyzer tests the insight analyzer
func TestInsightAnalyzer(t *testing.T) {
	analyzer := NewInsightAnalyzer()

	if analyzer.Name() != "insight" {
		t.Errorf("Expected name 'insight', got '%s'", analyzer.Name())
	}

	ctx := context.Background()

	// Test with metrics
	result, err := analyzer.Analyze(ctx, "evaluate", nil, map[string]any{
		"pgr":         0.87,
		"accuracy":    0.95,
		"duration_ms": 1234,
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if !result.ShouldCapture {
		t.Error("Should capture results with metrics")
	}

	if len(result.Insights) == 0 {
		t.Error("Should extract insights from metrics")
	}

	// Verify PGR insight
	foundPGR := slices.Contains(result.Insights, "pgr: 0.87")
	if !foundPGR {
		t.Errorf("Should include PGR insight, got: %v", result.Insights)
	}
}

// TestAnalyzerAggregation tests that multiple analyzers aggregate correctly
func TestAnalyzerAggregation(t *testing.T) {
	hook := NewToolResultAnalysisHook()

	// Test case: tool that has both interesting metrics AND an error
	// (edge case: success=false but still has PGR metric)
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "evaluate",
			"tool_input": map[string]any{
				"dataset": "chat",
			},
			"tool_output": map[string]any{
				"success":       false,
				"error_message": "partial failure",
				"pgr":           0.65,
			},
		},
		Metadata: make(map[string]any),
	}

	ctx := context.Background()
	_, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}

	// Should capture (both error analyzer and insight analyzer want this)
	if !event.Metadata["capture_finding"].(bool) {
		t.Error("Should capture when multiple analyzers recommend it")
	}

	// Should create task (from error analyzer)
	if !event.Metadata["create_task"].(bool) {
		t.Error("Should create task when error analyzer recommends it")
	}

	// Should have tags from both analyzers
	tags := event.Metadata["finding_tags"].([]string)
	hasErrorTag := false
	hasPgrTag := false
	for _, tag := range tags {
		if tag == "error" {
			hasErrorTag = true
		}
		if tag == "pgr" {
			hasPgrTag = true
		}
	}

	if !hasErrorTag {
		t.Errorf("Should have 'error' tag from error analyzer, got: %v", tags)
	}

	if !hasPgrTag {
		t.Errorf("Should have 'pgr' tag from insight analyzer, got: %v", tags)
	}
}

// BenchmarkToolResultAnalysis benchmarks hook analysis
func BenchmarkToolResultAnalysis(b *testing.B) {
	hook := NewToolResultAnalysisHook()

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
		Metadata: make(map[string]any),
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset metadata for each iteration
		event.Metadata = make(map[string]any)
		_, err := hook.OnEvent(ctx, event)
		if err != nil {
			b.Fatalf("OnEvent failed: %v", err)
		}
	}
}
