package agent

import (
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TestBuildProviderRequest_RaceWithSetters exercises the locking discipline
// added to buildProviderRequest. The TUI's live-switch paths (profile swap,
// model picker, thinking toggle) call SetModel / SetSystemPrompt while the
// agent is mid-turn — and buildProviderRequest runs on every turn. Before
// the RLock wrapping, the reader raced against the setters under -race.
//
// The test hand-builds a minimal *Agent with just enough wiring for
// buildProviderRequest and its only helper buildProviderTools to run, then
// hammers the reader concurrently with setters. It passes under -race if
// there are no data races; the returned req values are intentionally
// discarded because we only care about the locking, not the output.
func TestBuildProviderRequest_RaceWithSetters(t *testing.T) {
	ctx := t.Context()

	ag := &Agent{
		definition: &Definition{
			ID:           "race-agent",
			Name:         "race",
			Provider:     "stub",
			Model:        "model-a",
			SystemPrompt: "prompt-a",
			Capabilities: &Capabilities{MaxTokens: 1024, Temperature: 0.5},
		},
		toolReg: tools.NewRegistry(),
		logger:  noop.NewLogger(),
		tracer:  noop.NewTracer(),
		auditor: noop.NewAuditor(),
		ctx:     ctx,
	}

	const iters = 200
	var wg sync.WaitGroup

	wg.Go(func() {
		msgs := []*conversation.Message{
			{Role: conversation.RoleUser, Content: "hi"},
		}
		for range iters {
			_ = ag.buildProviderRequest(msgs)
		}
	})

	wg.Go(func() {
		for i := range iters {
			if i%2 == 0 {
				ag.SetModel("model-b")
				ag.SetSystemPrompt("prompt-b")
			} else {
				ag.SetModel("model-a")
				ag.SetSystemPrompt("prompt-a")
			}
		}
	})

	wg.Wait()

	// Sanity: the request still reflects whatever the last write was.
	msgs := []*conversation.Message{{Role: conversation.RoleUser, Content: "hi"}}
	req := ag.buildProviderRequest(msgs)
	if req.Model != "model-a" && req.Model != "model-b" {
		t.Fatalf("Model = %q, expected one of the written values", req.Model)
	}
	if req.SystemPrompt != "prompt-a" && req.SystemPrompt != "prompt-b" {
		t.Fatalf("SystemPrompt = %q, expected one of the written values", req.SystemPrompt)
	}
}
