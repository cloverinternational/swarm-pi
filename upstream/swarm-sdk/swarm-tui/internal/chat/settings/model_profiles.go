// model_profiles.go - Unified Model & Agent Profiles Settings Section
//
// ARCHITECTURAL OVERVIEW:
// ======================
// This component merges the previous SectionModel and SectionAgentProfiles into a single
// unified SectionModelProfiles. The architectural flaw of having a standalone "Browse Models"
// feature has been eliminated - models are now exclusively browsed and assigned through
// their functional roles within Profiles via the FallbackPicker.
//
// STATE MACHINE:
// ==============
// The unified state machine has 8 states:
//
//   ┌─────────┐
//   │  menu   │ ← Default entry point (5 options)
//   └────┬────┘
//        │ cursorIndex
//        ├─[0]─→ profile_list ─→ profile_edit_roles ─→ FallbackPicker
//        ├─[1]─→ manage_providers ─→ edit_provider ─→ provider_models
//        ├─[2]─→ add_provider
//        ├─[3]─→ swarm_agents
//        └─[4]─→ authentication

package settings

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// =============================================================================
// SECTION 1: STATE CONSTANTS (State Machine Primitives)
// =============================================================================

// ModelProfilesState defines the unified state machine constants.
// DEAD STATES REMOVED: browse_models, alias_variants, models, reasoning_effort
// These were pruned because standalone model browsing is architecturally flawed.
type ModelProfilesState string

const (
	// MPStateMenu is the default entry point with 5 hardcoded menu options
	MPStateMenu ModelProfilesState = "menu"

	// MPStateProfileList displays the list of agent profiles for configuration
	MPStateProfileList ModelProfilesState = "profile_list"

	// MPStateProfileEditRoles allows editing role-to-model mappings within a profile
	MPStateProfileEditRoles ModelProfilesState = "profile_edit_roles"

	// MPStateManageProviders displays the list of configured providers for editing
	MPStateManageProviders ModelProfilesState = "manage_providers"

	// MPStateAddProvider is the form for adding a new provider
	MPStateAddProvider ModelProfilesState = "add_provider"

	// MPStateEditProvider is the form for editing an existing provider
	MPStateEditProvider ModelProfilesState = "edit_provider"

	// MPStateProviderModels displays available models for a specific provider
	MPStateProviderModels ModelProfilesState = "provider_models"

	// MPStateSwarmAgents displays the Swarm agent model selection (Dream + Steering)
	MPStateSwarmAgents ModelProfilesState = "swarm_agents"

	// MPStateAuthentication displays OAuth authentication for providers
	MPStateAuthentication ModelProfilesState = "authentication"
)

// Menu index constants for the unified 5-option top-level menu
const (
	MPMenuConfigureProfiles int = 0 // Index 0: Configure Profiles
	MPMenuManageProviders   int = 1 // Index 1: Manage Providers
	MPMenuAddProvider       int = 2 // Index 2: Add Provider
	MPMenuSwarmAgents       int = 3 // Index 3: Swarm Agents (Dream + Steering model selection)
	MPMenuAuthentication    int = 4 // Index 4: Authentication (OAuth for providers)
)

// DefaultModelID is the model identifier used when no explicit model has been
// configured for a profile role.  Keeping it in a named constant (rather than
// scattered bare string literals) makes it easy to update in one place.
const DefaultModelID = "claude-fable-5"

// DefaultModelProfile returns a minimal AgentProfile pre-populated with the
// default model for every role alias.  Callers that need a sensible starting
// point (e.g. first-run setup) should use this instead of hard-coding the
// model string inline.
func DefaultModelProfile() AgentProfile {
	return AgentProfile{}
}

// =============================================================================
// SECTION 2: DATA STRUCTURES (Unified Struct Definition)
// =============================================================================

// SectionModelProfiles is the unified component combining model configuration
// and agent profile management. It encapsulates both the provider management
// flow (from old ModelSettings) and the profile configuration flow (from old
// SectionAgentProfiles).
type SectionModelProfiles struct {
	// --- State Machine ---
	state ModelProfilesState

	// --- Menu Navigation ---
	menuCursorIndex int // Current cursor position in the 5-item menu

	// --- Provider Management: delegated entirely to ModelSettings ---
	// When state is manage_providers / add_provider / edit_provider / provider_models
	// all rendering and key handling go through this reference.
	model *ModelSettings // set by manager via SetModelSettings()

	// --- Profile Management Fields ---
	profilesConfig  *profiles.ProfilesConfig // The full config including default
	profiles        []AgentProfile           // Uses SDK type alias
	selectedProfile int                      // Index in profiles list
	profileScroll   int                      // Scroll offset for profile list

	// Role Editing State - uses SDK ModelAlias type
	roles        []ModelAlias // Ordered list of role aliases for current profile
	selectedRole int          // Index in roles list
	roleScroll   int          // Scroll offset for roles list

	// --- FallbackPicker Integration ---
	pickerActive     bool            // Whether FallbackPicker is currently active
	fallbackPicker   *FallbackPicker // The picker instance for model selection
	pickerTargetRole ModelAlias      // Which role we're selecting a model for

	// --- Profile Manager ---
	profileManager *ProfileManager // For loading/saving profiles

	// --- Shared State (injected by manager so delegated renders have access) ---
	sharedState *State
	sharedTheme Theme

	// --- UI State ---
	width   int
	height  int
	focused bool

	// --- Error/Message Display ---
	errorMessage   string
	successMessage string

	// --- Activation Tracking ---
	activatedProfileID string // Set when a profile is activated; drained by TakeActivatedProfileID

	// --- Rename Mode State ---
	renameMode       bool   // Whether rename mode is active
	renameInput      string // Text being typed for new name
	renameCursorPos  int    // Cursor position in rename text
	renameProfileIdx int    // Index of profile being renamed

	// --- Bulk Model Assignment State ---
	roleChecked map[string]bool // Which roles are checked for bulk assignment

	// --- Swarm Agents State ---
	swarmAgentsCursor     int                                          // 0=Steering Provider, 1=Steering Model
	swarmSteeringProvider string                                       // Selected steering provider
	swarmSteeringModel    string                                       // Selected steering model
	swarmOnChange         func(steeringProvider, steeringModel string) // Callback

	// --- Authentication State ---
	authProviders []authProviderEntry // OAuth providers list (from auth.go)
	authCursor    int                 // Cursor position in auth provider list
	authViewState string              // "list" | "account_list" | "usage"
	authPickerIdx int                 // 0 = "Add account", 1..N = existing accounts

	// --- Authentication Reference ---
	authSettings *AuthSettings // Reference to global auth settings for OAuth flows
}

// SetModelSettings wires the existing ModelSettings instance so provider management
// screens are rendered and handled identically to the old SectionModel.
func (m *SectionModelProfiles) SetModelSettings(ms *ModelSettings) {
	m.model = ms
}

// NewSectionModelProfiles creates and initializes a new unified SectionModelProfiles instance.
// This is the factory function that sets up the initial state and loads existing configuration.
func NewSectionModelProfiles() *SectionModelProfiles {
	return &SectionModelProfiles{
		state:            MPStateMenu,
		menuCursorIndex:  0,
		profilesConfig:   nil,
		profiles:         []AgentProfile{},
		selectedProfile:  0,
		profileScroll:    0,
		roles:            []ModelAlias{},
		selectedRole:     0,
		roleScroll:       0,
		pickerActive:     false,
		fallbackPicker:   nil,
		pickerTargetRole: "",
		profileManager:   NewProfileManager(),
		width:            80,
		height:           24,
		focused:          false,
		errorMessage:     "",
		successMessage:   "",
	}
}

// =============================================================================
// SECTION 3: CONTROL FLOW & ROUTING (The Update/HandleKey Function)
// =============================================================================

// HandleKey processes keyboard input and routes to the appropriate state handler.
// This is the main entry point for the state machine's transition logic.
func (m *SectionModelProfiles) HandleKey(key string, sharedState *State) tea.Cmd {
	// PRIORITY 0: If rename mode is active, intercept all input
	if m.renameMode {
		return m.handleRenameInput(key, sharedState)
	}

	// PRIORITY 1: If FallbackPicker is active, it gets first crack at input
	if m.pickerActive && m.fallbackPicker != nil {
		return m.handlePickerInput(key, sharedState)
	}

	// PRIORITY 2: Route based on current state machine position
	switch m.state {
	case MPStateMenu:
		return m.handleMenuInput(key, sharedState)

	case MPStateProfileList:
		return m.handleProfileListInput(key, sharedState)

	case MPStateProfileEditRoles:
		return m.handleProfileEditRolesInput(key, sharedState)

	case MPStateManageProviders:
		return m.handleManageProvidersInput(key, sharedState)

	case MPStateAddProvider:
		return m.handleAddProviderInput(key, sharedState)

	case MPStateEditProvider:
		return m.handleEditProviderInput(key, sharedState)

	case MPStateProviderModels:
		return m.handleProviderModelsInput(key, sharedState)

	case MPStateSwarmAgents:
		return m.handleSwarmAgentsInput(key, sharedState)

	case MPStateAuthentication:
		return m.handleAuthenticationInput(key, sharedState)

	default:
		// Unknown state - reset to menu for safety
		m.state = MPStateMenu
		return nil
	}
}

// handleMenuInput processes input when in the top-level menu state.
// This is the critical routing function that implements the 3-option menu.
func (m *SectionModelProfiles) handleMenuInput(key string, sharedState *State) tea.Cmd {
	switch key {
	case "up", "k":
		// Navigate upward in the menu (wrap around)
		if m.menuCursorIndex > 0 {
			m.menuCursorIndex--
		} else {
			m.menuCursorIndex = MPMenuAuthentication // Wrap to last item
		}
		return nil

	case "down", "j":
		// Navigate downward in the menu (wrap around)
		if m.menuCursorIndex < MPMenuAuthentication {
			m.menuCursorIndex++
		} else {
			m.menuCursorIndex = MPMenuConfigureProfiles // Wrap to first item
		}
		return nil

	case "enter", " ": // Space or Enter selects the menu item
		return m.executeMenuSelection(sharedState)

	case "esc":
		// Esc on the top-level menu has no sub-state to go back to —
		// signal that focus should return to the sidebar by doing nothing here.
		// IsInNestedState() returns false at MPStateMenu so the manager will
		// close/blur settings as normal.
		return nil

	default:
		return nil
	}
}

// executeMenuSelection transitions state based on the current menu cursor index.
// This is the CORE ROUTING LOGIC that implements the user's requirements.
//
// LOGIC: Evaluate cursorIndex when Enter/Space is pressed
// and mutate the state pointer accordingly:
//   - cursorIndex == 0: Mutate the state pointer to profile_list
//   - cursorIndex == 1: Mutate the state pointer to manage_providers
//   - cursorIndex == 2: Mutate the state pointer to add_provider
func (m *SectionModelProfiles) executeMenuSelection(sharedState *State) tea.Cmd {
	switch m.menuCursorIndex {

	case MPMenuConfigureProfiles:
		m.state = MPStateProfileList
		if len(m.profiles) == 0 {
			return m.loadProfiles(sharedState)
		}
		return nil

	case MPMenuManageProviders:
		// Delegate entirely to ModelSettings — set its state and flip our state flag
		m.state = MPStateManageProviders
		if m.model != nil {
			m.model.state = "manage_providers"
			m.model.selectedProvider = 0
			m.model.scrollOffset = 0
		}
		return nil

	case MPMenuAddProvider:
		m.state = MPStateAddProvider
		if m.model != nil {
			m.model.state = "add_provider"
			m.model.formField = 0
			m.model.resetForm()
		}
		return nil

	case MPMenuSwarmAgents:
		m.state = MPStateSwarmAgents
		m.swarmAgentsCursor = 0
		m.LoadSwarmAgentsConfig()
		return nil

	case MPMenuAuthentication:
		m.state = MPStateAuthentication
		m.authCursor = 0
		m.loadAuthProviders()
		if m.authSettings != nil {
			m.authSettings.Refresh()
		}
		return nil

	default:
		m.state = MPStateMenu
		return nil
	}
}

// =============================================================================
// SECTION 4: PROFILE MANAGEMENT HANDLERS
// =============================================================================

// handleProfileListInput processes input when viewing the profile list.
func (m *SectionModelProfiles) handleProfileListInput(key string, sharedState *State) tea.Cmd {
	switch key {
	case "up", "k":
		if m.selectedProfile > 0 {
			m.selectedProfile--
		}
		if m.selectedProfile < m.profileScroll {
			m.profileScroll = m.selectedProfile
		}
		m.errorMessage = ""
		m.successMessage = ""
		return nil

	case "down", "j":
		if m.selectedProfile < len(m.profiles)-1 {
			m.selectedProfile++
		}
		visibleHeight := m.height - 10
		if visibleHeight < 1 {
			visibleHeight = 5
		}
		if m.selectedProfile >= m.profileScroll+visibleHeight {
			m.profileScroll = m.selectedProfile - visibleHeight + 1
		}
		m.errorMessage = ""
		m.successMessage = ""
		return nil

	case "enter", " ":
		// Enter opens role editing for the selected profile
		if len(m.profiles) > 0 && m.selectedProfile < len(m.profiles) {
			m.state = MPStateProfileEditRoles
			m.loadRolesForProfile(m.selectedProfile)
		}
		return nil

	case "a":
		// Activate: make selected profile the default
		if len(m.profiles) > 0 && m.selectedProfile < len(m.profiles) {
			p := m.profiles[m.selectedProfile]
			if err := m.profileManager.Manager.SetDefaultProfile(p.ID); err != nil {
				m.errorMessage = i18n.T("settings.residual_final.profiles.activate_failed", err)
			} else {
				m.profiles = m.profileManager.Manager.ListProfiles()
				m.successMessage = i18n.T("settings.residual_final.profiles.activated", p.Name)
				m.activatedProfileID = p.ID
			}
		}
		return nil

	case "d":
		// Duplicate: clone the selected profile
		if len(m.profiles) > 0 && m.selectedProfile < len(m.profiles) {
			src := m.profiles[m.selectedProfile]
			newName := src.Name + i18n.T("settings.residual_final.profiles.copy_suffix")
			cloned, err := m.profileManager.Manager.CloneProfile(src.ID, newName)
			if err != nil {
				m.errorMessage = i18n.T("settings.residual_final.profiles.duplicate_failed", err)
			} else {
				m.profiles = m.profileManager.Manager.ListProfiles()
				// Select the newly created clone
				for i, p := range m.profiles {
					if p.ID == cloned.ID {
						m.selectedProfile = i
						break
					}
				}
				m.successMessage = i18n.T("settings.residual_final.profiles.created", cloned.Name)
			}
		}
		return nil

	case "n":
		// New: create a blank profile with a default name
		newProfile, err := m.profileManager.Manager.CreateProfile(i18n.T("settings.residual_final.profiles.new_name"), "")
		if err != nil {
			m.errorMessage = i18n.T("settings.residual_final.profiles.create_failed", err)
		} else {
			m.profiles = m.profileManager.Manager.ListProfiles()
			// Select the new profile so user sees it immediately
			for i, p := range m.profiles {
				if p.ID == newProfile.ID {
					m.selectedProfile = i
					break
				}
			}
			m.successMessage = i18n.T("settings.residual_final.profiles.created_edit", newProfile.Name)
		}
		return nil

	case "x", "delete":
		// Delete: remove selected profile (SDK prevents deleting last/default)
		if len(m.profiles) > 0 && m.selectedProfile < len(m.profiles) {
			p := m.profiles[m.selectedProfile]
			if err := m.profileManager.Manager.DeleteProfile(p.ID); err != nil {
				m.errorMessage = i18n.T("settings.residual_final.profiles.delete_failed", err)
			} else {
				m.profiles = m.profileManager.Manager.ListProfiles()
				if m.selectedProfile >= len(m.profiles) {
					m.selectedProfile = len(m.profiles) - 1
				}
				if m.selectedProfile < 0 {
					m.selectedProfile = 0
				}
				m.successMessage = i18n.T("settings.residual_final.profiles.deleted", p.Name)
			}
		}
		return nil

	case "r":
		// Rename: enter rename mode for the selected profile
		if len(m.profiles) > 0 && m.selectedProfile < len(m.profiles) {
			p := m.profiles[m.selectedProfile]
			m.renameMode = true
			m.renameInput = p.Name
			m.renameCursorPos = len(p.Name)
			m.renameProfileIdx = m.selectedProfile
			m.errorMessage = ""
			m.successMessage = ""
		}
		return nil

	case "esc":
		m.state = MPStateMenu
		m.errorMessage = ""
		m.successMessage = ""
		return nil

	default:
		return nil
	}
}

// handleRenameInput processes input when in rename mode.
// This handles character input, backspace, Enter (save), and Escape (cancel).
func (m *SectionModelProfiles) handleRenameInput(key string, sharedState *State) tea.Cmd {
	switch key {
	case "enter":
		// Save the rename
		if len(m.profiles) > 0 && m.renameProfileIdx < len(m.profiles) {
			p := m.profiles[m.renameProfileIdx]
			newName := m.renameInput
			if newName == "" {
				m.errorMessage = i18n.T("settings.residual_final.profiles.name_empty")
				return nil
			}
			if newName == p.Name {
				// No change, just exit rename mode
				m.renameMode = false
				return nil
			}
			if err := m.profileManager.Manager.RenameProfile(p.ID, newName); err != nil {
				m.errorMessage = i18n.T("settings.residual_final.profiles.rename_failed", err)
			} else {
				m.profiles = m.profileManager.Manager.ListProfiles()
				m.successMessage = i18n.T("settings.residual_final.profiles.renamed", newName)
			}
		}
		m.renameMode = false
		return nil

	case "esc":
		// Cancel rename
		m.renameMode = false
		m.errorMessage = ""
		return nil

	case "backspace":
		// Delete character before cursor
		if m.renameCursorPos > 0 {
			m.renameInput = m.renameInput[:m.renameCursorPos-1] + m.renameInput[m.renameCursorPos:]
			m.renameCursorPos--
		}
		return nil

	case "delete":
		// Delete character at cursor
		if m.renameCursorPos < len(m.renameInput) {
			m.renameInput = m.renameInput[:m.renameCursorPos] + m.renameInput[m.renameCursorPos+1:]
		}
		return nil

	case "left", "ctrl+b":
		// Move cursor left
		if m.renameCursorPos > 0 {
			m.renameCursorPos--
		}
		return nil

	case "right", "ctrl+f":
		// Move cursor right
		if m.renameCursorPos < len(m.renameInput) {
			m.renameCursorPos++
		}
		return nil

	case "home", "ctrl+a":
		// Move cursor to start
		m.renameCursorPos = 0
		return nil

	case "end", "ctrl+e":
		// Move cursor to end
		m.renameCursorPos = len(m.renameInput)
		return nil

	default:
		// Check if it's a printable character (single rune or printable string)
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			// Insert character at cursor position
			m.renameInput = m.renameInput[:m.renameCursorPos] + key + m.renameInput[m.renameCursorPos:]
			m.renameCursorPos++
		} else if len(key) > 1 {
			// Try to insert if it's a printable string
			for _, r := range key {
				if r >= 32 && r < 127 {
					char := string(r)
					m.renameInput = m.renameInput[:m.renameCursorPos] + char + m.renameInput[m.renameCursorPos:]
					m.renameCursorPos++
				}
			}
		}
		return nil
	}
}

// handleProfileEditRolesInput processes input when editing role-to-model mappings.
func (m *SectionModelProfiles) handleProfileEditRolesInput(key string, sharedState *State) tea.Cmd {
	switch key {
	case "up", "k":
		if m.selectedRole > 0 {
			m.selectedRole--
		}
		if m.selectedRole < m.roleScroll {
			m.roleScroll = m.selectedRole
		}
		return nil

	case "down", "j":
		if m.selectedRole < len(m.roles)-1 {
			m.selectedRole++
		}
		visibleHeight := m.height - 10
		if m.selectedRole >= m.roleScroll+visibleHeight {
			m.roleScroll = m.selectedRole - visibleHeight + 1
		}
		return nil

	case "enter":
		// Open FallbackPicker for model selection on this role
		return m.openFallbackPickerForRole(sharedState)

	case " ":
		// Toggle checkbox for the selected role
		if m.selectedRole < len(m.roles) {
			roleAlias := string(m.roles[m.selectedRole])
			m.roleChecked[roleAlias] = !m.roleChecked[roleAlias]
		}
		return nil

	case "a":
		// Toggle all checkboxes
		allChecked := true
		for _, alias := range m.roles {
			if !m.roleChecked[string(alias)] {
				allChecked = false
				break
			}
		}
		// If all are checked, uncheck all; otherwise check all
		newValue := !allChecked
		for _, alias := range m.roles {
			m.roleChecked[string(alias)] = newValue
		}
		return nil

	case "esc":
		m.state = MPStateProfileList
		return nil

	default:
		return nil
	}
}

// openFallbackPickerForRole initializes and activates the FallbackPicker.
func (m *SectionModelProfiles) openFallbackPickerForRole(sharedState *State) tea.Cmd {
	// Check if providers exist via the ModelSettings instance (which owns provider state)
	hasProviders := m.model != nil && len(m.model.providers) > 0
	if !hasProviders {
		m.errorMessage = i18n.T("settings.residual_final.profiles.no_providers")
		return nil
	}

	// Get the current role
	if m.selectedRole >= len(m.roles) {
		return nil
	}

	roleAlias := m.roles[m.selectedRole]
	m.pickerTargetRole = roleAlias

	// Get the current profile and its chain for this role
	var chain *fallback.Chain
	if m.selectedProfile < len(m.profiles) {
		profile := m.profiles[m.selectedProfile]
		if rc, ok := profile.GetRole(roleAlias); ok && rc.Chain != nil {
			chain = rc.Chain
		}
	}

	// Create and initialize the FallbackPicker
	m.fallbackPicker = NewFallbackPicker(chain)

	// Set up the save callback — called by FallbackPicker when user confirms selection
	m.fallbackPicker.SetOnSave(func(savedChain *fallback.Chain) {
		if m.selectedProfile >= len(m.profiles) {
			return
		}
		profile := m.profiles[m.selectedProfile]

		// Build a RoleConfig from the saved chain
		rc := NewRoleConfigFromChain(savedChain)

		// Apply to all checked roles (bulk assignment)
		appliedCount := 0
		var lastAppliedAlias ModelAlias
		for _, alias := range m.roles {
			if m.roleChecked[string(alias)] {
				// Persist via SDK manager (handles validation + atomic write)
				if err := m.profileManager.Manager.SetRoleInProfile(profile.ID, alias, rc); err != nil {
					m.errorMessage = i18n.T("settings.residual_final.profiles.save_role_failed", string(alias), err)
					return
				}
				appliedCount++
				lastAppliedAlias = alias
			}
		}

		// Refresh in-memory list
		m.profiles = m.profileManager.Manager.ListProfiles()
		if appliedCount == 1 {
			m.successMessage = i18n.T("settings.residual_final.profiles.saved_role", string(lastAppliedAlias), savedChain.Primary.Model)
		} else {
			m.successMessage = i18n.T("settings.residual_final.profiles.applied_roles", appliedCount, savedChain.Primary.Model)
		}
	})

	m.pickerActive = true
	return nil
}

// NewRoleConfigFromChain creates a RoleConfig from a fallback chain
func NewRoleConfigFromChain(chain *fallback.Chain) RoleConfig {
	if chain == nil || chain.Len() == 0 {
		return RoleConfig{}
	}

	primary := chain.Primary
	rc := RoleConfig{
		Chain: chain,
	}

	// Set provider/model from primary if available
	if primary.Provider != "" {
	}

	return rc
}

// handlePickerInput delegates input to the FallbackPicker and handles its completion.
func (m *SectionModelProfiles) handlePickerInput(key string, sharedState *State) tea.Cmd {
	if m.fallbackPicker == nil {
		m.pickerActive = false
		return nil
	}

	// Delegate input to the picker
	handled := m.fallbackPicker.HandleKey(key)

	// Check if picker was closed (Escape pressed)
	if !handled && key == "esc" {
		m.pickerActive = false
		m.fallbackPicker = nil
		m.pickerTargetRole = ""
		return nil
	}

	_ = handled
	return nil
}

// =============================================================================
// SECTION 5: PROVIDER MANAGEMENT HANDLERS (delegated to ModelSettings)
// =============================================================================

// handleManageProvidersInput delegates entirely to the existing ModelSettings instance.
func (m *SectionModelProfiles) handleManageProvidersInput(key string, sharedState *State) tea.Cmd {
	if m.model == nil {
		m.state = MPStateMenu
		return nil
	}
	m.model.HandleKey(key)
	m.syncFromModel()
	return nil
}

// handleAddProviderInput delegates to ModelSettings.
func (m *SectionModelProfiles) handleAddProviderInput(key string, sharedState *State) tea.Cmd {
	if m.model == nil {
		m.state = MPStateMenu
		return nil
	}
	m.model.handleAddProviderKey(key)
	m.syncFromModel()
	return nil
}

// handleEditProviderInput delegates to ModelSettings.
func (m *SectionModelProfiles) handleEditProviderInput(key string, sharedState *State) tea.Cmd {
	if m.model == nil {
		m.state = MPStateMenu
		return nil
	}
	m.model.handleEditProviderKey(key)
	cmd := m.model.TakePendingCmd()
	m.syncFromModel()
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return cmd() }
}

// handleProviderModelsInput delegates to ModelSettings.
func (m *SectionModelProfiles) handleProviderModelsInput(key string, sharedState *State) tea.Cmd {
	if m.model == nil {
		m.state = MPStateMenu
		return nil
	}
	m.model.HandleKey(key) // Let model route to correct handler based on its state
	m.syncFromModel()
	return nil
}

// syncFromModel maps ModelSettings.state back to our MPState so the state machine stays in sync.
func (m *SectionModelProfiles) syncFromModel() {
	if m.model == nil {
		return
	}
	switch m.model.state {
	case "menu":
		m.state = MPStateMenu
	case "manage_providers":
		m.state = MPStateManageProviders
	case "add_provider":
		m.state = MPStateAddProvider
	case "edit_provider":
		m.state = MPStateEditProvider
	case "add_model":
		m.state = MPStateProviderModels
	case "edit_model":
		m.state = MPStateProviderModels
	case "provider_models":
		m.state = MPStateProviderModels
	case "confirm_delete":
		// stay — model handles the confirm overlay internally
	default:
		// browse_models or any unrelated model state → back to our menu
		m.state = MPStateMenu
	}
}

// =============================================================================
// SECTION 6: HELPER FUNCTIONS
// =============================================================================

// focusFormField sets focus on the current form field based on formField index
// loadProfiles loads profiles from the SDK manager (not a manual path).
func (m *SectionModelProfiles) loadProfiles(sharedState *State) tea.Cmd {
	if m.profileManager == nil {
		m.profileManager = NewProfileManager()
	}

	// Reload from disk to pick up any changes
	if err := m.profileManager.Manager.Reload(); err != nil {
		m.errorMessage = i18n.T("settings.residual_final.profiles.load_failed", err)
	}

	m.profiles = m.profileManager.Manager.ListProfiles()
	m.profilesConfig = m.profileManager.Manager.GetConfig()

	if len(m.profiles) == 0 {
		m.errorMessage = i18n.T("settings.residual_final.profiles.none_found")
	}

	return nil
}

// loadRolesForProfile extracts roles from the selected profile
func (m *SectionModelProfiles) loadRolesForProfile(profileIndex int) {
	if profileIndex >= len(m.profiles) {
		return
	}

	// Get all aliases in canonical order
	m.roles = AllAliases()
	m.selectedRole = 0
	m.roleScroll = 0

	// Initialize all roles as checked for bulk assignment
	m.roleChecked = make(map[string]bool)
	for _, alias := range m.roles {
		m.roleChecked[string(alias)] = true
	}
}

// =============================================================================
// SECTION 7: RENDERING (The View Function)
// =============================================================================

// Render renders the current state of the SectionModelProfiles component.
// sharedState and th are injected by the manager so delegated provider-screen
// renders have access to the live focus state and active colour palette.
func (m *SectionModelProfiles) Render(width, height int, sharedState *State, th Theme) string {
	m.width = width
	m.height = height
	// Persist so helper renderers can reference them without threading the
	// parameters all the way down the call chain.
	if sharedState != nil {
		m.sharedState = sharedState
	}
	m.sharedTheme = th

	// If FallbackPicker is active, render it instead
	if m.pickerActive && m.fallbackPicker != nil {
		return i18n.SettingsResidualModelsText(m.fallbackPicker.Render(width, height, th))
	}

	// STATE-BASED RENDERING: Switch on current state
	var rendered string
	switch m.state {
	case MPStateMenu:
		rendered = m.renderMenu(width, height)

	case MPStateProfileList:
		rendered = m.renderProfileList(width, height)

	case MPStateProfileEditRoles:
		rendered = m.renderProfileEditRoles(width, height)

	case MPStateManageProviders:
		rendered = m.renderManageProviders(width, height)

	case MPStateAddProvider:
		rendered = m.renderAddProvider(width, height)

	case MPStateEditProvider:
		rendered = m.renderEditProvider(width, height)

	case MPStateProviderModels:
		rendered = m.renderProviderModels(width, height)

	case MPStateSwarmAgents:
		rendered = m.renderSwarmAgents(width, height)

	case MPStateAuthentication:
		rendered = m.renderAuthentication(width, height)

	default:
		rendered = m.renderMenu(width, height)
	}
	return i18n.SettingsResidualModelsText(rendered)
}

// renderMenu renders the 3-item top-level menu
func (m *SectionModelProfiles) renderMenu(width, height int) string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		MarginBottom(1)

	b.WriteString(titleStyle.Render(i18n.T("settings.model_profiles.title")))
	b.WriteString("\n\n")

	menuItems := []string{
		i18n.T("settings.model_profiles.menu.profiles"),
		i18n.T("settings.model_profiles.menu.providers"),
		i18n.T("settings.model_profiles.menu.add_provider"),
		i18n.T("settings.model_profiles.menu.swarm_agents"),
		i18n.T("settings.model_profiles.menu.auth"),
	}

	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)

	for i, item := range menuItems {
		cursor := "  "
		if i == m.menuCursorIndex {
			cursor = cursorStyle.Render("▶ ")
			b.WriteString(cursor + cursorStyle.Render(item))
		} else {
			b.WriteString(cursor + normalStyle.Render(item))
		}

		switch i {
		case 0:
			b.WriteString("\n    " + descStyle.Render(i18n.T("settings.model_profiles.menu.profiles.description")))
		case 1:
			b.WriteString("\n    " + descStyle.Render(i18n.T("settings.model_profiles.menu.providers.description")))
		case 2:
			b.WriteString("\n    " + descStyle.Render(i18n.T("settings.model_profiles.menu.add_provider.description")))
		case 3:
			b.WriteString("\n    " + descStyle.Render(i18n.T("settings.model_profiles.menu.swarm_agents.description")))
		case 4:
			b.WriteString("\n    " + descStyle.Render(i18n.T("settings.model_profiles.menu.auth.description")))
		}
		b.WriteString("\n\n")
	}

	if m.successMessage != "" {
		successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
		b.WriteString(successStyle.Render("✓ " + m.successMessage + "\n"))
	}
	if m.errorMessage != "" {
		errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
		b.WriteString(errorStyle.Render("✗ " + m.errorMessage + "\n"))
	}

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	b.WriteString(helpStyle.Render(i18n.T("settings.model_profiles.hints.menu")))

	return b.String()
}

// renderProfileList renders the list of agent profiles with actions
func (m *SectionModelProfiles) renderProfileList(width, height int) string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))

	b.WriteString(titleStyle.Render(i18n.T("settings.model_profiles.profiles.title")))
	b.WriteString("\n\n")

	if len(m.profiles) == 0 {
		b.WriteString(dimStyle.Render(i18n.T("settings.model_profiles.profiles.empty")) + "\n")
	} else {
		// Reserve lines for: title(1) + blank(1) + status(2) + help(1) + blank(1) = 6
		const reservedLines = 6
		visibleHeight := height - reservedLines
		if visibleHeight < 1 {
			visibleHeight = 1
		}

		// Clamp scroll offset so the selected item is always visible
		startIdx := m.profileScroll
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx := startIdx + visibleHeight
		if endIdx > len(m.profiles) {
			endIdx = len(m.profiles)
		}

		for i := startIdx; i < endIdx; i++ {
			profile := m.profiles[i]
			isSelected := i == m.selectedProfile
			isActive := profile.IsDefault

			// Cursor
			if isSelected {
				b.WriteString(cursorStyle.Render("▶ "))
			} else {
				b.WriteString("  ")
			}

			// Profile name + active badge (or rename input if renaming this profile)
			if m.renameMode && i == m.renameProfileIdx {
				// Show inline text input for rename
				inputText := m.renameInput
				beforeCursor := inputText[:m.renameCursorPos]
				afterCursor := inputText[m.renameCursorPos:]
				// Render with cursor indicator
				b.WriteString(normalStyle.Render(beforeCursor))
				b.WriteString(cursorStyle.Render("│"))
				b.WriteString(normalStyle.Render(afterCursor))
			} else {
				name := profile.Name
				if isActive {
					b.WriteString(activeStyle.Render(name))
					b.WriteString(" " + activeStyle.Render(i18n.T("settings.model_profiles.profiles.active")))
				} else {
					b.WriteString(normalStyle.Render(name))
				}
			}

			// Role count
			roleCount := len(profile.Roles)
			if roleCount > 0 {
				b.WriteString(" " + dimStyle.Render(i18n.T("settings.model_profiles.profiles.roles", roleCount)))
			} else {
				b.WriteString(" " + dimStyle.Render(i18n.T("settings.model_profiles.profiles.no_roles")))
			}
			b.WriteString("\n")
		}

		// Scroll indicator when list is truncated
		if len(m.profiles) > visibleHeight {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  (%d/%d)", m.selectedProfile+1, len(m.profiles))) + "\n")
		}
	}

	b.WriteString("\n")

	// Status messages
	if m.errorMessage != "" {
		b.WriteString(errorStyle.Render("✗ "+m.errorMessage) + "\n\n")
	} else if m.successMessage != "" {
		b.WriteString(successStyle.Render("✓ "+m.successMessage) + "\n\n")
	}

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	b.WriteString(helpStyle.Render(i18n.T("settings.model_profiles.hints.profiles")))

	return b.String()
}

// renderProfileEditRoles renders the role editing screen
func (m *SectionModelProfiles) renderProfileEditRoles(width, height int) string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	roleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	modelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	fallbackStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	profileName := ""
	if m.selectedProfile < len(m.profiles) {
		profileName = m.profiles[m.selectedProfile].Name
	}

	b.WriteString(titleStyle.Render(i18n.T("settings.model_profiles.roles.title", profileName)))
	b.WriteString("\n\n")

	if len(m.roles) == 0 {
		b.WriteString(i18n.T("settings.model_profiles.roles.empty") + "\n")
	} else {
		for i, roleAlias := range m.roles {
			cursor := "  "
			if i == m.selectedRole {
				cursor = cursorStyle.Render("▶ ")
			}

			// Checkbox indicator
			checkbox := "☐ "
			if m.roleChecked[string(roleAlias)] {
				checkbox = "☑ "
			}

			displayName := AliasDisplayName(roleAlias)
			description := AliasDescription(roleAlias)

			b.WriteString(cursor + checkbox + roleStyle.Render(displayName))

			if m.selectedProfile < len(m.profiles) {
				profile := m.profiles[m.selectedProfile]
				if rc, ok := profile.GetRole(roleAlias); ok && rc.Chain != nil {
					primary := rc.Chain.Primary
					if primary.Provider != "" {
						b.WriteString(": " + modelStyle.Render(primary.Model))
					}

					if rc.Chain.Len() > 1 {
						b.WriteString(" " + fallbackStyle.Render(i18n.T("settings.model_profiles.roles.fallbacks", rc.Chain.Len()-1)))
					}
				} else {
					b.WriteString(": " + fallbackStyle.Render(i18n.T("settings.model_profiles.roles.not_configured")))
				}
			}
			b.WriteString("\n")

			b.WriteString("    " + fallbackStyle.Render(description) + "\n\n")
		}
	}

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	b.WriteString(helpStyle.Render(i18n.T("settings.model_profiles.hints.roles")))

	return b.String()
}

// renderManageProviders renders the provider list for management
// renderManageProviders delegates to ModelSettings for pixel-perfect parity
func (m *SectionModelProfiles) renderManageProviders(width, height int) string {
	if m.model != nil {
		return m.model.Render(width, height, m.sharedState, m.sharedTheme)
	}
	return i18n.T("settings.residual_final.profiles.provider_management_unavailable")
}

// renderAddProvider delegates to ModelSettings
func (m *SectionModelProfiles) renderAddProvider(width, height int) string {
	if m.model != nil {
		return m.model.Render(width, height, m.sharedState, m.sharedTheme)
	}
	return i18n.T("settings.residual_final.profiles.add_provider_unavailable")
}

// renderEditProvider delegates to ModelSettings
func (m *SectionModelProfiles) renderEditProvider(width, height int) string {
	if m.model != nil {
		return m.model.Render(width, height, m.sharedState, m.sharedTheme)
	}
	return i18n.T("settings.residual_final.profiles.edit_provider_unavailable")
}

// renderProviderModels delegates to ModelSettings
func (m *SectionModelProfiles) renderProviderModels(width, height int) string {
	if m.model != nil {
		return m.model.Render(width, height, m.sharedState, m.sharedTheme)
	}
	return i18n.T("settings.residual_final.profiles.provider_models_unavailable")
}

// =============================================================================
// SECTION 8: BUBBLE TEA INTERFACE IMPLEMENTATION
// =============================================================================

// Init initializes the component (Bubble Tea interface)
func (m *SectionModelProfiles) Init() tea.Cmd {
	return nil
}

// Update handles Bubble Tea messages
func (m *SectionModelProfiles) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case plexusSyncResultMsg:
		if m.model != nil {
			m.model.applyPlexusSyncResult(msg)
			m.syncFromModel()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m, m.HandleKey(msg.String(), nil)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	return m, nil
}

// View returns the rendered Bubble Tea view.
func (m *SectionModelProfiles) View() tea.View {
	return tea.NewView(m.Render(m.width, m.height, m.sharedState, m.sharedTheme))
}

// SetFocused sets the focus state of the component
func (m *SectionModelProfiles) SetFocused(focused bool) {
	m.focused = focused
}

// GetState returns the current state (for external state queries)
func (m *SectionModelProfiles) GetState() ModelProfilesState {
	return m.state
}

// TakeActivatedProfileID returns the last activated profile ID and clears it.
// Called by the manager after each key event to fire the onProfileChange callback.
func (m *SectionModelProfiles) TakeActivatedProfileID() string {
	id := m.activatedProfileID
	m.activatedProfileID = ""
	return id
}

// IsInNestedState returns true when the section is in any sub-screen (not the top menu).
// Used by the manager to decide whether Esc should close settings or just go back.
// =============================================================================
// SECTION: SWARM AGENTS (Dream + Steering model selection)
// =============================================================================

// SetSwarmAgentsCallback sets the callback fired when swarm agent models change.
func (m *SectionModelProfiles) SetSwarmAgentsCallback(fn func(steeringProvider, steeringModel string)) {
	m.swarmOnChange = fn
}

// SetSwarmAgentsConfig sets the initial swarm agent model configuration.
func (m *SectionModelProfiles) SetSwarmAgentsConfig(steeringProvider, steeringModel string) {
	m.swarmSteeringProvider = steeringProvider
	m.swarmSteeringModel = steeringModel
}

// GetSwarmAgentsConfig returns the current swarm agent model configuration.
func (m *SectionModelProfiles) GetSwarmAgentsConfig() (steeringProvider, steeringModel string) {
	return m.swarmSteeringProvider, m.swarmSteeringModel
}

// LoadSwarmAgentsConfig loads swarm agent settings from the persisted config.
func (m *SectionModelProfiles) LoadSwarmAgentsConfig() {
	if m.model == nil || m.model.configManager == nil {
		return
	}
	cfg, err := m.model.configManager.LoadConfig()
	if err != nil || cfg == nil || cfg.SwarmAgents == nil {
		return
	}
	m.swarmSteeringProvider = cfg.SwarmAgents.SteeringProvider
	m.swarmSteeringModel = cfg.SwarmAgents.SteeringModel
}

// saveSwarmAgentsConfig persists the swarm agent settings.
func (m *SectionModelProfiles) saveSwarmAgentsConfig() {
	if m.model == nil || m.model.configManager == nil {
		return
	}
	cfg, err := m.model.configManager.LoadConfig()
	if err != nil {
		return
	}
	if cfg == nil {
		return
	}
	cfg.SwarmAgents = &commands.SwarmAgentsConfig{
		SteeringProvider: m.swarmSteeringProvider,
		SteeringModel:    m.swarmSteeringModel,
	}
	_ = m.model.configManager.SaveConfig(cfg)

	if m.swarmOnChange != nil {
		m.swarmOnChange(m.swarmSteeringProvider, m.swarmSteeringModel)
	}
}

// availableProviderNames returns the list of configured provider names.
func (m *SectionModelProfiles) availableProviderNames() []string {
	if m.model == nil {
		return []string{"anthropic", "openai"}
	}
	names := m.model.GetProviderNames()
	if len(names) == 0 {
		return []string{"anthropic", "openai"}
	}
	return names
}

// handleSwarmAgentsInput processes keys in the swarm agents screen.
func (m *SectionModelProfiles) handleSwarmAgentsInput(key string, sharedState *State) tea.Cmd {
	providers := m.availableProviderNames()
	const totalItems = 2 // Steering Provider, Steering Model

	switch key {
	case "esc":
		m.state = MPStateMenu
		return nil

	case "up", "k":
		if m.swarmAgentsCursor > 0 {
			m.swarmAgentsCursor--
		}
		return nil

	case "down", "j":
		if m.swarmAgentsCursor < totalItems-1 {
			m.swarmAgentsCursor++
		}
		return nil

	case "left", "h":
		switch m.swarmAgentsCursor {
		case 0: // Steering Provider
			m.swarmSteeringProvider = cyclePrev(m.swarmSteeringProvider, providers)
			m.swarmSteeringModel = ""
			m.saveSwarmAgentsConfig()
		case 1: // Steering Model
			models := m.modelsForProvider(m.swarmSteeringProvider)
			m.swarmSteeringModel = cyclePrev(m.swarmSteeringModel, models)
			m.saveSwarmAgentsConfig()
		}
		return nil

	case "right", "l":
		switch m.swarmAgentsCursor {
		case 0:
			m.swarmSteeringProvider = cycleNext(m.swarmSteeringProvider, providers)
			m.swarmSteeringModel = ""
			m.saveSwarmAgentsConfig()
		case 1:
			models := m.modelsForProvider(m.swarmSteeringProvider)
			m.swarmSteeringModel = cycleNext(m.swarmSteeringModel, models)
			m.saveSwarmAgentsConfig()
		}
		return nil
	}
	return nil
}

// modelsForProvider returns known models for a provider name.
func (m *SectionModelProfiles) modelsForProvider(providerName string) []string {
	if m.model != nil {
		models := m.model.GetModelsForProvider(providerName)
		if len(models) > 0 {
			return models
		}
	}
	// Sensible defaults
	switch providerName {
	case "anthropic":
		return []string{"claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"}
	case "openai":
		return []string{"gpt-4o", "gpt-4o-mini", "o3-mini"}
	default:
		return []string{}
	}
}

// renderSwarmAgents renders the Swarm Agents configuration screen.
func (m *SectionModelProfiles) renderSwarmAgents(width, height int) string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).MarginTop(1)
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)

	b.WriteString(titleStyle.Render(i18n.T("settings.residual_final.profiles.swarm_agents.title")))
	b.WriteString("\n")
	b.WriteString(descStyle.Render(i18n.T("settings.residual_final.profiles.swarm_agents.description")))
	b.WriteString("\n")

	type row struct {
		label string
		value string
	}

	steerProv := m.swarmSteeringProvider
	if steerProv == "" {
		steerProv = i18n.T("settings.residual_final.common.not_set_parenthesized")
	}
	steerMod := m.swarmSteeringModel
	if steerMod == "" {
		steerMod = i18n.T("settings.residual_final.common.not_set_parenthesized")
	}

	// Steering Agent section
	b.WriteString(headerStyle.Render(i18n.T("settings.residual_final.profiles.swarm_agents.steering")))
	b.WriteString("\n")
	b.WriteString(descStyle.Render(i18n.T("settings.residual_final.profiles.swarm_agents.steering_description")))
	b.WriteString("\n\n")

	rows2 := []row{
		{i18n.T("settings.reliability.field.provider"), steerProv},
		{i18n.T("settings.reliability.field.model"), steerMod},
	}
	for i, r := range rows2 {
		cursor := "  "
		style := normalStyle
		if m.swarmAgentsCursor == i {
			cursor = cursorStyle.Render("▶ ")
			style = cursorStyle
		}
		b.WriteString(fmt.Sprintf("%s%-12s %s", cursor, style.Render(r.label), valueStyle.Render("< "+r.value+" >")))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	b.WriteString(helpStyle.Render(i18n.T("settings.residual_final.profiles.swarm_agents.hint")))

	return b.String()
}

// handleAuthenticationInput is a no-op shim — when SectionModels is showing
// the authentication state, the manager intercepts and routes keys directly
// to AuthSettings (v2 tea.Cmd) so OAuth flow commands can propagate. The
// only case we need to cover here is esc/backspace at the top-level list,
// which returns the user to the Models menu.
func (m *SectionModelProfiles) handleAuthenticationInput(key string, sharedState *State) tea.Cmd {
	if (key == "esc" || key == "backspace") &&
		(m.authSettings == nil || (!m.authSettings.IsInFlow() && !m.authSettings.IsShowingFlowResult() && !m.authSettings.IsInAccountPicker() && !m.authSettings.IsEditingCode())) {
		m.state = MPStateMenu
		return nil
	}
	return nil
}

// renderAuthentication renders the authentication providers list view.
func (m *SectionModelProfiles) renderAuthentication(width, height int) string {
	// Delegate to the shared AuthSettings UI so the user sees the real OAuth
	// flow screens (device codes, browser-wait, post-flow result) and the
	// account picker — the previously-stripped local UI did not implement
	// those states.
	if m.authSettings != nil {
		return m.authSettings.Render(width, height, m.sharedState, m.sharedTheme)
	}

	var b strings.Builder

	switch m.authViewState {
	case "account_list":
		return m.renderAuthAccountList(width, height)
	case "usage":
		return m.renderAuthUsage(width, height)
	}

	// Ensure minimum usable width
	if width < 20 {
		width = 20
	}

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		MarginBottom(1)

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true).
		Width(width)

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("12")).
		Bold(true)

	b.WriteString(titleStyle.Render(i18n.T("settings.auth.title")))
	b.WriteString("\n")
	b.WriteString(descStyle.Render(i18n.T("settings.model_profiles.auth.description")))
	b.WriteString("\n\n")

	if len(m.authProviders) == 0 {
		b.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Italic(true).
			Width(width).
			Render(i18n.T("settings.auth.empty")))
	} else {
		// Header row — adapt columns to available width
		colW := width - 4
		nameW := colW * 2 / 3
		if nameW > 20 {
			nameW = 20
		}
		statusW := colW - nameW
		if statusW < 8 {
			statusW = 8
		}
		sepW := nameW + statusW
		if sepW > width {
			sepW = width
		}

		b.WriteString(headerStyle.Render(fmt.Sprintf("%-*s %*s", nameW, i18n.T("settings.reliability.field.provider"), statusW, i18n.T("settings.model_profiles.auth.status"))))
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("7")).
			Render(strings.Repeat("─", sepW)))
		b.WriteString("\n")

		// Provider rows
		for i, p := range m.authProviders {
			isSelected := i == m.authCursor

			statusIcon := "○"
			statusLabel := i18n.T("settings.auth.status.not_logged_in")
			if len(p.Accounts) > 0 {
				statusIcon = "●"
				n := len(p.Accounts)
				if n == 1 {
					statusLabel = i18n.T("settings.auth.status.one_account")
				} else {
					statusLabel = i18n.T("settings.auth.status.accounts", n)
				}
			}

			cursor := "  "
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
			if isSelected {
				cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("▶ ")
				style = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
			}

			displayName := p.DisplayName
			maxNameLen := nameW - 3 // cursor + dot + space
			if maxNameLen < 4 {
				maxNameLen = 4
			}
			if len(displayName) > maxNameLen {
				displayName = displayName[:maxNameLen-1] + "…"
			}

			colorDot := lipgloss.NewStyle().Foreground(lipgloss.Color(p.Color)).Render("◆")
			name := style.Render(displayName)
			status := lipgloss.NewStyle().Foreground(lipgloss.Color("7")).
				Render(fmt.Sprintf("%s %s", statusIcon, statusLabel))

			b.WriteString(cursor + colorDot + " " + name + " " + status)
			b.WriteString("\n")

			// Show email of active account
			if len(p.Accounts) > 0 {
				for _, acct := range p.Accounts {
					if acct.IsActive && acct.Email != "" {
						email := acct.Email
						maxEmail := width - 6
						if maxEmail > 0 && len(email) > maxEmail {
							email = email[:maxEmail-1] + "…"
						}
						emailLine := lipgloss.NewStyle().
							Foreground(lipgloss.Color("8")).
							Italic(true).
							Render(fmt.Sprintf("    %s", email))
						b.WriteString(emailLine)
						b.WriteString("\n")
						break
					}
				}
			}

			// Hint for selected item
			if isSelected {
				hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
				var hint string
				if len(p.Accounts) > 0 {
					hint = i18n.T("settings.model_profiles.auth.manage")
				} else {
					hint = i18n.T("settings.model_profiles.auth.login")
				}
				b.WriteString(hintStyle.Render(hint))
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n")
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	if width < 50 {
		b.WriteString(helpStyle.Render(i18n.T("settings.model_profiles.auth.hints.compact")))
	} else {
		b.WriteString(helpStyle.Render(i18n.T("settings.model_profiles.auth.hints")))
	}

	return b.String()
}

// renderAuthAccountList renders the account picker sub-view.
func (m *SectionModelProfiles) renderAuthAccountList(width, height int) string {
	var b strings.Builder

	if m.authCursor < 0 || m.authCursor >= len(m.authProviders) {
		return m.renderAuthentication(width, height)
	}
	provider := m.authProviders[m.authCursor]
	accts := m.loadAccountsForProvider(provider.Name)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12"))

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true)

	b.WriteString(titleStyle.Render(i18n.T("settings.model_profiles.auth.accounts_title", provider.DisplayName)))
	b.WriteString("\n")
	b.WriteString(descStyle.Render(i18n.T("settings.model_profiles.auth.accounts_description", provider.DisplayName)))
	b.WriteString("\n\n")

	// "+ Add account" row
	isSelected := m.authPickerIdx == 0
	cursor := "  "
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	if isSelected {
		cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("▶ ")
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	}
	b.WriteString(cursor + style.Render(i18n.T("settings.model_profiles.auth.add_account")))
	b.WriteString("\n")

	// Account rows
	for i, acct := range accts {
		isSelected := m.authPickerIdx == i+1
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
		if isSelected {
			cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("▶ ")
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
		}

		activeMark := " "
		if acct.IsActive {
			activeMark = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("●")
		}

		label := acct.displayLabel()
		b.WriteString(cursor + activeMark + " " + style.Render(label))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	b.WriteString(helpStyle.Render(i18n.T("settings.model_profiles.auth.accounts_hints")))

	return b.String()
}

// renderAuthUsage renders the usage statistics placeholder.
func (m *SectionModelProfiles) renderAuthUsage(width, height int) string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12"))

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true)

	b.WriteString(titleStyle.Render(i18n.T("settings.auth.usage.row")))
	b.WriteString("\n\n")
	b.WriteString(descStyle.Render(i18n.T("settings.auth.usage.available")))
	b.WriteString("\n\n")
	b.WriteString(descStyle.Render(i18n.T("settings.auth.usage.start")))
	b.WriteString("\n\n")
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	b.WriteString(helpStyle.Render(i18n.T("settings.auth.usage.back")))

	return b.String()
}

// loadAuthProviders loads OAuth providers from the configuration.
func (m *SectionModelProfiles) loadAuthProviders() {
	cm, err := commands.NewConfigManager()
	if err != nil {
		return
	}
	configs, err := cm.LoadProviders()
	if err != nil {
		return
	}
	m.authProviders = make([]authProviderEntry, 0, len(configs))
	for _, p := range configs {
		if p.Type != "oauth" {
			continue
		}
		accts := m.loadAccountsForProvider(p.Name)
		m.authProviders = append(m.authProviders, authProviderEntry{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Color:       p.Color,
			IsAuthed:    p.Available || len(accts) > 0,
			Accounts:    accts,
		})
	}
}

// loadAccountsForProvider returns accounts for the given provider.
func (m *SectionModelProfiles) loadAccountsForProvider(provider string) []authAccount {
	return accountsForProvider(provider)
}

// SetAuthSettings sets the AuthSettings reference for OAuth flow integration.
func (m *SectionModelProfiles) SetAuthSettings(auth *AuthSettings) {
	m.authSettings = auth
}

// cyclePrev returns the previous item in a list, wrapping around.
func cyclePrev(current string, options []string) string {
	if len(options) == 0 {
		return current
	}
	idx := -1
	for i, o := range options {
		if o == current {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return options[len(options)-1]
	}
	return options[idx-1]
}

// cycleNext returns the next item in a list, wrapping around.
func cycleNext(current string, options []string) string {
	if len(options) == 0 {
		return current
	}
	idx := -1
	for i, o := range options {
		if o == current {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(options)-1 {
		return options[0]
	}
	return options[idx+1]
}

func (m *SectionModelProfiles) IsInNestedState() bool {
	return m.state != MPStateMenu
}

// IsEditingText returns true when a text input field is active.
// Used to prevent 'q' from being treated as quit.
func (m *SectionModelProfiles) IsEditingText() bool {
	switch m.state {
	case MPStateAddProvider, MPStateEditProvider:
		return true
	case MPStateProviderModels:
		// When editing models, check if the model form is being edited
		if m.model != nil && (m.model.formEditing ||
			(m.model.modelFormField >= 0 && m.model.modelFormField <= 2)) {
			return true
		}
	case MPStateAuthentication:
		// Claude OAuth code entry accepts character input — block 'q' quit.
		if m.authSettings != nil && m.authSettings.IsEditingCode() {
			return true
		}
	}
	return false
}
