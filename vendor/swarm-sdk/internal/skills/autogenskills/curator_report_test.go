package autogenskills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCuratorStructuredSummaryMalformed(t *testing.T) {
	if _, err := ParseCuratorStructuredSummary("```yaml\nconsolidations:\n - from: broken\n```"); err == nil {
		t.Fatal("expected malformed YAML error")
	}
	if _, err := ParseCuratorStructuredSummary("```json\n{}\n```"); err == nil {
		t.Fatal("expected exact fenced YAML requirement")
	}
}

func TestClassifyCuratorRemovalsUsesStateAuthorityExactlyOnce(t *testing.T) {
	before := testInventory("merged", "pruned", "modelled", "kept")
	after := testInventory("umbrella", "kept")
	state := &CuratorState{SkillStates: map[string]SkillMeta{
		"merged": {State: SkillStateArchived, AbsorbedInto: "umbrella", ArchiveReason: "absorbed"},
		"pruned": {State: SkillStateArchived, ArchiveReason: "obsolete"},
	}}
	model := "```yaml\nconsolidations:\n  - from: modelled\n    into: umbrella\n    reason: merged by model\n  - from: merged\n    into: missing\n    reason: must lose to state\nprunings: []\n```"
	got, err := ClassifyCuratorRemovals(before, after, state, model)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d classifications: %#v", len(got), got)
	}
	byName := map[string]CuratorRemovalClassification{}
	for _, item := range got {
		if _, duplicate := byName[item.Name]; duplicate {
			t.Fatalf("duplicate %q", item.Name)
		}
		byName[item.Name] = item
	}
	if byName["merged"].Into != "umbrella" || byName["merged"].Source != "curator-state" {
		t.Fatalf("state was not authoritative: %#v", byName["merged"])
	}
	if byName["pruned"].Kind != "pruned" || byName["pruned"].Reason != "obsolete" {
		t.Fatalf("wrong prune: %#v", byName["pruned"])
	}
	if byName["modelled"].Into != "umbrella" || byName["modelled"].Source != "model" {
		t.Fatalf("model secondary failed: %#v", byName["modelled"])
	}
}

func TestClassifyCuratorRemovalsRejectsMissingTarget(t *testing.T) {
	before := testInventory("old")
	after := testInventory()
	state := &CuratorState{SkillStates: map[string]SkillMeta{
		"old": {State: SkillStateArchived, AbsorbedInto: "not-there"},
	}}
	if _, err := ClassifyCuratorRemovals(before, after, state, ""); err == nil {
		t.Fatal("expected nonexistent target error")
	}
}

func TestClassifyCuratorRemovalsRejectsMissingProvenance(t *testing.T) {
	before := testInventory("old")
	after := testInventory()
	state := &CuratorState{SkillStates: map[string]SkillMeta{
		"old": {State: SkillStateArchived},
	}}
	if _, err := ClassifyCuratorRemovals(before, after, state, "no structured summary"); err == nil {
		t.Fatal("expected missing archive provenance to fail classification")
	}
}

func TestSnapshotInventoryAndWriteRunReport(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "old", "SKILL.md"), "old")
	writeTestFile(t, filepath.Join(root, "old", "references", "x.md"), "x")
	before, err := SnapshotCuratorInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Active["old"].Digest == "" {
		t.Fatalf("missing inventory digest: %#v", before)
	}
	if err := os.Rename(filepath.Join(root, "old"), filepath.Join(root, "umbrella")); err != nil {
		t.Fatal(err)
	}
	after, _ := SnapshotCuratorInventory(root)
	output := filepath.Join(t.TempDir(), "run")
	report, err := WriteCuratorRunReport(CuratorRunReportOptions{
		OutputDir: output, Before: before, After: after,
		State: &CuratorState{SkillStates: map[string]SkillMeta{"old": {State: SkillStateArchived, AbsorbedInto: "umbrella"}}},
		Model: "m", Provider: "p", TurnCount: 3, TokenCount: 42, Error: "boom",
		StartedAt: time.Unix(1, 0), FinishedAt: time.Unix(2, 0), RenameLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RenameSummary.Total != 1 || len(report.RenameSummary.Items) != 1 {
		t.Fatalf("rename summary: %#v", report.RenameSummary)
	}
	raw, err := os.ReadFile(filepath.Join(output, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded CuratorRunReport
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Provider != "p" || decoded.Model != "m" || decoded.TurnCount != 3 || decoded.TokenCount != 42 || decoded.Error != "boom" {
		t.Fatalf("missing run fields: %s", raw)
	}
	md, err := os.ReadFile(filepath.Join(output, "REPORT.md"))
	if err != nil || !strings.Contains(string(md), "Showing 1 of 1") {
		t.Fatalf("REPORT.md = %q, %v", md, err)
	}
}

func TestCuratorRenameSummaryBounded(t *testing.T) {
	before := testInventory("a", "b", "c")
	after := testInventory("umbrella")
	state := &CuratorState{SkillStates: map[string]SkillMeta{}}
	for _, name := range []string{"a", "b", "c"} {
		state.SkillStates[name] = SkillMeta{State: SkillStateArchived, AbsorbedInto: "umbrella"}
	}
	report, err := WriteCuratorRunReport(CuratorRunReportOptions{
		OutputDir: t.TempDir(), Before: before, After: after, State: state, RenameLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RenameSummary.Total != 3 || report.RenameSummary.Truncated != 1 || len(report.RenameSummary.Items) != 2 {
		t.Fatalf("unbounded summary: %#v", report.RenameSummary)
	}
}

func testInventory(names ...string) CuratorInventory {
	active := make(map[string]CuratorInventoryEntry)
	for _, name := range names {
		active[name] = CuratorInventoryEntry{Name: name}
	}
	return CuratorInventory{Active: active, Archived: make(map[string]CuratorInventoryEntry)}
}
