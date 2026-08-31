package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func decodeHistoryOutput(t *testing.T, output string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output)
	}
	return decoded
}

func TestSearchOptionsBodyFiltersAndMessageCountSort(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	var got SearchRequest
	tool := NewSearchToolWithOptions(workspace, func(_ context.Context, request SearchRequest) ([]Summary, error) {
		got = request
		return []Summary{
			{ID: "small", Preview: "unrelated", Body: "report studio", MessageCount: 4, Origin: "interactive", WorkspacePath: workspace, UpdatedAt: time.Unix(2, 0)},
			{ID: "large", Preview: "report studio", MessageCount: 642, Origin: "interactive", WorkspacePath: workspace, UpdatedAt: time.Unix(1, 0)},
			{ID: "agent", Preview: "report studio", MessageCount: 900, Origin: "subagent", WorkspacePath: workspace, UpdatedAt: time.Unix(3, 0)},
		}, nil
	})

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "report studio", "sort": "message_count", "min_messages": 5,
		"origin": "interactive", "search_body": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !got.SearchBody || got.Sort != "message_count" || got.MinMessages != 5 || got.Origin != "interactive" {
		t.Fatalf("backend request = %+v", got)
	}
	decoded := decodeHistoryOutput(t, result.Output)
	rows := decoded["results"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != "large" {
		t.Fatalf("results = %#v", rows)
	}
	if rows[0].(map[string]any)["message_count"] != float64(642) {
		t.Fatalf("message count missing/wrong: %#v", rows[0])
	}
}

func TestSearchBodyCanBeDisabled(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewSearchToolWithOptions(workspace, func(_ context.Context, request SearchRequest) ([]Summary, error) {
		return []Summary{{ID: "body-only", Preview: "other", Body: "orchestrator", WorkspacePath: workspace}}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": "orchestrator", "search_body": false})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if rows := decodeHistoryOutput(t, result.Output)["results"].([]any); len(rows) != 0 {
		t.Fatalf("body matched with search_body=false: %#v", rows)
	}
}

func TestGetOffsetTailHumanOnlyAndEmptyProvenance(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	conv := &conversation.Conversation{
		ID: "conv", WorkspacePath: workspace,
		Messages: []*conversation.Message{
			{ID: "u1", Role: conversation.RoleUser, Content: "first"},
			{ID: "tool", Role: conversation.RoleTool, Content: "machine result"},
			{ID: "a1", Role: conversation.RoleAssistant, Content: "answer"},
			{ID: "u2", Role: conversation.RoleUser, Content: "last"},
		},
	}
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) { return conv, nil })

	offsetResult, err := tool.Execute(context.Background(), map[string]any{
		"conversation_id": "conv", "offset": 1, "max_messages": 2, "human_only": true,
	})
	if err != nil {
		t.Fatalf("offset Execute: %v", err)
	}
	offset := decodeHistoryOutput(t, offsetResult.Output)
	if offset["total_message_count"] != float64(4) || offset["window_start"] != float64(1) || offset["window_end"] != float64(3) {
		t.Fatalf("offset provenance = %#v", offset)
	}
	rows := offset["messages"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != "a1" || offset["filtered_message_count"] != float64(1) {
		t.Fatalf("human-only rows = %#v, output=%#v", rows, offset)
	}

	tailResult, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "conv", "tail": 2})
	if err != nil {
		t.Fatalf("tail Execute: %v", err)
	}
	tailRows := decodeHistoryOutput(t, tailResult.Output)["messages"].([]any)
	if len(tailRows) != 2 || tailRows[0].(map[string]any)["id"] != "a1" || tailRows[1].(map[string]any)["id"] != "u2" {
		t.Fatalf("tail rows = %#v", tailRows)
	}

	emptyTool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "empty", WorkspacePath: workspace}, nil
	})
	emptyResult, err := emptyTool.Execute(context.Background(), map[string]any{"conversation_id": "empty"})
	if err != nil {
		t.Fatalf("empty Execute: %v", err)
	}
	empty := decodeHistoryOutput(t, emptyResult.Output)
	if empty["empty"] != true || empty["total_message_count"] != float64(0) || empty["rendered_message_count"] != float64(0) {
		t.Fatalf("empty provenance = %#v", empty)
	}
}

func TestGetRejectsTailAndOffsetTogether(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "conv", WorkspacePath: workspace}, nil
	})
	if _, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "conv", "tail": 2, "offset": 0}); err == nil {
		t.Fatal("expected tail/offset validation error")
	}
}
