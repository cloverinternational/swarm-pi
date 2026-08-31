package settings

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

type modelGenerationCapabilities struct {
	temperature    bool
	temperatureMax float64
	maxTokens      bool
	topP           bool
	topK           bool
}

// UnsupportedGenerationSettings identifies request fields that must be omitted
// for the active provider/model mode, even when a global request default set
// them before per-model overrides are applied.
type UnsupportedGenerationSettings struct {
	Temperature bool
	MaxTokens   bool
	TopP        bool
	TopK        bool
}

func (m *ModelSettings) generationCapabilities() modelGenerationCapabilities {
	if m == nil || m.selectedProvider < 0 || m.selectedProvider >= len(m.providers) {
		return generationCapabilitiesFor(commands.Provider{}, commands.ModelConfig{})
	}
	return generationCapabilitiesFor(m.providers[m.selectedProvider], commands.ModelConfig{
		ID:              m.modelFormID,
		ThinkingEnabled: m.modelFormThinkingEnabled,
	})
}

func generationCapabilitiesFor(provider commands.Provider, modelConfig commands.ModelConfig) modelGenerationCapabilities {
	caps := modelGenerationCapabilities{
		temperature:    true,
		temperatureMax: 2,
		maxTokens:      true,
		topP:           true,
		topK:           true,
	}
	if strings.TrimSpace(provider.Name) == "" && strings.TrimSpace(provider.APIType) == "" {
		return caps
	}
	name := strings.ToLower(strings.TrimSpace(provider.Name))
	apiType := strings.ToLower(strings.TrimSpace(provider.APIType))
	model := strings.ToLower(strings.TrimSpace(modelConfig.ID))

	switch {
	case strings.Contains(name, "cursor"), strings.Contains(name, "codex"):
		return modelGenerationCapabilities{}
	case apiType == "anthropic" || strings.Contains(name, "anthropic") || strings.Contains(name, "claude"):
		caps.temperatureMax = 1
		caps.topK = true
	case apiType == "gemini" || strings.Contains(name, "gemini") || strings.Contains(name, "google"):
		caps.topK = true
	case apiType == "ollama" || strings.Contains(name, "ollama"):
		caps.topK = true
	case strings.Contains(name, "openrouter"):
		// The OpenRouter provider currently uses the OpenAI translator, which
		// does not have a top_k wire field.
		caps.topK = false
	case apiType == "minimax" || strings.Contains(name, "minimax"):
		// MiniMax supports temperature and top_p but not top_k.
		caps.topK = false
	default:
		// OpenAI-compatible providers generally support temperature/top_p and
		// max output tokens. Their adapters omit top_k.
		caps.topK = false
	}

	// OpenAI reasoning models commonly reject sampling controls while reasoning
	// is enabled. Keep the control available when reasoning is explicitly off.
	if (apiType == "openai" || strings.Contains(name, "openai")) &&
		(strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "gpt-5")) &&
		modelConfig.ThinkingEnabled {
		caps.temperature = false
		caps.topP = false
	}
	return caps
}

// FilterUnsupportedGenerationSettings returns a request-safe copy while
// preserving the original persisted overrides for modes where they are valid.
func FilterUnsupportedGenerationSettings(provider commands.Provider, model commands.ModelConfig) commands.ModelConfig {
	unsupported := UnsupportedGenerationSettingsFor(provider, model)
	if unsupported.Temperature {
		model.Temperature = nil
	}
	if unsupported.MaxTokens {
		model.MaxTokens = 0
	}
	if unsupported.TopP {
		model.TopP = nil
	}
	if unsupported.TopK {
		model.TopK = nil
	}
	return model
}

// UnsupportedGenerationSettingsFor resolves which canonical request fields
// must be omitted for this provider/model mode.
func UnsupportedGenerationSettingsFor(provider commands.Provider, model commands.ModelConfig) UnsupportedGenerationSettings {
	caps := generationCapabilitiesFor(provider, model)
	return UnsupportedGenerationSettings{
		Temperature: !caps.temperature,
		MaxTokens:   !caps.maxTokens,
		TopP:        !caps.topP,
		TopK:        !caps.topK,
	}
}

func (c modelGenerationCapabilities) supports(field int) bool {
	switch field {
	case modelFieldTemperature:
		return c.temperature
	case modelFieldMaxTokens:
		return c.maxTokens
	case modelFieldTopP:
		return c.topP
	case modelFieldTopK:
		return c.topK
	default:
		return true
	}
}
