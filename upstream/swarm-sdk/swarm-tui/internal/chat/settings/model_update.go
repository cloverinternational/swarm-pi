package settings

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// HandleKey handles keyboard input for model settings
func (m *ModelSettings) HandleKey(key string) bool {
	if m.confirmOverride {
		return m.handleOverrideKey(key)
	}

	previousState := m.state
	defer m.clearSearchIfStateChanged(previousState)

	if m.handleSearchKey(key) {
		return true
	}
	if m.handleFilterKey(key) {
		return true
	}

	switch m.state {
	case "menu":
		return m.handleMenuKey(key)
	case "browse_models":
		return m.handleBrowseKey(key)
	case "manage_providers":
		return m.handleManageKey(key)
	case "add_provider":
		return m.handleAddProviderKey(key)
	case "edit_provider":
		return m.handleEditProviderKey(key)
	case "provider_models":
		return m.handleProviderModelsKey(key)
	case "models":
		return m.handleModelsKey(key)
	case "alias_variants":
		return m.handleAliasVariantsKey(key)
	case "add_model":
		return m.handleAddModelKey(key)
	case "edit_model":
		return m.handleEditModelKey(key)
	case "confirm_delete":
		return m.handleConfirmDeleteKey(key)
	}
	return false
}

func (m *ModelSettings) handleMenuKey(key string) bool {
	menuIDs := m.menuOptionIDs()
	if len(menuIDs) == 0 {
		return false
	}
	if m.selectedMenuItem < 0 {
		m.selectedMenuItem = 0
	}
	if m.selectedMenuItem >= len(menuIDs) {
		m.selectedMenuItem = len(menuIDs) - 1
	}

	switch key {
	case "up", "k":
		if m.selectedMenuItem > 0 {
			m.selectedMenuItem--
		}
		return true
	case "down", "j":
		if m.selectedMenuItem < len(menuIDs)-1 {
			m.selectedMenuItem++
		}
		return true
	case "enter", " ":
		switch menuIDs[m.selectedMenuItem] {
		case MenuBrowseModels:
			m.state = "browse_models"
			m.selectedProvider = 0
			m.selectedAlias = 0
			m.selectedVariant = 0
			m.scrollOffset = 0
		case MenuManageProviders:
			m.state = "manage_providers"
			m.selectedProvider = 0
			m.scrollOffset = 0
		case MenuAddProvider:
			m.state = "add_provider"
			m.formField = 0
			m.resetForm()
		case MenuReasoningEffort:
			m.cycleReasoningEffort(1)
		}
		return true
	case "left", "h":
		if menuIDs[m.selectedMenuItem] == MenuReasoningEffort {
			m.cycleReasoningEffort(-1)
			return true
		}
	case "right", "l":
		if menuIDs[m.selectedMenuItem] == MenuReasoningEffort {
			m.cycleReasoningEffort(1)
			return true
		}
	}
	return false
}

func (m *ModelSettings) menuOptionIDs() []int {
	ids := []int{
		MenuBrowseModels,
		MenuManageProviders,
		MenuAddProvider,
	}
	if m.selectionSupportsReasoningEffort() {
		ids = append(ids, MenuReasoningEffort)
	}
	return ids
}

func (m *ModelSettings) handleBrowseKey(key string) bool {
	if m.usesAliases() {
		switch key {
		case "up", "k":
			var results listResults = m.aliasResults()
			m.moveSelection(results.Indices, &m.selectedAlias, -1)
			return true
		case "down", "j":
			var results listResults = m.aliasResults()
			m.moveSelection(results.Indices, &m.selectedAlias, 1)
			return true
		case "enter":
			var results listResults = m.aliasResults()
			if len(results.Indices) == 0 {
				return true
			}
			m.ensureSelection(results.Indices, &m.selectedAlias)
			if len(m.aliasEntries) == 0 {
				m.state = "menu"
				return true
			}
			var alias commands.ModelAliasEntry = m.aliasEntries[m.selectedAlias]
			if len(alias.Variants) == 1 {
				var variant commands.ModelAliasVariant = alias.Variants[0]
				m.SetModel(variant.ProviderName, variant.Model.ID)
				return true
			}
			m.state = "alias_variants"
			m.selectedVariant = 0
			m.scrollOffset = 0
			return true
		case "esc", "backspace":
			m.state = "menu"
			return true
		}
		return false
	}

	switch key {
	case "up", "k":
		var results listResults = m.providerResults()
		m.moveSelection(results.Indices, &m.selectedProvider, -1)
		return true
	case "down", "j":
		var results listResults = m.providerResults()
		m.moveSelection(results.Indices, &m.selectedProvider, 1)
		return true
	case "enter":
		var results listResults = m.providerResults()
		if len(results.Indices) == 0 {
			return true
		}
		m.ensureSelection(results.Indices, &m.selectedProvider)
		m.state = "models"
		m.selectedModel = 0
		m.scrollOffset = 0
		return true
	case "esc", "backspace":
		m.state = "menu"
		return true
	}
	return false
}

func (m *ModelSettings) handleManageKey(key string) bool {
	switch key {
	case "up", "k":
		var results listResults = m.providerResults()
		m.moveSelection(results.Indices, &m.selectedProvider, -1)
		return true
	case "down", "j":
		var results listResults = m.providerResults()
		m.moveSelection(results.Indices, &m.selectedProvider, 1)
		return true
	case "enter":
		var results listResults = m.providerResults()
		if len(results.Indices) == 0 {
			return true
		}
		m.ensureSelection(results.Indices, &m.selectedProvider)
		if m.selectedProviderIsPlexus() {
			m.openPlexusProvider()
			return true
		}
		// View provider models
		m.state = "provider_models"
		m.selectedModel = 0
		m.scrollOffset = 0
		return true
	case "e":
		var results listResults = m.providerResults()
		if len(results.Indices) == 0 {
			return true
		}
		m.ensureSelection(results.Indices, &m.selectedProvider)
		// Edit provider
		m.searchActive = false // Exit search mode
		if m.selectedProviderIsPlexus() {
			m.openPlexusProvider()
			return true
		}
		m.state = "edit_provider"
		m.loadProviderToForm(m.selectedProvider)
		m.formField = 0
		return true
	case "d":
		var results listResults = m.providerResults()
		if len(results.Indices) == 0 {
			return true
		}
		m.ensureSelection(results.Indices, &m.selectedProvider)
		// Delete provider (with confirmation)
		m.searchActive = false // Exit search mode
		if len(m.providers) > 0 && m.selectedProvider < len(m.providers) {
			m.confirmDeleteType = "provider"
			m.confirmDeleteIndex = m.selectedProvider
			m.confirmSelected = 0 // Default to "No"
			m.confirmPreviousState = "manage_providers"
			m.state = "confirm_delete"
		}
		return true
	case "r":
		var results listResults = m.providerResults()
		if len(results.Indices) == 0 {
			return true
		}
		m.ensureSelection(results.Indices, &m.selectedProvider)
		return m.refreshOpenRouterModels()
	case "esc", "backspace":
		m.state = "menu"
		return true
	}
	return false
}

func (m *ModelSettings) handleProviderModelsKey(key string) bool {
	if len(m.providers) == 0 || m.selectedProvider >= len(m.providers) {
		return false
	}

	providerModels := m.providers[m.selectedProvider].Models
	switch key {
	case "up", "k":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		m.moveSelection(results.Indices, &m.selectedModel, -1)
		return true
	case "down", "j":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		m.moveSelection(results.Indices, &m.selectedModel, 1)
		return true
	case "enter", " ":
		if len(providerModels) == 0 {
			// Bounce back if provider somehow has no models
			m.state = "manage_providers"
			m.selectedModel = 0
			m.scrollOffset = 0
			return true
		}
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) == 0 {
			return true
		}
		m.ensureSelection(results.Indices, &m.selectedModel)
		// Select this model and return to main menu
		provider := m.providers[m.selectedProvider]
		model := providerModels[m.selectedModel]
		m.SetModel(provider.Name, model.ID)
		return true
	case "a":
		// Add new model
		m.searchActive = false // Exit search mode
		m.state = "add_model"
		m.editingModelIndex = -1
		m.resetModelForm()
		m.modelFormPreviousState = "provider_models" // Remember where we came from
		return true
	case "e":
		// Edit selected model
		m.searchActive = false // Exit search mode
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) > 0 {
			m.ensureSelection(results.Indices, &m.selectedModel)
			m.state = "edit_model"
			m.editingModelIndex = m.selectedModel
			m.loadModelToForm(m.selectedProvider, m.selectedModel)
			m.modelFormPreviousState = "provider_models" // Remember where we came from
		}
		return true
	case "d":
		// Delete selected model (with confirmation)
		m.searchActive = false // Exit search mode
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) > 0 {
			m.ensureSelection(results.Indices, &m.selectedModel)
			m.confirmDeleteType = "model"
			m.confirmDeleteIndex = m.selectedModel
			m.confirmSelected = 0 // Default to "No"
			m.confirmPreviousState = "provider_models"
			m.state = "confirm_delete"
		}
		return true
	case "r":
		return m.refreshOpenRouterModels()
	case "esc", "backspace", "tab":
		m.state = "manage_providers"
		return true
	}
	return false
}

func (m *ModelSettings) handleModelsKey(key string) bool {
	if len(m.providers) == 0 || m.selectedProvider >= len(m.providers) {
		return false
	}

	providerModels := m.providers[m.selectedProvider].Models
	switch key {
	case "up", "k":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		m.moveSelection(results.Indices, &m.selectedModel, -1)
		return true
	case "down", "j":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		m.moveSelection(results.Indices, &m.selectedModel, 1)
		return true
	case "enter":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) == 0 {
			return true
		}
		m.ensureSelection(results.Indices, &m.selectedModel)
		// Select this model
		provider := m.providers[m.selectedProvider]
		model := providerModels[m.selectedModel]
		m.SetModel(provider.Name, model.ID)
		return true
	case "a":
		// Add new model
		m.searchActive = false // Exit search mode
		m.state = "add_model"
		m.editingModelIndex = -1
		m.resetModelForm()
		m.modelFormPreviousState = "models" // Remember where we came from
		return true
	case "e":
		// Edit selected model
		m.searchActive = false // Exit search mode
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) > 0 {
			m.ensureSelection(results.Indices, &m.selectedModel)
			m.state = "edit_model"
			m.editingModelIndex = m.selectedModel
			m.loadModelToForm(m.selectedProvider, m.selectedModel)
			m.modelFormPreviousState = "provider_models" // Remember where we came from
		}
		return true
	case "d":
		// Delete selected model (with confirmation)
		m.searchActive = false // Exit search mode
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) > 0 {
			m.ensureSelection(results.Indices, &m.selectedModel)
			m.confirmDeleteType = "model"
			m.confirmDeleteIndex = m.selectedModel
			m.confirmSelected = 0 // Default to "No"
			m.confirmPreviousState = "models"
			m.state = "confirm_delete"
		}
		return true
	case "r":
		return m.refreshOpenRouterModels()
	case "esc", "backspace":
		m.state = "browse_models"
		return true
	}
	return false
}

func (m *ModelSettings) handleAliasVariantsKey(key string) bool {
	if len(m.aliasEntries) == 0 || m.selectedAlias >= len(m.aliasEntries) {
		m.state = "browse_models"
		return true
	}

	var variants []commands.ModelAliasVariant = m.aliasEntries[m.selectedAlias].Variants
	if len(variants) == 0 {
		m.state = "browse_models"
		return true
	}

	switch key {
	case "up", "k":
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		m.moveSelection(results.Indices, &m.selectedVariant, -1)
		return true
	case "down", "j":
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		m.moveSelection(results.Indices, &m.selectedVariant, 1)
		return true
	case "enter":
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		if len(results.Indices) == 0 {
			return true
		}
		m.ensureSelection(results.Indices, &m.selectedVariant)
		var variant commands.ModelAliasVariant = variants[m.selectedVariant]
		m.SetModel(variant.ProviderName, variant.Model.ID)
		return true
	case "esc", "backspace":
		m.state = "browse_models"
		return true
	}

	return false
}

func (m *ModelSettings) handleAddProviderKey(key string) bool {
	// ctrl+s saves immediately even when editing a text field
	if key == "ctrl+s" {
		if m.formEditing {
			m.formEditing = false
		}
		return m.doSaveProvider()
	}

	// If editing a text field, handle text input
	if m.formEditing {
		return m.handleTextInput(key)
	}

	// Navigation mode
	switch key {
	case "up", "k", "shift+tab":
		if m.formField > 0 {
			m.formField--
		}
		return true
	case "down", "j", "tab":
		if m.formField < 6 { // 0-6: provider type, display name, endpoint, auth method, api key, color, save button
			m.formField++
		}
		return true
	case "left", "h":
		// Cycle values for specific fields
		if m.formField == 0 {
			// Provider type - cycle backwards
			for i, pt := range providerTypePresets {
				if pt.name == m.formProviderType {
					prev := (i - 1 + len(providerTypePresets)) % len(providerTypePresets)
					m.formProviderType = providerTypePresets[prev].name
					m.formAPIEndpoint = providerTypePresets[prev].endpoint
					if supportsOAuth(m.formProviderType) {
						m.formAuthType = coerceAuthType(m.formProviderType, m.formAuthType)
					} else {
						m.formAuthType = "api_key"
					}
					break
				}
			}
		} else if m.formField == 3 {
			if supportsOAuth(m.formProviderType) {
				m.formAuthType = toggleAuthType(m.formAuthType)
			}
		} else if m.formField == 5 {
			// Color - cycle backwards
			m.formColorIndex = (m.formColorIndex - 1 + len(colorPresets)) % len(colorPresets)
		}
		return true
	case "right", "l":
		// Cycle values for specific fields
		if m.formField == 0 {
			// Provider type - cycle forwards
			for i, pt := range providerTypePresets {
				if pt.name == m.formProviderType {
					next := (i + 1) % len(providerTypePresets)
					m.formProviderType = providerTypePresets[next].name
					m.formAPIEndpoint = providerTypePresets[next].endpoint
					if supportsOAuth(m.formProviderType) {
						m.formAuthType = coerceAuthType(m.formProviderType, m.formAuthType)
					} else {
						m.formAuthType = "api_key"
					}
					break
				}
			}
		} else if m.formField == 3 {
			if supportsOAuth(m.formProviderType) {
				m.formAuthType = toggleAuthType(m.formAuthType)
			}
		} else if m.formField == 5 {
			// Color - cycle forwards
			m.formColorIndex = (m.formColorIndex + 1) % len(colorPresets)
		}
		return true
	case "enter", " ":
		if m.formField == 1 || m.formField == 2 || m.formField == 4 { // Display Name, Endpoint, or API Key
			// Enter edit mode for text fields
			m.formEditing = true
			if m.formField == 1 {
				m.formCursorPos = len(m.formDisplayName)
			} else if m.formField == 2 {
				m.formCursorPos = len(m.formAPIEndpoint)
			} else {
				m.formCursorPos = len(m.formAPIKey)
			}
		} else if m.formField == 6 { // Save button
			return m.doSaveProvider()
		}
		return true
	case "esc", "backspace":
		// Auto-save when leaving edit_provider so changes aren't silently discarded.
		// For add_provider, discard is the expected behaviour (the record doesn't exist yet).
		if m.state == "edit_provider" {
			m.saveExistingProvider(m.selectedProvider)
		}
		// Go back to menu
		m.state = "menu"
		m.resetForm()
		return true
	}

	// Handle character input for text fields when selected. Accept any single
	// printable character so URLs and API keys containing symbols like =, +, ~,
	// <, > (which are unicode.Symbol, not Punct) are typed through immediately —
	// no need to press Enter first (flow-through editing).
	if m.formField == 1 || m.formField == 2 || m.formField == 4 {
		if _, ok := singlePrintableRune(key); ok {
			m.formEditing = true
			return m.handleTextInput(key)
		}
	}

	return false
}

// doSaveProvider saves the provider immediately and returns to the menu.
func (m *ModelSettings) doSaveProvider() bool {
	if m.state == "edit_provider" {
		m.saveExistingProvider(m.selectedProvider)
	} else {
		m.saveNewProvider()
	}
	m.state = "menu"
	m.resetForm()
	return true
}

// singlePrintableRune reports whether key is exactly one printable rune, used to
// decide if a keypress in the provider form should start flow-through text
// entry. Accepts letters, digits, punctuation AND symbols (=, +, ~, <, > …) so
// URLs and API keys type through without pressing Enter first.
func singlePrintableRune(key string) (rune, bool) {
	if len(key) != 1 {
		return 0, false
	}
	r := rune(key[0])
	if unicode.IsPrint(r) {
		return r, true
	}
	return 0, false
}

func (m *ModelSettings) handleEditProviderKey(key string) bool {
	if m.selectedProviderIsPlexus() {
		return m.handlePlexusProviderKey(key)
	}
	// Similar to add provider
	return m.handleAddProviderKey(key)
}

func (m *ModelSettings) handleAddModelKey(key string) bool {
	// If editing a text field, handle text input
	if m.formEditing {
		return m.handleModelTextInput(key)
	}

	// Navigation mode
	switch key {
	case "up", "k":
		if m.modelFormField > 0 {
			m.modelFormField--
			isAdaptive := modelSupportsAdaptiveThinking(m.modelFormID)
			// Skip thinking budget/effort if thinking is disabled
			if !m.modelFormThinkingEnabled && (m.modelFormField == 5 || m.modelFormField == 4) {
				m.modelFormField = 3
			} else if m.modelFormThinkingEnabled {
				// Skip the hidden field depending on mode
				if isAdaptive && m.modelFormField == 4 {
					m.modelFormField = 3 // Skip budget, go to toggle
				} else if !isAdaptive && m.modelFormField == 5 {
					m.modelFormField = 4 // Skip effort, go to budget
				}
			}
			// Update cursor position to match new field's content length
			if m.modelFormField == 0 {
				m.formCursorPos = len(m.modelFormID)
			} else if m.modelFormField == 1 {
				m.formCursorPos = len(m.modelFormDisplayName)
			} else if m.modelFormField == 2 {
				m.formCursorPos = len(m.modelFormContext)
			}
		}
		return true
	case "down", "j":
		maxField := modelFieldSave
		if m.modelFormField < maxField {
			m.modelFormField++
			isAdaptive := modelSupportsAdaptiveThinking(m.modelFormID)
			// Skip thinking budget/effort if thinking is disabled
			if !m.modelFormThinkingEnabled && m.modelFormField == 4 {
				m.modelFormField = 6 // Jump to save button
			} else if m.modelFormThinkingEnabled {
				// Skip the hidden field depending on mode
				if isAdaptive && m.modelFormField == 4 {
					m.modelFormField = 5 // Skip budget, go to effort
				} else if !isAdaptive && m.modelFormField == 5 {
					m.modelFormField = 6 // Skip effort, go to save button
				}
			}
			// Update cursor position to match new field's content length
			if m.modelFormField == 0 {
				m.formCursorPos = len(m.modelFormID)
			} else if m.modelFormField == 1 {
				m.formCursorPos = len(m.modelFormDisplayName)
			} else if m.modelFormField == 2 {
				m.formCursorPos = len(m.modelFormContext)
			}
		}
		return true
	case "left", "h":
		if m.adjustModelGenerationField(-1) {
			return true
		}
		// Field 3: Toggle thinking enabled
		if m.modelFormField == 3 {
			m.modelFormThinkingEnabled = !m.modelFormThinkingEnabled
			// Reset budget/effort when disabling
			if !m.modelFormThinkingEnabled {
				m.modelFormThinkingBudget = 2048
				m.modelFormThinkingEffort = ""
			}
			return true
		}
		// Field 5: Cycle thinking effort
		if m.modelFormField == 5 && m.modelFormThinkingEnabled {
			efforts := modelThinkingEfforts(m.modelFormID)
			currentIdx := 0
			for i, e := range efforts {
				if e == m.modelFormThinkingEffort {
					currentIdx = i
					break
				}
			}
			currentIdx = (currentIdx - 1 + len(efforts)) % len(efforts)
			m.modelFormThinkingEffort = efforts[currentIdx]
			return true
		}
		// Field 6: Toggle diffusion
		if m.modelFormField == 6 {
			m.modelFormDiffusion = !m.modelFormDiffusion
			return true
		}
		return false
	case "right", "l":
		if m.adjustModelGenerationField(1) {
			return true
		}
		// Field 3: Toggle thinking enabled
		if m.modelFormField == 3 {
			m.modelFormThinkingEnabled = !m.modelFormThinkingEnabled
			// Reset budget/effort when disabling
			if !m.modelFormThinkingEnabled {
				m.modelFormThinkingBudget = 2048
				m.modelFormThinkingEffort = ""
			}
			return true
		}
		// Field 5: Cycle thinking effort
		if m.modelFormField == 5 && m.modelFormThinkingEnabled {
			efforts := modelThinkingEfforts(m.modelFormID)
			currentIdx := 0
			for i, e := range efforts {
				if e == m.modelFormThinkingEffort {
					currentIdx = i
					break
				}
			}
			currentIdx = (currentIdx + 1) % len(efforts)
			m.modelFormThinkingEffort = efforts[currentIdx]
			return true
		}
		// Field 6: Toggle diffusion
		if m.modelFormField == 6 {
			m.modelFormDiffusion = !m.modelFormDiffusion
			return true
		}
		return false
	case "enter", " ":
		if m.modelFormField < 3 || m.modelFormField == 4 { // Text fields (ID, Display Name, Context, Budget)
			// Enter edit mode
			m.formEditing = true
			if m.modelFormField == 0 {
				m.formCursorPos = len(m.modelFormID)
			} else if m.modelFormField == 1 {
				m.formCursorPos = len(m.modelFormDisplayName)
			} else if m.modelFormField == 2 {
				m.formCursorPos = len(m.modelFormContext)
			} else if m.modelFormField == 4 {
				// For budget field, convert to string for editing
				m.formCursorPos = len(fmt.Sprintf("%d", m.modelFormThinkingBudget))
			}
		} else if m.modelFormField == 3 {
			// Field 3: Toggle thinking enabled
			m.modelFormThinkingEnabled = !m.modelFormThinkingEnabled
			// Reset budget/effort when disabling
			if !m.modelFormThinkingEnabled {
				m.modelFormThinkingBudget = 2048
				m.modelFormThinkingEffort = ""
			}
		} else if m.modelFormField == 5 { // Thinking Effort - cycle through options
			efforts := modelThinkingEfforts(m.modelFormID)
			currentIdx := 0
			for i, e := range efforts {
				if e == m.modelFormThinkingEffort {
					currentIdx = i
					break
				}
			}
			currentIdx = (currentIdx + 1) % len(efforts)
			m.modelFormThinkingEffort = efforts[currentIdx]
		} else if m.modelFormField == 6 { // Diffusion toggle
			m.modelFormDiffusion = !m.modelFormDiffusion
		} else if m.adjustModelGenerationField(1) {
			// Generation controls cycle with Enter/Space as well as arrows.
		} else if m.modelFormField == modelFieldSave {
			if m.validateModelForm() {
				m.saveNewModel()
				m.state = m.modelFormPreviousState // Return to where we came from
			}
		}
		return true
	case "esc", "backspace":
		m.state = m.modelFormPreviousState // Return to where we came from
		m.resetModelForm()
		return true
	}

	// Handle character input for text fields when selected
	if m.modelFormField >= 0 && m.modelFormField <= 2 {
		if len(key) == 1 && (unicode.IsLetter(rune(key[0])) || unicode.IsDigit(rune(key[0])) || unicode.IsPunct(rune(key[0])) || key[0] == ' ') {
			m.formEditing = true
			return m.handleModelTextInput(key)
		}
	}

	return false
}

// handleModelTextInput handles text input when editing model form fields
func (m *ModelSettings) handleModelTextInput(key string) bool {
	switch key {
	case "esc":
		m.formEditing = false
		return true
	case "enter":
		m.formEditing = false
		// For budget field, parse the input
		if m.modelFormField == 4 {
			// Budget is already an int, no conversion needed
		}
		if m.modelFormField < 6 {
			m.modelFormField++
			// Skip field 5 if thinking is disabled
			if !m.modelFormThinkingEnabled && m.modelFormField == 5 {
				m.modelFormField = 6
			}
		}
		return true
	case "ctrl+c":
		m.formEditing = false
		return true
	case "backspace", "ctrl+h":
		if m.modelFormField == 0 && m.formCursorPos > 0 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.modelFormID) {
				m.formCursorPos = len(m.modelFormID)
			}
			if m.formCursorPos > 0 {
				m.modelFormID = m.modelFormID[:m.formCursorPos-1] + m.modelFormID[m.formCursorPos:]
				m.formCursorPos--
			}
		} else if m.modelFormField == 1 && m.formCursorPos > 0 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.modelFormDisplayName) {
				m.formCursorPos = len(m.modelFormDisplayName)
			}
			if m.formCursorPos > 0 {
				m.modelFormDisplayName = m.modelFormDisplayName[:m.formCursorPos-1] + m.modelFormDisplayName[m.formCursorPos:]
				m.formCursorPos--
			}
		} else if m.modelFormField == 2 && m.formCursorPos > 0 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.modelFormContext) {
				m.formCursorPos = len(m.modelFormContext)
			}
			if m.formCursorPos > 0 {
				m.modelFormContext = m.modelFormContext[:m.formCursorPos-1] + m.modelFormContext[m.formCursorPos:]
				m.formCursorPos--
			}
		} else if m.modelFormField == 4 && m.modelFormThinkingBudget > 0 {
			// For budget, remove last digit
			m.modelFormThinkingBudget = m.modelFormThinkingBudget / 10
		}
		return true
	case "delete", "ctrl+d":
		if m.modelFormField == 0 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.modelFormID) {
				m.formCursorPos = len(m.modelFormID)
			}
			if m.formCursorPos < len(m.modelFormID) {
				m.modelFormID = m.modelFormID[:m.formCursorPos] + m.modelFormID[m.formCursorPos+1:]
			}
		} else if m.modelFormField == 1 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.modelFormDisplayName) {
				m.formCursorPos = len(m.modelFormDisplayName)
			}
			if m.formCursorPos < len(m.modelFormDisplayName) {
				m.modelFormDisplayName = m.modelFormDisplayName[:m.formCursorPos] + m.modelFormDisplayName[m.formCursorPos+1:]
			}
		} else if m.modelFormField == 2 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.modelFormContext) {
				m.formCursorPos = len(m.modelFormContext)
			}
			if m.formCursorPos < len(m.modelFormContext) {
				m.modelFormContext = m.modelFormContext[:m.formCursorPos] + m.modelFormContext[m.formCursorPos+1:]
			}
		}
		return true
	case "left", "ctrl+b":
		if m.formCursorPos > 0 {
			m.formCursorPos--
		}
		return true
	case "right", "ctrl+f":
		if m.modelFormField == 0 && m.formCursorPos < len(m.modelFormID) {
			m.formCursorPos++
		} else if m.modelFormField == 1 && m.formCursorPos < len(m.modelFormDisplayName) {
			m.formCursorPos++
		} else if m.modelFormField == 2 && m.formCursorPos < len(m.modelFormContext) {
			m.formCursorPos++
		}
		return true
	case "home", "ctrl+a":
		m.formCursorPos = 0
		return true
	case "end", "ctrl+e":
		if m.modelFormField == 0 {
			m.formCursorPos = len(m.modelFormID)
		} else if m.modelFormField == 1 {
			m.formCursorPos = len(m.modelFormDisplayName)
		} else if m.modelFormField == 2 {
			m.formCursorPos = len(m.modelFormContext)
		}
		return true
	default:
		// Insert character at cursor
		if len(key) == 1 {
			ch := rune(key[0])
			if unicode.IsPrint(ch) {
				if m.modelFormField == 0 {
					// Ensure cursor is within bounds before slice operation
					if m.formCursorPos > len(m.modelFormID) {
						m.formCursorPos = len(m.modelFormID)
					}
					m.modelFormID = m.modelFormID[:m.formCursorPos] + key + m.modelFormID[m.formCursorPos:]
					m.formCursorPos++
				} else if m.modelFormField == 1 {
					// Ensure cursor is within bounds before slice operation
					if m.formCursorPos > len(m.modelFormDisplayName) {
						m.formCursorPos = len(m.modelFormDisplayName)
					}
					m.modelFormDisplayName = m.modelFormDisplayName[:m.formCursorPos] + key + m.modelFormDisplayName[m.formCursorPos:]
					m.formCursorPos++
				} else if m.modelFormField == 2 {
					// Ensure cursor is within bounds before slice operation
					if m.formCursorPos > len(m.modelFormContext) {
						m.formCursorPos = len(m.modelFormContext)
					}
					m.modelFormContext = m.modelFormContext[:m.formCursorPos] + key + m.modelFormContext[m.formCursorPos:]
					m.formCursorPos++
				} else if m.modelFormField == 4 {
					// For budget field, only accept digits
					if unicode.IsDigit(ch) {
						digit := int(ch - '0')
						m.modelFormThinkingBudget = m.modelFormThinkingBudget*10 + digit
						// Cap at 100000
						if m.modelFormThinkingBudget > 100000 {
							m.modelFormThinkingBudget = 100000
						}
					}
				}
				return true
			}
		}
	}
	return false
}

// handleTextInput handles text input when editing a field
func (m *ModelSettings) handleTextInput(key string) bool {
	switch key {
	case "esc":
		// Exit edit mode
		m.formEditing = false
		return true
	case "enter", "tab":
		// Exit edit mode and move to next field
		m.formEditing = false
		if m.formField < 6 {
			m.formField++
		}
		return true
	case "shift+tab":
		// Exit edit mode and move to previous field
		m.formEditing = false
		if m.formField > 0 {
			m.formField--
		}
		return true
	case "ctrl+c":
		// Cancel editing
		m.formEditing = false
		return true
	case "backspace", "ctrl+h":
		// Delete character before cursor
		if m.formField == 1 && m.formCursorPos > 0 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.formDisplayName) {
				m.formCursorPos = len(m.formDisplayName)
			}
			if m.formCursorPos > 0 {
				m.formDisplayName = m.formDisplayName[:m.formCursorPos-1] + m.formDisplayName[m.formCursorPos:]
				m.formCursorPos--
			}
		} else if m.formField == 2 && m.formCursorPos > 0 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.formAPIEndpoint) {
				m.formCursorPos = len(m.formAPIEndpoint)
			}
			if m.formCursorPos > 0 {
				m.formAPIEndpoint = m.formAPIEndpoint[:m.formCursorPos-1] + m.formAPIEndpoint[m.formCursorPos:]
				m.formCursorPos--
			}
		} else if m.formField == 4 && m.formCursorPos > 0 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.formAPIKey) {
				m.formCursorPos = len(m.formAPIKey)
			}
			if m.formCursorPos > 0 {
				m.formAPIKey = m.formAPIKey[:m.formCursorPos-1] + m.formAPIKey[m.formCursorPos:]
				m.formCursorPos--
			}
		}
		return true
	case "delete", "ctrl+d":
		// Delete character at cursor
		if m.formField == 1 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.formDisplayName) {
				m.formCursorPos = len(m.formDisplayName)
			}
			if m.formCursorPos < len(m.formDisplayName) {
				m.formDisplayName = m.formDisplayName[:m.formCursorPos] + m.formDisplayName[m.formCursorPos+1:]
			}
		} else if m.formField == 2 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.formAPIEndpoint) {
				m.formCursorPos = len(m.formAPIEndpoint)
			}
			if m.formCursorPos < len(m.formAPIEndpoint) {
				m.formAPIEndpoint = m.formAPIEndpoint[:m.formCursorPos] + m.formAPIEndpoint[m.formCursorPos+1:]
			}
		} else if m.formField == 4 {
			// Clamp cursor position to valid range
			if m.formCursorPos > len(m.formAPIKey) {
				m.formCursorPos = len(m.formAPIKey)
			}
			if m.formCursorPos < len(m.formAPIKey) {
				m.formAPIKey = m.formAPIKey[:m.formCursorPos] + m.formAPIKey[m.formCursorPos+1:]
			}
		}
		return true
	case "left", "ctrl+b":
		// Move cursor left
		if m.formCursorPos > 0 {
			m.formCursorPos--
		}
		return true
	case "right", "ctrl+f":
		// Move cursor right
		if m.formField == 1 && m.formCursorPos < len(m.formDisplayName) {
			m.formCursorPos++
		} else if m.formField == 2 && m.formCursorPos < len(m.formAPIEndpoint) {
			m.formCursorPos++
		} else if m.formField == 4 && m.formCursorPos < len(m.formAPIKey) {
			m.formCursorPos++
		}
		return true
	case "home", "ctrl+a":
		// Move cursor to start
		m.formCursorPos = 0
		return true
	case "end", "ctrl+e":
		// Move cursor to end
		if m.formField == 1 {
			m.formCursorPos = len(m.formDisplayName)
		} else if m.formField == 2 {
			m.formCursorPos = len(m.formAPIEndpoint)
		} else if m.formField == 4 {
			m.formCursorPos = len(m.formAPIKey)
		}
		return true
	default:
		// Insert character at cursor
		if len(key) == 1 {
			ch := rune(key[0])
			if unicode.IsPrint(ch) {
				if m.formField == 1 {
					// Ensure cursor is within bounds before slice operation
					if m.formCursorPos > len(m.formDisplayName) {
						m.formCursorPos = len(m.formDisplayName)
					}
					m.formDisplayName = m.formDisplayName[:m.formCursorPos] + key + m.formDisplayName[m.formCursorPos:]
					m.formCursorPos++
				} else if m.formField == 2 {
					// Ensure cursor is within bounds before slice operation
					if m.formCursorPos > len(m.formAPIEndpoint) {
						m.formCursorPos = len(m.formAPIEndpoint)
					}
					m.formAPIEndpoint = m.formAPIEndpoint[:m.formCursorPos] + key + m.formAPIEndpoint[m.formCursorPos:]
					m.formCursorPos++
				} else if m.formField == 4 {
					// Ensure cursor is within bounds before slice operation
					if m.formCursorPos > len(m.formAPIKey) {
						m.formCursorPos = len(m.formAPIKey)
					}
					m.formAPIKey = m.formAPIKey[:m.formCursorPos] + key + m.formAPIKey[m.formCursorPos:]
					m.formCursorPos++
				}
				return true
			}
		}
	}
	return false
}

// validateModelForm checks if model form is valid
func (m *ModelSettings) validateModelForm() bool {
	if m.modelFormID == "" || m.modelFormDisplayName == "" {
		return false
	}
	if m.modelFormTemperature != nil && (*m.modelFormTemperature < 0 || *m.modelFormTemperature > 2) {
		return false
	}
	if m.modelFormMaxTokens < 0 || m.modelFormMaxTokens > 2_000_000 {
		return false
	}
	if m.modelFormTopP != nil && (*m.modelFormTopP < 0 || *m.modelFormTopP > 1) {
		return false
	}
	return m.modelFormTopK == nil || *m.modelFormTopK >= 0
}

const (
	modelFieldTemperature = 7
	modelFieldMaxTokens   = 8
	modelFieldTopP        = 9
	modelFieldTopK        = 10
	modelFieldSave        = 11
)

func (m *ModelSettings) adjustModelGenerationField(direction int) bool {
	caps := m.generationCapabilities()
	if !caps.supports(m.modelFormField) {
		return m.modelFormField >= modelFieldTemperature && m.modelFormField <= modelFieldTopK
	}
	switch m.modelFormField {
	case modelFieldTemperature:
		m.modelFormTemperature = adjustOptionalFloat(m.modelFormTemperature, direction, 0.1, caps.temperatureMax)
	case modelFieldMaxTokens:
		m.modelFormMaxTokens = adjustInheritedInt(m.modelFormMaxTokens, direction, 1024, 2_000_000)
	case modelFieldTopP:
		m.modelFormTopP = adjustOptionalFloat(m.modelFormTopP, direction, 0.05, 1)
	case modelFieldTopK:
		m.modelFormTopK = adjustOptionalInt(m.modelFormTopK, direction, 1)
	default:
		return false
	}
	return true
}

func adjustOptionalFloat(value *float64, direction int, step, maximum float64) *float64 {
	if direction == 0 {
		return value
	}
	if value == nil {
		if direction < 0 {
			return nil
		}
		v := 0.0
		return &v
	}
	next := *value + float64(direction)*step
	if next < 0 {
		return nil
	}
	if next > maximum {
		next = maximum
	}
	next = float64(int(next*100+0.5)) / 100
	return &next
}

func adjustInheritedInt(value, direction, step, maximum int) int {
	if direction <= 0 {
		value -= step
		if value < 0 {
			return 0
		}
		return value
	}
	value += step
	if value > maximum {
		return maximum
	}
	return value
}

func adjustOptionalInt(value *int, direction, step int) *int {
	if direction == 0 {
		return value
	}
	if value == nil {
		if direction < 0 {
			return nil
		}
		v := 0
		return &v
	}
	next := *value + direction*step
	if next < 0 {
		return nil
	}
	return &next
}

// handleEditModelKey handles key input for edit model form
// Fields 0-6 are identity/reasoning/diffusion, 7-10 are generation
// overrides, and 11 is Save.
func (m *ModelSettings) handleEditModelKey(key string) bool {
	// If editing a text field, handle text input
	if m.formEditing {
		return m.handleModelTextInput(key)
	}

	// Navigation mode — all 8 fields are always navigable (greyed-out ones just don't respond to edits)
	switch key {
	case "up", "k":
		if m.modelFormField > 0 {
			m.modelFormField--
			m.syncCursorToField()
		}
		return true
	case "down", "j":
		if m.modelFormField < modelFieldSave {
			m.modelFormField++
			m.syncCursorToField()
		}
		return true
	case "left", "h":
		if m.adjustModelGenerationField(-1) {
			return true
		}
		if m.modelFormField == 3 {
			m.modelFormThinkingEnabled = !m.modelFormThinkingEnabled
			if !m.modelFormThinkingEnabled {
				m.modelFormThinkingBudget = 2048
				m.modelFormThinkingEffort = ""
			}
			return true
		}
		if m.modelFormField == 5 && m.modelFormThinkingEnabled {
			efforts := modelThinkingEfforts(m.modelFormID)
			idx := effortIndex(efforts, m.modelFormThinkingEffort)
			idx = (idx - 1 + len(efforts)) % len(efforts)
			m.modelFormThinkingEffort = efforts[idx]
			return true
		}
		if m.modelFormField == 6 {
			m.modelFormDiffusion = !m.modelFormDiffusion
			return true
		}
		return true // consume so nothing weird happens
	case "right", "l":
		if m.adjustModelGenerationField(1) {
			return true
		}
		if m.modelFormField == 3 {
			m.modelFormThinkingEnabled = !m.modelFormThinkingEnabled
			if !m.modelFormThinkingEnabled {
				m.modelFormThinkingBudget = 2048
				m.modelFormThinkingEffort = ""
			}
			return true
		}
		if m.modelFormField == 5 && m.modelFormThinkingEnabled {
			efforts := modelThinkingEfforts(m.modelFormID)
			idx := effortIndex(efforts, m.modelFormThinkingEffort)
			idx = (idx + 1) % len(efforts)
			m.modelFormThinkingEffort = efforts[idx]
			return true
		}
		if m.modelFormField == 6 {
			m.modelFormDiffusion = !m.modelFormDiffusion
			return true
		}
		return true
	case "enter", " ":
		// Text fields: 0-2
		if m.modelFormField >= 0 && m.modelFormField <= 2 {
			m.formEditing = true
			m.syncCursorToField()
			return true
		}
		// Toggle thinking
		if m.modelFormField == 3 {
			m.modelFormThinkingEnabled = !m.modelFormThinkingEnabled
			if !m.modelFormThinkingEnabled {
				m.modelFormThinkingBudget = 2048
				m.modelFormThinkingEffort = ""
			}
			return true
		}
		// Budget edit — only if thinking is on
		if m.modelFormField == 4 && m.modelFormThinkingEnabled {
			m.formEditing = true
			m.formCursorPos = len(fmt.Sprintf("%d", m.modelFormThinkingBudget))
			return true
		}
		// Effort cycle
		if m.modelFormField == 5 && m.modelFormThinkingEnabled {
			efforts := modelThinkingEfforts(m.modelFormID)
			idx := effortIndex(efforts, m.modelFormThinkingEffort)
			idx = (idx + 1) % len(efforts)
			m.modelFormThinkingEffort = efforts[idx]
			return true
		}
		// Diffusion toggle
		if m.modelFormField == 6 {
			m.modelFormDiffusion = !m.modelFormDiffusion
			return true
		}
		if m.adjustModelGenerationField(1) {
			return true
		}
		// Save button
		if m.modelFormField == modelFieldSave {
			if m.validateModelForm() {
				m.saveEditedModel()
				m.state = m.modelFormPreviousState // Return to where we came from
			}
			return true
		}
		return true
	case "esc", "backspace":
		m.state = m.modelFormPreviousState // Return to where we came from
		m.resetModelForm()
		return true
	}

	// Start typing on text fields
	if m.modelFormField >= 0 && m.modelFormField <= 2 {
		if len(key) == 1 && (unicode.IsLetter(rune(key[0])) || unicode.IsDigit(rune(key[0])) || unicode.IsPunct(rune(key[0])) || key[0] == ' ') {
			m.formEditing = true
			return m.handleModelTextInput(key)
		}
	}

	return false
}

// syncCursorToField positions the text cursor for the current field.
func (m *ModelSettings) syncCursorToField() {
	switch m.modelFormField {
	case 0:
		m.formCursorPos = len(m.modelFormID)
	case 1:
		m.formCursorPos = len(m.modelFormDisplayName)
	case 2:
		m.formCursorPos = len(m.modelFormContext)
	}
}

// effortIndex returns the index of val in efforts, defaulting to 0.
func effortIndex(efforts []string, val string) int {
	for i, e := range efforts {
		if e == val {
			return i
		}
	}
	return 0
}

// handleConfirmDeleteKey handles key input for confirmation dialog
func (m *ModelSettings) handleConfirmDeleteKey(key string) bool {
	switch key {
	case "left", "h":
		m.confirmSelected = 0 // No
		return true
	case "right", "l":
		m.confirmSelected = 1 // Yes
		return true
	case "enter", " ":
		if m.confirmSelected == 1 { // Yes - proceed with delete
			switch m.confirmDeleteType {
			case "model":
				m.deleteModel(m.selectedProvider, m.confirmDeleteIndex)
			case "provider":
				m.deleteProvider(m.confirmDeleteIndex)
			case "custom_provider":
				if m.confirmCustomMode == "edit" {
					m.saveExistingProvider(m.confirmCustomIndex)
				} else {
					m.saveNewProvider()
				}
				m.state = "menu"
				m.resetForm()
				m.confirmCustomMode = ""
				m.confirmCustomIndex = 0
				return true
			}
		}
		// Return to previous state
		m.state = m.confirmPreviousState
		return true
	case "esc", "backspace", "n":
		// Cancel - return to previous state
		m.state = m.confirmPreviousState
		return true
	case "y":
		// Quick confirm with 'y'
		switch m.confirmDeleteType {
		case "model":
			m.deleteModel(m.selectedProvider, m.confirmDeleteIndex)
		case "provider":
			m.deleteProvider(m.confirmDeleteIndex)
		case "custom_provider":
			if m.confirmCustomMode == "edit" {
				m.saveExistingProvider(m.confirmCustomIndex)
			} else {
				m.saveNewProvider()
			}
			m.state = "menu"
			m.resetForm()
			m.confirmCustomMode = ""
			m.confirmCustomIndex = 0
			return true
		}
		m.state = m.confirmPreviousState
		return true
	}
	return false
}

// loadModelToForm loads model data into form for editing
func (m *ModelSettings) loadModelToForm(providerIndex, modelIndex int) {
	if providerIndex >= len(m.providers) {
		return
	}
	if modelIndex >= len(m.providers[providerIndex].Models) {
		return
	}

	model := m.providers[providerIndex].Models[modelIndex]
	m.modelFormID = model.ID
	m.modelFormDisplayName = model.DisplayName
	m.modelFormContext = model.Context
	m.modelFormDiffusion = model.Diffusion
	m.modelFormTemperature = model.Temperature
	m.modelFormMaxTokens = model.MaxTokens
	m.modelFormTopP = model.TopP
	m.modelFormTopK = model.TopK
	m.modelFormField = 0
	m.formEditing = false
	m.formCursorPos = 0

	// Load thinking settings from provider config
	if m.configManager != nil {
		providers, err := m.configManager.LoadProviders()
		if err == nil {
			providerName := m.providers[providerIndex].Name
			for _, provider := range providers {
				if provider.Name == providerName {
					for _, modelCfg := range provider.Models {
						if modelCfg.ID == model.ID {
							m.modelFormThinkingEnabled = modelCfg.ThinkingEnabled
							m.modelFormThinkingBudget = modelCfg.ThinkingBudget
							m.modelFormThinkingEffort = modelCfg.ThinkingEffort
							m.modelFormDiffusion = modelCfg.Diffusion
							m.modelFormTemperature = modelCfg.Temperature
							m.modelFormMaxTokens = modelCfg.MaxTokens
							m.modelFormTopP = modelCfg.TopP
							m.modelFormTopK = modelCfg.TopK
							return
						}
					}
				}
			}
		}
	}

	// Default values if not found in config
	m.modelFormThinkingEnabled = false
	m.modelFormThinkingBudget = 2048
	m.modelFormThinkingEffort = ""
	m.modelFormTemperature = nil
	m.modelFormMaxTokens = 0
	m.modelFormTopP = nil
	m.modelFormTopK = nil
}

// saveEditedModel saves changes to an existing model
func (m *ModelSettings) saveEditedModel() {
	if m.selectedProvider >= len(m.providers) {
		return
	}
	if m.editingModelIndex < 0 || m.editingModelIndex >= len(m.providers[m.selectedProvider].Models) {
		return
	}

	var existing commands.ModelInfo = m.providers[m.selectedProvider].Models[m.editingModelIndex]

	// Parse the context string ("262144", "128k", "1m") into the integer
	// context_window. The runtime prefers the int over the legacy string
	// (GetModelContextWindow), so both must track the edit or a stale int
	// silently shadows the new value.
	parsedContextWindow := commands.ParseContextValue(m.modelFormContext)

	// Update the model
	m.providers[m.selectedProvider].Models[m.editingModelIndex] = commands.ModelInfo{
		ID:                      m.modelFormID,
		DisplayName:             m.modelFormDisplayName,
		Context:                 m.modelFormContext,
		ContextWindow:           parsedContextWindow,
		Description:             existing.Description,
		SupportsReasoningEffort: existing.SupportsReasoningEffort,
		ReasoningEfforts:        existing.ReasoningEfforts,
		Temperature:             m.modelFormTemperature,
		MaxTokens:               m.modelFormMaxTokens,
		TopP:                    m.modelFormTopP,
		TopK:                    m.modelFormTopK,
		Diffusion:               m.modelFormDiffusion,
	}

	// Persist ALL edited fields to config before resetting the form — not just
	// thinking settings. (Context/display-name edits used to be dropped here,
	// so they reverted on the next reload.)
	if m.configManager != nil {
		providers, err := m.configManager.LoadProviders()
		if err == nil {
			providerName := m.providers[m.selectedProvider].Name

			for i, provider := range providers {
				if provider.Name == providerName {
					// Find or create the model config. Match by the ORIGINAL ID
					// first so an ID rename updates in place instead of leaving
					// a stale duplicate behind.
					found := false
					for j, modelCfg := range provider.Models {
						if modelCfg.ID == existing.ID || modelCfg.ID == m.modelFormID {
							providers[i].Models[j].ID = m.modelFormID
							providers[i].Models[j].DisplayName = m.modelFormDisplayName
							providers[i].Models[j].Context = m.modelFormContext
							providers[i].Models[j].ContextWindow = parsedContextWindow
							providers[i].Models[j].ThinkingEnabled = m.modelFormThinkingEnabled
							providers[i].Models[j].ThinkingBudget = m.modelFormThinkingBudget
							providers[i].Models[j].ThinkingEffort = m.modelFormThinkingEffort
							providers[i].Models[j].Diffusion = m.modelFormDiffusion
							providers[i].Models[j].Temperature = m.modelFormTemperature
							providers[i].Models[j].MaxTokens = m.modelFormMaxTokens
							providers[i].Models[j].TopP = m.modelFormTopP
							providers[i].Models[j].TopK = m.modelFormTopK
							found = true
							break
						}
					}

					// If model not in config yet, add it with the full settings
					if !found {
						newModelCfg := commands.ModelConfig{
							ID:              m.modelFormID,
							DisplayName:     m.modelFormDisplayName,
							Context:         m.modelFormContext,
							ContextWindow:   parsedContextWindow,
							ThinkingEnabled: m.modelFormThinkingEnabled,
							ThinkingBudget:  m.modelFormThinkingBudget,
							ThinkingEffort:  m.modelFormThinkingEffort,
							Diffusion:       m.modelFormDiffusion,
							Temperature:     m.modelFormTemperature,
							MaxTokens:       m.modelFormMaxTokens,
							TopP:            m.modelFormTopP,
							TopK:            m.modelFormTopK,
						}
						providers[i].Models = append(providers[i].Models, newModelCfg)
					}
					break
				}
			}

			// Now save providers with the updated model settings
			if err := m.configManager.SaveProviders(providers); err != nil {
				logDebug("Failed to save providers with model settings: %v", err)
			} else if m.onModelConfigChange != nil {
				for _, provider := range providers {
					if provider.Name != providerName {
						continue
					}
					for _, model := range provider.Models {
						if model.ID == m.modelFormID {
							m.onModelConfigChange(providerName, model)
							break
						}
					}
					break
				}
			}
		}
	}

	m.resetModelForm()
}

// deleteModel removes a model from a provider
func (m *ModelSettings) deleteModel(providerIndex, modelIndex int) {
	if providerIndex >= len(m.providers) {
		return
	}
	if modelIndex >= len(m.providers[providerIndex].Models) {
		return
	}

	// Remove the model
	models := m.providers[providerIndex].Models
	m.providers[providerIndex].Models = append(models[:modelIndex], models[modelIndex+1:]...)

	// Adjust selection if needed
	if m.selectedModel >= len(m.providers[providerIndex].Models) {
		m.selectedModel = len(m.providers[providerIndex].Models) - 1
		if m.selectedModel < 0 {
			m.selectedModel = 0
		}
	}

	// Save to config file
	if m.configManager != nil {
		m.saveProvidersToConfig()
	}
}

// deleteProvider removes a provider
func (m *ModelSettings) deleteProvider(providerIndex int) {
	if providerIndex >= len(m.providers) {
		return
	}

	providerName := m.providers[providerIndex].Name

	// Remove the provider
	m.providers = append(m.providers[:providerIndex], m.providers[providerIndex+1:]...)

	// Adjust selection if needed
	if m.selectedProvider >= len(m.providers) {
		m.selectedProvider = len(m.providers) - 1
		if m.selectedProvider < 0 {
			m.selectedProvider = 0
		}
	}

	// Save to config file
	if m.configManager != nil {
		m.saveProvidersToConfig()
		// Also delete credentials for this provider
		_ = m.configManager.DeleteCredentials(providerName)
	}
}

// savePendingFormChanges commits any in-progress form edits to persistent storage
// without requiring the user to navigate to the [Save Provider] button.
// Called when the user chooses "Save & Exit" from the settings exit modal.
func (m *ModelSettings) savePendingFormChanges() {
	switch m.state {
	case "edit_provider":
		if !m.selectedProviderIsPlexus() {
			m.saveExistingProvider(m.selectedProvider)
		}
	case "add_provider":
		// Only persist a new provider if a name or API key was entered —
		// otherwise the user likely started the form by accident.
		if m.formDisplayName != "" || m.formAPIKey != "" {
			m.saveNewProvider()
		}
	}
}

func (m *ModelSettings) resetForm() {
	m.formProviderType = "OpenAI"
	m.formAuthType = defaultAuthTypeForProvider(m.formProviderType)
	m.formDisplayName = ""
	m.formAPIEndpoint = "https://api.openai.com/v1"
	m.formAPIKey = ""
	m.formColorIndex = 0
	m.formEditing = false
	m.formCursorPos = 0
	m.formField = 0
}

func (m *ModelSettings) resetModelForm() {
	m.modelFormField = 0
	m.modelFormID = ""
	m.modelFormDisplayName = ""
	m.modelFormContext = ""
	m.modelFormThinkingEnabled = false
	m.modelFormThinkingBudget = 2048
	m.modelFormThinkingEffort = ""
	m.modelFormDiffusion = false
	m.modelFormTemperature = nil
	m.modelFormMaxTokens = 0
	m.modelFormTopP = nil
	m.modelFormTopK = nil
	m.formEditing = false
	m.formCursorPos = 0
}

func (m *ModelSettings) saveNewModel() {
	if m.selectedProvider >= len(m.providers) {
		return
	}

	newModel := commands.ModelInfo{
		ID:            m.modelFormID,
		DisplayName:   m.modelFormDisplayName,
		Context:       m.modelFormContext,
		ContextWindow: commands.ParseContextValue(m.modelFormContext),
		Temperature:   m.modelFormTemperature,
		MaxTokens:     m.modelFormMaxTokens,
		TopP:          m.modelFormTopP,
		TopK:          m.modelFormTopK,
		Diffusion:     m.modelFormDiffusion,
	}

	m.providers[m.selectedProvider].Models = append(m.providers[m.selectedProvider].Models, newModel)
	m.resetModelForm()

	// Save to config file
	if m.configManager != nil {
		m.saveProvidersToConfig()
	}
}

// saveProvidersToConfig saves all providers to the config file
func (m *ModelSettings) saveProvidersToConfig() {
	if m.configManager == nil {
		return
	}

	// Load existing config to preserve thinking settings
	existingProviders, _ := m.configManager.LoadProviders()
	thinkingSettings := make(map[string]map[string]commands.ModelConfig) // provider -> model ID -> config
	existingAPIKeys := make(map[string]string)                           // provider name -> legacy stored api key
	existingSecretRefs := make(map[string]string)                        // provider name -> encrypted vault credential ID

	for _, provider := range existingProviders {
		thinkingSettings[provider.Name] = make(map[string]commands.ModelConfig)
		for _, model := range provider.Models {
			thinkingSettings[provider.Name][model.ID] = model
		}
		if provider.APIKey != "" {
			existingAPIKeys[provider.Name] = provider.APIKey
		}
		if provider.APIKeySecretRef != "" {
			existingSecretRefs[provider.Name] = provider.APIKeySecretRef
		}
	}

	providerConfigs := make([]commands.ProviderConfig, len(m.providers))
	for i, p := range m.providers {
		modelConfigs := make([]commands.ModelConfig, len(p.Models))
		for j, model := range p.Models {
			modelCfg := commands.ModelConfig{
				ID:            model.ID,
				DisplayName:   model.DisplayName,
				Context:       model.Context,
				ContextWindow: model.ContextWindow,
				Description:   model.Description,
				Diffusion:     model.Diffusion,
				Temperature:   model.Temperature,
				MaxTokens:     model.MaxTokens,
				TopP:          model.TopP,
				TopK:          model.TopK,
			}

			// Preserve catalog and reasoning metadata that is not editable here.
			if providerSettings, ok := thinkingSettings[p.Name]; ok {
				if existing, ok := providerSettings[model.ID]; ok {
					modelCfg = existing
					modelCfg.ID = model.ID
					modelCfg.DisplayName = model.DisplayName
					modelCfg.Context = model.Context
					modelCfg.ContextWindow = model.ContextWindow
					modelCfg.Description = model.Description
					modelCfg.Diffusion = model.Diffusion
					modelCfg.ThinkingEnabled = existing.ThinkingEnabled
					modelCfg.ThinkingBudget = existing.ThinkingBudget
					modelCfg.ThinkingEffort = existing.ThinkingEffort
				}
			}

			modelConfigs[j] = modelCfg
		}
		// Preserve the original Type, default to "api_key" if empty
		providerType := p.Type
		if providerType == "" {
			providerType = "api_key"
		}
		secretRef := p.APIKeySecretRef
		if secretRef == "" {
			secretRef = existingSecretRefs[p.Name]
		}
		legacyAPIKey := existingAPIKeys[p.Name]
		providerConfigs[i] = commands.ProviderConfig{
			Name:            p.Name,
			DisplayName:     p.DisplayName,
			Color:           p.Color,
			Type:            providerType,
			APIType:         p.APIType,
			BaseURL:         p.BaseURL,
			HTTPMaxRetries:  p.HTTPMaxRetries,
			APIKey:          legacyAPIKey,
			APIKeySecretRef: secretRef,
			Source:          p.Source,
			Available:       p.Available,
			Models:          modelConfigs,
		}
	}

	if err := m.configManager.SaveProviders(providerConfigs); err != nil {
		logDebug("Failed to save provider config: %v", err)
	}
}

func (m *ModelSettings) loadProviderToForm(index int) {
	if index >= len(m.providers) {
		return
	}
	p := m.providers[index]
	m.formDisplayName = p.DisplayName
	m.formAPIEndpoint = ""
	m.formAPIKey = ""
	m.formAuthType = ""

	// Try to match provider type
	if p.APIType != "" {
		m.formProviderType = resolveProviderTypeFromAPI(p.APIType)
	} else {
		m.formProviderType = "Custom"
		for _, pt := range providerTypePresets {
			if strings.Contains(strings.ToLower(p.Name), strings.ToLower(pt.name)) {
				m.formProviderType = pt.name
				m.formAPIEndpoint = pt.endpoint
				break
			}
		}
	}
	m.formAuthType = coerceAuthType(m.formProviderType, p.Type)

	// Load stored credentials for this provider
	if m.configManager != nil {
		if key, baseURL, err := m.configManager.LoadCredentials(p.Name); err == nil {
			if key != "" {
				m.formAPIKey = key
			}
			if baseURL != "" {
				m.formAPIEndpoint = baseURL
			}
		}
	}
	if m.formAPIEndpoint == "" && p.BaseURL != "" {
		m.formAPIEndpoint = p.BaseURL
	}

	// Find matching color
	m.formColorIndex = 0
	for i, c := range colorPresets {
		if c.hex == p.Color {
			m.formColorIndex = i
			break
		}
	}
}

func (m *ModelSettings) saveNewProvider() {
	// Generate provider name from display name
	var providerName string = strings.ToLower(strings.ReplaceAll(m.formDisplayName, " ", "_"))
	if providerName == "" {
		providerName = strings.ToLower(strings.ReplaceAll(m.formProviderType, " ", "_"))
	}
	if strings.EqualFold(m.formProviderType, "Plexus Gateway") {
		providerName = "plexus"
	}

	// Use display name or default to provider type
	displayName := m.formDisplayName
	if displayName == "" {
		displayName = m.formProviderType + " Provider"
	}

	// Determine type based on provider type preset
	var providerType string = coerceAuthType(m.formProviderType, m.formAuthType)
	var apiType string = resolveAPIType(m.formProviderType)
	// If the user picked the "OpenAI" preset for a custom provider (e.g. Z.AI, GLM),
	// the api_type should be "openai-compatible" not "openai" — "openai" is reserved
	// for the real OpenAI provider which uses Codex OAuth credentials.
	if apiType == "openai" && providerName != "openai" && providerName != "codex" {
		apiType = "openai-compatible"
	}

	newProvider := commands.Provider{
		Name:        providerName,
		DisplayName: displayName,
		Color:       colorPresets[m.formColorIndex].hex,
		Type:        providerType,
		APIType:     apiType,
		BaseURL:     m.formAPIEndpoint,
		Source:      "user",
		Models:      []commands.ModelInfo{},
		Available:   m.formAPIKey != "", // Available if API key provided
	}
	isPlexus := strings.EqualFold(m.formProviderType, "Plexus Gateway")
	var validatedPlexusModels []commands.ModelConfig
	if isPlexus {
		zero := 0
		newProvider.HTTPMaxRetries = &zero

		endpoint := strings.TrimRight(strings.TrimSpace(m.formAPIEndpoint), "/")
		key := strings.TrimSpace(m.formAPIKey)
		if endpoint == "" || key == "" {
			m.setRefreshStatus("Plexus requires a gateway URL and personal API key", true)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		models, err := commands.FetchModelsForProvider(ctx, "openai-compatible", endpoint, key)
		cancel()
		if err != nil {
			m.setRefreshStatus(fmt.Sprintf("Plexus validation failed: %v", err), true)
			return
		}
		if len(models) == 0 {
			m.setRefreshStatus("Plexus validation failed: this key has no authorized aliases", true)
			return
		}
		newProvider.BaseURL = endpoint
		validatedPlexusModels = models
	}
	m.providers = append(m.providers, newProvider)

	// Save to config file
	if m.configManager != nil {
		m.saveProvidersToConfig()

		if isPlexus {
			if err := m.persistPlexusProvider(newProvider.BaseURL, strings.TrimSpace(m.formAPIKey), "", validatedPlexusModels, true); err != nil {
				m.setRefreshStatus(fmt.Sprintf("Plexus was validated, but saving failed: %v", err), true)
				return
			}
			m.setRefreshStatus(fmt.Sprintf("Added %d authorized Plexus aliases", len(validatedPlexusModels)), false)
		} else {
			// Always persist credentials so URL-only providers (env-var auth) land
			// in the credentials store and stay consistent with providers.json.
			_ = m.configManager.SaveCredentials(providerName, m.formAPIKey, m.formAPIEndpoint)
			// Auto-fetch the model list so the user isn't left with an empty
			// provider they'd have to populate one model at a time. Best-effort:
			// a fetch failure (bad key, no /v1/models) is surfaced via the status
			// line but never blocks the save.
			m.autoFetchModelsForNewProvider(providerName, apiType)
		}
		m.reloadProviders(providerName)
		if m.onCredentialsChanged != nil {
			m.onCredentialsChanged(providerName)
		}
	}
}

// autoFetchModelsForNewProvider queries the provider's model-listing endpoint
// (via the shared commands.FetchModelsForProvider helper) and writes the result
// into providers.json for the just-added provider. It is best-effort: any error
// is reported on the refresh status line and the provider is left intact with an
// empty model list the user can fill manually. Only api-key providers with an
// endpoint + key are attempted — OAuth providers use their own refresh path.
func (m *ModelSettings) autoFetchModelsForNewProvider(providerName, apiType string) {
	if m.configManager == nil {
		return
	}
	if normalizeAuthType(m.formAuthType) == "oauth" {
		return // OAuth providers refresh via their dedicated buttons
	}
	if strings.TrimSpace(m.formAPIKey) == "" || strings.TrimSpace(m.formAPIEndpoint) == "" {
		return // nothing to authenticate/fetch with
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := commands.FetchModelsForProvider(ctx, apiType, m.formAPIEndpoint, m.formAPIKey)
	if err != nil {
		m.setRefreshStatus(fmt.Sprintf("Saved. Model fetch failed: %v", err), true)
		return
	}
	if len(models) == 0 {
		m.setRefreshStatus("Saved. Provider returned no models — add them manually.", true)
		return
	}
	providers, loadErr := m.configManager.LoadProviders()
	if loadErr != nil {
		return
	}
	for i := range providers {
		if strings.EqualFold(providers[i].Name, providerName) {
			providers[i].Models = models
			break
		}
	}
	if saveErr := m.configManager.SaveProviders(providers); saveErr != nil {
		m.setRefreshStatus(fmt.Sprintf("Saved, but storing models failed: %v", saveErr), true)
		return
	}
	m.setRefreshStatus(fmt.Sprintf("Added %d models for %s", len(models), providerName), false)
}

func (m *ModelSettings) saveExistingProvider(index int) {
	if index < 0 || index >= len(m.providers) {
		return
	}
	if strings.EqualFold(m.providers[index].Name, plexusProviderName) {
		return
	}

	prov := m.providers[index]
	var providerType string = coerceAuthType(m.formProviderType, m.formAuthType)
	var apiType string = resolveAPIType(m.formProviderType)
	// Same guard as saveNewProvider: custom providers that use the OpenAI preset
	// must not be stored as api_type="openai" or they'll try to use OPENAI_API_KEY.
	if apiType == "openai" && strings.ToLower(prov.Name) != "openai" && strings.ToLower(prov.Name) != "codex" {
		apiType = "openai-compatible"
	}

	// Keep original provider name for references; update auth type, display, and color
	prov.DisplayName = m.formDisplayName
	prov.Color = colorPresets[m.formColorIndex].hex
	prov.Type = providerType
	prov.APIType = apiType
	prov.BaseURL = m.formAPIEndpoint
	if strings.EqualFold(prov.Source, "cloud") {
		prov.Source = "user"
	}
	// Only mark available if API key provided OR if it's an OAuth provider with existing credentials
	if m.formAPIKey != "" {
		prov.Available = true
	}
	m.providers[index] = prov

	if m.configManager != nil {
		m.saveProvidersToConfig()

		// Persist credentials unconditionally so users can clear keys/URLs and
		// so URL-only edits land in the credentials store consistently.
		_ = m.configManager.SaveCredentials(prov.Name, m.formAPIKey, m.formAPIEndpoint)
		m.reloadProviders(prov.Name)
		if m.onCredentialsChanged != nil {
			m.onCredentialsChanged(prov.Name)
		}
	}
}

// handleOverrideKey handles keyboard input when the override confirmation is shown
func (m *ModelSettings) handleOverrideKey(key string) bool {
	switch key {
	case "left", "h":
		m.confirmSelected = (m.confirmSelected - 1 + 3) % 3
		return true
	case "right", "l":
		m.confirmSelected = (m.confirmSelected + 1) % 3
		return true
	case "o", "O": // Quick key for Override
		m.applyModel(m.pendingProvider, m.pendingModel, false)
		return true
	case "s", "S": // Quick key for Save to Profile
		m.applyModel(m.pendingProvider, m.pendingModel, true)
		return true
	case "n", "N", "esc", "c", "C": // Quick key for Cancel
		m.confirmOverride = false
		return true
	case "enter":
		if m.confirmSelected == 1 {
			m.applyModel(m.pendingProvider, m.pendingModel, false)
		} else if m.confirmSelected == 2 {
			m.applyModel(m.pendingProvider, m.pendingModel, true)
		} else {
			m.confirmOverride = false
		}
		return true
	}
	return false
}
