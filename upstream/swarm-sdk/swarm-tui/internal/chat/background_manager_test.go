package chat

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

func TestMarkCompleteDrainsBufferedUpdatesBeforeClosingSubscribers(t *testing.T) {
	manager := &BackgroundAgentManager{
		agents: make(map[string]*RunningAgent),
	}

	convID := "conv-test"
	source := make(chan agent.IntermediateUpdate, 4)
	sub := make(chan agent.IntermediateUpdate, 4)
	ra := &RunningAgent{
		ConvID:       convID,
		Status:       AgentStatusRunning,
		StartedAt:    time.Now(),
		LastUpdate:   time.Now(),
		sourceUpdate: source,
		subscribers: map[string]chan agent.IntermediateUpdate{
			"ui": sub,
		},
		updateBuffer: make([]agent.IntermediateUpdate, 0),
	}
	manager.agents[convID] = ra

	source <- agent.ContentUpdate{Content: "In", Append: false}
	source <- agent.ContentUpdate{Content: " a town", Append: true}

	// Complete first, then run fan-out to deterministically validate channel lifecycle.
	manager.MarkComplete(convID, "", nil, nil)

	done := make(chan struct{})
	go func() {
		manager.fanOutUpdates(ra)
		close(done)
	}()

	var updates []agent.ContentUpdate
	timeout := time.After(2 * time.Second)
	for {
		select {
		case update, ok := <-sub:
			if !ok {
				goto drained
			}
			contentUpdate, ok := update.(agent.ContentUpdate)
			if !ok {
				t.Fatalf("unexpected update type: %T", update)
			}
			updates = append(updates, contentUpdate)
		case <-timeout:
			t.Fatal("timed out waiting for subscriber channel to drain and close")
		}
	}

drained:
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fan-out goroutine to finish")
	}

	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	if updates[0].Content != "In" || updates[0].Append {
		t.Fatalf("unexpected first update: content=%q append=%v", updates[0].Content, updates[0].Append)
	}
	if updates[1].Content != " a town" || !updates[1].Append {
		t.Fatalf("unexpected second update: content=%q append=%v", updates[1].Content, updates[1].Append)
	}
}
