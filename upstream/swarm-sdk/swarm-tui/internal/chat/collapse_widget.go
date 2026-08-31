package chat

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/diffview"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/charmbracelet/x/ansi"
)

var catNLineRE = regexp.MustCompile(`^(\s*\d+)([→\t])(.*)$`)

// TodoItemForRender is a lightweight representation of a todo item for rendering.
// This avoids a direct dependency on the ii package from the widget layer.
type TodoItemForRender struct {
	ID        string
	Content   string
	Status    string   // "pending", "in_progress", "completed"
	Priority  string   // "low", "medium", "high"
	DependsOn []string // IDs of tasks this depends on
	Blocks    []string // IDs of tasks this blocks
}

// globalTodoManagerForRender is a function that returns the current todo list.
// It is set during app initialization to bridge the ii package to the widget.
var globalTodoManagerForRender func() []TodoItemForRender

// SetGlobalTodoManagerForRender registers the function that provides todo items for rendering.
func SetGlobalTodoManagerForRender(fn func() []TodoItemForRender) {
	globalTodoManagerForRender = fn
}

// GetGlobalTodoManagerForRender returns the registered todo provider function, or nil.
func GetGlobalTodoManagerForRender() func() []TodoItemForRender {
	return globalTodoManagerForRender
}

// CollapseWidgetConfig configures the collapse widget appearance
// CollapseWidgetConfig configures the collapse widget appearance
type CollapseWidgetConfig struct {
	// Symbols
	CollapsedSymbol string // Symbol when collapsed (e.g., "▶")
	CompactSymbol   string // Symbol when compact (e.g., "▽")
	ExpandedSymbol  string // Symbol when fully expanded (e.g., "▼")

	// Colors (from palette)
	BackgroundColor string // Background color for all styled text
	ToolNameColor   string
	MetadataColor   string
	ErrorColor      string
	FocusColor      string
	OutputColor     string
	ConnectorColor  string
	HintBarColor    string // Color for Ctrl+B hint bar background (theme-dependent)

	// Layout
	PreviewLines  int  // Number of lines to show in compact mode (default: 3)
	ShowTiming    bool // Whether to show execution timing
	ShowLineCount bool // Whether to show line counts
	ShowToolID    bool // Whether to show tool IDs in verbose mode
}

// DefaultCollapseWidgetConfig returns sensible defaults using the palette
// DefaultCollapseWidgetConfig returns sensible defaults using the palette
func DefaultCollapseWidgetConfig() CollapseWidgetConfig {
	return CollapseWidgetConfig{
		CollapsedSymbol: "",
		CompactSymbol:   "",
		ExpandedSymbol:  "",
		BackgroundColor: palette.Surface,
		ToolNameColor:   palette.Accent,
		MetadataColor:   palette.TextMuted,
		ErrorColor:      palette.Error,
		FocusColor:      palette.Warning,
		OutputColor:     palette.TextDim,
		ConnectorColor:  palette.TextMuted,
		HintBarColor:    palette.Warning, // Default to warning color for high visibility
		PreviewLines:    3,
		ShowTiming:      true,
		ShowLineCount:   true,
		ShowToolID:      false,
	}
}

// CollapseWidget renders tool collapse indicators and handles display logic
type CollapseWidget struct {
	config CollapseWidgetConfig
}

// NewCollapseWidget creates a new widget with the given config
func NewCollapseWidget(config CollapseWidgetConfig) *CollapseWidget {
	return &CollapseWidget{config: config}
}

// NewDefaultCollapseWidget creates a new widget with default config
func NewDefaultCollapseWidget() *CollapseWidget {
	return NewCollapseWidget(DefaultCollapseWidgetConfig())
}

// GetConfig returns the current config
func (w *CollapseWidget) GetConfig() CollapseWidgetConfig {
	return w.config
}

// SetConfig updates the config
func (w *CollapseWidget) SetConfig(config CollapseWidgetConfig) {
	w.config = config
}

// RenderToolHeader renders the tool call header with collapse indicator
// Returns the formatted header line ready for display
func (w *CollapseWidget) RenderToolHeader(state *ToolCallState, bullet string, params map[string]any, isStreaming bool, verbose bool) string {
	if state == nil {
		// Fallback if no state (shouldn't happen)
		return bullet + " " + "unknown"
	}

	// Build styles
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.config.ToolNameColor)).
		Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.MetadataColor))
	statusColor := palette.Success
	if isStreaming {
		statusColor = w.config.ToolNameColor
	} else if state.HasError {
		statusColor = w.config.ErrorColor
	}
	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(statusColor)).
		Bold(true)

	// Focus styling
	if state.IsFocused {
		nameStyle = nameStyle.Bold(true).Foreground(lipgloss.Color(w.config.FocusColor))
	}

	// Error styling
	if state.HasError {
		nameStyle = nameStyle.Foreground(lipgloss.Color(w.config.ErrorColor))
	}

	// Get collapse symbol
	symbol := w.getCollapseSymbol(state.CollapseLevel)

	// Build the header parts
	var parts []string
	if bullet != "" {
		parts = append(parts, statusStyle.Render(bullet))
	}
	if symbol != "" {
		parts = append(parts, symbol)
	}
	parts = append(parts, nameStyle.Render(state.ToolName))

	// Keep the primary argument visible even when collapsed. Secondary
	// parameters stay muted and progressively disclose in compact/full modes.
	paramStr := w.formatParams(params, state.CollapseLevel, verbose)
	if paramStr != "" {
		parts = append(parts, metaStyle.Render(paramStr))
	}

	// Add metadata
	var metadata []string

	// Line count (when collapsed or compact) - disabled for cleaner UI
	// if w.config.ShowLineCount && state.OutputLines > 0 {
	//	if state.CollapseLevel == CollapseLevelCollapsed {
	//		metadata = append(metadata, fmt.Sprintf("%d lines", state.OutputLines))
	//	}
	// }

	// Timing (when not streaming and has timing data)
	if w.config.ShowTiming && state.ExecutionTime > 0 && !isStreaming {
		metadata = append(metadata, fmt.Sprintf("%.2fs", state.ExecutionTime.Seconds()))
	}

	// Tool ID (in verbose mode)
	if w.config.ShowToolID && verbose && state.CallID != "" {
		// Truncate ID for display
		id := state.CallID
		if len(id) > 12 {
			id = id[:12] + "..."
		}
		metadata = append(metadata, fmt.Sprintf("id:%s", id))
	}

	if len(metadata) > 0 {
		parts = append(parts, metaStyle.Render("["+strings.Join(metadata, ", ")+"]"))
	}

	return shared.ReapplyBackground(strings.Join(parts, " "), w.config.BackgroundColor)
}

// RenderBashBackgroundHint renders a highlighted hint bar for backgrounding bash commands
// Shows "Ctrl+B to background" in a highlighted bar
func (w *CollapseWidget) RenderBashBackgroundHint() string {
	// Use theme-dependent hint bar color for background
	// Black text on theme accent/warning background for high visibility
	hintBarColor := w.config.HintBarColor
	if hintBarColor == "" {
		hintBarColor = "226" // Fallback to bright yellow if not set
	}

	hintStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(hintBarColor)).
		Foreground(lipgloss.Color("0")). // Black text
		Bold(true).
		Padding(0, 1)

	return hintStyle.Render(i18n.T("classic_chat.collapse.background_hint"))
}

// getCollapseSymbol returns the appropriate symbol for the collapse level
func (w *CollapseWidget) getCollapseSymbol(level CollapseLevel) string {
	switch level {
	case CollapseLevelCollapsed:
		return w.config.CollapsedSymbol
	case CollapseLevelCompact:
		return w.config.CompactSymbol
	case CollapseLevelFull:
		return w.config.ExpandedSymbol
	default:
		return w.config.CollapsedSymbol
	}
}

// formatParams formats parameters for display in tool call headers.
// Always shows all parameters as key=value pairs (sorted for determinism).
func (w *CollapseWidget) formatParams(params map[string]any, level CollapseLevel, verbose bool) string {
	if len(params) == 0 {
		return ""
	}

	// Per-value truncation length
	valueMaxLen := 80
	if level == CollapseLevelFull || verbose {
		valueMaxLen = 120
	}

	// Bash command is the primary argument. Avoid brackets so the header reads
	// as one compact sentence; extras remain progressive details.
	if cmdParam, ok := params["command"].(string); ok {
		cmdMaxLen := 100
		if level == CollapseLevelFull || verbose {
			cmdMaxLen = 200
		}
		result := truncateDisplayString(strings.Join(strings.Fields(cmdParam), " "), cmdMaxLen)

		if len(params) > 1 && (level != CollapseLevelCollapsed || verbose) {
			var extras []string
			for key, val := range params {
				if key == "command" {
					continue
				}
				extras = append(extras, fmt.Sprintf("%s=%v", key, truncateDisplayString(fmt.Sprint(val), 30)))
			}
			sort.Strings(extras)
			if len(extras) > 0 {
				result += " (" + strings.Join(extras, ", ") + ")"
			}
		}
		return result
	}

	// Build key=value list with primary keys first, then alphabetical
	primaryKeys := []string{"file_path", "path", "pattern", "url", "query"}
	shown := make(map[string]bool)
	var parts []string

	for _, pk := range primaryKeys {
		if val, ok := params[pk]; ok {
			s := fmt.Sprint(val)
			// For path keys, prefer left-truncation so the filename stays visible.
			if pk == "file_path" || pk == "path" {
				s = leftTruncatePath(s, valueMaxLen)
			} else {
				s = truncateDisplayString(s, valueMaxLen)
			}
			parts = append(parts, fmt.Sprintf("%s=%v", pk, s))
			shown[pk] = true
			if level == CollapseLevelCollapsed && !verbose {
				return parts[0]
			}
		}
	}

	if level == CollapseLevelCollapsed && !verbose {
		return ""
	}

	var remaining []string
	for key := range params {
		if !shown[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	for _, key := range remaining {
		val := params[key]
		parts = append(parts, fmt.Sprintf("%s=%v", key, truncateDisplayString(fmt.Sprint(val), valueMaxLen)))
	}

	return fmt.Sprintf("(%s)", strings.Join(parts, ", "))
}

// RenderOutput renders tool output based on collapse state
// Returns the lines to display and whether there are more lines
func (w *CollapseWidget) RenderOutput(state *ToolCallState, output string, width int) ([]string, int) {
	if state == nil || output == "" {
		return nil, 0
	}

	lines := strings.Split(output, "\n")
	totalLines := len(lines)

	// Styles
	resultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.OutputColor)).Background(lipgloss.Color(w.config.BackgroundColor))
	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor)).Background(lipgloss.Color(w.config.BackgroundColor))

	var result []string

	switch state.CollapseLevel {
	case CollapseLevelCollapsed:
		// Show nothing, header already has line count
		return nil, totalLines

	case CollapseLevelCompact:
		// Show first N lines - use more for Bash tool (minimum 5 lines)
		previewCount := w.config.PreviewLines

		// For Bash tool, show at least 5 lines
		if state.ToolName == "Bash" && previewCount < 5 {
			previewCount = 5
		}

		if previewCount > totalLines {
			previewCount = totalLines
		}

		for i := 0; i < previewCount; i++ {
			line := lines[i]
			if i == 0 {
				firstLine := "    " + connectorStyle.Render("⎿") + " " + resultStyle.Render(line)
				firstLine = shared.ReapplyBackground(firstLine, w.config.BackgroundColor)
				result = append(result, firstLine)
			} else {
				restLine := "      " + resultStyle.Render(line)
				restLine = shared.ReapplyBackground(restLine, w.config.BackgroundColor)
				result = append(result, restLine)
			}
		}

		remaining := totalLines - previewCount
		return result, remaining

	case CollapseLevelFull:
		// Show all lines
		for i, line := range lines {
			if i == 0 {
				firstLine := "    " + connectorStyle.Render("⎿") + " " + resultStyle.Render(line)
				firstLine = shared.ReapplyBackground(firstLine, w.config.BackgroundColor)
				result = append(result, firstLine)
			} else {
				restLine := "      " + resultStyle.Render(line)
				restLine = shared.ReapplyBackground(restLine, w.config.BackgroundColor)
				result = append(result, restLine)
			}
		}
		return result, 0
	}

	return nil, 0
}

// RenderDiffOutput renders a diff view for edit tools (Edit, Write, Patch)
// Always shows the actual diff content with hardcoded colors that can't be theme-overridden
func (w *CollapseWidget) RenderDiffOutput(state *ToolCallState, filePath, oldContent, newContent string, width int) ([]string, int) {
	// Compute the diff
	dv := diffview.NewForEdit(filePath, oldContent, newContent)
	diff := dv.GetDiff()

	if diff == nil || diff.IsEmpty() {
		// No changes - show a simple message
		noChanges := "    " + ansiFgMuted + "⎿" + ansiReset + " " + ansiFgDim + "(no changes)" + ansiReset
		return []string{shared.ReapplyBackground(noChanges, w.config.BackgroundColor)}, 0
	}

	var result []string
	codeWidth := width - 14 // Account for: "    ⎿ " (6) + line num (4) + symbol (2) + padding (2)
	if codeWidth < 20 {
		codeWidth = 20
	}

	// Calculate max line number width
	maxLineNum := 0
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			if line.BeforeNum > maxLineNum {
				maxLineNum = line.BeforeNum
			}
			if line.AfterNum > maxLineNum {
				maxLineNum = line.AfterNum
			}
		}
	}
	numWidth := len(fmt.Sprintf("%d", maxLineNum))
	if numWidth < 3 {
		numWidth = 3
	}

	firstLine := true
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			var rendered string

			// Format line number
			var lineNumStr string
			if line.Kind == diffview.DiffLineDelete {
				lineNumStr = fmt.Sprintf("%*d", numWidth, line.BeforeNum)
			} else if line.Kind == diffview.DiffLineInsert {
				lineNumStr = fmt.Sprintf("%*d", numWidth, line.AfterNum)
			} else {
				// Context line - show after num
				lineNumStr = fmt.Sprintf("%*d", numWidth, line.AfterNum)
			}

			// Truncate code if needed using ANSI-aware visual width
			code := line.Content
			if ansi.StringWidth(code) > codeWidth {
				code = ansi.Truncate(code, codeWidth-3, "") + "..."
			}
			// Pad code to full width for consistent background using ANSI-aware utility
			code = PadToWidth(code, codeWidth)

			switch line.Kind {
			case diffview.DiffLineInsert:
				// Green background with + symbol
				rendered = ansiBgGreen + ansiFgDim + lineNumStr + " " + ansiFgGreen + "+" + " " + ansiFgWhite + code + ansiReset

			case diffview.DiffLineDelete:
				// Red background with - symbol
				rendered = ansiBgRed + ansiFgDim + lineNumStr + " " + ansiFgRed + "-" + " " + ansiFgWhite + code + ansiReset

			default:
				// Context line - no background
				rendered = ansiFgDim + lineNumStr + "   " + ansiFgWhite + code + ansiReset
			}

			// Add connector prefix
			if firstLine {
				result = append(result, "    "+ansiFgMuted+"⎿"+ansiReset+" "+rendered)
				firstLine = false
			} else {
				result = append(result, "      "+rendered)
			}
		}
	}

	// Apply background to all lines
	for i := range result {
		result[i] = shared.ReapplyBackground(result[i], w.config.BackgroundColor)
	}
	return result, 0
}

// RenderReadOutput renders file content with syntax highlighting for Read tool
// Uses hardcoded ANSI colors that can't be overridden by terminal themes
func (w *CollapseWidget) RenderReadOutput(filePath, content string, width int) []string {
	if content == "" {
		emptyFile := "    " + ansiFgMuted + "⎿" + ansiReset + " " + ansiFgDim + "(empty file)" + ansiReset
		return []string{shared.ReapplyBackground(emptyFile, w.config.BackgroundColor)}
	}

	lines := strings.Split(content, "\n")
	var result []string

	// Calculate widths - account for full prefix including line numbers
	// Prefix is: "    ⎿ " (6) + lineNum (4-5) + " " (1) = 11-12 chars
	// Use 14 to be safe for large line numbers
	codeWidth := width - 14
	if codeWidth < 20 {
		codeWidth = 20
	}

	// Detect language from file path
	lang := detectLanguageFromPath(filePath)

	for i, line := range lines {
		var lineNumStr, codeContent string

		// Check if line has cat -n format line numbers
		if matches := catNLineRE.FindStringSubmatch(line); matches != nil {
			// Extract existing line number and content
			lineNumStr = strings.TrimSpace(matches[1])
			codeContent = matches[3]
		} else {
			// No line numbers in content, use index
			lineNumStr = fmt.Sprintf("%d", i+1)
			codeContent = line
		}

		// Pad line number for alignment
		if len(lineNumStr) < 4 {
			lineNumStr = fmt.Sprintf("%4s", lineNumStr)
		}

		// Apply syntax highlighting to the code content
		highlightedCode := highlightLineANSI(codeContent, lang, codeWidth, w.config.BackgroundColor)

		// Build the line: [connector] [linenum] [code]
		var rendered string
		if i == 0 {
			rendered = "    " + ansiFgMuted + "⎿" + ansiReset + " " + ansiFgDim + lineNumStr + ansiReset + " " + highlightedCode
		} else {
			rendered = "      " + ansiFgDim + lineNumStr + ansiReset + " " + highlightedCode
		}

		result = append(result, rendered)
	}

	// Apply background to all lines
	for i := range result {
		result[i] = shared.ReapplyBackground(result[i], w.config.BackgroundColor)
	}
	return result
}

// detectLanguageFromPath determines programming language from file extension
func detectLanguageFromPath(filePath string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))

	langMap := map[string]string{
		"go":    "go",
		"py":    "python",
		"js":    "javascript",
		"jsx":   "javascript",
		"ts":    "typescript",
		"tsx":   "typescript",
		"rs":    "rust",
		"java":  "java",
		"kt":    "kotlin",
		"c":     "c",
		"cpp":   "cpp",
		"cc":    "cpp",
		"h":     "c",
		"hpp":   "cpp",
		"swift": "swift",
		"rb":    "ruby",
		"php":   "php",
		"sh":    "bash",
		"bash":  "bash",
		"zsh":   "bash",
		"html":  "html",
		"htm":   "html",
		"css":   "css",
		"scss":  "scss",
		"json":  "json",
		"yaml":  "yaml",
		"yml":   "yaml",
		"toml":  "toml",
		"xml":   "xml",
		"md":    "markdown",
		"sql":   "sql",
	}

	if lang, ok := langMap[ext]; ok {
		return lang
	}

	// Check special filenames
	base := strings.ToLower(filepath.Base(filePath))
	if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") {
		return "dockerfile"
	}
	if base == "makefile" || strings.HasPrefix(base, "makefile.") {
		return "makefile"
	}

	return "text"
}

// highlightLineANSI applies syntax highlighting to a line using hardcoded ANSI codes.
// Input is always raw text (no ANSI codes), so we truncate first then highlight.
func highlightLineANSI(line string, lang string, maxWidth int, bgColor string) string {
	// Truncate raw text FIRST (before any ANSI codes)
	displayLine := line
	if len(displayLine) > maxWidth {
		if maxWidth > 3 {
			displayLine = displayLine[:maxWidth-3] + "..."
		} else {
			displayLine = displayLine[:maxWidth]
		}
	}

	// Check for comment first (highest priority)
	trimmed := strings.TrimSpace(displayLine)
	if isCommentLine(trimmed, lang) {
		commentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Background(lipgloss.Color(bgColor)).Italic(true)
		return commentStyle.Render(displayLine)
	}

	// Apply highlighting to already-truncated line
	return highlightPatterns(displayLine, lang, bgColor)
}

// isCommentLine checks if a line is a comment
func isCommentLine(trimmed string, lang string) bool {
	switch lang {
	case "go", "java", "javascript", "typescript", "c", "cpp", "rust", "swift", "kotlin":
		return strings.HasPrefix(trimmed, "//")
	case "python", "ruby", "bash", "shell", "yaml", "dockerfile", "makefile":
		return strings.HasPrefix(trimmed, "#")
	case "html", "xml":
		return strings.HasPrefix(trimmed, "<!--")
	case "css", "scss":
		return strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")
	default:
		return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")
	}
}

// highlightPatterns applies syntax highlighting patterns using lipgloss styles.
// Uses lipgloss instead of raw ANSI to avoid corruption when patterns match
// inside previously-added ANSI sequences.
func highlightPatterns(line string, lang string, bgColor string) string {
	// Define styles using lipgloss (handles ANSI properly)
	stringStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Background(lipgloss.Color(bgColor))             // Green
	numberStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")).Background(lipgloss.Color(bgColor))             // Orange
	keywordStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Background(lipgloss.Color(bgColor)).Bold(true) // Purple bold
	typeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa")).Background(lipgloss.Color(bgColor))               // Blue
	defaultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Background(lipgloss.Color(bgColor))      // White

	// Get language-specific keywords and types
	keywords := getKeywordsForLang(lang)
	types := getTypesForLang(lang)

	// Tokenize the line and apply styles to each token
	// This approach avoids regex matching inside ANSI sequences
	result := tokenizeAndHighlight(line, lang, keywords, types, stringStyle, numberStyle, keywordStyle, typeStyle, defaultStyle)

	return result
}

// tokenizeAndHighlight breaks a line into tokens and applies syntax highlighting.
// This is safer than regex replacement which can corrupt ANSI sequences.
// Coalesces consecutive same-style characters for efficiency.
func tokenizeAndHighlight(line string, lang string, keywords, types []string,
	stringStyle, numberStyle, keywordStyle, typeStyle, defaultStyle lipgloss.Style) string {

	if len(line) == 0 {
		return ""
	}

	// Build keyword and type sets for O(1) lookup
	keywordSet := make(map[string]bool)
	for _, kw := range keywords {
		keywordSet[kw] = true
	}
	typeSet := make(map[string]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	var result strings.Builder
	var defaultChars strings.Builder // Accumulate consecutive default-style chars
	i := 0

	// Flush accumulated default chars
	flushDefault := func() {
		if defaultChars.Len() > 0 {
			result.WriteString(defaultStyle.Render(defaultChars.String()))
			defaultChars.Reset()
		}
	}

	for i < len(line) {
		ch := line[i]

		// Handle string literals
		if ch == '"' || ch == '\'' || ch == '`' {
			flushDefault()
			quote := ch
			start := i
			i++
			for i < len(line) && line[i] != quote {
				if line[i] == '\\' && i+1 < len(line) {
					i++ // Skip escaped character
				}
				i++
			}
			if i < len(line) {
				i++ // Include closing quote
			}
			result.WriteString(stringStyle.Render(line[start:i]))
			continue
		}

		// Handle numbers
		if ch >= '0' && ch <= '9' {
			flushDefault()
			start := i
			for i < len(line) && ((line[i] >= '0' && line[i] <= '9') || line[i] == '.') {
				i++
			}
			result.WriteString(numberStyle.Render(line[start:i]))
			continue
		}

		// Handle identifiers (keywords, types, variables)
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' {
			flushDefault()
			start := i
			for i < len(line) && ((line[i] >= 'a' && line[i] <= 'z') ||
				(line[i] >= 'A' && line[i] <= 'Z') ||
				(line[i] >= '0' && line[i] <= '9') || line[i] == '_') {
				i++
			}
			word := line[start:i]

			if keywordSet[word] {
				result.WriteString(keywordStyle.Render(word))
			} else if typeSet[word] {
				result.WriteString(typeStyle.Render(word))
			} else {
				result.WriteString(defaultStyle.Render(word))
			}
			continue
		}

		// Default: accumulate characters (spaces, punctuation, etc.)
		defaultChars.WriteByte(ch)
		i++
	}

	// Flush any remaining default chars
	flushDefault()

	return result.String()
}

// getKeywordsForLang returns keywords for a language
func getKeywordsForLang(lang string) []string {
	switch lang {
	case "go":
		return []string{
			"func", "return", "if", "else", "for", "range", "switch", "case", "default",
			"break", "continue", "package", "import", "type", "struct", "interface",
			"map", "chan", "const", "var", "defer", "go", "select", "nil", "true", "false",
		}
	case "python":
		return []string{
			"def", "return", "if", "elif", "else", "for", "while", "in", "not", "and", "or",
			"class", "import", "from", "as", "try", "except", "finally", "raise", "with",
			"pass", "break", "continue", "lambda", "yield", "None", "True", "False", "self",
		}
	case "javascript", "typescript":
		return []string{
			"function", "return", "if", "else", "for", "while", "switch", "case", "default",
			"break", "continue", "const", "let", "var", "class", "extends", "import", "export",
			"from", "async", "await", "try", "catch", "finally", "throw", "new", "this",
			"null", "undefined", "true", "false",
		}
	case "rust":
		return []string{
			"fn", "return", "if", "else", "for", "while", "loop", "match", "struct", "enum",
			"impl", "trait", "use", "mod", "pub", "let", "mut", "const", "static",
			"async", "await", "move", "self", "Self", "true", "false",
		}
	case "java", "kotlin":
		return []string{
			"public", "private", "protected", "class", "interface", "extends", "implements",
			"return", "if", "else", "for", "while", "switch", "case", "default", "break",
			"continue", "new", "this", "super", "static", "final", "void", "null", "true", "false",
		}
	case "bash", "shell", "dockerfile":
		return []string{
			"if", "then", "else", "elif", "fi", "for", "while", "do", "done", "case", "esac",
			"function", "return", "local", "export", "source", "echo", "read", "exit",
			"FROM", "RUN", "CMD", "COPY", "ADD", "ENV", "WORKDIR", "EXPOSE", "ENTRYPOINT",
		}
	case "html", "xml":
		return []string{} // HTML uses tags, not keywords
	case "css", "scss":
		return []string{
			"import", "media", "keyframes", "font-face", "supports", "charset",
		}
	default:
		return []string{
			"if", "else", "for", "while", "return", "function", "class", "import",
			"true", "false", "null", "nil",
		}
	}
}

// getTypesForLang returns type names for a language
func getTypesForLang(lang string) []string {
	switch lang {
	case "go":
		return []string{
			"string", "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64", "complex64", "complex128",
			"bool", "byte", "rune", "error", "any",
		}
	case "typescript":
		return []string{
			"string", "number", "boolean", "any", "void", "never", "unknown",
			"Array", "Promise", "Map", "Set", "Record", "Partial", "Required",
		}
	case "rust":
		return []string{
			"i8", "i16", "i32", "i64", "i128", "isize",
			"u8", "u16", "u32", "u64", "u128", "usize",
			"f32", "f64", "bool", "char", "str", "String",
			"Vec", "Option", "Result", "Box", "Rc", "Arc",
		}
	case "java", "kotlin":
		return []string{
			"int", "long", "short", "byte", "float", "double", "boolean", "char",
			"String", "Integer", "Long", "Double", "Boolean", "Object", "List", "Map", "Set",
		}
	case "c", "cpp":
		return []string{
			"int", "long", "short", "char", "float", "double", "void",
			"unsigned", "signed", "size_t", "bool",
		}
	default:
		return []string{}
	}
}

// RenderPatchOutput renders a V4A patch format diff view
// Uses hardcoded ANSI colors that can't be overridden by terminal themes
func (w *CollapseWidget) RenderPatchOutput(patchInput string, width int) []string {
	if patchInput == "" {
		emptyPatch := "    " + ansiFgMuted + "⎿" + ansiReset + " " + ansiFgDim + i18n.T("classic_chat.collapse.empty_patch") + ansiReset
		return []string{shared.ReapplyBackground(emptyPatch, w.config.BackgroundColor)}
	}

	lines := strings.Split(patchInput, "\n")
	var result []string

	codeWidth := width - 10
	if codeWidth < 20 {
		codeWidth = 20
	}

	firstLine := true
	currentFile := ""

	for _, line := range lines {
		var rendered string

		// Detect file headers
		if strings.HasPrefix(line, "*** Begin Patch") {
			continue // Skip begin marker
		}
		if strings.HasPrefix(line, "*** End Patch") {
			continue // Skip end marker
		}
		if strings.HasPrefix(line, "*** Update File:") || strings.HasPrefix(line, "*** Add File:") || strings.HasPrefix(line, "*** Delete File:") {
			// File header - show in blue
			currentFile = strings.TrimPrefix(line, "*** Update File: ")
			currentFile = strings.TrimPrefix(currentFile, "*** Add File: ")
			currentFile = strings.TrimPrefix(currentFile, "*** Delete File: ")
			rendered = ansiFgType + ansiBold + line + ansiReset
		} else if after, ok := strings.CutPrefix(line, "+"); ok {
			// Addition line - green background
			code := after
			if ansi.StringWidth(code) > codeWidth {
				code = ansi.Truncate(code, codeWidth-3, "") + "..."
			}
			// Pad for consistent background using ANSI-aware utility
			code = PadToWidth(code, codeWidth)
			rendered = ansiBgGreen + ansiFgGreen + "+" + " " + ansiFgWhite + code + ansiReset
		} else if after, ok := strings.CutPrefix(line, "-"); ok {
			// Deletion line - red background
			code := after
			if ansi.StringWidth(code) > codeWidth {
				code = ansi.Truncate(code, codeWidth-3, "") + "..."
			}
			// Pad for consistent background using ANSI-aware utility
			code = PadToWidth(code, codeWidth)
			rendered = ansiBgRed + ansiFgRed + "-" + " " + ansiFgWhite + code + ansiReset
		} else if strings.HasPrefix(line, " ") || line == "" {
			// Context line
			code := line
			if ansi.StringWidth(code) > codeWidth {
				code = ansi.Truncate(code, codeWidth-3, "") + "..."
			}
			rendered = ansiFgDim + "  " + code + ansiReset
		} else {
			// Other lines (context markers, etc.)
			rendered = ansiFgDim + line + ansiReset
		}

		// Add connector prefix
		if firstLine {
			result = append(result, "    "+ansiFgMuted+"⎿"+ansiReset+" "+rendered)
			firstLine = false
		} else {
			result = append(result, "      "+rendered)
		}
	}

	// Detect language from file path for potential future syntax highlighting
	_ = currentFile

	// Apply background to all lines
	for i := range result {
		result[i] = shared.ReapplyBackground(result[i], w.config.BackgroundColor)
	}
	return result
}

// RenderTodoOutput renders a styled checklist view for TodoWrite/TodoRead tool results.
// It parses the tool output text and renders todo items with status icons and priority badges.
func (w *CollapseWidget) RenderTodoOutput(toolName string, output string, width int) []string {
	if output == "" {
		noTodos := "    " + ansiFgMuted + "⎿" + ansiReset + " " + ansiFgDim + i18n.T("classic_chat.collapse.no_todos") + ansiReset
		return []string{shared.ReapplyBackground(noTodos, w.config.BackgroundColor)}
	}

	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor)).Background(lipgloss.Color(w.config.BackgroundColor))

	// Status icons and colors (ANSI)
	statusIcon := map[string]string{
		"pending":     ansiFgDim + "○" + ansiReset,
		"in_progress": "\033[33m" + "◉" + ansiReset, // yellow
		"completed":   "\033[32m" + "✓" + ansiReset, // green
	}
	priorityBadge := map[string]string{
		"high":   "\033[31m" + "▲" + ansiReset, // red
		"medium": "\033[33m" + "─" + ansiReset, // yellow
		"low":    "\033[34m" + "▽" + ansiReset, // blue
	}

	var result []string
	lines := strings.Split(output, "\n")

	// Render summary line first (extract from first lines of output)
	summaryRendered := false
	todoStarted := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check for JSON todo items: "id", "content", "status", "priority"
		// The output contains JSON array of todo objects
		if strings.Contains(trimmed, `"id"`) && strings.Contains(trimmed, `"content"`) {
			todoStarted = true
			continue
		}

		// Parse individual todo fields from JSON-like output
		if todoStarted {
			// Skip JSON structural characters
			if trimmed == "[" || trimmed == "]" || trimmed == "{" || trimmed == "}," || trimmed == "}" {
				continue
			}
		}

		// Show summary/status messages
		if !todoStarted && !summaryRendered {
			// First meaningful line is the status message
			prefix := connectorStyle.Render("⎿")
			result = append(result, "    "+prefix+" "+ansiFgDim+trimmed+ansiReset)
			summaryRendered = true
			continue
		}

		// Show "Updated: N total (...)" summary line
		if strings.HasPrefix(trimmed, "Updated:") || strings.HasPrefix(trimmed, "Summary:") {
			prefix := connectorStyle.Render("⎿")
			if len(result) == 0 {
				result = append(result, "    "+prefix+" "+ansiFgDim+trimmed+ansiReset)
			} else {
				result = append(result, "      "+ansiFgDim+trimmed+ansiReset)
			}
			continue
		}
	}

	// Now try to render actual todo items from the TodoManager (live data)
	todoManager := GetGlobalTodoManagerForRender()
	if todoManager != nil {
		todos := todoManager()
		if len(todos) > 0 {
			// Build status map for checking blockers
			statusByID := make(map[string]string, len(todos))
			for _, todo := range todos {
				statusByID[todo.ID] = todo.Status
			}

			// Add separator if we already have summary lines
			if len(result) > 0 {
				result = append(result, "")
			}

			maxContent := width - 20 // Account for indentation, icon, priority
			if maxContent < 20 {
				maxContent = 20
			}

			for i, todo := range todos {
				icon := statusIcon[string(todo.Status)]
				if icon == "" {
					icon = ansiFgDim + "?" + ansiReset
				}
				badge := priorityBadge[string(todo.Priority)]
				if badge == "" {
					badge = " "
				}

				// Check for unmet dependencies
				hasUnmetDeps := false
				if len(todo.DependsOn) > 0 {
					for _, depID := range todo.DependsOn {
						if status, ok := statusByID[depID]; ok && status != "completed" {
							hasUnmetDeps = true
							break
						}
					}
				}

				// Truncate content if needed
				content := todo.Content
				if len(content) > maxContent {
					content = content[:maxContent-3] + "..."
				}

				// Style based on status
				var contentStyled string
				switch string(todo.Status) {
				case "completed":
					contentStyled = "\033[9;2m" + content + ansiReset // strikethrough + dim
				case "in_progress":
					contentStyled = "\033[1m" + content + ansiReset // bold
				default:
					contentStyled = content
				}

				prefix := "      "
				if i == 0 && len(result) == 0 {
					prefix = "    " + connectorStyle.Render("⎿") + " "
					prefix = shared.ReapplyBackground(prefix, w.config.BackgroundColor)
				}

				line := fmt.Sprintf("%s%s %s %s", prefix, icon, badge, contentStyled)

				// Add blocked indicator
				if hasUnmetDeps {
					line += " " + ansiFgDim + "\U0001f512" + ansiReset
				}

				// Add inline dependency info
				if len(todo.DependsOn) > 0 && todo.Status != "completed" {
					depIDs := strings.Join(todo.DependsOn, ", ")
					line += " " + ansiFgDim + i18n.T("classic_chat.collapse.depends_on", depIDs) + ansiReset
				}

				result = append(result, line)
			}
		}
	}

	// If we got nothing at all, show the raw output as fallback
	if len(result) == 0 {
		firstLine := true
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if firstLine {
				firstLineStr := "    " + connectorStyle.Render("⎿") + " " + trimmed
				firstLineStr = shared.ReapplyBackground(firstLineStr, w.config.BackgroundColor)
				result = append(result, firstLineStr)
				firstLine = false
			} else {
				result = append(result, "      "+trimmed)
			}
		}
	}

	// Apply background to all lines
	for i := range result {
		result[i] = shared.ReapplyBackground(result[i], w.config.BackgroundColor)
	}
	return result
}

// RenderGrepOutput renders grep search results with beautiful formatting
// Format: "Found N matches for pattern "X" in /path (filter: *.go):"
// Followed by: "File: /path/to/file" and "L123: matched content"
func (w *CollapseWidget) RenderGrepOutput(output string, width int) []string {
	if output == "" {
		noOutput := "    " + ansiFgMuted + "⎿" + ansiReset + " " + ansiFgDim + i18n.T("classic_chat.collapse.no_output") + ansiReset
		return []string{shared.ReapplyBackground(noOutput, w.config.BackgroundColor)}
	}

	lines := strings.Split(output, "\n")
	var result []string

	// Styles for different elements
	fileHeaderStyle := ansiFgBlue + ansiBold
	lineNumStyle := ansiFgDim
	matchCountStyle := "\033[32m" // green

	// Track state
	var currentFile string
	var matchesInFile int
	var summaryLine string
	inFileSection := false

	// Calculate widths
	codeWidth := width - 16 // Account for indentation and line numbers
	if codeWidth < 30 {
		codeWidth = 30
	}

	// Parse grep output
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Detect summary line: "Found N matches for pattern..."
		if strings.HasPrefix(trimmed, "Found ") && strings.Contains(trimmed, " matches for ") {
			summaryLine = trimmed
			// Highlight the match count in green
			summaryLine = strings.Replace(summaryLine, "Found ", matchCountStyle+"Found ", 1)
			if idx := strings.Index(summaryLine, " matches"); idx != -1 {
				summaryLine = summaryLine[:idx] + ansiReset + summaryLine[idx:]
			}
			result = append(result, "    "+ansiFgMuted+"⎿"+ansiReset+" "+summaryLine)
			continue
		}

		// Detect "No matches found" line
		if strings.HasPrefix(trimmed, "No matches found") {
			result = append(result, "    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+trimmed+ansiReset)
			continue
		}

		// Detect separator line: "---"
		if trimmed == "---" {
			if inFileSection && currentFile != "" {
				// Finished a file section
				inFileSection = false
				currentFile = ""
				matchesInFile = 0
			}
			continue
		}

		// Detect file header: "File: /path/to/file"
		if after, ok := strings.CutPrefix(trimmed, "File: "); ok {
			currentFile = after
			inFileSection = true
			matchesInFile = 0

			// Detect language from file extension
			lang := detectLanguageFromPath(currentFile)
			_ = lang // Will be used for syntax highlighting

			// Render file header with icon and styling
			fileIcon := "📄"
			fileDisplay := fileHeaderStyle + fileIcon + " " + currentFile + ansiReset

			// Add blank line before file header (except first one)
			if i > 1 {
				result = append(result, "")
			}
			result = append(result, "      "+fileDisplay)
			continue
		}

		// Detect match line: "L123: content"
		if strings.HasPrefix(trimmed, "L") && strings.Contains(trimmed, ": ") {
			parts := strings.SplitN(trimmed, ": ", 2)
			if len(parts) == 2 {
				lineNum := strings.TrimPrefix(parts[0], "L")
				content := parts[1]

				// Detect language from current file
				lang := detectLanguageFromPath(currentFile)

				// Apply syntax highlighting to the code content
				highlightedCode := highlightLineANSI(content, lang, codeWidth, w.config.BackgroundColor)

				// Truncate if too long
				if ansi.StringWidth(highlightedCode) > codeWidth {
					highlightedCode = ansi.Truncate(highlightedCode, codeWidth-3, "") + ansiFgDim + "..." + ansiReset
				}

				// Build the line with connector for first match in file
				var rendered string
				if matchesInFile == 0 {
					rendered = "      " + ansiFgMuted + "⎿" + ansiReset + " " + lineNumStyle + "L" + lineNum + ansiReset + "  " + highlightedCode
				} else {
					rendered = "        " + lineNumStyle + "L" + lineNum + ansiReset + "  " + highlightedCode
				}

				result = append(result, rendered)
				matchesInFile++
				continue
			}
		}

		// Detect note/warning lines
		if strings.HasPrefix(trimmed, "Note:") || strings.HasPrefix(trimmed, "Warning:") {
			result = append(result, "")
			result = append(result, "      "+ansiFgDim+trimmed+ansiReset)
			continue
		}

		// Fallback: render as plain text
		if len(result) == 0 {
			result = append(result, "    "+ansiFgMuted+"⎿"+ansiReset+" "+trimmed)
		} else {
			result = append(result, "      "+trimmed)
		}
	}

	// If no results were generated, show raw output
	if len(result) == 0 {
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if i == 0 {
				result = append(result, "    "+ansiFgMuted+"⎿"+ansiReset+" "+line)
			} else {
				result = append(result, "      "+line)
			}
		}
	}

	// Apply background to all lines
	for i := range result {
		result[i] = shared.ReapplyBackground(result[i], w.config.BackgroundColor)
	}
	return result
}

// RenderError renders tool error output based on collapse state
func (w *CollapseWidget) RenderError(state *ToolCallState, errorMsg string, width int) ([]string, int) {
	if state == nil || errorMsg == "" {
		return nil, 0
	}

	lines := strings.Split(errorMsg, "\n")
	totalLines := len(lines)

	// Styles
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ErrorColor)).Background(lipgloss.Color(w.config.BackgroundColor))
	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor)).Background(lipgloss.Color(w.config.BackgroundColor))

	var result []string

	switch state.CollapseLevel {
	case CollapseLevelCollapsed:
		// Still show first line of error - errors are important
		if len(lines) > 0 {
			errorLine := "    " + connectorStyle.Render("⎿") + " " + errorStyle.Render(i18n.T("chat_b.tool.error_line", lines[0]))
			errorLine = shared.ReapplyBackground(errorLine, w.config.BackgroundColor)
			result = append(result, errorLine)
		}
		return result, totalLines - 1

	case CollapseLevelCompact:
		// Show first N lines
		previewCount := w.config.PreviewLines
		if previewCount > totalLines {
			previewCount = totalLines
		}

		for i := 0; i < previewCount; i++ {
			line := lines[i]
			if i == 0 {
				errorLine := "    " + connectorStyle.Render("⎿") + " " + errorStyle.Render(i18n.T("chat_b.tool.error_line", line))
				errorLine = shared.ReapplyBackground(errorLine, w.config.BackgroundColor)
				result = append(result, errorLine)
			} else {
				restLine := "      " + errorStyle.Render(line)
				restLine = shared.ReapplyBackground(restLine, w.config.BackgroundColor)
				result = append(result, restLine)
			}
		}

		remaining := totalLines - previewCount
		return result, remaining

	case CollapseLevelFull:
		// Show all lines
		for i, line := range lines {
			if i == 0 {
				errorLine := "    " + connectorStyle.Render("⎿") + " " + errorStyle.Render(i18n.T("chat_b.tool.error_line", line))
				errorLine = shared.ReapplyBackground(errorLine, w.config.BackgroundColor)
				result = append(result, errorLine)
			} else {
				restLine := "      " + errorStyle.Render(line)
				restLine = shared.ReapplyBackground(restLine, w.config.BackgroundColor)
				result = append(result, restLine)
			}
		}
		return result, 0
	}

	return nil, 0
}

// RenderMoreIndicator renders the "▶ N more lines" indicator
func (w *CollapseWidget) RenderMoreIndicator(remainingLines int, level CollapseLevel) string {
	if remainingLines <= 0 {
		return ""
	}

	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.config.MetadataColor)).
		Background(lipgloss.Color(w.config.BackgroundColor)).
		Italic(true)

	hint := i18n.T("classic_chat.collapse.more_hint")
	if level == CollapseLevelCollapsed {
		result := "      " + metaStyle.Render(i18n.T("classic_chat.collapse.lines", remainingLines, hint))
		return shared.ReapplyBackground(result, w.config.BackgroundColor)
	}

	result := "      " + metaStyle.Render(i18n.T("classic_chat.collapse.more_lines", remainingLines, hint))
	return shared.ReapplyBackground(result, w.config.BackgroundColor)
}

// leftTruncatePath truncates a file path from the LEFT so the filename (rightmost
// component) stays visible. E.g. "/a/b/c/d/file.go" → "...c/d/file.go".
func leftTruncatePath(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[len(s)-maxLen:]
	}
	return "..." + s[len(s)-(maxLen-3):]
}

// truncateDisplayString truncates a string to maxLen, adding "..." if truncated
func truncateDisplayString(s string, maxLen int) string {
	// Remove newlines for display
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ") // Collapse whitespace

	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
