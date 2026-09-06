package hooks

import "testing"

func TestMetaNudgeBudgetOnlyOnePerWindow(t *testing.T) {
	b := NewMetaNudgeBudget(5)
	b.RecordUserTurn("s")
	if _, ok := b.TryClaim("s", MetaNudgeSkillReview); !ok {
		t.Fatal("first claim denied")
	}
	if _, ok := b.TryClaim("s", MetaNudgeMaintenance); ok {
		t.Fatal("second claim in window allowed")
	}
	for i := 0; i < 4; i++ {
		b.RecordUserTurn("s")
	}
	if _, ok := b.TryClaim("s", MetaNudgeBlock); ok {
		t.Fatal("claim allowed before five-turn interval")
	}
	b.RecordUserTurn("s")
	if seq, ok := b.TryClaim("s", MetaNudgeBlock); !ok || seq != 2 {
		t.Fatalf("next-window claim = (%d,%v), want (2,true)", seq, ok)
	}
}

func TestMetaNudgePriorityOrder(t *testing.T) {
	priorities := []MetaNudgePriority{MetaNudgeBlock, MetaNudgeSkillReview, MetaNudgeMaintenance, MetaNudgeTask}
	for i := 1; i < len(priorities); i++ {
		if priorities[i-1] <= priorities[i] {
			t.Fatalf("priority order invalid: %v", priorities)
		}
	}
	b := NewMetaNudgeBudget(1)
	b.RecordUserTurn("s")
	if _, ok := b.TryClaim("s", MetaNudgeBlock); !ok {
		t.Fatal("highest-priority claim denied")
	}
	for _, lower := range priorities[1:] {
		if _, ok := b.TryClaim("s", lower); ok {
			t.Fatalf("lower-priority claim %d allowed", lower)
		}
	}
}
