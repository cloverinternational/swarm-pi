package settings

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// WebSearchSettings manages the Web Search configuration panel.
// It lets the user choose the active backend (Auto / Anthropic / Exa),
// configure Exa-specific options, and see live status for each backend.
type WebSearchSettings struct {
	configManager *commands.ConfigManager
	// In-memory form state
	backend        string // "auto", "anthropic", "exa"
	exaSearchType  string // "auto", "neural", "keyword"
	maxResults     int
	timeoutSeconds int
}

var wsBackends = []string{"auto", "anthropic", "exa"}
var wsBackendLabels = map[string]string{
	"auto":      "Auto (smart default)",
	"anthropic": "Anthropic (Claude OAuth)",
	"exa":       "Exa (api.exa.ai)",
}

var wsSearchTypes = []string{"auto", "neural", "keyword"}
var wsSearchTypeLabels = map[string]string{
	"auto":    "Auto",
	"neural":  "Neural (semantic)",
	"keyword": "Keyword (exact)",
}

// NewWebSearchSettings loads persisted config and initialises the panel.
func NewWebSearchSettings(cm *commands.ConfigManager) *WebSearchSettings {
	s := &WebSearchSettings{
		configManager:  cm,
		backend:        "auto",
		exaSearchType:  "auto",
		maxResults:     10,
		timeoutSeconds: 30,
	}
	if cm != nil {
		if cfg, err := cm.LoadConfig(); err == nil && cfg != nil && cfg.WebSearch != nil {
			ws := cfg.WebSearch
			if ws.PreferredBackend != "" {
				s.backend = ws.PreferredBackend
			}
			if ws.ExaSearchType != "" {
				s.exaSearchType = ws.ExaSearchType
			}
			if ws.MaxResults > 0 {
				s.maxResults = ws.MaxResults
			}
			if ws.TimeoutSeconds > 0 {
				s.timeoutSeconds = ws.TimeoutSeconds
			}
		}
	}
	return s
}

// ReloadFromConfig reloads settings from the configuration file
func (w *WebSearchSettings) ReloadFromConfig() {
	if w.configManager == nil {
		return
	}

	if cfg, err := w.configManager.LoadConfig(); err == nil && cfg != nil && cfg.WebSearch != nil {
		ws := cfg.WebSearch
		if ws.PreferredBackend != "" {
			w.backend = ws.PreferredBackend
		}
		if ws.ExaSearchType != "" {
			w.exaSearchType = ws.ExaSearchType
		}
		if ws.MaxResults > 0 {
			w.maxResults = ws.MaxResults
		}
		if ws.TimeoutSeconds > 0 {
			w.timeoutSeconds = ws.TimeoutSeconds
		}
	}
}

// ─── Key handling ──────────────────────────────────────────────────────────────

// HandleKey processes keyboard input. Returns true when consumed.
//
// Item layout:
//
//	0 = Backend selector   (← / →  cycle)
//	1 = Exa Search Type    (← / →  cycle)
//	2 = Max Results        (← / →  ±1)
func (w *WebSearchSettings) HandleKey(key string, state *State) bool {
	if w == nil || state.Focus != FocusContent {
		return false
	}
	const totalItems = 3
	switch key {
	case "up", "k":
		if state.SelectedItem > 0 {
			state.SelectedItem--
			return true
		}
	case "down", "j":
		if state.SelectedItem < totalItems-1 {
			state.SelectedItem++
			return true
		}
	case "left", "h":
		switch state.SelectedItem {
		case 0:
			w.backend = cycleStringSlice(wsBackends, w.backend, -1)
			w.save()
			return true
		case 1:
			w.exaSearchType = cycleStringSlice(wsSearchTypes, w.exaSearchType, -1)
			w.save()
			return true
		case 2:
			if w.maxResults > 1 {
				w.maxResults--
				w.save()
			}
			return true
		}
	case "right", "l":
		switch state.SelectedItem {
		case 0:
			w.backend = cycleStringSlice(wsBackends, w.backend, +1)
			w.save()
			return true
		case 1:
			w.exaSearchType = cycleStringSlice(wsSearchTypes, w.exaSearchType, +1)
			w.save()
			return true
		case 2:
			if w.maxResults < 20 {
				w.maxResults++
				w.save()
			}
			return true
		}
	}
	return false
}

// save persists the current in-memory state to disk.
func (w *WebSearchSettings) save() {
	if w == nil || w.configManager == nil {
		logDebug("[WebSearchSettings] save failed: configManager is nil")
		return
	}
	cfg, err := w.configManager.LoadConfig()
	if err != nil {
		cfg = &commands.SwarmOSConfig{}
	}
	cfg.WebSearch = &commands.WebSearchConfig{
		PreferredBackend: w.backend,
		ExaSearchType:    w.exaSearchType,
		MaxResults:       w.maxResults,
		TimeoutSeconds:   w.timeoutSeconds,
	}
	if err := w.configManager.SaveConfig(cfg); err != nil {
		logDebug("[WebSearchSettings] save failed: could not save config: %v", err)
		return
	}
	logDebug("[WebSearchSettings] settings saved successfully")
}

// ─── Rendering ────────────────────────────────────────────────────────────────

// Render returns the rendered content for the Web Search settings panel.
func (w *WebSearchSettings) Render(width, height int, state *State, theme any) string {
	_ = height
	th := theme.(Theme)

	teal := lipgloss.Color("#00BCD4")
	headerStyle := lipgloss.NewStyle().Foreground(teal).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))

	innerWidth := maxInt(20, width-4)
	pad := 2
	detailPad := 4
	if width < 50 {
		pad = 1
		detailPad = 2
	}

	var sb strings.Builder

	// ── Header ──────────────────────────────────────────────────────────────
	sb.WriteString(headerStyle.Render("◆ Web Search"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("Choose the search engine the websearch tool uses and tune its behaviour."))
	sb.WriteString("\n\n")

	// ── Backend status indicators ────────────────────────────────────────────
	anthropicOK := w.isAnthropicReady()
	exaKey := w.exaAPIKey()
	exaOK := exaKey != ""

	statusStyle := lipgloss.NewStyle().Padding(0, detailPad)
	okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Bold(true)
	failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error))

	anthropicStatus := failStyle.Render("✗ Not configured (run 'swarm login')")
	if anthropicOK {
		anthropicStatus = okStyle.Render("✓ OAuth token present")
	}
	exaStatus := failStyle.Render("✗ No API key (add Exa provider or set EXA_API_KEY)")
	if exaOK {
		src := "EXA_API_KEY env"
		if os.Getenv("EXA_API_KEY") == "" {
			src = "provider config"
		}
		exaStatus = okStyle.Render(fmt.Sprintf("✓ API key set (%s)", src))
	}

	sb.WriteString(dimStyle.Render("Backend status:"))
	sb.WriteString("\n")
	sb.WriteString(statusStyle.Render(fmt.Sprintf("Anthropic — %s", anthropicStatus)))
	sb.WriteString("\n")
	sb.WriteString(statusStyle.Render(fmt.Sprintf("Exa       — %s", exaStatus)))
	sb.WriteString("\n\n")

	// ── Item 0: Backend selector ─────────────────────────────────────────────
	isSelected0 := state != nil && state.SelectedItem == 0 && state.Focus == FocusContent
	label0 := wsBackendLabels[w.backend]
	sb.WriteString(w.renderCycleItem(
		"Preferred Backend",
		"Which search engine the agent uses. 'Auto' picks Exa when a key is set.",
		fmt.Sprintf("< %s >", label0),
		isSelected0, pad, detailPad, innerWidth, th,
	))
	sb.WriteString("\n\n")

	// ── Item 1: Exa Search Type ──────────────────────────────────────────────
	isSelected1 := state != nil && state.SelectedItem == 1 && state.Focus == FocusContent
	label1 := wsSearchTypeLabels[w.exaSearchType]
	sb.WriteString(w.renderCycleItem(
		"Exa Search Type",
		"'Auto' lets Exa decide. 'Neural' is semantic; 'Keyword' is exact-match.",
		fmt.Sprintf("< %s >", label1),
		isSelected1, pad, detailPad, innerWidth, th,
	))
	sb.WriteString("\n\n")

	// ── Item 2: Max Results ──────────────────────────────────────────────────
	isSelected2 := state != nil && state.SelectedItem == 2 && state.Focus == FocusContent
	sb.WriteString(w.renderCycleItem(
		"Max Results per Query",
		"Maximum results returned by each search (1–20).",
		fmt.Sprintf("< %d >", w.maxResults),
		isSelected2, pad, detailPad, innerWidth, th,
	))
	sb.WriteString("\n\n")

	// ── Effective backend note ────────────────────────────────────────────────
	effectiveBackend := w.effectiveBackend(anthropicOK, exaOK)
	effectiveStyle := lipgloss.NewStyle().Foreground(teal).Padding(0, detailPad)
	sb.WriteString(dimStyle.Render("Effective backend at runtime:"))
	sb.WriteString("\n")
	sb.WriteString(effectiveStyle.Render(fmt.Sprintf("→ %s", effectiveBackend)))
	sb.WriteString("\n\n")

	// ── Hint bar ─────────────────────────────────────────────────────────────
	sb.WriteString(w.renderHintBar(innerWidth, th))
	return i18n.SettingsIntegrationsText(sb.String())
}

// effectiveBackend returns a human-readable string for what the tool will
// actually use given the saved preference and available credentials.
func (w *WebSearchSettings) effectiveBackend(anthropicOK, exaOK bool) string {
	switch w.backend {
	case "anthropic":
		if anthropicOK {
			return "Anthropic (Claude OAuth)"
		}
		return "Anthropic — not ready (OAuth missing)"
	case "exa":
		if exaOK {
			return "Exa (api.exa.ai)"
		}
		return "Exa — not ready (no API key)"
	default: // auto
		if exaOK {
			return "Exa (auto-selected: API key found)"
		}
		if anthropicOK {
			return "Anthropic (auto-selected: OAuth found)"
		}
		return "None — configure a backend above"
	}
}

func (w *WebSearchSettings) isAnthropicReady() bool {
	token, err := anthropic.GetStoredOAuthToken()
	return err == nil && token != nil && token.AccessToken != ""
}

// exaAPIKey returns the Exa API key from the providers config or env var.
func (w *WebSearchSettings) exaAPIKey() string {
	if key := os.Getenv("EXA_API_KEY"); key != "" {
		return key
	}
	if w.configManager == nil {
		return ""
	}
	providers, err := w.configManager.LoadProviders()
	if err != nil {
		return ""
	}
	for _, p := range providers {
		if strings.EqualFold(p.APIType, "exa") && p.APIKey != "" {
			return p.APIKey
		}
	}
	return ""
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (w *WebSearchSettings) renderCycleItem(label, description, value string, isSelected bool, pad, detailPad, width int, th Theme) string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, pad)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, detailPad)
	if isSelected {
		valueStyle = valueStyle.
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(th.Primary))
	}

	prefix := " "
	if isSelected {
		prefix = "▶"
	}

	lines := []string{
		labelStyle.Render(fmt.Sprintf("%s %s", prefix, label)),
	}
	if width >= 50 {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Padding(0, detailPad).Render(description),
		)
	}
	lines = append(lines, valueStyle.Render(value))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (w *WebSearchSettings) renderHintBar(width int, th Theme) string {
	if width < 45 {
		return ""
	}
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)
	hints := fmt.Sprintf("%s navigate  •  %s cycle option  •  %s sidebar  •  %s back",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("←/→"),
		keyStyle.Render("tab"),
		keyStyle.Render("esc"),
	)
	return hintStyle.Render(hints)
}

// cycleStringSlice cycles through a slice by delta (+1 or -1) and returns the
// next value, wrapping around. Returns first element for unknown current values.
// Returns empty string if slice is empty.
func cycleStringSlice(slice []string, current string, delta int) string {
	if len(slice) == 0 {
		return ""
	}
	for i, v := range slice {
		if v == current {
			n := (i + delta + len(slice)) % len(slice)
			return slice[n]
		}
	}
	return slice[0]
}
