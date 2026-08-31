package searchindex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteSearchAndFilters(t *testing.T) {
	engine, err := OpenSQLite(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	docs := []Document{
		{ID: "main", WorkspacePath: "/work", Title: "Report Studio", Preview: "PDF workflow", Body: "orchestrator pipeline", MessageCount: 642, UpdatedAt: time.Unix(2, 0), Origin: "interactive", BodyIndexed: true, FileModUnixNano: 123, FileSize: 456},
		{ID: "agent", WorkspacePath: "/work", Title: "Worker", Body: "orchestrator pipeline", MessageCount: 12, UpdatedAt: time.Unix(3, 0), Origin: "subagent", BodyIndexed: true},
		{ID: "other", WorkspacePath: "/other", Title: "Report Studio", Body: "orchestrator", MessageCount: 900, UpdatedAt: time.Unix(4, 0), Origin: "interactive", BodyIndexed: true},
		{ID: "hidden", WorkspacePath: "/work", Title: "Headless run", UpdatedAt: time.Unix(5, 0), Origin: "headless"},
	}
	for _, doc := range docs {
		if err := engine.Upsert(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	results, err := engine.Search(ctx, Query{Text: "orchestrator", SearchBody: true, WorkspacePath: "/work", Origin: "interactive", MinMessages: 20, Sort: "message_count", Order: "desc", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "main" || results[0].MessageCount != 642 {
		t.Fatalf("results = %#v", results)
	}
	metadataOnly, err := engine.Search(ctx, Query{Text: "orchestrator", SearchBody: false, WorkspacePath: "/work", Limit: 10})
	if err != nil || len(metadataOnly) != 0 {
		t.Fatalf("search_body=false leaked body match: results=%#v err=%v", metadataOnly, err)
	}
	state, ok, err := engine.State(ctx, "main")
	if err != nil || !ok || !state.BodyIndexed || state.FileModUnixNano != 123 || state.FileSize != 456 {
		t.Fatalf("state=%+v ok=%v err=%v", state, ok, err)
	}
	bodyResults, err := engine.Search(ctx, Query{Text: "orchestrator", SearchBody: true, WorkspacePath: "/work", Limit: 10})
	if err != nil || len(bodyResults) != 2 || bodyResults[0].Body == "" {
		t.Fatalf("body results=%+v err=%v", bodyResults, err)
	}
	withoutHeadless, err := engine.Search(ctx, Query{ExcludeHeadless: true, Limit: 10})
	if err != nil || len(withoutHeadless) != len(docs)-1 {
		t.Fatalf("without headless=%+v err=%v", withoutHeadless, err)
	}
	for _, result := range withoutHeadless {
		if result.Origin == "headless" {
			t.Fatalf("headless result leaked: %+v", result)
		}
	}
}

func TestSQLiteUpsertDeleteAndReset(t *testing.T) {
	engine, err := OpenSQLite(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	doc := Document{ID: "x", WorkspacePath: "/work", Title: "Old", Body: "alpha", UpdatedAt: time.Unix(1, 0)}
	if err := engine.Upsert(ctx, doc); err != nil {
		t.Fatal(err)
	}
	doc.Title, doc.Body = "New", "beta"
	if err := engine.Upsert(ctx, doc); err != nil {
		t.Fatal(err)
	}
	old, err := engine.Search(ctx, Query{Text: "alpha", SearchBody: true, Limit: 10})
	if err != nil || len(old) != 0 {
		t.Fatalf("old results=%v err=%v", old, err)
	}
	current, err := engine.Search(ctx, Query{Text: "beta", SearchBody: true, Limit: 10})
	if err != nil || len(current) != 1 {
		t.Fatalf("current results=%v err=%v", current, err)
	}
	if err := engine.Delete(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if ids, err := engine.IDs(ctx, ""); err != nil || len(ids) != 0 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if err := engine.Upsert(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if ids, err := engine.IDs(ctx, ""); err != nil || len(ids) != 0 {
		t.Fatalf("ids after reset=%v err=%v", ids, err)
	}
}

func TestSQLiteSchemaVersionRebuildsObjectsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE history_index_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO history_index_meta VALUES ('schema_version', '2');
		CREATE TABLE history_documents (
			id TEXT PRIMARY KEY, workspace_path TEXT NOT NULL, title TEXT NOT NULL,
			preview TEXT NOT NULL, body TEXT NOT NULL, message_count INTEGER NOT NULL,
			updated_at INTEGER NOT NULL, origin TEXT NOT NULL,
			body_indexed INTEGER NOT NULL DEFAULT 0
		);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	engine, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := Document{ID: "rebuilt", Title: "schema migration", Body: "tantivy parity", BodyIndexed: true}
	if err := engine.Upsert(context.Background(), doc); err != nil {
		t.Fatalf("upsert after schema rebuild: %v", err)
	}
	results, err := engine.Search(context.Background(), Query{Text: "parity", SearchBody: true, Limit: 10})
	if err != nil || len(results) != 1 || results[0].ID != doc.ID {
		t.Fatalf("results=%v err=%v", results, err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	engine, err = OpenSQLite(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer func() { _ = engine.Close() }()
	state, ok, err := engine.State(context.Background(), doc.ID)
	if err != nil || !ok || state.ID != doc.ID {
		t.Fatalf("second open rebuilt again: state=%+v ok=%v err=%v", state, ok, err)
	}
}

func TestSyncFilesSkipsUnchangedAndReloadsChangedConversation(t *testing.T) {
	engine, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.json")
	if err := os.WriteFile(path, []byte("old searchable text"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadCount := 0
	load := func(_ context.Context, file SourceFile, loadBody bool) (Document, bool, error) {
		loadCount++
		body, err := os.ReadFile(file.Path)
		return Document{Title: "fixture", Body: string(body), BodyIndexed: loadBody, UpdatedAt: time.Now()}, true, err
	}
	source := sourceFileForTest(t, path, "fixture")
	if err := SyncFiles(ctx, engine, "", []SourceFile{source}, true, load); err != nil {
		t.Fatal(err)
	}
	if loadCount != 1 {
		t.Fatalf("initial load count = %d, want 1", loadCount)
	}
	if err := SyncFiles(ctx, engine, "", []SourceFile{source}, true, load); err != nil {
		t.Fatal(err)
	}
	if loadCount != 1 {
		t.Fatalf("unchanged file was re-parsed: load count = %d", loadCount)
	}

	if err := os.WriteFile(path, []byte("new replacement searchable text"), 0o600); err != nil {
		t.Fatal(err)
	}
	source = sourceFileForTest(t, path, "fixture")
	if err := SyncFiles(ctx, engine, "", []SourceFile{source}, true, load); err != nil {
		t.Fatal(err)
	}
	if loadCount != 2 {
		t.Fatalf("changed file load count = %d, want 2", loadCount)
	}
	results, err := engine.Search(ctx, Query{Text: "replacement", SearchBody: true, Limit: 10})
	if err != nil || len(results) != 1 || results[0].ID != "fixture" {
		t.Fatalf("changed content results=%v err=%v", results, err)
	}
	if err := SyncFiles(ctx, engine, "", nil, true, load); err != nil {
		t.Fatal(err)
	}
	if ids, err := engine.IDs(ctx, ""); err != nil || len(ids) != 0 {
		t.Fatalf("deleted file was not tombstoned: ids=%v err=%v", ids, err)
	}
}

func TestSyncFilesOversizedConversationIndexesMetadataWithoutLoading(t *testing.T) {
	engine, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	path := filepath.Join(t.TempDir(), "oversized.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxConversationFileSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	loadCount := 0
	load := func(context.Context, SourceFile, bool) (Document, bool, error) {
		loadCount++
		return Document{}, true, nil
	}
	source := sourceFileForTest(t, path, "oversized")
	if err := SyncFiles(context.Background(), engine, "/work", []SourceFile{source}, true, load); err != nil {
		t.Fatal(err)
	}
	if loadCount != 0 {
		t.Fatalf("oversized file was parsed %d times", loadCount)
	}
	state, ok, err := engine.State(context.Background(), "oversized")
	if err != nil || !ok || !state.BodySkipped || state.BodyIndexed ||
		state.FileSize != MaxConversationFileSize+1 || state.FileModUnixNano != source.FileModUnixNano {
		t.Fatalf("oversized state=%+v ok=%v err=%v", state, ok, err)
	}
	if err := SyncFiles(context.Background(), engine, "/work", []SourceFile{source}, true, load); err != nil {
		t.Fatal(err)
	}
	if loadCount != 0 {
		t.Fatalf("unchanged oversized file was retried %d times", loadCount)
	}
}

func sourceFileForTest(t *testing.T, path, id string) SourceFile {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return SourceFile{
		ID: id, Path: path, WorkspacePath: "/work",
		FileModUnixNano: info.ModTime().UnixNano(), FileSize: info.Size(),
	}
}

func TestSQLiteMemoryConcurrentAccessUsesOneDatabase(t *testing.T) {
	engine, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- engine.Upsert(ctx, Document{ID: string(rune('a' + i)), Title: "concurrent"})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ids, err := engine.IDs(ctx, "")
	if err != nil || len(ids) != 16 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}

func TestSQLiteMetadataSearchUsesTokenSemantics(t *testing.T) {
	engine, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if err := engine.Upsert(ctx, Document{ID: "x", Title: "Orchestrator Notes"}); err != nil {
		t.Fatal(err)
	}
	exact, err := engine.Search(ctx, Query{Text: "orchestrator", Limit: 10})
	if err != nil || len(exact) != 1 {
		t.Fatalf("exact=%v err=%v", exact, err)
	}
	partial, err := engine.Search(ctx, Query{Text: "orch", Limit: 10})
	if err != nil || len(partial) != 0 {
		t.Fatalf("partial=%v err=%v", partial, err)
	}
}
