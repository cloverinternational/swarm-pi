package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
)

// buildLedgerFixture writes real ledger rows via the real writer
// (bench.Record/FlushForTest — the same code path production agents use),
// pointed at an isolated baseDir so the test never touches
// ~/.swarm/projects. workspacePath is what the writer resolves the bucket
// from; tests pass the same value to commonFlags.dir so the CLI resolves
// the identical bucket.
func buildLedgerFixture(t *testing.T, baseDir, workspacePath string, effects []bench.Effect) {
	t.Helper()
	restoreDir := bench.SetBaseDirForTest(baseDir)
	defer restoreDir()
	restoreGate := bench.SetObservationalHooksEnabled(true)
	defer restoreGate()
	for _, eff := range effects {
		bench.Record(eff, workspacePath, false)
	}
	bench.FlushForTest()
}

// ---------------------------------------------------------------------------
// (a) survive correctly reports a blob that IS in history and one that is
// NOT, against a REAL throwaway git repo — no mocked git anywhere.
// ---------------------------------------------------------------------------

func TestSurvive_RealGitRepo_DistinguishesInHistoryFromNot(t *testing.T) {
	repoDir, committedBlob, headSHA := newGitFixture(t)
	uncommittedBlob := gitHashObject(t, repoDir, []byte("this content was produced by a tool and then discarded\n"))

	baseDir := t.TempDir()
	now := time.Now().UTC()
	buildLedgerFixture(t, baseDir, repoDir, []bench.Effect{
		{Path: filepath.Join(repoDir, "committed.txt"), Op: "update", Tool: "apply_patch", PostBlob: committedBlob, TS: now},
		{Path: filepath.Join(repoDir, "scratch.txt"), Op: "create", Tool: "apply_patch", PostBlob: uncommittedBlob, TS: now},
	})

	var c commonFlags
	c.dir = repoDir
	c.baseDir = baseDir
	c.jsonOut = true

	out := captureStdout(t, func() {
		runSurvive([]string{"-dir", repoDir, "-base-dir", baseDir, "-repo", repoDir, "-json"})
	})

	var report surviveReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("survive -json output did not parse: %v\noutput:\n%s", err, out)
	}

	if len(report.NotEstablished) != 0 {
		t.Fatalf("expected zero not-established rows (both effects had a PostBlob), got %d", len(report.NotEstablished))
	}
	if len(report.Established) != 2 {
		t.Fatalf("expected 2 established blob groups, got %d: %+v", len(report.Established), report.Established)
	}

	byBlob := map[string]*blobGroup{}
	for _, g := range report.Established {
		byBlob[g.Blob] = g
	}

	committed, ok := byBlob[committedBlob]
	if !ok {
		t.Fatalf("committed blob %s missing from established groups", committedBlob)
	}
	if !committed.InHistory {
		t.Fatalf("committed blob %s must be reported InHistory=true", committedBlob)
	}
	found := false
	for _, c := range committed.Commits {
		if c == headSHA {
			found = true
		}
	}
	if !found {
		t.Fatalf("committed blob's commit list %v does not contain the real HEAD commit %s", committed.Commits, headSHA)
	}

	uncommitted, ok := byBlob[uncommittedBlob]
	if !ok {
		t.Fatalf("uncommitted blob %s missing from established groups", uncommittedBlob)
	}
	if uncommitted.InHistory {
		t.Fatalf("uncommitted blob %s must be reported InHistory=false, got Commits=%v", uncommittedBlob, uncommitted.Commits)
	}
	if len(uncommitted.Commits) != 0 {
		t.Fatalf("uncommitted blob must have zero commits, got %v", uncommitted.Commits)
	}

	if report.InHistoryCount != 1 {
		t.Fatalf("InHistoryCount = %d, want 1", report.InHistoryCount)
	}
	if report.NotInHistoryCount != 1 {
		t.Fatalf("NotInHistoryCount = %d, want 1", report.NotInHistoryCount)
	}

	// Human-readable mode must clearly label the ratio as survival, never a
	// verdict/score/pass-rate word.
	humanOut := captureStdout(t, func() {
		runSurvive([]string{"-dir", repoDir, "-base-dir", baseDir, "-repo", repoDir})
	})
	if !strings.Contains(humanOut, "SURVIVAL RATIO") {
		t.Fatalf("human output must contain a clearly labelled SURVIVAL RATIO section:\n%s", humanOut)
	}
	// The report deliberately SAYS "NOT a pass rate" as a disclaimer next to
	// the survival ratio — that substring appearing inside a negation is
	// the safety label working as intended. What must never appear is the
	// verdict metric PRINTED AS a headline value, e.g. "PASS RATE: 50%" or
	// "SCORE:"/"GRADE:" standing in front of a number.
	for _, forbidden := range []string{"pass rate:", "grade:", "score:", "PASS:", "FAIL:", "VERDICT:"} {
		if strings.Contains(humanOut, forbidden) {
			t.Fatalf("human output must never print a verdict-shaped metric label %q:\n%s", forbidden, humanOut)
		}
	}
	if !strings.Contains(humanOut, "NOT a pass rate") {
		t.Fatalf("human output must explicitly disclaim the survival ratio is not a pass rate:\n%s", humanOut)
	}
}

// ---------------------------------------------------------------------------
// (d) an effect with an empty PostBlob is "not established", never folded
// into the in-history/not-in-history survival counts.
// ---------------------------------------------------------------------------

func TestSurvive_EmptyPostBlob_IsNotEstablished_ExcludedFromRatio(t *testing.T) {
	repoDir, committedBlob, _ := newGitFixture(t)

	baseDir := t.TempDir()
	now := time.Now().UTC()
	buildLedgerFixture(t, baseDir, repoDir, []bench.Effect{
		{Path: filepath.Join(repoDir, "committed.txt"), Op: "update", Tool: "apply_patch", PostBlob: committedBlob, TS: now},
		// A delete: no surviving content, so PostBlob is legitimately empty.
		{Path: filepath.Join(repoDir, "gone.txt"), Op: "delete", Tool: "apply_patch", PostBlob: "", TS: now},
	})

	out := captureStdout(t, func() {
		runSurvive([]string{"-dir", repoDir, "-base-dir", baseDir, "-repo", repoDir, "-json"})
	})
	var report surviveReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("survive -json output did not parse: %v\noutput:\n%s", err, out)
	}

	if len(report.NotEstablished) != 1 {
		t.Fatalf("expected exactly 1 not-established row (the delete), got %d", len(report.NotEstablished))
	}
	if report.NotEstablished[0].Op != "delete" {
		t.Fatalf("not-established row has wrong Op: %+v", report.NotEstablished[0])
	}
	if len(report.Established) != 1 {
		t.Fatalf("expected exactly 1 established blob group, got %d", len(report.Established))
	}
	// The denominator for the survival ratio is in_history + not_in_history,
	// which must equal 1 (the established blob only) — never 2.
	denom := report.InHistoryCount + report.NotInHistoryCount
	if denom != 1 {
		t.Fatalf("survival-ratio denominator = %d, want 1 (not-established rows must never be folded in)", denom)
	}
}

// ---------------------------------------------------------------------------
// (b) empty/missing ledger prints the clean message, never an error.
// ---------------------------------------------------------------------------

func TestSurvive_EmptyLedger_CleanMessage(t *testing.T) {
	repoDir, _, _ := newGitFixture(t)
	baseDir := t.TempDir() // never written to — no ledger exists under it

	out := captureStdout(t, func() {
		runSurvive([]string{"-dir", repoDir, "-base-dir", baseDir, "-repo", repoDir})
	})
	if !strings.Contains(out, "nothing recorded") {
		t.Fatalf("expected the clean 'nothing recorded' message, got:\n%s", out)
	}
}

func TestLS_EmptyLedger_CleanMessage(t *testing.T) {
	baseDir := t.TempDir()
	dir := t.TempDir()
	out := captureStdout(t, func() {
		runLS([]string{"-dir", dir, "-base-dir", baseDir})
	})
	if !strings.Contains(out, "nothing recorded") {
		t.Fatalf("expected the clean 'nothing recorded' message, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// gitFindObject unit coverage (no CLI plumbing) — belt and suspenders around
// the one exec.Command call in this whole tool.
// ---------------------------------------------------------------------------

func TestGitFindObject_RealRepo(t *testing.T) {
	repoDir, committedBlob, headSHA := newGitFixture(t)
	uncommitted := gitHashObject(t, repoDir, []byte("never committed\n"))

	commits, err := gitFindObject(repoDir, committedBlob, 5*time.Second)
	if err != nil {
		t.Fatalf("gitFindObject(committed): %v", err)
	}
	if len(commits) != 1 || commits[0] != headSHA {
		t.Fatalf("gitFindObject(committed) = %v, want [%s]", commits, headSHA)
	}

	commits, err = gitFindObject(repoDir, uncommitted, 5*time.Second)
	if err != nil {
		t.Fatalf("gitFindObject(uncommitted): %v", err)
	}
	if len(commits) != 0 {
		t.Fatalf("gitFindObject(uncommitted) = %v, want empty", commits)
	}
}

func TestGitFindObject_RefusesNonHashInput(t *testing.T) {
	repoDir, _, _ := newGitFixture(t)
	if _, err := gitFindObject(repoDir, "rm -rf /", 5*time.Second); err == nil {
		t.Fatalf("gitFindObject must refuse a value that is not a git object hash")
	}
}

func TestLooksLikeGitHash(t *testing.T) {
	cases := map[string]bool{
		"": false,
		"e69de29bb2d1d6434b8b29ae775ad8c2e48c5391": true, // sha1, 40 hex
		strings.Repeat("a", 64):                    true, // sha256, 64 hex
		"not-a-hash":                               false,
		"e69de29bb2d1d6434b8b29ae775ad8c2e48c539Z": false, // bad char
		"rm -rf /": false,
	}
	for in, want := range cases {
		if got := looksLikeGitHash(in); got != want {
			t.Errorf("looksLikeGitHash(%q) = %v, want %v", in, got, want)
		}
	}
}
