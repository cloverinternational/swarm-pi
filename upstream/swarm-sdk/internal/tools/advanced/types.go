package advanced

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// Deferrable — opt-in interface for lazy-loaded tools
// ---------------------------------------------------------------------------

// Deferrable is an optional interface that tools.Tool implementations can
// satisfy to signal that their definition should NOT be included in the
// initial LLM context window. Deferred tools are still registered in the
// SDK registry and discoverable via ToolSearchTool; they are simply omitted
// from the provider.Tool slice sent with the first ChatRequest.
//
// When the model needs a deferred tool it uses ToolSearchTool to look it up
// by name, description, or tags. The orchestrator then loads the tool
// definition into context for subsequent turns.
//
// This mirrors the shouldDefer flag observed in Claude Code's binary.
type Deferrable interface {
	// ShouldDefer returns true if this tool should be excluded from the
	// initial provider context to save tokens. The tool remains registered
	// and discoverable via ToolSearchTool.
	ShouldDefer() bool
}

// ---------------------------------------------------------------------------
// StatefulTool — tools that read/write shared app state
// ---------------------------------------------------------------------------

// StatefulTool extends tools.Tool with the ability to access a shared
// AppState during execution. This enables programmatic tool calling
// patterns where intermediate tool results are stored in state rather
// than passed back through the LLM context.
//
// Implementations should use AppState.Get / AppState.Set for all shared
// data rather than package-level variables.
type StatefulTool interface {
	tools.Tool

	// ExecuteWithState is called instead of Execute when an AppStateManager
	// is available. The state parameter provides thread-safe access to shared
	// application state scoped to the executing agent.
	ExecuteWithState(params map[string]any, state *AppState) (*tools.ToolResult, error)
}

// ---------------------------------------------------------------------------
// AdvancedToolSpec — enriched tool metadata for search and deferred loading
// ---------------------------------------------------------------------------

// AdvancedToolSpec contains the full metadata for a tool, used by
// ToolSearchTool results and the deferred loading system. It is a
// read-only snapshot; mutating it has no effect on the underlying tool.
type AdvancedToolSpec struct {
	// Name is the tool's unique identifier.
	Name string `json:"name"`

	// Description is the human-readable explanation of the tool.
	Description string `json:"description"`

	// ShouldDefer is true if the tool opts into lazy loading.
	ShouldDefer bool `json:"should_defer"`

	// ConcurrencySafe is true if the tool can be executed in parallel
	// with other tools without race conditions.
	ConcurrencySafe bool `json:"concurrency_safe"`

	// InputExamples are concrete usage examples extracted from
	// tools.ToolWithExamples, if the tool implements that interface.
	InputExamples []tools.ToolExample `json:"input_examples,omitempty"`

	// Category groups related tools (e.g. "filesystem", "network").
	Category string `json:"category,omitempty"`

	// Tags are free-form keywords for search and filtering.
	Tags []string `json:"tags,omitempty"`

	// EstimatedTokens is a rough estimate of how many tokens the full
	// tool definition (name + description + schema + examples) would
	// consume in a provider context window. Zero means not estimated.
	EstimatedTokens int `json:"estimated_tokens,omitempty"`

	// Source indicates where the tool originates (builtin, mcp, config, custom).
	Source string `json:"source,omitempty"`

	// ServerName is set for MCP-sourced tools.
	ServerName string `json:"server_name,omitempty"`
}

// ---------------------------------------------------------------------------
// InputExample — alias with JSON-friendly description field
// ---------------------------------------------------------------------------

// InputExample is a JSON-serialisable representation of a single tool usage
// example. It wraps tools.ToolExample with explicit JSON tags for provider
// Metadata embedding.
type InputExample struct {
	// Description explains what this example demonstrates.
	Description string `json:"description,omitempty"`

	// Parameters holds the example input matching the tool's schema.
	Parameters map[string]any `json:"input"`

	// ExpectedOutput optionally shows the expected result.
	ExpectedOutput string `json:"expected_output,omitempty"`
}

// FromToolExample converts an SDK tools.ToolExample to an InputExample.
func FromToolExample(te tools.ToolExample) InputExample {
	return InputExample{
		Description:    te.Description,
		Parameters:     te.Parameters,
		ExpectedOutput: te.ExpectedOutput,
	}
}

// FromToolExamples converts a slice of SDK examples.
func FromToolExamples(examples []tools.ToolExample) []InputExample {
	if len(examples) == 0 {
		return nil
	}
	out := make([]InputExample, len(examples))
	for i, ex := range examples {
		out[i] = FromToolExample(ex)
	}
	return out
}

// ---------------------------------------------------------------------------
// Metadata keys — constants for provider.Tool.Metadata population
// ---------------------------------------------------------------------------

const (
	// MetaKeyInputExamples is the key in provider.Tool.Metadata for input examples.
	// Value type: []InputExample
	MetaKeyInputExamples = "input_examples"

	// MetaKeyShouldDefer is the key for the deferred loading flag.
	// Value type: bool
	MetaKeyShouldDefer = "should_defer"

	// MetaKeyConcurrencySafe is the key for concurrency safety.
	// Value type: bool
	MetaKeyConcurrencySafe = "concurrency_safe"

	// MetaKeyCategory is the key for tool category.
	// Value type: string
	MetaKeyCategory = "category"

	// MetaKeyTags is the key for tool tags.
	// Value type: []string
	MetaKeyTags = "tags"

	// MetaKeyEstimatedTokens is the key for estimated token count.
	// Value type: int
	MetaKeyEstimatedTokens = "estimated_tokens"

	// MetaKeySource is the key for tool source.
	// Value type: string (builtin, mcp, config, custom)
	MetaKeySource = "source"

	// MetaKeyServerName is the key for MCP server name.
	// Value type: string
	MetaKeyServerName = "server_name"
)
