package commands

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// DisplayMode represents a tool display mode
type DisplayMode struct {
	ID          string
	Name        string
	Description string
}

var displayModes = []DisplayMode{
	{"verbose", "commands.render.mode.verbose", "commands.render.mode.verbose_desc"},
	{"compact", "commands.render.mode.compact", "commands.render.mode.compact_desc"},
	{"minimal", "commands.render.mode.minimal", "commands.render.mode.minimal_desc"},
	{"hidden", "commands.render.mode.hidden", "commands.render.mode.hidden_desc"},
	{"diff", "commands.render.mode.diff", "commands.render.mode.diff_desc"},
}

// ColorPreset represents a predefined color scheme
type ColorPreset struct {
	Name        string
	ToolCall    string
	ToolOutput  string
	ToolError   string
	Connector   string
	Description string
}

var colorPresets = []ColorPreset{
	{
		Name:        "commands.render.color.default",
		ToolCall:    palette.Accent,
		ToolOutput:  palette.TextDim,
		ToolError:   palette.Error,
		Connector:   palette.TextMuted,
		Description: "commands.render.color.default_desc",
	},
	{
		Name:        "commands.render.color.blue_green",
		ToolCall:    palette.Info,
		ToolOutput:  palette.Success,
		ToolError:   palette.Error,
		Connector:   palette.TextMuted,
		Description: "commands.render.color.blue_green_desc",
	},
	{
		Name:        "commands.render.color.purple_cyan",
		ToolCall:    palette.AccentSoft,
		ToolOutput:  palette.Teal,
		ToolError:   palette.Error,
		Connector:   palette.TextMuted,
		Description: "commands.render.color.purple_cyan_desc",
	},
	{
		Name:        "commands.render.color.monochrome",
		ToolCall:    palette.Text,
		ToolOutput:  palette.TextDim,
		ToolError:   palette.Error,
		Connector:   palette.TextMuted,
		Description: "commands.render.color.monochrome_desc",
	},
}

// RenderSettings interface (matches the actual struct in chat package)
type RenderSettings interface {
	GetDisplayMode(toolName string) string
	GetMaxLines(toolName string) int
	GetToolConfig(toolName string) interface {
		GetName() string
		GetDisplayMode() string
		GetMaxLines() int
		GetShowParams() bool
	}
	SetToolMode(toolName, mode string)
	SetToolMaxLines(toolName string, maxLines int)
	SetThinkingDisplay(show bool)
	SetColorScheme(toolCall, toolOutput, toolError, connector string)
	Save() error
	Reset()
	GetShowThinking() bool
	GetStartupView() string
	SetStartupView(view string)
	GetToolCall() string
	GetToolOutput() string
	GetToolError() string
	GetConnector() string
	GetAllTools() []string
}

// ToolConfig interface
type ToolConfig interface {
	GetName() string
	GetDisplayMode() string
	GetMaxLines() int
	GetShowParams() bool
}

// RenderCommand manages tool output rendering settings
type RenderCommand struct {
	interactive  bool
	state        string // "menu", "tool_list", "tool_config", "mode_select", "maxlines", "colors"
	selected     int
	scrollOffset int
	maxVisible   int
	width        int
	height       int

	// Settings (set from app)
	settings RenderSettings

	// Navigation state
	selectedTool  string
	maxLinesInput string

	// Callbacks
	onUpdate func()
}

// Menu options
const (
	MenuViewSettings = iota
	MenuToggleThinking
	MenuToggleStartupView
	MenuConfigureTools
	MenuCustomizeColors
	MenuResetDefaults
	MenuSaveSettings
)

var renderMenuOptions = []string{
	"commands.render.menu.view",
	"commands.render.menu.toggle_thinking",
	"commands.render.menu.toggle_startup",
	"commands.render.menu.configure_tools",
	"commands.render.menu.colors",
	"commands.render.menu.reset",
	"commands.render.menu.save",
}

// NewRenderCommand creates a new render settings command
func NewRenderCommand() *RenderCommand {
	return &RenderCommand{
		interactive:  false,
		state:        "menu",
		selected:     0,
		scrollOffset: 0,
		maxVisible:   10,
		width:        80,
		height:       24,
	}
}

// Name returns the command name
func (c *RenderCommand) Name() string {
	return "render"
}

// Description returns the command description
func (c *RenderCommand) Description() string {
	return i18n.T("commands.render.description")
}

// Aliases returns command aliases
func (c *RenderCommand) Aliases() []string {
	return []string{"display", "output"}
}

// SetOnUpdate sets the callback for when settings are updated
func (c *RenderCommand) SetOnUpdate(callback func()) {
	c.onUpdate = callback
}

// SetSettings sets the render settings reference
func (c *RenderCommand) SetSettings(settings RenderSettings) {
	c.settings = settings
}

// Execute runs the command
func (c *RenderCommand) Execute(args []string) tea.Cmd {
	c.interactive = true
	c.state = "menu"
	c.selected = 0
	return nil
}

// IsInteractive returns whether the command is interactive
func (c *RenderCommand) IsInteractive() bool {
	return c.interactive
}

// Update handles keyboard input
func (c *RenderCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	if !c.interactive {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		c.maxVisible = max((msg.Height-12)/2, 5)

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if c.state == "menu" {
				c.interactive = false
			} else {
				c.state = "menu"
				c.selected = 0
			}
			return c, nil

		case "up", "k":
			c.handleUp()

		case "down", "j":
			c.handleDown()

		case "enter":
			c.handleEnter()

		// Number input for max lines
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if c.state == "maxlines" {
				c.maxLinesInput += msg.String()
			}

		case "backspace":
			if c.state == "maxlines" && len(c.maxLinesInput) > 0 {
				c.maxLinesInput = c.maxLinesInput[:len(c.maxLinesInput)-1]
			} else if c.state != "menu" {
				c.state = "menu"
				c.selected = 0
			}
		}
	}

	return c, nil
}

func (c *RenderCommand) handleUp() {
	switch c.state {
	case "menu":
		if c.selected > 0 {
			c.selected--
		}
	case "tool_list":
		if c.selected > 0 {
			c.selected--
			if c.selected < c.scrollOffset {
				c.scrollOffset = c.selected
			}
		}
	case "mode_select":
		if c.selected > 0 {
			c.selected--
		}
	case "colors":
		if c.selected > 0 {
			c.selected--
		}
	}
}

func (c *RenderCommand) handleDown() {
	switch c.state {
	case "menu":
		if c.selected < len(renderMenuOptions)-1 {
			c.selected++
		}
	case "tool_list":
		tools := c.settings.GetAllTools()
		if c.selected < len(tools)-1 {
			c.selected++
			if c.selected >= c.scrollOffset+c.maxVisible {
				c.scrollOffset = c.selected - c.maxVisible + 1
			}
		}
	case "mode_select":
		if c.selected < len(displayModes)-1 {
			c.selected++
		}
	case "colors":
		if c.selected < len(colorPresets)-1 {
			c.selected++
		}
	}
}

func (c *RenderCommand) handleEnter() {
	switch c.state {
	case "menu":
		switch c.selected {
		case MenuViewSettings:
			c.state = "view"
		case MenuToggleThinking:
			c.settings.SetThinkingDisplay(!c.settings.GetShowThinking())
			if c.onUpdate != nil {
				c.onUpdate()
			}
		case MenuToggleStartupView:
			current := c.settings.GetStartupView()
			if current == "simple" {
				c.settings.SetStartupView("advanced")
			} else {
				c.settings.SetStartupView("simple")
			}
			if c.onUpdate != nil {
				c.onUpdate()
			}
		case MenuConfigureTools:
			c.state = "tool_list"
			c.selected = 0
			c.scrollOffset = 0
		case MenuCustomizeColors:
			c.state = "colors"
			c.selected = 0
		case MenuResetDefaults:
			c.settings.Reset()
			if c.onUpdate != nil {
				c.onUpdate()
			}
			c.state = "menu"
		case MenuSaveSettings:
			// Best-effort: keep UI responsive even if persistence fails.
			_ = c.settings.Save()
			c.interactive = false
		}

	case "tool_list":
		tools := c.settings.GetAllTools()
		if c.selected < len(tools) {
			c.selectedTool = tools[c.selected]
			c.state = "tool_config"
			c.selected = 0
		}

	case "tool_config":
		// Tool config submenu: 0=set mode, 1=set max lines, 2=back
		switch c.selected {
		case 0:
			c.state = "mode_select"
			c.selected = 0
		case 1:
			c.state = "maxlines"
			c.maxLinesInput = fmt.Sprintf("%d", c.settings.GetMaxLines(c.selectedTool))
		case 2:
			c.state = "tool_list"
			c.selected = 0
		}

	case "mode_select":
		mode := displayModes[c.selected]
		c.settings.SetToolMode(c.selectedTool, mode.ID)
		if c.onUpdate != nil {
			c.onUpdate()
		}
		c.state = "tool_config"
		c.selected = 0

	case "maxlines":
		// Parse and set max lines
		var maxLines int
		if _, err := fmt.Sscanf(c.maxLinesInput, "%d", &maxLines); err == nil && maxLines > 0 {
			c.settings.SetToolMaxLines(c.selectedTool, maxLines)
			if c.onUpdate != nil {
				c.onUpdate()
			}
		}
		c.state = "tool_config"
		c.selected = 0

	case "colors":
		preset := colorPresets[c.selected]
		c.settings.SetColorScheme(preset.ToolCall, preset.ToolOutput, preset.ToolError, preset.Connector)
		if c.onUpdate != nil {
			c.onUpdate()
		}
		c.state = "menu"
		c.selected = 0
	}
}

// View renders the command UI
func (c *RenderCommand) View() string {
	if !c.interactive {
		return ""
	}

	if c.settings == nil {
		return c.renderError(i18n.T("commands.render.settings_uninitialized"))
	}

	var content string
	switch c.state {
	case "menu":
		content = c.renderMainMenu()
	case "view":
		content = c.renderViewSettings()
	case "tool_list":
		content = c.renderToolList()
	case "tool_config":
		content = c.renderToolConfig()
	case "mode_select":
		content = c.renderModeSelect()
	case "maxlines":
		content = c.renderMaxLinesInput()
	case "colors":
		content = c.renderColorPicker()
	default:
		content = c.renderMainMenu()
	}

	// Center both horizontally and vertically
	if c.width > 0 && c.height > 0 {
		content = lipgloss.Place(
			c.width,
			c.height,
			lipgloss.Center,
			lipgloss.Center,
			content,
		)
	}

	return content
}

func (c *RenderCommand) renderError(msg string) string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorRed)).
		Bold(true).
		Render(i18n.T("commands.common.error"))

	message := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(msg)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", message)
	return c.wrapContainer(content, 50)
}

func (c *RenderCommand) renderMainMenu() string {
	// Responsive width
	maxWidth := 55
	if c.width > 0 && c.width < 80 {
		maxWidth = max(c.width-10, 35)
	}

	// Header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)
	header := headerStyle.Render(i18n.T("commands.render.title"))

	// Current state indicator
	thinkingState := i18n.T("commands.common.off_upper")
	thinkingColor := palette.TextMuted
	if c.settings.GetShowThinking() {
		thinkingState = i18n.T("commands.common.on_upper")
		thinkingColor = palette.Success
	}

	stateStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(thinkingColor))
	stateIndicator := stateStyle.Render(i18n.T("commands.render.thinking_status", thinkingState))

	startupView := c.settings.GetStartupView()
	if startupView == "" {
		startupView = "simple"
	}
	startupColor := palette.Success
	if startupView == "advanced" {
		startupColor = palette.TextMuted
	}
	startupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(startupColor))
	startupIndicator := startupStyle.Render(i18n.T("commands.render.startup_view", startupViewLabel(startupView)))

	// Menu items
	var items []string
	for i, option := range renderMenuOptions {
		isSelected := i == c.selected

		var prefix string
		if isSelected {
			prefix = ">"
		} else {
			prefix = " "
		}

		if isSelected {
			itemStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(palette.Info)).
				Bold(true)
			items = append(items, itemStyle.Render(fmt.Sprintf("%s %s", prefix, i18n.T(option))))
		} else {
			itemStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(palette.TextDim))
			items = append(items, itemStyle.Render(fmt.Sprintf("%s %s", prefix, i18n.T(option))))
		}
	}

	menuList := strings.Join(items, "\n")

	// Keybinds footer
	keybinds := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextMuted)).
		Italic(true).
		Render(i18n.T("commands.render.main_hint"))

	// Assemble
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		stateIndicator,
		startupIndicator,
		"",
		menuList,
		"",
		keybinds,
	)

	// Responsive padding
	padding := 2
	if maxWidth < 45 {
		padding = 1
	}

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(padding, padding*2)

	return containerStyle.Render(content)
}

func (c *RenderCommand) renderViewSettings() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands.render.current_settings"))

	// Thinking display
	thinkingState := i18n.T("commands.common.disabled")
	if c.settings.GetShowThinking() {
		thinkingState = i18n.T("commands.common.enabled")
	}
	thinkingLine := i18n.T("commands.render.thinking_display", thinkingState)

	// Colors
	colorLines := []string{
		"",
		lipgloss.NewStyle().Bold(true).Render(i18n.T("commands.render.colors_label")),
		i18n.T("commands.render.tool_call_color", c.colorPreview(c.settings.GetToolCall())),
		i18n.T("commands.render.tool_output_color", c.colorPreview(c.settings.GetToolOutput())),
		i18n.T("commands.render.tool_error_color", c.colorPreview(c.settings.GetToolError())),
		i18n.T("commands.render.connector_color", c.colorPreview(c.settings.GetConnector())),
	}

	// Tools
	toolLines := []string{
		"",
		lipgloss.NewStyle().Bold(true).Render(i18n.T("commands.render.tool_settings_label")),
	}
	for _, toolName := range c.settings.GetAllTools() {
		config := c.settings.GetToolConfig(toolName)
		line := i18n.T("commands.render.tool_setting_row",
			config.GetName(),
			displayModeLabel(config.GetDisplayMode()),
			config.GetMaxLines(),
			yesNoLabel(config.GetShowParams()))
		toolLines = append(toolLines, line)
	}

	allLines := []string{thinkingLine}
	allLines = append(allLines, colorLines...)
	allLines = append(allLines, toolLines...)

	content := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(strings.Join(allLines, "\n"))

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands.render.view_hint"))

	fullContent := lipgloss.JoinVertical(lipgloss.Left, title, "", content, "", hint)
	return c.wrapContainer(fullContent, 90)
}

func (c *RenderCommand) renderToolList() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands.render.tool_list_title"))

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		MarginBottom(1).
		Render(i18n.T("commands.render.tool_list_subtitle"))

	tools := c.settings.GetAllTools()
	var items []string

	totalItems := len(tools)
	endIdx := c.scrollOffset + c.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}

	for i := c.scrollOffset; i < endIdx; i++ {
		tool := tools[i]
		config := c.settings.GetToolConfig(tool)
		isSelected := i == c.selected

		var itemText string
		if isSelected {
			toolLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render(fmt.Sprintf("▶ %s", tool))

			statusLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(i18n.T("commands.render.tool_status", displayModeLabel(config.GetDisplayMode()), config.GetMaxLines()))

			itemText = lipgloss.JoinVertical(lipgloss.Left, toolLine, statusLine, "")
		} else {
			itemText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render(fmt.Sprintf("  %-15s  %s", tool, displayModeLabel(config.GetDisplayMode())))
		}

		items = append(items, itemText)
	}

	// Add scroll indicator
	if totalItems > c.maxVisible {
		scrollInfo := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorYellow)).
			Bold(true).
			Align(lipgloss.Center).
			Render(i18n.T("commands.render.scroll_status", c.scrollOffset+1, endIdx, totalItems))
		items = append(items, "", scrollInfo)
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands.render.tool_list_hint"))

	content := lipgloss.JoinVertical(lipgloss.Left, title, subtitle, "", list, "", hint)
	return c.wrapContainer(content, 75)
}

func (c *RenderCommand) renderToolConfig() string {
	config := c.settings.GetToolConfig(c.selectedTool)

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands.render.configure_title", c.selectedTool))

	current := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Render(i18n.T("commands.render.configure_current", displayModeLabel(config.GetDisplayMode()), config.GetMaxLines()))

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		MarginBottom(1).
		Render(i18n.T("commands.render.configure_subtitle"))

	options := []string{
		i18n.T("commands.render.option.display_mode"),
		i18n.T("commands.render.option.max_lines"),
		i18n.T("commands.render.option.back_tools"),
	}

	var items []string
	for i, option := range options {
		isSelected := i == c.selected
		if isSelected {
			items = append(items, lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render("▶ "+option))
		} else {
			items = append(items, lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render("  "+option))
		}
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands.render.tool_list_hint"))

	content := lipgloss.JoinVertical(lipgloss.Left, title, current, "", subtitle, "", list, "", hint)
	return c.wrapContainer(content, 70)
}

func (c *RenderCommand) renderModeSelect() string {
	config := c.settings.GetToolConfig(c.selectedTool)

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands.render.mode_title", c.selectedTool))

	current := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Render(i18n.T("commands.render.current_value", displayModeLabel(config.GetDisplayMode())))

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		MarginBottom(1).
		Render(i18n.T("commands.render.mode_subtitle"))

	var items []string
	for i, mode := range displayModes {
		isSelected := i == c.selected
		isCurrent := mode.ID == config.GetDisplayMode()

		var itemText string
		if isSelected {
			modeLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render(fmt.Sprintf("▶ %s", i18n.T(mode.Name)))

			descLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(fmt.Sprintf("  %s", i18n.T(mode.Description)))

			if isCurrent {
				descLine = lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorGreen)).
					Render(i18n.T("commands.render.current_description", i18n.T(mode.Description)))
			}

			itemText = lipgloss.JoinVertical(lipgloss.Left, modeLine, descLine, "")
		} else {
			prefix := "  "
			if isCurrent {
				prefix = "● "
			}
			itemText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render(fmt.Sprintf("%s%s", prefix, i18n.T(mode.Name)))
		}

		items = append(items, itemText)
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands.render.mode_hint"))

	content := lipgloss.JoinVertical(lipgloss.Left, title, current, "", subtitle, "", list, "", hint)
	return c.wrapContainer(content, 75)
}

func (c *RenderCommand) renderMaxLinesInput() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands.render.max_lines_title", c.selectedTool))

	prompt := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(i18n.T("commands.render.max_lines_prompt"))

	input := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Bold(true).
		Render(fmt.Sprintf("> %s_", c.maxLinesInput))

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands.render.max_lines_hint"))

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", prompt, "", input, "", hint)
	return c.wrapContainer(content, 60)
}

func (c *RenderCommand) renderColorPicker() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands.render.color_title"))

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		MarginBottom(1).
		Render(i18n.T("commands.render.color_subtitle"))

	var items []string
	for i, preset := range colorPresets {
		isSelected := i == c.selected

		var itemText string
		if isSelected {
			presetLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render(fmt.Sprintf("▶ %s", i18n.T(preset.Name)))

			// Color preview
			previewLine := i18n.T("commands.render.color_preview",
				c.colorPreview(preset.ToolCall),
				c.colorPreview(preset.ToolOutput),
				c.colorPreview(preset.ToolError))

			descLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(fmt.Sprintf("  %s", i18n.T(preset.Description)))

			itemText = lipgloss.JoinVertical(lipgloss.Left, presetLine, previewLine, descLine, "")
		} else {
			itemText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render(fmt.Sprintf("  %s", i18n.T(preset.Name)))
		}

		items = append(items, itemText)
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands.render.tool_list_hint"))

	content := lipgloss.JoinVertical(lipgloss.Left, title, subtitle, "", list, "", hint)
	return c.wrapContainer(content, 75)
}

func (c *RenderCommand) colorPreview(hexColor string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(hexColor)).
		Bold(true).
		Render(fmt.Sprintf("■ %s", hexColor))
}

func displayModeLabel(modeID string) string {
	for _, mode := range displayModes {
		if mode.ID == modeID {
			return i18n.T(mode.Name)
		}
	}
	return modeID
}

func yesNoLabel(value bool) string {
	if value {
		return i18n.T("commands.common.yes")
	}
	return i18n.T("commands.common.no")
}

func startupViewLabel(view string) string {
	switch view {
	case "advanced":
		return i18n.T("commands.render.startup.advanced")
	case "simple":
		return i18n.T("commands.render.startup.simple")
	default:
		return view
	}
}

func (c *RenderCommand) wrapContainer(content string, width int) string {
	if c.width > 0 && c.width < width+4 {
		width = c.width - 4
	}

	container := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorDarkGray)).
		Padding(2, 3)

	return container.Render(content)
}
