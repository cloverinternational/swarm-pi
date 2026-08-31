package tools

import "slices"

import "context"

// ToolHook defines the interface for pre/post tool execution hooks.
// Hooks enable observability, validation, caching, and custom processing.
type ToolHook interface {
	// Name returns the hook identifier
	Name() string

	// Priority determines execution order (higher = runs earlier)
	// Range: 0-100, with 50 being default
	Priority() int

	// OnPreExecute runs before tool execution.
	// Can modify parameters or prevent execution.
	OnPreExecute(ctx context.Context, tool Tool, params map[string]any) (*PreExecuteResult, error)

	// OnPostExecute runs after tool execution.
	// Can modify results or add metadata.
	OnPostExecute(ctx context.Context, tool Tool, params map[string]any, result *ToolResult) (*PostExecuteResult, error)
}

// PreExecuteResult controls whether execution proceeds
type PreExecuteResult struct {
	// Allow determines if execution should proceed
	Allow bool

	// Reason explains why execution was blocked (if Allow is false)
	Reason string

	// ModifiedParams contains updated parameters (if modified)
	ModifiedParams map[string]any

	// InjectMetadata adds metadata to execution context
	InjectMetadata map[string]any

	// SkipExecution indicates the hook provides the result directly
	SkipExecution bool

	// DirectResult is the result to use if SkipExecution is true
	DirectResult *ToolResult
}

// PostExecuteResult can modify tool results
type PostExecuteResult struct {
	// ModifiedResult replaces the original result (if non-nil)
	ModifiedResult *ToolResult

	// AppendContent adds additional content blocks to result
	AppendContent []ContentBlock

	// InjectMetadata adds metadata to result
	InjectMetadata map[string]any

	// SuppressResult prevents the result from being returned
	SuppressResult bool

	// SuppressReason explains why result was suppressed
	SuppressReason string
}

// AllowExecution creates a PreExecuteResult that allows execution
func AllowExecution() *PreExecuteResult {
	return &PreExecuteResult{
		Allow: true,
	}
}

// BlockExecution creates a PreExecuteResult that blocks execution
func BlockExecution(reason string) *PreExecuteResult {
	return &PreExecuteResult{
		Allow:  false,
		Reason: reason,
	}
}

// ModifyParams creates a PreExecuteResult with modified parameters
func ModifyParams(params map[string]any) *PreExecuteResult {
	return &PreExecuteResult{
		Allow:          true,
		ModifiedParams: params,
	}
}

// SkipWithResult creates a PreExecuteResult that skips execution and returns a cached result
func SkipWithResult(result *ToolResult) *PreExecuteResult {
	return &PreExecuteResult{
		Allow:         true,
		SkipExecution: true,
		DirectResult:  result,
	}
}

// ContinueUnmodified creates a PostExecuteResult that doesn't modify the result
func ContinueUnmodified() *PostExecuteResult {
	return &PostExecuteResult{}
}

// ReplaceResult creates a PostExecuteResult that replaces the original result
func ReplaceResult(result *ToolResult) *PostExecuteResult {
	return &PostExecuteResult{
		ModifiedResult: result,
	}
}

// SuppressResult creates a PostExecuteResult that suppresses the result
func SuppressResult(reason string) *PostExecuteResult {
	return &PostExecuteResult{
		SuppressResult: true,
		SuppressReason: reason,
	}
}

// HookRegistry manages tool hooks
type HookRegistry interface {
	// Register adds a hook
	Register(hook ToolHook) error

	// Unregister removes a hook by name
	Unregister(name string) error

	// List returns all registered hooks sorted by priority (descending)
	List() []ToolHook

	// Get retrieves a hook by name
	Get(name string) (ToolHook, error)

	// ExecutePreHooks runs all pre-execution hooks in priority order
	ExecutePreHooks(ctx context.Context, tool Tool, params map[string]any) (*PreExecuteResult, error)

	// ExecutePostHooks runs all post-execution hooks in priority order
	ExecutePostHooks(ctx context.Context, tool Tool, params map[string]any, result *ToolResult) (*PostExecuteResult, error)

	// Clear removes all hooks
	Clear()

	// Count returns the number of registered hooks
	Count() int
}

// HookFilter allows filtering which tools a hook applies to
type HookFilter interface {
	// ShouldApply determines if the hook should run for this tool
	ShouldApply(tool Tool) bool
}

// FilteredHook wraps a hook with a filter
type FilteredHook struct {
	Hook   ToolHook
	Filter HookFilter
}

// ToolNameFilter filters by exact tool name
type ToolNameFilter struct {
	Names []string
}

func (f *ToolNameFilter) ShouldApply(tool Tool) bool {
	toolName := tool.Name()
	return slices.Contains(f.Names, toolName)
}

// ToolCategoryFilter filters by tool category
type ToolCategoryFilter struct {
	Categories []string
}

func (f *ToolCategoryFilter) ShouldApply(tool Tool) bool {
	// Would need tool metadata to check category
	// This is a placeholder for when metadata is available
	return true
}

// ToolSourceFilter filters by tool source (builtin, mcp, config)
type ToolSourceFilter struct {
	Sources []ToolSource
}

func (f *ToolSourceFilter) ShouldApply(tool Tool) bool {
	// Would need tool metadata to check source
	// This is a placeholder for when metadata is available
	return true
}
