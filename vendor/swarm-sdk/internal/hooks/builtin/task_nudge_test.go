package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func clearTaskNudgeTodos(t *testing.T) *ii.TodoManager {
	t.Helper()
	tm := ii.GetTodoManager()
	if tm == nil {
		t.Skip("no todo manager available")
	}
	tm.ClearTodos()
	t.Cleanup(func() { _ = tm.ClearTodos() })
	return tm
}

func TestTaskNudgeBudgetNoFireOnTurnOne(t *testing.T) {
	clearTaskNudgeTodos(t)
	budget := NewTaskNudgeBudget(TaskNudgeConfig{NudgeInterval: 5, ToolCallThreshold: 2})
	budget.RecordToolCall("c1")
	budget.RecordToolCall("c1")
	if _, ok := budget.CheckTurn(context.Background(), "c1", "design the feature"); ok {
		t.Fatal("nudge fired on first user turn")
	}
}

func TestTaskNudgeBudgetActivityAndCadence(t *testing.T) {
	clearTaskNudgeTodos(t)
	budget := NewTaskNudgeBudget(TaskNudgeConfig{NudgeInterval: 5, ToolCallThreshold: 2})

	if _, ok := budget.CheckTurn(context.Background(), "c1", "start"); ok {
		t.Fatal("nudge fired without tool activity")
	}
	budget.RecordToolCall("c1")
	if _, ok := budget.CheckTurn(context.Background(), "c1", "review architecture"); ok {
		t.Fatal("nudge fired below multi-step threshold")
	}
	budget.RecordToolCall("c1")
	msg, ok := budget.CheckTurn(context.Background(), "c1", "review architecture")
	if !ok {
		t.Fatal("nudge did not fire after multi-step activity")
	}
	if !strings.Contains(msg, taskNudgeText) || strings.Contains(msg, "\n") {
		t.Fatalf("unexpected nudge content: %q", msg)
	}
	if _, ok := budget.CheckTurn(context.Background(), "c1", "review architecture"); ok {
		t.Fatal("nudge ignored cadence")
	}
	if _, ok := budget.CheckTurn(context.Background(), "c1", "review architecture"); ok {
		t.Fatal("nudge ignored cadence")
	}
	if _, ok := budget.CheckTurn(context.Background(), "c1", "review architecture"); ok {
		t.Fatal("nudge ignored shared cadence")
	}
	if _, ok := budget.CheckTurn(context.Background(), "c1", "review architecture"); ok {
		t.Fatal("nudge ignored shared cadence")
	}
	if _, ok := budget.CheckTurn(context.Background(), "c1", "review architecture"); !ok {
		t.Fatal("nudge did not fire when cadence elapsed")
	}
}

func TestTaskNudgeBudgetSilentAfterTaskCreated(t *testing.T) {
	tm := clearTaskNudgeTodos(t)
	budget := NewTaskNudgeBudget(TaskNudgeConfig{ToolCallThreshold: 2})
	budget.CheckTurn(context.Background(), "c1", "start")
	budget.RecordToolCall("c1")
	budget.RecordToolCall("c1")
	if err := tm.SetTodos([]ii.TodoItem{{
		ID: "t1", Content: "work", Status: ii.TodoStatusCompleted, Priority: ii.TodoPriorityMedium,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := budget.CheckTurn(context.Background(), "c1", "continue"); ok {
		t.Fatal("nudge fired after a task had been created")
	}
}

func TestPromptSuggestsImmediateExecution(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   bool
	}{
		// Continuations.
		{"continue alone", "continue", true},
		{"continue with comma", "continue, please", true},
		{"go ahead", "go ahead and run it", true},
		{"keep going", "keep going where you left off", true},
		{"resume", "resume the merge", true},

		// Direct imperatives that the user expects to execute single-step.
		{"just run", "just run sentry", true},
		{"please run", "please run the email cli", true},
		{"merge", "merge gitea/main", true},
		{"pull", "pull from origin", true},
		{"show me", "show me the logs", true},
		{"check", "check what gold has", true},

		// Ambiguous / longer prompts: NOT suppressed (still nudge).
		{"empty", "", false},
		{"complex task", "I want to design a new gmail CLI that supports IMAP/SMTP and multi-account configuration", false},
		{"question", "what is the difference between bronze and silver", false},
		{"long imperative", "run a full backfill of the gold tables for the entire 2026-04-27 day window using the airflow DAGs and confirm via the metabase dashboard", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := promptSuggestsImmediateExecution(tc.prompt)
			if got != tc.want {
				t.Errorf("promptSuggestsImmediateExecution(%q) = %v, want %v", tc.prompt, got, tc.want)
			}
		})
	}
}

func TestCheckAndNudgeWithPrompt(t *testing.T) {
	tm := ii.GetTodoManager()
	if tm == nil {
		t.Skip("no todo manager available")
	}
	tm.ClearTodos()
	defer tm.ClearTodos()

	ctx := context.Background()

	// Imperative prompt with no tasks → suppressed.
	if _, ok := CheckAndNudgeWithPrompt(ctx, "just run email"); ok {
		t.Errorf("expected nudge suppressed on imperative prompt")
	}

	// Open-ended prompt with no tasks → still nudged.
	if _, ok := CheckAndNudgeWithPrompt(ctx, "design a gmail CLI with multi-account support"); !ok {
		t.Errorf("expected nudge on open-ended prompt")
	}

	// Empty prompt with no tasks → still nudged (back-compat with CheckAndNudge).
	if _, ok := CheckAndNudge(ctx); !ok {
		t.Errorf("expected nudge on empty prompt fallback")
	}
}
