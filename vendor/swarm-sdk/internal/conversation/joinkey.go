package conversation

// joinkey.go — the durable join key stamped into metadata.custom at
// conversation creation (PLAN.md gap G1) plus the symmetric origin tag (G6).
//
// WHY THIS EXISTS
//
// Hook events carry a session identity (hooks.HookInput.session_id) and, since
// the agentbridge identity fix, tool events carry AgentID/ConversationID. The
// conversation store carried none of it: across the 1,500 most recent files in
// ~/.swarmos/conversations/ not one had session_id, agent_id, parent or sha.
// The event stream and the conversation store therefore could not be joined,
// which makes sub-agent work unattributable and binds no code change to the
// agent that produced it.
//
// DESIGN CONSTRAINTS (deliberate, do not "simplify" away)
//
//  1. Additive only. Every key lives inside the pre-existing
//     ConversationMetadata.Custom bag (JSON: metadata.custom), alongside the
//     keys already in use there — origin, git_branch, forked_from,
//     compacted_from. No persisted struct field is added, renamed or removed,
//     so every conversation already on disk still loads byte-for-byte the same.
//  2. Optional on read. Every existing file lacks these keys, so ReadJoinKeys
//     treats missing as empty and never errors. Callers must do the same —
//     mirroring internal/conversation/usageindex/parse.go, which reads
//     metadata.custom.forked_from / compacted_from exactly this way.
//  3. Never overwrite. StampJoinKeys only fills keys that are absent or empty.
//     A caller that already set origin (headless does) keeps its value, and a
//     re-stamp of a loaded conversation is a no-op — the key stays stable for
//     the life of the conversation, which is what makes it a join key.
//  4. No hot-path cost. Stamping happens once, at creation, in
//     manager.Create. Nothing here runs per message and nothing here forks a
//     process (see gitsha.go for how git_sha is obtained without exec).

import (
	"sync"

	"github.com/google/uuid"
)

// Custom metadata keys. These are the wire names that appear under
// metadata.custom in the persisted JSON; they are exported so readers
// (usageindex, bench, analytics backfill, probes) can share one spelling
// instead of re-typing string literals that drift.
const (
	// CustomKeySessionID is THE join key: the process-scoped session identity
	// that hook events also carry as HookInput.session_id. Everything else in
	// this file is refinement; without this key the event stream and the
	// conversation store stay disconnected.
	CustomKeySessionID = "session_id"

	// CustomKeyAgentID names the agent definition that owns the conversation
	// (agent.Definition.ID — the same value tool events carry as Event.AgentID
	// via tools.OwnerAgentID).
	CustomKeyAgentID = "agent_id"

	// CustomKeyOrigin classifies how the conversation was started. Present on
	// headless conversations historically; stamped for interactive TUI runs too
	// so stratified reporting stops being guesswork on one side (gap G6).
	CustomKeyOrigin = "origin"

	// CustomKeyParentConversationID / CustomKeyParentAgentID attribute a child
	// run to the run that spawned it.
	//
	// NOTE (measured 2026-07-25, answers PLAN.md §10.2): sub-agent, delegate and
	// background runs do NOT currently get their own conversation file —
	// internal/agent/sub_agent.go executes with ConversationID:"" and the
	// message->storage callback is wired only on the parent client. So these two
	// keys have no producer today by design: the schema is defined and readable,
	// but nothing fabricates a parent link that does not exist. When child runs
	// gain their own persistence (PLAN.md gap G3) the producer sets
	// manager.CreateOptions.ParentConversationID/ParentAgentID and these keys
	// populate with no further change here.
	CustomKeyParentConversationID = "parent_conversation_id"
	CustomKeyParentAgentID        = "parent_agent_id"

	// CustomKeyGitSHA records HEAD at creation time, alongside the git_branch
	// key that already exists. It is a hint, not ground truth: a SHA
	// self-invalidates on rebase/amend/squash, so treat it as "where this run
	// started", never as a content identity.
	CustomKeyGitSHA = "git_sha"

	// CustomKeyGitBranch is the pre-existing branch key, named here so readers
	// can stop hardcoding the literal. Its writers are unchanged.
	CustomKeyGitBranch = "git_branch"
)

// Origin values. Untyped string constants so they drop into map[string]any and
// into manager.CreateOptions without a conversion. The vocabulary is fixed by
// the pre-existing enum in internal/tools/history/history.go (the HistorySearch
// "origin" parameter) — do not add values without changing that enum too.
const (
	// OriginInteractive is a human-driven TUI session.
	OriginInteractive = "interactive"

	// OriginSubagent is a sub-agent / delegated child run.
	OriginSubagent = "subagent"

	// OriginHeadless is a non-interactive `swarm -p` run. Same value as
	// HeadlessTag, which is what the headless creation path already writes.
	OriginHeadless = "headless"
)

// JoinKeys is the identity stamped onto a conversation at creation. Every field
// is optional: an empty field is simply not written, so a caller that knows only
// the session ID stamps only the session ID rather than writing empty strings
// that later readers would have to special-case.
type JoinKeys struct {
	// SessionID is the process-scoped session identity shared with the hook
	// event stream.
	SessionID string

	// AgentID is the owning agent definition ID.
	AgentID string

	// Origin is one of OriginInteractive, OriginSubagent, OriginHeadless.
	Origin string

	// ParentConversationID / ParentAgentID attribute a child run. See the
	// CustomKeyParentConversationID comment: no producer sets these today.
	ParentConversationID string
	ParentAgentID        string

	// GitSHA is HEAD at creation time (hint only).
	GitSHA string
}

// IsZero reports whether there is nothing to stamp.
func (j JoinKeys) IsZero() bool {
	return j.SessionID == "" && j.AgentID == "" && j.Origin == "" &&
		j.ParentConversationID == "" && j.ParentAgentID == "" && j.GitSHA == ""
}

// StampJoinKeys writes the non-empty fields of jk into md.Custom, creating the
// map lazily — the same pattern AddMessage uses for its terminal-state markers.
//
// Existing keys win. A key that is already present with a non-empty value is
// left exactly as it was, which is what keeps the stamp idempotent and stable:
// re-stamping a conversation loaded from disk changes nothing, and a caller that
// pre-seeded origin (the headless path does) is not second-guessed.
//
// A nil md is a no-op rather than a panic; conversation creation must never fail
// because of bookkeeping.
func StampJoinKeys(md *ConversationMetadata, jk JoinKeys) {
	if md == nil || jk.IsZero() {
		return
	}
	set := func(key, value string) {
		if value == "" {
			return
		}
		if existing, ok := md.Custom[key].(string); ok && existing != "" {
			return
		}
		if md.Custom == nil {
			md.Custom = make(map[string]any, 6)
		}
		md.Custom[key] = value
	}
	set(CustomKeySessionID, jk.SessionID)
	set(CustomKeyAgentID, jk.AgentID)
	set(CustomKeyOrigin, jk.Origin)
	set(CustomKeyParentConversationID, jk.ParentConversationID)
	set(CustomKeyParentAgentID, jk.ParentAgentID)
	set(CustomKeyGitSHA, jk.GitSHA)
}

// ReadJoinKeys extracts the join key from metadata. Missing keys, a nil Custom
// map, and non-string values all yield empty strings — never an error. Every
// conversation written before this change lacks all of these keys, so "absent"
// is the normal case for the majority of the store and callers must be able to
// treat the zero value as "unknown", not as "corrupt".
func ReadJoinKeys(md ConversationMetadata) JoinKeys {
	get := func(key string) string {
		if md.Custom == nil {
			return ""
		}
		s, _ := md.Custom[key].(string)
		return s
	}
	return JoinKeys{
		SessionID:            get(CustomKeySessionID),
		AgentID:              get(CustomKeyAgentID),
		Origin:               get(CustomKeyOrigin),
		ParentConversationID: get(CustomKeyParentConversationID),
		ParentAgentID:        get(CustomKeyParentAgentID),
		GitSHA:               get(CustomKeyGitSHA),
	}
}

// ClearProcessScopedJoinKeys removes the join keys whose value belongs to the
// process that created a conversation, so that a NEW conversation cloned from an
// old one's metadata gets re-stamped with current values instead of inheriting
// stale ones.
//
// This matters for compaction. The compaction paths deep-copy the previous
// conversation's metadata.custom to preserve project association, which would
// otherwise carry the OLD session_id forward. That silently breaks the join it
// exists to serve: a conversation resumed in a later process and then compacted
// would claim a session whose events live in a different run. git_sha is cleared
// for the same reason — HEAD has usually moved by then, and a creation-time
// snapshot that is not from creation time is worse than absent.
//
// Lineage-bearing keys are deliberately NOT cleared: origin and agent_id
// genuinely carry over (a compacted interactive conversation is still
// interactive, still owned by the same agent), and forked_from / compacted_from
// are the lineage record itself.
//
// A nil map is a no-op.
func ClearProcessScopedJoinKeys(custom map[string]any) {
	if custom == nil {
		return
	}
	delete(custom, CustomKeySessionID)
	delete(custom, CustomKeyGitSHA)
}

// ─── Process session identity ────────────────────────────────────────────────

var (
	processSessionOnce sync.Once
	processSessionMu   sync.RWMutex
	processSessionID   string
)

// ProcessSessionID returns the session identity for this process, minting a
// UUID on first use.
//
// One process = one session. Both front ends adopt this value rather than
// minting their own, so the identity stamped on a conversation is byte-identical
// to the one the hook/event stream reports:
//   - the TUI seeds its rootSessionID from here (chat/app_init.go), which is the
//     value handed to the plan/interaction brokers and to SessionStart;
//   - headless seeds its printer session ID from here (cmd/swarmos/main.go), which
//     is the value emitted on every stream-json event.
//
// Callers may read this freely; it is a single RLock on an already-resolved
// string and is never touched per message.
func ProcessSessionID() string {
	processSessionOnce.Do(func() {
		processSessionMu.Lock()
		defer processSessionMu.Unlock()
		if processSessionID == "" {
			processSessionID = uuid.NewString()
		}
	})
	processSessionMu.RLock()
	defer processSessionMu.RUnlock()
	return processSessionID
}

// SetProcessSessionID overrides the process session identity. It exists for
// tests and for an embedder that already owns a session identity (an IPC server
// resuming a client's session, say) and needs conversations stamped with that
// value instead of a freshly minted one.
//
// It must be called before the first ProcessSessionID call to be meaningful; a
// later call still takes effect for subsequently created conversations but
// leaves already-stamped ones alone, which is intentional — a stamped join key
// is immutable by design. An empty id is ignored.
func SetProcessSessionID(id string) {
	if id == "" {
		return
	}
	processSessionMu.Lock()
	processSessionID = id
	processSessionMu.Unlock()
	processSessionOnce.Do(func() {})
}
