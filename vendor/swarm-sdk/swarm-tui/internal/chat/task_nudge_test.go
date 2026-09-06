package chat

import (
	"strings"
	"testing"
	"time"

	"context"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// helpers for building test message slices.

func userMsg(content string) *conversation.Message {
	return &conversation.Message{
		Role:      conversation.RoleUser,
		Content:   content,
		Timestamp: time.Now(),
	}
}

func toolResultMsg() *conversation.Message {
	return &conversation.Message{
		Role: conversation.RoleUser,
		ToolResults: []conversation.ToolResult{
			{CallID: "call-1", Name: "bash", Output: "ok"},
		},
		Timestamp: time.Now(),
	}
}

func assistantMsg(content string) *conversation.Message {
	return &conversation.Message{
		Role:      conversation.RoleAssistant,
		Content:   content,
		Timestamp: time.Now(),
	}
}

// TestTaskNudgeActive_SilentOnFirstUserTurn verifies eager turn-one injection
// is disabled.
func TestTaskNudgeActive_SilentOnFirstUserTurn(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	defer mgr.ClearTodos()

	msgs := []*conversation.Message{
		userMsg("Add authentication to the app"),
	}
	if TaskNudgeActive(msgs) {
		t.Error("expected nudge inactive on first user turn")
	}
}

// TestTaskNudgeActive_SilentOnToolCallTurns verifies the nudge does NOT fire on
// intermediate tool-result turns (agent is mid-execution).
func TestTaskNudgeActive_SilentOnToolCallTurns(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	defer mgr.ClearTodos()

	msgs := []*conversation.Message{
		userMsg("Fix the login bug"),
		assistantMsg("I'll check the code."),
		toolResultMsg(),
	}
	if TaskNudgeActive(msgs) {
		t.Error("expected nudge inactive on tool-call turn")
	}
}

// TestTaskNudgeActive_SilentOnAssistantTurn verifies no nudge when last message
// is from the assistant.
func TestTaskNudgeActive_SilentOnAssistantTurn(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	defer mgr.ClearTodos()

	msgs := []*conversation.Message{
		userMsg("Tell me a joke"),
		assistantMsg("Why did the chicken cross the road?"),
	}
	if TaskNudgeActive(msgs) {
		t.Error("expected nudge inactive when last message is assistant")
	}
}

// TestTaskNudgeActive_SilentWhenTasksExist verifies the nudge is suppressed
// when pending tasks exist.
func TestTaskNudgeActive_SilentWhenTasksExist(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	if err := mgr.SetTodos([]ii.TodoItem{
		{ID: "t1", Content: "Implement JWT auth", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium},
	}); err != nil {
		t.Fatalf("SetTodos: %v", err)
	}
	defer mgr.ClearTodos()

	if TaskNudgeActive([]*conversation.Message{userMsg("what's next?")}) {
		t.Error("expected nudge inactive when pending tasks exist")
	}
}

// TestTaskNudgeActive_SilentWhenInProgress verifies the nudge is suppressed
// when an in-progress task exists.
func TestTaskNudgeActive_SilentWhenInProgress(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	if err := mgr.SetTodos([]ii.TodoItem{
		{ID: "t1", Content: "Refactor auth module", Status: ii.TodoStatusInProgress, Priority: ii.TodoPriorityMedium},
	}); err != nil {
		t.Fatalf("SetTodos: %v", err)
	}
	defer mgr.ClearTodos()

	if TaskNudgeActive([]*conversation.Message{userMsg("keep going")}) {
		t.Error("expected nudge inactive when in-progress task exists")
	}
}

// TestTaskNudgeActive_SilentAfterCompletionWithoutActivity verifies a new
// prompt alone is insufficient; multi-step tool activity is required.
func TestTaskNudgeActive_SilentAfterCompletionWithoutActivity(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	if err := mgr.SetTodos([]ii.TodoItem{
		{ID: "t1", Content: "Add tests", Status: ii.TodoStatusCompleted, Priority: ii.TodoPriorityMedium},
	}); err != nil {
		t.Fatalf("SetTodos: %v", err)
	}
	defer mgr.ClearTodos()

	if TaskNudgeActive([]*conversation.Message{userMsg("now add CI/CD config")}) {
		t.Error("expected nudge inactive without multi-step tool activity")
	}
}

// TestTaskNudgeActive_SilentOnEmptyHistory verifies no injection for empty msgs.
func TestTaskNudgeActive_SilentOnEmptyHistory(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	defer mgr.ClearTodos()

	if TaskNudgeActive([]*conversation.Message{}) {
		t.Error("expected nudge inactive for empty message list")
	}
}

// TestBuiltinCheckAndNudge_FiresWhenNoTasks verifies the builtin hook fires
// when no tasks are active — this is what HooksManager calls on the periodic path.
func TestBuiltinCheckAndNudge_FiresWhenNoTasks(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	defer mgr.ClearTodos()

	msg, shouldNudge := builtin.CheckAndNudge(context.Background())
	if !shouldNudge {
		t.Error("expected CheckAndNudge to fire when task list is empty")
	}
	if msg == "" {
		t.Error("expected non-empty nudge message")
	}
}

// TestBuiltinCheckAndNudge_SilentWhenTasksExist verifies the builtin hook does
// not fire when there are active tasks.
func TestBuiltinCheckAndNudge_SilentWhenTasksExist(t *testing.T) {
	mgr := ii.GetTodoManager()
	mgr.ClearTodos()
	if err := mgr.SetTodos([]ii.TodoItem{
		{ID: "t1", Content: "Work item", Status: ii.TodoStatusInProgress, Priority: ii.TodoPriorityMedium},
	}); err != nil {
		t.Fatalf("SetTodos: %v", err)
	}
	defer mgr.ClearTodos()

	_, shouldNudge := builtin.CheckAndNudge(context.Background())
	if shouldNudge {
		t.Error("expected CheckAndNudge to be silent when tasks are active")
	}
}

// TestTaskNudgeReminder_NoUserLeakage verifies the reminder text does not
// instruct the agent to disclose itself to the user.
func TestTaskNudgeReminder_NoUserLeakage(t *testing.T) {
	text := taskNudgeReminderText()

	forbidden := []string{
		"tell the user",
		"inform the user",
		"mention this to",
		"disclose",
		"let the user know",
	}
	lower := strings.ToLower(text)
	for _, phrase := range forbidden {
		if strings.Contains(lower, phrase) {
			t.Errorf("reminder contains phrase that could leak to user: %q", phrase)
		}
	}
	if strings.Contains(text, "\n") {
		t.Error("reminder must remain a single terse line")
	}
	if !strings.Contains(text, "Multi-step work detected") {
		t.Error("reminder must describe the activity threshold")
	}
}
