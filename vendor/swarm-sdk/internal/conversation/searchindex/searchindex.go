// Package searchindex provides a rebuildable full-text index over persisted
// conversation JSON. Conversation files remain the source of truth.
package searchindex

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"
)

const SchemaVersion = 4

// MaxConversationFileSize is the largest conversation JSON file that history
// search will parse. A decoded conversation needs several times its on-disk size
// in memory; 64 MiB keeps indexing bounded while covering ordinary long-running
// conversations. Larger files retain filesystem metadata in the index but skip
// message-body extraction.
const MaxConversationFileSize int64 = 64 << 20

var ErrUnavailable = errors.New("conversation search index unavailable")

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

type Document struct {
	ID              string
	WorkspacePath   string
	Title           string
	Preview         string
	Body            string
	MessageCount    int
	UpdatedAt       time.Time
	Origin          string
	BodyIndexed     bool
	BodySkipped     bool
	FileModUnixNano int64
	FileSize        int64
}

// SourceFile is the cheap filesystem state used to decide whether a persisted
// conversation needs to be opened and parsed.
type SourceFile struct {
	ID              string
	Path            string
	WorkspacePath   string
	FileModUnixNano int64
	FileSize        int64
}

// DocumentLoader parses a source file when SyncFiles determines that its index
// entry is missing, stale, or needs a body. The bool result reports whether the
// document belongs in the caller's visible index set.
type DocumentLoader func(context.Context, SourceFile, bool) (Document, bool, error)

type SegmentLoader func(ctx context.Context, file SourceFile) ([]Segment, error)

type syncConfig struct {
	segmentLoader    SegmentLoader
	workerCount      int
	maxInFlightBytes int64
}

type SyncOption func(*syncConfig)

// WithSegments enables segment maintenance during SyncFiles. It is a no-op when
// the engine does not implement SegmentEngine.
func WithSegments(loader SegmentLoader) SyncOption {
	return func(config *syncConfig) {
		config.segmentLoader = loader
	}
}

const (
	defaultSyncMaxInFlightBytes = int64(256 << 20)
	syncByteQuantum             = int64(1 << 20)
)

// withSyncLimits exists for deterministic tests. Production always derives its
// worker count from GOMAXPROCS and uses the package memory bound.
func withSyncLimits(workerCount int, maxInFlightBytes int64) SyncOption {
	return func(config *syncConfig) {
		config.workerCount = workerCount
		config.maxInFlightBytes = maxInFlightBytes
	}
}

type Query struct {
	Text            string
	SearchBody      bool
	WorkspacePath   string
	Origin          string
	ExcludeHeadless bool
	MinMessages     int
	Sort            string
	Order           string
	Limit           int
}

type Result struct {
	Document
	Score float64
}

type Engine interface {
	Upsert(context.Context, Document) error
	Delete(context.Context, string) error
	Search(context.Context, Query) ([]Result, error)
	State(context.Context, string) (Document, bool, error)
	// States returns the freshness state of every indexed document in scope,
	// keyed by ID, in ONE query. SyncFiles must never issue a per-file lookup:
	// this corpus has ~19k conversation files, and 19k round-trips cost more
	// than the JSON parsing the index exists to avoid.
	States(context.Context, string) (map[string]Document, error)
	IDs(context.Context, string) ([]string, error)
	Reset(context.Context) error
	Close() error
}

// SyncFiles refreshes a rebuildable index using filesystem metadata as the
// freshness signal. It never invokes load for unchanged files and never invokes
// it for oversized files.
func SyncFiles(ctx context.Context, engine Engine, workspace string, files []SourceFile, searchBody bool, load DocumentLoader, opts ...SyncOption) error {
	config := syncConfig{
		workerCount:      runtime.GOMAXPROCS(0),
		maxInFlightBytes: defaultSyncMaxInFlightBytes,
	}
	for _, option := range opts {
		if option != nil {
			option(&config)
		}
	}
	segmentEngine, segmentsEnabled := engine.(SegmentEngine)
	segmentsEnabled = segmentsEnabled && config.segmentLoader != nil
	if !segmentsEnabled {
		segmentEngine = nil
	}
	if config.workerCount < 1 {
		config.workerCount = 1
	}
	if config.maxInFlightBytes < syncByteQuantum {
		config.maxInFlightBytes = syncByteQuantum
	}

	// One bulk read of existing state, rather than one query per file. When
	// workspace is set the result is scoped to it; a file whose indexed row sits
	// under a different workspace_path (a relocated conversation) then simply
	// looks new and is re-indexed, which writes the corrected workspace anyway.
	states, err := engine.States(ctx, workspace)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(files))
	var jobs []syncJob
	for _, file := range files {
		if file.ID == "" {
			return fmt.Errorf("searchindex: source file id is required")
		}
		seen[file.ID] = struct{}{}
		state, exists := states[file.ID]
		unchanged := exists &&
			state.FileModUnixNano == file.FileModUnixNano &&
			state.FileSize == file.FileSize
		needsBody := searchBody && (!state.BodyIndexed && !state.BodySkipped)
		if unchanged && !needsBody {
			continue
		}
		jobs = append(jobs, syncJob{
			index:           len(jobs),
			file:            file,
			state:           state,
			exists:          exists,
			loadBody:        searchBody || (exists && state.BodyIndexed),
			replaceSegments: segmentsEnabled && !unchanged,
		})
	}

	loaderErrors, err := runSyncJobs(ctx, engine, segmentEngine, config, jobs, load)
	if loaderErrors != 0 {
		log.Printf("searchindex: skipped %d conversation file(s) after loader errors", loaderErrors)
	}
	if err != nil {
		return err
	}

	ids, err := engine.IDs(ctx, workspace)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			if err := engine.Delete(ctx, id); err != nil {
				return err
			}
			if segmentsEnabled {
				if err := segmentEngine.DeleteSegments(ctx, id); err != nil &&
					!errors.Is(err, ErrSegmentsUnsupported) {
					return err
				}
			}
		}
	}
	return nil
}

type syncJob struct {
	index           int
	file            SourceFile
	state           Document
	exists          bool
	loadBody        bool
	replaceSegments bool
}

type syncResult struct {
	syncJob
	doc      Document
	segments []Segment
	include  bool
	loadErr  error
	weight   int
}

func runSyncJobs(
	ctx context.Context,
	engine Engine,
	segmentEngine SegmentEngine,
	config syncConfig,
	jobs []syncJob,
	load DocumentLoader,
) (int, error) {
	if len(jobs) == 0 {
		return 0, nil
	}
	if len(jobs) == 1 {
		result := prepareSyncResult(ctx, config, load, syncResult{syncJob: jobs[0]})
		if result.loadErr != nil {
			return 1, nil
		}
		return 0, applySyncResult(ctx, engine, segmentEngine, result)
	}
	workerCount := min(config.workerCount, len(jobs))
	limiter := newSyncByteLimiter(config.maxInFlightBytes)
	work := make(chan syncResult)
	results := make(chan syncResult, workerCount)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for result := range work {
				result = prepareSyncResult(runCtx, config, load, result)
				select {
				case results <- result:
				case <-runCtx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, job := range jobs {
			weight, ok := limiter.acquire(runCtx, job.file.FileSize)
			if !ok {
				return
			}
			select {
			case work <- syncResult{syncJob: job, weight: weight}:
			case <-runCtx.Done():
				limiter.release(weight)
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	next := 0
	pending := make(map[int]syncResult, workerCount)
	loaderErrors := 0
	var writeErr error
	for result := range results {
		if writeErr != nil {
			continue
		}
		pending[result.index] = result
		for {
			ordered, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			if ordered.loadErr != nil {
				// Keep this source in seen, preserve any existing index rows, and
				// leave freshness stale so a later sync retries only this file.
				loaderErrors++
			} else if err := applySyncResult(runCtx, engine, segmentEngine, ordered); err != nil {
				writeErr = err
				cancel()
			}
			limiter.release(ordered.weight)
			next++
			if writeErr != nil {
				break
			}
		}
	}
	return loaderErrors, writeErr
}

func prepareSyncResult(ctx context.Context, config syncConfig, load DocumentLoader, result syncResult) syncResult {
	if result.replaceSegments {
		result.segments, result.loadErr = config.segmentLoader(ctx, result.file)
	}
	if result.loadErr == nil && result.file.FileSize <= MaxConversationFileSize {
		result.doc, result.include, result.loadErr = load(ctx, result.file, result.loadBody)
	}
	return result
}

func applySyncResult(ctx context.Context, engine Engine, segmentEngine SegmentEngine, result syncResult) error {
	if result.file.FileSize > MaxConversationFileSize {
		// States deliberately returns freshness columns only. Preserve the full
		// metadata row for an oversized conversation without decoding its body.
		result.doc = result.state
		if result.exists {
			if full, found, err := engine.State(ctx, result.file.ID); err == nil && found {
				result.doc = full
			}
		}
		result.include = true
		result.doc.Body = ""
		result.doc.BodyIndexed = false
		result.doc.BodySkipped = true
	} else if !result.include {
		if err := engine.Delete(ctx, result.file.ID); err != nil {
			return err
		}
		if segmentEngine != nil {
			if err := segmentEngine.DeleteSegments(ctx, result.file.ID); err != nil &&
				!errors.Is(err, ErrSegmentsUnsupported) {
				return err
			}
		}
		return nil
	} else {
		result.doc.BodySkipped = false
		result.doc.BodyIndexed = result.loadBody
	}

	result.doc.ID = result.file.ID
	result.doc.WorkspacePath = result.file.WorkspacePath
	result.doc.FileModUnixNano = result.file.FileModUnixNano
	result.doc.FileSize = result.file.FileSize
	if result.replaceSegments {
		if err := segmentEngine.ReplaceSegments(ctx, result.file.ID, result.segments); err != nil &&
			!errors.Is(err, ErrSegmentsUnsupported) {
			return err
		}
	}
	return engine.Upsert(ctx, result.doc)
}

type syncByteLimiter struct {
	tokens chan struct{}
}

func newSyncByteLimiter(maxBytes int64) *syncByteLimiter {
	tokenCount := int((maxBytes + syncByteQuantum - 1) / syncByteQuantum)
	return &syncByteLimiter{tokens: make(chan struct{}, tokenCount)}
}

func (l *syncByteLimiter) acquire(ctx context.Context, fileSize int64) (int, bool) {
	weight := int((max(fileSize, 1) + syncByteQuantum - 1) / syncByteQuantum)
	weight = min(weight, cap(l.tokens))
	acquired := 0
	for acquired < weight {
		select {
		case l.tokens <- struct{}{}:
			acquired++
		case <-ctx.Done():
			l.release(acquired)
			return 0, false
		}
	}
	return weight, true
}

func (l *syncByteLimiter) release(weight int) {
	for range weight {
		<-l.tokens
	}
}
