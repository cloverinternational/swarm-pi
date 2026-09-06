// ledger.go — the append-only JSONL sink for Effect rows.
//
// # Mechanics copied (deliberately) from proven code in this repo
//
// File-open discipline (O_CREATE|O_APPEND|O_WRONLY, one mutex-guarded
// *os.File per bucket) mirrors internal/observability/sink_jsonl.go.
// Hour-bucketed filenames, a buffered channel with a non-blocking send and a
// synchronous fallback, and periodic fsync mirror
// internal/hooks/builtin/bronze_event_capture.go. This file copies the file
// mechanics only, never the schema and never the Tracer/TraceEvent path:
// PLAN.md P5 recorded that internal/observability's redactor truncates every
// string over 512 chars unconditionally, which would silently mutilate a
// blob hash field on the rare 41+ char edge (SHA-256 blob ids) and is simply
// the wrong contract for a typed row that is deliberately small already.
//
// # The one property those two files don't need and this one does
//
// One JSONL file per bucket is a SINGLE-PROCESS assumption in
// sink_jsonl.go, correct there because a Tracer lives inside one process.
// bench does not get that luxury: many independent swarm processes (a TUI,
// several background `swarm -p` runs, sub-agents) can be measuring the SAME
// project concurrently, and PLAN.md's non-negotiables forbid a daemon or
// long-lived server to arbitrate between them. The fix is filename, not
// locking: "one file per hour AND per PID" (PLAN's own phrasing) means two
// processes racing on the same project+hour simply write to two different
// files — zero cross-process coordination required, and no lock file that
// could itself wedge a tool call if left stale by a killed process.
//
// PID alone is not actually unique, though: two containers (or any two PID
// namespaces) each running as PID 1 and both mounting the same
// ~/.swarm/projects bucket for the same repo — a realistic CI/sandbox
// topology — resolve to the identical PID. The filename therefore also
// carries a random per-process nonce (see procNonce below): uniqueness no
// longer depends on an assumption (OS-wide PID uniqueness) that containers
// routinely violate, while the PID is kept in the name because it is still
// useful for a human correlating a ledger file to a live/dead process on
// the box that actually wrote it.
package bench

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ledgerChanSize bounds how many Effect rows can be queued between the
// caller (a tool-call goroutine) and the background writer before rows start
// dropping. Sized generously relative to realistic tool-call-effect fan-out
// (a single apply_patch transaction rarely touches more than a handful of
// files) so drops should be a rare, measured event rather than routine.
const ledgerChanSize = 2048

// maxLedgerFileBytes is a soft cap per (bucket, hour, pid) file. When
// exceeded, the writer rolls to a new numbered file rather than growing one
// file without bound — this box has already accumulated a 3.8 GB log and
// 7.2 GB of conversation history, and an opt-in measurement feature must not
// add to that unbounded.
const maxLedgerFileBytes = 64 << 20 // 64 MiB

// maxHashableFileBytes bounds how large a file the background writer will
// read to compute PostBlob when a producing tool did not already supply one.
// Reading and hashing an arbitrarily large file on the background writer
// would still be bounded off the agent's hot path, but an unbounded read is
// still a liability on a box already short on headroom; files larger than
// this get an empty PostBlob (an honest "not established", never a fabricated
// one) rather than a multi-second hash of a multi-gigabyte file.
const maxHashableFileBytes = 8 << 20 // 8 MiB

// job is what Record() enqueues. It carries the workspace path so the
// background writer — not the caller — pays the (memoised) cost of
// resolving repo coordinates, keeping the caller's job construction to a
// handful of field copies and nothing else.
type job struct {
	effect        Effect
	workspacePath string
	needsPostHash bool
	// barrier, when non-nil, is closed by run() the moment this job is
	// dequeued — never processed as a real row. Because the channel is FIFO
	// and run() drains it strictly one job at a time, closing barrier here
	// is a correct "everything enqueued before this call has been written"
	// signal for Flush(), with no fixed sleep and no busy-poll.
	barrier chan struct{}
}

// ledgerStats is the observable counters a caller (or a test) can inspect to
// see backpressure, per the non-negotiable "drop-on-full is acceptable but
// then COUNT the drops so silence is visible".
//
// The invariant this struct exists to prove is
// enqueued == written + dropped + lost. "dropped" is channel backpressure
// (Record() couldn't even hand the row to the writer). "lost" is every
// write-path failure the writer goroutine hits AFTER a row was already
// enqueued — an empty bucket, a JSON marshal failure, an unopenable file, or
// a failed write() syscall. Both are silent-by-necessity (this is a
// best-effort sink that must never block or panic the caller), but neither
// is allowed to be silent-and-uncounted: a row that vanishes between
// enqueued and written must always show up in exactly one of dropped/lost,
// never in neither.
type ledgerStats struct {
	enqueued uint64
	written  uint64
	dropped  uint64
	// lost counts rows that were successfully enqueued but then failed on
	// the writer goroutine's side — see the invariant note above. Distinct
	// from dropped (which is purely channel backpressure, no writer
	// involvement) so a caller/operator can tell "the channel was full" from
	// "disk pressure or a bad path is failing writes" without conflating
	// two very different failure modes under one counter.
	lost uint64
	// panics counts recovered panics anywhere in the write path — from a
	// caller's Record() call through the background writer. Each one is a
	// row that did NOT make it to disk, isolated so it never reaches the
	// agent's turn.
	panics uint64
}

// Ledger is the append-only JSONL sink for one process. There is exactly one
// package-level instance (defaultLedger); it is a struct rather than bare
// package state only so tests can construct isolated instances instead of
// mutating global state and racing each other under `-race -parallel`.
type Ledger struct {
	baseDir string // overridden in tests; "" means the real ~/.swarm/projects

	startOnce sync.Once
	ch        chan job
	done      chan struct{}
	wg        sync.WaitGroup

	mu    sync.Mutex
	files map[string]*openLedgerFile // key: bucket + "/" + hourKey + "/" + rollIndex

	stats ledgerStats
}

type openLedgerFile struct {
	f          *os.File
	w          *bufio.Writer
	size       int64
	rollIndex  int
	writeCount int
}

var defaultLedger = &Ledger{}

// pid is cached once; os.Getpid() is cheap but there is no reason to call it
// per row when the filename component cannot change during the process
// lifetime.
var pid = os.Getpid()

// procNonce is a short random identifier generated once per process and
// folded into every ledger filename this process creates. PID alone
// collides across PID namespaces (two containers can both be PID 1 while
// sharing one bind-mounted ~/.swarm/projects bucket); a random nonce makes
// filename collisions astronomically unlikely regardless of how PIDs are
// reused or shared, with no cross-process coordination required. Falls back
// to a timestamp-derived value in the (essentially theoretical) case
// crypto/rand itself fails, so a nonce is always produced rather than
// leaving the filename component empty.
var procNonce = newProcNonce()

func newProcNonce() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	// crypto/rand failing is not something this package should ever be
	// unable to route around: fall back to a value derived from the
	// process start time and PID, which is not cryptographically random
	// but is still practically unique for this purpose.
	return fmt.Sprintf("%08x", uint32(time.Now().UnixNano())^uint32(pid))
}

// Record enqueues effect for asynchronous, best-effort persistence.
//
// This is the ONLY entry point production code should call. It is:
//   - gate-checked: a no-op unless ObservationalHooksEnabled(), so a process
//     that never opts in never even starts the background writer goroutine
//     (PLAN.md §9.3 — "nothing on by default" means no goroutine, no file
//     handle, not just a bool that says off).
//   - non-blocking: a full channel increments the drop counter and returns;
//     it never waits for the writer.
//   - panic-safe: any panic anywhere in this call (a caller bug, an
//     allocation failure building the job) is recovered and counted, never
//     propagated to the tool-call path that invoked it.
func Record(effect Effect, workspacePath string, needsPostHash bool) {
	defer recoverLedgerPanic(&defaultLedger.stats)
	if !ObservationalHooksEnabled() {
		return
	}
	defaultLedger.record(job{effect: effect, workspacePath: workspacePath, needsPostHash: needsPostHash})
}

func recoverLedgerPanic(stats *ledgerStats) {
	if r := recover(); r != nil {
		atomic.AddUint64(&stats.panics, 1)
	}
}

func (l *Ledger) record(j job) {
	l.startOnce.Do(l.start)
	select {
	case l.ch <- j:
		atomic.AddUint64(&l.stats.enqueued, 1)
	default:
		atomic.AddUint64(&l.stats.dropped, 1)
	}
}

func (l *Ledger) start() {
	l.ch = make(chan job, ledgerChanSize)
	l.done = make(chan struct{})
	l.files = make(map[string]*openLedgerFile)
	l.wg.Add(1)
	go l.run()
}

// run is the single goroutine that ever touches l.files or writes to a file.
// Serialising every write through one goroutine — rather than a mutex around
// concurrent writers — is what makes concurrent Record() calls from many
// tool-call goroutines produce whole, non-interleaved JSON lines: bufio's
// per-writer buffer is never shared, and os.File append semantics only need
// to hold within one writer per file.
func (l *Ledger) run() {
	defer l.wg.Done()
	for {
		select {
		case j := <-l.ch:
			if j.barrier != nil {
				close(j.barrier)
				continue
			}
			l.writeOne(j)
		case <-l.done:
			// Drain whatever is already queued before exiting so a Close()
			// (used by tests) does not silently lose rows that were already
			// accepted.
			for {
				select {
				case j := <-l.ch:
					if j.barrier != nil {
						close(j.barrier)
						continue
					}
					l.writeOne(j)
				default:
					l.closeAll()
					return
				}
			}
		}
	}
}

// writeOne resolves repo coordinates, optionally computes PostBlob from the
// file's current bytes (bounded, only when the producing tool did not
// already supply a hash computed from bytes it held in memory), and appends
// one JSON line. Every failure mode here is swallowed: a full disk, a
// missing directory, or a file that vanished between mutation and this
// (already-async) read must never be visible outside this function.
func (l *Ledger) writeOne(j job) {
	defer recoverLedgerPanic(&l.stats)

	eff := j.effect
	bucket, headSHA := repoCoords(j.workspacePath)
	if eff.HeadSHA == "" {
		eff.HeadSHA = headSHA
	}
	if j.needsPostHash && eff.PostBlob == "" {
		eff.PostBlob = l.bestEffortPostHash(eff.Path)
	}
	if bucket == "" {
		// No resolvable project identity at all (not a git repo and an
		// empty workspace path) — nothing safe to bucket this under.
		atomic.AddUint64(&l.stats.lost, 1)
		return
	}

	line, err := json.Marshal(eff)
	if err != nil {
		atomic.AddUint64(&l.stats.lost, 1)
		return
	}
	line = append(line, '\n')

	of := l.openFileFor(bucket, eff.TS)
	if of == nil {
		atomic.AddUint64(&l.stats.lost, 1)
		return
	}
	n, err := of.w.Write(line)
	if err != nil {
		atomic.AddUint64(&l.stats.lost, 1)
		return
	}
	of.size += int64(n)
	of.writeCount++
	atomic.AddUint64(&l.stats.written, 1)

	// Periodic flush (mirrors bronze's checkpoint interval) so a killed
	// process loses at most a handful of buffered rows, not the whole
	// session's worth.
	if of.writeCount%15 == 0 {
		_ = of.w.Flush()
	}
}

// bestEffortPostHash reads path's current content and hashes it, bounded by
// maxHashableFileBytes. This runs ONLY on the background writer goroutine,
// never on the agent's tool-call path — the "no new syscalls on the hot
// path" non-negotiable binds the synchronous caller, not an async worker
// that the caller never waits on. A tool that already computed a hash from
// bytes it held in memory (see forge/apply_patch.go) sets needsPostHash=false
// and this is never called.
func (l *Ledger) bestEffortPostHash(path string) string {
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxHashableFileBytes {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return blobHash(data)
}

func (l *Ledger) baseDirOrDefault() string {
	if l.baseDir != "" {
		return l.baseDir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if u, uerr := user.Current(); uerr == nil && u.HomeDir != "" {
			home = u.HomeDir
		}
	}
	if home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".swarm", "projects")
}

// openFileFor returns the open file for (bucket, hour), rolling to a new
// numbered file when the current one has grown past maxLedgerFileBytes.
// Must be called only from run() — no lock needed since run() is the sole
// writer goroutine by construction, but l.mu still guards l.files because
// Stats()/tests may read it concurrently.
func (l *Ledger) openFileFor(bucket string, ts time.Time) *openLedgerFile {
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	hourKey := ts.UTC().Format("2006-01-02-15")
	key := bucket + "/" + hourKey

	l.mu.Lock()
	of, ok := l.files[key]
	l.mu.Unlock()
	if ok && of.size < maxLedgerFileBytes {
		return of
	}
	rollIndex := 0
	if ok {
		rollIndex = of.rollIndex + 1
	}
	next := l.openNewFile(bucket, hourKey, rollIndex)
	if next == nil {
		return of // fall back to the old (oversized) handle rather than dropping the row
	}
	l.mu.Lock()
	if ok {
		_ = of.w.Flush()
		_ = of.f.Close()
	}
	l.files[key] = next
	l.mu.Unlock()
	return next
}

func (l *Ledger) openNewFile(bucket, hourKey string, rollIndex int) *openLedgerFile {
	dir := filepath.Join(l.baseDirOrDefault(), bucket, "bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	name := fmt.Sprintf("effects_%s_pid%d-%s.jsonl", hourKey, pid, procNonce)
	if rollIndex > 0 {
		name = fmt.Sprintf("effects_%s_pid%d-%s_%d.jsonl", hourKey, pid, procNonce, rollIndex+1)
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	size := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	return &openLedgerFile{f: f, w: bufio.NewWriter(f), size: size, rollIndex: rollIndex}
}

func (l *Ledger) closeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, of := range l.files {
		_ = of.w.Flush()
		_ = of.f.Close()
		delete(l.files, k)
	}
}

// Flush blocks until every row enqueued before this call has been written
// (or dropped) and fsync'd. Test-only: production code never waits on the
// ledger, by design.
func (l *Ledger) Flush() {
	if l.ch == nil {
		return // never started; nothing to flush
	}
	// A barrier job forces run() to have drained everything enqueued before
	// it: the channel is FIFO, run() processes exactly one job at a time,
	// and the barrier's own close(j.barrier) happens only after every prior
	// job's writeOne has returned. This blocks on a real completion signal
	// from the writer goroutine, never a fixed sleep.
	barrier := make(chan struct{})
	l.ch <- job{barrier: barrier}
	<-barrier
	l.mu.Lock()
	for _, of := range l.files {
		_ = of.w.Flush()
		_ = of.f.Sync()
	}
	l.mu.Unlock()
}

// Stats returns a snapshot of the ledger's counters. enqueued == written +
// dropped + lost is the invariant this exists to let a caller verify — see
// the ledgerStats doc comment for what distinguishes dropped from lost.
func (l *Ledger) Stats() (enqueued, written, dropped, lost, panicsRecovered uint64) {
	return atomic.LoadUint64(&l.stats.enqueued),
		atomic.LoadUint64(&l.stats.written),
		atomic.LoadUint64(&l.stats.dropped),
		atomic.LoadUint64(&l.stats.lost),
		atomic.LoadUint64(&l.stats.panics)
}

// Stats reports the default (package-level) ledger's counters — the
// observable half of "drop-on-full is acceptable but then COUNT the drops so
// silence is visible" (PLAN.md non-negotiable 3). enqueued == written +
// dropped + lost unconditionally, including every write-path failure mode
// (not just channel backpressure) — see ledgerStats.
func Stats() (enqueued, written, dropped, lost, panicsRecovered uint64) {
	return defaultLedger.Stats()
}

// SetBaseDirForTest points the default ledger's files at dir instead of the
// real ~/.swarm/projects, and resets its in-memory state so a fresh test
// starts from a clean slate. It exists so tests never touch the real
// measurement store on this machine — the whole reason this feature is
// opt-in is that ~/.swarm/projects has already accumulated multiple
// gigabytes of history from other subsystems, and a test run must not add to
// it or race a developer's real ledger.
//
// Returns a restore function; callers should defer it.
func SetBaseDirForTest(dir string) (restore func()) {
	prev := defaultLedger
	defaultLedger = &Ledger{baseDir: dir}
	return func() {
		defaultLedger.stop()
		defaultLedger = prev
	}
}

// FlushForTest blocks until the default ledger has written everything
// enqueued so far. Test-only.
func FlushForTest() {
	defaultLedger.Flush()
}

func (l *Ledger) stop() {
	if l.done == nil {
		return
	}
	close(l.done)
	l.wg.Wait()
}
