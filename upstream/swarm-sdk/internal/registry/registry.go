package registry

import (
	"context"
	"strings"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// Registry provides access to the model database.
type Registry struct {
	providers []catwalk.Provider
}

// New creates a new registry instance.
func New(opts ...Option) *Registry {
	r := &Registry{
		providers: embedded.GetAll(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Option defines a functional option for configuring the registry.
type Option func(*Registry)

// WithProviders allow overriding the default embedded providers.
func WithProviders(providers []catwalk.Provider) Option {
	return func(r *Registry) {
		r.providers = providers
	}
}

// Default returns a global default registry instance for quick access.
var Default = New()

// supplementalModels fills gaps in the vendored catwalk snapshot: models a
// provider ships that the embedded catalog predates. Keyed by catwalk provider
// ID. Entries are appended in GetProviders only when the embedded data does
// not already carry the model ID, so a catwalk bump that adds them supersedes
// these automatically. Context windows are the published values — the
// provider.DefaultContextWindowCap clamp in mapModel applies to these too.
var supplementalModels = map[string][]catwalk.Model{
	// GLM-5.2 (released 2026-02): Zhipu's 1M-context flagship, missing from
	// the embedded snapshot (which tops out at glm-5 / 204.8K). Specs per
	// Z.ai direct API: 1,048,576-token context, 32K max output,
	// $1.40/M input, $4.40/M output.
	"zhipu":        glm52Models(),
	"zhipu-coding": glm52Models(),
	"zai":          glm52Models(),
	// GPT-5.6 / 5.5 / 5.2 (2026): the live ChatGPT Codex /models catalog
	// lists these, but the embedded snapshot still tops out at the retired
	// gpt-5.1-codex family. Context windows and reasoning levels per the
	// catalog; the catalog carries no max-output figure, so DefaultMaxTokens
	// follows the existing 32K supplemental convention.
	"openai": openaiCodexModels(),
}

// glm52Models returns the GLM-5.2 supplemental entry (fresh slice per caller
// so provider mapping cannot alias shared state).
func glm52Models() []catwalk.Model {
	return []catwalk.Model{{
		ID:               "glm-5.2",
		Name:             "GLM-5.2",
		CostPer1MIn:      1.40,
		CostPer1MOut:     4.40,
		ContextWindow:    1_048_576,
		DefaultMaxTokens: 32_768,
		CanReason:        true,
	}}
}

// openaiCodexModels returns the supplemental OpenAI entries for the visible
// ChatGPT Codex catalog models (fresh slice per caller so provider mapping
// cannot alias shared state). Default reasoning efforts mirror the catalog:
// gpt-5.6-sol defaults to low, the rest to medium.
func openaiCodexModels() []catwalk.Model {
	gpt56Levels := func() []string {
		return []string{
			provider.ReasoningEffortLow,
			provider.ReasoningEffortMedium,
			provider.ReasoningEffortHigh,
			provider.ReasoningEffortXHigh,
			provider.ReasoningEffortMax,
		}
	}
	gpt5xLevels := func() []string {
		return []string{
			provider.ReasoningEffortLow,
			provider.ReasoningEffortMedium,
			provider.ReasoningEffortHigh,
			provider.ReasoningEffortXHigh,
		}
	}
	return []catwalk.Model{
		{
			ID:                     "gpt-5.6-sol",
			Name:                   "GPT-5.6 Sol",
			ContextWindow:          372_000,
			DefaultMaxTokens:       32_768,
			CanReason:              true,
			ReasoningLevels:        gpt56Levels(),
			DefaultReasoningEffort: provider.ReasoningEffortLow,
		},
		{
			ID:                     "gpt-5.6-terra",
			Name:                   "GPT-5.6 Terra",
			ContextWindow:          372_000,
			DefaultMaxTokens:       32_768,
			CanReason:              true,
			ReasoningLevels:        gpt56Levels(),
			DefaultReasoningEffort: provider.ReasoningEffortMedium,
		},
		{
			ID:                     "gpt-5.6-luna",
			Name:                   "GPT-5.6 Luna",
			ContextWindow:          372_000,
			DefaultMaxTokens:       32_768,
			CanReason:              true,
			ReasoningLevels:        gpt56Levels(),
			DefaultReasoningEffort: provider.ReasoningEffortMedium,
		},
		{
			ID:                     "gpt-5.5",
			Name:                   "GPT-5.5",
			ContextWindow:          272_000,
			DefaultMaxTokens:       32_768,
			CanReason:              true,
			ReasoningLevels:        gpt5xLevels(),
			DefaultReasoningEffort: provider.ReasoningEffortMedium,
		},
		{
			ID:                     "gpt-5.2",
			Name:                   "GPT-5.2",
			ContextWindow:          272_000,
			DefaultMaxTokens:       32_768,
			CanReason:              true,
			ReasoningLevels:        gpt5xLevels(),
			DefaultReasoningEffort: provider.ReasoningEffortMedium,
		},
	}
}

// mapModel converts a catwalk model into the SDK's canonical format, applying
// the default context-window policy cap.
func mapModel(cm catwalk.Model) Model {
	return Model{
		ID:                 cm.ID,
		Name:               cm.Name,
		CostPer1MIn:        cm.CostPer1MIn,
		CostPer1MOut:       cm.CostPer1MOut,
		CostPer1MInCached:  cm.CostPer1MInCached,
		CostPer1MOutCached: cm.CostPer1MOutCached,
		// Catwalk publishes the model's full capability (1M+ for current
		// frontier models); the SDK's default policy caps what agents
		// actually use. See provider.DefaultContextWindowCap.
		ContextWindow:          int64(provider.ClampContextWindow(int(cm.ContextWindow))),
		DefaultMaxTokens:       cm.DefaultMaxTokens,
		CanReason:              cm.CanReason,
		ReasoningLevels:        cm.ReasoningLevels,
		DefaultReasoningEffort: cm.DefaultReasoningEffort,
		SupportsImages:         cm.SupportsImages,
	}
}

// GetProviders returns all known providers mapped to the SDK's canonical format.
func (r *Registry) GetProviders() []Provider {
	var providers []Provider
	for _, cp := range r.providers {
		p := Provider{
			ID:                  string(cp.ID),
			Name:                cp.Name,
			Type:                string(cp.Type),
			DefaultLargeModelID: cp.DefaultLargeModelID,
			DefaultSmallModelID: cp.DefaultSmallModelID,
		}

		seen := make(map[string]bool, len(cp.Models))
		for _, cm := range cp.Models {
			seen[strings.ToLower(cm.ID)] = true
			p.Models = append(p.Models, mapModel(cm))
		}
		// Gap-fill models the embedded snapshot predates (see supplementalModels).
		for _, sm := range supplementalModels[string(cp.ID)] {
			if !seen[strings.ToLower(sm.ID)] {
				p.Models = append(p.Models, mapModel(sm))
			}
		}
		providers = append(providers, p)
	}
	return providers
}

// GetProvider finds a specific provider by its ID.
func (r *Registry) GetProvider(id string) *Provider {
	providers := r.GetProviders()
	for _, p := range providers {
		if strings.EqualFold(p.ID, id) {
			return &p
		}
	}
	return nil
}

// GetModel finds a specific model globally by its ID.
func (r *Registry) GetModel(id string) *Model {
	providers := r.GetProviders()
	for _, p := range providers {
		for _, m := range p.Models {
			if strings.EqualFold(m.ID, id) {
				return &m
			}
		}
	}
	return nil
}

// Global convenience functions using the Default registry.

func GetProviders() []Provider        { return Default.GetProviders() }
func GetProvider(id string) *Provider { return Default.GetProvider(id) }
func GetModel(id string) *Model       { return Default.GetModel(id) }

// ListSOTAModels returns the top models for a provider, sorted by release relevance.
func ListSOTAModels(providerID string) []Model {
	p := GetProvider(providerID)
	if p == nil {
		return nil
	}
	// Note: In a future iteration, we can add semantic version sorting here.
	return p.Models
}

// Refresh allows updating the registry from a live catwalk service.
func (r *Registry) Refresh(ctx context.Context) error {
	client := catwalk.New()
	live, err := client.GetProviders(ctx, "")
	if err != nil {
		return err
	}
	if len(live) > 0 {
		r.providers = live
	}
	return nil
}
