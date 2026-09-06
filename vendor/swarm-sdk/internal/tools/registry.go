package tools

// Registry manages tool registration and discovery.
type Registry interface {
	// Register adds a tool to the registry.
	// Returns error if a tool with the same name already exists.
	Register(tool Tool) error

	// Unregister removes a tool from the registry.
	Unregister(name string) error

	// Get retrieves a tool by name.
	// Returns error if tool is not found.
	Get(name string) (Tool, error)

	// List returns all registered tool names.
	List() []string

	// IsRegistered checks if a tool is registered.
	IsRegistered(name string) bool

	// HideTool marks a tool as hidden. Hidden tools remain enabled and
	// executable via Get()/Execute() but are excluded from List(). This
	// prevents them from appearing in the LLM's tool list while keeping
	// them available for internal subsystems.
	HideTool(name string) error
}

// Execute is no longer part of Registry — use [Executor] instead.
// See [NewExecutor] for creating an Executor that wraps a Registry.

// ToolScope defines where a tool is available.
type ToolScope string

const (
	// ScopeGlobal makes the tool available to all conversations.
	ScopeGlobal ToolScope = "global"

	// ScopeProject makes the tool available within a project.
	ScopeProject ToolScope = "project"

	// ScopeMode makes the tool available only within a specific mode.
	ScopeMode ToolScope = "mode"

	// ScopeAgent makes the tool available only to specific agents.
	ScopeAgent ToolScope = "agent"

	// ScopeConversation makes the tool available to a specific conversation.
	ScopeConversation ToolScope = "conversation"
)

// ToolRegistration contains metadata about a registered tool.
type ToolRegistration struct {
	// Tool is the tool instance.
	Tool Tool

	// Scope defines where this tool is available.
	Scope ToolScope

	// ScopeID identifies the scope (project ID, mode ID, etc.).
	ScopeID string

	// Enabled indicates if the tool is currently active.
	Enabled bool

	// Hidden indicates the tool is executable but excluded from List().
	// Hidden tools do not appear in the provider tool list sent to the LLM,
	// but can still be found via Get() and executed. Use this for tools that
	// should only be available to internal subsystems (e.g. MCP management
	// tools used by a sidebar assistant, not the main chat agent).
	Hidden bool

	// Metadata contains additional information.
	Metadata *ToolMetadata
}
