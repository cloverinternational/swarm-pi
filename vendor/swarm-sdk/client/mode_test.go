package client

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
)

// TestWithOperatingMode_OptionSetsField confirms the option wires the
// OperatingMode into the options struct so New() will see it.
func TestWithOperatingMode_OptionSetsField(t *testing.T) {
	m := &mode.OperatingMode{ID: "PLAN", Name: "PLAN"}
	o := options{}
	WithOperatingMode(m)(&o)
	if o.operatingMode != m {
		t.Errorf("operatingMode: got %v, want %v", o.operatingMode, m)
	}
}

// TestSetOperatingMode_RoundTrips confirms SetOperatingMode and
// OperatingMode agree on what's currently active, including the nil path.
func TestSetOperatingMode_RoundTrips(t *testing.T) {
	c := newTestClient()
	if c.OperatingMode() != nil {
		t.Fatalf("expected nil mode on fresh client, got %+v", c.OperatingMode())
	}

	m := &mode.OperatingMode{ID: "ACT", Name: "ACT"}
	c.SetOperatingMode(m)
	if got := c.OperatingMode(); got != m {
		t.Errorf("after Set: got %v want %v", got, m)
	}

	c.SetOperatingMode(nil)
	if c.OperatingMode() != nil {
		t.Errorf("after Set(nil): expected nil, got %+v", c.OperatingMode())
	}
}

// TestInjectModeFilter_AddsConfigToContext verifies injectModeFilter installs
// a *agent.ModeFilterConfig in req.Context["mode_filter"] reflecting the
// active mode's allow/block lists.
func TestInjectModeFilter_AddsConfigToContext(t *testing.T) {
	c := newTestClient()
	c.SetOperatingMode(&mode.OperatingMode{
		ID:                "PLAN",
		Name:              "PLAN",
		AllowedTools:      []string{"file_read", "file_*"},
		BlockedTools:      []string{"bash", "file_write"},
		HideBlockedTools:  true,
		SystemInstruction: "You are in PLAN mode.",
	})

	var req agent.ExecuteRequest
	c.injectModeFilter(&req)

	raw, ok := req.Context["mode_filter"]
	if !ok {
		t.Fatalf("expected mode_filter key in req.Context, got %+v", req.Context)
	}
	cfg, ok := raw.(*agent.ModeFilterConfig)
	if !ok {
		t.Fatalf("mode_filter: got %T, want *agent.ModeFilterConfig", raw)
	}
	if cfg.ModeName != "PLAN" {
		t.Errorf("ModeName: got %q want PLAN", cfg.ModeName)
	}
	if len(cfg.AllowedTools) != 2 || cfg.AllowedTools[0] != "file_read" {
		t.Errorf("AllowedTools: got %v", cfg.AllowedTools)
	}
	if len(cfg.BlockedTools) != 2 || cfg.BlockedTools[1] != "file_write" {
		t.Errorf("BlockedTools: got %v", cfg.BlockedTools)
	}
	if !cfg.HideBlockedTools {
		t.Errorf("HideBlockedTools: expected true")
	}
}

// TestInjectModeFilter_NilModeLeavesRequestUntouched confirms a client with
// no active mode performs no mutation on the request.
func TestInjectModeFilter_NilModeLeavesRequestUntouched(t *testing.T) {
	c := newTestClient()
	req := agent.ExecuteRequest{Message: "hi"}
	c.injectModeFilter(&req)
	if req.Context != nil {
		t.Errorf("expected nil Context, got %+v", req.Context)
	}
}

// TestInjectModeFilter_RespectsExistingFilter confirms a caller-supplied
// mode_filter takes precedence over the Client's active mode. This gives
// advanced callers an escape hatch without having to clear SetOperatingMode.
func TestInjectModeFilter_RespectsExistingFilter(t *testing.T) {
	c := newTestClient()
	c.SetOperatingMode(&mode.OperatingMode{
		ID:           "PLAN",
		Name:         "PLAN",
		AllowedTools: []string{"*"},
	})

	custom := &agent.ModeFilterConfig{
		ModeName:     "CUSTOM",
		AllowedTools: []string{"only_this"},
	}
	req := agent.ExecuteRequest{Context: map[string]any{"mode_filter": custom}}
	c.injectModeFilter(&req)

	got, ok := req.Context["mode_filter"].(*agent.ModeFilterConfig)
	if !ok || got.ModeName != "CUSTOM" {
		t.Errorf("caller's mode_filter should win; got %+v", req.Context["mode_filter"])
	}
}

// TestInjectModeFilter_CopiesSlices verifies that mutating the client's mode
// after injection does not mutate the ModeFilterConfig already handed to the
// request (so in-flight calls aren't affected by a runtime mode switch).
func TestInjectModeFilter_CopiesSlices(t *testing.T) {
	c := newTestClient()
	original := &mode.OperatingMode{
		ID:           "PLAN",
		Name:         "PLAN",
		AllowedTools: []string{"one", "two"},
		BlockedTools: []string{"three"},
	}
	c.SetOperatingMode(original)

	var req agent.ExecuteRequest
	c.injectModeFilter(&req)
	cfg := req.Context["mode_filter"].(*agent.ModeFilterConfig)

	// Mutating the original's slices must not leak into cfg.
	original.AllowedTools[0] = "MUTATED"
	original.BlockedTools = append(original.BlockedTools, "four")

	if cfg.AllowedTools[0] != "one" {
		t.Errorf("cfg.AllowedTools[0] leaked: got %q", cfg.AllowedTools[0])
	}
	if len(cfg.BlockedTools) != 1 {
		t.Errorf("cfg.BlockedTools grew from a mutation after injection: %v", cfg.BlockedTools)
	}
}
