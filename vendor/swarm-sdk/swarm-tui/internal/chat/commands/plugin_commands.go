package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// PluginCommand implements the /plugin command for Claude Code-compatible plugin management
type PluginCommand struct {
	interactive  bool
	state        string // "menu", "list", "search", "info", "install", "marketplace"
	selectedMenu int
	selectedItem int
	scrollOffset int
	maxVisible   int
	width        int
	height       int

	// Plugin management
	loader        *plugins.Loader
	marketplace   *plugins.MarketplaceDatabase
	searcher      *plugins.UnifiedSearcher
	plugins       []*plugins.Plugin
	searchResults []plugins.UnifiedSearchResult
	currentPlugin *plugins.Plugin
	searchQuery   string

	// Form state
	inputBuffer string

	// Results
	lastResult string
	lastError  string
}

// Menu options
const (
	PluginMenuList        = 0
	PluginMenuSearch      = 1
	PluginMenuInstall     = 2
	PluginMenuMarketplace = 3
	PluginMenuRefresh     = 4
)

var pluginMenuOptions = []string{
	"commands.plugin.menu.list",
	"commands.plugin.menu.search",
	"commands.plugin.menu.install",
	"commands.plugin.menu.marketplace",
	"commands.plugin.menu.refresh",
}

// NewPluginCommand creates a new /plugin command
func NewPluginCommand() *PluginCommand {
	return &PluginCommand{
		interactive:  false,
		state:        "menu",
		selectedMenu: 0,
		selectedItem: 0,
		scrollOffset: 0,
		maxVisible:   10,
		width:        80,
		height:       24,
		plugins:      []*plugins.Plugin{},
	}
}

func (c *PluginCommand) Name() string {
	return "plugin"
}

func (c *PluginCommand) Description() string {
	return i18n.T("commands.plugin.description")
}

func (c *PluginCommand) Aliases() []string {
	return []string{"plugins", "ext", "extension"}
}

func (c *PluginCommand) Execute(args []string) tea.Cmd {
	// Handle subcommands for non-interactive use
	if len(args) > 0 {
		subCmd := args[0]
		subArgs := args[1:]

		switch subCmd {
		case "list", "ls":
			return c.cmdList()

		case "enable":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.plugin.usage.enable"))
			}
			return c.cmdEnable(subArgs[0])

		case "disable":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.plugin.usage.disable"))
			}
			return c.cmdDisable(subArgs[0])

		case "search":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.plugin.usage.search"))
			}
			return c.cmdSearch(strings.Join(subArgs, " "))

		case "install":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.plugin.usage.install"))
			}
			return c.cmdInstall(subArgs[0])

		case "uninstall", "remove":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.plugin.usage.uninstall"))
			}
			return c.cmdUninstall(subArgs[0])

		case "info":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.plugin.usage.info"))
			}
			return c.cmdInfo(subArgs[0])

		case "refresh":
			return c.cmdRefresh()
		}
	}

	// Show interactive UI
	c.interactive = true
	c.state = "menu"
	c.selectedMenu = 0
	c.refreshPluginList()
	return nil
}

func (c *PluginCommand) IsInteractive() bool {
	return c.interactive
}

// SetLoader sets the plugin loader
func (c *PluginCommand) SetLoader(loader *plugins.Loader) {
	c.loader = loader
}

// SetMarketplace sets the marketplace database
func (c *PluginCommand) SetMarketplace(marketplace *plugins.MarketplaceDatabase) {
	c.marketplace = marketplace
}

// SetSearcher sets the unified searcher
func (c *PluginCommand) SetSearcher(searcher *plugins.UnifiedSearcher) {
	c.searcher = searcher
}

// Command implementations

func (c *PluginCommand) cmdList() tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.loader_uninitialized")}
		}

		allPlugins := c.loader.List()
		enabledPlugins := c.loader.GetEnabled()

		var lines []string
		lines = append(lines, i18n.T("commands.plugin.list_summary", len(allPlugins), len(enabledPlugins)))

		for _, plugin := range allPlugins {
			status := "[ ]"
			if plugin.Enabled {
				status = "[*]"
			}
			desc := plugin.Manifest.Description
			if len(desc) > 45 {
				desc = desc[:42] + "..."
			}
			source := c.getSourceLabel(plugin)
			lines = append(lines, fmt.Sprintf("  %s %-20s %s - %s", status, plugin.Manifest.Name, source, desc))
		}

		if len(allPlugins) == 0 {
			lines = append(lines, i18n.T("commands.plugin.none_installed_indented"))
		}

		return PluginResultMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *PluginCommand) cmdEnable(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.loader_uninitialized")}
		}

		if err := c.loader.Enable(name); err != nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.enable_failed", err)}
		}

		return PluginResultMsg{Result: i18n.T("commands.plugin.enabled", name)}
	}
}

func (c *PluginCommand) cmdDisable(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.loader_uninitialized")}
		}

		if err := c.loader.Disable(name); err != nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.disable_failed", err)}
		}

		return PluginResultMsg{Result: i18n.T("commands.plugin.disabled", name)}
	}
}

func (c *PluginCommand) cmdSearch(query string) tea.Cmd {
	return func() tea.Msg {
		if c.searcher == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.searcher_uninitialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		results, err := c.searcher.SearchPluginsOnly(ctx, query)
		if err != nil {
			return PluginResultMsg{Error: i18n.T("commands.common.search_failed", err)}
		}

		var lines []string
		lines = append(lines, i18n.T("commands.common.search_results_for", query))

		for _, result := range results {
			status := ""
			if result.Installed {
				status = i18n.T("commands.common.installed_parenthetical")
			}
			desc := result.Description
			if len(desc) > 40 {
				desc = desc[:37] + "..."
			}
			lines = append(lines, fmt.Sprintf("  %-20s%s - %s", result.Name, status, desc))
		}

		if len(results) == 0 {
			lines = append(lines, i18n.T("commands.common.no_results_indented"))
		}

		return PluginResultMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *PluginCommand) cmdInstall(source string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.loader_uninitialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		plugin, err := c.loader.Install(ctx, source, "")
		if err != nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.install_failed", err)}
		}

		return PluginResultMsg{Result: i18n.T("commands.plugin.installed", plugin.Manifest.Name, plugin.Manifest.Version)}
	}
}

func (c *PluginCommand) cmdUninstall(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.loader_uninitialized")}
		}

		if err := c.loader.Uninstall(name); err != nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.uninstall_failed", err)}
		}

		return PluginResultMsg{Result: i18n.T("commands.plugin.uninstalled", name)}
	}
}

func (c *PluginCommand) cmdInfo(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.loader_uninitialized")}
		}

		plugin := c.loader.Get(name)
		if plugin == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.not_found", name)}
		}

		var lines []string
		lines = append(lines, i18n.T("commands.plugin.label.plugin", plugin.Manifest.Name))
		lines = append(lines, i18n.T("commands.common.label.description", plugin.Manifest.Description))

		if plugin.Manifest.Version != "" {
			lines = append(lines, i18n.T("commands.common.label.version", plugin.Manifest.Version))
		}
		if plugin.Manifest.Author.Name != "" {
			lines = append(lines, i18n.T("commands.common.label.author", plugin.Manifest.Author.Name))
		}
		if plugin.Manifest.Category != "" {
			lines = append(lines, i18n.T("commands.common.label.category", plugin.Manifest.Category))
		}

		status := i18n.T("commands.common.disabled")
		if plugin.Enabled {
			status = i18n.T("commands.common.enabled")
		}
		lines = append(lines, i18n.T("commands.common.label.status", status))
		lines = append(lines, i18n.T("commands.common.label.source", c.getSourceLabel(plugin)))
		lines = append(lines, i18n.T("commands.common.label.path", plugin.Path))

		if len(plugin.Commands) > 0 {
			var cmdNames []string
			for _, cmd := range plugin.Commands {
				cmdNames = append(cmdNames, cmd.Name)
			}
			lines = append(lines, i18n.T("commands.plugin.label.commands", strings.Join(cmdNames, ", ")))
		}

		if len(plugin.Agents) > 0 {
			var agentNames []string
			for _, agent := range plugin.Agents {
				agentNames = append(agentNames, agent.Name)
			}
			lines = append(lines, i18n.T("commands.plugin.label.agents", strings.Join(agentNames, ", ")))
		}

		if len(plugin.MCPServers) > 0 {
			var serverNames []string
			for _, srv := range plugin.MCPServers {
				serverNames = append(serverNames, srv.Name)
			}
			lines = append(lines, i18n.T("commands.plugin.label.mcp_servers", strings.Join(serverNames, ", ")))
		}

		if len(plugin.Manifest.Keywords) > 0 {
			lines = append(lines, i18n.T("commands.plugin.label.keywords", strings.Join(plugin.Manifest.Keywords, ", ")))
		}

		return PluginResultMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *PluginCommand) cmdRefresh() tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return PluginResultMsg{Error: i18n.T("commands.plugin.loader_uninitialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := c.loader.Refresh(ctx); err != nil {
			return PluginResultMsg{Error: i18n.T("commands.common.refresh_failed", err)}
		}

		count := len(c.loader.List())
		return PluginResultMsg{Result: i18n.T("commands.plugin.refreshed", count)}
	}
}

func (c *PluginCommand) errorMsg(msg string) tea.Cmd {
	return func() tea.Msg {
		return PluginResultMsg{Error: msg}
	}
}

func (c *PluginCommand) refreshPluginList() {
	if c.loader != nil {
		c.plugins = c.loader.List()
	}
}

func (c *PluginCommand) getSourceLabel(plugin *plugins.Plugin) string {
	switch plugin.Source {
	case plugins.SourceUser:
		return "[USR]"
	case plugins.SourceProject:
		return "[PRJ]"
	case plugins.SourceMarketplace:
		return "[MKT]"
	case plugins.SourceGit:
		return "[GIT]"
	default:
		return "[LOC]"
	}
}

func (c *PluginCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	if !c.interactive {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return c.handleKey(msg)
	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		c.maxVisible = max((msg.Height-12)/2, 5)
	case PluginResultMsg:
		if msg.Error != "" {
			c.lastError = msg.Error
			c.lastResult = ""
		} else {
			c.lastResult = msg.Result
			c.lastError = ""
		}
	}

	return c, nil
}

func (c *PluginCommand) handleKey(msg tea.KeyMsg) (Command, tea.Cmd) {
	// Clear results on new input
	c.lastResult = ""
	c.lastError = ""

	switch msg.String() {
	case "esc":
		if c.state == "menu" {
			c.interactive = false
		} else {
			c.state = "menu"
			c.inputBuffer = ""
		}
		return c, nil

	case "backspace":
		if c.state == "search" || c.state == "install" {
			if len(c.inputBuffer) > 0 {
				c.inputBuffer = c.inputBuffer[:len(c.inputBuffer)-1]
				return c, nil
			}
		}
		if c.state != "menu" {
			c.state = "menu"
			c.inputBuffer = ""
		}
		return c, nil

	case "down", "j":
		c.handleDown()
	case "up", "k":
		c.handleUp()
	case "enter":
		return c.handleEnter()
	case " ", "space":
		if c.state == "list" && len(c.plugins) > 0 {
			// Toggle plugin enabled/disabled
			if c.selectedItem < len(c.plugins) {
				plugin := c.plugins[c.selectedItem]
				if plugin.Enabled {
					c.loader.Disable(plugin.Manifest.Name)
				} else {
					c.loader.Enable(plugin.Manifest.Name)
				}
				c.refreshPluginList()
			}
		}
	default:
		// Text input for search/install
		if (c.state == "search" || c.state == "install") && len(msg.String()) == 1 {
			c.inputBuffer += msg.String()
		}
	}

	return c, nil
}

func (c *PluginCommand) handleDown() {
	switch c.state {
	case "menu":
		if c.selectedMenu < len(pluginMenuOptions)-1 {
			c.selectedMenu++
		}
	case "list":
		if c.selectedItem < len(c.plugins)-1 {
			c.selectedItem++
			if c.selectedItem >= c.scrollOffset+c.maxVisible {
				c.scrollOffset = c.selectedItem - c.maxVisible + 1
			}
		}
	case "search":
		if c.selectedItem < len(c.searchResults)-1 {
			c.selectedItem++
			if c.selectedItem >= c.scrollOffset+c.maxVisible {
				c.scrollOffset = c.selectedItem - c.maxVisible + 1
			}
		}
	}
}

func (c *PluginCommand) handleUp() {
	switch c.state {
	case "menu":
		if c.selectedMenu > 0 {
			c.selectedMenu--
		}
	case "list", "search":
		if c.selectedItem > 0 {
			c.selectedItem--
			if c.selectedItem < c.scrollOffset {
				c.scrollOffset = c.selectedItem
			}
		}
	}
}

func (c *PluginCommand) handleEnter() (Command, tea.Cmd) {
	switch c.state {
	case "menu":
		switch c.selectedMenu {
		case PluginMenuList:
			c.state = "list"
			c.selectedItem = 0
			c.scrollOffset = 0
			c.refreshPluginList()
		case PluginMenuSearch:
			c.state = "search"
			c.inputBuffer = ""
			c.searchResults = nil
			c.selectedItem = 0
		case PluginMenuInstall:
			c.state = "install"
			c.inputBuffer = ""
		case PluginMenuMarketplace:
			c.state = "marketplace"
		case PluginMenuRefresh:
			return c, c.cmdRefresh()
		}

	case "list":
		if len(c.plugins) > 0 && c.selectedItem < len(c.plugins) {
			c.currentPlugin = c.plugins[c.selectedItem]
			c.state = "info"
		}

	case "search":
		if c.inputBuffer != "" && c.searchResults == nil {
			// Execute search
			c.searchQuery = c.inputBuffer
			if c.searcher != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				results, _ := c.searcher.SearchPluginsOnly(ctx, c.searchQuery)
				cancel()
				c.searchResults = results
				c.selectedItem = 0
				c.scrollOffset = 0
			}
		} else if len(c.searchResults) > 0 && c.selectedItem < len(c.searchResults) {
			// Install selected result
			result := c.searchResults[c.selectedItem]
			if !result.Installed {
				return c, c.cmdInstall(result.Name)
			}
		}

	case "install":
		if c.inputBuffer != "" {
			return c, c.cmdInstall(c.inputBuffer)
		}

	case "info":
		c.state = "list"

	case "marketplace":
		c.state = "menu"
	}

	return c, nil
}

func (c *PluginCommand) View() string {
	if !c.interactive {
		return ""
	}

	var content string
	switch c.state {
	case "menu":
		content = c.renderMenu()
	case "list":
		content = c.renderList()
	case "search":
		content = c.renderSearch()
	case "install":
		content = c.renderInstall()
	case "info":
		content = c.renderInfo()
	case "marketplace":
		content = c.renderMarketplace()
	}

	// Center on screen
	if c.height > 0 {
		lines := strings.Count(content, "\n") + 1
		topPad := (c.height - lines) / 2
		if topPad > 0 {
			content = strings.Repeat("\n", topPad) + content
		}
	}

	return content
}

func (c *PluginCommand) renderMenu() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim)).
		Italic(true)

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	normalStyle := lipgloss.NewStyle()

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	// Stats
	var enabledCount, totalCount int
	if c.loader != nil {
		totalCount = len(c.loader.List())
		enabledCount = len(c.loader.GetEnabled())
	}
	statusText := i18n.T("commands.plugin.status", totalCount, enabledCount)

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("commands.plugin.title")))
	lines = append(lines, subtitleStyle.Render(statusText))
	lines = append(lines, "")

	menuDescs := []string{
		i18n.T("commands.plugin.menu.list_desc"),
		i18n.T("commands.plugin.menu.search_desc"),
		i18n.T("commands.plugin.menu.install_desc"),
		i18n.T("commands.plugin.menu.marketplace_desc"),
		i18n.T("commands.plugin.menu.refresh_desc"),
	}

	for i, option := range pluginMenuOptions {
		cursor := "  "
		style := normalStyle
		if i == c.selectedMenu {
			cursor = "> "
			style = selectedStyle
		}
		lines = append(lines, cursor+style.Render(i18n.T(option)))
		if i == c.selectedMenu {
			lines = append(lines, "    "+dimStyle.Render(menuDescs[i]))
		}
	}

	// Result/Error
	if c.lastResult != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success)).Render(c.lastResult))
	}
	if c.lastError != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Error)).Render(c.lastError))
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.menu")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerWidth := 70
	if c.width > 0 && c.width < containerWidth+4 {
		containerWidth = c.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2)

	return container.Render(content)
}

func (c *PluginCommand) renderList() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim)).
		Italic(true)

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	normalStyle := lipgloss.NewStyle()

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Success))

	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("commands.plugin.installed_title")))
	lines = append(lines, subtitleStyle.Render(i18n.T("commands.plugin.count", len(c.plugins))))
	lines = append(lines, "")

	if len(c.plugins) == 0 {
		lines = append(lines, dimStyle.Render(i18n.T("commands.plugin.none_installed")))
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render(i18n.T("commands.plugin.add_hint")))
	} else {
		endIdx := c.scrollOffset + c.maxVisible
		if endIdx > len(c.plugins) {
			endIdx = len(c.plugins)
		}

		for i := c.scrollOffset; i < endIdx; i++ {
			plugin := c.plugins[i]
			isSelected := i == c.selectedItem

			status := inactiveStyle.Render("[ ]")
			if plugin.Enabled {
				status = activeStyle.Render("[*]")
			}

			cursor := "  "
			nameStyle := normalStyle
			if isSelected {
				cursor = "> "
				nameStyle = selectedStyle
			}

			name := plugin.Manifest.Name
			desc := plugin.Manifest.Description
			if len(desc) > 35 {
				desc = desc[:32] + "..."
			}

			source := c.getSourceLabel(plugin)
			line := fmt.Sprintf("%s%s %-18s %s %s", cursor, status, nameStyle.Render(name), dimStyle.Render(source), dimStyle.Render(desc))
			lines = append(lines, line)
		}
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.list_toggle")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerWidth := 85
	if c.width > 0 && c.width < containerWidth+4 {
		containerWidth = c.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2)

	return container.Render(content)
}

func (c *PluginCommand) renderSearch() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim)).
		Italic(true)

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	normalStyle := lipgloss.NewStyle()

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	inputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent))

	installedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Success))

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("commands.plugin.search_title")))

	if c.searchResults == nil {
		lines = append(lines, subtitleStyle.Render(i18n.T("commands.common.enter_search_query")))
		lines = append(lines, "")
		lines = append(lines, i18n.T("commands.common.search_label")+inputStyle.Render(c.inputBuffer+"_"))
	} else {
		lines = append(lines, subtitleStyle.Render(i18n.T("commands.common.results_found", c.searchQuery, len(c.searchResults))))
		lines = append(lines, "")

		if len(c.searchResults) == 0 {
			lines = append(lines, dimStyle.Render(i18n.T("commands.common.no_results")))
		} else {
			endIdx := c.scrollOffset + c.maxVisible
			if endIdx > len(c.searchResults) {
				endIdx = len(c.searchResults)
			}

			for i := c.scrollOffset; i < endIdx; i++ {
				result := c.searchResults[i]
				isSelected := i == c.selectedItem

				cursor := "  "
				nameStyle := normalStyle
				if isSelected {
					cursor = "> "
					nameStyle = selectedStyle
				}

				status := ""
				if result.Installed {
					status = installedStyle.Render(i18n.T("commands.common.installed_parenthetical"))
				}

				desc := result.Description
				if len(desc) > 35 {
					desc = desc[:32] + "..."
				}

				line := fmt.Sprintf("%s%-20s%s - %s", cursor, nameStyle.Render(result.Name), status, dimStyle.Render(desc))
				lines = append(lines, line)
			}
		}
	}

	// Result/Error
	if c.lastResult != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success)).Render(c.lastResult))
	}
	if c.lastError != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Error)).Render(c.lastError))
	}

	lines = append(lines, "")
	if c.searchResults == nil {
		lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.search")))
	} else {
		lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.search_results")))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerWidth := 80
	if c.width > 0 && c.width < containerWidth+4 {
		containerWidth = c.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2)

	return container.Render(content)
}

func (c *PluginCommand) renderInstall() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim)).
		Italic(true)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	inputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent))

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("commands.plugin.install_title")))
	lines = append(lines, subtitleStyle.Render(i18n.T("commands.plugin.install_prompt")))
	lines = append(lines, "")
	lines = append(lines, i18n.T("commands.common.source_label")+inputStyle.Render(c.inputBuffer+"_"))

	// Result/Error
	if c.lastResult != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success)).Render(c.lastResult))
	}
	if c.lastError != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Error)).Render(c.lastError))
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.install")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerWidth := 65
	if c.width > 0 && c.width < containerWidth+4 {
		containerWidth = c.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2)

	return container.Render(content)
}

func (c *PluginCommand) renderInfo() string {
	if c.currentPlugin == nil {
		return ""
	}

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Bold(true)

	valueStyle := lipgloss.NewStyle()

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Success))

	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Warning))

	plugin := c.currentPlugin
	var lines []string

	lines = append(lines, titleStyle.Render(i18n.T("commands.plugin.label.plugin", plugin.Manifest.Name)))
	lines = append(lines, "")

	lines = append(lines, labelStyle.Render(i18n.T("commands.common.description_label")))
	lines = append(lines, "  "+valueStyle.Render(plugin.Manifest.Description))
	lines = append(lines, "")

	if plugin.Manifest.Version != "" {
		lines = append(lines, labelStyle.Render(i18n.T("commands.common.version_label"))+valueStyle.Render(plugin.Manifest.Version))
	}
	if plugin.Manifest.Author.Name != "" {
		lines = append(lines, labelStyle.Render(i18n.T("commands.common.author_label"))+valueStyle.Render(plugin.Manifest.Author.Name))
	}
	if plugin.Manifest.Category != "" {
		lines = append(lines, labelStyle.Render(i18n.T("commands.common.category_label"))+valueStyle.Render(plugin.Manifest.Category))
	}

	statusLabel := labelStyle.Render(i18n.T("commands.common.status_label"))
	if plugin.Enabled {
		lines = append(lines, statusLabel+activeStyle.Render(i18n.T("commands.common.enabled")))
	} else {
		lines = append(lines, statusLabel+inactiveStyle.Render(i18n.T("commands.common.disabled")))
	}

	lines = append(lines, labelStyle.Render(i18n.T("commands.common.source_label"))+valueStyle.Render(c.getSourceLabel(plugin)))
	lines = append(lines, labelStyle.Render(i18n.T("commands.common.path_label"))+dimStyle.Render(plugin.Path))

	if len(plugin.Commands) > 0 {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(i18n.T("commands.plugin.commands_label")))
		for _, cmd := range plugin.Commands {
			lines = append(lines, "  - "+valueStyle.Render(cmd.Name)+": "+dimStyle.Render(cmd.Description))
		}
	}

	if len(plugin.Agents) > 0 {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(i18n.T("commands.plugin.agents_label")))
		for _, agent := range plugin.Agents {
			lines = append(lines, "  - "+valueStyle.Render(agent.Name)+": "+dimStyle.Render(agent.Description))
		}
	}

	if len(plugin.MCPServers) > 0 {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(i18n.T("commands.plugin.mcp_servers_label")))
		for _, srv := range plugin.MCPServers {
			serverType := srv.Type
			if serverType == "" {
				serverType = "stdio"
			}
			lines = append(lines, "  - "+valueStyle.Render(srv.Name)+dimStyle.Render(" ("+serverType+")"))
		}
	}

	if len(plugin.Manifest.Keywords) > 0 {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(i18n.T("commands.plugin.keywords_label"))+dimStyle.Render(strings.Join(plugin.Manifest.Keywords, ", ")))
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.return_list")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerWidth := 85
	if c.width > 0 && c.width < containerWidth+4 {
		containerWidth = c.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2)

	return container.Render(content)
}

func (c *PluginCommand) renderMarketplace() string {
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim)).
		Italic(true)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	normalStyle := lipgloss.NewStyle()

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("commands.plugin.marketplace_title")))
	lines = append(lines, subtitleStyle.Render(i18n.T("commands.plugin.marketplace_subtitle")))
	lines = append(lines, "")

	if c.marketplace != nil {
		sources := c.marketplace.GetSources()
		if len(sources) == 0 {
			lines = append(lines, dimStyle.Render(i18n.T("commands.plugin.no_marketplace_sources")))
		} else {
			for _, src := range sources {
				lines = append(lines, normalStyle.Render("  "+src.Name))
				lines = append(lines, dimStyle.Render("    "+src.URL))
			}
		}
	} else {
		lines = append(lines, dimStyle.Render(i18n.T("commands.plugin.marketplace_uninitialized")))
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.return_menu")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerWidth := 70
	if c.width > 0 && c.width < containerWidth+4 {
		containerWidth = c.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2)

	return container.Render(content)
}

// GetPluginNames returns a list of plugin names for autocomplete
func (c *PluginCommand) GetPluginNames() []string {
	if c.loader == nil {
		return nil
	}
	plugins := c.loader.List()
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Manifest.Name
	}
	return names
}

// Subcommands implements SubcommandProvider interface
func (c *PluginCommand) Subcommands() []Subcommand {
	return []Subcommand{
		{Name: "list", Description: i18n.T("commands.plugin.subcommand.list")},
		{Name: "enable", Description: i18n.T("commands.plugin.subcommand.enable")},
		{Name: "disable", Description: i18n.T("commands.plugin.subcommand.disable")},
		{Name: "search", Description: i18n.T("commands.plugin.subcommand.search")},
		{Name: "install", Description: i18n.T("commands.plugin.subcommand.install")},
		{Name: "uninstall", Description: i18n.T("commands.plugin.subcommand.uninstall")},
		{Name: "info", Description: i18n.T("commands.plugin.subcommand.info")},
		{Name: "refresh", Description: i18n.T("commands.plugin.subcommand.refresh")},
	}
}

// ArgumentCompletions implements SubcommandProvider interface
func (c *PluginCommand) ArgumentCompletions(subcommand string) []string {
	switch subcommand {
	case "enable", "disable", "info", "uninstall":
		// Return installed plugin names
		return c.GetPluginNames()
	default:
		return nil
	}
}

// PluginResultMsg is sent when a plugin operation completes
type PluginResultMsg struct {
	Result string
	Error  string
}
