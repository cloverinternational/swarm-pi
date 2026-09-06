package commands

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// Nerd Font icons for command categories
const (
	iconModel    = "\uf1b2"     // nf-fa-cube - Model/AI related
	iconAuth     = "\uf084"     // nf-fa-key - Authentication
	iconSettings = "\uf013"     // nf-fa-gear - Settings/Config
	iconData     = "\uf1c0"     // nf-fa-database - Data/Storage
	iconTool     = "\uf0ad"     // nf-fa-wrench - Tools
	iconDebug    = "\uf188"     // nf-fa-bug - Debug/Profile
	iconCode     = "\ue796"     // nf-dev-code - Code/Development
	iconView     = "\uf06e"     // nf-fa-eye - View/Display
	iconCloud    = "\uf0c2"     // nf-fa-cloud - Cloud
	iconCommand  = "\uf120"     // nf-fa-terminal - Generic command
	iconMCP      = "\U000f0649" // nf-md-code_json - MCP
)

// MCPPromptProvider is the interface for getting MCP prompts
type MCPPromptProvider interface {
	// GetAllPrompts returns prompts from all connected servers.
	// Map key is server name.
	GetAllPrompts() map[string][]*mcp.MCPPrompt

	// GetPrompt executes a prompt with arguments.
	GetPrompt(ctx context.Context, serverName, promptName string, args map[string]string) (*mcp.PromptResult, error)

	// IsServerConnected checks if a server is connected.
	IsServerConnected(serverName string) bool
}

// MCPPromptMatch represents an MCP prompt in the autocomplete list
type MCPPromptMatch struct {
	ServerName string
	Prompt     *mcp.MCPPrompt
	FullName   string // server:promptName
}

// PluginCommandProvider is the interface for getting plugin commands
type PluginCommandProvider interface {
	// ListCommands returns all available plugin command names (namespace:command format)
	ListCommands() []string
	// GetEnabledPluginCommands returns all commands from enabled plugins
	GetEnabledPluginCommands() []PluginCommandMatch
}

// PluginCommandMatch represents a plugin command in the autocomplete list
type PluginCommandMatch struct {
	FullName     string // namespace:command (e.g., "feature-dev:feature-dev")
	PluginName   string // plugin name
	CommandName  string // command name within plugin
	Description  string // command description
	ArgumentHint string // hint text for arguments (e.g., "Optional feature description")
}

// ArgumentOption is one live completion candidate for a slash-command
// argument. Value is the exact text inserted after the command. Label and
// Description are display-only. Badges surface current/default/builtin state.
// Action marks a row that describes what a no-argument command opens.
type ArgumentOption struct {
	Value       string
	Label       string
	Description string
	Badges      []string
	Action      bool
}

// ArgumentOptionProvider supplies live slash-command argument options.
type ArgumentOptionProvider interface {
	CommandArgumentOptions(commandName string) []ArgumentOption
}

// AutocompleteTheme defines colors for the autocomplete UI
type AutocompleteTheme struct {
	Background   string
	Border       string
	Selected     string
	SelectedBg   string
	Text         string
	TextDim      string
	IconModel    string
	IconAuth     string
	IconSettings string
	IconData     string
	IconTool     string
	IconDebug    string
	IconView     string
}

// DefaultAutocompleteTheme provides the model picker inspired theme
var DefaultAutocompleteTheme = AutocompleteTheme{
	Background:   palette.Panel,
	Border:       palette.Border,
	Selected:     palette.Accent,
	SelectedBg:   palette.AccentDim,
	Text:         palette.Text,
	TextDim:      palette.TextDim,
	IconModel:    palette.AccentSoft, // AI/Model
	IconAuth:     palette.Warning,    // security
	IconSettings: palette.Info,       // settings
	IconData:     palette.Teal,       // data
	IconTool:     palette.Accent,     // tools
	IconDebug:    palette.Info,       // debug
	IconView:     palette.Success,    // view
}

// Autocomplete manages command autocomplete UI
type Autocomplete struct {
	registry    *Registry
	input       string
	matches     []Command
	selectedIdx int
	visible     bool
	width       int
	height      int
	theme       AutocompleteTheme

	// Subcommand autocomplete state
	subcommandMode    bool
	subcommands       []Subcommand
	subcommandMatches []Subcommand
	currentCommand    Command

	// Dynamic argument autocomplete state
	argumentProvider ArgumentOptionProvider
	argumentMode     bool
	argumentMatches  []ArgumentOption

	// MCP prompt support
	mcpProvider   MCPPromptProvider
	mcpMatches    []MCPPromptMatch
	selectedIsMCP bool // true if current selection is an MCP prompt

	// Plugin command support
	pluginProvider   PluginCommandProvider
	pluginMatches    []PluginCommandMatch
	selectedIsPlugin bool // true if current selection is a plugin command
}

// NewAutocomplete creates a new autocomplete component
func NewAutocomplete(registry *Registry) *Autocomplete {
	return &Autocomplete{
		registry:    registry,
		visible:     false,
		selectedIdx: 0,
		theme:       DefaultAutocompleteTheme,
		width:       80,
		height:      10,
	}
}

// SetMCPProvider sets the MCP prompt provider for autocomplete
func (a *Autocomplete) SetMCPProvider(provider MCPPromptProvider) {
	a.mcpProvider = provider
}

// SetPluginProvider sets the plugin command provider for autocomplete
func (a *Autocomplete) SetPluginProvider(provider PluginCommandProvider) {
	a.pluginProvider = provider
}

// SetArgumentOptionProvider injects the source of live config argument options.
func (a *Autocomplete) SetArgumentOptionProvider(provider ArgumentOptionProvider) {
	a.argumentProvider = provider
}

// SetTheme updates autocomplete colours to match the active app theme.
func (a *Autocomplete) SetTheme(bg, border, selected, selectedBg, text, textDim string) {
	a.theme.Background = bg
	a.theme.Border = border
	a.theme.Selected = selected
	a.theme.SelectedBg = selectedBg
	a.theme.Text = text
	a.theme.TextDim = textDim
}

// SetInput updates the input and refreshes matches
func (a *Autocomplete) SetInput(input string) {
	a.input = input
	a.mcpMatches = nil
	a.pluginMatches = nil
	a.selectedIsMCP = false
	a.selectedIsPlugin = false
	a.argumentMode = false
	a.argumentMatches = nil

	// Only show autocomplete if input starts with /
	if !strings.HasPrefix(input, "/") {
		a.visible = false
		a.matches = nil
		a.subcommandMode = false
		return
	}

	// Extract command part (without leading /)
	cmdPart := strings.TrimPrefix(input, "/")

	// Split to get command name and potential subcommand
	parts := strings.Fields(cmdPart)

	if len(parts) == 0 {
		// Show all commands, plugin commands, and MCP prompts
		a.matches = a.registry.Match("")
		a.pluginMatches = a.getPluginCommandMatches("")
		a.mcpMatches = a.getMCPPromptMatches("")
		a.visible = len(a.matches) > 0 || len(a.pluginMatches) > 0 || len(a.mcpMatches) > 0
		a.selectedIdx = 0
		a.subcommandMode = false
		a.updateSelectionType()
		return
	}

	cmdName := parts[0]

	// Check if we have an exact command match and user is typing a subcommand
	if len(parts) > 1 || (len(parts) == 1 && strings.HasSuffix(input, " ")) {
		cmd, found := a.registry.Get(cmdName)
		if found {
			// Check if command supports subcommands
			if provider, ok := cmd.(SubcommandProvider); ok {
				a.subcommandMode = true
				a.currentCommand = cmd
				a.subcommands = provider.Subcommands()

				// Get the subcommand prefix
				var subPrefix string
				if len(parts) > 1 {
					subPrefix = strings.ToLower(parts[1])
				}

				// Filter subcommands by prefix
				a.subcommandMatches = nil
				for _, sub := range a.subcommands {
					if subPrefix == "" || strings.HasPrefix(strings.ToLower(sub.Name), subPrefix) {
						a.subcommandMatches = append(a.subcommandMatches, sub)
					}
				}

				a.visible = len(a.subcommandMatches) > 0
				a.selectedIdx = 0
				return
			}

			// Commands without subcommands may expose live argument choices.
			// Use the canonical name after registry alias resolution.
			if a.argumentProvider != nil {
				options := a.argumentProvider.CommandArgumentOptions(cmd.Name())
				if len(options) > 0 {
					a.argumentMode = true
					a.currentCommand = cmd

					// Parse the complete remainder so values containing spaces
					// (notably system-prompt names) stay one logical argument.
					argPrefix := strings.ToLower(strings.TrimSpace(remainderAfterTokens(input, 1)))
					for _, option := range options {
						if argumentOptionMatches(option, argPrefix) {
							a.argumentMatches = append(a.argumentMatches, option)
						}
					}

					a.visible = len(a.argumentMatches) > 0
					a.selectedIdx = 0
					return
				}
			}
		}
	}

	// Regular command matching (include plugin commands and MCP prompts)
	a.subcommandMode = false
	a.matches = a.registry.Match(cmdName)
	a.pluginMatches = a.getPluginCommandMatches(cmdName)
	a.mcpMatches = a.getMCPPromptMatches(cmdName)
	a.visible = len(a.matches) > 0 || len(a.pluginMatches) > 0 || len(a.mcpMatches) > 0
	a.selectedIdx = 0
	a.updateSelectionType()
}

func argumentOptionMatches(option ArgumentOption, prefix string) bool {
	if prefix == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		option.Value,
		option.Label,
		option.Description,
		strings.Join(option.Badges, " "),
	}, " "))
	return strings.Contains(haystack, prefix)
}

// getMCPPromptMatches returns MCP prompts matching the given prefix
func (a *Autocomplete) getMCPPromptMatches(prefix string) []MCPPromptMatch {
	if a.mcpProvider == nil {
		return nil
	}

	allPrompts := a.mcpProvider.GetAllPrompts()
	if len(allPrompts) == 0 {
		return nil
	}

	prefixLower := strings.ToLower(prefix)
	var matches []MCPPromptMatch

	for serverName, prompts := range allPrompts {
		if !a.mcpProvider.IsServerConnected(serverName) {
			continue
		}

		for _, prompt := range prompts {
			fullName := fmt.Sprintf("%s:%s", serverName, prompt.Name)
			nameLower := strings.ToLower(fullName)
			promptNameLower := strings.ToLower(prompt.Name)

			// Match against full name (server:prompt) or just prompt name
			if prefix == "" ||
				strings.HasPrefix(nameLower, prefixLower) ||
				strings.Contains(nameLower, prefixLower) ||
				strings.HasPrefix(promptNameLower, prefixLower) ||
				strings.Contains(promptNameLower, prefixLower) {

				matches = append(matches, MCPPromptMatch{
					ServerName: serverName,
					Prompt:     prompt,
					FullName:   fullName,
				})

				// Limit matches
				if len(matches) >= 10 {
					return matches
				}
			}
		}
	}

	return matches
}

// getPluginCommandMatches returns plugin commands matching the given prefix
func (a *Autocomplete) getPluginCommandMatches(prefix string) []PluginCommandMatch {
	if a.pluginProvider == nil {
		return nil
	}

	allCmds := a.pluginProvider.GetEnabledPluginCommands()
	if len(allCmds) == 0 {
		return nil
	}

	prefixLower := strings.ToLower(prefix)
	var matches []PluginCommandMatch

	for _, cmd := range allCmds {
		fullNameLower := strings.ToLower(cmd.FullName)
		cmdNameLower := strings.ToLower(cmd.CommandName)

		// Match against full name (namespace:command) or just command name
		if prefix == "" ||
			strings.HasPrefix(fullNameLower, prefixLower) ||
			strings.Contains(fullNameLower, prefixLower) ||
			strings.HasPrefix(cmdNameLower, prefixLower) ||
			strings.Contains(cmdNameLower, prefixLower) {

			matches = append(matches, cmd)

			// Limit matches
			if len(matches) >= 10 {
				return matches
			}
		}
	}

	return matches
}

// updateSelectionType updates the selectedIsMCP and selectedIsPlugin flags
func (a *Autocomplete) updateSelectionType() {
	totalBuiltin := len(a.matches)
	totalPlugin := len(a.pluginMatches)

	a.selectedIsPlugin = a.selectedIdx >= totalBuiltin && a.selectedIdx < totalBuiltin+totalPlugin
	a.selectedIsMCP = a.selectedIdx >= totalBuiltin+totalPlugin && a.selectedIdx < totalBuiltin+totalPlugin+len(a.mcpMatches)
}

// totalMatches returns the total number of matches (builtin + plugin + MCP)
func (a *Autocomplete) totalMatches() int {
	return len(a.matches) + len(a.pluginMatches) + len(a.mcpMatches)
}

// SetSize sets the autocomplete dimensions
func (a *Autocomplete) SetSize(width, height int) {
	a.width = width
	a.height = height
	if a.height > 12 {
		a.height = 12
	}
}

// IsVisible returns whether autocomplete is shown
func (a *Autocomplete) IsVisible() bool {
	return a.visible
}

// Hide hides the autocomplete
func (a *Autocomplete) Hide() {
	a.visible = false
}

// SelectedCommand returns the currently selected command name
func (a *Autocomplete) SelectedCommand() string {
	if !a.visible {
		return ""
	}

	if a.argumentMode {
		if a.selectedIdx < 0 || a.selectedIdx >= len(a.argumentMatches) {
			return ""
		}
		option := a.argumentMatches[a.selectedIdx]
		base := "/" + a.currentCommand.Name()
		if option.Action || strings.TrimSpace(option.Value) == "" {
			return base
		}
		return base + " " + option.Value
	}

	if a.subcommandMode {
		if a.selectedIdx >= len(a.subcommandMatches) {
			return ""
		}
		// Return the full command with subcommand
		return "/" + a.currentCommand.Name() + " " + a.subcommandMatches[a.selectedIdx].Name + " "
	}

	// Check if plugin command is selected
	if a.selectedIsPlugin {
		pluginIdx := a.selectedIdx - len(a.matches)
		if pluginIdx >= 0 && pluginIdx < len(a.pluginMatches) {
			return "/" + a.pluginMatches[pluginIdx].FullName + " "
		}
		return ""
	}

	// Check if MCP prompt is selected
	if a.selectedIsMCP {
		mcpIdx := a.selectedIdx - len(a.matches) - len(a.pluginMatches)
		if mcpIdx >= 0 && mcpIdx < len(a.mcpMatches) {
			return "/" + a.mcpMatches[mcpIdx].FullName + " "
		}
		return ""
	}

	if a.selectedIdx >= len(a.matches) {
		return ""
	}
	return "/" + a.matches[a.selectedIdx].Name() + " "
}

// CompleteSelection returns the input with the command portion replaced by the
// currently selected completion, PRESERVING any text the user typed after the
// command. This avoids wiping a message like "/foo my long text" down to just
// "/foo " when the user presses Tab to complete the command.
func (a *Autocomplete) CompleteSelection() string {
	completed := a.SelectedCommand()
	if completed == "" {
		return a.input
	}
	if a.argumentMode {
		// The partially typed argument is the filter query. Replace that entire
		// span with the selected live option instead of appending it.
		return completed
	}

	// The completion consumes the leading command token(s): one token for a
	// regular command ("/cmd "), two for a subcommand ("/cmd sub ").
	tokensConsumed := 1
	if a.subcommandMode {
		tokensConsumed = 2
	}

	rest := remainderAfterTokens(a.input, tokensConsumed)
	if rest == "" {
		// completed already ends with a trailing space.
		return completed
	}
	// completed ends with a single space, so appending the remainder directly
	// yields e.g. "/foo " + "my long text" = "/foo my long text".
	return completed + rest
}

// remainderAfterTokens skips the leading "/" and the first n whitespace-separated
// tokens of input, then returns the rest of the string with the separating
// whitespace trimmed but the remainder's internal spacing preserved.
func remainderAfterTokens(input string, n int) string {
	s := strings.TrimPrefix(input, "/")
	i := 0
	for t := 0; t < n; t++ {
		for i < len(s) && s[i] == ' ' { // skip leading whitespace
			i++
		}
		for i < len(s) && s[i] != ' ' { // skip the token itself
			i++
		}
	}
	for i < len(s) && s[i] == ' ' { // skip whitespace before the remainder
		i++
	}
	return s[i:]
}

// GetSelectedCommand returns the currently selected command object
func (a *Autocomplete) GetSelectedCommand() (Command, bool) {
	if !a.visible || a.selectedIsMCP || a.selectedIsPlugin || a.selectedIdx >= len(a.matches) {
		return nil, false
	}
	return a.matches[a.selectedIdx], true
}

// GetSelectedPluginCommand returns the currently selected plugin command
func (a *Autocomplete) GetSelectedPluginCommand() (*PluginCommandMatch, bool) {
	if !a.visible || !a.selectedIsPlugin {
		return nil, false
	}
	pluginIdx := a.selectedIdx - len(a.matches)
	if pluginIdx >= 0 && pluginIdx < len(a.pluginMatches) {
		return &a.pluginMatches[pluginIdx], true
	}
	return nil, false
}

// IsPluginCommandSelected returns true if the current selection is a plugin command
func (a *Autocomplete) IsPluginCommandSelected() bool {
	return a.selectedIsPlugin
}

// GetSelectedMCPPrompt returns the currently selected MCP prompt
func (a *Autocomplete) GetSelectedMCPPrompt() (*MCPPromptMatch, bool) {
	if !a.visible || !a.selectedIsMCP {
		return nil, false
	}
	mcpIdx := a.selectedIdx - len(a.matches) - len(a.pluginMatches)
	if mcpIdx >= 0 && mcpIdx < len(a.mcpMatches) {
		return &a.mcpMatches[mcpIdx], true
	}
	return nil, false
}

// IsMCPPromptSelected returns true if the current selection is an MCP prompt
func (a *Autocomplete) IsMCPPromptSelected() bool {
	return a.selectedIsMCP
}

// Update handles keyboard navigation
func (a *Autocomplete) Update(msg tea.Msg) tea.Cmd {
	if !a.visible {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "down", "ctrl+n":
			var maxIdx int
			if a.argumentMode {
				maxIdx = len(a.argumentMatches) - 1
			} else if a.subcommandMode {
				maxIdx = len(a.subcommandMatches) - 1
			} else {
				// Include builtin commands, plugin commands, and MCP prompts
				maxIdx = a.totalMatches() - 1
			}
			if a.selectedIdx < maxIdx {
				a.selectedIdx++
				a.updateSelectionType()
			}
		case "up", "ctrl+p":
			if a.selectedIdx > 0 {
				a.selectedIdx--
				a.updateSelectionType()
			}
		}
	}

	return nil
}

// getCommandIcon returns the appropriate icon and color for a command
func (a *Autocomplete) getCommandIcon(cmd Command) (string, string) {
	th := a.theme
	name := cmd.Name()

	switch name {
	case "model":
		return iconModel, th.IconModel
	case "auth":
		return iconAuth, th.IconAuth
	case "render":
		return iconView, th.IconView
	case "pprof":
		return iconDebug, th.IconDebug
	case "profile", "agents", "prompt":
		return iconModel, th.IconModel
	case "providers", "compaction":
		return iconSettings, th.IconSettings
	case "mcp":
		return iconTool, th.IconTool
	case "cloud":
		return iconCloud, th.TextDim
	case "hooks":
		return iconCode, th.IconTool
	case "thinking":
		return iconView, th.IconView
	case "compact":
		return iconData, th.IconData
	case "mode":
		return iconSettings, th.IconSettings
	case "skill":
		return iconCode, th.IconTool
	case "bug", "report", "feedback":
		return iconDebug, th.IconDebug
	default:
		return iconCommand, th.TextDim
	}
}

// truncateText truncates a string to fit within maxWidth visual columns,
// appending "…" if truncation occurs.
func truncateText(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	runes := []rune(s)
	result := ""
	for _, r := range runes {
		candidate := result + string(r)
		if lipgloss.Width(candidate+"…") > maxWidth {
			break
		}
		result = candidate
	}
	if result == "" {
		return "…"
	}
	return result + "…"
}

// clampLine hard-truncates a styled line to exactly maxWidth visual columns.
// If bg is non-empty, padding uses that background color; otherwise plain spaces.
func clampLine(content string, maxWidth int, bg string) string {
	w := ansi.StringWidth(content)
	if w > maxWidth {
		content = ansi.Truncate(content, maxWidth, "")
	} else if w < maxWidth {
		pad := strings.Repeat(" ", max(0, maxWidth-w))
		if bg != "" {
			pad = lipgloss.NewStyle().Background(lipgloss.Color(bg)).Render(pad)
		}
		content += pad
	}
	return content
}

// View renders the autocomplete dropdown
func (a *Autocomplete) View() string {
	if !a.visible {
		return ""
	}

	if a.argumentMode {
		return a.viewArguments()
	}

	if a.subcommandMode {
		return a.viewSubcommands()
	}

	totalCount := a.totalMatches()
	if totalCount == 0 {
		return ""
	}

	th := a.theme
	maxWidth := max(a.width-2, 40)

	var lines []string

	// Separator
	sepLine := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render(strings.Repeat("─", maxWidth))
	lines = append(lines, sepLine)

	// Visible window
	maxVisible := 8
	startIdx := 0
	endIdx := totalCount
	if totalCount > maxVisible {
		startIdx = max(a.selectedIdx-maxVisible/2, 0)
		endIdx = startIdx + maxVisible
		if endIdx > totalCount {
			endIdx = totalCount
			startIdx = max(endIdx-maxVisible, 0)
		}
	}

	// Items
	builtinCount := len(a.matches)
	pluginCount := len(a.pluginMatches)
	for i := startIdx; i < endIdx; i++ {
		isSelected := i == a.selectedIdx
		var line string
		if i < builtinCount {
			line = a.renderCommandLine(a.matches[i], isSelected, maxWidth)
		} else if i < builtinCount+pluginCount {
			line = a.renderPluginCommandLine(a.pluginMatches[i-builtinCount], isSelected, maxWidth)
		} else {
			line = a.renderMCPPromptLine(a.mcpMatches[i-builtinCount-pluginCount], isSelected, maxWidth)
		}
		bg := ""
		if isSelected {
			bg = th.SelectedBg
		}
		lines = append(lines, clampLine(line, maxWidth, bg))
	}

	return strings.Join(lines, "\n")
}

// viewArguments renders live slash-command argument choices.
func (a *Autocomplete) viewArguments() string {
	if len(a.argumentMatches) == 0 {
		return ""
	}

	th := a.theme
	// Respect the actual available width. Other autocomplete modes retain their
	// historical minimum; argument rows must not overflow narrow terminals.
	maxWidth := max(a.width-2, 1)
	lines := []string{
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Border)).
			Render(strings.Repeat("─", maxWidth)),
	}

	maxVisible := 8
	startIdx := 0
	endIdx := len(a.argumentMatches)
	if endIdx > maxVisible {
		startIdx = max(a.selectedIdx-maxVisible/2, 0)
		endIdx = startIdx + maxVisible
		if endIdx > len(a.argumentMatches) {
			endIdx = len(a.argumentMatches)
			startIdx = max(endIdx-maxVisible, 0)
		}
	}

	for i := startIdx; i < endIdx; i++ {
		selected := i == a.selectedIdx
		line := a.renderArgumentOptionLine(a.argumentMatches[i], selected, maxWidth)
		bg := ""
		if selected {
			bg = th.SelectedBg
		}
		lines = append(lines, clampLine(line, maxWidth, bg))
	}

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextDim)).
		Render(i18n.T("commands.autocomplete.argument_hint"))
	lines = append(lines, clampLine(hint, maxWidth, ""))
	return strings.Join(lines, "\n")
}

func (a *Autocomplete) renderArgumentOptionLine(option ArgumentOption, selected bool, maxWidth int) string {
	th := a.theme
	withBg := func(style lipgloss.Style) lipgloss.Style {
		if selected {
			return style.Background(lipgloss.Color(th.SelectedBg))
		}
		return style
	}

	var line strings.Builder
	if selected {
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" › "))
	} else {
		line.WriteString("   ")
	}

	icon, iconColor := a.getCommandIcon(a.currentCommand)
	if option.Action {
		icon = "↵"
		iconColor = th.IconSettings
	}
	line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(iconColor))).Render(icon))
	line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))

	label := option.Label
	if label == "" {
		label = option.Value
	}
	labelStyle := withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)))
	if selected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Selected)).Bold(true)
	}
	line.WriteString(labelStyle.Render(label))

	if option.Value != "" && option.Value != label {
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))
		line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim))).Render(option.Value))
	}
	for _, badge := range option.Badges {
		if strings.TrimSpace(badge) == "" {
			continue
		}
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))
		line.WriteString(withBg(lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Selected))).
			Render("[" + badge + "]"))
	}

	if option.Description != "" {
		currentLen := lipgloss.Width(line.String())
		spacing := max(28-currentLen, 2)
		descAvail := max(0, maxWidth-currentLen-spacing)
		if descAvail > 3 {
			line.WriteString(withBg(lipgloss.NewStyle()).Render(strings.Repeat(" ", spacing)))
			line.WriteString(withBg(lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextDim)).
				Italic(true)).
				Render(truncateText(option.Description, descAvail)))
		}
	}
	return line.String()
}

// renderCommandLine renders a single builtin command line
func (a *Autocomplete) renderCommandLine(cmd Command, isSelected bool, maxWidth int) string {
	th := a.theme
	icon, iconColor := a.getCommandIcon(cmd)

	// Only apply background on selected items; non-selected are transparent
	withBg := func(s lipgloss.Style) lipgloss.Style {
		if isSelected {
			return s.Background(lipgloss.Color(th.SelectedBg))
		}
		return s
	}

	var line strings.Builder

	if isSelected {
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" › "))
	} else {
		line.WriteString("   ")
	}

	line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(iconColor))).Render(icon))
	line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))

	nameStyle := withBg(lipgloss.NewStyle())
	if isSelected {
		nameStyle = nameStyle.Foreground(lipgloss.Color(th.Selected)).Bold(true)
	} else {
		nameStyle = nameStyle.Foreground(lipgloss.Color(th.Text))
	}
	line.WriteString(nameStyle.Render("/" + cmd.Name()))

	desc := cmd.Description()
	if desc != "" {
		currentLen := lipgloss.Width(line.String())
		spacing := max(22-currentLen, 2)
		line.WriteString(withBg(lipgloss.NewStyle()).Render(strings.Repeat(" ", spacing)))

		descAvail := max(0, maxWidth-currentLen-spacing)
		if descAvail > 3 {
			desc = truncateText(desc, descAvail)
			line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Italic(true)).Render(desc))
		}
	}

	return line.String()
}

// renderPluginCommandLine renders a single plugin command line
func (a *Autocomplete) renderPluginCommandLine(cmd PluginCommandMatch, isSelected bool, maxWidth int) string {
	th := a.theme

	withBg := func(s lipgloss.Style) lipgloss.Style {
		if isSelected {
			return s.Background(lipgloss.Color(th.SelectedBg))
		}
		return s
	}

	var line strings.Builder

	if isSelected {
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" › "))
	} else {
		line.WriteString("   ")
	}

	line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))).Render(iconCode))
	line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))

	nameStyle := withBg(lipgloss.NewStyle())
	if isSelected {
		nameStyle = nameStyle.Foreground(lipgloss.Color(th.Selected)).Bold(true)
	} else {
		nameStyle = nameStyle.Foreground(lipgloss.Color(th.Text))
	}
	line.WriteString(nameStyle.Render("/" + cmd.FullName))

	// Argument hint as ghost text (e.g., "prompt")
	if cmd.ArgumentHint != "" {
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))
		line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim))).Render(cmd.ArgumentHint))
	}

	if cmd.Description != "" {
		currentLen := lipgloss.Width(line.String())
		spacing := max(28-currentLen, 2)
		line.WriteString(withBg(lipgloss.NewStyle()).Render(strings.Repeat(" ", spacing)))

		descAvail := max(0, maxWidth-currentLen-spacing)
		if descAvail > 3 {
			desc := truncateText(cmd.Description, descAvail)
			line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Italic(true)).Render(desc))
		}
	}

	return line.String()
}

// renderMCPPromptLine renders a single MCP prompt line
func (a *Autocomplete) renderMCPPromptLine(prompt MCPPromptMatch, isSelected bool, maxWidth int) string {
	th := a.theme

	withBg := func(s lipgloss.Style) lipgloss.Style {
		if isSelected {
			return s.Background(lipgloss.Color(th.SelectedBg))
		}
		return s
	}

	var line strings.Builder

	if isSelected {
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" › "))
	} else {
		line.WriteString("   ")
	}

	line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))).Render(iconMCP))
	line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))

	nameStyle := withBg(lipgloss.NewStyle())
	if isSelected {
		nameStyle = nameStyle.Foreground(lipgloss.Color(th.Selected)).Bold(true)
	} else {
		nameStyle = nameStyle.Foreground(lipgloss.Color(th.Text))
	}
	line.WriteString(nameStyle.Render("/" + prompt.FullName))

	// Argument hints from MCP prompt arguments
	if len(prompt.Prompt.Arguments) > 0 {
		var argHints []string
		for _, arg := range prompt.Prompt.Arguments {
			if arg.Required {
				argHints = append(argHints, "<"+arg.Name+">")
			} else {
				argHints = append(argHints, "["+arg.Name+"]")
			}
		}
		hint := strings.Join(argHints, " ")
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))
		line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim))).Render(hint))
	}

	// Description
	desc := prompt.Prompt.Description
	if desc != "" {
		currentLen := lipgloss.Width(line.String())
		spacing := max(28-currentLen, 2)
		line.WriteString(withBg(lipgloss.NewStyle()).Render(strings.Repeat(" ", spacing)))

		descAvail := max(0, maxWidth-currentLen-spacing)
		if descAvail > 3 {
			desc = truncateText(desc, descAvail)
			line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Italic(true)).Render(desc))
		}
	}

	return line.String()
}

// viewSubcommands renders the subcommand autocomplete dropdown
func (a *Autocomplete) viewSubcommands() string {
	if len(a.subcommandMatches) == 0 {
		return ""
	}

	th := a.theme
	maxWidth := max(a.width-2, 40)

	var lines []string

	// Separator
	sepLine := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render(strings.Repeat("─", maxWidth))
	lines = append(lines, sepLine)

	// Visible window
	maxVisible := 8
	startIdx := 0
	endIdx := len(a.subcommandMatches)
	if len(a.subcommandMatches) > maxVisible {
		startIdx = max(a.selectedIdx-maxVisible/2, 0)
		endIdx = startIdx + maxVisible
		if endIdx > len(a.subcommandMatches) {
			endIdx = len(a.subcommandMatches)
			startIdx = max(endIdx-maxVisible, 0)
		}
	}

	// Items
	for i := startIdx; i < endIdx; i++ {
		sub := a.subcommandMatches[i]
		isSelected := i == a.selectedIdx

		withBg := func(s lipgloss.Style) lipgloss.Style {
			if isSelected {
				return s.Background(lipgloss.Color(th.SelectedBg))
			}
			return s
		}

		var line strings.Builder
		if isSelected {
			line.WriteString(withBg(lipgloss.NewStyle()).Render(" › "))
		} else {
			line.WriteString("   ")
		}

		icon, iconColor := a.getCommandIcon(a.currentCommand)
		line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(iconColor))).Render(icon))
		line.WriteString(withBg(lipgloss.NewStyle()).Render(" "))

		nameStyle := withBg(lipgloss.NewStyle())
		if isSelected {
			nameStyle = nameStyle.Foreground(lipgloss.Color(th.Selected)).Bold(true)
		} else {
			nameStyle = nameStyle.Foreground(lipgloss.Color(th.Text))
		}
		line.WriteString(nameStyle.Render(sub.Name))

		if sub.Description != "" {
			currentLen := lipgloss.Width(line.String())
			spacing := max(18-currentLen, 2)
			line.WriteString(withBg(lipgloss.NewStyle()).Render(strings.Repeat(" ", spacing)))
			descAvail := max(0, maxWidth-currentLen-spacing)
			if descAvail > 3 {
				desc := truncateText(sub.Description, descAvail)
				line.WriteString(withBg(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Italic(true)).Render(desc))
			}
		}

		bg := ""
		if isSelected {
			bg = th.SelectedBg
		}
		lines = append(lines, clampLine(line.String(), maxWidth, bg))
	}

	return strings.Join(lines, "\n")
}
