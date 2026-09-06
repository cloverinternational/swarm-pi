package inbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testFS is a minimal, self-contained FileSystem backed directly by the
// real filesystem under a t.TempDir() root. It deliberately does NOT
// reimplement internal/a2a's symlink/ownership hardening (SafeReadFile,
// IsUnsafeEntryError are trivial pass-throughs here) — this package's
// tests exist to prove inbox.Store's NDJSON/legacy-array/compaction/
// concurrency behavior, not to re-test internal/a2a's already-covered
// registry security primitives. WithHandle uses a real per-path
// sync.Mutex so the concurrent-SendMessage test below exercises genuine
// mutual exclusion, matching the locking contract
// internal/a2a.withRegistryHandle provides in production.
type testFS struct {
	locks sync.Map // path -> *sync.Mutex
}

func (fs *testFS) SecureDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}

func (fs *testFS) ResolvePath(dir, name, ext string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid name %q", name)
	}
	return filepath.Join(dir, name+ext), nil
}

func (fs *testFS) handleMutex(path string) *sync.Mutex {
	v, _ := fs.locks.LoadOrStore(path, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (fs *testFS) WithHandle(path string, fn func() error) error {
	mu := fs.handleMutex(path)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

func (fs *testFS) SafeReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (fs *testFS) ReadFile(path string) ([]byte, error)     { return os.ReadFile(path) }

func (fs *testFS) RemoveFile(path string) error {
	err := os.Remove(path)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

func (fs *testFS) WriteFileLocked(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func (fs *testFS) WriteFileAtomic(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func (fs *testFS) IsUnsafeEntryError(err error) bool { return false }

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	swarmPath := t.TempDir()
	return NewStore(&testFS{}), swarmPath
}

// TestSendMessage_NDJSONRoundTrip proves the append-only NDJSON write path:
// several messages sent to the same handle round-trip through ReadInbox in
// send order with every field intact, and the on-disk file is genuinely
// NDJSON (one JSON object per line, not a JSON array).
func TestSendMessage_NDJSONRoundTrip(t *testing.T) {
	store, swarmPath := newTestStore(t)

	if err := store.SendMessage(swarmPath, "bob", "alice", "hello"); err != nil {
		t.Fatalf("SendMessage 1: %v", err)
	}
	if err := store.SendMessage(swarmPath, "bob", "carol", "world"); err != nil {
		t.Fatalf("SendMessage 2: %v", err)
	}

	msgs, err := store.ReadInbox(swarmPath, "bob")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].From != "alice" || msgs[0].Text != "hello" {
		t.Errorf("msg[0] = %+v, want From=alice Text=hello", msgs[0])
	}
	if msgs[1].From != "carol" || msgs[1].Text != "world" {
		t.Errorf("msg[1] = %+v, want From=carol Text=world", msgs[1])
	}
	for i, m := range msgs {
		if m.Timestamp.IsZero() {
			t.Errorf("msg[%d].Timestamp is zero", i)
		}
		if m.Read {
			t.Errorf("msg[%d].Read = true, want false for a freshly sent message", i)
		}
	}

	// Verify the on-disk format is genuinely NDJSON: one JSON object per
	// non-empty line, and the first byte is NOT '[' (which would indicate
	// the legacy array format).
	raw, err := os.ReadFile(filepath.Join(swarmPath, "inboxes", "bob.json"))
	if err != nil {
		t.Fatalf("read raw inbox file: %v", err)
	}
	if len(raw) == 0 || raw[0] == '[' {
		t.Fatalf("expected NDJSON on-disk format (first byte != '['), got: %q", raw)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %q", len(lines), raw)
	}
	for i, line := range lines {
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line %d is not valid JSON: %v (%q)", i, err, line)
		}
	}
}

// TestReadInbox_LegacyArrayBackCompat proves ReadInbox transparently
// parses an inbox file still in the old JSON-array format (as if written
// by pre-NDJSON code, or by another process that has not yet upgraded it).
func TestReadInbox_LegacyArrayBackCompat(t *testing.T) {
	store, swarmPath := newTestStore(t)
	dir := filepath.Join(swarmPath, "inboxes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := []Message{
		{From: "alice", Text: "legacy-1", Timestamp: time.Now().UTC(), Read: false},
		{From: "bob", Text: "legacy-2", Timestamp: time.Now().UTC(), Read: true},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dave.json"), data, 0o600); err != nil {
		t.Fatalf("write legacy fixture: %v", err)
	}

	msgs, err := store.ReadInbox(swarmPath, "dave")
	if err != nil {
		t.Fatalf("ReadInbox on legacy array: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 legacy messages, got %d", len(msgs))
	}
	if msgs[0].Text != "legacy-1" || msgs[1].Text != "legacy-2" {
		t.Errorf("unexpected legacy message contents: %+v", msgs)
	}
}

// TestSendMessage_UpgradesLegacyArrayOnWrite proves the first SendMessage
// against an old-format (JSON-array) inbox rewrites the whole file as
// NDJSON, preserving the pre-existing messages plus the newly appended one.
func TestSendMessage_UpgradesLegacyArrayOnWrite(t *testing.T) {
	store, swarmPath := newTestStore(t)
	dir := filepath.Join(swarmPath, "inboxes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := []Message{{From: "alice", Text: "legacy-1", Timestamp: time.Now().UTC()}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy fixture: %v", err)
	}
	path := filepath.Join(dir, "erin.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write legacy fixture: %v", err)
	}

	if err := store.SendMessage(swarmPath, "erin", "carol", "new-message"); err != nil {
		t.Fatalf("SendMessage on legacy inbox: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgraded file: %v", err)
	}
	if len(raw) == 0 || raw[0] == '[' {
		t.Fatalf("expected file to be upgraded to NDJSON, still starts with '[': %q", raw)
	}

	msgs, err := store.ReadInbox(swarmPath, "erin")
	if err != nil {
		t.Fatalf("ReadInbox after upgrade: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after upgrade (1 legacy + 1 new), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Text != "legacy-1" || msgs[1].Text != "new-message" {
		t.Errorf("unexpected messages after upgrade: %+v", msgs)
	}
}

// TestSendMessage_TruncatesAtMaxMessages proves SendMessage caps the
// retained message count at MaxMessages, dropping the oldest entries
// first (FIFO eviction), matching the exact behavior the pre-extraction
// internal/a2a.SendMessage implemented inline on every write.
func TestSendMessage_TruncatesAtMaxMessages(t *testing.T) {
	store, swarmPath := newTestStore(t)

	total := MaxMessages + 25
	for i := 0; i < total; i++ {
		text := fmt.Sprintf("msg-%d", i)
		if err := store.SendMessage(swarmPath, "frank", "grace", text); err != nil {
			t.Fatalf("SendMessage %d: %v", i, err)
		}
	}

	msgs, err := store.ReadInbox(swarmPath, "frank")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) != MaxMessages {
		t.Fatalf("expected exactly MaxMessages=%d retained, got %d", MaxMessages, len(msgs))
	}
	// The oldest (msg-0 .. msg-24) must have been evicted; the newest
	// (msg-<total-1>) must be the last entry.
	wantFirst := fmt.Sprintf("msg-%d", total-MaxMessages)
	wantLast := fmt.Sprintf("msg-%d", total-1)
	if msgs[0].Text != wantFirst {
		t.Errorf("oldest retained message = %q, want %q", msgs[0].Text, wantFirst)
	}
	if msgs[len(msgs)-1].Text != wantLast {
		t.Errorf("newest retained message = %q, want %q", msgs[len(msgs)-1].Text, wantLast)
	}
}

// TestClearInbox_RemovesFile proves ClearInbox deletes the inbox file
// entirely (not just truncates it), and that a subsequent ReadInbox on a
// cleared/never-existed inbox returns an empty, non-nil slice rather than
// an error.
func TestClearInbox_RemovesFile(t *testing.T) {
	store, swarmPath := newTestStore(t)

	if err := store.SendMessage(swarmPath, "henry", "iris", "will be cleared"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	path := filepath.Join(swarmPath, "inboxes", "henry.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected inbox file to exist before Clear: %v", err)
	}

	if err := store.ClearInbox(swarmPath, "henry"); err != nil {
		t.Fatalf("ClearInbox: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected inbox file removed after Clear, stat err = %v", err)
	}

	msgs, err := store.ReadInbox(swarmPath, "henry")
	if err != nil {
		t.Fatalf("ReadInbox after Clear: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected empty inbox after Clear, got %d messages", len(msgs))
	}
}

// TestReadInbox_NeverSent proves ReadInbox on a handle that has never
// received a message returns an empty, non-nil slice and no error (never
// os.ErrNotExist bubbled up to the caller).
func TestReadInbox_NeverSent(t *testing.T) {
	store, swarmPath := newTestStore(t)

	msgs, err := store.ReadInbox(swarmPath, "never-sent-to")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if msgs == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

// TestSendMessage_ConcurrentSafety proves concurrent SendMessage calls to
// the SAME handle never lose a write: N goroutines each send one uniquely
// identifiable message concurrently, and ReadInbox afterward must contain
// all N. This exercises the WithHandle-based per-path locking the same
// way internal/a2a.withRegistryHandle serializes concurrent
// production callers (e.g. two peers DMing the same recipient at once).
func TestSendMessage_ConcurrentSafety(t *testing.T) {
	store, swarmPath := newTestStore(t)

	const n = 50
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := fmt.Sprintf("concurrent-%d", i)
			if err := store.SendMessage(swarmPath, "jack", "kim", text); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent SendMessage failed: %v", err)
	}

	msgs, err := store.ReadInbox(swarmPath, "jack")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) != n {
		t.Fatalf("expected all %d concurrent messages retained (n < MaxMessages=%d so none should be evicted), got %d", n, MaxMessages, len(msgs))
	}
	seen := make(map[string]bool, n)
	for _, m := range msgs {
		seen[m.Text] = true
	}
	for i := 0; i < n; i++ {
		text := fmt.Sprintf("concurrent-%d", i)
		if !seen[text] {
			t.Errorf("message %q missing from inbox after concurrent sends — a write was lost", text)
		}
	}
}

// TestReadLegacyArrayInbox_Standalone exercises the standalone
// readLegacyArrayInbox helper directly (ported verbatim from
// internal/a2a/discovery.go; unused by the production SendMessage/
// ReadInbox call paths, which do their own inline legacy-array detection,
// but preserved for behavioral parity — see inbox.go's doc comment).
func TestReadLegacyArrayInbox_Standalone(t *testing.T) {
	store, swarmPath := newTestStore(t)
	dir := filepath.Join(swarmPath, "inboxes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := []Message{{From: "alice", Text: "standalone-legacy"}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, "leo.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgs, err := store.readLegacyArrayInbox(path)
	if err != nil {
		t.Fatalf("readLegacyArrayInbox: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "standalone-legacy" {
		t.Fatalf("unexpected result: %+v", msgs)
	}

	// A missing file must return an empty slice, not an error.
	msgs, err = store.readLegacyArrayInbox(filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Fatalf("readLegacyArrayInbox on missing file: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected empty slice for missing file, got %d", len(msgs))
	}
}

// TestMaybeCompactInbox_CompactsAboveThreshold proves the ported
// maybeCompactInbox helper rewrites an oversized NDJSON file down to
// MaxMessages entries once its line count exceeds CompactionThreshold,
// and is a no-op below the threshold. Like readLegacyArrayInbox and
// writeNDJSONInbox, this helper is not wired into the production
// SendMessage call path (which truncates inline on every write instead)
// — this test proves the ported logic itself is correct and available,
// matching the pre-extraction file's (dead-code) behavior exactly.
func TestMaybeCompactInbox_CompactsAboveThreshold(t *testing.T) {
	store, swarmPath := newTestStore(t)
	dir := filepath.Join(swarmPath, "inboxes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "mona.json")

	// Below threshold: build exactly CompactionThreshold lines (not
	// exceeding it) and confirm maybeCompactInbox is a no-op.
	var msgs []Message
	for i := 0; i < CompactionThreshold; i++ {
		msgs = append(msgs, Message{From: "a", Text: fmt.Sprintf("m%d", i), Timestamp: time.Now().UTC()})
	}
	writeRawNDJSON(t, path, msgs)
	if err := store.maybeCompactInbox(path); err != nil {
		t.Fatalf("maybeCompactInbox (at threshold): %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lineCount := strings.Count(strings.TrimRight(string(raw), "\n"), "\n") + 1
	if lineCount != CompactionThreshold {
		t.Fatalf("expected no-op at exactly CompactionThreshold=%d lines, file now has %d lines", CompactionThreshold, lineCount)
	}

	// Above threshold: add one more line to cross CompactionThreshold and
	// confirm compaction rewrites the file down to MaxMessages lines,
	// keeping the most recent entries.
	msgs = append(msgs, Message{From: "a", Text: "trigger", Timestamp: time.Now().UTC()})
	writeRawNDJSON(t, path, msgs)
	if err := store.maybeCompactInbox(path); err != nil {
		t.Fatalf("maybeCompactInbox (above threshold): %v", err)
	}
	compacted, err := store.ReadInbox(swarmPath, "mona")
	if err != nil {
		t.Fatalf("ReadInbox after compaction: %v", err)
	}
	if len(compacted) != MaxMessages {
		t.Fatalf("expected compaction to leave exactly MaxMessages=%d entries, got %d", MaxMessages, len(compacted))
	}
	if compacted[len(compacted)-1].Text != "trigger" {
		t.Errorf("expected most recent entry %q retained after compaction, got %q", "trigger", compacted[len(compacted)-1].Text)
	}
}

// writeRawNDJSON is a test helper writing msgs as raw NDJSON directly
// (bypassing SendMessage/Store entirely) so compaction-threshold tests can
// construct an oversized file precisely without depending on
// MaxMessages-triggered truncation along the way.
func writeRawNDJSON(t *testing.T, path string, msgs []Message) {
	t.Helper()
	var buf strings.Builder
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write raw ndjson: %v", err)
	}
}
