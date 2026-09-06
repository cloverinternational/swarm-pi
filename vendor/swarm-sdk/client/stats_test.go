package client

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// TestTokenUsage_ZeroBeforeAnyTurn confirms the counters start at zero.
func TestTokenUsage_ZeroBeforeAnyTurn(t *testing.T) {
	c := &Client{}
	in, out := c.TokenUsage()
	if in != 0 || out != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", in, out)
	}
}

// TestTokenUsage_UpdatedFromFanout feeds a TokenCountUpdate through fanout
// and confirms TokenUsage reflects the most recent values.
func TestTokenUsage_UpdatedFromFanout(t *testing.T) {
	c := &Client{}
	ctx := context.Background()

	if err := c.fanout(ctx, agent.TokenCountUpdate{InputTokens: 100, OutputTokens: 25, Turn: 1}); err != nil {
		t.Fatalf("fanout 1: %v", err)
	}
	in, out := c.TokenUsage()
	if in != 100 || out != 25 {
		t.Errorf("after turn 1: got (%d,%d), want (100,25)", in, out)
	}

	// Subsequent updates overwrite (not accumulate), matching the semantics
	// documented on TokenUsage.
	if err := c.fanout(ctx, agent.TokenCountUpdate{InputTokens: 180, OutputTokens: 40, Turn: 2}); err != nil {
		t.Fatalf("fanout 2: %v", err)
	}
	in, out = c.TokenUsage()
	if in != 180 || out != 40 {
		t.Errorf("after turn 2: got (%d,%d), want (180,40)", in, out)
	}
}

// TestCacheStats_EmptyStorageReturnsZero confirms CacheStats does not panic
// when convStorage is nil (before any agent wiring).
func TestCacheStats_EmptyStorageReturnsZero(t *testing.T) {
	c := &Client{}
	creation, read := c.CacheStats()
	if creation != 0 || read != 0 {
		t.Errorf("expected (0,0) for nil storage, got (%d,%d)", creation, read)
	}
}

// TestTokenUsage_NoDataRaceUnderConcurrentFanout runs multiple goroutines
// pushing token updates through fanout and reading them back. Requires
// -race to be meaningful.
func TestTokenUsage_NoDataRaceUnderConcurrentFanout(t *testing.T) {
	c := &Client{}
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		for i := range 200 {
			_ = c.fanout(ctx, agent.TokenCountUpdate{InputTokens: i, OutputTokens: i / 2})
		}
		close(done)
	}()
	for range 200 {
		_, _ = c.TokenUsage()
	}
	<-done
}
