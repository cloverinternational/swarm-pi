package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestStore creates an OutputStore backed by a file in dir with the given name.
// The store is registered for cleanup via t.Cleanup.
func newTestStore(t *testing.T, dir, name string) *OutputStore {
	t.Helper()
	path := filepath.Join(dir, name+".output")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("newTestStore OpenFile %s: %v", path, err)
	}
	store := &OutputStore{
		path: path,
		f:    f,
		buf:  bufio.NewWriterSize(f, 64*1024),
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// writeNDJSON writes a slice of OutputRecords to path as NDJSON.
func writeNDJSON(t *testing.T, path string, records []OutputRecord) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("writeNDJSON OpenFile: %v", err)
	}
	defer f.Close()
	for _, rec := range records {
		rec.TS = time.Now().UnixMilli()
		line, _ := json.Marshal(rec)
		f.Write(line)         //nolint:errcheck
		f.Write([]byte("\n")) //nolint:errcheck
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestOutputStore_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir, "write-read")

	recs := []OutputRecord{
		{Type: "content", Content: "hello world"},
		{Type: "tool_call", Name: "Read"},
		{Type: "final", Content: "done"},
	}
	for _, rec := range recs {
		if err := store.WriteRecord(rec); err != nil {
			t.Fatalf("WriteRecord: %v", err)
		}
	}
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	parsed := ParseOutputStoreRecords(string(data))
	if len(parsed) != 3 {
		t.Fatalf("expected 3 records, got %d\nraw:\n%s", len(parsed), string(data))
	}
	if parsed[0].Type != "content" || parsed[0].Content != "hello world" {
		t.Errorf("parsed[0] = %+v", parsed[0])
	}
	if parsed[1].Type != "tool_call" || parsed[1].Name != "Read" {
		t.Errorf("parsed[1] = %+v", parsed[1])
	}
	if parsed[2].Type != "final" || parsed[2].Content != "done" {
		t.Errorf("parsed[2] = %+v", parsed[2])
	}
}

func TestOutputStore_OffsetRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "offset.output")

	writeNDJSON(t, path, []OutputRecord{
		{Type: "content", Content: "line one"},
		{Type: "content", Content: "line two"},
		{Type: "content", Content: "line three"},
	})

	allBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Find byte offset of the second record (after first newline).
	firstNewline := strings.IndexByte(string(allBytes), '\n')
	if firstNewline < 0 {
		t.Fatal("no newline in output file")
	}
	offset := int64(firstNewline + 1)

	remaining := allBytes[offset:]
	records := ParseOutputStoreRecords(string(remaining))
	if len(records) != 2 {
		t.Fatalf("expected 2 records after offset, got %d", len(records))
	}
	if records[0].Content != "line two" {
		t.Errorf("records[0].Content = %q, want %q", records[0].Content, "line two")
	}
	if records[1].Content != "line three" {
		t.Errorf("records[1].Content = %q, want %q", records[1].Content, "line three")
	}
}

func TestOutputStore_ConcurrentWrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := newTestStore(t, dir, "concurrent")

	const goroutines = 20
	const recsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			for j := range recsPerGoroutine {
				rec := OutputRecord{
					Type:    "content",
					Content: fmt.Sprintf("goroutine=%d record=%d", idx, j),
				}
				if err := store.WriteRecord(rec); err != nil {
					t.Errorf("WriteRecord goroutine=%d: %v", idx, err)
				}
				// Flush every 10 records to simulate realistic write patterns.
				if j%10 == 0 {
					_ = store.Flush()
				}
			}
		}(i)
	}
	wg.Wait()
	_ = store.Flush()

	// Every line in the file must be parseable JSON.
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	records := ParseOutputStoreRecords(string(data))
	expected := goroutines * recsPerGoroutine
	if len(records) != expected {
		t.Errorf("expected %d records, got %d", expected, len(records))
	}
}

func TestParseOutputStoreRecords_MalformedLines(t *testing.T) {
	content := `{"type":"content","ts":1000,"content":"good line 1"}
not-json-at-all
{"type":"final","ts":2000,"content":"good line 2"}
{broken json
{"type":"tool_call","ts":3000,"name":"Read"}
`
	records := ParseOutputStoreRecords(content)
	if len(records) != 3 {
		t.Fatalf("expected 3 valid records, got %d", len(records))
	}
	if records[0].Type != "content" {
		t.Errorf("records[0].Type = %q", records[0].Type)
	}
	if records[1].Type != "final" {
		t.Errorf("records[1].Type = %q", records[1].Type)
	}
	if records[2].Type != "tool_call" || records[2].Name != "Read" {
		t.Errorf("records[2] = %+v", records[2])
	}
}

func TestBuildResumeContext_MissingFile(t *testing.T) {
	// A non-existent agent ID must return ("", nil) — not an error.
	ctx, err := BuildResumeContext("definitely-does-not-exist-xyzzy-987654321")
	if err != nil {
		t.Errorf("expected no error for missing file, got: %v", err)
	}
	if ctx != "" {
		t.Errorf("expected empty context for missing file, got: %q", ctx)
	}
}

func TestBuildResumeContext_WithRecords(t *testing.T) {
	// Write a fake output file into the real cache dir so BuildResumeContext
	// finds it. We use a well-known test agent ID and clean up after.
	agentID := fmt.Sprintf("test-resume-%d", time.Now().UnixNano())
	path := OutputStorePath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	writeNDJSON(t, path, []OutputRecord{
		{Type: "content", Content: "I analysed the code."},
		{Type: "tool_call", Name: "Bash"},
		{Type: "tool_result", Output: "exit 0"},
		{Type: "final", Content: "Task complete."},
	})

	ctx, err := BuildResumeContext(agentID)
	if err != nil {
		t.Fatalf("BuildResumeContext: %v", err)
	}
	if ctx == "" {
		t.Fatal("expected non-empty resume context")
	}
	if !strings.Contains(ctx, agentID) {
		t.Errorf("expected agent ID %q in context, got:\n%s", agentID, ctx)
	}
	if !strings.Contains(ctx, "Task complete.") {
		t.Errorf("expected final message in context, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "PRIOR RUN CONTEXT") {
		t.Errorf("expected PRIOR RUN CONTEXT header, got:\n%s", ctx)
	}
}

func TestOutputStore_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir, "close-idempotent")

	// First close.
	if err := store.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second close must be a no-op (f == nil guard in Close()).
	if err := store.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestOutputStore_PathReturnsCorrectly(t *testing.T) {
	agentID := "path-check-agent"
	expected := OutputStorePath(agentID)
	if !strings.HasSuffix(expected, agentID+".output") {
		t.Errorf("OutputStorePath(%q) = %q, expected suffix %q", agentID, expected, agentID+".output")
	}
}
