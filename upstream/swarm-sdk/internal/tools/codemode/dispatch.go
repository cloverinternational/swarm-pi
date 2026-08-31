// Package codemode provides code-mode execution for the swarm-sdk.
package codemode

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// DispatchAdapter bridges sandbox tool calls to the tools.Executor.
// It implements sandbox.DispatchFn and routes calls through the full
// validation, permission, and tracing pipeline.
type DispatchAdapter struct {
	executor  tools.Executor
	registry  tools.Registry
	tracer    *TraceCollector
	sanitized map[string]string // sanitized name -> original name
}

// NewDispatchAdapter creates a dispatch adapter for the given executor.
func NewDispatchAdapter(executor tools.Executor, registry tools.Registry, tracer *TraceCollector, nameMapping map[string]string) *DispatchAdapter {
	return &DispatchAdapter{
		executor:  executor,
		registry:  registry,
		tracer:    tracer,
		sanitized: nameMapping,
	}
}

// Dispatch implements sandbox.DispatchFn.
// It resolves the tool name, invokes it through the executor, and records the call.
func (d *DispatchAdapter) Dispatch(ctx context.Context, toolName string, args map[string]any) (any, error) {
	// Resolve sanitized name to original
	originalName := toolName
	if mapped, ok := d.sanitized[toolName]; ok {
		originalName = mapped
	}

	// Record call start
	callID := d.tracer.StartCall(originalName, args)

	// Execute via executor (this runs validation, permissions, etc.)
	result, err := d.executor.Execute(ctx, originalName, args)

	// Record call end
	d.tracer.EndCall(callID, result, err)

	if err != nil {
		return nil, err
	}

	if result == nil {
		return nil, nil
	}

	// Return the result content for JS consumption
	// The tool result is converted to a JSON-serializable form
	return d.resultToJS(result), nil
}

// resultToJS converts a ToolResult to a JSON-serializable value for JS.
func (d *DispatchAdapter) resultToJS(result *tools.ToolResult) any {
	if result == nil {
		return nil
	}

	// If there's an error, this shouldn't be called (we return err instead)
	if result.IsError {
		return map[string]any{
			"error":  result.Error,
			"output": result.Output,
		}
	}

	// If there's a single text content, return it as a string
	if len(result.Content) == 1 {
		block := result.Content[0]
		if block.Type == tools.ContentTypeText {
			return block.Text
		}
	}

	// If there are multiple content blocks, return as array
	if len(result.Content) > 0 {
		var contents []any
		for _, block := range result.Content {
			switch block.Type {
			case tools.ContentTypeText:
				contents = append(contents, block.Text)
			case tools.ContentTypeImage:
				// Images are represented as objects with type and data
				contents = append(contents, map[string]any{
					"type":      "image",
					"mediaType": block.MimeType,
					"data":      block.Data,
				})
			default:
				contents = append(contents, map[string]any{
					"type": string(block.Type),
					"text": block.Text,
				})
			}
		}
		return contents
	}

	// Fallback to output string
	if result.Output != "" {
		return result.Output
	}

	// Return metadata if present
	if len(result.Metadata) > 0 {
		return result.Metadata
	}

	return nil
}
