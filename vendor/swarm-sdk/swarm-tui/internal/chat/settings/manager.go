package settings

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// DebugLogFunc is a function type for debug logging
type DebugLogFunc func(category, format string, args ...any)

// debugLogFunc is the actual debug log function, set by the app
var debugLogFunc DebugLogFunc

// SetDebugLogFunc sets the debug log function
func SetDebugLogFunc(fn DebugLogFunc) {
	debugLogFunc = fn
}

// logDebug logs a debug message to the debug screen
func logDebug(format string, args ...any) {
	if debugLogFunc != nil {
		debugLogFunc("SETTINGS", format, args...)
	}
}

// extractField uses reflection to extract a string field from an interface
func extractField(v any, fieldName string) string {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return ""
	}
	field := rv.FieldByName(fieldName)
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return field.String()
}

// Manager handles all settings sections and navigation
type Manager struct {
	state *State

	// Shared ConfigManager to prevent race conditions between settings
	configManager *commands.ConfigManager
	// Centralized save coordinator for all settings
	saveCoordinator *SaveCoordinator

	// Section handlers
	general       *GeneralSettings
	security      *SecuritySettings
	display       *DisplaySettings
	theme         *ThemeSettings // Theme & Background
	model         *ModelSettings
	modelProfiles *SectionModelProfiles // Unified Models section: profiles + providers
	agents        *AgentsSettings
	profiles      *ProfileSettings       // Agent profile configuration (kept for configBundles)
	configBundles *ConfigBundlesSettings // Configuration bundle management
	configSource  *ConfigSourceSettings  // Global/project config source switching
	auth          *AuthSettings

	// --- Config Source Provider (set externally) ---
	configSourceProvider configbundle.ConfigSourceProvider
	advanced             *AdvancedSettings
	cache                *CacheSettings
	mcp                  *MCPSettings
	proxies              *ProxySettings
	hooks                *HooksSettings
	skills               *SkillsSettings
	plugins              *PluginsSettings // Claude Code-compatible plugins
	systemPrompt         *SystemPromptSettings
	context              *ContextSettings
	compaction           *CompactionSettings
	plan                 *PlanSettings // Plan-before-act workflow
	reliability          *ReliabilitySettings
	voice                *VoiceSettings       // Voice input settings
	steering             *SteeringSettings    // Steering configuration
	webSearch            *WebSearchSettings   // Web search backend settings
	vault                *VaultSettings       // Credential vault management
	computerUse          *ComputerUseSettings // Screen control and input automation

	// Callbacks for actions
	onModelChange         func() tea.Cmd
	onAuthChange          func() tea.Cmd
	onRenderChange        func() tea.Cmd
	onMCPChange           func() tea.Cmd
	onProxyChange         func() tea.Cmd // Callback when proxy settings change
	onPromptChange        func(string) tea.Cmd
	onHookChange          func(name string, enabled bool) tea.Cmd
	onAgentChange         func(string) tea.Cmd
	onProfileChange       func(profileID string) tea.Cmd              // Callback when a profile is activated as default
	onProfileSaved        func(profileID, profileName string) tea.Cmd // Callback when profile roles are saved
	onToolToggle          func(serverName, toolName string, enabled bool) error
	onServerDelete        func(serverName string) error // Callback to delete MCP server
	onSpinnerChange       func(spinnerType string) tea.Cmd
	onDisplaySettingsSync func(showThinking, showFullToolOutput, richAnimations, copySelectionShortcut, autoCopySelectionOnMouse bool) tea.Cmd
	onThemeChange         func(themeName, backgroundMode string) tea.Cmd
	onStartupViewChange   func(startupView string) tea.Cmd

	// pendingCmd holds an async tea.Cmd produced by a section refresh (e.g. the
	// auth screen's expired-token refresh) so it can be drained and dispatched on
	// the next HandleMessage tick, since RefreshSection/SelectSection callers do
	// not propagate cmds. BUG #1 wiring.
	pendingCmd tea.Cmd
}

// SetConfigSourceProvider sets the config bundle provider for config source settings.
func (m *Manager) SetConfigSourceProvider(provider configbundle.ConfigSourceProvider) {
	m.configSourceProvider = provider
	if m.configSource == nil {
		m.configSource = NewConfigSourceSettings(provider)
	} else {
		m.configSource.configBundle = provider
		m.configSource.Refresh()
	}
	// Set the create project callback if the provider supports it
	m.configSource.SetOnCreateProject(func() error {
		return provider.CreateProjectConfig("")
	})
}

// NewManager creates a new settings manager
func NewManager(provider, model string, showThinking, showFullToolOutput, richAnimations, copySelectionShortcut, autoCopySelectionOnMouse bool, spinnerType, startupView string, cacheProvider CacheStatsProvider) *Manager {
	// Determine OAuth status - check if provider is using OAuth
	isOAuth := false
	oauthPrefix := ""
	// Note: OAuth detection will be set via SetOAuthStatus method

	// Create centralized SaveCoordinator to prevent race conditions
	saveCoordinator := NewSaveCoordinator()
	sharedConfigManager := saveCoordinator.GetConfigManager()

	compactionSettings := NewCompactionSettings(sharedConfigManager)
	// Init is called later via Manager.LoadSettings() so the error can be propagated.

	agents := NewAgentsSettings()
	profiles := NewProfileSettings()
	modelProfiles := NewSectionModelProfiles()

	manager := &Manager{
		state:           NewState(),
		configManager:   sharedConfigManager,
		saveCoordinator: saveCoordinator,
		general:         NewGeneralSettings(sharedConfigManager),
		security:        NewSecuritySettings(),
		display:         NewDisplaySettings(showThinking, showFullToolOutput, richAnimations, copySelectionShortcut, autoCopySelectionOnMouse, spinnerType, startupView),
		theme:           NewThemeSettings("SwarmCode", "solid"), // defaults; overridden via SetTheme
		model:           NewModelSettings(provider, model),
		modelProfiles:   modelProfiles,
		agents:          agents,
		profiles:        profiles,
		configBundles:   NewConfigBundlesSettings(profiles.GetManager(), agents),
		auth:            NewAuthSettings(provider, true),
		advanced:        NewAdvancedSettings(cacheProvider),
		cache:           NewCacheSettings(cacheProvider),
		mcp:             NewMCPSettings(),
		proxies:         NewProxySettings(),
		hooks:           NewHooksSettings(),
		skills:          NewSkillsSettings(),
		plugins:         NewPluginsSettings(),
		systemPrompt:    NewSystemPromptSettings(isOAuth, oauthPrefix),
		context:         NewContextSettings(),
		compaction:      compactionSettings,
		plan:            NewPlanSettings(sharedConfigManager),
		reliability:     NewReliabilitySettings(sharedConfigManager),
		voice:           NewVoiceSettings(),
		steering:        NewSteeringSettings(),
		webSearch:       NewWebSearchSettings(sharedConfigManager),
		vault:           NewVaultSettings(NewState()),
		computerUse:     NewComputerUseSettings(sharedConfigManager),
	}

	// Provider secrets are written only to the unlocked encrypted global vault.
	// The provider config persists the returned credential ID, never the secret.
	if manager.model != nil && manager.vault != nil {
		manager.model.SetVaultCredentialCallbacks(
			func(providerName, secret string) (string, error) {
				storage := manager.vault.GetStorage()
				if storage == nil {
					return "", vault.ErrVaultLocked
				}
				id := fmt.Sprintf("provider-%s-api-key", strings.ToLower(strings.TrimSpace(providerName)))
				cred := vault.Credential{
					ID:          id,
					Name:        providerName + " API key",
					Description: i18n.T("settings.vault.provider_credential.description"),
					Kind:        vault.CredentialKindAPIKey,
					Secret:      secret,
					Scope:       vault.ScopeGlobal,
					Tags:        []string{"provider", strings.ToLower(strings.TrimSpace(providerName))},
				}
				if err := storage.Store(context.Background(), cred); err != nil {
					return "", err
				}
				return id, nil
			},
			func(secretRef string) (string, error) {
				storage := manager.vault.GetStorage()
				if storage == nil {
					return "", vault.ErrVaultLocked
				}
				cred, err := storage.Retrieve(context.Background(), secretRef)
				if err != nil {
					return "", err
				}
				if cred.IsExpired() {
					return "", vault.ErrExpired
				}
				return cred.Secret, nil
			},
			func(secretRef string) error {
				storage := manager.vault.GetStorage()
				if storage == nil {
					return vault.ErrVaultLocked
				}
				return storage.Delete(context.Background(), secretRef)
			},
		)
	}

	// Link profile manager to model settings for override warnings
	if manager.model != nil && manager.profiles != nil {
		manager.model.SetProfileManager(manager.profiles.GetManager())
	}

	// Wire ModelSettings into the unified Models section for pixel-perfect provider screens
	if manager.modelProfiles != nil && manager.model != nil {
		manager.modelProfiles.SetModelSettings(manager.model)
	}

	// Share the AuthSettings instance so Models > Authentication delegates to
	// the same OAuth flow state machine that the top-level Authentication
	// section uses.
	if manager.modelProfiles != nil && manager.auth != nil {
		manager.modelProfiles.SetAuthSettings(manager.auth)
	}

	return manager
}

// Init initializes the manager and returns any commands that need to be run.
// This should be called by the parent component's Init method.
func (m *Manager) Init() tea.Cmd {
	var cmds []tea.Cmd

	// Initialize voice settings to discover microphones
	if m.voice != nil {
		if cmd := m.voice.Init(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// LoadSettings loads persisted configuration into all subsystems that require
// it (e.g. compaction model chain) and returns the first error encountered.
// Callers should log or surface this error; settings that fail to load fall
// back to their compile-time defaults so the application remains usable.
func (m *Manager) LoadSettings() error {
	if m.compaction != nil {
		if err := m.compaction.Init(); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) SetCallbacks(modelChange, authChange, renderChange func() tea.Cmd) {
	m.onModelChange = modelChange
	m.onAuthChange = authChange
	m.onRenderChange = renderChange
}

// SetCredentialChangedCallback connects OAuth completion to the application's
// live provider lifecycle.
func (m *Manager) SetCredentialChangedCallback(callback func(provider string) error) {
	if m.auth != nil {
		m.auth.SetCredentialChangedCallback(callback)
	}
}

// SetMCPCallback sets MCP change callback
func (m *Manager) SetMCPCallback(mcpChange func() tea.Cmd) {
	m.onMCPChange = mcpChange
}

// SetProxyCallback sets proxy change callback
func (m *Manager) SetProxyCallback(proxyChange func() tea.Cmd) {
	m.onProxyChange = proxyChange

	// Set callback on ProxySettings to trigger when proxy changes
	if m.proxies != nil {
		m.proxies.SetOnChangeCallback(func() {
			if m.onProxyChange != nil {
				m.onProxyChange()
			}
		})
	}
}

// SetPromptChangeCallback sets the callback for system prompt changes
func (m *Manager) SetPromptChangeCallback(promptChange func(string) tea.Cmd) {
	m.onPromptChange = promptChange
	if m.systemPrompt != nil {
		m.systemPrompt.SetOnPromptChange(func(prompt string) error {
			if promptChange != nil {
				promptChange(prompt)
			}
			return nil
		})
	}
}

// SetHookChangeCallback sets the callback for hook enable/disable changes
func (m *Manager) SetHookChangeCallback(hookChange func(name string, enabled bool) tea.Cmd) {
	m.onHookChange = hookChange
}

// SetSpinnerChangeCallback sets the callback for spinner type changes
func (m *Manager) SetSpinnerChangeCallback(spinnerChange func(spinnerType string) tea.Cmd) {
	m.onSpinnerChange = spinnerChange
}

// SetDisplaySettingsSyncCallback sets the callback for syncing display settings to persistent storage
func (m *Manager) SetDisplaySettingsSyncCallback(syncCallback func(showThinking, showFullToolOutput, richAnimations, copySelectionShortcut, autoCopySelectionOnMouse bool) tea.Cmd) {
	m.onDisplaySettingsSync = syncCallback
}

// SetThemeChangeCallback sets the callback invoked when the user changes the theme or background mode.
// The callback receives (themeName, backgroundMode) and should update app.theme + persist the values.
func (m *Manager) SetThemeChangeCallback(cb func(themeName, backgroundMode string) tea.Cmd) {
	m.onThemeChange = cb
}

// SetStartupViewChangeCallback sets the callback invoked when the user changes the startup view preference.
// The callback receives the new value ("simple" or "advanced") and should persist it.
func (m *Manager) SetStartupViewChangeCallback(cb func(startupView string) tea.Cmd) {
	m.onStartupViewChange = cb
}

// GetDisplayStartupView returns the current startup view preference from the display settings.
func (m *Manager) GetDisplayStartupView() string {
	if m.display != nil {
		return m.display.GetStartupView()
	}
	return "simple"
}

// SetTheme updates the in-memory theme settings (called at startup from persisted values).
func (m *Manager) SetTheme(themeName, backgroundMode string) {
	if m.theme != nil {
		m.theme.SetThemeName(themeName)
		m.theme.SetBackgroundMode(backgroundMode)
	}
}

// GetThemeSettings returns the theme settings handler.
func (m *Manager) GetThemeSettings() *ThemeSettings {
	return m.theme
}

// SetOAuthStatus updates OAuth status for system prompt preview
func (m *Manager) SetOAuthStatus(isOAuth bool, oauthPrefix string) {
	if m.systemPrompt != nil {
		m.systemPrompt.isOAuth = isOAuth
		m.systemPrompt.oauthPrefix = oauthPrefix
	}
}

// IsDirty returns true if any setting has changed since the dirty flag was last reset.
func (m *Manager) IsDirty() bool {
	return m.state != nil && m.state.Dirty
}

// ResetDirty clears the dirty flag (call when entering or leaving the settings tab).
func (m *Manager) ResetDirty() {
	if m.state != nil {
		m.state.Dirty = false
	}
}

// SavePendingChanges flushes any in-progress form edits to persistent storage.
// Call this when the user chooses "Save & Exit" from the settings exit modal.
// For the provider edit form specifically this means committing the form data even
// if the user never navigated to the explicit [Save Provider] button.
// Also flushes compaction and reliability settings that use deferred-save patterns.
func (m *Manager) SavePendingChanges() {
	if m.model != nil {
		m.model.savePendingFormChanges()
	}

	// Flush compaction settings if they have pending changes
	if m.compaction != nil && m.compaction.hasChanges {
		if err := m.compaction.Save(); err != nil {
			logDebug("[Manager] SavePendingChanges: compaction save failed: %v", err)
		}
	}

	// Flush reliability settings if they have pending changes
	if m.reliability != nil && m.reliability.hasChanges {
		if err := m.reliability.Save(); err != nil {
			logDebug("[Manager] SavePendingChanges: reliability save failed: %v", err)
		}
	}
}

func (m *Manager) SelectSection(section Section) {
	// Validate by checking actual membership in the Sections slice rather than
	// comparing the enum ordinal against len(Sections).  The latter breaks when
	// enum constants (e.g. SectionModel, SectionAgentProfiles) occupy ordinal
	// slots that are intentionally absent from the rendered Sections list.
	valid := false
	for _, sec := range Sections {
		if sec.ID == section {
			valid = true
			break
		}
	}
	if !valid {
		return
	}
	m.state.SelectedSection = section
	m.state.SelectedItem = 0
	m.state.ScrollOffset = 0

	// Refresh the section's data from config when entering it
	m.RefreshSection(section)
}

// RefreshSection reloads configuration data for the specified section
func (m *Manager) RefreshSection(section Section) {
	switch section {
	case SectionAuth:
		if m.auth != nil {
			// Refresh() returns an async cmd that refreshes expired active tokens.
			// Stash it for HandleMessage to dispatch (callers here don't take cmds).
			if cmd := m.auth.Refresh(); cmd != nil {
				m.pendingCmd = cmd
			}
		}
	case SectionSteering:
		if m.steering != nil {
			_ = m.steering.Load()
		}
	case SectionPlan:
		if m.plan != nil {
			m.plan.ReloadFromConfig()
		}
	case SectionWebSearch:
		if m.webSearch != nil {
			m.webSearch.ReloadFromConfig()
		}
	case SectionGeneral:
		if m.general != nil {
			m.general.ReloadFromConfig()
		}
	case SectionComputerUse:
		if m.computerUse != nil {
			m.computerUse.ReloadFromConfig()
		}
	case SectionReliability:
		if m.reliability != nil {
			m.reliability.ReloadFromConfig()
		}
	case SectionCompaction:
		if m.compaction != nil {
			m.compaction.ReloadFromConfig()
		}
		// Add other sections as needed
	}
}

func (m *Manager) SetFocus(focus Focus) {
	m.state.Focus = focus
}

func (m *Manager) HandleMouseClick(msg tea.Mouse) (tea.Cmd, bool) {
	if m.state == nil {
		return nil, false
	}
	if m.state.SelectedSection == SectionModel && m.model != nil {
		return m.model.HandleMouseClick(msg)
	}
	if m.state.SelectedSection == SectionModels {
		// Delegate to the embedded ModelSettings for provider-screen click support.
		if m.model != nil {
			return m.model.HandleMouseClick(msg)
		}
		return nil, false
	}
	return nil, false
}

// GetMCPSettings returns MCP settings
func (m *Manager) GetMCPSettings() *MCPSettings {
	return m.mcp
}

// GetSystemPromptSettings returns system prompt settings
func (m *Manager) GetSystemPromptSettings() *SystemPromptSettings {
	return m.systemPrompt
}

// GetActiveSystemPrompt returns the currently active system prompt with OAuth prefix if needed
func (m *Manager) GetActiveSystemPrompt() string {
	if m.systemPrompt != nil {
		return m.systemPrompt.GetActivePromptWithOAuth()
	}
	return "You are a helpful AI assistant."
}

// GetState returns the current state
func (m *Manager) GetState() *State {
	return m.state
}

// GetGeneralSettings returns general settings
func (m *Manager) GetGeneralSettings() *GeneralSettings {
	return m.general
}

// GetSecuritySettings returns security settings
func (m *Manager) GetSecuritySettings() *SecuritySettings {
	return m.security
}

// GetDisplaySettings returns display settings
func (m *Manager) GetDisplaySettings() *DisplaySettings {
	return m.display
}

// GetModelSettings returns model settings
func (m *Manager) GetModelSettings() *ModelSettings {
	return m.model
}

// GetVoiceSettings returns the transcription settings handler.
func (m *Manager) GetVoiceSettings() *VoiceSettings {
	return m.voice
}

// GetAdvancedSettings returns advanced settings
func (m *Manager) GetAdvancedSettings() *AdvancedSettings {
	return m.advanced
}

// GetCompactionSettings returns compaction settings
func (m *Manager) GetCompactionSettings() *CompactionSettings {
	return m.compaction
}

// GetPlanSettings returns Plan Mode workflow settings.
func (m *Manager) GetPlanSettings() *PlanSettings {
	return m.plan
}

// GetReliabilitySettings returns reliability settings.
func (m *Manager) GetReliabilitySettings() *ReliabilitySettings {
	return m.reliability
}

// GetHooksSettings returns hooks settings
func (m *Manager) GetHooksSettings() *HooksSettings {
	return m.hooks
}

// GetContextSettings returns context settings
func (m *Manager) GetContextSettings() *ContextSettings {
	return m.context
}

// GetAgentsSettings returns agents settings
func (m *Manager) GetAgentsSettings() *AgentsSettings {
	return m.agents
}

// GetProfileSettings returns profile settings
func (m *Manager) GetProfileSettings() *ProfileSettings {
	return m.profiles
}

// GetSkillsSettings returns skills settings
func (m *Manager) GetSkillsSettings() *SkillsSettings {
	return m.skills
}

// GetPluginsSettings returns plugins settings
func (m *Manager) GetPluginsSettings() *PluginsSettings {
	return m.plugins
}

// GetVaultSettings returns the vault settings handler.
func (m *Manager) GetVaultSettings() *VaultSettings {
	return m.vault
}

// GetModelProfilesSection returns the unified Models section handler.
func (m *Manager) GetModelProfilesSection() *SectionModelProfiles {
	return m.modelProfiles
}

// GetSteeringSettings returns the steering settings handler.
func (m *Manager) GetSteeringSettings() *SteeringSettings {
	return m.steering
}

// SetSteeringCallbacks sets up the steering config load/save callbacks.
// The save callback receives scope and config, persists via IPC, and returns a tea.Cmd.
// After wiring, an eager Load() populates the UI from disk so the renderer
// shows current values rather than zero defaults.
func (m *Manager) SetSteeringCallbacks(
	loadConfig func(scope string) (*core.SteeringConfig, error),
	saveConfig func(scope string, config *core.SteeringConfig) tea.Cmd,
) {
	if m.steering != nil {
		m.steering.SetLoadConfig(loadConfig)
		m.steering.SetOnConfigChange(saveConfig)
		_ = m.steering.Load()
	}
}

// SetAgentChangeCallback sets the callback for agent changes
func (m *Manager) SetAgentChangeCallback(agentChange func(string) tea.Cmd) {
	m.onAgentChange = agentChange
	if m.agents != nil {
		m.agents.SetOnAgentChange(func(id string) error {
			if agentChange != nil {
				agentChange(id)
			}
			return nil
		})
	}
}

// SetProfileChangeCallback sets the callback invoked when the user activates a profile
// as default (pressing "d" in the Agent Profiles settings screen).
// The callback receives the profile ID and should update the main chat model accordingly.
func (m *Manager) SetProfileChangeCallback(fn func(profileID string) tea.Cmd) {
	m.onProfileChange = fn
}

// SetProfileSavedCallback sets the callback invoked whenever profile roles are
// successfully saved (Ctrl+S / s in the roles editor).
// Receives the profile ID and profile name so the app can show a toast notification.
func (m *Manager) SetProfileSavedCallback(fn func(profileID, profileName string) tea.Cmd) {
	m.onProfileSaved = fn
}

// SetToolToggleCallback sets the callback for tool enable/disable changes
func (m *Manager) SetToolToggleCallback(handler func(serverName, toolName string, enabled bool) error) {
	m.onToolToggle = handler
	if m.mcp != nil {
		m.mcp.SetToolToggleCallback(handler)
	}
}

// SetServerDeleteCallback sets the callback for MCP server deletion
func (m *Manager) SetServerDeleteCallback(handler func(serverName string) error) {
	m.onServerDelete = handler
	if m.mcp != nil {
		m.mcp.SetServerDeleteCallback(handler)
	}
}

// ConvertTheme converts an interface{} theme to the settings Theme type.
// Handles both direct Theme values and struct types with matching field names.
func ConvertTheme(theme any) Theme {
	switch t := theme.(type) {
	case Theme:
		return t
	default:
		return Theme{
			Primary:   extractField(theme, "Primary"),
			Accent:    extractField(theme, "Accent"),
			Text:      extractField(theme, "Text"),
			TextDim:   extractField(theme, "TextDim"),
			TextMuted: extractField(theme, "TextMuted"),
			BG:        extractField(theme, "BG"),
			BGLight:   extractField(theme, "BGLight"),
			BGLighter: extractField(theme, "BGLighter"),
			Border:    extractField(theme, "Border"),
			Success:   extractField(theme, "Success"),
			Warning:   extractField(theme, "Warning"),
			Error:     extractField(theme, "Error"),
		}
	}
}

// RenderSidebar renders just the settings sidebar panel.
// Use this when embedding settings in a custom layout (e.g., home screen tabs).
func (m *Manager) RenderSidebar(width, height int, theme any) string {
	th := ConvertTheme(theme)
	return m.renderSidebar(width, height, th)
}

// RenderContent renders just the settings content panel for the currently selected section.
// Use this when embedding settings in a custom layout.
func (m *Manager) RenderContent(width, height int, theme any, animationFrame int) string {
	th := ConvertTheme(theme)
	return m.renderContentForSection(width, height, th, animationFrame)
}

// RenderHints renders the context-sensitive hints bar for the current settings state.
func (m *Manager) RenderHints(width int, theme any) string {
	th := ConvertTheme(theme)
	return m.renderHints(width, th)
}

// renderContentForSection renders the content for the currently selected section.
func (m *Manager) renderContentForSection(width, height int, th Theme, animationFrame int) string {
	switch m.state.SelectedSection {
	case SectionGeneral:
		return m.general.Render(width, height, m.state, th)
	case SectionSecurity:
		return m.security.Render(width, height, m.state, th)
	case SectionModel:
		return m.model.Render(width, height, m.state, th)
	case SectionModels:
		return m.modelProfiles.Render(width, height, m.state, th)
	case SectionProxies:
		return m.proxies.Render(width, height, m.state, th)
	case SectionAgents:
		return m.agents.Render(width, height, m.state, th)
	case SectionAgentProfiles:
		return m.profiles.Render(width, height, m.state, th)
	case SectionConfigBundles:
		return m.configBundles.Render(width, height)
	case SectionConfigSource:
		if m.configSource != nil {
			return m.configSource.View(width, th)
		}
		return ""
	case SectionMCP:
		return m.mcp.Render(width, height, m.state, th)
	case SectionHooks:
		return m.hooks.Render(width, height, m.state, th)
	case SectionSkills:
		return m.skills.Render(width, height, m.state, th)
	case SectionPlugins:
		return m.plugins.Render(width, height, m.state, th)
	case SectionSystemPrompt:
		return m.systemPrompt.Render(width, height, m.state, th)
	case SectionContext:
		return m.context.Render(width, height, m.state, th)
	case SectionDisplay:
		return m.display.Render(width, height, m.state, th, animationFrame)
	case SectionTheme:
		if m.theme != nil {
			return m.theme.Render(width, height, m.state, th, m.themePresets())
		}
		return ""
	case SectionAuth:
		// Render the AuthSettings UI directly — it owns the OAuth flow state
		// machines for Claude / Codex / Gemini and the account picker.
		if m.auth != nil {
			return m.auth.Render(width, height, m.state, th)
		}
		return ""
	case SectionCompaction:
		// Propagate the active chat model so the per-model override row can
		// display/toggle the override for the current model.
		if m.model != nil {
			m.compaction.SetCurrentChatModel(m.model.CurrentProvider(), m.model.CurrentModel())
		}
		return m.compaction.Render(width, height, m.state, th)
	case SectionPlan:
		if m.plan != nil {
			return m.plan.Render(width, height, m.state, th)
		}
		return ""
	case SectionReliability:
		return m.reliability.Render(width, height, m.state, th)
	case SectionAdvanced:
		return m.advanced.Render(width, height, m.state, th)
	case SectionCache:
		return m.cache.Render(width, height, m.state, th)
	case SectionVoice:
		if m.voice != nil {
			return m.voice.View(width, height, m.state.Focus == FocusContent)
		}
		return ""
	case SectionUpdates:
		return m.renderUpdates()
	case SectionSteering:
		return m.steering.Render(width, height, m.state, th)
	case SectionWebSearch:
		if m.webSearch != nil {
			return m.webSearch.Render(width, height, m.state, th)
		}
		return ""
	case SectionVault:
		if m.vault != nil {
			return m.vault.Render(m.state, width, height)
		}
		return ""
	case SectionComputerUse:
		if m.computerUse != nil {
			return m.computerUse.Render(width)
		}
		return ""
	default:
		return ""
	}
}

// Render renders the full settings view with sidebar
// animationFrame is provided by the shared AnimationClock for preview animations
func (m *Manager) Render(width, height int, theme any, animationFrame int) string {
	th := ConvertTheme(theme)
	hintsH := 2
	contentHeight := maxInt(5, height-hintsH)

	if width >= 80 {
		// Two-pane: sidebar + content
		sidebarWidth := width / 4
		if sidebarWidth < 20 {
			sidebarWidth = 20
		}
		if sidebarWidth > 40 {
			sidebarWidth = 40
		}
		contentWidth := maxInt(30, width-sidebarWidth-1)
		sidebar := m.renderSidebar(sidebarWidth, contentHeight, th)
		content := m.renderContentForSection(contentWidth, contentHeight, th, animationFrame)
		combined := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, content)
		hints := m.renderHints(width, th)
		return lipgloss.JoinVertical(lipgloss.Left, combined, hints)
	}

	// Single-pane: compact section selector + full-width content
	selectorH := 1
	innerContentH := maxInt(5, contentHeight-selectorH)
	selector := m.renderSectionSelector(width, th)
	content := m.renderContentForSection(maxInt(30, width), innerContentH, th, animationFrame)
	hints := m.renderHints(width, th)
	return lipgloss.JoinVertical(lipgloss.Left, selector, content, hints)
}

// renderSectionSelector renders a compact single-line section selector for narrow viewports.
// Shows: < SectionName > with the current section highlighted.
func (m *Manager) renderSectionSelector(width int, th Theme) string {
	// Find current section info
	currentName := i18n.T("settings.title")
	for _, sec := range Sections {
		if sec.ID == m.state.SelectedSection {
			currentName = localizedSectionName(sec)
			break
		}
	}

	arrowStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Background(lipgloss.Color(th.BGLight)).
		Bold(true)

	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Bold(true).
		Padding(0, 1)

	left := arrowStyle.Render(" < ")
	right := arrowStyle.Render(" > ")
	name := nameStyle.Render(currentName)

	center := lipgloss.JoinHorizontal(lipgloss.Center, left, name, right)

	row := lipgloss.NewStyle().
		Width(width).
		Background(lipgloss.Color(th.BGLight)).
		Align(lipgloss.Center).
		Render(center)

	return row
}

// groupedSections returns sections organized by group
func (m *Manager) groupedSections() map[string][]SectionInfo {
	grouped := make(map[string][]SectionInfo)
	for _, section := range Sections {
		group := section.Group
		if group == "" {
			group = "Advanced"
		}
		grouped[group] = append(grouped[group], section)
	}
	return grouped
}

// filteredSections applies search query to grouped sections
func (m *Manager) filteredSections(grouped map[string][]SectionInfo) map[string][]SectionInfo {
	if m.state.SearchQuery == "" {
		return grouped
	}
	query := strings.ToLower(m.state.SearchQuery)
	filtered := make(map[string][]SectionInfo)
	for groupName, sections := range grouped {
		var matches []SectionInfo
		for _, section := range sections {
			if strings.Contains(strings.ToLower(localizedSectionName(section)), query) ||
				strings.Contains(strings.ToLower(localizedSectionDescription(section)), query) ||
				strings.Contains(strings.ToLower(localizedGroupName(groupName)), query) ||
				strings.Contains(strings.ToLower(section.Name), query) ||
				strings.Contains(strings.ToLower(section.Description), query) ||
				strings.Contains(strings.ToLower(groupName), query) {
				matches = append(matches, section)
			}
		}
		if len(matches) > 0 {
			filtered[groupName] = matches
		}
	}
	return filtered
}

// visibleSections returns a flattened list of currently visible section IDs
func (m *Manager) visibleSections() []Section {
	grouped := m.groupedSections()
	filtered := m.filteredSections(grouped)
	var visible []Section
	for _, groupName := range SectionGroups {
		sections, exists := filtered[groupName]
		if !exists || len(sections) == 0 {
			continue
		}
		for _, section := range sections {
			visible = append(visible, section.ID)
		}
	}
	return visible
}

// groupForSection returns the group name for a given section
func groupForSection(sectionID Section) string {
	for _, section := range Sections {
		if section.ID == sectionID {
			return section.Group
		}
	}
	return "Advanced"
}

func localizedSectionName(section SectionInfo) string {
	keys := map[Section]string{
		SectionModels:        "settings.section.models.name",
		SectionAgents:        "settings.section.agents.name",
		SectionConfigSource:  "settings.section.config_source.name",
		SectionSystemPrompt:  "settings.section.system_prompts.name",
		SectionCompaction:    "settings.section.compaction.name",
		SectionPlan:          "settings.section.plan.name",
		SectionMCP:           "settings.section.mcp.name",
		SectionVoice:         "settings.section.voice.name",
		SectionHooks:         "settings.section.hooks.name",
		SectionSteering:      "settings.section.steering.name",
		SectionSkills:        "settings.section.skills.name",
		SectionPlugins:       "settings.section.plugins.name",
		SectionContext:       "settings.section.context.name",
		SectionWebSearch:     "settings.section.web_search.name",
		SectionComputerUse:   "settings.section.computer_use.name",
		SectionDisplay:       "settings.section.display.name",
		SectionTheme:         "settings.section.theme.name",
		SectionSecurity:      "settings.section.security.name",
		SectionAuth:          "settings.section.auth.name",
		SectionVault:         "settings.section.vault.name",
		SectionProxies:       "settings.section.proxies.name",
		SectionCache:         "settings.section.cache.name",
		SectionReliability:   "settings.section.reliability.name",
		SectionUpdates:       "settings.section.updates.name",
		SectionGeneral:       "settings.section.general.name",
		SectionConfigBundles: "settings.section.config_bundles.name",
		SectionAdvanced:      "settings.section.advanced.name",
	}
	if key, ok := keys[section.ID]; ok {
		return i18n.T(key)
	}
	return section.Name
}

func localizedSectionDescription(section SectionInfo) string {
	keys := map[Section]string{
		SectionModels:        "settings.section.models.description",
		SectionAgents:        "settings.section.agents.description",
		SectionConfigSource:  "settings.section.config_source.description",
		SectionSystemPrompt:  "settings.section.system_prompts.description",
		SectionCompaction:    "settings.section.compaction.description",
		SectionPlan:          "settings.section.plan.description",
		SectionMCP:           "settings.section.mcp.description",
		SectionVoice:         "settings.section.voice.description",
		SectionHooks:         "settings.section.hooks.description",
		SectionSteering:      "settings.section.steering.description",
		SectionSkills:        "settings.section.skills.description",
		SectionPlugins:       "settings.section.plugins.description",
		SectionContext:       "settings.section.context.description",
		SectionWebSearch:     "settings.section.web_search.description",
		SectionComputerUse:   "settings.section.computer_use.description",
		SectionDisplay:       "settings.section.display.description",
		SectionTheme:         "settings.section.theme.description",
		SectionSecurity:      "settings.section.security.description",
		SectionAuth:          "settings.section.auth.description",
		SectionVault:         "settings.section.vault.description",
		SectionProxies:       "settings.section.proxies.description",
		SectionCache:         "settings.section.cache.description",
		SectionReliability:   "settings.section.reliability.description",
		SectionUpdates:       "settings.section.updates.description",
		SectionGeneral:       "settings.section.general.description",
		SectionConfigBundles: "settings.section.config_bundles.description",
		SectionAdvanced:      "settings.section.advanced.description",
	}
	if key, ok := keys[section.ID]; ok {
		return i18n.T(key)
	}
	return section.Description
}

func localizedGroupName(group string) string {
	keys := map[string]string{
		"AI Config":            "settings.group.ai_config",
		"Tools & Integrations": "settings.group.tools_integrations",
		"Appearance":           "settings.group.appearance",
		"Security & Auth":      "settings.group.security_auth",
		"Performance":          "settings.group.performance",
		"Advanced":             "settings.group.advanced",
	}
	if key, ok := keys[group]; ok {
		return i18n.T(key)
	}
	return group
}

// renderSidebar renders the left sidebar with grouped sections and search
func (m *Manager) renderSidebar(width, height int, th Theme) string {
	var lines []string
	isFocused := m.state.Focus == FocusSidebar

	sbBg := th.BGLight
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(sbBg))

	// row creates a single padded line: styled text padded to full width with bg.
	row := func(text string, bg string) string {
		return lipgloss.PlaceHorizontal(width, lipgloss.Left, text,
			lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(lipgloss.Color(bg))))
	}

	// Title
	titleFg := th.Primary
	if !isFocused {
		titleFg = th.TextMuted
	}
	lines = append(lines, row(
		lipgloss.NewStyle().Foreground(lipgloss.Color(titleFg)).Background(lipgloss.Color(sbBg)).Bold(true).Render(" "+i18n.T("settings.title_upper")),
		sbBg))

	// Search — only when active or filtering
	if m.state.SearchActive {
		lines = append(lines, row(
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Background(lipgloss.Color(sbBg)).Render(" / "+m.state.SearchQuery+"█"),
			sbBg))
	} else if m.state.SearchQuery != "" {
		lines = append(lines, row(
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Background(lipgloss.Color(sbBg)).Italic(true).Render(" "+i18n.T("settings.sidebar.filter", m.state.SearchQuery)),
			sbBg))
	}

	// Divider
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Background(lipgloss.Color(sbBg)).Render(strings.Repeat("─", width)))

	// Grouped sections
	grouped := m.groupedSections()
	filtered := m.filteredSections(grouped)

	for _, groupName := range SectionGroups {
		sections, exists := filtered[groupName]
		if !exists || len(sections) == 0 {
			continue
		}

		groupActive := false
		for _, sec := range sections {
			if sec.ID == m.state.SelectedSection {
				groupActive = true
				break
			}
		}

		groupFg := th.TextMuted
		if isFocused {
			if groupActive {
				groupFg = th.Primary
			} else {
				groupFg = th.Accent
			}
		} else if groupActive {
			groupFg = th.Accent
		}

		lines = append(lines, row(
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(groupFg)).Background(lipgloss.Color(sbBg)).Render("  "+localizedGroupName(groupName)),
			sbBg))

		for _, section := range sections {
			isSelected := section.ID == m.state.SelectedSection
			label := localizedSectionName(section)

			if isSelected {
				selBg := th.BGLighter
				if isFocused {
					accent := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Background(lipgloss.Color(selBg)).Render("│")
					text := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Background(lipgloss.Color(selBg)).Bold(true).Render(" " + label)
					lines = append(lines, row(accent+text, selBg))
				} else {
					lines = append(lines, row(
						lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Background(lipgloss.Color(selBg)).Render("  "+label),
						selBg))
				}
			} else {
				itemFg := th.TextDim
				if !isFocused {
					itemFg = th.TextMuted
				}
				indent := "    "
				if width < 30 {
					indent = "  "
				}
				lines = append(lines, row(
					lipgloss.NewStyle().Foreground(lipgloss.Color(itemFg)).Background(lipgloss.Color(sbBg)).Render(indent+label),
					sbBg))
			}
		}
	}

	// No matches
	if m.state.SearchQuery != "" && len(filtered) == 0 {
		lines = append(lines, row(
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Background(lipgloss.Color(sbBg)).Italic(true).Render(" "+i18n.T("settings.sidebar.no_matches")),
			sbBg))
	}

	// Pad remaining height with empty bg lines
	for len(lines) < height {
		lines = append(lines, bgStyle.Render(strings.Repeat(" ", width)))
	}

	content := strings.Join(lines, "\n")

	// Add right border manually — single column of border chars
	borderColor := th.Border
	if isFocused {
		borderColor = th.Primary
	}
	borderCh := lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor)).Background(lipgloss.Color(sbBg)).Render("│")
	contentLines := strings.Split(content, "\n")
	for i := range contentLines {
		contentLines[i] = contentLines[i] + borderCh
	}

	return strings.Join(contentLines, "\n")
}

// HandleMessage processes Bubble Tea messages (including PasteMsg)

// themePresets builds the ThemePresetInfo slice from hard-coded palette data.
// We embed the colour data directly here so the settings package doesn't need
// to import the chat package (which would create a circular dependency).
func (m *Manager) themePresets() []ThemePresetInfo {
	return []ThemePresetInfo{
		{Name: "SwarmCode", Description: i18n.T("settings.theme.swarmcode.description"), Primary: "#6E64E8", Accent: "#39D2C0", BG: "#0E1118", BGLight: "#151B24", Text: "#FFFFFF"},
		{Name: "Midnight", Description: i18n.T("settings.theme.midnight.description"), Primary: "#A78BFA", Accent: "#818CF8", BG: "#0D0D1A", BGLight: "#12122A", Text: "#E2E8F0"},
		{Name: "Nord", Description: i18n.T("settings.theme.nord.description"), Primary: "#88C0D0", Accent: "#8FBCBB", BG: "#2E3440", BGLight: "#3B4252", Text: "#ECEFF4"},
		{Name: "Gruvbox Dark", Description: i18n.T("settings.theme.gruvbox.description"), Primary: "#FABD2F", Accent: "#8EC07C", BG: "#1D2021", BGLight: "#282828", Text: "#EBDBB2"},
		{Name: "Dracula", Description: i18n.T("settings.theme.dracula.description"), Primary: "#BD93F9", Accent: "#50FA7B", BG: "#282A36", BGLight: "#323443", Text: "#F8F8F2"},
		{Name: "Monokai", Description: i18n.T("settings.theme.monokai.description"), Primary: "#F92672", Accent: "#A6E22E", BG: "#272822", BGLight: "#3E3D32", Text: "#F8F8F2"},
		{Name: "Catppuccin", Description: i18n.T("settings.theme.catppuccin.description"), Primary: "#CBA6F7", Accent: "#94E2D5", BG: "#1E1E2E", BGLight: "#27273E", Text: "#CDD6F4"},
		{Name: "Tokyo Night", Description: i18n.T("settings.theme.tokyo_night.description"), Primary: "#7AA2F7", Accent: "#7DCFFF", BG: "#1A1B26", BGLight: "#24283B", Text: "#C0CAF5"},
		{Name: "Solarized Dark", Description: i18n.T("settings.theme.solarized.description"), Primary: "#268BD2", Accent: "#859900", BG: "#002B36", BGLight: "#073642", Text: "#FDF6E3"},
		{Name: "Horizon", Description: i18n.T("settings.theme.horizon.description"), Primary: "#E95678", Accent: "#25B0BC", BG: "#1C1E26", BGLight: "#232530", Text: "#D5D8DA"},
	}
}
