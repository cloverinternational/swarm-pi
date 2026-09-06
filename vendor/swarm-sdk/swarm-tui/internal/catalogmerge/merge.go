package catalogmerge

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
)

// ProviderUIConfig represents a provider definition merged for UI use.
type ProviderUIConfig struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"displayName"`
	Color       string          `json:"color,omitempty"`
	Type        string          `json:"type"`
	APIType     string          `json:"apiType,omitempty"`
	BaseURL     string          `json:"baseUrl,omitempty"`
	Source      string          `json:"source,omitempty"`
	Models      []ModelUIConfig `json:"models"`
	Name        string          `json:"-"`
	// SupportsOAuth is true for providers that have a first-party OAuth flow
	// (Anthropic, OpenAI, Gemini). When true the configure modal shows both
	// the OAuth and API-key paths so the user can choose.
	SupportsOAuth bool `json:"supportsOAuth"`
}

// ModelUIConfig represents a model definition merged for UI use.
type ModelUIConfig struct {
	ID                      string   `json:"id"`
	DisplayName             string   `json:"displayName"`
	Context                 string   `json:"context,omitempty"`
	ContextWindow           int      `json:"contextWindow,omitempty"`
	Description             string   `json:"description,omitempty"`
	SupportsReasoningEffort *bool    `json:"supportsReasoningEffort,omitempty"`
	ReasoningEfforts        []string `json:"reasoningEfforts,omitempty"`
}

// FormatContextWindow formats a context window in tokens into a compact string.
func FormatContextWindow(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	if tokens >= 1000 && tokens%1000 == 0 {
		return fmt.Sprintf("%dK", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func normalizeProviderSource(source string) string {
	var normalized string = strings.ToLower(strings.TrimSpace(source))
	switch normalized {
	case "cloud", "local", "user":
		return normalized
	default:
		return "local"
	}
}

func normalizeProviderID(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}

// ProviderSupportsOAuth returns true for providers that ship a first-party
// OAuth flow (Anthropic device-code, OpenAI device-code, Gemini browser-auth).
// Used to decide whether the configure modal shows an auth-method toggle.
func ProviderSupportsOAuth(apiType, providerID string) bool {
	check := func(s string) bool {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "anthropic", "openai", "gemini":
			return true
		}
		return false
	}
	return check(apiType) || check(providerID)
}

func catalogProviderName(provider cloud.CatalogProvider) string {
	if provider.Name != "" {
		return provider.Name
	}
	if provider.ID != "" {
		return provider.ID
	}
	return provider.DisplayName
}

// CatalogProviderToConfig converts a catalog provider into a UI provider config.
func CatalogProviderToConfig(provider cloud.CatalogProvider) ProviderUIConfig {
	var name string = catalogProviderName(provider)
	var displayName string = provider.DisplayName
	if displayName == "" {
		displayName = name
	}

	var providerType string = provider.Type
	if providerType == "" {
		providerType = "api_key"
	}

	var models []ModelUIConfig = make([]ModelUIConfig, 0, len(provider.Models))
	for _, model := range provider.Models {
		var modelID string = strings.TrimSpace(model.ID)
		if modelID == "" {
			continue
		}
		var context string = model.Context
		if context == "" {
			context = FormatContextWindow(model.ContextWindow)
		}
		models = append(models, ModelUIConfig{
			ID:                      modelID,
			DisplayName:             model.DisplayName,
			Context:                 context,
			ContextWindow:           model.ContextWindow,
			Description:             model.Description,
			SupportsReasoningEffort: cloneBoolPtr(model.SupportsReasoningEffort),
			ReasoningEfforts:        cloneStringSlice(model.ReasoningEfforts),
		})
	}

	return ProviderUIConfig{
		ID:            normalizeProviderID(provider.ID),
		Name:          name,
		DisplayName:   displayName,
		Color:         provider.Color,
		Type:          providerType,
		APIType:       provider.APIType,
		BaseURL:       provider.BaseURL,
		Source:        "cloud",
		Models:        models,
		SupportsOAuth: ProviderSupportsOAuth(provider.APIType, normalizeProviderID(provider.ID)),
	}
}

func mergeProviderModels(local ProviderUIConfig, cloud ProviderUIConfig) ([]ModelUIConfig, bool) {
	var localByID map[string]ModelUIConfig = make(map[string]ModelUIConfig, len(local.Models))
	var invalidLocal bool = false
	for _, model := range local.Models {
		var modelID string = strings.TrimSpace(model.ID)
		if modelID == "" {
			invalidLocal = true
			continue
		}
		if model.ID != modelID {
			invalidLocal = true
			model.ID = modelID
		}
		localByID[modelID] = model
	}

	var merged []ModelUIConfig = make([]ModelUIConfig, 0, len(cloud.Models)+len(local.Models))
	var changed bool = false

	for _, cloudModel := range cloud.Models {
		var modelID string = strings.TrimSpace(cloudModel.ID)
		if modelID == "" {
			continue
		}
		if cloudModel.ID != modelID {
			cloudModel.ID = modelID
		}
		var mergedModel ModelUIConfig = cloudModel
		if localModel, ok := localByID[modelID]; ok {
			if localModel.DisplayName != "" {
				mergedModel.DisplayName = localModel.DisplayName
			}
			if localModel.Context != "" {
				mergedModel.Context = localModel.Context
			}
			if localModel.Description != "" && mergedModel.Description == "" {
				mergedModel.Description = localModel.Description
			}
			if mergedModel.ContextWindow == 0 && localModel.ContextWindow > 0 {
				mergedModel.ContextWindow = localModel.ContextWindow
			}
			if localModel.SupportsReasoningEffort != nil {
				mergedModel.SupportsReasoningEffort = cloneBoolPtr(localModel.SupportsReasoningEffort)
				if !*localModel.SupportsReasoningEffort && localModel.ReasoningEfforts == nil {
					mergedModel.ReasoningEfforts = nil
				}
			}
			if localModel.ReasoningEfforts != nil {
				mergedModel.ReasoningEfforts = cloneStringSlice(localModel.ReasoningEfforts)
			}
			if !modelConfigsEqual(mergedModel, localModel) {
				changed = true
			}
			delete(localByID, modelID)
		} else {
			changed = true
		}
		merged = append(merged, mergedModel)
	}

	if len(localByID) > 0 {
		for _, localModel := range local.Models {
			var modelID string = strings.TrimSpace(localModel.ID)
			if modelID == "" {
				invalidLocal = true
				continue
			}
			if _, ok := localByID[modelID]; !ok {
				continue
			}
			if localModel.ID != modelID {
				invalidLocal = true
				localModel.ID = modelID
			}
			merged = append(merged, localModel)
			changed = true
		}
	}

	if invalidLocal {
		changed = true
	}

	return merged, changed
}

func modelConfigsEqual(left ModelUIConfig, right ModelUIConfig) bool {
	if left.ID != right.ID {
		return false
	}
	if left.DisplayName != right.DisplayName {
		return false
	}
	if left.Context != right.Context {
		return false
	}
	if left.ContextWindow != right.ContextWindow {
		return false
	}
	if left.Description != right.Description {
		return false
	}
	if !equalBoolPtr(left.SupportsReasoningEffort, right.SupportsReasoningEffort) {
		return false
	}
	if !equalStringSlice(left.ReasoningEfforts, right.ReasoningEfforts) {
		return false
	}
	return true
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func equalBoolPtr(left *bool, right *bool) bool {
	if left == nil && right == nil {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return *left == *right
}

func equalStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func mergeProviderConfig(local ProviderUIConfig, cloud ProviderUIConfig) (ProviderUIConfig, bool) {
	var changed bool = false
	var result ProviderUIConfig = local
	result.Source = normalizeProviderSource(result.Source)

	if result.Source == "cloud" {
		cloud.Source = "cloud"
		if providerConfigsEqual(result, cloud) {
			return cloud, false
		}
		return cloud, true
	}

	if result.DisplayName == "" && cloud.DisplayName != "" {
		result.DisplayName = cloud.DisplayName
		changed = true
	}
	if result.Color == "" && cloud.Color != "" {
		result.Color = cloud.Color
		changed = true
	}
	if result.Type == "" && cloud.Type != "" {
		result.Type = cloud.Type
		changed = true
	}
	if result.APIType == "" && cloud.APIType != "" {
		result.APIType = cloud.APIType
		changed = true
	}
	if result.BaseURL == "" && cloud.BaseURL != "" {
		result.BaseURL = cloud.BaseURL
		changed = true
	}

	var mergedModels []ModelUIConfig
	var modelsChanged bool
	mergedModels, modelsChanged = mergeProviderModels(result, cloud)
	if modelsChanged {
		result.Models = mergedModels
		changed = true
	}

	return result, changed
}

func providerConfigsEqual(left ProviderUIConfig, right ProviderUIConfig) bool {
	if left.ID != right.ID {
		return false
	}
	if left.Name != right.Name {
		return false
	}
	if left.DisplayName != right.DisplayName {
		return false
	}
	if left.Color != right.Color {
		return false
	}
	if left.Type != right.Type {
		return false
	}
	if left.APIType != right.APIType {
		return false
	}
	if left.BaseURL != right.BaseURL {
		return false
	}

	var leftSource string = normalizeProviderSource(left.Source)
	var rightSource string = normalizeProviderSource(right.Source)
	if leftSource != rightSource {
		return false
	}

	if len(left.Models) != len(right.Models) {
		return false
	}
	for i := range left.Models {
		if !modelConfigsEqual(left.Models[i], right.Models[i]) {
			return false
		}
	}

	return true
}

// MergeProvidersFromCatalog merges catalog providers into the local list.
func MergeProvidersFromCatalog(local []ProviderUIConfig, catalog *cloud.CatalogDocument) ([]ProviderUIConfig, bool) {
	if catalog == nil || len(catalog.Providers) == 0 {
		return local, false
	}

	var localByID map[string]ProviderUIConfig = make(map[string]ProviderUIConfig, len(local))
	for _, provider := range local {
		var providerID string = normalizeProviderID(provider.ID)
		if providerID == "" {
			continue
		}
		provider.ID = providerID
		localByID[providerID] = provider
	}

	var merged []ProviderUIConfig = make([]ProviderUIConfig, 0, len(local)+len(catalog.Providers))
	var usedLocal map[string]struct{} = make(map[string]struct{}, len(localByID))
	var changed bool = false

	for _, catalogProvider := range catalog.Providers {
		var cloudConfig ProviderUIConfig = CatalogProviderToConfig(catalogProvider)
		var providerID string = normalizeProviderID(cloudConfig.ID)
		if providerID == "" {
			continue
		}
		cloudConfig.ID = providerID

		var localProvider ProviderUIConfig
		var ok bool
		localProvider, ok = localByID[providerID]
		if ok {
			usedLocal[providerID] = struct{}{}
			var mergedProvider ProviderUIConfig
			var providerChanged bool
			mergedProvider, providerChanged = mergeProviderConfig(localProvider, cloudConfig)
			if providerChanged {
				changed = true
			}
			merged = append(merged, mergedProvider)
			continue
		}

		merged = append(merged, cloudConfig)
		changed = true
	}

	for _, provider := range local {
		var providerID string = normalizeProviderID(provider.ID)
		if providerID == "" {
			continue
		}
		if _, ok := usedLocal[providerID]; ok {
			continue
		}
		provider.Source = normalizeProviderSource(provider.Source)
		provider.ID = providerID
		merged = append(merged, provider)
	}

	return merged, changed
}
