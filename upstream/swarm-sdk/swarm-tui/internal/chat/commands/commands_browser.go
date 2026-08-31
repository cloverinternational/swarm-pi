package commands

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// CommandsBrowser implements the /commands command to browse all available commands
type CommandsBrowser struct {
	interactive  bool
	state        string // "list", "info"
	selectedItem int
	scrollOffset int
	maxVisible   int
	width        int
	height       int

	// Command sources
	registry      *Registry
	pluginsLoader *plugins.Loader

	// Command lists
	builtinCommands []CommandInfo
	pluginCommands  []PluginCommandInfo
	allCommands     []any // Combined list for display

	// Current selection
	currentCommand any

	// Results
}

// CommandInfo represents a built-in command
type CommandInfo struct {
	Name        string
	Description string
	Aliases     []string
	Source      string // "built-in"
}

// PluginCommandInfo represents a plugin command
type PluginCommandInfo struct {
	Name        string
	Description string
	Plugin      string
	Content     string
	Source      string // "plugin"
}

// NewCommandsBrowser creates a new /commands command
func NewCommandsBrowser() *CommandsBrowser {
	return &CommandsBrowser{
		interactive:  false,
		state:        "list",
		selectedItem: 0,
		scrollOffset: 0,
		maxVisible:   15,
		width:        80,
		height:       24,
	}
}

func (c *CommandsBrowser) Name() string {
	return "commands"
}

func (c *CommandsBrowser) Description() string {
	return i18n.T("commands.browser.description")
}

func (c *CommandsBrowser) Aliases() []string {
	return []string{"cmds", "command"}
}

// SetRegistry sets the command registry
func (c *CommandsBrowser) SetRegistry(registry *Registry) {
	c.registry = registry
}

// SetPluginsLoader sets the plugins loader
func (c *CommandsBrowser) SetPluginsLoader(loader *plugins.Loader) {
	c.pluginsLoader = loader
}

func (c *CommandsBrowser) Execute(args []string) tea.Cmd {
	// Handle subcommands
	if len(args) > 0 {
		subCmd := args[0]

		switch subCmd {
		case "list", "ls":
			return c.cmdList()
		case "info":
			if len(args) < 2 {
				return c.errorMsg(i18n.T("commands.browser.usage.info"))
			}
			return c.cmdInfo(args[1])
		case "plugin":
			// List only plugin commands
			return c.cmdListPlugins()
		case "builtin":
			// List only built-in commands
			return c.cmdListBuiltin()
		}
	}

	// Show interactive UI
	c.interactive = true
	c.state = "list"
	c.selectedItem = 0
	c.refreshCommandList()
	return nil
}

func (c *CommandsBrowser) IsInteractive() bool {
	return c.interactive
}

func (c *CommandsBrowser) refreshCommandList() {
	c.builtinCommands = []CommandInfo{}
	c.pluginCommands = []PluginCommandInfo{}
	c.allCommands = []any{}

	// Get built-in commands
	if c.registry != nil {
		for _, cmd := range c.registry.All() {
			info := CommandInfo{
				Name:        cmd.Name(),
				Description: cmd.Description(),
				Aliases:     cmd.Aliases(),
				Source:      "built-in",
			}
			c.builtinCommands = append(c.builtinCommands, info)
			c.allCommands = append(c.allCommands, info)
		}
	}

	// Get plugin commands
	if c.pluginsLoader != nil {
		enabledPlugins := c.pluginsLoader.GetEnabled()
		for _, plugin := range enabledPlugins {
			for _, cmd := range plugin.Commands {
				info := PluginCommandInfo{
					Name:        cmd.Name,
					Description: cmd.Description,
					Plugin:      plugin.Manifest.Name,
					Content:     cmd.Content,
					Source:      "plugin",
				}
				c.pluginCommands = append(c.pluginCommands, info)
				c.allCommands = append(c.allCommands, info)
			}
		}
	}

	// Sort all commands by name
	sort.Slice(c.allCommands, func(i, j int) bool {
		nameI := c.getCommandName(c.allCommands[i])
		nameJ := c.getCommandName(c.allCommands[j])
		return nameI < nameJ
	})
}

func (c *CommandsBrowser) getCommandName(cmd any) string {
	switch v := cmd.(type) {
	case CommandInfo:
		return v.Name
	case PluginCommandInfo:
		return v.Name
	default:
		return ""
	}
}

func (c *CommandsBrowser) cmdList() tea.Cmd {
	return func() tea.Msg {
		c.refreshCommandList()

		var lines []string
		lines = append(lines, i18n.T("commands.browser.report.available",
			len(c.builtinCommands), len(c.pluginCommands)))

		// List built-in commands
		if len(c.builtinCommands) > 0 {
			lines = append(lines, i18n.T("commands.browser.builtin.title"))
			for _, cmd := range c.builtinCommands {
				aliases := ""
				if len(cmd.Aliases) > 0 {
					aliases = i18n.T("commands.browser.aliases_inline", strings.Join(cmd.Aliases, ", "))
				}
				desc := cmd.Description
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}
				lines = append(lines, i18n.T("commands.browser.report.command", cmd.Name, aliases, desc))
			}
			lines = append(lines, "")
		}

		// List plugin commands
		if len(c.pluginCommands) > 0 {
			lines = append(lines, i18n.T("commands.browser.plugin.title"))
			for _, cmd := range c.pluginCommands {
				desc := cmd.Description
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}
				lines = append(lines, i18n.T("commands.browser.report.plugin_command", cmd.Name, cmd.Plugin, desc))
			}
		} else {
			lines = append(lines, i18n.T("commands.browser.plugin.none"))
			lines = append(lines, i18n.T("commands.browser.plugin.install"))
		}

		lines = append(lines, "")
		lines = append(lines, i18n.T("commands.browser.report.info_hint"))

		return CommandsBrowserMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *CommandsBrowser) cmdInfo(name string) tea.Cmd {
	return func() tea.Msg {
		c.refreshCommandList()

		// Search in built-in commands
		for _, cmd := range c.builtinCommands {
			if cmd.Name == name || contains(cmd.Aliases, name) {
				var lines []string
				lines = append(lines, i18n.T("commands.browser.field.command", cmd.Name))
				lines = append(lines, i18n.T("commands.browser.field.type_builtin"))
				lines = append(lines, i18n.T("commands.browser.field.description", cmd.Description))
				if len(cmd.Aliases) > 0 {
					lines = append(lines, i18n.T("commands.browser.field.aliases", strings.Join(cmd.Aliases, ", ")))
				}
				return CommandsBrowserMsg{Result: strings.Join(lines, "\n")}
			}
		}

		// Search in plugin commands
		for _, cmd := range c.pluginCommands {
			if cmd.Name == name {
				var lines []string
				lines = append(lines, i18n.T("commands.browser.field.command", cmd.Name))
				lines = append(lines, i18n.T("commands.browser.field.type_plugin"))
				lines = append(lines, i18n.T("commands.browser.field.plugin", cmd.Plugin))
				lines = append(lines, i18n.T("commands.browser.field.description", cmd.Description))
				lines = append(lines, "")
				lines = append(lines, i18n.T("commands.browser.content_preview"))
				lines = append(lines, "---")
				// Show first 300 chars of content
				preview := cmd.Content
				if len(preview) > 300 {
					preview = preview[:297] + "..."
				}
				lines = append(lines, preview)
				lines = append(lines, "---")
				lines = append(lines, "")
				lines = append(lines, i18n.T("commands.browser.usage.command", cmd.Name))
				return CommandsBrowserMsg{Result: strings.Join(lines, "\n")}
			}
		}

		return CommandsBrowserMsg{Error: i18n.T("commands.browser.error.not_found", name)}
	}
}

func (c *CommandsBrowser) cmdListPlugins() tea.Cmd {
	return func() tea.Msg {
		c.refreshCommandList()

		var lines []string
		lines = append(lines, i18n.T("commands.browser.report.plugin_count", len(c.pluginCommands)))

		if len(c.pluginCommands) == 0 {
			lines = append(lines, i18n.T("commands.browser.plugin.none"))
			lines = append(lines, i18n.T("commands.browser.plugin.install"))
			return CommandsBrowserMsg{Result: strings.Join(lines, "\n")}
		}

		// Group by plugin
		byPlugin := make(map[string][]PluginCommandInfo)
		for _, cmd := range c.pluginCommands {
			byPlugin[cmd.Plugin] = append(byPlugin[cmd.Plugin], cmd)
		}

		// Sort plugin names
		var pluginNames []string
		for name := range byPlugin {
			pluginNames = append(pluginNames, name)
		}
		sort.Strings(pluginNames)

		for _, pluginName := range pluginNames {
			cmds := byPlugin[pluginName]
			lines = append(lines, i18n.T("commands.browser.from_plugin", pluginName))
			for _, cmd := range cmds {
				desc := cmd.Description
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}
				lines = append(lines, i18n.T("commands.browser.report.simple_command", cmd.Name, desc))
			}
			lines = append(lines, "")
		}

		return CommandsBrowserMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *CommandsBrowser) cmdListBuiltin() tea.Cmd {
	return func() tea.Msg {
		c.refreshCommandList()

		var lines []string
		lines = append(lines, i18n.T("commands.browser.report.builtin_count", len(c.builtinCommands)))

		for _, cmd := range c.builtinCommands {
			aliases := ""
			if len(cmd.Aliases) > 0 {
				aliases = i18n.T("commands.browser.aliases_inline", strings.Join(cmd.Aliases, ", "))
			}
			desc := cmd.Description
			if len(desc) > 50 {
				desc = desc[:47] + "..."
			}
			lines = append(lines, i18n.T("commands.browser.report.command", cmd.Name, aliases, desc))
		}

		return CommandsBrowserMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *CommandsBrowser) errorMsg(msg string) tea.Cmd {
	return func() tea.Msg {
		return CommandsBrowserMsg{Error: msg}
	}
}

func contains(slice []string, str string) bool {
	return slices.Contains(slice, str)
}

// View renders the command browser UI
func (c *CommandsBrowser) View() string {
	if !c.interactive {
		return ""
	}

	var b strings.Builder

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Accent)).
		MarginBottom(1)

	b.WriteString(titleStyle.Render(i18n.T("commands.browser.title")))
	b.WriteString("\n\n")

	if c.state == "list" {
		c.refreshCommandList()
		c.renderList(&b)
	} else if c.state == "info" {
		c.renderInfo(&b)
	}

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim)).
		MarginTop(1)

	help := i18n.T("commands.browser.hint.list")
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func (c *CommandsBrowser) renderList(b *strings.Builder) {
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Background(lipgloss.Color(palette.AccentDim)).
		Bold(true)
	pluginStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success))
	builtinStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Info))

	// Show command counts
	countLine := i18n.T("commands.browser.total",
		len(c.allCommands), len(c.builtinCommands), len(c.pluginCommands))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render(countLine))
	b.WriteString("\n\n")

	// Calculate visible range
	start := c.scrollOffset
	end := c.scrollOffset + c.maxVisible
	if end > len(c.allCommands) {
		end = len(c.allCommands)
	}

	// Render visible commands
	for i := start; i < end; i++ {
		cmd := c.allCommands[i]

		var line string
		switch v := cmd.(type) {
		case CommandInfo:
			line = fmt.Sprintf("/%s - %s", v.Name, v.Description)
			if i == c.selectedItem {
				line = selectedStyle.Render(line)
			} else {
				line = builtinStyle.Render(line)
			}

		case PluginCommandInfo:
			line = fmt.Sprintf("/%s [%s] - %s", v.Name, v.Plugin, v.Description)
			if i == c.selectedItem {
				line = selectedStyle.Render(line)
			} else {
				line = pluginStyle.Render(line)
			}
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	// Show scroll indicator if needed
	if len(c.allCommands) > c.maxVisible {
		indicator := i18n.T("commands.browser.showing", start+1, end, len(c.allCommands))
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render(indicator))
	}
}

func (c *CommandsBrowser) renderInfo(b *strings.Builder) {
	if c.currentCommand == nil {
		b.WriteString(i18n.T("commands.browser.none_selected"))
		return
	}

	switch v := c.currentCommand.(type) {
	case CommandInfo:
		b.WriteString(i18n.T("commands.browser.field.command_line", v.Name))
		b.WriteString(i18n.T("commands.browser.field.type_builtin_line"))
		b.WriteString(i18n.T("commands.browser.field.description_line", v.Description))
		if len(v.Aliases) > 0 {
			b.WriteString(i18n.T("commands.browser.field.aliases_line", strings.Join(v.Aliases, ", ")))
		}

	case PluginCommandInfo:
		b.WriteString(i18n.T("commands.browser.field.command_line", v.Name))
		b.WriteString(i18n.T("commands.browser.field.type_plugin_line"))
		b.WriteString(i18n.T("commands.browser.field.plugin_line", v.Plugin))
		b.WriteString(i18n.T("commands.browser.field.description_block", v.Description))
		b.WriteString(i18n.T("commands.browser.content_preview_line"))
		b.WriteString("---\n")
		preview := v.Content
		if len(preview) > 500 {
			preview = preview[:497] + "..."
		}
		b.WriteString(preview)
		b.WriteString("\n---\n")
	}
}

// Update handles messages for the interactive UI
func (c *CommandsBrowser) Update(msg tea.Msg) (Command, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			c.interactive = false
			return c, nil

		case "up", "k":
			if c.selectedItem > 0 {
				c.selectedItem--
				if c.selectedItem < c.scrollOffset {
					c.scrollOffset = c.selectedItem
				}
			}

		case "down", "j":
			if c.selectedItem < len(c.allCommands)-1 {
				c.selectedItem++
				if c.selectedItem >= c.scrollOffset+c.maxVisible {
					c.scrollOffset = c.selectedItem - c.maxVisible + 1
				}
			}

		case "enter":
			if c.selectedItem >= 0 && c.selectedItem < len(c.allCommands) {
				c.currentCommand = c.allCommands[c.selectedItem]
				c.state = "info"
			}

		case "backspace":
			if c.state == "info" {
				c.state = "list"
			}
		}

	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		c.maxVisible = max(
			// Reserve space for header/footer
			(msg.Height - 10), 5)
	}

	return c, nil
}

// CommandsBrowserMsg is the result message
type CommandsBrowserMsg struct {
	Result string
	Error  string
}
