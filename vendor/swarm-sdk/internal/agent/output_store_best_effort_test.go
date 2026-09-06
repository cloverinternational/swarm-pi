package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadOutputStoreBestEffort_ReturnsFullContentWhenPresent proves the
// happy path of issue #284's fallback: a normal, fully-written output file
// is read back byte-for-byte, exactly like ReadOutputStore would return for
// a full read from offset 0.
func TestReadOutputStoreBestEffort_ReturnsFullContentWhenPresent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const agentID = "best-effort-happy-path"
	path := OutputStorePath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := `{"type":"content","content":"hello"}` + "\n" + `{"type":"final","content":"done"}` + "\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	got, err := ReadOutputStoreBestEffort(agentID)
	if err != nil {
		t.Fatalf("ReadOutputStoreBestEffort: %v", err)
	}
	if got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

// TestReadOutputStoreBestEffort_MissingFileReturnsEmptyNoError covers the
// "agent never wrote anything" case: no file at all must be a clean ("", nil)
// rather than an error, matching ReadOutputStore's own not-exist handling.
func TestReadOutputStoreBestEffort_MissingFileReturnsEmptyNoError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	got, err := ReadOutputStoreBestEffort("no-such-agent-ever-wrote-anything")
	if err != nil {
		t.Fatalf("expected no error for a missing file, got: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty content for a missing file, got %d bytes", len(got))
	}
}

// TestReadOutputStoreBestEffort_CapsAtDefaultMaxReadBytes proves the
// fallback still respects the same size cap ReadOutputStore enforces, so a
// runaway output file can never be handed back to a caller in one
// unbounded read.
func TestReadOutputStoreBestEffort_CapsAtDefaultMaxReadBytes(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const agentID = "best-effort-oversized"
	path := OutputStorePath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oversized := make([]byte, defaultMaxReadBytes+1024)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if err := os.WriteFile(path, oversized, 0o644); err != nil {
		t.Fatalf("write oversized test file: %v", err)
	}

	got, err := ReadOutputStoreBestEffort(agentID)
	if err != nil {
		t.Fatalf("ReadOutputStoreBestEffort: %v", err)
	}
	if int64(len(got)) != defaultMaxReadBytes {
		t.Fatalf("len(got) = %d, want exactly the cap %d", len(got), defaultMaxReadBytes)
	}
}
