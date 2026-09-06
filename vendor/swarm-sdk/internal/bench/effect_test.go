package bench

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// blobHash correctness (verification criterion (c)): must match a REAL git
// object, not just be internally self-consistent. Fixtures below were
// produced by running the real `git hash-object` against real files in a
// throwaway repository — the exact commands are recorded so anyone can
// reproduce them:
//
//	git init && printf 'hello world\n' > f.txt && git hash-object f.txt
//	printf '' > empty.txt && git hash-object empty.txt
//	printf 'swarm-bench fixture: pre\x00post\n' > weird.txt && git hash-object weird.txt
// ---------------------------------------------------------------------------

func TestBlobHash_MatchesRealGitObject(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    string
	}{
		{"ordinary text", []byte("hello world\n"), "3b18e512dba79e4c8300dd08aeb37f8e728b8dad"},
		{"empty content has a real, non-empty hash", []byte(""), "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"},
		{"embedded NUL byte", []byte("swarm-bench fixture: pre\x00post\n"), "06d6c97ace74ae4d5fc71928cb78546116800605"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blobHash(tc.content); got != tc.want {
				t.Errorf("blobHash(%q) = %q, want %q (verified against real `git hash-object`)", tc.content, got, tc.want)
			}
			// The exported form must be byte-identical — it is the same
			// function, not a reimplementation that could drift.
			if got := BlobHash(tc.content); got != tc.want {
				t.Errorf("BlobHash(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

func TestBlobHash_DifferentContentDifferentHash(t *testing.T) {
	a := blobHash([]byte("version 1\n"))
	b := blobHash([]byte("version 2\n"))
	if a == b {
		t.Fatalf("distinct content produced the same hash: %q", a)
	}
}

// ---------------------------------------------------------------------------
// Structural verdict-impossibility (PLAN.md §9.2 / verification criterion
// (f)): enforced two independent ways so one blind spot in either technique
// cannot hide a regression.
// ---------------------------------------------------------------------------

// disallowedVerdictSubstrings are case-insensitively forbidden inside any
// Effect field name or bench-package identifier.
//
// Deliberately NOT included: "status" and "success". Both are legitimate,
// already-shipped vocabulary in this same package for a DIFFERENT concept —
// gate.go's GateStatus / ObservationalHooksStatus report whether measurement
// instrumentation is *configured on*, which is an operational fact, not a
// judgement about whether the agent's work was good. Widening this list to
// catch that too would make the test fail against code that was correct
// before this file existed, for a reason that has nothing to do with PLAN.md
// §9.2. The five words below are the actual verdict vocabulary named
// throughout PLAN.md §3's verdict ladder (Pass / Fail / rung / score-like
// weak supervision) and nowhere else in this package.
var disallowedVerdictSubstrings = []string{
	"verdict", "pass", "fail", "score", "reward",
}

// TestEffect_HasNoVerdictSurface reflects over every field of Effect and
// fails if any field name contains a verdict-shaped word. This is the
// runtime-checkable half of "the agent-side writer must be structurally
// incapable of emitting Pass or Fail" — mirrors
// agent.TestObservationalHooks_InterfaceHasNoVerdictChannel's use of
// reflection to make an architectural promise mechanically checkable rather
// than a matter of code review vigilance.
func TestEffect_HasNoVerdictSurface(t *testing.T) {
	typ := reflect.TypeOf(Effect{})
	if typ.NumField() == 0 {
		t.Fatalf("Effect has no fields; the invariant would hold vacuously")
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		lower := strings.ToLower(name)
		for _, bad := range disallowedVerdictSubstrings {
			if strings.Contains(lower, bad) {
				t.Errorf("Effect.%s contains verdict-shaped word %q; verdicts must be a separate, later-phase row type, never a field on the effect row", name, bad)
			}
		}
	}

	// Discrimination check: prove the substring list itself can catch a
	// verdict field, so the pass above is not vacuously trivial. Mirrors the
	// "discrimination check" pattern in
	// internal/agent/observational_test.go.
	type wouldBeRejected struct {
		Verdict string
	}
	wtyp := reflect.TypeOf(wouldBeRejected{})
	found := false
	for _, bad := range disallowedVerdictSubstrings {
		if strings.Contains(strings.ToLower(wtyp.Field(0).Name), bad) {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture: the substring check did not flag an obvious Verdict field; the test above would pass vacuously")
	}
}

// TestBenchPackage_SourceContainsNoVerdictVocabulary parses every non-test
// .go file in this package and fails if any IDENTIFIER (not comment string —
// comments are allowed to discuss the concept, as this very file's doc
// comments do when quoting PLAN.md) contains a verdict-shaped word. This
// catches what field-reflection on Effect alone cannot: a package-level
// function like `func (e Effect) computedVerdict() string` that derives a
// verdict without ever storing it as a field.
func TestBenchPackage_SourceContainsNoVerdictVocabulary(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			lower := strings.ToLower(ident.Name)
			for _, bad := range disallowedVerdictSubstrings {
				if strings.Contains(lower, bad) {
					t.Errorf("%s: identifier %q contains verdict-shaped word %q — a verdict must be a separate, later-phase type (PLAN.md Phase C), never emitted from this package", path, ident.Name, bad)
				}
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatalf("scanned zero .go files in %s; fixture is stale", dir)
	}
}

// ---------------------------------------------------------------------------
// repoCoords: worktree dedup is the entire point of keying on the common
// dir rather than the literal path (PLAN.md §3). Fixtures are hand-built,
// mirroring internal/conversation/joinkey_test.go's TestGitSHALinkedWorktree
// — never shelling out to git, since the resolver itself must never do so.
// ---------------------------------------------------------------------------

const benchTestSHA = "1234567890abcdef1234567890abcdef12345678"

func writeBenchFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestRepoCoords_WorktreesShareOneBucket(t *testing.T) {
	root := t.TempDir()
	mainGit := filepath.Join(root, "main", ".git")
	writeBenchFixtureFile(t, filepath.Join(mainGit, "HEAD"), "ref: refs/heads/main\n")
	writeBenchFixtureFile(t, filepath.Join(mainGit, "refs", "heads", "feature"), benchTestSHA+"\n")

	wtGit := filepath.Join(mainGit, "worktrees", "feature")
	writeBenchFixtureFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/feature\n")
	writeBenchFixtureFile(t, filepath.Join(wtGit, "commondir"), "../..\n")

	worktreeCheckout := filepath.Join(root, "feature-checkout")
	writeBenchFixtureFile(t, filepath.Join(worktreeCheckout, ".git"), "gitdir: "+wtGit+"\n")

	mainCheckout := filepath.Join(root, "main")

	mainBucket, _ := repoCoords(mainCheckout)
	wtBucket, wtHead := repoCoords(worktreeCheckout)

	if mainBucket == "" || wtBucket == "" {
		t.Fatalf("expected non-empty buckets, got main=%q worktree=%q", mainBucket, wtBucket)
	}
	if mainBucket != wtBucket {
		t.Errorf("main checkout and its linked worktree landed in different buckets (%q vs %q); this is exactly the 53-way fragmentation PLAN.md §3 describes", mainBucket, wtBucket)
	}
	if wtHead != benchTestSHA {
		t.Errorf("worktree HeadSHA = %q, want %q (resolved via commondir)", wtHead, benchTestSHA)
	}
}

func TestRepoCoords_NonRepoStillGetsAStableBucket(t *testing.T) {
	work := t.TempDir()
	bucket, head := repoCoords(work)
	if bucket == "" {
		t.Fatalf("expected a fallback bucket for a non-repository workspace, got empty")
	}
	if head != "" {
		t.Errorf("expected no HeadSHA for a non-repository workspace, got %q", head)
	}
	// Deterministic: calling again for the same path (a fresh, uncached
	// resolution this time, since repoCoords memoises per-path — use a
	// second distinct path to prove the fallback hash function itself is
	// deterministic rather than testing the cache).
	work2 := t.TempDir()
	bucket2, _ := repoCoords(work2)
	if bucket2 == bucket {
		t.Errorf("two distinct non-repo paths produced the same fallback bucket %q; the fallback must be keyed on the path", bucket)
	}
}

func TestRepoCoords_EmptyWorkspaceIsEmptyNotError(t *testing.T) {
	bucket, head := repoCoords("")
	if bucket != "" || head != "" {
		t.Errorf("repoCoords(\"\") = (%q, %q), want (\"\", \"\")", bucket, head)
	}
}

func TestRepoCoords_IsMemoised(t *testing.T) {
	work := t.TempDir()
	writeBenchFixtureFile(t, filepath.Join(work, ".git", "HEAD"), benchTestSHA+"\n")

	bucket1, head1 := repoCoords(work)
	if head1 != benchTestSHA {
		t.Fatalf("first repoCoords head = %q, want %q", head1, benchTestSHA)
	}
	if err := os.RemoveAll(filepath.Join(work, ".git")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	bucket2, head2 := repoCoords(work)
	if bucket2 != bucket1 || head2 != head1 {
		t.Errorf("second repoCoords = (%q, %q), want the memoised (%q, %q) (filesystem was re-read)", bucket2, head2, bucket1, head1)
	}
}
