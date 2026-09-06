package manager

// joinkey_test.go — end-to-end proof for PLAN.md gap G1.
//
// These tests do NOT assert against an in-memory struct returned by Create.
// They drive the real creation path (Manager.Create over real file storage),
// then read the persisted JSON back off disk and inspect metadata.custom,
// because "the key is in the struct" and "the key survives to the store" are
// different claims and only the second one is worth anything: the join happens
// against files, not against structs.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// newTestManager builds a Manager over a real on-disk store rooted in a temp
// directory, so nothing here can touch ~/.swarmos/conversations.
func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	baseDir := t.TempDir()
	store, err := storage.NewFileStorage(storage.FileStorageConfig{BaseDir: baseDir})
	if err != nil {
		t.Fatalf("NewFileStorage: %v", err)
	}
	mgr, err := NewManager(Config{
		Storage: store,
		Logger:  observability.NewNopLogger(),
		Tracer:  observability.NewNoopTracer(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr, baseDir
}

// persistedCustom locates the conversation file written for id anywhere under
// baseDir (the store partitions by workspace) and returns its metadata.custom
// object exactly as it was serialised.
func persistedCustom(t *testing.T, baseDir, id string) map[string]any {
	t.Helper()
	var found string
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		if strings.Contains(filepath.Base(path), id) {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", baseDir, err)
	}
	if found == "" {
		t.Fatalf("no persisted file for conversation %s under %s", id, baseDir)
	}

	raw, err := os.ReadFile(found)
	if err != nil {
		t.Fatalf("read %s: %v", found, err)
	}
	var doc struct {
		Metadata struct {
			Custom map[string]any `json:"custom"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", found, err)
	}
	return doc.Metadata.Custom
}

// TestCreateStampsJoinKeysOnDisk is the headline test: create a conversation the
// normal way and confirm the join keys are present and non-empty in the file
// that lands in the store. Before this change every one of these assertions
// failed — the store carried none of these keys at all.
func TestCreateStampsJoinKeysOnDisk(t *testing.T) {
	mgr, baseDir := newTestManager(t)

	// A real repository shape so git_sha has something honest to resolve.
	workspace := t.TempDir()
	const headSHA = "abcdef0123456789abcdef0123456789abcdef01"
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "HEAD"), []byte(headSHA+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile HEAD: %v", err)
	}

	conv, err := mgr.Create(context.Background(), CreateOptions{
		Mode:          "chat",
		WorkspacePath: workspace,
		Origin:        conversation.OriginInteractive,
		AgentID:       "swarm-agent",
		SessionID:     "session-under-test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	custom := persistedCustom(t, baseDir, conv.ID)
	if custom == nil {
		t.Fatal("persisted metadata.custom is absent — no join key reached the store")
	}

	for key, want := range map[string]string{
		"session_id": "session-under-test",
		"agent_id":   "swarm-agent",
		"origin":     "interactive",
		"git_sha":    headSHA,
	} {
		got, ok := custom[key].(string)
		if !ok {
			t.Errorf("persisted metadata.custom[%q] missing (have %v)", key, sortedKeys(custom))
			continue
		}
		if got == "" {
			t.Errorf("persisted metadata.custom[%q] is empty", key)
		}
		if got != want {
			t.Errorf("persisted metadata.custom[%q] = %q, want %q", key, got, want)
		}
	}

	// Keys with no producer today must not be invented (see joinkey.go: child
	// runs do not get their own conversation file yet).
	for _, absent := range []string{"parent_conversation_id", "parent_agent_id"} {
		if _, present := custom[absent]; present {
			t.Errorf("metadata.custom[%q] was written with no parent supplied: %#v", absent, custom)
		}
	}
}

// TestCreateUsesProcessSessionIDByDefault: callers that do not supply a session
// still get the process session, which is the same value the hook stream and the
// headless stream-json events report. Without this, coverage would depend on
// every call site remembering to thread an identity through.
func TestCreateUsesProcessSessionIDByDefault(t *testing.T) {
	mgr, baseDir := newTestManager(t)

	conv, err := mgr.Create(context.Background(), CreateOptions{Mode: "chat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	custom := persistedCustom(t, baseDir, conv.ID)
	got, _ := custom["session_id"].(string)
	if got == "" {
		t.Fatal("session_id is absent or empty with no explicit SessionID")
	}
	if want := conversation.ProcessSessionID(); got != want {
		t.Errorf("session_id = %q, want the process session %q", got, want)
	}
}

// TestCreateDoesNotDisturbCallerMetadata is the additive-only guarantee at the
// creation boundary: existing keys keep their values and nothing supplied by the
// caller is dropped or rewritten.
func TestCreateDoesNotDisturbCallerMetadata(t *testing.T) {
	mgr, baseDir := newTestManager(t)

	conv, err := mgr.Create(context.Background(), CreateOptions{
		Mode:          "act",
		WorkspacePath: t.TempDir(),
		// The headless path seeds origin itself; the declarative field says
		// something different on purpose to prove the seeded value wins.
		Origin: conversation.OriginInteractive,
		Metadata: &conversation.ConversationMetadata{
			Tags: []string{conversation.HeadlessTag},
			Custom: map[string]any{
				"origin":         conversation.OriginHeadless,
				"workspace_path": "/work",
				"git_branch":     "feat/harness-anywhere",
				"forked_from":    "conv-root",
			},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	custom := persistedCustom(t, baseDir, conv.ID)
	for key, want := range map[string]string{
		"origin":         conversation.OriginHeadless,
		"workspace_path": "/work",
		"git_branch":     "feat/harness-anywhere",
		"forked_from":    "conv-root",
	} {
		if got, _ := custom[key].(string); got != want {
			t.Errorf("metadata.custom[%q] = %#v, want the caller's %q untouched", key, custom[key], want)
		}
	}
	if got, _ := custom["session_id"].(string); got == "" {
		t.Error("session_id was not added alongside the caller's keys")
	}
	if strings.Join(conv.Metadata.Tags, ",") != conversation.HeadlessTag {
		t.Errorf("Tags = %#v, want the caller's tags preserved", conv.Metadata.Tags)
	}
}

// TestLegacyConversationWithoutJoinKeysStillLoads is the backward-compatibility
// test. It writes a conversation in the pre-change shape — no session_id, no
// agent_id, no origin, no git_sha, just the keys that were actually in use — and
// confirms it loads, keeps its messages and its old custom keys, and reports
// empty join keys instead of failing.
//
// This is the majority case: every one of the ~1,500 most recent conversations
// in the real store looks like this.
func TestLegacyConversationWithoutJoinKeysStillLoads(t *testing.T) {
	mgr, baseDir := newTestManager(t)

	const id = "legacy-conv-no-join-keys"
	legacy := `{
  "id": "` + id + `",
  "created_at": "2025-11-02T10:00:00Z",
  "updated_at": "2025-11-02T10:05:00Z",
  "mode": "chat",
  "status": "active",
  "messages": [
    {"id": "m1", "role": "user", "content": "hello", "timestamp": "2025-11-02T10:00:00Z"},
    {"id": "m2", "role": "assistant", "content": "hi", "timestamp": "2025-11-02T10:00:30Z"}
  ],
  "metadata": {
    "tags": ["tui"],
    "custom": {
      "workspace_path": "/home/swarm/Work/mono",
      "git_branch": "main",
      "forked_from": "conv-root"
    }
  },
  "workspace_path": "/home/swarm/Work/mono"
}`
	if err := os.WriteFile(filepath.Join(baseDir, id+".json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	conv, err := mgr.Resume(context.Background(), id)
	if err != nil {
		t.Fatalf("Resume of a pre-join-key conversation failed: %v", err)
	}
	if conv.ID != id {
		t.Errorf("ID = %q, want %q", conv.ID, id)
	}
	if len(conv.Messages) != 2 {
		t.Errorf("loaded %d messages, want 2", len(conv.Messages))
	}
	if conv.Metadata.Custom["git_branch"] != "main" {
		t.Errorf("git_branch = %#v, want the legacy value intact", conv.Metadata.Custom["git_branch"])
	}
	if conv.Metadata.Custom["forked_from"] != "conv-root" {
		t.Errorf("forked_from = %#v, want the legacy lineage key intact", conv.Metadata.Custom["forked_from"])
	}

	// The read path must report "unknown", not blow up, and must not have
	// back-filled anything on load.
	if got := conversation.ReadJoinKeys(conv.Metadata); got != (conversation.JoinKeys{}) {
		t.Errorf("ReadJoinKeys(legacy) = %#v, want the zero value", got)
	}
	for _, key := range []string{"session_id", "agent_id", "origin", "git_sha"} {
		if _, present := conv.Metadata.Custom[key]; present {
			t.Errorf("loading back-filled %q into a legacy conversation: %#v", key, conv.Metadata.Custom)
		}
	}

	// And it must still be usable: adding a message and saving works.
	conv.AddMessage(&conversation.Message{ID: "m3", Role: conversation.RoleUser, Content: "again"})
	if err := mgr.Save(context.Background(), conv); err != nil {
		t.Fatalf("Save of a legacy conversation failed: %v", err)
	}
	reloaded, err := mgr.Resume(context.Background(), id)
	if err != nil {
		t.Fatalf("Resume after save: %v", err)
	}
	if len(reloaded.Messages) != 3 {
		t.Errorf("after save/reload got %d messages, want 3", len(reloaded.Messages))
	}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
