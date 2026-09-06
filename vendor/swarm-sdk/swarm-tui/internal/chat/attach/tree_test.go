package attach

import (
	"testing"
	"time"
)

func TestBuildTreePreservesParentHierarchyAndSorts(t *testing.T) {
	roots := BuildTree([]Target{
		{ID: "child-b", ParentID: "root", Name: "B", Kind: TargetKindSubAgent},
		{ID: "root", Name: "Root", Kind: TargetKindSubAgent},
		{ID: "child-a", ParentID: "root", Name: "A", Kind: TargetKindSubAgent},
	})
	if len(roots) != 1 || roots[0].Target.ID != "root" {
		t.Fatalf("roots = %#v, want root", roots)
	}
	if len(roots[0].Children) != 2 || roots[0].Children[0].Target.ID != "child-a" {
		t.Fatalf("children = %#v, want sorted children", roots[0].Children)
	}
}

func TestBuildTreeKeepsMalformedNodesVisible(t *testing.T) {
	roots := BuildTree([]Target{
		{ID: "a", ParentID: "b", Name: "A"},
		{ID: "b", ParentID: "a", Name: "B"},
		{ID: "orphan", ParentID: "missing", Name: "Orphan"},
		{Name: "anonymous"},
	})
	if len(roots) != 4 {
		t.Fatalf("roots = %d, want every malformed target visible", len(roots))
	}
}

func TestStateSnapshotBadgePriority(t *testing.T) {
	tests := []struct {
		name   string
		state  StateSnapshot
		status string
		want   StateBadge
	}{
		{"question", StateSnapshot{ModalID: "question"}, "running", StateBadgeQuestion},
		{"approval", StateSnapshot{ModalID: "approval"}, "running", StateBadgeApproval},
		{"plan", StateSnapshot{ModalID: "plan-approval"}, "running", StateBadgePlan},
		{"finished", StateSnapshot{}, "done", StateBadgeDone},
		{"stale", StateSnapshot{Stale: true}, "idle", StateBadgeStale},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.state.Badge(test.status); got != test.want {
				t.Fatalf("Badge() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSortTreeExplicitModes(t *testing.T) {
	now := time.Now()
	roots := BuildTree([]Target{
		{ID: "done", Name: "done", Status: "done"},
		{ID: "running", Name: "running", Status: "running", Snapshot: StateSnapshot{UpdatedAt: now}},
		{ID: "question", Name: "question", Status: "waiting", Snapshot: StateSnapshot{ModalID: "question"}},
	})

	SortTree(roots, SortAttention)
	if roots[0].Target.ID != "question" {
		t.Fatalf("attention order starts with %q, want question", roots[0].Target.ID)
	}

	SortTree(roots, SortStatus)
	if roots[0].Target.ID != "running" {
		t.Fatalf("status order starts with %q, want running", roots[0].Target.ID)
	}

	SortTree(roots, SortLineage)
	if roots[0].Target.ID != "done" {
		t.Fatalf("lineage order starts with %q, want alphabetical done", roots[0].Target.ID)
	}
}
