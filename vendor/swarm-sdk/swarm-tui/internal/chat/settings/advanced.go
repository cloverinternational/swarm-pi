package settings

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func init() {
	i18n.Register("settings.advanced", map[string]string{
		"settings.advanced.title":                       "Advanced Settings",
		"settings.advanced.warning":                     "Changing these settings may affect performance",
		"settings.advanced.max_tokens.label":            "Max Tokens",
		"settings.advanced.max_tokens.description":      "Maximum tokens per request",
		"settings.advanced.temperature.label":           "Temperature",
		"settings.advanced.temperature.description":     "Response randomness (0.0 - 1.0)",
		"settings.advanced.stream_response.label":       "Stream Response",
		"settings.advanced.stream_response.description": "Enable streaming for real-time responses",
		"settings.advanced.cache_prompts.label":         "Cache Prompts",
		"settings.advanced.cache_prompts.description":   "Enable prompt caching for faster responses",
		"settings.advanced.tool_mode.label":             "Advanced Tool Mode",
		"settings.advanced.tool_mode.description":       "Deferred tool loading for token optimization (experimental)",
		"settings.advanced.defer_threshold.label":       "Defer Token Threshold",
		"settings.advanced.defer_threshold.description": "Auto-defer tools exceeding this token estimate (100-5000)",
		"settings.advanced.hints":                       "%s navigate • %s select • %s / %s adjust",
		"settings.advanced.temperature.control":         "[ %.1f ]  < - / + >",
	}, map[string]string{
		"settings.advanced.title":                       "Configuración avanzada",
		"settings.advanced.warning":                     "Cambiar esta configuración puede afectar el rendimiento",
		"settings.advanced.max_tokens.label":            "Tokens máximos",
		"settings.advanced.max_tokens.description":      "Tokens máximos por solicitud",
		"settings.advanced.temperature.label":           "Temperatura",
		"settings.advanced.temperature.description":     "Aleatoriedad de la respuesta (0.0 - 1.0)",
		"settings.advanced.stream_response.label":       "Respuesta en streaming",
		"settings.advanced.stream_response.description": "Activar streaming para respuestas en tiempo real",
		"settings.advanced.cache_prompts.label":         "Caché de prompts",
		"settings.advanced.cache_prompts.description":   "Activar caché de prompts para respuestas más rápidas",
		"settings.advanced.tool_mode.label":             "Modo avanzado de herramientas",
		"settings.advanced.tool_mode.description":       "Carga diferida de herramientas para optimizar tokens (experimental)",
		"settings.advanced.defer_threshold.label":       "Umbral de tokens para diferir",
		"settings.advanced.defer_threshold.description": "Diferir automáticamente herramientas que superen esta estimación de tokens (100-5000)",
		"settings.advanced.hints":                       "%s navegar • %s seleccionar • %s / %s ajustar",
		"settings.advanced.temperature.control":         "[ %.1f ]  < - / + >",
	})
}

// AdvancedSettings handles advanced configuration
type AdvancedSettings struct {
	maxTokens      int
	temperature    float64 // 0.0 to 1.0
	streamResponse bool
	cachePrompts   bool

	configManager *commands.ConfigManager

	// Advanced Tool Use Framework settings
	advancedToolMode    bool // Enable deferred tool loading for token optimization
	deferTokenThreshold int  // Auto-defer tools above this token estimate

	// Callback for advanced tool mode changes
	onAdvancedToolModeChange func(enabled bool)
}

// NewAdvancedSettings creates a new advanced settings handler
func NewAdvancedSettings(cacheProvider CacheStatsProvider) *AdvancedSettings {
	cm, _ := commands.NewConfigManager()
	a := &AdvancedSettings{
		maxTokens:           31999, // ~32k tokens - must be under 32000 for Opus 4
		temperature:         0.7,
		streamResponse:      true,
		cachePrompts:        true,
		configManager:       cm,
		advancedToolMode:    false, // Disabled by default
		deferTokenThreshold: 500,   // Default: auto-defer tools exceeding 500 estimated tokens
	}

	// Load settings from config
	if cm != nil {
		if config, err := cm.LoadConfig(); err == nil {
			a.advancedToolMode = config.AdvancedToolMode
			if config.DeferTokenThreshold > 0 {
				a.deferTokenThreshold = config.DeferTokenThreshold
			}
			// Restore session settings persisted by the user.
			if config.MaxTokens > 0 {
				a.maxTokens = config.MaxTokens
			}
			if config.Temperature > 0 {
				a.temperature = config.Temperature
			}
			// StreamResponse and CachePrompts default to true.
			// Pointer fields: nil means never written 	 keep default.
			if config.StreamResponse != nil {
				a.streamResponse = *config.StreamResponse
			}
			if config.CachePrompts != nil {
				a.cachePrompts = *config.CachePrompts
			}
		}
	}

	return a
}

// GetMaxTokens returns max tokens
func (a *AdvancedSettings) GetMaxTokens() int {
	return a.maxTokens
}

// GetTemperature returns temperature
func (a *AdvancedSettings) GetTemperature() float64 {
	return a.temperature
}

// GetStreamResponse returns stream response setting
func (a *AdvancedSettings) GetStreamResponse() bool {
	return a.streamResponse
}

// GetCachePrompts returns cache prompts setting
func (a *AdvancedSettings) GetCachePrompts() bool {
	return a.cachePrompts
}

// SetMaxTokens sets max tokens
func (a *AdvancedSettings) SetMaxTokens(value int) {
	if value < 256 {
		value = 256
	}
	if value > 200000 {
		value = 200000
	}
	a.maxTokens = value
	if a.configManager != nil {
		if config, err := a.configManager.LoadConfig(); err == nil {
			config.MaxTokens = a.maxTokens
			_ = a.configManager.SaveConfig(config)
		}
	}
}

// AdjustMaxTokens adjusts max tokens by delta
func (a *AdvancedSettings) AdjustMaxTokens(delta int) {
	a.SetMaxTokens(a.maxTokens + delta)
}

// SetTemperature sets temperature
func (a *AdvancedSettings) SetTemperature(value float64) {
	if value < 0.0 {
		value = 0.0
	}
	if value > 1.0 {
		value = 1.0
	}
	a.temperature = value
	if a.configManager != nil {
		if config, err := a.configManager.LoadConfig(); err == nil {
			config.Temperature = a.temperature
			_ = a.configManager.SaveConfig(config)
		}
	}
}

// AdjustTemperature adjusts temperature by delta
func (a *AdvancedSettings) AdjustTemperature(delta float64) {
	a.SetTemperature(a.temperature + delta)
}

// ToggleStreamResponse toggles stream response
func (a *AdvancedSettings) ToggleStreamResponse() {
	a.streamResponse = !a.streamResponse
	if a.configManager != nil {
		if config, err := a.configManager.LoadConfig(); err == nil {
			v := a.streamResponse
			config.StreamResponse = &v
			_ = a.configManager.SaveConfig(config)
		}
	}
}

// ToggleCachePrompts toggles cache prompts
func (a *AdvancedSettings) ToggleCachePrompts() {
	a.cachePrompts = !a.cachePrompts
	if a.configManager != nil {
		if config, err := a.configManager.LoadConfig(); err == nil {
			v := a.cachePrompts
			config.CachePrompts = &v
			_ = a.configManager.SaveConfig(config)
		}
	}
}

// GetAdvancedToolMode returns whether advanced tool mode (deferred loading) is enabled
func (a *AdvancedSettings) GetAdvancedToolMode() bool {
	return a.advancedToolMode
}

// ToggleAdvancedToolMode toggles the advanced tool mode and persists to config
func (a *AdvancedSettings) ToggleAdvancedToolMode() {
	a.advancedToolMode = !a.advancedToolMode

	// Persist to config
	if a.configManager != nil {
		if config, err := a.configManager.LoadConfig(); err == nil {
			config.AdvancedToolMode = a.advancedToolMode
			_ = a.configManager.SaveConfig(config)
		}
	}

	// Notify callback
	if a.onAdvancedToolModeChange != nil {
		a.onAdvancedToolModeChange(a.advancedToolMode)
	}
}

// SetAdvancedToolModeCallback sets the callback for when advanced tool mode is toggled
func (a *AdvancedSettings) SetAdvancedToolModeCallback(fn func(enabled bool)) {
	a.onAdvancedToolModeChange = fn
}

// GetDeferTokenThreshold returns the token threshold for auto-deferring tools
func (a *AdvancedSettings) GetDeferTokenThreshold() int {
	return a.deferTokenThreshold
}

// SetDeferTokenThreshold sets the token threshold and persists to config
func (a *AdvancedSettings) SetDeferTokenThreshold(value int) {
	if value < 100 {
		value = 100
	}
	if value > 5000 {
		value = 5000
	}
	a.deferTokenThreshold = value

	// Persist to config
	if a.configManager != nil {
		if config, err := a.configManager.LoadConfig(); err == nil {
			config.DeferTokenThreshold = a.deferTokenThreshold
			_ = a.configManager.SaveConfig(config)
		}
	}
}

// AdjustDeferTokenThreshold adjusts the defer token threshold by delta
func (a *AdvancedSettings) AdjustDeferTokenThreshold(delta int) {
	a.SetDeferTokenThreshold(a.deferTokenThreshold + delta)
}

// HandleKey handles keyboard input for advanced settings
func (a *AdvancedSettings) HandleKey(key string, state *State) bool {
	return false
}

// Render renders the advanced settings view
func (a *AdvancedSettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)

	const (
		borderWidth      = 2
		containerPadding = 1
		titleHeight      = 4
		hintHeight       = 2
	)

	innerWidth := max(20, width-(borderWidth*2)-(containerPadding*2))
	innerHeight := max(5, height-(borderWidth*2)-(containerPadding*2)-titleHeight-hintHeight)

	// Render sections
	title := a.renderTitle(innerWidth, th)
	content := a.renderSettings(innerWidth, innerHeight, state, th)
	hints := a.renderHintBar(innerWidth, th)

	// Combine sections
	fullContent := lipgloss.JoinVertical(lipgloss.Left,
		title,
		content,
		hints,
	)

	// Apply container border
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(max(20, width-2)).
		Padding(0, containerPadding)

	return containerStyle.Render(fullContent)
}

// renderTitle renders the section title with centered styling
func (a *AdvancedSettings) renderTitle(width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.advanced.title"))

	if width < 50 {
		return title
	}

	// Warning/info message
	warningStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Warning)).
		Italic(true).
		Width(width).
		Align(lipgloss.Center)

	warning := warningStyle.Render(i18n.T("settings.advanced.warning"))

	return lipgloss.JoinVertical(lipgloss.Center, title, warning)
}

// renderSettings renders the settings items
func (a *AdvancedSettings) renderSettings(width, height int, state *State, th Theme) string {
	var lines []string

	// Settings items
	type settingItem struct {
		label       string
		description string
		itemType    string // "toggle", "number", "temperature"
		value       any
	}

	items := []settingItem{
		{i18n.T("settings.advanced.max_tokens.label"), i18n.T("settings.advanced.max_tokens.description"), "number", a.maxTokens},
		{i18n.T("settings.advanced.temperature.label"), i18n.T("settings.advanced.temperature.description"), "temperature", a.temperature},
		{i18n.T("settings.advanced.stream_response.label"), i18n.T("settings.advanced.stream_response.description"), "toggle", a.streamResponse},
		{i18n.T("settings.advanced.cache_prompts.label"), i18n.T("settings.advanced.cache_prompts.description"), "toggle", a.cachePrompts},
		{i18n.T("settings.advanced.tool_mode.label"), i18n.T("settings.advanced.tool_mode.description"), "toggle", a.advancedToolMode},
		{i18n.T("settings.advanced.defer_threshold.label"), i18n.T("settings.advanced.defer_threshold.description"), "number", a.deferTokenThreshold},
	}

	labelPad := 2
	descPad := 4
	if width < 50 {
		labelPad = 1
		descPad = 2
	}

	for i, item := range items {
		isSelected := i == state.SelectedItem && state.Focus == FocusContent

		// Label
		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Bold(isSelected).
			Padding(0, labelPad)
		if isSelected {
			labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
		}
		label := labelStyle.Render(fmt.Sprintf("%s %s", map[bool]string{true: "▶", false: " "}[isSelected], item.label))
		lines = append(lines, label)

		// Description - hide at very narrow widths
		if width >= 50 {
			descStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Padding(0, descPad)
			desc := descStyle.Render(item.description)
			lines = append(lines, desc)
		}

		// Control
		var control string
		switch item.itemType {
		case "toggle":
			control = renderToggle(item.value.(bool), isSelected, th)
		case "number":
			control = renderNumber(item.value.(int), isSelected, th)
		case "temperature":
			control = renderTemperature(item.value.(float64), isSelected, th)
		}
		lines = append(lines, control, "")
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return content
}

// renderHintBar renders keyboard navigation hints at the bottom
func (a *AdvancedSettings) renderHintBar(width int, th Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)

	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	hints := i18n.T("settings.advanced.hints",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Enter"),
		keyStyle.Render("-"),
		keyStyle.Render("+"))

	return hintStyle.Render(hints)
}

func renderTemperature(value float64, isSelected bool, th Theme) string {
	tempStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 1).
		Margin(0, 2)

	if isSelected {
		tempStyle = tempStyle.
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Primary))
	}

	return tempStyle.Render(i18n.T("settings.advanced.temperature.control", value))
}

// GetToolOverrides returns the current tool overrides
func (a *AdvancedSettings) GetToolOverrides() map[string]bool {
	if a.configManager == nil {
		return make(map[string]bool)
	}
	if config, err := a.configManager.LoadConfig(); err == nil {
		if config.ToolOverrides == nil {
			return make(map[string]bool)
		}
		return config.ToolOverrides
	}
	return make(map[string]bool)
}

// SetToolOverride sets whether a tool should be deferred
func (a *AdvancedSettings) SetToolOverride(toolName string, shouldDefer bool) error {
	if a.configManager == nil {
		return nil
	}

	config, err := a.configManager.LoadConfig()
	if err != nil {
		config = &commands.SwarmOSConfig{}
	}

	if config.ToolOverrides == nil {
		config.ToolOverrides = make(map[string]bool)
	}

	config.ToolOverrides[toolName] = shouldDefer
	return a.configManager.SaveConfig(config)
}

// RemoveToolOverride removes an override for a tool
func (a *AdvancedSettings) RemoveToolOverride(toolName string) error {
	if a.configManager == nil {
		return nil
	}

	config, err := a.configManager.LoadConfig()
	if err != nil {
		return nil
	}

	if config.ToolOverrides != nil {
		delete(config.ToolOverrides, toolName)
	}

	return a.configManager.SaveConfig(config)
}
