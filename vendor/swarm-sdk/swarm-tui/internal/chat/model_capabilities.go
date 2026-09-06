// Package chat provides model capabilities fetching from provider APIs
package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// defaultProviderRegistry is the built-in fallback used to seed ~/.swarmos/provider_registry.json
// if it doesn't exist yet. Maps local provider names to models.dev API keys.
// "_aggregator" means match across ALL models.dev providers (for meta-routers like OpenRouter).
var defaultProviderRegistry = map[string]string{
	"claudecode":  "anthropic",
	"anthropic":   "anthropic",
	"openai":      "openai",
	"codex":       "openai",
	"gemini":      "google",
	"cerebras":    "cerebras",
	"cerebrasv2":  "cerebras",
	"groq":        "groq",
	"together":    "together",
	"together-ai": "together",
	"togetherai":  "together",
	"deepseek":    "deepseek",
	"perplexity":  "perplexity",
	"fireworks":   "fireworks",
	"z.ai":        "zai",
	"openrouter":  "_aggregator",
}

// LoadProviderRegistry reads the provider registry from ~/.swarmos/provider_registry.json.
// If the file doesn't exist, it creates it with defaults and returns those.
func LoadProviderRegistry() map[string]string {
	home, err := os.UserHomeDir()
	if err != nil {
		logDebug("[ProviderRegistry] Failed to get home dir: %v, using defaults", err)
		return defaultProviderRegistry
	}

	registryPath := filepath.Join(home, ".swarmos", "provider_registry.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		// File doesn't exist — create it with defaults
		logDebug("[ProviderRegistry] Creating %s with defaults", registryPath)
		if writeErr := writeProviderRegistry(registryPath, defaultProviderRegistry); writeErr != nil {
			logDebug("[ProviderRegistry] Failed to write defaults: %v", writeErr)
		}
		return defaultProviderRegistry
	}

	var registry map[string]string
	if err := json.Unmarshal(data, &registry); err != nil {
		logDebug("[ProviderRegistry] Failed to parse %s: %v, using defaults", registryPath, err)
		return defaultProviderRegistry
	}

	logDebug("[ProviderRegistry] Loaded %d provider mappings from %s", len(registry), registryPath)
	return registry
}

func writeProviderRegistry(path string, registry map[string]string) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func lookupKnownContextWindow(modelID string) int {
	if modelID == "" {
		return 0
	}
	// Delegate to SDK which reads from ~/.swarmos/providers.json.
	// Empty providerName matches any provider entry in the catalog.
	if cw, ok := client.LookupModelContextWindow("", modelID); ok {
		return cw
	}
	return 0
}

// UpdateProvidersWithCapabilities updates providers.json with context window values
// (legacy flat map path — kept for OpenRouter fallback)
func UpdateProvidersWithCapabilities(capabilities map[string]int) error {
	providersJSONMu.Lock()
	defer providersJSONMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	providersPath := filepath.Join(home, ".swarmos", "providers.json")

	// Read current config
	data, err := readProvidersJSONWithRecovery(providersPath)
	if err != nil {
		return err
	}

	var providers []map[string]any
	if err := json.Unmarshal(data, &providers); err != nil {
		return err
	}

	updated := 0

	// Update each provider's models with context_window
	for i, provider := range providers {
		models, ok := provider["models"].([]any)
		if !ok {
			continue
		}

		for j, model := range models {
			modelMap, ok := model.(map[string]any)
			if !ok {
				continue
			}

			modelID, _ := modelMap["id"].(string)
			if modelID == "" {
				continue
			}

			var contextWindow int

			// Check capabilities map first (exact match), then normalized known values
			if cw, found := capabilities[modelID]; found && cw > 0 {
				contextWindow = cw
			} else if cw, found := capabilities[normalizeModelID(modelID)]; found && cw > 0 {
				contextWindow = cw
			} else if cw := lookupKnownContextWindow(modelID); cw > 0 {
				contextWindow = cw
			}

			if contextWindow > 0 {
				modelMap["context_window"] = contextWindow
				// Remove old "context" field if present
				delete(modelMap, "context")
				models[j] = modelMap
				updated++
			}
		}

		providers[i]["models"] = models
	}

	// Write updated config
	newData, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return err
	}

	if err := atomicWriteProvidersJSON(providersPath, newData, 0644); err != nil {
		return err
	}

	logDebug("[ModelCapabilities] Updated %d models with context_window values", updated)
	return nil
}

// EnrichProvidersFromModelsDev enriches providers.json with full model data from models.dev.
// Uses provider-scoped matching: each local provider is mapped to a models.dev provider key
// via ~/.swarmos/provider_registry.json. Only enriches fields that are missing or zero-valued
// in providers.json — user overrides are preserved.
func EnrichProvidersFromModelsDev(modelsDevData map[string]ModelsDevProvider) error {
	providersJSONMu.Lock()
	defer providersJSONMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	providersPath := filepath.Join(home, ".swarmos", "providers.json")
	data, err := readProvidersJSONWithRecovery(providersPath)
	if err != nil {
		return err
	}

	var providers []map[string]any
	if err := json.Unmarshal(data, &providers); err != nil {
		return err
	}

	// Load provider registry from config
	registry := LoadProviderRegistry()

	// Build flat lookup for aggregator providers (OpenRouter) — all models across all providers
	flatLookup := buildFlatModelLookup(modelsDevData)

	enriched := 0

	for i, provider := range providers {
		provName, _ := provider["name"].(string)
		if provName == "" {
			continue
		}

		// Look up models.dev key for this provider
		modelsDevKey, registered := registry[strings.ToLower(provName)]
		if !registered {
			logDebug("[Enrich] Skipping unregistered provider: %s", provName)
			continue
		}

		models, ok := provider["models"].([]any)
		if !ok {
			continue
		}

		// Get the models.dev model set for this provider
		var providerModels map[string]ModelsDevModel
		if modelsDevKey == "_aggregator" {
			// For aggregators like OpenRouter, use the flat lookup
			providerModels = flatLookup
		} else {
			mdProv, found := modelsDevData[modelsDevKey]
			if !found {
				logDebug("[Enrich] models.dev provider not found: %s (mapped from %s)", modelsDevKey, provName)
				continue
			}
			providerModels = mdProv.Models
		}

		for j, model := range models {
			modelMap, ok := model.(map[string]any)
			if !ok {
				continue
			}

			modelID, _ := modelMap["id"].(string)
			if modelID == "" {
				continue
			}

			// Find matching model in models.dev data
			mdModel, found := findModelsDevMatch(modelID, providerModels)
			if !found {
				continue
			}

			// Enrich fields — only set if missing/zero in providers.json
			if enrichModelField(modelMap, "context_window", int(mdModel.Limit.Context)) {
				delete(modelMap, "context") // Remove legacy field
			}
			enrichModelField(modelMap, "max_output_tokens", int(mdModel.Limit.Output))
			enrichModelFieldFloat(modelMap, "cost_input", mdModel.Cost.Input)
			enrichModelFieldFloat(modelMap, "cost_output", mdModel.Cost.Output)
			enrichModelFieldBool(modelMap, "reasoning", mdModel.Reasoning)
			enrichModelFieldBool(modelMap, "tool_call", mdModel.ToolCall)

			// Set display_name from models.dev if not already set
			if existing, _ := modelMap["display_name"].(string); existing == "" && mdModel.Name != "" {
				modelMap["display_name"] = mdModel.Name
			}

			models[j] = modelMap
			enriched++
		}

		providers[i]["models"] = models
	}

	// Write enriched config
	newData, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return err
	}

	if err := atomicWriteProvidersJSON(providersPath, newData, 0644); err != nil {
		return err
	}

	logDebug("[Enrich] Enriched %d models from models.dev", enriched)
	return nil
}

// buildFlatModelLookup creates a flat map of canonicalized model ID -> ModelsDevModel
// across ALL providers. Used for aggregator providers like OpenRouter.
func buildFlatModelLookup(data map[string]ModelsDevProvider) map[string]ModelsDevModel {
	flat := make(map[string]ModelsDevModel)
	for _, prov := range data {
		for modelID, model := range prov.Models {
			canonical := canonicalizeModelID(modelID)
			flat[canonical] = model
		}
	}
	return flat
}

// findModelsDevMatch finds a matching model in the models.dev data set.
// Tries exact match, then canonicalized match (strips date suffixes, normalizes versions).
func findModelsDevMatch(localID string, modelsDevModels map[string]ModelsDevModel) (ModelsDevModel, bool) {
	// Exact match
	if m, found := modelsDevModels[localID]; found {
		return m, true
	}

	// Canonicalized match — handles date suffixes and version format differences
	// e.g., "claude-haiku-4-5-20251001" → "claude-haiku-4.5" matches models.dev "claude-haiku-4.5"
	canonicalLocal := canonicalizeModelID(localID)
	if m, found := modelsDevModels[canonicalLocal]; found {
		return m, true
	}

	// Cross-canonicalize: both sides may need normalization
	for mdID, m := range modelsDevModels {
		if canonicalizeModelID(mdID) == canonicalLocal {
			return m, true
		}
	}

	return ModelsDevModel{}, false
}

// enrichModelField sets an int field on modelMap only if the current value is missing or zero.
// Returns true if the field was set.
func enrichModelField(modelMap map[string]any, key string, value int) bool {
	if value <= 0 {
		return false
	}
	existing, _ := modelMap[key].(float64) // JSON numbers decode as float64
	if existing > 0 {
		return false // User already set a value, don't override
	}
	modelMap[key] = value
	return true
}

// enrichModelFieldFloat sets a float64 field only if missing or zero.
func enrichModelFieldFloat(modelMap map[string]any, key string, value float64) {
	if value <= 0 {
		return
	}
	existing, _ := modelMap[key].(float64)
	if existing > 0 {
		return
	}
	modelMap[key] = value
}

// enrichModelFieldBool sets a bool field only if value is true and field is not already set.
func enrichModelFieldBool(modelMap map[string]any, key string, value bool) {
	if !value {
		return
	}
	if _, exists := modelMap[key]; exists {
		return // Don't override existing value
	}
	modelMap[key] = value
}

// hashProvidersJSON computes a SHA-256 hash of providers.json content.
func hashProvidersJSON() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(home, ".swarmos", "providers.json"))
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// getStoredHash reads the previously stored providers.json hash from the cache file.
func getStoredHash() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".swarmos", ".capabilities_hash"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// storeHash saves the providers.json hash to the cache file.
func storeHash(hash string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, ".swarmos", ".capabilities_hash"), []byte(hash), 0644)
}

// RefreshModelCapabilities fetches and updates model capabilities if needed.
// This should be called on app startup. It hashes providers.json and only
// fetches from models.dev when the config has changed (new model added, etc.).
func RefreshModelCapabilities() {
	// Hash current providers.json
	currentHash, err := hashProvidersJSON()
	if err != nil {
		logDebug("[ModelCapabilities] Failed to hash providers.json: %v, forcing refresh", err)
		currentHash = ""
	}

	// Compare with stored hash — skip if unchanged
	storedHash := getStoredHash()
	if currentHash != "" && currentHash == storedHash {
		logDebug("[ModelCapabilities] providers.json unchanged (hash match), skipping refresh")
		return
	}

	logDebug("[ModelCapabilities] providers.json changed (hash mismatch), refreshing from models.dev...")

	// Primary path: fetch provider-scoped data from models.dev for rich enrichment
	modelsDevData, fetchErr := FetchModelsDevByProvider()
	if fetchErr != nil {
		logDebug("[ModelCapabilities] Failed to fetch from models.dev: %v, falling back to flat enrichment", fetchErr)

		// Fallback: flat context-window-only enrichment
		capabilities, fallbackErr := FetchOpenRouterModels("")
		if fallbackErr != nil {
			logDebug("[ModelCapabilities] Fallback to OpenRouter also failed: %v", fallbackErr)
			capabilities = make(map[string]int)
		}
		if updateErr := UpdateProvidersWithCapabilities(capabilities); updateErr != nil {
			logDebug("[ModelCapabilities] Failed to update providers.json: %v", updateErr)
			return
		}
	} else {
		// Provider-scoped enrichment with full model data
		if enrichErr := EnrichProvidersFromModelsDev(modelsDevData); enrichErr != nil {
			logDebug("[ModelCapabilities] Failed to enrich providers.json: %v", enrichErr)
			return
		}
	}

	// Store the NEW hash (after enrichment wrote to providers.json)
	newHash, err := hashProvidersJSON()
	if err != nil {
		logDebug("[ModelCapabilities] Failed to hash enriched providers.json: %v", err)
		return
	}
	if err := storeHash(newHash); err != nil {
		logDebug("[ModelCapabilities] Failed to store hash: %v", err)
	}

	logDebug("[ModelCapabilities] Successfully refreshed model capabilities")
}
