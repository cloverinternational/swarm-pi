package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ToolSource indicates where a tool comes from
type ToolSource string

const (
	ToolSourceBuiltin ToolSource = "builtin" // Core builtin tools (bash, read, write, etc.)
	ToolSourceII      ToolSource = "ii"      // II agent tools (todo, shell, browser, dev)
	ToolSourceMCP     ToolSource = "mcp"     // MCP server tools
)

// ToolEntry represents a tool with full metadata
type ToolEntry struct {
	Tool        tools.Tool
	Name        string
	Source      ToolSource
	Package     string // e.g., "sdk/tools/builtin", "sdk/tools/ii"
	ServerName  string // For MCP tools
	Category    string // e.g., "file", "shell", "productivity"
	Description string
	ParamCount  int
}

// ToolTester provides an interactive UI for testing tools
type ToolTester struct {
	entries       []ToolEntry
	selectedTool  int
	scrollOffset  int
	state         string // "list", "params", "result"
	paramFields   []ParamField
	selectedField int
	lastResult    string
	lastError     string
	onExecute     func(ctx context.Context, tool tools.Tool, params map[string]any) (string, error)
}

// ParamField represents a parameter input field
type ParamField struct {
	Name        string
	Type        string
	Description string
	Value       string
	Required    bool
}

// NewToolTester creates a new tool tester
func NewToolTester() *ToolTester {
	return &ToolTester{
		entries:      []ToolEntry{},
		selectedTool: 0,
		state:        "list",
		paramFields:  []ParamField{},
	}
}

// SetTools sets the available tools (legacy - use SetToolEntries for full metadata)
func (t *ToolTester) SetTools(toolList []tools.Tool) {
	// Convert to entries with basic metadata
	t.entries = make([]ToolEntry, 0, len(toolList))
	for _, tool := range toolList {
		entry := ToolEntry{
			Tool:        tool,
			Name:        tool.Name(),
			Source:      ToolSourceBuiltin,
			Package:     "sdk/tools/builtin",
			Description: tool.Description(),
			ParamCount:  countParams(tool),
		}
		t.entries = append(t.entries, entry)
	}
	t.sortEntries()
}

// SetToolEntries sets the tool entries with full metadata
func (t *ToolTester) SetToolEntries(entries []ToolEntry) {
	t.entries = entries
	t.sortEntries()
}

// sortEntries sorts tools by source then name
func (t *ToolTester) sortEntries() {
	sort.Slice(t.entries, func(i, j int) bool {
		// Sort by source priority: builtin < ii < mcp
		sourcePriority := map[ToolSource]int{
			ToolSourceBuiltin: 0,
			ToolSourceII:      1,
			ToolSourceMCP:     2,
		}
		if sourcePriority[t.entries[i].Source] != sourcePriority[t.entries[j].Source] {
			return sourcePriority[t.entries[i].Source] < sourcePriority[t.entries[j].Source]
		}
		return t.entries[i].Name < t.entries[j].Name
	})
}

// countParams counts the number of parameters a tool has
func countParams(tool tools.Tool) int {
	schema := tool.Parameters()
	if schema == nil {
		return 0
	}
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return 0
	}
	props, ok := schemaMap["properties"].(map[string]any)
	if !ok {
		return 0
	}
	return len(props)
}

// SetOnExecute sets the execution callback
func (t *ToolTester) SetOnExecute(fn func(ctx context.Context, tool tools.Tool, params map[string]any) (string, error)) {
	t.onExecute = fn
}

// buildParamFields extracts parameters from tool schema
func (t *ToolTester) buildParamFields(tool tools.Tool) []ParamField {
	var fields []ParamField

	schema := tool.Parameters()
	if schema == nil {
		return fields
	}

	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return fields
	}

	props, ok := schemaMap["properties"].(map[string]any)
	if !ok {
		return fields
	}

	required := make(map[string]bool)
	if reqList, ok := schemaMap["required"].([]any); ok {
		for _, r := range reqList {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}

	for name, propRaw := range props {
		prop, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}

		field := ParamField{
			Name:     name,
			Required: required[name],
		}

		if typeName, ok := prop["type"].(string); ok {
			field.Type = typeName
		}
		if desc, ok := prop["description"].(string); ok {
			field.Description = desc
		}

		fields = append(fields, field)
	}

	// Sort fields: required first, then alphabetically
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Required != fields[j].Required {
			return fields[i].Required
		}
		return fields[i].Name < fields[j].Name
	})

	return fields
}

// Render renders the tool tester UI
func (t *ToolTester) Render(width, height int, th Theme) string {
	var rendered string
	switch t.state {
	case "params":
		rendered = t.renderParamsScreen(width, height, th)
	case "result":
		rendered = t.renderResultScreen(width, height, th)
	default:
		rendered = t.renderListScreen(width, height, th)
	}
	return i18n.SettingsIntegrationsText(rendered)
}

// getSourceBadge returns a styled badge for the tool source
func (t *ToolTester) getSourceBadge(source ToolSource, serverName string, th Theme) string {
	var text, bg string
	switch source {
	case ToolSourceBuiltin:
		text = "BUILTIN"
		bg = th.Warning
	case ToolSourceII:
		text = "II"
		bg = th.Success
	case ToolSourceMCP:
		if serverName != "" {
			text = "MCP:" + serverName
		} else {
			text = "MCP"
		}
		bg = th.Primary
	default:
		text = "?"
		bg = th.TextMuted
	}

	style := lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	return style.Render(text)
}

// renderListScreen shows the tool selection list with details panel
func (t *ToolTester) renderListScreen(width, height int, th Theme) string {
	if len(t.entries) == 0 {
		return t.renderEmptyState(width, height, th)
	}

	// Two-panel layout: list (60%) + details (40%)
	listWidth := (width * 55) / 100
	detailsWidth := width - listWidth - 3 // -3 for separator

	// Render both panels
	listPanel := t.renderToolList(listWidth, height-4, th)
	detailsPanel := t.renderToolDetails(detailsWidth, height-4, th)

	// Separator
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("│\n", height-6))

	// Combine panels
	combined := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, separator, detailsPanel)

	// Hint bar at bottom
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width - 4).
		Align(lipgloss.Center)
	hint := hintStyle.Render("↑/↓ navigate • Enter test tool • Esc back")

	content := lipgloss.JoinVertical(lipgloss.Left, combined, "", hint)

	// Container
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Width(width-2).
		Height(height-2).
		Padding(1, 1)

	return containerStyle.Render(content)
}

// renderEmptyState renders when no tools are available
func (t *ToolTester) renderEmptyState(width, height int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width - 4).
		Align(lipgloss.Center)

	msgStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width - 4).
		Align(lipgloss.Center)

	content := lipgloss.JoinVertical(lipgloss.Center,
		"",
		titleStyle.Render("Tool Tester"),
		"",
		msgStyle.Render("No tools available"),
		msgStyle.Render("Tools will appear once SDK is initialized"),
	)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Width(width-2).
		Height(height-2).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderToolList renders the left panel with tool list
func (t *ToolTester) renderToolList(width, height int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width - 2)

	title := titleStyle.Render(fmt.Sprintf("Tools (%d)", len(t.entries)))

	// Calculate visible area
	visibleHeight := height - 4
	if visibleHeight < 3 {
		visibleHeight = 3
	}

	// Adjust scroll
	if t.scrollOffset > t.selectedTool {
		t.scrollOffset = t.selectedTool
	}
	if t.selectedTool >= t.scrollOffset+visibleHeight {
		t.scrollOffset = t.selectedTool - visibleHeight + 1
	}

	var lines []string
	lines = append(lines, title, "")

	// Track current source for group headers
	var lastSource ToolSource = ""

	startIdx := t.scrollOffset
	endIdx := startIdx + visibleHeight
	if endIdx > len(t.entries) {
		endIdx = len(t.entries)
	}

	for i := startIdx; i < endIdx; i++ {
		entry := t.entries[i]
		isSelected := i == t.selectedTool

		// Add source group header if source changed
		if entry.Source != lastSource {
			headerStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Bold(true).
				Width(width - 2)

			var headerText string
			switch entry.Source {
			case ToolSourceBuiltin:
				headerText = "── Builtin ──"
			case ToolSourceII:
				headerText = "── II Agent ──"
			case ToolSourceMCP:
				headerText = "── MCP ──"
			}
			if headerText != "" && i == startIdx {
				lines = append(lines, headerStyle.Render(headerText))
			}
			lastSource = entry.Source
		}

		// Build tool line
		prefix := "  "
		if isSelected {
			prefix = "▶ "
		}

		// Tool name with param count
		paramInfo := ""
		if entry.ParamCount > 0 {
			paramInfo = fmt.Sprintf(" (%d)", entry.ParamCount)
		}

		lineText := fmt.Sprintf("%s%s%s", prefix, entry.Name, paramInfo)

		var lineStyle lipgloss.Style
		if isSelected {
			lineStyle = lipgloss.NewStyle().
				Background(lipgloss.Color(th.Primary)).
				Foreground(lipgloss.Color(th.Text)).
				Bold(true).
				Width(width - 2)
		} else {
			// Color based on source
			var fg string
			switch entry.Source {
			case ToolSourceBuiltin:
				fg = th.Warning
			case ToolSourceII:
				fg = th.Success
			case ToolSourceMCP:
				fg = th.Primary
			default:
				fg = th.Text
			}
			lineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(fg)).
				Width(width - 2)
		}

		lines = append(lines, lineStyle.Render(lineText))
	}

	// Scroll indicator
	if len(t.entries) > visibleHeight {
		scrollInfo := fmt.Sprintf("(%d-%d of %d)", startIdx+1, endIdx, len(t.entries))
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(width - 2).
			Align(lipgloss.Center)
		lines = append(lines, "", scrollStyle.Render(scrollInfo))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderToolDetails renders the right panel with selected tool details
func (t *ToolTester) renderToolDetails(width, height int, th Theme) string {
	if t.selectedTool >= len(t.entries) {
		return ""
	}

	entry := t.entries[t.selectedTool]

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width - 2)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width - 2)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Width(width - 2)

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Italic(true).
		Width(width - 2)

	var lines []string

	// Tool name
	lines = append(lines, titleStyle.Render(entry.Name))
	lines = append(lines, "")

	// Source badge
	badge := t.getSourceBadge(entry.Source, entry.ServerName, th)
	lines = append(lines, badge)
	lines = append(lines, "")

	// Package/Location
	if entry.Package != "" {
		lines = append(lines, labelStyle.Render("Package:"))
		lines = append(lines, valueStyle.Render("  "+entry.Package))
		lines = append(lines, "")
	}

	// Server (for MCP)
	if entry.Source == ToolSourceMCP && entry.ServerName != "" {
		lines = append(lines, labelStyle.Render("Server:"))
		lines = append(lines, valueStyle.Render("  "+entry.ServerName))
		lines = append(lines, "")
	}

	// Category
	if entry.Category != "" {
		lines = append(lines, labelStyle.Render("Category:"))
		lines = append(lines, valueStyle.Render("  "+entry.Category))
		lines = append(lines, "")
	}

	// Description
	if entry.Description != "" {
		lines = append(lines, labelStyle.Render("Description:"))
		// Word wrap description
		desc := entry.Description
		if len(desc) > 200 {
			desc = desc[:197] + "..."
		}
		// Simple word wrap
		wrapped := wrapText(desc, width-4)
		for _, line := range wrapped {
			lines = append(lines, descStyle.Render("  "+line))
		}
		lines = append(lines, "")
	}

	// Parameters
	paramCount := entry.ParamCount
	lines = append(lines, labelStyle.Render(fmt.Sprintf("Parameters: %d", paramCount)))

	if entry.Tool != nil && paramCount > 0 {
		params := t.buildParamFields(entry.Tool)
		for _, p := range params {
			reqMark := ""
			if p.Required {
				reqMark = "*"
			}
			paramLine := fmt.Sprintf("  • %s%s (%s)", p.Name, reqMark, p.Type)
			lines = append(lines, valueStyle.Render(paramLine))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// wrapText wraps text to specified width using visual width
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return lines
	}

	currentLine := words[0]
	currentWidth := lipgloss.Width(currentLine)
	for _, word := range words[1:] {
		wordWidth := lipgloss.Width(word)
		if currentWidth+1+wordWidth <= width {
			currentLine += " " + word
			currentWidth += 1 + wordWidth
		} else {
			lines = append(lines, currentLine)
			currentLine = word
			currentWidth = wordWidth
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

// renderParamsScreen shows parameter input form
func (t *ToolTester) renderParamsScreen(width, height int, th Theme) string {
	if t.selectedTool >= len(t.entries) {
		return ""
	}
	entry := t.entries[t.selectedTool]

	// Title with badge
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary))

	badge := t.getSourceBadge(entry.Source, entry.ServerName, th)
	title := lipgloss.JoinHorizontal(lipgloss.Center,
		titleStyle.Render("Test: "+entry.Name+" "),
		badge,
	)

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")

	// Description (truncated)
	if entry.Description != "" {
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(width - 8)
		desc := entry.Description
		if len(desc) > 100 {
			desc = desc[:97] + "..."
		}
		lines = append(lines, descStyle.Render(desc))
		lines = append(lines, "")
	}

	// Separator
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border))
	lines = append(lines, sepStyle.Render(strings.Repeat("─", width-8)))
	lines = append(lines, "")

	// Parameters
	if len(t.paramFields) == 0 {
		noParamsStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Width(width - 8)
		lines = append(lines, noParamsStyle.Render("✓ No parameters required"))
		lines = append(lines, noParamsStyle.Render("  Press Enter to execute"))
	} else {
		for i, field := range t.paramFields {
			isSelected := i == t.selectedField

			// Field label
			reqMark := ""
			if field.Required {
				reqMark = " *"
			}
			label := fmt.Sprintf("%s%s (%s)", field.Name, reqMark, field.Type)

			var labelStyle lipgloss.Style
			if isSelected {
				labelStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Bold(true)
			} else {
				labelStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Text))
			}
			lines = append(lines, labelStyle.Render(label))

			// Field description (if available)
			if field.Description != "" && isSelected {
				descStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Italic(true).
					Width(width - 12)
				desc := field.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				lines = append(lines, descStyle.Render("  "+desc))
			}

			// Field value (input box)
			valueText := field.Value
			if valueText == "" {
				valueText = "(empty)"
			}

			var valueStyle lipgloss.Style
			if isSelected {
				valueStyle = lipgloss.NewStyle().
					Background(lipgloss.Color(th.BGLight)).
					Foreground(lipgloss.Color(th.Text)).
					Width(width-12).
					Padding(0, 1).
					Border(lipgloss.RoundedBorder()).
					BorderForeground(lipgloss.Color(th.Primary))
			} else {
				valueStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Width(width-12).
					Padding(0, 1)
			}
			lines = append(lines, "  "+valueStyle.Render(valueText))
			lines = append(lines, "")
		}
	}

	// Hint
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width - 4).
		Align(lipgloss.Center)
	lines = append(lines, "", hintStyle.Render("Tab/↑/↓ navigate • Type to edit • Enter execute • Esc back"))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Width(width-2).
		Height(height-2).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderResultScreen shows execution result
func (t *ToolTester) renderResultScreen(width, height int, th Theme) string {
	entry := t.entries[t.selectedTool]

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary))

	badge := t.getSourceBadge(entry.Source, entry.ServerName, th)
	title := lipgloss.JoinHorizontal(lipgloss.Center,
		titleStyle.Render("Result: "+entry.Name+" "),
		badge,
	)

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")

	// Separator
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border))
	lines = append(lines, sepStyle.Render(strings.Repeat("─", width-8)))
	lines = append(lines, "")

	if t.lastError != "" {
		errorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).
			Width(width - 8)
		lines = append(lines, errorStyle.Render("✗ Error:"))
		lines = append(lines, errorStyle.Render("  "+t.lastError))
	} else {
		successStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Width(width - 8)
		lines = append(lines, successStyle.Render("✓ Success"))
		lines = append(lines, "")

		resultStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Width(width - 8)

		// Truncate result if too long
		result := t.lastResult
		maxLines := height - 14
		if maxLines < 5 {
			maxLines = 5
		}
		resultLines := strings.Split(result, "\n")
		if len(resultLines) > maxLines {
			result = strings.Join(resultLines[:maxLines], "\n") + "\n...(truncated)"
		}

		lines = append(lines, resultStyle.Render(result))
	}

	// Hint
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width - 4).
		Align(lipgloss.Center)
	lines = append(lines, "", hintStyle.Render("Enter/Esc back to tool list"))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Width(width-2).
		Height(height-2).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// HandleKey handles keyboard input
func (t *ToolTester) HandleKey(key string) {
	switch t.state {
	case "list":
		t.handleListKey(key)
	case "params":
		t.handleParamsKey(key)
	case "result":
		t.handleResultKey(key)
	}
}

func (t *ToolTester) handleListKey(key string) {
	switch key {
	case "down", "j":
		if t.selectedTool < len(t.entries)-1 {
			t.selectedTool++
		}
	case "up", "k":
		if t.selectedTool > 0 {
			t.selectedTool--
		}
	case "pagedown", "ctrl+d":
		t.selectedTool += 10
		if t.selectedTool >= len(t.entries) {
			t.selectedTool = len(t.entries) - 1
		}
	case "pageup", "ctrl+u":
		t.selectedTool -= 10
		if t.selectedTool < 0 {
			t.selectedTool = 0
		}
	case "home", "g":
		t.selectedTool = 0
		t.scrollOffset = 0
	case "end", "G":
		t.selectedTool = len(t.entries) - 1
	case "enter":
		if t.selectedTool < len(t.entries) {
			entry := t.entries[t.selectedTool]
			if entry.Tool != nil {
				t.paramFields = t.buildParamFields(entry.Tool)
				t.selectedField = 0
				t.state = "params"
			}
		}
	}
}

func (t *ToolTester) handleParamsKey(key string) {
	switch key {
	case "esc":
		t.state = "list"
	case "tab", "down":
		if len(t.paramFields) > 0 && t.selectedField < len(t.paramFields)-1 {
			t.selectedField++
		}
	case "up", "shift+tab":
		if t.selectedField > 0 {
			t.selectedField--
		}
	case "enter":
		t.executeCurrentTool()
	case "backspace":
		if len(t.paramFields) > 0 && t.selectedField < len(t.paramFields) {
			field := &t.paramFields[t.selectedField]
			if len(field.Value) > 0 {
				field.Value = field.Value[:len(field.Value)-1]
			}
		}
	default:
		// Type character into current field
		if len(key) == 1 && len(t.paramFields) > 0 && t.selectedField < len(t.paramFields) {
			t.paramFields[t.selectedField].Value += key
		}
	}
}

func (t *ToolTester) handleResultKey(key string) {
	switch key {
	case "esc", "enter":
		t.state = "list"
		t.lastResult = ""
		t.lastError = ""
	}
}

// executeCurrentTool runs the selected tool with current parameters
func (t *ToolTester) executeCurrentTool() {
	if t.selectedTool >= len(t.entries) || t.onExecute == nil {
		return
	}

	entry := t.entries[t.selectedTool]
	if entry.Tool == nil {
		t.lastError = "Tool not available"
		t.state = "result"
		return
	}

	// Build params map
	params := make(map[string]any)
	for _, field := range t.paramFields {
		if field.Value != "" {
			// Try to parse as JSON first for complex types
			var val any
			if err := json.Unmarshal([]byte(field.Value), &val); err == nil {
				params[field.Name] = val
			} else {
				params[field.Name] = field.Value
			}
		}
	}

	// Execute
	result, err := t.onExecute(context.Background(), entry.Tool, params)
	if err != nil {
		t.lastError = err.Error()
		t.lastResult = ""
	} else {
		t.lastResult = result
		t.lastError = ""
	}

	t.state = "result"
}

// GetState returns current state
func (t *ToolTester) GetState() string {
	return t.state
}

// SetState sets current state
func (t *ToolTester) SetState(state string) {
	t.state = state
}
