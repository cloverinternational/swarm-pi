// Package codemode provides code-mode execution for the swarm-sdk.
package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode/sandbox"
)

// RunCodeTool is the single tool exposed to the model in code mode.
// It accepts a JavaScript code snippet and executes it in a sandboxed
// environment where selected tools are available as callable functions.
type RunCodeTool struct {
	// stubs are the tool functions available inside the sandbox.
	stubs []ToolStubInfo

	// sandbox executes the JavaScript code.
	sandbox sandbox.Sandbox

	// tracer collects nested tool call metadata.
	tracer *TraceCollector

	// dispatch routes tool calls to the executor.
	dispatch *DispatchAdapter

	// maxRetries is the maximum number of retries on syntax errors.
	maxRetries int

	// renderer generates the tool description.
	renderer *SignatureRenderer
}

// NewRunCodeTool creates a new run_code tool.
func NewRunCodeTool(stubs []ToolStubInfo, sandbox sandbox.Sandbox, tracer *TraceCollector, dispatch *DispatchAdapter, maxRetries int) *RunCodeTool {
	return &RunCodeTool{
		stubs:      stubs,
		sandbox:    sandbox,
		tracer:     tracer,
		dispatch:   dispatch,
		maxRetries: maxRetries,
		renderer:   NewSignatureRenderer(),
	}
}

// Name returns the tool name.
func (t *RunCodeTool) Name() string {
	return "run_code"
}

// Description returns the tool description with embedded function signatures.
func (t *RunCodeTool) Description() string {
	baseDescription := `Write and run JavaScript code in a sandboxed environment.

The sandbox provides a restricted JavaScript runtime with no access to:
- File system (no 'fs' or file APIs)
- Network (no 'fetch', 'http', or network APIs)
- External modules (no 'require' or 'import')
- System APIs (no 'process', 'os', or system APIs)

State is preserved between calls (REPL-style). Set 'restart' to true to reset state.

The last expression's value is automatically captured as the return value.
Use 'console.log()' for debug output (appears in the 'printed' field).

Returns the last expression's value directly. If console.log was called, returns
{"output": "<printed text>", "result": <last expression>}.`

	return t.renderer.RenderDescription(t.stubs, baseDescription)
}

// Parameters returns the JSON schema for the tool parameters.
func (t *RunCodeTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{
				"type":        "string",
				"description": "The JavaScript code to execute in the sandbox.",
			},
			"restart": map[string]any{
				"type":        "boolean",
				"description": "Set to true to reset REPL state. When false (default), state is preserved between calls.",
			},
		},
		"required": []string{"code"},
	}
}

// Execute runs the JavaScript code in the sandbox.
func (t *RunCodeTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	code, ok := params["code"].(string)
	if !ok {
		return tools.NewErrorResult(errors.New("code must be a string")), nil
	}

	restart, _ := params["restart"].(bool)

	// Reset sandbox if requested
	if restart {
		if err := t.sandbox.Restart(); err != nil {
			return tools.NewErrorResult(err), nil
		}
	}

	// Reset tracer for this execution
	t.tracer.Reset()

	// Build sandbox tool stubs
	sandboxStubs := t.buildSandboxStubs()

	// Create dispatch function that routes through the adapter
	dispatchFn := t.buildDispatchFn()

	// Execute the code with retry on syntax/parse errors.
	// Syntax errors are retried because the LLM can self-correct; runtime
	// errors (tool failures, rejected promises) are not retried since they
	// indicate a logic problem the LLM needs to see.
	var result sandbox.EvalResult
	var err error
	maxAttempts := max(t.maxRetries+1, 1)
	for attempt := range maxAttempts {
		result, err = t.sandbox.Eval(ctx, code, sandboxStubs, dispatchFn)
		if err != nil {
			return tools.NewErrorResult(err), nil
		}
		// Only retry on syntax/parse errors, not runtime errors
		if !result.IsError || !isSyntaxError(result.ErrorMessage) {
			break
		}
		// On last attempt, return the error as-is
		if attempt == maxAttempts-1 {
			break
		}
	}

	// Build the tool result
	return t.buildToolResult(result), nil
}

// isSyntaxError checks if an error message indicates a JavaScript syntax/parse error.
func isSyntaxError(msg string) bool {
	return strings.Contains(msg, "SyntaxError") ||
		strings.Contains(msg, "Unexpected token") ||
		strings.Contains(msg, "Unexpected end of input") ||
		strings.Contains(msg, "Invalid or unexpected token")
}

// buildSandboxStubs converts ToolStubInfo to sandbox.ToolStub format.
func (t *RunCodeTool) buildSandboxStubs() []sandbox.ToolStub {
	stubs := make([]sandbox.ToolStub, len(t.stubs))
	for i, info := range t.stubs {
		stubs[i] = sandbox.ToolStub{
			Name:         info.Name,
			OriginalName: info.OriginalName,
			IsAsync:      info.IsAsync,
			Signature:    info.Description,
			Description:  info.Description,
		}
	}
	return stubs
}

// buildDispatchFn creates a dispatch function that routes to the adapter.
func (t *RunCodeTool) buildDispatchFn() sandbox.DispatchFn {
	return func(ctx context.Context, toolName string, args map[string]any) (any, error) {
		return t.dispatch.Dispatch(ctx, toolName, args)
	}
}

// buildToolResult constructs a ToolResult from the sandbox evaluation result.
func (t *RunCodeTool) buildToolResult(result sandbox.EvalResult) *tools.ToolResult {
	// Handle error case
	if result.IsError {
		errResult := tools.NewErrorResult(errors.New(result.ErrorMessage))
		errResult.Metadata = t.tracer.ToToolResultMetadata()
		if result.Printed != "" {
			errResult.Output = result.Printed
		}
		return errResult
	}

	// Build successful result
	toolResult := &tools.ToolResult{
		Metadata: t.tracer.ToToolResultMetadata(),
	}

	// Handle printed output
	if result.Printed != "" {
		toolResult.Output = result.Printed
		toolResult.Content = append(toolResult.Content, tools.TextContent(result.Printed))
	}

	// Handle return value. The value is always recorded in metadata for
	// programmatic consumers (and FormatResult). When there is no printed
	// output it is also rendered into Content/Output so the model sees it.
	// Previously only string values were rendered while objects/arrays were
	// dropped — causing run_code to report "(empty)" for any code whose last
	// expression was an object or array.
	if result.Value != nil {
		// If we have printed output, keep the result in metadata only so the
		// printed text remains the primary output (FormatResult appends it).
		if result.Printed != "" {
			toolResult.Metadata["result"] = result.Value
			toolResult.Metadata["has_result"] = true
		} else {
			// No printed output: return the value directly. Strings pass
			// through; objects/arrays are JSON-rendered so they are visible
			// instead of being dropped into the "(empty)" fallback.
			toolResult.Metadata["result"] = result.Value
			toolResult.Metadata["has_result"] = true
			rendered := renderResultValue(result.Value)
			if rendered != "" {
				toolResult.Output = rendered
				toolResult.Content = append(toolResult.Content, tools.TextContent(rendered))
			}
		}
	}

	// Ensure we have some content
	if len(toolResult.Content) == 0 {
		toolResult.Content = append(toolResult.Content, tools.TextContent("(empty)"))
		toolResult.Output = "(empty)"
	}

	return toolResult
}

// renderResultValue converts a run_code last-expression value into a string
// suitable for the model. Strings pass through untouched; everything else is
// JSON-encoded so objects and arrays are readable (not dropped or rendered as
// Go's map[...] syntax). On marshal failure it falls back to %v.
func renderResultValue(v any) string {
	if v == nil {
		return ""
	}
	if str, ok := v.(string); ok {
		return str
	}
	if b, err := json.MarshalIndent(v, "", "  "); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

// RunCodeArguments represents the arguments for the run_code tool.
type RunCodeArguments struct {
	Code    string `json:"code"`
	Restart bool   `json:"restart,omitempty"`
}

// Validate implements tools.ValidatableTool.
func (a *RunCodeArguments) Validate() error {
	if a.Code == "" {
		return errors.New("code cannot be empty")
	}
	return nil
}

// IsRunCodeTool checks if a tool is the run_code tool.
func IsRunCodeTool(tool tools.Tool) bool {
	return tool != nil && tool.Name() == "run_code"
}

// FormatResult formats a tool result for display.
// It extracts the result value and printed output in a readable format.
func FormatResult(result *tools.ToolResult) string {
	if result == nil {
		return ""
	}

	var b strings.Builder

	if result.Output != "" {
		b.WriteString(result.Output)
	}

	if hasResult, ok := result.Metadata["has_result"].(bool); ok && hasResult {
		if resultVal, ok := result.Metadata["result"]; ok {
			if b.Len() > 0 {
				b.WriteString("\n\nResult: ")
			} else {
				b.WriteString("Result: ")
			}
			b.WriteString(formatValue(resultVal))
		}
	}

	return b.String()
}

// formatValue formats a value for display.
func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]any:
		return formatMap(val)
	case []any:
		return formatArray(val)
	default:
		return ""
	}
}

// formatMap formats a map for display.
func formatMap(m map[string]any) string {
	var b strings.Builder
	b.WriteString("{")
	first := true
	for k, v := range m {
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(formatValue(v))
	}
	b.WriteString("}")
	return b.String()
}

// formatArray formats an array for display.
func formatArray(a []any) string {
	var b strings.Builder
	b.WriteString("[")
	for i, v := range a {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(formatValue(v))
	}
	b.WriteString("]")
	return b.String()
}
