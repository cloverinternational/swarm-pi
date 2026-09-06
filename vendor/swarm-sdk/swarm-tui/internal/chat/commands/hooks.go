package commands

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// HooksCommand implements the /hooks command with full dashboard UI
type HooksCommand struct {
	interactive  bool
	width        int
	height       int
	hooksManager HooksManagerProvider // Interface to get hooks manager
}

// HooksManagerProvider provides access to the hooks manager
type HooksManagerProvider interface {
	GetManager() *hooks.Manager
	GetStats() hooks.RegistryStats
	IsEnabled(name string) bool
	EnableHook(name string) error
	DisableHook(name string) error
}

// NewHooksCommand creates a new hooks command
func NewHooksCommand() *HooksCommand {
	return &HooksCommand{
		interactive: false,
		width:       80,
		height:      24,
	}
}

// Name returns the command name (without leading /)
func (c *HooksCommand) Name() string {
	return "hooks"
}

// Description returns autocomplete description
func (c *HooksCommand) Description() string {
	return i18n.T("commands_b.hooks.description")
}

// Aliases returns alternative names
func (c *HooksCommand) Aliases() []string {
	return []string{"hook", "events"}
}

// Execute runs when command is invoked
func (c *HooksCommand) Execute(args []string) tea.Cmd {
	c.interactive = true
	return nil
}

// IsInteractive returns true if command shows UI
func (c *HooksCommand) IsInteractive() bool {
	return c.interactive
}

// SetHooksManager sets the hooks manager provider
func (c *HooksCommand) SetHooksManager(provider HooksManagerProvider) {
	c.hooksManager = provider
}

// Update handles messages (for interactive commands)
func (c *HooksCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	if !c.interactive {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			c.interactive = false
			return c, nil
		}

	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
	}

	return c, nil
}

// View renders the command UI (for interactive commands)
func (c *HooksCommand) View() string {
	if !c.interactive {
		return ""
	}

	// For now, show a placeholder - we'll build the full dashboard next
	style := lipgloss.NewStyle().
		Width(c.width).
		Height(c.height).
		Padding(2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorCyan))

	content := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(ColorCyan)).
		Render(i18n.T("commands_b.hooks.title"))

	content += "\n\n"
	content += lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands_b.hooks.coming_soon"))

	content += "\n\n"
	content += renderPlaceholderStats(c)

	content += "\n\n"
	content += lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorYellow)).
		Render(i18n.T("commands_b.hooks.close_hint"))

	return style.Render(content)
}

func renderPlaceholderStats(c *HooksCommand) string {
	if c.hooksManager == nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorRed)).
			Render(i18n.T("commands_b.hooks.unavailable"))
	}

	stats := c.hooksManager.GetStats()

	return i18n.T("commands_b.hooks.stats",
		stats.TotalHooks,
		stats.EnabledHooks,
		stats.TotalExecutions,
		stats.TotalBlocked,
		stats.TotalModified,
		stats.TotalErrors,
	)
}
