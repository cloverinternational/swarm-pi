package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (d *DebugScreen) View() string {
	// Use the new clean UI rendering
	return d.renderCleanView()
}

// renderLogsView shows enhanced log viewer with filtering
func (d *DebugScreen) renderLogsView(height int) string {
	th := d.theme

	// Get stats for header
	current, capacity, percentage, filtered := d.enhancedLogsView.GetStats()

	// Build header with stats and active filters
	headerParts := []string{
		i18n.T("classic_chat_2.debug.logs.buffer", current, capacity, percentage),
	}

	// Follow + severity status so the user always knows why they see what they
	// see and whether the view is tracking new logs.
	followStr := i18n.T("classic_chat_2.debug.logs.follow")
	if !d.enhancedLogsView.FollowActive() {
		followStr = i18n.T("classic_chat_2.debug.logs.paused")
	}
	headerParts = append(headerParts, i18n.T("classic_chat_2.debug.logs.severity", followStr, d.enhancedLogsView.SeverityLabel()))

	// Inline search prompt (while typing) or the match position (after enter).
	if d.enhancedLogsView.SearchActive() {
		headerParts = append(headerParts, fmt.Sprintf("🔎 /%s▏", d.enhancedLogsView.SearchQuery()))
	} else if q := d.enhancedLogsView.SearchQuery(); q != "" {
		cur, total := d.enhancedLogsView.MatchInfo()
		headerParts = append(headerParts, i18n.T("classic_chat_2.debug.logs.search_match", q, cur, total))
	}

	// Show filtered count if filtering is active
	if filtered < current {
		headerParts = append(headerParts, i18n.T("classic_chat_2.debug.logs.showing", filtered, current))
	}

	// Show active filters with more detail
	activeFilters := []string{}
	filter := d.enhancedLogsView.filter

	// Check which levels are enabled/disabled
	enabledLevels := []string{}
	disabledLevels := []string{}
	for level := LogLevelTrace; level <= LogLevelFatal; level++ {
		if filter.EnabledLevels[level] {
			enabledLevels = append(enabledLevels, level.String())
		} else {
			disabledLevels = append(disabledLevels, level.String())
		}
	}

	// Show filter status
	if len(disabledLevels) > 0 && len(disabledLevels) < 6 {
		activeFilters = append(activeFilters, i18n.T("classic_chat_2.debug.logs.levels", strings.Join(enabledLevels, ",")))
	} else if len(enabledLevels) == 6 {
		activeFilters = append(activeFilters, i18n.T("classic_chat_2.debug.logs.levels_all"))
	}

	// Check category filtering
	if !filter.ShowAllCategories {
		enabledCats := []string{}
		for cat, enabled := range filter.EnabledCategories {
			if enabled {
				enabledCats = append(enabledCats, cat)
			}
		}
		if len(enabledCats) > 0 {
			activeFilters = append(activeFilters, i18n.T("classic_chat_2.debug.logs.categories", strings.Join(enabledCats, ",")))
		} else {
			activeFilters = append(activeFilters, i18n.T("classic_chat_2.debug.logs.categories_none"))
		}
	} else {
		activeFilters = append(activeFilters, i18n.T("classic_chat_2.debug.logs.categories_all"))
	}

	// Check search
	if filter.SearchText != "" {
		activeFilters = append(activeFilters, i18n.T("classic_chat_2.debug.logs.search", filter.SearchText))
	}

	if len(activeFilters) > 0 {
		filterStr := strings.Join(activeFilters, " • ")
		headerParts = append(headerParts, fmt.Sprintf("🔍 %s", filterStr))
	}

	header := strings.Join(headerParts, "\n")

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#89B4FA")).
		Bold(true).
		Padding(0, 1)

	// Render enhanced logs view
	logsContent := d.enhancedLogsView.View()

	// Build contextual instructions based on state
	var instructions string
	if current == 0 {
		instructions = i18n.T("classic_chat_2.debug.logs.empty")
	} else {
		quickFilters := []string{
			i18n.T("classic_chat_2.debug.logs.hint.severity"),
			i18n.T("classic_chat_2.debug.logs.hint.search"),
			i18n.T("classic_chat_2.debug.logs.hint.matches"),
			i18n.T("classic_chat_2.debug.logs.hint.reset"),
		}
		actions := []string{
			i18n.T("classic_chat_2.debug.logs.hint.filters"),
			i18n.T("classic_chat_2.debug.logs.hint.clear"),
			i18n.T("classic_chat_2.debug.logs.hint.copy"),
			i18n.T("classic_chat_2.debug.logs.hint.buffer"),
		}
		instructions = strings.Join(quickFilters, " • ") + "  |  " + strings.Join(actions, " • ")
	}

	instrStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 1)

	// Combine header + content + instructions
	content := headerStyle.Render(header) + "\n" +
		logsContent + "\n" +
		instrStyle.Render(instructions)

	return content
}

// Helper functions
