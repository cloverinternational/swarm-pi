package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const taskTOMLTemplate = `schema_version = "1.3"
artifacts = []
[task]
name = %q
description = %q
authors = [{ name = "harborwild (mined from swarm-sdk git history)" }]
keywords = ["wild", "regression", "swarm-sdk"]
[metadata]
source_sha = %q
source_parent_sha = %q
[verifier]
timeout_sec = 900.0
collect = []
[verifier.env]
[agent]
timeout_sec = 1800.0
[environment]
network_mode = %q
build_timeout_sec = 900.0
os = "linux"
mcp_servers = []
[environment.env]
[solution.env]
`

const dockerfileTemplate = `FROM golang:1.26-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
      git patch ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app/repo

# Self-contained repo snapshot at the bug's parent commit (pre-fix). Baked in
# at packaging time by harborwild via 'git archive' -- no network git remote
# or live mount of the authoring sandbox required to build this image.
COPY repo.tar.gz /tmp/repo.tar.gz
RUN tar xzf /tmp/repo.tar.gz -C /app/repo && rm /tmp/repo.tar.gz

# Establish the RED state: the regression test(s) exist, the production fix
# does not. This patch touches ONLY *_test.go files -- see task.toml
# metadata.source_sha for the commit this was split from.
COPY test.patch /tmp/test.patch
RUN cd /app/repo && patch -p1 < /tmp/test.patch && rm /tmp/test.patch

WORKDIR /app/repo/%s

# Warm the module cache at build time so the agent's run has no network
# dependency even under network_mode = "none".
RUN go mod download

# Confirm the task actually starts RED: the regression test must fail before
# any fix is applied. A task that is already green is not a real task.
# (Deliberately avoids a shell pipe here -- 'go test | tee' would make the
# RUN step's exit status reflect tee, not go test, masking a vacuous task.)
# (Deliberately NOT 'set -e' here either -- go test failing is the EXPECTED
# outcome pre-fix, and 'set -e' would abort this RUN step the instant that
# expected-failing command runs, before 'ec=$?' is ever reached, turning
# every real RED task into a spurious Docker build failure.)
RUN go test -tags %s -race -count=5 -run %q %s > /tmp/pretest.log 2>&1; ec=$?; \
    if [ "$ec" -eq 0 ]; then \
      echo "harborwild: FATAL -- regression test already passes before any fix; task is vacuous" >&2; \
      cat /tmp/pretest.log >&2; \
      exit 1; \
    fi; \
    echo "harborwild: confirmed RED -- regression test fails pre-fix as expected"
`

const testShTemplate = `#!/bin/bash
# Copied to /tests/test.sh by Harbor and run from the working directory.
# Re-runs the real regression test(s) that proved this bug RED before the fix
# and GREEN after it. Writes the Harbor reward contract file.
set -u
cd /app/repo/%s || { echo "harborwild: repo dir missing" >&2; echo 0 > /logs/verifier/reward.txt; exit 1; }

mkdir -p /logs/verifier

# Scoped to the same package tree as the regression test (TestPkg), not the
# whole module -- building './...' here would also compile unrelated
# packages elsewhere in the module (e.g. ones with cgo/system-library
# dependencies not installed in this minimal image), failing the verifier
# for reasons having nothing to do with whether the actual fix is correct.
if go build -tags %s %s; then
  echo "build: OK"
else
  echo "build: FAILED"
  echo 0 > /logs/verifier/reward.txt
  exit 0
fi

if go test -tags %s -race -run %q -v %s > /logs/verifier/test.log 2>&1; then
  echo "test: PASS"
  echo 1 > /logs/verifier/reward.txt
else
  echo "test: FAIL"
  cat /logs/verifier/test.log
  echo 0 > /logs/verifier/reward.txt
fi
exit 0
`

const solveShTemplate = `#!/bin/bash
# Oracle solution: applies the REAL historical fix diff for this bug (see
# task.toml metadata.source_sha). Used only to validate that this task is
# solvable and that the verifier actually discriminates buggy-vs-fixed -- not
# given to a real agent under evaluation.
set -eu
cd /app/repo
patch -p1 < "$(dirname "$0")/fix.patch"
`

// packageTask synthesizes and writes one Harbor task directory for the bug
// described by m. See main.go's doc comment for the RED-state synthesis
// rationale.
func packageTask(repoRoot, outDir string, m Manifest, dryRun bool) error {
	sha, err := gitResolve(repoRoot, m.SHA)
	if err != nil {
		return fmt.Errorf("resolve sha %s: %w", m.SHA, err)
	}
	parent, err := gitParent(repoRoot, sha)
	if err != nil {
		return fmt.Errorf("resolve parent of %s: %w", sha, err)
	}

	changed, err := gitChangedFiles(repoRoot, parent, sha)
	if err != nil {
		return fmt.Errorf("diff --name-only %s..%s: %w", parent, sha, err)
	}
	if len(changed) == 0 {
		return fmt.Errorf("commit %s has no diff against its parent %s", sha, parent)
	}

	var testFiles, prodFiles []string
	for _, f := range changed {
		if strings.HasSuffix(f, "_test.go") {
			testFiles = append(testFiles, f)
		} else {
			prodFiles = append(prodFiles, f)
		}
	}
	for _, f := range m.ExtraTestFiles {
		found := false
		for _, existing := range testFiles {
			if existing == f {
				found = true
				break
			}
		}
		if !found {
			testFiles = append(testFiles, f)
		}
	}
	sort.Strings(testFiles)
	sort.Strings(prodFiles)

	if len(testFiles) == 0 {
		return fmt.Errorf("commit %s changes no *_test.go files -- nothing to use as the RED-state setup patch; specify extraTestFiles if the test lives elsewhere", sha)
	}
	if len(prodFiles) == 0 {
		return fmt.Errorf("commit %s changes no non-test files -- nothing for solution/fix.patch to apply; this looks like a test-only commit, not a bugfix", sha)
	}

	testPatch, err := gitDiffPaths(repoRoot, parent, sha, testFiles)
	if err != nil {
		return fmt.Errorf("diff test files: %w", err)
	}
	fixPatch, err := gitDiffPaths(repoRoot, parent, sha, prodFiles)
	if err != nil {
		return fmt.Errorf("diff prod files: %w", err)
	}

	instructionBody, err := os.ReadFile(m.InstructionFile)
	if err != nil {
		return fmt.Errorf("read instruction file: %w", err)
	}

	buildTags := m.BuildTags
	if buildTags == "" {
		buildTags = "none"
	}

	taskDirName := filepath.Base(m.Slug)
	taskDir := filepath.Join(outDir, taskDirName)

	toml := fmt.Sprintf(taskTOMLTemplate, m.Slug, m.Title, sha, parent, m.NetworkMode)
	dockerfile := fmt.Sprintf(dockerfileTemplate, m.GoModDir, buildTags, m.TestRun, m.TestPkg)
	testSh := fmt.Sprintf(testShTemplate, m.GoModDir, buildTags, m.TestPkg, buildTags, m.TestRun, m.TestPkg)

	if dryRun {
		fmt.Printf("=== DRY RUN: %s ===\n", taskDir)
		fmt.Printf("sha=%s parent=%s\n", sha, parent)
		fmt.Printf("testFiles: %v\n", testFiles)
		fmt.Printf("prodFiles: %v\n", prodFiles)
		fmt.Printf("--- task.toml ---\n%s\n", toml)
		fmt.Printf("--- environment/Dockerfile ---\n%s\n", dockerfile)
		fmt.Printf("--- tests/test.sh ---\n%s\n", testSh)
		fmt.Printf("--- environment/test.patch (%d bytes) ---\n", len(testPatch))
		fmt.Printf("--- solution/fix.patch (%d bytes) ---\n", len(fixPatch))
		return nil
	}

	dirs := []string{
		taskDir,
		filepath.Join(taskDir, "environment"),
		filepath.Join(taskDir, "tests"),
		filepath.Join(taskDir, "solution"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	archive, err := gitArchiveTarGz(repoRoot, parent)
	if err != nil {
		return fmt.Errorf("git archive %s: %w", parent, err)
	}

	writes := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{filepath.Join(taskDir, "task.toml"), []byte(toml), 0o644},
		{filepath.Join(taskDir, "instruction.md"), instructionBody, 0o644},
		{filepath.Join(taskDir, "environment", "Dockerfile"), []byte(dockerfile), 0o644},
		{filepath.Join(taskDir, "environment", "repo.tar.gz"), archive, 0o644},
		{filepath.Join(taskDir, "environment", "test.patch"), []byte(testPatch), 0o644},
		{filepath.Join(taskDir, "tests", "test.sh"), []byte(testSh), 0o755},
		{filepath.Join(taskDir, "solution", "solve.sh"), []byte(solveShTemplate), 0o755},
		{filepath.Join(taskDir, "solution", "fix.patch"), []byte(fixPatch), 0o644},
	}
	for _, w := range writes {
		if err := os.WriteFile(w.path, w.data, w.mode); err != nil {
			return fmt.Errorf("write %s: %w", w.path, err)
		}
	}

	fmt.Printf("harborwild: wrote %s (sha=%s parent=%s, %d test file(s), %d prod file(s))\n",
		taskDir, sha[:12], parent[:12], len(testFiles), len(prodFiles))
	return nil
}
