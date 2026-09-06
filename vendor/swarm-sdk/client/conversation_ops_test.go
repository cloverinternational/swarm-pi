// Package client — conversation_ops_test.go
//
// Tests for the conversation operation helpers (SetConversationTitle, etc.)
package client

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
)

// TestSetConversationTitle_SetsAndPersistsTitle verifies that
// SetConversationTitle loads a conversation, sets its Title, and saves it back.
func TestSetConversationTitle_SetsAndPersistsTitle(t *testing.T) {
	ctx := context.Background()

	// Create a real client with a temp storage directory.
	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// Create a conversation.
	conv, err := c.NewConversation(ctx)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	if conv.Title != "" {
		t.Fatalf("expected empty initial title, got %q", conv.Title)
	}

	// Set the title.
	newTitle := "My awesome title"
	if err := c.SetConversationTitle(ctx, conv.ID, newTitle); err != nil {
		t.Fatalf("SetConversationTitle: %v", err)
	}

	// Load the conversation back and verify.
	loaded, err := c.LoadConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if loaded.Title != newTitle {
		t.Errorf("title mismatch: got %q, want %q", loaded.Title, newTitle)
	}
}

// TestSetConversationTitle_OverridesExistingTitle verifies that
// SetConversationTitle can overwrite an existing title.
func TestSetConversationTitle_OverridesExistingTitle(t *testing.T) {
	ctx := context.Background()

	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	conv, err := c.NewConversation(ctx)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	// First title.
	if err := c.SetConversationTitle(ctx, conv.ID, "First title"); err != nil {
		t.Fatalf("SetConversationTitle (first): %v", err)
	}

	// Override with second title.
	if err := c.SetConversationTitle(ctx, conv.ID, "Second title"); err != nil {
		t.Fatalf("SetConversationTitle (second): %v", err)
	}

	loaded, err := c.LoadConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if loaded.Title != "Second title" {
		t.Errorf("title mismatch: got %q, want %q", loaded.Title, "Second title")
	}
}

// TestSetConversationTitle_NilManagerReturnsError verifies that
// SetConversationTitle returns an error when the client has no conversation manager.
func TestSetConversationTitle_NilManagerReturnsError(t *testing.T) {
	ctx := context.Background()
	c := &Client{} // no convManager

	err := c.SetConversationTitle(ctx, "some-id", "title")
	if err == nil {
		t.Fatal("expected error for nil manager, got nil")
	}
	if err != errNoConvManager {
		t.Fatalf("expected errNoConvManager, got %v", err)
	}
}

// TestSetConversationTitle_NonExistentConversationReturnsError verifies that
// SetConversationTitle returns an error when the conversation does not exist.
func TestSetConversationTitle_NonExistentConversationReturnsError(t *testing.T) {
	ctx := context.Background()

	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	err = c.SetConversationTitle(ctx, "non-existent-id", "title")
	if err == nil {
		t.Fatal("expected error for non-existent conversation, got nil")
	}
}

// TestSetConversationTitle_MatchesStorageDirectly verifies that the title
// is written through the same storage backend as direct Save/Load.
func TestSetConversationTitle_MatchesStorageDirectly(t *testing.T) {
	ctx := context.Background()

	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// Create via manager.
	mgr := c.ConversationManager()
	conv, err := mgr.Create(ctx, manager.CreateOptions{
		Mode: "chat",
	})
	if err != nil {
		t.Fatalf("mgr.Create: %v", err)
	}

	// Use the SDK client convenience method.
	if err := c.SetConversationTitle(ctx, conv.ID, "Via SDK"); err != nil {
		t.Fatalf("SetConversationTitle: %v", err)
	}

	// Load via direct storage to prove it hit the same backend.
	store := c.Storage()
	if store == nil {
		t.Fatal("storage is nil")
	}
	loaded, err := store.Load(ctx, conv.ID)
	if err != nil {
		t.Fatalf("storage.Load: %v", err)
	}
	if loaded.Title != "Via SDK" {
		t.Errorf("title mismatch: got %q, want %q", loaded.Title, "Via SDK")
	}
}

// TestConversationTitle_WithDefaultNewChatPlaceholder verifies that a
// conversation created with the "New Chat" placeholder title can still be
// updated by SetConversationTitle.
func TestConversationTitle_WithDefaultNewChatPlaceholder(t *testing.T) {
	ctx := context.Background()

	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// Create a conversation with the "New Chat" placeholder title.
	mgr := c.ConversationManager()
	conv, err := mgr.Create(ctx, manager.CreateOptions{
		Mode: "chat",
	})
	if err != nil {
		t.Fatalf("mgr.Create: %v", err)
	}
	conv.Title = "New Chat"
	if err := mgr.Save(ctx, conv); err != nil {
		t.Fatalf("mgr.Save: %v", err)
	}

	// Now use SetConversationTitle to override the placeholder.
	if err := c.SetConversationTitle(ctx, conv.ID, "Real Title"); err != nil {
		t.Fatalf("SetConversationTitle: %v", err)
	}

	// Verify it was updated.
	loaded, err := c.LoadConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if loaded.Title != "Real Title" {
		t.Errorf("title mismatch: got %q, want %q", loaded.Title, "Real Title")
	}
}

func TestPersistCompactedGenerationAdvancesDurableClientContext(t *testing.T) {
	ctx := context.Background()
	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	conv, err := c.NewConversation(ctx)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	original := &conversation.Message{
		ID:      "original",
		Role:    conversation.RoleUser,
		Content: "original context",
	}
	if err := c.AddMessageToConversation(ctx, conv.ID, original); err != nil {
		t.Fatalf("AddMessageToConversation: %v", err)
	}
	compacted := []*conversation.Message{{
		ID:      "summary",
		Role:    conversation.RoleUser,
		Content: "compacted context",
	}}

	if err := c.persistCompactedGeneration(ctx, conv.ID, compacted, compacted[0].Content, 321); err != nil {
		t.Fatalf("persistCompactedGeneration: %v", err)
	}
	reloaded, err := c.ResumeConversation(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ResumeConversation: %v", err)
	}
	active := reloaded.ActiveMessages()
	if len(active) != 1 || active[0].ID != compacted[0].ID {
		t.Fatalf("active context = %#v, want compacted generation", active)
	}
	if generated, _ := active[0].Metadata[conversation.CompactionGeneratedMetadataKey].(bool); !generated {
		t.Fatalf("compacted metadata = %#v, want generated marker", active[0].Metadata)
	}
	if compacted[0].Metadata != nil {
		t.Fatalf("client persistence mutated agent-owned message: %#v", compacted[0].Metadata)
	}
	if len(reloaded.Messages) != 2 || reloaded.Messages[0].ID != original.ID {
		t.Fatalf("durable transcript = %#v, want original plus compacted generation", reloaded.Messages)
	}
	history, err := c.conversationHistoryForExecution(ctx, conv.ID)
	if err != nil {
		t.Fatalf("conversationHistoryForExecution: %v", err)
	}
	if len(history) != 1 || history[0].ID != compacted[0].ID {
		t.Fatalf("next execution history = %#v, want active compacted generation only", history)
	}
}

func TestChatCtxReturnsConversationHistoryLoadFailure(t *testing.T) {
	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	_, err = c.ChatCtx(context.Background(), "missing-conversation", "do not execute")
	if err == nil || !strings.Contains(err.Error(), "load conversation history") {
		t.Fatalf("ChatCtx error = %v, want conversation history load failure", err)
	}
}
