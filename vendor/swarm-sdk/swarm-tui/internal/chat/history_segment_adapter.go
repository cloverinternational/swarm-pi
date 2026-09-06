package chat

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/searchindex"
)

// historySegmentIndex adapts the on-disk search index to the narrow segment
// interface HistorySearch accepts.
//
// It exists because the tool is constructed once at startup while the index is
// opened per call. Holding an open handle for the process lifetime would keep a
// SQLite connection (and its page cache) alive for a feature most sessions
// never use, and would have to be closed on shutdown paths that do not exist.
// Opening per call costs ~13ms, which is the same trade indexedHistorySearch
// already makes.
//
// Without this adapter the segment parameters are wired end to end everywhere
// EXCEPT production: the tool would be built with a nil segment searcher and
// every tool_name / tool_outcome / stats request would fail with "segment index
// unavailable" even though the index beneath it was fully populated.
type historySegmentIndex struct{}

// withSegmentEngine opens the index, runs fn, and always closes it. Every
// method below is a one-shot query, so this keeps the open/close pairing in one
// place instead of repeating a defer in each.
func withSegmentEngine[T any](fn func(searchindex.SegmentEngine) (T, error)) (T, error) {
	var zero T
	engine, err := openHistoryIndex()
	if err != nil {
		return zero, err
	}
	defer func() { _ = engine.Close() }()
	segmentEngine, ok := engine.(searchindex.SegmentEngine)
	if !ok {
		return zero, searchindex.ErrSegmentsUnsupported
	}
	return fn(segmentEngine)
}

func (historySegmentIndex) SearchSegments(ctx context.Context, query searchindex.SegmentQuery) ([]searchindex.SegmentHit, error) {
	return withSegmentEngine(func(e searchindex.SegmentEngine) ([]searchindex.SegmentHit, error) {
		return e.SearchSegments(ctx, query)
	})
}

func (historySegmentIndex) TermStats(ctx context.Context, filter searchindex.SegmentFilter, opts searchindex.StatsOptions) ([]searchindex.TermStat, error) {
	return withSegmentEngine(func(e searchindex.SegmentEngine) ([]searchindex.TermStat, error) {
		return e.TermStats(ctx, filter, opts)
	})
}

func (historySegmentIndex) SegmentCount(ctx context.Context, filter searchindex.SegmentFilter) (int, error) {
	return withSegmentEngine(func(e searchindex.SegmentEngine) (int, error) {
		return e.SegmentCount(ctx, filter)
	})
}
