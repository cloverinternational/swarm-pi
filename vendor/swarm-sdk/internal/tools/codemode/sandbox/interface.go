// Package sandbox defines the interface for code-mode execution environments.
//
// The Sandbox abstracts over different JavaScript runtimes (goja, QuickJS, etc.)
// so that the code-mode tool can delegate execution without depending on any
// specific interpreter. The primary implementation is GojaSandbox, which uses
// github.com/dop251/goja for pure-Go JavaScript execution with Promise support.
package sandbox

import (
	"context"
)

// DispatchFn is the callback a sandbox uses to invoke a tool.
// The sandbox calls this for each tool invocation from inside the JS code.
// The implementation should route to tools.Executor.Execute with proper
// validation, permissions, and tracing.
//
// Parameters:
//   - ctx: the context from the run_code tool call, with timeout/cancellation
//   - toolName: the original (non-sanitized) tool name
//   - args: the arguments passed from JS (already JSON-compatible)
//
// Returns:
//   - result: the tool's return value, which will be JSON-serialized back to JS
//   - err: any error from the tool; this becomes a rejected Promise in JS
type DispatchFn func(ctx context.Context, toolName string, args map[string]any) (result any, err error)

// EvalResult is the outcome of a sandbox Eval call.
type EvalResult struct {
	// Value is the last expression's value (may be nil for statement-only code).
	Value any

	// Printed is the concatenated stdout from console.log calls.
	Printed string

	// IsError indicates whether the script threw or rejected.
	IsError bool

	// ErrorMessage is the formatted error for model retry (when IsError).
	ErrorMessage string

	// StackTrace is the JS stack trace (when IsError), for debugging.
	StackTrace string

	// ToolCalls maps synthetic call_id → ToolCall metadata for tracing.
	// Each nested tool call from inside the sandbox gets an entry here.
	ToolCalls map[string]ToolCallMeta

	// ToolReturns maps synthetic call_id → ToolReturn metadata for tracing.
	ToolReturns map[string]ToolReturnMeta
}

// ToolCallMeta captures metadata about a nested tool call for observability.
type ToolCallMeta struct {
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
	CallID   string         `json:"call_id"`
}

// ToolReturnMeta captures metadata about a nested tool return for observability.
type ToolReturnMeta struct {
	ToolName   string `json:"tool_name"`
	CallID     string `json:"call_id"`
	IsError    bool   `json:"is_error"`
	DurationMS int64  `json:"duration_ms"`
}

// ToolStub describes a tool function to be exposed inside the sandbox.
type ToolStub struct {
	// Name is the sanitized JavaScript-safe identifier.
	Name string `json:"name"`

	// OriginalName is the actual tool name (may differ if sanitized).
	OriginalName string `json:"original_name"`

	// IsAsync indicates whether the tool should be called with await.
	// True for ParallelCapable tools, false for sequential tools.
	IsAsync bool `json:"is_async"`

	// Signature is the JSDoc-style signature for the tool description.
	// Example: "async function search(query: string, limit?: number): SearchResult[]"
	Signature string `json:"signature"`

	// Description is the tool's one-line description for JSDoc.
	Description string `json:"description"`
}

// Sandbox is an isolated JavaScript execution environment.
//
// A Sandbox persists global state across Eval calls (REPL semantics).
// Use Restart to clear state, Close to release resources.
//
// Thread safety: a Sandbox is NOT safe for concurrent Eval calls.
// Create one Sandbox per conversation and serialize access.
type Sandbox interface {
	// Eval executes a JavaScript code snippet in the sandbox.
	//
	// The sandbox injects the provided tool stubs as callable functions.
	// Each tool call from JS triggers a call to dispatch; the sandbox
	// handles the Promise resolution/rejection based on dispatch's return.
	//
	// The context controls timeout and cancellation:
	//   - ctx.Deadline() → interrupt the JS runtime
	//   - ctx.Err() != nil → abort and return Canceled error
	//
	// Returns EvalResult with the last expression value, printed output,
	// and nested call metadata. On JS error, IsError is true with details.
	Eval(ctx context.Context, code string, stubs []ToolStub, dispatch DispatchFn) (EvalResult, error)

	// Restart clears the sandbox's global state (REPL reset).
	// After Restart, the sandbox behaves as if freshly created.
	Restart() error

	// Close releases the sandbox's resources.
	// After Close, the sandbox must not be used.
	Close() error
}

// Factory creates a new Sandbox for a conversation.
//
// Implementations may pool sandboxes, pre-warm runtimes, etc.
// The returned Sandbox should be in a fresh state (no prior globals).
type Factory func() Sandbox
