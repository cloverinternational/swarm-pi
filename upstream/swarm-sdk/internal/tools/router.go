// Package tools provides tool routing and discovery for parallel execution.
package tools

import (
	"strings"
)

// ToolRouter aggregates tools from multiple sources (registry, MCP, dynamic)
// and provides routing capabilities for tool execution. It determines which
// tools can be executed in parallel based on their capabilities.
type ToolRouter struct {
	// registry is the main tool registry
	registry Registry

	// mcpTools are tools from MCP servers (optional)
	mcpTools map[string]Tool

	// specs contains all configured tool specifications with parallel support flags
	specs []ConfiguredToolSpec
}

// ConfiguredToolSpec wraps a tool with parallel execution metadata.
type ConfiguredToolSpec struct {
	// Tool is the actual tool instance
	Tool Tool

	// SupportsParallel indicates if this tool can be executed concurrently
	SupportsParallel bool
}

// ToolRouterConfig configures the tool router.
type ToolRouterConfig struct {
	// Registry is the main tool registry
	Registry Registry

	// MCPTools are tools from MCP servers (optional)
	MCPTools map[string]Tool
}

// NewToolRouter creates a new tool router from the given configuration.
func NewToolRouter(config ToolRouterConfig) *ToolRouter {
	router := &ToolRouter{
		registry: config.Registry,
		mcpTools: config.MCPTools,
		specs:    []ConfiguredToolSpec{},
	}

	// Build specs from registry
	router.buildSpecs()

	return router
}

// buildSpecs constructs the tool specifications from registry and MCP tools.
func (r *ToolRouter) buildSpecs() {
	r.specs = []ConfiguredToolSpec{}

	// Add tools from registry
	if r.registry != nil {
		for _, name := range r.registry.List() {
			tool, err := r.registry.Get(name)
			if err != nil {
				continue
			}

			supportsParallel := determineParallelSupport(tool)
			r.specs = append(r.specs, ConfiguredToolSpec{
				Tool:             tool,
				SupportsParallel: supportsParallel,
			})
		}
	}

	// Add MCP tools
	for _, mcpTool := range r.mcpTools {
		// MCP tools default to non-parallel unless they implement ParallelCapable
		supportsParallel := determineParallelSupport(mcpTool)
		r.specs = append(r.specs, ConfiguredToolSpec{
			Tool:             mcpTool,
			SupportsParallel: supportsParallel,
		})
	}
}

// GetTool retrieves a tool by name from any source (registry or MCP).
func (r *ToolRouter) Tool(name string) (Tool, bool) {
	// Try registry first
	if r.registry != nil {
		tool, err := r.registry.Get(name)
		if err == nil && tool != nil {
			return tool, true
		}
	}

	// Try MCP tools
	if r.mcpTools != nil {
		if tool, ok := r.mcpTools[name]; ok {
			return tool, true
		}
	}

	return nil, false
}

// ToolSupportsParallel checks if a tool can be executed concurrently.
func (r *ToolRouter) ToolSupportsParallel(name string) bool {
	for _, spec := range r.specs {
		if spec.Tool.Name() == name {
			return spec.SupportsParallel
		}
	}

	// If not found in specs, assume non-parallel for safety
	return false
}

// ListTools returns all tool names available through this router.
func (r *ToolRouter) ListTools() []string {
	names := make([]string, 0, len(r.specs))
	for _, spec := range r.specs {
		names = append(names, spec.Tool.Name())
	}
	return names
}

// ListParallelTools returns names of tools that support parallel execution.
func (r *ToolRouter) ListParallelTools() []string {
	names := make([]string, 0)
	for _, spec := range r.specs {
		if spec.SupportsParallel {
			names = append(names, spec.Tool.Name())
		}
	}
	return names
}

// ListExclusiveTools returns names of tools that require exclusive execution.
func (r *ToolRouter) ListExclusiveTools() []string {
	names := make([]string, 0)
	for _, spec := range r.specs {
		if !spec.SupportsParallel {
			names = append(names, spec.Tool.Name())
		}
	}
	return names
}

// GetSpec returns the configured spec for a tool by name.
func (r *ToolRouter) Spec(name string) (*ConfiguredToolSpec, bool) {
	for i, spec := range r.specs {
		if spec.Tool.Name() == name {
			return &r.specs[i], true
		}
	}
	return nil, false
}

// Refresh rebuilds the tool specifications from current registry state.
// This should be called if tools are added/removed from the registry.
func (r *ToolRouter) Refresh() {
	r.buildSpecs()
}

// determineParallelSupport checks if a tool supports parallel execution.
// It uses multiple strategies to determine this:
// 1. Check if tool explicitly implements ParallelCapable interface
// 2. Check metadata SupportsParallel field
// 3. Use heuristics based on tool name and characteristics
func determineParallelSupport(tool Tool) bool {
	// Strategy 1: Check if tool explicitly implements ParallelCapable
	if pc, ok := tool.(ParallelCapable); ok {
		return pc.SupportsParallel()
	}

	// Strategy 2: Check metadata
	if mp, ok := tool.(MetadataProvider); ok {
		metadata := mp.ToolMetadata()
		if metadata != nil && metadata.SupportsParallel {
			return true
		}
	}

	// Strategy 3: Heuristics based on tool characteristics
	return isToolParallelSafeByHeuristic(tool)
}

// isToolParallelSafeByHeuristic uses heuristics to determine if a tool is parallel-safe.
// This is a fallback when the tool doesn't explicitly declare parallel support.
func isToolParallelSafeByHeuristic(tool Tool) bool {
	name := strings.ToLower(tool.Name())

	// Read-only tools are generally parallel-safe
	readOnlyPatterns := []string{
		"read", "get", "list", "search", "grep", "find",
		"check", "status", "view", "show", "cat",
	}

	for _, pattern := range readOnlyPatterns {
		if strings.Contains(name, pattern) {
			// Check if it's also idempotent (strong indicator of read-only)
			if it, ok := tool.(IdempotentTool); ok && it.IsIdempotent() {
				return true
			}
		}
	}

	// Write/modify tools are NOT parallel-safe
	writePatterns := []string{
		"write", "edit", "delete", "remove", "update",
		"create", "modify", "set", "bash", "exec",
	}

	for _, pattern := range writePatterns {
		if strings.Contains(name, pattern) {
			return false
		}
	}

	// Background agent spawning is parallel-safe (registration is thread-safe)
	if strings.Contains(name, "spawn") || strings.Contains(name, "background") {
		if strings.Contains(name, "agent") {
			return true
		}
	}

	// Default to non-parallel for safety
	return false
}
