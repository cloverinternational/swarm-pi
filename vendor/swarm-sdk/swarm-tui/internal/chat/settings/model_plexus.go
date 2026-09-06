package settings

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

const plexusProviderName = "plexus"

type plexusSyncResultMsg struct {
	generation uint64
	endpoint   string
	secretRef  string
	key        string
	persistKey bool
	models     []commands.ModelConfig
	err        error
}

func (m *ModelSettings) selectedProviderIsPlexus() bool {
	return m.selectedProvider >= 0 && m.selectedProvider < len(m.providers) &&
		strings.EqualFold(m.providers[m.selectedProvider].Name, plexusProviderName)
}

func (m *ModelSettings) openPlexusProvider() {
	if !m.selectedProviderIsPlexus() {
		return
	}
	m.state = "edit_provider"
	m.loadProviderToForm(m.selectedProvider)
	m.formAPIKey = ""
	m.formEditing = false
	m.plexusField = 0
	p := m.providers[m.selectedProvider]
	m.plexusConnected = false
	m.plexusStatusErr = false
	if p.Available || p.APIKeySecretRef != "" {
		m.plexusStatusText = "Credential configured · press Sync aliases to verify"
	} else {
		m.plexusStatusText = "Enter your personal Plexus key to connect"
	}
	m.plexusDefaultAlias = ""
	if strings.EqualFold(m.currentProvider, plexusProviderName) && modelExists(p.Models, m.currentModel) {
		m.plexusDefaultAlias = m.currentModel
	} else if len(p.Models) > 0 {
		m.plexusDefaultAlias = p.Models[0].ID
	}
}

func modelExists(models []commands.ModelInfo, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func (m *ModelSettings) handlePlexusProviderKey(key string) bool {
	if m.formEditing {
		handled := m.handleTextInput(key)
		if handled && (m.plexusField == 0 || m.plexusField == 1) {
			m.plexusRequestGeneration++
			m.plexusConnected = false
			m.plexusStatusText = "Connection settings changed; connect again to validate"
			m.plexusStatusErr = false
		}
		if m.plexusField == 1 && handled {
			m.plexusConnected = false
			m.plexusStatusText = "Key changed · connect again to validate"
			m.plexusStatusErr = false
		}
		return handled
	}

	switch key {
	case "up", "k", "shift+tab":
		if m.plexusField > 0 {
			m.plexusField--
		}
		return true
	case "down", "j", "tab":
		if m.plexusField < 5 {
			m.plexusField++
		}
		return true
	case "left", "h":
		if m.plexusField == 2 {
			m.cyclePlexusDefault(-1)
		}
		return true
	case "right", "l":
		if m.plexusField == 2 {
			m.cyclePlexusDefault(1)
		}
		return true
	case "enter", " ":
		switch m.plexusField {
		case 0:
			m.formField = 2
			m.formEditing = true
			m.formCursorPos = len(m.formAPIEndpoint)
		case 1:
			m.formField = 4
			m.formEditing = true
			m.formCursorPos = len(m.formAPIKey)
		case 2:
			m.cyclePlexusDefault(1)
		case 3:
			m.pendingCmd = m.beginPlexusSync()
		case 4:
			m.usePlexusNow()
		case 5:
			m.disconnectPlexus()
		}
		return true
	case "r":
		m.pendingCmd = m.beginPlexusSync()
		return true
	case "esc", "backspace":
		m.plexusRequestGeneration++
		m.state = "manage_providers"
		m.formEditing = false
		m.formAPIKey = ""
		return true
	}

	if (m.plexusField == 0 || m.plexusField == 1) && len(key) == 1 {
		if m.plexusField == 0 {
			m.formField = 2
			m.formCursorPos = len(m.formAPIEndpoint)
		} else {
			m.formField = 4
			m.formCursorPos = len(m.formAPIKey)
		}
		m.formEditing = true
		handled := m.handleTextInput(key)
		if handled {
			m.plexusRequestGeneration++
			m.plexusConnected = false
			m.plexusStatusText = "Connection settings changed; connect again to validate"
			m.plexusStatusErr = false
		}
		return handled
	}
	return false
}

func (m *ModelSettings) cyclePlexusDefault(delta int) {
	if !m.selectedProviderIsPlexus() {
		return
	}
	models := m.providers[m.selectedProvider].Models
	if len(models) == 0 {
		return
	}
	index := 0
	for i, model := range models {
		if model.ID == m.plexusDefaultAlias {
			index = i
			break
		}
	}
	index = (index + delta + len(models)) % len(models)
	m.plexusDefaultAlias = models[index].ID
}

func (m *ModelSettings) beginPlexusSync() tea.Cmd {
	if !m.selectedProviderIsPlexus() || m.configManager == nil {
		return nil
	}
	endpoint := strings.TrimRight(strings.TrimSpace(m.formAPIEndpoint), "/")
	if endpoint == "" {
		m.setPlexusStatus("Gateway URL is required", true)
		return nil
	}
	p := m.providers[m.selectedProvider]
	key := strings.TrimSpace(m.formAPIKey)
	secretRef := p.APIKeySecretRef
	persistKey := key != ""
	if key == "" {
		storedKey, _, err := m.configManager.LoadCredentials(plexusProviderName)
		if err != nil {
			m.setPlexusStatus(fmt.Sprintf("Could not load the saved Plexus key: %v", err), true)
			return nil
		}
		key = strings.TrimSpace(storedKey)
		persistKey = key != ""
	}
	if key == "" && secretRef != "" {
		if m.resolveProviderSecret == nil {
			m.setPlexusStatus("Unlock Settings → Vault before syncing Plexus", true)
			return nil
		}
		var err error
		key, err = m.resolveProviderSecret(secretRef)
		if err != nil {
			m.setPlexusStatus("Unlock Settings → Vault or reconnect your Plexus key", true)
			return nil
		}
	}
	if key == "" {
		m.setPlexusStatus("Enter your personal Plexus key first", true)
		return nil
	}
	m.plexusRequestGeneration++
	generation := m.plexusRequestGeneration
	m.setPlexusStatus("Connecting and loading authorized aliases...", false)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		models, err := commands.FetchModelsForProvider(ctx, "openai-compatible", endpoint, key)
		if err == nil && len(models) == 0 {
			err = fmt.Errorf("this key has no authorized aliases")
		}
		return plexusSyncResultMsg{
			generation: generation,
			endpoint:   endpoint,
			secretRef:  secretRef,
			key:        key,
			persistKey: persistKey,
			models:     models,
			err:        err,
		}
	}
}

func (m *ModelSettings) applyPlexusSyncResult(msg plexusSyncResultMsg) {
	if msg.generation != m.plexusRequestGeneration {
		return
	}
	if msg.err != nil {
		m.plexusConnected = false
		m.setPlexusStatus(fmt.Sprintf("Connection failed: %v", msg.err), true)
		return
	}
	apiKey := ""
	if msg.persistKey {
		apiKey = msg.key
		msg.secretRef = ""
	}
	if err := m.persistPlexusProvider(msg.endpoint, apiKey, msg.secretRef, msg.models, true); err != nil {
		m.plexusConnected = false
		m.setPlexusStatus(fmt.Sprintf("Connected, but saving failed: %v", err), true)
		return
	}
	previousDefault := m.plexusDefaultAlias
	m.reloadProviders(plexusProviderName)
	if previousDefault != "" && modelExists(m.providers[m.selectedProvider].Models, previousDefault) {
		m.plexusDefaultAlias = previousDefault
	} else {
		m.plexusDefaultAlias = m.providers[m.selectedProvider].Models[0].ID
	}
	m.formAPIKey = ""
	m.plexusConnected = true
	m.plexusLastSync = time.Now()
	m.setPlexusStatus(fmt.Sprintf("Connected   %d authorized aliases   saved in providers.json", len(msg.models)), false)
	if m.onCredentialsChanged != nil {
		m.onCredentialsChanged(plexusProviderName)
	}
}

func (m *ModelSettings) TakePendingCmd() tea.Cmd {
	cmd := m.pendingCmd
	m.pendingCmd = nil
	return cmd
}

func (m *ModelSettings) connectOrSyncPlexus() {
	if !m.selectedProviderIsPlexus() || m.configManager == nil {
		return
	}
	endpoint := strings.TrimRight(strings.TrimSpace(m.formAPIEndpoint), "/")
	if endpoint == "" {
		m.setPlexusStatus("Gateway URL is required", true)
		return
	}

	p := m.providers[m.selectedProvider]
	key := strings.TrimSpace(m.formAPIKey)
	secretRef := p.APIKeySecretRef
	persistKey := key != ""
	if key == "" {
		storedKey, _, err := m.configManager.LoadCredentials(plexusProviderName)
		if err != nil {
			m.setPlexusStatus(fmt.Sprintf("Could not load the saved Plexus key: %v", err), true)
			return
		}
		key = strings.TrimSpace(storedKey)
		persistKey = key != ""
	}
	if key == "" && secretRef != "" {
		if m.resolveProviderSecret == nil {
			m.setPlexusStatus("Unlock Settings → Vault before syncing Plexus", true)
			return
		}
		var err error
		key, err = m.resolveProviderSecret(secretRef)
		if err != nil {
			m.setPlexusStatus("Unlock Settings → Vault or reconnect your Plexus key", true)
			return
		}
	}
	if key == "" {
		m.setPlexusStatus("Enter your personal Plexus key first", true)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := commands.FetchModelsForProvider(ctx, "openai-compatible", endpoint, key)
	if err != nil {
		m.plexusConnected = false
		m.setPlexusStatus(fmt.Sprintf("Connection failed: %v", err), true)
		return
	}
	if len(models) == 0 {
		m.plexusConnected = false
		m.setPlexusStatus("Connected, but this key has no authorized aliases", true)
		return
	}

	apiKey := ""
	if persistKey {
		apiKey = key
		secretRef = ""
	}
	if err := m.persistPlexusProvider(endpoint, apiKey, secretRef, models, true); err != nil {
		m.plexusConnected = false
		m.setPlexusStatus(fmt.Sprintf("Connected, but saving failed: %v", err), true)
		return
	}

	previousDefault := m.plexusDefaultAlias
	m.reloadProviders(plexusProviderName)
	if previousDefault != "" && modelExists(m.providers[m.selectedProvider].Models, previousDefault) {
		m.plexusDefaultAlias = previousDefault
	} else {
		m.plexusDefaultAlias = m.providers[m.selectedProvider].Models[0].ID
	}
	m.formAPIKey = ""
	m.plexusConnected = true
	m.plexusLastSync = time.Now()
	m.setPlexusStatus(fmt.Sprintf("Connected · %d authorized aliases · saved in providers.json", len(models)), false)
	if m.onCredentialsChanged != nil {
		m.onCredentialsChanged(plexusProviderName)
	}
}

func (m *ModelSettings) persistPlexusProvider(endpoint, apiKey, secretRef string, models []commands.ModelConfig, available bool) error {
	providers, err := m.configManager.LoadProviders()
	if err != nil {
		return err
	}
	zero := 0
	for i := range providers {
		if !strings.EqualFold(providers[i].Name, plexusProviderName) {
			continue
		}
		providers[i].BaseURL = endpoint
		providers[i].APIKey = apiKey
		providers[i].APIKeySecretRef = secretRef
		providers[i].Available = available
		providers[i].HTTPMaxRetries = &zero
		providers[i].Models = append([]commands.ModelConfig(nil), models...)
		return m.configManager.SaveProviders(providers)
	}
	return fmt.Errorf("Plexus provider is not configured")
}

func (m *ModelSettings) usePlexusNow() {
	if !m.plexusConnected || m.plexusDefaultAlias == "" {
		m.setPlexusStatus("Connect Plexus and select an authorized alias first", true)
		return
	}
	m.SetModel(plexusProviderName, m.plexusDefaultAlias)
	m.setPlexusStatus("Plexus is now active with "+m.plexusDefaultAlias, false)
}

func (m *ModelSettings) plexusConfigSnapshot() (commands.ProviderConfig, error) {
	providers, err := m.configManager.LoadProviders()
	if err != nil {
		return commands.ProviderConfig{}, err
	}
	for _, provider := range providers {
		if strings.EqualFold(provider.Name, plexusProviderName) {
			return provider, nil
		}
	}
	return commands.ProviderConfig{}, fmt.Errorf("Plexus provider is not configured")
}

func (m *ModelSettings) restorePlexusConfig(snapshot commands.ProviderConfig) error {
	providers, err := m.configManager.LoadProviders()
	if err != nil {
		return err
	}
	for i := range providers {
		if strings.EqualFold(providers[i].Name, plexusProviderName) {
			providers[i] = snapshot
			return m.configManager.SaveProviders(providers)
		}
	}
	return fmt.Errorf("Plexus provider is not configured")
}

func (m *ModelSettings) disconnectPlexus() {
	if !m.selectedProviderIsPlexus() || m.configManager == nil {
		return
	}
	m.plexusRequestGeneration++
	p := m.providers[m.selectedProvider]
	snapshot, err := m.plexusConfigSnapshot()
	if err != nil {
		m.setPlexusStatus(fmt.Sprintf("Disconnect failed: %v", err), true)
		return
	}
	if p.APIKeySecretRef != "" {
		if m.resolveProviderSecret == nil || m.deleteProviderSecret == nil {
			m.setPlexusStatus("Unlock Settings → Vault before disconnecting", true)
			return
		}
		if _, err := m.resolveProviderSecret(p.APIKeySecretRef); err != nil {
			m.setPlexusStatus("Unlock Settings → Vault before disconnecting", true)
			return
		}
	}
	// Clear the provider reference before deleting the credential. A failed
	// config save therefore leaves the previous working state recoverable.
	if err := m.persistPlexusProvider(m.formAPIEndpoint, "", "", nil, false); err != nil {
		m.setPlexusStatus(fmt.Sprintf("Disconnect failed: %v", err), true)
		return
	}
	if p.APIKeySecretRef != "" {
		if err := m.deleteProviderSecret(p.APIKeySecretRef); err != nil {
			if restoreErr := m.restorePlexusConfig(snapshot); restoreErr != nil {
				m.setPlexusStatus(fmt.Sprintf("Disconnect failed and rollback failed: %v", restoreErr), true)
			} else {
				m.setPlexusStatus("Disconnect failed; the previous configuration was restored", true)
			}
			m.reloadProviders(plexusProviderName)
			return
		}
	}
	m.reloadProviders(plexusProviderName)
	m.formAPIKey = ""
	m.plexusDefaultAlias = ""
	m.plexusConnected = false
	m.setPlexusStatus("Disconnected · enter a personal key to reconnect", false)
	if m.onCredentialsChanged != nil {
		m.onCredentialsChanged(plexusProviderName)
	}
}

func (m *ModelSettings) setPlexusStatus(text string, isError bool) {
	m.plexusStatusText = text
	m.plexusStatusErr = isError
}

func (m *ModelSettings) renderPlexusProvider(width, height int, th Theme) string {
	selected := func(field int) string {
		if m.plexusField == field {
			return "▶ "
		}
		return "  "
	}
	value := func(text string, enabled bool) string {
		style := lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight)).Padding(0, 1)
		if !enabled {
			style = style.Foreground(lipgloss.Color(th.TextMuted))
		}
		return style.Render(text)
	}

	keyDisplay := "Paste personal key..."
	if strings.TrimSpace(m.formAPIKey) != "" {
		keyDisplay = strings.Repeat("•", len([]rune(m.formAPIKey)))
		if m.formEditing && m.plexusField == 1 {
			keyDisplay += "│"
		}
	} else if m.selectedProviderIsPlexus() && m.providers[m.selectedProvider].APIKeySecretRef != "" {
		keyDisplay = "•••••••••••• (legacy Vault credential)"
	} else if m.selectedProviderIsPlexus() && m.providers[m.selectedProvider].Available {
		keyDisplay = "•••••••••••• (saved in providers.json)"
	}
	statusColor := th.Success
	if m.plexusStatusErr {
		statusColor = th.Error
	}
	status := lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Render(m.plexusStatusText)

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render("PLEXUS GATEWAY"))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("Route Swarm models through your organization gateway"))
	b.WriteString("\n\n")
	b.WriteString(selected(0) + "Gateway URL\n   " + value(m.formAPIEndpoint, true) + "\n\n")
	b.WriteString(selected(1) + "Personal API Key\n   " + value(keyDisplay, true) + "\n")
	b.WriteString("   " + status + "\n\n")

	aliasesEnabled := m.plexusConnected && m.selectedProviderIsPlexus()
	b.WriteString("  AVAILABLE ALIASES")
	if aliasesEnabled {
		b.WriteString(fmt.Sprintf(" · %d authorized", len(m.providers[m.selectedProvider].Models)))
	} else {
		b.WriteString(" · connect a valid key to enable")
	}
	if !m.plexusLastSync.IsZero() {
		b.WriteString(" · synced " + m.plexusLastSync.Format("15:04:05"))
	}
	b.WriteString("\n")
	if aliasesEnabled {
		for _, model := range m.providers[m.selectedProvider].Models {
			marker := "  "
			if model.ID == m.plexusDefaultAlias {
				marker = "● "
			}
			b.WriteString("   " + marker + model.ID + " · Authorized\n")
		}
	} else {
		b.WriteString("   (locked until the key is validated)\n")
	}
	b.WriteString("\n")
	defaultText := "locked"
	if aliasesEnabled && m.plexusDefaultAlias != "" {
		defaultText = "‹ " + m.plexusDefaultAlias + " ›"
	}
	b.WriteString(selected(2) + "Default alias  " + value(defaultText, aliasesEnabled) + "\n\n")
	action := "Connect & load aliases"
	if m.plexusConnected {
		action = "Sync aliases"
	}
	b.WriteString(selected(3) + "[ " + action + " ]\n")
	b.WriteString(selected(4) + "[ Use Plexus now ]")
	if !aliasesEnabled {
		b.WriteString(" (disabled)")
	}
	b.WriteString("\n")
	b.WriteString(selected(5) + "[ Disconnect ]\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("↑/↓ navigate · enter edit/activate · ←/→ default alias · r sync · esc back"))
	return b.String()
}
