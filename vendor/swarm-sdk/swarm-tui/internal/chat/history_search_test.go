package chat

import (
	"context"
	"testing"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/searchindex"
	historytools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/history"
)

func TestIndexedHistorySearchFindsBodyAndExactCount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	workspace := t.TempDir()
	client, err := sdkclient.New(
		sdkclient.WithoutAutoConfig(),
		sdkclient.WithProvider("anthropic", "claude-sonnet-4-5"),
		sdkclient.WithAPIKey("dummy"),
		sdkclient.WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	conv, err := client.CreateConversationWithOptions(ctx, manager.CreateOptions{
		Mode: "act", WorkspacePath: workspace,
		Metadata: &conversation.ConversationMetadata{Tags: []string{"tui"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mgr := client.ConversationManager()
	if err := mgr.AddMessage(ctx, conv.ID, &conversation.Message{ID: "u", Role: conversation.RoleUser, Content: "Build the report studio"}); err != nil {
		t.Fatalf("add user: %v", err)
	}
	if err := mgr.AddMessage(ctx, conv.ID, &conversation.Message{ID: "a", Role: conversation.RoleAssistant, Content: "The orchestrator pipeline is ready"}); err != nil {
		t.Fatalf("add assistant: %v", err)
	}

	results, err := indexedHistorySearch(ctx, client, historytools.SearchRequest{
		Query: "orchestrator", WorkspacePath: workspace, SearchBody: true,
		Sort: "relevance", Order: "desc", Limit: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].ID != conv.ID {
		t.Fatalf("results = %#v", results)
	}
	if results[0].MessageCount != 2 || results[0].Preview != "Build the report studio" || !results[0].SearchMatched {
		t.Fatalf("result = %#v", results[0])
	}
}

func TestIndexedHistorySearchFindsFailedToolResultOnlyText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	workspace := t.TempDir()
	client, err := sdkclient.New(
		sdkclient.WithoutAutoConfig(),
		sdkclient.WithProvider("anthropic", "claude-sonnet-4-5"),
		sdkclient.WithAPIKey("dummy"),
		sdkclient.WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	conv, err := client.CreateConversationWithOptions(ctx, manager.CreateOptions{
		Mode: "act", WorkspacePath: workspace,
		Metadata: &conversation.ConversationMetadata{Tags: []string{"tui"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := client.ConversationManager().AddMessage(ctx, conv.ID, &conversation.Message{
		ID: "assistant", Role: conversation.RoleAssistant, Content: "The command completed.",
		ToolResults: []conversation.ToolResult{{
			Name: "Bash", CallID: "call-failed",
			Output: "statisticalfailuretoken returned by stderr",
			Error:  &conversation.ToolError{Type: "exit_status", Message: "exit status 1"},
		}},
	}); err != nil {
		t.Fatalf("add assistant: %v", err)
	}

	results, err := indexedHistorySearch(ctx, client, historytools.SearchRequest{
		Query: "statisticalfailuretoken", WorkspacePath: workspace,
		SearchBody: true, ToolOutcome: searchindex.OutcomeFailed,
		SegmentKind: searchindex.SegmentToolResult,
		Sort:        "relevance", Order: "desc", Limit: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].ID != conv.ID {
		t.Fatalf("results = %#v", results)
	}
	if !results[0].SearchMatched {
		t.Fatalf("result did not report segment match: %#v", results[0])
	}
}
