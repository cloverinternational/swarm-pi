package chat

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	chatsettings "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// modelGenerationSettings is the single runtime seam for per-model generation
// overrides. Nil fields mean "use the provider/model default".
type modelGenerationSettings struct {
	mu sync.RWMutex

	model       string
	temperature *float64
	maxTokens   int
	topP        *float64
	topK        *int
	unsupported chatsettings.UnsupportedGenerationSettings
}

func newModelGenerationSettings(model string) *modelGenerationSettings {
	return &modelGenerationSettings{model: strings.TrimSpace(model)}
}

func (s *modelGenerationSettings) reset(model string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.model = strings.TrimSpace(model)
	s.temperature = nil
	s.maxTokens = 0
	s.topP = nil
	s.topK = nil
	s.unsupported = chatsettings.UnsupportedGenerationSettings{}
	s.mu.Unlock()
}

func (s *modelGenerationSettings) applyModel(providerConfig commands.Provider, model commands.ModelConfig) {
	if s == nil {
		return
	}
	unsupported := chatsettings.UnsupportedGenerationSettingsFor(providerConfig, model)
	model = chatsettings.FilterUnsupportedGenerationSettings(providerConfig, model)
	s.mu.Lock()
	s.model = strings.TrimSpace(model.ID)
	s.temperature = cloneGenerationFloat(model.Temperature)
	s.maxTokens = model.MaxTokens
	s.topP = cloneGenerationFloat(model.TopP)
	s.topK = cloneGenerationInt(model.TopK)
	s.unsupported = unsupported
	s.mu.Unlock()
}

func (s *modelGenerationSettings) apply(req *provider.ChatRequest) {
	if s == nil || req == nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.model != "" && !strings.EqualFold(strings.TrimSpace(req.Model), s.model) {
		return
	}
	if s.unsupported.Temperature {
		req.Temperature = nil
	} else if s.temperature != nil {
		req.Temperature = cloneGenerationFloat(s.temperature)
	}
	if s.unsupported.MaxTokens {
		req.MaxTokens = nil
	} else if s.maxTokens > 0 {
		value := s.maxTokens
		req.MaxTokens = &value
	}
	if s.unsupported.TopP {
		req.TopP = nil
	} else if s.topP != nil {
		req.TopP = cloneGenerationFloat(s.topP)
	}
	if s.unsupported.TopK {
		req.TopK = nil
	} else if s.topK != nil {
		req.TopK = cloneGenerationInt(s.topK)
	}
}

func cloneGenerationFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneGenerationInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type generationSettingsProvider struct {
	provider.Provider
	settings *modelGenerationSettings
}

func newGenerationSettingsProvider(base provider.Provider, settings *modelGenerationSettings) provider.Provider {
	if base == nil || settings == nil {
		return base
	}
	return &generationSettingsProvider{Provider: base, settings: settings}
}

func (p *generationSettingsProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.settings.apply(&req)
	return p.Provider.Chat(ctx, req)
}

func (p *generationSettingsProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.settings.apply(&req)
	return p.Provider.Stream(ctx, req)
}

func (p *generationSettingsProvider) LastProviderJSON() json.RawMessage {
	if debugProvider, ok := p.Provider.(provider.DebugProvider); ok {
		return debugProvider.LastProviderJSON()
	}
	return nil
}

// SetRawEventCallback preserves raw provider events through the generation
// settings wrapper for the Debug Inspector.
func (p *generationSettingsProvider) SetRawEventCallback(callback provider.RawEventCallback) {
	if capable, ok := p.Provider.(rawEventCapable); ok {
		capable.SetRawEventCallback(callback)
	}
}
