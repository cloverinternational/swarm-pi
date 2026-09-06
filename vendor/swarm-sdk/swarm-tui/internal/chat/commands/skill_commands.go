package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// SkillCommand implements the /skill command for skill management
type SkillCommand struct {
	interactive  bool
	state        string // "menu", "list", "search", "info", "install", "confirm"
	selectedMenu int
	selectedItem int
	scrollOffset int
	maxVisible   int
	width        int
	height       int

	// Skill management
	loader        *skills.Loader
	skills        []*skills.Skill
	searchResults []skills.SkillSearchResult
	currentSkill  *skills.Skill
	searchQuery   string

	// Form state
	inputBuffer string

	// Results
	lastResult string
	lastError  string
}

// OpenSkillPickerMsg is sent when the user wants to open the skill picker overlay
type OpenSkillPickerMsg struct{}

// Menu options
const (
	SkillMenuList    = 0
	SkillMenuSearch  = 1
	SkillMenuInstall = 2
	SkillMenuRefresh = 3
)

var skillMenuOptions = []string{
	"commands.skill.menu.list",
	"commands.skill.menu.search",
	"commands.skill.menu.install",
	"commands.skill.menu.refresh",
}

// NewSkillCommand creates a new /skill command
func NewSkillCommand() *SkillCommand {
	return &SkillCommand{
		interactive:  false,
		state:        "menu",
		selectedMenu: 0,
		selectedItem: 0,
		scrollOffset: 0,
		maxVisible:   10,
		width:        80,
		height:       24,
		skills:       []*skills.Skill{},
	}
}

func (c *SkillCommand) Name() string {
	return "skill"
}

func (c *SkillCommand) Description() string {
	return i18n.T("commands.skill.description")
}

func (c *SkillCommand) Aliases() []string {
	return []string{"skills"}
}

func (c *SkillCommand) Execute(args []string) tea.Cmd {
	// Handle subcommands for non-interactive use
	if len(args) > 0 {
		subCmd := args[0]
		subArgs := args[1:]

		switch subCmd {
		case "list", "ls":
			return c.cmdList()

		case "enable":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.skill.usage.enable"))
			}
			return c.cmdEnable(subArgs[0])

		case "disable":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.skill.usage.disable"))
			}
			return c.cmdDisable(subArgs[0])

		case "search":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.skill.usage.search"))
			}
			return c.cmdSearch(strings.Join(subArgs, " "))

		case "install":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.skill.usage.install"))
			}
			return c.cmdInstall(subArgs[0])

		case "uninstall", "remove":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.skill.usage.uninstall"))
			}
			return c.cmdUninstall(subArgs[0])

		case "info":
			if len(subArgs) == 0 {
				return c.errorMsg(i18n.T("commands.skill.usage.info"))
			}
			return c.cmdInfo(subArgs[0])

		case "refresh":
			return c.cmdRefresh()
		}
	}

	// Open the skill picker overlay instead of the old interactive UI
	return func() tea.Msg {
		return OpenSkillPickerMsg{}
	}
}

func (c *SkillCommand) IsInteractive() bool {
	return false // Opens SkillPicker overlay instead of own interactive UI
}

// SetLoader sets the skill loader
func (c *SkillCommand) SetLoader(loader *skills.Loader) {
	c.loader = loader
}

// Command implementations

func (c *SkillCommand) cmdList() tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.loader_uninitialized")}
		}

		allSkills := c.loader.List()
		activeSkills := c.loader.GetActiveSkills()

		activeMap := make(map[string]bool)
		for _, s := range activeSkills {
			activeMap[s.Metadata.Name] = true
		}

		var lines []string
		lines = append(lines, i18n.T("commands.skill.list_summary", len(allSkills), len(activeSkills)))

		for _, skill := range allSkills {
			status := "[ ]"
			if activeMap[skill.Metadata.Name] {
				status = "[*]"
			}
			desc := skill.Metadata.Description
			if len(desc) > 45 {
				desc = desc[:42] + "..."
			}
			lines = append(lines, fmt.Sprintf("  %s %-20s - %s", status, skill.Metadata.Name, desc))
		}

		if len(allSkills) == 0 {
			lines = append(lines, i18n.T("commands.skill.none_installed_indented"))
		}

		return SkillResultMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *SkillCommand) cmdEnable(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.loader_uninitialized")}
		}

		if err := c.loader.Activate(name); err != nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.enable_failed", err)}
		}

		return SkillResultMsg{Result: i18n.T("commands.skill.enabled", name)}
	}
}

func (c *SkillCommand) cmdDisable(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.loader_uninitialized")}
		}

		if err := c.loader.Deactivate(name); err != nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.disable_failed", err)}
		}

		return SkillResultMsg{Result: i18n.T("commands.skill.disabled", name)}
	}
}

func (c *SkillCommand) cmdSearch(query string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.loader_uninitialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		results, err := c.loader.Search(ctx, query)
		if err != nil {
			return SkillResultMsg{Error: i18n.T("commands.common.search_failed", err)}
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

		return SkillResultMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *SkillCommand) cmdInstall(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.loader_uninitialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		skill, err := c.loader.Install(ctx, name)
		if err != nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.install_failed", err)}
		}

		return SkillResultMsg{Result: i18n.T("commands.skill.installed", skill.Metadata.Name, skill.Metadata.Version)}
	}
}

func (c *SkillCommand) cmdUninstall(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.loader_uninitialized")}
		}

		if err := c.loader.Uninstall(name); err != nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.uninstall_failed", err)}
		}

		return SkillResultMsg{Result: i18n.T("commands.skill.uninstalled", name)}
	}
}

func (c *SkillCommand) cmdInfo(name string) tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.loader_uninitialized")}
		}

		skill, found := c.loader.Registry.Get(name)
		if !found {
			return SkillResultMsg{Error: i18n.T("commands.skill.not_found", name)}
		}

		var lines []string
		lines = append(lines, i18n.T("commands.skill.label.skill", skill.Metadata.Name))
		lines = append(lines, i18n.T("commands.common.label.description", skill.Metadata.Description))

		if skill.Metadata.Version != "" {
			lines = append(lines, i18n.T("commands.common.label.version", skill.Metadata.Version))
		}
		if skill.Metadata.Author != "" {
			lines = append(lines, i18n.T("commands.common.label.author", skill.Metadata.Author))
		}
		if skill.Metadata.Category != "" {
			lines = append(lines, i18n.T("commands.common.label.category", skill.Metadata.Category))
		}

		status := i18n.T("commands.common.disabled")
		if c.loader.Registry.IsActive(name) {
			status = i18n.T("commands.common.enabled")
		}
		lines = append(lines, i18n.T("commands.common.label.status", status))
		lines = append(lines, i18n.T("commands.common.label.path", skill.Path))

		if len(skill.Scripts) > 0 {
			var scriptNames []string
			for _, s := range skill.Scripts {
				scriptNames = append(scriptNames, s.Name)
			}
			lines = append(lines, i18n.T("commands.skill.label.scripts", strings.Join(scriptNames, ", ")))
		}

		if len(skill.References) > 0 {
			var refNames []string
			for _, r := range skill.References {
				refNames = append(refNames, r.Name)
			}
			lines = append(lines, i18n.T("commands.skill.label.references", strings.Join(refNames, ", ")))
		}

		if len(skill.Metadata.Tags) > 0 {
			lines = append(lines, i18n.T("commands.skill.label.tags", strings.Join(skill.Metadata.Tags, ", ")))
		}

		return SkillResultMsg{Result: strings.Join(lines, "\n")}
	}
}

func (c *SkillCommand) cmdRefresh() tea.Cmd {
	return func() tea.Msg {
		if c.loader == nil {
			return SkillResultMsg{Error: i18n.T("commands.skill.loader_uninitialized")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := c.loader.Initialize(ctx); err != nil {
			return SkillResultMsg{Error: i18n.T("commands.common.refresh_failed", err)}
		}

		count := len(c.loader.List())
		return SkillResultMsg{Result: i18n.T("commands.skill.refreshed", count)}
	}
}

func (c *SkillCommand) errorMsg(msg string) tea.Cmd {
	return func() tea.Msg {
		return SkillResultMsg{Error: msg}
	}
}

func (c *SkillCommand) refreshSkillList() {
	if c.loader != nil {
		c.skills = c.loader.List()
	}
}

func (c *SkillCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
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
	case SkillResultMsg:
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

func (c *SkillCommand) handleKey(msg tea.KeyMsg) (Command, tea.Cmd) {
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
		if c.state == "list" && len(c.skills) > 0 {
			// Toggle skill enabled/disabled
			if c.selectedItem < len(c.skills) {
				skill := c.skills[c.selectedItem]
				if c.loader.Registry.IsActive(skill.Metadata.Name) {
					c.loader.Deactivate(skill.Metadata.Name)
				} else {
					c.loader.Activate(skill.Metadata.Name)
				}
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

func (c *SkillCommand) handleDown() {
	switch c.state {
	case "menu":
		if c.selectedMenu < len(skillMenuOptions)-1 {
			c.selectedMenu++
		}
	case "list":
		if c.selectedItem < len(c.skills)-1 {
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

func (c *SkillCommand) handleUp() {
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

func (c *SkillCommand) handleEnter() (Command, tea.Cmd) {
	switch c.state {
	case "menu":
		switch c.selectedMenu {
		case SkillMenuList:
			c.state = "list"
			c.selectedItem = 0
			c.scrollOffset = 0
			c.refreshSkillList()
		case SkillMenuSearch:
			c.state = "search"
			c.inputBuffer = ""
			c.searchResults = nil
			c.selectedItem = 0
		case SkillMenuInstall:
			c.state = "install"
			c.inputBuffer = ""
		case SkillMenuRefresh:
			return c, c.cmdRefresh()
		}

	case "list":
		if len(c.skills) > 0 && c.selectedItem < len(c.skills) {
			c.currentSkill = c.skills[c.selectedItem]
			c.state = "info"
		}

	case "search":
		if c.inputBuffer != "" && c.searchResults == nil {
			// Execute search
			c.searchQuery = c.inputBuffer
			if c.loader != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				results, _ := c.loader.Search(ctx, c.searchQuery)
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
	}

	return c, nil
}

func (c *SkillCommand) View() string {
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

func (c *SkillCommand) renderMenu() string {
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
	var activeCount, totalCount int
	if c.loader != nil {
		totalCount = len(c.loader.List())
		activeCount = len(c.loader.GetActiveSkills())
	}
	statusText := i18n.T("commands.skill.status", totalCount, activeCount)

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("commands.skill.title")))
	lines = append(lines, subtitleStyle.Render(statusText))
	lines = append(lines, "")

	menuDescs := []string{
		i18n.T("commands.skill.menu.list_desc"),
		i18n.T("commands.skill.menu.search_desc"),
		i18n.T("commands.skill.menu.install_desc"),
		i18n.T("commands.skill.menu.refresh_desc"),
	}

	for i, option := range skillMenuOptions {
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

func (c *SkillCommand) renderList() string {
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
	lines = append(lines, titleStyle.Render(i18n.T("commands.skill.installed_title")))
	lines = append(lines, subtitleStyle.Render(i18n.T("commands.skill.count", len(c.skills))))
	lines = append(lines, "")

	if len(c.skills) == 0 {
		lines = append(lines, dimStyle.Render(i18n.T("commands.skill.none_installed")))
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render(i18n.T("commands.skill.add_hint")))
	} else {
		endIdx := c.scrollOffset + c.maxVisible
		if endIdx > len(c.skills) {
			endIdx = len(c.skills)
		}

		for i := c.scrollOffset; i < endIdx; i++ {
			skill := c.skills[i]
			isSelected := i == c.selectedItem
			isActive := c.loader != nil && c.loader.Registry.IsActive(skill.Metadata.Name)

			status := inactiveStyle.Render("[ ]")
			if isActive {
				status = activeStyle.Render("[*]")
			}

			cursor := "  "
			nameStyle := normalStyle
			if isSelected {
				cursor = "> "
				nameStyle = selectedStyle
			}

			name := skill.Metadata.Name
			desc := skill.Metadata.Description
			if len(desc) > 40 {
				desc = desc[:37] + "..."
			}

			line := fmt.Sprintf("%s%s %-20s %s", cursor, status, nameStyle.Render(name), dimStyle.Render(desc))
			lines = append(lines, line)
		}
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.list_toggle")))

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

func (c *SkillCommand) renderSearch() string {
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
	lines = append(lines, titleStyle.Render(i18n.T("commands.skill.search_title")))

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

func (c *SkillCommand) renderInstall() string {
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
	lines = append(lines, titleStyle.Render(i18n.T("commands.skill.install_title")))
	lines = append(lines, subtitleStyle.Render(i18n.T("commands.skill.install_prompt")))
	lines = append(lines, "")
	lines = append(lines, i18n.T("commands.skill.name_label")+inputStyle.Render(c.inputBuffer+"_"))

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

	containerWidth := 60
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

func (c *SkillCommand) renderInfo() string {
	if c.currentSkill == nil {
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

	skill := c.currentSkill
	var lines []string

	lines = append(lines, titleStyle.Render(i18n.T("commands.skill.label.skill", skill.Metadata.Name)))
	lines = append(lines, "")

	lines = append(lines, labelStyle.Render(i18n.T("commands.common.description_label")))
	lines = append(lines, "  "+valueStyle.Render(skill.Metadata.Description))
	lines = append(lines, "")

	if skill.Metadata.Version != "" {
		lines = append(lines, labelStyle.Render(i18n.T("commands.common.version_label"))+valueStyle.Render(skill.Metadata.Version))
	}
	if skill.Metadata.Author != "" {
		lines = append(lines, labelStyle.Render(i18n.T("commands.common.author_label"))+valueStyle.Render(skill.Metadata.Author))
	}
	if skill.Metadata.Category != "" {
		lines = append(lines, labelStyle.Render(i18n.T("commands.common.category_label"))+valueStyle.Render(skill.Metadata.Category))
	}

	isActive := c.loader != nil && c.loader.Registry.IsActive(skill.Metadata.Name)
	statusLabel := labelStyle.Render(i18n.T("commands.common.status_label"))
	if isActive {
		lines = append(lines, statusLabel+activeStyle.Render(i18n.T("commands.common.enabled")))
	} else {
		lines = append(lines, statusLabel+inactiveStyle.Render(i18n.T("commands.common.disabled")))
	}

	lines = append(lines, labelStyle.Render(i18n.T("commands.common.path_label"))+dimStyle.Render(skill.Path))

	if len(skill.Scripts) > 0 {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(i18n.T("commands.skill.scripts_label")))
		for _, s := range skill.Scripts {
			lines = append(lines, "  - "+valueStyle.Render(s.Name))
		}
	}

	if len(skill.References) > 0 {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(i18n.T("commands.skill.references_label")))
		for _, r := range skill.References {
			lines = append(lines, "  - "+valueStyle.Render(r.Name))
		}
	}

	if len(skill.Metadata.Tags) > 0 {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(i18n.T("commands.skill.tags_label"))+dimStyle.Render(strings.Join(skill.Metadata.Tags, ", ")))
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.common.hint.return_list")))

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

// GetSkillNames returns a list of skill names for autocomplete
func (c *SkillCommand) GetSkillNames() []string {
	if c.loader == nil {
		return nil
	}
	skills := c.loader.List()
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Metadata.Name
	}
	return names
}

// Subcommands implements SubcommandProvider interface
func (c *SkillCommand) Subcommands() []Subcommand {
	return []Subcommand{
		{Name: "list", Description: i18n.T("commands.skill.subcommand.list")},
		{Name: "enable", Description: i18n.T("commands.skill.subcommand.enable")},
		{Name: "disable", Description: i18n.T("commands.skill.subcommand.disable")},
		{Name: "search", Description: i18n.T("commands.skill.subcommand.search")},
		{Name: "install", Description: i18n.T("commands.skill.subcommand.install")},
		{Name: "uninstall", Description: i18n.T("commands.skill.subcommand.uninstall")},
		{Name: "info", Description: i18n.T("commands.skill.subcommand.info")},
		{Name: "refresh", Description: i18n.T("commands.skill.subcommand.refresh")},
	}
}

// ArgumentCompletions implements SubcommandProvider interface
func (c *SkillCommand) ArgumentCompletions(subcommand string) []string {
	switch subcommand {
	case "enable", "disable", "info", "uninstall":
		// Return installed skill names
		return c.GetSkillNames()
	default:
		return nil
	}
}

// SkillResultMsg is sent when a skill operation completes
type SkillResultMsg struct {
	Result string
	Error  string
}
