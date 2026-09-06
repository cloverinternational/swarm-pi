package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// ProxyConfig represents a proxy configuration
type ProxyConfig struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key,omitempty"` // Proxy API key for authentication
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	IsDefault   bool   `json:"is_default"`
}

// ProxySettings handles proxy configuration
type ProxySettings struct {
	proxies       []ProxyConfig
	selectedIndex int
	configPath    string

	// Form state for adding/editing proxies
	formEditing     bool
	formName        string
	formDisplayName string
	formBaseURL     string
	formAPIKey      string
	formDescription string
	formField       int // 0=name, 1=displayName, 2=baseURL, 3=apiKey, 4=description

	// Callback when proxy settings change
	onChangeCallback func()
}

// NewProxySettings creates a new proxy settings handler
func NewProxySettings() *ProxySettings {
	homeDir, _ := os.UserHomeDir()
	configDir := filepath.Join(homeDir, ".swarmos")
	configPath := filepath.Join(configDir, "proxies.json")

	ps := &ProxySettings{
		proxies:    []ProxyConfig{},
		configPath: configPath,
	}

	// Load existing proxies
	ps.LoadProxies()

	// Add built-in proxies if none exist
	if len(ps.proxies) == 0 {
		ps.proxies = ps.getBuiltinProxies()
		ps.SaveProxies()
	}

	return ps
}

// getBuiltinProxies returns default proxy configurations
func (ps *ProxySettings) getBuiltinProxies() []ProxyConfig {
	return []ProxyConfig{
		{
			Name:        "direct",
			DisplayName: "Direct (No Proxy)",
			BaseURL:     "",
			APIKey:      "",
			Description: "Connect directly to provider APIs",
			Enabled:     true,
			IsDefault:   true,
		},
		{
			Name:        "swarmcode-proxy",
			DisplayName: "SwarmCode Proxy",
			BaseURL:     "http://localhost:8080",
			APIKey:      "",
			Description: "Local SwarmCode Provider Proxy",
			Enabled:     false,
			IsDefault:   false,
		},
	}
}

// LoadProxies loads proxy configurations from disk
func (ps *ProxySettings) LoadProxies() error {
	data, err := os.ReadFile(ps.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read proxies.json: %w", err)
	}

	return json.Unmarshal(data, &ps.proxies)
}

// SaveProxies saves proxy configurations to disk
func (ps *ProxySettings) SaveProxies() error {
	// Ensure config directory exists
	dir := filepath.Dir(ps.configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(ps.proxies, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal proxies: %w", err)
	}

	return os.WriteFile(ps.configPath, data, 0600) // Secure permissions for API keys
}

// GetProxies returns all proxies
func (ps *ProxySettings) GetProxies() []ProxyConfig {
	return ps.proxies
}

// GetDefaultProxy returns the default proxy configuration
func (ps *ProxySettings) GetDefaultProxy() *ProxyConfig {
	for i := range ps.proxies {
		if ps.proxies[i].IsDefault {
			return &ps.proxies[i]
		}
	}

	// Return direct if no default set
	for i := range ps.proxies {
		if ps.proxies[i].Name == "direct" {
			return &ps.proxies[i]
		}
	}

	return nil
}

// GetProxyForProvider returns the base URL to use for a provider
// Note: Anthropic SDK appends "/v1/messages" to BaseURL
// Proxy expects: /v1/{provider}/messages (e.g., /v1/anthropic/messages)
// We need to construct BaseURL so that: BaseURL + "/v1/messages" = "proxy/v1/anthropic/messages"
// Solution: Remove "/v1" from the middle by returning "proxy/v1/anthropic" without the trailing /v1
func (ps *ProxySettings) GetProxyForProvider(providerName string) string {
	defaultProxy := ps.GetDefaultProxy()
	if defaultProxy == nil || !defaultProxy.Enabled {
		return ""
	}

	if defaultProxy.Name == "direct" || defaultProxy.BaseURL == "" {
		return ""
	}

	// Proxy format: /v1/{provider}/{endpoint}
	// SDK appends: /v1/messages
	// We want: proxy.com/v1/anthropic/messages
	// SDK will do: BaseURL + "/v1/messages"
	// So BaseURL should be: proxy.com/v1/anthropic (remove the /v1 that SDK adds)
	baseURL := strings.TrimRight(defaultProxy.BaseURL, "/")

	// For now, just return proxy base URL
	// The proxy needs to be smart enough to route based on the provider
	return baseURL
}

// GetProxyAPIKey returns the API key for the default proxy
func (ps *ProxySettings) GetProxyAPIKey() string {
	defaultProxy := ps.GetDefaultProxy()
	if defaultProxy == nil || !defaultProxy.Enabled {
		return ""
	}

	return defaultProxy.APIKey
}

// ToggleProxy toggles a proxy's enabled state
func (ps *ProxySettings) ToggleProxy(index int) {
	if index < 0 || index >= len(ps.proxies) {
		return
	}
	ps.proxies[index].Enabled = !ps.proxies[index].Enabled
	ps.SaveProxies()

	// Notify callback that proxy settings changed
	if ps.onChangeCallback != nil {
		ps.onChangeCallback()
	}
}

// SetDefaultProxy sets a proxy as the default
func (ps *ProxySettings) SetDefaultProxy(index int) {
	if index < 0 || index >= len(ps.proxies) {
		return
	}

	// Clear all defaults
	for i := range ps.proxies {
		ps.proxies[i].IsDefault = false
	}

	// Set new default
	ps.proxies[index].IsDefault = true
	ps.SaveProxies()

	// Notify callback that proxy settings changed
	if ps.onChangeCallback != nil {
		ps.onChangeCallback()
	}
}

// DeleteProxy deletes a proxy
func (ps *ProxySettings) DeleteProxy(index int) error {
	if index < 0 || index >= len(ps.proxies) {
		return fmt.Errorf("invalid index")
	}

	// Don't delete if it's the default
	if ps.proxies[index].IsDefault {
		return fmt.Errorf("cannot delete default proxy")
	}

	// Don't delete built-in "direct" proxy
	if ps.proxies[index].Name == "direct" {
		return fmt.Errorf("cannot delete direct proxy")
	}

	ps.proxies = append(ps.proxies[:index], ps.proxies[index+1:]...)
	return ps.SaveProxies()
}

// StartAddProxy starts the form for adding a new proxy
func (ps *ProxySettings) StartAddProxy() {
	ps.formEditing = true
	ps.formName = ""
	ps.formDisplayName = ""
	ps.formBaseURL = ""
	ps.formAPIKey = ""
	ps.formDescription = ""
	ps.formField = 0
}

// StartEditProxy starts the form for editing an existing proxy
func (ps *ProxySettings) StartEditProxy(index int) {
	if index < 0 || index >= len(ps.proxies) {
		return
	}

	proxy := ps.proxies[index]
	ps.formEditing = true
	ps.formName = proxy.Name
	ps.formDisplayName = proxy.DisplayName
	ps.formBaseURL = proxy.BaseURL
	ps.formAPIKey = proxy.APIKey
	ps.formDescription = proxy.Description
	ps.formField = 0
	ps.selectedIndex = index
}

// CancelForm cancels the proxy form
func (ps *ProxySettings) CancelForm() {
	ps.formEditing = false
}

// IsFormEditing returns whether the form is currently being edited
func (ps *ProxySettings) IsFormEditing() bool {
	return ps.formEditing
}

// SaveForm saves the proxy form
func (ps *ProxySettings) SaveForm() error {
	// Validate
	if ps.formName == "" {
		return fmt.Errorf("name is required")
	}
	if ps.formDisplayName == "" {
		return fmt.Errorf("display name is required")
	}
	if ps.formBaseURL == "" && ps.formName != "direct" {
		return fmt.Errorf("base URL is required")
	}

	// Check for duplicate names (excluding current proxy being edited)
	for i, p := range ps.proxies {
		if p.Name == ps.formName && i != ps.selectedIndex {
			return fmt.Errorf("proxy with name '%s' already exists", ps.formName)
		}
	}

	newProxy := ProxyConfig{
		Name:        ps.formName,
		DisplayName: ps.formDisplayName,
		BaseURL:     strings.TrimRight(ps.formBaseURL, "/"),
		APIKey:      ps.formAPIKey,
		Description: ps.formDescription,
		Enabled:     true,
		IsDefault:   false,
	}

	// Check if editing existing proxy
	if ps.formEditing && ps.selectedIndex < len(ps.proxies) {
		// Preserve IsDefault status
		newProxy.IsDefault = ps.proxies[ps.selectedIndex].IsDefault
		ps.proxies[ps.selectedIndex] = newProxy
	} else {
		ps.proxies = append(ps.proxies, newProxy)
	}

	ps.formEditing = false
	return ps.SaveProxies()
}

// HandleFormInput handles input in the proxy form
func (ps *ProxySettings) HandleFormInput(input string) {
	switch ps.formField {
	case 0: // Name
		ps.formName = input
	case 1: // Display name
		ps.formDisplayName = input
	case 2: // Base URL
		ps.formBaseURL = input
	case 3: // API Key
		ps.formAPIKey = input
	case 4: // Description
		ps.formDescription = input
	}
}

// NextFormField moves to the next form field
func (ps *ProxySettings) NextFormField() {
	ps.formField = (ps.formField + 1) % 5
}

// PrevFormField moves to the previous form field
func (ps *ProxySettings) PrevFormField() {
	ps.formField--
	if ps.formField < 0 {
		ps.formField = 4
	}
}

// GetCurrentFormValue returns the value of the currently focused form field
func (ps *ProxySettings) GetCurrentFormValue() string {
	switch ps.formField {
	case 0:
		return ps.formName
	case 1:
		return ps.formDisplayName
	case 2:
		return ps.formBaseURL
	case 3:
		return ps.formAPIKey
	case 4:
		return ps.formDescription
	default:
		return ""
	}
}

// ClearCurrentField clears the current form field
func (ps *ProxySettings) ClearCurrentField() {
	ps.HandleFormInput("")
}

// HandlePaste handles pasted text in the current form field
func (ps *ProxySettings) HandlePaste(pastedText string) {
	currentValue := ps.GetCurrentFormValue()
	ps.HandleFormInput(currentValue + pastedText)
}

// GetFormField returns the current form field index (for external access)
func (ps *ProxySettings) GetFormField() int {
	return ps.formField
}

// Render renders the proxy settings view
func (ps *ProxySettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)

	if ps.formEditing {
		return ps.renderForm(width, height, th)
	}

	return ps.renderList(width, height, state, th)
}

// renderList renders the proxy list
func (ps *ProxySettings) renderList(width, height int, state *State, th Theme) string {
	const (
		borderWidth      = 2
		containerPadding = 1
		titleHeight      = 4
		hintHeight       = 2
	)

	innerWidth := maxInt(20, width-(borderWidth*2)-(containerPadding*2))
	innerHeight := maxInt(5, height-(borderWidth*2)-(containerPadding*2)-titleHeight-hintHeight)

	title := ps.renderTitle(innerWidth, th)
	content := ps.renderProxyList(innerWidth, innerHeight, state, th)
	hints := ps.renderHints(innerWidth, th)

	fullContent := lipgloss.JoinVertical(lipgloss.Left, title, content, hints)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(0, containerPadding)

	return containerStyle.Render(fullContent)
}

// renderTitle renders the section title
func (ps *ProxySettings) renderTitle(width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render("Proxy Configuration")

	// Status badges
	enabledCount := 0
	defaultProxy := ""
	for _, p := range ps.proxies {
		if p.Enabled {
			enabledCount++
		}
		if p.IsDefault {
			defaultProxy = p.DisplayName
		}
	}

	statusBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Success)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(fmt.Sprintf("● %d/%d Enabled", enabledCount, len(ps.proxies)))

	defaultBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Primary)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Render(fmt.Sprintf("→ %s", defaultProxy))

	if width < 50 {
		// At narrow widths show only enabled count badge
		centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(statusBadge)
		return lipgloss.JoinVertical(lipgloss.Left, title, centeredBadges)
	}

	badgesRow := lipgloss.JoinHorizontal(lipgloss.Center, statusBadge, " ", defaultBadge)
	centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badgesRow)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", centeredBadges)
}

// renderProxyList renders the list of proxies
func (ps *ProxySettings) renderProxyList(width, height int, state *State, th Theme) string {
	var lines []string

	// Calculate visible range
	maxVisible := (height / 5) // Each proxy item ~5 lines
	if maxVisible < 1 {
		maxVisible = 1
	}
	state.MaxVisible = maxVisible

	visibleStart := state.ScrollOffset
	visibleEnd := state.ScrollOffset + maxVisible
	if visibleEnd > len(ps.proxies) {
		visibleEnd = len(ps.proxies)
	}

	for i := visibleStart; i < visibleEnd; i++ {
		proxy := ps.proxies[i]
		isSelected := i == state.SelectedItem && state.Focus == FocusContent

		// Proxy card
		cardStyle := lipgloss.NewStyle().
			Width(maxInt(20, width-4)).
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

		if isSelected {
			cardStyle = cardStyle.BorderForeground(lipgloss.Color(th.Primary))
		} else {
			cardStyle = cardStyle.BorderForeground(lipgloss.Color(th.Border))
		}

		// Header with name and status
		nameStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Text))
		statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success))

		statusText := ""
		if proxy.IsDefault {
			statusText += " [DEFAULT]"
		}
		if proxy.Enabled {
			statusText += " ✓"
		} else {
			statusText += " ✗"
		}

		header := nameStyle.Render(proxy.DisplayName) + statusStyle.Render(statusText)

		// URL
		urlStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
		urlLine := ""
		if proxy.BaseURL != "" {
			urlLine = urlStyle.Render(fmt.Sprintf("URL: %s", proxy.BaseURL))
		} else {
			urlLine = urlStyle.Render("Direct connection (no proxy)")
		}

		// API Key indicator
		keyLine := ""
		if proxy.APIKey != "" {
			keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success))
			keyLine = keyStyle.Render("🔑 API Key configured")
		}

		// Description
		descLine := ""
		if proxy.Description != "" {
			descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim))
			descLine = descStyle.Render(proxy.Description)
		}

		contentLines := []string{header, urlLine}
		if keyLine != "" {
			contentLines = append(contentLines, keyLine)
		}
		if descLine != "" {
			contentLines = append(contentLines, descLine)
		}

		content := lipgloss.JoinVertical(lipgloss.Left, contentLines...)
		lines = append(lines, cardStyle.Render(content), "")
	}

	if len(lines) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(width).
			Align(lipgloss.Center)
		lines = append(lines, emptyStyle.Render("No proxies configured"))
	}

	return strings.Join(lines, "\n")
}

// renderHints renders keyboard hints
func (ps *ProxySettings) renderHints(width int, th Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)

	var hints string
	if width < 45 {
		return ""
	} else if width < 65 {
		hints = "↑↓:Nav  ⏎:Edit  Spc:Toggle  D:Def  A:Add  Esc:Back"
	} else {
		hints = "↑/↓:Navigate  Enter/E:Edit  Space:Toggle  D:Set Default  A:Add  X:Delete  Esc:Back"
	}
	return hintStyle.Render(hints)
}

// renderForm renders the add/edit proxy form
func (ps *ProxySettings) renderForm(width, height int, th Theme) string {
	const (
		borderWidth      = 2
		containerPadding = 2
	)

	formContainerPad := containerPadding
	if width < 50 {
		formContainerPad = 1
	}
	innerWidth := maxInt(20, width-(borderWidth*2)-(formContainerPad*2))

	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(innerWidth).
		Align(lipgloss.Center)

	formTitle := "Add Proxy"
	if ps.selectedIndex < len(ps.proxies) {
		formTitle = "Edit Proxy"
	}
	lines = append(lines, titleStyle.Render(formTitle), "")

	// Form fields
	fields := []struct {
		label       string
		value       string
		index       int
		placeholder string
	}{
		{"Name", ps.formName, 0, "e.g., my-proxy"},
		{"Display Name", ps.formDisplayName, 1, "e.g., My Proxy Server"},
		{"Base URL", ps.formBaseURL, 2, "e.g., http://216.238.82.160:8081"},
		{"API Key", ps.formAPIKey, 3, "e.g., sk-proxy-..."},
		{"Description", ps.formDescription, 4, "Optional description"},
	}

	for _, field := range fields {
		isActive := field.index == ps.formField

		labelStyle := lipgloss.NewStyle().Bold(true)
		if isActive {
			labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
		} else {
			labelStyle = labelStyle.Foreground(lipgloss.Color(th.Text))
		}

		// Show placeholder if field is empty
		displayValue := field.value
		if displayValue == "" && !isActive {
			placeholderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Italic(true)
			displayValue = placeholderStyle.Render(field.placeholder)
		}

		// Mask API key display
		if field.index == 3 && field.value != "" && !isActive {
			displayValue = strings.Repeat("●", len(field.value))
		}

		// Add cursor for active field
		if isActive {
			displayValue = displayValue + "█"
		}

		inputStyle := lipgloss.NewStyle().
			Width(maxInt(20, innerWidth-4)).
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

		if isActive {
			inputStyle = inputStyle.BorderForeground(lipgloss.Color(th.Primary))
		} else {
			inputStyle = inputStyle.BorderForeground(lipgloss.Color(th.Border))
		}

		label := labelStyle.Render(field.label)
		input := inputStyle.Render(displayValue)

		lines = append(lines, label, input, "")
	}

	// Hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(innerWidth).
		Align(lipgloss.Center)
	var formHints string
	if innerWidth < 45 {
		formHints = ""
	} else if innerWidth < 65 {
		formHints = "Tab:Nav  Type:Edit  ⏎:Save  Esc:Cancel"
	} else {
		formHints = "Tab/↑/↓:Navigate  Type:Edit  Ctrl+V:Paste  Enter:Save  Esc:Cancel  Ctrl+U:Clear"
	}
	if formHints != "" {
		lines = append(lines, "", hintStyle.Render(formHints))
	}

	content := strings.Join(lines, "\n")

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(0, formContainerPad)

	return containerStyle.Render(content)
}

// ApplyProxyToProvider applies proxy configuration to a provider config
func (ps *ProxySettings) ApplyProxyToProvider(provider *commands.ProviderConfig) {
	if provider == nil {
		return
	}

	// Get the proxy URL for this provider
	proxyURL := ps.GetProxyForProvider(strings.ToLower(provider.APIType))
	if proxyURL != "" {
		provider.BaseURL = proxyURL
	}
}

// SetOnChangeCallback sets a callback to be called when proxy settings change
func (ps *ProxySettings) SetOnChangeCallback(callback func()) {
	ps.onChangeCallback = callback
}
