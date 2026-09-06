package plan_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plan"
)

type editedPlanBroker struct {
	edited string
	seen   string
}

func (b *editedPlanBroker) EnterPlanMode(context.Context) error { return nil }

func (b *editedPlanBroker) RequestPlanApproval(_ context.Context, submitted string) (plan.ApprovalResponse, error) {
	b.seen = submitted
	return plan.ApprovalResponse{Approved: true, EditedPlan: b.edited}, nil
}

type rejectedPlanBroker struct{}

func (b *rejectedPlanBroker) EnterPlanMode(context.Context) error { return nil }

func (b *rejectedPlanBroker) RequestPlanApproval(_ context.Context, _ string) (plan.ApprovalResponse, error) {
	return plan.ApprovalResponse{Approved: false, Feedback: "Revise it"}, nil
}

func TestReadSubmittedPlanFileWorkspaceBoundaryAndTextValidation(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	cfg := plan.Config{WorkDir: workspace}

	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	valid := filepath.Join(workspace, "plans", "meaningful.md")
	if err := os.MkdirAll(filepath.Dir(valid), 0o755); err != nil {
		t.Fatal(err)
	}
	write(valid, []byte("# Local plan\n\nDo the work.\n"))

	got, err := plan.ReadSubmittedPlanFile(cfg, filepath.Join("plans", "meaningful.md"))
	if err != nil {
		t.Fatalf("valid relative plan: %v", err)
	}
	if got != "# Local plan\n\nDo the work." {
		t.Fatalf("valid plan content = %q", got)
	}

	outsideFile := filepath.Join(outside, "outside.md")
	write(outsideFile, []byte("# Outside"))
	for name, candidate := range map[string]string{
		"absolute escape": outsideFile,
		"relative escape": filepath.Join("..", filepath.Base(outside), "outside.md"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := plan.ReadSubmittedPlanFile(cfg, candidate); err == nil || !strings.Contains(err.Error(), "outside workspace") {
				t.Fatalf("ReadSubmittedPlanFile(%q) error = %v, want workspace rejection", candidate, err)
			}
		})
	}

	link := filepath.Join(workspace, "outside-link.md")
	if err := os.Symlink(outsideFile, link); err == nil {
		if _, err := plan.ReadSubmittedPlanFile(cfg, link); err == nil {
			t.Fatal("symlink escape was accepted")
		}
	}

	empty := filepath.Join(workspace, "empty.md")
	write(empty, []byte(" \n\t"))
	if _, err := plan.ReadSubmittedPlanFile(cfg, empty); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty file error = %v", err)
	}

	binary := filepath.Join(workspace, "binary.bin")
	write(binary, []byte{'p', 'l', 'a', 'n', 0, 'x'})
	if _, err := plan.ReadSubmittedPlanFile(cfg, binary); err == nil || !strings.Contains(err.Error(), "UTF-8 text") {
		t.Fatalf("binary file error = %v", err)
	}

	invalidUTF8 := filepath.Join(workspace, "invalid.txt")
	write(invalidUTF8, []byte{0xff, 0xfe})
	if _, err := plan.ReadSubmittedPlanFile(cfg, invalidUTF8); err == nil || !strings.Contains(err.Error(), "UTF-8 text") {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}

	if _, err := plan.ReadSubmittedPlanFile(cfg, workspace); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory error = %v", err)
	}

	fifo := filepath.Join(workspace, "blocking.fifo")
	if err := exec.Command("mkfifo", fifo).Run(); err == nil {
		done := make(chan error, 1)
		go func() {
			_, err := plan.ReadSubmittedPlanFile(cfg, fifo)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("FIFO error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("FIFO plan submission blocked while opening")
		}
	}

	oversized := filepath.Join(workspace, "oversized.md")
	write(oversized, []byte(strings.Repeat("x", int(plan.MaxSubmittedPlanBytes)+1)))
	if _, err := plan.ReadSubmittedPlanFile(cfg, oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized file error = %v", err)
	}
}

func TestExitPlanModeToolCopiesWorkspaceFileWithoutChangingSource(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	workspace := t.TempDir()
	source := filepath.Join(workspace, "feature-plan.md")
	original := "# Feature plan\n\n1. Implement\n2. Verify\n"
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := plan.Config{WorkDir: workspace, SessionID: "copy-session"}
	result, err := plan.NewExitPlanModeTool(nil, cfg).Execute(context.Background(), map[string]any{
		"plan_file": "feature-plan.md",
	})
	if err != nil || result.IsError {
		t.Fatalf("Execute result=%v err=%v", result, err)
	}

	sourceAfter, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceAfter) != original {
		t.Fatalf("source plan changed: got %q want %q", sourceAfter, original)
	}

	canonical, err := plan.ReadPlanFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != strings.TrimSpace(original) {
		t.Fatalf("canonical plan = %q", canonical)
	}
}

func TestExitPlanModeToolPersistsUserEditedApproval(t *testing.T) {
	defer builtin.ResetPlanModeForTest()
	builtin.PlanModeEntered()
	t.Setenv("SWARM_HOME", t.TempDir())
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "draft.md"), []byte("# Draft"), 0o600); err != nil {
		t.Fatal(err)
	}

	broker := &editedPlanBroker{edited: "# Approved edit"}
	cfg := plan.Config{WorkDir: workspace, SessionID: "edited-session"}
	result, err := plan.NewExitPlanModeTool(broker, cfg).Execute(context.Background(), map[string]any{
		"plan_file": "draft.md",
	})
	if err != nil || result.IsError {
		t.Fatalf("Execute result=%v err=%v", result, err)
	}
	if broker.seen != "# Draft" {
		t.Fatalf("broker saw %q", broker.seen)
	}

	canonical, err := plan.ReadPlanFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != broker.edited {
		t.Fatalf("canonical edited plan = %q, want %q", canonical, broker.edited)
	}
	if builtin.IsInPlanMode() {
		t.Fatal("approved plan left plan mode active")
	}
}

func TestExitPlanModeToolRejectionKeepsPlanModeActive(t *testing.T) {
	defer builtin.ResetPlanModeForTest()
	builtin.PlanModeEntered()
	t.Setenv("SWARM_HOME", t.TempDir())

	tool := plan.NewExitPlanModeTool(&rejectedPlanBroker{}, plan.Config{
		WorkDir:   t.TempDir(),
		SessionID: "rejected-session",
	})
	result, err := tool.Execute(context.Background(), map[string]any{"plan": "# Draft"})
	if err != nil || result.IsError {
		t.Fatalf("Execute result=%v err=%v", result, err)
	}
	if !builtin.IsInPlanMode() {
		t.Fatal("rejected plan exited plan mode")
	}
}

func TestExitPlanModeToolRejectsAmbiguousInputs(t *testing.T) {
	tool := plan.NewExitPlanModeTool(nil, plan.Config{WorkDir: t.TempDir()})
	params := map[string]any{"plan": "# Inline", "plan_file": "plan.md"}
	if err := tool.Validate(params); err == nil {
		t.Fatal("Validate accepted both plan and plan_file")
	}
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Output, "not both") {
		t.Fatalf("ambiguous Execute result = %+v", result)
	}
}

func TestExitPlanModeToolSchemaExposesPlanFile(t *testing.T) {
	tool := plan.NewExitPlanModeTool(nil, plan.Config{WorkDir: t.TempDir()})
	schema, ok := tool.Parameters().(map[string]any)
	if !ok {
		t.Fatalf("Parameters() type = %T", tool.Parameters())
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties type = %T", schema["properties"])
	}
	if _, ok := properties["plan_file"]; !ok {
		t.Fatalf("plan_file missing from schema: %#v", properties)
	}
	if _, ok := properties["plan"]; !ok {
		t.Fatalf("legacy plan missing from schema: %#v", properties)
	}
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
}

func TestExitPlanModeToolValidatesInlineLegacyAndEditedContent(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	invalid := []struct {
		name    string
		content string
	}{
		{name: "oversized", content: strings.Repeat("x", int(plan.MaxSubmittedPlanBytes)+1)},
		{name: "nul", content: "plan\x00content"},
		{name: "invalid utf8", content: string([]byte{0xff, 0xfe})},
	}

	for _, tc := range invalid {
		t.Run("inline/"+tc.name, func(t *testing.T) {
			tool := plan.NewExitPlanModeTool(nil, plan.Config{WorkDir: t.TempDir(), SessionID: "inline-" + tc.name})
			result, err := tool.Execute(context.Background(), map[string]any{"plan": tc.content})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatalf("invalid inline plan was accepted: %s", tc.name)
			}
		})
	}

	t.Run("legacy canonical", func(t *testing.T) {
		cfg := plan.Config{WorkDir: t.TempDir(), SessionID: "legacy-invalid"}
		if err := plan.WritePlanFile(cfg, "legacy\x00plan"); err != nil {
			t.Fatal(err)
		}
		result, err := plan.NewExitPlanModeTool(nil, cfg).Execute(context.Background(), map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatal("invalid legacy canonical plan was accepted")
		}
	})

	for _, tc := range invalid {
		t.Run("edited/"+tc.name, func(t *testing.T) {
			defer builtin.ResetPlanModeForTest()
			builtin.PlanModeEntered()
			cfg := plan.Config{WorkDir: t.TempDir(), SessionID: "edited-" + tc.name}
			broker := &editedPlanBroker{edited: tc.content}
			result, err := plan.NewExitPlanModeTool(broker, cfg).Execute(
				context.Background(), map[string]any{"plan": "# Valid draft"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatalf("invalid edited plan was accepted: %s", tc.name)
			}
			if !builtin.IsInPlanMode() {
				t.Fatal("edited-plan validation failure exited plan mode")
			}
		})
	}
}

func TestExitPlanModeToolRejectsWrongTypedInputs(t *testing.T) {
	tool := plan.NewExitPlanModeTool(nil, plan.Config{WorkDir: t.TempDir()})
	for _, params := range []map[string]any{
		{"plan": 123},
		{"plan_file": true},
	} {
		if err := tool.Validate(params); err == nil {
			t.Fatalf("Validate accepted %#v", params)
		}
	}
}
