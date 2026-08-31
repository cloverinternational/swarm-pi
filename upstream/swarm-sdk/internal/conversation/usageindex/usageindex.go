// Package usageindex maintains a derived, incrementally refreshed index of
// provider-reported token usage recorded in persisted conversation JSON.
//
// Conversation files remain the source of truth. The database only caches what
// was already parsed out of them, keyed by (path, size, mtime), so a refresh
// re-reads only the conversations that actually changed. Deleting the database
// is always safe: the next sync rebuilds it from disk.
package usageindex

import (
	"errors"
	"time"
)

// SchemaVersion is bumped whenever the stored columns or their meaning change.
// A mismatch drops and rebuilds the tables rather than migrating: everything
// here is derived data that can be recomputed from conversation JSON.
const SchemaVersion = 2

var ErrUnavailable = errors.New("conversation usage index unavailable")

type Backend string

const (
	BackendAuto   Backend = "auto"
	BackendTurso  Backend = "turso"
	BackendSQLite Backend = "sqlite"
)

type OpenOptions struct {
	Backend    Backend
	TursoPath  string
	SQLitePath string
}

// CacheSchema records which persisted layout a response used. The two layouts
// disagree about whether tokens.input already contains cache tokens, so the
// distinction has to survive normalization for tests and diagnostics.
type CacheSchema int

const (
	// CacheSchemaNone means the response reported no prompt-cache activity.
	CacheSchemaNone CacheSchema = 0
	// CacheSchemaTokens is the current SDK layout: tokens.cache_creation and
	// tokens.cache_read are populated and tokens.input EXCLUDES both.
	CacheSchemaTokens CacheSchema = 1
	// CacheSchemaMetadata is the older layout: cache detail lives only under
	// message.metadata.cache_metrics and tokens.input ALREADY INCLUDES it.
	CacheSchemaMetadata CacheSchema = 2
)

// Response is one provider reply normalized into disjoint token buckets, so
// that FreshInput + CacheWrite + CacheRead + Output + Other == Total and no
// dimension is counted twice regardless of which persisted layout produced it.
type Response struct {
	Seq            int
	DedupKey       string
	LineageScoped  bool
	ConversationID string
	Timestamp      time.Time

	FreshInput   int64
	CacheWrite   int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
	Output       int64
	// Other holds provider totals not broken out above (for example Gemini
	// thought tokens). Keeping the remainder preserves the provider's own
	// total instead of silently dropping or double counting it.
	Other  int64
	Total  int64
	Schema CacheSchema
}

// InputContext is the full input side of one call: what the provider had to
// process, whether it came fresh, from a cache write, or from a cache read.
func (r Response) InputContext() int64 {
	return r.FreshInput + r.CacheWrite + r.CacheRead
}

// FileState is the cheap identity of a conversation file. A stat-only walk
// produces it in milliseconds, which is what makes incremental sync possible.
type FileState struct {
	Path      string
	Size      int64
	ModTimeNS int64
}

// FileRecord is everything the index stores about one parsed conversation file.
type FileRecord struct {
	FileState
	ConversationID string
	ParentID       string
	TotalTokens    int64
	// Unreadable marks a file that could not be parsed. It is still recorded so
	// a broken file is not re-read on every single refresh.
	Unreadable bool
	Responses  []Response
}

// Summary is the deduplicated roll-up presented by the consumption view.
type Summary struct {
	FreshInput  int64
	CacheWrite  int64
	CacheRead   int64
	Output      int64
	Other       int64
	TotalTokens int64

	ConversationCount  int64
	ResponseCount      int64
	DuplicateFiles     int64
	DuplicateResponses int64
	UnreadableFiles    int64

	// CacheCapable* restrict the totals to responses from a provider that
	// actually reports prompt-cache activity (Schema != CacheSchemaNone).
	// A blended cache rate is misleading the moment a non-caching provider is
	// in the mix: its input lands entirely in FreshInput and drags the ratio
	// down, which looks exactly like a cache regression that never happened.
	CacheCapableResponses  int64
	CacheCapableFreshInput int64
	CacheCapableCacheWrite int64
	CacheCapableCacheRead  int64
}

// InputTokens is the full input context across every counted response.
func (s Summary) InputTokens() int64 { return s.FreshInput + s.CacheWrite + s.CacheRead }

// CacheHitRate is the share of cache-capable input served from cache, in
// [0,1]. Responses from providers that report no cache activity are excluded
// from both sides of the ratio. Returns 0 when nothing cache-capable was seen.
func (s Summary) CacheHitRate() float64 {
	total := s.CacheCapableFreshInput + s.CacheCapableCacheWrite + s.CacheCapableCacheRead
	if total <= 0 {
		return 0
	}
	return float64(s.CacheCapableCacheRead) / float64(total)
}

// CacheCapableShare is the fraction of counted responses that came from a
// cache-reporting provider, i.e. how much of the traffic the hit rate speaks
// for. A low share means the hit rate describes a minority of the work.
func (s Summary) CacheCapableShare() float64 {
	if s.ResponseCount <= 0 {
		return 0
	}
	return float64(s.CacheCapableResponses) / float64(s.ResponseCount)
}

// CacheHitRatio is the share of input context served from the prompt cache.
func (s Summary) CacheHitRatio() float64 {
	input := s.InputTokens()
	if input <= 0 {
		return 0
	}
	return float64(s.CacheRead) / float64(input)
}

// DayBucket is one day of deduplicated usage, used for the trend view.
type DayBucket struct {
	Day        time.Time
	FreshInput int64
	CacheWrite int64
	CacheRead  int64
	Output     int64
	Responses  int64
	// CacheCapable* mirror the Summary fields for one day.
	CacheCapableResponses  int64
	CacheCapableFreshInput int64
	CacheCapableCacheWrite int64
	CacheCapableCacheRead  int64
}

// InputContext is the input side of a day's usage.
func (d DayBucket) InputContext() int64 { return d.FreshInput + d.CacheWrite + d.CacheRead }

// CacheHitRate is the day's cache-capable hit rate in [0,1].
func (d DayBucket) CacheHitRate() float64 {
	total := d.CacheCapableFreshInput + d.CacheCapableCacheWrite + d.CacheCapableCacheRead
	if total <= 0 {
		return 0
	}
	return float64(d.CacheCapableCacheRead) / float64(total)
}

type FindingKind string

const (
	FindingSpike      FindingKind = "spike"
	FindingCacheBreak FindingKind = "cache_break"
)

type FindingCause string

const (
	CauseUnknown        FindingCause = ""
	CauseTTLExpiry      FindingCause = "ttl_expiry"
	CausePrefixMutation FindingCause = "prefix_mutation"
)

// Finding is one detected anomaly: an input spike, or a cache break where a
// prefix that should have been read from cache was paid for again.
type Finding struct {
	Kind           FindingKind
	ConversationID string
	Timestamp      time.Time
	// Tokens is the input context for a spike, or the re-paid cache write for
	// a cache break.
	Tokens int64
	// Ratio is how many times the rolling median a spike reached.
	Ratio float64
	// GapSeconds is the wall-clock gap since the previous turn, which is what
	// separates a TTL expiry from a prefix mutation.
	GapSeconds int64
	Cause      FindingCause
}

// SyncStats reports what a refresh actually had to do. FilesParsed is the
// number that matters: on an unchanged store it must be zero.
type SyncStats struct {
	FilesScanned    int64
	FilesParsed     int64
	FilesRemoved    int64
	BytesParsed     int64
	UnreadableFiles int64
	Changed         bool
	Duration        time.Duration
}
