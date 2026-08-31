package compaction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// Test helpers

func makeMessageWithToolResults(toolResults []conversation.ToolResult) *conversation.Message {
	return &conversation.Message{
		Role:        conversation.RoleAssistant,
		Content:     "Processing...",
		ToolResults: toolResults,
		Metadata:    make(map[string]any),
	}
}

func makeToolResult(toolName, callID, output string) conversation.ToolResult {
	return conversation.ToolResult{
		CallID: callID,
		Name:   toolName,
		Output: output,
	}
}

// TestMicroCompaction_SingleTool tests compaction with a single tool type.
// Verifies: Last 3 preserved, older ones compacted (Claude Code behavior)
func TestMicroCompaction_SingleTool(t *testing.T) {
	mc := NewMicroCompactor()

	// Create 5 messages with Read tool results
	messages := []*conversation.Message{
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-1", strings.Repeat("x", 5000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-2", strings.Repeat("x", 5000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-3", strings.Repeat("x", 5000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-4", strings.Repeat("x", 5000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-5", strings.Repeat("x", 5000)),
		}),
	}

	compacted, saved := mc.Process(messages)

	// Verify results
	if len(compacted) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(compacted))
	}

	// First 2 should be compacted, last 3 preserved
	if !strings.Contains(compacted[0].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected first tool result to be compacted")
	}
	if !strings.Contains(compacted[1].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected second tool result to be compacted")
	}
	if strings.Contains(compacted[2].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected third tool result to be preserved")
	}
	if strings.Contains(compacted[3].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected fourth tool result to be preserved")
	}
	if strings.Contains(compacted[4].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected fifth tool result to be preserved")
	}

	// Check metadata
	if !IsMessageCompacted(compacted[0]) {
		t.Error("Expected first message to have compacted metadata")
	}
	if !IsMessageCompacted(compacted[1]) {
		t.Error("Expected second message to have compacted metadata")
	}
	if IsMessageCompacted(compacted[2]) {
		t.Error("Expected third message NOT to have compacted metadata")
	}

	// Verify token savings
	if saved == 0 {
		t.Error("Expected token savings")
	}

	t.Logf("✓ Tokens saved: %d", saved)
	t.Logf("✓ Stats: %+v", mc.GetStats())
}

// TestMicroCompaction_MixedTools tests compaction with multiple tool types.
// Each tool type has independent counter (Claude Code behavior)
func TestMicroCompaction_MixedTools(t *testing.T) {
	mc := NewMicroCompactor()

	messages := []*conversation.Message{
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-1", strings.Repeat("x", 5000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Bash", "call-2", strings.Repeat("x", 1000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-3", strings.Repeat("x", 5000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Grep", "call-4", strings.Repeat("x", 2000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-5", strings.Repeat("x", 5000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Bash", "call-6", strings.Repeat("x", 1000)),
		}),
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-7", strings.Repeat("x", 5000)),
		}),
	}

	compacted, _ := mc.Process(messages)

	// Each tool type has its own counter
	// Read: positions 0,2,4,6 → keep last 3 (2,4,6), compact 0
	// Bash: positions 1,5 → keep both (< 3)
	// Grep: position 3 → keep (< 3)

	if !strings.Contains(compacted[0].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected first Read (oldest) to be compacted")
	}
	if strings.Contains(compacted[1].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected first Bash to be preserved")
	}
	if strings.Contains(compacted[2].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected second Read to be preserved")
	}
	if strings.Contains(compacted[3].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected Grep to be preserved")
	}

	t.Logf("✓ Mixed tools handled correctly with independent counters")
}

// TestMicroCompaction_MultipleResultsPerMessage tests messages with multiple tool results
func TestMicroCompaction_MultipleResultsPerMessage(t *testing.T) {
	mc := NewMicroCompactor()

	// Single message with 5 Read results
	messages := []*conversation.Message{
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Read", "call-1", strings.Repeat("x", 5000)),
			makeToolResult("Read", "call-2", strings.Repeat("x", 5000)),
			makeToolResult("Read", "call-3", strings.Repeat("x", 5000)),
			makeToolResult("Read", "call-4", strings.Repeat("x", 5000)),
			makeToolResult("Read", "call-5", strings.Repeat("x", 5000)),
		}),
	}

	compacted, saved := mc.Process(messages)

	msg := compacted[0]

	// Last 3 should be preserved, first 2 compacted
	if !strings.Contains(msg.ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Expected first tool result to be compacted")
	}
	if !strings.Contains(msg.ToolResults[1].Output, "[Tool result compacted") {
		t.Error("Expected second tool result to be compacted")
	}
	if strings.Contains(msg.ToolResults[2].Output, "[Tool result compacted") {
		t.Error("Expected third tool result to be preserved")
	}
	if strings.Contains(msg.ToolResults[3].Output, "[Tool result compacted") {
		t.Error("Expected fourth tool result to be preserved")
	}
	if strings.Contains(msg.ToolResults[4].Output, "[Tool result compacted") {
		t.Error("Expected fifth tool result to be preserved")
	}

	if saved == 0 {
		t.Error("Expected token savings")
	}

	// Check we have 2 compaction markers in metadata
	compactedCount := GetCompactedCount(msg)
	if compactedCount != 2 {
		t.Errorf("Expected 2 compacted results, got %d", compactedCount)
	}

	t.Logf("✓ Multiple results per message handled correctly")
}

// TestMicroCompaction_ExcludedTools tests that non-heavy tools are not compacted
func TestMicroCompaction_ExcludedTools(t *testing.T) {
	mc := NewMicroCompactor()

	messages := []*conversation.Message{
		makeMessageWithToolResults([]conversation.ToolResult{
			makeToolResult("Task", "call-1", strings.Repeat("x", 5000)),
			makeToolResult("BackgroundTask", "call-2", strings.Repeat("x", 5000)),
			makeToolResult("ApplyPatch", "call-3", strings.Repeat("x", 5000)),
		}),
	}

	compacted, saved := mc.Process(messages)

	// None should be compacted
	msg := compacted[0]
	for i, result := range msg.ToolResults {
		if strings.Contains(result.Output, "[Tool result compacted") {
			t.Errorf("Tool result %d should not be compacted (tool: %s)", i, result.Name)
		}
	}

	if saved != 0 {
		t.Errorf("Expected no token savings, got %d", saved)
	}

	if IsMessageCompacted(msg) {
		t.Error("Expected message NOT to have compacted metadata")
	}

	t.Logf("✓ Excluded tools not compacted")
}

// TestMicroCompaction_RetentionCount tests custom retention counts
func TestMicroCompaction_RetentionCount(t *testing.T) {
	testCases := []struct {
		retention     int
		resultCount   int
		expectCompact int
		expectKeep    int
	}{
		{retention: 1, resultCount: 5, expectCompact: 4, expectKeep: 1},
		{retention: 2, resultCount: 5, expectCompact: 3, expectKeep: 2},
		{retention: 3, resultCount: 5, expectCompact: 2, expectKeep: 3}, // Default
		{retention: 5, resultCount: 5, expectCompact: 0, expectKeep: 5},
		{retention: 10, resultCount: 5, expectCompact: 0, expectKeep: 5},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("retention_%d", tc.retention), func(t *testing.T) {
			mc := NewMicroCompactor()
			mc.SetRetentionCount(tc.retention)

			results := make([]conversation.ToolResult, tc.resultCount)
			for i := 0; i < tc.resultCount; i++ {
				results[i] = makeToolResult("Read", fmt.Sprintf("call-%d", i), strings.Repeat("x", 1000))
			}

			messages := []*conversation.Message{
				makeMessageWithToolResults(results),
			}

			compacted, _ := mc.Process(messages)

			msg := compacted[0]
			numCompacted := 0
			numKept := 0
			for _, result := range msg.ToolResults {
				if strings.Contains(result.Output, "[Tool result compacted") {
					numCompacted++
				} else {
					numKept++
				}
			}

			if numCompacted != tc.expectCompact {
				t.Errorf("Expected %d compacted, got %d", tc.expectCompact, numCompacted)
			}
			if numKept != tc.expectKeep {
				t.Errorf("Expected %d kept, got %d", tc.expectKeep, numKept)
			}

			t.Logf("✓ Retention %d: %d compacted, %d kept", tc.retention, numCompacted, numKept)
		})
	}
}

// TestMicroCompaction_Statistics tests stats tracking
func TestMicroCompaction_Statistics(t *testing.T) {
	mc := NewMicroCompactor()

	results := []conversation.ToolResult{
		makeToolResult("Read", "call-1", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-2", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-3", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-4", strings.Repeat("x", 5000)),
	}

	messages := []*conversation.Message{
		makeMessageWithToolResults(results),
	}

	mc.Process(messages)

	stats := mc.GetStats()

	if stats.TotalCompactions != 1 {
		t.Errorf("Expected 1 compaction run, got %d", stats.TotalCompactions)
	}
	if stats.ResultsCompacted != 1 {
		t.Errorf("Expected 1 result compacted, got %d", stats.ResultsCompacted)
	}
	if stats.TokensSaved == 0 {
		t.Error("Expected non-zero tokens saved")
	}
	if stats.LastCompaction.IsZero() {
		t.Error("Expected LastCompaction to be set")
	}

	t.Logf("✓ Statistics: %+v", stats)

	// Create fresh messages for second run (compacting already-compacted messages doesn't re-compact)
	results2 := []conversation.ToolResult{
		makeToolResult("Read", "call-5", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-6", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-7", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-8", strings.Repeat("x", 5000)),
	}
	messages2 := []*conversation.Message{
		makeMessageWithToolResults(results2),
	}

	mc.Process(messages2)
	stats2 := mc.GetStats()

	if stats2.TotalCompactions != 2 {
		t.Errorf("Expected 2 compaction runs, got %d", stats2.TotalCompactions)
	}
	if stats2.TokensSaved <= stats.TokensSaved {
		t.Error("Expected cumulative tokens saved to increase")
	}

	t.Logf("✓ Statistics accumulate correctly: %+v", stats2)
}

// TestMicroCompaction_EmptyMessages tests edge case with no messages
func TestMicroCompaction_EmptyMessages(t *testing.T) {
	mc := NewMicroCompactor()

	compacted, saved := mc.Process([]*conversation.Message{})

	if len(compacted) != 0 {
		t.Error("Expected empty result")
	}
	if saved != 0 {
		t.Error("Expected zero tokens saved")
	}

	t.Logf("✓ Empty messages handled")
}

// TestMicroCompaction_ProcessEnabled tests config-based processing
func TestMicroCompaction_ProcessEnabled(t *testing.T) {
	results := []conversation.ToolResult{
		makeToolResult("Read", "call-1", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-2", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-3", strings.Repeat("x", 5000)),
		makeToolResult("Read", "call-4", strings.Repeat("x", 5000)),
	}

	messages := []*conversation.Message{
		makeMessageWithToolResults(results),
	}

	// Disabled - should not compact
	compacted1, saved1 := ProcessEnabled(messages, false, 3)
	if saved1 != 0 {
		t.Error("Expected no compaction when disabled")
	}
	if strings.Contains(compacted1[0].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Should not compact when disabled")
	}

	// Enabled - should compact
	compacted2, saved2 := ProcessEnabled(messages, true, 3)
	if saved2 == 0 {
		t.Error("Expected compaction when enabled")
	}
	if !strings.Contains(compacted2[0].ToolResults[0].Output, "[Tool result compacted") {
		t.Error("Should compact when enabled")
	}

	t.Logf("✓ ProcessEnabled respects config")
}

// Benchmark tests
func BenchmarkMicroCompaction_100Results(b *testing.B) {
	mc := NewMicroCompactor()
	results := make([]conversation.ToolResult, 100)
	for i := range 100 {
		results[i] = makeToolResult("Read", fmt.Sprintf("call-%d", i), strings.Repeat("x", 5000))
	}

	messages := []*conversation.Message{
		makeMessageWithToolResults(results),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.Process(messages)
	}
}

func BenchmarkMicroCompaction_1000Results(b *testing.B) {
	mc := NewMicroCompactor()
	results := make([]conversation.ToolResult, 1000)
	for i := range 1000 {
		results[i] = makeToolResult("Read", fmt.Sprintf("call-%d", i), strings.Repeat("x", 5000))
	}

	messages := []*conversation.Message{
		makeMessageWithToolResults(results),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.Process(messages)
	}
}
