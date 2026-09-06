package autogenskills

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TestCurator_ReviewAll_emptyDir returns empty for missing directory.
func TestCurator_ReviewAll_emptyDir(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{}, f, "")

	results := c.ReviewAll("/nonexistent")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// TestCurator_ReviewAll_freshSkill marks fresh skills as none.
func TestCurator_ReviewAll_freshSkill(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 30}, f, "")

	// Create a skill
	opts := CreateOptions{
		Name:          "fresh-skill",
		Description:   "Fresh",
		Instructions:  "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
		TriggerReason: TriggerManual,
	}
	f.Create(opts)

	results := c.ReviewAll(dir)
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result, got %d", len(results))
	}
	// Find the result for our skill
	var found bool
	for _, r := range results {
		if r.SkillName == "fresh-skill" && r.Action == ActionNone {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ActionNone for fresh-skill, got results: %+v", results)
	}
}

// TestCurator_ReviewAll_oldSkill marks old skills for archive.
func TestCurator_ReviewAll_oldSkill(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 1}, f, "")

	// Create a skill file with old mtime
	skillDir := filepath.Join(dir, "old-skill")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillPath, []byte("---\nname: old-skill\n---\n"), 0644)
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(skillPath, oldTime, oldTime)

	results := c.ReviewAll(dir)
	var found bool
	for _, r := range results {
		if r.SkillName == "old-skill" && r.Action == ActionArchive {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ActionArchive for old-skill, got results: %+v", results)
	}
}

// TestCurator_Archive moves skill to archive subdirectory.
func TestCurator_Archive(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{}, f, "")

	// Create a skill
	skillDir := filepath.Join(dir, "to-archive")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("test"), 0644)

	newPath, err := c.Archive(dir, "to-archive")
	if err != nil {
		t.Fatalf("archive failed: %v", err)
	}

	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Error("archived skill should exist")
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Error("original skill should not exist")
	}

	// Verify state was updated
	state := c.GetState()
	if meta, ok := state.SkillStates["to-archive"]; ok {
		if meta.State != SkillStateArchived {
			t.Errorf("expected state %q, got %q", SkillStateArchived, meta.State)
		}
		if meta.ArchivedAt == nil {
			t.Error("expected ArchivedAt to be set")
		}
	}
}

// TestCurator_NeedsReview_true when old skills exist.
func TestCurator_NeedsReview_true(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 1}, f, "")

	// Create old skill
	skillDir := filepath.Join(dir, "old")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillPath, []byte("test"), 0644)
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(skillPath, oldTime, oldTime)

	if !c.NeedsReview(dir) {
		t.Error("expected NeedsReview=true for old skill")
	}
}

// TestCurator_NeedsReview_false when all skills fresh.
func TestCurator_NeedsReview_false(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 30}, f, "")

	// Create fresh skill
	f.Create(CreateOptions{
		Name:          "fresh",
		Description:   "Fresh",
		Instructions:  "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
		TriggerReason: TriggerManual,
	})

	if c.NeedsReview(dir) {
		t.Error("expected NeedsReview=false for fresh skills")
	}
}

// TestCurator_defaults applies zero-value-safe defaults.
func TestCurator_defaults(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{}, f, "") // zero config

	if c.cfg.Interval != "1h" {
		t.Errorf("default interval = %q, want 1h", c.cfg.Interval)
	}
	if c.cfg.ArchiveAfterDays != 90 {
		t.Errorf("default archive_days = %d, want 90", c.cfg.ArchiveAfterDays)
	}
	if c.cfg.StaleAfterDays != 30 {
		t.Errorf("default stale_days = %d, want 30", c.cfg.StaleAfterDays)
	}
	if c.cfg.PatchAfterDays != 60 {
		t.Errorf("default patch_days = %d, want 60", c.cfg.PatchAfterDays)
	}
	if c.cfg.ConsolidateTagOverlap != 0.5 {
		t.Errorf("default consolidate_tag_overlap = %f, want 0.5", c.cfg.ConsolidateTagOverlap)
	}
}

// TestCurator_PinnedSkillNeverArchived verifies pinned skills are never archived.
func TestCurator_PinnedSkillNeverArchived(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 1}, f, "")

	// Create old skill
	skillDir := filepath.Join(dir, "old-pinned")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillPath, []byte("---\nname: old-pinned\n---\nInstructions\n"), 0644)
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(skillPath, oldTime, oldTime)

	// Pin it
	c.Pin("old-pinned")

	results := c.ReviewAll(dir)
	for _, r := range results {
		if r.SkillName == "old-pinned" && r.Action == ActionArchive {
			t.Error("pinned skill should not be recommended for archive")
		}
	}
}

// TestCurator_Restore moves skill from archive back to active.
func TestCurator_Restore(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{}, f, "")

	// Create and archive a skill
	skillDir := filepath.Join(dir, "to-restore")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("test"), 0644)
	c.Archive(dir, "to-restore")

	// Verify it's in archive
	archivePath := filepath.Join(dir, "archive", "to-restore")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Fatal("archived skill should exist before restore")
	}

	// Restore it
	newPath, err := c.Restore(dir, "to-restore")
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Error("restored skill should exist")
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Error("archived skill should not exist after restore")
	}
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		t.Error("skill should be back in active directory")
	}

	// Verify state was updated
	state := c.GetState()
	if meta, ok := state.SkillStates["to-restore"]; ok {
		if meta.State == SkillStateArchived {
			t.Error("skill should not be archived after restore")
		}
		if meta.ArchivedAt != nil {
			t.Error("ArchivedAt should be nil after restore")
		}
	}
}

// TestCurator_PinUnpin toggles pinned state.
func TestCurator_PinUnpin(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{}, f, "")

	skillDir := filepath.Join(dir, "pinned-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pinned-skill\n---\nInstructions\n"), 0644)

	// Pin
	c.Pin("pinned-skill")
	state := c.GetState()
	meta, ok := state.SkillStates["pinned-skill"]
	if !ok {
		t.Fatal("expected skill to be tracked in state")
	}
	if !meta.Pinned {
		t.Error("expected skill to be pinned")
	}
	if meta.State != SkillStatePinned {
		t.Errorf("expected state %q, got %q", SkillStatePinned, meta.State)
	}

	// Unpin
	c.Unpin("pinned-skill")
	state = c.GetState()
	meta = state.SkillStates["pinned-skill"]
	if meta.Pinned {
		t.Error("expected skill to be unpinned")
	}
	if meta.State != SkillStateActive {
		t.Errorf("expected state %q after unpin, got %q", SkillStateActive, meta.State)
	}

	// Unpin non-existent skill should error
	err := c.Unpin("nonexistent")
	if err == nil {
		t.Error("expected error when unpinning nonexistent skill")
	}
}

// TestCurator_MarkUsed updates last_used_at.
func TestCurator_MarkUsed(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{}, f, "")

	// Create a skill on disk
	skillDir := filepath.Join(dir, "my-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: my-skill\n---\nInstructions\n"), 0644)

	// Mark it used
	before := time.Now()
	c.MarkUsed("my-skill")
	after := time.Now()

	state := c.GetState()
	meta, ok := state.SkillStates["my-skill"]
	if !ok {
		t.Fatal("expected skill to be tracked in state")
	}
	if meta.LastUsedAt.Before(before) || meta.LastUsedAt.After(after) {
		t.Errorf("expected last_used_at between %v and %v, got %v", before, after, meta.LastUsedAt)
	}

	// Mark used again — should update
	time.Sleep(time.Millisecond) // ensure time advances
	c.MarkUsed("my-skill")
	state = c.GetState()
	meta = state.SkillStates["my-skill"]
	if meta.LastUsedAt.Before(before) {
		t.Error("last_used_at should have been updated")
	}
}

func TestCurator_StatePersistence(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	statePath := filepath.Join(dir, ".curator_state")

	c1 := NewCurator(CuratorConfig{}, f, statePath)
	c1.Pin("test-skill")
	c1.MarkUsed("test-skill")
	c1.SetPaused(true)
	if err := c1.SaveState(); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("state file should exist after SaveState")
	}

	// Create a new curator loading from the same state
	c2 := NewCurator(CuratorConfig{}, f, statePath)

	if !c2.IsPaused() {
		t.Error("expected curator to be paused after reload")
	}
	state := c2.GetState()
	if meta, ok := state.SkillStates["test-skill"]; !ok {
		t.Error("expected skill to be in state after reload")
	} else if !meta.Pinned {
		t.Error("expected skill to be pinned after reload")
	}
}

// TestCurator_PausedReturnsNoActions verifies paused curator returns empty.
func TestCurator_PausedReturnsNoActions(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 1}, f, "")

	// Create old skill
	skillDir := filepath.Join(dir, "old")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillPath, []byte("test"), 0644)
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(skillPath, oldTime, oldTime)

	// Pause curator
	c.SetPaused(true)

	results := c.ReviewAll(dir)
	if len(results) != 0 {
		t.Errorf("paused curator should return no results, got %d", len(results))
	}

	if c.NeedsReview(dir) {
		t.Error("paused curator should return NeedsReview=false")
	}
}

// TestCurator_PatchAfterDays suggests patching for old but active skills.
func TestCurator_PatchAfterDays(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 30, PatchAfterDays: 1}, f, "")

	// Create skill with old creation time but recent use
	skillDir := filepath.Join(dir, "old-but-used")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillPath, []byte("---\nname: old-but-used\n---\nInstructions\n"), 0644)
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(skillPath, oldTime, oldTime)

	// Set state: created long ago, used recently (within ArchiveAfterDays)
	c.state.SkillStates["old-but-used"] = SkillMeta{
		State:      SkillStateActive,
		CreatedAt:  oldTime,
		LastUsedAt: time.Now(),
		Pinned:     false,
	}

	results := c.ReviewAll(dir)
	found := false
	for _, r := range results {
		if r.SkillName == "old-but-used" && r.Action == ActionPatch {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ActionPatch for old-but-used skill, got results: %+v", results)
	}
}

func TestCurator_AutomaticTransitions_GracePeriodAndArchive(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 1}, f, "")

	// Create a skill file with old mtime
	skillDir := filepath.Join(dir, "old-skill")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillPath, []byte("---\nname: old-skill\n---\n"), 0644)
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(skillPath, oldTime, oldTime)

	// Phase 1: first transition pass seeds grace despite the old mtime.
	results1, err := c.AutomaticTransitions(dir, nil)
	if err != nil {
		t.Fatalf("AutomaticTransitions failed: %v", err)
	}
	var foundArchive1 bool
	for _, r := range results1 {
		if r.SkillName == "old-skill" && r.Action == ActionArchive {
			foundArchive1 = true
		}
	}
	if foundArchive1 {
		t.Error("first transition pass should apply grace")
	}

	// File should not be moved during first-seen grace.
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		t.Error("original skill dir should still exist during grace")
	}

	// Phase 2: Simulate time passing — set LastUsedAt to old time
	c.mu.Lock()
	meta := c.state.SkillStates["old-skill"]
	meta.LastUsedAt = oldTime
	c.state.SkillStates["old-skill"] = meta
	c.mu.Unlock()

	// Phase 3: second transition pass archives deterministically.
	results2, err := c.AutomaticTransitions(dir, nil)
	if err != nil {
		t.Fatalf("AutomaticTransitions 2 failed: %v", err)
	}
	var foundArchive2 bool
	for _, r := range results2 {
		if r.SkillName == "old-skill" && r.Action == ActionArchive {
			foundArchive2 = true
		}
	}
	if !foundArchive2 {
		t.Errorf("second transition pass should archive, got: %+v", results2)
	}

	// State and filesystem both reflect the atomic archive.
	state := c.GetState()
	if meta, ok := state.SkillStates["old-skill"]; ok {
		if meta.State != SkillStateArchived {
			t.Errorf("expected archived state, got %q", meta.State)
		}
	} else {
		t.Error("old-skill should be in state")
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "old-skill", "SKILL.md")); err != nil {
		t.Fatalf("archived file missing: %v", err)
	}
}

func TestCurator_Run_Paused(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{}, f, "")
	c.state.Paused = true

	results, err := c.Run(dir)
	if err != nil {
		t.Fatalf("Run should not error when paused, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results when paused, got: %+v", results)
	}
}

// TestCuratorProbe_RealSkills is a diagnostic probe that runs the curator
// against the real ~/.swarm/skills/autogen/ directory. It does NOT modify
// real skills — it uses ReviewAll (read-only) rather than Run.
func TestCuratorProbe_RealSkills(t *testing.T) {
	realDir := filepath.Join(os.Getenv("HOME"), ".swarm", "skills", "autogen")
	if _, err := os.Stat(realDir); os.IsNotExist(err) {
		t.Skip("no real autogen skills directory found")
	}

	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: realDir}
	f, _ := NewSkillFactory(cfg, reg, nil)
	c := NewCurator(CuratorConfig{ConsolidateTagOverlap: 0.5}, f, "")

	results := c.ReviewAll(realDir)

	archiveCount := 0
	staleCount := 0
	consolidateCount := 0
	patchCount := 0
	noneCount := 0

	for _, r := range results {
		switch r.Action {
		case ActionArchive:
			archiveCount++
		case ActionMarkStale:
			staleCount++
		case ActionConsolidate:
			consolidateCount++
		case ActionPatch:
			patchCount++
		case ActionNone:
			noneCount++
		}
	}

	t.Logf("=== CURATOR PROBE: %s ===", realDir)
	t.Logf("Total skills reviewed: %d", len(results))
	t.Logf("Archive candidates:    %d", archiveCount)
	t.Logf("Stale candidates:      %d", staleCount)
	t.Logf("Consolidate candidates:%d", consolidateCount)
	t.Logf("Patch candidates:     %d", patchCount)
	t.Logf("No action needed:     %d", noneCount)

	// Log every consolidation candidate in detail
	for _, r := range results {
		if r.Action == ActionConsolidate {
			t.Logf("  [CONSOLIDATE] %s -> related: %v", r.SkillName, r.RelatedSkills)
		}
	}

	// ReviewAll must return well-formed results: the action buckets must
	// partition the result set exactly, and every result must name a skill
	// and carry a recognized action.
	if archiveCount+staleCount+consolidateCount+patchCount+noneCount != len(results) {
		t.Errorf("action counts (%d+%d+%d+%d+%d) do not sum to result count %d",
			archiveCount, staleCount, consolidateCount, patchCount, noneCount, len(results))
	}
	for i, r := range results {
		if r.SkillName == "" {
			t.Errorf("result %d has empty SkillName", i)
		}
		switch r.Action {
		case ActionArchive, ActionMarkStale, ActionConsolidate, ActionPatch, ActionNone:
			// recognized
		default:
			t.Errorf("result %d (%s) has unrecognized action %v", i, r.SkillName, r.Action)
		}
	}
}

// newTestCurator builds a Curator with a nil factory and a temp state path.
func newTestCurator(t *testing.T, cfg CuratorConfig) *Curator {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "curator_state.json")
	return NewCurator(cfg, nil, statePath)
}

// TestCurator_ShouldRun_freshCurator: first cadence check seeds and defers.
func TestCurator_ShouldRun_freshCurator(t *testing.T) {
	c := newTestCurator(t, CuratorConfig{}.WithDefaults())
	if c.ShouldRun() {
		t.Errorf("expected ShouldRun=false for fresh curator")
	}
	if c.GetLastRunAt().IsZero() {
		t.Error("fresh cadence check did not persist a seed timestamp")
	}
}

// TestCurator_ShouldRun_immediatelyAfterRun: SetLastRunAt(now) with default 24h
// gap => false (not enough time elapsed).
func TestCurator_ShouldRun_immediatelyAfterRun(t *testing.T) {
	c := newTestCurator(t, CuratorConfig{}.WithDefaults())
	c.SetLastRunAt(time.Now())
	if c.ShouldRun() {
		t.Errorf("expected ShouldRun=false immediately after a run with 24h gap")
	}
}

// TestCurator_ShouldRun_after25h: LastRunAt 25h ago with default 24h gap => true.
func TestCurator_ShouldRun_after25h(t *testing.T) {
	c := newTestCurator(t, CuratorConfig{}.WithDefaults())
	c.SetLastRunAt(time.Now().Add(-25 * time.Hour))
	if !c.ShouldRun() {
		t.Errorf("expected ShouldRun=true when LastRunAt is 25h ago (>24h gap)")
	}
}

// TestCurator_ShouldRun_paused: paused curator never runs, even if LastRunAt zero.
func TestCurator_ShouldRun_paused(t *testing.T) {
	c := newTestCurator(t, CuratorConfig{}.WithDefaults())
	c.SetPaused(true)
	if c.ShouldRun() {
		t.Errorf("expected ShouldRun=false when paused")
	}
}

// TestCuratorConfig_MinRunGapDefault: WithDefaults applies "24h".
func TestCuratorConfig_MinRunGapDefault(t *testing.T) {
	out := CuratorConfig{}.WithDefaults()
	if out.MinRunGap != "24h" {
		t.Errorf("expected MinRunGap default %q, got %q", "24h", out.MinRunGap)
	}
}

// TestCurator_ShouldRun_customGap: a custom MinRunGap of 1h is respected.
func TestCurator_ShouldRun_customGap(t *testing.T) {
	cfg := CuratorConfig{MinRunGap: "1h"}.WithDefaults()
	if cfg.MinRunGap != "1h" {
		t.Fatalf("expected MinRunGap to remain %q, got %q", "1h", cfg.MinRunGap)
	}

	tests := []struct {
		name    string
		lastRun time.Duration // how long ago LastRunAt was
		wantRun bool
	}{
		{name: "90m ago exceeds 1h gap", lastRun: 90 * time.Minute, wantRun: true},
		{name: "30m ago within 1h gap", lastRun: 30 * time.Minute, wantRun: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestCurator(t, cfg)
			c.SetLastRunAt(time.Now().Add(-tc.lastRun))
			if got := c.ShouldRun(); got != tc.wantRun {
				t.Errorf("ShouldRun()=%v, want %v (lastRun %v ago, gap 1h)", got, tc.wantRun, tc.lastRun)
			}
		})
	}
}

// TestCurator_GetLastRunAt: returns the value set by SetLastRunAt.
func TestCurator_GetLastRunAt(t *testing.T) {
	c := newTestCurator(t, CuratorConfig{}.WithDefaults())
	if !c.GetLastRunAt().IsZero() {
		t.Errorf("expected zero LastRunAt for fresh curator, got %v", c.GetLastRunAt())
	}
	now := time.Now()
	c.SetLastRunAt(now)
	if !c.GetLastRunAt().Equal(now) {
		t.Errorf("GetLastRunAt()=%v, want %v", c.GetLastRunAt(), now)
	}
}

func TestCurator_RunDoesNotConsumeCadenceBeforeOrchestratorSuccess(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, ".curator_state")
	autogenDir := filepath.Join(dir, "autogen")
	if err := os.MkdirAll(autogenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewCurator(CuratorConfig{}.WithDefaults(), nil, statePath)

	if _, err := c.Run(autogenDir); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if got := c.GetLastRunAt(); !got.IsZero() {
		t.Fatalf("analysis consumed successful-run cadence: LastRunAt=%v", got)
	}
	if c.ShouldRun() {
		t.Fatal("first cadence check should seed and defer")
	}
}

func TestCurator_RunReconcilesFilesystemPlacement(t *testing.T) {
	dir := t.TempDir()
	autogenDir := filepath.Join(dir, "autogen")
	activeDir := filepath.Join(autogenDir, "active-skill")
	archivedDir := filepath.Join(autogenDir, "archive", "archived-skill")
	for _, path := range []string{activeDir, archivedDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: placeholder\ndescription: placeholder\n---\nbody"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := NewCurator(CuratorConfig{}.WithDefaults(), nil, filepath.Join(autogenDir, ".curator_state"))
	c.state.SkillStates["active-skill"] = SkillMeta{State: SkillStateArchived}
	c.state.SkillStates["archived-skill"] = SkillMeta{State: SkillStateActive}

	if err := c.ReconcileAndSave(autogenDir); err != nil {
		t.Fatal(err)
	}
	if got := c.state.SkillStates["active-skill"].State; got != SkillStateActive {
		t.Fatalf("active filesystem skill remained %q", got)
	}
	archived := c.state.SkillStates["archived-skill"]
	if archived.State != SkillStateArchived || archived.ArchivedAt == nil {
		t.Fatalf("archived filesystem skill not reconciled: %+v", archived)
	}
}

func TestCurator_RunPreviewDoesNotMutateStateOrFilesystem(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "old-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: old-skill\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(skillPath, old, old); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, ".curator_state")
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 1}, nil, statePath)
	c.state.SkillStates["ghost"] = SkillMeta{State: SkillStateStale, LastUsedAt: old}
	before := c.GetState()

	results, err := c.Run(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != ActionArchive {
		t.Fatalf("unexpected preview: %+v", results)
	}
	after := c.GetState()
	if len(after.SkillStates) != len(before.SkillStates) || after.SkillStates["ghost"] != before.SkillStates["ghost"] {
		t.Fatalf("preview mutated state: before=%+v after=%+v", before, after)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("preview wrote state file: %v", err)
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("preview moved skill: %v", err)
	}
}

func TestCurator_AutomaticTransitions_StaleAndReactivate(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCurator(CuratorConfig{StaleAfterDays: 1, ArchiveAfterDays: 3}, nil, filepath.Join(dir, ".curator_state"))
	if _, err := c.AutomaticTransitions(dir, nil); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	meta := c.state.SkillStates["skill"]
	meta.LastUsedAt = time.Now().Add(-48 * time.Hour)
	c.state.SkillStates["skill"] = meta
	c.mu.Unlock()
	if _, err := c.AutomaticTransitions(dir, nil); err != nil {
		t.Fatal(err)
	}
	if got := c.GetState().SkillStates["skill"].State; got != SkillStateStale {
		t.Fatalf("state=%q, want stale", got)
	}
	if err := c.MarkUsed("skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AutomaticTransitions(dir, nil); err != nil {
		t.Fatal(err)
	}
	if got := c.GetState().SkillStates["skill"].State; got != SkillStateActive {
		t.Fatalf("state=%q, want active", got)
	}
}

func TestCurator_AutomaticTransitions_ProtectsPinnedAndExternal(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"pinned", "scheduled"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := NewCurator(CuratorConfig{ArchiveAfterDays: 1}, nil, filepath.Join(dir, ".curator_state"))
	if _, err := c.AutomaticTransitions(dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Pin("pinned"); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	for _, name := range []string{"pinned", "scheduled"} {
		meta := c.state.SkillStates[name]
		meta.LastUsedAt = time.Now().Add(-48 * time.Hour)
		c.state.SkillStates[name] = meta
	}
	c.mu.Unlock()
	if _, err := c.AutomaticTransitions(dir, func(name string) bool { return name == "scheduled" }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pinned", "scheduled"} {
		if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err != nil {
			t.Fatalf("%s was not protected: %v", name, err)
		}
	}
}

func TestCurator_IgnoresHiddenOperationalDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".curator_backups", "snapshot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".curator_backups", "snapshot", "manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCurator(CuratorConfig{}, nil, filepath.Join(dir, ".curator_state"))
	results, err := c.Run(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("hidden operational directory entered review: %#v", results)
	}
	if _, err := c.AutomaticTransitions(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := c.GetState().SkillStates[".curator_backups"]; exists {
		t.Fatal("hidden operational directory entered curator state")
	}
}
