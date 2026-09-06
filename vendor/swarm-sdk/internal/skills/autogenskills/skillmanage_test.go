package autogenskills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TestSkillManageTool_New_nilService rejects nil service.
func TestSkillManageTool_New_nilService(t *testing.T) {
	_, err := NewSkillManageTool(nil)
	if err == nil {
		t.Error("expected error for nil service")
	}
}

// TestSkillManageTool_New_success creates tool with valid service.
func TestSkillManageTool_New_success(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	tool, err := NewSkillManageTool(svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool == nil {
		t.Fatal("expected non-nil tool")
	}
	if tool.Name() != "SkillManage" {
		t.Errorf("Name() = %q, want SkillManage", tool.Name())
	}
}

// TestSkillManageTool_Execute_create creates a skill.
func TestSkillManageTool_Execute_create(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	tool, _ := NewSkillManageTool(svc)

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":       "create",
		"name":         "manage-test",
		"description":  "Test skill from SkillManage",
		"instructions": "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
		"tags":         "test, manage",
		"category":     "testing",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "Created skill") {
		t.Errorf("expected success message, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "manage-test") {
		t.Errorf("expected skill name in output, got: %q", result.Output)
	}
}

// TestSkillManageTool_Execute_unknownAction returns error for unsupported actions.
func TestSkillManageTool_Execute_unknownAction(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	tool, _ := NewSkillManageTool(svc)

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":       "delete",
		"name":         "any",
		"description":  "d",
		"instructions": "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "Unknown action") {
		t.Errorf("expected 'Unknown action' message, got: %q", result.Output)
	}
}

// TestSkillManageTool_Execute_list returns created skills.
func TestSkillManageTool_Execute_list(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	// Create a skill first via the factory so it's in the registry
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "list-test-skill",
		Description:   "Test skill for listing",
		Instructions:  strings.Repeat("Test instructions for listing. ", 10),
		TriggerReason: TriggerManual,
	})

	tool, _ := NewSkillManageTool(svc)
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	if !strings.Contains(result.Output, "list-test-skill") {
		t.Errorf("expected skill name in list output, got: %s", result.Output)
	}
}

// TestSkillManageTool_Execute_view returns skill details.
func TestSkillManageTool_Execute_view(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	// Create a skill first
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "view-test-skill",
		Description:   "Test skill for viewing",
		Instructions:  strings.Repeat("Test instructions for viewing. ", 10),
		TriggerReason: TriggerManual,
	})

	tool, _ := NewSkillManageTool(svc)
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "view",
		"name":   "view-test-skill",
	})
	if err != nil {
		t.Fatalf("view returned error: %v", err)
	}
	if !strings.Contains(result.Output, "view-test-skill") {
		t.Errorf("expected skill name in view output, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Test instructions for viewing") {
		t.Errorf("expected instructions in view output, got: %s", result.Output)
	}

	second, err := tool.Execute(context.Background(), map[string]any{
		"action": "view",
		"name":   "view-test-skill",
	})
	if err != nil {
		t.Fatalf("second view returned error: %v", err)
	}
	if second.Output != result.Output {
		t.Fatal("small unpaged view changed from its stable legacy response")
	}
}

func TestSkillManageTool_Execute_viewBoundsLargeSkill(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	factory, _ := NewSkillFactory(cfg, reg, nil)
	instructions := "LARGE-HEAD\n" + strings.Repeat(strings.Repeat("x", 100)+"\n", 1_050) + "LARGE-TAIL"
	if created := factory.Create(CreateOptions{
		Name:          "large-view-skill",
		Description:   "Large skill for bounded view testing",
		Instructions:  instructions,
		TriggerReason: TriggerManual,
	}); created.Error != nil {
		t.Fatal(created.Error)
	}

	tool, _ := NewSkillManageTool(svc)
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "view",
		"name":   "large-view-skill",
	})
	if err != nil {
		t.Fatalf("view returned error: %v", err)
	}
	if len(result.Output) >= 100_000 {
		t.Fatalf("large view was not bounded: %d bytes", len(result.Output))
	}
	if !strings.Contains(result.Output, "Instructions range: [0,80000) of ") ||
		!strings.Contains(result.Output, " Unicode characters") {
		t.Fatalf("large view lacks total-size range: %s", result.Output)
	}
	if !strings.Contains(result.Output, "expected_revision: ") {
		t.Fatal("large view lacks expected_revision")
	}
}

func TestSkillManageTool_Execute_viewPagesKeepRevision(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	factory, _ := NewSkillFactory(cfg, reg, nil)
	if created := factory.Create(CreateOptions{
		Name:          "paged-view-skill",
		Description:   "Paged skill view test",
		Instructions:  "0123456789abcdefghij",
		TriggerReason: TriggerManual,
	}); created.Error != nil {
		t.Fatal(created.Error)
	}
	tool, _ := NewSkillManageTool(svc)

	first, err := tool.Execute(context.Background(), map[string]any{
		"action": "view",
		"name":   "paged-view-skill",
		"offset": 0,
		"limit":  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.Execute(context.Background(), map[string]any{
		"action": "view",
		"name":   "paged-view-skill",
		"offset": 10,
		"limit":  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.Output, "0123456789") || strings.Contains(first.Output, "abcdefghij") {
		t.Fatalf("first page returned wrong slice: %s", first.Output)
	}
	if !strings.Contains(second.Output, "abcdefghij") || strings.Contains(second.Output, "0123456789") {
		t.Fatalf("second page returned wrong slice: %s", second.Output)
	}
	firstRevision := outputLineValue(t, first.Output, "expected_revision: ")
	secondRevision := outputLineValue(t, second.Output, "expected_revision: ")
	if firstRevision == "" || firstRevision != secondRevision {
		t.Fatalf("page revisions differ: %q != %q", firstRevision, secondRevision)
	}
}

func outputLineValue(t *testing.T, output, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("output lacks line prefix %q", prefix)
	return ""
}

// TestSkillManageTool_Execute_viewNotFound returns not found message.
func TestSkillManageTool_Execute_viewNotFound(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	tool, _ := NewSkillManageTool(svc)
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "view",
		"name":   "nonexistent-skill",
	})
	if err != nil {
		t.Fatalf("view returned error: %v", err)
	}
	if !strings.Contains(result.Output, "not found") {
		t.Errorf("expected 'not found' message, got: %s", result.Output)
	}
}

// TestSkillManageTool_Execute_listEmpty returns message when no skills.
func TestSkillManageTool_Execute_listEmpty(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	tool, _ := NewSkillManageTool(svc)
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	if !strings.Contains(result.Output, "No autogenerated skills") {
		t.Errorf("expected empty list message, got: %s", result.Output)
	}
}

// TestSkillManageTool_Execute_disabledService fails when service is disabled.
func TestSkillManageTool_Execute_disabledService(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := DefaultConfig() // ModeNever
	svc, _ := NewService(cfg, reg, nil)
	tool, _ := NewSkillManageTool(svc)

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":       "create",
		"name":         "disabled-test",
		"description":  "Test",
		"instructions": "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in the test.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "failed") {
		t.Errorf("expected failure message, got: %q", result.Output)
	}
}

// TestSkillManageTool_Parameters returns valid schema.
func TestSkillManageTool_Parameters(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	tool, _ := NewSkillManageTool(svc)

	params := tool.Parameters()
	if params == nil {
		t.Error("expected non-nil parameters")
	}
	schema := params.(map[string]any)
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["append"]; !ok {
		t.Fatal("patch append option is missing from the schema")
	}
	nameSchema := properties["name"].(map[string]any)
	if nameSchema["pattern"] != "^[a-z0-9]+(-[a-z0-9]+)*$" {
		t.Fatalf("name schema does not document the enforced grammar: %#v", nameSchema)
	}
	instructions := properties["instructions"].(map[string]any)["description"].(string)
	if strings.Contains(instructions, "≥200") {
		t.Fatalf("instruction schema promises an unconditional minimum: %s", instructions)
	}
}

// TestSkillManageTool_IsIdempotent returns false.
func TestSkillManageTool_IsIdempotent(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	tool, _ := NewSkillManageTool(svc)

	if tool.IsIdempotent() {
		t.Error("expected non-idempotent (write operation)")
	}
}

// TestSkillManageTool_Execute_patch patches an existing skill.
func TestSkillManageTool_Execute_patch(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	svc, _ := NewService(cfg, reg, nil)

	// Create a skill first
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "patch-tool-test",
		Description:   "Original",
		Instructions:  strings.Repeat("Original instructions. ", 20),
		TriggerReason: TriggerManual,
	})

	tool, _ := NewSkillManageTool(svc)
	revision, _ := svc.CurrentRevision(context.Background(), "patch-tool-test")
	result, err := tool.Execute(context.Background(), map[string]any{
		"action":            "patch",
		"name":              "patch-tool-test",
		"instructions":      strings.Repeat("Improved instructions. ", 20),
		"expected_revision": revision,
	})
	if err != nil {
		t.Fatalf("patch returned error: %v", err)
	}
	if !strings.Contains(result.Output, "Patched") {
		t.Errorf("expected patch success message, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "1.0.1") {
		t.Errorf("expected new version in output, got: %s", result.Output)
	}
}

// TestSkillManageTool_Execute_archive moves a skill to archive via SkillManage.
func TestSkillManageTool_Execute_archive(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	svc, _ := NewService(cfg, reg, nil)

	// Create a skill
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "archive-me",
		Description:   "To be archived",
		Instructions:  strings.Repeat("Archive me. ", 20),
		TriggerReason: TriggerManual,
	})

	// Wire up a curator so archive action works
	curator := NewCurator(CuratorConfig{}, svc.factory, filepath.Join(dir, ".curator_state"))
	svc.SetCurator(curator)

	tool, _ := NewSkillManageTool(svc)
	revision, _ := svc.CurrentRevision(context.Background(), "archive-me")
	result, err := tool.Execute(context.Background(), map[string]any{
		"action":            "archive",
		"name":              "archive-me",
		"pruning_reason":    "obsolete test fixture",
		"expected_revision": revision,
	})
	if err != nil {
		t.Fatalf("archive returned error: %v", err)
	}
	if !strings.Contains(result.Output, "Archived") {
		t.Errorf("expected archive success message, got: %s", result.Output)
	}

	// Verify skill was physically moved
	archivedPath := filepath.Join(dir, "archive", "archive-me")
	if _, err := os.Stat(archivedPath); os.IsNotExist(err) {
		t.Error("archived skill should exist in archive/ subdirectory")
	}
	originalPath := filepath.Join(dir, "archive-me")
	if _, err := os.Stat(originalPath); !os.IsNotExist(err) {
		t.Error("original skill dir should not exist after archive")
	}
}

func TestSkillManageTool_CreateRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	svc, _ := NewService(cfg, reg, nil)
	tool, _ := NewSkillManageTool(svc)
	params := map[string]any{
		"action":       "create",
		"name":         "class-level-skill",
		"description":  "A class-level workflow",
		"instructions": strings.Repeat("Preserved instructions. ", 12),
	}
	first, _ := tool.Execute(context.Background(), params)
	if !strings.Contains(first.Output, "Created skill") {
		t.Fatalf("first create failed: %s", first.Output)
	}
	second, _ := tool.Execute(context.Background(), params)
	if !strings.Contains(second.Output, "already exists") ||
		!strings.Contains(second.Output, "patch") {
		t.Fatalf("expected patch-oriented overwrite refusal, got: %s", second.Output)
	}
}

func TestSkillManageTool_WriteFilePreservesNarrowContextInPackage(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	svc, _ := NewService(cfg, reg, nil)
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "provider-debugging",
		Description:   "Debug provider integrations",
		Instructions:  strings.Repeat("Provider workflow. ", 15),
		TriggerReason: TriggerManual,
	})
	tool, _ := NewSkillManageTool(svc)
	revision, _ := svc.CurrentRevision(context.Background(), "provider-debugging")

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":            "write_file",
		"name":              "provider-debugging",
		"file_path":         "references/claude-401.md",
		"file_content":      "Exact reproduction and recovery details.",
		"expected_revision": revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Wrote support file") {
		t.Fatalf("write_file failed: %s", result.Output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "provider-debugging", "references", "claude-401.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "Exact reproduction and recovery details." {
		t.Fatalf("unexpected support content: %q", data)
	}
}

func TestSkillManageTool_WriteFileRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	svc, _ := NewService(cfg, reg, nil)
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "safe-package",
		Description:   "Safe package",
		Instructions:  strings.Repeat("Safe instructions. ", 15),
		TriggerReason: TriggerManual,
	})
	tool, _ := NewSkillManageTool(svc)

	result, _ := tool.Execute(context.Background(), map[string]any{
		"action":       "write_file",
		"name":         "safe-package",
		"file_path":    "../escaped.md",
		"file_content": "must not escape",
	})
	if !strings.Contains(result.Output, "must stay inside") {
		t.Fatalf("expected traversal rejection, got: %s", result.Output)
	}
}

func TestSkillManageTool_ArchiveRequiresKnowledgeDisposition(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	tool, _ := NewSkillManageTool(svc)
	result, _ := tool.Execute(context.Background(), map[string]any{
		"action": "archive",
		"name":   "narrow-skill",
	})
	if !strings.Contains(result.Output, "absorbed_into") {
		t.Fatalf("expected archive provenance requirement, got: %s", result.Output)
	}
}

func TestSkillManageTool_AutonomousWriteRequiresView(t *testing.T) {
	dir := t.TempDir()
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: dir}
	svc, _ := NewService(cfg, reg, nil)
	factory, _ := NewSkillFactory(cfg, reg, nil)
	factory.Create(CreateOptions{
		Name:          "loaded-first",
		Description:   "Existing umbrella",
		Instructions:  strings.Repeat("Existing workflow. ", 15),
		TriggerReason: TriggerManual,
	})
	tool, _ := NewSkillManageTool(svc)
	revision, _ := svc.CurrentRevision(context.Background(), "loaded-first")
	tool.RequireReadBeforeWrite()

	blocked, _ := tool.Execute(context.Background(), map[string]any{
		"action":       "patch",
		"name":         "loaded-first",
		"instructions": strings.Repeat("Updated workflow. ", 15),
	})
	if !strings.Contains(blocked.Output, "must be viewed") {
		t.Fatalf("expected read-before-write refusal, got: %s", blocked.Output)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{
		"action": "view",
		"name":   "loaded-first",
	}); err != nil {
		t.Fatal(err)
	}
	allowed, _ := tool.Execute(context.Background(), map[string]any{
		"action":            "patch",
		"name":              "loaded-first",
		"instructions":      strings.Repeat("Updated workflow. ", 15),
		"expected_revision": revision,
	})
	if !strings.Contains(allowed.Output, "Patched skill") {
		t.Fatalf("expected patch after view, got: %s", allowed.Output)
	}
}

func TestSkillManageTool_ReviewAllowsNoMutationOutcome(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	tool, _ := NewSkillManageTool(svc)
	result, err := tool.Execute(context.Background(), map[string]any{
		"action":        "review",
		"review_reason": "the issue was a one-off environment outage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "without mutation") {
		t.Fatalf("expected valid no-mutation outcome, got: %s", result.Output)
	}
}
