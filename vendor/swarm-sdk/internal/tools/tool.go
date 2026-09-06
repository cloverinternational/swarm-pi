// Package tools defines the interface for tools that agents can execute.
//
// # Ring 0 — pure interface definitions
//
// This file contains only interface declarations. No implementations, no
// concrete types, no compile-time assertions. Those live in base.go so that
// this file serves as the authoritative interface contract.
package tools

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

// Tool defines the minimal interface that all tools must implement.
// Tools provide capabilities to agents (file I/O, web access, code execution, etc.).
//
// Only four methods are mandatory. Additional capabilities (validation, permissions,
// content types, caching hints) are expressed through optional extension interfaces
// that the registry detects at runtime via type assertions:
//
//   - [ValidatableTool]   — parameter validation before Execute
//   - [IdempotentTool]    — safe-to-cache hint
//   - [PermissionedTool]  — permission requirements
//   - [ContentTypedTool]  — MIME types this tool can produce
//   - [HintedTool]        — optimisation guidance for the registry
//
// Embed [BaseTool] to get zero-value defaults for all optional interfaces so
// you only need to override the behaviour you care about.
type Tool interface {
	// Name returns the unique identifier for this tool.
	// Must be lowercase with underscores (e.g., "file_read", "web_search").
	Name() string

	// Description returns a human-readable description of what the tool does.
	// This is shown to the LLM to help it decide when to use the tool.
	// Should be detailed (3-4+ sentences) for best results with Claude.
	Description() string

	// Parameters returns the JSON Schema defining the tool's parameters.
	// The schema is used for validation and is sent to the LLM for function calling.
	Parameters() any

	// Execute runs the tool with the given parameters.
	// Returns the result or an error.
	// All errors should use the SDK error taxonomy.
	Execute(ctx context.Context, params map[string]any) (*ToolResult, error)
}

// ─── Optional capability interfaces ─────────────────────────────────────────

// ValidatableTool extends Tool with pre-execution parameter validation.
// The registry calls Validate before Execute to fail fast on bad input.
type ValidatableTool interface {
	Tool
	// Validate checks if the given parameters are valid for this tool.
	// Return a non-nil error to reject execution before it starts.
	Validate(params map[string]any) error
}

// IdempotentTool extends Tool with a cache-safety hint.
type IdempotentTool interface {
	Tool
	// IsIdempotent returns true when the tool produces the same output for the
	// same input (e.g. file_read). The registry may use this to skip re-execution.
	IsIdempotent() bool
}

// SafeRepositoryInspector is a mechanically read-only, workspace-confined
// repository text inspection capability. Agents that require safe inspection
// resolve this capability rather than trusting a tool name or prompt claim.
type SafeRepositoryInspector interface {
	Tool
	// IsSafeRepositoryInspector is a marker method. Implementations must not
	// mutate state, execute processes, or access the network.
	IsSafeRepositoryInspector() bool
}

// PermissionedTool extends Tool with permission requirements.
// The registry enforces these before calling Execute.
type PermissionedTool interface {
	Tool
	// RequiresPermission returns the permissions needed to execute this tool.
	// An empty slice means no special permissions are required.
	RequiresPermission() []Permission
}

// ContentTypedTool extends Tool with MIME-type advertisement.
type ContentTypedTool interface {
	Tool
	// SupportedContentTypes returns the content types this tool can produce.
	// Used for provider capability negotiation.
	SupportedContentTypes() []ContentType
}

// HintedTool extends Tool with optimisation guidance.
type HintedTool interface {
	Tool
	// OptimizationHints provides guidance for efficient tool use.
	// Return nil to use registry defaults.
	OptimizationHints() *OptimizationHints
}

// ToolWithExamples extends Tool with input examples for complex tools.
// This is optional but recommended for tools with nested parameters.
// Based on Anthropic's advanced tool use features.
type ToolWithExamples interface {
	Tool
	// InputExamples returns example inputs for this tool.
	// Each example must be valid according to Parameters() schema.
	InputExamples() []ToolExample
}

// StreamingTool extends Tool with incremental output support.
// Tools implementing this interface can stream stdout/stderr as they execute,
// rather than waiting until completion to return all output at once.
type StreamingTool interface {
	Tool
	// ExecuteStreaming runs the tool and emits output incrementally via callback.
	// The onOutput callback is called for each chunk of output (typically one line).
	// The stream parameter is "stdout" or "stderr".
	// Returns the complete ToolResult after execution finishes.
	ExecuteStreaming(ctx context.Context, params map[string]any,
		onOutput func(chunk string, stream string)) (*ToolResult, error)
}

// VisionRequiringTool extends Tool with a vision capability requirement.
// Tools that process images (file_read for images, screenshot analysis, etc.)
// should implement this interface to signal they need vision capability.
// The registry can use this to route tool calls through vision-capable models
// when the primary agent model lacks vision support.
type VisionRequiringTool interface {
	Tool
	// RequiresVision returns true when this tool needs vision capability
	// to process the given parameters. This allows dynamic detection
	// (e.g., Read tool returns true only when the file is an image).
	// Static vision tools can always return true.
	RequiresVision(params map[string]any) bool
}

// ParallelCapable indicates a tool can be executed concurrently with other tools.
// Tools that are read-only, stateless, or use proper synchronization should implement
// this interface to enable parallel execution. Tools that modify global state, filesystem,
// or have race conditions should NOT implement this interface.
//
// Examples of parallel-safe tools:
//   - file_read (read-only)
//   - grep (read-only)
//   - list_dir (read-only)
//
// Examples of non-parallel-safe tools:
//   - file_write (filesystem modification)
//   - bash (arbitrary side effects)
//   - todo_write (shared state modification)
type ParallelCapable interface {
	// SupportsParallel returns true if this tool can be executed concurrently
	// with other tools in the same batch. The tool must be thread-safe and
	// not have race conditions with other tool executions.
	SupportsParallel() bool
}

// MetadataProvider allows tools to supply registry metadata.
type MetadataProvider interface {
	ToolMetadata() *ToolMetadata
}

// HostedMetadataProvider allows tools to expose hosted policy and capability metadata.
type HostedMetadataProvider interface {
	HostedMetadata() *hosted.PolicyMetadata
}
