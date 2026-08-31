package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSearchOutputIsCompactAndOmitsRedundantFields(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewSearchTool(workspace, func(context.Context, string, string) ([]Summary, error) {
		return []Summary{{
			ID:            "conv-1",
			Title:         "Auth work",
			Preview:       "Auth work", // duplicate title: omit without information loss
			MessageCount:  0,           // zero-value metadata: omit
			UpdatedAt:     time.Date(2026, 7, 19, 12, 34, 56, 789, time.UTC),
			WorkspacePath: workspace,
		}}, nil
	})

	result, err := tool.Execute(context.Background(), map[string]any{"query": "auth"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, "\n") || strings.Contains(result.Output, "  ") {
		t.Fatalf("HistorySearch output must be compact JSON: %q", result.Output)
	}
	var payload struct {
		WorkspacePath string `json:"workspace_path"`
		Results       []map[string]any
	}
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.WorkspacePath != workspace || len(payload.Results) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	row := payload.Results[0]
	if _, ok := row["workspace_path"]; ok {
		t.Fatal("current-scope row repeated top-level workspace_path")
	}
	if _, ok := row["preview"]; ok {
		t.Fatal("row repeated title as an identical preview")
	}
	if _, ok := row["message_count"]; ok {
		t.Fatal("row emitted zero-value message_count")
	}
	if row["updated_at"] != "2026-07-19T12:34:56Z" {
		t.Fatalf("timestamp = %v, want compact RFC3339", row["updated_at"])
	}
}

func TestSearchAllScopeKeepsPerResultWorkspaceProvenance(t *testing.T) {
	current := filepath.Join(t.TempDir(), "current")
	other := filepath.Join(filepath.Dir(current), "other")
	tool := NewSearchTool(current, func(context.Context, string, string) ([]Summary, error) {
		return []Summary{{ID: "x", Title: "Result", WorkspacePath: other}}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"scope": "all"})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Results []struct {
			WorkspacePath string `json:"workspace_path"`
		} `json:"results"`
	}
	decodeResult(t, result, &payload)
	if len(payload.Results) != 1 || payload.Results[0].WorkspacePath != other {
		t.Fatalf("all-scope provenance was lost: %s", result.Output)
	}
}
