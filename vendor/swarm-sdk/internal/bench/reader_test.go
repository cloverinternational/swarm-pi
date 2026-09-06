package bench

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// (b) missing/empty ledger — the reader's job is to make this indistinguishable
// from "nothing recorded yet", never an error.
// ---------------------------------------------------------------------------

func TestListEffectFiles_MissingDir_EmptyNotError(t *testing.T) {
	files, err := ListEffectFiles(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListEffectFiles on a missing dir must not error, got: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected zero files, got %d", len(files))
	}
}

func TestReadEffects_EmptyFileList_CleanZeroResult(t *testing.T) {
	stats, err := ReadEffects(nil, func(Effect) error { return nil })
	if err != nil {
		t.Fatalf("ReadEffects(nil): %v", err)
	}
	if stats.RowsRead != 0 || stats.FilesRead != 0 || stats.LinesSkipped != 0 {
		t.Fatalf("expected an all-zero ReadStats, got %+v", stats)
	}
}

// ---------------------------------------------------------------------------
// (c) a truncated final line (the writer is append-only and may be
// mid-write) must be skipped, never fail the whole read.
// ---------------------------------------------------------------------------

func TestReadEffects_TruncatedFinalLine_SkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "effects_2026-07-25-06_pid1.jsonl")

	// Two whole, well-formed rows followed by a THIRD row that was cut off
	// mid-append: valid JSON prefix, no closing brace, no trailing newline —
	// exactly what a reader can observe if it races ledger.go's writer
	// between the Write() call landing the first N bytes and the rest.
	content := `{"path":"/a.txt","tool":"t1","post_blob":"aaaa","ts":"2026-07-25T06:00:00Z"}
{"path":"/b.txt","tool":"t2","post_blob":"bbbb","ts":"2026-07-25T06:01:00Z"}
{"path":"/c.txt","tool":"t3","post_blob":"cccc`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	files, err := ListEffectFiles(dir)
	if err != nil {
		t.Fatalf("ListEffectFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	var got []Effect
	stats, err := ReadEffects(files, func(eff Effect) error {
		got = append(got, eff)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadEffects must not fail on a truncated final line, got: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected exactly the 2 well-formed rows, got %d: %+v", len(got), got)
	}
	if got[0].Path != "/a.txt" || got[1].Path != "/b.txt" {
		t.Fatalf("unexpected rows: %+v", got)
	}
	if stats.RowsRead != 2 {
		t.Fatalf("stats.RowsRead = %d, want 2", stats.RowsRead)
	}
	if stats.LinesSkipped != 1 {
		t.Fatalf("stats.LinesSkipped = %d, want 1 (the truncated line)", stats.LinesSkipped)
	}
}

// TestReadEffects_MultipleFiles_AllRowsSeen proves the truncation tolerance
// above is not accidentally hiding a "stop at first bad file" bug: a second,
// entirely well-formed file after the truncated one must still be read.
func TestReadEffects_MultipleFiles_AllRowsSeen(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "effects_2026-07-25-06_pid1.jsonl"),
		`{"path":"/a.txt","tool":"t1","post_blob":"aaaa","ts":"2026-07-25T06:00:00Z"}`+"\n"+
			`{"path":"/broken.txt","tool":"t2"`) // truncated, no newline
	mustWriteFile(t, filepath.Join(dir, "effects_2026-07-25-07_pid1.jsonl"),
		`{"path":"/z.txt","tool":"t3","post_blob":"zzzz","ts":"2026-07-25T07:00:00Z"}`+"\n")

	files, err := ListEffectFiles(dir)
	if err != nil {
		t.Fatalf("ListEffectFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	// Filenames sort by hour first, so the earlier hour must come first.
	if files[0].Name > files[1].Name {
		t.Fatalf("files not sorted: %v", files)
	}

	var got []Effect
	stats, err := ReadEffects(files, func(eff Effect) error {
		got = append(got, eff)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadEffects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 well-formed rows across both files, got %d: %+v", len(got), got)
	}
	if stats.FilesRead != 2 {
		t.Fatalf("stats.FilesRead = %d, want 2", stats.FilesRead)
	}
	if stats.LinesSkipped != 1 {
		t.Fatalf("stats.LinesSkipped = %d, want 1", stats.LinesSkipped)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// (d) an effect with an empty PostBlob is "not established" — the reader's
// job here is only to hand the row through unmodified; the bucket split
// itself is cmd/swarm-bench's responsibility (survive.go:groupByBlob), but
// this test proves the reader never fabricates a value or drops the row.
// ---------------------------------------------------------------------------

func TestReadEffects_EmptyPostBlob_PassedThroughVerbatim(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "effects_2026-07-25-06_pid1.jsonl"),
		`{"path":"/deleted.txt","tool":"t1","op":"delete","ts":"2026-07-25T06:00:00Z"}`+"\n")

	files, err := ListEffectFiles(dir)
	if err != nil {
		t.Fatalf("ListEffectFiles: %v", err)
	}
	var got []Effect
	if _, err := ReadEffects(files, func(eff Effect) error {
		got = append(got, eff)
		return nil
	}); err != nil {
		t.Fatalf("ReadEffects: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].PostBlob != "" {
		t.Fatalf("PostBlob must stay empty (not established), got %q", got[0].PostBlob)
	}
	if got[0].Op != "delete" {
		t.Fatalf("Op = %q, want delete", got[0].Op)
	}
}

// ---------------------------------------------------------------------------
// Bucket/base-dir resolution must match the writer's exactly (ledger.go),
// or a reader could silently look in the wrong place.
// ---------------------------------------------------------------------------

func TestBucketWiring_MatchesWhatTheWriterActuallyWrote(t *testing.T) {
	baseDir := t.TempDir()
	restoreDir := SetBaseDirForTest(baseDir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	work := t.TempDir() // not a git repo: exercises the fallback bucket
	Record(Effect{Path: filepath.Join(work, "f.txt"), Tool: "test_tool", TS: time.Now()}, work, false)
	FlushForTest()

	bucket := BucketFor(work)
	if bucket == "" {
		t.Fatalf("BucketFor returned empty bucket for a non-empty workspace path")
	}
	dir := BucketDir(baseDir, bucket)
	files, err := ListEffectFiles(dir)
	if err != nil {
		t.Fatalf("ListEffectFiles(%s): %v", dir, err)
	}
	if len(files) != 1 {
		t.Fatalf("expected the reader's resolved bucket dir to contain the 1 file the writer just wrote; got %d files in %s", len(files), dir)
	}
}
