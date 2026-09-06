package searchindex

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// countingEngine wraps an Engine and records how many per-file State lookups
// SyncFiles performs. The whole point of the stat-based design is that a search
// costs a bounded number of queries rather than one per conversation file, and
// nothing else in the suite would notice a regression to per-file lookups --
// it would still be correct, just quadratically more expensive as the corpus
// grows. On the real corpus that regression cost ~7s per search.
type countingEngine struct {
	Engine
	stateCalls  int
	statesCalls int
}

func (c *countingEngine) State(ctx context.Context, id string) (Document, bool, error) {
	c.stateCalls++
	return c.Engine.State(ctx, id)
}

func (c *countingEngine) States(ctx context.Context, workspace string) (map[string]Document, error) {
	c.statesCalls++
	return c.Engine.States(ctx, workspace)
}

func newStatesTestEngine(t *testing.T) *SQLite {
	t.Helper()
	engine, err := OpenSQLite(filepath.Join(t.TempDir(), "history-search.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func TestStatesReturnsFreshnessForEveryDocument(t *testing.T) {
	ctx := context.Background()
	engine := newStatesTestEngine(t)

	docs := []Document{
		{ID: "a", WorkspacePath: "/w1", Title: "alpha", UpdatedAt: time.Unix(0, 1000),
			FileModUnixNano: 111, FileSize: 11, BodyIndexed: true},
		{ID: "b", WorkspacePath: "/w1", Title: "beta", UpdatedAt: time.Unix(0, 2000),
			FileModUnixNano: 222, FileSize: 22, BodySkipped: true},
		{ID: "c", WorkspacePath: "/w2", Title: "gamma", UpdatedAt: time.Unix(0, 3000),
			FileModUnixNano: 333, FileSize: 33},
	}
	for _, d := range docs {
		if err := engine.Upsert(ctx, d); err != nil {
			t.Fatalf("Upsert %s: %v", d.ID, err)
		}
	}

	all, err := engine.States(ctx, "")
	if err != nil {
		t.Fatalf("States(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("States(all) returned %d documents, want 3", len(all))
	}
	// The freshness fields are the entire reason this method exists; a map that
	// came back without them would silently force a full re-parse every search.
	if got := all["a"]; got.FileModUnixNano != 111 || got.FileSize != 11 || !got.BodyIndexed {
		t.Errorf("state a = %+v, want mod=111 size=11 bodyIndexed=true", got)
	}
	if got := all["b"]; !got.BodySkipped {
		t.Errorf("state b = %+v, want bodySkipped=true", got)
	}

	scoped, err := engine.States(ctx, "/w1")
	if err != nil {
		t.Fatalf("States(/w1): %v", err)
	}
	if len(scoped) != 2 {
		t.Fatalf("States(/w1) returned %d documents, want 2", len(scoped))
	}
	if _, present := scoped["c"]; present {
		t.Error("States(/w1) leaked a document from /w2")
	}
}

func TestSyncFilesDoesNotQueryPerFile(t *testing.T) {
	ctx := context.Background()
	counting := &countingEngine{Engine: newStatesTestEngine(t)}

	files := make([]SourceFile, 0, 50)
	for i := 0; i < 50; i++ {
		files = append(files, SourceFile{
			ID:              string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Path:            "/tmp/x.json",
			WorkspacePath:   "/w",
			FileModUnixNano: int64(i + 1),
			FileSize:        int64(i + 1),
		})
	}

	load := func(ctx context.Context, file SourceFile, loadBody bool) (Document, bool, error) {
		return Document{Title: "t-" + file.ID, UpdatedAt: time.Unix(0, 1)}, true, nil
	}

	if err := SyncFiles(ctx, counting, "/w", files, true, load); err != nil {
		t.Fatalf("SyncFiles (cold): %v", err)
	}
	if counting.stateCalls != 0 {
		t.Errorf("cold sync made %d per-file State calls, want 0", counting.stateCalls)
	}
	if counting.statesCalls != 1 {
		t.Errorf("cold sync made %d bulk States calls, want exactly 1", counting.statesCalls)
	}

	// A second sync with identical filesystem metadata must re-read state once
	// and load nothing at all.
	loads := 0
	countingLoad := func(ctx context.Context, file SourceFile, loadBody bool) (Document, bool, error) {
		loads++
		return load(ctx, file, loadBody)
	}
	if err := SyncFiles(ctx, counting, "/w", files, true, countingLoad); err != nil {
		t.Fatalf("SyncFiles (warm): %v", err)
	}
	if loads != 0 {
		t.Errorf("warm sync re-parsed %d files, want 0", loads)
	}
	if counting.stateCalls != 0 {
		t.Errorf("warm sync made %d per-file State calls, want 0", counting.stateCalls)
	}
	if counting.statesCalls != 2 {
		t.Errorf("total bulk States calls = %d, want 2 (one per sync)", counting.statesCalls)
	}
}

// TestSyncFilesSurvivesOneUnreadableConversation pins the single most expensive
// bug found in this work: SyncFiles used to return the first load error, which
// made indexedHistorySearch fall back to a full-corpus scan. One orphaned .json
// file — present on disk, not loadable — took every history search from 0.3s to
// 42s over 8 GB, for every search, forever.
func TestSyncFilesSurvivesOneUnreadableConversation(t *testing.T) {
	ctx := context.Background()
	engine := newStatesTestEngine(t)

	files := []SourceFile{
		{ID: "good-1", Path: "/tmp/a.json", WorkspacePath: "/w", FileModUnixNano: 1, FileSize: 1},
		{ID: "orphan", Path: "/tmp/b.json", WorkspacePath: "/w", FileModUnixNano: 2, FileSize: 2},
		{ID: "good-2", Path: "/tmp/c.json", WorkspacePath: "/w", FileModUnixNano: 3, FileSize: 3},
	}

	load := func(ctx context.Context, file SourceFile, loadBody bool) (Document, bool, error) {
		if file.ID == "orphan" {
			return Document{}, false, errors.New("conversation not found")
		}
		return Document{Title: "title-" + file.ID, UpdatedAt: time.Unix(0, 1)}, true, nil
	}

	if err := SyncFiles(ctx, engine, "/w", files, true, load); err != nil {
		t.Fatalf("SyncFiles returned %v; one bad file must not fail the sync", err)
	}

	states, err := engine.States(ctx, "/w")
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	// Every readable conversation must still be indexed, including the one
	// AFTER the failure -- an early return would have skipped it entirely.
	for _, id := range []string{"good-1", "good-2"} {
		if _, ok := states[id]; !ok {
			t.Errorf("%s missing from the index after a sibling failed to load", id)
		}
	}
	if _, ok := states["orphan"]; ok {
		t.Error("unreadable conversation should not have been indexed")
	}
}

// TestSyncFilesKeepsExistingRowWhenReloadFails proves a transient read failure
// does not delete a document that was previously indexed successfully.
func TestSyncFilesKeepsExistingRowWhenReloadFails(t *testing.T) {
	ctx := context.Background()
	engine := newStatesTestEngine(t)

	file := SourceFile{ID: "flaky", Path: "/tmp/f.json", WorkspacePath: "/w", FileModUnixNano: 1, FileSize: 1}
	ok := func(ctx context.Context, f SourceFile, loadBody bool) (Document, bool, error) {
		return Document{Title: "indexed once", UpdatedAt: time.Unix(0, 1)}, true, nil
	}
	if err := SyncFiles(ctx, engine, "/w", []SourceFile{file}, true, ok); err != nil {
		t.Fatalf("initial SyncFiles: %v", err)
	}

	// The file changes on disk, so it will be re-read -- and the read fails.
	file.FileModUnixNano = 2
	file.FileSize = 2
	fail := func(ctx context.Context, f SourceFile, loadBody bool) (Document, bool, error) {
		return Document{}, false, errors.New("transient I/O error")
	}
	if err := SyncFiles(ctx, engine, "/w", []SourceFile{file}, true, fail); err != nil {
		t.Fatalf("SyncFiles after failure: %v", err)
	}

	// Presence is checked through States because that is what SyncFiles reads,
	// but the CONTENT check has to go through State: States deliberately
	// projects freshness columns only, so it never carries a title.
	states, err := engine.States(ctx, "/w")
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	if _, present := states["flaky"]; !present {
		t.Fatal("a transient read failure deleted a previously good document")
	}
	full, found, err := engine.State(ctx, "flaky")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !found {
		t.Fatal("document vanished from the full-row lookup")
	}
	if full.Title != "indexed once" {
		t.Errorf("title = %q, want the last good value preserved", full.Title)
	}
}
