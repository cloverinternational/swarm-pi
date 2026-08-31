// Package serve — session_scope.go
//
// Per-P07.B CONTRACT.md ("Session-scoped workspace/conversation + attempt
// identity wiring"): client.Client's `workspace` field (client/client.go)
// is ONE shared mutable field on the ONE shared *client.Client instance
// serve.NewMux wraps (see mux.go's NewMux(c *client.Client) — every
// attached RPC session dispatches through the SAME *client.Client). Two
// attachclient sessions selecting different workspaces concurrently via
// client.setWorkspace therefore raced on that single field, silently
// cross-contaminating each other's selection — violating
// docs/architecture/swarm-attach/package-boundaries.md:423's documented
// invariant: "Selection is session-scoped and cannot mutate another
// client."
//
// sessionScopeRegistry fixes this by giving each caller-supplied
// identity.ClientSessionID (see internal/identity/identity.go, minted by
// attachclient.newSessionID as of this same phase) its OWN SessionScope
// (Workspace/Conversation), keyed independently of client.Client's shared
// field. This registry MUST NOT live inside client.Client — it is
// deliberately serve-package-local, per-RPC-session state that must never
// leak into client.Client's single shared instance (client.Client remains
// engine-owned, session-agnostic; see
// docs/architecture/swarm-attach/adr-001-engine-ownership.md).
//
// client_methods.go's handleSetWorkspace consults this registry when the
// caller supplies a non-empty client_session_id RPC param (the exact JSON
// tag spelling mandated by CONTRACT.md section 3, matching
// internal/journal/record.go:71's Record.ClientSessionID tag) instead of
// mutating the shared *client.Client. Callers that do not yet supply
// client_session_id keep exercising today's global-field behavior
// unchanged (see the compatibility fallback comment in
// handleSetWorkspace).
package serve

import (
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/identity"
)

// SessionScope holds the workspace/conversation selection for exactly one
// identity.ClientSessionID. Zero value is a valid "nothing selected yet"
// scope.
type SessionScope struct {
	Workspace    string
	Conversation string
}

// sessionScopeRegistry is a concurrency-safe map from
// identity.ClientSessionID to that session's own *SessionScope. All
// methods are safe for concurrent use by multiple goroutines, which is the
// exact property this phase's real bug (two attached clients racing on
// client.Client's single shared `workspace` field) requires: two distinct
// ClientSessionIDs calling Set/Get/Delete concurrently must never observe
// or mutate each other's scope.
type sessionScopeRegistry struct {
	mu     sync.RWMutex
	scopes map[identity.ClientSessionID]*SessionScope
}

// newSessionScopeRegistry constructs an empty registry.
func newSessionScopeRegistry() *sessionScopeRegistry {
	return &sessionScopeRegistry{
		scopes: make(map[identity.ClientSessionID]*SessionScope),
	}
}

// Get returns a COPY of id's current scope and whether one exists yet. A
// copy (not a pointer into the map) is returned deliberately so callers
// can never mutate another session's scope through an aliased pointer —
// every mutation must go through Set.
func (r *sessionScopeRegistry) Get(id identity.ClientSessionID) (SessionScope, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.scopes[id]
	if !ok {
		return SessionScope{}, false
	}
	return *s, true
}

// SetWorkspace records dir as id's session-scoped workspace selection,
// creating id's scope entry on first use. It never touches any other
// session's entry in the map.
func (r *sessionScopeRegistry) SetWorkspace(id identity.ClientSessionID, dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.scopes[id]
	if !ok {
		s = &SessionScope{}
		r.scopes[id] = s
	}
	s.Workspace = dir
}

// SetConversation records convID as id's session-scoped active
// conversation selection, creating id's scope entry on first use. It
// never touches any other session's entry in the map.
func (r *sessionScopeRegistry) SetConversation(id identity.ClientSessionID, convID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.scopes[id]
	if !ok {
		s = &SessionScope{}
		r.scopes[id] = s
	}
	s.Conversation = convID
}

// Set replaces id's entire scope wholesale (both Workspace and
// Conversation) in one call. Provided for callers/tests that want to seed
// or overwrite a complete scope atomically rather than field-by-field.
func (r *sessionScopeRegistry) Set(id identity.ClientSessionID, scope SessionScope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := scope
	r.scopes[id] = &cp
}

// Delete removes id's scope entirely (e.g. on session disconnect). It is
// safe to call Delete for an id with no existing entry (a no-op).
//
// NOTE (scope of this phase): no caller wires Delete to an actual
// disconnect-detection signal yet — as of this phase, neither
// serve/mux.go nor serve/client_methods.go has any existing mechanism
// that detects an attachclient session ending (no such wiring exists to
// hook into today; inventing new disconnect-detection plumbing is out of
// scope for P07.B per its brief, which explicitly says "it is fine to
// leave Delete unwired by a caller this phase and just implement+test the
// method"). Delete is implemented and tested here so a future phase that
// DOES add disconnect detection has a ready, correct method to call.
func (r *sessionScopeRegistry) Delete(id identity.ClientSessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.scopes, id)
}

// Len reports the number of sessions currently tracked. Test-observability
// helper only.
func (r *sessionScopeRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.scopes)
}

// globalSessionScopes is the one process-wide session-scope registry
// consulted by client_methods.go's handleSetWorkspace (and future
// session-scoped handlers). It is package-private: nothing outside serve
// reaches into it directly, matching the "lives in the serve package"
// requirement from this phase's brief (session scoping is per-RPC-session
// dispatch state, not exported daemon API surface).
//
// A package-level singleton (rather than a field threaded through Mux) is
// used deliberately: MethodHandler's signature
// (func(ctx, *client.Client, json.RawMessage) (any, error)), defined in
// method.go, is shared across every registered method and is out of
// scope for this worker to change (method.go is not in P07.B's # FILES:
// list) — so handlers reach session-scoped state via this package-level
// registry rather than an added parameter.
var globalSessionScopes = newSessionScopeRegistry()
