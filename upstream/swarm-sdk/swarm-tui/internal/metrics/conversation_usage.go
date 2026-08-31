package metrics

import (
	"context"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/usageindex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// ConversationUsageSummary is a deduplicated roll-up of provider-reported usage
// already persisted on assistant messages in the conversation store. Every
// tool-loop turn contributes its full input context again, matching what
// providers process and bill.
//
// The input side is split into the three things that are billed differently and
// behave differently: tokens sent fresh, tokens written into the prompt cache,
// and tokens read back out of it.
type ConversationUsageSummary struct {
	FreshInput         int64
	CacheWrite         int64
	CacheRead          int64
	InputTokens        int64
	OutputTokens       int64
	OtherTokens        int64
	TotalTokens        int64
	ConversationCount  int64
	ResponseCount      int64
	DuplicateFiles     int64
	DuplicateResponses int64
	UnreadableFiles    int64
	// CacheCapable* cover only responses from providers that report prompt-cache
	// activity. Mixing a non-caching provider into the ratio makes the cache look
	// broken when it is not: all of its input counts as uncached.
	CacheCapableResponses  int64
	CacheCapableFreshInput int64
	CacheCapableCacheWrite int64
	CacheCapableCacheRead  int64
}

// CacheHitRatio is the share of CACHE-CAPABLE input context served from cache.
//
// Responses from providers that report no cache activity are excluded from
// both sides. Including them produced a phantom regression in the real index:
// a month whose blended rate read 60.7% had an actual Anthropic rate of 93.9%,
// the difference being 100,197 responses from a non-caching provider.
func (s ConversationUsageSummary) CacheHitRatio() float64 {
	total := s.CacheCapableFreshInput + s.CacheCapableCacheWrite + s.CacheCapableCacheRead
	if total <= 0 {
		return 0
	}
	return float64(s.CacheCapableCacheRead) / float64(total)
}

// CacheCapableShare is the fraction of responses the hit rate speaks for.
// Well below 1 means most traffic came from a provider without prompt caching.
func (s ConversationUsageSummary) CacheCapableShare() float64 {
	if s.ResponseCount <= 0 {
		return 0
	}
	return float64(s.CacheCapableResponses) / float64(s.ResponseCount)
}

// ConversationUsageSnapshot is one refresh of the consumption view: the
// roll-up, the recent trend, and the anomalies worth showing.
type ConversationUsageSnapshot struct {
	Summary ConversationUsageSummary
	Daily   []usageindex.DayBucket
	Spikes  []usageindex.Finding
	Breaks  []usageindex.Finding

	// Backend names the index engine that answered, for diagnostics.
	Backend string
	// Degraded means the index could not be opened and the numbers came from a
	// full scan: identical results, far slower.
	Degraded       bool
	DegradedReason string
	// FilesParsed is how many conversation files this refresh had to open.
	// On an unchanged store it is zero, which is the point of the index.
	FilesParsed  int64
	FilesScanned int64
	SyncDuration time.Duration
}

// DefaultConversationStorageDir returns the SDK's standard global
// conversation directory. Callers using custom SDK storage should pass that
// directory directly instead.
func DefaultConversationStorageDir() (string, error) {
	return paths.ConversationsDir(), nil
}

// summaryFrom converts the index roll-up into the view's shape.
func summaryFrom(summary usageindex.Summary) ConversationUsageSummary {
	return ConversationUsageSummary{
		FreshInput:             summary.FreshInput,
		CacheWrite:             summary.CacheWrite,
		CacheRead:              summary.CacheRead,
		InputTokens:            summary.InputTokens(),
		OutputTokens:           summary.Output,
		OtherTokens:            summary.Other,
		TotalTokens:            summary.TotalTokens,
		ConversationCount:      summary.ConversationCount,
		ResponseCount:          summary.ResponseCount,
		DuplicateFiles:         summary.DuplicateFiles,
		DuplicateResponses:     summary.DuplicateResponses,
		UnreadableFiles:        summary.UnreadableFiles,
		CacheCapableResponses:  summary.CacheCapableResponses,
		CacheCapableFreshInput: summary.CacheCapableFreshInput,
		CacheCapableCacheWrite: summary.CacheCapableCacheWrite,
		CacheCapableCacheRead:  summary.CacheCapableCacheRead,
	}
}

// LoadConversationUsage refreshes the usage index and returns everything the
// consumption view renders. The index is keyed by file size and modification
// time, so an unchanged conversation store costs a directory walk instead of
// re-reading gigabytes of JSON.
func LoadConversationUsage(ctx context.Context, root string) (ConversationUsageSnapshot, error) {
	snapshot, err := usageindex.Load(ctx, root, usageindex.LoadOptions{
		OpenOptions: usageindex.OpenOptions{Backend: usageIndexBackend()},
		Days:        usageTrendDays,
		Findings:    usageFindingLimit,
	})
	if err != nil {
		return ConversationUsageSnapshot{}, err
	}
	return ConversationUsageSnapshot{
		Summary:        summaryFrom(snapshot.Summary),
		Daily:          snapshot.Daily,
		Spikes:         snapshot.Spikes,
		Breaks:         snapshot.Breaks,
		Backend:        string(snapshot.Backend),
		Degraded:       snapshot.Degraded,
		DegradedReason: snapshot.DegradedReason,
		FilesParsed:    snapshot.Stats.FilesParsed,
		FilesScanned:   snapshot.Stats.FilesScanned,
		SyncDuration:   snapshot.Stats.Duration,
	}, nil
}

// ScanConversationUsage reconstructs historical consumption directly from the
// conversation files, without touching the index. It is the fallback path and
// produces identical numbers, so the two can be compared in tests.
func ScanConversationUsage(ctx context.Context, root string) (ConversationUsageSummary, error) {
	snapshot, err := usageindex.ScanWithoutIndex(ctx, root, usageFindingLimit)
	if err != nil {
		return ConversationUsageSummary{}, err
	}
	return summaryFrom(snapshot.Summary), nil
}

const (
	// usageTrendDays is how much history the consumption trend shows.
	usageTrendDays = 14
	// usageFindingLimit is how many spikes and cache breaks are surfaced.
	usageFindingLimit = 10
)

// usageIndexBackend lets an operator pin the engine, mirroring the history
// search index's SWARM_HISTORY_INDEX_BACKEND override.
//
// Unlike the history search index, this defaults to SQLite rather than
// BackendAuto (Turso-first). The usage index is a single file shared by every
// swarm process on a machine, and multiple concurrent TUI instances are a
// normal, expected setup here — not an edge case. SQLite's WAL mode plus
// busy_timeout has decades of validation for exactly that pattern: a writer
// blocks a bounded time, then fails, instead of hanging. Turso's native
// engine is newer and its multi-process lock behavior under contention is not
// proven the same way; a stuck lock acquisition inside its native code cannot
// be preempted by Go's context cancellation, which previously produced an
// indefinite "Refreshing usage data…" hang when several swarm processes were
// open at once. Set SWARM_USAGE_INDEX_BACKEND=turso to opt back in.
func usageIndexBackend() usageindex.Backend {
	switch os.Getenv("SWARM_USAGE_INDEX_BACKEND") {
	case "turso":
		return usageindex.BackendTurso
	case "auto":
		return usageindex.BackendAuto
	case "sqlite":
		return usageindex.BackendSQLite
	default:
		return usageindex.BackendSQLite
	}
}
