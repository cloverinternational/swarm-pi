package chat

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func TestTaskPanelOrdersHierarchyAndFocus(t *testing.T) {
	tasks := []ii.TodoItem{
		{ID: "1", Content: "Parent", Status: ii.TodoStatusInProgress, Category: ii.TaskCategoryActing},
		{ID: "2", Content: "Child A", Status: ii.TodoStatusInProgress, Active: true, ParentID: "1", Category: ii.TaskCategoryActing},
		{ID: "3", Content: "Sibling", Status: ii.TodoStatusPending, Category: ii.TaskCategoryActing},
	}
	entries := orderTasksHierarchically(tasks)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].task.ID != "1" || entries[0].depth != 0 {
		t.Fatalf("parent should render first at depth 0: %+v", entries[0])
	}
	if entries[1].task.ID != "2" || entries[1].depth != 1 {
		t.Fatalf("child should render under parent at depth 1: %+v", entries[1])
	}
	if entries[2].task.ID != "3" {
		t.Fatalf("sibling root should render last: %+v", entries[2])
	}
}

func TestTaskPanelActiveVsInProgressGlyphs(t *testing.T) {
	m := NewTaskPanelModel()
	m.SetWidth(60)
	m.SetTasks([]ii.TodoItem{
		{ID: "1", Content: "Parent", Status: ii.TodoStatusInProgress, Category: ii.TaskCategoryActing},
		{ID: "2", Content: "Focused child", Status: ii.TodoStatusInProgress, Active: true, ParentID: "1", Category: ii.TaskCategoryActing},
	})
	out := m.View()
	if !strings.Contains(out, "●") {
		t.Fatal("active focus glyph missing")
	}
	if !strings.Contains(out, "◐") {
		t.Fatal("in-progress non-focused parent glyph missing")
	}
}
