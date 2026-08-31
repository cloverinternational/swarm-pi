package conversation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStampJoinKeysWritesAllKeys is the base case: a conversation created with
// no metadata at all still ends up carrying every join key it was given, under
// the exact wire names that will appear in metadata.custom on disk.
func TestStampJoinKeysWritesAllKeys(t *testing.T) {
	md := &ConversationMetadata{}
	StampJoinKeys(md, JoinKeys{
		SessionID:            "sess-1",
		AgentID:              "agent-1",
		Origin:               OriginInteractive,
		ParentConversationID: "conv-parent",
		ParentAgentID:        "agent-parent",
		GitSHA:               "0123456789abcdef0123456789abcdef01234567",
	})

	want := map[string]string{
		"session_id":             "sess-1",
		"agent_id":               "agent-1",
		"origin":                 "interactive",
		"parent_conversation_id": "conv-parent",
		"parent_agent_id":        "agent-parent",
		"git_sha":                "0123456789abcdef0123456789abcdef01234567",
	}
	for key, expected := range want {
		got, ok := md.Custom[key].(string)
		if !ok || got != expected {
			t.Errorf("Custom[%q] = %#v, want %q", key, md.Custom[key], expected)
		}
	}
	if len(md.Custom) != len(want) {
		t.Errorf("Custom has %d keys (%#v), want exactly %d", len(md.Custom), md.Custom, len(want))
	}
}

// TestStampJoinKeysSkipsEmptyFields proves empty fields are omitted rather than
// written as "". A reader must be able to treat "key absent" as the only form of
// "unknown"; empty-string values would create a second one.
func TestStampJoinKeysSkipsEmptyFields(t *testing.T) {
	md := &ConversationMetadata{}
	StampJoinKeys(md, JoinKeys{SessionID: "sess-only"})

	if len(md.Custom) != 1 {
		t.Fatalf("Custom = %#v, want exactly one key", md.Custom)
	}
	if _, present := md.Custom[CustomKeyAgentID]; present {
		t.Errorf("empty AgentID was written: %#v", md.Custom)
	}
	if _, present := md.Custom[CustomKeyGitSHA]; present {
		t.Errorf("empty GitSHA was written: %#v", md.Custom)
	}
}

// TestStampJoinKeysPreservesExistingValues is the additive-only guarantee: a key
// that already carries a value is never rewritten. This is what makes the stamp
// idempotent (so a re-stamp cannot mutate a persisted join key) and what lets
// the headless path keep the origin it seeded itself.
func TestStampJoinKeysPreservesExistingValues(t *testing.T) {
	md := &ConversationMetadata{Custom: map[string]any{
		"origin":         OriginHeadless,
		"session_id":     "original-session",
		"workspace_path": "/work",
		"forked_from":    "conv-root",
	}}

	StampJoinKeys(md, JoinKeys{
		SessionID: "new-session",
		Origin:    OriginInteractive,
		AgentID:   "agent-1",
	})

	if md.Custom["session_id"] != "original-session" {
		t.Errorf("session_id = %#v, want the pre-existing value to survive", md.Custom["session_id"])
	}
	if md.Custom["origin"] != OriginHeadless {
		t.Errorf("origin = %#v, want the pre-existing value to survive", md.Custom["origin"])
	}
	// Unrelated pre-existing keys must be untouched — the change is additive.
	if md.Custom["workspace_path"] != "/work" || md.Custom["forked_from"] != "conv-root" {
		t.Errorf("unrelated keys were disturbed: %#v", md.Custom)
	}
	// The genuinely absent key is still filled.
	if md.Custom["agent_id"] != "agent-1" {
		t.Errorf("agent_id = %#v, want it to be added", md.Custom["agent_id"])
	}
}

// TestStampJoinKeysOverwritesEmptyStringValue covers the awkward middle state: a
// key present but empty is treated as absent, otherwise a conversation written
// by an older or sloppier producer would be permanently un-stampable.
func TestStampJoinKeysOverwritesEmptyStringValue(t *testing.T) {
	md := &ConversationMetadata{Custom: map[string]any{"session_id": ""}}
	StampJoinKeys(md, JoinKeys{SessionID: "sess-1"})
	if md.Custom["session_id"] != "sess-1" {
		t.Errorf("session_id = %#v, want the empty placeholder to be filled", md.Custom["session_id"])
	}
}

// TestStampJoinKeysNilAndZeroAreNoOps: bookkeeping must never panic or allocate
// its way into a conversation that asked for nothing.
func TestStampJoinKeysNilAndZeroAreNoOps(t *testing.T) {
	StampJoinKeys(nil, JoinKeys{SessionID: "sess"}) // must not panic

	md := &ConversationMetadata{}
	StampJoinKeys(md, JoinKeys{})
	if md.Custom != nil {
		t.Errorf("Custom = %#v, want nil (no keys, no allocation)", md.Custom)
	}
}

// TestReadJoinKeysToleratesLegacyMetadata is the backward-compatibility contract
// at the type level: every conversation written before this change has none of
// these keys, so reading them must yield zero values and never an error or a
// panic — including when Custom is nil, or holds a non-string under one of our
// key names.
func TestReadJoinKeysToleratesLegacyMetadata(t *testing.T) {
	if got := ReadJoinKeys(ConversationMetadata{}); got != (JoinKeys{}) {
		t.Errorf("ReadJoinKeys(nil Custom) = %#v, want zero value", got)
	}

	legacy := ConversationMetadata{Custom: map[string]any{
		"workspace_path": "/work",
		"git_branch":     "main",
		"forked_from":    "conv-root",
	}}
	if got := ReadJoinKeys(legacy); got != (JoinKeys{}) {
		t.Errorf("ReadJoinKeys(legacy) = %#v, want zero value", got)
	}

	// A wrong-typed value (e.g. a number written by some other producer) must
	// degrade to "" rather than panicking on the type assertion.
	weird := ConversationMetadata{Custom: map[string]any{"session_id": 42}}
	if got := ReadJoinKeys(weird); got.SessionID != "" {
		t.Errorf("SessionID = %q, want empty for a non-string value", got.SessionID)
	}
}

// TestReadJoinKeysRoundTrip pairs the writer and the reader so a rename of one
// key on one side cannot pass unnoticed.
func TestReadJoinKeysRoundTrip(t *testing.T) {
	want := JoinKeys{
		SessionID:            "sess-1",
		AgentID:              "agent-1",
		Origin:               OriginSubagent,
		ParentConversationID: "conv-parent",
		ParentAgentID:        "agent-parent",
		GitSHA:               "0123456789abcdef0123456789abcdef01234567",
	}
	md := &ConversationMetadata{}
	StampJoinKeys(md, want)
	if got := ReadJoinKeys(*md); got != want {
		t.Errorf("round trip = %#v, want %#v", got, want)
	}
}

// TestClearProcessScopedJoinKeys documents exactly which keys a compaction-style
// metadata clone must drop, and which it must keep.
func TestClearProcessScopedJoinKeys(t *testing.T) {
	custom := map[string]any{
		"session_id":     "old-session",
		"git_sha":        "0123456789abcdef0123456789abcdef01234567",
		"agent_id":       "agent-1",
		"origin":         OriginInteractive,
		"workspace_path": "/work",
		"compacted_from": "conv-old",
	}
	ClearProcessScopedJoinKeys(custom)

	for _, gone := range []string{"session_id", "git_sha"} {
		if _, present := custom[gone]; present {
			t.Errorf("%s survived; a clone would inherit a stale value", gone)
		}
	}
	for key, want := range map[string]string{
		"agent_id":       "agent-1",
		"origin":         OriginInteractive,
		"workspace_path": "/work",
		"compacted_from": "conv-old",
	} {
		if custom[key] != want {
			t.Errorf("Custom[%q] = %#v, want %q preserved", key, custom[key], want)
		}
	}

	ClearProcessScopedJoinKeys(nil) // must not panic
}

// TestProcessSessionIDIsStable: the join key is worthless if it changes between
// two conversations created by the same process.
func TestProcessSessionIDIsStable(t *testing.T) {
	first := ProcessSessionID()
	if first == "" {
		t.Fatal("ProcessSessionID returned empty")
	}
	if second := ProcessSessionID(); second != first {
		t.Errorf("ProcessSessionID changed between calls: %q then %q", first, second)
	}
}

// TestSetProcessSessionIDOverrides covers the embedder path (and restores the
// previous value so test ordering cannot leak).
func TestSetProcessSessionIDOverrides(t *testing.T) {
	original := ProcessSessionID()
	t.Cleanup(func() { SetProcessSessionID(original) })

	SetProcessSessionID("explicit-session")
	if got := ProcessSessionID(); got != "explicit-session" {
		t.Errorf("ProcessSessionID = %q, want the override", got)
	}

	SetProcessSessionID("") // ignored
	if got := ProcessSessionID(); got != "explicit-session" {
		t.Errorf("ProcessSessionID = %q, want the empty set to be ignored", got)
	}
}

// ─── git_sha resolution (no subprocess) ──────────────────────────────────────

const testSHA = "1234567890abcdef1234567890abcdef12345678"

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// TestGitSHAFromLooseRef is the ordinary checkout: HEAD is symbolic, the branch
// tip is a loose ref file. Built by hand rather than by shelling out to git,
// which is the whole point — the resolver must never fork a process.
func TestGitSHAFromLooseRef(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".git", "HEAD"), "ref: refs/heads/feat/harness-anywhere\n")
	writeFile(t, filepath.Join(work, ".git", "refs", "heads", "feat", "harness-anywhere"), testSHA+"\n")

	if got := GitSHA(work); got != testSHA {
		t.Errorf("GitSHA = %q, want %q", got, testSHA)
	}
}

// TestGitSHAFromSubdirectory: a conversation whose workspace is a subdirectory
// of the checkout must still resolve, matching git's own containment rule.
func TestGitSHAFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, ".git", "refs", "heads", "main"), testSHA)

	sub := filepath.Join(root, "swarm-sdk", "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if got := GitSHA(sub); got != testSHA {
		t.Errorf("GitSHA(subdir) = %q, want %q", got, testSHA)
	}
}

// TestGitSHADetachedHead: HEAD holds the SHA directly.
func TestGitSHADetachedHead(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".git", "HEAD"), testSHA+"\n")
	if got := GitSHA(work); got != testSHA {
		t.Errorf("GitSHA = %q, want %q", got, testSHA)
	}
}

// TestGitSHAFromPackedRefs: after gc the branch tip lives in packed-refs, not in
// a loose file. The peeled-tag line ("^<sha>") must not be mistaken for an entry.
func TestGitSHAFromPackedRefs(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(work, ".git", "packed-refs"),
		"# pack-refs with: peeled fully-peeled sorted \n"+
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa refs/tags/v1\n"+
			"^bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"+
			testSHA+" refs/heads/main\n")

	if got := GitSHA(work); got != testSHA {
		t.Errorf("GitSHA = %q, want %q", got, testSHA)
	}
}

// TestGitSHALinkedWorktree: ".git" is a file pointing at a private git dir whose
// HEAD is local but whose refs live in the shared common dir. This is the shape
// this repository itself uses under .worktrees/, so getting it wrong would mean
// silently no git_sha for exactly the branchy runs we care about.
func TestGitSHALinkedWorktree(t *testing.T) {
	root := t.TempDir()
	mainGit := filepath.Join(root, "main", ".git")
	writeFile(t, filepath.Join(mainGit, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(mainGit, "refs", "heads", "feature"), testSHA+"\n")

	wtGit := filepath.Join(mainGit, "worktrees", "feature")
	writeFile(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/feature\n")
	writeFile(t, filepath.Join(wtGit, "commondir"), "../..\n")

	work := filepath.Join(root, "feature-checkout")
	writeFile(t, filepath.Join(work, ".git"), "gitdir: "+wtGit+"\n")

	if got := GitSHA(work); got != testSHA {
		t.Errorf("GitSHA(worktree) = %q, want %q resolved via commondir", got, testSHA)
	}
}

// TestGitSHAAbsentIsEmptyNotError: a workspace outside any repository, an unborn
// branch, and an empty path all yield "" — a normal answer, never a failure that
// could block conversation creation.
func TestGitSHAAbsentIsEmptyNotError(t *testing.T) {
	if got := GitSHA(""); got != "" {
		t.Errorf("GitSHA(\"\") = %q, want empty", got)
	}
	if got := GitSHA(t.TempDir()); got != "" {
		t.Errorf("GitSHA(non-repo) = %q, want empty", got)
	}

	unborn := t.TempDir()
	writeFile(t, filepath.Join(unborn, ".git", "HEAD"), "ref: refs/heads/main\n")
	if got := GitSHA(unborn); got != "" {
		t.Errorf("GitSHA(unborn branch) = %q, want empty", got)
	}
}

// TestGitSHARejectsMalformedRef: a ref file holding something that is not an
// object ID must not be persisted as if it were one.
func TestGitSHARejectsMalformedRef(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(work, ".git", "refs", "heads", "main"), "not-a-sha\n")
	if got := GitSHA(work); got != "" {
		t.Errorf("GitSHA(malformed) = %q, want empty", got)
	}

	traversal := t.TempDir()
	writeFile(t, filepath.Join(traversal, ".git", "HEAD"), "ref: ../../../etc/passwd\n")
	if got := GitSHA(traversal); got != "" {
		t.Errorf("GitSHA(traversal ref) = %q, want empty", got)
	}
}

// TestGitSHAIsMemoised guards the no-hot-path-cost constraint: the second call
// for a workspace must not touch the filesystem again. Verified by deleting the
// repository out from under it — a cached answer survives, an uncached one would
// change.
func TestGitSHAIsMemoised(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".git", "HEAD"), testSHA+"\n")

	if got := GitSHA(work); got != testSHA {
		t.Fatalf("first GitSHA = %q, want %q", got, testSHA)
	}
	if err := os.RemoveAll(filepath.Join(work, ".git")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if got := GitSHA(work); got != testSHA {
		t.Errorf("second GitSHA = %q, want the memoised %q (filesystem was re-read)", got, testSHA)
	}
}

// TestGitSHAAgainstThisRepository is the reality check: run the resolver against
// the checkout the test itself lives in. Skipped when the tree is not a git
// repository (tarball export, vendored copy) so it can never be a flaky gate.
func TestGitSHAAgainstThisRepository(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	sha := GitSHA(wd)
	if sha == "" {
		t.Skip("not running inside a git checkout")
	}
	if len(sha) != 40 && len(sha) != 64 {
		t.Errorf("GitSHA(%s) = %q, want a 40- or 64-char object ID", wd, sha)
	}
	if strings.TrimSpace(sha) != sha {
		t.Errorf("GitSHA returned unt-rimmed value %q", sha)
	}
}
