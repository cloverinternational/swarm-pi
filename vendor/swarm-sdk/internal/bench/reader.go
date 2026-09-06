// reader.go — a read-only, streaming iterator over the ledger this package
// writes (ledger.go). This is the ONLY thing cmd/swarm-bench needs from
// internal/bench beyond the Effect type itself: where the files live, and
// how to walk them without loading the whole ledger into memory or failing
// on a writer's in-flight partial line.
//
// # Why this lives in internal/bench, not cmd/swarm-bench
//
// Bucket/path resolution (DefaultBaseDir, BucketFor, BucketDir) must use the
// exact same hashing and fallback rules the writer uses (repoCoords in
// effect.go, baseDirOrDefault in ledger.go) or `swarm-bench` run from a
// worktree would compute a different bucket than the one the agent process
// wrote to and silently show "nothing recorded" against a non-empty ledger.
// Keeping resolution here means there is exactly one implementation of
// "where does this row live" for both the writer and every reader.
//
// # What this file deliberately does NOT do
//
// No exec.Command, no git anywhere in this file or this package — the git
// subprocess (git log --all --find-object) is a read-only *interpretation*
// of ledger content, not ledger mechanics, and belongs in the CLI that
// needs it (cmd/swarm-bench/survive.go), never in this dependency-light
// leaf package (PLAN.md Phase B file list is explicit that only
// cmd/swarm-bench/main.go owns that call).
//
// No verdict-shaped aggregation. This file returns raw Effect rows and a
// truthful count of what it skipped; it does not compute a ratio, a score,
// or anything resembling PLAN.md §6's refused headline. That interpretation
// step is the CLI's job, done in the open where it can be read and audited
// (see survive.go's doc comment on the survival ratio it prints).
package bench

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultBaseDir returns the root of the bench ledger tree
// (normally ~/.swarm/projects), using the exact same home-directory
// resolution and fallback chain the writer uses. Exported so a separate
// read-only binary (cmd/swarm-bench) never needs to reimplement — and
// risk drifting from — that fallback chain.
func DefaultBaseDir() string {
	return (&Ledger{}).baseDirOrDefault()
}

// BucketFor resolves the ledger bucket for workspacePath the same way the
// writer does (repoCoords in effect.go): hashed from the git COMMON
// directory so every linked worktree of one repository reads the same
// bucket a writer in any of those worktrees wrote to. Returns "" only when
// workspacePath itself is "" — every other input, including a directory
// that is not a git repository at all, still yields a stable bucket.
func BucketFor(workspacePath string) string {
	bucket, _ := repoCoords(workspacePath)
	return bucket
}

// BucketDir joins a base dir and a bucket into the directory the writer
// actually creates *.jsonl files under (baseDir/bucket/bench — see
// ledger.go:openNewFile).
func BucketDir(baseDir, bucket string) string {
	return filepath.Join(baseDir, bucket, "bench")
}

// EffectFile is one ledger file discovered on disk.
type EffectFile struct {
	// Path is the absolute path to the file.
	Path string
	// Name is the base filename (effects_<hour>_pid<pid>[_<roll>].jsonl).
	// Since the PID-collision fix, the pid component is followed by a
	// "-<nonce>" per-process random suffix: effects_<hour>_pid<pid>-<nonce>
	// [_<roll>].jsonl. Callers must not parse this filename structurally —
	// only the ".jsonl" suffix and the fact that files sort roughly
	// chronologically by their leading hour key are load-bearing here.
	Name string
}

// ListEffectFiles returns every *.jsonl file directly under bucketDir,
// sorted by filename (which sorts by hour first, since every filename
// begins with the "2006-01-02-15" hour key — a reasonable coarse
// chronological order across files; ReadEffects/callers that need exact
// ordering must sort by each row's own TS, since multiple processes writing
// concurrently within the same hour are not otherwise orderable).
//
// A missing bucketDir is the normal "nothing recorded yet" case, not an
// error: it returns (nil, nil), never (nil, err), so callers can treat
// "no files" and "empty ledger" identically without special-casing
// os.IsNotExist themselves.
func ListEffectFiles(bucketDir string) ([]EffectFile, error) {
	entries, err := os.ReadDir(bucketDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []EffectFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		files = append(files, EffectFile{Path: filepath.Join(bucketDir, e.Name()), Name: e.Name()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// maxEffectLineBytes bounds a single JSONL line ReadEffects will accept.
// Effect rows are small typed structs (a handful of short strings and a
// timestamp); a line anywhere near this bound is already not a well-formed
// row, and this cap is what stops a corrupt or hostile file from making the
// line scanner allocate without bound.
const maxEffectLineBytes = 1 << 20 // 1 MiB

// ReadStats summarises what ReadEffects saw, including what it could not
// use — so a caller can report the truth about a partial read instead of
// silently under-counting (the same "count it so silence is visible"
// discipline ledger.go applies to dropped rows).
type ReadStats struct {
	// FilesRead is how many ledger files were opened successfully.
	FilesRead int
	// FilesMissing is how many listed files had vanished by the time this
	// read tried to open them (a concurrent roll or, in principle, a
	// deleted file) — not an error, just worth surfacing.
	FilesMissing int
	// RowsRead is how many well-formed Effect rows were decoded and passed
	// to the caller's fn.
	RowsRead int
	// LinesSkipped is how many lines failed to parse as an Effect — the
	// expected shape of a writer's in-flight, not-yet-newline-terminated
	// final line, or any other corruption. Skipped, never fatal: PLAN.md's
	// "missing evidence → Undetermined, never Fail" extends structurally to
	// "a torn write must not fail the whole read".
	LinesSkipped int
}

// ReadEffects streams every effect row across files in order, calling fn
// once per decoded row. It never loads more than one line at a time into
// memory (via bufio.Scanner) — the caller decides what, if anything, to
// retain, which is what keeps this safe against a ledger that has grown to
// many files. Returning a non-nil error from fn stops the walk early and
// that error is returned to ReadEffects' caller unchanged; any other read
// failure (a line too long, a line that doesn't parse) is recorded in the
// returned stats and the walk continues.
func ReadEffects(files []EffectFile, fn func(Effect) error) (ReadStats, error) {
	var stats ReadStats
	for _, ef := range files {
		stop, err := readOneEffectFile(ef.Path, fn, &stats)
		if err != nil {
			return stats, err
		}
		if stop {
			return stats, nil
		}
	}
	return stats, nil
}

func readOneEffectFile(path string, fn func(Effect) error, stats *ReadStats) (stop bool, err error) {
	f, openErr := os.Open(path)
	if openErr != nil {
		if os.IsNotExist(openErr) {
			stats.FilesMissing++
			return false, nil
		}
		return false, openErr
	}
	defer f.Close()
	stats.FilesRead++

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxEffectLineBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var eff Effect
		if jsonErr := json.Unmarshal([]byte(line), &eff); jsonErr != nil {
			// A partial final line from a writer that is mid-append (the
			// writer appends bytes then a trailing '\n'; a reader racing
			// that append can see the line before the '\n' lands) or any
			// other corruption. Skip it, keep reading — see (c) in the task
			// spec: a truncated final line must not fail the whole read.
			stats.LinesSkipped++
			continue
		}
		stats.RowsRead++
		if err := fn(eff); err != nil {
			return true, err
		}
	}
	if scanErr := sc.Err(); scanErr != nil {
		// bufio.ErrTooLong or an I/O error reading this particular file.
		// Treat as "stop reading this file", not "fail the whole ledger
		// read" — the same tolerance principle applied to a single bad
		// line, just at file granularity.
		stats.LinesSkipped++
	}
	return false, nil
}
