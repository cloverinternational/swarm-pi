package commands

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// LoopCommand implements /loop — schedule a recurring task using the loop skill.
//
// Usage:
//
//	/loop <interval> [task]   — start a recurring loop every <interval>
//	/loop stop                — stop the active loop (clears cron jobs)
//	/loop status              — show any active loop
//	/loop                     — show usage
//
// Examples:
//
//	/loop 5m check for new emails
//	/loop 1h summarize recent activity
//	/loop stop
type LoopCommand struct{}

// NewLoopCommand creates a new loop command.
func NewLoopCommand() *LoopCommand { return &LoopCommand{} }

func (c *LoopCommand) Name() string { return "loop" }
func (c *LoopCommand) Description() string {
	return i18n.T("commands_b.loop.description")
}
func (c *LoopCommand) Aliases() []string { return []string{"repeat", "schedule"} }

// Execute parses the interval/task deterministically in the TUI and injects a
// crisp tool-call instruction as a chat prompt.
//
// It deliberately does NOT inject the raw loop-skill instructions: those tell
// the reader to "use ParseLoopInput / IntervalToCron", which are Go functions
// — weaker models sitting in a code repo read that as an implementation task
// and start grepping the codebase instead of calling CronCreate. The TUI does
// that parsing itself; the model only receives literal CronCreate arguments.
func (c *LoopCommand) Execute(args []string) tea.Cmd {
	input := strings.TrimSpace(strings.Join(args, " "))

	switch strings.ToLower(input) {
	case "stop":
		prompt := "Stop the active loop. This is a scheduling instruction — do not write, search for, or modify any code. " +
			"Call the CronList tool, then call CronDelete for every recurring loop job it returns, and confirm to the user what was stopped."
		return func() tea.Msg { return LoopInjectPromptMsg{Prompt: prompt} }
	case "status":
		prompt := "Report loop status. This is a scheduling instruction — do not write, search for, or modify any code. " +
			"Call the CronList tool and summarize any active recurring jobs (id, schedule, prompt). If there are none, say so."
		return func() tea.Msg { return LoopInjectPromptMsg{Prompt: prompt} }
	}

	interval, task := skills.ParseLoopInput(input)
	if task == "" {
		return func() tea.Msg { return LoopUsageMsg{} }
	}
	cronExpr, err := skills.IntervalToCron(interval)
	if err != nil {
		return func() tea.Msg { return LoopErrorMsg{Error: i18n.T("commands_b.loop.invalid_interval", interval, err)} }
	}

	prompt := fmt.Sprintf(`A recurring loop was requested via /loop. This is a SCHEDULING instruction — do not write, search for, or modify any code.

Do the following now:
1. Call the CronCreate tool with exactly these arguments:
   - prompt: %q
   - cron: %q
   - recurring: true
2. Perform the task once immediately: %s
3. Confirm to the user with the cron job ID and the schedule (every %s; recurring jobs auto-expire after 7 days).`,
		task, cronExpr, task, interval)
	return func() tea.Msg { return LoopInjectPromptMsg{Prompt: prompt} }
}

func (c *LoopCommand) Update(msg tea.Msg) (Command, tea.Cmd) { return c, nil }
func (c *LoopCommand) View() string                          { return "" }
func (c *LoopCommand) IsInteractive() bool                   { return false }

// Subcommands satisfies SubcommandProvider.
func (c *LoopCommand) Subcommands() []Subcommand {
	return []Subcommand{
		{Name: "stop", Description: i18n.T("commands_b.loop.stop_description")},
		{Name: "status", Description: i18n.T("commands_b.loop.status_description")},
	}
}

// LoopInjectPromptMsg asks the app to inject a prompt into the chat input and
// send it — making the command visible in the conversation and triggering an
// agent response. The app sets textInput to Prompt and calls handleSendMessage.
type LoopInjectPromptMsg struct {
	Prompt string
}

// LoopUsageMsg asks the app to show /loop usage (no task was given).
type LoopUsageMsg struct{}

// LoopErrorMsg reports a /loop parse error (e.g. unsupported interval).
type LoopErrorMsg struct {
	Error string
}
