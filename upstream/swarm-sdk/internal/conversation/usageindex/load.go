package usageindex

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// defaultSyncTimeout bounds how long Load waits on the index backend before
// giving up and falling back to a full filesystem scan.
//
// The index database is a single file shared by every swarm process on a
// machine. If another process holds a write lock while it is itself slow (a
// large first build, for example) or — worse — a native driver's lock
// acquisition does not honor Go context cancellation because it is blocked
// inside a C/Rust call rather than a cancellable Go operation, Sync could
// otherwise block forever. That previously surfaced as the Usage screen
// showing "Refreshing usage data…" indefinitely, because the goroutine that
// would send the completion message never returned. This timeout guarantees
// Load always returns.
const defaultSyncTimeout = 20 * time.Second

// Snapshot is everything the consumption view needs from one refresh.
type Snapshot struct {
	Summary Summary
	Daily   []DayBucket
	Spikes  []Finding
	Breaks  []Finding
	Backend Backend
	Stats   SyncStats
	// Degraded is set when no index backend could be opened, or it did not
	// finish within SyncTimeout, and the numbers came from a full in-memory
	// scan instead. Everything still works, it is just slow, and the view
	// can say so.
	Degraded bool
	// DegradedReason explains why the index was unavailable.
	DegradedReason string
}

// LoadOptions configures a refresh.
type LoadOptions struct {
	OpenOptions
	// Days of history for the trend series.
	Days int
	// Findings is how many spikes and cache breaks to return.
	Findings int
	// SyncTimeout bounds the indexed path (open + sync + read) before Load
	// falls back to a full filesystem scan. Zero uses defaultSyncTimeout.
	SyncTimeout time.Duration
}

// DefaultConversationRoot is the SDK's standard conversation directory.
func DefaultConversationRoot() (string, error) {
	return paths.ConversationsDir(), nil
}

// Load refreshes the index and returns the consumption snapshot. When no index
// backend can be opened, or the indexed path does not finish within
// SyncTimeout, it falls back to a full filesystem scan so the view degrades
// in speed rather than hanging or disappearing. The fallback scan touches no
// shared database, so it cannot itself be blocked by another process's lock.
func Load(ctx context.Context, root string, options LoadOptions) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		// Nothing to do: opening a database connection for an already-dead
		// context would just be wasted work racing against nothing.
		return Snapshot{}, err
	}
	if options.Days <= 0 {
		options.Days = 14
	}
	if options.Findings <= 0 {
		options.Findings = 10
	}
	timeout := options.SyncTimeout
	if timeout <= 0 {
		timeout = defaultSyncTimeout
	}
	if strings.TrimSpace(options.TursoPath) == "" && strings.TrimSpace(options.SQLitePath) == "" {
		turso, sqlite, err := DefaultPaths()
		if err != nil {
			return Snapshot{}, err
		}
		options.TursoPath, options.SQLitePath = turso, sqlite
	}

	boundedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type loaded struct {
		snapshot Snapshot
		backend  Backend
	}
	result, err, timedOut := boundedCall(boundedCtx, func(ctx context.Context) (loaded, error) {
		snapshot, backend, err := loadIndexed(ctx, root, options)
		return loaded{snapshot, backend}, err
	})
	if !timedOut && err == nil {
		result.snapshot.Backend = result.backend
		return result.snapshot, nil
	}

	var indexErr error
	if timedOut {
		if callerErr := ctx.Err(); callerErr != nil {
			// The caller's own context ended (not our added timeout): honor
			// that directly instead of masking it with a fallback scan.
			return Snapshot{}, callerErr
		}
		indexErr = fmt.Errorf("usage index timed out after %s (a concurrent process may be holding a lock): %w",
			timeout, err)
	} else {
		indexErr = err
	}

	snapshot, scanErr := ScanWithoutIndex(ctx, root, options.Findings)
	if scanErr != nil {
		return Snapshot{}, scanErr
	}
	snapshot.Degraded = true
	snapshot.DegradedReason = indexErr.Error()
	return snapshot, nil
}

// boundedCall runs fn in its own goroutine and races its result against
// ctx.Done(). It always returns by the time ctx is done, even if fn itself
// ignores cancellation — which matters for drivers whose blocking calls are
// implemented in native code that Go's context cannot preempt. When ctx wins
// the race, timedOut is true and fn's goroutine is abandoned: it may still be
// running, but that is a contained background cost, never a caller-visible
// hang.
func boundedCall[T any](ctx context.Context, fn func(context.Context) (T, error)) (value T, err error, timedOut bool) {
	type outcome struct {
		value T
		err   error
	}
	resultCh := make(chan outcome, 1)
	go func() {
		v, e := fn(ctx)
		resultCh <- outcome{v, e}
	}()
	select {
	case result := <-resultCh:
		return result.value, result.err, false
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err(), true
	}
}

// loadIndexed is the indexed-path body: open the backend, sync it, and read
// back the aggregates. Callers should bound it with a timeout — see Load.
func loadIndexed(ctx context.Context, root string, options LoadOptions) (Snapshot, Backend, error) {
	store, backend, err := Open(ctx, options.OpenOptions)
	if err != nil {
		return Snapshot{}, "", err
	}
	defer store.Close()

	stats, err := store.Sync(ctx, root)
	if err != nil {
		return Snapshot{}, "", err
	}
	summary, err := store.Summary(ctx)
	if err != nil {
		return Snapshot{}, "", err
	}
	daily, err := store.Daily(ctx, options.Days)
	if err != nil {
		return Snapshot{}, "", err
	}
	spikes, err := store.Findings(ctx, FindingSpike, options.Findings)
	if err != nil {
		return Snapshot{}, "", err
	}
	breaks, err := store.Findings(ctx, FindingCacheBreak, options.Findings)
	if err != nil {
		return Snapshot{}, "", err
	}
	return Snapshot{
		Summary: summary, Daily: daily, Spikes: spikes, Breaks: breaks, Stats: stats,
	}, backend, nil
}

// ScanWithoutIndex reproduces a full snapshot straight from conversation JSON,
// using the same parsing, deduplication and detection as the indexed path. It
// exists so a missing or unusable database never changes the numbers, only the
// time they take to produce.
func ScanWithoutIndex(ctx context.Context, root string, findingLimit int) (Snapshot, error) {
	if strings.TrimSpace(root) == "" {
		return Snapshot{}, errEmptyRoot
	}
	files, err := walkConversationFiles(ctx, root)
	if err != nil {
		return Snapshot{}, err
	}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	rows := make([]fileRow, 0, len(paths))
	responses := make(map[string][]Response, len(paths))
	var stats SyncStats
	stats.FilesScanned = int64(len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		state := files[path]
		row := fileRow{Path: path}
		parsed, parseErr := parseConversationFile(path, state.Size)
		if parseErr != nil {
			row.Unreadable = true
			row.ConversationID = path
			rows = append(rows, row)
			stats.UnreadableFiles++
			continue
		}
		row.ConversationID = strings.TrimSpace(parsed.ID)
		if row.ConversationID == "" {
			row.ConversationID = path
			for index := range parsed.Responses {
				parsed.Responses[index].ConversationID = row.ConversationID
			}
		}
		row.ParentID = parsed.ParentID
		row.ResponseCount = int64(len(parsed.Responses))
		row.TotalTokens = parsed.TotalTokens
		rows = append(rows, row)
		responses[path] = parsed.Responses
		stats.FilesParsed++
		stats.BytesParsed += state.Size
	}

	base, updates := resolveFiles(rows)
	resolution := make(map[string]fileRow, len(updates))
	for _, update := range updates {
		resolution[update.Path] = update
	}

	summary, daily, findings, err := aggregate(ctx, base, func(visit func(aggregateRow) error) error {
		for _, path := range paths {
			resolved, exists := resolution[path]
			if !exists || !resolved.Active {
				continue
			}
			for _, response := range responses[path] {
				ts := int64(0)
				if !response.Timestamp.IsZero() {
					ts = response.Timestamp.Unix()
				}
				if err := visit(aggregateRow{
					ConversationID: response.ConversationID,
					LineageRoot:    resolved.LineageRoot,
					DedupKey:       response.DedupKey,
					LineageScoped:  response.LineageScoped,
					Seq:            response.Seq,
					TS:             ts,
					Day:            dayOf(ts),
					FreshInput:     response.FreshInput,
					CacheWrite:     response.CacheWrite,
					CacheWrite5m:   response.CacheWrite5m,
					CacheWrite1h:   response.CacheWrite1h,
					CacheRead:      response.CacheRead,
					Output:         response.Output,
					Other:          response.Other,
					Total:          response.Total,
					Schema:         response.Schema,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	stats.Changed = true
	return Snapshot{
		Summary: summary,
		Daily:   daily,
		Spikes:  limitFindings(findings, FindingSpike, findingLimit),
		Breaks:  limitFindings(findings, FindingCacheBreak, findingLimit),
		Stats:   stats,
	}, nil
}

func limitFindings(findings []Finding, kind FindingKind, limit int) []Finding {
	if limit <= 0 {
		limit = 10
	}
	matched := make([]Finding, 0, limit)
	for _, finding := range findings {
		if finding.Kind != kind {
			continue
		}
		matched = append(matched, finding)
		if len(matched) == limit {
			break
		}
	}
	return matched
}
