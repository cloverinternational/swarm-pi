package settings

import (
	"runtime"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ComputerUseSettings handles Computer Use configuration.
type ComputerUseSettings struct {
	enabled       bool
	configManager *commands.ConfigManager

	// Callback when enabled state changes
	onEnabledChange func(enabled bool)
}

// NewComputerUseSettings creates a new Computer Use settings handler.
func NewComputerUseSettings(cm *commands.ConfigManager) *ComputerUseSettings {
	s := &ComputerUseSettings{
		configManager: cm,
		enabled:       false, // Disabled by default
	}

	// Load from config
	if cm != nil {
		if config, err := cm.LoadConfig(); err == nil {
			s.enabled = config.ComputerUseEnabled
		}
	}

	return s
}

// IsEnabled returns whether Computer Use is enabled.
func (s *ComputerUseSettings) IsEnabled() bool {
	return s.enabled
}

// SetEnabled sets whether Computer Use is enabled.
func (s *ComputerUseSettings) SetEnabled(enabled bool) {
	s.enabled = enabled

	// Persist to config
	saveErr := error(nil)
	if s.configManager != nil {
		config, err := s.configManager.LoadConfig()
		if err != nil {
			config = &commands.SwarmOSConfig{}
		}
		config.ComputerUseEnabled = enabled
		saveErr = s.configManager.SaveConfig(config)
		if saveErr != nil {
			logDebug("[ComputerUseSettings] save failed: could not save config: %v", saveErr)
		} else {
			logDebug("[ComputerUseSettings] settings saved successfully")
		}
	}

	// Notify callback only if save succeeded (or no configManager which is a different issue)
	if s.onEnabledChange != nil && saveErr == nil {
		s.onEnabledChange(enabled)
	}
}

// SetOnEnabledChange sets the callback for when enabled state changes.
func (s *ComputerUseSettings) SetOnEnabledChange(fn func(enabled bool)) {
	s.onEnabledChange = fn
}

// ReloadFromConfig reloads settings from the configuration file
func (s *ComputerUseSettings) ReloadFromConfig() {
	if s.configManager == nil {
		return
	}

	if config, err := s.configManager.LoadConfig(); err == nil && config != nil {
		s.enabled = config.ComputerUseEnabled
	}
}

// IsSupported returns whether Computer Use is supported on this platform.
func (s *ComputerUseSettings) IsSupported() bool {
	return runtime.GOOS == "linux"
}

// GetDisplayName returns a human-readable platform name.
func (s *ComputerUseSettings) GetPlatformName() string {
	switch runtime.GOOS {
	case "linux":
		return "Linux (X11/Wayland)"
	case "darwin":
		return "macOS (not supported - use Claude Code)"
	case "windows":
		return "Windows (not supported)"
	default:
		return runtime.GOOS
	}
}

// Render renders the Computer Use settings panel.
func (s *ComputerUseSettings) Render(width int) string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#39D2C0"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
	enabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4CAF50"))
	disabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F44336"))
	itemStyle := lipgloss.NewStyle().PaddingLeft(2)

	b.WriteString(titleStyle.Render("Computer Use"))
	b.WriteByte('\n')
	b.WriteString(descStyle.Render("Screen control and input automation tools"))
	b.WriteByte('\n')
	b.WriteByte('\n')

	// Platform support
	platformLabel := "Platform: "
	b.WriteString(platformLabel)
	if s.IsSupported() {
		b.WriteString(enabledStyle.Render(s.GetPlatformName()))
	} else {
		b.WriteString(disabledStyle.Render(s.GetPlatformName() + " - not supported"))
	}
	b.WriteByte('\n')
	b.WriteByte('\n')

	// Enable/disable toggle
	b.WriteString("Tools: ")
	if s.enabled {
		b.WriteString(enabledStyle.Render("Enabled"))
	} else {
		b.WriteString(disabledStyle.Render("Disabled"))
	}
	b.WriteByte('\n')
	b.WriteByte('\n')

	// Instructions
	b.WriteString(descStyle.Render("Press Enter to toggle."))
	b.WriteByte('\n')
	b.WriteByte('\n')

	// Tool list (when enabled)
	if s.enabled && s.IsSupported() {
		b.WriteString(titleStyle.Render("Available Tools:"))
		b.WriteByte('\n')

		tools := []string{
			"screenshot - Capture screen as JPEG",
			"mouse_move - Move cursor to coordinates",
			"left_click, right_click - Mouse clicks",
			"type - Type text via keyboard",
			"key - Press key combinations",
			"scroll - Scroll at position",
			"read_clipboard, write_clipboard - Clipboard access",
		}

		for _, tool := range tools {
			b.WriteString(itemStyle.Render("• " + tool))
			b.WriteByte('\n')
		}
	}

	return i18n.SettingsIntegrationsText(b.String())
}

// HandleKey handles keyboard input for the settings panel.
func (s *ComputerUseSettings) HandleKey(key string) (handled bool, action string) {
	switch key {
	case "enter", " ":
		if s.IsSupported() {
			s.SetEnabled(!s.enabled)
			if s.enabled {
				return true, i18n.T("settings.integrations.computer.enabled_notice")
			}
			return true, i18n.T("settings.integrations.computer.disabled_notice")
		}
		return true, i18n.T("settings.integrations.computer.unsupported_notice")
	}
	return false, ""
}

// Items returns the menu items for Computer Use settings.
func (s *ComputerUseSettings) Items() []SettingsItem {
	status := i18n.T("settings.integrations.common.disabled")
	if s.enabled {
		status = i18n.T("settings.integrations.common.enabled")
	}

	if !s.IsSupported() {
		status = i18n.T("settings.integrations.computer.unsupported_on", runtime.GOOS)
	}

	return []SettingsItem{
		{
			Label:       i18n.T("settings.integrations.computer.enable_tools"),
			Description: i18n.T("settings.integrations.computer.automation_status", status),
			Value:       s.enabled,
			Type:        "toggle",
		},
	}
}

// SettingsItem represents a settings menu item.
type SettingsItem struct {
	Label       string
	Description string
	Value       bool
	Type        string
}
