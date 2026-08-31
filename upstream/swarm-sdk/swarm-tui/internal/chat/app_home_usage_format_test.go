package chat

import (
	"errors"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/metrics"
)

func TestFormatUsageTokenCountUsesReadableLargeUnits(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want string
	}{
		{name: "plain", in: 999, want: "999"},
		{name: "thousands", in: 1_500, want: "1.5k"},
		{name: "millions", in: 228_000_000, want: "228.0M"},
		{name: "billions", in: 19_904_593_480, want: "19.90B"},
		{name: "trillions", in: 1_250_000_000_000, want: "1.25T"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatUsageTokenCount(tt.in); got != tt.want {
				t.Fatalf("formatUsageTokenCount(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestConversationUsageScanDoesNotKeepUsageScreenLoading(t *testing.T) {
	app := &App{
		usageLoading:        true,
		usageLoadGeneration: 7,
	}

	app.applyConversationUsageFetched(conversationUsageFetchedMsg{
		generation: 7,
		snapshot: metrics.ConversationUsageSnapshot{
			Summary: metrics.ConversationUsageSummary{
				TotalTokens: 42,
			},
		},
	})

	if !app.usageLoading {
		t.Fatal("conversation scan completion must not control provider loading state")
	}
	if app.usageResult == nil || app.usageResult.Conversation == nil {
		t.Fatal("expected conversation usage result to be stored")
	}

	providerResult := &usageDataResult{}
	app.applyUsageDataFetched(usageDataFetchedMsg{
		generation: 7,
		result:     providerResult,
	})

	if app.usageLoading {
		t.Fatal("provider fetch completion should make the Usage screen usable")
	}
	if providerResult.Conversation == nil || providerResult.Conversation.TotalTokens != 42 {
		t.Fatal("provider result should preserve an already-completed conversation scan")
	}
}

func TestUsageFetchIgnoresStaleGeneration(t *testing.T) {
	current := &usageDataResult{ConversationErr: errors.New("current")}
	app := &App{
		usageLoading:        true,
		usageLoadGeneration: 8,
		usageResult:         current,
	}

	app.applyUsageDataFetched(usageDataFetchedMsg{
		generation: 7,
		result:     &usageDataResult{},
	})
	app.applyConversationUsageFetched(conversationUsageFetchedMsg{
		generation: 7,
		snapshot:   metrics.ConversationUsageSnapshot{Summary: metrics.ConversationUsageSummary{TotalTokens: 99}},
	})

	if !app.usageLoading {
		t.Fatal("stale provider result should not clear current loading state")
	}
	if app.usageResult != current {
		t.Fatal("stale results should not replace current generation data")
	}
}
