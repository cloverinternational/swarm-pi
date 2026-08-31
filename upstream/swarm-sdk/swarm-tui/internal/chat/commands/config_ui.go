// Package commands: config_ui.go defines lightweight slash commands that open
// an existing configuration surface (an overlay switcher or a Settings section)
// without leaving the chat. They are deliberately NON-interactive: instead of
// becoming a.activeCommand, each emits an OpenConfigUIMsg that the chat package
// translates into the appropriate App-level action. This keeps the commands
// package free of the settings import (settings imports commands, so commands
// must not import settings).
package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// OpenConfigUIMsg is emitted by a config slash command to ask the App to open a
// configuration surface. Target is a neutral key the chat package maps to an
// overlay switcher (profile/agents/prompt) or a Settings section
// (compaction/providers). Arg is an optional direct-apply argument (e.g. a
// profile id/name) that enables a no-UI fast path where the App supports it.
type OpenConfigUIMsg struct {
	Target string
	Arg    string
}

// configUICommand is a small, reusable Command implementation that opens a
// config surface by emitting an OpenConfigUIMsg. It is never interactive; the
// overlay/section it triggers is an App-level modal, not a command view.
type configUICommand struct {
	name    string
	aliases []string
	desc    string
	target  string
}

// Name returns the slash command name (without the leading /).
func (c *configUICommand) Name() string { return c.name }

// Description returns the autocomplete description.
func (c *configUICommand) Description() string { return c.desc }

// Aliases returns alternative command names.
func (c *configUICommand) Aliases() []string { return c.aliases }

// IsInteractive is always false: this command emits a message and returns; it
// does not host its own view.
func (c *configUICommand) IsInteractive() bool { return false }

// View renders nothing (non-interactive command).
func (c *configUICommand) View() string { return "" }

// Update is a no-op (non-interactive command).
func (c *configUICommand) Update(_ tea.Msg) (Command, tea.Cmd) { return c, nil }

// Execute emits an OpenConfigUIMsg carrying this command's target and any
// trailing args joined as a direct-apply argument.
func (c *configUICommand) Execute(args []string) tea.Cmd {
	arg := strings.TrimSpace(strings.Join(args, " "))
	target := c.target
	return func() tea.Msg { return OpenConfigUIMsg{Target: target, Arg: arg} }
}

// NewProfileSwitchCommand creates /profile — opens the model-profile switcher
// overlay, or with an argument switches directly to that profile.
func NewProfileSwitchCommand() Command {
	return &configUICommand{
		name:   "profile",
		desc:   i18n.T("commands.config_ui.profile.description"),
		target: "profile",
	}
}

// NewAgentsCommand creates /agents (alias /subagents) — opens the sub-agent
// switcher overlay, or with an argument sets the default agent directly.
func NewAgentsCommand() Command {
	return &configUICommand{
		name:    "agents",
		aliases: []string{"subagents"},
		desc:    i18n.T("commands.config_ui.agents.description"),
		target:  "agents",
	}
}

// NewPromptCommand creates /prompt (alias /systemprompt) — opens the system
// prompt switcher overlay, or with an argument activates that prompt directly.
func NewPromptCommand() Command {
	return &configUICommand{
		name:    "prompt",
		aliases: []string{"systemprompt"},
		desc:    i18n.T("commands.config_ui.prompt.description"),
		target:  "prompt",
	}
}

// NewCompactionCommand creates /compaction — opens the Settings → Compaction
// section (there is no quick overlay for compaction config).
func NewCompactionCommand() Command {
	return &configUICommand{
		name:   "compaction",
		desc:   i18n.T("commands.config_ui.compaction.description"),
		target: "compaction",
	}
}

// NewProvidersCommand creates /providers (alias /provider) — opens the
// Settings → Models section (providers, profiles, and model config).
func NewProvidersCommand() Command {
	return &configUICommand{
		name:    "providers",
		aliases: []string{"provider"},
		desc:    i18n.T("commands.config_ui.providers.description"),
		target:  "providers",
	}
}
