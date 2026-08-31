// Package quick provides a zero-configuration entry point for the Swarm SDK.
//
// It auto-detects the LLM provider from the model name and reads the API key
// from the environment, so most users need only one line to get a working agent:
//
//	ag, err := quick.NewAgent("claude-sonnet-4-5")
//	result, _ := ag.Run(ctx, "Explain context windows in one paragraph.")
//	fmt.Println(result.Message)
//
// # Provider Auto-Detection
//
// The model name prefix determines which provider is used:
//
//	claude-*                  → Anthropic   (ANTHROPIC_API_KEY)
//	gpt-*, o1-*, o3-*, o4-*  → OpenAI      (OPENAI_API_KEY)
//	gemini-*                  → Gemini      (GEMINI_API_KEY)
//
// An error is returned when the model prefix is unrecognised or the required
// environment variable is not set. For unsupported providers, use
// [agent.Build] directly and supply the provider explicitly.
package quick

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Option configures a quick agent. Pass options to [NewAgent].
type Option func(*agent.Builder)

// WithSystemPrompt sets the agent's instruction preamble.
func WithSystemPrompt(prompt string) Option {
	return func(b *agent.Builder) { b.SystemPrompt(prompt) }
}

// WithTools registers tools the agent may call.
//
//	ag, err := quick.NewAgent("claude-sonnet-4-5",
//	    quick.WithTools(
//	        tools.Func[SearchParams]("search", "Search docs", searchFn),
//	    ),
//	)
func WithTools(tt ...tools.Tool) Option {
	return func(b *agent.Builder) { b.Tools(tt...) }
}

// WithMaxTurns limits how many provider round-trips the agent may make.
func WithMaxTurns(n int) Option {
	return func(b *agent.Builder) { b.MaxTurns(n) }
}

// WithTimeout sets the maximum wall-clock time for a single execution.
func WithTimeout(d time.Duration) Option {
	return func(b *agent.Builder) { b.Timeout(d) }
}

// WithID sets a custom agent identifier used in logs and traces.
func WithID(id string) Option {
	return func(b *agent.Builder) { b.ID(id) }
}

// WithThinking enables extended thinking mode (Anthropic claude-3-7+ only).
// budgetTokens is the token budget for internal reasoning; 0 uses the default (8192).
func WithThinking(budgetTokens int) Option {
	return func(b *agent.Builder) { b.WithThinking(budgetTokens) }
}

// NewAgent creates a zero-config agent from a model name.
//
// The provider is auto-detected from the model name prefix and the API key is
// read from the corresponding environment variable:
//
//	quick.NewAgent("claude-sonnet-4-5")          // ANTHROPIC_API_KEY
//	quick.NewAgent("gpt-4o")                     // OPENAI_API_KEY
//	quick.NewAgent("gemini-2.0-flash")           // GEMINI_API_KEY
//
// Pass [Option] values to configure system prompt, tools, turn limits, etc.:
//
//	ag, err := quick.NewAgent("claude-sonnet-4-5",
//	    quick.WithSystemPrompt("You are a Go expert."),
//	    quick.WithTools(myReadTool, myBashTool),
//	    quick.WithMaxTurns(20),
//	)
func NewAgent(model string, opts ...Option) (*agent.Agent, error) {
	prov, err := providerForModel(model)
	if err != nil {
		return nil, fmt.Errorf("quick.NewAgent: %w", err)
	}

	b := agent.Build().Provider(prov).Model(model)
	for _, o := range opts {
		o(b)
	}

	ag, err := b.Create()
	if err != nil {
		return nil, fmt.Errorf("quick.NewAgent: %w", err)
	}
	return ag, nil
}

// providerForModel auto-detects and initialises the provider for the given model name.
func providerForModel(model string) (provider.Provider, error) {
	m := strings.ToLower(model)

	switch {
	case strings.HasPrefix(m, "claude"):
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("model %q requires ANTHROPIC_API_KEY to be set", model)
		}
		return anthropic.NewFromEnv()

	case strings.HasPrefix(m, "gpt-"),
		strings.HasPrefix(m, "o1-"),
		strings.HasPrefix(m, "o3-"),
		strings.HasPrefix(m, "o4-"),
		strings.HasPrefix(m, "chatgpt-"):
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("model %q requires OPENAI_API_KEY to be set", model)
		}
		return openai.NewFromEnv()

	case strings.HasPrefix(m, "gemini"):
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("model %q requires GEMINI_API_KEY to be set", model)
		}
		return gemini.New(gemini.Config{
			APIKey:   apiKey,
			AuthMode: gemini.AuthModeAPIKey,
		})

	default:
		return nil, fmt.Errorf(
			"unrecognised model prefix %q — supported prefixes: claude-*, gpt-*, o1-*, o3-*, o4-*, gemini-*; "+
				"for other providers use agent.Build().Provider(p).Model(model).Create()",
			model,
		)
	}
}
