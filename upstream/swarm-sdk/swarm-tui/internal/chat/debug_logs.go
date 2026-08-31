package chat

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/envelope"
)

var (
	logTimestampRE = regexp.MustCompile(`\[(\d{2}:\d{2}:\d{2}\.\d{3})\]`)
	logCategoryRE  = regexp.MustCompile(`\[([A-Z][A-Z0-9_-]*)\]`)
)

// LogLevel represents the severity of a log entry
type LogLevel int

// LogType represents the type/category of log entry for visual differentiation
type LogType int

const (
	LogTypeUnknown      LogType = iota
	LogTypeSubAgent             // Sub-agent related logs
	LogTypeTool                 // Tool call/result logs
	LogTypeAPI                  // API request/response logs
	LogTypeConversation         // Conversation build/change logs
	LogTypeState                // State change logs
	LogTypeSDK                  // SDK related logs
	LogTypeMCP                  // MCP server logs
	LogTypeSystem               // System/general logs
	LogTypeHook                 // Hook execution logs
	LogTypeWorkflow             // Workflow related logs
	LogTypeUI                   // UI related logs
	LogTypeError                // Error-specific logs
)

// String returns the string representation of LogType
func (l LogType) String() string {
	switch l {
	case LogTypeSubAgent:
		return "SUBAGENT"
	case LogTypeTool:
		return "TOOL"
	case LogTypeAPI:
		return "API"
	case LogTypeConversation:
		return "CONV"
	case LogTypeState:
		return "STATE"
	case LogTypeSDK:
		return "SDK"
	case LogTypeMCP:
		return "MCP"
	case LogTypeSystem:
		return "SYSTEM"
	case LogTypeHook:
		return "HOOK"
	case LogTypeWorkflow:
		return "WORKFLOW"
	case LogTypeUI:
		return "UI"
	case LogTypeError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Icon returns the icon for this log type
func (l LogType) Icon() string {
	switch l {
	case LogTypeSubAgent:
		return "◈" // Diamond for sub-agents
	case LogTypeTool:
		return "▸" // Arrow for tools
	case LogTypeAPI:
		return "◉" // Circle for API
	case LogTypeConversation:
		return "◎" // Bullseye for conversation
	case LogTypeState:
		return "◆" // Diamond solid for state
	case LogTypeSDK:
		return "◇" // Outline diamond for SDK
	case LogTypeMCP:
		return "□" // Square for MCP
	case LogTypeSystem:
		return "•" // Bullet for system
	case LogTypeHook:
		return "⚓" // Anchor for hooks
	case LogTypeWorkflow:
		return "⚙" // Gear for workflows
	case LogTypeUI:
		return "◐" // Half-circle for UI
	case LogTypeError:
		return "✘" // X for errors
	default:
		return " " // Space for unknown
	}
}

// Color returns the color for this log type
func (l LogType) Color() string {
	switch l {
	case LogTypeSubAgent:
		return "#39D2C0" // Cyan - matches ColorSubAgentIdentity
	case LogTypeTool:
		return "#F9E2AF" // Yellow/gold for tools
	case LogTypeAPI:
		return "#89B4FA" // Blue for API
	case LogTypeConversation:
		return "#CBA6F7" // Purple for conversation
	case LogTypeState:
		return "#A6E3A1" // Green for state
	case LogTypeSDK:
		return "#74C7EC" // Light blue for SDK
	case LogTypeMCP:
		return "#FAB387" // Orange for MCP
	case LogTypeSystem:
		return "#CDD6F4" // White/gray for system
	case LogTypeHook:
		return "#F5C2E7" // Pink for hooks
	case LogTypeWorkflow:
		return "#B4BEFE" // Lavender for workflows
	case LogTypeUI:
		return "#94E2D5" // Teal for UI
	case LogTypeError:
		return "#F38BA8" // Red for errors
	default:
		return "#6C7086" // Gray for unknown
	}
}

const (
	LogLevelTrace LogLevel = iota
	LogLevelDebug
	LogLevelInfo
	LogLevelWarn
	LogLevelError
	LogLevelFatal
	LogLevelUnknown
)

func (l LogLevel) String() string {
	switch l {
	case LogLevelTrace:
		return "TRACE"
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelInfo:
		return "INFO"
	case LogLevelWarn:
		return "WARN"
	case LogLevelError:
		return "ERROR"
	case LogLevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// LogEntry represents a structured log entry with parsed metadata
type LogEntry struct {
	Timestamp time.Time
	Level     LogLevel
	Category  string // e.g., "SDK", "MCP", "STATE", "API"
	Message   string
	Raw       string // Original unparsed message

	// Enhanced fields for visual differentiation
	LogType     LogType // Type of log entry for icon/color
	IndentLevel int     // Indentation level for nested operations
	ParentID    string  // ID of parent operation (for hierarchy)
	EntityID    string  // ID of entity this log relates to (sub-agent ID, tool ID, etc.)
	EntityName  string  // Name of entity (sub-agent name, tool name, etc.)
	DurationMs  int     // Duration in milliseconds (for timing visualization)
	RelatedID   string  // ID of related operation (tool call→result pairing)
}

// ParseLogMessage parses a raw log message into structured fields
func ParseLogMessage(raw string) LogEntry {
	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     LogLevelUnknown,
		Category:  "GENERAL",
		Message:   raw,
		Raw:       raw,
	}

	// Extract timestamp [HH:MM:SS.mmm]
	if matches := logTimestampRE.FindStringSubmatch(raw); len(matches) > 1 {
		// Parse time (assuming today's date)
		if t, err := time.Parse("15:04:05.000", matches[1]); err == nil {
			now := time.Now()
			entry.Timestamp = time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), now.Location())
		}
	}

	// Extract log level
	upperRaw := strings.ToUpper(raw)
	switch {
	case strings.Contains(upperRaw, "TRACE"):
		entry.Level = LogLevelTrace
	case strings.Contains(upperRaw, "DEBUG") || strings.Contains(upperRaw, "DBG"):
		entry.Level = LogLevelDebug
	case strings.Contains(upperRaw, "INFO"):
		entry.Level = LogLevelInfo
	case strings.Contains(upperRaw, "WARN"):
		entry.Level = LogLevelWarn
	case strings.Contains(upperRaw, "ERROR") || strings.Contains(upperRaw, "ERR") || strings.Contains(upperRaw, "FAIL"):
		entry.Level = LogLevelError
	case strings.Contains(upperRaw, "FATAL"):
		entry.Level = LogLevelFatal
	}

	// Extract category [CATEGORY]
	matches := logCategoryRE.FindAllStringSubmatch(raw, -1)
	for _, match := range matches {
		if len(match) > 1 {
			candidate := match[1]
			// Skip timestamps and log levels
			if !logTimestampRE.MatchString("["+candidate+"]") &&
				candidate != entry.Level.String() {
				entry.Category = candidate
				break
			}
		}
	}

	return entry
}

// CircularLogBuffer implements a fixed-size circular buffer for log entries
type CircularLogBuffer struct {
	entries  []LogEntry
	capacity int
	writePos int
	full     bool
	size     int // Current number of entries
}

// NewCircularLogBuffer creates a new circular log buffer with the given capacity
func NewCircularLogBuffer(capacity int) *CircularLogBuffer {
	if capacity < 100 {
		capacity = 100 // Minimum capacity
	}
	if capacity > 50000 {
		capacity = 50000 // Maximum capacity
	}
	return &CircularLogBuffer{
		entries:  make([]LogEntry, capacity),
		capacity: capacity,
		writePos: 0,
		full:     false,
		size:     0,
	}
}

// Add appends a log entry to the buffer, overwriting oldest if full
func (b *CircularLogBuffer) Add(entry LogEntry) {
	b.entries[b.writePos] = entry
	b.writePos = (b.writePos + 1) % b.capacity

	if b.writePos == 0 {
		b.full = true
	}

	if !b.full {
		b.size++
	}
}

// GetAll returns all log entries in chronological order
// Note: When buffer is not full, returns a slice of the internal array (no allocation)
// When buffer is full, returns a reordered copy
func (b *CircularLogBuffer) GetAll() []LogEntry {
	if !b.full {
		// Not full - return slice of internal array (no allocation)
		return b.entries[:b.size]
	}

	// Buffer is full, need to reorder
	result := make([]LogEntry, b.capacity)
	copy(result, b.entries[b.writePos:])
	copy(result[b.capacity-b.writePos:], b.entries[:b.writePos])
	return result
}

// GetVisible returns only the visible entries based on offset and limit
// This avoids allocating/copying the entire buffer for rendering
func (b *CircularLogBuffer) GetVisible(offset, limit int) []LogEntry {
	if b.size == 0 {
		return nil
	}

	// Get the logical start position
	var start int
	if b.full {
		start = b.writePos
	} else {
		start = 0
	}

	// Apply offset
	start = (start + offset) % b.capacity
	if offset >= b.size {
		return nil
	}

	count := b.size - offset
	if count > limit {
		count = limit
	}

	result := make([]LogEntry, count)
	for i := 0; i < count; i++ {
		idx := (start + i) % b.capacity
		result[i] = b.entries[idx]
	}
	return result
}

// Size returns the current number of entries in the buffer
func (b *CircularLogBuffer) Size() int {
	return b.size
}

// Capacity returns the maximum capacity of the buffer
func (b *CircularLogBuffer) Capacity() int {
	return b.capacity
}

// IsFull returns true if the buffer is at capacity
func (b *CircularLogBuffer) IsFull() bool {
	return b.full
}

// Clear removes all entries from the buffer
func (b *CircularLogBuffer) Clear() {
	b.writePos = 0
	b.full = false
	b.size = 0
}

// SetCapacity resizes the buffer (preserves most recent entries)
func (b *CircularLogBuffer) SetCapacity(newCapacity int) {
	if newCapacity < 100 {
		newCapacity = 100
	}
	if newCapacity > 50000 {
		newCapacity = 50000
	}

	// Get current entries
	current := b.GetAll()

	// Keep only the most recent entries if reducing size
	if len(current) > newCapacity {
		current = current[len(current)-newCapacity:]
	}

	// Create new buffer
	b.entries = make([]LogEntry, newCapacity)
	b.capacity = newCapacity
	b.writePos = 0
	b.full = false
	b.size = 0

	// Re-add entries
	for _, entry := range current {
		b.Add(entry)
	}
}

// LogFilter defines filtering criteria for log entries
type LogFilter struct {
	EnabledLevels     map[LogLevel]bool // Which levels to show
	EnabledCategories map[string]bool   // Which categories to show
	SearchText        string            // Text search (case-insensitive)
	ShowAllCategories bool              // If true, show all categories
}

// NewLogFilter creates a filter with all levels and categories enabled
func NewLogFilter() *LogFilter {
	return &LogFilter{
		EnabledLevels: map[LogLevel]bool{
			LogLevelTrace:   true,
			LogLevelDebug:   true,
			LogLevelInfo:    true,
			LogLevelWarn:    true,
			LogLevelError:   true,
			LogLevelFatal:   true,
			LogLevelUnknown: true,
		},
		EnabledCategories: make(map[string]bool),
		SearchText:        "",
		ShowAllCategories: true,
	}
}

// Matches returns true if the entry passes the filter
func (f *LogFilter) Matches(entry LogEntry) bool {
	// Check level filter
	if !f.EnabledLevels[entry.Level] {
		return false
	}

	// Check category filter
	if !f.ShowAllCategories {
		if !f.EnabledCategories[entry.Category] {
			return false
		}
	}

	// Check text search
	if f.SearchText != "" {
		searchLower := strings.ToLower(f.SearchText)
		rawLower := strings.ToLower(entry.Raw)
		if !strings.Contains(rawLower, searchLower) {
			return false
		}
	}

	return true
}

// DiscoverCategories extracts all unique categories from log entries
func DiscoverCategories(entries []LogEntry) []string {
	categoryMap := make(map[string]bool)
	for _, entry := range entries {
		categoryMap[entry.Category] = true
	}

	categories := make([]string, 0, len(categoryMap))
	for cat := range categoryMap {
		categories = append(categories, cat)
	}
	return categories
}

// FilterPanel represents the interactive filter configuration UI
type FilterPanel struct {
	visible        bool
	width          int
	height         int
	filter         *LogFilter
	searchText     string // Simple string input
	searchCursor   int    // Cursor position in search text
	focusedField   int    // 0=levels, 1=categories, 2=search
	levelCursor    int    // Which level is selected
	categoryCursor int    // Which category is selected
	categories     []string
	theme          Theme
}

// NewFilterPanel creates a new filter panel
func NewFilterPanel(filter *LogFilter, categories []string) *FilterPanel {
	return &FilterPanel{
		visible:        false,
		filter:         filter,
		searchText:     "",
		searchCursor:   0,
		focusedField:   0,
		levelCursor:    0,
		categoryCursor: 0,
		categories:     categories,
	}
}

// Update handles filter panel input
func (fp *FilterPanel) Update(msg tea.Msg) tea.Cmd {
	if !fp.visible {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "f":
			fp.visible = false
			return nil

		case "tab":
			fp.focusedField = (fp.focusedField + 1) % 3

		case "shift+tab":
			fp.focusedField = (fp.focusedField + 2) % 3

		case "up", "k":
			switch fp.focusedField {
			case 0: // Levels
				if fp.levelCursor > 0 {
					fp.levelCursor--
				}
			case 1: // Categories
				if fp.categoryCursor > 0 {
					fp.categoryCursor--
				}
			}

		case "down", "j":
			switch fp.focusedField {
			case 0: // Levels
				if fp.levelCursor < 6 {
					fp.levelCursor++
				}
			case 1: // Categories
				if fp.categoryCursor < len(fp.categories) {
					fp.categoryCursor++
				}
			}

		case "enter", " ":
			switch fp.focusedField {
			case 0: // Toggle level
				level := LogLevel(fp.levelCursor)
				fp.filter.EnabledLevels[level] = !fp.filter.EnabledLevels[level]

			case 1: // Toggle category
				if fp.categoryCursor == 0 {
					// "All Categories" option
					fp.filter.ShowAllCategories = !fp.filter.ShowAllCategories
				} else if fp.categoryCursor <= len(fp.categories) {
					cat := fp.categories[fp.categoryCursor-1]
					fp.filter.EnabledCategories[cat] = !fp.filter.EnabledCategories[cat]
					fp.filter.ShowAllCategories = false
				}
			}

		case "r":
			// Reset all filters
			fp.filter = NewLogFilter()
			fp.searchText = ""
			fp.filter.SearchText = ""

		// Quick filter keys (NEW)
		case "1":
			// Toggle Sub-Agent filter
			fp.filter.ShowAllCategories = false
			fp.toggleCategoryByPattern("SUBAGENT", "AGENT", "AGENTDISPATCH")
		case "2":
			// Toggle Tool filter
			fp.filter.ShowAllCategories = false
			fp.toggleCategoryByPattern("TOOL", "TOOLCALL", "TOOLRESULT")
		case "3":
			// Toggle API filter
			fp.filter.ShowAllCategories = false
			fp.toggleCategoryByPattern("API", "HTTP", "SSE")
		case "4":
			// Toggle Conversation filter
			fp.filter.ShowAllCategories = false
			fp.toggleCategoryByPattern("CONV", "CONVERSATION", "MESSAGE")
		case "5":
			// Toggle Error filter (show only errors)
			fp.filter.ShowAllCategories = true
			// Disable all levels except Error and Fatal
			for level := range fp.filter.EnabledLevels {
				fp.filter.EnabledLevels[level] = false
			}
			fp.filter.EnabledLevels[LogLevelError] = true
			fp.filter.EnabledLevels[LogLevelFatal] = true

		case "backspace", "delete":
			// Handle text input in search field
			if fp.focusedField == 2 && len(fp.searchText) > 0 {
				fp.searchText = fp.searchText[:len(fp.searchText)-1]
				fp.filter.SearchText = fp.searchText
			}

		default:
			// Handle regular character input for search
			if fp.focusedField == 2 {
				// Check if it's a printable character
				if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] < 127 {
					fp.searchText += msg.String()
					fp.filter.SearchText = fp.searchText
				}
			}
		}
	}

	return nil
}

// View renders the filter panel
func (fp *FilterPanel) View() string {
	if !fp.visible {
		return ""
	}

	th := fp.theme

	var sections []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 1)
	sections = append(sections, titleStyle.Render("═══ LOG FILTERS ═══"))

	// Quick Filters Section (NEW)
	quickStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#A6E3A1")).
		Bold(true)
	sections = append(sections, "")
	sections = append(sections, quickStyle.Render("Quick Filters:"))

	// Quick filter buttons
	quickFilters := []struct {
		key   string
		icon  string
		label string
		color string
	}{
		{"1", LogTypeSubAgent.Icon(), "Sub-Agents", LogTypeSubAgent.Color()},
		{"2", LogTypeTool.Icon(), "Tools", LogTypeTool.Color()},
		{"3", LogTypeAPI.Icon(), "API", LogTypeAPI.Color()},
		{"4", LogTypeConversation.Icon(), "Conversation", LogTypeConversation.Color()},
		{"5", LogTypeError.Icon(), "Errors", LogTypeError.Color()},
	}

	for _, qf := range quickFilters {
		iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(qf.color))
		line := fmt.Sprintf("  [%s] %s %s", qf.key, iconStyle.Render(qf.icon), qf.label)
		sections = append(sections, line)
	}

	// Log Levels Section
	sections = append(sections, "")
	levelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#89B4FA")).
		Bold(true)
	sections = append(sections, levelStyle.Render("Log Levels:"))

	levels := []LogLevel{
		LogLevelTrace, LogLevelDebug, LogLevelInfo,
		LogLevelWarn, LogLevelError, LogLevelFatal,
	}

	for i, level := range levels {
		cursor := "  "
		if fp.focusedField == 0 && fp.levelCursor == i {
			cursor = "→ "
		}

		checkbox := "[ ]"
		if fp.filter.EnabledLevels[level] {
			checkbox = "[✓]"
		}

		levelColor := getLevelColor(level)
		levelNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(levelColor))

		line := cursor + checkbox + " " + levelNameStyle.Render(level.String())
		sections = append(sections, line)
	}

	// Categories Section with Icons
	sections = append(sections, "")
	catStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#CBA6F7")).
		Bold(true)
	sections = append(sections, catStyle.Render("Categories:"))

	// "All Categories" option
	cursor := "  "
	if fp.focusedField == 1 && fp.categoryCursor == 0 {
		cursor = "→ "
	}
	checkbox := "[ ]"
	if fp.filter.ShowAllCategories {
		checkbox = "[✓]"
	}
	sections = append(sections, cursor+checkbox+" All Categories")

	// Individual categories with icons
	for i, cat := range fp.categories {
		cursor = "  "
		if fp.focusedField == 1 && fp.categoryCursor == i+1 {
			cursor = "→ "
		}

		checkbox = "[ ]"
		if fp.filter.EnabledCategories[cat] && !fp.filter.ShowAllCategories {
			checkbox = "[✓]"
		}

		// Determine log type and icon for this category
		logType := LogTypeUnknown
		switch cat {
		case "SUBAGENT", "AGENT", "AGENTDISPATCH", "AGENTINNER":
			logType = LogTypeSubAgent
		case "TOOL", "TOOLCALL", "TOOLRESULT":
			logType = LogTypeTool
		case "API", "HTTP", "SSE":
			logType = LogTypeAPI
		case "CONV", "CONVERSATION", "MESSAGE":
			logType = LogTypeConversation
		case "SDK":
			logType = LogTypeSDK
		case "STATE":
			logType = LogTypeState
		case "HOOK":
			logType = LogTypeHook
		case "MCP":
			logType = LogTypeMCP
		case "WORKFLOW":
			logType = LogTypeWorkflow
		case "UI":
			logType = LogTypeUI
		case "ERROR":
			logType = LogTypeError
		}

		icon := logType.Icon()
		iconColor := logType.Color()
		iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(iconColor))

		line := cursor + checkbox + " " + iconStyle.Render(icon) + " " + cat
		sections = append(sections, line)
	}

	// Search Section
	sections = append(sections, "")
	searchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F9E2AF")).
		Bold(true)
	sections = append(sections, searchStyle.Render("Text Search:"))

	cursor = "  "
	if fp.focusedField == 2 {
		cursor = "→ "
	}

	// Simple text input display
	searchDisplay := fp.searchText
	if searchDisplay == "" {
		searchDisplay = "(type to search...)"
	}
	if fp.focusedField == 2 {
		searchDisplay += "█" // Show cursor
	}
	sections = append(sections, cursor+searchDisplay)

	// Instructions
	sections = append(sections, "")
	instrStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true)
	sections = append(sections, instrStyle.Render("Tab: Switch section | ↑↓: Navigate | Space: Toggle | 1-5: Quick Filter | R: Reset | F/Esc: Close"))

	content := strings.Join(sections, "\n")

	// Wrap in bordered box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Padding(1, 2).
		Width(min(fp.width, 65))

	return boxStyle.Render(content)
}

// Show displays the filter panel
func (fp *FilterPanel) Show() {
	fp.visible = true
}

// Hide closes the filter panel
func (fp *FilterPanel) Hide() {
	fp.visible = false
}

// IsVisible returns whether the panel is shown
func (fp *FilterPanel) IsVisible() bool {
	return fp.visible
}

// SetSize updates the panel dimensions
func (fp *FilterPanel) SetSize(width, height int) {
	fp.width = width
	fp.height = height
}

// SetTheme updates the theme
func (fp *FilterPanel) SetTheme(theme Theme) {
	fp.theme = theme
}

// UpdateCategories refreshes the available categories
func (fp *FilterPanel) UpdateCategories(categories []string) {
	fp.categories = categories
}

// toggleCategoryByPattern toggles all categories matching any of the given patterns
func (fp *FilterPanel) toggleCategoryByPattern(patterns ...string) {
	// Check if any matching category is already enabled
	anyEnabled := false
	for _, cat := range fp.categories {
		for _, pattern := range patterns {
			if strings.Contains(cat, pattern) && fp.filter.EnabledCategories[cat] {
				anyEnabled = true
				break
			}
		}
		if anyEnabled {
			break
		}
	}

	// If any are enabled, disable all (toggle off). Otherwise enable all (toggle on).
	targetState := !anyEnabled

	for _, cat := range fp.categories {
		for _, pattern := range patterns {
			if strings.Contains(cat, pattern) {
				fp.filter.EnabledCategories[cat] = targetState
				break
			}
		}
	}
}

// getLevelColor returns the color for a log level
func getLevelColor(level LogLevel) string {
	switch level {
	case LogLevelTrace:
		return "#6C7086" // Gray
	case LogLevelDebug:
		return "#89B4FA" // Blue
	case LogLevelInfo:
		return "#A6E3A1" // Green
	case LogLevelWarn:
		return "#FAB387" // Orange
	case LogLevelError:
		return "#F38BA8" // Red
	case LogLevelFatal:
		return "#F38BA8" // Red
	default:
		return "#CDD6F4" // White
	}
}

// EnhancedLogsView provides an efficient, filterable log viewer
type EnhancedLogsView struct {
	buffer       *CircularLogBuffer
	filter       *LogFilter
	filterPanel  *FilterPanel
	scrollOffset int // Manual scroll position
	width        int
	height       int
	theme        Theme
	cachedStyles map[LogLevel]lipgloss.Style // Style cache for performance

	autoFollow   bool // Stick to the newest logs (bottom) as they stream in
	severityMode int  // Quick severity cycle: 0=ALL 1=INFO+ 2=WARN+ 3=ERROR+

	// Inline '/' search state. searchQuery highlights + jumps between matches
	// (via n/N) WITHOUT filtering the list, so surrounding context stays visible.
	searching   bool
	searchQuery string
	matches     []int // display-order indices of matching entries
	matchCursor int   // index into matches for n/N navigation

	// View caching (Crush technique)
	cachedView       string
	cachedViewOffset int
	cachedViewDirty  bool
	cachedBufferSize int // Track buffer size changes
}

// NewEnhancedLogsView creates a new enhanced logs viewer
func NewEnhancedLogsView(capacity int) *EnhancedLogsView {
	buffer := NewCircularLogBuffer(capacity)
	filter := NewLogFilter()
	filterPanel := NewFilterPanel(filter, []string{})

	// Pre-create styles for each log level (performance optimization)
	cachedStyles := make(map[LogLevel]lipgloss.Style)
	for level := LogLevelTrace; level <= LogLevelFatal; level++ {
		cachedStyles[level] = lipgloss.NewStyle().
			Foreground(lipgloss.Color(getLevelColor(level)))
	}

	return &EnhancedLogsView{
		buffer:       buffer,
		filter:       filter,
		filterPanel:  filterPanel,
		scrollOffset: 0,
		autoFollow:   true, // Follow newest logs by default (most common need)
		cachedStyles: cachedStyles,
	}
}

// AddLog adds a new log message
func (elv *EnhancedLogsView) AddLog(message string) {
	entry := ParseLogMessage(message)
	elv.buffer.Add(entry)
	elv.cachedViewDirty = true // Mark cache dirty on new log

	// Only update categories if this is a new one (avoid expensive full scan)
	// Check if category already known
	isNewCategory := !slices.Contains(elv.filterPanel.categories, entry.Category)
	if isNewCategory {
		// New category found - add it directly
		elv.filterPanel.categories = append(elv.filterPanel.categories, entry.Category)
	}
}

// Update handles messages
func (elv *EnhancedLogsView) Update(msg tea.Msg) tea.Cmd {
	// Filter panel gets priority
	if elv.filterPanel.IsVisible() {
		elv.cachedViewDirty = true // Filter changes invalidate cache
		return elv.filterPanel.Update(msg)
	}

	// While the inline '/' search prompt is active it consumes all keys until
	// the user confirms (enter) or cancels (esc).
	if elv.searching {
		if km, ok := msg.(tea.KeyMsg); ok {
			elv.handleSearchKey(km)
		}
		return nil
	}

	// Handle manual scrolling
	switch msg := msg.(type) {
	case tea.KeyMsg:
		oldOffset := elv.scrollOffset
		switch msg.String() {
		case "j", "down":
			elv.scrollOffset += 3
		case "k", "up":
			if elv.scrollOffset > 0 {
				elv.scrollOffset -= 3
				if elv.scrollOffset < 0 {
					elv.scrollOffset = 0
				}
			}
			elv.autoFollow = false // scrolling up pauses follow
		case "ctrl+d":
			elv.scrollOffset += 10
		case "ctrl+u":
			if elv.scrollOffset > 0 {
				elv.scrollOffset -= 10
				if elv.scrollOffset < 0 {
					elv.scrollOffset = 0
				}
			}
			elv.autoFollow = false // scrolling up pauses follow
		case "g":
			elv.scrollOffset = 0
			elv.autoFollow = false // jumped to oldest; pause follow
		case "G":
			elv.scrollOffset = 999999 // Scroll to bottom
			elv.autoFollow = true     // jumped to newest; resume follow
		}
		// Mark cache dirty if scroll changed
		if elv.scrollOffset != oldOffset {
			elv.cachedViewDirty = true
		}
	}

	return nil
}

// FollowActive reports whether the view is pinned to the newest logs.
func (elv *EnhancedLogsView) FollowActive() bool { return elv.autoFollow }

// SearchActive reports whether the inline '/' search prompt is open.
func (elv *EnhancedLogsView) SearchActive() bool { return elv.searching }

// SearchQuery returns the current search text.
func (elv *EnhancedLogsView) SearchQuery() string { return elv.searchQuery }

// MatchInfo returns the 1-based current match position and the total match
// count (0,0 when there are no matches).
func (elv *EnhancedLogsView) MatchInfo() (int, int) {
	if len(elv.matches) == 0 {
		return 0, 0
	}
	return elv.matchCursor + 1, len(elv.matches)
}

// StartSearch opens the inline search prompt.
func (elv *EnhancedLogsView) StartSearch() {
	elv.searching = true
	elv.autoFollow = false // don't let new logs move the view while searching
	elv.cachedViewDirty = true
}

// ClearSearch cancels any active search and removes match highlighting.
func (elv *EnhancedLogsView) ClearSearch() {
	elv.searching = false
	elv.searchQuery = ""
	elv.matches = elv.matches[:0]
	elv.matchCursor = 0
	elv.cachedViewDirty = true
}

// handleSearchKey edits the live search query and recomputes matches as the
// user types. enter confirms and jumps to the first match; esc cancels+clears.
func (elv *EnhancedLogsView) handleSearchKey(km tea.KeyMsg) {
	switch km.String() {
	case "esc":
		elv.searching = false
		elv.searchQuery = ""
		elv.matches = elv.matches[:0]
		elv.matchCursor = 0
	case "enter":
		elv.searching = false
		elv.recomputeMatches()
		if len(elv.matches) > 0 {
			elv.matchCursor = 0
			elv.scrollToCurrentMatch()
		}
	case "backspace", "ctrl+h":
		if n := len(elv.searchQuery); n > 0 {
			elv.searchQuery = elv.searchQuery[:n-1]
			elv.recomputeMatches()
		}
	case "space":
		elv.searchQuery += " "
		elv.recomputeMatches()
	default:
		if s := km.String(); len([]rune(s)) == 1 {
			elv.searchQuery += s
			elv.recomputeMatches()
		}
	}
	elv.cachedViewDirty = true
}

// hasLevelOrCategoryFilter mirrors renderLogs' notion of an active filter,
// excluding the search term (search does not filter the list). It is used so
// match indices line up with the displayed (possibly level-filtered) list.
func (elv *EnhancedLogsView) hasLevelOrCategoryFilter() bool {
	f := elv.filter
	if !f.ShowAllCategories {
		return true
	}
	return !f.EnabledLevels[LogLevelTrace] || !f.EnabledLevels[LogLevelDebug] ||
		!f.EnabledLevels[LogLevelInfo] || !f.EnabledLevels[LogLevelWarn] ||
		!f.EnabledLevels[LogLevelError]
}

// recomputeMatches rebuilds the list of display-order indices whose raw text
// contains the (case-insensitive) search query.
func (elv *EnhancedLogsView) recomputeMatches() {
	elv.matches = elv.matches[:0]
	elv.matchCursor = 0
	if elv.searchQuery == "" {
		return
	}
	q := strings.ToLower(elv.searchQuery)
	hasFilter := elv.hasLevelOrCategoryFilter()
	displayIdx := 0
	for _, e := range elv.buffer.GetAll() {
		if hasFilter && !elv.filter.Matches(e) {
			continue
		}
		if strings.Contains(strings.ToLower(e.Raw), q) {
			elv.matches = append(elv.matches, displayIdx)
		}
		displayIdx++
	}
}

// NextMatch / PrevMatch cycle the viewport through search hits.
func (elv *EnhancedLogsView) NextMatch() {
	if len(elv.matches) == 0 {
		return
	}
	elv.matchCursor = (elv.matchCursor + 1) % len(elv.matches)
	elv.scrollToCurrentMatch()
}

func (elv *EnhancedLogsView) PrevMatch() {
	if len(elv.matches) == 0 {
		return
	}
	elv.matchCursor = (elv.matchCursor - 1 + len(elv.matches)) % len(elv.matches)
	elv.scrollToCurrentMatch()
}

// scrollToCurrentMatch brings the active match into view a few lines from the
// top and pauses follow so it stays put.
func (elv *EnhancedLogsView) scrollToCurrentMatch() {
	if len(elv.matches) == 0 {
		return
	}
	off := elv.matches[elv.matchCursor] - 2
	if off < 0 {
		off = 0
	}
	elv.scrollOffset = off
	elv.autoFollow = false
	elv.cachedViewDirty = true
}

// SeverityLabel returns the human-readable name of the current quick-severity mode.
func (elv *EnhancedLogsView) SeverityLabel() string {
	switch elv.severityMode {
	case 1:
		return "INFO+"
	case 2:
		return "WARN+"
	case 3:
		return "ERROR+"
	default:
		return "ALL"
	}
}

// CycleSeverity advances the one-key severity filter: ALL -> INFO+ -> WARN+ ->
// ERROR+ -> ALL. It replaces the old e/w/i separate-toggle keys with a single
// predictable cycle and applies the level set to the active filter.
func (elv *EnhancedLogsView) CycleSeverity() {
	elv.severityMode = (elv.severityMode + 1) % 4
	elv.applySeverityMode()
	elv.cachedViewDirty = true
}

// ResetSeverity clears the quick-severity filter back to ALL.
func (elv *EnhancedLogsView) ResetSeverity() {
	elv.severityMode = 0
	elv.cachedViewDirty = true
}

// applySeverityMode maps severityMode onto the filter's enabled log levels.
// Unknown/unparsed lines are always kept visible so raw output is never hidden.
func (elv *EnhancedLogsView) applySeverityMode() {
	f := elv.filter
	set := func(tr, db, in, wn, er, ft bool) {
		f.EnabledLevels[LogLevelTrace] = tr
		f.EnabledLevels[LogLevelDebug] = db
		f.EnabledLevels[LogLevelInfo] = in
		f.EnabledLevels[LogLevelWarn] = wn
		f.EnabledLevels[LogLevelError] = er
		f.EnabledLevels[LogLevelFatal] = ft
	}
	f.EnabledLevels[LogLevelUnknown] = true
	switch elv.severityMode {
	case 1: // INFO and above
		set(false, false, true, true, true, true)
	case 2: // WARN and above
		set(false, false, false, true, true, true)
	case 3: // ERROR and above
		set(false, false, false, false, true, true)
	default: // ALL
		set(true, true, true, true, true, true)
	}
}

// View renders the logs view
func (elv *EnhancedLogsView) View() string {
	// If filter panel is visible, show it as overlay
	if elv.filterPanel.IsVisible() {
		return lipgloss.Place(
			elv.width,
			elv.height,
			lipgloss.Center,
			lipgloss.Center,
			elv.filterPanel.View(),
		)
	}

	// Otherwise show logs
	return elv.renderLogs()
}

// renderLogs generates the log display
func (elv *EnhancedLogsView) renderLogs() string {
	visibleHeight := elv.height
	if visibleHeight < 1 {
		visibleHeight = 40
	}

	// Check if we have any filter active
	hasFilter := elv.filter.SearchText != "" || !elv.filter.ShowAllCategories ||
		!elv.filter.EnabledLevels[LogLevelTrace] ||
		!elv.filter.EnabledLevels[LogLevelDebug] ||
		!elv.filter.EnabledLevels[LogLevelInfo] ||
		!elv.filter.EnabledLevels[LogLevelWarn] ||
		!elv.filter.EnabledLevels[LogLevelError]

	// Fast path: return cached view if valid (Crush technique)
	if !elv.cachedViewDirty &&
		elv.cachedViewOffset == elv.scrollOffset &&
		elv.cachedBufferSize == elv.buffer.Size() &&
		!hasFilter &&
		elv.cachedView != "" {
		return elv.cachedView
	}

	var filtered []LogEntry
	var totalLines int

	if hasFilter {
		// With filter active, we need to process all entries to get correct count
		allLogs := elv.buffer.GetAll()
		filtered = make([]LogEntry, 0, len(allLogs)/2) // Pre-allocate reasonable size
		for _, entry := range allLogs {
			if elv.filter.Matches(entry) {
				filtered = append(filtered, entry)
			}
		}
		totalLines = len(filtered)
	} else {
		// No filter - can use buffer directly
		totalLines = elv.buffer.Size()
	}

	// Calculate scroll bounds
	maxScroll := totalLines - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	// Follow mode keeps the viewport pinned to the newest logs.
	if elv.autoFollow {
		elv.scrollOffset = maxScroll
	}
	if elv.scrollOffset > maxScroll {
		elv.scrollOffset = maxScroll
	}

	// Get only visible entries
	var visibleEntries []LogEntry
	start := elv.scrollOffset
	count := visibleHeight
	if start+count > totalLines {
		count = totalLines - start
	}
	if count < 0 {
		count = 0
	}

	if hasFilter {
		// Use pre-filtered slice
		if start < len(filtered) {
			end := start + count
			if end > len(filtered) {
				end = len(filtered)
			}
			visibleEntries = filtered[start:end]
		}
	} else {
		// Get visible entries directly from buffer
		visibleEntries = elv.buffer.GetVisible(start, count)
	}

	// Build visible lines with pre-sized string builder (Crush technique)
	var sb strings.Builder
	sb.Grow(len(visibleEntries) * 120) // Pre-size for typical log line length

	currentMatch := -1
	if elv.searchQuery != "" && len(elv.matches) > 0 {
		currentMatch = elv.matches[elv.matchCursor]
	}
	for i, entry := range visibleEntries {
		if i > 0 {
			sb.WriteByte('\n')
		}
		line := elv.formatLogEntry(entry)
		if start+i == currentMatch {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Bold(true).Render("▸ ") + line
		}
		sb.WriteString(line)
	}

	// Add scroll indicator if needed
	if totalLines > visibleHeight {
		percentage := 0
		if maxScroll > 0 {
			percentage = int((float64(elv.scrollOffset) / float64(maxScroll)) * 100)
		}
		scrollInfo := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6C7086")).
			Render(fmt.Sprintf(" [%d-%d of %d (%d%%)]",
				elv.scrollOffset+1,
				min(elv.scrollOffset+visibleHeight, totalLines),
				totalLines,
				percentage))
		sb.WriteByte('\n')
		sb.WriteString(scrollInfo)
	}

	content := sb.String()

	// Cache the result if no filter active (Crush technique)
	if !hasFilter {
		elv.cachedView = content
		elv.cachedViewOffset = elv.scrollOffset
		elv.cachedBufferSize = elv.buffer.Size()
		elv.cachedViewDirty = false
	}

	return content
}

// formatLogEntry formats a single log entry with enhanced visual differentiation
func (elv *EnhancedLogsView) formatLogEntry(entry LogEntry) string {
	// Build indentation based on indent level
	indent := strings.Repeat("  ", entry.IndentLevel)

	// Build tree connector based on indent level
	var treeConnector string
	if entry.IndentLevel > 0 {
		// Use tree characters for nested items
		treeConnector = "├─ "
		if entry.IndentLevel > 1 {
			// For deeper nesting, show continuing vertical line
			treeConnector = strings.Repeat("│  ", entry.IndentLevel-1) + "├─ "
		}
	}

	// Get icon for log type with its color
	icon := entry.LogType.Icon()
	iconColor := entry.LogType.Color()
	iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(iconColor))

	// Timestamp - compact format
	timestamp := entry.Timestamp.Format("15:04:05")
	timestampStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086"))

	// Level indicator (only show if not INFO to save space)
	var levelIndicator string
	if entry.Level != LogLevelInfo && entry.Level != LogLevelUnknown {
		levelStyle := elv.cachedStyles[entry.Level]
		levelStr := entry.Level.String()
		// Truncate level names for compactness
		switch entry.Level {
		case LogLevelTrace:
			levelStr = "TR"
		case LogLevelDebug:
			levelStr = "DB"
		case LogLevelWarn:
			levelStr = "WN"
		case LogLevelError:
			levelStr = "ER"
		case LogLevelFatal:
			levelStr = "FT"
		}
		levelIndicator = levelStyle.Render(levelStr) + " "
	}

	// Category - show abbreviated for common ones
	category := entry.Category
	switch category {
	case "SUBAGENT", "SUB_AGENT":
		category = "SA"
	case "CONVERSATION":
		category = "CONV"
	}
	categoryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF"))

	// Entity info if available
	var entityInfo string
	if entry.EntityName != "" {
		entityStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(iconColor)).
			Bold(true)
		entityInfo = entityStyle.Render(entry.EntityName) + " "
	} else if entry.EntityID != "" {
		// Show shortened ID if no name
		shortID := entry.EntityID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		entityStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(iconColor))
		entityInfo = entityStyle.Render(shortID) + " "
	}

	// Duration indicator if available
	var durationIndicator string
	if entry.DurationMs > 0 {
		durColor := "#A6E3A1" // Green for fast
		if entry.DurationMs > 1000 {
			durColor = "#F9E2AF" // Yellow for medium
		}
		if entry.DurationMs > 5000 {
			durColor = "#F38BA8" // Red for slow
		}
		durStr := fmt.Sprintf("[%dms] ", entry.DurationMs)
		durationIndicator = lipgloss.NewStyle().
			Foreground(lipgloss.Color(durColor)).
			Render(durStr)
	}

	// Build the formatted line
	// Format: indent [icon] [timestamp] [level] [category] entity message
	var parts []string

	// Start with indentation
	if indent != "" || treeConnector != "" {
		parts = append(parts, indent+treeConnector)
	}

	// Add icon
	parts = append(parts, iconStyle.Render(icon)+" ")

	// Add timestamp
	parts = append(parts, timestampStyle.Render(timestamp)+" ")

	// Add level indicator (if not empty)
	if levelIndicator != "" {
		parts = append(parts, levelIndicator)
	}

	// Add abbreviated category
	parts = append(parts, categoryStyle.Render("["+category+"]")+" ")

	// Add entity info if available
	if entityInfo != "" {
		parts = append(parts, entityInfo)
	}

	// Add duration if available
	if durationIndicator != "" {
		parts = append(parts, durationIndicator)
	}

	// Add message (potentially truncated or styled based on log type)
	message := entry.Message

	// For sub-agent logs, highlight lifecycle events
	if entry.LogType == LogTypeSubAgent {
		upperMsg := strings.ToUpper(message)
		if strings.Contains(upperMsg, "CREATED") || strings.Contains(upperMsg, "SPAWN") {
			msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1")) // Green
			message = msgStyle.Render(message)
		} else if strings.Contains(upperMsg, "COMPLETED") || strings.Contains(upperMsg, "DONE") {
			msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")) // Blue
			message = msgStyle.Render(message)
		}
	}

	parts = append(parts, message)

	return strings.Join(parts, "")
}

// SetSize updates dimensions
func (elv *EnhancedLogsView) SetSize(width, height int) {
	elv.width = width
	elv.height = height
	elv.filterPanel.SetSize(width, height)
}

// SetTheme updates the theme
func (elv *EnhancedLogsView) SetTheme(theme Theme) {
	elv.theme = theme
	elv.filterPanel.SetTheme(theme)
}

// ToggleFilterPanel shows/hides the filter panel
func (elv *EnhancedLogsView) ToggleFilterPanel() {
	if elv.filterPanel.IsVisible() {
		elv.filterPanel.Hide()
	} else {
		elv.filterPanel.Show()
	}
}

// GetStats returns buffer statistics
func (elv *EnhancedLogsView) GetStats() (current, capacity int, percentage float64, filtered int) {
	current = elv.buffer.Size()
	capacity = elv.buffer.Capacity()
	percentage = float64(current) / float64(capacity) * 100

	// Count filtered logs
	allLogs := elv.buffer.GetAll()
	for _, entry := range allLogs {
		if elv.filter.Matches(entry) {
			filtered++
		}
	}

	return
}

// ClearLogs clears all logs from the buffer
func (elv *EnhancedLogsView) ClearLogs() {
	elv.buffer.Clear()
}

// SetBufferCapacity changes the buffer size
func (elv *EnhancedLogsView) SetBufferCapacity(newCapacity int) {
	elv.buffer.SetCapacity(newCapacity)
}

// ════════════════════════════════════════════════════════════════════════════════
// Enhanced Raw Events View - High-performance SSE event viewer
// ════════════════════════════════════════════════════════════════════════════════

// RawEvent represents a single SSE event
type RawEvent struct {
	Timestamp string
	EventType string
	Data      string
	Raw       string
	Envelope  *envelope.Envelope // Transformed canonical data
}

// CircularEventBuffer implements a fixed-size circular buffer for raw events
type CircularEventBuffer struct {
	events   []RawEvent
	capacity int
	writePos int
	size     int
}

// NewCircularEventBuffer creates a new circular event buffer
func NewCircularEventBuffer(capacity int) *CircularEventBuffer {
	if capacity < 100 {
		capacity = 100
	}
	if capacity > 5000 {
		capacity = 5000
	}
	return &CircularEventBuffer{
		events:   make([]RawEvent, capacity),
		capacity: capacity,
	}
}

// Add appends an event, overwriting oldest if full
func (b *CircularEventBuffer) Add(event RawEvent) {
	b.events[b.writePos] = event
	b.writePos = (b.writePos + 1) % b.capacity
	if b.size < b.capacity {
		b.size++
	}
}

// Size returns current event count
func (b *CircularEventBuffer) Size() int {
	return b.size
}

// GetVisible returns only the visible events for the viewport
func (b *CircularEventBuffer) GetVisible(offset, limit int) []RawEvent {
	if b.size == 0 || offset >= b.size {
		return nil
	}

	// Calculate logical start (oldest event position)
	var logicalStart int
	if b.size == b.capacity {
		logicalStart = b.writePos // Buffer is full, oldest is at writePos
	} else {
		logicalStart = 0 // Buffer not full, oldest is at 0
	}

	// Apply offset from logical start
	start := (logicalStart + offset) % b.capacity
	count := b.size - offset
	if count > limit {
		count = limit
	}
	if count <= 0 {
		return nil
	}

	result := make([]RawEvent, count)
	for i := 0; i < count; i++ {
		idx := (start + i) % b.capacity
		result[i] = b.events[idx]
	}
	return result
}

// Clear removes all events
func (b *CircularEventBuffer) Clear() {
	b.writePos = 0
	b.size = 0
}

// EnhancedRawEventsView provides an efficient raw events viewer
type EnhancedRawEventsView struct {
	buffer       *CircularEventBuffer
	scrollOffset int
	width        int
	height       int
	theme        Theme
	autoScroll   bool

	// Pre-cached styles for performance
	eventNumStyle   lipgloss.Style
	timestampStyle  lipgloss.Style
	eventTypeStyle  lipgloss.Style
	dataStyle       lipgloss.Style
	scrollInfoStyle lipgloss.Style

	// View caching (Crush technique)
	cachedView       string
	cachedViewOffset int
	cachedViewDirty  bool
	cachedBufferSize int
}

// NewEnhancedRawEventsView creates a new raw events viewer
func NewEnhancedRawEventsView(capacity int) *EnhancedRawEventsView {
	return &EnhancedRawEventsView{
		buffer:          NewCircularEventBuffer(capacity),
		autoScroll:      true,
		cachedViewDirty: true,
		// Styles will be initialized in SetTheme
		eventNumStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true),
		timestampStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("#CBA6F7")),
		eventTypeStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1")),
		dataStyle:       lipgloss.NewStyle().Foreground(lipgloss.Color("#CDD6F4")),
		scrollInfoStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")),
	}
}

// AddEvent adds a new raw event
func (v *EnhancedRawEventsView) AddEvent(eventType, data string, env *envelope.Envelope) {
	event := RawEvent{
		Timestamp: time.Now().Format("15:04:05.000"),
		EventType: eventType,
		Data:      data,
		Raw:       fmt.Sprintf("[%s] [%s] %s", time.Now().Format("15:04:05.000"), eventType, data),
		Envelope:  env,
	}
	v.buffer.Add(event)
	v.cachedViewDirty = true

	// Auto-scroll to bottom when enabled
	if v.autoScroll {
		v.scrollOffset = 999999 // Will be clamped in View
	}
}

// Update handles key events
func (v *EnhancedRawEventsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		oldOffset := v.scrollOffset
		switch msg.String() {
		case "j", "down":
			v.scrollOffset += 3
			v.autoScroll = false
		case "k", "up":
			v.scrollOffset -= 3
			if v.scrollOffset < 0 {
				v.scrollOffset = 0
			}
			v.autoScroll = false
		case "ctrl+d":
			v.scrollOffset += 10
			v.autoScroll = false
		case "ctrl+u":
			v.scrollOffset -= 10
			if v.scrollOffset < 0 {
				v.scrollOffset = 0
			}
			v.autoScroll = false
		case "g":
			v.scrollOffset = 0
			v.autoScroll = false
		case "G":
			v.scrollOffset = 999999
			v.autoScroll = true
		case "a":
			v.autoScroll = !v.autoScroll
			if v.autoScroll {
				v.scrollOffset = 999999
			}
		case "c":
			v.buffer.Clear()
			v.scrollOffset = 0
			v.cachedViewDirty = true
		}
		if v.scrollOffset != oldOffset {
			v.cachedViewDirty = true
		}
	}
	return nil
}

// View renders the raw events
func (v *EnhancedRawEventsView) View() string {
	visibleHeight := v.height
	if visibleHeight < 3 {
		visibleHeight = 3
	}

	totalEvents := v.buffer.Size()

	// Empty state
	if totalEvents == 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6C7086")).
			Italic(true).
			Render("\n  No raw API events captured yet.\n  Send a message to see SSE events here.")
	}

	// Calculate scroll bounds
	maxScroll := totalEvents - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if v.scrollOffset > maxScroll {
		v.scrollOffset = maxScroll
	}
	if v.scrollOffset < 0 {
		v.scrollOffset = 0
	}

	// Fast path: return cached view if valid
	if !v.cachedViewDirty &&
		v.cachedViewOffset == v.scrollOffset &&
		v.cachedBufferSize == totalEvents &&
		v.cachedView != "" {
		return v.cachedView
	}

	// Get only visible events
	visible := v.buffer.GetVisible(v.scrollOffset, visibleHeight)
	if len(visible) == 0 {
		return ""
	}

	// Build view with pre-sized builder
	var sb strings.Builder
	sb.Grow(len(visible) * 150)

	contentWidth := v.width - 12
	if contentWidth < 40 {
		contentWidth = 40
	}

	for i, event := range visible {
		if i > 0 {
			sb.WriteByte('\n')
		}

		// Event number (global index)
		eventIdx := v.scrollOffset + i + 1
		sb.WriteString(v.eventNumStyle.Render(fmt.Sprintf("[%3d]", eventIdx)))
		sb.WriteByte(' ')

		// Timestamp
		sb.WriteString(v.timestampStyle.Render("[" + event.Timestamp + "]"))
		sb.WriteByte(' ')

		// Event type
		sb.WriteString(v.eventTypeStyle.Render("[" + event.EventType + "]"))
		sb.WriteByte(' ')

		// Data (truncated if needed)
		data := event.Data
		maxDataLen := contentWidth - 35
		if maxDataLen < 20 {
			maxDataLen = 20
		}
		if len(data) > maxDataLen {
			data = data[:maxDataLen-3] + "..."
		}
		sb.WriteString(v.dataStyle.Render(data))
		// Add canonical info if available
		if event.Envelope != nil && event.Envelope.Canonical != nil {
			c := event.Envelope.Canonical
			if c.Usage != nil && (c.Usage.InputTokens > 0 || c.Usage.OutputTokens > 0 || c.Usage.CacheReadTokens > 0 || c.Usage.CacheCreationTokens > 0) {
				usageStr := fmt.Sprintf("  ↳ CANONICAL: in=%d out=%d read=%d create=%d",
					c.Usage.InputTokens, c.Usage.OutputTokens, c.Usage.CacheReadTokens, c.Usage.CacheCreationTokens)
				sb.WriteByte('\n')
				sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#CBA6F7")).Render(usageStr))
			}
			if c.FinishReason != "" {
				sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8")).Render(fmt.Sprintf(" [%s]", c.FinishReason)))
			}
		}
	}

	// Scroll indicator
	if totalEvents > visibleHeight {
		percentage := 0
		if maxScroll > 0 {
			percentage = int((float64(v.scrollOffset) / float64(maxScroll)) * 100)
		}
		autoIndicator := ""
		if v.autoScroll {
			autoIndicator = " AUTO"
		}
		sb.WriteByte('\n')
		sb.WriteString(v.scrollInfoStyle.Render(fmt.Sprintf("─── %d-%d of %d (%d%%)%s ───",
			v.scrollOffset+1,
			min(v.scrollOffset+visibleHeight, totalEvents),
			totalEvents,
			percentage,
			autoIndicator)))
	}

	content := sb.String()

	// Cache the result
	v.cachedView = content
	v.cachedViewOffset = v.scrollOffset
	v.cachedBufferSize = totalEvents
	v.cachedViewDirty = false

	return content
}

// SetSize updates dimensions
func (v *EnhancedRawEventsView) SetSize(width, height int) {
	if v.width != width || v.height != height {
		v.width = width
		v.height = height
		v.cachedViewDirty = true
	}
}

// SetTheme updates the theme and refreshes cached styles
func (v *EnhancedRawEventsView) SetTheme(theme Theme) {
	v.theme = theme
	v.dataStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
	v.cachedViewDirty = true
}

// IsAutoScroll returns auto-scroll state
func (v *EnhancedRawEventsView) IsAutoScroll() bool {
	return v.autoScroll
}

// GetStats returns buffer statistics
func (v *EnhancedRawEventsView) GetStats() (current, capacity int) {
	return v.buffer.Size(), v.buffer.capacity
}

// Clear removes all events
func (v *EnhancedRawEventsView) Clear() {
	v.buffer.Clear()
	v.scrollOffset = 0
	v.cachedViewDirty = true
}
