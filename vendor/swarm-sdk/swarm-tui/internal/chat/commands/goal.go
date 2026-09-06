package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// GoalCommand implements /goal — set a stop condition that keeps the agent
// looping until the condition is satisfied.
//
// Usage:
//
//	/goal <condition>  — set a new goal
//	/goal              — show the active goal
//	/goal clear        — clear the active goal
type GoalCommand struct{}

// NewGoalCommand creates a new goal command.
func NewGoalCommand() *GoalCommand { return &GoalCommand{} }

func (c *GoalCommand) Name() string { return "goal" }
func (c *GoalCommand) Description() string {
	return i18n.T("commands_b.goal.description")
}
func (c *GoalCommand) Aliases() []string { return []string{"objective"} }

// Execute parses args and returns a GoalCommandMsg for the app to handle.
func (c *GoalCommand) Execute(args []string) tea.Cmd {
	condition := strings.TrimSpace(strings.Join(args, " "))
	action := "set"
	if condition == "" {
		action = "status"
	} else if strings.ToLower(condition) == "clear" {
		action = "clear"
		condition = ""
	}
	return func() tea.Msg {
		return GoalCommandMsg{Action: action, Condition: condition}
	}
}

func (c *GoalCommand) Update(msg tea.Msg) (Command, tea.Cmd) { return c, nil }
func (c *GoalCommand) View() string                          { return "" }
func (c *GoalCommand) IsInteractive() bool                   { return false }

// Subcommands satisfies SubcommandProvider so the autocomplete can hint.
func (c *GoalCommand) Subcommands() []Subcommand {
	return []Subcommand{
		{Name: "clear", Description: i18n.T("commands_b.goal.clear_description")},
	}
}

// GoalCommandMsg is dispatched to the app when the user runs /goal.
type GoalCommandMsg struct {
	// Action is one of "set", "clear", "status".
	Action string
	// Condition is the goal description (only meaningful when Action == "set").
	Condition string
}

// GoalNotifyMsg carries the result of a goal action back to the user as a
// notification.
type GoalNotifyMsg struct {
	Level   string // "info" | "success" | "warning" | "error"
	Message string
}

// FormatGoalStatus builds a human-readable status string for the active goal.
// goalState and goalCondition come from GoalHook.GetGoal().
func FormatGoalStatus(state, condition, lastReason string) string {
	if state == "" || state == "cleared" {
		return i18n.T("commands_b.goal.no_goal")
	}
	out := i18n.T("commands_b.goal.active", condition, state)
	if lastReason != "" {
		out += i18n.T("commands_b.goal.last_check", lastReason)
	}
	return out
}
