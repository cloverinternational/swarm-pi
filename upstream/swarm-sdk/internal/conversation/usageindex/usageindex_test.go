package usageindex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openTestStore opens an index on the requested backend, skipping when the
// native Turso library is not present on this machine.
func openTestStore(t *testing.T, backend Backend) *Store {
	t.Helper()
	dir := t.TempDir()
	store, opened, err := Open(context.Background(), OpenOptions{
		Backend:    backend,
		TursoPath:  filepath.Join(dir, "usage-index.turso"),
		SQLitePath: filepath.Join(dir, "usage-index.sqlite"),
	})
	if err != nil {
		if backend == BackendTurso {
			t.Skipf("turso backend unavailable: %v", err)
		}
		t.Fatalf("open %s index: %v", backend, err)
	}
	if backend != BackendAuto && opened != backend {
		t.Fatalf("opened backend %q, want %q", opened, backend)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// eachBackend runs a test body against every backend so the two engines cannot
// silently diverge.
func eachBackend(t *testing.T, body func(t *testing.T, store *Store)) {
	t.Helper()
	for _, backend := range []Backend{BackendSQLite, BackendTurso} {
		t.Run(string(backend), func(t *testing.T) {
			body(t, openTestStore(t, backend))
		})
	}
}

func writeConversation(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// tokensSchemaA is the current layout: cache lives on the tokens object and
// input excludes it.
func tokensSchemaA(id, timestamp string, input, output, cacheWrite, cacheRead int64) string {
	return fmt.Sprintf(`{"id":%q,"timestamp":%q,"role":"assistant","content":"x",
		"tokens":{"input":%d,"output":%d,"total":%d,"cache_creation":%d,"cache_read":%d}}`,
		id, timestamp, input, output, input+output+cacheWrite+cacheRead, cacheWrite, cacheRead)
}

// tokensSchemaB is the older layout: input already includes cache, and cache
// detail lives under metadata.cache_metrics.
func tokensSchemaB(id, timestamp string, input, output, cacheWrite, cacheRead int64) string {
	return fmt.Sprintf(`{"id":%q,"timestamp":%q,"role":"assistant","content":"x",
		"tokens":{"input":%d,"output":%d,"total":%d},
		"metadata":{"cache_metrics":{"cache_creation_tokens":%d,"cache_creation_1h_tokens":%d,
			"cache_creation_5m_tokens":0,"cache_read_tokens":%d}}}`,
		id, timestamp, input, output, input+output, cacheWrite, cacheWrite, cacheRead)
}

func conversation(id string, messages ...string) string {
	body := `{"id":"` + id + `","messages":[`
	for index, message := range messages {
		if index > 0 {
			body += ","
		}
		body += message
	}
	return body + `],"metadata":{"custom":{}}}`
}

func TestSyncCountsBothCacheSchemasWithoutDoubleCounting(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		writeConversation(t, root, "a.json", conversation("conv-a",
			tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 900, 8000)))
		writeConversation(t, root, "b.json", conversation("conv-b",
			tokensSchemaB("m2", "2026-07-01T11:00:00Z", 9000, 50, 1000, 7000)))

		if _, err := store.Sync(context.Background(), root); err != nil {
			t.Fatalf("sync: %v", err)
		}
		summary, err := store.Summary(context.Background())
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		// Schema A: fresh 100, write 900, read 8000. Schema B: input 9000
		// already contains write 1000 + read 7000, leaving 1000 fresh.
		if summary.FreshInput != 1100 {
			t.Errorf("FreshInput = %d, want 1100", summary.FreshInput)
		}
		if summary.CacheWrite != 1900 {
			t.Errorf("CacheWrite = %d, want 1900", summary.CacheWrite)
		}
		if summary.CacheRead != 15000 {
			t.Errorf("CacheRead = %d, want 15000", summary.CacheRead)
		}
		if summary.Output != 100 {
			t.Errorf("Output = %d, want 100", summary.Output)
		}
		if got, want := summary.InputTokens(), int64(18000); got != want {
			t.Errorf("InputTokens = %d, want %d", got, want)
		}
		if summary.ResponseCount != 2 || summary.ConversationCount != 2 {
			t.Errorf("counts = %d responses / %d conversations, want 2/2",
				summary.ResponseCount, summary.ConversationCount)
		}
	})
}

func TestSyncPicksUpConversationOwnedLayout(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		writeConversation(t, root, "conv-new-layout/conversation.json", conversation("conv-new-layout",
			tokensSchemaA("response-1", "2026-07-27T12:00:00Z", 321, 54, 20, 40)))

		stats, err := store.Sync(context.Background(), root)
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		if stats.FilesScanned != 1 || stats.FilesParsed != 1 {
			t.Fatalf("sync stats = %+v, want one new-layout transcript parsed", stats)
		}
		summary, err := store.Summary(context.Background())
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if summary.ConversationCount != 1 || summary.ResponseCount != 1 ||
			summary.FreshInput != 321 || summary.CacheWrite != 20 ||
			summary.CacheRead != 40 || summary.Output != 54 {
			t.Fatalf("new-layout usage not indexed: %+v", summary)
		}
	})
}

func TestSyncDoesNotReparseUnchangedFiles(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		writeConversation(t, root, "a.json", conversation("conv-a",
			tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 0, 0)))
		writeConversation(t, root, "b.json", conversation("conv-b",
			tokensSchemaA("m2", "2026-07-01T10:05:00Z", 200, 50, 0, 0)))

		first, err := store.Sync(context.Background(), root)
		if err != nil {
			t.Fatalf("first sync: %v", err)
		}
		if first.FilesParsed != 2 {
			t.Fatalf("first sync parsed %d files, want 2", first.FilesParsed)
		}

		second, err := store.Sync(context.Background(), root)
		if err != nil {
			t.Fatalf("second sync: %v", err)
		}
		// This is the whole point of the index: an unchanged store must not
		// open a single conversation file.
		if second.FilesParsed != 0 {
			t.Errorf("second sync parsed %d files, want 0", second.FilesParsed)
		}
		if second.Changed {
			t.Errorf("second sync reported a change on an unchanged store")
		}
		if second.FilesScanned != 2 {
			t.Errorf("second sync scanned %d files, want 2", second.FilesScanned)
		}

		writeConversation(t, root, "b.json", conversation("conv-b",
			tokensSchemaA("m2", "2026-07-01T10:05:00Z", 200, 50, 0, 0),
			tokensSchemaA("m3", "2026-07-01T10:06:00Z", 300, 60, 0, 0)))
		third, err := store.Sync(context.Background(), root)
		if err != nil {
			t.Fatalf("third sync: %v", err)
		}
		if third.FilesParsed != 1 {
			t.Errorf("third sync parsed %d files, want 1", third.FilesParsed)
		}
		summary, err := store.Summary(context.Background())
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if summary.ResponseCount != 3 {
			t.Errorf("ResponseCount = %d, want 3", summary.ResponseCount)
		}
	})
}

func TestSyncRemovesDeletedConversations(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		path := writeConversation(t, root, "a.json", conversation("conv-a",
			tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 0, 0)))
		writeConversation(t, root, "b.json", conversation("conv-b",
			tokensSchemaA("m2", "2026-07-01T10:05:00Z", 200, 50, 0, 0)))
		if _, err := store.Sync(context.Background(), root); err != nil {
			t.Fatalf("sync: %v", err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove fixture: %v", err)
		}
		stats, err := store.Sync(context.Background(), root)
		if err != nil {
			t.Fatalf("resync: %v", err)
		}
		if stats.FilesRemoved != 1 {
			t.Errorf("FilesRemoved = %d, want 1", stats.FilesRemoved)
		}
		summary, err := store.Summary(context.Background())
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if summary.ResponseCount != 1 || summary.ConversationCount != 1 {
			t.Errorf("after delete: %d responses / %d conversations, want 1/1",
				summary.ResponseCount, summary.ConversationCount)
		}
	})
}

func TestSyncDeduplicatesDuplicateFileCopies(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		body := conversation("conv-a",
			tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 0, 0),
			tokensSchemaA("m2", "2026-07-01T10:01:00Z", 200, 60, 0, 0))
		writeConversation(t, root, "conv-a.json", body)
		writeConversation(t, root, "workspace/conv-a.json", body)

		if _, err := store.Sync(context.Background(), root); err != nil {
			t.Fatalf("sync: %v", err)
		}
		summary, err := store.Summary(context.Background())
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if summary.DuplicateFiles != 1 {
			t.Errorf("DuplicateFiles = %d, want 1", summary.DuplicateFiles)
		}
		if summary.ResponseCount != 2 {
			t.Errorf("ResponseCount = %d, want 2 (one copy counted)", summary.ResponseCount)
		}
		if summary.ConversationCount != 1 {
			t.Errorf("ConversationCount = %d, want 1", summary.ConversationCount)
		}
	})
}

func TestSyncDeduplicatesClonedResponsesAcrossLineage(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		// Responses without IDs are matched by timestamp, but only within one
		// fork lineage: a compacted child repeats its parent's turns verbatim.
		shared := `{"timestamp":"2026-07-01T10:00:00Z","role":"assistant",
			"tokens":{"input":1000,"output":100,"total":1100}}`
		writeConversation(t, root, "parent.json", conversation("conv-parent", shared))
		child := `{"id":"conv-child","messages":[` + shared + `],
			"metadata":{"custom":{"compacted_from":"conv-parent"}}}`
		writeConversation(t, root, "child.json", child)

		if _, err := store.Sync(context.Background(), root); err != nil {
			t.Fatalf("sync: %v", err)
		}
		summary, err := store.Summary(context.Background())
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if summary.DuplicateResponses != 1 {
			t.Errorf("DuplicateResponses = %d, want 1", summary.DuplicateResponses)
		}
		if summary.ResponseCount != 1 {
			t.Errorf("ResponseCount = %d, want 1", summary.ResponseCount)
		}
		if summary.TotalTokens != 1100 {
			t.Errorf("TotalTokens = %d, want 1100", summary.TotalTokens)
		}
	})
}

func TestSyncKeepsProviderTotalRemainder(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		// Some providers report a total covering dimensions not broken out,
		// for example Gemini thought tokens. The remainder must survive.
		writeConversation(t, root, "a.json", conversation("conv-a",
			`{"id":"m1","timestamp":"2026-07-01T10:00:00Z","role":"assistant",
			  "tokens":{"input":1000,"output":100,"total":1600}}`))
		if _, err := store.Sync(context.Background(), root); err != nil {
			t.Fatalf("sync: %v", err)
		}
		summary, err := store.Summary(context.Background())
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if summary.Other != 500 {
			t.Errorf("Other = %d, want 500", summary.Other)
		}
		if summary.TotalTokens != 1600 {
			t.Errorf("TotalTokens = %d, want 1600", summary.TotalTokens)
		}
	})
}

func TestSyncRecordsUnreadableFilesOnce(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		writeConversation(t, root, "good.json", conversation("conv-a",
			tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 0, 0)))
		writeConversation(t, root, "truncated.json", `{"id":"conv-b","messages":[{"role":"assis`)

		first, err := store.Sync(context.Background(), root)
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		if first.UnreadableFiles != 1 {
			t.Errorf("UnreadableFiles = %d, want 1", first.UnreadableFiles)
		}
		summary, err := store.Summary(context.Background())
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if summary.UnreadableFiles != 1 {
			t.Errorf("summary.UnreadableFiles = %d, want 1", summary.UnreadableFiles)
		}
		second, err := store.Sync(context.Background(), root)
		if err != nil {
			t.Fatalf("resync: %v", err)
		}
		// A broken file must not be re-read forever.
		if second.FilesParsed != 0 {
			t.Errorf("second sync parsed %d files, want 0", second.FilesParsed)
		}
	})
}

func TestSyncHonorsCancellation(t *testing.T) {
	store := openTestStore(t, BackendSQLite)
	root := t.TempDir()
	writeConversation(t, root, "a.json", conversation("conv-a",
		tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 0, 0)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Sync(ctx, root); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestSyncRejectsEmptyRoot(t *testing.T) {
	store := openTestStore(t, BackendSQLite)
	if _, err := store.Sync(context.Background(), "   "); err == nil {
		t.Fatal("expected error for empty root")
	}
}

func TestDailyRollupOrdersOldestFirst(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		writeConversation(t, root, "a.json", conversation("conv-a",
			tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 0, 0),
			tokensSchemaA("m2", "2026-07-03T10:00:00Z", 300, 50, 0, 0)))
		if _, err := store.Sync(context.Background(), root); err != nil {
			t.Fatalf("sync: %v", err)
		}
		buckets, err := store.Daily(context.Background(), 14)
		if err != nil {
			t.Fatalf("daily: %v", err)
		}
		if len(buckets) != 2 {
			t.Fatalf("got %d buckets, want 2", len(buckets))
		}
		if !buckets[0].Day.Before(buckets[1].Day) {
			t.Errorf("buckets are not oldest-first: %v then %v", buckets[0].Day, buckets[1].Day)
		}
		if buckets[1].FreshInput != 300 {
			t.Errorf("second bucket FreshInput = %d, want 300", buckets[1].FreshInput)
		}
	})
}

func TestSchemaVersionMismatchRebuilds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage-index.sqlite")
	ctx := context.Background()
	store, _, err := Open(ctx, OpenOptions{Backend: BackendSQLite, SQLitePath: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	root := t.TempDir()
	writeConversation(t, root, "a.json", conversation("conv-a",
		tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 0, 0)))
	if _, err := store.Sync(ctx, root); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO usage_index_meta(key,value) VALUES('schema_version','0')
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		t.Fatalf("downgrade schema version: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, _, err := Open(ctx, OpenOptions{Backend: BackendSQLite, SQLitePath: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	states, err := reopened.FileStates(ctx)
	if err != nil {
		t.Fatalf("file states: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("stale rows survived a schema rebuild: %d", len(states))
	}
	stats, err := reopened.Sync(ctx, root)
	if err != nil {
		t.Fatalf("sync after rebuild: %v", err)
	}
	if stats.FilesParsed != 1 {
		t.Errorf("rebuild reparsed %d files, want 1", stats.FilesParsed)
	}
}

func TestOpenRejectsUnknownBackend(t *testing.T) {
	if _, _, err := Open(context.Background(), OpenOptions{Backend: "postgres"}); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestStreamingParserMatchesBufferedParser(t *testing.T) {
	root := t.TempDir()
	body := conversation("conv-a",
		tokensSchemaA("m1", "2026-07-01T10:00:00Z", 100, 50, 900, 8000),
		tokensSchemaB("m2", "2026-07-01T10:05:00Z", 9000, 60, 1000, 7000),
		`{"role":"user","content":"ignored"}`)
	path := writeConversation(t, root, "a.json", body)

	buffered, err := parseConversationBytes([]byte(body))
	if err != nil {
		t.Fatalf("buffered parse: %v", err)
	}
	streamed, err := parseConversationStream(path)
	if err != nil {
		t.Fatalf("streaming parse: %v", err)
	}
	if buffered.ID != streamed.ID || buffered.TotalTokens != streamed.TotalTokens {
		t.Fatalf("identity mismatch: %+v vs %+v", buffered, streamed)
	}
	if len(buffered.Responses) != len(streamed.Responses) {
		t.Fatalf("response count mismatch: %d vs %d", len(buffered.Responses), len(streamed.Responses))
	}
	for index := range buffered.Responses {
		if buffered.Responses[index] != streamed.Responses[index] {
			t.Errorf("response %d mismatch:\n buffered=%+v\n streamed=%+v",
				index, buffered.Responses[index], streamed.Responses[index])
		}
	}
}

func TestStreamingParserReadsMetadataAfterMessages(t *testing.T) {
	root := t.TempDir()
	// The conversation ID and lineage metadata are written after the messages
	// array in real files, so the streaming decoder must not depend on order.
	body := `{"messages":[` +
		`{"timestamp":"2026-07-01T10:00:00Z","role":"assistant","tokens":{"input":10,"output":5,"total":15}}` +
		`],"id":"conv-late","metadata":{"custom":{"forked_from":"conv-root"}}}`
	path := writeConversation(t, root, "late.json", body)
	parsed, err := parseConversationStream(path)
	if err != nil {
		t.Fatalf("streaming parse: %v", err)
	}
	if parsed.ID != "conv-late" || parsed.ParentID != "conv-root" {
		t.Fatalf("identity = %q / parent %q", parsed.ID, parsed.ParentID)
	}
	if len(parsed.Responses) != 1 || parsed.Responses[0].ConversationID != "conv-late" {
		t.Fatalf("responses not stamped with conversation id: %+v", parsed.Responses)
	}
}

func TestCompactJSONKeepsStructureAndDropsLongStrings(t *testing.T) {
	long := strings.Repeat("x", maxRetainedStringBytes+10)
	source := `{"id":"conv-a","content":"` + long + `","tokens":{"input":1234,"total":-5},` +
		`"nested":[{"short":"keep me"},1.5e3,true,null]}`
	compacted, err := compactJSON(strings.NewReader(source))
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !json.Valid(compacted) {
		t.Fatalf("compaction produced invalid JSON: %s", compacted)
	}
	var got map[string]any
	if err := json.Unmarshal(compacted, &got); err != nil {
		t.Fatalf("unmarshal compacted: %v", err)
	}
	if got["id"] != "conv-a" {
		t.Errorf("id = %v, want conv-a (short strings must survive)", got["id"])
	}
	if got["content"] != "" {
		t.Errorf("long string was retained: %q", got["content"])
	}
	tokens, ok := got["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("tokens object lost: %v", got["tokens"])
	}
	if tokens["input"] != float64(1234) || tokens["total"] != float64(-5) {
		t.Errorf("numbers altered: %v", tokens)
	}
	nested, ok := got["nested"].([]any)
	if !ok || len(nested) != 4 {
		t.Fatalf("nested array altered: %v", got["nested"])
	}
	if first, _ := nested[0].(map[string]any); first["short"] != "keep me" {
		t.Errorf("nested short string lost: %v", nested[0])
	}
}

func TestCompactJSONHandlesEscapesAtTheStringBoundary(t *testing.T) {
	// A quote and a backslash inside a string must not be mistaken for the end
	// of that string, including when the string is long enough to be elided.
	long := strings.Repeat(`a\"b`, maxRetainedStringBytes)
	source := `{"quoted":"say \"hi\"","trailing":"back\\slash","huge":"` + long + `","after":42}`
	compacted, err := compactJSON(strings.NewReader(source))
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(compacted, &got); err != nil {
		t.Fatalf("unmarshal compacted %s: %v", compacted, err)
	}
	if got["quoted"] != `say "hi"` {
		t.Errorf("escaped quotes mangled: %v", got["quoted"])
	}
	if got["trailing"] != `back\slash` {
		t.Errorf("escaped backslash mangled: %v", got["trailing"])
	}
	if got["huge"] != "" {
		t.Errorf("long escaped string retained: %q", got["huge"])
	}
	if got["after"] != float64(42) {
		t.Errorf("parser lost sync after elided string: %v", got["after"])
	}
}

func TestCompactJSONRejectsUnterminatedString(t *testing.T) {
	if _, err := compactJSON(strings.NewReader(`{"id":"conv`)); err == nil {
		t.Fatal("expected an error for a truncated string")
	}
}

// fixtureStore writes a store exercising both cache layouts, a duplicated file,
// a compacted fork and a cache break, so parity checks cover real behaviour
// rather than a trivial case.
func fixtureStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	duplicated := conversation("conv-dup",
		tokensSchemaA("d1", "2026-07-01T10:00:00Z", 50, 20, 1000, 40000),
		tokensSchemaA("d2", "2026-07-01T10:00:12Z", 50, 20, 42000, 0))
	writeConversation(t, root, "dup.json", duplicated)
	writeConversation(t, root, "workspace/dup.json", duplicated)
	writeConversation(t, root, "meta.json", conversation("conv-meta",
		tokensSchemaB("b1", "2026-07-02T09:00:00Z", 9000, 40, 1000, 7000)))
	shared := `{"timestamp":"2026-07-03T08:00:00Z","role":"assistant",
		"tokens":{"input":1000,"output":100,"total":1100}}`
	writeConversation(t, root, "parent.json", conversation("conv-parent", shared))
	writeConversation(t, root, "child.json",
		`{"id":"conv-child","messages":[`+shared+`],
		  "metadata":{"custom":{"compacted_from":"conv-parent"}}}`)
	writeConversation(t, root, "broken.json", `{"id":"conv-broken","messages":[{`)
	return root
}

func TestScanWithoutIndexMatchesIndexedResults(t *testing.T) {
	ctx := context.Background()
	root := fixtureStore(t)

	store := openTestStore(t, BackendSQLite)
	if _, err := store.Sync(ctx, root); err != nil {
		t.Fatalf("sync: %v", err)
	}
	indexed, err := store.Summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	scanned, err := ScanWithoutIndex(ctx, root, 10)
	if err != nil {
		t.Fatalf("scan without index: %v", err)
	}
	// The fallback exists so an unusable database costs time, never accuracy.
	if scanned.Summary != indexed {
		t.Errorf("fallback summary differs from indexed summary:\n scan   =%+v\n indexed=%+v",
			scanned.Summary, indexed)
	}

	indexedDaily, err := store.Daily(ctx, 14)
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	if len(indexedDaily) != len(scanned.Daily) {
		t.Fatalf("daily length %d vs %d", len(indexedDaily), len(scanned.Daily))
	}
	for index := range indexedDaily {
		if indexedDaily[index] != scanned.Daily[index] {
			t.Errorf("daily[%d] %+v vs %+v", index, indexedDaily[index], scanned.Daily[index])
		}
	}

	indexedBreaks, err := store.Findings(ctx, FindingCacheBreak, 10)
	if err != nil {
		t.Fatalf("findings: %v", err)
	}
	if len(indexedBreaks) != len(scanned.Breaks) {
		t.Fatalf("cache breaks %d vs %d", len(indexedBreaks), len(scanned.Breaks))
	}
	for index := range indexedBreaks {
		if indexedBreaks[index] != scanned.Breaks[index] {
			t.Errorf("break[%d] %+v vs %+v", index, indexedBreaks[index], scanned.Breaks[index])
		}
	}
	if indexed.UnreadableFiles != 1 {
		t.Errorf("UnreadableFiles = %d, want 1", indexed.UnreadableFiles)
	}
	if indexed.DuplicateFiles != 1 || indexed.DuplicateResponses != 1 {
		t.Errorf("dedup counters = %d files / %d responses, want 1/1",
			indexed.DuplicateFiles, indexed.DuplicateResponses)
	}
}

func TestLoadFallsBackWhenNoBackendOpens(t *testing.T) {
	root := fixtureStore(t)
	// An unknown backend cannot open, which is the same situation as a missing
	// native library on a user's machine.
	snapshot, err := Load(context.Background(), root, LoadOptions{
		OpenOptions: OpenOptions{Backend: "not-a-backend", TursoPath: "x", SQLitePath: "y"},
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !snapshot.Degraded || snapshot.DegradedReason == "" {
		t.Errorf("expected a degraded snapshot, got %+v", snapshot)
	}
	if snapshot.Summary.ResponseCount == 0 {
		t.Error("degraded snapshot reported no responses")
	}
}

func TestLoadUsesIndexAndReportsBackend(t *testing.T) {
	root := fixtureStore(t)
	dir := t.TempDir()
	options := LoadOptions{OpenOptions: OpenOptions{
		Backend:    BackendSQLite,
		TursoPath:  filepath.Join(dir, "usage-index.turso"),
		SQLitePath: filepath.Join(dir, "usage-index.sqlite"),
	}}
	first, err := Load(context.Background(), root, options)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if first.Degraded {
		t.Fatalf("index unexpectedly unavailable: %s", first.DegradedReason)
	}
	if first.Backend != BackendSQLite {
		t.Errorf("backend = %q", first.Backend)
	}
	if first.Stats.FilesParsed == 0 {
		t.Error("first load parsed nothing")
	}
	second, err := Load(context.Background(), root, options)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if second.Stats.FilesParsed != 0 {
		t.Errorf("second load parsed %d files, want 0", second.Stats.FilesParsed)
	}
	if second.Summary != first.Summary {
		t.Errorf("summary changed without the store changing:\n%+v\n%+v", first.Summary, second.Summary)
	}
}

func TestLargeConversationParsesWithoutRetainingContent(t *testing.T) {
	// Reproduces the shape that made the real store's 1.66 GB conversation
	// allocate gigabytes: a small number of responses buried in enormous
	// message content, including escaped quotes.
	bulk := strings.Repeat(`lorem \"ipsum\" dolor `, 400_000)
	var builder strings.Builder
	builder.WriteString(`{"id":"conv-huge","messages":[`)
	for turn := range 5 {
		if turn > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(fmt.Sprintf(
			`{"id":"m%d","timestamp":"2026-07-01T10:0%d:00Z","role":"assistant","content":"%s",
			  "tokens":{"input":%d,"output":10,"total":%d,"cache_creation":0,"cache_read":0}}`,
			turn, turn, bulk, 1000+turn, 1010+turn))
	}
	builder.WriteString(`],"metadata":{"custom":{}}}`)
	body := builder.String()
	if len(body) < streamingThreshold {
		t.Fatalf("fixture is %d bytes, expected to exceed the streaming threshold", len(body))
	}

	root := t.TempDir()
	path := writeConversation(t, root, "huge.json", body)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	parsed, err := parseConversationFile(path, info.Size())
	if err != nil {
		t.Fatalf("parse huge conversation: %v", err)
	}
	if parsed.ID != "conv-huge" {
		t.Errorf("ID = %q", parsed.ID)
	}
	if len(parsed.Responses) != 5 {
		t.Fatalf("got %d responses, want 5", len(parsed.Responses))
	}
	if parsed.Responses[4].FreshInput != 1004 {
		t.Errorf("last response FreshInput = %d, want 1004", parsed.Responses[4].FreshInput)
	}
	if parsed.Responses[0].DedupKey != "id:m0" {
		t.Errorf("dedup key = %q", parsed.Responses[0].DedupKey)
	}
}

func TestNormalizeSchemaSelection(t *testing.T) {
	tests := []struct {
		name       string
		tokens     rawTokens
		cache      rawCacheMetrics
		wantSchema CacheSchema
		wantFresh  int64
		wantWrite  int64
		wantRead   int64
		wantTotal  int64
	}{
		{
			name:       "tokens layout keeps input as fresh",
			tokens:     rawTokens{Input: 7, Output: 633, Total: 41790, CacheCreation: 4664, CacheRead: 36486},
			wantSchema: CacheSchemaTokens,
			wantFresh:  7, wantWrite: 4664, wantRead: 36486, wantTotal: 41790,
		},
		{
			name:       "metadata layout subtracts cache from input",
			tokens:     rawTokens{Input: 17191, Output: 274, Total: 17465},
			cache:      rawCacheMetrics{Creation: 6423, Creation1h: 6423, Read: 10758},
			wantSchema: CacheSchemaMetadata,
			wantFresh:  10, wantWrite: 6423, wantRead: 10758, wantTotal: 17465,
		},
		{
			name:       "no cache reported",
			tokens:     rawTokens{Input: 500, Output: 100, Total: 600},
			wantSchema: CacheSchemaNone,
			wantFresh:  500, wantTotal: 600,
		},
		{
			name:       "metadata cache larger than input cannot go negative",
			tokens:     rawTokens{Input: 100, Output: 10, Total: 110},
			cache:      rawCacheMetrics{Creation: 500, Read: 500},
			wantSchema: CacheSchemaMetadata,
			wantFresh:  0, wantWrite: 500, wantRead: 500, wantTotal: 1010,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalize(test.tokens, test.cache)
			if got.Schema != test.wantSchema {
				t.Errorf("Schema = %d, want %d", got.Schema, test.wantSchema)
			}
			if got.FreshInput != test.wantFresh {
				t.Errorf("FreshInput = %d, want %d", got.FreshInput, test.wantFresh)
			}
			if got.CacheWrite != test.wantWrite {
				t.Errorf("CacheWrite = %d, want %d", got.CacheWrite, test.wantWrite)
			}
			if got.CacheRead != test.wantRead {
				t.Errorf("CacheRead = %d, want %d", got.CacheRead, test.wantRead)
			}
			if got.Total != test.wantTotal {
				t.Errorf("Total = %d, want %d", got.Total, test.wantTotal)
			}
			if sum := got.FreshInput + got.CacheWrite + got.CacheRead + got.Output + got.Other; sum != got.Total {
				t.Errorf("buckets sum to %d but Total is %d", sum, got.Total)
			}
		})
	}
}

func TestDetectCacheBreakClassifiesCause(t *testing.T) {
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Unix()
	previous := aggregateRow{ConversationID: "c", TS: base, CacheRead: 40000, CacheWrite: 1000}

	tests := []struct {
		name      string
		current   aggregateRow
		wantBreak bool
		wantCause FindingCause
	}{
		{
			name:      "short gap is a prefix mutation",
			current:   aggregateRow{ConversationID: "c", TS: base + 12, CacheRead: 0, CacheWrite: 42000},
			wantBreak: true, wantCause: CausePrefixMutation,
		},
		{
			// Regression: this used to assert CauseTTLExpiry. That was wrong.
			// With no per-TTL evidence we cannot know whether the cache was 5m
			// or 1h, and the TUI requests 1h by default -- under which a 30
			// minute gap has NOT expired and the rewrite is a real prefix
			// mutation. Claiming "expiry" here excused 59 of 62 observed breaks
			// as unavoidable and hid the actionable class. Ambiguous is the
			// only honest answer.
			name:      "mid gap without ttl evidence is ambiguous",
			current:   aggregateRow{ConversationID: "c", TS: base + 1800, CacheRead: 0, CacheWrite: 42000},
			wantBreak: true, wantCause: CauseUnknown,
		},
		{
			// Beyond the longest possible TTL, expiry is certain regardless of
			// which TTL was in force.
			name:      "gap beyond one hour is an unambiguous ttl expiry",
			current:   aggregateRow{ConversationID: "c", TS: base + 4000, CacheRead: 0, CacheWrite: 42000},
			wantBreak: true, wantCause: CauseTTLExpiry,
		},
		{
			// A known 5m cache: 30 minutes really has expired.
			name: "known five minute cache expires on a mid gap",
			current: aggregateRow{ConversationID: "c", TS: base + 1800, CacheRead: 0,
				CacheWrite: 42000, CacheWrite5m: 42000},
			wantBreak: true, wantCause: CauseTTLExpiry,
		},
		{
			name: "one hour cache tolerates a longer gap",
			current: aggregateRow{ConversationID: "c", TS: base + 1800, CacheRead: 0,
				CacheWrite: 42000, CacheWrite1h: 42000},
			wantBreak: true, wantCause: CausePrefixMutation,
		},
		{
			name:      "healthy cache reuse is not a break",
			current:   aggregateRow{ConversationID: "c", TS: base + 30, CacheRead: 41000, CacheWrite: 12000},
			wantBreak: false,
		},
		{
			name:      "small rewrite is not a break",
			current:   aggregateRow{ConversationID: "c", TS: base + 30, CacheRead: 0, CacheWrite: 100},
			wantBreak: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finding, ok := detectCacheBreak(previous, test.current)
			if ok != test.wantBreak {
				t.Fatalf("detected = %v, want %v", ok, test.wantBreak)
			}
			if ok && finding.Cause != test.wantCause {
				t.Errorf("cause = %q, want %q", finding.Cause, test.wantCause)
			}
		})
	}
}

func TestDetectorFlagsSpikesButNotSteadyGrowth(t *testing.T) {
	detect := newDetector()
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Unix()
	// A conversation that grows steadily must not be reported as a spike.
	for turn := range 40 {
		detect.observe(aggregateRow{
			ConversationID: "steady",
			TS:             base + int64(turn)*60,
			FreshInput:     int64(30_000 + turn*1_000),
		})
	}
	if len(detect.spikes) != 0 {
		t.Fatalf("steady growth produced %d spikes: %+v", len(detect.spikes), detect.spikes)
	}

	for turn := range spikeWindow {
		detect.observe(aggregateRow{
			ConversationID: "spiky",
			TS:             base + int64(turn)*60,
			FreshInput:     30_000,
		})
	}
	detect.observe(aggregateRow{ConversationID: "spiky", TS: base + 9999, FreshInput: 400_000})
	if len(detect.spikes) != 1 {
		t.Fatalf("expected exactly one spike, got %d", len(detect.spikes))
	}
	if detect.spikes[0].Ratio < spikeMultiple {
		t.Errorf("spike ratio = %f, want >= %f", detect.spikes[0].Ratio, spikeMultiple)
	}
	if detect.spikes[0].ConversationID != "spiky" {
		t.Errorf("spike attributed to %q", detect.spikes[0].ConversationID)
	}
}

func TestSyncStoresFindings(t *testing.T) {
	eachBackend(t, func(t *testing.T, store *Store) {
		root := t.TempDir()
		messages := []string{
			tokensSchemaA("m1", "2026-07-01T10:00:00Z", 10, 50, 1000, 40000),
			tokensSchemaA("m2", "2026-07-01T10:00:12Z", 10, 50, 42000, 0),
		}
		writeConversation(t, root, "a.json", conversation("conv-a", messages...))
		if _, err := store.Sync(context.Background(), root); err != nil {
			t.Fatalf("sync: %v", err)
		}
		breaks, err := store.Findings(context.Background(), FindingCacheBreak, 10)
		if err != nil {
			t.Fatalf("findings: %v", err)
		}
		if len(breaks) != 1 {
			t.Fatalf("got %d cache breaks, want 1", len(breaks))
		}
		if breaks[0].Cause != CausePrefixMutation {
			t.Errorf("cause = %q, want %q", breaks[0].Cause, CausePrefixMutation)
		}
		if breaks[0].Tokens != 42000 {
			t.Errorf("re-paid tokens = %d, want 42000", breaks[0].Tokens)
		}
		if breaks[0].GapSeconds != 12 {
			t.Errorf("gap = %ds, want 12s", breaks[0].GapSeconds)
		}
	})
}

// TestNormalizeReadsPerTTLSplitFromTokens covers the gap that allowed the
// cache-break misclassification to ship: every pre-existing test that
// exercised the per-TTL split fed it through the LEGACY metadata.cache_metrics
// layout, so the current `tokens` layout was never asserted. Current-schema
// rows therefore always reported a zero split, and detectCacheBreak silently
// fell back to assuming a 5 minute TTL.
func TestNormalizeReadsPerTTLSplitFromTokens(t *testing.T) {
	t.Run("current tokens layout carries the 1h split", func(t *testing.T) {
		got := normalize(rawTokens{
			Input: 2, Output: 647, Total: 54840,
			CacheCreation: 54191, Creation1h: 54191,
		}, rawCacheMetrics{})
		if got.Schema != CacheSchemaTokens {
			t.Fatalf("schema = %v, want %v", got.Schema, CacheSchemaTokens)
		}
		if got.CacheWrite1h != 54191 {
			t.Errorf("CacheWrite1h = %d, want 54191", got.CacheWrite1h)
		}
		if got.CacheWrite5m != 0 {
			t.Errorf("CacheWrite5m = %d, want 0", got.CacheWrite5m)
		}
	})

	t.Run("legacy metadata layout still works", func(t *testing.T) {
		got := normalize(rawTokens{Input: 100},
			rawCacheMetrics{Creation: 6423, Creation1h: 6423, Read: 10758})
		if got.Schema != CacheSchemaMetadata {
			t.Fatalf("schema = %v, want %v", got.Schema, CacheSchemaMetadata)
		}
		if got.CacheWrite1h != 6423 {
			t.Errorf("CacheWrite1h = %d, want 6423", got.CacheWrite1h)
		}
	})

	t.Run("tokens split wins over legacy when both present", func(t *testing.T) {
		got := normalize(rawTokens{
			Input: 1, CacheCreation: 900, Creation5m: 900,
		}, rawCacheMetrics{Creation: 500, Creation1h: 500})
		if got.CacheWrite5m != 900 {
			t.Errorf("CacheWrite5m = %d, want 900", got.CacheWrite5m)
		}
	})

	t.Run("absent split stays zero rather than guessing", func(t *testing.T) {
		got := normalize(rawTokens{Input: 5, CacheCreation: 1000}, rawCacheMetrics{})
		if got.CacheWrite5m != 0 || got.CacheWrite1h != 0 {
			t.Errorf("split = (%d,%d), want (0,0)", got.CacheWrite5m, got.CacheWrite1h)
		}
	})
}

// TestAggregateSegmentsCacheCapableTraffic pins the phantom-regression bug.
//
// A provider that reports no prompt-cache activity puts all of its input into
// FreshInput. Blending that into the cache ratio drags it down and looks
// exactly like a cache regression, which is what a naive time series over the
// real index showed: a drop to 60.7% in a month whose Anthropic-only rate was
// actually 93.9%.
func TestAggregateSegmentsCacheCapableTraffic(t *testing.T) {
	rows := []aggregateRow{
		// Cache-capable provider: 900 of 1000 input tokens served from cache.
		{ConversationID: "c", DedupKey: "k1", Seq: 1, TS: 1000, Day: 1,
			FreshInput: 50, CacheWrite: 50, CacheRead: 900, Total: 1000,
			Schema: CacheSchemaTokens},
		// Non-caching provider: large uncached input, no cache fields at all.
		{ConversationID: "c", DedupKey: "k2", Seq: 2, TS: 2000, Day: 1,
			FreshInput: 9000, Total: 9000, Schema: CacheSchemaNone},
	}
	summary, daily, _, err := aggregate(context.Background(), Summary{},
		func(visit func(aggregateRow) error) error {
			for _, r := range rows {
				if err := visit(r); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	// Blended, the ratio is 900/10000 = 9% and looks like a catastrophe.
	blended := float64(summary.CacheRead) /
		float64(summary.FreshInput+summary.CacheWrite+summary.CacheRead)
	if blended > 0.10 {
		t.Fatalf("precondition: blended rate should be badly diluted, got %.3f", blended)
	}

	// Segmented, it correctly reports the cache-capable provider's 90%.
	if got := summary.CacheHitRate(); got < 0.89 || got > 0.91 {
		t.Errorf("Summary.CacheHitRate() = %.3f, want ~0.90", got)
	}
	if summary.CacheCapableResponses != 1 {
		t.Errorf("CacheCapableResponses = %d, want 1", summary.CacheCapableResponses)
	}
	if got := summary.CacheCapableShare(); got < 0.49 || got > 0.51 {
		t.Errorf("CacheCapableShare() = %.3f, want ~0.50", got)
	}

	if len(daily) != 1 {
		t.Fatalf("daily buckets = %d, want 1", len(daily))
	}
	if got := daily[0].CacheHitRate(); got < 0.89 || got > 0.91 {
		t.Errorf("DayBucket.CacheHitRate() = %.3f, want ~0.90", got)
	}
	if daily[0].Responses != 2 || daily[0].CacheCapableResponses != 1 {
		t.Errorf("daily responses = %d/%d, want 2 total and 1 cache-capable",
			daily[0].Responses, daily[0].CacheCapableResponses)
	}
}

// TestCacheHitRateHandlesNoCacheCapableTraffic guards the divide-by-zero path.
func TestCacheHitRateHandlesNoCacheCapableTraffic(t *testing.T) {
	var s Summary
	if got := s.CacheHitRate(); got != 0 {
		t.Errorf("CacheHitRate() = %v, want 0", got)
	}
	if got := s.CacheCapableShare(); got != 0 {
		t.Errorf("CacheCapableShare() = %v, want 0", got)
	}
	var d DayBucket
	if got := d.CacheHitRate(); got != 0 {
		t.Errorf("DayBucket.CacheHitRate() = %v, want 0", got)
	}
}
