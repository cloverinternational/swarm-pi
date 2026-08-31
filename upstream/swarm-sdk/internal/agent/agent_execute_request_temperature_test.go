package agent

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TestBuildProviderRequest_ForceTemperature locks in the determinism plumbing
// used by headless `-p` mode. A temperature of exactly 0.0 is treated as
// "unset" (omitempty) unless ForceTemperature is set, so without the force flag
// no temperature is sent and the provider falls back to its non-deterministic
// default. With ForceTemperature, temperature=0.0 must be sent so the provider
// decodes greedily and re-runs are reproducible.
func TestBuildProviderRequest_ForceTemperature(t *testing.T) {
	ctx := t.Context()

	newAgent := func(temp float64, force bool) *Agent {
		return &Agent{
			definition: &Definition{
				ID:       "temp-agent",
				Name:     "temp",
				Provider: "stub",
				Model:    "model-a",
				Capabilities: &Capabilities{
					MaxTokens:        1024,
					Temperature:      temp,
					ForceTemperature: force,
				},
			},
			toolReg: tools.NewRegistry(),
			logger:  noop.NewLogger(),
			tracer:  noop.NewTracer(),
			auditor: noop.NewAuditor(),
			ctx:     ctx,
		}
	}

	msgs := []*conversation.Message{{Role: conversation.RoleUser, Content: "hi"}}

	cases := []struct {
		name      string
		temp      float64
		force     bool
		wantSet   bool
		wantValue float64
	}{
		{"zero_unforced_omits", 0.0, false, false, 0},
		{"zero_forced_sends_zero", 0.0, true, true, 0.0},
		{"positive_unforced_sends", 0.7, false, true, 0.7},
		{"positive_forced_sends", 0.3, true, true, 0.3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newAgent(tc.temp, tc.force).buildProviderRequest(msgs)
			if tc.wantSet {
				if req.Temperature == nil {
					t.Fatalf("expected temperature=%v to be sent, got nil", tc.wantValue)
				}
				if *req.Temperature != tc.wantValue {
					t.Fatalf("expected temperature=%v, got %v", tc.wantValue, *req.Temperature)
				}
			} else if req.Temperature != nil {
				t.Fatalf("expected temperature to be omitted (nil), got %v", *req.Temperature)
			}
		})
	}
}
