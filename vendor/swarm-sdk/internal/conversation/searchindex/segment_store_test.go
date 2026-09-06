package searchindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func openSegmentTestSQLite(t *testing.T) *SQLite {
	t.Helper()
	engine, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func segmentFixture(conversationID string) []Segment {
	at := time.Unix(1700000000, 123)
	return []Segment{
		{
			ConversationID: conversationID, WorkspacePath: "/work", Ordinal: 0,
			MessageID: "m1", Role: "assistant", Kind: SegmentToolCall,
			ToolName: "Bash", CallID: "call-1", Timestamp: at, Text: "run tests",
		},
		{
			ConversationID: conversationID, WorkspacePath: "/work", Ordinal: 1,
			MessageID: "m1", Role: "tool", Kind: SegmentToolResult,
			ToolName: "Bash", CallID: "call-1", Failed: true, Runtime: true,
			Timestamp: at.Add(time.Second), Text: "tests failed",
		},
		{
			ConversationID: conversationID, WorkspacePath: "/work", Ordinal: 2,
			MessageID: "m2", Role: "assistant", Kind: SegmentMessage,
			Text: "explain failure",
		},
	}
}

func TestSQLiteSegmentsRoundTripReplaceAndShrink(t *testing.T) {
	engine := openSegmentTestSQLite(t)
	ctx := context.Background()
	segments := segmentFixture("conversation")
	if err := engine.ReplaceSegments(ctx, "conversation", segments); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReplaceSegments(ctx, "conversation", segments); err != nil {
		t.Fatalf("idempotent replacement: %v", err)
	}
	hits, err := engine.SearchSegments(ctx, SegmentQuery{
		Filter: SegmentFilter{ConversationID: "conversation"},
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]Segment, len(hits))
	for index := range hits {
		got[index] = hits[index].Segment
	}
	// Empty-text results use recency order.
	want := []Segment{segments[1], segments[0], segments[2]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got: %#v\nwant: %#v", got, want)
	}

	if err := engine.ReplaceSegments(ctx, "conversation", segments[:1]); err != nil {
		t.Fatalf("shrink replacement: %v", err)
	}
	count, err := engine.SegmentCount(ctx, SegmentFilter{ConversationID: "conversation"})
	if err != nil || count != 1 {
		t.Fatalf("count after shrink = %d, err=%v", count, err)
	}
	hits, err = engine.SearchSegments(ctx, SegmentQuery{Text: "failed", Limit: 10})
	if err != nil || len(hits) != 0 {
		t.Fatalf("orphan FTS row after shrink: hits=%+v err=%v", hits, err)
	}
}

func TestSQLiteSegmentFiltersAndEmptyText(t *testing.T) {
	engine := openSegmentTestSQLite(t)
	ctx := context.Background()
	if err := engine.ReplaceSegments(ctx, "conversation", segmentFixture("conversation")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		filter SegmentFilter
		want   []int
	}{
		{name: "tool", filter: SegmentFilter{ToolName: "Bash"}, want: []int{1, 0}},
		{name: "kind", filter: SegmentFilter{Kind: SegmentMessage}, want: []int{2}},
		{name: "failed outcome", filter: SegmentFilter{Outcome: OutcomeFailed}, want: []int{1}},
		{name: "exclude runtime", filter: SegmentFilter{ExcludeRuntime: true}, want: []int{0, 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hits, err := engine.SearchSegments(ctx, SegmentQuery{Filter: test.filter, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			var ordinals []int
			for _, hit := range hits {
				ordinals = append(ordinals, hit.Ordinal)
			}
			if !reflect.DeepEqual(ordinals, test.want) {
				t.Fatalf("ordinals=%v want=%v", ordinals, test.want)
			}
		})
	}
}

func TestSQLiteTermStatsUnigramFilteredAndCardinality(t *testing.T) {
	engine := openSegmentTestSQLite(t)
	ctx := context.Background()
	first := []Segment{
		{ConversationID: "one", Ordinal: 0, Kind: SegmentMessage, Role: "user", Text: "alpha alpha beta"},
		{ConversationID: "one", Ordinal: 1, Kind: SegmentMessage, Role: "user", Text: "alpha"},
		{ConversationID: "one", Ordinal: 2, Kind: SegmentToolResult, ToolName: "Bash", Text: "globalonly"},
	}
	second := []Segment{
		{ConversationID: "two", Ordinal: 0, Kind: SegmentMessage, Role: "assistant", Text: "alpha beta"},
	}
	if err := engine.ReplaceSegments(ctx, "one", first); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReplaceSegments(ctx, "two", second); err != nil {
		t.Fatal(err)
	}
	stats, err := engine.TermStats(ctx, SegmentFilter{Role: "user"}, StatsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertTermStat(t, stats, TermStat{Term: "alpha", Occurrences: 3, Segments: 2, Conversations: 1})
	assertTermStat(t, stats, TermStat{Term: "beta", Occurrences: 1, Segments: 1, Conversations: 1})
	if findTermStat(stats, "globalonly") != nil {
		t.Fatalf("filtered statistics returned corpus-wide term: %+v", stats)
	}

	all, err := engine.TermStats(ctx, SegmentFilter{}, StatsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertTermStat(t, all, TermStat{Term: "alpha", Occurrences: 4, Segments: 3, Conversations: 2})
}

func TestSQLiteTermStatsNGramsDoNotCrossSegments(t *testing.T) {
	engine := openSegmentTestSQLite(t)
	ctx := context.Background()
	segments := []Segment{
		{ConversationID: "ngrams", Ordinal: 0, Text: "one two three"},
		{ConversationID: "ngrams", Ordinal: 1, Text: "four five six"},
	}
	if err := engine.ReplaceSegments(ctx, "ngrams", segments); err != nil {
		t.Fatal(err)
	}
	bigrams, err := engine.TermStats(ctx, SegmentFilter{}, StatsOptions{NGram: 2, Top: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"one two", "two three", "four five", "five six"} {
		assertTermStat(t, bigrams, TermStat{Term: term, Occurrences: 1, Segments: 1, Conversations: 1})
	}
	if findTermStat(bigrams, "three four") != nil {
		t.Fatalf("bigram crossed segment boundary: %+v", bigrams)
	}
	trigrams, err := engine.TermStats(ctx, SegmentFilter{}, StatsOptions{NGram: 3, Top: 20})
	if err != nil {
		t.Fatal(err)
	}
	assertTermStat(t, trigrams, TermStat{Term: "one two three", Occurrences: 1, Segments: 1, Conversations: 1})
	assertTermStat(t, trigrams, TermStat{Term: "four five six", Occurrences: 1, Segments: 1, Conversations: 1})
	if findTermStat(trigrams, "two three four") != nil {
		t.Fatalf("trigram crossed segment boundary: %+v", trigrams)
	}
}

func TestSQLiteTermStatsBoundsAndStopwords(t *testing.T) {
	engine := openSegmentTestSQLite(t)
	ctx := context.Background()
	segments := []Segment{{
		ConversationID: "bounds", Ordinal: 0,
		Text: "the the a alpha alpha bravo charlie delta echo",
	}}
	if err := engine.ReplaceSegments(ctx, "bounds", segments); err != nil {
		t.Fatal(err)
	}
	stats, err := engine.TermStats(ctx, SegmentFilter{}, StatsOptions{
		Top: MaxStatsTop + 100, MinCount: 2, MinTermLength: 3, ExcludeStopwords: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Term != "alpha" {
		t.Fatalf("bounded stats=%+v", stats)
	}

	manyWords := make([]string, MaxStatsTop+100)
	for index := range manyWords {
		manyWords[index] = fmt.Sprintf("term%03d", index)
	}
	if err := engine.ReplaceSegments(ctx, "many", []Segment{{
		ConversationID: "many", Ordinal: 0, Text: strings.Join(manyWords, " "),
	}}); err != nil {
		t.Fatal(err)
	}
	stats, err = engine.TermStats(ctx, SegmentFilter{ConversationID: "many"}, StatsOptions{Top: MaxStatsTop + 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != MaxStatsTop {
		t.Fatalf("Top cap returned %d terms, want %d", len(stats), MaxStatsTop)
	}
}

func TestSQLiteV3MigratesToV4ExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE history_index_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO history_index_meta VALUES ('schema_version', '3');
		CREATE TABLE history_documents (
			id TEXT PRIMARY KEY, workspace_path TEXT NOT NULL, title TEXT NOT NULL,
			preview TEXT NOT NULL, body TEXT NOT NULL, message_count INTEGER NOT NULL,
			updated_at INTEGER NOT NULL, origin TEXT NOT NULL,
			body_indexed INTEGER NOT NULL DEFAULT 0, body_skipped INTEGER NOT NULL DEFAULT 0,
			file_mod_unix_nano INTEGER NOT NULL DEFAULT 0, file_size INTEGER NOT NULL DEFAULT 0
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
	if err := engine.ReplaceSegments(context.Background(), "kept", []Segment{{
		ConversationID: "kept", Ordinal: 0, Text: "survives reopen",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	count, err := engine.SegmentCount(context.Background(), SegmentFilter{})
	if err != nil || count != 1 {
		t.Fatalf("second open rebuilt v4: count=%d err=%v", count, err)
	}
	var version int
	if err := engine.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM history_index_meta WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("schema version=%d want=4", version)
	}
}

func TestSQLiteSegmentStoreBounds(t *testing.T) {
	engine := openSegmentTestSQLite(t)
	ctx := context.Background()
	tooMany := make([]Segment, MaxSegmentsPerConversation+1)
	if err := engine.ReplaceSegments(ctx, "many", tooMany); err == nil {
		t.Fatal("expected MaxSegmentsPerConversation error")
	}
	tooLarge := []Segment{{
		ConversationID: "large", Ordinal: 0, Text: strings.Repeat("x", MaxSegmentTextBytes+1),
	}}
	if err := engine.ReplaceSegments(ctx, "large", tooLarge); err == nil {
		t.Fatal("expected MaxSegmentTextBytes error")
	}
}

func TestSyncFilesMaintainsSegmentsAndSurvivesLoaderError(t *testing.T) {
	engine := openSegmentTestSQLite(t)
	ctx := context.Background()
	file := SourceFile{ID: "one", WorkspacePath: "/w", FileModUnixNano: 1, FileSize: 10}
	documentLoads, segmentLoads := 0, 0
	loadDocument := func(context.Context, SourceFile, bool) (Document, bool, error) {
		documentLoads++
		return Document{Title: "one"}, true, nil
	}
	loadSegments := func(_ context.Context, source SourceFile) ([]Segment, error) {
		segmentLoads++
		return []Segment{{ConversationID: source.ID, Ordinal: 0, Text: "indexed"}}, nil
	}
	if err := SyncFiles(ctx, engine, "/w", []SourceFile{file}, false, loadDocument, WithSegments(loadSegments)); err != nil {
		t.Fatal(err)
	}
	if err := SyncFiles(ctx, engine, "/w", []SourceFile{file}, false, loadDocument, WithSegments(loadSegments)); err != nil {
		t.Fatal(err)
	}
	if documentLoads != 1 || segmentLoads != 1 {
		t.Fatalf("loads document=%d segments=%d, want 1 each", documentLoads, segmentLoads)
	}

	file.FileModUnixNano = 2
	badSegments := func(context.Context, SourceFile) ([]Segment, error) {
		segmentLoads++
		return nil, errors.New("unreadable")
	}
	if err := SyncFiles(ctx, engine, "/w", []SourceFile{file}, false, loadDocument, WithSegments(badSegments)); err != nil {
		t.Fatalf("one segment loader error aborted sync: %v", err)
	}
	if documentLoads != 1 {
		t.Fatalf("document refreshed despite segment loader failure: loads=%d", documentLoads)
	}
	if err := SyncFiles(ctx, engine, "/w", nil, false, loadDocument, WithSegments(loadSegments)); err != nil {
		t.Fatal(err)
	}
	count, err := engine.SegmentCount(ctx, SegmentFilter{})
	if err != nil || count != 0 {
		t.Fatalf("deleted conversation retained segments: count=%d err=%v", count, err)
	}
}

func assertTermStat(t *testing.T, stats []TermStat, want TermStat) {
	t.Helper()
	got := findTermStat(stats, want.Term)
	if got == nil || *got != want {
		t.Fatalf("term %q: got=%+v want=%+v (all=%+v)", want.Term, got, want, stats)
	}
}

func findTermStat(stats []TermStat, term string) *TermStat {
	for index := range stats {
		if stats[index].Term == term {
			return &stats[index]
		}
	}
	return nil
}
