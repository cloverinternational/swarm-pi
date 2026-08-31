package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/identity"
)

// ─── sessionScopeRegistry unit tests ───────────────────────────────────────

// TestSessionScopeRegistry_TwoSessionsIsolated is the REAL two-session
// isolation test this phase's brief requires: two independent
// identity.ClientSessionID values hammering Set/Get concurrently under
// `go test -race` must never observe or clobber each other's workspace
// value. This directly exercises the fix for the bug described in
// CONTRACT.md ("two attached clients selecting different workspaces
// concurrently race on client.Client's single shared `workspace` field").
func TestSessionScopeRegistry_TwoSessionsIsolated(t *testing.T) {
	reg := newSessionScopeRegistry()

	sidA := identity.NewClientSessionID()
	sidB := identity.NewClientSessionID()
	if sidA == sidB {
		t.Fatal("sanity: two freshly minted ClientSessionIDs must differ")
	}

	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine A: hammers sidA's workspace with a distinctive, easily
	// recognized value pattern; after each write it re-reads and asserts
	// the value it observes is one IT wrote (never sidB's pattern).
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			want := fmt.Sprintf("/workspace/A/%d", i)
			reg.SetWorkspace(sidA, want)
			got, ok := reg.Get(sidA)
			if !ok {
				t.Errorf("sidA: expected a scope to exist after Set")
				return
			}
			// got.Workspace may lag "want" if goroutine A raced ahead of
			// itself across iterations, but it must ALWAYS be an /A/
			// value never a /B/ value — that is the actual isolation
			// property under test, not exact interleave ordering.
			if got.Workspace != "" && !hasPrefixA(got.Workspace) {
				t.Errorf("sidA observed a non-A workspace value: %q (cross-contamination from sidB)", got.Workspace)
				return
			}
		}
	}()

	// Goroutine B: symmetric hammering of sidB.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			want := fmt.Sprintf("/workspace/B/%d", i)
			reg.SetWorkspace(sidB, want)
			got, ok := reg.Get(sidB)
			if !ok {
				t.Errorf("sidB: expected a scope to exist after Set")
				return
			}
			if got.Workspace != "" && !hasPrefixB(got.Workspace) {
				t.Errorf("sidB observed a non-B workspace value: %q (cross-contamination from sidA)", got.Workspace)
				return
			}
		}
	}()

	wg.Wait()

	finalA, ok := reg.Get(sidA)
	if !ok || !hasPrefixA(finalA.Workspace) {
		t.Fatalf("sidA final workspace = %+v, want an /workspace/A/* value", finalA)
	}
	finalB, ok := reg.Get(sidB)
	if !ok || !hasPrefixB(finalB.Workspace) {
		t.Fatalf("sidB final workspace = %+v, want an /workspace/B/* value", finalB)
	}
	if finalA.Workspace == finalB.Workspace {
		t.Fatalf("sidA and sidB converged on the SAME workspace value %q — isolation broken", finalA.Workspace)
	}
	if reg.Len() != 2 {
		t.Fatalf("registry Len() = %d, want 2 (one entry per session)", reg.Len())
	}
}

func hasPrefixA(s string) bool { return len(s) >= 11 && s[:11] == "/workspace/" && s[11] == 'A' }
func hasPrefixB(s string) bool { return len(s) >= 11 && s[:11] == "/workspace/" && s[11] == 'B' }

// TestSessionScopeRegistry_DeleteRemovesOnlyTargetSession proves Delete
// removes exactly one session's scope and leaves an unrelated session's
// scope untouched.
func TestSessionScopeRegistry_DeleteRemovesOnlyTargetSession(t *testing.T) {
	reg := newSessionScopeRegistry()
	sidA := identity.NewClientSessionID()
	sidB := identity.NewClientSessionID()

	reg.SetWorkspace(sidA, "/ws/a")
	reg.SetWorkspace(sidB, "/ws/b")

	reg.Delete(sidA)

	if _, ok := reg.Get(sidA); ok {
		t.Error("sidA scope should be gone after Delete")
	}
	gotB, ok := reg.Get(sidB)
	if !ok || gotB.Workspace != "/ws/b" {
		t.Errorf("sidB scope should be untouched by deleting sidA, got %+v, ok=%v", gotB, ok)
	}

	// Delete on an already-absent (or never-existing) id is a safe no-op.
	reg.Delete(sidA)
	reg.Delete(identity.NewClientSessionID())
}

// TestSessionScopeRegistry_SetConversationIndependentOfWorkspace proves
// Workspace and Conversation are tracked independently within one
// session's scope.
func TestSessionScopeRegistry_SetConversationIndependentOfWorkspace(t *testing.T) {
	reg := newSessionScopeRegistry()
	sid := identity.NewClientSessionID()

	reg.SetWorkspace(sid, "/ws/one")
	reg.SetConversation(sid, "conv-1")

	got, ok := reg.Get(sid)
	if !ok {
		t.Fatal("expected scope to exist")
	}
	if got.Workspace != "/ws/one" || got.Conversation != "conv-1" {
		t.Fatalf("got %+v, want Workspace=/ws/one Conversation=conv-1", got)
	}
}

// TestSessionScopeRegistry_GetReturnsIndependentCopy proves the SessionScope
// returned by Get is a copy: mutating it must never affect the registry's
// internal state (Get must not leak a pointer into the map).
func TestSessionScopeRegistry_GetReturnsIndependentCopy(t *testing.T) {
	reg := newSessionScopeRegistry()
	sid := identity.NewClientSessionID()
	reg.SetWorkspace(sid, "/ws/original")

	got, _ := reg.Get(sid)
	got.Workspace = "/ws/mutated-by-caller"

	got2, _ := reg.Get(sid)
	if got2.Workspace != "/ws/original" {
		t.Fatalf("registry state leaked through a Get() copy: got2.Workspace = %q, want unchanged /ws/original", got2.Workspace)
	}
}

// ─── handleSetWorkspace RPC-level tests ────────────────────────────────────

// TestHandleSetWorkspace_SessionScoped_TwoClientsIsolated is the
// RPC-dispatch-level counterpart of the registry unit test above: it
// proves that two attachclient sessions (represented here by two distinct
// client_session_id RPC param values dispatched through the SAME shared
// *client.Client / Mux, exactly as serve.NewMux wires ALL attached RPC
// sessions in production — see mux.go's NewMux(c *client.Client)) never
// cross-contaminate each other's selected workspace, and that the shared
// *client.Client's own workspace field is left untouched by session-scoped
// calls.
func TestHandleSetWorkspace_SessionScoped_TwoClientsIsolated(t *testing.T) {
	sharedClient := &client.Client{}
	m := NewMux(sharedClient)
	ctx := context.Background()

	sidA := identity.NewClientSessionID()
	sidB := identity.NewClientSessionID()

	dirA := "/workspace/session-a"
	dirB := "/workspace/session-b"

	rawA := json.RawMessage(`{"dir":` + strconv.Quote(dirA) + `,"client_session_id":` + strconv.Quote(sidA.String()) + `}`)
	rawB := json.RawMessage(`{"dir":` + strconv.Quote(dirB) + `,"client_session_id":` + strconv.Quote(sidB.String()) + `}`)

	if _, err := m.Dispatch(ctx, "client.setWorkspace", rawA); err != nil {
		t.Fatalf("dispatch setWorkspace for sidA: %v", err)
	}
	if _, err := m.Dispatch(ctx, "client.setWorkspace", rawB); err != nil {
		t.Fatalf("dispatch setWorkspace for sidB: %v", err)
	}

	scopeA, ok := globalSessionScopes.Get(sidA)
	if !ok || scopeA.Workspace != dirA {
		t.Fatalf("sidA scope = %+v (ok=%v), want Workspace=%q", scopeA, ok, dirA)
	}
	scopeB, ok := globalSessionScopes.Get(sidB)
	if !ok || scopeB.Workspace != dirB {
		t.Fatalf("sidB scope = %+v (ok=%v), want Workspace=%q", scopeB, ok, dirB)
	}
	if scopeA.Workspace == scopeB.Workspace {
		t.Fatalf("sidA and sidB converged on the same workspace %q — session isolation broken", scopeA.Workspace)
	}

	// The single shared *client.Client instance backing this Mux must NOT
	// have been mutated by either session-scoped call — that is the whole
	// point of the fix (package-boundaries.md:423's invariant).
	if sharedClient.WorkspaceDir() != "" {
		t.Fatalf("shared client.Client.workspace was mutated by a session-scoped setWorkspace call: got %q, want empty", sharedClient.WorkspaceDir())
	}

	globalSessionScopes.Delete(sidA)
	globalSessionScopes.Delete(sidB)
}

// TestHandleSetWorkspace_ConcurrentSessions_Race hammers handleSetWorkspace
// through Mux.Dispatch concurrently for two distinct client_session_id
// values under `go test -race`, asserting neither session's final
// observed workspace ever contains the other session's marker substring.
func TestHandleSetWorkspace_ConcurrentSessions_Race(t *testing.T) {
	sharedClient := &client.Client{}
	m := NewMux(sharedClient)
	ctx := context.Background()

	sidA := identity.NewClientSessionID()
	sidB := identity.NewClientSessionID()

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(2)

	dispatchN := func(sid identity.ClientSessionID, marker string) {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			dir := fmt.Sprintf("/workspace/%s/%d", marker, i)
			raw := json.RawMessage(`{"dir":` + strconv.Quote(dir) + `,"client_session_id":` + strconv.Quote(sid.String()) + `}`)
			if _, err := m.Dispatch(ctx, "client.setWorkspace", raw); err != nil {
				t.Errorf("dispatch setWorkspace(%s): %v", marker, err)
				return
			}
		}
	}

	go dispatchN(sidA, "A")
	go dispatchN(sidB, "B")
	wg.Wait()

	scopeA, ok := globalSessionScopes.Get(sidA)
	if !ok {
		t.Fatal("sidA scope missing after concurrent dispatch")
	}
	scopeB, ok := globalSessionScopes.Get(sidB)
	if !ok {
		t.Fatal("sidB scope missing after concurrent dispatch")
	}
	if !hasPrefixA(scopeA.Workspace) {
		t.Errorf("sidA final workspace %q does not look like an A value (contamination)", scopeA.Workspace)
	}
	if !hasPrefixB(scopeB.Workspace) {
		t.Errorf("sidB final workspace %q does not look like a B value (contamination)", scopeB.Workspace)
	}

	globalSessionScopes.Delete(sidA)
	globalSessionScopes.Delete(sidB)
}

// TestHandleSetWorkspace_LegacyFallback_EmptyClientSessionID proves the
// compatibility fallback: when client_session_id is empty/absent,
// handleSetWorkspace calls through to EXACTLY today's global
// c.SetWorkspace behavior, unchanged (matching
// serve/set_workspace_test.go's pre-existing
// TestSetWorkspace_SetsAndReturnsWorkspace, which is NOT in this worker's
// editable file list and therefore must keep passing unmodified).
func TestHandleSetWorkspace_LegacyFallback_EmptyClientSessionID(t *testing.T) {
	sharedClient := &client.Client{}
	m := NewMux(sharedClient)
	ctx := context.Background()
	dir := "/legacy/workspace/no-session-id"

	raw := json.RawMessage(`{"dir":` + strconv.Quote(dir) + `}`)
	res, err := m.Dispatch(ctx, "client.setWorkspace", raw)
	if err != nil {
		t.Fatalf("dispatch setWorkspace (legacy, no client_session_id): %v", err)
	}
	got, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res)
	}
	if got["workspace"] != dir {
		t.Errorf("workspace = %v, want %q", got["workspace"], dir)
	}
	// The legacy path must mutate the SHARED client.Client directly —
	// that is precisely "today's global-field behavior unchanged".
	if sharedClient.WorkspaceDir() != dir {
		t.Fatalf("shared client.Client.workspace = %q, want %q (legacy fallback must mutate the shared client)", sharedClient.WorkspaceDir(), dir)
	}
}

// TestHandleSetWorkspace_LegacyFallback_WhitespaceClientSessionID proves
// a whitespace-only client_session_id (not literally empty, but
// meaningless) is treated the same as absent: legacy fallback applies.
func TestHandleSetWorkspace_LegacyFallback_WhitespaceClientSessionID(t *testing.T) {
	sharedClient := &client.Client{}
	m := NewMux(sharedClient)
	ctx := context.Background()
	dir := "/legacy/workspace/whitespace-session-id"

	raw := json.RawMessage(`{"dir":` + strconv.Quote(dir) + `,"client_session_id":"   "}`)
	if _, err := m.Dispatch(ctx, "client.setWorkspace", raw); err != nil {
		t.Fatalf("dispatch setWorkspace: %v", err)
	}
	if sharedClient.WorkspaceDir() != dir {
		t.Fatalf("shared client.Client.workspace = %q, want %q", sharedClient.WorkspaceDir(), dir)
	}
}

// TestHandleSetWorkspace_MalformedClientSessionID_FallsBackToLegacy
// proves that a syntactically non-empty but INVALID client_session_id
// (one that fails identity.ParseClientSessionID) does not hard-fail the
// call — per this phase's additive/non-breaking mandate it falls through
// to the legacy global-field path exactly as if the field were absent.
func TestHandleSetWorkspace_MalformedClientSessionID_FallsBackToLegacy(t *testing.T) {
	sharedClient := &client.Client{}
	m := NewMux(sharedClient)
	ctx := context.Background()
	dir := "/legacy/workspace/malformed-session-id"

	raw := json.RawMessage(`{"dir":` + strconv.Quote(dir) + `,"client_session_id":"not-a-real-session-id"}`)
	res, err := m.Dispatch(ctx, "client.setWorkspace", raw)
	if err != nil {
		t.Fatalf("dispatch setWorkspace with malformed client_session_id: %v", err)
	}
	got, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", res)
	}
	if got["workspace"] != dir {
		t.Errorf("workspace = %v, want %q", got["workspace"], dir)
	}
	if sharedClient.WorkspaceDir() != dir {
		t.Fatalf("shared client.Client.workspace = %q, want %q (malformed client_session_id must fall back to legacy path)", sharedClient.WorkspaceDir(), dir)
	}
}
