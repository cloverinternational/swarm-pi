package plan_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plan"
)

// ─── DefaultConfig ────────────────────────────────────────────────────────────

func TestDefaultConfig_Enabled(t *testing.T) {
	cfg := plan.DefaultConfig()
	if !cfg.Enabled {
		t.Error("DefaultConfig: Enabled should be true")
	}
}

func TestDefaultConfig_PlanFileName(t *testing.T) {
	cfg := plan.DefaultConfig()
	if cfg.PlanFileName != "plan.md" {
		t.Errorf("DefaultConfig.PlanFileName = %q, want %q", cfg.PlanFileName, "plan.md")
	}
}

func TestDefaultConfig_AutoClearContextFalse(t *testing.T) {
	cfg := plan.DefaultConfig()
	if cfg.AutoClearContext {
		t.Error("DefaultConfig.AutoClearContext should default to false")
	}
}

func TestDefaultConfig_WorkDirEmpty(t *testing.T) {
	cfg := plan.DefaultConfig()
	// WorkDir is intentionally empty so it resolves to os.Getwd() at runtime
	if cfg.WorkDir != "" {
		t.Errorf("DefaultConfig.WorkDir = %q, want empty string", cfg.WorkDir)
	}
}

// ─── PlanFilePath ─────────────────────────────────────────────────────────────

func TestPlanFilePath_UsesWorkDir(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{
		PlanFileName: "MY_PLAN.md",
		WorkDir:      dir,
	}
	got := plan.PlanFilePath(cfg)
	want := filepath.Join(dir, "MY_PLAN.md")
	if got != want {
		t.Errorf("PlanFilePath = %q, want %q", got, want)
	}
}

func TestPlanFilePath_DefaultsToCurrentDir(t *testing.T) {
	cfg := plan.DefaultConfig()
	got := plan.PlanFilePath(cfg)
	if !filepath.IsAbs(got) {
		t.Errorf("PlanFilePath should be absolute, got %q", got)
	}
	if filepath.Base(got) != "plan.md" {
		t.Errorf("PlanFilePath base = %q, want plan.md", filepath.Base(got))
	}
}

func TestPlanFilePath_EmptyFileNameDefaultsToPLAN(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{
		PlanFileName: "",
		WorkDir:      dir,
	}
	got := plan.PlanFilePath(cfg)
	if filepath.Base(got) != "plan.md" {
		t.Errorf("PlanFilePath with empty FileName = %q, want plan.md as base", got)
	}
}

// ─── ReadPlanFile / WritePlanFile ─────────────────────────────────────────────

func TestWriteAndReadPlanFile_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	content := "# My Plan\n\n1. Step one\n2. Step two"

	if err := plan.WritePlanFile(cfg, content); err != nil {
		t.Fatalf("WritePlanFile: %v", err)
	}

	got, err := plan.ReadPlanFile(cfg)
	if err != nil {
		t.Fatalf("ReadPlanFile: %v", err)
	}
	if got != content {
		t.Errorf("ReadPlanFile = %q, want %q", got, content)
	}
}

func TestReadPlanFile_NonExistent_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}

	got, err := plan.ReadPlanFile(cfg)
	if err != nil {
		t.Fatalf("ReadPlanFile on missing file: %v", err)
	}
	if got != "" {
		t.Errorf("ReadPlanFile on missing file = %q, want empty string", got)
	}
}

func TestWritePlanFile_StripsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	content := "plan content"

	if err := plan.WritePlanFile(cfg, content); err != nil {
		t.Fatalf("WritePlanFile: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "PLAN.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// WritePlanFile appends a newline; ReadPlanFile trims it — verify the file ends in \n
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("WritePlanFile should append a newline to the file")
	}
}

func TestWritePlanFile_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{
		PlanFileName: "PLAN.md",
		WorkDir:      filepath.Join(dir, "nonexistent", "nested"),
	}
	if err := plan.WritePlanFile(cfg, "some plan"); err != nil {
		t.Fatalf("WritePlanFile should create parent dirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nonexistent", "nested", "PLAN.md")); err != nil {
		t.Errorf("Plan file not found after WritePlanFile: %v", err)
	}
}

// ─── NoopPlanBroker ───────────────────────────────────────────────────────────

func TestNoopPlanBroker_EnterPlanMode_NoError(t *testing.T) {
	var b plan.NoopPlanBroker
	if err := b.EnterPlanMode(context.Background()); err != nil {
		t.Errorf("NoopPlanBroker.EnterPlanMode error: %v", err)
	}
}

func TestNoopPlanBroker_RequestPlanApproval_AutoApproves(t *testing.T) {
	var b plan.NoopPlanBroker
	content := "# My implementation plan"
	resp, err := b.RequestPlanApproval(context.Background(), content)
	if err != nil {
		t.Fatalf("RequestPlanApproval: %v", err)
	}
	if !resp.Approved {
		t.Error("NoopPlanBroker should auto-approve all plans")
	}
	if resp.EditedPlan != content {
		t.Errorf("EditedPlan = %q, want %q", resp.EditedPlan, content)
	}
	if resp.ClearContext {
		t.Error("NoopPlanBroker should not request context clearing")
	}
}

// ─── PlanModeSystemPrompt ─────────────────────────────────────────────────────

func TestPlanModeSystemPrompt_NonEmpty(t *testing.T) {
	cfg := plan.DefaultConfig()
	prompt := plan.PlanModeSystemPrompt(cfg)
	if strings.TrimSpace(prompt) == "" {
		t.Error("PlanModeSystemPrompt should not be empty")
	}
}

func TestPlanModeSystemPrompt_ContainsPlanMode(t *testing.T) {
	cfg := plan.DefaultConfig()
	prompt := plan.PlanModeSystemPrompt(cfg)
	if !strings.Contains(prompt, "Plan Mode") && !strings.Contains(prompt, "plan mode") {
		t.Errorf("PlanModeSystemPrompt should mention Plan Mode, got: %q", prompt[:100])
	}
}

func TestPlanModeSystemPrompt_ContainsWorkspaceFileWorkflow(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "MY_PLAN.md", WorkDir: dir}
	prompt := plan.PlanModeSystemPrompt(cfg)
	if !strings.Contains(prompt, dir) || !strings.Contains(prompt, "plan_file") {
		t.Errorf("PlanModeSystemPrompt should describe workspace-local plan_file submission: %q", prompt)
	}
	if strings.Contains(prompt, "ONLY the exact plan artifact") {
		t.Errorf("PlanModeSystemPrompt still describes a plan-only authorization exception: %q", prompt)
	}
}

// ─── EnterPlanModeTool ────────────────────────────────────────────────────────

func TestEnterPlanModeTool_Name(t *testing.T) {
	tool := plan.NewEnterPlanModeTool(nil, plan.DefaultConfig())
	if tool.Name() != "enter_plan_mode" {
		t.Errorf("Name = %q, want enter_plan_mode", tool.Name())
	}
}

func TestEnterPlanModeTool_Description_NonEmpty(t *testing.T) {
	tool := plan.NewEnterPlanModeTool(nil, plan.DefaultConfig())
	if strings.TrimSpace(tool.Description()) == "" {
		t.Error("EnterPlanModeTool.Description() should not be empty")
	}
}

func TestEnterPlanModeTool_Validate_AcceptsEmpty(t *testing.T) {
	tool := plan.NewEnterPlanModeTool(nil, plan.DefaultConfig())
	if err := tool.Validate(nil); err != nil {
		t.Errorf("EnterPlanModeTool.Validate should accept empty params: %v", err)
	}
}

func TestEnterPlanModeTool_Execute_NilBroker_Succeeds(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	tool := plan.NewEnterPlanModeTool(nil, cfg)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}
	if result.IsError {
		t.Errorf("Execute should succeed with nil broker, got error: %v", result.Output)
	}
}

func TestEnterPlanModeTool_Execute_NilBroker_MentionsPlanMode(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	tool := plan.NewEnterPlanModeTool(nil, cfg)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Output, "plan mode") && !strings.Contains(result.Output, "PLAN MODE") {
		t.Errorf("Execute result should mention plan mode, got: %q", result.Output)
	}
}

func TestEnterPlanModeTool_Execute_WithNoopBroker_CallsEnterPlanMode(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	broker := &plan.NoopPlanBroker{}
	tool := plan.NewEnterPlanModeTool(broker, cfg)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("Execute with NoopBroker should succeed, got error: %v", result.Output)
	}
}

// ─── ExitPlanModeTool ─────────────────────────────────────────────────────────

func TestExitPlanModeTool_Name(t *testing.T) {
	tool := plan.NewExitPlanModeTool(nil, plan.DefaultConfig())
	if tool.Name() != "exit_plan_mode" {
		t.Errorf("Name = %q, want exit_plan_mode", tool.Name())
	}
}

func TestExitPlanModeTool_Description_NonEmpty(t *testing.T) {
	tool := plan.NewExitPlanModeTool(nil, plan.DefaultConfig())
	if strings.TrimSpace(tool.Description()) == "" {
		t.Error("ExitPlanModeTool.Description() should not be empty")
	}
}

func TestExitPlanModeTool_Validate_EmptyPlan_Passes(t *testing.T) {
	// Empty plan is now allowed - exit_plan_mode falls back to PLAN.md
	tool := plan.NewExitPlanModeTool(nil, plan.DefaultConfig())
	err := tool.Validate(map[string]any{"plan": ""})
	if err != nil {
		t.Errorf("Validate should allow empty plan (PLAN.md fallback): %v", err)
	}
}

func TestExitPlanModeTool_Validate_NonEmptyPlan_Passes(t *testing.T) {
	tool := plan.NewExitPlanModeTool(nil, plan.DefaultConfig())
	err := tool.Validate(map[string]any{"plan": "step 1: do something"})
	if err != nil {
		t.Errorf("Validate should pass non-empty plan: %v", err)
	}
}

func TestExitPlanModeTool_Execute_NilBroker_AutoApproves(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	tool := plan.NewExitPlanModeTool(nil, cfg)

	planContent := "# Implementation Plan\n\n1. Do the thing"
	result, err := tool.Execute(context.Background(), map[string]any{"plan": planContent})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("Execute with nil broker should auto-approve, got error: %v", result.Output)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Output), &decoded); err != nil {
		t.Fatalf("Execute result is not valid JSON: %v", err)
	}
	if approved, ok := decoded["approved"].(bool); !ok || !approved {
		t.Errorf("nil-broker result should have approved=true, got: %v", decoded["approved"])
	}
}

func TestExitPlanModeTool_Execute_WithNoopBroker_AutoApproves(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	broker := &plan.NoopPlanBroker{}
	tool := plan.NewExitPlanModeTool(broker, cfg)

	planContent := "# Implementation Plan\n\n1. Do the thing"
	result, err := tool.Execute(context.Background(), map[string]any{"plan": planContent})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("Execute with NoopBroker should succeed, got error: %v", result.Output)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Output), &decoded); err != nil {
		t.Fatalf("Execute result is not valid JSON: %v", err)
	}
	if approved, ok := decoded["approved"].(bool); !ok || !approved {
		t.Errorf("NoopBroker result should have approved=true, got: %v", decoded["approved"])
	}
}

func TestExitPlanModeTool_Execute_WritesAndReadsFromPlanFile(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	tool := plan.NewExitPlanModeTool(nil, cfg)

	planContent := "# Plan\n\nDo stuff"
	_, err := tool.Execute(context.Background(), map[string]any{"plan": planContent})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Plan file should have been written
	saved, err := plan.ReadPlanFile(cfg)
	if err != nil {
		t.Fatalf("ReadPlanFile after Execute: %v", err)
	}
	if saved != planContent {
		t.Errorf("Plan file content = %q, want %q", saved, planContent)
	}
}

func TestExitPlanModeTool_Execute_FallsBackToPlanFile(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}

	// Pre-write a plan file
	filePlan := "# Plan from file\n\n1. Something"
	if err := plan.WritePlanFile(cfg, filePlan); err != nil {
		t.Fatalf("WritePlanFile: %v", err)
	}

	// Call with empty plan param — should fall back to file
	tool := plan.NewExitPlanModeTool(nil, cfg)
	result, err := tool.Execute(context.Background(), map[string]any{"plan": ""})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("Execute should succeed with file fallback, got error: %v", result.Output)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Output), &decoded); err != nil {
		t.Fatalf("Result is not valid JSON: %v", err)
	}
	if approved, ok := decoded["approved"].(bool); !ok || !approved {
		t.Errorf("Expected approved=true, got: %v", decoded["approved"])
	}
}

func TestExitPlanModeTool_Execute_EmptyPlanNoFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	tool := plan.NewExitPlanModeTool(nil, cfg)

	// No plan param, no plan file
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Error("Execute with no plan and no file should return an error result")
	}
}

func TestExitPlanModeTool_Execute_ClearedContextField(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{PlanFileName: "PLAN.md", WorkDir: dir}
	// NoopBroker returns ClearContext=false
	broker := &plan.NoopPlanBroker{}
	tool := plan.NewExitPlanModeTool(broker, cfg)

	result, err := tool.Execute(context.Background(), map[string]any{
		"plan": "# Plan\n\nDo stuff",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Output), &decoded); err != nil {
		t.Fatalf("Result is not valid JSON: %v", err)
	}
	if clearCtx, ok := decoded["clear_context"].(bool); !ok || clearCtx {
		t.Errorf("clear_context should be false for NoopBroker, got: %v", decoded["clear_context"])
	}
}

// ─── Session-scoped plan file path ───────────────────────────────────────────

func TestPlanFilePath_WithSessionID_IsSessionScoped(t *testing.T) {
	// Isolate the SwarmOS root so the session-scoped path resolves under a
	// temp dir rather than the developer's real ~/.swarm.
	t.Setenv("SWARM_HOME", t.TempDir())

	cfg := plan.Config{
		PlanFileName: "plan.md",
		SessionID:    "test-session-abc123",
	}
	got := plan.PlanFilePath(cfg)

	// Should be in ~/.swarm/conversations/<sessionID>/plan.md
	// (paths.ConversationsDir()), not in WorkDir or CWD.
	if !strings.Contains(got, "test-session-abc123") {
		t.Errorf("Session-scoped path should contain session ID, got: %q", got)
	}
	if !strings.HasPrefix(got, paths.ConversationsDir()) {
		t.Errorf("Session-scoped path should be under the canonical conversations dir %q, got: %q", paths.ConversationsDir(), got)
	}
	if filepath.Base(got) != "plan.md" {
		t.Errorf("PlanFilePath base = %q, want plan.md", filepath.Base(got))
	}
}

func TestPlanFilePath_WithSessionID_IgnoresWorkDir(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{
		PlanFileName: "plan.md",
		SessionID:    "test-session-xyz",
		WorkDir:      dir,
	}
	got := plan.PlanFilePath(cfg)

	// SessionID takes priority over WorkDir
	if strings.HasPrefix(got, dir) {
		t.Errorf("When SessionID is set, path should not be under WorkDir (%q), got: %q", dir, got)
	}
	if !strings.Contains(got, "test-session-xyz") {
		t.Errorf("Session-scoped path should contain session ID, got: %q", got)
	}
}

func TestPlanFilePath_WithoutSessionID_UsesWorkDir(t *testing.T) {
	dir := t.TempDir()
	cfg := plan.Config{
		PlanFileName: "MYPLAN.md",
		SessionID:    "", // no session ID
		WorkDir:      dir,
	}
	got := plan.PlanFilePath(cfg)

	want := filepath.Join(dir, "MYPLAN.md")
	if got != want {
		t.Errorf("Without SessionID, PlanFilePath = %q, want %q", got, want)
	}
}

func TestPlanModeInstructionsDescribeCeremonyWithoutAuthorizationChanges(t *testing.T) {
	cfg := plan.Config{WorkDir: t.TempDir(), PlanFileName: "session-plan.md"}

	prompt := plan.PlanModeSystemPrompt(cfg)
	if !strings.Contains(prompt, "does not change tool authorization") || !strings.Contains(prompt, "plan_file") {
		t.Fatalf("system prompt must describe ceremony semantics and file submission: %q", prompt)
	}

	description := plan.NewEnterPlanModeTool(nil, cfg).Description()
	if !strings.Contains(description, "approval ceremony") || strings.Contains(description, "exact session plan artifact") {
		t.Fatalf("tool description must describe ceremony semantics without an artifact exception: %q", description)
	}
}

func TestCanonicalPlanCopiesRemainSessionScoped(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	workspace := t.TempDir()
	cfgA := plan.Config{SessionID: "session-a", PlanFileName: "plan.md", WorkDir: workspace}
	cfgB := plan.Config{SessionID: "session-b", PlanFileName: "plan.md", WorkDir: workspace}

	if result, err := plan.NewExitPlanModeTool(nil, cfgA).Execute(context.Background(), map[string]any{"plan": "# A"}); err != nil || result.IsError {
		t.Fatalf("exit session A: result=%v err=%v", result, err)
	}
	if result, err := plan.NewExitPlanModeTool(nil, cfgB).Execute(context.Background(), map[string]any{"plan": "# B"}); err != nil || result.IsError {
		t.Fatalf("exit session B: result=%v err=%v", result, err)
	}

	gotA, err := plan.ReadPlanFile(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := plan.ReadPlanFile(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	if gotA != "# A" || gotB != "# B" {
		t.Fatalf("canonical copies crossed sessions: A=%q B=%q", gotA, gotB)
	}
}
