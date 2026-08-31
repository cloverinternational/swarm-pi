package settings

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// auditTheme returns a minimal Theme for rendering tests
func auditTheme() Theme {
	return Theme{
		BG:        "#0E1118",
		BGLight:   "#151B24",
		BGLighter: "#1E2530",
		Text:      "#FFFFFF",
		TextDim:   "#B0B0B0",
		TextMuted: "#707070",
		Primary:   "#6E64E8",
		Accent:    "#39D2C0",
		Border:    "#3B4261",
		Success:   "#22C55E",
		Warning:   "#F59E0B",
		Error:     "#EF4444",
	}
}

// TestSettingsRenderAtWidths renders every settings section at multiple widths
// and checks that no output line exceeds the given width.
func TestSettingsRenderAtWidths(t *testing.T) {
	setTempHome(t)
	manager := NewManager("anthropic", "claude-sonnet-4-20250514",
		false, false, false, false, false, "Matrix", "simple", nil)

	th := auditTheme()
	widths := []int{40, 50, 60, 70, 80, 100, 120}
	height := 30

	sections := []Section{
		SectionGeneral, SectionSecurity, SectionModel, SectionProxies,
		SectionAgents, SectionAgentProfiles, SectionConfigBundles,
		SectionMCP, SectionHooks, SectionSkills, SectionPlugins,
		SectionSystemPrompt, SectionContext, SectionDisplay, SectionTheme,
		SectionAuth, SectionCompaction, SectionReliability,
		SectionAdvanced, SectionCache,
	}

	sectionNames := map[Section]string{
		SectionGeneral:       "General",
		SectionSecurity:      "Security",
		SectionModel:         "Model",
		SectionProxies:       "Proxies",
		SectionAgents:        "Agents",
		SectionAgentProfiles: "AgentProfiles",
		SectionConfigBundles: "ConfigBundles",
		SectionMCP:           "MCP",
		SectionHooks:         "Hooks",
		SectionSkills:        "Skills",
		SectionPlugins:       "Plugins",
		SectionSystemPrompt:  "SystemPrompt",
		SectionContext:       "Context",
		SectionDisplay:       "Display",
		SectionTheme:         "Theme",
		SectionAuth:          "Auth",
		SectionCompaction:    "Compaction",
		SectionReliability:   "Reliability",
		SectionAdvanced:      "Advanced",
		SectionCache:         "Cache",
	}

	for _, section := range sections {
		for _, w := range widths {
			name := sectionNames[section]
			t.Run(name+"/"+itoa(w), func(t *testing.T) {
				manager.state.SelectedSection = section

				// Render the full manager (which includes sidebar logic)
				output := manager.Render(w, height, th, 0)

				lines := strings.Split(output, "\n")
				for i, line := range lines {
					lineW := lipgloss.Width(line)
					if lineW > w+2 { // +2 tolerance for border chars
						t.Errorf("line %d exceeds width %d (got %d): %q",
							i+1, w, lineW, truncStr(line, 80))
					}
				}

				// Also test just the content section render
				contentOutput := manager.renderContentForSection(
					maxInt(30, w), maxInt(5, height-4), ConvertTheme(th), 0)

				contentLines := strings.Split(contentOutput, "\n")
				for i, line := range contentLines {
					lineW := lipgloss.Width(line)
					if lineW > w+4 { // wider tolerance for content-only
						t.Errorf("content line %d exceeds width %d (got %d): %q",
							i+1, w, lineW, truncStr(line, 80))
					}
				}
			})
		}
	}
}

// TestManagerSinglePane verifies Manager renders in single-pane mode below 80 cols.
func TestManagerSinglePane(t *testing.T) {
	setTempHome(t)
	manager := NewManager("anthropic", "claude-sonnet-4-20250514",
		false, false, false, false, false, "Matrix", "simple", nil)

	th := auditTheme()

	for _, w := range []int{40, 50, 60, 70} {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			output := manager.Render(w, 25, th, 0)
			lines := strings.SplitSeq(output, "\n")

			// Should NOT contain sidebar group headers (indicating two-pane)
			for line := range lines {
				stripped := strings.TrimSpace(line)
				if stripped == "AI Config" || stripped == "Tools & Integrations" {
					t.Errorf("width=%d: found sidebar group header %q in single-pane mode", w, stripped)
				}
			}
		})
	}
}

// TestManagerTwoPane verifies Manager renders in two-pane mode at 80+ cols.
func TestManagerTwoPane(t *testing.T) {
	setTempHome(t)
	manager := NewManager("anthropic", "claude-sonnet-4-20250514",
		false, false, false, false, false, "Matrix", "simple", nil)

	th := auditTheme()

	for _, w := range []int{80, 100, 120} {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			output := manager.Render(w, 30, th, 0)

			// Should contain sidebar group headers
			if !strings.Contains(output, "AI Config") {
				t.Errorf("width=%d: missing sidebar group header in two-pane mode", w)
			}
		})
	}
}

func itoa(i int) string {
	s := ""
	if i == 0 {
		return "0"
	}
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
