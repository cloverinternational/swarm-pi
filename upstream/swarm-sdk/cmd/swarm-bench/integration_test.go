// integration_test.go — end-to-end tests that build the REAL swarm-bench
// binary and exec it as a subprocess, asserting on actual process exit
// codes. The in-process tests elsewhere in this package call runLS/
// runSurvive directly for speed and precision; this file exists
// specifically to prove requirement (b) literally ("prints the clean
// message and exits 0"), which an in-process call cannot fully prove since
// it never observes a real process exit code.
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
)

var (
	builtBinOnce sync.Once
	builtBinPath string
	builtBinErr  error
)

// TestMain builds the real swarm-bench binary once for every integration
// test in this package and removes the scratch directory when the whole
// test binary exits — the directory must outlive every individual test
// (see buildSwarmBenchBinary's comment on why t.TempDir() is wrong here),
// so this is the one place in the package allowed to own that lifetime.
func TestMain(m *testing.M) {
	code := m.Run()
	if builtBinDir != "" {
		_ = os.RemoveAll(builtBinDir)
	}
	os.Exit(code)
}

var builtBinDir string

// buildSwarmBenchBinary compiles the real cmd/swarm-bench binary exactly as
// the task's verification section specifies (-tags fts5), once per test
// run, and returns its path.
//
// Deliberately uses os.MkdirTemp, NOT t.TempDir(): this helper is shared
// across every test in the package via sync.Once, so the directory must
// outlive whichever single test happens to trigger the build first —
// t.TempDir() would register cleanup on THAT test and get removed before a
// later test in the same run tries to exec the binary.
func buildSwarmBenchBinary(t *testing.T) string {
	t.Helper()
	builtBinOnce.Do(func() {
		cwd, err := os.Getwd() // .../swarm-sdk/cmd/swarm-bench
		if err != nil {
			builtBinErr = err
			return
		}
		moduleRoot := filepath.Join(cwd, "..", "..") // .../swarm-sdk
		tmpDir, err := os.MkdirTemp("", "swarm-bench-integration-test-*")
		if err != nil {
			builtBinErr = err
			return
		}
		builtBinDir = tmpDir
		binPath := filepath.Join(tmpDir, "swarm-bench-under-test")
		cmd := exec.Command("go", "build", "-tags", "fts5", "-o", binPath, "./cmd/swarm-bench")
		cmd.Dir = moduleRoot
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			builtBinErr = err
			t.Logf("go build output:\n%s", out.String())
			return
		}
		builtBinPath = binPath
	})
	if builtBinErr != nil {
		t.Fatalf("building swarm-bench: %v", builtBinErr)
	}
	return builtBinPath
}

func runBinary(t *testing.T, bin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		return outBuf.String(), errBuf.String(), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return outBuf.String(), errBuf.String(), exitErr.ExitCode()
	}
	t.Fatalf("running %s %v: %v", bin, args, err)
	return "", "", -1
}

// TestIntegration_EmptyLedger_RealProcess_ExitsZero is requirement (b),
// proven against a real compiled process rather than an in-process call.
func TestIntegration_EmptyLedger_RealProcess_ExitsZero(t *testing.T) {
	bin := buildSwarmBenchBinary(t)
	dir := t.TempDir()     // a real, empty (non-git) directory
	baseDir := t.TempDir() // never written to: no ledger exists under it

	for _, sub := range [][]string{
		{"ls", "-dir", dir, "-base-dir", baseDir},
		{"survive", "-dir", dir, "-base-dir", baseDir, "-repo", dir},
	} {
		out, errOut, code := runBinary(t, bin, sub...)
		if code != 0 {
			t.Fatalf("%v: exit code = %d, want 0. stdout=%q stderr=%q", sub, code, out, errOut)
		}
		if !strings.Contains(out, "nothing recorded") {
			t.Fatalf("%v: expected 'nothing recorded' in stdout, got: %q (stderr=%q)", sub, out, errOut)
		}
	}
}

// TestIntegration_FullSmoke_RealGitRepo_RealLedger runs `ls` and `survive`
// against a real ledger (written via the real bench.Record path) and a real
// git repository, end to end, as separate OS processes.
func TestIntegration_FullSmoke_RealGitRepo_RealLedger(t *testing.T) {
	bin := buildSwarmBenchBinary(t)
	repoDir, committedBlob, headSHA := newGitFixture(t)
	baseDir := t.TempDir()
	now := time.Now().UTC()
	buildLedgerFixture(t, baseDir, repoDir, []bench.Effect{
		{Path: filepath.Join(repoDir, "committed.txt"), Op: "update", Tool: "apply_patch", PostBlob: committedBlob, TS: now, SessionID: "sess-integration"},
		{Path: filepath.Join(repoDir, "gone.txt"), Op: "delete", Tool: "apply_patch", TS: now, SessionID: "sess-integration"},
	})

	lsOut, lsErr, lsCode := runBinary(t, bin, "ls", "-dir", repoDir, "-base-dir", baseDir)
	if lsCode != 0 {
		t.Fatalf("ls: exit code = %d, stdout=%q stderr=%q", lsCode, lsOut, lsErr)
	}
	if !strings.Contains(lsOut, "committed.txt") || !strings.Contains(lsOut, "gone.txt") {
		t.Fatalf("ls output missing expected paths:\n%s", lsOut)
	}

	surviveOut, surviveErr, surviveCode := runBinary(t, bin, "survive", "-dir", repoDir, "-base-dir", baseDir, "-repo", repoDir)
	if surviveCode != 0 {
		t.Fatalf("survive: exit code = %d, stdout=%q stderr=%q", surviveCode, surviveOut, surviveErr)
	}
	if !strings.Contains(surviveOut, headSHA[:10]) {
		t.Fatalf("survive output must show the real commit that introduced the blob (%s):\n%s", headSHA, surviveOut)
	}
	if !strings.Contains(surviveOut, "NOT ESTABLISHED") || !strings.Contains(surviveOut, "1\n") {
		t.Logf("survive output:\n%s", surviveOut) // informational; exact formatting checked elsewhere
	}

	t.Logf("=== real `swarm-bench ls` output ===\n%s", lsOut)
	t.Logf("=== real `swarm-bench survive` output ===\n%s", surviveOut)
}
