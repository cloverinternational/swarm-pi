package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/judge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/minimax"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

// providerEnvVar maps a provider name to the env var holding its API key.
var providerEnvVar = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
	"gemini":    "GEMINI_API_KEY",
	"minimax":   "MINIMAX_API_KEY",
}

// registerProvider registers the named provider on the registry. It accepts the
// providers whose Register signatures are compatible with *SimpleRegistry, plus
// anthropic/gemini which take the provider.Registry interface.
func registerProvider(reg *provider.SimpleRegistry, name string) error {
	switch name {
	case "anthropic":
		return anthropic.Register(reg)
	case "gemini":
		return gemini.Register(reg)
	case "openai":
		return openai.Register(reg)
	case "minimax":
		return minimax.Register(reg)
	default:
		return fmt.Errorf("unknown provider %q (supported: anthropic, openai, gemini, minimax)", name)
	}
}

// buildSDKJudge constructs an LLM-backed judge.Judge for the given provider and
// model, resolving the API key from --llm-api-key or the provider's standard
// env var. Returns a clear error so the caller can fall back to NoopJudge.
func buildSDKJudge(providerName, model, apiKey string) (judge.Judge, error) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	if providerName == "" {
		return nil, fmt.Errorf("--llm-provider is required when --llm is set")
	}
	if apiKey == "" {
		if env, ok := providerEnvVar[providerName]; ok {
			apiKey = os.Getenv(env)
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key for %s (set --llm-api-key or %s)", providerName, providerEnvVar[providerName])
	}

	logger := observability.NewNopLogger()
	reg := provider.NewSimpleRegistry(logger)
	if err := registerProvider(reg, providerName); err != nil {
		return nil, err
	}
	factory, err := agent.NewSimpleFactory(agent.FactoryConfig{
		ProviderRegistry: reg,
		Logger:           logger,
	})
	if err != nil {
		return nil, fmt.Errorf("create factory: %w", err)
	}
	return judge.NewSDKJudge(judge.SDKJudgeConfig{
		Factory: factory,
		Provider: provider.Config{
			Name:   providerName,
			Model:  model,
			APIKey: apiKey,
		},
		Logger: logger,
	})
}
