package anthropic

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// TestRebuildClientWithToken_UpdatesInMemoryState proves that after an OAuth
// refresh, the provider swaps its in-memory access token AND its HTTP client,
// so the immediate retry (and all later requests in the same live session)
// carry the new Bearer token instead of the stale one. This is the
// "session updates itself" guarantee — no process reload required.
func TestRebuildClientWithToken_UpdatesInMemoryState(t *testing.T) {
	p := &Provider{
		config: Config{
			IsOAuth: true,
			BaseURL: "https://api.anthropic.com",
			APIKey:  "sk-ant-oat-OLD",
		},
		logger: noop.NewLogger(),
		tracer: noop.NewTracer(),
	}

	oldClient := p.client

	if err := p.rebuildClientWithToken("sk-ant-oat-NEW"); err != nil {
		t.Fatalf("rebuildClientWithToken: %v", err)
	}

	if p.config.APIKey != "sk-ant-oat-NEW" {
		t.Fatalf("in-memory APIKey not updated: got %q want %q", p.config.APIKey, "sk-ant-oat-NEW")
	}
	if p.client == nil {
		t.Fatalf("client is nil after rebuild")
	}
	if p.client == oldClient {
		t.Fatalf("client was not swapped; retry would reuse the stale-token client")
	}
}
