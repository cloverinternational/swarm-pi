package autogenskills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/agent/mocks"
)

// TestEndToEnd_SkillCreationFlow tests the complete autogenskills lifecycle.
func TestEndToEnd_SkillCreationFlow(t *testing.T) {
	// Setup test directory
	testDir := t.TempDir()

	// Create agent with autogenskills
	ag := createAgentWithAutogen(t, testDir)

	// Phase 1: Verify SkillManage tool is registered
	toolReg := ag.ToolRegistry()
	skillManage, err := toolReg.Get("SkillManage")
	if err != nil {
		t.Fatalf("SkillManage tool not registered: %v", err)
	}

	// Phase 2: Execute SkillManage tool to create a skill
	result, err := skillManage.Execute(context.Background(), map[string]any{
		"action":       "create",
		"name":         "test-e2e-skill",
		"description":  "End-to-end test skill for file operations",
		"instructions": strings.Repeat("Use read and write tools to manipulate files. Always check file existence before reading. ", 10),
		"tags":         "test, e2e",
		"category":     "testing",
	})
	if err != nil {
		t.Fatalf("SkillManage tool execution failed: %v", err)
	}

	t.Logf("Tool result: %+v", result)

	// Phase 3: Verify skill was created on disk
	skillPath := filepath.Join(testDir, "test-e2e-skill", "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("skill file not created: %v", err)
	}

	if !strings.Contains(string(content), "test-e2e-skill") {
		t.Error("skill file doesn't contain expected name")
	}
	if !strings.Contains(string(content), "read and write tools") {
		t.Error("skill file doesn't contain expected instructions")
	}

	t.Logf("✓ Skill created at %s (%d bytes)", skillPath, len(content))
}

// TestEndToEnd_HookReceivesEvents verifies the lifecycle hook receives tool events.
func TestEndToEnd_HookReceivesEvents(t *testing.T) {
	testDir := t.TempDir()

	// Create service and hook with metrics
	cfg := Config{
		Mode:       ModeAuto,
		Trigger:    TriggerConfig{ToolCallThreshold: 3, NudgeInterval: 1},
		AutogenDir: testDir,
	}
	reg := skills.NewRegistry()
	metrics := &Metrics{}
	svc, err := NewService(cfg, reg, metrics)
	if err != nil {
		t.Fatal(err)
	}

	hook, err := NewLifecycleHook(svc)
	if err != nil {
		t.Fatal(err)
	}

	// Verify hook name
	if hook.Name() != "autogenskills" {
		t.Errorf("expected hook name 'autogenskills', got %q", hook.Name())
	}

	// Verify filter only accepts tool events
	if !hook.Filter(hooks.Event{Type: hooks.EventToolAfterExecute}) {
		t.Error("should accept tool.after_execute")
	}
	if !hook.Filter(hooks.Event{Type: hooks.EventToolExecutionFailed}) {
		t.Error("should accept tool.execution_failed")
	}
	if hook.Filter(hooks.Event{Type: "session.start"}) {
		t.Error("should reject session.start")
	}

	// Simulate tool after-execute event
	ctx := context.Background()
	result, err := hook.OnEvent(ctx, hooks.Event{Type: hooks.EventToolAfterExecute})
	if err != nil {
		t.Fatalf("hook.OnEvent failed: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Error("expected ActionContinue")
	}

	// Verify metrics incremented
	snap := svc.Snapshot()
	if snap.ToolCallCount != 1 {
		t.Errorf("expected 1 tool call, got %d", snap.ToolCallCount)
	}

	// Simulate error event
	_, _ = hook.OnEvent(ctx, hooks.Event{Type: hooks.EventToolExecutionFailed})
	snap = svc.Snapshot()
	if snap.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", snap.ErrorCount)
	}
}

// TestEndToEnd_MultipleTurnsAndNudgeInterval verifies nudge interval is respected.
func TestEndToEnd_MultipleTurnsAndNudgeInterval(t *testing.T) {
	testDir := t.TempDir()

	cfg := Config{
		Mode:       ModeAuto,
		Trigger:    TriggerConfig{ToolCallThreshold: 3, NudgeInterval: 3},
		AutogenDir: testDir,
	}
	reg := skills.NewRegistry()
	metrics := &Metrics{}
	svc, err := NewService(cfg, reg, metrics)
	if err != nil {
		t.Fatal(err)
	}

	// Turn 1: No nudge (threshold not met)
	svc.RunTurn()
	svc.HandleToolCall()
	nudge := svc.RunTurn()
	if !nudge.IsZero() {
		t.Errorf("expected no nudge on turn 1, got: %s", nudge)
	}

	// Turn 2: Still no nudge (threshold not met)
	svc.RunTurn()
	svc.HandleToolCall()
	nudge = svc.RunTurn()
	if !nudge.IsZero() {
		t.Errorf("expected no nudge on turn 2, got: %s", nudge)
	}

	// Turn 3: Threshold met — should nudge
	svc.RunTurn()
	svc.HandleToolCall()
	nudge = svc.RunTurn()
	if nudge.IsZero() {
		t.Error("expected nudge on turn 3 (threshold met), got empty")
	}

	firstNudge := nudge

	// Turn 4: Should NOT nudge (interval hasn't passed)
	svc.RunTurn()
	svc.HandleToolCall()
	nudge = svc.RunTurn()
	if !nudge.IsZero() {
		t.Errorf("expected no nudge on turn 4 (interval blocking), got: %s", nudge)
	}

	// Turn 5: Still no nudge
	svc.RunTurn()
	svc.HandleToolCall()
	nudge = svc.RunTurn()
	if !nudge.IsZero() {
		t.Errorf("expected no nudge on turn 5 (interval blocking), got: %s", nudge)
	}

	// Turn 6: Interval passed — should nudge again
	svc.RunTurn()
	svc.HandleToolCall()
	nudge = svc.RunTurn()
	if nudge.IsZero() {
		t.Error("expected nudge on turn 6 (interval passed), got empty")
	}
	if nudge != firstNudge {
		t.Logf("Nudge repeated (expected identical): %s", nudge)
	}
}

// TestEndToEnd_NudgeGenerationWithExistingSkills verifies nudges list existing skills.
func TestEndToEnd_NudgeGenerationWithExistingSkills(t *testing.T) {
	testDir := t.TempDir()

	cfg := Config{
		Mode:       ModeAuto,
		Trigger:    TriggerConfig{ToolCallThreshold: 1, NudgeInterval: 1},
		AutogenDir: testDir,
	}
	reg := skills.NewRegistry()

	// Create a skill first so it appears in nudges
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "existing-skill",
		Description:   "Already exists",
		Instructions:  strings.Repeat("Test instructions for existing skill. ", 10),
		TriggerReason: TriggerManual,
	})

	metrics := &Metrics{}
	svc, err := NewService(cfg, reg, metrics)
	if err != nil {
		t.Fatal(err)
	}

	// Reach threshold
	svc.RunTurn()
	svc.HandleToolCall()

	// Build nudge with registry (should list existing skill)
	nudgeFn := BuildNudgeFnWithRegistry(svc, reg)
	nudge := nudgeFn(nil)

	if nudge == "" {
		t.Fatal("expected nudge with existing skills")
	}

	if !strings.Contains(nudge, "existing-skill") {
		t.Errorf("expected nudge to mention 'existing-skill', got: %s", nudge)
	}

	t.Logf("Nudge with existing skills: %s", nudge)
}

// Helper functions

func createAgentWithAutogen(t *testing.T, testDir string) *agent.Agent {
	mockProv := mocks.NewMockProvider("mock")
	ag, err := agent.New(agent.Config{
		Definition:   &agent.Definition{ID: "e2e-1", Name: "e2e", Provider: "mock", Model: "test"},
		Provider:     mockProv,
		ToolRegistry: tools.NewRegistry(),
		Logger:       mocks.NewMockLogger(),
		Tracer:       mocks.NewMockTracer(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wire autogenskills
	cfg := Config{
		Mode:       ModeAuto,
		Trigger:    TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 3},
		AutogenDir: testDir,
	}
	reg := skills.NewRegistry()
	_, err = RegisterWithAgent(ag, cfg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	return ag
}
