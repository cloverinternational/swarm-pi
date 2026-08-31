package mcptools

import (
	"maps"
	"strings"
	"sync"
)

// MCPToolNameResolver resolves MCP tool API names to friendly display names
// API names look like: "mcp_filesystem_read_file"
// Friendly names look like: "filesystem:read-file"
type MCPToolNameResolver struct {
	mu      sync.RWMutex
	mapping map[string]string // API name -> friendly name
}

// NewMCPToolNameResolver creates a new resolver
func NewMCPToolNameResolver() *MCPToolNameResolver {
	return &MCPToolNameResolver{
		mapping: make(map[string]string),
	}
}

// Register adds a mapping from API name to friendly name
func (r *MCPToolNameResolver) Register(apiName, friendlyName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mapping[apiName] = friendlyName
}

// Resolve returns the friendly name for an API name
// If not found, returns a cleaned version of the API name
func (r *MCPToolNameResolver) Resolve(apiName string) string {
	r.mu.RLock()
	friendlyName, ok := r.mapping[apiName]
	r.mu.RUnlock()

	if ok {
		return friendlyName
	}

	// Fallback: clean up the API name
	return r.cleanAPIName(apiName)
}

// IsMCPTool returns true if the API name is from an MCP server
func (r *MCPToolNameResolver) IsMCPTool(apiName string) bool {
	return strings.HasPrefix(apiName, "mcp_")
}

// cleanAPIName converts API names to friendly format
// "mcp_filesystem_read_file" -> "filesystem:read-file"
func (r *MCPToolNameResolver) cleanAPIName(apiName string) string {
	// Not an MCP tool, return as-is
	if !strings.HasPrefix(apiName, "mcp_") {
		return apiName
	}

	// Remove "mcp_" prefix
	name := strings.TrimPrefix(apiName, "mcp_")

	// Split into parts: server_tool_name
	// The first part is the server name, the rest is the tool name
	parts := strings.SplitN(name, "_", 2)
	if len(parts) == 2 {
		server := parts[0]
		tool := parts[1]
		// Replace underscores in tool name with hyphens for readability
		tool = strings.ReplaceAll(tool, "_", "-")
		return server + ":" + tool
	}

	// No transformation possible, just remove mcp_ prefix
	return name
}

// BulkRegister registers multiple mappings at once
func (r *MCPToolNameResolver) BulkRegister(mappings map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	maps.Copy(r.mapping, mappings)
}

// GetMapping returns a copy of all registered mappings (for debugging/export)
func (r *MCPToolNameResolver) GetMapping() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]string, len(r.mapping))
	maps.Copy(result, r.mapping)
	return result
}

// Clear removes all registered mappings
func (r *MCPToolNameResolver) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mapping = make(map[string]string)
}

// Count returns the number of registered mappings
func (r *MCPToolNameResolver) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.mapping)
}

// GetServerName extracts the server name from an API name
// "mcp_filesystem_read_file" -> "filesystem"
func (r *MCPToolNameResolver) GetServerName(apiName string) string {
	if !strings.HasPrefix(apiName, "mcp_") {
		return ""
	}

	name := strings.TrimPrefix(apiName, "mcp_")
	parts := strings.SplitN(name, "_", 2)
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

// GetToolName extracts just the tool name from an API name
// "mcp_filesystem_read_file" -> "read-file"
func (r *MCPToolNameResolver) GetToolName(apiName string) string {
	if !strings.HasPrefix(apiName, "mcp_") {
		return apiName
	}

	name := strings.TrimPrefix(apiName, "mcp_")
	parts := strings.SplitN(name, "_", 2)
	if len(parts) == 2 {
		return strings.ReplaceAll(parts[1], "_", "-")
	}
	return name
}
