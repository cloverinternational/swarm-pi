package autogenskills

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TestNudgeBuilder_BuildNudge_noThreshold returns empty when thresholds not met.
func TestNudgeBuilder_BuildNudge_noThreshold(t *testing.T) {
	b := NewNudgeBuilder()
	ctx := NudgeContext{TurnCount: 1, ToolCallCount: 1}
	cfg := Config{Mode: ModeAuto, Trigger: TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 1}}

	frag := b.BuildNudge(ctx, cfg)
	if !frag.IsZero() {
		t.Errorf("expected zero fragment, got: %q", frag.String())
	}
}

// TestNudgeBuilder_BuildNudge_thresholdMet returns non-empty when threshold met.
func TestNudgeBuilder_BuildNudge_thresholdMet(t *testing.T) {
	b := NewNudgeBuilder()
	ctx := NudgeContext{TurnCount: 5, ToolCallCount: 5}
	cfg := Config{Mode: ModeAuto, Trigger: TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 1}}

	frag := b.BuildNudge(ctx, cfg)
	if frag.IsZero() {
		t.Error("expected non-zero fragment when threshold met")
	}
	if !strings.Contains(frag.String(), "skill") {
		t.Error("fragment should mention skills")
	}
}

// TestNudgeBuilder_BuildNudge_respectsInterval blocks when interval not elapsed.
func TestNudgeBuilder_BuildNudge_respectsInterval(t *testing.T) {
	b := NewNudgeBuilder()
	ctx := NudgeContext{TurnCount: 5, ToolCallCount: 5, LastNudgeTurn: 4}
	cfg := Config{Mode: ModeAuto, Trigger: TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 5}}

	frag := b.BuildNudge(ctx, cfg)
	if !frag.IsZero() {
		t.Error("should not nudge — interval not elapsed")
	}
}

// TestNudgeBuilder_BuildNudge_includesExistingSkills lists skill names in fragment.
func TestNudgeBuilder_BuildNudge_includesExistingSkills(t *testing.T) {
	b := NewNudgeBuilder()
	ctx := NudgeContext{
		TurnCount:          5,
		ToolCallCount:      5,
		ExistingSkillNames: []string{"git-commit", "go-test"},
	}
	cfg := Config{Mode: ModeAuto, Trigger: TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 1}}

	frag := b.BuildNudge(ctx, cfg)
	if !strings.Contains(frag.String(), "git-commit") {
		t.Error("fragment should mention existing skill names")
	}
}

// TestNudgeBuilder_BuildThresholdNudge_specific includes threshold name.
func TestNudgeBuilder_BuildThresholdNudge_specific(t *testing.T) {
	b := NewNudgeBuilder()
	ctx := NudgeContext{TurnCount: 5, ToolCallCount: 5}
	cfg := Config{Mode: ModeAuto, Trigger: TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 1}}

	frag := b.BuildThresholdNudge(ctx, cfg, "tool_call_threshold")
	if frag.IsZero() {
		t.Error("expected non-zero fragment")
	}
	if !strings.Contains(frag.String(), "tool_call_threshold") {
		t.Error("fragment should mention threshold name")
	}
}

// TestNudgeBuilder_BuildPostErrorNudge_noResolution returns empty if no errors resolved.
func TestNudgeBuilder_BuildPostErrorNudge_noResolution(t *testing.T) {
	b := NewNudgeBuilder()
	ctx := NudgeContext{ErrorResolvedCount: 0}
	cfg := Config{Mode: ModeAuto, Trigger: TriggerConfig{ErrorResolutionThreshold: 3, NudgeInterval: 1}}

	frag := b.BuildPostErrorNudge(ctx, cfg, "connection timeout")
	if !frag.IsZero() {
		t.Error("should not nudge without resolved errors")
	}
}

// TestNudgeBuilder_BuildPostErrorNudge_thresholdMet nudges after enough resolutions.
func TestNudgeBuilder_BuildPostErrorNudge_thresholdMet(t *testing.T) {
	b := NewNudgeBuilder()
	ctx := NudgeContext{ErrorResolvedCount: 3, TurnCount: 10}
	cfg := Config{Mode: ModeAuto, Trigger: TriggerConfig{ErrorResolutionThreshold: 3, NudgeInterval: 1}}

	frag := b.BuildPostErrorNudge(ctx, cfg, "connection timeout")
	if frag.IsZero() {
		t.Error("expected non-zero fragment when error threshold met")
	}
	if !strings.Contains(frag.String(), "connection timeout") {
		t.Error("fragment should mention error pattern")
	}
}

// TestNudgeBuilder_fragmentUnderLimit ensures all nudge types stay under 2048 chars.
func TestNudgeBuilder_fragmentUnderLimit(t *testing.T) {
	b := NewNudgeBuilder()
	ctx := NudgeContext{
		TurnCount:          5,
		ToolCallCount:      5,
		ExistingSkillNames: []string{"skill-one", "skill-two"},
	}
	cfg := Config{Mode: ModeAuto, Trigger: TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 1}}

	fragments := []NudgeFragment{
		b.BuildNudge(ctx, cfg),
		b.BuildThresholdNudge(ctx, cfg, "tool_call_threshold"),
	}

	for _, frag := range fragments {
		if err := frag.Validate(); err != nil {
			t.Errorf("fragment exceeds limit: %v", err)
		}
	}
}

// TestBuildNudgeFn_returnsString verifies closure returns wrapped nudge.
func TestBuildNudgeFn_returnsString(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{ToolCallThreshold: 1, NudgeInterval: 1}}
	m := &Metrics{}
	svc, _ := NewService(cfg, reg, m)

	// Advance to threshold
	svc.RunTurn()
	svc.HandleToolCall()

	fn := BuildNudgeFn(svc, nil)
	result := fn(nil)
	if result == "" {
		t.Fatal("expected non-empty nudge string")
	}
	if !strings.Contains(result, "<system-reminder>") {
		t.Error("should wrap in system-reminder tags")
	}
	if !strings.Contains(result, "Consider creating a skill") {
		t.Error("should contain nudge text")
	}
}

// TestBuildNudgeFn_zeroWhenNoNudge returns empty when threshold not met.
func TestBuildNudgeFn_zeroWhenNoNudge(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{ToolCallThreshold: 5, NudgeInterval: 1}}
	m := &Metrics{}
	svc, _ := NewService(cfg, reg, m)

	fn := BuildNudgeFn(svc, nil)
	result := fn(nil)
	if result != "" {
		t.Errorf("expected empty, got: %q", result)
	}
}

// TestBuildNudgeFnWithRegistry_listsSkillNames.
func TestBuildNudgeFnWithRegistry(t *testing.T) {
	reg := skills.NewRegistry()
	// Pre-register a skill
	reg.RegisterSkill(&skills.Skill{Metadata: skills.SkillMetadata{Name: "preloaded"}}, true)

	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{ToolCallThreshold: 1, NudgeInterval: 1}}
	m := &Metrics{}
	svc, _ := NewService(cfg, reg, m)

	svc.RunTurn()
	svc.HandleToolCall()

	fn := BuildNudgeFnWithRegistry(svc, reg)
	result := fn(nil)
	if !strings.Contains(result, "preloaded") {
		t.Error("nudge should mention existing skill names from registry")
	}
}
