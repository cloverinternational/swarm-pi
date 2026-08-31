package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/charmbracelet/x/ansi"
)

func TestAgentTaskModalSelectionAndDetails(t *testing.T) {
	modal := NewAgentTaskModal()
	modal.maxVis = 2
	modal.visible = []ii.TodoItem{
		{ID: "1", Content: "First"},
		{ID: "2", Content: "Second"},
		{ID: "3", Content: "Third"},
	}

	modal.Update("down")
	modal.Update("down")
	if modal.cursor != 2 || modal.scroll != 1 {
		t.Fatalf("selection = cursor %d scroll %d, want cursor 2 scroll 1", modal.cursor, modal.scroll)
	}

	modal.Update("enter")
	if !modal.expanded {
		t.Fatal("Enter should expand the selected task")
	}

	modal.Update("tab")
	if modal.section != 1 || modal.cursor != 0 || modal.scroll != 0 || modal.expanded {
		t.Fatalf("Tab should switch sections and reset navigation: %#v", modal)
	}
}

func TestRenderTaskMenuLineUsesTaskStateOnly(t *testing.T) {
	task := ii.TodoItem{
		ID:         "7",
		Content:    "Build compact task UI",
		ActiveForm: "Building compact task UI",
		Status:     ii.TodoStatusInProgress,
		Active:     true,
	}

	got := ansi.Strip(renderTaskMenuLine(DefaultTheme, task, 80, true))
	for _, want := range []string{"›", "●", "#7", "Building compact task UI"} {
		if !strings.Contains(got, want) {
			t.Errorf("task line %q does not contain %q", got, want)
		}
	}
	for _, unwanted := range []string{"TaskManage", "update", "audit", "tool"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("task line %q contains internal activity %q", got, unwanted)
		}
	}
}

func TestRenderTaskDetailsShowsUsefulFields(t *testing.T) {
	task := ii.TodoItem{
		ID:          "9",
		Description: "Keep task state readable without exposing orchestration logs.",
		Status:      ii.TodoStatusPending,
		Category:    ii.TaskCategoryPlanning,
		DependsOn:   []string{"4", "5"},
		Blocks:      []string{"12"},
		Notes:       []string{"Older note", "Latest useful note"},
	}

	got := ansi.Strip(strings.Join(renderTaskDetails(DefaultTheme, task, 64), "\n"))
	for _, want := range []string{
		"Status: pending",
		"Category: planning",
		"Keep task state readable",
		"Blocked by: #4, #5",
		"Blocks: #12",
		"Latest useful note",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("details %q do not contain %q", got, want)
		}
	}
	if strings.Contains(got, "Older note") {
		t.Errorf("details should only show the latest note: %q", got)
	}
}

func TestAgentTaskModalPreservesSelectedTaskAcrossReorder(t *testing.T) {
	manager := ii.GetTodoManager()
	original := manager.Todos()
	t.Cleanup(func() {
		if err := manager.SetTodos(original); err != nil {
			t.Fatalf("restore tasks: %v", err)
		}
	})
	tasks := []ii.TodoItem{
		{ID: "1", Content: "First", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium, Sequence: 1},
		{ID: "2", Content: "Second", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium, Sequence: 2},
	}
	if err := manager.SetTodos(tasks); err != nil {
		t.Fatalf("set tasks: %v", err)
	}

	modal := NewAgentTaskModal()
	modal.Render(80, 24, DefaultTheme, nil)
	modal.Update("down")
	if modal.selectedID != "2" {
		t.Fatalf("selected task = %q, want 2", modal.selectedID)
	}

	tasks[1].Status = ii.TodoStatusInProgress
	tasks[1].Active = true
	if err := manager.SetTodos(tasks); err != nil {
		t.Fatalf("reorder tasks: %v", err)
	}
	modal.Render(80, 24, DefaultTheme, nil)
	if modal.selectedID != "2" || modal.visible[modal.cursor].ID != "2" {
		t.Fatalf("selection moved after reorder: selected=%q cursor task=%q", modal.selectedID, modal.visible[modal.cursor].ID)
	}
}

func TestAgentTaskModalFitsConstrainedViewport(t *testing.T) {
	manager := ii.GetTodoManager()
	original := manager.Todos()
	t.Cleanup(func() {
		if err := manager.SetTodos(original); err != nil {
			t.Fatalf("restore tasks: %v", err)
		}
	})
	task := ii.TodoItem{
		ID:          "1",
		Content:     "Compact task",
		Description: strings.Repeat("Long task details need to remain inside the viewport. ", 8),
		Status:      ii.TodoStatusInProgress,
		Priority:    ii.TodoPriorityMedium,
		Notes:       []string{strings.Repeat("Latest note. ", 12)},
	}
	if err := manager.SetTodos([]ii.TodoItem{task}); err != nil {
		t.Fatalf("set tasks: %v", err)
	}

	modal := NewAgentTaskModal()
	modal.expanded = true
	const width, height = 24, 14
	rendered := modal.Render(width, height, DefaultTheme, nil)
	if got := lipgloss.Width(rendered); got > width {
		t.Fatalf("modal width = %d, want <= %d", got, width)
	}
	if got := lipgloss.Height(rendered); got > height {
		t.Fatalf("modal height = %d, want <= %d:\n%s", got, height, ansi.Strip(rendered))
	}
}

func TestAgentTaskModalRemembersSelectionPerSection(t *testing.T) {
	manager := ii.GetTodoManager()
	original := manager.Todos()
	t.Cleanup(func() {
		if err := manager.SetTodos(original); err != nil {
			t.Fatalf("restore tasks: %v", err)
		}
	})
	tasks := []ii.TodoItem{
		{ID: "1", Content: "Open one", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium, Sequence: 1},
		{ID: "2", Content: "Open two", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium, Sequence: 2},
		{ID: "3", Content: "Done one", Status: ii.TodoStatusCompleted, Priority: ii.TodoPriorityMedium, Sequence: 3},
		{ID: "4", Content: "Done two", Status: ii.TodoStatusCompleted, Priority: ii.TodoPriorityMedium, Sequence: 4},
	}
	if err := manager.SetTodos(tasks); err != nil {
		t.Fatalf("set tasks: %v", err)
	}

	modal := NewAgentTaskModal()
	modal.Render(80, 24, DefaultTheme, nil)
	modal.Update("down") // #2 in OPEN
	modal.Update("tab")
	modal.Render(80, 24, DefaultTheme, nil)
	modal.Update("down") // #4 in DONE
	modal.Update("tab")
	modal.Render(80, 24, DefaultTheme, nil)
	if modal.selectedID != "2" {
		t.Fatalf("open selection = %q after tab round-trip, want 2", modal.selectedID)
	}

	modal.Update("tab")
	modal.Render(80, 24, DefaultTheme, nil)
	if modal.selectedID != "4" {
		t.Fatalf("completed selection = %q after tab round-trip, want 4", modal.selectedID)
	}
}

func TestAgentTaskModalFitsTinyViewports(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{
		{width: 15, height: 8},
		{width: 8, height: 4},
		{width: 2, height: 2},
		{width: 1, height: 1},
	} {
		rendered := NewAgentTaskModal().Render(size.width, size.height, DefaultTheme, nil)
		if got := lipgloss.Width(rendered); got > size.width {
			t.Errorf("%dx%d modal width = %d", size.width, size.height, got)
		}
		if got := lipgloss.Height(rendered); got > size.height {
			t.Errorf("%dx%d modal height = %d", size.width, size.height, got)
		}
	}
}
