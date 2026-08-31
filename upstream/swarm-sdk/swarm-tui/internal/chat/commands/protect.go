package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ProtectCommand implements /protect — configure the protected-branch
// guardrail for the current workspace. When protection is on and the repo is
// checked out on a protected branch (default main/master), the agent is
// hard-blocked from mutating files or running mutating git/shell commands and
// is steered to use a git worktree instead.
//
// Usage:
//
//	/protect              — show current protection status
//	/protect on           — enable protection (default branches main, master)
//	/protect off          — disable protection
//	/protect on <b1> <b2> — enable protection for specific branches
//	/protect branches ... — set the protected branch list (keeps enabled state)
type ProtectCommand struct{}

// NewProtectCommand creates a new protect command.
func NewProtectCommand() *ProtectCommand { return &ProtectCommand{} }

func (c *ProtectCommand) Name() string { return "protect" }
func (c *ProtectCommand) Description() string {
	return i18n.T("commands.protect.description")
}
func (c *ProtectCommand) Aliases() []string { return []string{"guard"} }

// Execute parses args and returns a ProtectCommandMsg for the app to handle.
func (c *ProtectCommand) Execute(args []string) tea.Cmd {
	action := "status"
	var branches []string

	if len(args) > 0 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "on", "enable", "protect":
			action = "on"
			branches = normalizeBranches(args[1:])
		case "off", "disable", "unprotect":
			action = "off"
		case "branches", "set":
			action = "branches"
			branches = normalizeBranches(args[1:])
		case "status", "show":
			action = "status"
		default:
			// Bare "/protect main develop" == enable for those branches.
			action = "on"
			branches = normalizeBranches(args)
		}
	}

	a := action
	b := branches
	return func() tea.Msg {
		return ProtectCommandMsg{Action: a, Branches: b}
	}
}

func (c *ProtectCommand) Update(_ tea.Msg) (Command, tea.Cmd) { return c, nil }
func (c *ProtectCommand) View() string                        { return "" }
func (c *ProtectCommand) IsInteractive() bool                 { return false }

// Subcommands satisfies SubcommandProvider so autocomplete can hint.
func (c *ProtectCommand) Subcommands() []Subcommand {
	return []Subcommand{
		{Name: "on", Description: i18n.T("commands.protect.subcommand.on")},
		{Name: "off", Description: i18n.T("commands.protect.subcommand.off")},
		{Name: "status", Description: i18n.T("commands.protect.subcommand.status")},
		{Name: "branches", Description: i18n.T("commands.protect.subcommand.branches")},
	}
}

// normalizeBranches trims and filters branch name arguments.
func normalizeBranches(args []string) []string {
	var out []string
	for _, a := range args {
		if b := strings.TrimSpace(a); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// ProtectCommandMsg is dispatched to the app when the user runs /protect.
type ProtectCommandMsg struct {
	// Action is one of "on", "off", "status", "branches".
	Action string
	// Branches is the optional protected branch list (for "on"/"branches").
	Branches []string
}
