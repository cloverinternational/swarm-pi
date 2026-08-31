package hooks

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// HooksDashboard is a full-screen TUI for managing hooks and viewing events
type HooksDashboard struct {
	width  int
	height int

	// Current tab
	activeTab DashboardTab

	// Event stream
	events        []EventDisplay
	maxEvents     int
	eventScroll   int
	autoScroll    bool
	eventFilters  []string
	showAllEvents bool

	// Hook registry
	registeredHooks []HookDisplay
	selectedHook    int

	// Stats
	stats hooks.RegistryStats

	// Hooks manager reference
	hooksManager *HooksManager
}

// DashboardTab represents which tab is active
type DashboardTab int

const (
	TabEventStream DashboardTab = iota
	TabRegistry
	TabStats
	TabFilters
)

// EventDisplay represents an event for display
type EventDisplay struct {
	Timestamp time.Time
	Type      string
	Action    string // "Continue", "Block", "Modify"
	HookName  string
	Duration  time.Duration
	Details   string
	Color     string
}

// HookDisplay represents a hook for display
type HookDisplay struct {
	Name         string
	Enabled      bool
	Priority     int
	Scope        hooks.HookScope
	ScopeID      string
	Executions   int64
	Blocked      int64
	Modified     int64
	Errors       int64
	AvgDuration  time.Duration
	LastExecuted time.Time
}

// NewHooksDashboard creates a new hooks dashboard
func NewHooksDashboard(hooksManager *HooksManager) *HooksDashboard {
	return &HooksDashboard{
		hooksManager:  hooksManager,
		activeTab:     TabEventStream,
		events:        make([]EventDisplay, 0),
		maxEvents:     1000,
		autoScroll:    true,
		showAllEvents: false,
		eventFilters:  []string{"tool.*", "message.*", "provider.*"},
	}
}

// SetSize updates dashboard dimensions
func (d *HooksDashboard) SetSize(width, height int) {
	d.width = width
	d.height = height
}

// Update handles input
func (d *HooksDashboard) Update(msg tea.Msg) (*HooksDashboard, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			d.activeTab = (d.activeTab + 1) % 4
		case "shift+tab":
			d.activeTab = (d.activeTab + 3) % 4
		case "1":
			d.activeTab = TabEventStream
		case "2":
			d.activeTab = TabRegistry
		case "3":
			d.activeTab = TabStats
		case "4":
			d.activeTab = TabFilters

		// Navigation
		case "up", "k":
			d.handleUp()
		case "down", "j":
			d.handleDown()
		case "pageup":
			d.handlePageUp()
		case "pagedown":
			d.handlePageDown()

		// Actions
		case " ", "enter":
			d.handleAction()
		case "a":
			d.autoScroll = !d.autoScroll
		case "f":
			d.showAllEvents = !d.showAllEvents
		}

	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
	}

	// Refresh data
	d.refreshData()

	return d, nil
}

// View renders the dashboard
func (d *HooksDashboard) View() string {
	if d.width == 0 || d.height == 0 {
		return ""
	}

	// Main container
	container := lipgloss.NewStyle().
		Width(d.width).
		Height(d.height)

	// Header with tabs
	header := d.renderHeader()

	// Content area (height minus header and footer)
	contentHeight := max(10, d.height-4)
	content := d.renderContent(contentHeight)

	// Footer with keybindings
	footer := d.renderFooter()

	return container.Render(
		lipgloss.JoinVertical(lipgloss.Left,
			header,
			content,
			footer,
		),
	)
}

func (d *HooksDashboard) renderHeader() string {
	tabs := []string{
		i18n.T("classic_chat_2.hooks.tab.events"),
		i18n.T("classic_chat_2.hooks.tab.registry"),
		i18n.T("classic_chat_2.hooks.tab.stats"),
		i18n.T("classic_chat_2.hooks.tab.filters"),
	}
	tabStyles := make([]string, len(tabs))

	for i, tab := range tabs {
		style := lipgloss.NewStyle().
			Padding(0, 2).
			Bold(true)

		if i == int(d.activeTab) {
			style = style.
				Foreground(lipgloss.Color(palette.Teal)).
				Background(lipgloss.Color(palette.Border)).
				Underline(true)
		} else {
			style = style.
				Foreground(lipgloss.Color(palette.TextMuted))
		}

		tabStyles[i] = style.Render(fmt.Sprintf("%d. %s", i+1, tab))
	}

	headerStyle := lipgloss.NewStyle().
		Width(d.width).
		Padding(0, 1).
		BorderBottom(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(palette.Border))

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Teal)).
		Render(i18n.T("classic_chat_2.hooks.title"))

	return headerStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left,
			title,
			lipgloss.JoinHorizontal(lipgloss.Left, tabStyles...),
		),
	)
}

func (d *HooksDashboard) renderContent(height int) string {
	contentStyle := lipgloss.NewStyle().
		Width(d.width).
		Height(height).
		Padding(1)

	var content string
	safeHeight := max(10, height-2)
	switch d.activeTab {
	case TabEventStream:
		content = d.renderEventStream(safeHeight)
	case TabRegistry:
		content = d.renderRegistry(safeHeight)
	case TabStats:
		content = d.renderStats(safeHeight)
	case TabFilters:
		content = d.renderFilters(safeHeight)
	}

	return contentStyle.Render(content)
}

func (d *HooksDashboard) renderEventStream(height int) string {
	if len(d.events) == 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.TextMuted)).
			Render(i18n.T("classic_chat_2.hooks.no_events"))
	}

	// Calculate visible range
	visibleCount := max(1, height-2)
	startIdx := max(max(0, len(d.events)-visibleCount-d.eventScroll), 0)
	endIdx := min(max(0, len(d.events)-d.eventScroll), len(d.events))

	var lines []string
	lines = append(lines, lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Teal)).
		Render(i18n.T("classic_chat_2.hooks.event_stream", len(d.events), d.eventScroll)))
	lines = append(lines, "")

	for i := startIdx; i < endIdx; i++ {
		event := d.events[i]
		timestamp := event.Timestamp.Format("15:04:05.000")

		actionStyle := lipgloss.NewStyle()
		switch event.Action {
		case "Continue":
			actionStyle = actionStyle.Foreground(lipgloss.Color(palette.Success))
		case "Block":
			actionStyle = actionStyle.Foreground(lipgloss.Color(palette.Error))
		case "Modify":
			actionStyle = actionStyle.Foreground(lipgloss.Color(palette.Warning))
		}

		line := fmt.Sprintf("%s  %s  %s  %s",
			lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Render(timestamp),
			lipgloss.NewStyle().Foreground(lipgloss.Color(event.Color)).Render(event.Type),
			actionStyle.Render(event.Action),
			lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Info)).Render(event.HookName),
		)

		if event.Duration > 0 {
			line += fmt.Sprintf(" (%s)", event.Duration)
		}

		lines = append(lines, line)

		if event.Details != "" {
			lines = append(lines, lipgloss.NewStyle().
				Foreground(lipgloss.Color(palette.TextMuted)).
				Render("  └─ "+event.Details))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (d *HooksDashboard) renderRegistry(height int) string {
	if len(d.registeredHooks) == 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.TextMuted)).
			Render(i18n.T("classic_chat_2.hooks.none_registered"))
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Teal)).
		Render(i18n.T("classic_chat_2.hooks.registered")))
	lines = append(lines, "")

	for i, hook := range d.registeredHooks {
		selected := i == d.selectedHook

		enabledIcon := "✗"
		enabledColor := palette.Error
		if hook.Enabled {
			enabledIcon = "✓"
			enabledColor = palette.Success
		}

		nameStyle := lipgloss.NewStyle()
		if selected {
			nameStyle = nameStyle.Bold(true).Underline(true)
		}

		line := i18n.T("classic_chat_2.hooks.registry_row",
			lipgloss.NewStyle().Foreground(lipgloss.Color(enabledColor)).Render(enabledIcon),
			nameStyle.Render(hook.Name),
			hook.Priority,
		)

		lines = append(lines, line)

		if selected {
			// Show detailed stats for selected hook
			lines = append(lines, lipgloss.NewStyle().
				Foreground(lipgloss.Color(palette.TextMuted)).
				Render(i18n.T("classic_chat_2.hooks.registry_detail",
					hook.Scope, hook.Executions, hook.Blocked, hook.Modified, hook.Errors)))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (d *HooksDashboard) renderStats(height int) string {
	var lines []string
	lines = append(lines, lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Teal)).
		Render(i18n.T("classic_chat_2.hooks.stats.title")))
	lines = append(lines, "")

	lines = append(lines, i18n.T("classic_chat_2.hooks.stats.total", d.stats.TotalHooks))
	lines = append(lines, i18n.T("classic_chat_2.hooks.stats.enabled", d.stats.EnabledHooks))
	lines = append(lines, i18n.T("classic_chat_2.hooks.stats.executions", d.stats.TotalExecutions))
	lines = append(lines, i18n.T("classic_chat_2.hooks.stats.blocked", d.stats.TotalBlocked))
	lines = append(lines, i18n.T("classic_chat_2.hooks.stats.modified", d.stats.TotalModified))
	lines = append(lines, i18n.T("classic_chat_2.hooks.stats.errors", d.stats.TotalErrors))

	if d.stats.TotalExecutions > 0 {
		blockRate := float64(d.stats.TotalBlocked) / float64(d.stats.TotalExecutions) * 100
		modifyRate := float64(d.stats.TotalModified) / float64(d.stats.TotalExecutions) * 100

		lines = append(lines, "")
		lines = append(lines, i18n.T("classic_chat_2.hooks.stats.block_rate", blockRate))
		lines = append(lines, i18n.T("classic_chat_2.hooks.stats.modify_rate", modifyRate))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (d *HooksDashboard) renderFilters(height int) string {
	var lines []string
	lines = append(lines, lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Teal)).
		Render(i18n.T("classic_chat_2.hooks.filters.title")))
	lines = append(lines, "")

	showAllText := i18n.T("classic_chat_2.hooks.filters.filtered")
	if d.showAllEvents {
		showAllText = i18n.T("classic_chat_2.hooks.filters.all")
	}
	lines = append(lines, i18n.T("classic_chat_2.hooks.filters.mode", showAllText))
	lines = append(lines, "")

	if !d.showAllEvents {
		lines = append(lines, i18n.T("classic_chat_2.hooks.filters.active"))
		for _, filter := range d.eventFilters {
			lines = append(lines, fmt.Sprintf("  • %s", filter))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (d *HooksDashboard) renderFooter() string {
	footerStyle := lipgloss.NewStyle().
		Width(d.width).
		Padding(0, 1).
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Foreground(lipgloss.Color(palette.TextMuted))

	keybinds := []string{
		i18n.T("classic_chat_2.hooks.footer.switch_tab"),
		i18n.T("classic_chat_2.hooks.footer.navigate"),
		i18n.T("classic_chat_2.hooks.footer.toggle"),
		i18n.T("classic_chat_2.hooks.footer.auto_scroll"),
		i18n.T("classic_chat_2.hooks.footer.filter"),
		i18n.T("classic_chat_2.hooks.footer.close"),
	}

	return footerStyle.Render(strings.Join(keybinds, " | "))
}

// Helper methods
func (d *HooksDashboard) handleUp() {
	switch d.activeTab {
	case TabEventStream:
		if d.eventScroll < len(d.events)-1 {
			d.eventScroll++
			d.autoScroll = false
		}
	case TabRegistry:
		if d.selectedHook > 0 {
			d.selectedHook--
		}
	}
}

func (d *HooksDashboard) handleDown() {
	switch d.activeTab {
	case TabEventStream:
		if d.eventScroll > 0 {
			d.eventScroll--
		}
	case TabRegistry:
		if d.selectedHook < len(d.registeredHooks)-1 {
			d.selectedHook++
		}
	}
}

func (d *HooksDashboard) handlePageUp() {
	if d.activeTab == TabEventStream {
		d.eventScroll += 10
		if d.eventScroll > len(d.events)-1 {
			d.eventScroll = len(d.events) - 1
		}
		d.autoScroll = false
	}
}

func (d *HooksDashboard) handlePageDown() {
	if d.activeTab == TabEventStream {
		d.eventScroll -= 10
		if d.eventScroll < 0 {
			d.eventScroll = 0
		}
	}
}

func (d *HooksDashboard) handleAction() {
	if d.activeTab == TabRegistry && len(d.registeredHooks) > 0 {
		hook := d.registeredHooks[d.selectedHook]
		if hook.Enabled {
			if err := d.hooksManager.DisableHook(hook.Name); err != nil {
				logDebug("[HooksDashboard] Failed to disable hook %s: %v", hook.Name, err)
			}
		} else {
			if err := d.hooksManager.EnableHook(hook.Name); err != nil {
				logDebug("[HooksDashboard] Failed to enable hook %s: %v", hook.Name, err)
			}
		}
	}
}

func (d *HooksDashboard) refreshData() {
	if d.hooksManager == nil {
		return
	}

	// Refresh stats
	d.stats = d.hooksManager.GetStats()

	// Refresh registered hooks
	manager := d.hooksManager.GetManager()
	if manager != nil {
		registrations := manager.List()
		d.registeredHooks = make([]HookDisplay, len(registrations))
		for i, reg := range registrations {
			d.registeredHooks[i] = HookDisplay{
				Name:       reg.Hook.Name(),
				Enabled:    reg.Enabled,
				Priority:   reg.Hook.Priority(),
				Scope:      reg.Scope,
				ScopeID:    reg.ScopeID,
				Executions: reg.ExecutionCount,
				Blocked:    reg.BlockedCount,
				Modified:   reg.ModifiedCount,
				Errors:     reg.ErrorCount,
			}
		}
	}
}

// AddEvent adds an event to the stream (called from hooks)
func (d *HooksDashboard) AddEvent(eventType, action, hookName, details string, duration time.Duration) {
	// Determine color based on event type
	color := palette.Text
	if strings.HasPrefix(eventType, "tool.") {
		color = palette.Teal
	} else if strings.HasPrefix(eventType, "message.") {
		color = palette.AccentSoft
	} else if strings.HasPrefix(eventType, "provider.") {
		color = palette.Info
	} else if strings.HasPrefix(eventType, "steering.") {
		color = palette.Warning
	}

	event := EventDisplay{
		Timestamp: time.Now(),
		Type:      eventType,
		Action:    action,
		HookName:  hookName,
		Duration:  duration,
		Details:   details,
		Color:     color,
	}

	d.events = append(d.events, event)

	// Trim if exceeding max
	if len(d.events) > d.maxEvents {
		d.events = d.events[len(d.events)-d.maxEvents:]
	}

	// Auto-scroll to latest
	if d.autoScroll {
		d.eventScroll = 0
	}
}
