package compaction

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/google/uuid"
)

// Constants for compaction thresholds and limits
const (
	// AutoCompactThreshold is the fraction of the context window at which
	// auto-compaction fires proactively.  Set to 0.80 so the remaining 20 %
	// (≈ 25 600 tokens on a 128 k model) is available for the summarization
	// output — summaries that run out of room produce truncated handoffs.
	// Rule of thumb: trigger at 80 %, leave 20 % for output tokens.
	AutoCompactThreshold = 0.80

	// MaxFilesToRecover is the maximum number of files to recover after compaction
	// INCREASED from 5 to 15 to match Claude Code's behavior
	MaxFilesToRecover = 15

	// MaxTokensPerFile is the maximum tokens for a single recovered file
	// INCREASED from 10000 to 15000 for better context preservation
	MaxTokensPerFile = 15000

	// MaxTotalFileTokens is the maximum total tokens for all recovered files
	// INCREASED from 50000 to 100000 for comprehensive file recovery
	MaxTotalFileTokens = 100000

	// TokensPerChar is the rough ratio of tokens to characters (4 chars ≈ 1 token)
	TokensPerChar = 0.25
)

// CompactionResult contains the result of a compaction operation
type CompactionResult struct {
	// Compacted indicates if compaction was performed
	Compacted bool

	// Summary is the handoff summary for continuing work
	Summary string

	// RecentUserMessages contains recent user messages preserved for context
	RecentUserMessages []string

	// RecoveredFiles contains files that were automatically recovered
	RecoveredFiles []RecoveredFile

	// OriginalTokens is the token count before compaction
	OriginalTokens int

	// CompactedTokens is the token count after compaction
	CompactedTokens int

	// RecoveryMethod is "direct", "chunked", or "deterministic".
	RecoveryMethod string

	// SummaryAttempts counts bounded summarizer calls made by the service. A
	// provider fallback chain may make multiple model attempts inside one call.
	SummaryAttempts int

	// FallbackUsed identifies which configured fallback produced the summary.
	FallbackUsed bool
	UsedProvider string
	UsedModel    string

	// MissingSections lists canonical sections the summary did not clearly
	// include (matched via the robust hasSection matcher). This is a SOFT
	// quality signal only — a non-empty list never aborts compaction.
	MissingSections []string

	// SizeWarning is set (non-fatal) when the estimated post-compaction size
	// still exceeds the auto-compact threshold. Compaction still succeeds; the
	// caller may choose to schedule a follow-up pass.
	SizeWarning string

	// NewConvID is retained for client compatibility. Stable-ID compaction sets
	// it to the existing conversation ID after advancing the active boundary.
	NewConvID string

	// CompactedMessages holds the newly active generation.
	CompactedMessages []*conversation.Message

	// Error contains any error that occurred (nil on success)
	Error error
}

// RecoveredFile represents a file recovered after compaction
type RecoveredFile struct {
	Path      string
	Content   string
	Tokens    int
	Truncated bool
}

// FileAccessRecord tracks when files were accessed
type FileAccessRecord struct {
	Path        string
	ReadAt      time.Time
	WriteAt     time.Time
	AccessCount int
}

// Todo represents a task/todo item to preserve across compaction
type Todo struct {
	Content    string   `json:"content"`
	Status     string   `json:"status"` // "pending", "in_progress", "completed"
	ActiveForm string   `json:"activeForm,omitempty"`
	DependsOn  []string `json:"dependsOn,omitempty"` // IDs of tasks this depends on
}

// CompactionContext captures the full state to preserve during compaction.
// This enables mode-aware compaction with context preservation.
type CompactionContext struct {
	// Mode state
	CurrentMode string   // "plan", "act", "auto", "off", "debug"
	ModeName    string   // Human-readable name like "PLAN Mode"
	ModeHistory []string // Recent mode transitions for context

	// Task state
	ActiveTodos    []Todo // Current todo items (pending + in_progress)
	CompletedTodos []Todo // Recently completed todos (for context)

	// MCP state
	ActiveMCPServers []string // Connected MCP server names
	RecentMCPTools   []string // Recently used MCP tool names

	// File state (in addition to FileAccessRecord)
	ModifiedFiles []string // Files modified this session
	ReadFiles     []string // Files read this session

	// Hooks state
	ActiveHooks []string // Enabled hook names

	// Conversation metadata
	StartTime    time.Time
	MessageCount int
	TokensUsed   int

	// SystemPrompt is the agent's system prompt, preserved across compaction
	SystemPrompt string

	// Strategy
	Strategy CompactionStrategy

	// ConversationJSONPath is the absolute path to the conversation JSON file on disk.
	// When set, it is embedded in the compaction summary prompt so the summarizing
	// model knows where the full transcript lives, and it is included in the
	// post-compaction handoff message so the resuming agent can read raw history
	// if needed. Also forwarded to the micro-compactor so pointer messages
	// reference the file.
	ConversationJSONPath string

	// Preserve flags - control what content is kept during compaction
	PreserveSystemMessages bool // When true, system messages are preserved during compaction
	PreserveToolResults    bool // When true, tool results are preserved during compaction

	// Plan mode state — carried forward across compaction so the agent's
	// plan is never lost when the context window is trimmed.
	PlanModeActive bool   // True when the agent is currently in plan mode
	ActivePlan     string // The plan content the agent is working with

	// RootSessionID is the TUI session's stable identifier (set at session
	// start, never mutated by compaction).  Used by the plan tools to
	// derive a session-scoped file path instead of writing to WorkDir.
	RootSessionID string

	// SuppressFollowUpQuestions controls the continuation instruction injected
	// into the post-compact summary message.
	// true  	 auto-compact: tell the model to resume directly without greeting
	//          or asking the user for direction.
	// false 	 manual /compact: the model may naturally ask what to work on next.
	// Mirrors Claude Code's suppressFollowUpQuestions parameter in
	// getCompactUserSummaryMessage().
	SuppressFollowUpQuestions bool
}

// CompactionConfig configures the compaction service
type CompactionConfig struct {
	// ContextLimit is the model's context limit in tokens
	ContextLimit int

	// AutoCompactThreshold is the percentage (0.0-1.0) that triggers auto-compaction
	AutoCompactThreshold float64

	// MaxFilesToRecover is the maximum number of files to recover
	MaxFilesToRecover int

	// MaxTokensPerFile limits tokens per recovered file
	MaxTokensPerFile int

	// MaxTotalFileTokens limits total tokens for all recovered files
	MaxTotalFileTokens int

	// SummaryMaxTokens is the max_tokens cap for the summarization LLM call.
	// Defaults to DefaultSummaryMaxTokens (20000) for standard models, or
	// DefaultSummaryMaxTokensHighContext (30000) for models with 200k+ context windows.
	// Set higher for models with larger output windows or when more detailed summaries are needed.
	SummaryMaxTokens int

	// SummarizeFunc is called to generate the summary (uses LLM)
	SummarizeFunc func(ctx context.Context, messages []*conversation.Message, prompt string) (string, error)

	// ReadFileFunc reads a file's content (for file recovery)
	ReadFileFunc func(path string) (string, error)

	// ToolProvider allows injecting the todo tool during compaction (optional)
	// If provided, the CompactionTodoTool will be available to Claude during summarization
	ToolProvider func(ctx context.Context, compCtx *CompactionContext) (any, error)

	// ProgressFunc is an optional callback invoked at coarse-grained stage
	// boundaries during CompactWithContext, letting a caller (e.g. the TUI)
	// drive a progress indicator. stage is a short machine-readable name
	// (e.g. "micro_compaction", "summarizing", "summarizing_chunk_2_of_5",
	// "verifying", "recovering_files", "done") and pct is a monotonically
	// non-decreasing fraction in [0.0, 1.0] estimating overall completion.
	// Never called concurrently by a single CompactWithContext invocation.
	// Nil-safe: leave unset to disable progress reporting entirely.
	ProgressFunc func(stage string, pct float64)
}

// DefaultConfig returns a default compaction configuration
func DefaultConfig(contextLimit int) CompactionConfig {
	return CompactionConfig{
		ContextLimit:         contextLimit,
		AutoCompactThreshold: AutoCompactThreshold,
		MaxFilesToRecover:    MaxFilesToRecover,
		MaxTokensPerFile:     MaxTokensPerFile,
		MaxTotalFileTokens:   MaxTotalFileTokens,
		ReadFileFunc:         DefaultReadFileFunc(),
	}
}

// DefaultReadFileFunc returns a default file reading function that reads from the local filesystem.
// This can be used as a default for ReadFileFunc in CompactionConfig.
func DefaultReadFileFunc() func(path string) (string, error) {
	return func(path string) (string, error) {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(content), nil
	}
}

// Service provides compaction functionality
type Service struct {
	config     CompactionConfig
	fileMu     sync.RWMutex
	fileAccess map[string]*FileAccessRecord
}

// NewService creates a new compaction service
func NewService(config CompactionConfig) *Service {
	return &Service{
		config:     config,
		fileAccess: make(map[string]*FileAccessRecord),
	}
}

// GetSummaryMaxTokens returns the configured summary max tokens, falling back to the default.
// For models with context windows >= 200k, it uses DefaultSummaryMaxTokensHighContext (30000).
func (s *Service) GetSummaryMaxTokens() int {
	if s.config.SummaryMaxTokens > 0 {
		return s.config.SummaryMaxTokens
	}
	// Use higher limit for large context window models
	if s.config.ContextLimit >= HighContextThreshold {
		return DefaultSummaryMaxTokensHighContext
	}
	return DefaultSummaryMaxTokens
}

// SetSummarizeFunc updates the summarization function without recreating the service.
// This preserves file access records and other accumulated state.
func (s *Service) SetSummarizeFunc(fn func(ctx context.Context, messages []*conversation.Message, prompt string) (string, error)) {
	s.config.SummarizeFunc = fn
}

// SetReadFileFunc updates the file reading function.
func (s *Service) SetReadFileFunc(fn func(path string) (string, error)) {
	s.config.ReadFileFunc = fn
}

// SetContextLimit updates the context limit.
func (s *Service) SetContextLimit(limit int) {
	s.config.ContextLimit = limit
}

// SetSummaryMaxTokens updates the output reservation used by bounded summary
// preflight. Provider requests apply their own capability cap as a final guard.
func (s *Service) SetSummaryMaxTokens(limit int) {
	s.config.SummaryMaxTokens = limit
}

// SetProgressFunc updates the stage-progress callback without recreating the
// service (mirrors SetSummarizeFunc/SetReadFileFunc). Pass nil to disable
// progress reporting. See CompactionConfig.ProgressFunc for stage names.
func (s *Service) SetProgressFunc(fn func(stage string, pct float64)) {
	s.config.ProgressFunc = fn
}

// RecordFileAccess records that a file was accessed
func (s *Service) RecordFileAccess(path string, isWrite bool) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	record, exists := s.fileAccess[path]
	if !exists {
		record = &FileAccessRecord{Path: path}
		s.fileAccess[path] = record
	}

	now := time.Now()
	if isWrite {
		record.WriteAt = now
	} else {
		record.ReadAt = now
	}
	record.AccessCount++
}

// ClearFileAccess clears all file access records
func (s *Service) ClearFileAccess() {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	s.fileAccess = make(map[string]*FileAccessRecord)
}

// reportProgress invokes the configured ProgressFunc, if any (Fix 3). Nil-safe:
// no-ops when ProgressFunc is unset so callers never need to check for nil.
func (s *Service) reportProgress(stage string, pct float64) {
	if s.config.ProgressFunc == nil {
		return
	}
	s.config.ProgressFunc(stage, pct)
}

// ShouldAutoCompact checks if automatic compaction should trigger
func (s *Service) ShouldAutoCompact(tokenCount int) bool {
	threshold := int(float64(s.config.ContextLimit) * s.config.AutoCompactThreshold)
	return tokenCount >= threshold
}

// GetThresholdInfo returns information about the compaction threshold
func (s *Service) GetThresholdInfo(tokenCount int) ThresholdInfo {
	threshold := int(float64(s.config.ContextLimit) * s.config.AutoCompactThreshold)
	percentUsed := int(float64(tokenCount) / float64(s.config.ContextLimit) * 100)
	tokensRemaining := threshold - tokenCount

	return ThresholdInfo{
		IsAboveThreshold: tokenCount >= threshold,
		PercentUsed:      percentUsed,
		TokensRemaining:  tokensRemaining,
		ContextLimit:     s.config.ContextLimit,
		Threshold:        threshold,
	}
}

// ThresholdInfo contains information about the compaction threshold
type ThresholdInfo struct {
	IsAboveThreshold bool
	PercentUsed      int
	TokensRemaining  int
	ContextLimit     int
	Threshold        int
}

// summarizeWithContext wraps SummarizeFunc to inject compaction context and todo tool.
// This allows Claude to update todos during the summarization process, ensuring they
// are preserved in the compacted conversation.
func (s *Service) summarizeWithContext(ctx context.Context, messages []*conversation.Message, prompt string, compCtx *CompactionContext) (string, error) {
	// Create a todo tool for this compaction context
	todoTool := NewCompactionTodoTool(compCtx)

	// Inject both the tool and context into the context
	enhancedCtx := WithCompactionTodoTool(ctx, todoTool)
	enhancedCtx = WithCompactionContext(enhancedCtx, compCtx)

	// Call the wrapped SummarizeFunc with enhanced context
	return s.config.SummarizeFunc(enhancedCtx, messages, prompt)
}

// Compact performs compaction on the conversation (backwards compatible)
func (s *Service) Compact(ctx context.Context, conv *conversation.Conversation, manual bool) (*CompactionResult, error) {
	// Use default context with no mode info for backwards compatibility
	compCtx := &CompactionContext{
		Strategy: StrategyStandard,
	}
	return s.CompactWithContext(ctx, conv, manual, compCtx)
}

// CompactWithContext performs compaction with full context preservation.
// This is the preferred method that supports mode-aware compaction.
//
// Pipeline (matching Claude Code's 3-stage approach):
//
//	Stage 1: Micro-compaction — deterministic tool result trimming (saves 60-70% of tool tokens)
//	Stage 2: Full compaction — LLM summarization with Y3A prompt
//	Stage 3: Post-compaction — verification + restoration attachments
func (s *Service) CompactWithContext(ctx context.Context, conv *conversation.Conversation, manual bool, compCtx *CompactionContext) (*CompactionResult, error) {
	if s.config.SummarizeFunc == nil {
		return nil, fmt.Errorf("compaction.summarize_func_required: SummarizeFunc must be configured")
	}

	// Clean up browser processes before compaction to prevent phantom processes
	if err := CleanupBrowserProcesses(ctx); err != nil {
		// Log but don't fail - browsers are ephemeral
		fmt.Printf("[compaction] Warning: browser cleanup error: %v\n", err)
	}

	// Default to standard strategy if not set
	if compCtx == nil {
		compCtx = &CompactionContext{Strategy: StrategyStandard}
	}
	if compCtx.Strategy == "" {
		compCtx.Strategy = StrategyStandard
	}
	// Keep the mode contract correct for every caller, including SDK users that
	// do not pass through the TUI's buildCompactionContext helper. Manual
	// /compact may end at a natural handoff; automatic compaction must resume
	// the interrupted task without waiting for a new user instruction.
	compCtx.SuppressFollowUpQuestions = !manual

	// Restore persisted state into in-memory records (merge, not overwrite).
	// This ensures file access records and tasks survive across restarts.
	if restored := s.RestoreFromConversation(conv); restored != nil {
		// Merge tasks from persisted state if live context has none
		if len(compCtx.ActiveTodos) == 0 && len(restored.ActiveTodos) > 0 {
			compCtx.ActiveTodos = restored.ActiveTodos
		}
		if len(compCtx.CompletedTodos) == 0 && len(restored.CompletedTodos) > 0 {
			compCtx.CompletedTodos = restored.CompletedTodos
		}
		// Merge mode context from persisted state if live context is empty
		if compCtx.CurrentMode == "" && restored.CurrentMode != "" {
			compCtx.CurrentMode = restored.CurrentMode
			compCtx.ModeName = restored.ModeName
		}
		if len(compCtx.ActiveMCPServers) == 0 && len(restored.ActiveMCPServers) > 0 {
			compCtx.ActiveMCPServers = restored.ActiveMCPServers
		}
	}

	// Snapshot current state to conversation JSON before compaction
	s.SnapshotToConversation(conv, compCtx)

	result := &CompactionResult{
		// CurrentContextSize is the authoritative context size: InputContextSize()
		// from the last assistant message. TotalTokens double-counts on multi-turn
		// conversations and must not be used here.
		OriginalTokens: conv.CurrentContextSize,
	}
	activeMessages := conv.ActiveMessages()
	for i, msg := range activeMessages {
		if msg != nil {
			activeMessages[i] = msg.Clone()
		}
	}

	// ── Stage 1: Micro-compaction ──────────────────────────────────────
	// Run deterministic tool result trimming BEFORE the expensive LLM call.
	// This keeps last 3 results per heavy tool type (Read, Bash, Grep, etc.)
	// and replaces older results with pointer strings. Typically saves 60-70%
	// of tool tokens, often enough to avoid full compaction entirely.
	mc := NewMicroCompactor()
	if compCtx.ConversationJSONPath != "" {
		mc.SetConversationPath(compCtx.ConversationJSONPath)
	}
	_, microSaved := mc.Process(activeMessages)
	s.reportProgress("micro_compaction", 0.05)
	if microSaved > 0 {
		// Update the authoritative token count after micro-compaction.
		// We can only estimate the savings since we don't have API counts.
		result.OriginalTokens = conv.CurrentContextSize // before micro-compact
	}

	// ── Stage 2: Full compaction (LLM summarization) ───────────────────
	compressionPrompt := CompressionPrompt

	// Append conversation JSON path to the prompt so the summarizing model
	// knows where the full transcript is persisted on disk. It can reference
	// or read this file if it needs to reconstruct details that fell outside
	// the context window.
	if compCtx.ConversationJSONPath != "" {
		compressionPrompt += fmt.Sprintf(`

CONVERSATION TRANSCRIPT LOCATION:
The complete conversation history (including all tool inputs/outputs) is persisted as JSON at:
  %s

You may reference this path in your summary so the resuming agent can inspect the raw transcript if needed. Do not attempt to read or parse it during summarization — it is provided for reference only.`,
			compCtx.ConversationJSONPath)
	}

	var summary string
	var err error

	// ── Anti-compounding guard ──────────────────────────────────────────
	// Mirrors Codex CLI's is_summary_message() filter (codex-rs/core/src/compact.rs):
	// if activeMessages already contains a prior compaction-generated handoff
	// bundle (summary + restored files + tasks + mode, all tagged with
	// conversation.CompactionGeneratedMetadataKey — this is generation 2+),
	// that entire bundle is condensed to a single short "previous summary"
	// anchor before being fed to the summarizer. Without this, compaction #2
	// would re-summarize compaction #1's already-lossy summary (plus its
	// restored-file/task/mode prose) in full, compounding loss and wasting
	// tokens on content that is not natural conversation.
	summarizationInput := filterCompoundingCompactionMessages(activeMessages)

	s.reportProgress("summarizing", 0.15)
	summary, result.RecoveryMethod, result.SummaryAttempts, err =
		s.summarizeBounded(ctx, summarizationInput, compressionPrompt, compCtx)

	if err != nil {
		summary = buildDeterministicRecoverySummary(activeMessages, compCtx, err)
		result.RecoveryMethod = "deterministic"
	}

	// Format summary: strip <analysis> CoT scratchpad and extract <summary> content.
	// Mirrors Claude Code's formatCompactSummary() — strips analysis tags,
	// extracts <summary> block with "Summary:\n" header.
	summary = FormatCompactSummary(summary)

	// Validate minimum summary quality (matches CC's EgH validation)
	if len(summary) < 200 {
		tooShortErr := fmt.Errorf(
			"compaction.summary_too_short: summary is %d chars, expected at least 200",
			len(summary),
		)
		summary = buildDeterministicRecoverySummary(activeMessages, compCtx, tooShortErr)
		result.RecoveryMethod = "deterministic"
	}

	// result.Summary holds the formatted summary body ONLY. The continuation
	// prefix (`SummaryPrefix`) is applied once at message-construction time in
	// BuildCompactedMessagesWithContext — double-prefixing was producing a
	// malformed leading block that surveys saw as "empty summary / truncated
	// continuation" because the display squashed the duplicate header.
	result.Summary = summary
	result.Compacted = true

	// ── Stage 3: Post-compaction sizing + soft quality signals ──────────
	// NON-FATAL: estimate the post-compact size and record any missing
	// sections / size warning. Matching Codex CLI, a generated summary is
	// never discarded over header phrasing — doing so aborts compaction and
	// causes runaway token growth. verifyCompaction returns nil in normal
	// operation; a non-nil error here indicates a programming error, not a
	// summary-quality problem, so it is logged but still non-fatal.
	if err := s.verifyCompaction(result, summary); err != nil {
		result.SizeWarning = err.Error()
	}

	// NEW: Strict verification with retry logic (Phase 6)
	// Verify summary has all required sections and retry if needed
	s.reportProgress("verifying", 0.75)
	verifyResult := VerifySummary(summary)
	if result.RecoveryMethod != "deterministic" && !verifyResult.Valid && verifyResult.RetryRecommended {
		// Attempt retry with enhanced prompt
		retryPrompt := GenerateRetryPrompt(compressionPrompt, verifyResult)
		retrySummary, retryMethod, retryAttempts, retryErr :=
			s.summarizeBounded(ctx, summarizationInput, retryPrompt, compCtx)
		result.SummaryAttempts += retryAttempts

		if retryErr == nil && retrySummary != "" {
			retrySummary = FormatCompactSummary(retrySummary)
			retryVerify := VerifySummary(retrySummary)

			// Use retry result if better
			if retryVerify.Score > verifyResult.Score {
				summary = retrySummary
				verifyResult = retryVerify
				result.RecoveryMethod = retryMethod
				result.Compacted = true
			}
		}

		// If still invalid after retry, log but don't fail
		if !verifyResult.Valid {
			// Non-fatal: compaction completed, summary just scored below threshold.
			// Callers can inspect verifyResult via the VerifySummary function independently.
			_ = verifyResult.MissingSections
		}
	}
	result.Summary = summary
	if result.RecoveryMethod == "deterministic" {
		result.CompactedTokens = EstimateTokens(summary) + 2_000
	}

	// File recovery is now ENABLED - recover files using the compaction service
	// and populate RecoveredFiles for API compatibility
	s.reportProgress("recovering_files", 0.85)
	if result.RecoveryMethod != "deterministic" {
		result.RecoveredFiles = s.recoverFiles()
	}

	s.reportProgress("done", 1.0)
	return result, nil
}

// CompactHierarchical performs compaction with hierarchical summarization.
// This is the advanced approach matching Claude Code's multi-level preservation:
// - Level 1: Recent messages (preserved verbatim)
// - Level 2: Important messages (errors, decisions - preserved verbatim)
// - Level 3: Remaining messages (summarized in chunks)
func (s *Service) CompactHierarchical(ctx context.Context, conv *conversation.Conversation, manual bool, compCtx *CompactionContext) (*HierarchicalCompactResult, error) {
	// First perform standard compaction
	result, err := s.CompactWithContext(ctx, conv, manual, compCtx)
	if err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, result.Error
	}

	// Build hierarchical summary
	hierConfig := DefaultHierarchicalConfig()
	summaryFunc := func(msgs []*conversation.Message) (string, error) {
		return s.summarizeWithContext(ctx, msgs, CompressionPrompt, compCtx)
	}

	hierSummary, err := BuildHierarchicalSummary(conv.Messages, hierConfig, summaryFunc)
	if err != nil {
		// Fallback to standard result
		return &HierarchicalCompactResult{
			CompactionResult:    *result,
			HierarchicalSummary: HierarchicalSummary{},
			PreservedMessages:   []*conversation.Message{},
		}, nil
	}

	// Collect all preserved messages
	preserved := make([]*conversation.Message, 0)
	preserved = append(preserved, hierSummary.RecentMessages...)
	preserved = append(preserved, hierSummary.ImportantMessages...)

	return &HierarchicalCompactResult{
		CompactionResult:    *result,
		HierarchicalSummary: *hierSummary,
		PreservedMessages:   preserved,
	}, nil
}

// verifyCompaction estimates the compacted token size and records soft
// quality signals. It is intentionally NON-FATAL for section/header mismatches.
//
// Rationale: a generated summary must never be discarded merely because the
// model phrased its headers differently than the prompt template. Codex CLI
// (codex-rs/core/src/compact.rs run_compact_task_inner_impl) keeps whatever the
// model returns — `get_last_assistant_message_from_turn(...).unwrap_or_default()`
// — with zero header verification, then replaces history. Hard-failing here
// aborts compaction, leaves the full history in place, and causes the token
// count to run away (each retry summarizes an ever-larger transcript — the
// observed 180M-token estimate).
//
// Genuinely unusable summaries (empty / too short) are already rejected by the
// len(summary) < 200 guard in the caller before this runs. The robust
// VerifySummary matcher (verify.go) supplies the structured quality score for
// callers that want it; here we only RECORD missing sections, never fail.
func (s *Service) verifyCompaction(result *CompactionResult, summary string) error {
	// Use the robust matcher (markdown headers, **bold**, "Name:" prefix,
	// keyword fallback, and synonyms) rather than a literal strings.Contains,
	// so legitimately-phrased summaries are not flagged.
	for _, section := range []string{"Primary Request", "Current Work"} {
		if !hasSection(summary, section) {
			// Soft signal only — record but do not fail compaction.
			result.MissingSections = append(result.MissingSections, section)
		}
	}

	// Estimate compacted token size (summary + overhead for restoration)
	summaryTokens := EstimateTokens(summary)
	// Estimate total: summary + file attachments (~30k) + tasks (~2k) + continuation (~500)
	estimatedTotal := summaryTokens + 32500
	result.CompactedTokens = estimatedTotal

	// Record (do not fail) if the estimate is still above threshold. Aborting
	// here would defeat the purpose: a large-but-valid summary is still a net
	// reduction versus the uncompacted history. The caller may inspect
	// result.CompactedTokens to decide whether a follow-up pass is warranted.
	if s.config.ContextLimit > 0 {
		threshold := int(float64(s.config.ContextLimit) * AutoCompactThreshold)
		if estimatedTotal >= threshold {
			result.SizeWarning = fmt.Sprintf("post-compaction size %d still exceeds threshold %d", estimatedTotal, threshold)
		}
	}

	return nil
}

// maxPriorSummaryAnchorChars bounds the condensed "previous summary" anchor
// that replaces a prior compaction-generated handoff bundle when re-feeding
// history into a new compaction pass. Keeps the anchor a small, fixed cost
// regardless of how large the original synthetic bundle was.
const maxPriorSummaryAnchorChars = 4_000

// filterCompoundingCompactionMessages implements the anti-compounding guard
// (Fix 2). It mirrors Codex CLI's is_summary_message() filter in
// codex-rs/core/src/compact.rs: before feeding history into a NEW compaction
// pass, prior synthetic compaction-generated handoff messages (tagged via
// conversation.CompactionGeneratedMetadataKey -- see BuildCompactedMessagesWithContext,
// which sets this on every message it produces) are excluded from the
// token-heavy re-summarization content. Only a single short "previous
// summary" anchor extracted from the most recent such bundle is carried
// forward structurally, so compaction #2 never re-summarizes compaction #1's
// already-lossy summary/restored-files prose as if it were fresh
// conversation content.
//
// If activeMessages contains no compaction-generated messages (generation 1),
// this is a no-op and returns messages unchanged.
func filterCompoundingCompactionMessages(messages []*conversation.Message) []*conversation.Message {
	if !containsCompactionGeneratedMessage(messages) {
		return messages
	}
	priorSummary := extractPriorSummary(messages)
	filtered := make([]*conversation.Message, 0, len(messages)+1)
	if priorSummary != "" {
		filtered = append(filtered, &conversation.Message{
			Role: conversation.RoleUser,
			Content: "[Previous compaction summary -- condensed, not re-summarized]\n" +
				priorSummary,
		})
	}
	for _, msg := range messages {
		if msg == nil || isCompactionGeneratedMessage(msg) {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

// isCompactionGeneratedMessage reports whether msg was synthesized by a prior
// compaction pass (see conversation.CompactionGeneratedMetadataKey).
func isCompactionGeneratedMessage(msg *conversation.Message) bool {
	if msg == nil || msg.Metadata == nil {
		return false
	}
	generated, _ := msg.Metadata[conversation.CompactionGeneratedMetadataKey].(bool)
	return generated
}

// containsCompactionGeneratedMessage reports whether messages includes at
// least one prior compaction-generated handoff message, i.e. this is
// generation 2+ of compaction on this conversation.
func containsCompactionGeneratedMessage(messages []*conversation.Message) bool {
	for _, msg := range messages {
		if isCompactionGeneratedMessage(msg) {
			return true
		}
	}
	return false
}

// extractPriorSummary pulls the summary body out of the most recent
// compaction-generated handoff bundle in messages, stripping the
// continuation boilerplate (SummaryPrefix / transcript-path / "Recent
// messages are preserved..." notices) that BuildCompactedMessagesWithContext
// wraps around the raw summary text. Returns "" if no compaction-generated
// message is found. The result is truncated to maxPriorSummaryAnchorChars so
// a chain of compactions cannot cause the anchor itself to grow unbounded.
func extractPriorSummary(messages []*conversation.Message) string {
	// The first compaction-generated message in a bundle is always the
	// summary message (Message 1 in BuildCompactedMessagesWithContext); later
	// compaction-generated messages in the same bundle are restored
	// files/tasks/mode context, which are not prose summary and are dropped
	// entirely rather than condensed.
	var summaryContent string
	for _, msg := range messages {
		if !isCompactionGeneratedMessage(msg) {
			continue
		}
		if strings.Contains(msg.Content, "continued from a previous conversation") ||
			strings.HasPrefix(strings.TrimSpace(msg.Content), strings.TrimSpace(SummaryPrefix)) {
			summaryContent = msg.Content
			// Keep scanning: a later generation's summary message should win
			// over an earlier one if somehow both survived in history.
		}
	}
	if summaryContent == "" {
		return ""
	}
	// Strip the boilerplate wrapper, keeping just the summary body.
	body := summaryContent
	body = strings.TrimPrefix(body, SummaryPrefix)
	if idx := strings.Index(body, "\n\nIf you need specific details"); idx >= 0 {
		body = body[:idx]
	}
	if idx := strings.Index(body, "\n\nRecent messages are preserved verbatim"); idx >= 0 {
		body = body[:idx]
	}
	body = strings.TrimSpace(body)
	if len(body) > maxPriorSummaryAnchorChars {
		body = body[:maxPriorSummaryAnchorChars] + "\n...[condensed]"
	}
	return body
}

// collectRecentUserMessages extracts recent user messages within token budget
// This preserves the user's recent requests for continuity (Codex approach)
func collectRecentUserMessages(messages []*conversation.Message, maxTokens int) []string {
	var userMessages []string

	// Collect user messages from newest to oldest
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != conversation.RoleUser || msg.Content == "" {
			continue
		}

		// Skip system/context messages that start with special markers
		if strings.HasPrefix(msg.Content, "# ") ||
			strings.HasPrefix(msg.Content, "<") ||
			strings.HasPrefix(msg.Content, "Context ") {
			continue
		}

		userMessages = append(userMessages, msg.Content)
	}

	// Reverse to get chronological order
	for i, j := 0, len(userMessages)-1; i < j; i, j = i+1, j-1 {
		userMessages[i], userMessages[j] = userMessages[j], userMessages[i]
	}

	// Apply token budget, keeping most recent messages
	var selected []string
	totalTokens := 0

	for i := len(userMessages) - 1; i >= 0; i-- {
		tokens := EstimateTokens(userMessages[i])
		if totalTokens+tokens > maxTokens {
			// Truncate this message to fit remaining budget
			remaining := maxTokens - totalTokens
			if remaining > 100 { // Only include if meaningful
				maxChars := int(float64(remaining) / TokensPerChar)
				if maxChars < len(userMessages[i]) {
					truncated := userMessages[i][:maxChars] + "\n...[truncated]"
					selected = append([]string{truncated}, selected...)
				}
			}
			break
		}
		selected = append([]string{userMessages[i]}, selected...)
		totalTokens += tokens
	}

	return selected
}

// recoverFiles selects and reads recently accessed files for recovery
func (s *Service) recoverFiles() []RecoveredFile {
	if s.config.ReadFileFunc == nil {
		return nil
	}

	// Get important files sorted by access time
	importantFiles := s.getImportantFiles()

	var recovered []RecoveredFile
	totalTokens := 0

	for _, record := range importantFiles {
		if len(recovered) >= s.config.MaxFilesToRecover {
			break
		}

		if totalTokens >= s.config.MaxTotalFileTokens {
			break
		}

		content, err := s.config.ReadFileFunc(record.Path)
		if err != nil {
			continue
		}

		tokens := EstimateTokens(content)
		truncated := false

		// Truncate if too large
		if tokens > s.config.MaxTokensPerFile {
			maxChars := int(float64(s.config.MaxTokensPerFile) / TokensPerChar)
			if maxChars < len(content) {
				content = content[:maxChars]
				truncated = true
				tokens = s.config.MaxTokensPerFile
			}
		}

		// Check total budget
		if totalTokens+tokens > s.config.MaxTotalFileTokens {
			// Truncate to fit remaining budget
			remainingBudget := s.config.MaxTotalFileTokens - totalTokens
			maxChars := int(float64(remainingBudget) / TokensPerChar)
			if maxChars < len(content) && maxChars > 0 {
				content = content[:maxChars]
				truncated = true
				tokens = remainingBudget
			} else if maxChars <= 0 {
				break
			}
		}

		recovered = append(recovered, RecoveredFile{
			Path:      record.Path,
			Content:   content,
			Tokens:    tokens,
			Truncated: truncated,
		})
		totalTokens += tokens
	}

	return recovered
}

// recoverFilesLimited recovers up to maxFiles files
func (s *Service) recoverFilesLimited(maxFiles int) []RecoveredFile {
	if s.config.ReadFileFunc == nil {
		return nil
	}

	// Get important files sorted by access time
	importantFiles := s.getImportantFiles()

	var recovered []RecoveredFile
	totalTokens := 0

	for _, record := range importantFiles {
		if len(recovered) >= maxFiles {
			break
		}

		if totalTokens >= s.config.MaxTotalFileTokens {
			break
		}

		content, err := s.config.ReadFileFunc(record.Path)
		if err != nil {
			continue
		}

		tokens := EstimateTokens(content)
		truncated := false

		// Truncate if too large
		if tokens > s.config.MaxTokensPerFile {
			maxChars := int(float64(s.config.MaxTokensPerFile) / TokensPerChar)
			if maxChars < len(content) {
				content = content[:maxChars]
				truncated = true
				tokens = s.config.MaxTokensPerFile
			}
		}

		recovered = append(recovered, RecoveredFile{
			Path:      record.Path,
			Content:   content,
			Tokens:    tokens,
			Truncated: truncated,
		})
		totalTokens += tokens
	}

	return recovered
}

// getImportantFiles returns files sorted by importance (most recent first)
func (s *Service) getImportantFiles() []*FileAccessRecord {
	s.fileMu.RLock()
	defer s.fileMu.RUnlock()
	var files []*FileAccessRecord
	for _, record := range s.fileAccess {
		cloned := *record
		files = append(files, &cloned)
	}

	// Sort by most recent access (read or write)
	sort.Slice(files, func(i, j int) bool {
		timeI := files[i].ReadAt
		if files[i].WriteAt.After(timeI) {
			timeI = files[i].WriteAt
		}
		timeJ := files[j].ReadAt
		if files[j].WriteAt.After(timeJ) {
			timeJ = files[j].WriteAt
		}
		return timeI.After(timeJ)
	})

	return files
}

// recoverFilesWithContext recovers files using context-aware importance scoring.
// This prioritizes modified files and uses context hints for better recovery.
func (s *Service) recoverFilesWithContext(compCtx *CompactionContext) []RecoveredFile {
	if s.config.ReadFileFunc == nil {
		return nil
	}

	// Get files sorted by importance score
	scoredFiles := s.scoreFilesForRecovery(compCtx)

	// Use higher limits for comprehensive strategy
	maxFiles := s.config.MaxFilesToRecover + 3 // Recover more files
	maxTotalTokens := s.config.MaxTotalFileTokens + 20000

	var recovered []RecoveredFile
	totalTokens := 0

	for _, scored := range scoredFiles {
		if len(recovered) >= maxFiles {
			break
		}
		if totalTokens >= maxTotalTokens {
			break
		}

		content, err := s.config.ReadFileFunc(scored.Path)
		if err != nil {
			continue
		}

		tokens := EstimateTokens(content)
		truncated := false

		// Truncate if too large
		if tokens > s.config.MaxTokensPerFile {
			maxChars := int(float64(s.config.MaxTokensPerFile) / TokensPerChar)
			if maxChars < len(content) {
				content = content[:maxChars]
				truncated = true
				tokens = s.config.MaxTokensPerFile
			}
		}

		// Check total budget
		if totalTokens+tokens > maxTotalTokens {
			remainingBudget := maxTotalTokens - totalTokens
			maxChars := int(float64(remainingBudget) / TokensPerChar)
			if maxChars < len(content) && maxChars > 0 {
				content = content[:maxChars]
				truncated = true
				tokens = remainingBudget
			} else if maxChars <= 0 {
				break
			}
		}

		recovered = append(recovered, RecoveredFile{
			Path:      scored.Path,
			Content:   content,
			Tokens:    tokens,
			Truncated: truncated,
		})
		totalTokens += tokens
	}

	return recovered
}

// ScoredFile represents a file with an importance score for recovery prioritization
type ScoredFile struct {
	Path       string
	Score      float64
	IsModified bool
	LastAccess time.Time
}

// scoreFilesForRecovery scores files by importance for recovery prioritization
func (s *Service) scoreFilesForRecovery(compCtx *CompactionContext) []ScoredFile {
	scored := make(map[string]*ScoredFile) // Use map to dedupe

	// Create a set of modified files for quick lookup
	modifiedSet := make(map[string]bool)
	for _, path := range compCtx.ModifiedFiles {
		modifiedSet[path] = true
	}
	// Also mark as modified from the context's read files that are actually modifications
	for _, path := range compCtx.ReadFiles {
		// Check if this file was also written to
		for _, modPath := range compCtx.ModifiedFiles {
			if path == modPath {
				modifiedSet[path] = true
			}
		}
	}

	// Score a synchronized snapshot of the service's file access records.
	for _, record := range s.getImportantFiles() {
		score := 0.0
		isModified := modifiedSet[record.Path] || !record.WriteAt.IsZero()

		// Modified files get highest priority
		if isModified {
			score += 100.0
		}

		// Recent access bonus (up to 50 points, decays over hours)
		lastAccess := record.ReadAt
		if record.WriteAt.After(lastAccess) {
			lastAccess = record.WriteAt
		}
		hoursSinceAccess := time.Since(lastAccess).Hours()
		if hoursSinceAccess < 24 {
			score += 50.0 * (1.0 - hoursSinceAccess/24.0)
		}

		// Access count bonus (up to 20 points)
		if record.AccessCount > 0 {
			countBonus := float64(record.AccessCount) * 5.0
			if countBonus > 20.0 {
				countBonus = 20.0
			}
			score += countBonus
		}

		// Important file patterns
		if isConfigFile(record.Path) {
			score += 30.0
		}
		if isMainFile(record.Path) {
			score += 25.0
		}
		if isTestFile(record.Path) {
			score += 10.0
		}

		scored[record.Path] = &ScoredFile{
			Path:       record.Path,
			Score:      score,
			IsModified: isModified,
			LastAccess: lastAccess,
		}
	}

	// Also add files from CompactionContext that aren't in the service's internal map
	// This includes files tracked by the filetracker.Recorder via tool execution
	now := time.Now()
	for _, path := range compCtx.ModifiedFiles {
		if _, exists := scored[path]; !exists {
			score := 100.0 // Modified files get highest priority
			if isConfigFile(path) {
				score += 30.0
			}
			if isMainFile(path) {
				score += 25.0
			}
			scored[path] = &ScoredFile{
				Path:       path,
				Score:      score,
				IsModified: true,
				LastAccess: now,
			}
		}
	}

	for _, path := range compCtx.ReadFiles {
		if _, exists := scored[path]; !exists {
			score := 50.0 // Read files get medium priority
			if isConfigFile(path) {
				score += 30.0
			}
			if isMainFile(path) {
				score += 25.0
			}
			scored[path] = &ScoredFile{
				Path:       path,
				Score:      score,
				IsModified: false,
				LastAccess: now,
			}
		}
	}

	// Convert map to slice
	var result []ScoredFile
	for _, sf := range scored {
		result = append(result, *sf)
	}

	// Sort by score descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})

	return result
}

// isConfigFile checks if a path is a configuration file
func isConfigFile(path string) bool {
	configPatterns := []string{
		"config", "settings", ".json", ".yaml", ".yml", ".toml",
		"go.mod", "go.sum", "package.json", "Cargo.toml", "Makefile",
		".env", "Dockerfile", "docker-compose",
	}
	lowerPath := strings.ToLower(path)
	for _, pattern := range configPatterns {
		if strings.Contains(lowerPath, pattern) {
			return true
		}
	}
	return false
}

// isMainFile checks if a path is a main entry point
func isMainFile(path string) bool {
	mainPatterns := []string{
		"main.go", "main.py", "main.js", "main.ts", "index.",
		"app.go", "app.py", "app.js", "app.ts",
		"server.go", "server.py", "server.js", "server.ts",
	}
	lowerPath := strings.ToLower(path)
	for _, pattern := range mainPatterns {
		if strings.Contains(lowerPath, pattern) {
			return true
		}
	}
	return false
}

// isTestFile checks if a path is a test file
func isTestFile(path string) bool {
	testPatterns := []string{
		"_test.go", "_test.py", ".test.js", ".test.ts",
		".spec.js", ".spec.ts", "test_", "tests/",
	}
	lowerPath := strings.ToLower(path)
	for _, pattern := range testPatterns {
		if strings.Contains(lowerPath, pattern) {
			return true
		}
	}
	return false
}

// BuildCompactedMessages creates the compacted message array.
// Returns a single user message containing the summary and recovered files.
func (s *Service) BuildCompactedMessages(result *CompactionResult) []*conversation.Message {
	return s.BuildCompactedMessagesWithContext(result, nil)
}

// BuildCompactedMessagesWithContext creates the post-compaction message array.
// Matches Claude Code's post-compact structure (NGR template + restoration attachments):
//
//  1. User message: Summary (NGR template with transcript path reference)
//  2. User message: File attachments (top 5 files, 50k budget)
//  3. User message: Task list (active + recently completed todos)
//  4. User message: Recent verbatim messages from original conversation
//
// All messages use RoleUser because system messages are handled separately
// by the Anthropic API (system field), not as messages in the conversation.
//
// sourceMessages is the (optional, variadic) pre-compaction active-message
// slice that was actually fed to the summarizer (e.g. the cloned
// activeMessages passed to CompactWithContext / CompactFunc). Pass it via
// slice-spread: BuildCompactedMessagesWithContext(result, compCtx, activeMessages...).
// It is used to recover the last few verbatim user messages for Message 5
// ("Recent Messages"). Omit it (or pass nothing) when the pre-compaction
// messages are unavailable — Message 5 is simply omitted in that case. This
// parameter is variadic (rather than a plain slice) so existing call sites
// that only pass (result, compCtx) keep compiling unchanged.
func (s *Service) BuildCompactedMessagesWithContext(result *CompactionResult, compCtx *CompactionContext, sourceMessages ...*conversation.Message) []*conversation.Message {
	var messages []*conversation.Message
	now := time.Now()

	// ── Message 1: Summary (getCompactUserSummaryMessage format) ──────
	// Mirrors Claude Code's getCompactUserSummaryMessage():
	//   prefix + formatted-summary + transcript-path + "recent messages" note
	//   + optional continuation instruction (only for auto-compact).
	var summaryBuilder strings.Builder
	summaryBuilder.WriteString(SummaryPrefix)
	summaryBuilder.WriteString("\n")
	summaryBuilder.WriteString(result.Summary)

	// Transcript path reference — mirrors CC's optional transcriptPath block.
	// Wording: "If you need specific details from before compaction … read the
	// full transcript at: <path>"
	if compCtx != nil && compCtx.ConversationJSONPath != "" {
		summaryBuilder.WriteString(fmt.Sprintf(
			"\n\nIf you need specific details from before compaction (like exact code snippets, "+
				"error messages, or content you generated), read the full transcript at: %s",
			compCtx.ConversationJSONPath,
		))
	}

	summaryBuilder.WriteString("\n\nRecent messages are preserved verbatim below.\n\n")

	// Keep the boundary passive. The TUI sends a separate explicit continuation
	// trigger when auto-resume is enabled; embedding an imperative task here can
	// make non-Anthropic models re-execute summaries in a compaction loop.
	summaryBuilder.WriteString("This is background context from before compaction, not a new instruction. " +
		"Wait for the next user message or explicit continuation trigger before taking action.")

	summaryMsg := &conversation.Message{
		ID:        uuid.New().String(),
		Timestamp: now,
		Role:      conversation.RoleUser,
		Content:   summaryBuilder.String(),
	}
	messages = append(messages, summaryMsg)

	// ── Message 2: File attachments (restoration) ──────────────────────
	// Recover top 5 files within 50k token budget, matching CC's hgH function.
	if compCtx != nil && result.RecoveryMethod != "deterministic" {
		recoveredFiles := s.recoverFilesWithContext(compCtx)
		if len(recoveredFiles) == 0 {
			// Fallback to basic file recovery if context-aware has no data
			recoveredFiles = s.recoverFilesLimited(MaxFilesToRecover)
		}
		if len(recoveredFiles) > 0 {
			var fileBuilder strings.Builder
			fileBuilder.WriteString("## Restored Files\n\n")
			for _, f := range recoveredFiles {
				ext := fileExtension(f.Path)
				fileBuilder.WriteString(fmt.Sprintf("### %s\n```%s\n%s\n```\n\n", f.Path, ext, f.Content))
			}
			fileMsg := &conversation.Message{
				ID:        uuid.New().String(),
				Timestamp: now.Add(1 * time.Millisecond),
				Role:      conversation.RoleUser,
				Content:   fileBuilder.String(),
			}
			messages = append(messages, fileMsg)
		}
	}

	// Message 2.5: MCP Context (restoration)
	// NEW (Phase 7): MCP servers and tools preservation
	if compCtx != nil && len(compCtx.ActiveMCPServers) > 0 {
		mcpCtx := &MCPContext{
			Servers: make([]MCPServerInfo, 0),
		}
		for _, serverName := range compCtx.ActiveMCPServers {
			mcpCtx.AddServer(MCPServerInfo{
				Name:    serverName,
				Enabled: true,
			})
		}
		mcpContent := FormatMCPContext(mcpCtx)
		if mcpContent != "" {
			mcpMsg := &conversation.Message{
				ID:        uuid.New().String(),
				Timestamp: now.Add(2 * time.Millisecond),
				Role:      conversation.RoleUser,
				Content:   mcpContent,
			}
			messages = append(messages, mcpMsg)
		}
	}

	// ── Message 3: Task list (restoration) ─────────────────────────────
	// Matches CC's VgH function: inject active + recently completed todos
	if compCtx != nil && (len(compCtx.ActiveTodos) > 0 || len(compCtx.CompletedTodos) > 0) {
		var taskBuilder strings.Builder
		taskBuilder.WriteString("## Current Tasks\n\n")

		// In-progress first
		inProgress := filterTodos(compCtx.ActiveTodos, "in_progress")
		if len(inProgress) > 0 {
			taskBuilder.WriteString("### In Progress\n")
			for _, t := range inProgress {
				line := fmt.Sprintf("- [ ] **%s**", t.Content)
				if len(t.DependsOn) > 0 {
					line += fmt.Sprintf(" (depends on: %s)", strings.Join(t.DependsOn, ", "))
				}
				taskBuilder.WriteString(line + "\n")
			}
			taskBuilder.WriteString("\n")
		}

		// Pending
		pending := filterTodos(compCtx.ActiveTodos, "pending")
		if len(pending) > 0 {
			taskBuilder.WriteString("### Pending\n")
			for _, t := range pending {
				line := fmt.Sprintf("- [ ] %s", t.Content)
				if len(t.DependsOn) > 0 {
					line += fmt.Sprintf(" (depends on: %s)", strings.Join(t.DependsOn, ", "))
				}
				taskBuilder.WriteString(line + "\n")
			}
			taskBuilder.WriteString("\n")
		}

		// Recently completed (last 5)
		if len(compCtx.CompletedTodos) > 0 {
			taskBuilder.WriteString("### Recently Completed\n")
			start := 0
			if len(compCtx.CompletedTodos) > 5 {
				start = len(compCtx.CompletedTodos) - 5
			}
			for _, t := range compCtx.CompletedTodos[start:] {
				taskBuilder.WriteString(fmt.Sprintf("- [x] %s\n", t.Content))
			}
		}

		taskMsg := &conversation.Message{
			ID:        uuid.New().String(),
			Timestamp: now.Add(2 * time.Millisecond),
			Role:      conversation.RoleUser,
			Content:   taskBuilder.String(),
		}
		messages = append(messages, taskMsg)
	}

	// ── Message 4: Mode context ────────────────────────────────────────
	if compCtx != nil && compCtx.CurrentMode != "" {
		var modeBuilder strings.Builder
		if compCtx.ModeName != "" {
			modeBuilder.WriteString(fmt.Sprintf("**Current Mode**: %s\n", compCtx.ModeName))
		}
		if len(compCtx.ActiveMCPServers) > 0 {
			modeBuilder.WriteString(fmt.Sprintf("**Available MCP Servers**: %s\n", strings.Join(compCtx.ActiveMCPServers, ", ")))
		}
		// Inject active plan — this is the critical state that must survive
		// compaction so the agent never loses its approved or in-progress plan.
		if compCtx.PlanModeActive && compCtx.ActivePlan != "" {
			modeBuilder.WriteString("\n**PLAN MODE ACTIVE** — The following plan was approved and must be followed precisely:\n\n")
			modeBuilder.WriteString("```markdown\n")
			modeBuilder.WriteString(compCtx.ActivePlan)
			modeBuilder.WriteString("\n```\n")
			modeBuilder.WriteString("\nContinue implementing this plan. Do not deviate without explicit user approval.\n")
		}
		if modeBuilder.Len() > 0 {
			modeMsg := &conversation.Message{
				ID:        uuid.New().String(),
				Timestamp: now.Add(3 * time.Millisecond),
				Role:      conversation.RoleUser,
				Content:   modeBuilder.String(),
			}
			messages = append(messages, modeMsg)
		}
	}

	// ── Message 5: Recent verbatim messages (restoration) ──────────────
	// Mirrors Claude Code's getRecentMessagesVerbatim(): inject last few
	// user messages to provide local context/flow.
	if len(sourceMessages) > 0 {
		// Use the pre-compaction messages that were actually summarized, not
		// result.CompactedMessages (which is the OUTPUT of this very function
		// and is always nil/unset at call time — see BuildCompactedMessagesWithContext
		// callers, which only set result.CompactedMessages AFTER this call returns).
		recentUserMsgs := collectRecentUserMessages(sourceMessages, 5000)
		if len(recentUserMsgs) > 0 {
			var msgBuilder strings.Builder
			msgBuilder.WriteString("## Recent Messages\n\n")
			for _, m := range recentUserMsgs {
				msgBuilder.WriteString(m + "\n\n")
			}
			recentMsg := &conversation.Message{
				ID:        uuid.New().String(),
				Timestamp: now.Add(4 * time.Millisecond),
				Role:      conversation.RoleUser,
				Content:   msgBuilder.String(),
			}
			messages = append(messages, recentMsg)
		}
	}

	for _, msg := range messages {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any)
		}
		msg.Metadata[conversation.CompactionGeneratedMetadataKey] = true
	}
	return messages
}

// filterTodos returns todos with the given status
func filterTodos(todos []Todo, status string) []Todo {
	var filtered []Todo
	for _, t := range todos {
		if t.Status == status {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// fileExtension returns the file extension without the dot
func fileExtension(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i+1:]
		}
		if path[i] == '/' {
			break
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// State persistence — sync in-memory state ↔ conversation JSON
// ---------------------------------------------------------------------------

// SnapshotToConversation writes the service's in-memory file access records
// and the given CompactionContext (tasks, mode) into the conversation's
// CompactionState so they survive across restarts and compaction cycles.
func (s *Service) SnapshotToConversation(conv *conversation.Conversation, compCtx *CompactionContext) {
	cs := conv.EnsureCompactionState()

	// File access records
	cs.FileAccess = nil
	for _, record := range s.GetFileAccess() {
		cs.FileAccess = append(cs.FileAccess, conversation.FileAccessEntry{
			Path:        record.Path,
			ReadAt:      record.ReadAt,
			WriteAt:     record.WriteAt,
			AccessCount: record.AccessCount,
		})
	}

	// Tasks
	if compCtx != nil {
		cs.Tasks = nil
		for _, t := range compCtx.ActiveTodos {
			cs.Tasks = append(cs.Tasks, conversation.TaskEntry{
				Content:   t.Content,
				Status:    t.Status,
				DependsOn: t.DependsOn,
			})
		}
		for _, t := range compCtx.CompletedTodos {
			cs.Tasks = append(cs.Tasks, conversation.TaskEntry{
				Content:   t.Content,
				Status:    t.Status,
				DependsOn: t.DependsOn,
			})
		}

		// Mode context
		cs.ModeContext = &conversation.ModeContextSnapshot{
			CurrentMode:      compCtx.CurrentMode,
			ModeName:         compCtx.ModeName,
			ActiveMCPServers: compCtx.ActiveMCPServers,
			ActiveHooks:      compCtx.ActiveHooks,
			ModifiedFiles:    compCtx.ModifiedFiles,
			PlanModeActive:   compCtx.PlanModeActive,
			ActivePlan:       compCtx.ActivePlan,
			RootSessionID:    compCtx.RootSessionID,
		}
	}
}

// RestoreFromConversation populates the service's in-memory file access records
// and returns a CompactionContext from the conversation's persisted state.
// Returns nil if no CompactionState exists.
func (s *Service) RestoreFromConversation(conv *conversation.Conversation) *CompactionContext {
	cs := conv.CompactionState
	if cs == nil {
		return nil
	}

	// Restore file access records (merge — don't overwrite if we already have data)
	s.fileMu.Lock()
	for _, entry := range cs.FileAccess {
		if _, exists := s.fileAccess[entry.Path]; !exists {
			s.fileAccess[entry.Path] = &FileAccessRecord{
				Path:        entry.Path,
				ReadAt:      entry.ReadAt,
				WriteAt:     entry.WriteAt,
				AccessCount: entry.AccessCount,
			}
		}
	}
	s.fileMu.Unlock()

	// Reconstruct CompactionContext from persisted state
	compCtx := &CompactionContext{
		Strategy: StrategyStandard,
	}

	// Tasks
	for _, t := range cs.Tasks {
		todo := Todo{
			Content:   t.Content,
			Status:    t.Status,
			DependsOn: t.DependsOn,
		}
		if t.Status == "completed" {
			compCtx.CompletedTodos = append(compCtx.CompletedTodos, todo)
		} else {
			compCtx.ActiveTodos = append(compCtx.ActiveTodos, todo)
		}
	}

	// Mode context
	if cs.ModeContext != nil {
		compCtx.CurrentMode = cs.ModeContext.CurrentMode
		compCtx.ModeName = cs.ModeContext.ModeName
		compCtx.ActiveMCPServers = cs.ModeContext.ActiveMCPServers
		compCtx.ActiveHooks = cs.ModeContext.ActiveHooks
		compCtx.ModifiedFiles = cs.ModeContext.ModifiedFiles
		compCtx.PlanModeActive = cs.ModeContext.PlanModeActive
		compCtx.ActivePlan = cs.ModeContext.ActivePlan
		compCtx.RootSessionID = cs.ModeContext.RootSessionID
	}

	return compCtx
}

// GetFileAccess returns the current in-memory file access map (read-only snapshot).
func (s *Service) GetFileAccess() map[string]*FileAccessRecord {
	s.fileMu.RLock()
	defer s.fileMu.RUnlock()
	snapshot := make(map[string]*FileAccessRecord, len(s.fileAccess))
	for path, record := range s.fileAccess {
		cloned := *record
		snapshot[path] = &cloned
	}
	return snapshot
}

// EstimateTokens estimates token count from text (4 chars ≈ 1 token)
func EstimateTokens(text string) int {
	return int(float64(len(text)) * TokensPerChar)
}
