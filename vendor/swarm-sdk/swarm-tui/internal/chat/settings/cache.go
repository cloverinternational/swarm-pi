package settings

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// CacheStatsProvider interface for accessing cache statistics
type CacheStatsProvider interface {
	GetStats() any
	ResetSessionStats()
	ResetAllStats()
}

// CacheSettings handles cache statistics and management
type CacheSettings struct {
	statsProvider CacheStatsProvider
	selectedView  string // "overview", "models", "invalidations"
	selectedItem  int
	scrollOffset  int
	viewWidth     int // cached width for sub-render methods
}

// NewCacheSettings creates a new cache settings handler
func NewCacheSettings(provider CacheStatsProvider) *CacheSettings {
	return &CacheSettings{
		statsProvider: provider,
		selectedView:  "overview",
		selectedItem:  0,
		scrollOffset:  0,
	}
}

// HandleKey handles keyboard input for cache settings
func (c *CacheSettings) HandleKey(key string) bool {
	switch key {
	case "up", "k":
		if c.selectedItem > 0 {
			c.selectedItem--
			if c.selectedItem < c.scrollOffset {
				c.scrollOffset = c.selectedItem
			}
		}
		return true
	case "down", "j":
		maxItems := c.getMaxItems()
		if c.selectedItem < maxItems-1 {
			c.selectedItem++
			if c.selectedItem >= c.scrollOffset+10 {
				c.scrollOffset = c.selectedItem - 9
			}
		}
		return true
	case "1":
		c.selectedView = "overview"
		c.selectedItem = 0
		c.scrollOffset = 0
		return true
	case "2":
		c.selectedView = "models"
		c.selectedItem = 0
		c.scrollOffset = 0
		return true
	case "3":
		c.selectedView = "invalidations"
		c.selectedItem = 0
		c.scrollOffset = 0
		return true
	case "r":
		// Reset session stats
		if c.statsProvider != nil {
			c.statsProvider.ResetSessionStats()
		}
		return true
	case "R":
		// Reset all stats (Shift+R)
		if c.statsProvider != nil {
			c.statsProvider.ResetAllStats()
		}
		return true
	}
	return false
}

func (c *CacheSettings) getMaxItems() int {
	switch c.selectedView {
	case "overview":
		return 8 // Number of overview items
	case "models":
		// Get actual model count from stats
		if c.statsProvider != nil {
			if stats := c.getTypedStats(); stats != nil && len(stats.ModelStats) > 0 {
				return len(stats.ModelStats)
			}
		}
		return 1
	case "invalidations":
		return 3
	}
	return 0
}

// CacheStats is a copy of the struct from render_config.go for type assertion
type CacheStats struct {
	SessionCreationTokens     int
	SessionReadTokens         int
	SessionHitRate            float64
	SessionStartTime          time.Time
	TotalRequests             int64
	TotalCreationTokens       int64
	TotalReadTokens           int64
	TotalHitRate              float64
	TotalInputTokens          int64
	TotalOutputTokens         int64
	SystemPromptInvalidations int64
	ToolInvalidations         int64
	MCPInvalidations          int64
	ModelStats                map[string]*ModelCacheStats
	AverageHitRate            float64
	TotalTokensSaved          int64
	TotalCostSaved            float64
	LastUpdated               time.Time
	FirstRecorded             time.Time
}

type ModelCacheStats struct {
	ModelName      string
	Provider       string
	TotalRequests  int64
	CreationTokens int64
	ReadTokens     int64
	HitRate        float64
	LastUsed       time.Time
	TokensSaved    int64
	CostSaved      float64
	AverageLatency float64
	CacheHits      int64
	CacheMisses    int64
	InputTokens    int64
	OutputTokens   int64
}

// getTypedStats attempts to get stats as *CacheStats
func (c *CacheSettings) getTypedStats() *CacheStats {
	if c.statsProvider == nil {
		return nil
	}
	stats := c.statsProvider.GetStats()
	if stats == nil {
		return nil
	}

	// Type assert to *CacheStats
	if cs, ok := stats.(*CacheStats); ok {
		return cs
	}
	return nil
}

// Render renders the cache settings view
func (c *CacheSettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)
	c.viewWidth = width
	var lines []string

	// Title with centered styling
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center).
		Padding(1, 0)
	title := titleStyle.Render(i18n.T("settings.cache.title"))
	lines = append(lines, title)

	// Status badges row
	stats := c.getTypedStats()
	badgeRow := c.renderStatusBadges(stats, width, th)
	lines = append(lines, badgeRow, "")

	// View tabs
	tabsLine := c.renderTabs(th)
	lines = append(lines, tabsLine, "")

	// Render content based on selected view
	switch c.selectedView {
	case "overview":
		lines = append(lines, c.renderOverview(th)...)
	case "models":
		lines = append(lines, c.renderModels(th)...)
	case "invalidations":
		lines = append(lines, c.renderInvalidations(th)...)
	}

	// Hint bar with keyboard shortcuts
	lines = append(lines, "")
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	var hintText string
	if width < 50 {
		hintText = i18n.T("settings.cache.hints.compact",
			keyStyle.Render("1-3"),
			keyStyle.Render("↑/↓"))
	} else {
		hintText = i18n.T("settings.cache.hints.full",
			keyStyle.Render("1-3"),
			keyStyle.Render("r"),
			keyStyle.Render("R"),
			keyStyle.Render("↑/↓"))
	}
	lines = append(lines, hintStyle.Render(hintText))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	// Container with rounded border
	containerStyle := lipgloss.NewStyle().
		Width(maxInt(20, width-2)).
		Height(maxInt(5, height-2)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.TextMuted)).
		Padding(0, 1)

	return containerStyle.Render(content)
}

func (c *CacheSettings) renderStatusBadges(stats *CacheStats, width int, th Theme) string {
	if stats == nil {
		return ""
	}

	if width < 50 {
		// Too narrow for badges
		return ""
	}

	badgeStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Bold(true)

	// Calculate hit rates
	sessionHitRate := 0.0
	if stats.SessionCreationTokens+stats.SessionReadTokens > 0 {
		sessionHitRate = float64(stats.SessionReadTokens) / float64(stats.SessionCreationTokens+stats.SessionReadTokens)
	}

	totalHitRate := 0.0
	if stats.TotalCreationTokens+stats.TotalReadTokens > 0 {
		totalHitRate = float64(stats.TotalReadTokens) / float64(stats.TotalCreationTokens+stats.TotalReadTokens)
	}

	// Create badges with appropriate colors
	sessionBadgeColor := th.Success
	if sessionHitRate < 0.5 {
		sessionBadgeColor = th.Error
	} else if sessionHitRate < 0.8 {
		sessionBadgeColor = th.Warning
	}

	totalBadgeColor := th.Success
	if totalHitRate < 0.5 {
		totalBadgeColor = th.Error
	} else if totalHitRate < 0.8 {
		totalBadgeColor = th.Warning
	}

	sessionBadge := badgeStyle.
		Background(lipgloss.Color(sessionBadgeColor)).
		Foreground(lipgloss.Color(th.Text)).
		Render(i18n.T("settings.cache.badge.session", sessionHitRate*100))

	totalBadge := badgeStyle.
		Background(lipgloss.Color(totalBadgeColor)).
		Foreground(lipgloss.Color(th.Text)).
		Render(i18n.T("settings.cache.badge.total", totalHitRate*100))

	var badgesRow string
	if width < 70 {
		badgesRow = lipgloss.JoinHorizontal(lipgloss.Center, sessionBadge, totalBadge)
	} else {
		tokensBadge := badgeStyle.
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Render(i18n.T("settings.cache.badge.saved", formatNumber(stats.TotalTokensSaved)))
		badgesRow = lipgloss.JoinHorizontal(lipgloss.Center, sessionBadge, totalBadge, tokensBadge)
	}

	return lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Center).
		Render(badgesRow)
}

func (c *CacheSettings) renderTabs(th Theme) string {
	tabs := []struct {
		name   string
		key    string
		active bool
	}{
		{i18n.T("settings.cache.tab.overview"), "1", c.selectedView == "overview"},
		{i18n.T("settings.cache.tab.models"), "2", c.selectedView == "models"},
		{i18n.T("settings.cache.tab.invalidations"), "3", c.selectedView == "invalidations"},
	}

	var tabStrs []string
	tabPad := 2
	if c.viewWidth < 50 {
		tabPad = 1
	}
	for _, tab := range tabs {
		var style lipgloss.Style
		if tab.active {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Background(lipgloss.Color(th.BGLighter)).
				Bold(true).
				Padding(0, tabPad)
		} else {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Padding(0, tabPad)
		}
		if c.viewWidth < 50 {
			shortNames := []string{
				i18n.T("settings.cache.tab.overview.short"),
				i18n.T("settings.cache.tab.models.short"),
				i18n.T("settings.cache.tab.invalidations.short"),
			}
			short := shortNames[len(tabStrs)]
			tabStrs = append(tabStrs, style.Render(fmt.Sprintf("[%s]%s", tab.key, short)))
		} else {
			tabStrs = append(tabStrs, style.Render(fmt.Sprintf("[%s] %s", tab.key, tab.name)))
		}
	}

	outerPad := 2
	if c.viewWidth < 50 {
		outerPad = 1
	}
	return lipgloss.NewStyle().
		Padding(0, outerPad).
		Render(lipgloss.JoinHorizontal(lipgloss.Left, tabStrs...))
}

func (c *CacheSettings) renderOverview(th Theme) []string {
	var lines []string

	stats := c.getTypedStats()
	if stats == nil {
		lines = append(lines, c.renderSection(i18n.T("settings.cache.no_data"), th))
		lines = append(lines, c.renderStatLine(i18n.T("settings.cache.status"), i18n.T("settings.cache.not_initialized"), th))
		return lines
	}

	// Session statistics
	lines = append(lines, c.renderSection(i18n.T("settings.cache.section.session"), th))
	sessionDuration := time.Since(stats.SessionStartTime)
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.duration"), formatDuration(sessionDuration), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.created"), i18n.T("settings.cache.tokens", formatNumber(int64(stats.SessionCreationTokens))), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.read"), i18n.T("settings.cache.tokens", formatNumber(int64(stats.SessionReadTokens))), th))

	sessionHitRate := 0.0
	if stats.SessionCreationTokens+stats.SessionReadTokens > 0 {
		sessionHitRate = float64(stats.SessionReadTokens) / float64(stats.SessionCreationTokens+stats.SessionReadTokens)
	}
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.hit_rate"), c.renderHitRateBar(sessionHitRate, th), th))
	lines = append(lines, "")

	// All-time statistics
	lines = append(lines, c.renderSection(i18n.T("settings.cache.section.all_time"), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.total_requests"), formatNumber(stats.TotalRequests), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.total_created"), i18n.T("settings.cache.tokens", formatNumber(stats.TotalCreationTokens)), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.total_read"), i18n.T("settings.cache.tokens", formatNumber(stats.TotalReadTokens)), th))

	totalHitRate := 0.0
	if stats.TotalCreationTokens+stats.TotalReadTokens > 0 {
		totalHitRate = float64(stats.TotalReadTokens) / float64(stats.TotalCreationTokens+stats.TotalReadTokens)
	}
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.total_hit_rate"), c.renderHitRateBar(totalHitRate, th), th))

	costSaved := fmt.Sprintf("$%.2f", stats.TotalCostSaved)
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.tokens_saved"), i18n.T("settings.cache.savings", formatNumber(stats.TotalTokensSaved), costSaved), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.first_recorded"), formatTimeAgo(stats.FirstRecorded), th))
	lines = append(lines, "")

	// Efficiency metrics
	lines = append(lines, c.renderSection(i18n.T("settings.cache.section.efficiency"), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.efficiency"), fmt.Sprintf("%.1f%%", totalHitRate*100), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.input_tokens"), formatNumber(stats.TotalInputTokens), th))
	lines = append(lines, c.renderStatLine(i18n.T("settings.cache.stat.output_tokens"), formatNumber(stats.TotalOutputTokens), th))

	return lines
}

func (c *CacheSettings) renderModels(th Theme) []string {
	var lines []string

	stats := c.getTypedStats()
	if stats == nil || len(stats.ModelStats) == 0 {
		lines = append(lines, c.renderSection(i18n.T("settings.cache.section.models_empty"), th))
		return lines
	}

	lines = append(lines, c.renderSection(i18n.T("settings.cache.section.models"), th))
	lines = append(lines, "")

	// Convert map to slice and sort by requests
	var models []*ModelCacheStats
	for _, modelStats := range stats.ModelStats {
		models = append(models, modelStats)
	}

	// Sort by total requests (descending)
	for i := 0; i < len(models)-1; i++ {
		for j := i + 1; j < len(models); j++ {
			if models[j].TotalRequests > models[i].TotalRequests {
				models[i], models[j] = models[j], models[i]
			}
		}
	}

	// Render top models
	for i, model := range models {
		isSelected := i == c.selectedItem && c.selectedView == "models"
		costSaved := fmt.Sprintf("$%.2f", model.CostSaved)
		lines = append(lines, c.renderModelCard(
			model.ModelName,
			model.Provider,
			model.TotalRequests,
			model.HitRate,
			costSaved,
			isSelected,
			th,
		)...)
		lines = append(lines, "")
	}

	return lines
}

func (c *CacheSettings) renderInvalidations(th Theme) []string {
	var lines []string

	stats := c.getTypedStats()
	if stats == nil {
		lines = append(lines, c.renderSection(i18n.T("settings.cache.section.invalidations_empty"), th))
		return lines
	}

	lines = append(lines, c.renderSection(i18n.T("settings.cache.section.invalidations"), th))
	lines = append(lines, "")

	invalidations := []struct {
		label string
		count int64
		desc  string
	}{
		{i18n.T("settings.cache.invalidation.system.label"), stats.SystemPromptInvalidations, i18n.T("settings.cache.invalidation.system.description")},
		{i18n.T("settings.cache.invalidation.tools.label"), stats.ToolInvalidations, i18n.T("settings.cache.invalidation.tools.description")},
		{i18n.T("settings.cache.invalidation.mcp.label"), stats.MCPInvalidations, i18n.T("settings.cache.invalidation.mcp.description")},
	}

	for _, inv := range invalidations {
		lines = append(lines, c.renderInvalidationLine(inv.label, inv.count, inv.desc, th))
	}

	return lines
}

func (c *CacheSettings) renderSection(title string, th Theme) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 2)
	return style.Render("▶ " + title)
}

func (c *CacheSettings) renderStatLine(label, value string, th Theme) string {
	pad := 4
	labelW := 25
	if c.viewWidth < 50 {
		pad = 2
		labelW = minInt(18, maxInt(10, c.viewWidth/2))
	} else if c.viewWidth < 70 {
		labelW = 20
	}
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Width(labelW).
		Padding(0, pad)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)

	return labelStyle.Render(label) + valueStyle.Render(value)
}

func (c *CacheSettings) renderHitRateBar(rate float64, th Theme) string {
	barWidth := maxInt(10, minInt(20, c.viewWidth-20))
	filled := int(rate * float64(barWidth))

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	barStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(c.getHitRateColor(rate)))

	return fmt.Sprintf("%s %.1f%%", barStyle.Render(bar), rate*100)
}

func (c *CacheSettings) getHitRateColor(rate float64) string {
	if rate >= 0.8 {
		return palette.Success
	} else if rate >= 0.5 {
		return palette.Warning
	}
	return palette.Error
}

func (c *CacheSettings) renderModelCard(name, provider string, requests int64, hitRate float64, saved string, isSelected bool, th Theme) []string {
	var lines []string

	namePad := 4
	detailPad := 6
	if c.viewWidth < 50 {
		namePad = 2
		detailPad = 3
	}

	// Model name
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, namePad)
	if isSelected {
		nameStyle = nameStyle.Background(lipgloss.Color(th.BGLighter))
	}
	lines = append(lines, nameStyle.Render(fmt.Sprintf("▶ %s", name)))

	// Provider
	providerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, detailPad)
	lines = append(lines, providerStyle.Render(i18n.T("settings.cache.model.provider", provider)))

	// Stats - stack at narrow widths
	statsStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, detailPad)
	if c.viewWidth < 60 {
		lines = append(lines, statsStyle.Render(i18n.T("settings.cache.model.requests", requests)))
		lines = append(lines, statsStyle.Render(i18n.T("settings.cache.model.details.compact", hitRate*100, saved)))
	} else {
		lines = append(lines, statsStyle.Render(i18n.T("settings.cache.model.details", requests, hitRate*100, saved)))
	}

	// Progress bar
	barStyle := lipgloss.NewStyle().Padding(0, detailPad)
	lines = append(lines, barStyle.Render(c.renderHitRateBar(hitRate, th)))

	return lines
}

func (c *CacheSettings) renderInvalidationLine(label string, count int64, desc string, th Theme) string {
	pad := 4
	labelW := 30
	if c.viewWidth < 50 {
		pad = 2
		// Stack vertically at narrow widths (labelW unused on this early-return path)
		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Bold(true).
			Padding(0, pad)
		countStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning))
		return labelStyle.Render(label) + " " + countStyle.Render(fmt.Sprintf("%d", count))
	} else if c.viewWidth < 70 {
		labelW = 22
	}

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(true).
		Width(labelW).
		Padding(0, pad)

	countStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Warning)).
		Width(10)

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true)

	return labelStyle.Render(label) + countStyle.Render(fmt.Sprintf("%d", count)) + descStyle.Render(desc)
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func formatNumber(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000.0)
	} else if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000.0)
	}
	return fmt.Sprintf("%d", n)
}

func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	days := int(d.Hours() / 24)
	if days > 0 {
		return i18n.T("settings.cache.time.days_ago", days)
	}
	hours := int(d.Hours())
	if hours > 0 {
		return i18n.T("settings.cache.time.hours_ago", hours)
	}
	minutes := int(d.Minutes())
	return i18n.T("settings.cache.time.minutes_ago", minutes)
}
