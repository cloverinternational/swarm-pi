package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestRegisterAnnoyedToolExposesToolWithConversationLoaderAndNudge(t *testing.T) {
	registry := tools.NewSimpleRegistry(nil, nil)
	hooksManager := NewHooksManager(nil, nil, t.TempDir())
	sdk := &SDKIntegration{
		toolRegistry: registry,
		hooksManager: hooksManager,
	}

	var loadedConversationID string
	loadConversation := func(_ context.Context, conversationID string) (*conversation.Conversation, error) {
		loadedConversationID = conversationID
		return &conversation.Conversation{}, nil
	}
	if err := sdk.registerAnnoyedTool(loadConversation); err != nil {
		t.Fatalf("registerAnnoyedTool: %v", err)
	}

	annoyed, err := registry.Get("annoyed")
	if err != nil {
		t.Fatalf("annoyed tool is not registered: %v", err)
	}
	if !hooksManager.IsEnabled("annoyance-nudge") {
		t.Fatal("annoyance nudge is not enabled")
	}

	ctx, cancel := context.WithCancel(tools.WithOwnerInfo(
		context.Background(), "agent", "user", "conversation-123",
	))
	cancel()
	if _, err := annoyed.Execute(ctx, map[string]any{"issue": "registration probe"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("annoyed.Execute error = %v, want context.Canceled", err)
	}
	if loadedConversationID != "conversation-123" {
		t.Fatalf("conversation loader received %q, want %q", loadedConversationID, "conversation-123")
	}
}

func TestRegisterAnnoyedToolWithoutHooksStillExposesTool(t *testing.T) {
	registry := tools.NewSimpleRegistry(nil, nil)
	sdk := &SDKIntegration{toolRegistry: registry}

	loadConversation := func(context.Context, string) (*conversation.Conversation, error) {
		return nil, nil
	}
	if err := sdk.registerAnnoyedTool(loadConversation); err != nil {
		t.Fatalf("registerAnnoyedTool: %v", err)
	}
	if !registry.IsRegistered("annoyed") {
		t.Fatal("annoyed tool is not registered")
	}
}
