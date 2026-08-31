package history

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestGetAllWorkspacesReadsAcrossDirectories verifies that all_workspaces=true lets
// an agent read a conversation by ID regardless of which workspace it belongs to,
// without having to know or pass the conversation's workspace path.
func TestGetAllWorkspacesReadsAcrossDirectories(t *testing.T) {
	current := filepath.Join(t.TempDir(), "current")
	other := filepath.Join(filepath.Dir(current), "other-project")
	tool := NewGetTool(current, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: other, Messages: []*conversation.Message{
			{ID: "m1", Role: conversation.RoleUser, Content: "hello"},
		}}, nil
	})

	// Default (no opt-in): a cross-workspace conversation must be rejected.
	if _, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x"}); err == nil {
		t.Fatal("expected default cross-workspace read to be rejected")
	}

	// Opt-in: reads regardless of directory and reports the conv's real workspace.
	result, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x", "all_workspaces": true})
	if err != nil {
		t.Fatalf("all_workspaces read failed: %v", err)
	}
	var output struct {
		WorkspacePath string           `json:"workspace_path"`
		Messages      []map[string]any `json:"messages"`
	}
	decodeResult(t, result, &output)
	if output.WorkspacePath != other {
		t.Fatalf("workspace_path = %q, want %q", output.WorkspacePath, other)
	}
	if len(output.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(output.Messages))
	}
}

// TestGetAllWorkspacesRejectsNonBool guards the parameter type.
func TestGetAllWorkspacesRejectsNonBool(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: workspace}, nil
	})
	if _, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x", "all_workspaces": "yes"}); err == nil {
		t.Fatal("expected all_workspaces type error")
	}
}
