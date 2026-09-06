package autogenskills

import (
	"slices"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/agent/mocks"
)

func createTestAgent(t *testing.T) *agent.Agent {
	mockProv := mocks.NewMockProvider("mock")
	ag, err := agent.New(agent.Config{
		Definition:   &agent.Definition{ID: "test-1", Name: "test", Provider: "mock", Model: "test"},
		Provider:     mockProv,
		ToolRegistry: tools.NewRegistry(),
		Logger:       mocks.NewMockLogger(),
		Tracer:       mocks.NewMockTracer(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ag
}

// TestRegisterWithAgent_disabled returns nil for ModeNever.
func TestRegisterWithAgent_disabled(t *testing.T) {
	ag := createTestAgent(t)
	reg := skills.NewRegistry()
	svc, err := RegisterWithAgent(ag, DefaultConfig(), reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != nil {
		t.Error("expected nil service for disabled mode")
	}
}

// TestRegisterWithAgent_nilAgent rejects nil agent.
func TestRegisterWithAgent_nilAgent(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	_, err := RegisterWithAgent(nil, cfg, reg, nil)
	if err == nil {
		t.Error("expected error for nil agent")
	}
}

// TestRegisterWithAgent_nilRegistry rejects nil skills registry.
func TestRegisterWithAgent_nilRegistry(t *testing.T) {
	ag := createTestAgent(t)
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	_, err := RegisterWithAgent(ag, cfg, nil, nil)
	if err == nil {
		t.Error("expected error for nil registry")
	}
}

// TestRegisterWithAgent_success wires all components.
func TestRegisterWithAgent_success(t *testing.T) {
	ag := createTestAgent(t)
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}

	svc, err := RegisterWithAgent(ag, cfg, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service")
	}

	// Verify SkillManage tool was registered
	found := slices.Contains(ag.ToolRegistry().List(), "SkillManage")
	if !found {
		t.Error("SkillManage tool should be registered")
	}
}

// TestRegisterWithAgent_preservesExistingFn composes old + new.
func TestRegisterWithAgent_preservesExistingFn(t *testing.T) {
	ag := createTestAgent(t)
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{ToolCallThreshold: 1, NudgeInterval: 1}}

	// Existing fn returns static text
	existingFn := func([]*conversation.Message) string {
		return "existing-nudge"
	}

	svc, err := RegisterWithAgent(ag, cfg, reg, existingFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Trigger a nudge by doing a tool call
	svc.RunTurn()
	svc.HandleToolCall()
}

// TestJoinEphemeralParts verifies the helper.
func TestJoinEphemeralParts(t *testing.T) {
	result := joinEphemeralParts([]string{"a", "b", "c"})
	expected := "a\n\nb\n\nc"
	if result != expected {
		t.Errorf("joinEphemeralParts = %q, want %q", result, expected)
	}
}

// TestJoinEphemeralParts_single returns unchanged.
func TestJoinEphemeralParts_single(t *testing.T) {
	result := joinEphemeralParts([]string{"only"})
	if result != "only" {
		t.Errorf("joinEphemeralParts = %q, want only", result)
	}
}

// TestJoinEphemeralParts_empty returns empty.
func TestJoinEphemeralParts_empty(t *testing.T) {
	result := joinEphemeralParts([]string{})
	if result != "" {
		t.Errorf("joinEphemeralParts = %q, want empty", result)
	}
}
