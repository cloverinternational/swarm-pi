package tools

import "github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"

// ─── BaseTool ────────────────────────────────────────────────────────────────

// BaseTool provides zero-value default implementations for all optional tool
// interfaces. Embed it in your tool struct to avoid boilerplate:
//
//	type MyTool struct {
//	    tools.BaseTool
//	}
//	func (t *MyTool) Name() string        { return "my_tool" }
//	func (t *MyTool) Description() string { return "..." }
//	func (t *MyTool) Parameters() any     { return mySchema }
//	func (t *MyTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
//	    // your logic
//	}
type BaseTool struct{}

// Validate always returns nil (no validation). Override to add checks.
func (BaseTool) Validate(_ map[string]any) error { return nil }

// IsIdempotent returns false by default. Override if your tool is read-only.
func (BaseTool) IsIdempotent() bool { return false }

// RequiresPermission returns an empty slice (no permissions required). Override to restrict access.
func (BaseTool) RequiresPermission() []Permission { return nil }

// SupportedContentTypes returns nil (any content type). Override to advertise specific types.
func (BaseTool) SupportedContentTypes() []ContentType { return nil }

// OptimizationHints returns nil (registry defaults). Override to tune behaviour.
func (BaseTool) OptimizationHints() *OptimizationHints { return nil }

// RequiresVision returns false by default. Override if your tool needs vision capability.
// The params argument allows dynamic detection (e.g., Read tool returns true for image files).
func (BaseTool) RequiresVision(_ map[string]any) bool { return false }

// compile-time check: BaseTool satisfies all optional capability interfaces
// when embedded in a concrete type that provides the four mandatory methods.
var (
	_ interface{ Validate(map[string]any) error }         = BaseTool{}
	_ interface{ IsIdempotent() bool }                    = BaseTool{}
	_ interface{ RequiresPermission() []Permission }      = BaseTool{}
	_ interface{ SupportedContentTypes() []ContentType }  = BaseTool{}
	_ interface{ OptimizationHints() *OptimizationHints } = BaseTool{}
	_ interface{ RequiresVision(map[string]any) bool }    = BaseTool{}
)

// ─── Concrete types ───────────────────────────────────────────────────────────

// ToolExample demonstrates tool usage.
type ToolExample struct {
	// Description explains what this example does.
	Description string
	// Parameters shows example input (must match tool schema).
	Parameters map[string]any
	// ExpectedOutput shows example output (optional).
	ExpectedOutput string
}

// ToolMetadata contains additional information about a tool.
type ToolMetadata struct {
	// Version is the tool version.
	Version string
	// Author is who created the tool.
	Author string
	// Category groups related tools (filesystem, network, etc.).
	Category string
	// Tags are keywords for discovery.
	Tags []string
	// Deprecated indicates if this tool should no longer be used.
	Deprecated bool
	// ReplacedBy suggests an alternative if deprecated.
	ReplacedBy string
	// Source indicates where the tool comes from (builtin, mcp, config).
	Source ToolSource
	// ServerName identifies the MCP server for ToolSourceMCP tools.
	ServerName string
	// SupportsParallel indicates if this tool can be executed concurrently
	// with other tools. Determined by checking ParallelCapable or heuristics.
	SupportsParallel bool
	// Hosted carries additive hosted policy metadata for this tool.
	Hosted *hosted.PolicyMetadata
}

// ToolSource indicates where a tool originates.
type ToolSource string

const (
	// ToolSourceBuiltin indicates a built-in SDK tool.
	ToolSourceBuiltin ToolSource = "builtin"
	// ToolSourceMCP indicates a tool from an MCP server.
	ToolSourceMCP ToolSource = "mcp"
	// ToolSourceConfig indicates a tool from config/YAML.
	ToolSourceConfig ToolSource = "config"
	// ToolSourceCustom indicates a custom user-provided tool.
	ToolSourceCustom ToolSource = "custom"
)
