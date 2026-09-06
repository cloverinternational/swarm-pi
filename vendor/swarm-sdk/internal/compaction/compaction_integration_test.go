package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// =============================================================================
// REAL COMPACTION INTEGRATION TEST
//
// Loads the largest real conversation from ~/.swarm/conversations/,
// runs the full 3-stage compaction pipeline, and dumps verbose traces of
// every step — micro-compaction, summarization, verification, and message
// assembly — with file references, token counts, and before/after state.
//
// Run with:
//   RUN_COMPACTION_INTEGRATION=1 go test -v -run TestRealCompactionPipeline ./swarm-sdk/compaction/
//
// Outputs are written to:
//   /tmp/compaction_test_<timestamp>/
//     ├── 00_input_conversation.json      # The raw conversation loaded
//     ├── 01_micro_compaction_trace.json   # Per-tool-result decisions
//     ├── 02_summarize_request.json        # Exact prompt + messages sent to LLM
//     ├── 03_summarize_response.json       # Raw LLM response
//     ├── 04_analysis_stripped.json        # Summary after <analysis> tag removal
//     ├── 05_verification_result.json      # Post-compaction verification
//     ├── 06_compacted_messages.json       # Final assembled messages
//     └── 07_pipeline_trace.json           # Full execution trace with timings
// =============================================================================

// traceEntry is a single step in the execution trace.
type traceEntry struct {
	Step      int            `json:"step"`
	Stage     string         `json:"stage"`
	Action    string         `json:"action"`
	File      string         `json:"file,omitempty"`
	Line      string         `json:"line,omitempty"`
	Detail    string         `json:"detail"`
	Before    any            `json:"before,omitempty"`
	After     any            `json:"after,omitempty"`
	Duration  string         `json:"duration,omitempty"`
	Timestamp string         `json:"timestamp"`
	TokenInfo *tokenSnapshot `json:"token_info,omitempty"`
}

type tokenSnapshot struct {
	OriginalContextSize int `json:"original_context_size"`
	EstimatedSaved      int `json:"estimated_saved,omitempty"`
	SummaryTokens       int `json:"summary_tokens,omitempty"`
	PostCompactEstimate int `json:"post_compact_estimate,omitempty"`
}

type pipelineTracer struct {
	entries []traceEntry
	stepNum int
	outDir  string
}

func newPipelineTracer(outDir string) *pipelineTracer {
	return &pipelineTracer{outDir: outDir}
}

func (pt *pipelineTracer) trace(stage, action, detail string, before, after any) {
	pt.stepNum++
	pt.entries = append(pt.entries, traceEntry{
		Step:      pt.stepNum,
		Stage:     stage,
		Action:    action,
		Detail:    detail,
		Before:    before,
		After:     after,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func (pt *pipelineTracer) traceWithTokens(stage, action, detail string, tokens *tokenSnapshot) {
	pt.stepNum++
	pt.entries = append(pt.entries, traceEntry{
		Step:      pt.stepNum,
		Stage:     stage,
		Action:    action,
		Detail:    detail,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		TokenInfo: tokens,
	})
}

func (pt *pipelineTracer) traceWithFile(stage, action, file, line, detail string) {
	pt.stepNum++
	pt.entries = append(pt.entries, traceEntry{
		Step:      pt.stepNum,
		Stage:     stage,
		Action:    action,
		File:      file,
		Line:      line,
		Detail:    detail,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
}

func (pt *pipelineTracer) dump(t *testing.T) {
	path := filepath.Join(pt.outDir, "07_pipeline_trace.json")
	writeJSON(t, path, pt.entries)
	t.Logf("[TRACE] Full pipeline trace written to %s (%d steps)", path, len(pt.entries))
}

func writeJSON(t *testing.T, path string, data any) {
	t.Helper()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Logf("[WARN] Failed to marshal JSON for %s: %v", path, err)
		return
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Logf("[WARN] Failed to write %s: %v", path, err)
	}
}

// TestRealCompactionPipeline loads a real conversation and runs the full
// compaction pipeline with verbose tracing.
func TestRealCompactionPipeline(t *testing.T) {
	if os.Getenv("RUN_COMPACTION_INTEGRATION") != "1" {
		t.Skip("Skipping real compaction integration test; set RUN_COMPACTION_INTEGRATION=1")
	}

	// ── Setup output directory ───────────────────────────────────────
	outDir := filepath.Join("/tmp", fmt.Sprintf("compaction_test_%d", time.Now().Unix()))
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir %s: %v", outDir, err)
	}
	t.Logf("[SETUP] Output directory: %s", outDir)

	tracer := newPipelineTracer(outDir)
	tracer.traceWithFile("setup", "create_output_dir", outDir, "", "Created output directory for test artifacts")

	// ── Step 1: Find the largest real conversation ───────────────────
	t.Log("[STEP 1] Scanning ~/.swarm/conversations/ for largest conversation...")
	tracer.trace("load", "scan_conversations", "Scanning ~/.swarm/conversations/ for real conversations sorted by file size", nil, nil)

	convDir := paths.ConversationsDir()
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read conversations dir: %v", err)
	}

	type convFile struct {
		Name string
		Size int64
	}
	var convFiles []convFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		convFiles = append(convFiles, convFile{Name: e.Name(), Size: info.Size()})
	}
	sort.Slice(convFiles, func(i, j int) bool { return convFiles[i].Size > convFiles[j].Size })

	if len(convFiles) == 0 {
		t.Fatal("No conversation files found in ~/.swarm/conversations/")
	}

	t.Logf("[STEP 1] Found %d conversations. Top 5 by size:", len(convFiles))
	for i, cf := range convFiles {
		if i >= 5 {
			break
		}
		t.Logf("  %d. %s (%d KB)", i+1, cf.Name, cf.Size/1024)
	}

	// ── Step 2: Load the conversation ────────────────────────────────
	selectedFile := convFiles[0].Name
	convPath := filepath.Join(convDir, selectedFile)
	t.Logf("[STEP 2] Loading conversation: %s (%d KB)", selectedFile, convFiles[0].Size/1024)
	tracer.traceWithFile("load", "read_conversation", convPath, "", fmt.Sprintf("Loading %d KB conversation file", convFiles[0].Size/1024))

	loadStart := time.Now()
	rawData, err := os.ReadFile(convPath)
	if err != nil {
		t.Fatalf("Failed to read conversation file: %v", err)
	}
	loadDur := time.Since(loadStart)

	// Parse the conversation JSON
	var convJSON struct {
		ID                 string            `json:"id"`
		CreatedAt          string            `json:"created_at"`
		UpdatedAt          string            `json:"updated_at"`
		Mode               string            `json:"mode"`
		Status             string            `json:"status"`
		Messages           []json.RawMessage `json:"messages"`
		TotalTokens        int               `json:"total_tokens"`
		CurrentContextSize int               `json:"current_context_size"`
		Metadata           map[string]any    `json:"metadata"`
	}
	if err := json.Unmarshal(rawData, &convJSON); err != nil {
		t.Fatalf("Failed to parse conversation JSON: %v", err)
	}

	t.Logf("[STEP 2] Loaded in %v:", loadDur)
	t.Logf("  ID:                  %s", convJSON.ID)
	t.Logf("  Messages:            %d", len(convJSON.Messages))
	t.Logf("  CurrentContextSize:  %d tokens", convJSON.CurrentContextSize)
	t.Logf("  TotalTokens:         %d", convJSON.TotalTokens)
	t.Logf("  Mode:                %s", convJSON.Mode)
	t.Logf("  Status:              %s", convJSON.Status)

	tracer.traceWithTokens("load", "conversation_loaded",
		fmt.Sprintf("Loaded %d messages from %s", len(convJSON.Messages), selectedFile),
		&tokenSnapshot{OriginalContextSize: convJSON.CurrentContextSize})

	// Parse messages into SDK conversation.Message objects
	var messages []*conversation.Message
	roleCounts := map[string]int{}
	toolResultCount := 0
	totalContentChars := 0

	for i, rawMsg := range convJSON.Messages {
		var msg struct {
			ID          string                    `json:"id"`
			Timestamp   string                    `json:"timestamp"`
			Role        string                    `json:"role"`
			Content     string                    `json:"content"`
			ToolResults []conversation.ToolResult `json:"tool_results"`
			Metadata    map[string]any            `json:"metadata"`
		}
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			t.Logf("[WARN] Failed to parse message %d: %v", i, err)
			continue
		}

		sdkMsg := &conversation.Message{
			ID:          msg.ID,
			Role:        conversation.Role(msg.Role),
			Content:     msg.Content,
			ToolResults: msg.ToolResults,
			Metadata:    msg.Metadata,
		}
		messages = append(messages, sdkMsg)
		roleCounts[msg.Role]++
		toolResultCount += len(msg.ToolResults)
		totalContentChars += len(msg.Content)
		for _, tr := range msg.ToolResults {
			totalContentChars += len(tr.Output)
		}
	}

	t.Logf("[STEP 2] Message breakdown:")
	for role, count := range roleCounts {
		t.Logf("  %s: %d messages", role, count)
	}
	t.Logf("  Tool results: %d across all messages", toolResultCount)
	t.Logf("  Total content: %d chars (~%d estimated tokens)", totalContentChars, EstimateTokens(fmt.Sprintf("%d", totalContentChars)))

	// Dump input conversation summary (not full content — too large)
	inputSummary := map[string]any{
		"file":                 selectedFile,
		"file_size_kb":         convFiles[0].Size / 1024,
		"message_count":        len(messages),
		"role_counts":          roleCounts,
		"tool_result_count":    toolResultCount,
		"total_content_chars":  totalContentChars,
		"current_context_size": convJSON.CurrentContextSize,
		"total_tokens":         convJSON.TotalTokens,
		"mode":                 convJSON.Mode,
	}
	writeJSON(t, filepath.Join(outDir, "00_input_conversation.json"), inputSummary)

	// ── Step 3: Stage 1 — Micro-compaction ───────────────────────────
	t.Log("[STEP 3] Stage 1: Running micro-compaction (deterministic tool result trimming)...")
	tracer.traceWithFile("micro_compaction", "start",
		"compaction/micro.go", "Process()",
		fmt.Sprintf("Processing %d messages with %d tool results", len(messages), toolResultCount))

	mc := NewMicroCompactor()

	// Pre-compaction tool result inventory
	preToolInventory := map[string]int{}
	for _, msg := range messages {
		for _, tr := range msg.ToolResults {
			if tr.Name != "" {
				preToolInventory[tr.Name]++
			}
		}
	}
	t.Logf("[STEP 3] Pre-compaction tool inventory:")
	for tool, count := range preToolInventory {
		isHeavy := HeavyTools[tool]
		t.Logf("  %s: %d results (heavy=%v)", tool, count, isHeavy)
	}

	microStart := time.Now()
	_, microSaved := mc.Process(messages)
	microDur := time.Since(microStart)

	// Post-compaction tool result inventory
	postToolInventory := map[string]struct{ Full, Pointer int }{}
	for _, msg := range messages {
		for _, tr := range msg.ToolResults {
			if tr.Name == "" {
				continue
			}
			entry := postToolInventory[tr.Name]
			if strings.Contains(tr.Output, "saved") || strings.Contains(tr.Output, "completed successfully") {
				entry.Pointer++
			} else {
				entry.Full++
			}
			postToolInventory[tr.Name] = entry
		}
	}

	t.Logf("[STEP 3] Micro-compaction completed in %v:", microDur)
	t.Logf("  Estimated tokens saved: %d", microSaved)
	t.Logf("  Compaction stats: %+v", mc.GetStats())
	t.Logf("  Post-compaction tool breakdown:")
	for tool, counts := range postToolInventory {
		t.Logf("    %s: %d full, %d pointer", tool, counts.Full, counts.Pointer)
	}

	microTrace := map[string]any{
		"duration_ms":         microDur.Milliseconds(),
		"estimated_saved":     microSaved,
		"stats":               mc.GetStats(),
		"pre_tool_inventory":  preToolInventory,
		"post_tool_inventory": postToolInventory,
		"retention_count":     DefaultRetentionCount,
		"heavy_tools":         HeavyTools,
	}
	writeJSON(t, filepath.Join(outDir, "01_micro_compaction_trace.json"), microTrace)

	tracer.traceWithTokens("micro_compaction", "complete",
		fmt.Sprintf("Micro-compaction saved ~%d tokens in %v", microSaved, microDur),
		&tokenSnapshot{OriginalContextSize: convJSON.CurrentContextSize, EstimatedSaved: microSaved})

	// ── Step 4: Stage 2 — Full compaction (LLM summarization) ────────
	t.Log("[STEP 4] Stage 2: Full compaction via LLM summarization...")
	tracer.traceWithFile("summarization", "start",
		"compaction/compaction.go", "CompactWithContext()",
		"Building summarization request with Y3A prompt")

	// Build conversation for compaction
	conv := &conversation.Conversation{
		ID:                 convJSON.ID,
		CurrentContextSize: convJSON.CurrentContextSize,
		TotalTokens:        convJSON.TotalTokens,
		Messages:           messages,
	}

	// Track the summarize call for inspection
	var capturedPrompt string
	var capturedMessageCount int
	var summarizeDuration time.Duration

	service := NewService(CompactionConfig{
		ContextLimit:       200000,
		MaxFilesToRecover:  MaxFilesToRecover,
		MaxTokensPerFile:   MaxTokensPerFile,
		MaxTotalFileTokens: MaxTotalFileTokens,
		SummarizeFunc: func(ctx context.Context, msgs []*conversation.Message, prompt string) (string, error) {
			capturedPrompt = prompt
			capturedMessageCount = len(msgs)

			t.Logf("[STEP 4] SummarizeFunc called:")
			t.Logf("  Messages to summarize: %d", len(msgs))
			t.Logf("  Prompt length: %d chars (%d estimated tokens)", len(prompt), EstimateTokens(prompt))

			// Log the full prompt being sent
			summarizeReq := map[string]any{
				"system_prompt": SummarizationSystemPrompt,
				"user_prompt":   prompt,
				"message_count": len(msgs),
				"prompt_tokens": EstimateTokens(prompt),
				"max_tokens":    DefaultSummaryMaxTokens,
			}

			// Add message role breakdown
			reqRoles := map[string]int{}
			for _, m := range msgs {
				reqRoles[string(m.Role)]++
			}
			summarizeReq["message_roles"] = reqRoles

			// Sample first and last messages for context
			if len(msgs) > 0 {
				first := msgs[0]
				contentPreview := first.Content
				if len(contentPreview) > 500 {
					contentPreview = contentPreview[:500] + "...[truncated]"
				}
				summarizeReq["first_message"] = map[string]any{
					"role":    first.Role,
					"content": contentPreview,
				}
			}
			if len(msgs) > 1 {
				last := msgs[len(msgs)-1]
				contentPreview := last.Content
				if len(contentPreview) > 500 {
					contentPreview = contentPreview[:500] + "...[truncated]"
				}
				summarizeReq["last_message"] = map[string]any{
					"role":    last.Role,
					"content": contentPreview,
				}
			}

			writeJSON(t, filepath.Join(outDir, "02_summarize_request.json"), summarizeReq)

			// Use a mock response that matches what a real model would produce.
			// In a live test with RUN_COMPACTION_LIVE=1, this would call the real API.
			sumStart := time.Now()
			var response string

			if os.Getenv("RUN_COMPACTION_LIVE") == "1" {
				t.Log("[STEP 4] LIVE MODE: Would call real API here (not implemented in unit test)")
				t.Log("[STEP 4] To test with real API, use the TUI integration test")
				response = buildMockSummary(msgs)
			} else {
				response = buildMockSummary(msgs)
			}

			summarizeDuration = time.Since(sumStart)
			_ = response // captured for JSON dump above

			t.Logf("[STEP 4] Summary generated in %v:", summarizeDuration)
			t.Logf("  Response length: %d chars (%d estimated tokens)", len(response), EstimateTokens(response))

			writeJSON(t, filepath.Join(outDir, "03_summarize_response.json"), map[string]any{
				"raw_response":    response,
				"response_chars":  len(response),
				"response_tokens": EstimateTokens(response),
				"duration_ms":     summarizeDuration.Milliseconds(),
			})

			return response, nil
		},
		ReadFileFunc: func(path string) (string, error) {
			content, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			return string(content), nil
		},
	})

	tracer.trace("summarization", "service_configured",
		fmt.Sprintf("Service configured: context_limit=200000, max_files=%d, max_tokens_per_file=%d",
			MaxFilesToRecover, MaxTokensPerFile), nil, nil)

	// Build compaction context
	compCtx := &CompactionContext{
		Strategy:     StrategyStandard,
		CurrentMode:  convJSON.Mode,
		MessageCount: len(messages),
	}

	// Run the full pipeline
	t.Log("[STEP 4] Calling CompactWithContext...")
	compactStart := time.Now()
	result, err := service.CompactWithContext(context.Background(), conv, false, compCtx)
	compactDur := time.Since(compactStart)

	if err != nil {
		t.Fatalf("[STEP 4] CompactWithContext returned error: %v", err)
	}

	t.Logf("[STEP 4] CompactWithContext completed in %v", compactDur)
	t.Logf("  Compacted:        %v", result.Compacted)
	t.Logf("  OriginalTokens:   %d", result.OriginalTokens)
	t.Logf("  CompactedTokens:  %d", result.CompactedTokens)
	t.Logf("  Summary length:   %d chars", len(result.Summary))
	t.Logf("  Messages sent:    %d", capturedMessageCount)
	t.Logf("  Prompt chars:     %d", len(capturedPrompt))

	if result.Error != nil {
		t.Logf("[STEP 4] Result.Error: %v", result.Error)
	}

	tracer.traceWithTokens("summarization", "complete",
		fmt.Sprintf("Pipeline completed in %v, compacted=%v", compactDur, result.Compacted),
		&tokenSnapshot{
			OriginalContextSize: result.OriginalTokens,
			SummaryTokens:       EstimateTokens(result.Summary),
			PostCompactEstimate: result.CompactedTokens,
		})

	// ── Step 5: Verify analysis tag stripping ────────────────────────
	t.Log("[STEP 5] Checking <analysis> tag stripping...")
	tracer.traceWithFile("post_processing", "analysis_strip",
		"compaction/compaction.go", "stripAnalysisTags()",
		"Verifying <analysis> tags were stripped from summary")

	if strings.Contains(result.Summary, "<analysis>") {
		t.Error("[STEP 5] FAIL: Summary still contains <analysis> tags!")
	} else {
		t.Log("[STEP 5] OK: No <analysis> tags in final summary")
	}

	// Save stripped summary
	writeJSON(t, filepath.Join(outDir, "04_analysis_stripped.json"), map[string]any{
		"summary":                result.Summary,
		"summary_chars":          len(result.Summary),
		"summary_tokens":         EstimateTokens(result.Summary),
		"contains_analysis_tags": strings.Contains(result.Summary, "<analysis>"),
		"has_primary_request":    strings.Contains(result.Summary, "Primary Request"),
		"has_current_state":      strings.Contains(result.Summary, "Current State"),
	})

	// ── Step 6: Post-compaction verification ─────────────────────────
	t.Log("[STEP 6] Post-compaction verification...")
	tracer.traceWithFile("verification", "check",
		"compaction/compaction.go", "verifyCompaction()",
		"Checking required sections and token budget")

	verificationResult := map[string]any{
		"compacted":        result.Compacted,
		"error":            fmt.Sprintf("%v", result.Error),
		"original_tokens":  result.OriginalTokens,
		"compacted_tokens": result.CompactedTokens,
		"context_limit":    200000,
		"threshold":        int(200000 * AutoCompactThreshold),
		"under_threshold":  result.CompactedTokens < int(200000*AutoCompactThreshold),
	}

	if result.Compacted {
		reduction := 0
		if result.OriginalTokens > 0 {
			reduction = 100 - (result.CompactedTokens * 100 / result.OriginalTokens)
		}
		verificationResult["reduction_pct"] = reduction
		t.Logf("[STEP 6] Reduction: %d%% (%d -> %d tokens)", reduction, result.OriginalTokens, result.CompactedTokens)
	}

	writeJSON(t, filepath.Join(outDir, "05_verification_result.json"), verificationResult)

	// ── Step 7: Build compacted messages ─────────────────────────────
	t.Log("[STEP 7] Building compacted messages (post-compact assembly)...")
	tracer.traceWithFile("assembly", "build_messages",
		"compaction/compaction.go", "BuildCompactedMessagesWithContext()",
		"Assembling post-compact messages: summary + tasks + mode + ack + continuation")

	if !result.Compacted || result.Error != nil {
		t.Log("[STEP 7] Skipping message assembly — compaction did not succeed")
	} else {
		assemblyStart := time.Now()
		compactedMsgs := service.BuildCompactedMessagesWithContext(result, compCtx, messages...)
		assemblyDur := time.Since(assemblyStart)

		t.Logf("[STEP 7] Assembled %d messages in %v:", len(compactedMsgs), assemblyDur)

		msgSummaries := make([]map[string]any, len(compactedMsgs))
		totalCompactedChars := 0
		for i, msg := range compactedMsgs {
			contentPreview := msg.Content
			if len(contentPreview) > 300 {
				contentPreview = contentPreview[:300] + "...[truncated]"
			}
			msgSummaries[i] = map[string]any{
				"index":          i,
				"role":           msg.Role,
				"content_chars":  len(msg.Content),
				"content_tokens": EstimateTokens(msg.Content),
				"preview":        contentPreview,
			}
			totalCompactedChars += len(msg.Content)
			t.Logf("  [%d] role=%s, %d chars (%d tokens): %s",
				i, msg.Role, len(msg.Content), EstimateTokens(msg.Content),
				truncate(msg.Content, 100))
		}

		totalCompactedTokens := EstimateTokens(strings.Repeat("x", totalCompactedChars))
		t.Logf("[STEP 7] Total compacted content: %d chars (~%d tokens)", totalCompactedChars, totalCompactedTokens)

		// Verify alternating roles
		for i := 1; i < len(compactedMsgs); i++ {
			if compactedMsgs[i].Role == compactedMsgs[i-1].Role {
				t.Logf("[STEP 7] WARN: Adjacent messages %d and %d both have role=%s", i-1, i, compactedMsgs[i].Role)
			}
		}

		writeJSON(t, filepath.Join(outDir, "06_compacted_messages.json"), map[string]any{
			"message_count":        len(compactedMsgs),
			"total_chars":          totalCompactedChars,
			"total_tokens":         totalCompactedTokens,
			"assembly_duration_ms": assemblyDur.Milliseconds(),
			"messages":             msgSummaries,
		})

		tracer.traceWithTokens("assembly", "complete",
			fmt.Sprintf("Assembled %d messages, %d total tokens", len(compactedMsgs), totalCompactedTokens),
			&tokenSnapshot{PostCompactEstimate: totalCompactedTokens})
	}

	// ── Dump full trace ──────────────────────────────────────────────
	tracer.dump(t)

	t.Logf("\n========================================")
	t.Logf("COMPACTION PIPELINE TEST COMPLETE")
	t.Logf("Output directory: %s", outDir)
	t.Logf("========================================")
}

// buildMockSummary creates a realistic mock summary from the conversation.
// This exercises the full pipeline without requiring an API call.
func buildMockSummary(msgs []*conversation.Message) string {
	// Extract some real content from the messages for a realistic mock
	var userRequests []string
	var filesReferenced []string
	var errorsFound []string

	for _, msg := range msgs {
		if msg.Role == conversation.RoleUser && msg.Content != "" {
			preview := msg.Content
			if len(preview) > 200 {
				preview = preview[:200]
			}
			userRequests = append(userRequests, preview)
		}
		// Look for file references in tool results
		for _, tr := range msg.ToolResults {
			if tr.Name == "Read" || tr.Name == "Edit" || tr.Name == "Write" {
				if len(tr.Output) > 0 {
					// Try to extract file path from the first line
					lines := strings.SplitN(tr.Output, "\n", 2)
					if len(lines) > 0 && len(lines[0]) < 200 {
						filesReferenced = append(filesReferenced, lines[0])
					}
				}
			}
			if tr.Name == "Bash" && strings.Contains(tr.Output, "error") {
				preview := tr.Output
				if len(preview) > 200 {
					preview = preview[:200]
				}
				errorsFound = append(errorsFound, preview)
			}
		}
	}

	// Cap lists
	if len(userRequests) > 5 {
		userRequests = userRequests[:5]
	}
	if len(filesReferenced) > 10 {
		filesReferenced = filesReferenced[:10]
	}
	if len(errorsFound) > 3 {
		errorsFound = errorsFound[:3]
	}

	var sb strings.Builder

	sb.WriteString("<analysis>\nAnalyzing conversation with ")
	sb.WriteString(fmt.Sprintf("%d messages. ", len(msgs)))
	sb.WriteString("Identifying user requests, technical decisions, and current state.\n")
	sb.WriteString("</analysis>\n\n")

	sb.WriteString("1. **Primary Request and Intent**: ")
	if len(userRequests) > 0 {
		sb.WriteString("The user requested: ")
		sb.WriteString(userRequests[0])
		if len(userRequests) > 1 {
			sb.WriteString(fmt.Sprintf(" (and %d more requests)", len(userRequests)-1))
		}
	} else {
		sb.WriteString("Unable to determine primary request from conversation history.")
	}
	sb.WriteString("\n\n")

	sb.WriteString("2. **Key Technical Concepts**: Go, SDK architecture, compaction pipeline, ")
	sb.WriteString("micro-compaction, LLM summarization, context window management.\n\n")

	sb.WriteString("3. **Files and Code Sections**:\n")
	if len(filesReferenced) > 0 {
		for _, f := range filesReferenced {
			sb.WriteString(fmt.Sprintf("   - %s\n", truncate(f, 100)))
		}
	} else {
		sb.WriteString("   - No specific files identified in conversation\n")
	}
	sb.WriteString("\n")

	sb.WriteString("4. **Errors and Fixes**:\n")
	if len(errorsFound) > 0 {
		for _, e := range errorsFound {
			sb.WriteString(fmt.Sprintf("   - %s\n", truncate(e, 150)))
		}
	} else {
		sb.WriteString("   - No errors encountered\n")
	}
	sb.WriteString("\n")

	sb.WriteString("5. **Current State**: The conversation covered development work across ")
	sb.WriteString(fmt.Sprintf("%d messages. ", len(msgs)))
	sb.WriteString("Work is in progress and should be continued.\n\n")

	sb.WriteString("6. **User Feedback**: User provided direction throughout the conversation.\n\n")

	sb.WriteString("7. **Next Steps**: Continue with the current task as described in the conversation.\n")

	return sb.String()
}
