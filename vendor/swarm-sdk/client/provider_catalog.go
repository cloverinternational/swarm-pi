package client

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// providerCatalogEntry mirrors the shape of ~/.swarm/config/providers.json entries
// that the TUI writes. We only decode the fields the SDK needs — context
// window and model identity — and ignore the rest so schema evolution in the
// TUI doesn't break SDK consumers.
type providerCatalogEntry struct {
	Name        string                `json:"name"`
	DisplayName string                `json:"display_name"`
	Models      []providerCatalogItem `json:"models"`
}

type providerCatalogItem struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	ContextWindow int    `json:"context_window"` // canonical integer form
	Context       string `json:"context"`        // legacy/string form: "262144", "200k", "1M"
}

// LookupModelContextWindow resolves the context window (in tokens) for a given
// provider + model from ~/.swarm/config/providers.json. Returns (0, false) when
// the file is missing, the entry is absent, or no window is recorded.
//
// Matching precedence:
//  1. Exact provider name (or display_name) + exact model ID.
//  2. Exact model ID under any provider (handles model strings that identify
//     themselves, e.g. "accounts/fireworks/routers/kimi-k2p5-turbo").
//
// Window parsing accepts integer tokens ("262144"), lowercase-k shorthand
// ("200k" → 200_000), and uppercase-M shorthand ("1M" → 1_000_000). On parse
// failure the field is ignored and the next fallback runs.
func LookupModelContextWindow(providerName, modelID string) (int, bool) {
	return lookupContextWindowFrom(
		paths.ProvidersFile(),
		providerName, modelID,
	)
}

func lookupContextWindowFrom(path, providerName, modelID string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var catalog []providerCatalogEntry
	if err := json.Unmarshal(data, &catalog); err != nil {
		return 0, false
	}

	normProvider := strings.ToLower(strings.TrimSpace(providerName))
	normModel := strings.TrimSpace(modelID)

	var exact, anyProvider int
	var exactOK, anyOK bool

	for _, p := range catalog {
		providerMatch := normProvider == "" ||
			strings.EqualFold(p.Name, normProvider) ||
			strings.EqualFold(p.DisplayName, normProvider)
		for _, m := range p.Models {
			if m.ID != normModel {
				continue
			}
			cw, ok := parseContextWindow(m)
			if !ok {
				continue
			}
			if providerMatch && !exactOK {
				exact, exactOK = cw, true
			} else if !anyOK {
				anyProvider, anyOK = cw, true
			}
		}
	}
	if exactOK {
		return exact, true
	}
	if anyOK {
		return anyProvider, true
	}
	return 0, false
}

// parseContextWindow prefers the canonical integer field. Falls back to the
// legacy string form with k/M suffix support.
func parseContextWindow(m providerCatalogItem) (int, bool) {
	if m.ContextWindow > 0 {
		return m.ContextWindow, true
	}
	s := strings.TrimSpace(m.Context)
	if s == "" {
		return 0, false
	}
	return parseTokenCount(s)
}

// ModelCapabilityInfo describes the runtime capabilities of a specific model.
// Migrated from swarm-tui/headless/core/model_capabilities.go (Z1 decoupling).
type ModelCapabilityInfo struct {
	ContextWindow            int
	SupportsReasoning        bool
	ReasoningLevels          []string
	SupportsDisableReasoning bool // e.g. Cerebras GLM can turn off reasoning for speed
	SupportsVision           bool
	SupportsTools            bool
}

// modelCapabilitiesRegistry is the canonical static capability registry.
// Key format: "provider:modelID" (all lowercase).
var modelCapabilitiesRegistry = map[string]*ModelCapabilityInfo{
	// OpenAI models
	"openai:gpt-5.2-codex": {
		ContextWindow: 200000, SupportsReasoning: true,
		ReasoningLevels: []string{"low", "med", "high", "xhigh"},
		SupportsVision:  true, SupportsTools: true,
	},
	"openai:gpt-4o": {
		ContextWindow: 128000, SupportsVision: true, SupportsTools: true,
	},
	"openai:gpt-4o-mini": {
		ContextWindow: 128000, SupportsVision: true, SupportsTools: true,
	},
	// Anthropic models
	"anthropic:claude-opus-4-7": {
		ContextWindow: 300000, SupportsReasoning: true,
		ReasoningLevels: []string{"low", "medium", "high", "max", "xhigh"},
		SupportsVision:  true, SupportsTools: true,
	},
	"anthropic:claude-opus-4-6": {
		ContextWindow: 300000, SupportsReasoning: true,
		ReasoningLevels: []string{"low", "medium", "high", "max"},
		SupportsVision:  true, SupportsTools: true,
	},
	"anthropic:claude-opus-4-5-20251101": {
		ContextWindow: 200000, SupportsVision: true, SupportsTools: true,
	},
	"anthropic:claude-sonnet-4-6": {
		ContextWindow: 300000, SupportsVision: true, SupportsTools: true,
	},
	"anthropic:claude-haiku-4-5-20251001": {
		ContextWindow: 200000, SupportsVision: true, SupportsTools: true,
	},
	// Cerebras models
	"cerebras:zai-glm-4.7": {
		ContextWindow: 128000, SupportsReasoning: true,
		SupportsDisableReasoning: true, SupportsTools: true,
	},
	// Google models
	"google:gemini-2.0-flash": {
		ContextWindow: 300000, SupportsVision: true, SupportsTools: true,
	},
	"google:gemini-2.5-pro": {
		ContextWindow: 300000, SupportsReasoning: true,
		ReasoningLevels: []string{"low", "med", "high"},
		SupportsVision:  true, SupportsTools: true,
	},
	// xAI / Grok models
	"xai:grok-4.3": {
		ContextWindow: 131072, SupportsReasoning: true,
		ReasoningLevels: []string{"low", "med", "high"},
		SupportsVision:  true, SupportsTools: true,
	},
	"xai:grok-4.3-mini": {
		ContextWindow: 131072, SupportsReasoning: true,
		ReasoningLevels: []string{"low", "high"},
		SupportsVision:  false, SupportsTools: true,
	},
	"xai:grok-4-1": {
		ContextWindow: 131072, SupportsReasoning: true,
		ReasoningLevels: []string{"low", "med", "high"},
		SupportsVision:  true, SupportsTools: true,
	},
	"xai:grok-4-1-mini": {
		ContextWindow: 131072, SupportsTools: true,
	},
}

// ModelCapabilities returns the capability profile for a provider+model pair.
// Key lookup uses lowercase "provider:modelID". Returns (zero-value, false) when
// the model is not in the static registry. Callers should fall back to
// LookupModelContextWindow for context-window-only queries against providers.json.
func ModelCapabilities(providerName, modelID string) (ModelCapabilityInfo, bool) {
	key := strings.ToLower(providerName) + ":" + strings.ToLower(modelID)
	if c, ok := modelCapabilitiesRegistry[key]; ok {
		return *c, true
	}
	return ModelCapabilityInfo{}, false
}

// AllModelCapabilities returns a shallow copy of the full capability registry.
// Intended for IPC/ACP servers that need to broadcast all known capabilities.
func AllModelCapabilities() map[string]*ModelCapabilityInfo {
	out := make(map[string]*ModelCapabilityInfo, len(modelCapabilitiesRegistry))
	maps.Copy(out, modelCapabilitiesRegistry)
	return out
}

// ValidateReasoningLevel returns true if the given level is valid for the model.
func ValidateReasoningLevel(providerName, modelID, level string) bool {
	info, ok := ModelCapabilities(providerName, modelID)
	if !ok || !info.SupportsReasoning {
		return false
	}
	return slices.Contains(info.ReasoningLevels, level)
}

// ExtractProviderFromModel extracts the provider name from a model string.
// Migrated from swarm-tui/internal/chat/provider_utils.go (Z2 partial decoupling).
//
// Handles formats:
//   - "provider/model-name"    → returns "provider"
//   - "models/model-name"      → strips prefix, applies keyword detection
//   - "model.gguf"             → returns "local"
//   - bare model name          → keyword-based detection
func ExtractProviderFromModel(model string) string {
	if model == "" {
		return ""
	}
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "models/")
	lowerModel := strings.ToLower(model)

	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		if len(parts) == 2 && parts[0] != "" {
			return strings.ToLower(strings.TrimSpace(parts[0]))
		}
	}

	if strings.HasSuffix(lowerModel, ".gguf") {
		return "local"
	}
	if strings.Contains(lowerModel, "claude") || strings.Contains(lowerModel, "sonnet") ||
		strings.Contains(lowerModel, "opus") || strings.Contains(lowerModel, "haiku") {
		return "anthropic"
	}
	if strings.Contains(lowerModel, "gpt") || strings.Contains(lowerModel, "o1") ||
		strings.Contains(lowerModel, "o3") || strings.HasPrefix(lowerModel, "openai") {
		return "openai"
	}
	if strings.Contains(lowerModel, "gemini") {
		return "google"
	}
	if strings.Contains(lowerModel, "cerebras") {
		return "cerebras"
	}
	if strings.Contains(lowerModel, "grok") || strings.HasPrefix(lowerModel, "xai") {
		return "xai"
	}
	if strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "zai-glm") ||
		strings.Contains(lowerModel, "zhipu") {
		return "zhipu"
	}
	if strings.Contains(lowerModel, "qwen") {
		return "qwen"
	}
	if strings.Contains(lowerModel, "deepseek") {
		return "deepseek"
	}
	if strings.Contains(lowerModel, "minimax") {
		return "minimax"
	}
	if strings.Contains(lowerModel, "llama") {
		return "meta"
	}
	if strings.Contains(lowerModel, "mistral") || strings.Contains(lowerModel, "mixtral") {
		return "mistral"
	}
	if strings.Contains(lowerModel, "openrouter") {
		return "openrouter"
	}
	return ""
}

// parseTokenCount handles "262144", "200k", "1M", "1.5M" — case-insensitive
// suffix. Non-numeric input returns (0, false).
func parseTokenCount(s string) (int, bool) {
	raw := strings.ReplaceAll(strings.TrimSpace(s), "_", "")
	if raw == "" {
		return 0, false
	}
	mult := 1.0
	last := raw[len(raw)-1]
	switch last {
	case 'k', 'K':
		mult = 1_000
		raw = raw[:len(raw)-1]
	case 'm', 'M':
		mult = 1_000_000
		raw = raw[:len(raw)-1]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	n := int(f * mult)
	if n <= 0 {
		return 0, false
	}
	return n, true
}

// ── Provider config (LoadProviders / AC-6) ────────────────────────────────

// ProviderModel describes a single model entry within a ProviderConfig.
// JSON tags match the format written by the TUI's providers.json enrichment.
type ProviderModel struct {
	ID                      string   `json:"id"`
	DisplayName             string   `json:"display_name"`
	ContextWindow           int      `json:"context_window"`
	Context                 string   `json:"context"` // legacy "128k" string
	SupportsReasoningEffort *bool    `json:"supports_reasoning_effort,omitempty"`
	ReasoningEfforts        []string `json:"reasoning_efforts,omitempty"`
	ThinkingEnabled         bool     `json:"thinking_enabled,omitempty"`
	ThinkingBudget          int      `json:"thinking_budget,omitempty"`
	ThinkingEffort          string   `json:"thinking_effort,omitempty"`
	MaxOutputTokens         int      `json:"max_output_tokens,omitempty"`
	CostInput               float64  `json:"cost_input,omitempty"`
	CostOutput              float64  `json:"cost_output,omitempty"`
	Reasoning               bool     `json:"reasoning,omitempty"`
	ToolCall                bool     `json:"tool_call,omitempty"`
}

// ProviderConfig represents a provider entry from providers.json.
// It mirrors the shape written by the TUI's provider enrichment pipeline.
type ProviderConfig struct {
	Name             string            `json:"name"`
	DisplayName      string            `json:"display_name"`
	Color            string            `json:"color"`
	Type             string            `json:"type"`     // "api_key" or "oauth"
	APIType          string            `json:"api_type"` // "anthropic", "openai", "openai-compatible"
	BaseURL          string            `json:"base_url,omitempty"`
	HTTPMaxRetries   *int              `json:"http_max_retries,omitempty"`
	Available        bool              `json:"available"`
	Models           []ProviderModel   `json:"models"`
	CodexQueryParams map[string]string `json:"codex_query_params,omitempty"`
	CodexHTTPHeaders map[string]string `json:"codex_http_headers,omitempty"`
}

// LoadProviders reads provider configuration from disk.
//
// It tries ~/.swarm/config/providers.json first (map format), then falls
// back to ~/.swarm/config/providers.json (legacy array format).  Both paths are
// written by the TUI's provider enrichment pipeline.
//
// Returns (nil, err) if neither file is readable.
func LoadProviders() ([]ProviderConfig, error) {
	// Canonical providers file (~/.swarm/config/providers.json). Tolerates both
	// the SDK map-keyed format and the legacy TUI array format handled below.
	data, err := os.ReadFile(paths.ProvidersFile())
	if err != nil {
		return nil, err
	}

	// Try map format first (SDK config: {"anthropic": {...}, "openai": {...}}).
	var providerMap map[string]ProviderConfig
	if jsonErr := json.Unmarshal(data, &providerMap); jsonErr == nil {
		providers := make([]ProviderConfig, 0, len(providerMap))
		for name, pc := range providerMap {
			if pc.Name == "" {
				pc.Name = name
			}
			providers = append(providers, pc)
		}
		return providers, nil
	}

	// Fall back to array format (legacy TUI: [{...}, {...}]).
	var providers []ProviderConfig
	if jsonErr := json.Unmarshal(data, &providers); jsonErr != nil {
		return nil, jsonErr
	}
	return providers, nil
}
