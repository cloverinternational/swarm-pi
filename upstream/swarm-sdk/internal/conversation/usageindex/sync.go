package usageindex

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// DefaultPaths returns the standard index locations, alongside the history
// search index and inside the conversation store it describes.
func DefaultPaths() (tursoPath string, sqlitePath string, err error) {
	dir := filepath.Join(paths.ConversationsDir(), "_index")
	return filepath.Join(dir, "usage-index.turso"), filepath.Join(dir, "usage-index.sqlite"), nil
}

// errEmptyRoot is returned when no conversation directory was supplied.
var errEmptyRoot = errors.New("conversation storage directory is empty")

// Sync writes parsed files in batches. One transaction per file meant one
// fsync per file, which dominated the first build of a large store.
const (
	syncBatchFiles     = 256
	syncBatchResponses = 20_000
)

// Sync brings the index up to date with the conversation store.
//
// The expensive part of the old scanner was re-reading 7 GB of JSON on every
// refresh. Here a stat-only walk decides what changed, and only those files are
// opened; an unchanged store costs one directory walk and nothing else.
func (s *Store) Sync(ctx context.Context, root string) (SyncStats, error) {
	started := time.Now()
	var stats SyncStats
	if strings.TrimSpace(root) == "" {
		return stats, errEmptyRoot
	}

	onDisk, walkErr := walkConversationFiles(ctx, root)
	if walkErr != nil {
		return stats, walkErr
	}
	stats.FilesScanned = int64(len(onDisk))

	indexed, err := s.FileStates(ctx)
	if err != nil {
		return stats, err
	}

	batch := make([]FileRecord, 0, syncBatchFiles)
	batched := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := s.ReplaceFiles(ctx, batch); err != nil {
			return err
		}
		for index := range batch {
			batch[index] = FileRecord{}
		}
		batch = batch[:0]
		batched = 0
		return nil
	}

	for path, state := range onDisk {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		previous, exists := indexed[path]
		if exists && previous.Size == state.Size && previous.ModTimeNS == state.ModTimeNS {
			continue
		}
		record := FileRecord{FileState: state}
		parsed, parseErr := parseConversationFile(path, state.Size)
		if parseErr != nil {
			record.Unreadable = true
		} else {
			record.ConversationID = strings.TrimSpace(parsed.ID)
			if record.ConversationID == "" {
				// Files without an ID still need a stable identity so duplicate
				// resolution has something to group on.
				record.ConversationID = path
				for index := range parsed.Responses {
					parsed.Responses[index].ConversationID = record.ConversationID
				}
			}
			record.ParentID = parsed.ParentID
			record.Responses = parsed.Responses
			record.TotalTokens = parsed.TotalTokens
		}
		batch = append(batch, record)
		batched += len(record.Responses)
		stats.FilesParsed++
		stats.BytesParsed += state.Size
		if record.Unreadable {
			stats.UnreadableFiles++
		}
		stats.Changed = true
		if len(batch) >= syncBatchFiles || batched >= syncBatchResponses {
			if err := flush(); err != nil {
				return stats, err
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}

	var removed []string
	for path := range indexed {
		if _, exists := onDisk[path]; !exists {
			removed = append(removed, path)
		}
	}
	if len(removed) > 0 {
		sort.Strings(removed)
		if err := s.RemoveFiles(ctx, removed); err != nil {
			return stats, err
		}
		stats.FilesRemoved = int64(len(removed))
		stats.Changed = true
	}

	needsAggregates := stats.Changed
	if !needsAggregates {
		// Recompute anyway when the roll-up is missing, for example after a
		// schema rebuild that kept no aggregates.
		summary, err := s.Summary(ctx)
		if err != nil {
			return stats, err
		}
		needsAggregates = summary.ResponseCount == 0 && len(onDisk) > 0
	}
	if needsAggregates {
		if err := s.recompute(ctx); err != nil {
			return stats, err
		}
	}
	stats.Duration = time.Since(started)
	return stats, nil
}

func walkConversationFiles(ctx context.Context, root string) (map[string]FileState, error) {
	files := make(map[string]FileState)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "debug", "metadata", "_index":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		files[path] = FileState{Path: path, Size: info.Size(), ModTimeNS: info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// recompute resolves duplicate conversation copies, walks the deduplicated
// responses once, and stores the roll-up plus detected findings.
func (s *Store) recompute(ctx context.Context) error {
	rows, err := s.loadFileRows(ctx)
	if err != nil {
		return err
	}
	summary, updates := resolveFiles(rows)
	if err := s.applyFileResolution(ctx, updates); err != nil {
		return err
	}
	summary, buckets, findings, err := aggregate(ctx, summary, func(visit func(aggregateRow) error) error {
		return s.eachActiveResponse(ctx, visit)
	})
	if err != nil {
		return err
	}
	return s.writeAggregates(ctx, summary, buckets, findings)
}

// aggregate deduplicates responses and builds the roll-up, the daily series and
// the findings in a single pass. It is shared by the indexed path and the
// index-less fallback so the two can never disagree about what a token is.
func aggregate(ctx context.Context, base Summary,
	iterate func(visit func(aggregateRow) error) error) (Summary, []DayBucket, []Finding, error) {
	summary := base
	seen := make(map[string]struct{})
	daily := make(map[int64]*DayBucket)
	detect := newDetector()

	err := iterate(func(row aggregateRow) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := row.DedupKey
		if row.LineageScoped {
			// Forked and compacted conversations copy earlier responses
			// verbatim; scoping the key to the lineage root keeps one copy.
			key = "lineage:" + row.LineageRoot + "|" + key
		}
		if key != "" {
			if _, exists := seen[key]; exists {
				summary.DuplicateResponses++
				return nil
			}
			seen[key] = struct{}{}
		}
		summary.FreshInput += row.FreshInput
		summary.CacheWrite += row.CacheWrite
		summary.CacheRead += row.CacheRead
		summary.Output += row.Output
		summary.Other += row.Other
		summary.TotalTokens += row.Total
		summary.ResponseCount++
		// Only providers that report cache activity belong in the cache ratio.
		if row.Schema != CacheSchemaNone {
			summary.CacheCapableResponses++
			summary.CacheCapableFreshInput += row.FreshInput
			summary.CacheCapableCacheWrite += row.CacheWrite
			summary.CacheCapableCacheRead += row.CacheRead
		}

		if row.Day > 0 {
			bucket, exists := daily[row.Day]
			if !exists {
				bucket = &DayBucket{Day: time.Unix(row.Day*secondsPerDay, 0).UTC()}
				daily[row.Day] = bucket
			}
			bucket.FreshInput += row.FreshInput
			bucket.CacheWrite += row.CacheWrite
			bucket.CacheRead += row.CacheRead
			bucket.Output += row.Output
			bucket.Responses++
			if row.Schema != CacheSchemaNone {
				bucket.CacheCapableResponses++
				bucket.CacheCapableFreshInput += row.FreshInput
				bucket.CacheCapableCacheWrite += row.CacheWrite
				bucket.CacheCapableCacheRead += row.CacheRead
			}
		}
		detect.observe(row)
		return nil
	})
	if err != nil {
		return Summary{}, nil, nil, err
	}

	buckets := make([]DayBucket, 0, len(daily))
	for _, bucket := range daily {
		buckets = append(buckets, *bucket)
	}
	sort.Slice(buckets, func(left, right int) bool {
		return buckets[left].Day.Before(buckets[right].Day)
	})
	return summary, buckets, detect.findings(), nil
}

// resolveFiles picks the winning copy of each duplicated conversation and
// stamps lineage roots, mirroring the previous scanner's rules: the copy with
// the most recorded usage wins, ties broken by response count then path.
func resolveFiles(rows []fileRow) (Summary, []fileRow) {
	var summary Summary
	winners := make(map[string]fileRow)
	for _, row := range rows {
		if row.Unreadable {
			summary.UnreadableFiles++
			continue
		}
		if row.ResponseCount == 0 {
			continue
		}
		current, exists := winners[row.ConversationID]
		if !exists {
			winners[row.ConversationID] = row
			continue
		}
		summary.DuplicateFiles++
		if betterCopy(row, current) {
			winners[row.ConversationID] = row
		}
	}
	summary.ConversationCount = int64(len(winners))

	parents := make(map[string]string, len(winners))
	for id, row := range winners {
		parents[id] = strings.TrimSpace(row.ParentID)
	}

	updates := make([]fileRow, 0, len(rows))
	for _, row := range rows {
		winner, exists := winners[row.ConversationID]
		active := exists && winner.Path == row.Path
		lineage := row.ConversationID
		if active {
			lineage = lineageRoot(row.ConversationID, parents)
		}
		if row.Active == active && row.LineageRoot == lineage {
			continue
		}
		row.Active = active
		row.LineageRoot = lineage
		updates = append(updates, row)
	}
	return summary, updates
}

func betterCopy(candidate, current fileRow) bool {
	if candidate.TotalTokens != current.TotalTokens {
		return candidate.TotalTokens > current.TotalTokens
	}
	if candidate.ResponseCount != current.ResponseCount {
		return candidate.ResponseCount > current.ResponseCount
	}
	return candidate.Path < current.Path
}

func lineageRoot(id string, parents map[string]string) string {
	seen := make(map[string]struct{})
	current := id
	for current != "" {
		if _, exists := seen[current]; exists {
			return id
		}
		seen[current] = struct{}{}
		parent, exists := parents[current]
		if !exists || parent == "" {
			return current
		}
		current = parent
	}
	return id
}
