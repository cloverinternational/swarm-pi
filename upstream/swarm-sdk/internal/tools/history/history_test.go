package history

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestSearchSupportsAllAndExactWorkspaceScopes(t *testing.T) {
	current := filepath.Join(t.TempDir(), "current")
	other := filepath.Join(filepath.Dir(current), "other")
	var selected []string
	tool := NewSearchTool(current, func(_ context.Context, _ string, workspace string) ([]Summary, error) {
		selected = append(selected, workspace)
		return []Summary{
			{ID: "current", WorkspacePath: current},
			{ID: "other", WorkspacePath: other},
		}, nil
	})

	all, err := tool.Execute(context.Background(), map[string]any{"scope": "all"})
	if err != nil {
		t.Fatal(err)
	}
	var allPayload struct {
		Scope   string `json:"scope"`
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	decodeResult(t, all, &allPayload)
	if selected[0] != "" || allPayload.Scope != "all" || len(allPayload.Results) != 2 || allPayload.Results[0].ID != "current" || allPayload.Results[1].ID != "other" {
		t.Fatalf("all-workspace search did not preserve scope and results: %s (selected %q)", all.Output, selected[0])
	}

	exact, err := tool.Execute(context.Background(), map[string]any{"workspace_path": other})
	if err != nil {
		t.Fatal(err)
	}
	var exactPayload struct {
		WorkspacePath string `json:"workspace_path"`
		Results       []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	decodeResult(t, exact, &exactPayload)
	if selected[1] != other || exactPayload.WorkspacePath != other || len(exactPayload.Results) != 1 || exactPayload.Results[0].ID != "other" {
		t.Fatalf("exact-workspace search did not constrain the backend and output: %s (selected %q)", exact.Output, selected[1])
	}
}

func TestSearchRejectsInvalidScopeCombinations(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	called := false
	tool := NewSearchTool(workspace, func(context.Context, string, string) ([]Summary, error) {
		called = true
		return nil, nil
	})
	for _, params := range []map[string]any{
		{"scope": "invalid"},
		{"scope": "all", "workspace_path": workspace},
		{"workspace_path": "relative"},
	} {
		if _, err := tool.Execute(context.Background(), params); err == nil {
			t.Fatalf("accepted invalid scope parameters: %#v", params)
		}
	}
	if called {
		t.Fatal("backend called for invalid scope parameters")
	}
}

func TestGetSupportsExactCrossWorkspacePath(t *testing.T) {
	current := filepath.Join(t.TempDir(), "current")
	other := filepath.Join(filepath.Dir(current), "other")
	tool := NewGetTool(current, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: other}, nil
	})
	if _, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x"}); err == nil {
		t.Fatal("default scope must remain the current workspace")
	}
	result, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x", "workspace_path": other})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		WorkspacePath string `json:"workspace_path"`
	}
	decodeResult(t, result, &payload)
	if payload.WorkspacePath != other {
		t.Fatalf("missing cross-workspace provenance: %s", result.Output)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x", "workspace_path": current}); err == nil {
		t.Fatal("requested workspace must exactly match the loaded conversation")
	}
}

func TestSearchScopesCanonicalWorkspaceAndLimitsResults(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	workspaceWithDots := filepath.Join(workspace, "nested", "..") + string(os.PathSeparator)
	tool := NewSearchTool(workspaceWithDots, func(context.Context, string, string) ([]Summary, error) {
		return []Summary{
			{ID: "a", Title: "Auth decision", Preview: "Use OAuth", WorkspacePath: workspace + string(os.PathSeparator), UpdatedAt: time.Unix(2, 0)},
			{ID: "b", Title: "Other", Preview: "unrelated", WorkspacePath: filepath.Join(filepath.Dir(workspace), "outside"), UpdatedAt: time.Unix(3, 0)},
			{ID: "c", Title: "Auth followup", Preview: "tokens", WorkspacePath: workspace, UpdatedAt: time.Unix(1, 0)},
			{ID: "relative", Title: "Auth relative", WorkspacePath: filepath.Join("relative", "workspace"), UpdatedAt: time.Unix(4, 0)},
		}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"query": "auth", "limit": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	decodeResult(t, result, &payload)
	if len(payload.Results) != 1 || payload.Results[0].ID != "a" {
		t.Fatalf("unexpected scoped search: %s", result.Output)
	}
}

func TestWorkspaceCanonicalizationPreservesSignificantWhitespace(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace ")
	withoutSpace := strings.TrimSuffix(workspace, " ")
	search := NewSearchTool(workspace, func(context.Context, string, string) ([]Summary, error) {
		return []Summary{
			{ID: "exact", WorkspacePath: workspace},
			{ID: "trimmed", WorkspacePath: withoutSpace},
		}, nil
	})
	result, err := search.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	decodeResult(t, result, &payload)
	if len(payload.Results) != 1 || payload.Results[0].ID != "exact" {
		t.Fatalf("workspace whitespace was not compared exactly: %s", result.Output)
	}

	get := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "trimmed", WorkspacePath: withoutSpace}, nil
	})
	if _, err := get.Execute(context.Background(), map[string]any{"conversation_id": "trimmed"}); err == nil {
		t.Fatal("HistoryGet should not collapse workspace paths with significant whitespace")
	}
}

func TestHistoryToolsFailClosedForInvalidCurrentWorkspace(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
	}{
		{name: "empty", workspace: ""},
		{name: "whitespace", workspace: "  "},
		{name: "relative", workspace: filepath.Join("relative", "workspace")},
		{name: "root", workspace: string(os.PathSeparator)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			searchCalled := false
			search := NewSearchTool(test.workspace, func(context.Context, string, string) ([]Summary, error) {
				searchCalled = true
				return nil, nil
			})
			if _, err := search.Execute(context.Background(), nil); err == nil {
				t.Fatal("HistorySearch should reject an invalid current workspace")
			}
			if searchCalled {
				t.Fatal("HistorySearch called its backend before validating workspace scope")
			}

			getCalled := false
			get := NewGetTool(test.workspace, func(context.Context, string) (*conversation.Conversation, error) {
				getCalled = true
				return nil, nil
			})
			if _, err := get.Execute(context.Background(), map[string]any{"conversation_id": "x"}); err == nil {
				t.Fatal("HistoryGet should reject an invalid current workspace")
			}
			if getCalled {
				t.Fatal("HistoryGet called its backend before validating workspace scope")
			}
		})
	}
}

func TestWorkspaceSymlinkAliasesRemainDistinct(t *testing.T) {
	parent := t.TempDir()
	realWorkspace := filepath.Join(parent, "real")
	if err := os.Mkdir(realWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasWorkspace := filepath.Join(parent, "alias")
	if err := os.Symlink(realWorkspace, aliasWorkspace); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	search := NewSearchTool(realWorkspace, func(context.Context, string, string) ([]Summary, error) {
		return []Summary{
			{ID: "real", WorkspacePath: realWorkspace},
			{ID: "alias", WorkspacePath: aliasWorkspace},
		}, nil
	})
	result, err := search.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	decodeResult(t, result, &payload)
	if len(payload.Results) != 1 || payload.Results[0].ID != "real" {
		t.Fatalf("symlink alias unexpectedly widened search scope: %s", result.Output)
	}

	get := NewGetTool(realWorkspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "alias", WorkspacePath: aliasWorkspace}, nil
	})
	if _, err := get.Execute(context.Background(), map[string]any{"conversation_id": "alias"}); err == nil {
		t.Fatal("HistoryGet should reject a conversation recorded under a different symlink alias")
	}
}

func TestGetRejectsInvalidOrCrossWorkspace(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "a")
	invalid := []struct {
		name string
		path string
	}{
		{name: "cross workspace", path: filepath.Join(filepath.Dir(workspace), "b")},
		{name: "empty", path: ""},
		{name: "relative", path: filepath.Join("relative", "workspace")},
		{name: "root", path: string(os.PathSeparator)},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
				return &conversation.Conversation{ID: "x", WorkspacePath: test.path}, nil
			})
			if _, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x"}); err == nil {
				t.Fatalf("expected workspace access error for %q", test.path)
			}
		})
	}
}

func TestGetPreservesNewestMessagesBeforeApplyingCharacterLimit(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: workspace, Messages: []*conversation.Message{
			{ID: strings.Repeat("old", 400), Role: conversation.RoleUser, Content: strings.Repeat("o", 100)},
			{ID: "new", Role: conversation.RoleAssistant, Content: "newest-turn"},
		}}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x", "max_messages": 2.0, "max_chars": 600.0})
	if err != nil {
		t.Fatal(err)
	}

	var output struct {
		Messages []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"messages"`
		Truncated           bool `json:"truncated"`
		ContentTruncated    bool `json:"content_truncated"`
		OmittedMessageCount int  `json:"omitted_message_count"`
	}
	decodeResult(t, result, &output)
	if len(output.Messages) != 1 || output.Messages[0].ID != "new" {
		t.Fatalf("newest message was not prioritized within encoded budget: %+v", output.Messages)
	}
	if output.Messages[0].Content != "newest-turn" {
		t.Fatalf("newest message was not preserved: %+v", output.Messages)
	}
	if !output.Truncated || !output.ContentTruncated || output.OmittedMessageCount != 1 {
		t.Fatalf("dishonest truncation metadata: %+v", output)
	}
}

func TestGetUsesTinyCharacterBudgetOnNewestMessageFirst(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: workspace, Messages: []*conversation.Message{
			{ID: "old", Content: "older"},
			{ID: "new", Content: "éclair"},
		}}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x", "max_chars": 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Output) > 1 {
		t.Fatalf("max_chars=1 produced %d bytes: %q", len(result.Output), result.Output)
	}
	if !json.Valid([]byte(result.Output)) {
		t.Fatalf("tiny bounded output is not valid JSON: %q", result.Output)
	}
}

func TestGetReportsOmittedMessages(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: workspace, Messages: []*conversation.Message{
			{ID: "one", Content: "one"},
			{ID: "two", Content: "two"},
			{ID: "three", Content: "three"},
		}}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x", "max_messages": 2})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Messages            []map[string]any `json:"messages"`
		Truncated           bool             `json:"truncated"`
		ContentTruncated    bool             `json:"content_truncated"`
		OmittedMessageCount int              `json:"omitted_message_count"`
	}
	decodeResult(t, result, &output)
	if len(output.Messages) != 2 || !output.Truncated || output.ContentTruncated || output.OmittedMessageCount != 1 {
		t.Fatalf("unexpected omission metadata: %+v", output)
	}
}

func TestGetIncludesSanitizedStructuredToolTraffic(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: workspace, Messages: []*conversation.Message{
			{
				ID:   "call",
				Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{{
					ID: "call-1", Name: "Bash",
					Parameters: map[string]any{"command": "cat package-lock.json", "api_key": "super-secret"},
				}},
			},
			{
				ID:   "result",
				Role: conversation.RoleTool,
				ToolResults: []conversation.ToolResult{{
					CallID: "call-1", Name: "Bash", Output: "package-lock.json\nAuthorization: Bearer abc123",
					Content: []conversation.ContentBlock{
						{Type: "text", Text: "visible"},
						{Type: "image", MimeType: "image/png", Data: []byte{1, 2, 3}},
					},
				}},
			},
		}}, nil
	})

	result, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x"})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Messages []struct {
			ID          string           `json:"id"`
			ToolCalls   []map[string]any `json:"tool_calls"`
			ToolResults []map[string]any `json:"tool_results"`
		} `json:"messages"`
	}
	decodeResult(t, result, &output)
	if len(output.Messages) != 2 || output.Messages[0].ID != "call" || output.Messages[1].ID != "result" {
		t.Fatalf("tool-only messages were not preserved in order: %+v", output.Messages)
	}
	encoded := result.Output
	if !strings.Contains(encoded, `"tool_calls"`) || !strings.Contains(encoded, `"tool_results"`) || !strings.Contains(encoded, "package-lock.json") {
		t.Fatalf("structured tool traffic missing: %s", encoded)
	}
	if strings.Contains(encoded, "super-secret") || strings.Contains(encoded, "abc123") || strings.Contains(encoded, "AQID") {
		t.Fatalf("secret or binary payload leaked: %s", encoded)
	}
	if !strings.Contains(encoded, "[REDACTED]") || !strings.Contains(encoded, "[binary omitted: image/png, 3 bytes]") {
		t.Fatalf("sanitization provenance missing: %s", encoded)
	}
}

func TestGetToolTrafficCountsTowardCharacterLimit(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: workspace, Messages: []*conversation.Message{
			{ID: strings.Repeat("old", 300), ToolResults: []conversation.ToolResult{{CallID: "old-call", Output: "older-output"}}},
			{ID: "new", ToolCalls: []conversation.ToolCall{{ID: "new-call", Name: "Read", Parameters: map[string]any{"file_path": "newest.txt"}}}},
		}}, nil
	})

	result, err := tool.Execute(context.Background(), map[string]any{
		"conversation_id": "x", "max_messages": 2, "max_chars": 700,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Messages []struct {
			ID          string           `json:"id"`
			ToolCalls   []map[string]any `json:"tool_calls"`
			ToolResults []map[string]any `json:"tool_results"`
		} `json:"messages"`
		ContentTruncated bool `json:"content_truncated"`
	}
	decodeResult(t, result, &output)
	if len(output.Messages) != 1 || output.Messages[0].ID != "new" {
		t.Fatalf("unexpected message ordering: %+v", output.Messages)
	}
	if !output.ContentTruncated {
		t.Fatalf("tool payload truncation was not reported: %s", result.Output)
	}
	if strings.Contains(result.Output, "older-output") {
		t.Fatalf("older tool payload consumed budget before newest payload: %s", result.Output)
	}
}

func TestGetTinyBudgetStopsBeforeUnboundedToolRows(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	calls := make([]conversation.ToolCall, 4_000)
	for i := range calls {
		calls[i] = conversation.ToolCall{
			ID:   strings.Repeat("oversized-call-id", 100),
			Name: strings.Repeat("oversized-tool-name", 100),
			Parameters: map[string]any{
				strings.Repeat("oversized-key", 100): strings.Repeat("oversized-value", 100),
			},
		}
	}
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{
			ID:            "x",
			WorkspacePath: workspace,
			Messages: []*conversation.Message{{
				ID: "tool-only", Role: conversation.RoleAssistant, ToolCalls: calls,
			}},
		}, nil
	})

	result, err := tool.Execute(context.Background(), map[string]any{
		"conversation_id": "x", "max_chars": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Output) > 1 {
		t.Fatalf("max_chars=1 produced %d bytes: %q", len(result.Output), result.Output)
	}
	if strings.Contains(result.Output, "oversized-tool-name") {
		t.Fatalf("tool rows survived exhausted budget: %s", result.Output)
	}
	if !json.Valid([]byte(result.Output)) {
		t.Fatalf("tiny bounded output is not valid JSON: %q", result.Output)
	}
}

func TestGetBoundsCompleteResponseWithOversizedMetadataAndToolRows(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), strings.Repeat("workspace-segment", 40))
	calls := make([]conversation.ToolCall, 4_000)
	for i := range calls {
		calls[i] = conversation.ToolCall{
			ID:   strings.Repeat("call-id", 100),
			Name: strings.Repeat("tool-name", 100),
			Parameters: map[string]any{
				"payload": strings.Repeat("oversized-value", 100),
			},
		}
	}
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{
			ID:            strings.Repeat("conversation-id", 100),
			Title:         strings.Repeat("oversized-title", 100),
			WorkspacePath: workspace,
			Messages: []*conversation.Message{{
				ID: "tool-only", Role: conversation.RoleAssistant, ToolCalls: calls,
			}},
		}, nil
	})
	const maxChars = 256
	result, err := tool.Execute(context.Background(), map[string]any{
		"conversation_id": "x", "max_chars": maxChars,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Output) > maxChars {
		t.Fatalf("complete response = %d bytes, max_chars = %d", len(result.Output), maxChars)
	}
	if !json.Valid([]byte(result.Output)) {
		t.Fatalf("bounded output is not valid JSON: %q", result.Output)
	}
	if strings.Contains(result.Output, "oversized-value") {
		t.Fatalf("oversized tool rows survived the total response budget: %s", result.Output)
	}
}

func TestGetRedactsNormalizedSensitiveKeyVariants(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	secrets := []string{
		"x-api-key-secret", "api-key-secret", "private-key-secret",
		"cookie-secret", "session-secret", "client-secret-value",
	}
	parameters := map[string]any{
		"X-Api-Key": secrets[0],
		"api-key":   secrets[1],
		"nested": map[string]any{
			"privateKey":    secrets[2],
			"Cookie":        secrets[3],
			"session_id":    secrets[4],
			"client secret": secrets[5],
		},
	}
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{
			ID:            "x",
			WorkspacePath: workspace,
			Messages: []*conversation.Message{{
				ID: "call", Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{{ID: "1", Name: "HTTP", Parameters: parameters}},
			}},
		}, nil
	})
	result, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(result.Output, secret) {
			t.Fatalf("sensitive-key variant leaked %q: %s", secret, result.Output)
		}
	}
	if got := strings.Count(result.Output, "[REDACTED]"); got < len(secrets) {
		t.Fatalf("redaction markers = %d, want at least %d: %s", got, len(secrets), result.Output)
	}
}

func TestGetHumanOnlyPreservesHumanTextAndOmitsToolTraffic(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	tool := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
		return &conversation.Conversation{ID: "x", WorkspacePath: workspace, Messages: []*conversation.Message{
			{ID: "reminder", Role: conversation.RoleUser, Content: "<system-reminder>machine only</system-reminder>"},
			{ID: "human", Role: conversation.RoleUser, Content: "<system-reminder>machine</system-reminder>\nreal question"},
			{ID: "tool", Role: conversation.RoleTool, ToolResults: []conversation.ToolResult{{Output: "result"}}},
		}}, nil
	})

	result, err := tool.Execute(context.Background(), map[string]any{"conversation_id": "x", "human_only": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Output, `"id":"reminder"`) || strings.Contains(result.Output, `"id":"tool"`) {
		t.Fatalf("human_only retained machine-only messages: %s", result.Output)
	}
	if !strings.Contains(result.Output, `"id":"human"`) || !strings.Contains(result.Output, "real question") || strings.Contains(result.Output, "machine") {
		t.Fatalf("human_only did not preserve cleaned human text: %s", result.Output)
	}
}

func TestNumericParametersRejectMalformedOrOutOfRangeValues(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	invalid := []any{"10", 1.5, 0, -1, maxCharsLimit + 1, true}

	for index, value := range invalid {
		t.Run(fmt.Sprintf("%d_%T_%v", index, value, value), func(t *testing.T) {
			searchCalled := false
			search := NewSearchTool(workspace, func(context.Context, string, string) ([]Summary, error) {
				searchCalled = true
				return nil, nil
			})
			if _, err := search.Execute(context.Background(), map[string]any{"limit": value}); err == nil {
				t.Fatalf("HistorySearch accepted invalid limit %#v", value)
			}
			if searchCalled {
				t.Fatal("HistorySearch called backend with invalid limit")
			}

			for _, parameter := range []string{"max_messages", "max_chars"} {
				getCalled := false
				get := NewGetTool(workspace, func(context.Context, string) (*conversation.Conversation, error) {
					getCalled = true
					return &conversation.Conversation{ID: "x", WorkspacePath: workspace}, nil
				})
				params := map[string]any{"conversation_id": "x", parameter: value}
				if _, err := get.Execute(context.Background(), params); err == nil {
					t.Fatalf("HistoryGet accepted invalid %s %#v", parameter, value)
				}
				if getCalled {
					t.Fatalf("HistoryGet called backend with invalid %s", parameter)
				}
			}
		})
	}
}

func decodeResult(t *testing.T, result *tools.ToolResult, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(result.Output), target); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result.Output)
	}
}
