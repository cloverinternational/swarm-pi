package chat

import (
	"testing"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
)

func TestSDKIntegrationActiveConversationUpdatesLegacyAndClientState(t *testing.T) {
	client := &sdkclient.Client{}
	sdk := &SDKIntegration{sdkClient: client}

	sdk.SetActiveConversation("  conv-active  ")

	if got := sdk.currentActiveConversationID(); got != "conv-active" {
		t.Fatalf("legacy active conversation: got %q want %q", got, "conv-active")
	}
	if got := client.ActiveConversation(); got != "conv-active" {
		t.Fatalf("client active conversation: got %q want %q", got, "conv-active")
	}
	if got := client.Snapshot().ActiveConvID; got != "conv-active" {
		t.Fatalf("client snapshot active conversation: got %q want %q", got, "conv-active")
	}

	sdk.ClearActiveConversation()
	if got := sdk.currentActiveConversationID(); got != "" {
		t.Fatalf("legacy active conversation after clear: got %q", got)
	}
	if got := client.ActiveConversation(); got != "" {
		t.Fatalf("client active conversation after clear: got %q", got)
	}
}

func TestSDKIntegrationRapidSetClearLeavesBothStatesCleared(t *testing.T) {
	client := &sdkclient.Client{}
	sdk := &SDKIntegration{sdkClient: client}

	for i := 0; i < 1000; i++ {
		sdk.SetActiveConversation("conv-stale")
		sdk.ClearActiveConversation()
	}

	if got := sdk.currentActiveConversationID(); got != "" {
		t.Fatalf("legacy active conversation after rapid clear: got %q", got)
	}
	if got := client.Snapshot().ActiveConvID; got != "" {
		t.Fatalf("client active conversation after rapid clear: got %q", got)
	}
}
