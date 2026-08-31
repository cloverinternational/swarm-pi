package taskstore

import (
	"strings"
	"sync"
	"testing"
)

// helper: build a store backed by no file, suitable for in-memory tests.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return &Store{
		Version:        Version,
		ConversationID: "conv-test",
		Tasks:          []Task{},
	}
}

func mustAdd(t *testing.T, s *Store, task Task) {
	t.Helper()
	if err := s.AddTask(task); err != nil {
		t.Fatalf("AddTask(%q) failed: %v", task.ID, err)
	}
}

// TestAddTask_RejectsSelfParent asserts that a task that names itself as
// its own parent is rejected with ErrCycleDetected.
func TestAddTask_RejectsSelfParent(t *testing.T) {
	s := newTestStore(t)
	err := s.AddTask(Task{ID: "self", Subject: "loop", ParentID: "self"})
	if err != ErrCycleDetected {
		t.Fatalf("expected ErrCycleDetected on self-parent, got %v", err)
	}
}

// TestAddTask_RejectsCycle asserts that A → B → C → A is refused at the
// AddTask step (we already have A → B → C; AddTask of A with parent C
// would close the cycle).
func TestAddTask_RejectsCycleViaUpdate(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Task{ID: "A", Subject: "root"})
	mustAdd(t, s, Task{ID: "B", Subject: "child", ParentID: "A"})
	mustAdd(t, s, Task{ID: "C", Subject: "grandchild", ParentID: "B"})

	// Attempt to make A a child of C — that would close A→B→C→A.
	err := s.UpdateTask(Task{ID: "A", Subject: "root", ParentID: "C"})
	if err != ErrCycleDetected {
		t.Fatalf("expected ErrCycleDetected on cycle-creating update, got %v", err)
	}
}

// TestAddTask_RejectsUnknownParent asserts that pointing ParentID at an
// id that does not exist is rejected.
func TestAddTask_RejectsUnknownParent(t *testing.T) {
	s := newTestStore(t)
	err := s.AddTask(Task{ID: "child", Subject: "orphan", ParentID: "ghost"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected unknown-parent error, got %v", err)
	}
}

// TestChildrenOf_OrderedBySiblingIndex asserts that ChildrenOf returns
// children in SiblingIndex ascending order, with deleted children filtered.
func TestChildrenOf_OrderedBySiblingIndex(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Task{ID: "p", Subject: "parent"})
	mustAdd(t, s, Task{ID: "c2", Subject: "second", ParentID: "p", SiblingIndex: 2})
	mustAdd(t, s, Task{ID: "c1", Subject: "first", ParentID: "p", SiblingIndex: 1})
	mustAdd(t, s, Task{ID: "c3", Subject: "third", ParentID: "p", SiblingIndex: 3})

	if err := s.DeleteTask("c2"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	got := s.ChildrenOf("p")
	if len(got) != 2 {
		t.Fatalf("expected 2 live children, got %d", len(got))
	}
	if got[0].ID != "c1" || got[1].ID != "c3" {
		t.Fatalf("children out of order: got %s, %s; want c1, c3", got[0].ID, got[1].ID)
	}
}

// TestDisplayNumber_FlatLegacy returns a single ordinal for legacy
// top-level tasks (no ParentID, no SiblingIndex).
func TestDisplayNumber_FlatLegacy(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Task{ID: "L1", Subject: "legacy 1"})
	mustAdd(t, s, Task{ID: "L2", Subject: "legacy 2"})

	if got := s.DisplayNumber("L1"); got != "1" {
		t.Fatalf("expected legacy task L1 → \"1\", got %q", got)
	}
	if got := s.DisplayNumber("L2"); got != "2" {
		t.Fatalf("expected legacy task L2 → \"2\", got %q", got)
	}
}

// TestDisplayNumber_NestedHierarchy verifies x.y.z numbering walks the
// ancestor chain correctly.
func TestDisplayNumber_NestedHierarchy(t *testing.T) {
	s := newTestStore(t)

	// Two top-level tasks (1 and 2).
	mustAdd(t, s, Task{ID: "r1", Subject: "root1"})
	mustAdd(t, s, Task{ID: "r2", Subject: "root2"})
	// Children of r2: a, b (1.1 → "2.1", 1.2 → "2.2").
	mustAdd(t, s, Task{ID: "a", Subject: "a", ParentID: "r2", SiblingIndex: 1})
	mustAdd(t, s, Task{ID: "b", Subject: "b", ParentID: "r2", SiblingIndex: 2})
	// Grandchild under a: g (2.1.1).
	mustAdd(t, s, Task{ID: "g", Subject: "g", ParentID: "a", SiblingIndex: 1})

	cases := map[string]string{
		"r1": "1",
		"r2": "2",
		"a":  "2.1",
		"b":  "2.2",
		"g":  "2.1.1",
	}
	for id, want := range cases {
		if got := s.DisplayNumber(id); got != want {
			t.Errorf("DisplayNumber(%q) = %q, want %q", id, got, want)
		}
	}
}

// TestAddDecomposedSubtask_HappyPath claims a parent, adds two children,
// and verifies inheritance + dense sibling indices + DisplayNumber.
func TestAddDecomposedSubtask_HappyPath(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Task{
		ID:             "parent",
		Subject:        "do the thing",
		OriginPromptID: "conv-test:abcd",
		PlanID:         "plan-1",
	})

	c1, err := s.AddDecomposedSubtask("parent",
		Task{ID: "c1", Subject: "first step"},
		"background-worker:wrk-1")
	if err != nil {
		t.Fatalf("first child failed: %v", err)
	}
	if c1.OriginPromptID != "conv-test:abcd" {
		t.Errorf("expected inherited OriginPromptID, got %q", c1.OriginPromptID)
	}
	if c1.PlanID != "plan-1" {
		t.Errorf("expected inherited PlanID, got %q", c1.PlanID)
	}
	if c1.SiblingIndex != 1 {
		t.Errorf("expected SiblingIndex=1, got %d", c1.SiblingIndex)
	}
	if c1.DecomposedBy != "agent:parent" {
		t.Errorf("expected DecomposedBy=agent:parent, got %q", c1.DecomposedBy)
	}

	// Second child: claimParent="" because the parent was already
	// claimed on the first call.
	c2, err := s.AddDecomposedSubtask("parent",
		Task{ID: "c2", Subject: "second step"},
		"")
	if err != nil {
		t.Fatalf("second child failed: %v", err)
	}
	if c2.SiblingIndex != 2 {
		t.Errorf("expected dense SiblingIndex=2, got %d", c2.SiblingIndex)
	}

	if got := s.DisplayNumber("c1"); got != "1.1" {
		t.Errorf("DisplayNumber(c1) = %q, want 1.1", got)
	}
	if got := s.DisplayNumber("c2"); got != "1.2" {
		t.Errorf("DisplayNumber(c2) = %q, want 1.2", got)
	}
}

// TestAddDecomposedSubtask_IdempotencyCAS asserts that two callers
// trying to claim the same parent at once result in exactly one success
// and one ErrAlreadyDecomposed. This is the regression guard against
// duplicate decomposer runs racing.
func TestAddDecomposedSubtask_IdempotencyCAS(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Task{ID: "p", Subject: "parent"})

	const claimers = 8
	var wg sync.WaitGroup
	successes := make(chan struct{}, claimers)
	already := make(chan struct{}, claimers)

	for i := range claimers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.ClaimDecomposition("p", "actor-test")
			switch err {
			case nil:
				successes <- struct{}{}
			case ErrAlreadyDecomposed:
				already <- struct{}{}
			default:
				t.Errorf("unexpected error from claimer %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(successes)
	close(already)

	if len(successes) != 1 {
		t.Fatalf("expected exactly 1 successful claim, got %d", len(successes))
	}
	if len(already) != claimers-1 {
		t.Fatalf("expected %d ErrAlreadyDecomposed, got %d", claimers-1, len(already))
	}

	got, _ := s.GetTask("p")
	if got.DecompositionAttempts != 1 {
		t.Errorf("expected DecompositionAttempts=1, got %d", got.DecompositionAttempts)
	}
}

// TestAddDecomposedSubtask_RejectsCycle asserts that you cannot decompose
// a task into one of its own ancestors.
func TestAddDecomposedSubtask_RejectsCycle(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Task{ID: "A", Subject: "A"})
	mustAdd(t, s, Task{ID: "B", Subject: "B", ParentID: "A"})

	// Try to add A as a child of B — would create A → B → A.
	_, err := s.AddDecomposedSubtask("B", Task{ID: "A"}, "")
	if err != ErrCycleDetected {
		t.Fatalf("expected ErrCycleDetected, got %v", err)
	}
}

// TestRecordDecompositionFailure_ReleasesClaim asserts a failed run
// clears the DecomposedBy slot so the host can retry, and stamps
// LastDecompositionError. DecompositionAttempts is preserved (it counts
// attempts, not successes).
func TestRecordDecompositionFailure_ReleasesClaim(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Task{ID: "p", Subject: "parent"})

	if err := s.ClaimDecomposition("p", "wrk-1"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := s.RecordDecompositionFailure("p", "llm timeout"); err != nil {
		t.Fatalf("RecordDecompositionFailure: %v", err)
	}

	got, _ := s.GetTask("p")
	if got.DecomposedBy != "" {
		t.Errorf("expected DecomposedBy cleared, got %q", got.DecomposedBy)
	}
	if got.LastDecompositionError != "llm timeout" {
		t.Errorf("expected error stamped, got %q", got.LastDecompositionError)
	}
	if got.DecompositionAttempts != 1 {
		t.Errorf("expected attempts=1, got %d", got.DecompositionAttempts)
	}

	// Second claim should now succeed (slot was released).
	if err := s.ClaimDecomposition("p", "wrk-2"); err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	got, _ = s.GetTask("p")
	if got.DecompositionAttempts != 2 {
		t.Errorf("expected attempts=2 after retry, got %d", got.DecompositionAttempts)
	}
}

// TestAncestorChain_OrderedLeafToRoot asserts the chain ordering
// contract.
func TestAncestorChain_OrderedLeafToRoot(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Task{ID: "root", Subject: "r"})
	mustAdd(t, s, Task{ID: "mid", Subject: "m", ParentID: "root"})
	mustAdd(t, s, Task{ID: "leaf", Subject: "l", ParentID: "mid"})

	chain := s.AncestorChain("leaf")
	if len(chain) != 3 {
		t.Fatalf("expected 3-element chain, got %d", len(chain))
	}
	if chain[0].ID != "leaf" || chain[1].ID != "mid" || chain[2].ID != "root" {
		t.Fatalf("chain order wrong: got %s,%s,%s", chain[0].ID, chain[1].ID, chain[2].ID)
	}
}
