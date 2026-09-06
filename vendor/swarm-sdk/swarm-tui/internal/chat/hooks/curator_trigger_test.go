package hooks

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
)

func waitForCurator(t *testing.T, hm *HooksManager) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hm.curatorMu.Lock()
		running := hm.curatorRunning
		hm.curatorMu.Unlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for curator")
}

// TestRunCuratorIfDue_NoService verifies the daemon entry point is a safe no-op
// when no autogenskills service is wired in.
func TestRunCuratorIfDue_NoService(t *testing.T) {
	hm := newTestHooksManager(t)
	if hm.RunCuratorIfDue() {
		t.Fatalf("RunCuratorIfDue returned true with no service configured")
	}
}

// TestRunCuratorIfDue_NotDue verifies that when a curator just ran, the daily
// cadence gate (MinRunGap) suppresses another run.
func TestRunCuratorIfDue_NotDue(t *testing.T) {
	dir := t.TempDir()
	cfg := autogenskills.Config{
		Mode:       autogenskills.ModeAuto,
		AutogenDir: dir,
		Curator:    autogenskills.CuratorConfig{MinRunGap: "24h"}.WithDefaults(),
	}
	svc, err := autogenskills.NewService(cfg, skills.NewRegistry(), nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	statePath := filepath.Join(dir, ".curator_state")
	curator := autogenskills.NewCurator(cfg.Curator, nil, statePath)
	// Mark a run as having just happened: ShouldRun must now be false.
	curator.SetLastRunAt(time.Now())
	svc.SetCurator(curator)

	hm := newTestHooksManager(t)
	if err := hm.EnableAutogenSkills(svc, skills.NewRegistry()); err != nil {
		t.Fatalf("EnableAutogenSkills: %v", err)
	}

	if hm.RunCuratorIfDue() {
		t.Fatalf("RunCuratorIfDue returned true even though curator ran moments ago (gate should suppress)")
	}
}

// TestRunCuratorIfDue_Due verifies that a curator past its MinRunGap is allowed
// to run via the daemon entry point.
func TestRunCuratorIfDue_Due(t *testing.T) {
	dir := t.TempDir()
	cfg := autogenskills.Config{
		Mode:       autogenskills.ModeAuto,
		AutogenDir: dir,
		Curator:    autogenskills.CuratorConfig{MinRunGap: "1h"}.WithDefaults(),
	}
	svc, err := autogenskills.NewService(cfg, skills.NewRegistry(), nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	statePath := filepath.Join(dir, ".curator_state")
	curator := autogenskills.NewCurator(cfg.Curator, nil, statePath)
	// Last run was 2h ago, gap is 1h → due.
	curator.SetLastRunAt(time.Now().Add(-2 * time.Hour))
	svc.SetCurator(curator)

	hm := newTestHooksManager(t)
	if err := hm.EnableAutogenSkills(svc, skills.NewRegistry()); err != nil {
		t.Fatalf("EnableAutogenSkills: %v", err)
	}

	if !hm.RunCuratorIfDue() {
		t.Fatalf("RunCuratorIfDue returned false even though MinRunGap elapsed (should run)")
	}
	waitForCurator(t, hm)
}

func TestRunCuratorIfDue_FreshInstallationSeedsAndDefers(t *testing.T) {
	dir := t.TempDir()
	cfg := autogenskills.Config{
		Mode:       autogenskills.ModeAuto,
		AutogenDir: dir,
		Curator:    autogenskills.CuratorConfig{MinRunGap: "1h"}.WithDefaults(),
	}
	svc, err := autogenskills.NewService(cfg, skills.NewRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	curator := autogenskills.NewCurator(cfg.Curator, nil, filepath.Join(dir, ".curator_state"))
	svc.SetCurator(curator)
	hm := newTestHooksManager(t)
	if err := hm.EnableAutogenSkills(svc, skills.NewRegistry()); err != nil {
		t.Fatal(err)
	}
	if hm.RunCuratorIfDue() {
		t.Fatal("fresh installation ran instead of deferring")
	}
	if curator.GetLastRunAt().IsZero() {
		t.Fatal("fresh installation did not seed cadence")
	}
}

func TestNotifyAgentIdle_UsesCadenceGate(t *testing.T) {
	dir := t.TempDir()
	cfg := autogenskills.Config{
		Mode:       autogenskills.ModeAuto,
		AutogenDir: dir,
		Curator: autogenskills.CuratorConfig{
			Interval:  "1ms",
			MinRunGap: "24h",
		}.WithDefaults(),
	}
	svc, err := autogenskills.NewService(cfg, skills.NewRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	curator := autogenskills.NewCurator(cfg.Curator, nil, filepath.Join(dir, ".curator_state"))
	curator.SetLastRunAt(time.Now())
	svc.SetCurator(curator)
	hm := newTestHooksManager(t)
	if err := hm.EnableAutogenSkills(svc, skills.NewRegistry()); err != nil {
		t.Fatal(err)
	}
	before := curator.GetLastRunAt()
	hm.NotifyAgentIdle()
	time.Sleep(30 * time.Millisecond)
	waitForCurator(t, hm)
	if !curator.GetLastRunAt().Equal(before) {
		t.Fatal("idle timer bypassed cadence gate")
	}
}

func TestRunCuratorIfDue_ConsolidationOffSkipsAgent(t *testing.T) {
	dir := t.TempDir()
	cfg := autogenskills.Config{
		Mode:       autogenskills.ModeAuto,
		AutogenDir: dir,
		Curator:    autogenskills.CuratorConfig{MinRunGap: "1h"}.WithDefaults(),
	}
	svc, err := autogenskills.NewService(cfg, skills.NewRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	curator := autogenskills.NewCurator(cfg.Curator, nil, filepath.Join(dir, ".curator_state"))
	curator.SetLastRunAt(time.Now().Add(-2 * time.Hour))
	svc.SetCurator(curator)
	hm := newTestHooksManager(t)
	if err := hm.EnableAutogenSkills(svc, skills.NewRegistry()); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	hm.curatorAgentRun = func(context.Context, []autogenskills.ReviewResult, bool) (*autogenskills.CuratorAgentResult, error) {
		calls.Add(1)
		return &autogenskills.CuratorAgentResult{}, nil
	}
	if !hm.RunCuratorIfDue() {
		t.Fatal("due curator did not start")
	}
	waitForCurator(t, hm)
	if calls.Load() != 0 {
		t.Fatalf("agent called %d times with consolidation off", calls.Load())
	}
}

func TestRunCuratorIfDue_PostAgentReconciles(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := autogenskills.Config{
		Mode:       autogenskills.ModeAuto,
		AutogenDir: dir,
		Curator: autogenskills.CuratorConfig{
			Consolidate: true,
			MinRunGap:   "1h",
		}.WithDefaults(),
	}
	svc, err := autogenskills.NewService(cfg, skills.NewRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	curator := autogenskills.NewCurator(cfg.Curator, nil, filepath.Join(dir, ".curator_state"))
	curator.SetLastRunAt(time.Now().Add(-2 * time.Hour))
	svc.SetCurator(curator)
	hm := newTestHooksManager(t)
	if err := hm.EnableAutogenSkills(svc, skills.NewRegistry()); err != nil {
		t.Fatal(err)
	}
	hm.curatorAgentRun = func(context.Context, []autogenskills.ReviewResult, bool) (*autogenskills.CuratorAgentResult, error) {
		archiveDir := filepath.Join(dir, "archive")
		if err := os.MkdirAll(archiveDir, 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(skillDir, filepath.Join(archiveDir, "skill")); err != nil {
			return nil, err
		}
		return &autogenskills.CuratorAgentResult{}, nil
	}
	if !hm.RunCuratorIfDue() {
		t.Fatal("due curator did not start")
	}
	waitForCurator(t, hm)
	meta := curator.GetState().SkillStates["skill"]
	if meta.State != autogenskills.SkillStateArchived || meta.ArchivedAt == nil {
		t.Fatalf("post-agent placement was not reconciled: %+v", meta)
	}
}
