package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func TestPersistInLoopCompactionAdvancesDurableActiveContext(t *testing.T) {
	sdk, _ := newJoinKeyTestSDK(t)
	ctx := context.Background()

	conv, err := sdk.CreateConversationWithBranch(ctx, "chat", "main")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	original := &conversation.Message{
		ID:      "original-user",
		Role:    conversation.RoleUser,
		Content: "original context",
	}
	if err := sdk.AddMessage(ctx, conv.ID, original); err != nil {
		t.Fatalf("add original message: %v", err)
	}

	compacted := []*conversation.Message{{
		ID:      "compacted-summary",
		Role:    conversation.RoleUser,
		Content: "durable compacted context",
	}}
	if err := sdk.persistInLoopCompaction(
		ctx, conv.ID, compacted, compacted[0].Content, 321,
	); err != nil {
		t.Fatalf("persist in-loop compaction: %v", err)
	}

	reloaded := sdk.GetConversation(ctx, conv.ID)
	if reloaded == nil {
		t.Fatal("persisted conversation was not reloadable")
	}
	if len(reloaded.Messages) != 2 || reloaded.Messages[0].ID != original.ID {
		t.Fatalf("durable transcript = %#v, want archived original plus compacted generation", reloaded.Messages)
	}
	if reloaded.CompactionState == nil || reloaded.CompactionState.Generation != 1 ||
		reloaded.CompactionState.CompactionCount != 1 {
		t.Fatalf("compaction state = %#v, want generation/count 1", reloaded.CompactionState)
	}
	if reloaded.CurrentContextSize != 321 {
		t.Fatalf("current context size = %d, want 321", reloaded.CurrentContextSize)
	}
	active := reloaded.ActiveMessages()
	if len(active) != 1 || active[0].ID != compacted[0].ID {
		t.Fatalf("next execution context = %#v, want only compacted generation", active)
	}
	if generated, _ := active[0].Metadata[conversation.CompactionGeneratedMetadataKey].(bool); !generated {
		t.Fatalf("active compacted message metadata = %#v, want generated marker", active[0].Metadata)
	}
	if compacted[0].Metadata != nil {
		t.Fatalf("persistence callback mutated agent-owned compacted message: %#v", compacted[0].Metadata)
	}
}

func TestLoadMessagesFromSDKSurfacesCompactionHandoffButHidesFromSidecar(t *testing.T) {
	sdk, _ := newJoinKeyTestSDK(t)
	ctx := context.Background()
	conv, err := sdk.CreateConversationWithBranch(ctx, "chat", "main")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	original := &conversation.Message{
		ID:      "original-user",
		Role:    conversation.RoleUser,
		Content: "real original request",
	}
	if err := sdk.AddMessage(ctx, conv.ID, original); err != nil {
		t.Fatalf("add original message: %v", err)
	}
	compacted := []*conversation.Message{{
		ID:      "compacted-summary",
		Role:    conversation.RoleUser,
		Content: "internal continuation summary",
	}}
	if err := sdk.persistInLoopCompaction(ctx, conv.ID, compacted, compacted[0].Content, 321); err != nil {
		t.Fatalf("persist compaction: %v", err)
	}
	followup := &conversation.Message{
		ID:      "real-followup",
		Role:    conversation.RoleUser,
		Content: "real follow-up request",
	}
	if err := sdk.AddMessage(ctx, conv.ID, followup); err != nil {
		t.Fatalf("add follow-up message: %v", err)
	}

	app := &App{sdk: sdk}
	app.loadMessagesFromSDK(conv.ID)

	// The compaction-generated handoff message is no longer silently dropped
	// (see convertCompactionHandoffMessage in app_sdk_helpers.go): it is now
	// rendered as a collapsed-by-default Role="system" entry between the two
	// real user turns, so a user/agent can inspect exactly what was handed off
	// instead of it vanishing from the transcript entirely.
	if len(app.messages) != 3 {
		t.Fatalf("visible messages = %#v, want two real user turns plus one compaction handoff entry", app.messages)
	}
	if app.messages[0].Content != original.Content || app.messages[2].Content != followup.Content {
		t.Fatalf("visible messages = %#v, real history was hidden or reordered", app.messages)
	}
	if app.messages[1].Role != "system" {
		t.Fatalf("compaction handoff message role = %q, want %q", app.messages[1].Role, "system")
	}
	if !strings.Contains(app.messages[1].Content, "internal continuation summary") {
		t.Fatalf("compaction handoff content = %q, want it to preserve the original summary text", app.messages[1].Content)
	}
	active := sdk.GetConversation(ctx, conv.ID).ActiveMessages()
	if len(active) != 2 || active[0].ID != compacted[0].ID || active[1].ID != followup.ID {
		t.Fatalf("provider-visible active context = %#v, want generated summary plus follow-up", active)
	}
	// convertSDKMessages (app_sdk_helpers.go) intentionally no longer drops
	// compaction-generated messages — it renders them as a collapsed Role=
	// "system" handoff entry (see convertCompactionHandoffMessage) instead of
	// silently hiding them, matching loadMessagesFromSDK above.
	converted := convertSDKMessages([]*conversation.Message{original, active[0], followup})
	if len(converted) != 3 {
		t.Fatalf("history conversion = %#v, want two real turns plus one compaction handoff entry", converted)
	}
	if converted[0].Content != original.Content || converted[2].Content != followup.Content {
		t.Fatalf("history conversion = %#v, real history was hidden or reordered", converted)
	}
	if converted[1].Role != "system" || !strings.Contains(converted[1].Content, "internal continuation summary") {
		t.Fatalf("compaction handoff entry = %#v, want collapsed system message preserving summary text", converted[1])
	}
	// loadSidecarMessages is a distinct 10-message sidebar preview, not the
	// main transcript — it deliberately still excludes generated compaction
	// context so a large handoff blob can't crowd real turns out of the
	// preview window. That behavior is intentionally unchanged.
	sidecar := (&App{sdk: sdk}).loadSidecarMessages(conv.ID)
	if len(sidecar) != 2 || sidecar[0].Content != original.Content || sidecar[1].Content != followup.Content {
		t.Fatalf("sidecar preview leaked generated context: %#v", sidecar)
	}
}

func TestA2AExecutionRefreshesPersistedAutoCompactionConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	initialProvider := &testChatProvider{name: "openai"}
	agt := newTestAgent(t, initialProvider, "openai", "gpt-5.1")
	agt.SetAutoCompactionConfig(agent.AutoCompactionConfig{
		EnableAutoCompaction: true,
		Threshold: agent.AutoCompactionThreshold{
			Mode:  agent.AutoCompactionThresholdPercent,
			Value: 0.8,
		},
	})
	sdk := &SDKIntegration{
		agent:        agt,
		provider:     initialProvider,
		providerName: "openai",
		currentModel: "gpt-5.1",
	}

	configManager, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	config, err := configManager.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	config.SetEnableAutoCompaction(false)
	if err := configManager.SaveConfig(config); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if got := sdk.activeAgentWithFreshCompactionConfig(); got != agt {
		t.Fatal("A2A execution selected a different agent")
	}
	if cfg := agt.AutoCompactionConfig(); cfg.EnableAutoCompaction {
		t.Fatalf("A2A execution retained stale auto-compaction config: %#v", cfg)
	}
}
