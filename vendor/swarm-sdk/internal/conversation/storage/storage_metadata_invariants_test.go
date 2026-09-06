package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func newTestDirectoryStorage(t *testing.T, baseDir string) *DirectoryFileStorage {
	t.Helper()
	store, err := NewDirectoryFileStorage(DirectoryFileStorageConfig{
		BaseDir:   baseDir,
		CacheSize: 10,
	})
	if err != nil {
		t.Fatalf("NewDirectoryFileStorage: %v", err)
	}
	return store
}

func writePartitionedConversation(t *testing.T, baseDir, workspacePath, id string, data []byte) string {
	t.Helper()
	wsDir := EncodeWorkspacePath(workspacePath)
	dir := filepath.Join(baseDir, wsDir)
	if err := os.MkdirAll(filepath.Join(baseDir, IndexDir), 0o755); err != nil {
		t.Fatalf("MkdirAll index: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile conversation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, IndexDir, id), []byte(wsDir), 0o644); err != nil {
		t.Fatalf("WriteFile index: %v", err)
	}
	return path
}

func TestNormalizeWorkspacePathPrecedence(t *testing.T) {
	t.Run("top-level wins", func(t *testing.T) {
		conv := &conversation.Conversation{
			WorkspacePath: "/top",
			Metadata: conversation.ConversationMetadata{
				Custom: map[string]any{"workspace_path": "/custom"},
			},
		}
		normalizeWorkspacePath(conv, "/parent")
		if conv.WorkspacePath != "/top" {
			t.Fatalf("WorkspacePath = %q, want /top", conv.WorkspacePath)
		}
	})

	t.Run("custom string wins over parent", func(t *testing.T) {
		conv := &conversation.Conversation{
			Metadata: conversation.ConversationMetadata{
				Custom: map[string]any{"workspace_path": "/custom"},
			},
		}
		normalizeWorkspacePath(conv, "/parent")
		if conv.WorkspacePath != "/custom" {
			t.Fatalf("WorkspacePath = %q, want /custom", conv.WorkspacePath)
		}
	})

	t.Run("decoded parent is final fallback", func(t *testing.T) {
		conv := &conversation.Conversation{
			Metadata: conversation.ConversationMetadata{
				Custom: map[string]any{"workspace_path": 42},
			},
		}
		normalizeWorkspacePath(conv, "/parent")
		if conv.WorkspacePath != "/parent" {
			t.Fatalf("WorkspacePath = %q, want /parent", conv.WorkspacePath)
		}
	})
}

func TestSaveNormalizesWorkspacePathBeforeWarmAndColdReads(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	store := newTestDirectoryStorage(t, baseDir)
	conv := &conversation.Conversation{
		ID: "save-normalized",
		Metadata: conversation.ConversationMetadata{
			Custom: map[string]any{"workspace_path": "/workspace/from-custom"},
		},
	}

	if err := store.Save(ctx, conv); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if conv.WorkspacePath != "/workspace/from-custom" {
		t.Fatalf("saved conversation WorkspacePath = %q", conv.WorkspacePath)
	}

	warm, err := store.Load(ctx, conv.ID)
	if err != nil {
		t.Fatalf("warm Load: %v", err)
	}
	if warm.WorkspacePath != "/workspace/from-custom" {
		t.Fatalf("warm WorkspacePath = %q", warm.WorkspacePath)
	}

	coldStore := newTestDirectoryStorage(t, baseDir)
	cold, err := coldStore.Load(ctx, conv.ID)
	if err != nil {
		t.Fatalf("cold Load: %v", err)
	}
	if cold.WorkspacePath != "/workspace/from-custom" {
		t.Fatalf("cold WorkspacePath = %q", cold.WorkspacePath)
	}
}

func TestDecodedWorkspaceDirectoryHintAcrossLoadAndListingPaths(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	const (
		id            = "parent-fallback"
		workspacePath = "/workspace/from-parent"
	)
	data, err := json.Marshal(&conversation.Conversation{ID: id})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	writePartitionedConversation(t, baseDir, workspacePath, id, data)

	t.Run("direct load cold and warm", func(t *testing.T) {
		store := newTestDirectoryStorage(t, baseDir)
		for i := 0; i < 2; i++ {
			got, err := store.Load(ctx, id)
			if err != nil {
				t.Fatalf("Load %d: %v", i, err)
			}
			if got.WorkspacePath != workspacePath {
				t.Fatalf("Load %d WorkspacePath = %q, want %q", i, got.WorkspacePath, workspacePath)
			}
		}
	})

	t.Run("ordinary listing cold and warm", func(t *testing.T) {
		store := newTestDirectoryStorage(t, baseDir)
		for i := 0; i < 2; i++ {
			got, err := store.ListByWorkspace(ctx, workspacePath)
			if err != nil {
				t.Fatalf("ListByWorkspace %d: %v", i, err)
			}
			if len(got) != 1 || got[0].WorkspacePath != workspacePath {
				t.Fatalf("ListByWorkspace %d = %#v", i, got)
			}
			if i == 0 {
				if _, err := store.Load(ctx, id); err != nil {
					t.Fatalf("populate cache: %v", err)
				}
			}
		}
	})

	t.Run("pooled metadata and full listing", func(t *testing.T) {
		for _, filter := range []Filter{
			{WorkspacePath: workspacePath, ExcludeMessages: true},
			{WorkspacePath: workspacePath},
		} {
			store := newTestDirectoryStorage(t, baseDir)
			got, err := store.Query(ctx, filter)
			if err != nil {
				t.Fatalf("Query(%+v): %v", filter, err)
			}
			if len(got) != 1 || got[0].WorkspacePath != workspacePath {
				t.Fatalf("Query(%+v) = %#v", filter, got)
			}
		}
	})
}

func TestLargeLegacyMetadataAfterMessagesIsComplete(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	const (
		id              = "large-legacy"
		parentWorkspace = "/workspace/from-parent"
		customWorkspace = "/workspace/from-custom"
	)

	// Preserve legacy field order: a messages payload larger than the former
	// 256 KiB read cap comes before summary and metadata.
	largeContent := strings.Repeat("x", 512*1024)
	raw := `{"id":"` + id + `","messages":[{"id":"m1","role":"user","content":"` +
		largeContent + `"}],"summary":{"message_count":37,"last_model":"legacy-model"},` +
		`"metadata":{"tags":["legacy","important"],"custom":{"workspace_path":"` +
		customWorkspace + `","git_branch":"main"}}}`
	writePartitionedConversation(t, baseDir, parentWorkspace, id, []byte(raw))

	store := newTestDirectoryStorage(t, baseDir)
	got, err := store.Query(ctx, Filter{ExcludeMessages: true})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Query returned %d conversations, want 1", len(got))
	}
	conv := got[0]
	// The messages array is authoritative even when a stale legacy summary says 37.
	if conv.Summary == nil || conv.Summary.MessageCount != 1 || conv.Summary.LastModel != "legacy-model" {
		t.Fatalf("Summary = %#v", conv.Summary)
	}
	if strings.Join(conv.Metadata.Tags, ",") != "legacy,important" {
		t.Fatalf("Tags = %#v", conv.Metadata.Tags)
	}
	if conv.Metadata.Custom["git_branch"] != "main" {
		t.Fatalf("Custom = %#v", conv.Metadata.Custom)
	}
	if conv.WorkspacePath != customWorkspace {
		t.Fatalf("WorkspacePath = %q, want custom metadata path %q", conv.WorkspacePath, customWorkspace)
	}
	if conv.Messages != nil {
		t.Fatalf("ExcludeMessages returned %d messages", len(conv.Messages))
	}
}

func TestSaveWorkspaceMoveRemovesOldPartitionCopy(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	store := newTestDirectoryStorage(t, baseDir)
	conv := &conversation.Conversation{ID: "move-me", WorkspacePath: "/workspace/old"}
	if err := store.Save(ctx, conv); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	oldPath := filepath.Join(baseDir, EncodeWorkspacePath("/workspace/old"), conv.ID+".json")
	conv.WorkspacePath = "/workspace/new"
	if err := store.Save(ctx, conv); err != nil {
		t.Fatalf("moved Save: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old workspace copy still exists: err=%v", err)
	}
	got, err := store.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].WorkspacePath != "/workspace/new" {
		t.Fatalf("moved listing = %#v", got)
	}
}
