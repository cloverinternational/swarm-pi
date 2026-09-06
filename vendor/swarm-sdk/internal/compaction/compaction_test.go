package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestDirectSummarization verifies single-pass summarization works correctly
func TestDirectSummarization(t *testing.T) {
	messages := make([]*conversation.Message, 50)
	for i := range 50 {
		messages[i] = &conversation.Message{
			ID:      fmt.Sprintf("msg_%d", i+1),
			Role:    conversation.RoleUser,
			Content: fmt.Sprintf("Message %d content with some detail about what the user did", i+1),
		}
	}

	callCount := 0
	service := &Service{
		config: CompactionConfig{
			ContextLimit: 200000,
			SummarizeFunc: func(ctx context.Context, msgs []*conversation.Message, prompt string) (string, error) {
				callCount++
				// Return a valid summary with required sections
				return `1. **Primary Request and Intent**: The user asked to fix the login bug.

2. **Key Technical Concepts**: Go, REST API, JWT tokens.

3. **Files and Code Sections**: src/auth/login.go modified.

4. **Errors and Fixes**: Fixed nil pointer in auth handler.

5. **Current State**: Login flow works, tests passing.

6. **User Feedback**: User confirmed the fix works.

7. **Next Steps**: Deploy to staging.`, nil
			},
		},
		fileAccess: make(map[string]*FileAccessRecord),
	}

	conv := &conversation.Conversation{
		ID:                 "test",
		CurrentContextSize: 150000,
		Messages:           messages,
	}

	result, err := service.CompactWithContext(context.Background(), conv, true, nil)
	if err != nil {
		t.Fatalf("CompactWithContext failed: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Compaction returned error: %v", result.Error)
	}
	if !result.Compacted {
		t.Fatal("Compaction did not complete")
	}
	if result.Summary == "" {
		t.Fatal("Summary is empty")
	}
	if callCount != 1 {
		t.Errorf("Expected exactly 1 LLM call (single-pass), got %d", callCount)
	}

	t.Logf("OK: single-pass summarization, 1 LLM call for %d messages", len(messages))
}

// TestFormatCompactSummary verifies FormatCompactSummary strips <analysis> tags
// and extracts <summary> content — matching Claude Code's formatCompactSummary().

// TestCompactWithContextDoesNotCompoundPriorSummary verifies Fix 2: running
// CompactWithContext a second time on a conversation whose active messages
// already include a prior compaction-generated handoff bundle (summary +
// restored files) does NOT feed that entire bundle back into the summarizer
// verbatim. This mirrors Codex CLI's is_summary_message() anti-compounding
// filter (codex-rs/core/src/compact.rs) -- otherwise compaction #2 would
// re-summarize compaction #1's already-lossy summary as if it were fresh
// conversation prose, compounding information loss on every pass.
func TestCompactWithContextDoesNotCompoundPriorSummary(t *testing.T) {
	const firstSummaryMarker = "UNIQUE-FIRST-SUMMARY-MARKER-Fixed-the-login-bug-with-JWT-refresh-token-rotation"
	firstSummaryBody := "1. **Primary Request**: " + firstSummaryMarker + " " + strings.Repeat("detail ", 200) +
		"\n2. **Key Technical Concepts**: Go, JWT.\n3. **Files and Code Sections**: auth.go.\n" +
		"7. **Pending Tasks**: none.\n8. **Current Work**: " + strings.Repeat("more detail ", 200)
	var capturedInputs [][]*conversation.Message
	service := NewService(CompactionConfig{
		ContextLimit: 200_000,
		SummarizeFunc: func(_ context.Context, msgs []*conversation.Message, _ string) (string, error) {
			capturedInputs = append(capturedInputs, msgs)
			if len(capturedInputs) == 1 {
				return firstSummaryBody, nil
			}
			return "1. **Primary Request**: Continue the follow-up work.\n2. **Key Technical Concepts**: Go.\n" +
				"3. **Files and Code Sections**: auth.go.\n7. **Pending Tasks**: none.\n8. **Current Work**: done.", nil
		},
	})
	conv := &conversation.Conversation{
		ID:                 "anti-compound-test",
		CurrentContextSize: 10_000,
		Messages: []*conversation.Message{
			{ID: "u1", Role: conversation.RoleUser, Content: "Please fix the login bug."},
			{ID: "a1", Role: conversation.RoleAssistant, Content: "I rotated the JWT refresh tokens."},
		},
	}
	compCtx := &CompactionContext{Strategy: StrategyStandard}
	// ── First compaction pass ──────────────────────────────────────────
	result1, err := service.CompactWithContext(context.Background(), conv, true, compCtx)
	if err != nil {
		t.Fatalf("first CompactWithContext failed: %v", err)
	}
	if !result1.Compacted || result1.Error != nil {
		t.Fatalf("first compaction did not succeed: %+v", result1)
	}
	if !strings.Contains(result1.Summary, firstSummaryMarker) {
		t.Fatalf("first summary missing marker: %s", result1.Summary)
	}
	preCompactionActive := conv.ActiveMessages()
	compacted1 := service.BuildCompactedMessagesWithContext(result1, compCtx, preCompactionActive...)
	if len(compacted1) == 0 {
		t.Fatal("first compaction produced no active messages")
	}
	foundGenerated := false
	for _, msg := range compacted1 {
		if generated, _ := msg.Metadata[conversation.CompactionGeneratedMetadataKey].(bool); generated {
			foundGenerated = true
		}
	}
	if !foundGenerated {
		t.Fatal("compacted messages missing compaction_generated marker; test setup invalid")
	}
	// Advance the conversation's active generation to the compacted bundle,
	// then append a couple of new turns -- simulating the agent continuing to
	// work after the first compaction.
	conv.AdvanceActiveContext(compacted1, result1.Summary, result1.CompactedTokens)
	conv.AddMessage(&conversation.Message{ID: "u2", Role: conversation.RoleUser, Content: "Now add rate limiting too."})
	conv.AddMessage(&conversation.Message{ID: "a2", Role: conversation.RoleAssistant, Content: "Added a token bucket limiter."})
	// ── Second compaction pass ─────────────────────────────────────────
	result2, err := service.CompactWithContext(context.Background(), conv, true, compCtx)
	if err != nil {
		t.Fatalf("second CompactWithContext failed: %v", err)
	}
	if !result2.Compacted || result2.Error != nil {
		t.Fatalf("second compaction did not succeed: %+v", result2)
	}
	if len(capturedInputs) != 2 {
		t.Fatalf("expected exactly 2 summarizer calls, got %d", len(capturedInputs))
	}
	secondInput := capturedInputs[1]
	// The core assertion: the second call's summarization input must NOT
	// contain the full verbatim text of the first compaction's synthetic
	// summary/restored-files blob.
	for _, msg := range secondInput {
		if msg == nil {
			continue
		}
		if strings.Contains(msg.Content, firstSummaryBody) {
			t.Fatalf("second summarization input re-fed the first summary verbatim in full: %q", msg.Content)
		}
	}
	// It also must not contain any message still tagged as compaction-generated
	// from the first pass -- those are excluded structurally, not merely
	// truncated.
	for _, msg := range secondInput {
		if isCompactionGeneratedMessage(msg) {
			t.Fatalf("second summarization input still contains a compaction_generated message: %q", msg.Content)
		}
	}
	// The genuinely new user/assistant turns from after the first compaction
	// must still be present -- the filter should not eat real conversation.
	foundFollowUp := false
	for _, msg := range secondInput {
		if strings.Contains(msg.Content, "rate limiting") {
			foundFollowUp = true
		}
	}
	if !foundFollowUp {
		t.Fatalf("second summarization input dropped genuine follow-up conversation: %+v", secondInput)
	}
	t.Logf("OK: second compaction pass excluded %d-char first summary from re-summarization, second input had %d messages",
		len(firstSummaryBody), len(secondInput))
}


// progressCall records one ProgressFunc invocation for TestCompactWithContextReportsProgressStagesInOrder.
type progressCall struct {
	stage string
	pct   float64
}

// TestCompactWithContextReportsProgressStagesInOrder verifies Fix 3: a
// configured ProgressFunc receives stage callbacks in a sane order with
// monotonically non-decreasing pct, ending at ("done", 1.0).
func TestCompactWithContextReportsProgressStagesInOrder(t *testing.T) {
	var calls []progressCall
	service := NewService(CompactionConfig{
		ContextLimit: 200_000,
		SummarizeFunc: func(context.Context, []*conversation.Message, string) (string, error) {
			return "1. **Primary Request**: Fix the bug.\n2. **Key Technical Concepts**: Go.\n" +
				"3. **Files and Code Sections**: main.go.\n7. **Pending Tasks**: none.\n8. **Current Work**: done.", nil
		},
		ProgressFunc: func(stage string, pct float64) {
			calls = append(calls, progressCall{stage: stage, pct: pct})
		},
	})
	conv := &conversation.Conversation{
		ID:                 "progress-test",
		CurrentContextSize: 5_000,
		Messages: []*conversation.Message{
			{ID: "u1", Role: conversation.RoleUser, Content: "Please fix the login bug."},
			{ID: "a1", Role: conversation.RoleAssistant, Content: "Fixed it."},
		},
	}
	result, err := service.CompactWithContext(context.Background(), conv, true, &CompactionContext{Strategy: StrategyStandard})
	if err != nil {
		t.Fatalf("CompactWithContext failed: %v", err)
	}
	if !result.Compacted || result.Error != nil {
		t.Fatalf("compaction did not succeed: %+v", result)
	}
	if len(calls) == 0 {
		t.Fatal("ProgressFunc was never called")
	}
	wantStages := []string{"micro_compaction", "summarizing", "verifying", "recovering_files", "done"}
	if len(calls) != len(wantStages) {
		t.Fatalf("got %d progress calls %+v, want %d stages %v", len(calls), calls, len(wantStages), wantStages)
	}
	lastPct := -1.0
	for i, call := range calls {
		if call.stage != wantStages[i] {
			t.Errorf("call %d: stage = %q, want %q", i, call.stage, wantStages[i])
		}
		if call.pct < lastPct {
			t.Errorf("call %d: pct = %v regressed below previous pct %v (non-monotonic)", i, call.pct, lastPct)
		}
		if call.pct < 0 || call.pct > 1.0 {
			t.Errorf("call %d: pct = %v out of [0,1] range", i, call.pct)
		}
		lastPct = call.pct
	}
	final := calls[len(calls)-1]
	if final.stage != "done" || final.pct != 1.0 {
		t.Fatalf("final progress call = %+v, want {done 1.0}", final)
	}
}

// TestSummarizeBoundedReportsPerChunkProgress verifies Fix 3's chunked-path
// progress reporting: when summarizeBounded must split input into multiple
// chunks, it reports a "summarizing_chunk_i_of_n" stage per chunk with pct
// interpolated between 0.15 and 0.70, in increasing order.
func TestSummarizeBoundedReportsPerChunkProgress(t *testing.T) {
	var calls []progressCall
	service := NewService(CompactionConfig{
		ContextLimit:     50_000,
		SummaryMaxTokens: 4_000,
		SummarizeFunc: func(_ context.Context, messages []*conversation.Message, prompt string) (string, error) {
			return boundedTestSummary, nil
		},
		ProgressFunc: func(stage string, pct float64) {
			calls = append(calls, progressCall{stage: stage, pct: pct})
		},
	})
	messages := []*conversation.Message{
		{ID: "one", Role: conversation.RoleUser, Content: strings.Repeat("a", 60_000)},
		{ID: "two", Role: conversation.RoleAssistant, Content: strings.Repeat("b", 60_000)},
		{ID: "three", Role: conversation.RoleUser, Content: strings.Repeat("c", 60_000)},
	}
	_, method, _, err := service.summarizeBounded(
		context.Background(),
		messages,
		"Create the final passive summary.",
		&CompactionContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if method != "chunked" {
		t.Fatalf("method = %q, want chunked", method)
	}
	if len(calls) == 0 {
		t.Fatal("expected per-chunk progress calls, got none")
	}
	lastPct := -1.0
	for i, call := range calls {
		if !strings.HasPrefix(call.stage, "summarizing_chunk_") {
			t.Errorf("call %d: stage = %q, want summarizing_chunk_ prefix", i, call.stage)
		}
		if call.pct < 0.15 || call.pct > 0.70 {
			t.Errorf("call %d: pct = %v outside [0.15, 0.70] interpolation range", i, call.pct)
		}
		if call.pct < lastPct {
			t.Errorf("call %d: pct = %v regressed below previous pct %v", i, call.pct, lastPct)
		}
		lastPct = call.pct
	}
}

func TestFormatCompactSummary(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "strips single analysis block",
			input:    "Before <analysis>thinking here</analysis> After",
			expected: "Before  After",
		},
		{
			name:     "strips multiple analysis blocks",
			input:    "<analysis>first</analysis>Middle<analysis>second</analysis>End",
			expected: "MiddleEnd",
		},
		{
			name:     "no analysis tags unchanged",
			input:    "Just a regular summary with Primary Request and Current Work",
			expected: "Just a regular summary with Primary Request and Current Work",
		},
		{
			name:     "multiline analysis block",
			input:    "Start\n<analysis>\nLine 1\nLine 2\n</analysis>\nEnd",
			expected: "Start\n\nEnd",
		},
		{
			name:     "extracts summary block content",
			input:    "<analysis>CoT scratchpad</analysis>\n<summary>\n1. Primary Request\n2. Key Technical\n</summary>",
			expected: "Summary:\n1. Primary Request\n2. Key Technical",
		},
		{
			name:     "summary block without analysis",
			input:    "<summary>\nSection 1\nSection 2\n</summary>",
			expected: "Summary:\nSection 1\nSection 2",
		},
		{
			name:     "no tags — raw output preserved",
			input:    "1. Primary Request and Intent:\n   Fix the bug\n8. Current Work:\n   Debugging",
			expected: "1. Primary Request and Intent:\n   Fix the bug\n8. Current Work:\n   Debugging",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatCompactSummary(tt.input)
			if got != tt.expected {
				t.Errorf("FormatCompactSummary(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestMicroCompactionBeforeFullCompaction verifies Stage 1 runs before Stage 2
func TestMicroCompactionBeforeFullCompaction(t *testing.T) {
	// Create messages with heavy tool results that should be micro-compacted
	messages := make([]*conversation.Message, 0, 20)

	// Add 15 Read tool results — only last 10 should survive micro-compaction
	for i := range 15 {
		messages = append(messages, &conversation.Message{
			ID:   fmt.Sprintf("msg_%d", i),
			Role: conversation.RoleAssistant,
			ToolResults: []conversation.ToolResult{
				{
					Name:   "Read",
					CallID: fmt.Sprintf("call_%d", i),
					Output: fmt.Sprintf("File content %d with lots of detail that takes up tokens. "+
						"This simulates a real file read result that would be quite large. "+
						"Line 1\nLine 2\nLine 3\nLine 4\nLine 5", i),
				},
			},
		})
	}

	summarizeCalled := false
	var summarizeMessages []*conversation.Message

	service := &Service{
		config: CompactionConfig{
			ContextLimit: 200000,
			SummarizeFunc: func(ctx context.Context, msgs []*conversation.Message, prompt string) (string, error) {
				summarizeCalled = true
				summarizeMessages = msgs
				return `1. **Primary Request and Intent**: The user asked to test the micro-compaction pipeline to verify that old tool results are trimmed before LLM summarization, ensuring efficient context usage.

2. **Key Technical Concepts**: Go, micro-compaction, tool result retention, pointer strings.

3. **Files and Code Sections**: compaction/micro.go and compaction/compaction.go were the main files under test.

4. **Errors and Fixes**: No errors encountered during testing.

5. **Current State**: Micro-compaction is working correctly. The pipeline trims old tool results and keeps only the most recent 3 per tool type. All tests passing.

6. **User Feedback**: User confirmed the approach matches Claude Code's wM algorithm.

7. **Next Steps**: Continue with integration testing of the full compaction pipeline.`, nil
			},
		},
		fileAccess: make(map[string]*FileAccessRecord),
	}

	conv := &conversation.Conversation{
		ID:                 "test-micro",
		CurrentContextSize: 150000,
		Messages:           messages,
	}

	result, err := service.CompactWithContext(context.Background(), conv, true, nil)
	if err != nil {
		t.Fatalf("CompactWithContext failed: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Compaction returned error: %v", result.Error)
	}

	if !summarizeCalled {
		t.Fatal("SummarizeFunc was never called")
	}

	// Count how many Read tool results still have full content vs pointer strings
	fullResults := 0
	pointerResults := 0
	for _, msg := range summarizeMessages {
		for _, tr := range msg.ToolResults {
			if tr.Name == "Read" {
				if strings.Contains(tr.Output, "File content") {
					fullResults++
				} else if strings.Contains(tr.Output, "[Tool result compacted") {
					pointerResults++
				}
			}
		}
	}

	// Last 3 should be full, first 12 should be pointers (retention=3, the
	// default that matches Claude Code's wM algorithm).
	if fullResults != 3 {
		t.Errorf("Expected 3 full Read results (retention=3), got %d", fullResults)
	}
	if pointerResults != 12 {
		t.Errorf("Expected 12 pointer Read results, got %d", pointerResults)
	}

	t.Logf("OK: micro-compaction ran before summarization, %d full / %d pointer results", fullResults, pointerResults)
}

// TestPostCompactionVerification verifies that a summary which lacks a clearly
// matched "Primary Request" header is NOT discarded. Matching Codex CLI, a
// generated summary is never thrown away over header phrasing — doing so aborts
// compaction and causes runaway token growth. The missing section is recorded
// as a soft signal (result.MissingSections) but compaction still succeeds.
func TestPostCompactionVerification(t *testing.T) {
	service := &Service{
		config: CompactionConfig{
			ContextLimit: 200000,
			SummarizeFunc: func(ctx context.Context, msgs []*conversation.Message, prompt string) (string, error) {
				// Return summary missing "Primary Request" section but long enough to pass length check.
				// This tests that a missing section is a SOFT signal, not a fatal error.
				return "This is a detailed summary without the required section headers. " +
					"Current State: everything is fine and working as expected. " +
					"The project has been updated with several improvements across multiple files. " +
					"Key changes include refactoring the authentication module, updating the database schema, " +
					"and fixing several bugs in the frontend. All tests are passing and the code compiles cleanly. " +
					"Next steps involve deploying to staging and running integration tests.", nil
			},
		},
		fileAccess: make(map[string]*FileAccessRecord),
	}

	conv := &conversation.Conversation{
		ID:                 "test-verify",
		CurrentContextSize: 150000,
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "test"},
		},
	}

	result, err := service.CompactWithContext(context.Background(), conv, true, nil)
	if err != nil {
		t.Fatalf("CompactWithContext failed: %v", err)
	}

	// Compaction must NOT fail just because a header is phrased differently.
	if result.Error != nil {
		t.Fatalf("Expected non-fatal compaction for missing section, got error: %v", result.Error)
	}
	if !result.Compacted {
		t.Fatal("Expected compaction to succeed (Compacted=true) despite missing section")
	}
	if result.Summary == "" {
		t.Fatal("Expected the generated summary to be preserved, got empty")
	}
	// The missing section should be recorded as a soft signal for observability.
	foundMissing := false
	for _, s := range result.MissingSections {
		if strings.Contains(s, "Primary Request") {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Errorf("Expected 'Primary Request' recorded in MissingSections soft signal, got: %v", result.MissingSections)
	}

	t.Logf("OK: missing section recorded as soft signal, compaction still succeeded: %v", result.MissingSections)
}

// TestPostCompactionLongSummaryNonStandardHeaders reproduces the exact
// production failure: a long, perfectly valid summary whose headers are phrased
// differently than the prompt template (e.g. "Overview"/"Current Tasks" instead
// of "Primary Request"/"Current Work"). The old literal strings.Contains gate
// hard-failed this and aborted compaction, causing runaway token growth. The
// summary must now be preserved and compaction must succeed.
func TestPostCompactionLongSummaryNonStandardHeaders(t *testing.T) {
	longSummary := "## Overview\n" +
		strings.Repeat("The user asked to refactor the authentication subsystem and migrate the "+
			"token store to a new schema. We traced the login flow, updated middleware, and "+
			"verified the session cache behaves correctly under load. ", 60) +
		"\n## Current Tasks\nDeploy to staging and run the integration suite.\n"

	if len(longSummary) < 10000 {
		t.Fatalf("test setup: expected a >10000 char summary, got %d", len(longSummary))
	}

	service := &Service{
		config: CompactionConfig{
			ContextLimit: 200000,
			SummarizeFunc: func(ctx context.Context, msgs []*conversation.Message, prompt string) (string, error) {
				return longSummary, nil
			},
		},
		fileAccess: make(map[string]*FileAccessRecord),
	}

	conv := &conversation.Conversation{
		ID:                 "test-long-nonstandard",
		CurrentContextSize: 150000,
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "refactor auth"},
		},
	}

	result, err := service.CompactWithContext(context.Background(), conv, true, nil)
	if err != nil {
		t.Fatalf("CompactWithContext failed: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Expected non-fatal compaction for non-standard headers, got error: %v", result.Error)
	}
	if !result.Compacted || result.Summary == "" {
		t.Fatalf("Expected the summary to be preserved and compacted; Compacted=%v len(Summary)=%d", result.Compacted, len(result.Summary))
	}

	t.Logf("OK: long summary with non-standard headers preserved; MissingSections=%v", result.MissingSections)
}

// TestPostCompactionVerificationTooShort verifies that an unusably short model
// response falls back to a bounded deterministic handoff.
func TestPostCompactionVerificationTooShort(t *testing.T) {
	service := &Service{
		config: CompactionConfig{
			ContextLimit: 200000,
			SummarizeFunc: func(ctx context.Context, msgs []*conversation.Message, prompt string) (string, error) {
				return "Too short", nil
			},
		},
		fileAccess: make(map[string]*FileAccessRecord),
	}

	conv := &conversation.Conversation{
		ID:                 "test-short",
		CurrentContextSize: 150000,
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "test"},
		},
	}

	result, err := service.CompactWithContext(context.Background(), conv, true, nil)
	if err != nil {
		t.Fatalf("CompactWithContext failed: %v", err)
	}

	if result.Error != nil {
		t.Fatalf("too-short summary should recover, got: %v", result.Error)
	}
	if !result.Compacted || result.RecoveryMethod != "deterministic" {
		t.Fatalf("result = %+v, want deterministic recovery", result)
	}
	if !strings.Contains(result.Summary, "summary_too_short") {
		t.Fatalf("recovery summary does not preserve cause: %s", result.Summary)
	}
}

// TestBuildCompactedMessagesWithContext verifies the post-compact message assembly
func TestBuildCompactedMessagesWithContext(t *testing.T) {
	service := &Service{
		config: CompactionConfig{
			ContextLimit: 200000,
		},
		fileAccess: make(map[string]*FileAccessRecord),
	}

	result := &CompactionResult{
		Summary:   "1. **Primary Request**: Fix the bug.\n5. **Current State**: Fixed.",
		Compacted: true,
	}

	compCtx := &CompactionContext{
		CurrentMode: "act",
		ModeName:    "ACT Mode",
		ActiveTodos: []Todo{
			{Content: "Deploy changes", Status: "pending"},
			{Content: "Run tests", Status: "in_progress"},
		},
		CompletedTodos: []Todo{
			{Content: "Fix the bug", Status: "completed"},
		},
		ActiveMCPServers: []string{"edgartools", "filesystem"},
	}

	sourceMessages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "Please fix the login bug."},
		{Role: conversation.RoleAssistant, Content: "Working on it."},
		{Role: conversation.RoleUser, Content: "Also run the tests once fixed."},
	}
	msgs := service.BuildCompactedMessagesWithContext(result, compCtx, sourceMessages...)

	// Verify structure: summary + tasks + mode + ack + continuation
	if len(msgs) < 4 {
		t.Fatalf("Expected at least 4 messages, got %d", len(msgs))
	}

	// Message 1: summary (user)
	if msgs[0].Role != conversation.RoleUser {
		t.Errorf("Message 0 should be user, got %s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "continued from a previous conversation") {
		t.Error("Message 0 should contain continuation notice")
	}
	if !strings.Contains(msgs[0].Content, "Primary Request") {
		t.Error("Message 0 should contain the summary")
	}

	// Find the task list message
	foundTasks := false
	foundMode := false
	for _, msg := range msgs {
		if generated, _ := msg.Metadata[conversation.CompactionGeneratedMetadataKey].(bool); !generated {
			t.Errorf("compaction message missing generated marker: %#v", msg.Metadata)
		}
		if strings.Contains(msg.Content, "Current Tasks") {
			foundTasks = true
			if !strings.Contains(msg.Content, "Deploy changes") {
				t.Error("Task list should contain 'Deploy changes'")
			}
			if !strings.Contains(msg.Content, "In Progress") {
				t.Error("Task list should have 'In Progress' section")
			}
		}
		if strings.Contains(msg.Content, "ACT Mode") {
			foundMode = true
		}
	}
	if !foundTasks {
		t.Error("No task list message found in compacted messages")
	}
	if !foundMode {
		t.Error("No mode context message found in compacted messages")
	}

	// Compacted history is passive. Auto-resume, when enabled, is emitted once
	// by the TUI lifecycle rather than persisted as a synthetic conversation.
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "continue the conversation") ||
			strings.Contains(msg.Content, "I'm continuing") {
			t.Fatalf("compacted history contains imperative continuation: %q", msg.Content)
		}
	}

	t.Logf("OK: %d passive compacted messages with tasks and mode context", len(msgs))
}

// TestBuildCompactedMessagesWithContextIncludesRecentVerbatimMessages verifies
// Fix 1: BuildCompactedMessagesWithContext threads the pre-compaction
// sourceMessages through to Message 5 ("## Recent Messages") so recent
// verbatim user turns actually survive compaction. Previously this branch
// checked result.CompactedMessages, which is nil at every real call site
// (callers only set it AFTER calling this function), so the section never
// appeared in practice.
func TestBuildCompactedMessagesWithContextIncludesRecentVerbatimMessages(t *testing.T) {
	service := &Service{
		config:     CompactionConfig{ContextLimit: 200000},
		fileAccess: make(map[string]*FileAccessRecord),
	}
	result := &CompactionResult{
		Summary:   "1. **Primary Request**: Fix the bug.\n5. **Current State**: Fixed.",
		Compacted: true,
	}
	sourceMessages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "Please add retry logic to the HTTP client."},
		{Role: conversation.RoleAssistant, Content: "Added exponential backoff."},
		{Role: conversation.RoleUser, Content: "Also cap retries at 5 attempts."},
	}
	msgs := service.BuildCompactedMessagesWithContext(result, nil, sourceMessages...)
	var recentMsg *conversation.Message
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "## Recent Messages") {
			recentMsg = msg
			break
		}
	}
	if recentMsg == nil {
		t.Fatalf("expected a '## Recent Messages' section, got %d messages: %+v", len(msgs), msgs)
	}
	if !strings.Contains(recentMsg.Content, "Please add retry logic to the HTTP client.") {
		t.Errorf("recent messages section missing first verbatim user message: %s", recentMsg.Content)
	}
	if !strings.Contains(recentMsg.Content, "Also cap retries at 5 attempts.") {
		t.Errorf("recent messages section missing second verbatim user message: %s", recentMsg.Content)
	}
	// Assistant-only content must not leak into the user-message-only recap.
	if strings.Contains(recentMsg.Content, "Added exponential backoff.") {
		t.Errorf("recent messages section should only contain user messages, found assistant content: %s", recentMsg.Content)
	}
	// Without sourceMessages, the section must be omitted entirely (regression
	// guard against reintroducing the dead result.CompactedMessages check).
	msgsNoSource := service.BuildCompactedMessagesWithContext(result, nil)
	for _, msg := range msgsNoSource {
		if strings.Contains(msg.Content, "## Recent Messages") {
			t.Errorf("expected no '## Recent Messages' section without sourceMessages, got: %s", msg.Content)
		}
	}
}

// TestCompactionTodoTool verifies the CompactionTodoTool can update todos during summarization
func TestCompactionTodoTool(t *testing.T) {
	compCtx := &CompactionContext{
		ActiveTodos: []Todo{
			{Content: "Fix authentication bug", Status: "in_progress", ActiveForm: "Fixing authentication bug"},
			{Content: "Add dark mode toggle", Status: "pending", ActiveForm: "Adding dark mode toggle"},
			{Content: "Write tests", Status: "pending", ActiveForm: "Writing tests"},
		},
		CompletedTodos: []Todo{},
	}

	todoTool := NewCompactionTodoTool(compCtx)
	if todoTool.Name() != CompactionTodoToolName {
		t.Errorf("Expected tool name %s, got %s", CompactionTodoToolName, todoTool.Name())
	}

	// Test listing
	result, err := todoTool.Execute(context.Background(), map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("List returned error: %v", result.Error)
	}

	// Test completing
	_, err = todoTool.Execute(context.Background(), map[string]any{
		"action": "update", "todo_id": "0", "status": "completed",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if len(compCtx.ActiveTodos) != 2 {
		t.Errorf("Expected 2 active todos, got %d", len(compCtx.ActiveTodos))
	}
	if len(compCtx.CompletedTodos) != 1 {
		t.Errorf("Expected 1 completed, got %d", len(compCtx.CompletedTodos))
	}

	// Test removing
	_, err = todoTool.Execute(context.Background(), map[string]any{
		"action": "remove", "todo_id": "0",
	})
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if len(compCtx.ActiveTodos) != 1 {
		t.Errorf("Expected 1 active todo, got %d", len(compCtx.ActiveTodos))
	}

	t.Log("OK: CompactionTodoTool operations working")
}

// TestCompactionContextInjection verifies the todo tool is correctly injected into context
func TestCompactionContextInjection(t *testing.T) {
	compCtx := &CompactionContext{
		ActiveTodos: []Todo{{Content: "Test task", Status: "pending"}},
	}

	todoTool := NewCompactionTodoTool(compCtx)
	ctx := context.Background()
	ctx = WithCompactionTodoTool(ctx, todoTool)
	ctx = WithCompactionContext(ctx, compCtx)

	if GetCompactionTodoTool(ctx) == nil {
		t.Fatal("Failed to retrieve todo tool from context")
	}
	if GetCompactionContext(ctx) == nil {
		t.Fatal("Failed to retrieve compaction context")
	}

	t.Log("OK: context injection working")
}

// TestServiceSetters verifies the new setter methods work
func TestServiceSetters(t *testing.T) {
	svc := NewService(DefaultConfig(200000))

	// Test SetContextLimit
	svc.SetContextLimit(128000)
	if svc.config.ContextLimit != 128000 {
		t.Errorf("SetContextLimit: expected 128000, got %d", svc.config.ContextLimit)
	}

	// Test SetSummarizeFunc
	called := false
	svc.SetSummarizeFunc(func(ctx context.Context, msgs []*conversation.Message, prompt string) (string, error) {
		called = true
		return "test", nil
	})
	svc.config.SummarizeFunc(context.Background(), nil, "")
	if !called {
		t.Error("SetSummarizeFunc: function was not set")
	}

	// Test SetReadFileFunc
	svc.SetReadFileFunc(func(path string) (string, error) {
		return "content", nil
	})
	content, _ := svc.config.ReadFileFunc("test.go")
	if content != "content" {
		t.Errorf("SetReadFileFunc: expected 'content', got %q", content)
	}

	// Test GetSummaryMaxTokens default
	if svc.GetSummaryMaxTokens() != DefaultSummaryMaxTokens {
		t.Errorf("GetSummaryMaxTokens default: expected %d, got %d", DefaultSummaryMaxTokens, svc.GetSummaryMaxTokens())
	}

	// Test file access preserved across setter calls
	svc.RecordFileAccess("/test/file.go", true)
	svc.SetContextLimit(256000) // Should NOT clear fileAccess
	files := svc.getImportantFiles()
	if len(files) != 1 {
		t.Errorf("File access should survive SetContextLimit, got %d files", len(files))
	}

	t.Log("OK: service setters preserve state")
}
