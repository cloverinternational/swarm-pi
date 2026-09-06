package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
)

func TestLS_FiltersByTool_And_JSON(t *testing.T) {
	repoDir, _, _ := newGitFixture(t)
	baseDir := t.TempDir()
	now := time.Now().UTC()
	buildLedgerFixture(t, baseDir, repoDir, []bench.Effect{
		{Path: filepath.Join(repoDir, "a.txt"), Tool: "apply_patch", Op: "create", PostBlob: "aaaa", TS: now.Add(-2 * time.Minute)},
		{Path: filepath.Join(repoDir, "b.txt"), Tool: "bash", Op: "create", PostBlob: "bbbb", TS: now.Add(-1 * time.Minute)},
		{Path: filepath.Join(repoDir, "c.txt"), Tool: "apply_patch", Op: "update", PostBlob: "cccc", TS: now},
	})

	out := captureStdout(t, func() {
		runLS([]string{"-dir", repoDir, "-base-dir", baseDir, "-tool", "apply_patch", "-json"})
	})

	var rows []bench.Effect
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ls -json output did not parse: %v\noutput:\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows filtered to Tool=apply_patch, got %d: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Tool != "apply_patch" {
			t.Fatalf("row leaked past the -tool filter: %+v", r)
		}
	}
	// Most-recent-first ordering.
	if !rows[0].TS.After(rows[1].TS) {
		t.Fatalf("expected most-recent-first ordering, got %+v then %+v", rows[0].TS, rows[1].TS)
	}
}

func TestLS_LimitFlag_CapsRows(t *testing.T) {
	repoDir, _, _ := newGitFixture(t)
	baseDir := t.TempDir()
	now := time.Now().UTC()
	var effects []bench.Effect
	for i := 0; i < 5; i++ {
		effects = append(effects, bench.Effect{
			Path: filepath.Join(repoDir, "f.txt"), Tool: "apply_patch", Op: "update",
			PostBlob: "blob", TS: now.Add(time.Duration(i) * time.Second),
		})
	}
	buildLedgerFixture(t, baseDir, repoDir, effects)

	out := captureStdout(t, func() {
		runLS([]string{"-dir", repoDir, "-base-dir", baseDir, "-n", "2", "-json"})
	})
	var rows []bench.Effect
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ls -json output did not parse: %v\noutput:\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("expected -n 2 to cap output at 2 rows, got %d", len(rows))
	}
}

func TestLS_HumanTable_ContainsExpectedColumnsAndPath(t *testing.T) {
	repoDir, _, _ := newGitFixture(t)
	baseDir := t.TempDir()
	buildLedgerFixture(t, baseDir, repoDir, []bench.Effect{
		{Path: filepath.Join(repoDir, "special.txt"), Tool: "apply_patch", Op: "create", PostBlob: "deadbeef", TS: time.Now().UTC()},
	})

	out := captureStdout(t, func() {
		runLS([]string{"-dir", repoDir, "-base-dir", baseDir})
	})
	if !strings.Contains(out, "special.txt") {
		t.Fatalf("human table must show the recorded path, got:\n%s", out)
	}
	if !strings.Contains(out, "TOOL") || !strings.Contains(out, "PATH") {
		t.Fatalf("human table must have TOOL/PATH column headers, got:\n%s", out)
	}
	// No verdict-shaped output anywhere in ls either.
	lower := strings.ToLower(out)
	for _, forbidden := range []string{"verdict", "score:", "grade:"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("ls output must never contain %q:\n%s", forbidden, out)
		}
	}
}
