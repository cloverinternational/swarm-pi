// Package client — messaging_ensure_conv_test.go
//
// Regression tests for the "ensure an active conversation" behaviour that
// SendMessage relies on when the caller supplies no conversation id (the
// daemon / serve / thin-TUI first-message path).
//
// Before this was added, a client with no active conversation ran the first
// turn STATELESS (ChatCtx guards persistence on convID != "") and then
// back-filled ActiveConvID from the GLOBAL most-recent conversation across
// ALL workspaces (ListConversationsMeta("")). Two daemons in different
// workspaces would therefore both latch onto one unrelated conversation. The
// in-process TUI never hit this because it creates a workspace-scoped
// conversation itself (app_messaging.go); these tests move that guarantee
// into the client so every thin UI inherits it.
package client

import (
	"context"
	"testing"
)

func mustClientWS(t *testing.T, storageDir, workspace string) *Client {
	t.Helper()
	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(storageDir),
		WithWorkspace(workspace),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

// A fresh client with no active conversation must mint a NEW conversation
// scoped to its own workspace, and make it active.
func TestEnsureActiveConversation_CreatesWorkspaceScopedWhenNoneActive(t *testing.T) {
	ctx := context.Background()
	ws := t.TempDir()
	c := mustClientWS(t, t.TempDir(), ws)

	if got := c.ActiveConversation(); got != "" {
		t.Fatalf("expected no active conversation initially, got %q", got)
	}

	id, err := c.ensureActiveConversation(ctx)
	if err != nil {
		t.Fatalf("ensureActiveConversation: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty conversation id")
	}
	if got := c.ActiveConversation(); got != id {
		t.Errorf("active conv = %q, want %q", got, id)
	}

	conv, err := c.LoadConversation(ctx, id)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if conv.WorkspacePath != ws {
		t.Errorf("conversation workspace = %q, want %q", conv.WorkspacePath, ws)
	}
}

// The core regression: a fresh client in workspace A, sharing a conversation
// store with another client in workspace B (whose conversation is the globally
// most-recent), must NOT adopt B's conversation. It must create its own.
func TestEnsureActiveConversation_DoesNotAdoptForeignWorkspaceConversation(t *testing.T) {
	ctx := context.Background()
	store := t.TempDir()
	wsA, wsB := t.TempDir(), t.TempDir()

	// A conversation in workspace B — the globally most-recently-updated one.
	cB := mustClientWS(t, store, wsB)
	bConv, err := cB.NewConversation(ctx)
	if err != nil {
		t.Fatalf("seed workspace B conversation: %v", err)
	}

	// A fresh client in workspace A sharing the same store.
	cA := mustClientWS(t, store, wsA)
	id, err := cA.ensureActiveConversation(ctx)
	if err != nil {
		t.Fatalf("ensureActiveConversation: %v", err)
	}
	if id == bConv.ID {
		t.Fatalf("client A adopted workspace B's conversation %q (cross-workspace leak)", id)
	}

	conv, err := cA.LoadConversation(ctx, id)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if conv.WorkspacePath != wsA {
		t.Errorf("new conversation workspace = %q, want %q (must be scoped to client A)", conv.WorkspacePath, wsA)
	}
}

// When a conversation is already active, ensureActiveConversation must return
// it unchanged (no churn, idempotent).
func TestEnsureActiveConversation_ReturnsExistingActive(t *testing.T) {
	ctx := context.Background()
	c := mustClientWS(t, t.TempDir(), t.TempDir())

	first, err := c.ensureActiveConversation(ctx)
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	second, err := c.ensureActiveConversation(ctx)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if first != second {
		t.Errorf("ensure not idempotent: first=%q second=%q", first, second)
	}
}
