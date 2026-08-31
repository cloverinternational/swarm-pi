// effect.go — the ledger's atomic row (PLAN.md §3 "the stored atom is the
// file effect") plus the two small, dependency-free helpers it needs:
// a git-compatible blob hash and a git-common-dir-based project bucket.
//
// # Why blob hashes, not a commit SHA
//
// A commit SHA self-invalidates on the first rebase, amend, or squash, and
// agent history *is* rewritten routinely. A git blob hash is content
// addressed: sha1("blob "+len(content)+"\x00"+content) is the same value
// today, after a rebase, and after a squash, because it depends only on the
// bytes, never on history. Storing it also solves redaction for free — a
// 40-hex string carries no content, so there is nothing in the ledger row to
// scrub.
//
// # Why this file has no dependencies on the rest of the SDK
//
// Mirrors the design note at the top of gate.go: every layer that must
// record a measurement needs to import this package, so this package must
// never drag the rest of the SDK in behind it. In particular it does NOT
// import internal/conversation, even though internal/conversation/gitsha.go
// solves an almost identical problem (resolving HEAD without a subprocess).
// Importing that package would make the whole `bench` package depend on
// conversation's full transitive dependency graph merely to reuse ~40 lines
// of file-reading logic. Instead this file reimplements the *approach* —
// walk up to find .git, follow a "gitdir:" file for linked worktrees, resolve
// "commondir", read HEAD, follow one ref — deliberately kept smaller because
// bench only needs a stable bucket key and a best-effort hint, not a
// guaranteed-correct SHA for every edge case gitsha.go handles.
package bench

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Effect is one file mutation — the atom PLAN.md §3 defines the ledger
// around. It is the only row kind Phase B step 1 emits; episode and verdict
// rows are later phases over the same append-only file.
//
// # STRUCTURAL GUARANTEE: no verdict is possible from this type
//
// Effect has no field named (or resembling) Verdict, Pass, Fail, Score,
// Reward, or Status, and this file defines no function or method that
// derives one from an Effect or from anything an Effect carries (notably:
// there is no field for a tool exit code at all — Op/PreBlob/PostBlob record
// WHAT changed, never whether the change was good). PLAN.md §9.2 requires
// this be enforced by the type system and a test, not by convention:
// TestEffect_HasNoVerdictSurface (reflection over the struct fields) and
// TestBenchPackage_SourceContainsNoVerdictVocabulary (an AST scan of every
// .go file in this package, non-test, for verdict-shaped identifiers) both
// live in effect_test.go. A later phase that wants to append a `verdict` row
// kind does so as a NEW, separate type in a NEW file — it can never become a
// field on Effect without both tests failing first.
type Effect struct {
	// Path is the absolute path the tool mutated.
	Path string `json:"path"`

	// Op classifies what happened to Path: "create", "update", "delete", or
	// "move" — the toolout.FileOp vocabulary, copied as a plain string so
	// this package does not need to import internal/toolout either.
	Op string `json:"op,omitempty"`

	// PreBlob / PostBlob are git blob hashes (see blobHash) of the content
	// before and after the mutation. Either may be empty: PreBlob is empty
	// for a create (there was no prior content), PostBlob is empty for a
	// delete (there is no surviving content to hash). An empty value is
	// "not established", never "empty file" — an empty file has a real,
	// non-empty blob hash (the hash of zero bytes).
	PreBlob  string `json:"pre_blob,omitempty"`
	PostBlob string `json:"post_blob,omitempty"`

	// HeadSHA is HEAD at the time this row was written — a HINT for "where
	// did this run start", never ground truth (PLAN.md §3). It self-
	// invalidates the moment history is rewritten, which is why PreBlob/
	// PostBlob, not this field, are what later phases diff against.
	HeadSHA string `json:"head_sha,omitempty"`

	// Tool is the registered tool name that performed the mutation.
	Tool string `json:"tool"`

	// SessionID is the join key: byte-identical to
	// conversation.ProcessSessionID() (internal/conversation/joinkey.go),
	// which is the SAME value the conversation metadata "session_id" custom
	// field stores and the SAME value hooks.EventAgentStarted (SessionStart)
	// carries in its ConversationID/Data["session_id"] fields.
	//
	// It is NOT the same thing as the "session_id" that appears elsewhere in
	// the hook stream: hooks.EventUserPromptSubmit and
	// hooks.EventAgentStopped populate their own Data["session_id"] (and the
	// Claude-Code-compatible external hook JSON's "session_id") with the
	// CONVERSATION id, not the process session id — see
	// hooks/event.go:ConversationID's doc comment for the full picture. To
	// join THIS field, match against conversation.ProcessSessionID() /
	// metadata.custom["session_id"] / a SessionStart event, never against a
	// UserPromptSubmit or AgentStopped event's "session_id". Populated by the
	// caller (internal/agent/agent_tools.go already resolves it); this
	// package does not resolve it itself, to avoid importing
	// internal/conversation.
	SessionID string `json:"session_id,omitempty"`

	// AgentID / ConversationID attribute the row to the agent definition and
	// conversation that produced it. Both may be empty (sub-agent runs do not
	// currently get their own conversation file — see joinkey.go's note on
	// PLAN.md §10.2); an empty value is the documented normal case, not an
	// error.
	AgentID        string `json:"agent_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`

	// TS is when the row was recorded (UTC).
	TS time.Time `json:"ts"`
}

// blobHash computes the git object hash for content the same way `git
// hash-object` computes the hash of a blob: sha1("blob "+len(content)+"\x00"+content).
// It never shells out to git — see effect_test.go for a fixture verifying
// this matches a real git object.
func blobHash(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// BlobHash is the exported form of blobHash, for callers outside this
// package that already hold mutation bytes in memory (see
// internal/tools/forge/apply_patch.go, which computes PreBlobSHA1/
// PostBlobSHA1 on toolout.FileEffect at the moment it commits a change — that
// is the "bytes the tool already had" the non-negotiables require; nothing
// downstream of that point re-reads a file from disk to compute a hash).
func BlobHash(content []byte) string {
	return blobHash(content)
}

// maxGitRefFileBytes bounds every file this package reads, mirroring
// gitsha.go's identical cap: HEAD and a loose ref are tens of bytes,
// packed-refs is the only file that can grow, and a few megabytes is already
// an extreme repository. A hostile or corrupt .git must not make this
// allocate without bound.
const maxGitRefFileBytes = 8 << 20 // 8 MiB

type repoCoordsResult struct {
	bucket  string
	headSHA string
}

var repoCoordsCache sync.Map // map[string]repoCoordsResult

// repoCoords resolves the ledger bucket and HEAD hint for workspacePath.
//
// bucket is derived from the git COMMON directory shared by every linked
// worktree of one repository — never from workspacePath verbatim. That is
// what stops `.worktrees/<feature>/` checkouts from fragmenting one project
// into N ledger buckets (PLAN.md §3: "the store has already fragmented
// project identity 53 ways"). When workspacePath is not inside a git
// repository, bucket falls back to a hash of the workspace path itself, so
// every run still lands in a stable, filesystem-safe bucket.
//
// The hash algorithm (sha256, first 16 hex chars) matches the convention
// already used by ~/.swarm/projects/<hash>/{bronze,findings,silver,gold} —
// see internal/findings/types.go:workspaceHash and
// swarm-tui/internal/analytics/backfill/helpers.go:projectHashForWorkspace —
// so `bench` sits as a sibling of those directories under the same bucket
// scheme, and is never generically walked by their path-specific scanners.
//
// Memoised per workspace path for the life of the process: the first call
// does a handful of small file reads, every later call for the same
// workspace is a sync.Map load. There is no exec.Command anywhere in this
// function.
func repoCoords(workspacePath string) (bucket, headSHA string) {
	if workspacePath == "" {
		return "", ""
	}
	if cached, ok := repoCoordsCache.Load(workspacePath); ok {
		r := cached.(repoCoordsResult)
		return r.bucket, r.headSHA
	}
	r := resolveRepoCoords(workspacePath)
	repoCoordsCache.Store(workspacePath, r)
	return r.bucket, r.headSHA
}

func resolveRepoCoords(workspacePath string) repoCoordsResult {
	gitDir, commonDir := locateBenchGitDir(workspacePath)
	if gitDir == "" {
		// Not a git repository: bucket on the workspace path itself so the
		// ledger still has a stable home.
		return repoCoordsResult{bucket: hashBucket(workspacePath)}
	}
	// bucketRoot is the directory identity to hash for the bucket: the
	// common dir when this is a linked worktree (shared across every
	// worktree of the same repo), otherwise the git dir itself.
	bucketRoot := gitDir
	if commonDir != "" {
		bucketRoot = commonDir
	}
	return repoCoordsResult{
		bucket:  hashBucket(bucketRoot),
		headSHA: resolveBenchHeadSHA(gitDir, commonDir),
	}
}

func hashBucket(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// locateBenchGitDir walks upward from workspacePath to find a .git entry,
// returning the git directory and (for a linked worktree) the shared common
// directory. Mirrors internal/conversation/gitsha.go:locateGitDir.
func locateBenchGitDir(workspacePath string) (gitDir, commonDir string) {
	dir := workspacePath
	for {
		candidate := filepath.Join(dir, ".git")
		info, err := os.Stat(candidate)
		switch {
		case err == nil && info.IsDir():
			return candidate, ""
		case err == nil && info.Mode().IsRegular():
			contents := strings.TrimSpace(readBenchSmallFile(candidate))
			pointer := strings.TrimSpace(strings.TrimPrefix(contents, "gitdir:"))
			if pointer == "" || pointer == contents {
				return "", ""
			}
			if !filepath.IsAbs(pointer) {
				pointer = filepath.Join(dir, pointer)
			}
			return pointer, readBenchCommonDir(pointer)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

func readBenchCommonDir(gitDir string) string {
	pointer := strings.TrimSpace(readBenchSmallFile(filepath.Join(gitDir, "commondir")))
	if pointer == "" {
		return ""
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(gitDir, pointer)
	}
	return filepath.Clean(pointer)
}

// resolveBenchHeadSHA reads HEAD and follows at most one level of ref,
// checking loose refs in both the git dir and the common dir before falling
// back to packed-refs. Returns "" for anything it cannot resolve cheaply —
// "" is a normal, expected answer (HeadSHA is a hint only).
func resolveBenchHeadSHA(gitDir, commonDir string) string {
	head := strings.TrimSpace(readBenchSmallFile(filepath.Join(gitDir, "HEAD")))
	if head == "" {
		return ""
	}
	if !strings.HasPrefix(head, "ref:") {
		return validBenchSHA(head)
	}
	ref := strings.TrimSpace(strings.TrimPrefix(head, "ref:"))
	if ref == "" || strings.Contains(ref, "..") {
		return ""
	}
	for _, dir := range []string{gitDir, commonDir} {
		if dir == "" {
			continue
		}
		if sha := validBenchSHA(strings.TrimSpace(readBenchSmallFile(filepath.Join(dir, filepath.FromSlash(ref))))); sha != "" {
			return sha
		}
	}
	for _, dir := range []string{gitDir, commonDir} {
		if dir == "" {
			continue
		}
		if sha := lookupBenchPackedRef(filepath.Join(dir, "packed-refs"), ref); sha != "" {
			return sha
		}
	}
	return ""
}

func lookupBenchPackedRef(path, ref string) string {
	contents := readBenchSmallFile(path)
	if contents == "" {
		return ""
	}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		sha, name, found := strings.Cut(line, " ")
		if !found || strings.TrimSpace(name) != ref {
			continue
		}
		return validBenchSHA(sha)
	}
	return ""
}

func readBenchSmallFile(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxGitRefFileBytes {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func validBenchSHA(s string) string {
	s = strings.TrimSpace(s)
	if len(s) != 40 && len(s) != 64 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return ""
		}
	}
	return s
}
