package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// CompactionNeededUpdate must surface a UI status message (progress bar /
// notification for the SDK's in-loop compaction) but must NOT schedule a
// TUI-side compaction: the in-loop CompactFunc owns the actual work, and a
// CompactRequestMsg here would double-compact mid-turn.
func TestCompactionNeededUpdateQueuesStatusOnlyNotCompaction(t *testing.T) {
	app := &App{updateQueue: make(chan tea.Msg, 2)}

	app.queueAgentIntermediateUpdateWithContext(
		a2aExecutionSourceLocal,
		"conversation-1",
		agent.CompactionNeededUpdate{
			CurrentTokens: 90_000,
			Threshold:     80_000,
			ContextLimit:  100_000,
			PercentUsed:   0.9,
		},
	)

	if got := len(app.updateQueue); got != 1 {
		t.Fatalf("CompactionNeededUpdate queued %d runtime messages; want exactly 1 UI status message", got)
	}
	msg := <-app.updateQueue
	if _, ok := msg.(commands.CompactRequestMsg); ok {
		t.Fatal("CompactionNeededUpdate scheduled a TUI compaction; the SDK's in-loop CompactFunc owns the work")
	}
	started, ok := msg.(agentAutoCompactionStartedMsg)
	if !ok {
		t.Fatalf("queued message has type %T; want agentAutoCompactionStartedMsg", msg)
	}
	if started.currentTokens != 90_000 || started.threshold != 80_000 || started.contextLimit != 100_000 {
		t.Fatalf("status message carried %+v; want the update's token figures", started)
	}
}

func TestSubAgentCompactionNeededUpdateDoesNotScheduleExtraCompaction(t *testing.T) {
	app := &App{updateQueue: make(chan tea.Msg, 2)}

	app.queueAgentIntermediateUpdateWithContext(
		a2aExecutionSourceLocal,
		"conversation-1",
		agent.SubAgentUpdate{
			AgentID:   "subagent-1",
			AgentName: "worker",
			Update: agent.CompactionNeededUpdate{
				CurrentTokens: 90_000,
				Threshold:     80_000,
				ContextLimit:  100_000,
				PercentUsed:   0.9,
			},
		},
	)

	if got := len(app.updateQueue); got != 1 {
		t.Fatalf("SubAgentUpdate queued %d runtime messages; want only the sub-agent update", got)
	}
	if msg := <-app.updateQueue; func() bool {
		_, ok := msg.(agentSubAgentUpdateMsg)
		return ok
	}() == false {
		t.Fatalf("queued message has type %T; want agentSubAgentUpdateMsg", msg)
	}
}

func TestCompactionPressureThenFinalResponseDoesNotCompactUntilUserSubmits(t *testing.T) {
	app := NewApp()
	app.currentConvID = "conversation-1"
	app.streamingMessage = true
	app.streamingInProgress = true
	app.messages = []Message{{Role: "assistant"}}

	app.queueAgentIntermediateUpdateWithContext(
		a2aExecutionSourceLocal,
		app.currentConvID,
		agent.CompactionNeededUpdate{
			CurrentTokens: 90_000,
			Threshold:     80_000,
			ContextLimit:  100_000,
			PercentUsed:   0.9,
		},
	)
	app.updateQueue <- agentResponseMsg{
		content:        "final answer",
		conversationID: app.currentConvID,
	}

	_, cmd := app.Update(drainQueueMsg{})
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		if _, ok := msg.(commands.CompactRequestMsg); ok {
			t.Fatal("final agent response scheduled auto-compaction; want compaction to wait for the next user submission")
		}
	}
}
