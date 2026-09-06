package agent

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// NewQuick creates a ready-to-use agent from any provider with sensible defaults.
// It is the zero-boilerplate entry point for simple use cases.
//
// model is the model identifier to use (e.g. "claude-sonnet-4-5",
// "gpt-4o", "gemini-2.0-flash"). Pass an empty string to use the
// provider's first reported SupportedModel.
//
// The agent has an empty tool registry — register tools after creation:
//
//	ag.ToolRegistry().Register(myTool)
//
// For production use with custom logging, tracing, and tool configuration,
// use NewSimpleFactory instead.
//
// Example — one-shot question with Anthropic:
//
//	p, err := anthropic.NewFromEnv()
//	ag, err := agent.NewQuick(p, "claude-sonnet-4-5")
//	result, err := ag.Run(ctx, "What is the capital of France?")
//	fmt.Println(result.Message)
func NewQuick(prov provider.Provider, model string) (*Agent, error) {
	if prov == nil {
		return nil, fmt.Errorf("agent.NewQuick: provider is required")
	}
	if model == "" {
		caps := prov.Capabilities()
		if len(caps.SupportedModels) > 0 {
			model = caps.SupportedModels[0]
		}
	}
	if model == "" {
		return nil, fmt.Errorf("agent.NewQuick: model is required — pass a model name or configure a default on the provider")
	}

	def := &Definition{
		ID:       prov.Name() + "-quick",
		Name:     prov.Name() + " agent",
		Provider: prov.Name(),
		Model:    model,
	}

	return New(Config{
		Definition:   def,
		Provider:     prov,
		ToolRegistry: tools.NewRegistry(),
	})
}
