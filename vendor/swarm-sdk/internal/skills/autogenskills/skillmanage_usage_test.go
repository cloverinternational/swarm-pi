package autogenskills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

func TestSkillManagePreviewDeniesWritesAndDoesNotBumpUsage(t *testing.T) {
	svc, curator, _ := newServiceWithCurator(t)
	res := svc.CreateSkill(CreateOptions{
		Name:          "preview-only",
		Description:   "preview target",
		Instructions:  strings.Repeat("read-only preview instructions. ", 10),
		TriggerReason: TriggerLLMNudge,
	})
	if res.Error != nil {
		t.Fatalf("create: %v", res.Error)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	curator.mu.Lock()
	curator.state.SkillStates["preview-only"] = SkillMeta{State: SkillStateStale, LastUsedAt: old, CreatedAt: old}
	curator.mu.Unlock()

	tool, err := NewPreviewSkillManageTool(svc)
	if err != nil {
		t.Fatal(err)
	}
	view, err := tool.Execute(context.Background(), map[string]any{"action": "view", "name": "preview-only"})
	if err != nil || !strings.Contains(view.Output, "preview-only") {
		t.Fatalf("preview view failed: output=%v err=%v", view, err)
	}
	denied, err := tool.Execute(context.Background(), map[string]any{
		"action": "patch", "name": "preview-only", "instructions": "must not be written",
	})
	if err != nil || !strings.Contains(denied.Output, "read-only") {
		t.Fatalf("preview patch not denied: output=%v err=%v", denied, err)
	}
	meta := curator.GetState().SkillStates["preview-only"]
	if !meta.LastUsedAt.Equal(old) || meta.State != SkillStateStale {
		t.Fatalf("preview changed lifecycle metadata: %+v", meta)
	}
}

// TestSkillManageViewMarksSkillUsed mirrors Hermes semantics
// (tools/skills_tool.py _skill_view_with_bump): viewing a skill through the
// tool layer counts as a use and resets its stale/archive clock. Before this
// fix only the main loop's LifecycleHook marked usage — the curator
// sub-agent's views never did, so the same skills were re-flagged stale on
// every curator run forever.
func TestSkillManageViewMarksSkillUsed(t *testing.T) {
	svc, curator, dir := newServiceWithCurator(t)

	res := svc.CreateSkill(CreateOptions{
		Name:          "usage-tracked",
		Description:   "d",
		Instructions:  "this is a sufficiently long instruction body for the create validation to accept without complaint. it keeps going to be safe and repeats itself to be safe and long enough for the two hundred char floor.",
		TriggerReason: TriggerLLMNudge,
	})
	if res.Error != nil {
		t.Fatalf("create: %v", res.Error)
	}

	// Backdate the skill to stale.
	old := time.Now().Add(-40 * 24 * time.Hour)
	curator.mu.Lock()
	curator.state.SkillStates["usage-tracked"] = SkillMeta{
		State:      SkillStateStale,
		LastUsedAt: old,
		CreatedAt:  old,
	}
	curator.mu.Unlock()

	tool, err := NewSkillManageTool(svc)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(context.Background(), map[string]any{
		"action": "view",
		"name":   "usage-tracked",
	})
	if err != nil || out == nil {
		t.Fatalf("view: %v", err)
	}

	meta := curator.GetState().SkillStates["usage-tracked"]
	if !meta.LastUsedAt.After(old) {
		t.Errorf("view did not refresh LastUsedAt: %v", meta.LastUsedAt)
	}
	if meta.State != SkillStateActive {
		t.Errorf("view did not revive stale skill: state=%s", meta.State)
	}
	_ = dir
}

// TestSkillManagePatchMarksSkillUsed: patching is activity too (Hermes
// bump_patch feeds latest_activity_at).
func TestSkillManagePatchMarksSkillUsed(t *testing.T) {
	svc, curator, _ := newServiceWithCurator(t)

	res := svc.CreateSkill(CreateOptions{
		Name:          "patch-tracked",
		Description:   "d",
		Instructions:  "this is a sufficiently long instruction body for the create validation to accept without complaint. it keeps going to be safe and repeats itself to be safe and long enough for the two hundred char floor.",
		TriggerReason: TriggerLLMNudge,
	})
	if res.Error != nil {
		t.Fatalf("create: %v", res.Error)
	}

	old := time.Now().Add(-40 * 24 * time.Hour)
	curator.mu.Lock()
	curator.state.SkillStates["patch-tracked"] = SkillMeta{
		State:      SkillStateStale,
		LastUsedAt: old,
		CreatedAt:  old,
	}
	curator.mu.Unlock()

	tool, err := NewSkillManageTool(svc)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := svc.CurrentRevision(context.Background(), "patch-tracked")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{
		"action":            "patch",
		"name":              "patch-tracked",
		"instructions":      "updated body content",
		"expected_revision": revision,
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}

	meta := curator.GetState().SkillStates["patch-tracked"]
	if !meta.LastUsedAt.After(old) {
		t.Errorf("patch did not refresh LastUsedAt: %v", meta.LastUsedAt)
	}
}

func newServiceWithCurator(t *testing.T) (*Service, *Curator, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{Mode: ModeAuto, AutogenDir: dir}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cfg: %v", err)
	}
	reg := skills.NewRegistry()
	svc, err := NewService(cfg, reg, nil)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	curator := NewCurator(cfg.Curator, nil, filepath.Join(dir, ".curator_state"))
	svc.SetCurator(curator)
	return svc, curator, dir
}
