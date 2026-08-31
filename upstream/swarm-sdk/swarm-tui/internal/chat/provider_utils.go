package chat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// resolveAPITypeResult holds the resolved API type and default base URL for a provider.
type resolveAPITypeResult struct {
	APIType        string
	DefaultBaseURL string
}

// resolveAPITypeFromName maps a provider name to its API type and default base URL.
// This is the canonical fallback when providers.json entries lack an api_type field
// (e.g. files written by older versions, or migrated/synced configs).
//
// The SDK's builtin profile registry is the authoritative source for provider routing.
// The legacy switch below serves as a fallback for providers not in the SDK registry.
//
// The mapping distinguishes between "openai" (uses OPENAI_API_KEY / Codex OAuth)
// and "openai-compatible" (uses provider-specific API key + base URL) so that
// credential resolution routes to the correct auth backend.
func resolveAPITypeFromName(providerName string) resolveAPITypeResult {
	// Step 1: Try SDK registry (authoritative source of truth)
	if sdkResult := lookupProviderInSDKRegistry(providerName); sdkResult.Found {
		return resolveAPITypeResult{
			APIType:        sdkResult.APIType,
			DefaultBaseURL: sdkResult.BaseURL,
		}
	}

	// Step 2: Legacy fallback for providers not in SDK registry
	normalized := provider.NormalizeProviderName(providerName)

	switch normalized {
	// First-class providers with their own auth flow.
	case "openai":
		return resolveAPITypeResult{APIType: "openai"}
	case "anthropic":
		return resolveAPITypeResult{APIType: "anthropic"}
	case "gemini":
		return resolveAPITypeResult{APIType: "gemini"}

	// OpenAI-compatible providers — each has a known default base URL.
	case "cerebras":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.cerebras.ai/v1"}
	case "fireworks":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.fireworks.ai/inference/v1"}
	case "groq":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.groq.com/openai/v1"}
	case "openrouter":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://openrouter.ai/api/v1"}
	case "plexus":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "http://localhost:4000/v1"}
	case "together":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.together.xyz/v1"}
	case "deepseek":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.deepseek.com/v1"}
	case "perplexity":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.perplexity.ai"}
	case "z.ai":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.z.ai/api/coding/paas/v4"}
	case "wafer.ai":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://pass.wafer.ai/v1"}
	case "mistral":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.mistral.ai/v1"}
	case "moonshot":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.moonshot.cn/v1"}
	case "replicate":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://api.replicate.com/v1"}
	case "chutes":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://llm.chutes.ai/v1"}
	case "qwen":
		return resolveAPITypeResult{APIType: "openai-compatible", DefaultBaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"}
	case "xai":
		// xAI / Grok — first-class provider with SuperGrok OAuth + API key support.
		// Uses OpenAI-compatible API at https://api.x.ai/v1.
		return resolveAPITypeResult{APIType: "xai", DefaultBaseURL: defaultXAIBaseURL}
	case "cursor":
		// Cursor (Anysphere) — own wire family (Connect-RPC at api2.cursor.sh).
		return resolveAPITypeResult{APIType: "cursor", DefaultBaseURL: "https://api2.cursor.sh"}
	case "kimi":
		return resolveAPITypeResult{APIType: "openai-compatible"}
	case "meta":
		return resolveAPITypeResult{APIType: "openai-compatible"}
	case "zhipu":
		return resolveAPITypeResult{APIType: "openai-compatible"}

	// Anthropic-compatible providers.
	case "minimax":
		return resolveAPITypeResult{APIType: "anthropic", DefaultBaseURL: "https://api.minimax.io/anthropic"}

	// Local / Ollama — OpenAI-compatible with no fixed base URL.
	case "local":
		return resolveAPITypeResult{APIType: "openai-compatible"}

	default:
		// Unknown provider — assume openai-compatible so the user can provide
		// their own base URL and API key via the settings UI.
		return resolveAPITypeResult{APIType: "openai-compatible"}
	}
}

// loadAllProviders loads provider configs from both SDK config path and legacy path.
// It tries ~/.swarmos/config/providers.json first (map format), then ~/.swarmos/providers.json (array format).
// Returns a slice of ProviderConfig regardless of which format was found.
func loadAllProviders() ([]ProviderConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	sdkConfigPath := filepath.Join(home, ".swarmos", "config", "providers.json")
	legacyPath := filepath.Join(home, ".swarmos", "providers.json")

	// MERGE both sources instead of using whichever exists first. A partial
	// SDK-config map (e.g. one that only contains "fireworks") used to SHADOW
	// the complete legacy array, so custom providers like OpenRouter were never
	// registered and any profile/chain referencing them failed "provider not
	// registered". Layer them: legacy array is the base, the SDK-config map
	// takes precedence (it carries backfilled api_type). Keyed by lowercase
	// name; insertion order preserved.
	byName := make(map[string]ProviderConfig)
	order := make([]string, 0, 32)
	add := func(pc ProviderConfig, fallbackName string) {
		if pc.Name == "" {
			pc.Name = fallbackName
		}
		key := strings.ToLower(strings.TrimSpace(pc.Name))
		if key == "" {
			return
		}
		if _, exists := byName[key]; !exists {
			order = append(order, key)
		}
		byName[key] = pc // legacy first (base), map second (overrides value)
	}

	// Base layer: legacy array format. Recovery-aware: a 0-byte or torn
	// providers.json (a crash or a concurrent TUI/daemon writer mid-write)
	// used to silently yield ZERO providers — the settings, Manage Providers
	// and Auth screens all went empty. Now the bad file is quarantined and
	// the newest parseable backup is restored in its place.
	if data, rerr := readProvidersJSONWithRecovery(legacyPath); rerr == nil {
		var arr []ProviderConfig
		if json.Unmarshal(data, &arr) == nil {
			for _, pc := range arr {
				add(pc, "")
			}
		}
	}
	// Precedence layer: SDK config map format.
	if data, rerr := readProvidersJSONWithRecovery(sdkConfigPath); rerr == nil {
		var m map[string]ProviderConfig
		if json.Unmarshal(data, &m) == nil {
			for name, pc := range m {
				add(pc, name)
			}
		}
	}

	if len(byName) == 0 {
		return nil, errors.New("no providers found in ~/.swarmos/config/providers.json or ~/.swarmos/providers.json")
	}

	providers := make([]ProviderConfig, 0, len(order))
	for _, key := range order {
		providers = append(providers, byName[key])
	}

	// Backfill missing api_type from SDK registry and persist the merged,
	// complete config back to the SDK map path (self-heals a partial file).
	if updated, _ := BackfillProvidersConfig(providers); updated {
		saveBackfilledProviders(sdkConfigPath, providers)
	}

	return providers, nil
}

// saveBackfilledProviders persists backfilled provider configs to disk.
// It writes in the SDK map format (preferred) to the config path.
// Errors are logged but not returned — backfill is best-effort and
// should not block startup.
func saveBackfilledProviders(configPath string, providers []ProviderConfig) {
	// Convert slice to map format for SDK compatibility
	providerMap := make(map[string]ProviderConfig, len(providers))
	for _, p := range providers {
		name := p.Name
		if name == "" {
			continue
		}
		providerMap[strings.ToLower(name)] = p
	}

	data, err := json.MarshalIndent(providerMap, "", "  ")
	if err != nil {
		logDebug("[Backfill] Failed to marshal backfilled providers: %v", err)
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logDebug("[Backfill] Failed to create config directory: %v", err)
		return
	}

	if err := atomicWriteProvidersJSON(configPath, data, 0644); err != nil {
		logDebug("[Backfill] Failed to write backfilled providers: %v", err)
		return
	}

	logDebug("[Backfill] Successfully persisted backfilled provider configs to %s", configPath)
}

// extractProviderFromModel extracts the provider name from a model string.
// Delegates to client.ExtractProviderFromModel (Z2 decoupling, ACP-2026-SDK-003-A2).
func extractProviderFromModel(model string) string {
	return client.ExtractProviderFromModel(model)
}

// findProviderConfig finds a provider config by name from the loaded configs
// Returns nil if not found
func findProviderConfig(providerConfigs []ProviderConfig, providerName string) *ProviderConfig {
	if providerName == "" {
		return nil
	}

	providerName = strings.ToLower(strings.TrimSpace(providerName))

	// Try exact match first
	for i := range providerConfigs {
		if strings.ToLower(providerConfigs[i].Name) == providerName {
			return &providerConfigs[i]
		}
	}

	// Try display name match
	for i := range providerConfigs {
		if strings.ToLower(providerConfigs[i].DisplayName) == providerName {
			return &providerConfigs[i]
		}
	}

	// Try partial match (e.g., "claude" matches "ClaudeCode")
	for i := range providerConfigs {
		lowerName := strings.ToLower(providerConfigs[i].Name)
		lowerDisplay := strings.ToLower(providerConfigs[i].DisplayName)
		if strings.Contains(lowerName, providerName) || strings.Contains(lowerDisplay, providerName) {
			return &providerConfigs[i]
		}
	}

	return nil
}

// getProviderColor returns the color for a provider from loaded configs
// Falls back to a default color if not found
func getProviderColor(providerConfigs []ProviderConfig, providerName string) string {
	config := findProviderConfig(providerConfigs, providerName)
	if config != nil && config.Color != "" {
		return config.Color
	}

	// Default fallback colors for common providers (if not in config)
	switch strings.ToLower(providerName) {
	case "anthropic", "claudecode":
		return "#D4A574" // Bronze/gold
	case "openai":
		return "#19C37D" // Green
	case "google", "gemini":
		return "#4285F4" // Google blue
	case "cerebras":
		return "#FF6B6B" // Red
	case "xai":
		return "#F0ABFC" // Pink
	case "deepseek":
		return "#86EFAC" // Light green
	case "wafer", "wafer.ai":
		return "#00D4AA" // Teal/cyan
	case "meta":
		return "#FCA5A5" // Light red
	case "mistral":
		return "#FDBA74" // Orange
	case "zhipu":
		return "#A78BFA" // Purple
	case "qwen":
		return "#F59E0B" // Amber
	case "minimax":
		return "#7DD3FC" // Sky blue
	case "groq":
		return "#F97316" // Orange
	case "together", "together-ai", "togetherai":
		return "#34D399" // Emerald
	case "perplexity":
		return "#60A5FA" // Blue
	case "fireworks":
		return "#FB923C" // Orange-red
	case "local":
		return "#D1D5DB" // Light gray
	case "openrouter":
		return "#C4B5FD" // Purple
	default:
		return "#9CA3AF" // Gray fallback
	}
}

// getProviderIcon returns an icon for a provider
// Uses simple symbols differentiated by provider
func getProviderIcon(providerName string) string {
	switch strings.ToLower(providerName) {
	case "anthropic", "claudecode":
		return "◆"
	case "openai":
		return "◇"
	case "google", "gemini":
		return "◈"
	case "cerebras":
		return "◉"
	case "xai":
		return "◒"
	case "deepseek":
		return "◓"
	case "wafer", "wafer.ai":
		return "◈"
	case "meta":
		return "◎"
	case "mistral":
		return "◑"
	case "zhipu":
		return "◐"
	case "qwen":
		return "◑"
	case "minimax":
		return "◊"
	case "groq":
		return "◈"
	case "together", "together-ai", "togetherai":
		return "◎"
	case "perplexity":
		return "◍"
	case "fireworks":
		return "◉"
	case "local":
		return "◕"
	case "openrouter":
		return "◔"
	default:
		return "◆" // Default diamond
	}
}

// getModelShortName extracts a short display name from a model string
func getModelShortName(model string) string {
	if model == "" {
		return ""
	}

	model = strings.TrimSpace(model)
	lowerModel := strings.ToLower(model)

	// Remove common prefixes
	model = strings.TrimPrefix(model, "models/")

	// If it has a provider prefix (provider/model), extract just the model part
	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		if len(parts) == 2 {
			model = parts[1]
			lowerModel = strings.ToLower(model)
		}
	}

	// Claude models - specific variants
	if strings.Contains(lowerModel, "opus") {
		return "opus"
	}
	if strings.Contains(lowerModel, "sonnet") {
		return "sonnet"
	}
	if strings.Contains(lowerModel, "haiku") {
		return "haiku"
	}

	// GPT models
	if strings.Contains(lowerModel, "gpt-4") {
		if strings.Contains(lowerModel, "turbo") {
			return "gpt-4-turbo"
		}
		return "gpt-4"
	}
	if strings.Contains(lowerModel, "gpt-3.5") {
		return "gpt-3.5"
	}

	// O-series models
	if strings.HasPrefix(lowerModel, "o1") {
		return "o1"
	}
	if strings.HasPrefix(lowerModel, "o3") {
		return "o3"
	}

	// Gemini models - extract version info
	if strings.Contains(lowerModel, "gemini") {
		if strings.Contains(lowerModel, "3") {
			if strings.Contains(lowerModel, "pro") {
				return "3Pro"
			}
			if strings.Contains(lowerModel, "flash") {
				return "3Flash"
			}
			return "gemini3"
		}
		if strings.Contains(lowerModel, "2.0") || strings.Contains(lowerModel, "2-0") {
			if strings.Contains(lowerModel, "flash") {
				return "2Flash"
			}
			return "gemini2"
		}
		if strings.Contains(lowerModel, "1.5") || strings.Contains(lowerModel, "1-5") {
			if strings.Contains(lowerModel, "pro") {
				return "1.5Pro"
			}
			if strings.Contains(lowerModel, "flash") {
				return "1.5Flash"
			}
			return "gemini1.5"
		}
		return "gemini"
	}

	// Llama models
	if strings.Contains(lowerModel, "llama") {
		return "llama"
	}

	// Other common models
	if strings.Contains(lowerModel, "grok") {
		return "grok"
	}
	if strings.Contains(lowerModel, "deepseek") {
		return "deepseek"
	}
	if strings.Contains(lowerModel, "mistral") || strings.Contains(lowerModel, "mixtral") {
		return "mistral"
	}

	// For unknown models, truncate to reasonable length
	runes := []rune(model)
	if len(runes) > 12 {
		return string(runes[:9]) + "..."
	}

	return model
}
