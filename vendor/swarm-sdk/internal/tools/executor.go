// Package tools defines the interface for tools that agents can execute.
package tools

import "context"

// ExecutableRegistry is an optional extension of [Registry] that provides
// a built-in Execute method with stats, timeouts, tracing, and permission
// enforcement already wired in.
//
// [SimpleRegistry] satisfies this interface. Any custom Registry that also
// provides these methods will automatically get the fast-path in [NewExecutor]
// rather than falling back to the generic wrapper.
type ExecutableRegistry interface {
	Registry
	// Execute looks up the named tool, enforces permissions, and runs it.
	Execute(ctx context.Context, name string, params map[string]any) (*ToolResult, error)
	// SetPermissionChecker configures the permission checker used during Execute.
	SetPermissionChecker(PermissionChecker)
}

// Executor looks up and runs a tool by name, applying validation and permission
// checks before calling Execute.
//
// Executor is intentionally separate from [Registry] so that the registry can
// focus on registration and lookup while execution policy (validation,
// permissions, timeouts, stats) lives in the Executor.
//
// Create an Executor by wrapping a Registry:
//
//	exec := tools.NewExecutor(reg, checker)
//	result, err := exec.Execute(ctx, "file_read", params)
type Executor interface {
	Execute(ctx context.Context, name string, params map[string]any) (*ToolResult, error)
}

// NewExecutor wraps a Registry with an Executor that applies the provided
// PermissionChecker before each tool call.  Pass a nil checker to skip
// permission enforcement.
//
// If the registry satisfies [ExecutableRegistry] (e.g. [SimpleRegistry]),
// the built-in Execute implementation is used directly — it includes stats,
// timeouts, and tracing. Otherwise a minimal generic wrapper is used.
func NewExecutor(reg Registry, checker PermissionChecker) Executor {
	if er, ok := reg.(ExecutableRegistry); ok {
		// Fast path: registry already has a full Execute implementation.
		if checker != nil {
			er.SetPermissionChecker(checker)
		}
		return er
	}
	// Slow path: generic wrapper for custom Registry implementations.
	return &genericExecutor{reg: reg, checker: checker}
}

// genericExecutor is a fallback Executor for registries that don't implement
// ExecutableRegistry.
type genericExecutor struct {
	reg     Registry
	checker PermissionChecker
}

func (e *genericExecutor) Execute(ctx context.Context, name string, params map[string]any) (*ToolResult, error) {
	tool, err := e.reg.Get(name)
	if err != nil {
		return nil, err
	}
	// Validate if the tool supports it.
	if vt, ok := tool.(ValidatableTool); ok {
		if err := vt.Validate(params); err != nil {
			return nil, err
		}
	}
	// Enforce permissions if the tool declares any.
	if e.checker != nil {
		if pt, ok := tool.(PermissionedTool); ok {
			required := pt.RequiresPermission()
			if len(required) > 0 && !e.checker.Check(ctx, required) {
				return nil, &toolPermissionError{tool: name}
			}
		}
	}
	return measuredExecute(ctx, tool, name, params)
}

// toolPermissionError is returned when a tool's permission check fails.
type toolPermissionError struct{ tool string }

func (e *toolPermissionError) Error() string {
	return "permission denied for tool: " + e.tool
}

// compile-time: verify SimpleRegistry satisfies ExecutableRegistry so the
// fast path in NewExecutor is guaranteed to engage.
var _ ExecutableRegistry = (*SimpleRegistry)(nil)
