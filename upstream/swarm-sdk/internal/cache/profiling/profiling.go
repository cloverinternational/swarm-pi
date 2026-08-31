package profiling

import (
	"context"
)

// CacheProfiler is the main interface for cache profiling functionality.
// Implementations can detect, track, and report on cache breaks.
type CacheProfiler interface {
	// BeforeTranslation is called before translating messages to provider format.
	// It computes a hash of the messages and returns it for later comparison.
	// Returns the hash string and any error (errors should not block execution).
	BeforeTranslation(ctx context.Context, messages any) (string, error)

	// AfterTranslation is called after translating to provider format.
	// It compares the translated JSON against the hash from BeforeTranslation
	// to detect any cache instabilities.
	// preHash is the hash returned from BeforeTranslation.
	// translatedJSON is the serialized provider request (typically map[string]any).
	AfterTranslation(ctx context.Context, preHash string, translatedJSON any) error

	// TrackMutation is called when a message or related structure is modified.
	// This helps identify which code paths are causing cache breaks.
	TrackMutation(ctx context.Context, event *MutationEvent) error

	// RecordAPIResponse is called after receiving an API response.
	// It updates cache hit metrics and correlates with break events.
	RecordAPIResponse(ctx context.Context, metrics *APIMetrics) error

	// GetMetrics returns aggregated metrics for a conversation.
	// Returns nil if no data is available.
	Metrics(conversationID string) *CacheMetrics

	// GenerateReport creates a comprehensive analysis report for a conversation.
	GenerateReport(conversationID string) *CacheBreakReport

	// ExportMetrics exports all data in the specified format.
	// Format can be "json", "jsonl", or provider-specific names.
	ExportMetrics(ctx context.Context, conversationID string, format string) (any, error)

	// Reset clears all data for a conversation (optional).
	// Useful for testing or when starting a new conversation.
	Reset(conversationID string) error
}

// MutationEvent tracks when a message is modified
type MutationEvent struct {
	// Identification
	MessageID      string
	ConversationID string
	TurnNumber     int

	// Mutation details
	Type           string // "LOAD", "SAVE", "COMPACTION", "HOOK", "TOOL_CALL"
	Location       string // File:Line where mutation occurred
	BeforeContent  any
	AfterContent   any
	FieldModified  string // e.g., "Content", "ToolCalls", "Metadata"
	ReasonModified string // Human-readable reason

	// Stack trace
	StackTrace []StackFrame
}
