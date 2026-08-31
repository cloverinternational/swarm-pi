package autogenskills

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TestNewService_disabled returns no-op service for ModeNever.
func TestNewService_disabled(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig()
	svc, err := NewService(cfg, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	frag := svc.RunTurn()
	if !frag.IsZero() {
		t.Error("disabled service should never nudge")
	}
}

// TestNewService_manual creates service but does not auto-nudge.
func TestNewService_manual(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	m := &Metrics{}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Run many turns
	for range 10 {
		svc.RunTurn()
		svc.HandleToolCall()
	}

	// Manual mode should not generate nudges
	frag := svc.GetNudgeFragment(nil)
	if !frag.IsZero() {
		t.Error("manual mode should not generate nudges")
	}
}

// TestNewService_auto_nudge generates nudges when threshold met.
func TestNewService_auto_nudge(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{
		Mode: ModeAuto,
		Trigger: TriggerConfig{
			ToolCallThreshold: 3,
			NudgeInterval:     1,
		},
		AutogenDir: t.TempDir(),
	}
	m := &Metrics{}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No nudge before threshold
	for range 2 {
		svc.RunTurn()
		svc.HandleToolCall()
	}
	frag := svc.RunTurn()
	if !frag.IsZero() {
		t.Error("should not nudge before threshold")
	}

	// Third tool call should trigger nudge on next turn
	svc.HandleToolCall()
	frag = svc.RunTurn()
	if frag.IsZero() {
		t.Error("expected nudge after threshold")
	}
}

// TestNewService_auto_respectsInterval blocks nudges within interval.
func TestNewService_auto_respectsInterval(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{
		Mode: ModeAuto,
		Trigger: TriggerConfig{
			ToolCallThreshold: 3,
			NudgeInterval:     5,
		},
		AutogenDir: t.TempDir(),
	}
	m := &Metrics{}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Trigger first nudge
	for range 3 {
		svc.RunTurn()
		svc.HandleToolCall()
	}
	frag := svc.RunTurn()
	if frag.IsZero() {
		t.Error("expected first nudge")
	}

	// Immediate next turn should not nudge
	for range 3 {
		svc.RunTurn()
		svc.HandleToolCall()
	}
	frag = svc.RunTurn()
	if !frag.IsZero() {
		t.Error("should not nudge — interval not elapsed")
	}
}

// TestService_CreateSkill_manual creates skills in manual mode.
func TestService_CreateSkill_manual(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	opts := CreateOptions{
		Name:          "manual-skill",
		Description:   "Test",
		Instructions:  "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
		TriggerReason: TriggerManual,
	}

	result := svc.CreateSkill(opts)
	if !result.IsSuccess() {
		t.Fatalf("expected success, got: %v", result.Error)
	}
}

// TestService_CreateSkill_disabled fails when service is disabled.
func TestService_CreateSkill_disabled(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig()
	svc, _ := NewService(cfg, reg, nil)

	opts := CreateOptions{
		Name:          "disabled-skill",
		Description:   "Test",
		Instructions:  "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
		TriggerReason: TriggerManual,
	}

	result := svc.CreateSkill(opts)
	if result.IsSuccess() {
		t.Error("expected failure for disabled service")
	}
	if !errors.Is(result.Error, ErrDisabled) {
		t.Errorf("expected ErrDisabled, got: %v", result.Error)
	}
}

// TestService_Snapshot returns metrics.
func TestService_Snapshot(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	m := &Metrics{}
	svc, _ := NewService(cfg, reg, m)

	svc.HandleToolCall()
	svc.HandleError()
	svc.RunTurn()

	snap := svc.Snapshot()
	if snap.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", snap.ToolCallCount)
	}
	if snap.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", snap.ErrorCount)
	}
	if snap.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", snap.TurnCount)
	}
}

// TestService_GetNudgeFragment_auto returns non-empty for auto mode.
func TestService_GetNudgeFragment_auto(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{
		Mode: ModeAuto,
		Trigger: TriggerConfig{
			ToolCallThreshold: 1,
			NudgeInterval:     1,
		},
		AutogenDir: t.TempDir(),
	}
	m := &Metrics{}
	svc, _ := NewService(cfg, reg, m)

	svc.RunTurn()
	svc.HandleToolCall()

	frag := svc.GetNudgeFragment([]string{"existing-skill"})
	if frag.IsZero() {
		t.Error("expected nudge fragment in auto mode")
	}
}

// TestService_GetNudgeFragment_never returns empty for never mode.
func TestService_GetNudgeFragment_never(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig()
	svc, _ := NewService(cfg, reg, nil)

	frag := svc.GetNudgeFragment(nil)
	if !frag.IsZero() {
		t.Error("never mode should not generate fragments")
	}
}

// TestService_ViewSkill_found returns skill when it exists.
func TestService_ViewSkill_found(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	// Create a skill via factory
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "view-service-skill",
		Description:   "Service view test",
		Instructions:  strings.Repeat("Service view test instructions. ", 10),
		TriggerReason: TriggerManual,
	})

	skill, err := svc.ViewSkill("view-service-skill")
	if err != nil {
		t.Fatalf("ViewSkill returned error: %v", err)
	}
	if skill.Metadata.Name != "view-service-skill" {
		t.Errorf("skill name = %q, want view-service-skill", skill.Metadata.Name)
	}
}

// TestService_ViewSkill_notFound returns error when skill does not exist.
func TestService_ViewSkill_notFound(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	_, err := svc.ViewSkill("no-such-skill")
	if err == nil {
		t.Error("expected error for non-existent skill")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

// TestService_ViewSkill_disabled returns ErrDisabled for disabled service.
func TestService_ViewSkill_disabled(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig()
	svc, _ := NewService(cfg, reg, nil)

	_, err := svc.ViewSkill("any-skill")
	if !errors.Is(err, ErrDisabled) {
		t.Errorf("expected ErrDisabled, got: %v", err)
	}
}

// TestService_ListSkills returns only autogen skills.
func TestService_ListSkills(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	// Create a skill via factory (source="autogen")
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "list-service-skill",
		Description:   "Service list test",
		Instructions:  strings.Repeat("Service list test instructions. ", 10),
		TriggerReason: TriggerManual,
	})

	skillList := svc.ListSkills()
	if len(skillList) == 0 {
		t.Error("expected at least one autogen skill")
	}
	found := false
	for _, sk := range skillList {
		if sk.Metadata.Name == "list-service-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Error("list-service-skill not found in ListSkills result")
	}
}

// TestService_ListSkills_disabled returns nil for disabled service.
func TestService_ListSkills_disabled(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig()
	svc, _ := NewService(cfg, reg, nil)

	result := svc.ListSkills()
	if result != nil {
		t.Errorf("expected nil for disabled service, got %v", result)
	}
}

// TestService_ListSkillNames returns just the names.
func TestService_ListSkillNames(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "names-test-skill",
		Description:   "Names test",
		Instructions:  strings.Repeat("Names test instructions. ", 10),
		TriggerReason: TriggerManual,
	})

	names := svc.ListSkillNames()
	if len(names) == 0 {
		t.Error("expected at least one skill name")
	}
	if names[0] != "names-test-skill" {
		t.Errorf("skill name = %q, want names-test-skill", names[0])
	}
}

// TestService_concurrentSafety runs service methods from many goroutines.
func TestService_concurrentSafety(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{NudgeInterval: 1}}
	m := &Metrics{}
	svc, _ := NewService(cfg, reg, m)

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			for range 10 {
				svc.RunTurn()
				svc.HandleToolCall()
				svc.HandleError()
				svc.HandleErrorResolved()
			}
		})
	}
	wg.Wait()

	snap := svc.Snapshot()
	if snap.TurnCount != 1000 {
		t.Errorf("TurnCount = %d, want 1000", snap.TurnCount)
	}
	if snap.ToolCallCount != 1000 {
		t.Errorf("ToolCallCount = %d, want 1000", snap.ToolCallCount)
	}
}
