package commands

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// Color constants for UI
const (
	ColorWhite    = palette.Text
	ColorCyan     = palette.Teal
	ColorGray     = palette.TextMuted
	ColorDarkGray = palette.Border
	ColorGreen    = palette.Success
	ColorYellow   = palette.Warning
	ColorOrange   = palette.Warning
	ColorRed      = palette.Error
	ColorPurple   = palette.AccentSoft
	ColorBlue     = palette.Info
	ColorSurface  = palette.Surface
	ColorPanel    = palette.Panel
	ColorPanelAlt = palette.PanelAlt
	ColorBorder   = palette.Border
	ColorMuted    = palette.TextDim
	ColorAccent   = palette.Accent
	ColorAccent2  = palette.AccentDim
	ColorSuccess  = palette.Success
)

// Provider represents an AI provider with rich metadata
type Provider struct {
	Name            string
	DisplayName     string
	Color           string
	Type            string // "oauth" or "api_key"
	APIType         string
	BaseURL         string
	HTTPMaxRetries  *int
	APIKeySecretRef string
	Source          string
	Models          []ModelInfo
	Available       bool
}

// ModelInfo represents a model with metadata
type ModelInfo struct {
	ID                      string
	DisplayName             string
	Context                 string
	ContextWindow           int
	Description             string
	SupportsReasoningEffort *bool
	ReasoningEfforts        []string
	Temperature             *float64
	MaxTokens               int
	TopP                    *float64
	TopK                    *int
	Diffusion               bool // text-diffusion model (denoising reveal rendering)
}

// ModelInfoFromConfig converts persistent model data to the picker/settings
// representation without collapsing explicit-zero generation overrides.
func ModelInfoFromConfig(config ModelConfig) ModelInfo {
	return ModelInfo{
		ID:                      config.ID,
		DisplayName:             config.DisplayName,
		Context:                 config.Context,
		ContextWindow:           config.ContextWindow,
		Description:             config.Description,
		SupportsReasoningEffort: config.SupportsReasoningEffort,
		ReasoningEfforts:        config.ReasoningEfforts,
		Temperature:             config.Temperature,
		MaxTokens:               config.MaxTokens,
		TopP:                    config.TopP,
		TopK:                    config.TopK,
		Diffusion:               config.Diffusion,
	}
}

// ApplyToConfig overlays editable ModelInfo fields on an existing config. The
// base preserves catalog metadata and provider-specific reasoning fields.
func (model ModelInfo) ApplyToConfig(base ModelConfig) ModelConfig {
	base.ID = model.ID
	base.DisplayName = model.DisplayName
	base.Context = model.Context
	base.ContextWindow = model.ContextWindow
	base.Description = model.Description
	base.SupportsReasoningEffort = model.SupportsReasoningEffort
	base.ReasoningEfforts = model.ReasoningEfforts
	base.Temperature = model.Temperature
	base.MaxTokens = model.MaxTokens
	base.TopP = model.TopP
	base.TopK = model.TopK
	base.Diffusion = model.Diffusion
	return base
}

// ProviderCatalogRefreshedMsg reports completion of a background provider
// catalog refresh. Errors preserve the previous cached catalog.
type ProviderCatalogRefreshedMsg struct {
	Provider string
	Err      error
}

// ModelCommand implements the /model command with main menu
type ModelCommand struct {
	interactive      bool
	state            string // "menu", "provider", "provider_models", "model", "alias_variants", "agent"
	selectedMenu     int    // Which main menu option is selected
	selectedProvider int
	selectedModel    int
	selectedAlias    int
	selectedVariant  int
	scrollOffset     int // For scrolling in long lists
	maxVisible       int // Max items visible at once (set based on height)
	width            int
	height           int
	searchQuery      string
	searchActive     bool
	tabIndex         int
	filterProvider   string
	filterContext    int
	filterTags       map[ModelTag]bool
	collapsedGroups  map[string]bool
	favoriteModels   []ModelRef
	recentModels     []ModelRef
	readmeCache      map[string]ModelReadmeEntry
	linkTargets      map[string]string
	providers        []Provider                                                        // Loaded from config
	aliasEntries     []ModelAliasEntry                                                 // Model aliases from catalog
	currentProvider  string                                                            // Current active provider
	currentModel     string                                                            // Current active model
	configManager    *ConfigManager                                                    // Config manager
	beforeOpen       func() tea.Cmd                                                    // Async refresh hook invoked before loading config
	onSelect         func(provider, model, providerDisplay, modelDisplay string) error // Callback when model is selected
}

// Menu options
const (
	MenuChooseModel    = 0
	MenuChooseProvider = 1
	MenuAgentConfig    = 2
)

func menuOptions() []string {
	return []string{
		i18n.T("commands_b.model.menu.choose_model"),
		i18n.T("commands_b.model.menu.choose_provider"),
		i18n.T("commands_b.model.menu.agent_config"),
	}
}

const (
	TabFastCheap = iota
	TabLongContext
	TabCoding
	TabVision
	TabAll
)

const (
	ContextFilterAny = iota
	ContextFilter32k
	ContextFilter128k
	ContextFilter1M
)

var modelTabs = []struct {
	label      string
	contextMin int
	tags       []ModelTag
}{
	{label: "commands_b.model.tab_fast_cheap", tags: []ModelTag{TagFast}},
	{label: "commands_b.model.tab_long_context", contextMin: 128000},
	{label: "commands_b.model.tab_coding", tags: []ModelTag{TagCoding}},
	{label: "commands_b.model.tag_vision", tags: []ModelTag{TagVision}},
	{label: "commands_b.model.tab_all"},
}

// NewModelCommand creates a new /model command
func NewModelCommand() *ModelCommand {
	cm, err := NewConfigManager()
	if err != nil {
		// Fallback to basic initialization
		return &ModelCommand{
			interactive:  false,
			state:        "menu",
			selectedMenu: 0,
			scrollOffset: 0,
			maxVisible:   10,
			width:        60,
			height:       20,
			providers:    []Provider{},
			readmeCache:  make(map[string]ModelReadmeEntry),
		}
	}

	// Load providers from config
	providers := loadProvidersFromConfig()
	aliasEntries := LoadModelAliases(providers)

	// Load current model from config
	config, err := cm.LoadConfig()
	if err != nil {
		config = &SwarmOSConfig{
			CurrentProvider: "ClaudeCode",
			CurrentModel:    "claude-opus-4-20250514",
		}
	}
	var favorites []ModelRef = config.FavoriteModels
	var recents []ModelRef = config.RecentModels
	var readmeCache map[string]ModelReadmeEntry = make(map[string]ModelReadmeEntry)

	return &ModelCommand{
		interactive:      false,
		state:            "menu",
		selectedMenu:     0,
		selectedProvider: 0,
		selectedModel:    0,
		scrollOffset:     0,
		maxVisible:       10,
		width:            60,
		height:           20,
		tabIndex:         TabAll,
		filterContext:    ContextFilterAny,
		filterTags:       make(map[ModelTag]bool),
		collapsedGroups:  make(map[string]bool),
		readmeCache:      readmeCache,
		providers:        providers,
		aliasEntries:     aliasEntries,
		currentProvider:  config.CurrentProvider,
		currentModel:     config.CurrentModel,
		favoriteModels:   favorites,
		recentModels:     recents,
		configManager:    cm,
	}
}

// Name returns the command name
func (m *ModelCommand) Name() string {
	return "model"
}

// Description returns command description
func (m *ModelCommand) Description() string {
	return i18n.T("commands_b.model.description")
}

// Aliases returns command aliases
func (m *ModelCommand) Aliases() []string {
	return []string{"models", "m"}
}

// Execute activates the model picker
func (m *ModelCommand) Execute(args []string) tea.Cmd {
	var refreshCmd tea.Cmd
	if m.beforeOpen != nil {
		refreshCmd = m.beforeOpen()
	}
	// Load the cached catalog immediately; the async refresh message reloads it
	// again without blocking keyboard input or rendering.
	m.refreshFromConfig()
	m.interactive = true
	m.state = "menu"
	m.selectedMenu = 0
	return refreshCmd
}

// IsInteractive returns true (this command has interactive UI)
func (m *ModelCommand) IsInteractive() bool {
	return m.interactive
}

// SetCurrent updates the active provider/model (used by app callbacks).
func (m *ModelCommand) SetCurrent(provider, model string) {
	if provider != "" {
		m.currentProvider = provider
	}
	if model != "" {
		m.currentModel = model
	}
}

// refreshFromConfig reloads providers and current selection from disk.
func (m *ModelCommand) refreshFromConfig() {
	if m.configManager == nil {
		return
	}

	// Reload providers (migration/availability handled in ConfigManager)
	m.providers = loadProvidersFromConfig()
	m.aliasEntries = LoadModelAliases(m.providers)

	if cfg, err := m.configManager.LoadConfig(); err == nil && cfg != nil {
		m.currentProvider = cfg.CurrentProvider
		m.currentModel = cfg.CurrentModel
		m.favoriteModels = cfg.FavoriteModels
		m.recentModels = cfg.RecentModels
	}

	// Try to align selection indices with current provider/model
	m.selectedProvider = 0
	for i, p := range m.providers {
		if strings.EqualFold(p.Name, m.currentProvider) {
			m.selectedProvider = i
			break
		}
	}
	m.selectedModel = 0
	if m.selectedProvider < len(m.providers) {
		for i, mdl := range m.providers[m.selectedProvider].Models {
			if mdl.ID == m.currentModel {
				m.selectedModel = i
				break
			}
		}
	}
}

// SetOnSelect sets the callback for when a model is selected
// Callback receives: providerID, modelID, providerDisplayName, modelDisplayName
func (m *ModelCommand) SetOnSelect(fn func(provider, model, providerDisplay, modelDisplay string) error) {
	m.onSelect = fn
}

// SetBeforeOpen sets a lightweight refresh hook that runs before providers are
// reloaded for /model or Ctrl+M.
func (m *ModelCommand) SetBeforeOpen(fn func() tea.Cmd) {
	m.beforeOpen = fn
}

func (m *ModelCommand) modelSelectionUsesAliases() bool {
	return len(m.aliasEntries) > 0
}

func (m *ModelCommand) applySelection(provider string, model string, providerDisplay string, modelDisplay string) error {
	var previousConfig *SwarmOSConfig
	var candidateConfig *SwarmOSConfig
	if m.configManager != nil {
		var err error
		previousConfig, err = m.configManager.LoadConfig()
		if err != nil {
			return err
		}
		candidateConfig, err = m.configManager.UpdateCurrentModel(provider, model)
		if err != nil {
			return err
		}
	}

	if m.onSelect != nil {
		if err := m.onSelect(provider, model, providerDisplay, modelDisplay); err != nil {
			if m.configManager != nil && previousConfig != nil {
				if rollbackErr := m.configManager.SaveConfig(previousConfig); rollbackErr != nil {
					return fmt.Errorf("%w (config rollback failed: %v)", err, rollbackErr)
				}
			}
			return err
		}
	}

	m.currentProvider = provider
	m.currentModel = model
	if candidateConfig != nil {
		m.favoriteModels = candidateConfig.FavoriteModels
		m.recentModels = candidateConfig.RecentModels
	}
	m.state = "menu"
	m.selectedModel = 0
	m.selectedVariant = 0
	m.scrollOffset = 0
	m.searchQuery = ""
	m.searchActive = false
	m.interactive = false
	return nil
}

// Select applies a provider/model choice through the same transactional path
// used by the interactive picker.
func (m *ModelCommand) Select(provider, model, providerDisplay, modelDisplay string) error {
	return m.applySelection(provider, model, providerDisplay, modelDisplay)
}

// CurrentSelection returns the command's committed provider/model selection.
func (m *ModelCommand) CurrentSelection() (string, string) {
	return m.currentProvider, m.currentModel
}

// Update handles user input
func (m *ModelCommand) Update(msg tea.Msg) (updated Command, cmd tea.Cmd) {
	updated = m
	if refresh, ok := msg.(ProviderCatalogRefreshedMsg); ok {
		if refresh.Err == nil {
			m.refreshFromConfig()
		}
		return updated, nil
	}
	if !m.interactive {
		return updated, nil
	}

	var prevState string = m.state
	defer func() {
		if m.state != prevState {
			m.searchQuery = ""
			m.searchActive = false
		}
		var readmeCmd tea.Cmd = m.ensureReadmeCmd()
		if readmeCmd != nil {
			if cmd != nil {
				cmd = tea.Batch(cmd, readmeCmd)
			} else {
				cmd = readmeCmd
			}
		}
	}()

	switch msg := msg.(type) {
	case ModelReadmeMsg:
		m.applyReadmeMsg(msg)
	case tea.KeyMsg:
		key := msg.String()
		if m.searchEnabled() && !m.searchActive && (key == "/" || key == "ctrl+f") {
			m.searchActive = true
			return m, nil
		}
		if m.searchEnabled() && m.searchActive && (key == "j" || key == "k") {
			if m.handleSearchInput(key) {
				return m, nil
			}
		}

		switch key {
		case "esc":
			if m.searchEnabled() && m.searchActive {
				m.searchActive = false
				return m, nil
			}
			if m.state == "menu" {
				m.interactive = false
			} else {
				// Go back to main menu
				m.state = "menu"
			}
			return m, nil

		case "down", "j":
			switch m.state {
			case "menu":
				if m.selectedMenu < len(menuOptions())-1 {
					m.selectedMenu++
				}
			case "provider":
				var results listResults = m.providerResults()
				m.moveSelection(results.Indices, &m.selectedProvider, 1)
			case "provider_models":
				// Models for selected provider only
				var results groupedResults = m.providerModelResults(m.selectedProvider)
				m.moveSelection(results.Indices, &m.selectedModel, 1)
			case "model":
				if m.modelSelectionUsesAliases() {
					var results listResults = m.aliasResults()
					m.moveSelection(results.Indices, &m.selectedModel, 1)
				} else {
					// Get all models from all providers
					var allModels []AllModelEntry = m.getAllModels()
					var results groupedResults = m.allModelResults(allModels)
					m.moveSelection(results.Indices, &m.selectedModel, 1)
				}
			case "alias_variants":
				var results groupedResults = m.aliasVariantResults(m.selectedAlias)
				m.moveSelection(results.Indices, &m.selectedVariant, 1)
			}

		case "up", "k":
			switch m.state {
			case "menu":
				if m.selectedMenu > 0 {
					m.selectedMenu--
				}
			case "provider":
				var results listResults = m.providerResults()
				m.moveSelection(results.Indices, &m.selectedProvider, -1)
			case "provider_models":
				var results groupedResults = m.providerModelResults(m.selectedProvider)
				m.moveSelection(results.Indices, &m.selectedModel, -1)
			case "model":
				if m.modelSelectionUsesAliases() {
					var results listResults = m.aliasResults()
					m.moveSelection(results.Indices, &m.selectedModel, -1)
				} else {
					var allModels []AllModelEntry = m.getAllModels()
					var results groupedResults = m.allModelResults(allModels)
					m.moveSelection(results.Indices, &m.selectedModel, -1)
				}
			case "alias_variants":
				var results groupedResults = m.aliasVariantResults(m.selectedAlias)
				m.moveSelection(results.Indices, &m.selectedVariant, -1)
			}

		case "enter":
			switch m.state {
			case "menu":
				// Handle menu selection
				switch m.selectedMenu {
				case MenuChooseModel:
					m.state = "model"
					m.selectedModel = 0
					m.scrollOffset = 0
				case MenuChooseProvider:
					m.state = "provider"
					m.selectedProvider = 0
					m.scrollOffset = 0
				case MenuAgentConfig:
					m.state = "agent"
					// TODO: Implement agent config
				}

			case "provider":
				// Provider selected - show models for this provider
				var results listResults = m.providerResults()
				if len(results.Indices) == 0 {
					return m, nil
				}
				m.ensureSelection(results.Indices, &m.selectedProvider)
				m.state = "provider_models"
				m.selectedModel = 0
				m.scrollOffset = 0

			case "provider_models":
				// Model selected from provider's models
				providerData := m.providers[m.selectedProvider]
				if len(providerData.Models) == 0 {
					// No models – return to provider list safely
					m.state = "provider"
					m.selectedModel = 0
					m.scrollOffset = 0
					return m, nil
				}
				var results groupedResults = m.providerModelResults(m.selectedProvider)
				if len(results.Indices) == 0 {
					return m, nil
				}
				m.ensureSelection(results.Indices, &m.selectedModel)
				modelData := providerData.Models[m.selectedModel]
				provider := providerData.Name
				model := modelData.ID
				providerDisplay := providerData.DisplayName
				modelDisplay := modelData.DisplayName

				m.applySelection(provider, model, providerDisplay, modelDisplay)

			case "model":
				if m.modelSelectionUsesAliases() {
					if len(m.aliasEntries) == 0 {
						m.state = "menu"
						return m, nil
					}
					var results listResults = m.aliasResults()
					if len(results.Indices) == 0 {
						return m, nil
					}
					m.ensureSelection(results.Indices, &m.selectedModel)
					m.selectedAlias = m.selectedModel
					var alias ModelAliasEntry = m.aliasEntries[m.selectedAlias]
					if len(alias.Variants) == 1 {
						var variant ModelAliasVariant = alias.Variants[0]
						m.applySelection(variant.ProviderName, variant.Model.ID, variant.ProviderDisplayName, variant.Model.DisplayName)
						return m, nil
					}
					m.state = "alias_variants"
					m.selectedVariant = 0
					m.scrollOffset = 0
					return m, nil
				}

				// Model selected from unified list
				var allModels []AllModelEntry = m.getAllModels()
				if len(allModels) == 0 {
					m.state = "menu"
					return m, nil
				}
				var results groupedResults = m.allModelResults(allModels)
				if len(results.Indices) == 0 {
					return m, nil
				}
				m.ensureSelection(results.Indices, &m.selectedModel)

				modelEntry := allModels[m.selectedModel]
				provider := modelEntry.ProviderName
				model := modelEntry.Model.ID
				providerDisplay := modelEntry.Provider.DisplayName
				modelDisplay := modelEntry.Model.DisplayName

				m.applySelection(provider, model, providerDisplay, modelDisplay)

			case "alias_variants":
				if m.selectedAlias >= len(m.aliasEntries) {
					m.state = "model"
					m.selectedVariant = 0
					m.scrollOffset = 0
					return m, nil
				}
				var variants []ModelAliasVariant = m.aliasEntries[m.selectedAlias].Variants
				if len(variants) == 0 {
					m.state = "model"
					m.selectedVariant = 0
					m.scrollOffset = 0
					return m, nil
				}
				var results groupedResults = m.aliasVariantResults(m.selectedAlias)
				if len(results.Indices) == 0 {
					return m, nil
				}
				m.ensureSelection(results.Indices, &m.selectedVariant)
				var variant ModelAliasVariant = variants[m.selectedVariant]
				m.applySelection(variant.ProviderName, variant.Model.ID, variant.ProviderDisplayName, variant.Model.DisplayName)
			}

		case "1", "2", "3", "4", "5":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			if m.tabsEnabled() {
				var idx int
				switch key {
				case "1":
					idx = 0
				case "2":
					idx = 1
				case "3":
					idx = 2
				case "4":
					idx = 3
				case "5":
					idx = 4
				}
				m.setTabIndex(idx)
			}

		case "p":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			if m.tabsEnabled() {
				m.cycleProviderFilter()
			}

		case "c":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			if m.tabsEnabled() {
				m.cycleContextFilter()
			}

		case "s":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			if m.tabsEnabled() {
				m.toggleTagFilter(TagFast)
			}

		case "K":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			if m.tabsEnabled() {
				m.toggleTagFilter(TagCoding)
			}

		case "v":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			if m.tabsEnabled() {
				m.toggleTagFilter(TagVision)
			}

		case "t":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			if m.tabsEnabled() {
				m.toggleTagFilter(TagTools)
			}

		case "f":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			var provider string
			var model string
			var ok bool
			provider, model, ok = m.selectedModelRef()
			if ok {
				_ = m.toggleFavorite(provider, model)
				m.scrollOffset = 0
				m.syncSelectionToSearch()
			}

		case "g":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			var results groupedResults
			var ok bool
			results, ok = m.groupedResultsForState()
			if ok {
				var key string = groupKeyForSelection(results.Groups, m.selectedModel)
				if key == "" && m.state == "alias_variants" {
					key = groupKeyForSelection(results.Groups, m.selectedVariant)
				}
				if key != "" {
					m.collapsedGroups[key] = !m.collapsedGroups[key]
					m.syncSelectionToSearch()
				}
			}

		case "G":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			var results groupedResults
			var ok bool
			results, ok = m.groupedResultsForState()
			if ok {
				m.setGroupsCollapsed(results.Groups, true)
				m.syncSelectionToSearch()
			}

		case "E":
			if m.searchActive {
				if m.handleSearchInput(key) {
					return m, nil
				}
				return m, nil
			}
			var results groupedResults
			var ok bool
			results, ok = m.groupedResultsForState()
			if ok {
				m.setGroupsCollapsed(results.Groups, false)
				m.syncSelectionToSearch()
			}

		case "backspace":
			if m.searchActive {
				if m.searchQuery != "" {
					m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
					m.scrollOffset = 0
					m.syncSelectionToSearch()
				} else {
					m.searchActive = false
				}
				return m, nil
			}
			if m.searchEnabled() && m.searchQuery != "" {
				m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
				m.scrollOffset = 0
				m.syncSelectionToSearch()
				return m, nil
			}
			if m.state != "menu" {
				m.state = "menu"
			}
		case "tab":
			if m.searchEnabled() && m.searchQuery != "" {
				if m.autocompleteSearch() {
					return m, nil
				}
			}
			if m.state != "menu" {
				m.state = "menu"
			}
		case "ctrl+u":
			if m.searchEnabled() && m.searchQuery != "" {
				m.searchQuery = ""
				m.scrollOffset = 0
				m.syncSelectionToSearch()
				return m, nil
			}
		default:
			if m.handleSearchInput(msg.String()) {
				return m, nil
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Calculate max visible items based on height
		// Reserve space for header, filters, hints, borders (about 12 lines)
		// Each model card takes 2 lines (name + context), so divide by 2
		m.maxVisible = max((msg.Height-12)/2, 4)
	}

	return updated, cmd
}

// AllModelEntry combines provider and model for cross-provider display
type AllModelEntry struct {
	ProviderName string
	Provider     Provider
	Model        ModelInfo
}

type listResults struct {
	Indices    []int
	Scores     map[int]int
	Highlights map[int][]int
}

type modelGroup struct {
	Key       string
	Title     string
	Indices   []int
	Collapsed bool
}

type groupedResults struct {
	Groups     []modelGroup
	Indices    []int
	Scores     map[int]int
	Highlights map[int][]int
}

func indexOfInt(values []int, needle int) int {
	for i, value := range values {
		if value == needle {
			return i
		}
	}
	return -1
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func (m *ModelCommand) adjustScroll(pos int, total int) {
	if m.maxVisible <= 0 {
		m.scrollOffset = 0
		return
	}
	if total <= m.maxVisible {
		m.scrollOffset = 0
		return
	}
	if pos < m.scrollOffset {
		m.scrollOffset = pos
	} else if pos >= m.scrollOffset+m.maxVisible {
		m.scrollOffset = pos - m.maxVisible + 1
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	if m.scrollOffset > total-m.maxVisible {
		m.scrollOffset = total - m.maxVisible
	}
}

func (m *ModelCommand) moveSelection(filtered []int, current *int, delta int) {
	if len(filtered) == 0 {
		return
	}
	pos := indexOfInt(filtered, *current)
	if pos == -1 {
		pos = 0
	}
	pos += delta
	if pos < 0 {
		pos = 0
	} else if pos >= len(filtered) {
		pos = len(filtered) - 1
	}
	*current = filtered[pos]
	m.adjustScroll(pos, len(filtered))
}

func (m *ModelCommand) ensureSelection(filtered []int, current *int) {
	if len(filtered) == 0 {
		return
	}
	pos := indexOfInt(filtered, *current)
	if pos == -1 {
		*current = filtered[0]
		pos = 0
	}
	m.adjustScroll(pos, len(filtered))
}

func (m *ModelCommand) syncSelectionToSearch() {
	if !m.searchEnabled() {
		return
	}

	switch m.state {
	case "provider":
		var results listResults = m.providerResults()
		m.ensureSelection(results.Indices, &m.selectedProvider)
	case "provider_models":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		m.ensureSelection(results.Indices, &m.selectedModel)
	case "model":
		if m.modelSelectionUsesAliases() {
			var results listResults = m.aliasResults()
			m.ensureSelection(results.Indices, &m.selectedModel)
		} else {
			var allModels []AllModelEntry = m.getAllModels()
			var results groupedResults = m.allModelResults(allModels)
			m.ensureSelection(results.Indices, &m.selectedModel)
		}
	case "alias_variants":
		if m.selectedAlias >= 0 && m.selectedAlias < len(m.aliasEntries) {
			var results groupedResults = m.aliasVariantResults(m.selectedAlias)
			m.ensureSelection(results.Indices, &m.selectedVariant)
		}
	}
}

func (m *ModelCommand) handleSearchInput(key string) bool {
	if !m.searchEnabled() {
		return false
	}
	if !m.searchActive {
		return false
	}
	if len(key) != 1 {
		return false
	}
	if !unicode.IsPrint(rune(key[0])) {
		return false
	}
	m.searchQuery += key
	m.scrollOffset = 0
	m.syncSelectionToSearch()
	return true
}

func (m *ModelCommand) autocompleteSearch() bool {
	if !m.searchEnabled() {
		return false
	}

	switch m.state {
	case "provider":
		var results listResults = m.providerResults()
		if len(results.Indices) == 0 {
			return false
		}
		var idx int = m.selectedProvider
		if indexOfInt(results.Indices, idx) == -1 {
			idx = results.Indices[0]
		}
		prov := m.providers[idx]
		if prov.DisplayName != "" {
			m.searchQuery = prov.DisplayName
		} else {
			m.searchQuery = prov.Name
		}
	case "provider_models":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) == 0 {
			return false
		}
		var idx int = m.selectedModel
		if indexOfInt(results.Indices, idx) == -1 {
			idx = results.Indices[0]
		}
		model := m.providers[m.selectedProvider].Models[idx]
		if model.DisplayName != "" {
			m.searchQuery = model.DisplayName
		} else {
			m.searchQuery = model.ID
		}
	case "model":
		if m.modelSelectionUsesAliases() {
			var results listResults = m.aliasResults()
			if len(results.Indices) == 0 {
				return false
			}
			var idx int = m.selectedModel
			if indexOfInt(results.Indices, idx) == -1 {
				idx = results.Indices[0]
			}
			alias := m.aliasEntries[idx]
			if alias.DisplayName != "" {
				m.searchQuery = alias.DisplayName
			} else {
				m.searchQuery = alias.Name
			}
			break
		}
		var allModels []AllModelEntry = m.getAllModels()
		var results groupedResults = m.allModelResults(allModels)
		if len(results.Indices) == 0 {
			return false
		}
		var idx int = m.selectedModel
		if indexOfInt(results.Indices, idx) == -1 {
			idx = results.Indices[0]
		}
		entry := allModels[idx]
		if entry.Model.DisplayName != "" {
			m.searchQuery = entry.Model.DisplayName
		} else {
			m.searchQuery = entry.Model.ID
		}
	case "alias_variants":
		if m.selectedAlias < 0 || m.selectedAlias >= len(m.aliasEntries) {
			return false
		}
		var variants []ModelAliasVariant = m.aliasEntries[m.selectedAlias].Variants
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		if len(results.Indices) == 0 {
			return false
		}
		var idx int = m.selectedVariant
		if indexOfInt(results.Indices, idx) == -1 {
			idx = results.Indices[0]
		}
		variant := variants[idx]
		label := variant.ProviderDisplayName
		if label == "" {
			label = variant.ProviderName
		}
		m.searchQuery = label
	}

	m.scrollOffset = 0
	m.syncSelectionToSearch()
	return true
}

func (m *ModelCommand) renderSearchLine() string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Bold(true)
	label := labelStyle.Render(i18n.T("commands_b.model.search_label"))

	queryStyle := lipgloss.NewStyle()
	if m.searchQuery == "" {
		queryStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorGray)).
			Italic(true)
		placeholder := i18n.T("commands_b.model.search_hint")
		if m.searchActive {
			placeholder = "|"
			queryStyle = lipgloss.NewStyle()
		}
		return lipgloss.JoinHorizontal(lipgloss.Left, label, " ", queryStyle.Render(placeholder))
	}

	cursor := ""
	if m.searchActive {
		cursor = "|"
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, label, " ", queryStyle.Render(m.searchQuery+cursor))
}

func (m *ModelCommand) renderSearchBar(width int) string {
	content := m.searchQuery
	if content == "" {
		content = i18n.T("commands_b.model.search_placeholder")
		if m.searchActive {
			content = ""
		}
	}
	if m.searchActive {
		content = content + "|"
	}

	fieldStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Background(lipgloss.Color(ColorPanel)).
		Padding(0, 1)
	if m.searchQuery == "" {
		fieldStyle = fieldStyle.Foreground(lipgloss.Color(ColorMuted))
	}
	if width > 0 {
		fieldStyle = fieldStyle.Width(width)
	}
	return fieldStyle.Render(content)
}

func renderToggleChip(label string, active bool, accent string) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Background(lipgloss.Color(ColorPanelAlt)).
		Padding(0, 1)
	if active {
		style = style.
			Foreground(lipgloss.Color(ColorWhite)).
			Background(lipgloss.Color(accent)).
			Bold(true)
	}
	return style.Render(label)
}

func renderNeutralChip(label string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Background(lipgloss.Color(ColorPanelAlt)).
		Padding(0, 1).
		Render(label)
}

func joinLeftRight(width int, left string, right string) string {
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	if rightWidth == 0 {
		return left
	}
	// Use inline max() for zero overhead bounds checking
	gap := max(0, width-leftWidth-rightWidth)
	if gap < 1 {
		// Don't silently drop content - instead wrap to next line or truncate right side
		// Truncate right to fit if needed
		if leftWidth+1 < width {
			maxRightWidth := max(0, width-leftWidth-1)
			if maxRightWidth > 3 {
				// Use ansi.Truncate to preserve ANSI escape sequences
				if ansi.StringWidth(right) > maxRightWidth-3 {
					right = ansi.Truncate(right, maxRightWidth-3, "...")
				}
			}
			return left + " " + right
		}
		// If even left side is too wide, just return it
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func renderHeaderRow(width int, left string, right string) string {
	if left == "" && right == "" {
		return ""
	}
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if lipgloss.Width(left)+lipgloss.Width(right)+1 > width {
		return lipgloss.JoinVertical(lipgloss.Left, left, right)
	}
	return joinLeftRight(width, left, right)
}

func trimLabelWithHighlights(label string, highlights []int, maxWidth int) (string, []int) {
	if maxWidth <= 0 {
		return "", nil
	}
	runes := []rune(label)
	if len(runes) <= maxWidth {
		return label, highlights
	}
	if maxWidth <= 3 {
		// Use inline bounds checking to prevent out-of-bounds during resize
		endIdx := min(maxWidth, len(runes))
		trimmed := string(runes[:endIdx])
		return trimmed, filterHighlightIndices(highlights, len([]rune(trimmed)))
	}
	// Use inline bounds checking to prevent negative index issues during resize
	endIdx := min(max(0, maxWidth-3), len(runes))
	trimmed := string(runes[:endIdx]) + "..."
	return trimmed, filterHighlightIndices(highlights, len([]rune(trimmed)))
}

func filterHighlightIndices(indices []int, max int) []int {
	if max <= 0 || len(indices) == 0 {
		return nil
	}
	filtered := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx >= 0 && idx < max {
			filtered = append(filtered, idx)
		}
	}
	return filtered
}

func renderTagChips(tags map[ModelTag]bool) string {
	if len(tags) == 0 {
		return ""
	}
	type tagInfo struct {
		tag   ModelTag
		label string
		color string
	}
	ordered := []tagInfo{
		{TagTools, i18n.T("commands_b.model.tag_tools"), palette.Teal},
		{TagCoding, i18n.T("commands_b.model.tag_code"), palette.Info},
		{TagVision, i18n.T("commands_b.model.tag_vision"), palette.Success},
		{TagFast, i18n.T("commands_b.model.tag_fast"), palette.Warning},
		{TagLongContext, i18n.T("commands_b.model.tag_long"), palette.AccentSoft},
	}
	var chips []string
	for _, info := range ordered {
		if !tags[info.tag] {
			continue
		}
		chips = append(chips, renderToggleChip(info.label, true, info.color))
	}
	return strings.Join(chips, " ")
}

func (m *ModelCommand) renderStatusChips(provider string, model string) string {
	var chips []string
	if provider == m.currentProvider && model == m.currentModel {
		chips = append(chips, renderToggleChip(i18n.T("commands_b.model.active"), true, ColorSuccess))
	}
	if m.isFavorite(provider, model) {
		chips = append(chips, renderToggleChip(i18n.T("commands_b.model.favorite"), true, ColorYellow))
	}
	if m.isRecent(provider, model) {
		chips = append(chips, renderToggleChip(i18n.T("commands_b.model.recent"), true, ColorBlue))
	}
	if len(chips) == 0 {
		return ""
	}
	return strings.Join(chips, " ")
}

func (m *ModelCommand) renderToolbar(width int) string {
	if m.filterTags == nil {
		m.filterTags = make(map[ModelTag]bool)
	}
	toggles := []string{
		renderToggleChip(i18n.T("commands_b.model.tag_fast"), m.filterTags[TagFast], palette.Warning),
		renderToggleChip(i18n.T("commands_b.model.tag_code"), m.filterTags[TagCoding], palette.Info),
		renderToggleChip(i18n.T("commands_b.model.tag_vision"), m.filterTags[TagVision], palette.Success),
		renderToggleChip(i18n.T("commands_b.model.tag_tools"), m.filterTags[TagTools], palette.Teal),
	}
	toggleLine := strings.Join(toggles, " ")
	toggleWidth := lipgloss.Width(toggleLine)
	searchWidth := max(20, width-toggleWidth-2)
	if searchWidth < 20 {
		searchWidth = width
	}
	searchBar := m.renderSearchBar(searchWidth)

	if searchWidth == width {
		return lipgloss.JoinVertical(lipgloss.Left, searchBar, toggleLine)
	}
	return joinLeftRight(width, searchBar, toggleLine)
}

func (m *ModelCommand) renderTabChips() string {
	if !m.tabsEnabled() {
		return ""
	}
	var parts []string
	for i, tab := range modelTabs {
		active := m.normalizedTabIndex() == i
		parts = append(parts, renderToggleChip(i18n.T(tab.label), active, ColorAccent))
	}
	return strings.Join(parts, " ")
}

func (m *ModelCommand) renderFilterChips(showProvider bool) string {
	var parts []string
	if strings.TrimSpace(m.searchQuery) != "" {
		parts = append(parts, renderNeutralChip(i18n.T("commands_b.model.sort_best_match")))
	} else {
		parts = append(parts, renderNeutralChip(i18n.T("commands_b.model.sort_catalog")))
	}
	if showProvider {
		label := i18n.T("commands_b.model.provider_all")
		if m.filterProvider != "" {
			label = i18n.T("commands_b.model.provider_filter", m.providerDisplayName(m.filterProvider))
		}
		parts = append(parts, renderToggleChip(label, m.filterProvider != "", ColorAccent))
	}
	_, _, contextLabel := m.contextFilterRange()
	ctxLabel := i18n.T("commands_b.model.context_filter", contextLabel)
	parts = append(parts, renderToggleChip(ctxLabel, m.filterContext != ContextFilterAny, ColorAccent))
	return strings.Join(parts, " ")
}

func highlightText(text string, indices []int, baseStyle lipgloss.Style, highlightStyle lipgloss.Style) string {
	if len(indices) == 0 {
		return baseStyle.Render(text)
	}
	var indexSet map[int]bool = make(map[int]bool, len(indices))
	for _, idx := range indices {
		indexSet[idx] = true
	}
	var runes []rune = []rune(text)
	var builder strings.Builder
	for i, r := range runes {
		if indexSet[i] {
			builder.WriteString(highlightStyle.Render(string(r)))
		} else {
			builder.WriteString(baseStyle.Render(string(r)))
		}
	}
	return builder.String()
}

func (m *ModelCommand) renderModelBadges(provider string, model string) string {
	var badges []string = make([]string, 0, 2)
	var favoriteStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorYellow)).
		Bold(true)
	var recentStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorBlue)).
		Bold(true)

	if m.isFavorite(provider, model) {
		badges = append(badges, favoriteStyle.Render("★"))
	}
	if m.isRecent(provider, model) {
		badges = append(badges, recentStyle.Render("⟳"))
	}
	if len(badges) == 0 {
		return ""
	}
	return strings.Join(badges, " ")
}

func renderPanel(content string, width int) string {
	style := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorSurface)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorBorder)).
		Padding(0, 1)
	if width > 0 {
		style = style.Width(width)
	}
	return style.Render(content)
}

func renderCardLine(line string, width int, selected bool) string {
	style := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorPanel)).
		Foreground(lipgloss.Color(ColorWhite))
	if selected {
		style = style.
			Background(lipgloss.Color(ColorAccent2)).
			Foreground(lipgloss.Color(ColorWhite)).
			Bold(true)
	}
	// Padding adds 2 chars (1 left, 1 right), so reduce width accordingly
	// This ensures the total rendered width matches the requested width
	if width > 2 {
		style = style.Width(max(1, width-2))
	}
	return style.Padding(0, 1).Render(line)
}

func renderGroupHeaderLine(label string, width int, active bool) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Background(lipgloss.Color(ColorSurface)).
		Bold(true)
	if active {
		style = style.Foreground(lipgloss.Color(ColorWhite))
	}
	if width > 0 {
		style = style.Width(width)
	}
	return style.Padding(0, 1).Render(label)
}

func (m *ModelCommand) providerByName(name string) *Provider {
	for i := range m.providers {
		if strings.EqualFold(m.providers[i].Name, name) {
			return &m.providers[i]
		}
	}
	return nil
}

func (m *ModelCommand) renderModelDetail(provider Provider, model ModelInfo, width int) string {
	innerWidth := max(10, width-4)
	if innerWidth < 10 {
		innerWidth = width
	}
	name := model.DisplayName
	if name == "" {
		name = model.ID
	}
	providerLabel := provider.DisplayName
	if providerLabel == "" {
		providerLabel = provider.Name
	}
	contextLabel := strings.TrimSpace(model.Context)
	if contextLabel == "" && model.ContextWindow > 0 {
		contextLabel = formatContextWindow(model.ContextWindow)
	}
	if contextLabel == "" {
		contextLabel = i18n.T("commands_b.model.not_available")
	}

	titleStyle := lipgloss.NewStyle().Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted))
	statusChips := m.renderStatusChips(provider.Name, model.ID)
	titleLine := joinLeftRight(innerWidth, titleStyle.Render(name), statusChips)
	metaLine := joinLeftRight(innerWidth, metaStyle.Render(providerLabel+" • "+model.ID), metaStyle.Render(i18n.T("commands_b.model.context_value", contextLabel)))

	tags := InferModelTags(provider.Name, model)
	capLine := renderTagChips(tags)
	if capLine != "" {
		capLine = metaStyle.Render(i18n.T("commands_b.model.capabilities")) + " " + capLine
	}

	var descTitle string = i18n.T("commands_b.model.description_title")
	var descLines []string
	var descStyle lipgloss.Style = lipgloss.NewStyle()
	var mutedStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Italic(true)
	var readmeKey string = ModelReadmeKey(provider.Name, model.ID)
	var readmeEntry ModelReadmeEntry
	var hasReadme bool
	if m.readmeCache != nil {
		readmeEntry, hasReadme = m.readmeCache[readmeKey]
	}
	var wrapWidth int = max(20, innerWidth-2)
	if wrapWidth < 20 {
		wrapWidth = innerWidth
	}
	var linkBase string = readmeLinkBaseURL(provider)
	var linkRegistry *readmeLinkRegistry = newReadmeLinkRegistry("model-readme")
	if hasReadme {
		if readmeEntry.Source != "" {
			descTitle = i18n.T("commands_b.model.description_source", readmeEntry.Source)
		}
		switch {
		case readmeEntry.Loading:
			descLines = []string{mutedStyle.Render(i18n.T("commands_b.model.fetching_description"))}
		case strings.TrimSpace(readmeEntry.Text) == "":
			descLines = []string{mutedStyle.Render(i18n.T("commands_b.model.no_description"))}
		default:
			var wrapped []string = wrapReadmeLinesWithRegistry(readmeEntry.Text, wrapWidth, linkBase, linkRegistry)
			var idx int
			for idx = range wrapped {
				descLines = append(descLines, descStyle.Render(wrapped[idx]))
			}
			if len(linkRegistry.targets) > 0 {
				m.linkTargets = linkRegistry.targets
			}
		}
	} else {
		descLines = []string{mutedStyle.Render(i18n.T("commands_b.model.fetching_description"))}
	}

	infoLines := []string{
		i18n.T("commands_b.model.provider_value", providerLabel),
		i18n.T("commands_b.model.model_id", model.ID),
		i18n.T("commands_b.model.context_filter", contextLabel),
	}
	if provider.APIType != "" {
		infoLines = append(infoLines, i18n.T("commands_b.model.api_value", provider.APIType))
	}
	if provider.BaseURL != "" {
		infoLines = append(infoLines, i18n.T("commands_b.model.endpoint_value", provider.BaseURL))
	}

	status := i18n.T("commands_b.model.status_ready")
	if !provider.Available {
		status = i18n.T("commands_b.model.status_not_configured")
	}
	availabilityLines := []string{
		i18n.T("commands_b.model.status_value", status),
	}

	sections := []string{
		titleLine,
		metaLine,
	}
	if capLine != "" {
		sections = append(sections, capLine)
	}
	sections = append(sections, "")
	sections = append(sections, renderDetailCard(descTitle, descLines, innerWidth))
	sections = append(sections, renderDetailCard(i18n.T("commands_b.model.information"), infoLines, innerWidth))
	sections = append(sections, renderDetailCard(i18n.T("commands_b.model.availability"), availabilityLines, innerWidth))

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return renderPanel(content, width)
}

func (m *ModelCommand) renderAliasDetail(alias ModelAliasEntry, width int) string {
	innerWidth := max(10, width-4)
	if innerWidth < 10 {
		innerWidth = width
	}
	name := alias.DisplayName
	if name == "" {
		name = alias.Name
	}
	titleStyle := lipgloss.NewStyle().Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted))

	var providerNames []string
	for _, variant := range alias.Variants {
		label := variant.ProviderDisplayName
		if label == "" {
			label = variant.ProviderName
		}
		providerNames = append(providerNames, label)
	}
	sort.Strings(providerNames)
	uniqueProviders := uniqueStrings(providerNames)
	displayProviders := strings.Join(uniqueProviders, ", ")
	maxLen := max(0, innerWidth-10)
	if maxLen > 0 && len(displayProviders) > maxLen {
		if maxLen <= 3 {
			endIdx := min(maxLen, len([]rune(displayProviders)))
			displayProviders = string([]rune(displayProviders)[:endIdx])
		} else {
			endIdx := min(maxLen-3, len([]rune(displayProviders)))
			displayProviders = string([]rune(displayProviders)[:endIdx]) + "..."
		}
	}

	contextLabel := AliasContextLabel(alias.Variants)
	if contextLabel == "" {
		contextLabel = i18n.T("commands_b.model.not_available")
	}

	var repProvider Provider
	var repModel ModelInfo
	var repOK bool
	repProvider, repModel, repOK = AliasRepresentativeModel(alias, m.currentProvider, m.providers)

	tags := aliasEntryTags(alias.Variants)
	capLine := renderTagChips(tags)
	if capLine != "" {
		capLine = metaStyle.Render(i18n.T("commands_b.model.capabilities")) + " " + capLine
	}

	header := titleStyle.Render(name)
	meta := metaStyle.Render(i18n.T("commands_b.model.providers_context", len(alias.Variants), contextLabel))
	var representativeLabel string = i18n.T("commands_b.model.not_available")
	if repOK {
		repProviderLabel := repProvider.DisplayName
		if repProviderLabel == "" {
			repProviderLabel = repProvider.Name
		}
		representativeLabel = fmt.Sprintf("%s • %s", repProviderLabel, repModel.ID)
	}
	infoLines := []string{
		i18n.T("commands_b.model.representative", representativeLabel),
		i18n.T("commands_b.model.providers_value", displayProviders),
		i18n.T("commands_b.model.context_filter", contextLabel),
	}

	var descTitle string = i18n.T("commands_b.model.description_title")
	var descLines []string
	var descStyle lipgloss.Style = lipgloss.NewStyle()
	var mutedStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Italic(true)
	var wrapWidth int = max(20, innerWidth-2)
	if wrapWidth < 20 {
		wrapWidth = innerWidth
	}
	if repOK {
		var linkBase string = readmeLinkBaseURL(repProvider)
		var linkRegistry *readmeLinkRegistry = newReadmeLinkRegistry("alias-readme")
		var readmeKey string = ModelReadmeKey(repProvider.Name, repModel.ID)
		var readmeEntry ModelReadmeEntry
		var hasReadme bool
		if m.readmeCache != nil {
			readmeEntry, hasReadme = m.readmeCache[readmeKey]
		}
		if hasReadme {
			if readmeEntry.Source != "" {
				descTitle = i18n.T("commands_b.model.description_source", readmeEntry.Source)
			}
			switch {
			case readmeEntry.Loading:
				descLines = []string{mutedStyle.Render(i18n.T("commands_b.model.fetching_description"))}
			case strings.TrimSpace(readmeEntry.Text) == "":
				descLines = []string{mutedStyle.Render(i18n.T("commands_b.model.no_description"))}
			default:
				var wrapped []string = wrapReadmeLinesWithRegistry(readmeEntry.Text, wrapWidth, linkBase, linkRegistry)
				var idx int
				for idx = range wrapped {
					descLines = append(descLines, descStyle.Render(wrapped[idx]))
				}
				if len(linkRegistry.targets) > 0 {
					m.linkTargets = linkRegistry.targets
				}
			}
		} else {
			descLines = []string{mutedStyle.Render(i18n.T("commands_b.model.fetching_description"))}
		}
	} else {
		descLines = []string{mutedStyle.Render(i18n.T("commands_b.model.no_description"))}
	}

	sections := []string{header, meta}
	if capLine != "" {
		sections = append(sections, capLine)
	}
	sections = append(sections, "")
	sections = append(sections, renderDetailCard(descTitle, descLines, innerWidth))
	sections = append(sections, renderDetailCard(i18n.T("commands_b.model.family"), infoLines, innerWidth))
	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return renderPanel(content, width)
}

func renderDetailCard(title string, lines []string, width int) string {
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Bold(true)
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(ColorBorder)).
		Background(lipgloss.Color(ColorPanel)).
		Padding(1, 1)
	if width > 0 {
		cardStyle = cardStyle.Width(width)
	}
	return cardStyle.Render(lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(title), body))
}

func (m *ModelCommand) isFavorite(provider, model string) bool {
	return IndexOfModelRef(m.favoriteModels, provider, model) >= 0
}

func (m *ModelCommand) isRecent(provider, model string) bool {
	return IndexOfModelRef(m.recentModels, provider, model) >= 0
}

func (m *ModelCommand) toggleFavorite(provider, model string) bool {
	if m.configManager == nil {
		return m.isFavorite(provider, model)
	}
	cfg, err := m.configManager.UpdateConfig(func(cfg *SwarmOSConfig) {
		cfg.FavoriteModels, _ = ToggleFavoriteModels(cfg.FavoriteModels, provider, model)
	})
	if err == nil && cfg != nil {
		m.favoriteModels = cfg.FavoriteModels
		m.recentModels = cfg.RecentModels
	}
	return m.isFavorite(provider, model)
}

func (m *ModelCommand) setTabIndex(index int) {
	if index < 0 || index >= len(modelTabs) {
		return
	}
	m.tabIndex = index
	m.scrollOffset = 0
	m.syncSelectionToSearch()
}

func (m *ModelCommand) cycleProviderFilter() {
	if len(m.providers) == 0 {
		m.filterProvider = ""
		return
	}
	if m.filterProvider == "" {
		m.filterProvider = m.providers[0].Name
		m.scrollOffset = 0
		m.syncSelectionToSearch()
		return
	}
	var currentIndex int = -1
	for i, provider := range m.providers {
		if strings.EqualFold(provider.Name, m.filterProvider) {
			currentIndex = i
			break
		}
	}
	if currentIndex == -1 {
		m.filterProvider = m.providers[0].Name
	} else if currentIndex >= len(m.providers)-1 {
		m.filterProvider = ""
	} else {
		m.filterProvider = m.providers[currentIndex+1].Name
	}
	m.scrollOffset = 0
	m.syncSelectionToSearch()
}

func (m *ModelCommand) cycleContextFilter() {
	switch m.filterContext {
	case ContextFilterAny:
		m.filterContext = ContextFilter32k
	case ContextFilter32k:
		m.filterContext = ContextFilter128k
	case ContextFilter128k:
		m.filterContext = ContextFilter1M
	default:
		m.filterContext = ContextFilterAny
	}
	m.scrollOffset = 0
	m.syncSelectionToSearch()
}

func (m *ModelCommand) toggleTagFilter(tag ModelTag) {
	if m.filterTags == nil {
		m.filterTags = make(map[ModelTag]bool)
	}
	if m.filterTags[tag] {
		delete(m.filterTags, tag)
	} else {
		m.filterTags[tag] = true
	}
	m.scrollOffset = 0
	m.syncSelectionToSearch()
}

func (m *ModelCommand) groupedResultsForState() (groupedResults, bool) {
	switch m.state {
	case "provider_models":
		return m.providerModelResults(m.selectedProvider), true
	case "model":
		if m.modelSelectionUsesAliases() {
			return groupedResults{}, false
		}
		var allModels []AllModelEntry = m.getAllModels()
		return m.allModelResults(allModels), true
	case "alias_variants":
		return m.aliasVariantResults(m.selectedAlias), true
	default:
		return groupedResults{}, false
	}
}

func groupKeyForSelection(groups []modelGroup, selection int) string {
	for _, group := range groups {
		if slices.Contains(group.Indices, selection) {
			return group.Key
		}
	}
	return ""
}

func (m *ModelCommand) setGroupsCollapsed(groups []modelGroup, collapsed bool) {
	for _, group := range groups {
		if group.Key == "" {
			continue
		}
		m.collapsedGroups[group.Key] = collapsed
	}
}

func (m *ModelCommand) selectedModelRef() (string, string, bool) {
	switch m.state {
	case "provider_models":
		if m.selectedProvider < 0 || m.selectedProvider >= len(m.providers) {
			return "", "", false
		}
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) == 0 {
			return "", "", false
		}
		m.ensureSelection(results.Indices, &m.selectedModel)
		var provider Provider = m.providers[m.selectedProvider]
		if m.selectedModel < 0 || m.selectedModel >= len(provider.Models) {
			return "", "", false
		}
		return provider.Name, provider.Models[m.selectedModel].ID, true
	case "model":
		if m.modelSelectionUsesAliases() {
			return "", "", false
		}
		var allModels []AllModelEntry = m.getAllModels()
		var results groupedResults = m.allModelResults(allModels)
		if len(results.Indices) == 0 {
			return "", "", false
		}
		m.ensureSelection(results.Indices, &m.selectedModel)
		if m.selectedModel < 0 || m.selectedModel >= len(allModels) {
			return "", "", false
		}
		var entry AllModelEntry = allModels[m.selectedModel]
		return entry.ProviderName, entry.Model.ID, true
	case "alias_variants":
		if m.selectedAlias < 0 || m.selectedAlias >= len(m.aliasEntries) {
			return "", "", false
		}
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		if len(results.Indices) == 0 {
			return "", "", false
		}
		m.ensureSelection(results.Indices, &m.selectedVariant)
		var variants []ModelAliasVariant = m.aliasEntries[m.selectedAlias].Variants
		if m.selectedVariant < 0 || m.selectedVariant >= len(variants) {
			return "", "", false
		}
		return variants[m.selectedVariant].ProviderName, variants[m.selectedVariant].Model.ID, true
	default:
		return "", "", false
	}
}

func (m *ModelCommand) applyReadmeMsg(msg ModelReadmeMsg) {
	if m.readmeCache == nil {
		m.readmeCache = make(map[string]ModelReadmeEntry)
	}
	var entry ModelReadmeEntry
	entry.Text = msg.Text
	entry.Source = msg.Source
	entry.Err = msg.Err
	entry.Loading = false
	m.readmeCache[msg.Key] = entry
}

func (m *ModelCommand) currentDetailModel() (Provider, ModelInfo, bool) {
	switch m.state {
	case "provider_models":
		if m.selectedProvider < 0 || m.selectedProvider >= len(m.providers) {
			return Provider{}, ModelInfo{}, false
		}
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) == 0 {
			return Provider{}, ModelInfo{}, false
		}
		m.ensureSelection(results.Indices, &m.selectedModel)
		var provider Provider = m.providers[m.selectedProvider]
		if m.selectedModel < 0 || m.selectedModel >= len(provider.Models) {
			return Provider{}, ModelInfo{}, false
		}
		return provider, provider.Models[m.selectedModel], true
	case "model":
		if m.modelSelectionUsesAliases() {
			var results listResults = m.aliasResults()
			if len(results.Indices) == 0 {
				return Provider{}, ModelInfo{}, false
			}
			m.ensureSelection(results.Indices, &m.selectedModel)
			if m.selectedModel < 0 || m.selectedModel >= len(m.aliasEntries) {
				return Provider{}, ModelInfo{}, false
			}
			var alias ModelAliasEntry = m.aliasEntries[m.selectedModel]
			return AliasRepresentativeModel(alias, m.currentProvider, m.providers)
		}
		var allModels []AllModelEntry = m.getAllModels()
		var results groupedResults = m.allModelResults(allModels)
		if len(results.Indices) == 0 {
			return Provider{}, ModelInfo{}, false
		}
		m.ensureSelection(results.Indices, &m.selectedModel)
		if m.selectedModel < 0 || m.selectedModel >= len(allModels) {
			return Provider{}, ModelInfo{}, false
		}
		var entry AllModelEntry = allModels[m.selectedModel]
		return entry.Provider, entry.Model, true
	case "alias_variants":
		if m.selectedAlias < 0 || m.selectedAlias >= len(m.aliasEntries) {
			return Provider{}, ModelInfo{}, false
		}
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		if len(results.Indices) == 0 {
			return Provider{}, ModelInfo{}, false
		}
		m.ensureSelection(results.Indices, &m.selectedVariant)
		var variants []ModelAliasVariant = m.aliasEntries[m.selectedAlias].Variants
		if m.selectedVariant < 0 || m.selectedVariant >= len(variants) {
			return Provider{}, ModelInfo{}, false
		}
		var provider Provider = aliasVariantProvider(variants[m.selectedVariant], m.providers)
		return provider, variants[m.selectedVariant].Model, true
	default:
		return Provider{}, ModelInfo{}, false
	}
}

func (m *ModelCommand) ensureReadmeCmd() tea.Cmd {
	var provider Provider
	var model ModelInfo
	var ok bool
	provider, model, ok = m.currentDetailModel()
	if !ok {
		return nil
	}
	if strings.TrimSpace(model.ID) == "" {
		return nil
	}
	if m.readmeCache == nil {
		m.readmeCache = make(map[string]ModelReadmeEntry)
	}
	var key string = ModelReadmeKey(provider.Name, model.ID)
	var entry ModelReadmeEntry
	var exists bool
	entry, exists = m.readmeCache[key]
	if exists {
		if entry.Loading || entry.Text != "" || entry.Err != "" {
			return nil
		}
	}
	entry.Loading = true
	m.readmeCache[key] = entry
	return FetchModelReadmeCmd(provider, model)
}

func (m *ModelCommand) providerDisplayName(name string) string {
	for _, provider := range m.providers {
		if strings.EqualFold(provider.Name, name) {
			if provider.DisplayName != "" {
				return provider.DisplayName
			}
			return provider.Name
		}
	}
	return name
}
