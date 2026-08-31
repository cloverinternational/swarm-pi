package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func progressTestPath(t *testing.T, agentID string) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := OutputStorePath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return path
}

func appendRaw(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("append open: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("append write: %v", err)
	}
}

func toolCountOf(t *testing.T, b *BackgroundAgent) int {
	t.Helper()
	b.mu.RLock()
	defer b.mu.RUnlock()
	v, ok := b.result.Metadata["tool_count"]
	if !ok {
		return 0
	}
	n, ok := v.(int)
	if !ok {
		t.Fatalf("tool_count is %T, want int", v)
	}
	return n
}

// tool_count is a cumulative total. Reading only newly appended bytes must
// still produce the same running total a whole-file rescan produced.
func TestProgressScanAccumulatesAcrossChunks(t *testing.T) {
	const agentID = "prog-cumulative"
	path := progressTestPath(t, agentID)

	b := &BackgroundAgent{result: &BackgroundAgentResult{}}
	var st progressScanState

	writeNDJSON(t, path, []OutputRecord{
		{Type: "tool_call", Name: "Read"},
		{Type: "tool_call", Name: "Bash"},
		{Type: "content", Content: "first"},
	})
	b.updateProgressFromFile(agentID, &st)
	if got := toolCountOf(t, b); got != 2 {
		t.Fatalf("after chunk 1 tool_count = %d, want 2", got)
	}

	afterFirst := st.offset
	if afterFirst == 0 {
		t.Fatal("offset did not advance after consuming a chunk")
	}

	writeNDJSON(t, path, []OutputRecord{
		{Type: "tool_call", Name: "Grep"},
		{Type: "tool_call", Name: "Edit"},
	})
	b.updateProgressFromFile(agentID, &st)
	if got := toolCountOf(t, b); got != 4 {
		t.Fatalf("after chunk 2 tool_count = %d, want 4 (cumulative) — earlier chunks were lost", got)
	}
	if st.offset <= afterFirst {
		t.Fatalf("offset did not advance on the second chunk: %d <= %d", st.offset, afterFirst)
	}
}

// With no new bytes the scan must do nothing: no double counting, no rewind.
func TestProgressScanIdempotentWithoutNewData(t *testing.T) {
	const agentID = "prog-idle"
	path := progressTestPath(t, agentID)

	b := &BackgroundAgent{result: &BackgroundAgentResult{}}
	var st progressScanState

	writeNDJSON(t, path, []OutputRecord{{Type: "tool_call", Name: "Read"}})
	b.updateProgressFromFile(agentID, &st)
	off, count := st.offset, toolCountOf(t, b)

	for i := 0; i < 5; i++ {
		b.updateProgressFromFile(agentID, &st)
	}
	if st.offset != off {
		t.Fatalf("offset moved with no new data: %d -> %d", off, st.offset)
	}
	if got := toolCountOf(t, b); got != count {
		t.Fatalf("tool_count changed with no new data: %d -> %d", count, got)
	}
}

// A chunk can end mid-line. That partial record must not be consumed, and must
// still be counted once it is completed.
func TestProgressScanDoesNotLosePartialRecord(t *testing.T) {
	const agentID = "prog-partial"
	path := progressTestPath(t, agentID)

	b := &BackgroundAgent{result: &BackgroundAgentResult{}}
	var st progressScanState

	// One complete record, then a deliberately truncated one.
	appendRaw(t, path, `{"type":"tool_call","name":"Read"}`+"\n")
	appendRaw(t, path, `{"type":"tool_call","na`)

	b.updateProgressFromFile(agentID, &st)
	if got := toolCountOf(t, b); got != 1 {
		t.Fatalf("tool_count = %d, want 1 (partial record must not count)", got)
	}

	// Complete the truncated record.
	appendRaw(t, path, `me":"Bash"}`+"\n")
	b.updateProgressFromFile(agentID, &st)
	if got := toolCountOf(t, b); got != 2 {
		t.Fatalf("tool_count = %d, want 2 — the record split across chunks was dropped", got)
	}
}

// If the store is truncated or replaced, the scan must restart instead of
// parking at an offset past EOF and never reading again.
func TestProgressScanResetsOnTruncation(t *testing.T) {
	const agentID = "prog-truncate"
	path := progressTestPath(t, agentID)

	b := &BackgroundAgent{result: &BackgroundAgentResult{}}
	var st progressScanState

	writeNDJSON(t, path, []OutputRecord{
		{Type: "tool_call", Name: "Read"},
		{Type: "tool_call", Name: "Bash"},
	})
	b.updateProgressFromFile(agentID, &st)
	if st.offset == 0 {
		t.Fatal("offset did not advance before truncation")
	}

	if err := os.Truncate(path, 0); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	b.updateProgressFromFile(agentID, &st) // observes shrink, resets
	if st.offset != 0 {
		t.Fatalf("offset = %d after truncation, want 0", st.offset)
	}

	writeNDJSON(t, path, []OutputRecord{{Type: "tool_call", Name: "Grep"}})
	b.updateProgressFromFile(agentID, &st)
	if got := toolCountOf(t, b); got != 1 {
		t.Fatalf("tool_count = %d after rewrite, want 1 — scan did not resume", got)
	}
}
