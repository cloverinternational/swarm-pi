package backfill

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	sdkanalytics "github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
)

func TestRunBackfillsConversationEvents(t *testing.T) {
	home := t.TempDir()
	opts := DefaultOptions(home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	store, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{BaseDir: opts.ConversationsDir})
	if err != nil {
		t.Fatalf("NewDirectoryFileStorage: %v", err)
	}
	createdAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	conv := &conversation.Conversation{
		ID:            "conv-backfill",
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt.Add(2 * time.Minute),
		Mode:          "ACT",
		Status:        conversation.StatusCompleted,
		WorkspacePath: workspace,
		Metadata: conversation.ConversationMetadata{
			Custom: map[string]any{"git_branch": "main", "workspace_path": workspace},
		},
		Messages: []*conversation.Message{
			{
				ID:        "msg-user",
				Timestamp: createdAt.Add(time.Second),
				Role:      conversation.RoleUser,
				Content:   "hello",
			},
			{
				Timestamp: createdAt.Add(30 * time.Second),
				Role:      conversation.RoleAssistant,
				Content:   "done",
				Provider:  "openai",
				Model:     "gpt-test",
				ToolCalls: []conversation.ToolCall{{
					ID:         "tool-1",
					Name:       "Read",
					Parameters: map[string]any{"file_path": "README.md"},
				}},
			},
			{
				ID:        "msg-tool",
				Timestamp: createdAt.Add(time.Minute),
				Role:      conversation.RoleUser,
				ToolResults: []conversation.ToolResult{{
					CallID: "tool-1",
					Name:   "Read",
					Output: "contents",
				}},
			},
		},
	}
	if err := store.Save(context.Background(), conv); err != nil {
		t.Fatalf("Save conversation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close storage: %v", err)
	}

	var eventTypes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/collect":
			var req sdkanalytics.BatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode collect: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for _, event := range req.Events {
				eventTypes = append(eventTypes, event.EventType)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	configureAnalyticsEnv(t, home, server.URL)

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v\n%+v", err, result)
	}
	for _, want := range []string{"conversation.created", "session.started", "message.finalized", "tool.started", "tool.completed", "conversation.completed", "session.stopped"} {
		if !slices.Contains(eventTypes, want) {
			t.Fatalf("event types %v missing %s", eventTypes, want)
		}
	}
	if result.EventsEnqueued == 0 {
		t.Fatalf("EventsEnqueued = 0")
	}
	if result.SpoolAfter.Events.Files != 0 {
		t.Fatalf("event spool files after flush = %d, want 0", result.SpoolAfter.Events.Files)
	}
}

func TestRunBackfillsArtifacts(t *testing.T) {
	home := t.TempDir()
	opts := DefaultOptions(home)
	workspace := filepath.Join(home, "workspace")
	projectHash := projectHashForWorkspace(workspace)
	projectDir := filepath.Join(opts.SwarmDir, "projects", projectHash)
	writeFile(t, filepath.Join(workspace, ".swarm", "bronze", "2026", "04", "01", "events_12.jsonl"), `{"event_id":"legacy-bronze-1","ts":"2026-04-01T12:00:00Z","type":"tool.after_execute","conv_id":"conv-backfill"}`+"\n")
	writeFile(t, filepath.Join(workspace, ".swarm", "gold", "daily", "2026-04-01-12-00-gold-insight.md"), "---\ntimestamp: 2026-04-01T12:04:00Z\ncategory: gold-analysis\nseverity: high\n---\n\nLegacy markdown insight.\n")
	writeFile(t, filepath.Join(projectDir, "findings", "cache", "2026", "04", "01", "findings.jsonl"), `{"finding_id":"finding-1","timestamp":"2026-04-01T12:00:00Z","tool_name":"Read"}`+"\n")
	writeFile(t, filepath.Join(projectDir, "silver", "2026", "04", "01", "tree_conv-backfill.json"), `{"conversation_id":"conv-backfill","generated_at":"2026-04-01T12:01:00Z","total_events":1}`)
	writeFile(t, filepath.Join(projectDir, "gold", "runs", "2026", "04", "01", "run_run-1.json"), `{"id":"run-1","started_at":"2026-04-01T12:02:00Z","trees_analyzed":1,"insights_generated":1,"duration_ms":10}`)
	writeFile(t, filepath.Join(projectDir, "gold", "insights", "2026", "04", "01", "insights_run-1.json"), `[{"id":"insight-1","analysis_run_id":"run-1","generated_at":"2026-04-01T12:03:00Z","title":"Insight","category":"pattern","severity":"high"}]`)

	var artifactTypes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/artifacts":
			var req sdkanalytics.ArtifactBatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode artifacts: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for _, artifact := range req.Artifacts {
				artifactTypes = append(artifactTypes, artifact.ArtifactType)
				if artifact.ProjectHash != projectHash {
					t.Errorf("artifact project hash = %q, want %q", artifact.ProjectHash, projectHash)
				}
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	configureAnalyticsEnv(t, home, server.URL)

	opts.Include = IncludeArtifacts
	opts.Workspace = workspace
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v\n%+v", err, result)
	}
	// dream_memory intentionally absent: the Dream/memory subsystem was removed
	// in 4a62d32d and backfill no longer scans project memory files.
	for _, want := range []string{"bronze_event", "finding", "silver_tree", "gold_run", "gold_insight"} {
		if !slices.Contains(artifactTypes, want) {
			t.Fatalf("artifact types %v missing %s", artifactTypes, want)
		}
	}
	if result.ArtifactsEnqueued != len(artifactTypes) {
		t.Fatalf("ArtifactsEnqueued = %d, sent = %d", result.ArtifactsEnqueued, len(artifactTypes))
	}
}

func configureAnalyticsEnv(t *testing.T, home, collectorURL string) {
	t.Helper()
	// Telemetry is opt-in and off by default; exercise the enabled path by
	// explicitly opting in and supplying a collector URL + token.
	t.Setenv("SWARM_ANALYTICS_ENABLED", "1")
	t.Setenv("SWARM_ANALYTICS_DISABLED", "")
	t.Setenv("SWARM_ANALYTICS_COLLECTOR_URL", collectorURL)
	t.Setenv("SWARM_ANALYTICS_AUTH_TOKEN", "test-token")
	t.Setenv("SWARM_ANALYTICS_SPOOL_DIR", filepath.Join(home, "spool"))
	t.Setenv("SWARM_ANALYTICS_DEVICE_ID", "device-test")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
