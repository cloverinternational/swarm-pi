package attach

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

func TestBuildFoldersSeparatesMachinesSharingWorkspace(t *testing.T) {
	targets := []Target{
		{ID: "local", Name: "local", Workspace: "/work/mono"},
		{ID: "remote", Name: "remote", Machine: "builder", Workspace: "/work/mono"},
	}
	folders := BuildFolders(targets, nil, SortLineage)
	if len(folders) != 2 {
		t.Fatalf("folder count = %d, want 2", len(folders))
	}
	if folders[0].Key == folders[1].Key {
		t.Fatalf("folder keys collided: %#v", folders)
	}
}

func TestTargetSummaryUsesLatestStructuredMessage(t *testing.T) {
	target := Target{
		ID:   "agent",
		Name: "worker",
		Snapshot: StateSnapshot{
			Messages: []state.MessageState{
				{Role: "user", Content: "inspect the repository"},
				{Role: "assistant", Content: "I found the attach screen"},
			},
		},
	}
	if got := TargetSummary(target); got != "I found the attach screen" {
		t.Fatalf("summary = %q, want latest assistant content", got)
	}
}

func TestFolderAggregateBadgeAndCounts(t *testing.T) {
	targets := []Target{
		{ID: "running", Status: "running", Snapshot: StateSnapshot{UpdatedAt: time.Now()}},
		{ID: "question", Status: "waiting", Snapshot: StateSnapshot{ModalID: "question"}},
		{ID: "done", Status: "done"},
	}
	folders := BuildFolders(targets, nil, SortLineage)
	if len(folders) != 1 {
		t.Fatalf("folder count = %d, want 1", len(folders))
	}
	folder := folders[0]
	if folder.Badge != StateBadgeQuestion {
		t.Fatalf("aggregate badge = %q, want question", folder.Badge)
	}
	if folder.Counts.Total != 3 || folder.Counts.Questions != 1 ||
		folder.Counts.Running != 1 || folder.Counts.Finished != 1 {
		t.Fatalf("counts = %#v, want total=3 question=1 running=1 finished=1", folder.Counts)
	}
}

func TestFlattenFoldersHonorsExpansion(t *testing.T) {
	targets := []Target{
		{ID: "a", Workspace: "/work/a"},
		{ID: "b", Workspace: "/work/b"},
	}
	folders := BuildFolders(targets, map[string]bool{FolderKey(targets[0]): true}, SortLineage)
	rows := FlattenFolders(folders)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (two folders plus one expanded session)", len(rows))
	}
}
