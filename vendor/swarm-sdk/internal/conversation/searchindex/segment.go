package searchindex

import (
	"context"
	"time"
)

// Segment is one indexable unit of a conversation.
//
// The original index stored ONE row per conversation with every message
// flattened into a single body blob, which made three things impossible:
// attributing a match to a particular tool call, filtering to failed calls, and
// indexing a conversation larger than MaxConversationFileSize at all. It also
// silently dropped tool calls and tool results entirely, so nothing a tool did
// or returned was ever searchable.
//
// Segments fix all of that by preserving the structure the flattening threw
// away, and they make file size a non-issue: a huge conversation becomes many
// small rows rather than one oversized document.
type Segment struct {
	// ConversationID is the owning conversation.
	ConversationID string
	// WorkspacePath is denormalized from the conversation so segment queries
	// can filter by workspace without a join.
	WorkspacePath string
	// Ordinal is the segment's position within the conversation, assigned in
	// traversal order. It makes results reproducible and lets a caller ask for
	// the segments surrounding a hit.
	Ordinal int
	// MessageID is the message this segment came from.
	MessageID string
	// Role is the message role: user, assistant, tool, or system.
	Role string
	// Kind distinguishes prose from tool traffic. See the SegmentKind values.
	Kind string
	// ToolName is the tool this segment belongs to, empty for plain messages.
	ToolName string
	// CallID links a SegmentToolCall to its SegmentToolResult.
	CallID string
	// Failed reports that a tool result carried an error. It is only
	// meaningful for SegmentToolResult.
	Failed bool
	// Runtime marks a runtime-injected pseudo-message. A user role is not
	// proof of human authorship, so vocabulary analysis of what a PERSON said
	// must be able to exclude these.
	Runtime bool
	// Timestamp is the owning message's timestamp.
	Timestamp time.Time
	// Text is the cleaned, bounded searchable text of this segment.
	Text string
}

// Segment kinds.
const (
	SegmentMessage    = "message"
	SegmentToolCall   = "tool_call"
	SegmentToolResult = "tool_result"
)

// Tool outcome filters for SegmentFilter.Outcome.
const (
	OutcomeAny       = ""
	OutcomeFailed    = "failed"
	OutcomeSucceeded = "succeeded"
)

// MaxSegmentTextBytes bounds a single segment's indexed text. A tool result can
// be megabytes; indexing all of it would reproduce the memory problem segments
// exist to solve, and terms beyond this point add little to vocabulary
// statistics. Text is truncated on a UTF-8 boundary.
const MaxSegmentTextBytes = 64 << 10

// MaxSegmentsPerConversation bounds how many segments one conversation may
// contribute, so a single pathological transcript cannot dominate the index or
// the statistics computed from it.
const MaxSegmentsPerConversation = 20000

// SegmentFilter selects a subset of segments. Every zero value means "do not
// filter on this field", so the zero SegmentFilter selects everything.
type SegmentFilter struct {
	WorkspacePath  string
	ConversationID string
	// ToolName restricts to one tool, e.g. "Bash".
	ToolName string
	// Kind restricts to one SegmentKind.
	Kind string
	// Role restricts to one message role.
	Role string
	// Outcome is OutcomeAny, OutcomeFailed, or OutcomeSucceeded. It implies
	// Kind == SegmentToolResult, because only a result has an outcome.
	Outcome string
	// ExcludeRuntime drops runtime-injected pseudo-messages.
	ExcludeRuntime bool
	// Since and Until bound the timestamp range when non-zero.
	Since time.Time
	Until time.Time
}

// SegmentQuery searches segment text.
type SegmentQuery struct {
	Filter SegmentFilter
	// Text is an FTS5 match expression. Empty matches every segment allowed by
	// Filter.
	Text string
	// Limit bounds returned hits. Zero means DefaultSegmentLimit.
	Limit int
}

// DefaultSegmentLimit is used when SegmentQuery.Limit is zero.
const DefaultSegmentLimit = 50

// SegmentHit is one segment matched by SearchSegments.
type SegmentHit struct {
	Segment
	// Score is the FTS rank; higher is more relevant.
	Score float64
}

// StatsOptions controls vocabulary and phrase statistics.
type StatsOptions struct {
	// NGram is the phrase length in tokens. 1 is a vocabulary count; 2 and 3
	// are the useful phrase sizes. Values below 1 are treated as 1.
	NGram int
	// Top bounds how many terms are returned, highest count first. Zero means
	// DefaultStatsTop.
	Top int
	// MinCount drops terms occurring fewer than this many times.
	MinCount int
	// MinTermLength drops short tokens, which are almost always noise in a
	// frequency list.
	MinTermLength int
	// ExcludeStopwords drops a small built-in English stopword list. It is off
	// by default because a stopword list is a judgement call and the caller
	// doing statistics may specifically care about function words.
	ExcludeStopwords bool
}

// DefaultStatsTop is used when StatsOptions.Top is zero.
const DefaultStatsTop = 50

// MaxStatsTop caps StatsOptions.Top. Frequency tables are verbose and an
// unbounded one is unusable in a terminal and ruinous in a context window.
const MaxStatsTop = 500

// MaxStatsNGram caps StatsOptions.NGram. Beyond three tokens, counts become so
// sparse that the result is a list of unique sentences.
const MaxStatsNGram = 5

// TermStat is one row of a frequency table.
type TermStat struct {
	// Term is the token, or the space-joined n-gram.
	Term string
	// Occurrences is the total number of times the term appears.
	Occurrences int
	// Segments is the number of distinct segments containing the term, which
	// distinguishes "said once in a thousand places" from "said a thousand
	// times in one place".
	Segments int
	// Conversations is the number of distinct conversations containing it.
	Conversations int
}

// SegmentEngine is implemented by an index that stores segments. It is separate
// from Engine so an engine that predates segments still satisfies Engine.
type SegmentEngine interface {
	// ReplaceSegments atomically replaces every segment of one conversation.
	// Replacing rather than merging keeps the index consistent with a
	// conversation that shrank, and makes re-indexing idempotent.
	ReplaceSegments(ctx context.Context, conversationID string, segments []Segment) error
	// DeleteSegments removes every segment of one conversation.
	DeleteSegments(ctx context.Context, conversationID string) error
	// SearchSegments returns segments matching a query, most relevant first.
	SearchSegments(ctx context.Context, query SegmentQuery) ([]SegmentHit, error)
	// TermStats returns a frequency table over the segments selected by filter.
	TermStats(ctx context.Context, filter SegmentFilter, opts StatsOptions) ([]TermStat, error)
	// SegmentCount reports how many segments match a filter, so a caller can
	// tell an empty result from an empty corpus.
	SegmentCount(ctx context.Context, filter SegmentFilter) (int, error)
}
