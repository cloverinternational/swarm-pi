package bench

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

// ---------------------------------------------------------------------------
// (b) gate OFF ⇒ zero rows and no file created.
// ---------------------------------------------------------------------------

func TestRecord_GateOff_NoFileNoRows(t *testing.T) {
	dir := t.TempDir()
	restoreDir := SetBaseDirForTest(dir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(false)
	defer restoreGate()

	Record(Effect{Path: "/tmp/should-not-be-written.txt", Tool: "test_tool", TS: time.Now()}, "/tmp", false)
	FlushForTest()

	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("gate OFF must create nothing under the base dir; found %d entries: %v", len(entries), entries)
	}
	enqueued, written, _, _, _ := Stats()
	if enqueued != 0 || written != 0 {
		t.Fatalf("gate OFF must not enqueue or write anything; got enqueued=%d written=%d", enqueued, written)
	}
}

// TestRecord_GateOn_ProducesAFile is the positive contrast to the test
// above: proves the harness itself is capable of producing a row (so the
// gate-off test isn't silently vacuous because nothing ever works).
func TestRecord_GateOn_ProducesAFile(t *testing.T) {
	dir := t.TempDir()
	restoreDir := SetBaseDirForTest(dir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	work := t.TempDir() // not a git repo; exercises the fallback bucket
	Record(Effect{Path: filepath.Join(work, "f.txt"), Tool: "test_tool", SessionID: "sess-1", TS: time.Now()}, work, false)
	FlushForTest()

	rows := readAllBenchRows(t, dir)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 row, got %d: %+v", len(rows), rows)
	}
	if rows[0].Tool != "test_tool" || rows[0].SessionID != "sess-1" {
		t.Fatalf("row content wrong: %+v", rows[0])
	}
}

// TestOpenNewFile_NameCarriesPIDAndNonce is the regression test for
// REVIEW.md's PID-collision finding: the on-disk filename must not depend
// on OS-wide PID uniqueness alone, since two containers can both be PID 1
// while sharing one bind-mounted ~/.swarm/projects bucket. procNonce is
// generated once per process and must appear in every file this process
// creates, alongside (not instead of) the PID.
func TestOpenNewFile_NameCarriesPIDAndNonce(t *testing.T) {
	if procNonce == "" {
		t.Fatalf("procNonce must never be empty — see newProcNonce's fallback path")
	}
	dir := t.TempDir()
	restoreDir := SetBaseDirForTest(dir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	work := t.TempDir()
	Record(Effect{Path: filepath.Join(work, "f.txt"), Tool: "test_tool", TS: time.Now()}, work, false)
	FlushForTest()

	bucket, _ := repoCoords(work)
	entries, err := os.ReadDir(filepath.Join(dir, bucket, "bench"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 ledger file, got %d: %v", len(entries), entries)
	}
	name := entries[0].Name()
	wantPIDComponent := fmt.Sprintf("pid%d-%s", pid, procNonce)
	if !strings.Contains(name, wantPIDComponent) {
		t.Fatalf("filename %q does not contain expected pid+nonce component %q — PID alone is not collision-safe across PID namespaces (REVIEW.md non-blocking defect 1)", name, wantPIDComponent)
	}
}

// ---------------------------------------------------------------------------
// (d) two concurrent writers do not corrupt or interleave rows.
// ---------------------------------------------------------------------------

func TestRecord_ConcurrentWritersProduceWholeNonInterleavedLines(t *testing.T) {
	dir := t.TempDir()
	restoreDir := SetBaseDirForTest(dir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	work := t.TempDir()
	const goroutines = 40
	const perGoroutine = 25
	total := goroutines * perGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				Record(Effect{
					Path: filepath.Join(work, "f.txt"),
					Tool: "concurrent_tool",
					// SessionID doubles as a per-row nonce so we can also
					// verify no row was silently duplicated or truncated.
					SessionID: sessionNonce(g, i),
					TS:        time.Now(),
				}, work, false)
			}
		}(g)
	}
	wg.Wait()
	FlushForTest()

	rows := readAllBenchRows(t, dir)
	if len(rows) != total {
		t.Fatalf("expected %d whole rows from %d concurrent goroutines, got %d (a corrupted/interleaved write would show up as a wrong count or a JSON parse failure above)", total, goroutines, len(rows))
	}
	seen := make(map[string]bool, total)
	for _, r := range rows {
		if seen[r.SessionID] {
			t.Fatalf("duplicate nonce %q — a row was written twice", r.SessionID)
		}
		seen[r.SessionID] = true
	}
	if len(seen) != total {
		t.Fatalf("expected %d distinct nonces, got %d", total, len(seen))
	}
}

func sessionNonce(g, i int) string {
	return "g" + itoa(g) + "-i" + itoa(i)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ---------------------------------------------------------------------------
// (e) a panicking/failing writer does not break the caller.
// ---------------------------------------------------------------------------

// TestRecord_NeverPanicsEvenWithHostileBaseDir points the ledger at a path
// that cannot possibly be a directory (a regular file occupies the name),
// so every MkdirAll/OpenFile call inside the writer fails, and asserts
// Record() itself never panics and the caller observes nothing but a
// silently-dropped/failed-write outcome.
func TestRecord_NeverPanicsEvenWithHostileBaseDir(t *testing.T) {
	parent := t.TempDir()
	blocked := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("occupying this path"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	restoreDir := SetBaseDirForTest(blocked)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	work := t.TempDir()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Record panicked with a hostile base dir: %v", r)
			}
		}()
		for i := 0; i < 5; i++ {
			Record(Effect{Path: filepath.Join(work, "f.txt"), Tool: "test_tool", TS: time.Now()}, work, false)
		}
	}()
	FlushForTest() // must also not hang or panic when every open fails
}

// TestRecoverLedgerPanic_IsolatesAPanickingCaller simulates a bug in the
// bench_capture.go call site itself (not the writer goroutine) — the
// non-negotiable is "never blocks, never mutates, never errors into the
// agent path", which covers bugs in the recording code, not just disk
// failures downstream of it.
func TestRecoverLedgerPanic_IsolatesAPanickingCaller(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recoverLedgerPanic did not isolate the panic: %v", r)
		}
	}()
	func() {
		var stats ledgerStats
		defer recoverLedgerPanic(&stats)
		panic("simulated bug in a hypothetical caller")
	}()
}

// ---------------------------------------------------------------------------
// Backpressure: drop-on-full is acceptable but the drop must be counted
// (PLAN.md non-negotiable 3).
// ---------------------------------------------------------------------------

func TestRecord_FullChannelDropsAndCounts(t *testing.T) {
	dir := t.TempDir()
	restoreDir := SetBaseDirForTest(dir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	// Enqueue far more than ledgerChanSize without ever letting the writer
	// goroutine drain (start() is only called on the first record(), so the
	// very first send always succeeds and starts the goroutine — from then
	// on we are racing the writer, which is why we send a large multiple of
	// the channel size: even a fast writer cannot keep up with an unbounded
	// synchronous burst from the test goroutine for long enough to prevent
	// at least some drops).
	work := t.TempDir()
	total := ledgerChanSize * 50
	for i := 0; i < total; i++ {
		Record(Effect{Path: filepath.Join(work, "f.txt"), Tool: "flood_tool", TS: time.Now()}, work, false)
	}
	FlushForTest()

	enqueued, written, dropped, lost, _ := Stats()
	if enqueued+dropped != uint64(total) {
		t.Fatalf("enqueued(%d)+dropped(%d) = %d, want %d — every Record() call must be accounted for exactly once", enqueued, dropped, enqueued+dropped, total)
	}
	if written+lost != enqueued {
		t.Fatalf("written(%d)+lost(%d) = %d, want enqueued(%d) — every enqueued row must end up written or lost, never neither (dropped is a disjoint, earlier outcome at the channel-send stage)", written, lost, written+lost, enqueued)
	}
	if dropped == 0 {
		t.Fatalf("expected at least some drops when flooding a bounded channel with %d rows against a %d-capacity buffer; got 0 drops — either the bound is not enforced or this test is not actually exercising backpressure", total, ledgerChanSize)
	}
	if written == 0 {
		t.Fatalf("expected at least some rows to have been written despite the flood")
	}
	t.Logf("flood test: enqueued=%d written=%d dropped=%d (silence about drops would be the bug)", enqueued, written, dropped)
}

// ---------------------------------------------------------------------------
// Silent-loss paths in writeOne(): every one of the four early returns must
// be counted, not silent. REVIEW.md Blocking Defect 1 — before this fix,
// enqueued == written + dropped held only for channel backpressure; these
// four writer-side failure modes vanished a row without a trace.
// ---------------------------------------------------------------------------

// TestRecord_EmptyBucket_CountsAsLostNotSilent drives writeOne's first
// silent-loss branch: a workspacePath that resolves to bucket=="" (no git
// repo and no path at all — repoCoords("") is a real, tested no-op, see
// TestRepoCoords_EmptyWorkspaceIsEmptyNotError in effect_test.go). Before
// the fix this row vanished from every counter; now it must land in lost.
func TestRecord_EmptyBucket_CountsAsLostNotSilent(t *testing.T) {
	dir := t.TempDir()
	restoreDir := SetBaseDirForTest(dir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	Record(Effect{Path: "/tmp/no-bucket.txt", Tool: "test_tool", TS: time.Now()}, "", false)
	FlushForTest()

	enqueued, written, dropped, lost, _ := Stats()
	if enqueued != 1 {
		t.Fatalf("expected exactly 1 enqueued row, got %d", enqueued)
	}
	if written != 0 {
		t.Fatalf("a bucket-less row must never be written, got written=%d", written)
	}
	if dropped != 0 {
		t.Fatalf("a bucket-less row is a writer-side loss, not channel backpressure; expected dropped=0, got %d", dropped)
	}
	if lost != 1 {
		t.Fatalf("expected the empty-bucket row to be counted in lost exactly once, got lost=%d (this is the exact silent-loss bug REVIEW.md flagged)", lost)
	}
}

// TestRecord_UnopenableBucketDir_CountsAsLostNotSilent drives writeOne's
// "file handle unobtainable" branch by making os.MkdirAll fail: a regular
// file is pre-created at the exact path the writer needs as a directory
// (<baseDir>/<bucket>/bench), so MkdirAll returns a "not a directory" error
// and openNewFile returns nil. This is the realistic shape of the "disk
// full / bad perms" failure mode REVIEW.md traced, made deterministic
// without needing to actually fill a disk.
func TestRecord_UnopenableBucketDir_CountsAsLostNotSilent(t *testing.T) {
	dir := t.TempDir()
	restoreDir := SetBaseDirForTest(dir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	work := t.TempDir()
	bucket, _ := repoCoords(work)
	if bucket == "" {
		t.Fatalf("setup: expected a non-empty fallback bucket for a real temp dir workspace")
	}
	// Block the "bench" subdirectory the writer will try to MkdirAll by
	// pre-creating a plain file at that exact path.
	if err := os.MkdirAll(filepath.Join(dir, bucket), 0o755); err != nil {
		t.Fatalf("setup MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, bucket, "bench"), []byte("blocking file, not a directory"), 0o644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	Record(Effect{Path: filepath.Join(work, "f.txt"), Tool: "test_tool", TS: time.Now()}, work, false)
	FlushForTest()

	enqueued, written, dropped, lost, _ := Stats()
	if enqueued != 1 {
		t.Fatalf("expected exactly 1 enqueued row, got %d", enqueued)
	}
	if written != 0 {
		t.Fatalf("a row whose bucket dir cannot be created must never be written, got written=%d", written)
	}
	if dropped != 0 {
		t.Fatalf("an unopenable-file row is a writer-side loss, not channel backpressure; expected dropped=0, got %d", dropped)
	}
	if lost != 1 {
		t.Fatalf("expected the unopenable-file row to be counted in lost exactly once, got lost=%d (this is the exact silent-loss bug REVIEW.md flagged)", lost)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func readAllBenchRows(t *testing.T, baseDir string) []Effect {
	t.Helper()
	var rows []Effect
	_ = filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		f, ferr := os.Open(path)
		if ferr != nil {
			t.Fatalf("Open(%s): %v", path, ferr)
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var e Effect
			if err := json.Unmarshal(line, &e); err != nil {
				t.Fatalf("%s: corrupted/interleaved JSON line: %v\nline: %s", path, err, line)
			}
			rows = append(rows, e)
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("scan %s: %v", path, err)
		}
		return nil
	})
	return rows
}

// ---------------------------------------------------------------------------
// Overhead measurement: what does the capture seam cost on the caller's own
// goroutine (the only part that is actually "on the hot path" — everything
// past the channel send happens on the background writer goroutine and is
// explicitly out of scope for this number, per PLAN.md C1(f)).
// ---------------------------------------------------------------------------

// BenchmarkRecord_GateOn measures the real per-call cost once measurement is
// opted in: gate check + job construction + non-blocking channel send. The
// background writer is intentionally NOT drained between iterations, so this
// also naturally exercises the drop path once the channel fills — a realistic
// steady-state cost, not just the best case of an always-empty channel.
func BenchmarkRecord_GateOn(b *testing.B) {
	dir := b.TempDir()
	restoreDir := SetBaseDirForTest(dir)
	defer restoreDir()
	restoreGate := SetObservationalHooksEnabled(true)
	defer restoreGate()

	work := b.TempDir()
	eff := Effect{Path: filepath.Join(work, "f.txt"), Tool: "bench_tool", TS: time.Now()}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Record(eff, work, false)
	}
}

// BenchmarkRecord_GateOff measures the compiled-in-default cost: every call
// must be (per PLAN.md §9.3) effectively free, since nothing is registered
// unless explicitly enabled.
func BenchmarkRecord_GateOff(b *testing.B) {
	restoreGate := SetObservationalHooksEnabled(false)
	defer restoreGate()

	work := b.TempDir()
	eff := Effect{Path: filepath.Join(work, "f.txt"), Tool: "bench_tool", TS: time.Now()}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Record(eff, work, false)
	}
}
