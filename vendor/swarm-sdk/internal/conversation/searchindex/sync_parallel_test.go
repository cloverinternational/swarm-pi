package searchindex

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type syncMemoryEngine struct {
	mu       sync.Mutex
	docs     map[string]Document
	segments map[string][]Segment
}

func newSyncMemoryEngine() *syncMemoryEngine {
	return &syncMemoryEngine{
		docs:     make(map[string]Document),
		segments: make(map[string][]Segment),
	}
}

func (e *syncMemoryEngine) Upsert(_ context.Context, doc Document) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.docs[doc.ID] = doc
	return nil
}

func (e *syncMemoryEngine) Delete(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.docs, id)
	return nil
}

func (e *syncMemoryEngine) Search(context.Context, Query) ([]Result, error) {
	return nil, nil
}

func (e *syncMemoryEngine) State(_ context.Context, id string) (Document, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	doc, ok := e.docs[id]
	return doc, ok, nil
}

func (e *syncMemoryEngine) States(_ context.Context, workspace string) (map[string]Document, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	states := make(map[string]Document)
	for id, doc := range e.docs {
		if workspace == "" || doc.WorkspacePath == workspace {
			states[id] = doc
		}
	}
	return states, nil
}

func (e *syncMemoryEngine) IDs(_ context.Context, workspace string) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var ids []string
	for id, doc := range e.docs {
		if workspace == "" || doc.WorkspacePath == workspace {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (e *syncMemoryEngine) Reset(context.Context) error {
	return nil
}

func (e *syncMemoryEngine) Close() error {
	return nil
}

func (e *syncMemoryEngine) ReplaceSegments(_ context.Context, id string, segments []Segment) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.segments[id] = append([]Segment(nil), segments...)
	return nil
}

func (e *syncMemoryEngine) DeleteSegments(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.segments, id)
	return nil
}

func TestSyncFilesParallelMatchesSequentialDocumentsAndSegments(t *testing.T) {
	files := syncTestFiles(80)
	loadDocument := func(_ context.Context, file SourceFile, loadBody bool) (Document, bool, error) {
		return Document{
			Title:        "title-" + file.ID,
			Preview:      "preview-" + file.ID,
			Body:         "body-" + file.ID,
			MessageCount: int(file.FileSize),
			UpdatedAt:    time.Unix(0, file.FileModUnixNano),
			Origin:       "interactive",
		}, file.ID != "conversation-017", nil
	}
	loadSegments := func(_ context.Context, file SourceFile) ([]Segment, error) {
		return []Segment{
			{ConversationID: file.ID, WorkspacePath: file.WorkspacePath, Ordinal: 0, Kind: SegmentMessage, Text: "message-" + file.ID},
			{ConversationID: file.ID, WorkspacePath: file.WorkspacePath, Ordinal: 1, Kind: SegmentToolResult, Text: "result-" + file.ID, Failed: true},
		}, nil
	}

	sequential := newSyncMemoryEngine()
	if err := SyncFiles(context.Background(), sequential, "/workspace", files, true, loadDocument,
		WithSegments(loadSegments), withSyncLimits(1, defaultSyncMaxInFlightBytes)); err != nil {
		t.Fatalf("sequential SyncFiles: %v", err)
	}
	parallel := newSyncMemoryEngine()
	if err := SyncFiles(context.Background(), parallel, "/workspace", files, true, loadDocument,
		WithSegments(loadSegments), withSyncLimits(7, defaultSyncMaxInFlightBytes)); err != nil {
		t.Fatalf("parallel SyncFiles: %v", err)
	}

	if !reflect.DeepEqual(parallel.docs, sequential.docs) {
		t.Errorf("parallel documents differ from sequential\nparallel: %#v\nsequential: %#v", parallel.docs, sequential.docs)
	}
	if !reflect.DeepEqual(parallel.segments, sequential.segments) {
		t.Errorf("parallel segments differ from sequential\nparallel: %#v\nsequential: %#v", parallel.segments, sequential.segments)
	}
}

func TestSyncFilesParallelSkipsOnlyFailingLoader(t *testing.T) {
	files := syncTestFiles(64)
	engine := newSyncMemoryEngine()
	load := func(_ context.Context, file SourceFile, _ bool) (Document, bool, error) {
		if file.ID == "conversation-031" {
			return Document{}, false, fmt.Errorf("unreadable fixture")
		}
		return Document{Title: file.ID}, true, nil
	}

	if err := SyncFiles(context.Background(), engine, "/workspace", files, true, load,
		withSyncLimits(6, defaultSyncMaxInFlightBytes)); err != nil {
		t.Fatalf("one loader error aborted SyncFiles: %v", err)
	}
	if len(engine.docs) != len(files)-1 {
		t.Fatalf("indexed %d documents, want %d", len(engine.docs), len(files)-1)
	}
	if _, ok := engine.docs["conversation-031"]; ok {
		t.Fatal("failing conversation was indexed")
	}
	for _, file := range files {
		if file.ID == "conversation-031" {
			continue
		}
		if _, ok := engine.docs[file.ID]; !ok {
			t.Errorf("readable conversation %q was skipped", file.ID)
		}
	}
}

func TestSyncFilesParallelConcurrencyIsBounded(t *testing.T) {
	const workerLimit = 4
	files := syncTestFiles(40)
	engine := newSyncMemoryEngine()
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	var arrivals atomic.Int32
	var started sync.WaitGroup
	started.Add(workerLimit)
	load := func(_ context.Context, file SourceFile, _ bool) (Document, bool, error) {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		if arrivals.Add(1) <= workerLimit {
			started.Done()
		}
		<-release
		active.Add(-1)
		return Document{Title: file.ID}, true, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- SyncFiles(context.Background(), engine, "/workspace", files, false, load,
			withSyncLimits(workerLimit, defaultSyncMaxInFlightBytes))
	}()
	started.Wait()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("SyncFiles: %v", err)
	}
	if got := maximum.Load(); got != workerLimit {
		t.Fatalf("maximum concurrent loaders = %d, want exactly %d", got, workerLimit)
	}
}

func syncTestFiles(count int) []SourceFile {
	files := make([]SourceFile, count)
	for index := range files {
		files[index] = SourceFile{
			ID:              fmt.Sprintf("conversation-%03d", index),
			Path:            fmt.Sprintf("/tmp/conversation-%03d.json", index),
			WorkspacePath:   "/workspace",
			FileModUnixNano: int64(index + 1),
			FileSize:        int64(index + 1),
		}
	}
	return files
}
