package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/codex"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/cursor"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/prompts"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

func getModelContextWindow(providerName, model string) int {
	// Try to load from config
	providers, err := loadAllProviders()
	if err != nil {
		logDebug("[ContextWindow] Failed to load providers: %v", err)
		return 0
	}

	// Normalize the model ID for matching
	normalizedModel := normalizeModelID(model)
	hasExactProvider := false
	for i := range providers {
		if providerConfigMatches(&providers[i], providerName, true) {
			hasExactProvider = true
			break
		}
	}

	// Match only within the active provider identity. Model IDs are frequently
	// shared across providers and can have different effective limits.
	var exactMatch, normalizedMatch *struct {
		provider *ProviderConfig
		model    *ProviderModel
	}

	for i := range providers {
		providerConfig := &providers[i]
		if !providerConfigMatches(providerConfig, providerName, hasExactProvider) {
			continue
		}
		for j := range providerConfig.Models {
			m := &providerConfig.Models[j]

			// Exact match (highest priority)
			if m.ID == model {
				exactMatch = &struct {
					provider *ProviderConfig
					model    *ProviderModel
				}{providerConfig, m}
				break
			}

			// Normalized match (second priority)
			if normalizedMatch == nil && normalizeModelID(m.ID) == normalizedModel {
				normalizedMatch = &struct {
					provider *ProviderConfig
					model    *ProviderModel
				}{providerConfig, m}
			}
		}
		if exactMatch != nil {
			break
		}
	}

	// Use the best match found
	var match *struct {
		provider *ProviderConfig
		model    *ProviderModel
	}
	var matchType string

	if exactMatch != nil {
		match = exactMatch
		matchType = "exact"
	} else if normalizedMatch != nil {
		match = normalizedMatch
		matchType = "normalized"
	}

	if match != nil {
		m := match.model
		provider := match.provider

		// Priority 1: New format - context_window (integer)
		if m.ContextWindow > 0 {
			logDebug("[ContextWindow] model=%s matched=%s provider=%s context_window=%d (match_type=%s, int format)",
				model, m.ID, provider.Name, m.ContextWindow, matchType)
			return m.ContextWindow
		}

		// Priority 2: Legacy format - context (string like "128000" or "128k")
		if m.Context != "" {
			cw := parseContextString(m.Context)
			if cw > 0 {
				logDebug("[ContextWindow] model=%s matched=%s provider=%s context_window=%d (match_type=%s, legacy string '%s')",
					model, m.ID, provider.Name, cw, matchType, m.Context)
				return cw
			}
		}

		// Priority 3: Known model context windows map
		if cw := lookupKnownContextWindow(m.ID); cw > 0 {
			logDebug("[ContextWindow] model=%s matched=%s provider=%s context_window=%d (match_type=%s, known map)",
				model, m.ID, provider.Name, cw, matchType)
			return cw
		}
		if normalizedModel != "" {
			if cw := lookupKnownContextWindow(normalizedModel); cw > 0 {
				logDebug("[ContextWindow] model=%s matched=%s provider=%s context_window=%d (match_type=%s, known map normalized)",
					model, m.ID, provider.Name, cw, matchType)
				return cw
			}
		}

		// Model found but no context window configured
		logDebug("[ContextWindow] model=%s matched=%s provider=%s - no context_window in config (match_type=%s)",
			model, m.ID, provider.Name, matchType)
		return 0
	}

	if cw := lookupKnownContextWindow(model); cw > 0 {
		logDebug("[ContextWindow] model=%s context_window=%d (known map, no config match)", model, cw)
		return cw
	}
	if normalizedModel != "" && normalizedModel != model {
		if cw := lookupKnownContextWindow(normalizedModel); cw > 0 {
			logDebug("[ContextWindow] model=%s context_window=%d (known map normalized, no config match)", model, cw)
			return cw
		}
	}

	// Model not found in config
	logDebug("[ContextWindow] provider=%s model=%s not found in providers.json (tried exact, normalized)", providerName, model)
	return 0
}

func providerConfigMatches(config *ProviderConfig, providerName string, exactOnly bool) bool {
	if config == nil {
		return false
	}
	wanted := strings.TrimSpace(providerName)
	if wanted == "" {
		return true
	}
	for _, candidate := range []string{config.Name, config.DisplayName} {
		if strings.EqualFold(strings.TrimSpace(candidate), wanted) {
			return true
		}
	}
	if exactOnly {
		return false
	}
	wantedNormalized := provider.NormalizeProviderName(wanted)
	for _, candidate := range []string{config.Name, config.APIType} {
		if wantedNormalized != "" &&
			strings.EqualFold(provider.NormalizeProviderName(candidate), wantedNormalized) {
			return true
		}
	}
	return false
}

// normalizeModelID removes common prefixes and normalizes model IDs for matching
// e.g., "anthropic/claude-3.5-sonnet" -> "claude-3.5-sonnet"
// e.g., "claude-3-5-sonnet-20241022" -> "claude-3-5-sonnet-20241022"
func normalizeModelID(id string) string {
	// Remove provider prefixes (OpenRouter style)
	prefixes := []string{
		"anthropic/", "openai/", "google/", "meta/", "mistral/",
		"meta-llama/", "deepseek/", "cohere/", "nvidia/", "amazon/",
	}
	normalized := strings.ToLower(id)
	for _, prefix := range prefixes {
		normalized = strings.TrimPrefix(normalized, prefix)
	}

	// Remove common suffixes like ":free", ":extended", ":beta"
	suffixes := []string{":free", ":extended", ":beta", ":online"}
	for _, suffix := range suffixes {
		normalized = strings.TrimSuffix(normalized, suffix)
	}

	return normalized
}

// canonicalizeModelID produces a canonical form for matching local IDs against models.dev IDs.
// Extends normalizeModelID with:
//   - Strip date suffixes: "claude-opus-4-20250514" → "claude-opus-4" (Anthropic YYYYMMDD)
//   - Strip date suffixes: "gpt-5-2025-08-07" → "gpt-5" (OpenAI YYYY-MM-DD)
//   - Normalize version digits: "claude-3-5-sonnet" → "claude-3.5-sonnet"
//
// This bridges the gap between providers.json IDs (date-suffixed, hyphen versions)
// and models.dev IDs (short names, dot versions).
func canonicalizeModelID(id string) string {
	canonical := normalizeModelID(id)

	// Strip Anthropic-style date suffix: trailing -YYYYMMDD (8 digits)
	if len(canonical) > 9 && canonical[len(canonical)-9] == '-' {
		datePart := canonical[len(canonical)-8:]
		allDigits := true
		for _, c := range datePart {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			canonical = canonical[:len(canonical)-9]
		}
	}

	// Strip OpenAI-style date suffix: trailing -YYYY-MM-DD (pattern: -NNNN-NN-NN)
	if len(canonical) > 11 {
		tail := canonical[len(canonical)-11:]
		if tail[0] == '-' && tail[5] == '-' && tail[8] == '-' &&
			isDigits(tail[1:5]) && isDigits(tail[6:8]) && isDigits(tail[9:11]) {
			canonical = canonical[:len(canonical)-11]
		}
	}

	// Normalize version digits: replace "X-Y" with "X.Y" where X,Y are single digits
	// e.g., "claude-3-5-sonnet" → "claude-3.5-sonnet", "claude-haiku-4-5" → "claude-haiku-4.5"
	result := make([]byte, 0, len(canonical))
	for i := 0; i < len(canonical); i++ {
		if i > 0 && i < len(canonical)-1 &&
			canonical[i] == '-' &&
			canonical[i-1] >= '0' && canonical[i-1] <= '9' &&
			canonical[i+1] >= '0' && canonical[i+1] <= '9' {
			result = append(result, '.')
		} else {
			result = append(result, canonical[i])
		}
	}

	return string(result)
}

// isDigits returns true if all characters in s are ASCII digits.
func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// parseContextString parses a context window string like "128000", "128k", or "1m"
func parseContextString(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}

	// Check for multiplier suffix
	multiplier := 1
	if strings.HasSuffix(s, "k") {
		multiplier = 1000
		s = strings.TrimSuffix(s, "k")
	} else if strings.HasSuffix(s, "m") {
		multiplier = 1000000
		s = strings.TrimSuffix(s, "m")
	}

	// Parse the number
	var value int
	if _, err := fmt.Sscanf(s, "%d", &value); err != nil {
		return 0
	}

	return value * multiplier
}

// OpenRouterModelInfo contains full model info from OpenRouter API
type OpenRouterModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	Description   string `json:"description,omitempty"`
}

func formatContextWindow(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	if tokens >= 1000 && tokens%1000 == 0 {
		return fmt.Sprintf("%dK", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// FetchOpenRouterModels fetches model info from OpenRouter API
// Returns a map of model ID to context window size
func FetchOpenRouterModels(apiKey string) (map[string]int, error) {
	var models []OpenRouterModelInfo
	var err error
	models, err = FetchOpenRouterModelsDetailed(apiKey)
	if err != nil {
		return nil, err
	}

	var result map[string]int = make(map[string]int)
	for _, model := range models {
		if model.ContextLength > 0 {
			result[model.ID] = model.ContextLength
		}
	}
	return result, nil
}

// FetchOpenRouterModelsDetailed fetches full model info from OpenRouter API
func FetchOpenRouterModelsDetailed(apiKey string) ([]OpenRouterModelInfo, error) {
	var req *http.Request
	var err error
	req, err = http.NewRequest("GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, err
	}

	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	var client *http.Client = &http.Client{Timeout: 30 * time.Second}
	var resp *http.Response
	resp, err = client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("OpenRouter API returned status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Description   string `json:"description"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var models []OpenRouterModelInfo = make([]OpenRouterModelInfo, 0, len(result.Data))
	for _, model := range result.Data {
		if model.ID == "" {
			continue
		}
		models = append(models, OpenRouterModelInfo{
			ID:            model.ID,
			Name:          model.Name,
			ContextLength: model.ContextLength,
			Description:   model.Description,
		})
	}

	return models, nil
}

// writeOpenRouterModelsToProvidersJSON writes fetched models into the OpenRouter
// entry of providers.json, tagging them with the given source label.
func writeOpenRouterModelsToProvidersJSON(models []OpenRouterModelInfo, source string) (int, error) {
	providersJSONMu.Lock()
	defer providersJSONMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("failed to get home directory: %w", err)
	}

	providersPath := filepath.Join(home, ".swarmos", "providers.json")
	data, err := readProvidersJSONWithRecovery(providersPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read providers.json: %w", err)
	}

	var providers []map[string]interface{}
	if err = json.Unmarshal(data, &providers); err != nil {
		return 0, fmt.Errorf("failed to parse providers.json: %w", err)
	}

	found := false
	for i, provider := range providers {
		name, ok := provider["name"].(string)
		if !ok || !strings.EqualFold(name, "openrouter") {
			continue
		}
		found = true
		existingByID := make(map[string]map[string]interface{})
		if existingModels, ok := provider["models"].([]interface{}); ok {
			for _, rawModel := range existingModels {
				existingModel, ok := rawModel.(map[string]interface{})
				if !ok {
					continue
				}
				id, _ := existingModel["id"].(string)
				if id != "" {
					existingByID[strings.ToLower(id)] = existingModel
				}
			}
		}
		providerModels := make([]map[string]interface{}, 0, len(models))
		for _, model := range models {
			modelEntry := make(map[string]interface{})
			for key, value := range existingByID[strings.ToLower(model.ID)] {
				modelEntry[key] = value
			}
			modelEntry["id"] = model.ID
			modelEntry["display_name"] = model.Name
			modelEntry["context"] = formatContextWindow(model.ContextLength)
			modelEntry["context_window"] = model.ContextLength
			modelEntry["description"] = model.Description
			providerModels = append(providerModels, modelEntry)
		}
		providers[i]["models"] = providerModels
		providers[i]["last_refreshed"] = time.Now().Format(time.RFC3339)
		providers[i]["source"] = source
		break
	}

	if !found {
		return 0, fmt.Errorf("OpenRouter provider not found in providers.json")
	}

	updatedData, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal providers.json: %w", err)
	}

	if err = atomicWriteProvidersJSON(providersPath, updatedData, 0644); err != nil {
		return 0, fmt.Errorf("failed to write providers.json: %w", err)
	}

	return len(models), nil
}

// RefreshOpenRouterModels fetches models from OpenRouter API and saves to providers.json.
// Falls back to models.dev if OpenRouter fails.
// Returns the number of models fetched.
func RefreshOpenRouterModels() (int, error) {
	apiKey := getOpenRouterAPIKey()
	models, err := FetchOpenRouterModelsDetailed(apiKey)
	source := "openrouter"
	if err != nil {
		logDebug("[OpenRouter] Failed to fetch from OpenRouter API: %v, trying models.dev as fallback", err)
		source = "models.dev"
		models, err = FetchModelsDevModelsDetailed()
		if err != nil {
			return 0, fmt.Errorf("failed to fetch OpenRouter models and models.dev fallback failed: %w", err)
		}
		logDebug("[OpenRouter] Successfully fetched %d models from models.dev fallback", len(models))
	} else {
		logDebug("[OpenRouter] Successfully fetched %d models from OpenRouter API", len(models))
	}

	return writeOpenRouterModelsToProvidersJSON(models, source)
}

// FetchModelsDevModelsDetailed fetches full model info from models.dev API
// Returns a map of model ID -> context window size for ALL providers
func FetchModelsDevModelsDetailed() ([]OpenRouterModelInfo, error) {
	var req *http.Request
	var err error
	req, err = http.NewRequest("GET", "https://models.dev/api.json", nil)
	if err != nil {
		return nil, err
	}

	var client *http.Client = &http.Client{Timeout: 30 * time.Second}
	var resp *http.Response
	resp, err = client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("models.dev API returned status %d", resp.StatusCode)
	}

	// models.dev returns a different structure: { "provider_name": { "models": { "model_id": {...} } } }
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var models []OpenRouterModelInfo = make([]OpenRouterModelInfo, 0)

	// Parse the nested structure
	for _, providerData := range result {
		providerMap, ok := providerData.(map[string]interface{})
		if !ok {
			continue
		}

		// Get models from this provider
		modelsData, ok := providerMap["models"].(map[string]interface{})
		if !ok {
			continue
		}

		for modelID, modelData := range modelsData {
			modelMap, ok := modelData.(map[string]interface{})
			if !ok {
				continue
			}

			name, _ := modelMap["name"].(string)
			contextWindow := 0

			// Try to get context window from limit.context
			if limit, ok := modelMap["limit"].(map[string]interface{}); ok {
				if ctx, ok := limit["context"].(float64); ok {
					contextWindow = int(ctx)
				}
			}

			models = append(models, OpenRouterModelInfo{
				ID:            modelID,
				Name:          name,
				ContextLength: contextWindow,
				Description:   "",
			})
		}
	}

	return models, nil
}

// ModelsDevCost represents pricing info from models.dev
type ModelsDevCost struct {
	Input  float64 `json:"input"`  // Cost per 1M input tokens (USD)
	Output float64 `json:"output"` // Cost per 1M output tokens (USD)
}

// ModelsDevLimit represents token limits from models.dev
// Uses float64 because JSON numbers may include decimal points (e.g. 200000.0)
type ModelsDevLimit struct {
	Context float64 `json:"context"` // Context window size in tokens
	Output  float64 `json:"output"`  // Max output tokens
}

// ModelsDevModel represents a single model from models.dev
type ModelsDevModel struct {
	Name      string         `json:"name"`
	Limit     ModelsDevLimit `json:"limit"`
	Cost      ModelsDevCost  `json:"cost"`
	Reasoning bool           `json:"reasoning"`
	ToolCall  bool           `json:"tool_call"`
}

// ModelsDevProvider represents a provider's models from models.dev
type ModelsDevProvider struct {
	Models map[string]ModelsDevModel `json:"models"`
}

// FetchModelsDevByProvider fetches models.dev data preserving provider context.
// Returns map[providerKey]ModelsDevProvider (e.g. "anthropic" -> {models: {...}})
func FetchModelsDevByProvider() (map[string]ModelsDevProvider, error) {
	req, err := http.NewRequest("GET", "https://models.dev/api.json", nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("models.dev API returned status %d", resp.StatusCode)
	}

	var result map[string]ModelsDevProvider
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	logDebug("[ModelsDev] Fetched %d providers from models.dev", len(result))
	return result, nil
}

// FetchAllModelContextWindows fetches context windows from models.dev and returns a unified map
// This is used to populate context_window for ALL providers (not just OpenRouter)
func FetchAllModelContextWindows() (map[string]int, error) {
	models, err := FetchModelsDevModelsDetailed()
	if err != nil {
		return nil, err
	}

	contextMap := make(map[string]int)
	for _, model := range models {
		if model.ContextLength > 0 {
			contextMap[model.ID] = model.ContextLength
		}
	}

	return contextMap, nil
}

// GetOpenRouterModelInfo fetches info for a specific model from OpenRouter
// Useful for getting context window for a model not in our config
func GetOpenRouterModelInfo(modelID string) (*OpenRouterModelInfo, error) {
	var apiKey string = getOpenRouterAPIKey()
	var models []OpenRouterModelInfo
	var err error
	models, err = FetchOpenRouterModelsDetailed(apiKey)
	if err != nil {
		return nil, err
	}

	for _, model := range models {
		if model.ID == modelID {
			return &model, nil
		}
	}

	return nil, fmt.Errorf("model %s not found in OpenRouter", modelID)
}

// RefreshFromModelsDev refreshes OpenRouter models from models.dev source explicitly.
// Useful when OpenRouter API is unavailable or as a primary source.
func RefreshFromModelsDev() (int, error) {
	logDebug("[ModelsDev] Fetching models from models.dev API")
	models, err := FetchModelsDevModelsDetailed()
	if err != nil {
		return 0, fmt.Errorf("failed to fetch models from models.dev: %w", err)
	}
	logDebug("[ModelsDev] Successfully fetched %d models", len(models))
	return writeOpenRouterModelsToProvidersJSON(models, "models.dev")
}

// GetContextWindowForModel returns the context window for a model, fetching from API if needed
func GetContextWindowForModel(model string, providerName string) int {
	// First try local config
	contextWindow := getModelContextWindow(providerName, model)
	if contextWindow > 0 {
		return contextWindow
	}

	// For OpenRouter, try fetching from their API
	if strings.ToLower(providerName) == "openrouter" {
		if info, err := GetOpenRouterModelInfo(model); err == nil {
			return provider.ClampContextWindow(info.ContextLength)
		}
	}

	// Return 0 to indicate unknown - let caller decide on default
	return 0
}

func (sdk *SDKIntegration) IsOAuth() bool {
	return sdk.isOAuth
}

// IsCodexBacked returns true when requests are routed through Codex semantics.
func (sdk *SDKIntegration) IsCodexBacked() bool {
	return isCodexProvider(sdk)
}

// EnsureCodexPromptIntegrity refreshes/enforces canonical Codex instructions on the agent.
func (sdk *SDKIntegration) EnsureCodexPromptIntegrity(ctx context.Context, refresh bool) (string, bool) {
	if sdk == nil || sdk.activeAgent() == nil || !sdk.IsCodexBacked() {
		return "", false
	}

	var prompt string
	var changed bool
	prompt, changed = resolveCodexCanonicalPrompt(ctx, sdk.currentModel, refresh)
	if strings.TrimSpace(prompt) == "" {
		return "", false
	}

	var currentPrompt string = sdk.activeAgent().SystemPrompt()
	if currentPrompt != prompt {
		sdk.activeAgent().SetSystemPrompt(prompt)
		return prompt, true
	}
	return prompt, changed
}

// applyUserSystemPromptToAgent applies the user's custom system prompt to the agent,
// always preserving any mandatory provider-specific prefix.
//
//   - For Codex-backed providers: the canonical Codex instructions (required by the
//     Codex backend) are kept at the top; the user prompt is appended after them.
//   - For OAuth Anthropic: the OAuth prefix is kept; the user prompt follows.
//   - For all other providers: the user prompt replaces the agent's current prompt.
//
// If userPrompt is empty the method is a no-op.
func (sdk *SDKIntegration) applyUserSystemPromptToAgent(ctx context.Context, userPrompt string) {
	if sdk == nil || sdk.harnessGoverned() || sdk.activeAgent() == nil {
		return
	}
	trimmed := strings.TrimSpace(userPrompt)
	if trimmed == "" {
		return
	}

	if sdk.IsCodexBacked() {
		canonical, _ := resolveCodexCanonicalPrompt(ctx, sdk.currentModel, false)
		if strings.TrimSpace(canonical) == "" {
			// Fallback — no canonical prompt available, just set user prompt directly.
			sdk.activeAgent().SetSystemPrompt(trimmed)
			logDebug("applyUserSystemPromptToAgent: no canonical Codex prompt, using user prompt directly")
			return
		}
		// The Codex backend requires the canonical instructions at the top.
		// Append the user's custom guidance in a clearly delimited section.
		if strings.Contains(trimmed, canonical) {
			// User prompt already contains the canonical block — don't duplicate.
			sdk.activeAgent().SetSystemPrompt(trimmed)
		} else {
			combined := canonical + "\n\n<custom_instructions>\n" + trimmed + "\n</custom_instructions>"
			sdk.activeAgent().SetSystemPrompt(combined)
		}
		logDebug("applyUserSystemPromptToAgent: Codex prompt set (canonical=%d + user=%d)", len(canonical), len(trimmed))
		return
	}

	// For non-Codex providers just set the user prompt as-is.
	// The OAuth prefix (if required) is already embedded by GetActivePromptWithOAuth()
	// when the settings manager builds the prompt string.
	sdk.activeAgent().SetSystemPrompt(trimmed)
	logDebug("applyUserSystemPromptToAgent: system prompt set (%d chars)", len(trimmed))
}

// ReloadProvider recreates the provider with fresh OAuth token
func (sdk *SDKIntegration) ReloadProvider() error {
	if sdk.harnessGoverned() {
		return fmt.Errorf("provider reload is disabled while the session is governed by a harness")
	}
	return sdk.reloadProviderCandidate(sdk.providerName, sdk.currentModel)
}

// reloadProviderCandidate constructs the replacement stack before committing
// provider, model, and active-agent state.
func (sdk *SDKIntegration) reloadProviderCandidate(providerName, model string) error {
	logDebug("ReloadProvider: rebuilding provider=%s model=%s", providerName, model)

	builder := sdk.providerBuilder
	if builder == nil {
		builder = defaultSDKProviderBuilder
	}
	configLoader := sdk.reliabilityConfigLoader
	if configLoader == nil {
		configLoader = loadSwarmOSConfig
	}

	buildDeps := providerBuildDeps{
		Logger:           sdk.logger,
		Tracer:           sdk.tracer,
		RawDebug:         sdk.rawDebug,
		EndpointOverride: sdk.endpointOverride,
	}
	prov, systemPrompt, authToken, _, err := builder(providerName, model, buildDeps)
	if err != nil {
		logDebug("ReloadProvider: buildProvider failed: %v", err)
		return err
	}

	logDebug("ReloadProvider: provider created successfully, type=%T", prov)

	stack, err := buildRuntimeProviderStack(
		prov,
		providerName,
		model,
		buildDeps,
		configLoader,
		builder,
		sdk.wrapProviderWithContext,
		nil,
	)
	if err != nil {
		logDebug("ReloadProvider: buildRuntimeProviderStack failed: %v", err)
		return err
	}

	sdk.provider = stack
	// Commit identity only after every fallible construction step succeeds.
	sdk.providerName = providerName
	sdk.currentModel = model
	sdk.authToken = authToken
	configuredContextWindow := GetContextWindowForModel(sdk.currentModel, sdk.providerName)
	effectiveContextWindow := configuredContextWindow
	if effectiveContextWindow <= 0 {
		effectiveContextWindow = provider.ClampContextWindow(stack.Capabilities().MaxContextWindow)
	}
	if effectiveContextWindow <= 0 {
		effectiveContextWindow = provider.DefaultUnknownContextWindow
	}
	if sdk.registryProviderSlot != nil {
		sdk.registryProviderSlot.SetRuntime(
			stack,
			sdk.providerName,
			sdk.currentModel,
			effectiveContextWindow,
		)
	}

	// Update agent
	sdk.activeAgent().SetProvider(stack)
	// Always assign, including zero, before resolving the effective fallback.
	// This clears any explicit context window inherited from the previous model.
	sdk.activeAgent().SetConfiguredContextWindow(configuredContextWindow)
	sdk.activeAgent().SetSystemPrompt(systemPrompt)
	if model != "" {
		sdk.activeAgent().SetModel(model)
	}

	// Clear the chain so execution uses the direct provider path.
	// When the user manually switches provider/model, the new provider is fully
	// configured (with correct OAuth tokens, API keys, etc.). The chain was set
	// at startup from the profile system and its registry factories may have stale
	// credentials — bypassing the chain ensures the freshly-built provider is used.
	sdk.activeAgent().SetChain(nil)

	// Ensure the switched-to provider is registered in the shared providerRegistry
	// so that executeWithChain (sub-agents, Task tool) can resolve it by name.
	//
	// CRITICAL normalization detail: the agent factory looks providers up by
	// provider.NormalizeProviderName(def.Provider) (e.g. "claudecode" and "claude"
	// both normalize to "anthropic"). So we MUST register under the NORMALIZED
	// name — registering under the raw sdk.providerName (e.g. "claudecode") leaves
	// the factory's "anthropic" lookup unresolved and sub-agents fail with
	// "provider 'claudecode' (normalized: 'anthropic') is not registered". We also
	// alias the raw name → normalized so lookups by either spelling succeed.
	if sdk.providerRegistry != nil {
		rawName := sdk.providerName
		normalized := provider.NormalizeProviderName(rawName)
		registered := func() bool {
			return sdk.providerRegistry.IsRegistered(normalized) || sdk.providerRegistry.IsRegistered(rawName)
		}
		if !registered() {
			// Try re-reading providers.json first (may have been updated since startup)
			_ = registerAllProviders(sdk.providerRegistry, sdk.logger, sdk.tracer, sdk.isOAuth)
		}
		if !registered() {
			// Still not registered — add a factory that builds the provider on demand.
			// Register under the normalized name (what the factory looks up) and alias
			// the raw name to it so either spelling resolves.
			bldDeps := providerBuildDeps{
				Logger:           sdk.logger,
				Tracer:           sdk.tracer,
				RawDebug:         sdk.rawDebug,
				EndpointOverride: sdk.endpointOverride,
			}
			buildName := rawName // build with the user-facing name so auth/baseURL resolution matches the active provider
			if err := sdk.providerRegistry.Register(normalized, func(cfg provider.Config) (provider.Provider, error) {
				model := cfg.Model
				if model == "" {
					model = sdk.currentModel
				}
				p, _, _, _, err := builder(buildName, model, bldDeps)
				return p, err
			}); err == nil {
				if rawName != "" && !strings.EqualFold(rawName, normalized) {
					sdk.providerRegistry.RegisterAlias(rawName, normalized)
				}
				logDebug("ReloadProvider: registered provider %q (alias %q) in shared registry (was missing)", normalized, rawName)
			}
		}
	}

	// Re-apply the user's selected system prompt on top of the base provider prompt.
	// buildProvider returns a minimal default ("You are a helpful AI assistant." or the
	// OAuth/Codex prefix).  We must overlay the user's selection so that a model/OAuth
	// reload doesn't silently discard whatever the user had configured in Settings.
	userPrompt := ""
	if sdk.userSystemPromptGetter != nil {
		userPrompt = sdk.userSystemPromptGetter()
	}
	if strings.TrimSpace(userPrompt) != "" {
		sdk.applyUserSystemPromptToAgent(context.Background(), userPrompt)
		logDebug("ReloadProvider: re-applied user system prompt (%d chars)", len(userPrompt))
	} else if sdk.IsCodexBacked() {
		// No user prompt but still Codex — enforce canonical instructions.
		_, _ = sdk.EnsureCodexPromptIntegrity(context.Background(), false)
	}

	sdk.logger.Info(context.Background(), "provider.reloaded",
		observability.F("provider", sdk.providerName),
	)

	logDebug("ReloadProvider: complete")
	return nil
}

// wrapProviderWithContext applies the real-time context injector wrapper if configured.
func (sdk *SDKIntegration) wrapProviderWithContext(prov provider.Provider) provider.Provider {
	if sdk == nil || prov == nil {
		return prov
	}
	contextProvider := newContextInjectingProviderWithCapture(prov, sdk.contextOrchestrator, sdk.contextCapture)
	return newGenerationSettingsProvider(contextProvider, sdk.generationSettings)
}

// applyProviderModelThinkingConfig applies an explicit per-model override to
// the effective state, which SwitchProvider resets to the global preference at
// the model-switch boundary. Catalog-only model records omit all three fields;
// those are unspecified and leave the reset global setting unchanged.
func (sdk *SDKIntegration) applyProviderModelThinkingConfig(model commands.ModelConfig) {
	configured := model.ThinkingEnabledSet || model.ThinkingEnabled || model.ThinkingBudget > 0 || model.ThinkingEffort != ""
	if !configured {
		return
	}

	sdk.thinkingEnabled = model.ThinkingEnabled
	if model.ThinkingBudget >= 1024 {
		sdk.thinkingBudget = model.ThinkingBudget
	}
	if model.ThinkingEnabled {
		sdk.thinkingEffort = model.ThinkingEffort
	} else {
		sdk.thinkingEffort = ""
	}
}

// ApplyProviderModelConfig updates the live per-model request overrides without
// rebuilding the provider. The generation wrapper snapshots these settings on
// every request.
func (sdk *SDKIntegration) ApplyProviderModelConfig(model commands.ModelConfig) {
	if sdk == nil || sdk.harnessGoverned() {
		return
	}
	if sdk.generationSettings == nil {
		sdk.generationSettings = newModelGenerationSettings(model.ID)
	}
	providerConfig := commands.Provider{Name: sdk.providerName}
	if configured, err := getProviderConfigFromFile(sdk.providerName); err == nil && configured != nil {
		providerConfig.APIType = configured.APIType
	}
	sdk.generationSettings.applyModel(providerConfig, model)
	sdk.applyProviderModelThinkingConfig(model)
}

// SwitchProvider updates the provider name/model and rebuilds the provider.
func (sdk *SDKIntegration) SwitchProvider(providerName, model string) error {
	if sdk.harnessGoverned() {
		return fmt.Errorf("provider/model switching is disabled while the session is governed by a harness")
	}
	// Lowercase for case-insensitive comparison but do NOT NormalizeProviderName —
	// that rewrites custom names (e.g. "wafer" → "wafer.ai"), breaking registry lookups.
	lowered := strings.ToLower(strings.TrimSpace(providerName))
	logDebug("SwitchProvider called: providerName=%s lowered=%s currentProvider=%s model=%s currentModel=%s",
		providerName, lowered, sdk.providerName, model, sdk.currentModel)

	// Log to debug screen
	sdk.debugLog(fmt.Sprintf("[SwitchProvider] provider=%s model=%s", providerName, model))

	if lowered == "" {
		lowered = sdk.providerName
	}

	candidateModel := sdk.currentModel
	if model != "" {
		candidateModel = model
	}
	previousModel := sdk.currentModel
	modelChanged := candidateModel != previousModel

	providerChanged := lowered != sdk.providerName
	if !providerChanged && !modelChanged {
		logDebug("SwitchProvider: provider/model unchanged (provider=%s model=%s), skipping rebuild", lowered, candidateModel)
		return nil
	}

	// Reset model-derived effective state exactly once at the switch boundary,
	// before any fallible config or catalog lookup. A matching local model may
	// then apply an explicit override below; unknown/catalog-only models retain
	// the user's global preference instead of the previous model's override.
	nextThinkingEnabled := sdk.thinkingDefaultEnabled
	nextThinkingBudget := sdk.thinkingDefaultBudget
	nextThinkingEffort := sdk.thinkingDefaultEffort
	nextDiffusionModel := false
	nextReasoningEffort := ""
	nextGenerationSettings := newModelGenerationSettings(candidateModel)

	if providerChanged {
		logDebug("SwitchProvider: provider changed from %s to %s, rebuilding", sdk.providerName, lowered)
	} else {
		logDebug("SwitchProvider: model changed from %s to %s, rebuilding provider stack", previousModel, candidateModel)
	}

	if configMgr, cfgErr := commands.NewConfigManager(); cfgErr == nil {
		if cfg, loadErr := configMgr.LoadConfig(); loadErr == nil && cfg != nil {
			nextReasoningEffort = cfg.GetReasoningEffortForModel(lowered, candidateModel)
		}
		// Apply per-model thinking + diffusion settings from providers.json
		if providers, provErr := configMgr.LoadProviders(); provErr == nil {
			for _, prov := range providers {
				if strings.EqualFold(prov.Name, lowered) {
					for _, mdl := range prov.Models {
						if strings.EqualFold(mdl.ID, candidateModel) {
							configured := mdl.ThinkingEnabledSet || mdl.ThinkingEnabled ||
								mdl.ThinkingBudget > 0 || mdl.ThinkingEffort != ""
							if configured {
								nextThinkingEnabled = mdl.ThinkingEnabled
								if mdl.ThinkingBudget >= 1024 {
									nextThinkingBudget = mdl.ThinkingBudget
								}
								if mdl.ThinkingEnabled {
									nextThinkingEffort = mdl.ThinkingEffort
								} else {
									nextThinkingEffort = ""
								}
							}
							nextGenerationSettings.applyModel(commands.Provider{
								Name:    prov.Name,
								APIType: prov.APIType,
							}, mdl)
							nextDiffusionModel = mdl.Diffusion
							break
						}
					}
					break
				}
			}
		}
	}

	if err := sdk.reloadProviderCandidate(lowered, candidateModel); err != nil {
		return err
	}
	sdk.thinkingEnabled = nextThinkingEnabled
	sdk.thinkingBudget = nextThinkingBudget
	sdk.thinkingEffort = nextThinkingEffort
	sdk.diffusionModel = nextDiffusionModel
	sdk.generationSettings = nextGenerationSettings
	sdk.SetReasoningEffort(nextReasoningEffort)
	sdk.reloadAutoCompactionConfig()
	if sdk.sdkClient != nil {
		_ = sdk.sdkClient.SetProvider(context.Background(), lowered, candidateModel)
	}
	return nil
}

// CredentialChanged rebuilds the live provider even when provider/model
// identity is unchanged, so newly persisted credentials take effect in place.
func (sdk *SDKIntegration) CredentialChanged(providerName, model string) error {
	if sdk.harnessGoverned() {
		return fmt.Errorf("credential changes are disabled while the session is governed by a harness")
	}
	lowered := strings.ToLower(strings.TrimSpace(providerName))
	if lowered == "" {
		lowered = sdk.providerName
	}
	if model == "" {
		model = sdk.currentModel
	}
	return sdk.reloadProviderCandidate(lowered, model)
}

// Helper functions

const (
	codexQueryParamsEnv = "SWARMOS_CODEX_QUERY_PARAMS"
	codexHTTPHeadersEnv = "SWARMOS_CODEX_HTTP_HEADERS"
)

func parseStringMapJSON(raw string) map[string]string {
	var trimmed string = strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		logDebug("Failed to parse JSON object: %v", err)
		return nil
	}

	normalized := make(map[string]string)
	for key, value := range decoded {
		var trimmedKey string = strings.TrimSpace(key)
		if trimmedKey == "" || value == nil {
			continue
		}
		normalized[trimmedKey] = strings.TrimSpace(fmt.Sprintf("%v", value))
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func mergeStringMaps(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	merged := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		var trimmedKey string = strings.TrimSpace(key)
		var trimmedValue string = strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		merged[trimmedKey] = trimmedValue
	}
	for key, value := range override {
		var trimmedKey string = strings.TrimSpace(key)
		var trimmedValue string = strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		merged[trimmedKey] = trimmedValue
	}

	if len(merged) == 0 {
		return nil
	}
	return merged
}

func resolveCodexPassthrough(providerCfg *ProviderConfig) (map[string]string, map[string]string) {
	var configQuery map[string]string
	var configHeaders map[string]string
	if providerCfg != nil {
		configQuery = providerCfg.CodexQueryParams
		configHeaders = providerCfg.CodexHTTPHeaders
	}

	envQuery := parseStringMapJSON(os.Getenv(codexQueryParamsEnv))
	envHeaders := parseStringMapJSON(os.Getenv(codexHTTPHeadersEnv))

	return mergeStringMaps(configQuery, envQuery), mergeStringMaps(configHeaders, envHeaders)
}

func isCodexInstructionsInvalid(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "instructions are not valid")
}

func isCodexProvider(sdk *SDKIntegration) bool {
	if sdk == nil {
		return false
	}
	return isCodexBackedRequest(sdk.providerName, sdk.currentModel, sdk.provider)
}

// min3 returns the minimum of two integers (helper for string slicing)
func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ProviderEndpointOverride is a per-SDK custom endpoint contract. APIType is
// "openai" or "anthropic"; non-empty key and URL values override all persisted
// credentials, OAuth sessions, environment variables, and proxy settings.
type ProviderEndpointOverride struct {
	APIType string
	APIKey  string
	BaseURL string
}

func resolveProviderProxy(apiType string, endpointOverride *ProviderEndpointOverride) (string, string) {
	if endpointOverride != nil {
		return "", ""
	}
	proxySettings := settings.NewProxySettings()
	return proxySettings.GetProxyForProvider(apiType), proxySettings.GetProxyAPIKey()
}

// buildProviderWithTracker wraps buildProvider to support token tracking.
func buildProviderWithTracker(providerName, currentModel string, logger observability.Logger, tracer observability.Tracer, tokenTracker interface{}, rawDebug bool, endpointOverride *ProviderEndpointOverride) (provider.Provider, string, string, bool, error) {
	return buildProviderWithOverride(providerName, currentModel, logger, tracer, rawDebug, endpointOverride)
}

func buildProvider(providerName, currentModel string, logger observability.Logger, tracer observability.Tracer, rawDebug bool) (provider.Provider, string, string, bool, error) {
	return buildProviderWithOverride(providerName, currentModel, logger, tracer, rawDebug, nil)
}

func buildProviderWithOverride(providerName, currentModel string, logger observability.Logger, tracer observability.Tracer, rawDebug bool, endpointOverride *ProviderEndpointOverride) (provider.Provider, string, string, bool, error) {
	normalized := provider.NormalizeProviderName(providerName)
	modelName := strings.ToLower(currentModel)
	isCodexModel := strings.Contains(modelName, "codex")

	// Try to load provider config to get APIType
	providerCfg, cfgErr := getProviderConfigFromFile(providerName)

	// Determine API type from config or fall back to SDK registry or legacy logic
	var apiType string
	var configBaseURL string
	var authType string
	var httpMaxRetries *int

	// Step 1: Try providers.json first (user overrides)
	if cfgErr == nil && providerCfg != nil && providerCfg.APIType != "" {
		// Use config values if APIType is set
		apiType = providerCfg.APIType
		configBaseURL = providerCfg.BaseURL
		authType = providerCfg.Type
		httpMaxRetries = providerCfg.HTTPMaxRetries
		logDebug("buildProvider: loaded config for %s: apiType=%s, baseURL=%s, authType=%s", providerName, apiType, configBaseURL, authType)
	} else {
		// Step 2: Try SDK registry (authoritative source for known providers)
		sdkResult := lookupProviderInSDKRegistry(providerName)
		if sdkResult.Found {
			apiType = sdkResult.APIType
			configBaseURL = sdkResult.BaseURL
			authType = sdkResult.AuthType
			logDebug("buildProvider: resolved %s from SDK registry: apiType=%s, baseURL=%s", providerName, apiType, configBaseURL)

			// Backfill the provider config so it persists for next time
			if providerCfg != nil {
				backfillProviderConfigFromRegistry(providerCfg, providerName)
			}
		} else {
			// Step 3: Legacy fallback for providers not in config or SDK registry
			logDebug("buildProvider: no config or SDK profile for %s (err: %v), using legacy fallback", providerName, cfgErr)
			switch normalized {
			case "openai", "codex":
				apiType = "openai"
			case "gemini", "gemini-code-assist", "google":
				apiType = "gemini"
			case "xai", "grok":
				apiType = "xai"
			default:
				apiType = "anthropic"
			}
		}
	}

	if endpointOverride != nil && endpointOverride.APIType != "" {
		apiType = endpointOverride.APIType
		authType = "api_key"
		// Override presence is the isolation boundary. Empty values mean use
		// the selected protocol's defaults / no authentication, never values
		// inherited from a persisted provider profile.
		configBaseURL = endpointOverride.BaseURL
		providerCfg = nil
	}

	logDebug("buildProvider: name=%s normalized=%s apiType=%s baseURL=%s", providerName, normalized, apiType, configBaseURL)

	// Safety guard: if a custom provider was saved with api_type="openai" (e.g. by choosing
	// the "OpenAI" preset in the UI for a Z.AI / GLM / other OpenAI-compatible provider),
	// but the provider name is not actually "openai" or "codex", treat it as "openai-compatible"
	// so it uses the provider's own credentials rather than OPENAI_API_KEY.
	if apiType == "openai" && normalized != "openai" && normalized != "codex" {
		logDebug("buildProvider: correcting api_type from 'openai' to 'openai-compatible' for non-OpenAI provider %s", providerName)
		apiType = "openai-compatible"
	}

	// Apply proxy configuration if enabled. A run-scoped endpoint contract is
	// isolated from persisted proxy routing and credentials, even when it only
	// overrides the protocol or key and leaves BaseURL empty.
	proxyURL, proxyAPIKey := resolveProviderProxy(apiType, endpointOverride)
	usingProxy := false
	if proxyURL != "" {
		logDebug("buildProvider: applying proxy URL: %s (original: %s) for apiType: %s", proxyURL, configBaseURL, apiType)
		configBaseURL = proxyURL
		usingProxy = true
	}
	switch apiType {
	case "gemini":
		auth, err := getGeminiAuth(context.Background(), authType, providerName)
		if err != nil {
			return nil, "", "", false, err
		}

		// Use proxy API key if proxy is enabled
		apiKey := auth.apiKey
		if usingProxy && proxyAPIKey != "" {
			apiKey = proxyAPIKey
		}

		cfg := gemini.Config{
			AuthMode: gemini.AuthModeOAuth,
			Model:    currentModel,
			BaseURL:  configBaseURL, // Use proxy URL if set
			Timeout:  300,
			Logger:   logger,
			Tracer:   tracer,
		}

		if !auth.isOAuth || usingProxy {
			cfg.AuthMode = gemini.AuthModeAPIKey
			cfg.APIKey = apiKey
		}

		if normalized == "gemini-code-assist" {
			cfg.Name = "gemini-code-assist"
		}

		// Enable raw debug output if requested
		if rawDebug {
			cfg.RawDebugWriter = os.Stderr
		}

		prov, err := gemini.New(cfg)
		if err != nil {
			return nil, "", "", auth.isOAuth, fmt.Errorf("failed to create gemini provider: %w", err)
		}

		systemPrompt := prompts.GetGeminiSystemPrompt(true) // Interactive mode

		// For Gemini, we might return the access token if available, but for now empty string is fine
		// as the provider manages it internally.
		return prov, systemPrompt, "", auth.isOAuth, nil

	case "openai":
		var auth providerAuth
		var err error
		if endpointOverride != nil {
			auth.apiKey = endpointOverride.APIKey
		} else {
			auth, err = getOpenAIAuth(context.Background(), authType, providerName)
			if err != nil {
				return nil, "", "", false, err
			}
		}

		if auth.isOAuth && !usingProxy {
			resolvedCodexBaseURL := normalizeOpenAIOAuthBaseURL(configBaseURL)
			codexQueryParams, codexHeaders := resolveCodexPassthrough(providerCfg)
			var codexCfg codex.Config = codex.Config{
				AccessToken:      auth.accessToken,
				AccountID:        auth.accountID,
				BaseURL:          resolvedCodexBaseURL,
				Logger:           logger,
				Tracer:           tracer,
				ExtraQueryParams: codexQueryParams,
				ExtraHeaders:     codexHeaders,
				// Catalog-driven per-model wire conventions; nil falls
				// back to the provider's prefix heuristics.
				ModelOptions: codex.ModelWireOptionsFromCache(),
			}

			// Enable raw debug output if requested
			if rawDebug {
				codexCfg.RawDebugWriter = os.Stderr
			}

			var codexProvider *codex.Provider
			codexProvider, err = codex.New(codexCfg)
			if err != nil {
				return nil, "", "", auth.isOAuth, fmt.Errorf("failed to create codex provider: %w", err)
			}

			reportedBaseURL := resolvedCodexBaseURL
			if reportedBaseURL == "" {
				reportedBaseURL = defaultOpenAIOAuthCodexBaseURL
			}

			var systemPrompt string
			systemPrompt, _ = prompts.RefreshCodexSystemPromptForModel(context.Background(), currentModel)
			if systemPrompt == "" {
				systemPrompt = prompts.GetCodexSystemPromptForModel(context.Background(), currentModel)
			}
			return codexProvider, systemPrompt, auth.accessToken, auth.isOAuth, nil
		}

		// Use proxy API key if proxy is enabled, otherwise use provider API key
		apiKey := auth.apiKey
		if usingProxy && proxyAPIKey != "" {
			apiKey = proxyAPIKey
		}

		cfg := openai.Config{
			APIKey:         apiKey,
			BaseURL:        configBaseURL, // Use proxy URL if set, otherwise will be empty and provider uses default
			OrganizationID: auth.orgID,
			Logger:         logger,
			Tracer:         tracer,
			HTTPMaxRetries: httpMaxRetries,
		}

		// Enable raw debug output if requested
		if rawDebug {
			cfg.RawDebugWriter = os.Stderr
		}

		prov, err := openai.New(cfg)
		if err != nil {
			return nil, "", "", auth.isOAuth, fmt.Errorf("failed to create openai provider: %w", err)
		}

		systemPrompt := "You are a helpful AI assistant."
		if normalized == "codex" || isCodexModel {
			systemPrompt, _ = prompts.RefreshCodexSystemPromptForModel(context.Background(), currentModel)
			if systemPrompt == "" {
				systemPrompt = prompts.GetCodexSystemPromptForModel(context.Background(), currentModel)
			}
		}

		return prov, systemPrompt, auth.apiKey, auth.isOAuth, nil

	case "xai":
		// xAI / Grok — OpenAI-compatible transport at https://api.x.ai/v1.
		// Accepts either XAI_API_KEY or a SuperGrok OAuth bearer token.
		auth, err := getXAIAuth(context.Background(), authType, providerName)
		if err != nil {
			return nil, "", "", false, err
		}

		xaiBaseURL := auth.baseURL
		if xaiBaseURL == "" {
			xaiBaseURL = configBaseURL
		}
		if xaiBaseURL == "" {
			xaiBaseURL = defaultXAIBaseURL
		}

		// Use proxy API key if proxy is enabled
		xaiKey := auth.apiKey
		if usingProxy && proxyAPIKey != "" {
			xaiKey = proxyAPIKey
		}

		cfg := openai.Config{
			APIKey:         xaiKey,
			BaseURL:        xaiBaseURL,
			Name:           "xai",
			Logger:         logger,
			Tracer:         tracer,
			HTTPMaxRetries: httpMaxRetries,
		}

		// Enable raw debug output if requested
		if rawDebug {
			cfg.RawDebugWriter = os.Stderr
		}

		prov, err := openai.New(cfg)
		if err != nil {
			return nil, "", "", auth.isOAuth, fmt.Errorf("failed to create xAI provider: %w", err)
		}

		return prov, "You are Grok, a helpful AI assistant made by xAI.", auth.apiKey, auth.isOAuth, nil

	case "cursor":
		// Cursor (Anysphere) — Connect-RPC provider.
		// Cursor's backend at api2.cursor.sh does NOT speak OpenAI's
		// /v1/chat/completions; it uses Connect-RPC (ChatService).
		token, err := getCursorAccessToken()
		if err != nil {
			return nil, "", "", false, fmt.Errorf("cursor: %w", err)
		}

		cursorBaseURL := configBaseURL
		if cursorBaseURL == "" {
			cursorBaseURL = "https://api2.cursor.sh"
		}

		cursorCfg := cursor.Config{
			APIKey:  token,
			BaseURL: cursorBaseURL,
			Name:    normalized,
		}
		if rawDebug {
			cursorCfg.RawDebugWriter = os.Stderr
		}

		prov, err := cursor.New(cursorCfg)
		if err != nil {
			return nil, "", "", false, fmt.Errorf("failed to create cursor provider: %w", err)
		}

		logDebug("buildProvider: created cursor provider name=%s baseURL=%s", normalized, cursorBaseURL)

		return prov, "You are a helpful AI assistant.", token, true, nil

	case "openai-compatible":
		// OpenAI-compatible providers like Cerebras, OpenRouter
		// (Cursor is NOT here — it has its own case below)
		var auth providerAuth
		var err error
		if endpointOverride != nil {
			auth.apiKey = endpointOverride.APIKey
		} else {
			auth, err = getOpenAICompatibleAuth(providerName)
			if err != nil {
				return nil, "", "", false, err
			}
		}

		// providers.json (configBaseURL) is what the settings UI edits, so it
		// wins; auth.baseURL may be a stale copy from credentials.json saved
		// when the key was first entered. configBaseURL is also the proxy URL
		// when a proxy is enabled, which must take precedence anyway.
		baseURL := configBaseURL
		if baseURL == "" {
			baseURL = auth.baseURL
		}
		if baseURL == "" {
			return nil, "", "", false, fmt.Errorf("no base URL configured for %s", providerName)
		}

		// Use proxy API key if proxy is enabled, otherwise use provider API key
		apiKey := auth.apiKey
		if usingProxy && proxyAPIKey != "" {
			apiKey = proxyAPIKey
		}

		cfg := openai.Config{
			APIKey:         apiKey,
			BaseURL:        baseURL,
			Name:           strings.ToLower(strings.TrimSpace(providerName)), // User-chosen name, not normalized alias
			Logger:         logger,
			Tracer:         tracer,
			HTTPMaxRetries: httpMaxRetries,
		}

		// Enable raw debug output if requested
		if rawDebug {
			cfg.RawDebugWriter = os.Stderr
		}

		prov, err := openai.New(cfg)
		if err != nil {
			return nil, "", "", false, fmt.Errorf("failed to create %s provider: %w", providerName, err)
		}

		logDebug("buildProvider: created openai-compatible provider name=%s baseURL=%s", normalized, baseURL)

		systemPrompt := "You are a helpful AI assistant."
		return prov, systemPrompt, auth.apiKey, false, nil

	default: // "anthropic" or unknown
		var auth providerAuth
		var err error
		if endpointOverride != nil {
			auth.apiKey = endpointOverride.APIKey
		} else {
			auth, err = getAnthropicAuth(authType, providerName)
			if err != nil {
				return nil, "", "", false, err
			}
		}

		// Use proxy URL if set, otherwise use auth.baseURL, otherwise default
		baseURL := configBaseURL
		if baseURL == "" {
			baseURL = auth.baseURL
		}
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}

		// Use proxy API key if proxy is enabled AND not using OAuth
		// When using OAuth, we send the OAuth token through the proxy
		apiKey := auth.apiKey
		if usingProxy && proxyAPIKey != "" && !auth.isOAuth {
			apiKey = proxyAPIKey
		} else if usingProxy && auth.isOAuth {
		}

		cfg := anthropic.Config{
			APIKey:       apiKey,
			BaseURL:      baseURL,
			DefaultModel: "", // Will use model from request
			Timeout:      -1, // No timeout for streaming - responses can take minutes for large contexts
			MaxRetries:   3,
			IsOAuth:      auth.isOAuth,
			BetaHeaders:  []string{anthropic.BetaPromptCaching},
		}

		cfg.Logger = logger
		cfg.Tracer = tracer

		// Enable raw debug output if requested
		if rawDebug {
			cfg.RawDebugWriter = os.Stderr
		}

		prov, err := anthropic.New(cfg)
		if err != nil {
			return nil, "", "", auth.isOAuth, fmt.Errorf("failed to create anthropic provider: %w", err)
		}

		systemPrompt := "You are a helpful AI assistant."
		if auth.isOAuth {
			systemPrompt = anthropic.GetCLISystemPromptPrefix()
		}

		return prov, systemPrompt, auth.apiKey, auth.isOAuth, nil
	}
}

func defaultModelForProvider(providerName string) string {
	switch provider.NormalizeProviderName(providerName) {
	case "gemini", "gemini-code-assist", "google":
		return "gemini-2.5-pro"
	case "openai", "codex":
		return "gpt-4-turbo"
	case "xai":
		return "grok-4.3"
	default:
		return "claude-3-5-sonnet-20241022"
	}
}

// intPtr returns a pointer to an int
func intPtr(i int) *int {
	return &i
}

// Note: truncateString is defined in debug_provider.go
