package hooks

import "context"

// Registry manages hook registration and execution.
// This is Ring 0 - interface only, implementation in Ring 1.
type Registry interface {
	// Register adds a hook to the registry.
	// Hooks are executed in priority order (highest first).
	Register(hook Hook, scope HookScope, scopeID string) error

	// Unregister removes a hook from the registry.
	Unregister(name string, scope HookScope, scopeID string) error

	// Emit sends an event through the hook chain.
	// Hooks process the event in priority order.
	// Processing stops if any hook blocks the event.
	Emit(ctx context.Context, event Event) (*Event, error)

	// List returns all registered hooks.
	List() []HookRegistration

	// ListByScope returns hooks registered in a specific scope.
	ListByScope(scope HookScope, scopeID string) []HookRegistration

	// IsRegistered checks if a hook is registered.
	IsRegistered(name string) bool
}

// HookScope defines where a hook is active.
type HookScope string

const (
	// ScopeGlobal makes the hook active for all conversations.
	ScopeGlobal HookScope = "global"

	// ScopeProject makes the hook active within a project.
	ScopeProject HookScope = "project"

	// ScopeMode makes the hook active only within a specific mode.
	ScopeMode HookScope = "mode"

	// ScopeConversation makes the hook active for a specific conversation.
	ScopeConversation HookScope = "conversation"
)

// HookRegistration contains metadata about a registered hook.
type HookRegistration struct {
	// Hook is the hook instance.
	Hook Hook

	// Scope defines where this hook is active.
	Scope HookScope

	// ScopeID identifies the scope (project ID, mode ID, conversation ID).
	ScopeID string

	// Enabled indicates if the hook is currently active.
	Enabled bool

	// PermissionPolicy indicates if the hook is allowed to execute.
	PermissionPolicy HookPermissionPolicy

	// ExecutionCount tracks how many times this hook has been called.
	ExecutionCount int64

	// BlockedCount tracks how many times this hook blocked events.
	BlockedCount int64

	// ModifiedCount tracks how many times this hook modified events.
	ModifiedCount int64

	// ErrorCount tracks how many times this hook returned errors.
	ErrorCount int64
}

// HookOutput captures the output from a single hook execution.
type HookOutput struct {
	HookName string // Name of the hook
	Output   string // stdout/stderr from the hook
	Success  bool   // Whether the hook executed successfully
	Error    string // Error message if any
}

// ExecutionResult represents the outcome of executing hooks for an event.
type ExecutionResult struct {
	// FinalEvent is the event after all hook processing.
	FinalEvent *Event

	// Blocked indicates if the event was blocked by a hook.
	Blocked bool

	// BlockedBy identifies which hook blocked the event.
	BlockedBy string

	// BlockReason explains why the event was blocked.
	BlockReason string

	// Modified indicates if the event was modified.
	Modified bool

	// HooksExecuted is the number of hooks that processed this event.
	HooksExecuted int

	// ExecutionTimeMS is the total time spent in hooks.
	ExecutionTimeMS int64

	// HookOutputs contains output from each hook that executed.
	HookOutputs []HookOutput
}
