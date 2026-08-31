package chat

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func TestSnapshotCompactionRunCapturesActiveProfileProviderAndModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first := &testChatProvider{
		name:         "profile-provider",
		capabilities: provider.Capabilities{MaxContextWindow: 272_000, MaxOutputTokens: 16_384},
	}
	second := &testChatProvider{
		name:         "replacement-provider",
		capabilities: provider.Capabilities{MaxContextWindow: 128_000, MaxOutputTokens: 8_192},
	}
	slot := &providerSlot{}
	slot.SetRuntime(first, "profile-alias", "profile-model", 272_000)
	sdk := &SDKIntegration{
		provider:             first,
		providerName:         "profile-alias",
		currentModel:         "profile-model",
		registryProviderSlot: slot,
	}
	app := &App{
		sdk:             sdk,
		currentProvider: "profile-alias",
		currentModel:    "profile-model",
	}

	snapshot := app.snapshotCompactionRun(commands.CompactRequestMsg{}, "")
	if snapshot.Provider != first {
		t.Fatalf("snapshot provider = %T, want original profile provider", snapshot.Provider)
	}
	if snapshot.ProviderName != "profile-alias" || snapshot.Model != "profile-model" {
		t.Fatalf("snapshot identity = %s/%s, want profile-alias/profile-model", snapshot.ProviderName, snapshot.Model)
	}
	if snapshot.ContextLimit != 272_000 {
		t.Fatalf("snapshot context = %d, want 272000", snapshot.ContextLimit)
	}
	if snapshot.SummaryContextLimit != snapshot.ContextLimit {
		t.Fatalf("summary context = %d, want active agent context %d", snapshot.SummaryContextLimit, snapshot.ContextLimit)
	}

	slot.SetRuntime(second, "replacement-alias", "replacement-model", 128_000)
	sdk.providerName = "replacement-alias"
	sdk.currentModel = "replacement-model"
	app.currentProvider = "replacement-alias"
	app.currentModel = "replacement-model"

	if snapshot.Provider != first || snapshot.ProviderName != "profile-alias" || snapshot.Model != "profile-model" {
		t.Fatal("in-flight compaction snapshot changed after profile switch")
	}
	if got := sdk.GetProvider(); got != second {
		t.Fatalf("active provider = %T, want replacement provider", got)
	}
}
