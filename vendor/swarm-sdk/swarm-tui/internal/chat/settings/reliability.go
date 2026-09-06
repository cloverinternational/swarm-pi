package settings

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

const (
	reliabilityModeMain          = "main"
	reliabilityModeFallback      = "fallback"
	reliabilityModeRateLimitEdit = "rate_limit_edit"
)

const (
	reliabilityItemRetryEnabled = iota
	reliabilityItemMaxRetries
	reliabilityItemRotateOnRateLimit
	reliabilityItemRetryAfterFallback
	reliabilityItemFallbackChain
)

const (
	rateLimitFieldProvider = iota
	rateLimitFieldModel
	rateLimitFieldRPM
	rateLimitFieldEnabled
	rateLimitFieldBurst
	rateLimitFieldSave
	rateLimitFieldCancel
)

// ReliabilitySettings handles retry, fallback, and per-model rate-limit settings.
type ReliabilitySettings struct {
	configManager *commands.ConfigManager
	picker        *FallbackPicker
	providers     []commands.ProviderConfig

	retry      commands.RetrySettingsConfig
	rateLimits []commands.ModelRateLimitConfig

	mode                 string
	hasChanges           bool
	statusMessage        string
	rateLimitField       int
	editingRateLimitIdx  int
	draftRateLimitConfig commands.ModelRateLimitConfig
}

// NewReliabilitySettings creates a new reliability settings handler.
func NewReliabilitySettings(configManagers ...*commands.ConfigManager) *ReliabilitySettings {
	var cm *commands.ConfigManager
	if len(configManagers) > 0 && configManagers[0] != nil {
		cm = configManagers[0]
	} else {
		cm, _ = commands.NewConfigManager()
	}
	r := &ReliabilitySettings{
		configManager:       cm,
		mode:                reliabilityModeMain,
		editingRateLimitIdx: -1,
	}
	r.ReloadFromConfig()
	return r
}

// ReloadFromConfig reloads settings from the configuration file
func (r *ReliabilitySettings) ReloadFromConfig() {
	cfg := &commands.SwarmOSConfig{}
	if r.configManager != nil {
		if loaded, err := r.configManager.LoadConfig(); err == nil && loaded != nil {
			cfg = loaded
		}
	}

	r.retry = cfg.GetRetrySettings()
	r.rateLimits = cloneRateLimits(cfg.GetModelRateLimits())
	r.reloadProviders()
	r.setPicker(cfg.GetChatFallbackChain())
}

func (r *ReliabilitySettings) reloadProviders() {
	if r.configManager == nil {
		return
	}
	providers, err := r.configManager.LoadProviders()
	if err == nil {
		r.providers = providers
	}
}

func (r *ReliabilitySettings) setPicker(chain *fallback.Chain) {
	if chain == nil || chain.IsEmpty() {
		chain = fallback.NewChainWithDefaults()
	}

	r.picker = NewFallbackPicker(chain)
	r.picker.SetOnSave(func(updatedChain *fallback.Chain) {
		if err := r.SetFallbackChain(updatedChain); err != nil {
			r.statusMessage = fmt.Sprintf("Failed to save fallback chain: %v", err)
			return
		}
		r.statusMessage = "Fallback chain saved."
	})
}

func cloneRateLimits(in []commands.ModelRateLimitConfig) []commands.ModelRateLimitConfig {
	if len(in) == 0 {
		return []commands.ModelRateLimitConfig{}
	}
	out := make([]commands.ModelRateLimitConfig, len(in))
	copy(out, in)
	return out
}

func (r *ReliabilitySettings) updateConfig(update func(*commands.SwarmOSConfig)) error {
	if r.configManager == nil {
		return errors.New("config manager unavailable")
	}

	cfg, err := r.configManager.LoadConfig()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &commands.SwarmOSConfig{}
	}

	update(cfg)
	if err := r.configManager.SaveConfig(cfg); err != nil {
		return err
	}

	r.hasChanges = false
	return nil
}

// GetRetrySettings returns current retry settings state.
func (r *ReliabilitySettings) GetRetrySettings() commands.RetrySettingsConfig {
	return r.retry
}

// ToggleRetryEnabled toggles retry behavior.
func (r *ReliabilitySettings) ToggleRetryEnabled() {
	r.retry.Enabled = !r.retry.Enabled
	if err := r.saveRetrySettings(); err != nil {
		r.statusMessage = fmt.Sprintf("Failed to save retry settings: %v", err)
	}
}

// ToggleRotateOnRateLimit toggles provider rotation after 429s.
func (r *ReliabilitySettings) ToggleRotateOnRateLimit() {
	r.retry.RotateOnRateLimit = !r.retry.RotateOnRateLimit
	if err := r.saveRetrySettings(); err != nil {
		r.statusMessage = fmt.Sprintf("Failed to save retry settings: %v", err)
	}
}

// AdjustMaxRetries changes max retries per provider by delta.
func (r *ReliabilitySettings) AdjustMaxRetries(delta int) {
	r.retry.MaxRetriesPerProvider = clampRangeInt(r.retry.MaxRetriesPerProvider+delta, 1, 20)
	if err := r.saveRetrySettings(); err != nil {
		r.statusMessage = fmt.Sprintf("Failed to save retry settings: %v", err)
	}
}

// AdjustRetryAfterFallbackMs changes the retry delay by delta milliseconds.
func (r *ReliabilitySettings) AdjustRetryAfterFallbackMs(delta int) {
	r.retry.RetryAfterFallbackMs = clampRangeInt(r.retry.RetryAfterFallbackMs+delta, 1000, 300000)
	if err := r.saveRetrySettings(); err != nil {
		r.statusMessage = fmt.Sprintf("Failed to save retry settings: %v", err)
	}
}

func (r *ReliabilitySettings) saveRetrySettings() error {
	current := r.retry
	return r.updateConfig(func(cfg *commands.SwarmOSConfig) {
		cfg.SetRetrySettings(current)
	})
}

// Save persists the current reliability settings to config.json.
// Call this after one or more mutations (Toggle, Adjust, Add/Update/Delete) to
// commit the changes to disk.  Without Save, changes only live in memory.
func (r *ReliabilitySettings) Save() error {
	if r == nil || r.configManager == nil {
		return nil
	}

	cfg, err := r.configManager.LoadConfig()
	if err != nil || cfg == nil {
		cfg = &commands.SwarmOSConfig{}
	}

	cfg.SetRetrySettings(r.retry)
	cfg.SetChatFallbackChain(r.GetFallbackChain())
	cfg.SetModelRateLimits(r.rateLimits)

	if err := r.configManager.SaveConfig(cfg); err != nil {
		return fmt.Errorf("reliability settings: save failed: %w", err)
	}

	r.hasChanges = false
	return nil
}

// GetFallbackChain returns the current primary chat fallback chain.
func (r *ReliabilitySettings) GetFallbackChain() *fallback.Chain {
	if r.picker == nil || r.picker.GetChain() == nil {
		return fallback.NewChainWithDefaults()
	}
	return r.picker.GetChain()
}

// SetFallbackChain updates and persists the primary chat fallback chain.
func (r *ReliabilitySettings) SetFallbackChain(chain *fallback.Chain) error {
	if chain == nil || chain.IsEmpty() {
		return errors.New("fallback chain cannot be empty")
	}

	if err := r.updateConfig(func(cfg *commands.SwarmOSConfig) {
		cfg.SetChatFallbackChain(chain)
	}); err != nil {
		return err
	}

	r.setPicker(chain)
	return nil
}

// GetModelRateLimits returns current per-model rate limits.
func (r *ReliabilitySettings) GetModelRateLimits() []commands.ModelRateLimitConfig {
	return cloneRateLimits(r.rateLimits)
}

func normalizeRateLimit(limit commands.ModelRateLimitConfig) (commands.ModelRateLimitConfig, error) {
	limit.Provider = strings.TrimSpace(limit.Provider)
	limit.Model = strings.TrimSpace(limit.Model)

	switch {
	case limit.Provider == "":
		return commands.ModelRateLimitConfig{}, errors.New("provider is required")
	case limit.Model == "":
		return commands.ModelRateLimitConfig{}, errors.New("model is required")
	case limit.RequestsPerMinute <= 0:
		return commands.ModelRateLimitConfig{}, errors.New("requests per minute must be greater than zero")
	}

	if limit.Burst < 0 {
		limit.Burst = 0
	}
	return limit, nil
}

// AddRateLimit appends a new per-model rate limit and persists it.
func (r *ReliabilitySettings) AddRateLimit(limit commands.ModelRateLimitConfig) error {
	normalized, err := normalizeRateLimit(limit)
	if err != nil {
		return err
	}

	updated := append(cloneRateLimits(r.rateLimits), normalized)
	return r.persistRateLimits(updated)
}

// UpdateRateLimit updates an existing per-model rate limit by index and persists it.
func (r *ReliabilitySettings) UpdateRateLimit(index int, limit commands.ModelRateLimitConfig) error {
	if index < 0 || index >= len(r.rateLimits) {
		return fmt.Errorf("rate limit index out of range: %d", index)
	}

	normalized, err := normalizeRateLimit(limit)
	if err != nil {
		return err
	}

	updated := cloneRateLimits(r.rateLimits)
	updated[index] = normalized
	return r.persistRateLimits(updated)
}

// DeleteRateLimit removes a per-model rate limit by index and persists the result.
func (r *ReliabilitySettings) DeleteRateLimit(index int) error {
	if index < 0 || index >= len(r.rateLimits) {
		return fmt.Errorf("rate limit index out of range: %d", index)
	}

	updated := cloneRateLimits(r.rateLimits)
	updated = append(updated[:index], updated[index+1:]...)
	return r.persistRateLimits(updated)
}

func (r *ReliabilitySettings) persistRateLimits(limits []commands.ModelRateLimitConfig) error {
	if err := r.updateConfig(func(cfg *commands.SwarmOSConfig) {
		cfg.SetModelRateLimits(limits)
	}); err != nil {
		return err
	}
	r.rateLimits = cloneRateLimits(limits)
	return nil
}

func (r *ReliabilitySettings) rateLimitStartIndex() int {
	return reliabilityItemFallbackChain + 1
}

func (r *ReliabilitySettings) addRateLimitIndex() int {
	return r.rateLimitStartIndex() + len(r.rateLimits)
}

func (r *ReliabilitySettings) totalItems() int {
	return r.addRateLimitIndex() + 1
}

func (r *ReliabilitySettings) isRateLimitItem(index int) bool {
	return index >= r.rateLimitStartIndex() && index < r.addRateLimitIndex()
}

func (r *ReliabilitySettings) rateLimitListIndex(itemIndex int) int {
	return itemIndex - r.rateLimitStartIndex()
}

func (r *ReliabilitySettings) ensureSelectedItemInRange(state *State) {
	if state == nil {
		return
	}
	maxIdx := r.totalItems() - 1
	if maxIdx < 0 {
		maxIdx = 0
	}
	if state.SelectedItem < 0 {
		state.SelectedItem = 0
	}
	if state.SelectedItem > maxIdx {
		state.SelectedItem = maxIdx
	}
}

// IsInNestedState returns true if reliability settings is in a sub-mode (fallback or rate limit edit).
func (r *ReliabilitySettings) IsInNestedState() bool {
	return r.mode != reliabilityModeMain
}

// HandleKey processes keyboard input for reliability settings.
func (r *ReliabilitySettings) HandleKey(key string, state *State) bool {
	if state == nil || state.Focus != FocusContent {
		return false
	}

	switch r.mode {
	case reliabilityModeFallback:
		return r.handleFallbackKey(key, state)
	case reliabilityModeRateLimitEdit:
		return r.handleRateLimitEditKey(key, state)
	default:
		return r.handleMainKey(key, state)
	}
}

func (r *ReliabilitySettings) handleMainKey(key string, state *State) bool {
	r.ensureSelectedItemInRange(state)
	totalItems := r.totalItems()
	currentItem := state.SelectedItem

	switch key {
	case "up", "k":
		if state.SelectedItem > 0 {
			state.SelectedItem--
		}
		return true
	case "down", "j":
		if state.SelectedItem < totalItems-1 {
			state.SelectedItem++
		}
		return true
	case "left", "h", "-":
		switch currentItem {
		case reliabilityItemMaxRetries:
			r.AdjustMaxRetries(-1)
			r.statusMessage = "Updated max retries."
			return true
		case reliabilityItemRetryAfterFallback:
			r.AdjustRetryAfterFallbackMs(-1000)
			r.statusMessage = "Updated retry-after-fallback delay."
			return true
		default:
			if r.isRateLimitItem(currentItem) {
				idx := r.rateLimitListIndex(currentItem)
				limit := r.rateLimits[idx]
				limit.RequestsPerMinute = clampRangeInt(limit.RequestsPerMinute-1, 1, 60000)
				if err := r.UpdateRateLimit(idx, limit); err != nil {
					r.statusMessage = fmt.Sprintf("Failed to update rate limit: %v", err)
				} else {
					r.statusMessage = "Updated rate limit requests per minute."
				}
				return true
			}
		}
	case "right", "l", "+", "=":
		switch currentItem {
		case reliabilityItemMaxRetries:
			r.AdjustMaxRetries(1)
			r.statusMessage = "Updated max retries."
			return true
		case reliabilityItemRetryAfterFallback:
			r.AdjustRetryAfterFallbackMs(1000)
			r.statusMessage = "Updated retry-after-fallback delay."
			return true
		default:
			if r.isRateLimitItem(currentItem) {
				idx := r.rateLimitListIndex(currentItem)
				limit := r.rateLimits[idx]
				limit.RequestsPerMinute = clampRangeInt(limit.RequestsPerMinute+1, 1, 60000)
				if err := r.UpdateRateLimit(idx, limit); err != nil {
					r.statusMessage = fmt.Sprintf("Failed to update rate limit: %v", err)
				} else {
					r.statusMessage = "Updated rate limit requests per minute."
				}
				return true
			}
		}
	case " ", "space", "enter":
		switch {
		case currentItem == reliabilityItemRetryEnabled:
			r.ToggleRetryEnabled()
			r.statusMessage = "Updated retry enabled setting."
			return true
		case currentItem == reliabilityItemRotateOnRateLimit:
			r.ToggleRotateOnRateLimit()
			r.statusMessage = "Updated rotate-on-rate-limit setting."
			return true
		case currentItem == reliabilityItemFallbackChain:
			r.mode = reliabilityModeFallback
			r.statusMessage = ""
			return true
		case r.isRateLimitItem(currentItem):
			r.startRateLimitEdit(r.rateLimitListIndex(currentItem))
			return true
		case currentItem == r.addRateLimitIndex():
			r.startRateLimitEdit(-1)
			return true
		}
	case "d", "delete", "backspace":
		if r.isRateLimitItem(currentItem) {
			idx := r.rateLimitListIndex(currentItem)
			if err := r.DeleteRateLimit(idx); err != nil {
				r.statusMessage = fmt.Sprintf("Failed to delete rate limit: %v", err)
			} else {
				r.statusMessage = "Deleted rate limit."
			}
			r.ensureSelectedItemInRange(state)
			return true
		}
	case "esc":
		state.Focus = FocusSidebar
		return true
	}

	return false
}

func (r *ReliabilitySettings) handleFallbackKey(key string, state *State) bool {
	if key == "tab" {
		r.mode = reliabilityModeMain
		return false
	}

	// Let picker handle ESC first if it's in a nested state (select_provider/select_model)
	if r.picker != nil && r.picker.IsInNestedState() {
		if r.picker.HandleKey(key) {
			return true
		}
	}

	if key == "esc" {
		r.mode = reliabilityModeMain
		return true
	}

	if r.picker != nil && r.picker.HandleKey(key) {
		return true
	}

	switch key {
	case "up", "k":
		r.mode = reliabilityModeMain
		if state.SelectedItem > 0 {
			state.SelectedItem--
		}
		return true
	case "down", "j":
		r.mode = reliabilityModeMain
		if state.SelectedItem < r.totalItems()-1 {
			state.SelectedItem++
		}
		return true
	}

	return false
}

func (r *ReliabilitySettings) startRateLimitEdit(index int) {
	r.mode = reliabilityModeRateLimitEdit
	r.rateLimitField = 0
	r.editingRateLimitIdx = index

	if index >= 0 && index < len(r.rateLimits) {
		r.draftRateLimitConfig = r.rateLimits[index]
		if r.draftRateLimitConfig.RequestsPerMinute <= 0 {
			r.draftRateLimitConfig.RequestsPerMinute = 1
		}
	} else {
		provider, model := r.defaultProviderModel()
		r.draftRateLimitConfig = commands.ModelRateLimitConfig{
			Provider:          provider,
			Model:             model,
			RequestsPerMinute: 10,
			Enabled:           true,
			Burst:             1,
		}
	}

	if strings.TrimSpace(r.draftRateLimitConfig.Provider) == "" || strings.TrimSpace(r.draftRateLimitConfig.Model) == "" {
		provider, model := r.defaultProviderModel()
		r.draftRateLimitConfig.Provider = provider
		r.draftRateLimitConfig.Model = model
	}
}

func (r *ReliabilitySettings) handleRateLimitEditKey(key string, state *State) bool {
	const totalFields = 7

	switch key {
	case "up", "k":
		if r.rateLimitField > 0 {
			r.rateLimitField--
		}
		return true
	case "down", "j":
		if r.rateLimitField < totalFields-1 {
			r.rateLimitField++
		}
		return true
	case "left", "h", "-":
		r.adjustRateLimitEditField(-1)
		return true
	case "right", "l", "+", "=":
		r.adjustRateLimitEditField(1)
		return true
	case " ", "space", "enter":
		switch r.rateLimitField {
		case rateLimitFieldEnabled:
			r.draftRateLimitConfig.Enabled = !r.draftRateLimitConfig.Enabled
			return true
		case rateLimitFieldSave:
			return r.saveRateLimitEdit(state)
		case rateLimitFieldCancel:
			r.mode = reliabilityModeMain
			r.statusMessage = "Cancelled rate limit edit."
			return true
		default:
			r.adjustRateLimitEditField(1)
			return true
		}
	case "esc":
		r.mode = reliabilityModeMain
		r.statusMessage = "Cancelled rate limit edit."
		return true
	}

	return false
}

func (r *ReliabilitySettings) adjustRateLimitEditField(delta int) {
	switch r.rateLimitField {
	case rateLimitFieldProvider:
		r.cycleDraftProvider(delta)
	case rateLimitFieldModel:
		r.cycleDraftModel(delta)
	case rateLimitFieldRPM:
		r.draftRateLimitConfig.RequestsPerMinute = clampRangeInt(r.draftRateLimitConfig.RequestsPerMinute+delta, 1, 60000)
	case rateLimitFieldEnabled:
		r.draftRateLimitConfig.Enabled = !r.draftRateLimitConfig.Enabled
	case rateLimitFieldBurst:
		r.draftRateLimitConfig.Burst = clampRangeInt(r.draftRateLimitConfig.Burst+delta, 0, 1000)
	}
}

func (r *ReliabilitySettings) saveRateLimitEdit(state *State) bool {
	var err error
	if r.editingRateLimitIdx >= 0 {
		err = r.UpdateRateLimit(r.editingRateLimitIdx, r.draftRateLimitConfig)
	} else {
		err = r.AddRateLimit(r.draftRateLimitConfig)
	}
	if err != nil {
		r.statusMessage = fmt.Sprintf("Failed to save rate limit: %v", err)
		return true
	}

	r.mode = reliabilityModeMain
	r.statusMessage = "Saved rate limit."
	if state != nil {
		if r.editingRateLimitIdx >= 0 {
			state.SelectedItem = r.rateLimitStartIndex() + r.editingRateLimitIdx
		} else {
			state.SelectedItem = r.rateLimitStartIndex() + len(r.rateLimits) - 1
		}
		r.ensureSelectedItemInRange(state)
	}
	r.editingRateLimitIdx = -1
	return true
}

func (r *ReliabilitySettings) providerNames() []string {
	names := make([]string, 0, len(r.providers))
	for _, provider := range r.providers {
		name := strings.TrimSpace(provider.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return []string{"ClaudeCode", "Anthropic", "OpenAI", "Gemini"}
	}
	return names
}

func (r *ReliabilitySettings) modelsForProvider(providerName string) []string {
	for _, provider := range r.providers {
		if provider.Name != providerName {
			continue
		}
		models := make([]string, 0, len(provider.Models))
		for _, model := range provider.Models {
			id := strings.TrimSpace(model.ID)
			if id != "" {
				models = append(models, id)
			}
		}
		if len(models) > 0 {
			return models
		}
		break
	}
	if strings.TrimSpace(r.draftRateLimitConfig.Model) != "" {
		return []string{r.draftRateLimitConfig.Model}
	}
	return []string{"default-model"}
}

func (r *ReliabilitySettings) defaultProviderModel() (string, string) {
	if chain := r.GetFallbackChain(); chain != nil && !chain.IsEmpty() {
		return chain.Primary.Provider, chain.Primary.Model
	}
	providers := r.providerNames()
	provider := providers[0]
	models := r.modelsForProvider(provider)
	return provider, models[0]
}

func (r *ReliabilitySettings) cycleDraftProvider(delta int) {
	providers := r.providerNames()
	if len(providers) == 0 {
		return
	}

	current := 0
	for i, name := range providers {
		if name == r.draftRateLimitConfig.Provider {
			current = i
			break
		}
	}
	current = wrapIndex(current+delta, len(providers))
	r.draftRateLimitConfig.Provider = providers[current]

	models := r.modelsForProvider(r.draftRateLimitConfig.Provider)
	r.draftRateLimitConfig.Model = models[0]
}

func (r *ReliabilitySettings) cycleDraftModel(delta int) {
	models := r.modelsForProvider(r.draftRateLimitConfig.Provider)
	if len(models) == 0 {
		return
	}

	current := 0
	for i, model := range models {
		if model == r.draftRateLimitConfig.Model {
			current = i
			break
		}
	}
	current = wrapIndex(current+delta, len(models))
	r.draftRateLimitConfig.Model = models[current]
}

func wrapIndex(index, total int) int {
	if total <= 0 {
		return 0
	}
	index %= total
	if index < 0 {
		index += total
	}
	return index
}

func clampRangeInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// Render renders reliability settings with retry controls, fallback chain, and per-model rate limits.
func (r *ReliabilitySettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)

	const (
		borderWidth      = 2
		containerPadding = 1
		titleHeight      = 4
		hintHeight       = 2
	)

	innerWidth := maxInt(20, width-(borderWidth*2)-(containerPadding*2))
	innerHeight := maxInt(5, height-(borderWidth*2)-(containerPadding*2)-titleHeight-hintHeight)

	title := r.renderTitle(innerWidth, th)

	var content string
	if r.mode == reliabilityModeRateLimitEdit {
		content = r.renderRateLimitEditor(innerWidth, innerHeight, state, th)
	} else {
		content = r.renderMainContent(innerWidth, innerHeight, state, th)
	}

	hints := r.renderHintBar(innerWidth, th)

	fullContent := lipgloss.JoinVertical(lipgloss.Left, title, content, hints)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(0, containerPadding).
		Render(fullContent)
}

func (r *ReliabilitySettings) renderTitle(width int, th Theme) string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center).
		Render(i18n.T("settings.reliability.title"))

	if width < 50 {
		return title
	}

	chain := r.GetFallbackChain()
	fallbackCount := 0
	if chain != nil {
		fallbackCount = len(chain.Fallbacks)
	}

	badgeStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1)

	retryBadge := badgeStyle.Render(i18n.T("settings.reliability.badge.retry", reliabilityBoolLabel(r.retry.Enabled)))
	fallbackBadge := badgeStyle.Render(i18n.T("settings.reliability.badge.fallbacks", fallbackCount))

	var badges string
	if width < 70 {
		badges = lipgloss.JoinHorizontal(lipgloss.Center, retryBadge, " ", fallbackBadge)
	} else {
		rateBadge := badgeStyle.Render(i18n.T("settings.reliability.badge.rate_limits", len(r.rateLimits)))
		badges = lipgloss.JoinHorizontal(lipgloss.Center, retryBadge, " ", fallbackBadge, " ", rateBadge)
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badges),
	)
}

func (r *ReliabilitySettings) renderMainContent(width, height int, state *State, th Theme) string {
	r.ensureSelectedItemInRange(state)

	var lines []string
	lines = append(lines, r.renderSectionHeader(i18n.T("settings.reliability.retry.title"), th))
	lines = append(lines, r.renderKeyValueRow(i18n.T("settings.reliability.retry.enabled"), renderToggle(r.retry.Enabled, isSelected(state, reliabilityItemRetryEnabled), th), isSelected(state, reliabilityItemRetryEnabled), th))
	lines = append(lines, r.renderKeyValueRow(i18n.T("settings.reliability.retry.max"), fmt.Sprintf("%d", r.retry.MaxRetriesPerProvider), isSelected(state, reliabilityItemMaxRetries), th))
	lines = append(lines, r.renderKeyValueRow(i18n.T("settings.reliability.retry.rotate"), renderToggle(r.retry.RotateOnRateLimit, isSelected(state, reliabilityItemRotateOnRateLimit), th), isSelected(state, reliabilityItemRotateOnRateLimit), th))
	lines = append(lines, r.renderKeyValueRow(i18n.T("settings.reliability.retry.delay"), fmt.Sprintf("%d", r.retry.RetryAfterFallbackMs), isSelected(state, reliabilityItemRetryAfterFallback), th))
	lines = append(lines, "")

	lines = append(lines, r.renderSectionHeader(i18n.T("settings.reliability.fallback.title"), th))
	fallbackSelected := isSelected(state, reliabilityItemFallbackChain)
	lines = append(lines, r.renderKeyValueRow(i18n.T("settings.reliability.fallback.editor"), i18n.T("settings.reliability.fallback.edit"), fallbackSelected, th))
	if r.mode == reliabilityModeFallback && fallbackSelected && r.picker != nil {
		pickerHeight := height / 3
		if pickerHeight < 8 {
			pickerHeight = 8
		}
		lines = append(lines, r.picker.Render(maxInt(20, width-2), pickerHeight, th))
	} else if chain := r.GetFallbackChain(); chain != nil && !chain.IsEmpty() {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(0, 2).
			Render(i18n.T("settings.reliability.fallback.primary", chain.Primary.Provider, chain.Primary.Model)))
		if len(chain.Fallbacks) > 0 {
			lines = append(lines, lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Padding(0, 2).
				Render(i18n.T("settings.reliability.fallback.count", len(chain.Fallbacks))))
		}
	}
	lines = append(lines, "")

	lines = append(lines, r.renderSectionHeader(i18n.T("settings.reliability.limits.title"), th))
	if len(r.rateLimits) == 0 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(0, 2).
			Render(i18n.T("settings.reliability.limits.empty")))
	}

	for i, limit := range r.rateLimits {
		itemIndex := r.rateLimitStartIndex() + i
		label := fmt.Sprintf("%s/%s", limit.Provider, limit.Model)
		value := fmt.Sprintf("%d rpm", limit.RequestsPerMinute)
		if limit.Burst > 0 {
			value += i18n.T("settings.reliability.limits.burst_value", limit.Burst)
		}
		if !limit.Enabled {
			value += i18n.T("settings.reliability.disabled")
		}
		lines = append(lines, r.renderKeyValueRow(label, value, isSelected(state, itemIndex), th))
	}

	lines = append(lines, r.renderKeyValueRow(i18n.T("settings.reliability.limits.add"), i18n.T("settings.reliability.limits.create"), isSelected(state, r.addRateLimitIndex()), th))

	if strings.TrimSpace(r.statusMessage) != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(r.statusMessage))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Height(height).Render(content)
}

func (r *ReliabilitySettings) renderRateLimitEditor(width, height int, state *State, th Theme) string {
	title := i18n.T("settings.reliability.limits.add")
	if r.editingRateLimitIdx >= 0 {
		title = i18n.T("settings.reliability.limits.edit")
	}

	var lines []string
	lines = append(lines, r.renderSectionHeader(title, th))
	lines = append(lines, r.renderEditorField(i18n.T("settings.reliability.field.provider"), r.draftRateLimitConfig.Provider, rateLimitFieldProvider, th))
	lines = append(lines, r.renderEditorField(i18n.T("settings.reliability.field.model"), r.draftRateLimitConfig.Model, rateLimitFieldModel, th))
	lines = append(lines, r.renderEditorField(i18n.T("settings.reliability.field.rpm"), fmt.Sprintf("%d", r.draftRateLimitConfig.RequestsPerMinute), rateLimitFieldRPM, th))
	lines = append(lines, r.renderEditorField(i18n.T("settings.reliability.field.enabled"), reliabilityBoolLabel(r.draftRateLimitConfig.Enabled), rateLimitFieldEnabled, th))
	lines = append(lines, r.renderEditorField(i18n.T("settings.reliability.field.burst"), fmt.Sprintf("%d", r.draftRateLimitConfig.Burst), rateLimitFieldBurst, th))
	lines = append(lines, "")
	lines = append(lines, r.renderEditorField(i18n.T("settings.reliability.save"), i18n.T("settings.reliability.press_enter"), rateLimitFieldSave, th))
	lines = append(lines, r.renderEditorField(i18n.T("settings.reliability.cancel"), i18n.T("settings.reliability.press_enter"), rateLimitFieldCancel, th))
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(i18n.T("settings.reliability.editor.hint")))

	if strings.TrimSpace(r.statusMessage) != "" {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Render(r.statusMessage))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Height(height).Render(content)
}

func (r *ReliabilitySettings) renderSectionHeader(title string, th Theme) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Render(title)
}

func (r *ReliabilitySettings) renderKeyValueRow(label, value string, selected bool, th Theme) string {
	prefix := "  "
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1)
	if selected {
		prefix = "▶ "
		style = style.
			Background(lipgloss.Color(th.BGLight)).
			Bold(true)
	}
	return style.Render(fmt.Sprintf("%s%s: %s", prefix, label, value))
}

func (r *ReliabilitySettings) renderEditorField(label, value string, field int, th Theme) string {
	selected := r.rateLimitField == field
	prefix := "  "
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1)
	if selected {
		prefix = "▶ "
		style = style.
			Background(lipgloss.Color(th.BGLight)).
			Bold(true)
	}
	return style.Render(fmt.Sprintf("%s%s: %s", prefix, label, value))
}

func (r *ReliabilitySettings) renderHintBar(width int, th Theme) string {
	var hints string

	switch r.mode {
	case reliabilityModeFallback:
		hints = i18n.T("settings.reliability.hints.fallback")
	case reliabilityModeRateLimitEdit:
		hints = i18n.T("settings.reliability.hints.editor")
	default:
		hints = i18n.T("settings.reliability.hints.main")
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center).
		Render(hints)
}

func reliabilityBoolLabel(value bool) string {
	if value {
		return i18n.T("settings.common.on")
	}
	return i18n.T("settings.common.off")
}

func isSelected(state *State, index int) bool {
	return state != nil && state.Focus == FocusContent && state.SelectedItem == index
}
