// Package client — list_exclude_headless_test.go
//
// Verifies that headless (`swarm -p`) conversations, tagged
// conversation.HeadlessTag, are excluded from ListConversationsMeta (the single
// choke point for both the TUI conversation picker and the agent HistorySearch
// tool) while remaining reachable by direct ID lookup.
package client

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
)

func TestListConversationsMeta_ExcludesHeadless(t *testing.T) {
	ctx := context.Background()
	ws := t.TempDir()

	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// A normal (interactive) conversation in the workspace.
	normal, err := c.CreateConversationWithOptions(ctx, manager.CreateOptions{
		Mode:          "act",
		WorkspacePath: ws,
		Metadata:      &conversation.ConversationMetadata{Tags: []string{"tui"}},
	})
	if err != nil {
		t.Fatalf("create normal: %v", err)
	}

	// A headless conversation in the SAME workspace.
	headless, err := c.CreateConversationWithOptions(ctx, manager.CreateOptions{
		Mode:          "act",
		WorkspacePath: ws,
		Metadata:      &conversation.ConversationMetadata{Tags: []string{conversation.HeadlessTag}},
	})
	if err != nil {
		t.Fatalf("create headless: %v", err)
	}

	metas, err := c.ListConversationsMeta(ctx, ws)
	if err != nil {
		t.Fatalf("ListConversationsMeta: %v", err)
	}
	seen := map[string]bool{}
	for _, m := range metas {
		seen[m.ID] = true
	}
	if !seen[normal.ID] {
		t.Errorf("normal conversation %s should appear in listing", normal.ID)
	}
	if seen[headless.ID] {
		t.Errorf("headless conversation %s must NOT appear in listing", headless.ID)
	}
	withHeadless, err := c.ListConversations(ctx, ListOptions{WorkspacePath: ws, IncludeHeadless: true})
	if err != nil {
		t.Fatalf("ListConversations(include headless): %v", err)
	}
	foundHeadless := false
	for _, summary := range withHeadless {
		if summary.ID == headless.ID {
			foundHeadless = true
			break
		}
	}
	if !foundHeadless {
		t.Errorf("explicit IncludeHeadless should return %s", headless.ID)
	}

	// The headless conversation is still on disk and reachable by ID.
	if _, err := c.LoadConversation(ctx, headless.ID); err != nil {
		t.Errorf("LoadConversation(headless) should still succeed: %v", err)
	}
}
