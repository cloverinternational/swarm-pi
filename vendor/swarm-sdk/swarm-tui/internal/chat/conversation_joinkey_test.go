package chat

// conversation_joinkey_test.go — proves the TUI's own creation paths declare an
// origin (PLAN.md gap G6) and carry the join key (gap G1).
//
// This drives the real SDKIntegration methods the TUI calls, over a real SDK
// client and a real on-disk store, and then reads metadata.custom back off the
// created conversation. Before this change the interactive path wrote NO origin
// key at all — origin existed only on headless conversations, which is exactly
// why stratified reporting was guesswork on the TUI side.

import (
	"context"
	"testing"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// newJoinKeyTestSDK builds the smallest SDKIntegration that can create
// conversations: a real client backed by a temp storage dir, a real workspace,
// and a no-op logger. No provider call is made by any creation path.
func newJoinKeyTestSDK(t *testing.T) (*SDKIntegration, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()

	c, err := sdkclient.New(
		sdkclient.WithoutAutoConfig(),
		sdkclient.WithProvider("anthropic", "claude-sonnet-4-5"),
		sdkclient.WithAPIKey("dummy"),
		sdkclient.WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return &SDKIntegration{
		sdkClient:     c,
		workspaceRoot: workspace,
		projectRoot:   workspace,
		logger:        observability.NewNopLogger(),
	}, workspace
}

// TestCreateConversationWithBranchStampsInteractiveOrigin is the G6 fix: an
// interactive TUI conversation now declares origin=interactive, using the same
// vocabulary the HistorySearch "origin" filter accepts.
func TestCreateConversationWithBranchStampsInteractiveOrigin(t *testing.T) {
	sdk, _ := newJoinKeyTestSDK(t)

	conv, err := sdk.CreateConversationWithBranch(context.Background(), "chat", "feat/harness-anywhere")
	if err != nil {
		t.Fatalf("CreateConversationWithBranch: %v", err)
	}

	keys := conversation.ReadJoinKeys(conv.Metadata)
	if keys.Origin != conversation.OriginInteractive {
		t.Errorf("origin = %q, want %q (custom = %#v)", keys.Origin, conversation.OriginInteractive, conv.Metadata.Custom)
	}
	if keys.SessionID == "" {
		t.Error("session_id is empty — the conversation cannot be joined to the event stream")
	}
	if keys.SessionID != conversation.ProcessSessionID() {
		t.Errorf("session_id = %q, want the process session %q", keys.SessionID, conversation.ProcessSessionID())
	}
	if keys.AgentID == "" {
		t.Error("agent_id is empty — the conversation is not attributable to an agent")
	}
	// The pre-existing keys this path already wrote must be untouched.
	if conv.Metadata.Custom["git_branch"] != "feat/harness-anywhere" {
		t.Errorf("git_branch = %#v, want the branch this path has always written", conv.Metadata.Custom["git_branch"])
	}
	if conv.Metadata.Custom["workspace_path"] == nil {
		t.Error("workspace_path was dropped")
	}
}

// TestCreateHeadlessConversationKeepsHeadlessOrigin: the headless path already
// seeded origin itself, and the declarative field must not change what it wrote.
// This is the "existing values are never overwritten" guarantee at a real call
// site rather than in a unit test.
func TestCreateHeadlessConversationKeepsHeadlessOrigin(t *testing.T) {
	sdk, _ := newJoinKeyTestSDK(t)

	conv, err := sdk.CreateHeadlessConversation(context.Background(), "act")
	if err != nil {
		t.Fatalf("CreateHeadlessConversation: %v", err)
	}

	keys := conversation.ReadJoinKeys(conv.Metadata)
	if keys.Origin != conversation.OriginHeadless {
		t.Errorf("origin = %q, want %q", keys.Origin, conversation.OriginHeadless)
	}
	if keys.SessionID == "" {
		t.Error("session_id is empty on a headless conversation")
	}
	// The tag-based exclusion this path depends on must still work.
	if len(conv.Metadata.Tags) != 1 || conv.Metadata.Tags[0] != conversation.HeadlessTag {
		t.Errorf("Tags = %#v, want [%q]", conv.Metadata.Tags, conversation.HeadlessTag)
	}
}

// TestForkConversationStampsOriginAndKeepsLineage: a fork is interactive, and
// its parent is recorded as forked_from — NOT as parent_conversation_id, which
// is reserved for spawn attribution (see joinkey.go). Conflating the two would
// make both unreadable.
func TestForkConversationStampsOriginAndKeepsLineage(t *testing.T) {
	sdk, _ := newJoinKeyTestSDK(t)
	ctx := context.Background()

	source, err := sdk.CreateConversationWithBranch(ctx, "chat", "main")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := sdk.addMsg(ctx, source.ID, &conversation.Message{
		ID: "m1", Role: conversation.RoleUser, Content: "first",
	}); err != nil {
		t.Fatalf("add message: %v", err)
	}

	fork, err := sdk.ForkConversation(ctx, source.ID, 1)
	if err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}

	keys := conversation.ReadJoinKeys(fork.Metadata)
	if keys.Origin != conversation.OriginInteractive {
		t.Errorf("fork origin = %q, want %q", keys.Origin, conversation.OriginInteractive)
	}
	if keys.SessionID == "" {
		t.Error("fork session_id is empty")
	}
	if fork.Metadata.Custom["forked_from"] != source.ID {
		t.Errorf("forked_from = %#v, want %q", fork.Metadata.Custom["forked_from"], source.ID)
	}
	if keys.ParentConversationID != "" {
		t.Errorf("parent_conversation_id = %q; fork lineage must not be recorded as spawn attribution", keys.ParentConversationID)
	}
}
