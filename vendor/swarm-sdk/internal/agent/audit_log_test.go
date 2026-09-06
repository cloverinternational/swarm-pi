package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuditLog_NilSafe(t *testing.T) {
	var a *AuditLog
	a.Write(AuditRecord{Kind: "test"}) // must not panic
	if got := a.WrittenTotal(); got != 0 {
		t.Errorf("nil log WrittenTotal should be 0, got %d", got)
	}
	if got := a.DroppedTotal(); got != 0 {
		t.Errorf("nil log DroppedTotal should be 0, got %d", got)
	}
	if got := a.Path(); got != "" {
		t.Errorf("nil log Path should be empty, got %q", got)
	}
	if err := a.Close(); err != nil {
		t.Errorf("nil log Close should not error, got %v", err)
	}
}

func TestNewAuditLog_EmptyPathReturnsNil(t *testing.T) {
	a, err := NewAuditLog("")
	if err != nil {
		t.Fatalf("empty path should not error, got %v", err)
	}
	if a != nil {
		t.Fatal("empty path should return nil log")
	}
}

func TestAuditLog_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "steering.jsonl")

	a, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}

	a.Write(AuditRecord{Kind: "snapshot", Concerns: 2, Flushed: 5, Dropped: 1})
	a.Write(AuditRecord{Kind: "halt", Peer: "alice", Reason: "loop", TTLSeconds: 60})
	a.Write(AuditRecord{Kind: "ask", Question: "Continue?", Urgency: "high"})

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := a.WrittenTotal(); got != 3 {
		t.Errorf("expected WrittenTotal=3, got %d", got)
	}

	// Read back and verify shape.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// Verify each is valid JSON with expected fields.
	type rec struct {
		Timestamp  string `json:"ts"`
		Kind       string `json:"kind"`
		Concerns   int    `json:"concerns,omitempty"`
		Peer       string `json:"peer,omitempty"`
		Question   string `json:"question,omitempty"`
		Urgency    string `json:"urgency,omitempty"`
		TTLSeconds int    `json:"ttl_seconds,omitempty"`
	}
	for i, line := range lines {
		var r rec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, line)
		}
		if r.Timestamp == "" {
			t.Errorf("line %d missing timestamp: %s", i, line)
		}
	}

	// First record was a snapshot.
	if !strings.Contains(lines[0], `"kind":"snapshot"`) {
		t.Errorf("expected first kind=snapshot, got: %s", lines[0])
	}
	if !strings.Contains(lines[0], `"concerns":2`) {
		t.Errorf("expected concerns=2, got: %s", lines[0])
	}
}

func TestAuditLog_AppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "steering.jsonl")

	// First session writes 1 record.
	a1, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog #1: %v", err)
	}
	a1.Write(AuditRecord{Kind: "snapshot", Concerns: 1})
	if err := a1.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}

	// Second session appends another.
	a2, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog #2: %v", err)
	}
	a2.Write(AuditRecord{Kind: "snapshot", Concerns: 2})
	if err := a2.Close(); err != nil {
		t.Fatalf("Close #2: %v", err)
	}

	// File should contain 2 lines total.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines after re-open, got %d:\n%s", len(lines), data)
	}
}

func TestAuditLog_AutoTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")
	a, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}
	defer a.Close()

	before := time.Now().UTC()
	a.Write(AuditRecord{Kind: "test"})
	a.Close()

	data, _ := os.ReadFile(path)
	var got AuditRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Timestamp == "" {
		t.Fatal("auto timestamp not set")
	}
	parsed, err := time.Parse(time.RFC3339Nano, got.Timestamp)
	if err != nil {
		t.Fatalf("timestamp not RFC3339Nano: %v", err)
	}
	if parsed.Before(before.Add(-time.Second)) || parsed.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("auto timestamp out of range: %v", parsed)
	}
}

func TestAuditLog_PreservesProvidedTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")
	a, _ := NewAuditLog(path)
	defer a.Close()

	a.Write(AuditRecord{Timestamp: "2025-01-01T00:00:00Z", Kind: "test"})
	a.Close()

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"ts":"2025-01-01T00:00:00Z"`) {
		t.Errorf("provided timestamp not preserved: %s", data)
	}
}

func TestAuditLog_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	a, _ := NewAuditLog(filepath.Join(dir, "x.jsonl"))
	if err := a.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("second Close should be no-op, got %v", err)
	}
}

func TestAuditLog_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")
	a, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}
	a.Close()
	// Must not panic; record is silently dropped.
	a.Write(AuditRecord{Kind: "after-close"})

	// Assert the record was actually dropped — nothing written after Close.
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "after-close") {
		t.Errorf("record written after Close() was not dropped: %q", string(data))
	}
}

func TestAuditLog_Concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.jsonl")
	a, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			a.Write(AuditRecord{Kind: "test", Concerns: id})
		}(i)
	}
	wg.Wait()

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 32 {
		t.Errorf("expected 32 records, got %d", len(lines))
	}
}

func TestAuditLog_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "nested", "subdir", "audit.jsonl")
	a, err := NewAuditLog(deep)
	if err != nil {
		t.Fatalf("NewAuditLog with nested path: %v", err)
	}
	defer a.Close()
	a.Write(AuditRecord{Kind: "test"})
	a.Close()

	if _, err := os.Stat(deep); err != nil {
		t.Errorf("expected file created at %s, got %v", deep, err)
	}
}
