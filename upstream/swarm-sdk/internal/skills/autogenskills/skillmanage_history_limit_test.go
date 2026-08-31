package autogenskills

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TestSkillManageTool_HistoryHonorsLimit proves issue #278: calling
// SkillManage(action="history", limit=N) must return AT MOST N revisions,
// newest first, instead of silently ignoring the limit and dumping the
// entire revision history back to the caller.
func TestSkillManageTool_HistoryHonorsLimit(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, err := NewService(cfg, reg, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	tool, _ := NewSkillManageTool(svc)

	instr := "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test."
	create, err := tool.Execute(context.Background(), map[string]any{
		"action":       "create",
		"name":         "history-limit-test",
		"description":  "d",
		"instructions": instr,
	})
	if err != nil || create.IsError {
		t.Fatalf("create failed: %v %#v", err, create)
	}

	// Produce several more revisions so the history has >3 entries.
	for i := 0; i < 4; i++ {
		patch, err := tool.Execute(context.Background(), map[string]any{
			"action":            "patch",
			"name":              "history-limit-test",
			"instructions":      instr + " more text to change the body each round.",
			"expected_revision": latestRevision(t, tool, "history-limit-test"),
		})
		if err != nil || patch.IsError {
			t.Fatalf("patch %d failed: %v %#v", i, err, patch)
		}
	}

	full, err := tool.Execute(context.Background(), map[string]any{
		"action": "history",
		"name":   "history-limit-test",
	})
	if err != nil || full.IsError {
		t.Fatalf("history (no limit) failed: %v %#v", err, full)
	}
	var fullRevisions []SkillRevision
	if err := json.Unmarshal([]byte(full.Output), &fullRevisions); err != nil {
		t.Fatalf("unmarshal full history: %v", err)
	}
	if len(fullRevisions) < 4 {
		t.Fatalf("expected at least 4 revisions to make this test meaningful, got %d", len(fullRevisions))
	}

	limited, err := tool.Execute(context.Background(), map[string]any{
		"action": "history",
		"name":   "history-limit-test",
		"limit":  1,
	})
	if err != nil || limited.IsError {
		t.Fatalf("history (limit=1) failed: %v %#v", err, limited)
	}
	var limitedRevisions []SkillRevision
	if err := json.Unmarshal([]byte(limited.Output), &limitedRevisions); err != nil {
		t.Fatalf("unmarshal limited history: %v", err)
	}
	if len(limitedRevisions) != 1 {
		t.Fatalf("limit=1 returned %d revisions, want 1", len(limitedRevisions))
	}
	if limitedRevisions[0].ID != fullRevisions[0].ID {
		t.Fatalf("limit=1 did not return the newest revision: got %s, want %s", limitedRevisions[0].ID, fullRevisions[0].ID)
	}

	limited5, err := tool.Execute(context.Background(), map[string]any{
		"action": "history",
		"name":   "history-limit-test",
		"limit":  5,
	})
	if err != nil || limited5.IsError {
		t.Fatalf("history (limit=5) failed: %v %#v", err, limited5)
	}
	var limited5Revisions []SkillRevision
	if err := json.Unmarshal([]byte(limited5.Output), &limited5Revisions); err != nil {
		t.Fatalf("unmarshal limit=5 history: %v", err)
	}
	if len(limited5Revisions) > 5 {
		t.Fatalf("limit=5 returned %d revisions, want <= 5", len(limited5Revisions))
	}
	for i, rev := range limited5Revisions {
		if rev.ID != fullRevisions[i].ID {
			t.Fatalf("limit=5 revision %d = %s, want %s (order must match full history)", i, rev.ID, fullRevisions[i].ID)
		}
	}
}

// latestRevision fetches the newest revision ID for name via the tool's own
// history action, so the patch-chain test above always targets the current
// head instead of a stale expected_revision.
func latestRevision(t *testing.T, tool *SkillManageTool, name string) string {
	t.Helper()
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "history",
		"name":   name,
		"limit":  1,
	})
	if err != nil || result.IsError {
		t.Fatalf("latestRevision: history failed: %v %#v", err, result)
	}
	var revisions []SkillRevision
	if err := json.Unmarshal([]byte(result.Output), &revisions); err != nil {
		t.Fatalf("latestRevision: unmarshal failed: %v", err)
	}
	if len(revisions) == 0 {
		t.Fatalf("latestRevision: no revisions for %s", name)
	}
	return revisions[0].ID
}
