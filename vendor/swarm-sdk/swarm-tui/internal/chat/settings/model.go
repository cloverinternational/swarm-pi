package settings

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// ModelSettings handles model/provider settings with integrated picker and provider management
type ModelSettings struct {
	currentProvider   string
	currentModel      string
	providers         []commands.Provider
	state             string // "menu", "browse_models", "alias_variants", "manage_providers", "add_provider", "edit_provider", "provider_models", "models", "add_model", "edit_model", "confirm_delete"
	selectedProvider  int
	selectedModel     int
	selectedAlias     int
	selectedVariant   int
	selectedMenuItem  int
	scrollOffset      int
	maxVisible        int
	configManager     *commands.ConfigManager
	openRouterRefresh func() (int, error)
	cursorRefresh     func() (int, error)
	anthropicRefresh  func() (int, error)
	codexRefresh      func() (int, error)
	refreshStatusText string
	refreshStatusErr  bool
	aliasEntries      []commands.ModelAliasEntry
	searchQuery       string
	searchActive      bool
	tabIndex          int
	filterProvider    string
	filterContext     int
	filterTags        map[commands.ModelTag]bool
	collapsedGroups   map[string]bool
	favoriteModels    []commands.ModelRef
	recentModels      []commands.ModelRef
	readmeCache       map[string]commands.ModelReadmeEntry
	readmeLinkTargets map[string]string

	// Form state for add/edit provider
	formField        int
	formProviderType string // "OpenAI", "Anthropic", "Custom"
	formAuthType     string // "oauth" or "api_key"
	formDisplayName  string
	formAPIEndpoint  string
	formAPIKey       string
	formColorIndex   int  // Index into color presets
	formEditing      bool // Whether currently editing a text field
	formCursorPos    int  // Cursor position in text field

	// Form state for add/edit model
	modelFormField           int
	modelFormID              string
	modelFormDisplayName     string
	modelFormContext         string
	modelFormThinkingEnabled bool
	modelFormThinkingBudget  int
	modelFormThinkingEffort  string
	modelFormDiffusion       bool // text-diffusion model (denoising reveal rendering)
	modelFormTemperature     *float64
	modelFormMaxTokens       int
	modelFormTopP            *float64
	modelFormTopK            *int
	editingModelIndex        int    // Index of model being edited (-1 for add)
	modelFormPreviousState   string // State to return to after save/cancel

	// Confirmation dialog state
	confirmDeleteType    string // "model" or "provider"
	confirmDeleteIndex   int    // Index of item to delete
	confirmSelected      int    // 0 = No, 1 = Yes
	confirmPreviousState string // State to return to on cancel
	confirmCustomMode    string // "add" or "edit"
	confirmCustomIndex   int    // Provider index for custom edit

	// Override confirmation state
	confirmOverride bool
	pendingProvider string
	pendingModel    string
	profileManager  *ProfileManager

	// Callback for when a model is selected
	// CRITICAL: This must be called when SetModel is invoked so the SDK can be notified
	onModelSelect           func(provider, model string)
	onReasoningEffortChange func(effort string)
	onModelConfigChange     func(provider string, model commands.ModelConfig)
	// onCredentialsChanged fires after a provider's API key/URL is saved so the
	// running SDK can rebuild the live client without a TUI restart.
	onCredentialsChanged func(providerName string)
	reasoningEffort      string

	// Plexus provider configuration. Permissions remain server-owned; the TUI
	// only caches the aliases returned for the connected key.
	plexusField             int
	plexusConnected         bool
	plexusStatusText        string
	plexusStatusErr         bool
	plexusDefaultAlias      string
	plexusLastSync          time.Time
	storeProviderSecret     func(providerName, secret string) (string, error)
	resolveProviderSecret   func(secretRef string) (string, error)
	deleteProviderSecret    func(secretRef string) error
	pendingCmd              tea.Cmd
	plexusRequestGeneration uint64
}

// Menu items for model settings
const (
	MenuBrowseModels = iota
	MenuManageProviders
	MenuAddProvider
	MenuReasoningEffort
)

var modelMenuOptions = []struct {
	name string
	desc string
}{
	{"Browse & Select Models", "Choose a model family and then pick a provider"},
	{"Manage Providers", "View, edit, and configure AI providers"},
	{"Add New Provider", "Add a custom AI provider (OpenAI-compatible API)"},
	{"Reasoning Effort", "Configure default reasoning effort for supported models"},
}

var modelTabs = []struct {
	label      string
	contextMin int
	tags       []commands.ModelTag
}{
	{label: "Fast/Cheap", tags: []commands.ModelTag{commands.TagFast}},
	{label: "Long Context", contextMin: 128000},
	{label: "Coding", tags: []commands.ModelTag{commands.TagCoding}},
	{label: "Vision", tags: []commands.ModelTag{commands.TagVision}},
	{label: "All"},
}

// Color presets for providers
var colorPresets = []struct {
	name string
	hex  string
}{
	{"Cyan", "#00FFFF"},
	{"Green", "#00FF00"},
	{"Purple", "#BD93F9"},
	{"Orange", "#FFA500"},
	{"Blue", "#8BE9FD"},
	{"Yellow", "#FFFF00"},
	{"Pink", "#FF79C6"},
	{"Red", "#FF5555"},
}

// Provider type presets
var providerTypePresets = []struct {
	name     string
	endpoint string
}{
	{"OpenAI", "https://api.openai.com/v1"},
	{"Anthropic", "https://api.anthropic.com/v1"},
	{"Gemini", "https://cloudcode-pa.googleapis.com"},
	{"Cerebras", "https://api.cerebras.ai/v1"},
	{"Fireworks", "https://api.fireworks.ai/inference/v1"},
	{"Groq", "https://api.groq.com/openai/v1"},
	{"Together AI", "https://api.together.xyz/v1"},
	{"DeepSeek", "https://api.deepseek.com/v1"},
	{"xAI (Grok)", "https://api.x.ai/v1"},
	{"Z.AI (GLM)", "https://api.z.ai/api/paas/v4"},
	{"Mistral AI", "https://api.mistral.ai/v1"},
	{"Moonshot (Kimi)", "https://api.moonshot.ai/v1"},
	{"Wafer.ai", "https://pass.wafer.ai/v1"},
	{"Perplexity", "https://api.perplexity.ai"},
	{"Plexus Gateway", "http://localhost:4000/v1"},
	{"OpenRouter", "https://openrouter.ai/api/v1"},
	{"MiniMax", "https://api.minimax.io/anthropic"},
	{"Exa", "https://api.exa.ai"},
	{"Custom", ""},
}

// NewModelSettings creates a new model settings handler
func NewModelSettings(provider, model string) *ModelSettings {
	cm, _ := commands.NewConfigManager()
	providers := loadProviders()
	aliasEntries := commands.LoadModelAliases(providers)
	var favorites []commands.ModelRef
	var recents []commands.ModelRef
	var reasoningEffort string = sdkprovider.ReasoningEffortAuto
	var readmeCache map[string]commands.ModelReadmeEntry = make(map[string]commands.ModelReadmeEntry)
	if cm != nil {
		if cfg, err := cm.LoadConfig(); err == nil && cfg != nil {
			favorites = cfg.FavoriteModels
			recents = cfg.RecentModels
			reasoningEffort = cfg.GetReasoningEffortForModel(provider, model)
		}
	}

	return &ModelSettings{
		currentProvider:  provider,
		currentModel:     model,
		providers:        providers,
		aliasEntries:     aliasEntries,
		state:            "menu",
		selectedMenuItem: 0,
		selectedProvider: 0,
		selectedModel:    0,
		scrollOffset:     0,
		maxVisible:       8,
		configManager:    cm,
		formProviderType: "OpenAI",
		formAuthType:     defaultAuthTypeForProvider("OpenAI"),
		formColorIndex:   0,
		tabIndex:         commands.TabAll,
		filterContext:    commands.ContextFilterAny,
		filterTags:       make(map[commands.ModelTag]bool),
		collapsedGroups:  make(map[string]bool),
		favoriteModels:   favorites,
		recentModels:     recents,
		reasoningEffort:  sdkprovider.NormalizeReasoningEffortSetting(reasoningEffort),
		readmeCache:      readmeCache,
	}
}

func (m *ModelSettings) SetOpenRouterRefreshCallback(refresh func() (int, error)) {
	m.openRouterRefresh = refresh
}

// SetCursorRefreshCallback wires the live Cursor AvailableModels fetch used by
// the model UI's refresh ("r") action.
func (m *ModelSettings) SetCursorRefreshCallback(refresh func() (int, error)) {
	m.cursorRefresh = refresh
}

// SetAnthropicRefreshCallback wires the live Anthropic /v1/models fetch (Claude
// Code OAuth) used by the model UI's refresh ("r") action.
func (m *ModelSettings) SetAnthropicRefreshCallback(refresh func() (int, error)) {
	m.anthropicRefresh = refresh
}

// SetCodexRefreshCallback wires the live Codex backend /models fetch (ChatGPT
// OAuth) used by the model UI's refresh ("r") action.
func (m *ModelSettings) SetCodexRefreshCallback(refresh func() (int, error)) {
	m.codexRefresh = refresh
}

func (m *ModelSettings) reloadModelPrefs() {
	if m.configManager == nil {
		return
	}
	cfg, err := m.configManager.LoadConfig()
	if err != nil || cfg == nil {
		return
	}
	m.favoriteModels = cfg.FavoriteModels
	m.recentModels = cfg.RecentModels
	m.reasoningEffort = cfg.GetReasoningEffortForModel(m.currentProvider, m.currentModel)
}

func (m *ModelSettings) isSelectedOpenRouter() bool {
	if len(m.providers) == 0 || m.selectedProvider >= len(m.providers) {
		return false
	}
	var providerName string = m.providers[m.selectedProvider].Name
	return strings.EqualFold(providerName, "openrouter")
}

// isSelectedCursor reports whether the currently selected provider is Cursor.
func (m *ModelSettings) isSelectedCursor() bool {
	if len(m.providers) == 0 || m.selectedProvider >= len(m.providers) {
		return false
	}
	return strings.EqualFold(m.providers[m.selectedProvider].Name, "cursor")
}

// isSelectedAnthropic reports whether the currently-selected provider is an
// Anthropic/Claude-family entry (the API-key "Anthropic" entry or the OAuth
// "ClaudeCode" entry), both of which refresh from the Anthropic /v1/models
// catalog.
func (m *ModelSettings) isSelectedAnthropic() bool {
	if len(m.providers) == 0 || m.selectedProvider >= len(m.providers) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(m.providers[m.selectedProvider].Name)) {
	case "anthropic", "claudecode", "claude", "claude code", "claude-code":
		return true
	default:
		return false
	}
}

// isSelectedOpenAICodex reports whether the currently-selected provider is an
// OpenAI ChatGPT-OAuth entry whose model list refreshes from the Codex backend
// /models catalog.
func (m *ModelSettings) isSelectedOpenAICodex() bool {
	if len(m.providers) == 0 || m.selectedProvider >= len(m.providers) {
		return false
	}
	selected := m.providers[m.selectedProvider]
	if strings.EqualFold(selected.APIType, "openai") && strings.EqualFold(selected.Type, "oauth") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(selected.Name)) {
	case "codex", "openai (oauth)", "openai-oauth", "chatgpt":
		return true
	default:
		return false
	}
}

// isSelectedRefreshable reports whether the selected provider supports a live
// model refresh ("r" key) — OpenRouter, Cursor, Anthropic (Claude Code OAuth),
// and OpenAI Codex (ChatGPT OAuth).
func (m *ModelSettings) isSelectedRefreshable() bool {
	return m.isSelectedOpenRouter() || m.isSelectedCursor() || m.isSelectedAnthropic() || m.isSelectedOpenAICodex()
}

func (m *ModelSettings) setRefreshStatus(text string, isError bool) {
	m.refreshStatusText = text
	m.refreshStatusErr = isError
}

func (m *ModelSettings) reloadProviders(selectedProviderName string) {
	var providers []commands.Provider = loadProviders()
	m.providers = providers
	m.aliasEntries = commands.LoadModelAliases(providers)

	if len(providers) == 0 {
		m.selectedProvider = 0
		m.selectedModel = 0
		m.selectedAlias = 0
		m.selectedVariant = 0
		m.scrollOffset = 0
		return
	}

	var selectedIndex int = 0
	for i, provider := range providers {
		if strings.EqualFold(provider.Name, selectedProviderName) {
			selectedIndex = i
			break
		}
	}

	m.selectedProvider = selectedIndex
	m.selectedModel = 0
	m.selectedAlias = 0
	m.selectedVariant = 0
	m.scrollOffset = 0
}

// refreshOpenRouterModels dispatches a live model refresh for the selected
// provider. It supports OpenRouter and Cursor (both fetch their catalog over
// HTTP and rewrite providers.json); other providers are a no-op.
func (m *ModelSettings) refreshOpenRouterModels() bool {
	switch {
	case m.isSelectedCursor():
		return m.runProviderRefresh("Cursor", m.cursorRefresh)
	case m.isSelectedOpenRouter():
		return m.runProviderRefresh("OpenRouter", m.openRouterRefresh)
	case m.isSelectedAnthropic():
		return m.runProviderRefresh("Anthropic", m.anthropicRefresh)
	case m.isSelectedOpenAICodex():
		return m.runProviderRefresh("OpenAI (Codex)", m.codexRefresh)
	default:
		return false
	}
}

// runProviderRefresh executes a provider's live model-refresh callback, reloads
// providers from disk, and reports status in the UI.
func (m *ModelSettings) runProviderRefresh(label string, refresh func() (int, error)) bool {
	if refresh == nil {
		m.setRefreshStatus(i18n.T("settings.residual_final.model.refresh_unavailable", label), true)
		return true
	}

	var selectedProviderName string = m.providers[m.selectedProvider].Name
	var count int
	var err error
	count, err = refresh()
	if err != nil {
		m.setRefreshStatus(i18n.T("settings.residual_final.model.refresh_failed", label, err), true)
		return true
	}

	m.reloadProviders(selectedProviderName)
	m.setRefreshStatus(i18n.T("settings.residual_final.model.refreshed", label, count), false)
	return true
}

// GetCurrentProvider returns current provider
func (m *ModelSettings) CurrentProvider() string {
	return m.currentProvider
}

// GetCurrentModel returns current model
func (m *ModelSettings) CurrentModel() string {
	return m.currentModel
}

// GetProviders returns the list of available providers with their models
func (m *ModelSettings) GetProviders() []commands.Provider {
	return m.providers
}

// GetProviderNames returns a list of configured provider names.
func (m *ModelSettings) GetProviderNames() []string {
	names := make([]string, len(m.providers))
	for i, p := range m.providers {
		names[i] = p.Name
	}
	return names
}

// GetModelsForProvider returns known model IDs for a given provider name.
func (m *ModelSettings) GetModelsForProvider(providerName string) []string {
	for _, p := range m.providers {
		if p.Name == providerName && len(p.Models) > 0 {
			ids := make([]string, len(p.Models))
			for i, mod := range p.Models {
				ids[i] = mod.ID
			}
			return ids
		}
	}
	return nil
}

// GetReasoningEffort returns the active reasoning effort setting.
func (m *ModelSettings) GetReasoningEffort() string {
	return sdkprovider.NormalizeReasoningEffortSetting(m.reasoningEffort)
}

// SetOnReasoningEffortChange sets the callback invoked when reasoning effort changes.
func (m *ModelSettings) SetOnReasoningEffortChange(fn func(effort string)) {
	m.onReasoningEffortChange = fn
}

// SetOnModelConfigChange registers a callback for persisted per-model edits so
// the live runtime can apply them immediately.
func (m *ModelSettings) SetOnModelConfigChange(fn func(provider string, model commands.ModelConfig)) {
	m.onModelConfigChange = fn
}

func (m *ModelSettings) setReasoningEffort(effort string, persist bool) {
	var normalized string = sdkprovider.NormalizeReasoningEffortSetting(effort)
	if normalized == m.reasoningEffort {
		return
	}

	m.reasoningEffort = normalized

	if persist && m.configManager != nil {
		if _, err := m.configManager.UpdateConfig(func(cfg *commands.SwarmOSConfig) {
			cfg.SetReasoningEffort(normalized)
		}); err != nil {
			logDebug("Failed to save reasoning effort config: %v", err)
		}
	}

	if m.onReasoningEffortChange != nil {
		m.onReasoningEffortChange(normalized)
	}
}

func (m *ModelSettings) selectionSupportsReasoningEffort() bool {
	return len(m.availableReasoningEfforts()) > 1
}

func (m *ModelSettings) availableReasoningEfforts() []string {
	var providerCfg *commands.Provider
	var modelCfg *commands.ModelInfo
	providerCfg, modelCfg = m.currentProviderModel()
	if providerCfg == nil {
		return []string{sdkprovider.ReasoningEffortAuto}
	}
	if modelCfg == nil {
		return commands.AvailableReasoningEffortSettings(
			providerCfg.APIType,
			providerCfg.Name,
			m.currentModel,
			nil,
			nil,
		)
	}

	return commands.AvailableReasoningEffortSettings(
		providerCfg.APIType,
		providerCfg.Name,
		modelCfg.ID,
		modelCfg.SupportsReasoningEffort,
		modelCfg.ReasoningEfforts,
	)
}

func (m *ModelSettings) cycleReasoningEffort(direction int) {
	var options []string = m.availableReasoningEfforts()
	if len(options) == 0 {
		return
	}

	var current string = m.GetReasoningEffort()
	var currentIndex int = 0
	for i, option := range options {
		if option == current {
			currentIndex = i
			break
		}
	}

	var nextIndex int = currentIndex + direction
	for nextIndex < 0 {
		nextIndex += len(options)
	}
	nextIndex = nextIndex % len(options)
	m.setReasoningEffort(options[nextIndex], true)
}

// IsInNestedState returns true if we're in a form or nested view (not the main menu)
func (m *ModelSettings) IsInNestedState() bool {
	return m.state != "menu"
}

// IsEditingText returns true if user is currently editing a text field
func (m *ModelSettings) IsEditingText() bool {
	return m.formEditing
}

func (m *ModelSettings) usesAliases() bool {
	return len(m.aliasEntries) > 0
}

func (m *ModelSettings) searchEnabled() bool {
	switch m.state {
	case "browse_models", "alias_variants", "manage_providers", "provider_models", "models":
		return true
	default:
		return false
	}
}

func (m *ModelSettings) tabsEnabled() bool {
	switch m.state {
	case "provider_models", "models", "alias_variants":
		return true
	case "browse_models":
		return m.usesAliases()
	default:
		return false
	}
}

func (m *ModelSettings) normalizedTabIndex() int {
	if m.tabIndex < 0 || m.tabIndex >= len(modelTabs) {
		return commands.TabAll
	}
	return m.tabIndex
}

func (m *ModelSettings) currentTab() (string, int, []commands.ModelTag) {
	var tab struct {
		label      string
		contextMin int
		tags       []commands.ModelTag
	} = modelTabs[m.normalizedTabIndex()]
	return tab.label, tab.contextMin, tab.tags
}

func (m *ModelSettings) recommendedOnly() bool {
	return false
}

func (m *ModelSettings) contextFilterRange() (int, int, string) {
	switch m.filterContext {
	case commands.ContextFilter32k:
		return 32000, 0, "32K+"
	case commands.ContextFilter128k:
		return 128000, 0, "128K+"
	case commands.ContextFilter1M:
		return 1000000, 0, "1M+"
	default:
		return 0, 0, i18n.T("settings.residual_final.model.any")
	}
}

func (m *ModelSettings) effectiveSearch() (string, commands.SearchFilters) {
	var parsed commands.ParsedSearch = commands.ParseSearchQuery(m.searchQuery)
	var filters commands.SearchFilters = commands.SearchFilters{Tags: make(map[commands.ModelTag]bool)}
	filters.Provider = parsed.Filters.Provider
	if filters.Provider == "" {
		filters.Provider = m.filterProvider
	}
	for tag := range m.filterTags {
		filters.Tags[tag] = true
	}
	_, tabContext, tabTags := m.currentTab()
	if tabContext > 0 {
		filters.ContextMin = tabContext
	}
	for _, tag := range tabTags {
		filters.Tags[tag] = true
	}
	if parsed.Filters.ContextMin > 0 || parsed.Filters.ContextMax > 0 {
		filters.ContextMin = parsed.Filters.ContextMin
		filters.ContextMax = parsed.Filters.ContextMax
	} else if filters.ContextMin == 0 && filters.ContextMax == 0 {
		filters.ContextMin, filters.ContextMax, _ = m.contextFilterRange()
	}
	for tag := range parsed.Filters.Tags {
		filters.Tags[tag] = true
	}
	return parsed.Text, filters
}

func (m *ModelSettings) buildGroupedResults(groups []modelGroup, scores map[int]int, highlights map[int][]int) groupedResults {
	var flattened []int = make([]int, 0)
	for i := range groups {
		var collapsed bool = m.collapsedGroups[groups[i].Key]
		groups[i].Collapsed = collapsed
		if collapsed {
			continue
		}
		flattened = append(flattened, groups[i].Indices...)
	}
	return groupedResults{
		Groups:     groups,
		Indices:    flattened,
		Scores:     scores,
		Highlights: highlights,
	}
}

func (m *ModelSettings) providerResults() listResults {
	var parsed commands.ParsedSearch = commands.ParseSearchQuery(m.searchQuery)
	var searchText string = parsed.Text
	var providerFilter string = parsed.Filters.Provider
	var indices []int = make([]int, 0, len(m.providers))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	for i, prov := range m.providers {
		if !matchesProviderFilter(providerFilter, prov.Name, prov.DisplayName) {
			continue
		}
		var label string = prov.DisplayName
		if label == "" {
			label = prov.Name
		}
		var candidateParts []string = []string{
			prov.DisplayName,
			prov.Name,
			prov.APIType,
			prov.BaseURL,
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}
	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(m.providers[left].DisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(m.providers[left].Name))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(m.providers[right].DisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(m.providers[right].Name))
			}
			return leftLabel < rightLabel
		})
	}
	return listResults{Indices: indices, Scores: scores, Highlights: highlights}
}

func (m *ModelSettings) aliasResults() listResults {
	var searchText string
	var filters commands.SearchFilters
	searchText, filters = m.effectiveSearch()
	var indices []int = make([]int, 0, len(m.aliasEntries))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	var recommendedOnly bool = m.recommendedOnly()

	for i, alias := range m.aliasEntries {
		var variants []commands.ModelAliasVariant = alias.Variants
		if len(variants) == 0 {
			continue
		}
		if filters.Provider != "" {
			var filteredVariants []commands.ModelAliasVariant = make([]commands.ModelAliasVariant, 0, len(variants))
			for _, variant := range variants {
				if matchesProviderFilter(filters.Provider, variant.ProviderName, variant.ProviderDisplayName) {
					filteredVariants = append(filteredVariants, variant)
				}
			}
			variants = filteredVariants
			if len(variants) == 0 {
				continue
			}
		}

		if recommendedOnly {
			var hasRecommended bool
			for _, variant := range variants {
				if m.isFavorite(variant.ProviderName, variant.Model.ID) || m.isRecent(variant.ProviderName, variant.Model.ID) {
					hasRecommended = true
					break
				}
			}
			if !hasRecommended {
				continue
			}
		}

		var context int = aliasEntryContext(variants)
		if !matchesContextRange(context, filters.ContextMin, filters.ContextMax) {
			continue
		}
		var tags map[commands.ModelTag]bool = aliasEntryTags(variants)
		if !matchesTags(tags, filters.Tags) {
			continue
		}

		var label string = alias.DisplayName
		if label == "" {
			label = alias.Name
		}
		var candidateParts []string = make([]string, 0, 2+len(variants)*4)
		candidateParts = append(candidateParts, alias.DisplayName, alias.Name)
		for _, variant := range variants {
			candidateParts = append(candidateParts, variant.ProviderDisplayName, variant.ProviderName)
			candidateParts = append(candidateParts, variant.Model.DisplayName, variant.Model.ID)
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}

	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(m.aliasEntries[left].DisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(m.aliasEntries[left].Name))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(m.aliasEntries[right].DisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(m.aliasEntries[right].Name))
			}
			return leftLabel < rightLabel
		})
	}
	return listResults{Indices: indices, Scores: scores, Highlights: highlights}
}

func (m *ModelSettings) providerModelResults(providerIdx int) groupedResults {
	if providerIdx < 0 || providerIdx >= len(m.providers) {
		return groupedResults{}
	}
	var provider commands.Provider = m.providers[providerIdx]
	var searchText string
	var filters commands.SearchFilters
	searchText, filters = m.effectiveSearch()
	filters.Provider = ""

	var indices []int = make([]int, 0, len(provider.Models))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	var recommendedOnly bool = m.recommendedOnly()

	for i, model := range provider.Models {
		var context int = commands.ContextFromModel(model)
		if !matchesContextRange(context, filters.ContextMin, filters.ContextMax) {
			continue
		}
		var tags map[commands.ModelTag]bool = commands.InferModelTags(provider.Name, model)
		if !matchesTags(tags, filters.Tags) {
			continue
		}
		if recommendedOnly && !m.isFavorite(provider.Name, model.ID) && !m.isRecent(provider.Name, model.ID) {
			continue
		}

		var label string = model.DisplayName
		if label == "" {
			label = model.ID
		}
		var candidateParts []string = []string{
			model.DisplayName,
			model.ID,
			model.Context,
			provider.DisplayName,
			provider.Name,
		}
		if model.ContextWindow > 0 {
			candidateParts = append(candidateParts, formatContextWindow(model.ContextWindow))
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}

	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(provider.Models[left].DisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(provider.Models[left].ID))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(provider.Models[right].DisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(provider.Models[right].ID))
			}
			return leftLabel < rightLabel
		})
	}

	var favorites []int = make([]int, 0)
	var recents []int = make([]int, 0)
	var used map[int]bool = make(map[int]bool)
	for _, idx := range indices {
		var model commands.ModelInfo = provider.Models[idx]
		if m.isFavorite(provider.Name, model.ID) {
			favorites = append(favorites, idx)
			used[idx] = true
			continue
		}
		if m.isRecent(provider.Name, model.ID) {
			recents = append(recents, idx)
			used[idx] = true
		}
	}

	var groups []modelGroup = make([]modelGroup, 0)
	if len(favorites) > 0 {
		groups = append(groups, modelGroup{
			Key:     "favorites:" + strings.ToLower(provider.Name),
			Title:   i18n.T("settings.residual_final.model.group.favorites"),
			Indices: favorites,
		})
	}
	if len(recents) > 0 {
		groups = append(groups, modelGroup{
			Key:     "recents:" + strings.ToLower(provider.Name),
			Title:   i18n.T("settings.residual_final.model.group.recent"),
			Indices: recents,
		})
	}

	if !recommendedOnly {
		var aliasOrder []string = make([]string, 0)
		var aliasTitle map[string]string = make(map[string]string)
		var aliasByModelID map[string]string = make(map[string]string)
		for _, alias := range m.aliasEntries {
			var aliasKey string = "alias:" + strings.ToLower(provider.Name) + ":" + strings.ToLower(strings.TrimSpace(alias.Name))
			if aliasKey == "alias:"+strings.ToLower(provider.Name)+":" {
				aliasKey = "alias:" + strings.ToLower(provider.Name) + ":" + strings.ToLower(strings.TrimSpace(alias.DisplayName))
			}
			var hasProvider bool
			for _, variant := range alias.Variants {
				if strings.EqualFold(variant.ProviderName, provider.Name) {
					aliasByModelID[variant.Model.ID] = aliasKey
					hasProvider = true
				}
			}
			if hasProvider {
				var title string = alias.DisplayName
				if title == "" {
					title = alias.Name
				}
				aliasOrder = append(aliasOrder, aliasKey)
				aliasTitle[aliasKey] = title
			}
		}

		var aliasGroups map[string][]int = make(map[string][]int)
		var other []int = make([]int, 0)
		for _, idx := range indices {
			if used[idx] {
				continue
			}
			var model commands.ModelInfo = provider.Models[idx]
			var aliasKey string = aliasByModelID[model.ID]
			if aliasKey != "" {
				aliasGroups[aliasKey] = append(aliasGroups[aliasKey], idx)
				continue
			}
			other = append(other, idx)
		}

		for _, aliasKey := range aliasOrder {
			var groupIndices []int = aliasGroups[aliasKey]
			if len(groupIndices) == 0 {
				continue
			}
			var title string = aliasTitle[aliasKey]
			if title == "" {
				title = i18n.T("settings.residual_final.model.group.models")
			}
			groups = append(groups, modelGroup{
				Key:     aliasKey,
				Title:   title,
				Indices: groupIndices,
			})
		}

		if len(other) > 0 {
			groups = append(groups, modelGroup{
				Key:     "other:" + strings.ToLower(provider.Name),
				Title:   i18n.T("settings.residual_final.model.group.other"),
				Indices: other,
			})
		}
	}

	if len(groups) == 0 {
		groups = append(groups, modelGroup{
			Key:     "models:" + strings.ToLower(provider.Name),
			Title:   i18n.T("settings.residual_final.model.group.models"),
			Indices: indices,
		})
	}
	return m.buildGroupedResults(groups, scores, highlights)
}

func (m *ModelSettings) aliasVariantResults(aliasIdx int) groupedResults {
	if aliasIdx < 0 || aliasIdx >= len(m.aliasEntries) {
		return groupedResults{}
	}
	var alias commands.ModelAliasEntry = m.aliasEntries[aliasIdx]
	var variants []commands.ModelAliasVariant = alias.Variants

	var searchText string
	var filters commands.SearchFilters
	searchText, filters = m.effectiveSearch()
	var indices []int = make([]int, 0, len(variants))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	var recommendedOnly bool = m.recommendedOnly()

	for i, variant := range variants {
		if !matchesProviderFilter(filters.Provider, variant.ProviderName, variant.ProviderDisplayName) {
			continue
		}
		var context int = commands.ContextFromModel(variant.Model)
		if !matchesContextRange(context, filters.ContextMin, filters.ContextMax) {
			continue
		}
		var tags map[commands.ModelTag]bool = commands.InferModelTags(variant.ProviderName, variant.Model)
		if !matchesTags(tags, filters.Tags) {
			continue
		}
		if recommendedOnly && !m.isFavorite(variant.ProviderName, variant.Model.ID) && !m.isRecent(variant.ProviderName, variant.Model.ID) {
			continue
		}

		var label string = variant.ProviderDisplayName
		if label == "" {
			label = variant.ProviderName
		}
		var candidateParts []string = []string{
			label,
			variant.ProviderName,
			variant.Model.DisplayName,
			variant.Model.ID,
			variant.Model.Context,
		}
		if variant.Model.ContextWindow > 0 {
			candidateParts = append(candidateParts, formatContextWindow(variant.Model.ContextWindow))
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}

	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(variants[left].ProviderDisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(variants[left].ProviderName))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(variants[right].ProviderDisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(variants[right].ProviderName))
			}
			return leftLabel < rightLabel
		})
	} else {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			var leftLabel string = strings.ToLower(strings.TrimSpace(variants[left].ProviderDisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(variants[left].ProviderName))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(variants[right].ProviderDisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(variants[right].ProviderName))
			}
			return leftLabel < rightLabel
		})
	}

	var favorites []int = make([]int, 0)
	var recents []int = make([]int, 0)
	var used map[int]bool = make(map[int]bool)
	for _, idx := range indices {
		var variant commands.ModelAliasVariant = variants[idx]
		if m.isFavorite(variant.ProviderName, variant.Model.ID) {
			favorites = append(favorites, idx)
			used[idx] = true
			continue
		}
		if m.isRecent(variant.ProviderName, variant.Model.ID) {
			recents = append(recents, idx)
			used[idx] = true
		}
	}

	var groups []modelGroup = make([]modelGroup, 0)
	if len(favorites) > 0 {
		groups = append(groups, modelGroup{
			Key:     "favorites:alias:" + strings.ToLower(alias.Name),
			Title:   i18n.T("settings.residual_final.model.group.favorites"),
			Indices: favorites,
		})
	}
	if len(recents) > 0 {
		groups = append(groups, modelGroup{
			Key:     "recents:alias:" + strings.ToLower(alias.Name),
			Title:   i18n.T("settings.residual_final.model.group.recent"),
			Indices: recents,
		})
	}

	if !recommendedOnly {
		var available []int = make([]int, 0)
		var unavailable []int = make([]int, 0)
		for _, idx := range indices {
			if used[idx] {
				continue
			}
			if variants[idx].ProviderAvailable {
				available = append(available, idx)
			} else {
				unavailable = append(unavailable, idx)
			}
		}
		if len(available) > 0 {
			groups = append(groups, modelGroup{
				Key:     "available:alias:" + strings.ToLower(alias.Name),
				Title:   i18n.T("settings.residual_final.model.group.configured"),
				Indices: available,
			})
		}
		if len(unavailable) > 0 {
			groups = append(groups, modelGroup{
				Key:     "unavailable:alias:" + strings.ToLower(alias.Name),
				Title:   i18n.T("settings.residual_final.model.group.not_configured"),
				Indices: unavailable,
			})
		}
	}

	if len(groups) == 0 {
		groups = append(groups, modelGroup{
			Key:     "providers:alias:" + strings.ToLower(alias.Name),
			Title:   i18n.T("settings.residual_final.model.group.providers"),
			Indices: indices,
		})
	}
	return m.buildGroupedResults(groups, scores, highlights)
}

func (m *ModelSettings) adjustScroll(pos int, total int) {
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

func (m *ModelSettings) moveSelection(filtered []int, current *int, delta int) {
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

func (m *ModelSettings) ensureSelection(filtered []int, current *int) {
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

func (m *ModelSettings) syncSelectionToSearch() {
	if !m.searchEnabled() {
		return
	}
	switch m.state {
	case "browse_models":
		if m.usesAliases() {
			var results listResults = m.aliasResults()
			m.ensureSelection(results.Indices, &m.selectedAlias)
		} else {
			var results listResults = m.providerResults()
			m.ensureSelection(results.Indices, &m.selectedProvider)
		}
	case "alias_variants":
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		m.ensureSelection(results.Indices, &m.selectedVariant)
	case "manage_providers":
		var results listResults = m.providerResults()
		m.ensureSelection(results.Indices, &m.selectedProvider)
	case "provider_models":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		m.ensureSelection(results.Indices, &m.selectedModel)
	case "models":
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		m.ensureSelection(results.Indices, &m.selectedModel)
	}
}

func (m *ModelSettings) autocompleteSearch() bool {
	if !m.searchEnabled() {
		return false
	}

	switch m.state {
	case "browse_models":
		if m.usesAliases() {
			var results listResults = m.aliasResults()
			if len(results.Indices) == 0 {
				return false
			}
			var idx int = m.selectedAlias
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
	case "alias_variants":
		if m.selectedAlias < 0 || m.selectedAlias >= len(m.aliasEntries) {
			return false
		}
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		if len(results.Indices) == 0 {
			return false
		}
		var idx int = m.selectedVariant
		if indexOfInt(results.Indices, idx) == -1 {
			idx = results.Indices[0]
		}
		var label string = m.aliasEntries[m.selectedAlias].Variants[idx].ProviderDisplayName
		if label == "" {
			label = m.aliasEntries[m.selectedAlias].Variants[idx].ProviderName
		}
		m.searchQuery = label
	case "manage_providers":
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
	case "provider_models", "models":
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
	}

	m.scrollOffset = 0
	m.syncSelectionToSearch()
	return true
}

func (m *ModelSettings) handleSearchKey(key string) bool {
	if !m.searchEnabled() {
		return false
	}
	if !m.searchActive {
		switch key {
		case "/", "ctrl+f":
			m.searchActive = true
			return true
		default:
			return false
		}
	}

	switch key {
	case "esc":
		m.searchActive = false
		return true
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			m.scrollOffset = 0
			m.syncSelectionToSearch()
		} else {
			m.searchActive = false
		}
		return true
	case "ctrl+u":
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.scrollOffset = 0
			m.syncSelectionToSearch()
		}
		return true
	case "tab":
		return m.autocompleteSearch()
	}

	if len(key) == 1 && unicode.IsPrint(rune(key[0])) {
		// Don't consume action keys when in models/provider_models/manage_providers state
		if m.state == "models" || m.state == "provider_models" || m.state == "manage_providers" {
			switch key {
			case "a", "e", "d", "r":
				// Let these keys pass through to the main handler
				return false
			}
		}
		m.searchQuery += key
		m.scrollOffset = 0
		m.syncSelectionToSearch()
		return true
	}

	return false
}

func (m *ModelSettings) setTabIndex(index int) {
	if index < 0 || index >= len(modelTabs) {
		return
	}
	m.tabIndex = index
	m.scrollOffset = 0
	m.syncSelectionToSearch()
}

func (m *ModelSettings) cycleProviderFilter() {
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

func (m *ModelSettings) cycleContextFilter() {
	switch m.filterContext {
	case commands.ContextFilterAny:
		m.filterContext = commands.ContextFilter32k
	case commands.ContextFilter32k:
		m.filterContext = commands.ContextFilter128k
	case commands.ContextFilter128k:
		m.filterContext = commands.ContextFilter1M
	default:
		m.filterContext = commands.ContextFilterAny
	}
	m.scrollOffset = 0
	m.syncSelectionToSearch()
}

func (m *ModelSettings) toggleTagFilter(tag commands.ModelTag) {
	if m.filterTags == nil {
		m.filterTags = make(map[commands.ModelTag]bool)
	}
	if m.filterTags[tag] {
		delete(m.filterTags, tag)
	} else {
		m.filterTags[tag] = true
	}
	m.scrollOffset = 0
	m.syncSelectionToSearch()
}

func (m *ModelSettings) groupedResultsForState() (groupedResults, bool) {
	switch m.state {
	case "provider_models", "models":
		return m.providerModelResults(m.selectedProvider), true
	case "alias_variants":
		return m.aliasVariantResults(m.selectedAlias), true
	default:
		return groupedResults{}, false
	}
}

func (m *ModelSettings) setGroupsCollapsed(groups []modelGroup, collapsed bool) {
	for _, group := range groups {
		if group.Key == "" {
			continue
		}
		m.collapsedGroups[group.Key] = collapsed
	}
}

func (m *ModelSettings) selectedModelRef() (string, string, bool) {
	switch m.state {
	case "provider_models", "models":
		if m.selectedProvider < 0 || m.selectedProvider >= len(m.providers) {
			return "", "", false
		}
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) == 0 {
			return "", "", false
		}
		m.ensureSelection(results.Indices, &m.selectedModel)
		var provider commands.Provider = m.providers[m.selectedProvider]
		if m.selectedModel < 0 || m.selectedModel >= len(provider.Models) {
			return "", "", false
		}
		return provider.Name, provider.Models[m.selectedModel].ID, true
	case "alias_variants":
		if m.selectedAlias < 0 || m.selectedAlias >= len(m.aliasEntries) {
			return "", "", false
		}
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		if len(results.Indices) == 0 {
			return "", "", false
		}
		m.ensureSelection(results.Indices, &m.selectedVariant)
		var variants []commands.ModelAliasVariant = m.aliasEntries[m.selectedAlias].Variants
		if m.selectedVariant < 0 || m.selectedVariant >= len(variants) {
			return "", "", false
		}
		return variants[m.selectedVariant].ProviderName, variants[m.selectedVariant].Model.ID, true
	default:
		return "", "", false
	}
}

func (m *ModelSettings) applyReadmeMsg(msg commands.ModelReadmeMsg) {
	if m.readmeCache == nil {
		m.readmeCache = make(map[string]commands.ModelReadmeEntry)
	}
	var entry commands.ModelReadmeEntry
	entry.Text = msg.Text
	entry.Source = msg.Source
	entry.Err = msg.Err
	entry.Loading = false
	m.readmeCache[msg.Key] = entry
}

func (m *ModelSettings) currentDetailModel() (commands.Provider, commands.ModelInfo, bool) {
	switch m.state {
	case "browse_models":
		if !m.usesAliases() {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		var results listResults = m.aliasResults()
		if len(results.Indices) == 0 {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		m.ensureSelection(results.Indices, &m.selectedAlias)
		if m.selectedAlias < 0 || m.selectedAlias >= len(m.aliasEntries) {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		var alias commands.ModelAliasEntry = m.aliasEntries[m.selectedAlias]
		return commands.AliasRepresentativeModel(alias, m.currentProvider, m.providers)
	case "provider_models", "models":
		if m.selectedProvider < 0 || m.selectedProvider >= len(m.providers) {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		var results groupedResults = m.providerModelResults(m.selectedProvider)
		if len(results.Indices) == 0 {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		m.ensureSelection(results.Indices, &m.selectedModel)
		var provider commands.Provider = m.providers[m.selectedProvider]
		if m.selectedModel < 0 || m.selectedModel >= len(provider.Models) {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		return provider, provider.Models[m.selectedModel], true
	case "alias_variants":
		if m.selectedAlias < 0 || m.selectedAlias >= len(m.aliasEntries) {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		var results groupedResults = m.aliasVariantResults(m.selectedAlias)
		if len(results.Indices) == 0 {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		m.ensureSelection(results.Indices, &m.selectedVariant)
		var variants []commands.ModelAliasVariant = m.aliasEntries[m.selectedAlias].Variants
		if m.selectedVariant < 0 || m.selectedVariant >= len(variants) {
			return commands.Provider{}, commands.ModelInfo{}, false
		}
		var provider commands.Provider = commands.Provider{
			Name:        variants[m.selectedVariant].ProviderName,
			DisplayName: variants[m.selectedVariant].ProviderDisplayName,
			Color:       variants[m.selectedVariant].ProviderColor,
			Available:   variants[m.selectedVariant].ProviderAvailable,
		}
		var found *commands.Provider = m.providerByName(variants[m.selectedVariant].ProviderName)
		if found != nil {
			provider.APIType = found.APIType
			provider.BaseURL = found.BaseURL
			provider.Available = found.Available
			if provider.DisplayName == "" {
				provider.DisplayName = found.DisplayName
			}
		}
		return provider, variants[m.selectedVariant].Model, true
	default:
		return commands.Provider{}, commands.ModelInfo{}, false
	}
}

func (m *ModelSettings) ensureReadmeCmd() tea.Cmd {
	var provider commands.Provider
	var model commands.ModelInfo
	var ok bool
	provider, model, ok = m.currentDetailModel()
	if !ok {
		return nil
	}
	if strings.TrimSpace(model.ID) == "" {
		return nil
	}
	if m.readmeCache == nil {
		m.readmeCache = make(map[string]commands.ModelReadmeEntry)
	}
	var key string = commands.ModelReadmeKey(provider.Name, model.ID)
	var entry commands.ModelReadmeEntry
	var exists bool
	entry, exists = m.readmeCache[key]
	if exists {
		if entry.Loading || entry.Text != "" || entry.Err != "" {
			return nil
		}
	}
	entry.Loading = true
	m.readmeCache[key] = entry
	return commands.FetchModelReadmeCmd(provider, model)
}

func (m *ModelSettings) providerDisplayName(name string) string {
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

func (m *ModelSettings) isFavorite(provider, model string) bool {
	return commands.IndexOfModelRef(m.favoriteModels, provider, model) >= 0
}

func (m *ModelSettings) isRecent(provider, model string) bool {
	return commands.IndexOfModelRef(m.recentModels, provider, model) >= 0
}

func (m *ModelSettings) toggleFavorite(provider, model string) bool {
	if m.configManager == nil {
		return m.isFavorite(provider, model)
	}
	cfg, err := m.configManager.UpdateConfig(func(cfg *commands.SwarmOSConfig) {
		cfg.FavoriteModels, _ = commands.ToggleFavoriteModels(cfg.FavoriteModels, provider, model)
	})
	if err == nil && cfg != nil {
		m.favoriteModels = cfg.FavoriteModels
		m.recentModels = cfg.RecentModels
	}
	return m.isFavorite(provider, model)
}

func (m *ModelSettings) handleFilterKey(key string) bool {
	if !m.tabsEnabled() {
		return false
	}
	switch key {
	case "1", "2", "3", "4", "5":
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
		return true
	case "p":
		m.cycleProviderFilter()
		return true
	case "c":
		m.cycleContextFilter()
		return true
	case "s":
		m.toggleTagFilter(commands.TagFast)
		return true
	case "K":
		m.toggleTagFilter(commands.TagCoding)
		return true
	case "v":
		m.toggleTagFilter(commands.TagVision)
		return true
	case "t":
		m.toggleTagFilter(commands.TagTools)
		return true
	case "f":
		var provider string
		var model string
		var ok bool
		provider, model, ok = m.selectedModelRef()
		if ok {
			_ = m.toggleFavorite(provider, model)
			m.scrollOffset = 0
			m.syncSelectionToSearch()
		}
		return true
	case "g":
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
		return true
	case "G":
		var results groupedResults
		var ok bool
		results, ok = m.groupedResultsForState()
		if ok {
			m.setGroupsCollapsed(results.Groups, true)
			m.syncSelectionToSearch()
		}
		return true
	case "E":
		var results groupedResults
		var ok bool
		results, ok = m.groupedResultsForState()
		if ok {
			m.setGroupsCollapsed(results.Groups, false)
			m.syncSelectionToSearch()
		}
		return true
	default:
		return false
	}
}

func (m *ModelSettings) clearSearchIfStateChanged(previous string) {
	if m.state == previous {
		return
	}
	m.searchQuery = ""
	m.searchActive = false
	m.scrollOffset = 0
}

func (m *ModelSettings) renderSearchLine(th Theme) string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 2)
	label := labelStyle.Render(i18n.T("settings.residual_final.model.search.label"))

	queryStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text))
	if m.searchQuery == "" {
		queryStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		placeholder := i18n.T("settings.residual_final.model.search.placeholder")
		if m.searchActive {
			placeholder = "|"
			queryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
		}
		return lipgloss.JoinHorizontal(lipgloss.Left, label, " ", queryStyle.Render(placeholder))
	}

	cursor := ""
	if m.searchActive {
		cursor = "|"
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, label, " ", queryStyle.Render(m.searchQuery+cursor))
}

func (m *ModelSettings) renderSearchBar(width int, th Theme) string {
	content := m.searchQuery
	if content == "" {
		content = i18n.T("settings.residual_final.model.search.editing")
		if m.searchActive {
			content = ""
		}
	}
	if m.searchActive {
		content = content + "|"
	}
	fieldStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 1)
	if m.searchQuery == "" {
		fieldStyle = fieldStyle.Foreground(lipgloss.Color(th.TextMuted))
	}
	if width > 0 {
		fieldStyle = fieldStyle.Width(width)
	}
	return fieldStyle.Render(content)
}

func (m *ModelSettings) renderStatusChips(provider string, model string, th Theme) string {
	var chips []string
	if provider == m.currentProvider && model == m.currentModel {
		chips = append(chips, renderToggleChip(i18n.T("settings.residual_final.model.filter.active"), true, th.Success, th))
	}
	if m.isFavorite(provider, model) {
		chips = append(chips, renderToggleChip(i18n.T("settings.residual_final.model.filter.favorite"), true, th.Warning, th))
	}
	if m.isRecent(provider, model) {
		chips = append(chips, renderToggleChip(i18n.T("settings.residual_final.model.filter.recent"), true, th.Primary, th))
	}
	if len(chips) == 0 {
		return ""
	}
	return strings.Join(chips, " ")
}

func (m *ModelSettings) renderToolbar(width int, th Theme) string {
	if m.filterTags == nil {
		m.filterTags = make(map[commands.ModelTag]bool)
	}
	toggles := []string{
		renderToggleChip(i18n.T("settings.residual_final.model.filter.fast"), m.filterTags[commands.TagFast], palette.Warning, th),
		renderToggleChip(i18n.T("settings.residual_final.model.filter.code"), m.filterTags[commands.TagCoding], palette.Info, th),
		renderToggleChip(i18n.T("settings.residual_final.model.filter.vision"), m.filterTags[commands.TagVision], palette.Success, th),
		renderToggleChip(i18n.T("settings.residual_final.model.filter.tools"), m.filterTags[commands.TagTools], palette.Teal, th),
	}
	toggleLine := strings.Join(toggles, " ")
	toggleWidth := lipgloss.Width(toggleLine)
	searchWidth := width - toggleWidth - 2
	if searchWidth < 20 {
		searchWidth = width
	}
	searchBar := m.renderSearchBar(searchWidth, th)

	if searchWidth == width {
		return lipgloss.JoinVertical(lipgloss.Left, searchBar, toggleLine)
	}
	return joinLeftRight(width, searchBar, toggleLine)
}

func (m *ModelSettings) renderTabChips(th Theme) string {
	if !m.tabsEnabled() {
		return ""
	}
	tabLabels := []string{
		i18n.T("settings.residual_final.model.tab.fast_cheap"),
		i18n.T("settings.residual_final.model.tab.long_context"),
		i18n.T("settings.residual_final.model.tab.coding"),
		i18n.T("settings.residual_final.model.filter.vision"),
		i18n.T("settings.residual_final.model.tab.all"),
	}
	var parts []string
	for i, label := range tabLabels {
		active := m.normalizedTabIndex() == i
		parts = append(parts, renderToggleChip(label, active, th.Primary, th))
	}
	return strings.Join(parts, " ")
}

func (m *ModelSettings) renderFilterChips(showProvider bool, th Theme) string {
	var parts []string
	if strings.TrimSpace(m.searchQuery) != "" {
		parts = append(parts, renderNeutralChip(i18n.T("settings.residual_final.model.sort.best_match"), th))
	} else {
		parts = append(parts, renderNeutralChip(i18n.T("settings.residual_final.model.sort.catalog"), th))
	}
	if showProvider {
		label := i18n.T("settings.residual_final.model.provider_all")
		if m.filterProvider != "" {
			label = i18n.T("settings.residual_final.model.provider_value", m.providerDisplayName(m.filterProvider))
		}
		parts = append(parts, renderToggleChip(label, m.filterProvider != "", th.Primary, th))
	}
	_, _, contextLabel := m.contextFilterRange()
	ctxLabel := i18n.T("settings.residual_final.model.context_value", contextLabel)
	parts = append(parts, renderToggleChip(ctxLabel, m.filterContext != commands.ContextFilterAny, th.Primary, th))
	return strings.Join(parts, " ")
}

func (m *ModelSettings) providerByName(name string) *commands.Provider {
	for i := range m.providers {
		if strings.EqualFold(m.providers[i].Name, name) {
			return &m.providers[i]
		}
	}
	return nil
}

func (m *ModelSettings) currentProviderModel() (*commands.Provider, *commands.ModelInfo) {
	var providerCfg *commands.Provider = m.providerByName(m.currentProvider)
	if providerCfg == nil {
		return nil, nil
	}

	for i := range providerCfg.Models {
		if strings.EqualFold(providerCfg.Models[i].ID, m.currentModel) {
			return providerCfg, &providerCfg.Models[i]
		}
	}

	return providerCfg, nil
}

func (m *ModelSettings) renderModelDetail(provider commands.Provider, model commands.ModelInfo, width int, th Theme) string {
	innerWidth := width - 4
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
		contextLabel = i18n.T("settings.residual_final.common.not_available")
	}

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	statusChips := m.renderStatusChips(provider.Name, model.ID, th)
	titleLine := joinLeftRight(innerWidth, titleStyle.Render(name), statusChips)
	metaLine := joinLeftRight(innerWidth, metaStyle.Render(providerLabel+" • "+model.ID), metaStyle.Render(i18n.T("settings.residual_final.model.context_inline", contextLabel)))

	tags := commands.InferModelTags(provider.Name, model)
	capLine := renderTagChips(tags, th)
	if capLine != "" {
		capLine = metaStyle.Render(i18n.T("settings.residual_final.model.capabilities")) + " " + capLine
	}

	var descTitle string = i18n.T("settings.residual_final.common.description")
	var descLines []string
	var descStyle lipgloss.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	var mutedStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true)
	var readmeKey string = commands.ModelReadmeKey(provider.Name, model.ID)
	var readmeEntry commands.ModelReadmeEntry
	var hasReadme bool
	if m.readmeCache != nil {
		readmeEntry, hasReadme = m.readmeCache[readmeKey]
	}
	var wrapWidth int = innerWidth - 2
	if wrapWidth < 20 {
		wrapWidth = innerWidth
	}
	if hasReadme {
		if readmeEntry.Source != "" {
			descTitle = i18n.T("settings.residual_final.model.description_source", readmeEntry.Source)
		}
		switch {
		case readmeEntry.Loading:
			descLines = []string{mutedStyle.Render(i18n.T("settings.residual_final.model.description_fetching"))}
		case strings.TrimSpace(readmeEntry.Text) == "":
			descLines = []string{mutedStyle.Render(i18n.T("settings.residual_final.model.description_empty"))}
		default:
			var linkBase string = commands.ReadmeLinkBaseURL(provider)
			var lines []string
			var targets map[string]string
			lines, targets = commands.WrapReadmeLinesWithTargets(readmeEntry.Text, wrapWidth, linkBase, "settings-model-readme")
			if len(targets) > 0 {
				if m.readmeLinkTargets == nil {
					m.readmeLinkTargets = make(map[string]string, len(targets))
				}
				maps.Copy(m.readmeLinkTargets, targets)
			}
			var idx int
			for idx = 0; idx < len(lines); idx++ {
				descLines = append(descLines, descStyle.Render(lines[idx]))
			}
		}
	} else {
		descLines = []string{mutedStyle.Render(i18n.T("settings.residual_final.model.description_fetching"))}
	}

	infoLines := []string{
		i18n.T("settings.residual_final.model.provider_value", providerLabel),
		i18n.T("settings.residual_final.model.model_id_value", model.ID),
		i18n.T("settings.residual_final.model.context_value", contextLabel),
	}
	if provider.APIType != "" {
		infoLines = append(infoLines, i18n.T("settings.residual_final.model.api_value", provider.APIType))
	}
	if provider.BaseURL != "" {
		infoLines = append(infoLines, i18n.T("settings.residual_final.model.endpoint_value", provider.BaseURL))
	}

	status := i18n.T("settings.residual_final.common.ready_upper")
	if !provider.Available {
		status = i18n.T("settings.residual_final.common.not_configured_upper")
	}
	availabilityLines := []string{
		i18n.T("settings.residual_final.common.status_value", status),
	}

	sections := []string{
		titleLine,
		metaLine,
	}
	if capLine != "" {
		sections = append(sections, capLine)
	}
	sections = append(sections, "")
	sections = append(sections, renderDetailCard(descTitle, descLines, innerWidth, th))
	sections = append(sections, renderDetailCard(i18n.T("settings.residual_final.model.information"), infoLines, innerWidth, th))
	sections = append(sections, renderDetailCard(i18n.T("settings.residual_final.model.availability"), availabilityLines, innerWidth, th))

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return renderPanel(content, width, th)
}

func (m *ModelSettings) renderAliasDetail(alias commands.ModelAliasEntry, width int, th Theme) string {
	innerWidth := width - 4
	if innerWidth < 10 {
		innerWidth = width
	}
	name := alias.DisplayName
	if name == "" {
		name = alias.Name
	}
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))

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
	maxLen := innerWidth - 10
	if maxLen > 0 && len(displayProviders) > maxLen {
		if maxLen <= 3 {
			displayProviders = displayProviders[:maxLen]
		} else {
			displayProviders = displayProviders[:maxLen-3] + "..."
		}
	}

	contextLabel := commands.AliasContextLabel(alias.Variants)
	if contextLabel == "" {
		contextLabel = i18n.T("settings.residual_final.common.not_available")
	}

	var repProvider commands.Provider
	var repModel commands.ModelInfo
	var repOK bool
	repProvider, repModel, repOK = commands.AliasRepresentativeModel(alias, m.currentProvider, m.providers)

	tags := aliasVariantTags(alias.Variants)
	capLine := renderTagChips(tags, th)
	if capLine != "" {
		capLine = metaStyle.Render(i18n.T("settings.residual_final.model.capabilities")) + " " + capLine
	}

	header := titleStyle.Render(name)
	meta := metaStyle.Render(i18n.T("settings.residual_final.model.providers_context", len(alias.Variants), contextLabel))
	var representativeLabel string = i18n.T("settings.residual_final.common.not_available")
	if repOK {
		repProviderLabel := repProvider.DisplayName
		if repProviderLabel == "" {
			repProviderLabel = repProvider.Name
		}
		representativeLabel = fmt.Sprintf("%s • %s", repProviderLabel, repModel.ID)
	}
	infoLines := []string{
		i18n.T("settings.residual_final.model.representative_value", representativeLabel),
		i18n.T("settings.residual_final.model.providers_value", displayProviders),
		i18n.T("settings.residual_final.model.context_value", contextLabel),
	}

	var descTitle string = i18n.T("settings.residual_final.common.description")
	var descLines []string
	var descStyle lipgloss.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	var mutedStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true)
	var wrapWidth int = innerWidth - 2
	if wrapWidth < 20 {
		wrapWidth = innerWidth
	}
	if repOK {
		var readmeKey string = commands.ModelReadmeKey(repProvider.Name, repModel.ID)
		var readmeEntry commands.ModelReadmeEntry
		var hasReadme bool
		if m.readmeCache != nil {
			readmeEntry, hasReadme = m.readmeCache[readmeKey]
		}
		if hasReadme {
			if readmeEntry.Source != "" {
				descTitle = i18n.T("settings.residual_final.model.description_source", readmeEntry.Source)
			}
			switch {
			case readmeEntry.Loading:
				descLines = []string{mutedStyle.Render(i18n.T("settings.residual_final.model.description_fetching"))}
			case strings.TrimSpace(readmeEntry.Text) == "":
				descLines = []string{mutedStyle.Render(i18n.T("settings.residual_final.model.description_empty"))}
			default:
				var linkBase string = commands.ReadmeLinkBaseURL(repProvider)
				var lines []string
				var targets map[string]string
				lines, targets = commands.WrapReadmeLinesWithTargets(readmeEntry.Text, wrapWidth, linkBase, "settings-alias-readme")
				if len(targets) > 0 {
					if m.readmeLinkTargets == nil {
						m.readmeLinkTargets = make(map[string]string, len(targets))
					}
					maps.Copy(m.readmeLinkTargets, targets)
				}
				var idx int
				for idx = 0; idx < len(lines); idx++ {
					descLines = append(descLines, descStyle.Render(lines[idx]))
				}
			}
		} else {
			descLines = []string{mutedStyle.Render(i18n.T("settings.residual_final.model.description_fetching"))}
		}
	} else {
		descLines = []string{mutedStyle.Render(i18n.T("settings.residual_final.model.description_empty"))}
	}

	sections := []string{header, meta}
	if capLine != "" {
		sections = append(sections, capLine)
	}
	sections = append(sections, "")
	sections = append(sections, renderDetailCard(descTitle, descLines, innerWidth, th))
	sections = append(sections, renderDetailCard(i18n.T("settings.residual_final.model.family"), infoLines, innerWidth, th))
	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return renderPanel(content, width, th)
}

func (m *ModelSettings) renderModelBadges(provider string, model string, th Theme) string {
	var badges []string = make([]string, 0, 3)
	var favoriteStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Warning)).
		Bold(true)
	var recentStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	var diffusionStyle lipgloss.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Success)).
		Bold(true)

	if m.isFavorite(provider, model) {
		badges = append(badges, favoriteStyle.Render("★"))
	}
	if m.isRecent(provider, model) {
		badges = append(badges, recentStyle.Render("⟳"))
	}
	if m.isDiffusionModel(provider, model) {
		badges = append(badges, diffusionStyle.Render("∿"))
	}
	if len(badges) == 0 {
		return ""
	}
	return strings.Join(badges, " ")
}

// isDiffusionModel reports whether the given provider/model is flagged as a
// text-diffusion model in the loaded provider list.
func (m *ModelSettings) isDiffusionModel(provider string, model string) bool {
	for _, p := range m.providers {
		if !strings.EqualFold(p.Name, provider) {
			continue
		}
		for _, mdl := range p.Models {
			if strings.EqualFold(mdl.ID, model) {
				return mdl.Diffusion
			}
		}
	}
	return false
}

// SetModel sets the current model and notifies the SDK via callback.
func (m *ModelSettings) SetModel(provider, model string) {
	// If profile manager is available, check if this is an override of the active profile
	if m.profileManager != nil {
		activeProfile, err := m.profileManager.GetActiveProfile()
		if err == nil && activeProfile != nil {
			// Resolve the main model for this profile
			pointer, err := m.profileManager.ResolveAlias(AliasMain)
			if err == nil {
				// If the selected model is different from the profile's main model,
				// and we haven't already confirmed this override, show the warning.
				if (pointer.Provider != provider || pointer.Model != model) && !m.confirmOverride {
					m.pendingProvider = provider
					m.pendingModel = model
					m.confirmOverride = true
					m.confirmSelected = 1 // Default to Override
					return
				}
			}
		}
	}

	m.applyModel(provider, model, false)
}

func (m *ModelSettings) applyModel(provider, model string, saveToProfile bool) {
	// If requested, save to the active profile permanently
	if saveToProfile && m.profileManager != nil {
		activeProfile, err := m.profileManager.GetActiveProfile()
		if err == nil && activeProfile != nil {
			rc := NewRoleConfig(provider, model)
			if err := m.profileManager.SetRoleInProfile(activeProfile.ID, AliasMain, rc); err != nil {
				logDebug("Failed to save to profile: %v", err)
			} else {
				logDebug("Saved model %s/%s to profile %s", provider, model, activeProfile.ID)
			}
		}
	}

	m.currentProvider = provider
	m.currentModel = model
	m.state = "menu"
	m.selectedMenuItem = 0
	m.selectedProvider = 0
	m.selectedModel = 0
	m.selectedAlias = 0
	m.selectedVariant = 0
	m.scrollOffset = 0
	m.searchQuery = ""
	m.searchActive = false
	m.confirmOverride = false // Clear override state

	// Save to config
	if m.configManager != nil {
		if _, err := m.configManager.UpdateCurrentModel(provider, model); err != nil {
			logDebug("Failed to save model config: %v", err)
		}
		if cfg, err := m.configManager.LoadConfig(); err == nil && cfg != nil {
			m.reasoningEffort = cfg.GetReasoningEffortForModel(provider, model)
		}
	}
	m.reloadModelPrefs()

	if m.onReasoningEffortChange != nil {
		m.onReasoningEffortChange(m.GetReasoningEffort())
	}

	// CRITICAL: Notify the app so it can update the SDK
	// Without this, the SDK doesn't know about model changes made through settings
	if m.onModelSelect != nil {
		m.onModelSelect(provider, model)
	}
}

// SyncCurrent updates the displayed current provider/model without triggering
// callbacks or saving to config. Used for runtime overrides like fallback chain
// switches where the SDK already knows about the change.
func (m *ModelSettings) SyncCurrent(provider, model string) {
	m.currentProvider = provider
	m.currentModel = model
}

// SetOnModelSelect sets the callback that's invoked when a model is selected.
// This callback should update the SDK's provider/model to keep it in sync.
func (m *ModelSettings) SetOnModelSelect(fn func(provider, model string)) {
	m.onModelSelect = fn
}

// SetOnCredentialsChanged sets the callback invoked after a provider's
// credentials (API key / base URL) are saved. The app uses this to rebuild
// the live SDK provider client so changes take effect without a restart.
func (m *ModelSettings) SetOnCredentialsChanged(fn func(providerName string)) {
	m.onCredentialsChanged = fn
}

// SetVaultCredentialCallbacks connects provider settings to the currently
// unlocked encrypted vault without coupling ModelSettings to vault internals.
func (m *ModelSettings) SetVaultCredentialCallbacks(
	store func(providerName, secret string) (string, error),
	resolve func(secretRef string) (string, error),
	deleteFn func(secretRef string) error,
) {
	m.storeProviderSecret = store
	m.resolveProviderSecret = resolve
	m.deleteProviderSecret = deleteFn
}

// SetProfileManager sets the profile manager for override detection
func (m *ModelSettings) SetProfileManager(pm *ProfileManager) {
	m.profileManager = pm
}

// SetConfigManager sets the config manager (useful for testing)
func (m *ModelSettings) SetConfigManager(cm *commands.ConfigManager) {
	m.configManager = cm
}
