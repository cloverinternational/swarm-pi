package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	sdkmcp "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// MCPTools provides tools for managing MCP servers via AI assistant
type MCPTools struct {
	mcpManager *MCPManager
}

// NewMCPTools creates a new MCP tools instance
func NewMCPTools(mcpManager *MCPManager) *MCPTools {
	return &MCPTools{
		mcpManager: mcpManager,
	}
}

// GetTools returns all available MCP management tools
func (mt *MCPTools) GetTools() []tools.Tool {
	return []tools.Tool{
		&AddMCPServerTool{mcpManager: mt.mcpManager},
		&EnableMCPServerTool{mcpManager: mt.mcpManager},
		&DisableMCPServerTool{mcpManager: mt.mcpManager},
		&ListMCPServersTool{mcpManager: mt.mcpManager},
		&GetMCPServerStatusTool{mcpManager: mt.mcpManager},
		&TestMCPServerTool{mcpManager: mt.mcpManager},
		&DeleteMCPServerTool{mcpManager: mt.mcpManager},
		&ListMCPServerToolsTool{mcpManager: mt.mcpManager},
		&ConfigureMCPServerToolsTool{mcpManager: mt.mcpManager},
		&ReconnectMCPServerTool{mcpManager: mt.mcpManager},
	}
}

// AddMCPServerTool adds a new MCP server
type AddMCPServerTool struct {
	mcpManager *MCPManager
}

func (t *AddMCPServerTool) Name() string {
	return "add_mcp_server"
}

func (t *AddMCPServerTool) Description() string {
	return "Add a new MCP (Model Context Protocol) server to the system. Returns the server configuration details."
}

func (t *AddMCPServerTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Unique name for the MCP server (e.g., 'filesystem', 'github', 'slack')",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "Server type: 'stdio' (subprocess), 'sse' (Server-Sent Events), 'http' (HTTP), or 'oauth' (OAuth 2.0)",
				"enum":        []string{"stdio", "sse", "http", "oauth"},
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Command to execute (required for stdio type)",
			},
			"args": map[string]any{
				"type":        "array",
				"description": "Command arguments (for stdio type)",
				"items": map[string]any{
					"type": "string",
				},
			},
			"url": map[string]any{
				"type":        "string",
				"description": "Server URL (required for sse/http/oauth types)",
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "HTTP headers (for sse/http/oauth types)",
			},
			"client_id": map[string]any{
				"type":        "string",
				"description": "OAuth client ID (required for oauth type)",
			},
			"scopes": map[string]any{
				"type":        "array",
				"description": "OAuth scopes (for oauth type)",
				"items": map[string]any{
					"type": "string",
				},
			},
			"env": map[string]any{
				"type":        "object",
				"description": "Environment variables for the server",
			},
			"work_dir": map[string]any{
				"type":        "string",
				"description": "Working directory (for stdio type)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Connection timeout in seconds (default: 30)",
				"default":     30,
			},
			"enabled": map[string]any{
				"type":        "boolean",
				"description": "Whether to enable the server immediately (default: true)",
				"default":     true,
			},
		},
		"required": []string{"name", "type"},
	}
}

func (t *AddMCPServerTool) Validate(params map[string]any) error        { return nil }
func (t *AddMCPServerTool) IsIdempotent() bool                          { return false }
func (t *AddMCPServerTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *AddMCPServerTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *AddMCPServerTool) RequiresPermission() []tools.Permission      { return nil }

func (t *AddMCPServerTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	serverType, _ := params["type"].(string)

	if name == "" || serverType == "" {
		return tools.NewErrorResult(errors.New("name and type are required")), nil
	}

	// Build server config
	config := &commands.MCPServerConfig{
		Name:    name,
		Type:    commands.MCPServerType(serverType),
		Enabled: true,
	}

	if enabled, ok := params["enabled"].(bool); ok {
		config.Enabled = enabled
	}

	// Type-specific configuration
	switch serverType {
	case "stdio":
		command, _ := params["command"].(string)
		if command == "" {
			return tools.NewErrorResult(errors.New("command is required for stdio type")), nil
		}
		config.Command = command

		if argsArray, ok := params["args"].([]any); ok {
			for _, arg := range argsArray {
				if argStr, ok := arg.(string); ok {
					config.Args = append(config.Args, argStr)
				}
			}
		}

		if workDir, ok := params["work_dir"].(string); ok {
			config.WorkingDir = workDir
		}

	case "sse", "http", "oauth":
		url, _ := params["url"].(string)
		if url == "" {
			return tools.NewErrorResult(fmt.Errorf("url is required for %s type", serverType)), nil
		}
		config.URL = url

		if headersMap, ok := params["headers"].(map[string]any); ok {
			config.Headers = make(map[string]string)
			for k, v := range headersMap {
				if vStr, ok := v.(string); ok {
					config.Headers[k] = vStr
				}
			}
		}

		if serverType == "oauth" {
			clientID, _ := params["client_id"].(string)
			if clientID == "" {
				return tools.NewErrorResult(errors.New("client_id is required for oauth type")), nil
			}

			oauthConfig := &commands.MCPOAuthConfig{
				ClientID: clientID,
			}

			if clientSecret, ok := params["client_secret"].(string); ok {
				oauthConfig.ClientSecret = clientSecret
			}

			if scopesArray, ok := params["scopes"].([]any); ok {
				var scopesList []string
				for _, scope := range scopesArray {
					if scopeStr, ok := scope.(string); ok {
						scopesList = append(scopesList, scopeStr)
					}
				}
				oauthConfig.Scopes = strings.Join(scopesList, " ")
			}

			config.OAuth = oauthConfig
		}
	}

	// Common configuration
	if envMap, ok := params["env"].(map[string]any); ok {
		config.Env = make(map[string]string)
		for k, v := range envMap {
			if vStr, ok := v.(string); ok {
				config.Env[k] = vStr
			}
		}
	}

	if timeout, ok := params["timeout"].(float64); ok {
		config.Timeout = int(timeout)
	}

	// Add the server
	err := t.mcpManager.AddServer(ctx, config)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("Failed to add MCP server: %v", err)), nil
	}

	result := map[string]any{
		"success": true,
		"server": map[string]any{
			"name":    config.Name,
			"type":    string(config.Type),
			"enabled": config.Enabled,
		},
		"message": fmt.Sprintf("MCP server '%s' added successfully", name),
	}

	jsonResult, _ := json.MarshalIndent(result, "", "  ")
	return tools.NewToolResult(string(jsonResult)), nil
}

// EnableMCPServerTool enables an MCP server
type EnableMCPServerTool struct {
	mcpManager *MCPManager
}

func (t *EnableMCPServerTool) Name() string {
	return "enable_mcp_server"
}

func (t *EnableMCPServerTool) Description() string {
	return "Enable an MCP server to make its tools available. The server will be started and connected."
}

func (t *EnableMCPServerTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the MCP server to enable",
			},
		},
		"required": []string{"name"},
	}
}

func (t *EnableMCPServerTool) Validate(params map[string]any) error        { return nil }
func (t *EnableMCPServerTool) IsIdempotent() bool                          { return true }
func (t *EnableMCPServerTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *EnableMCPServerTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *EnableMCPServerTool) RequiresPermission() []tools.Permission      { return nil }

func (t *EnableMCPServerTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(errors.New("name is required")), nil
	}

	err := t.mcpManager.EnableServer(name)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("Failed to enable server: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("MCP server '%s' enabled successfully", name)), nil
}

// DisableMCPServerTool disables an MCP server
type DisableMCPServerTool struct {
	mcpManager *MCPManager
}

func (t *DisableMCPServerTool) Name() string {
	return "disable_mcp_server"
}

func (t *DisableMCPServerTool) Description() string {
	return "Disable an MCP server to hide its tools. The server will be disconnected but not removed."
}

func (t *DisableMCPServerTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the MCP server to disable",
			},
		},
		"required": []string{"name"},
	}
}

func (t *DisableMCPServerTool) Validate(params map[string]any) error        { return nil }
func (t *DisableMCPServerTool) IsIdempotent() bool                          { return true }
func (t *DisableMCPServerTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *DisableMCPServerTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *DisableMCPServerTool) RequiresPermission() []tools.Permission      { return nil }

func (t *DisableMCPServerTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(errors.New("name is required")), nil
	}

	err := t.mcpManager.DisableServer(name)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("Failed to disable server: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("MCP server '%s' disabled successfully", name)), nil
}

// ListMCPServersTool lists all MCP servers
type ListMCPServersTool struct {
	mcpManager *MCPManager
}

func (t *ListMCPServersTool) Name() string {
	return "list_mcp_servers"
}

func (t *ListMCPServersTool) Description() string {
	return "List all MCP servers with their status, connection state, and available tools count."
}

func (t *ListMCPServersTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"include_disabled": map[string]any{
				"type":        "boolean",
				"description": "Include disabled servers in the list (default: true)",
				"default":     true,
			},
		},
	}
}

func (t *ListMCPServersTool) Validate(params map[string]any) error        { return nil }
func (t *ListMCPServersTool) IsIdempotent() bool                          { return true }
func (t *ListMCPServersTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ListMCPServersTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *ListMCPServersTool) RequiresPermission() []tools.Permission      { return nil }

func (t *ListMCPServersTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	includeDisabled := true
	if val, ok := params["include_disabled"].(bool); ok {
		includeDisabled = val
	}

	servers := t.mcpManager.GetServers()

	var serverList []map[string]any
	for _, server := range servers {
		if !server.Config.Enabled && !includeDisabled {
			continue
		}

		serverInfo := map[string]any{
			"name":       server.Config.Name,
			"type":       string(server.Config.Type),
			"enabled":    server.Config.Enabled,
			"connected":  server.Connected,
			"tool_count": len(server.Tools),
		}

		if server.Error != "" {
			serverInfo["error"] = server.Error
		}

		if server.Config.Type == "stdio" {
			serverInfo["command"] = server.Config.Command
		} else {
			serverInfo["url"] = server.Config.URL
		}

		serverList = append(serverList, serverInfo)
	}

	result := map[string]any{
		"servers": serverList,
		"total":   len(serverList),
	}

	jsonResult, _ := json.MarshalIndent(result, "", "  ")
	return tools.NewToolResult(string(jsonResult)), nil
}

// GetMCPServerStatusTool gets detailed status of a specific MCP server
type GetMCPServerStatusTool struct {
	mcpManager *MCPManager
}

func (t *GetMCPServerStatusTool) Name() string {
	return "get_mcp_server_status"
}

func (t *GetMCPServerStatusTool) Description() string {
	return "Get detailed status information for a specific MCP server including connection state, tools, and errors."
}

func (t *GetMCPServerStatusTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the MCP server",
			},
		},
		"required": []string{"name"},
	}
}

func (t *GetMCPServerStatusTool) Validate(params map[string]any) error        { return nil }
func (t *GetMCPServerStatusTool) IsIdempotent() bool                          { return true }
func (t *GetMCPServerStatusTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *GetMCPServerStatusTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *GetMCPServerStatusTool) RequiresPermission() []tools.Permission      { return nil }

func (t *GetMCPServerStatusTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(errors.New("name is required")), nil
	}

	server := t.mcpManager.GetServer(name)
	if server == nil {
		return tools.NewErrorResult(fmt.Errorf("MCP server '%s' not found", name)), nil
	}

	status := map[string]any{
		"name":      server.Config.Name,
		"type":      string(server.Config.Type),
		"enabled":   server.Config.Enabled,
		"connected": server.Connected,
	}

	if server.Error != "" {
		status["error"] = server.Error
		status["status"] = "error"
	} else if server.Connected {
		status["status"] = "connected"
	} else if server.Config.Enabled {
		status["status"] = "connecting"
	} else {
		status["status"] = "disabled"
	}

	// Add configuration details
	config := map[string]any{
		"type": string(server.Config.Type),
	}

	if server.Config.Type == "stdio" {
		config["command"] = server.Config.Command
		if len(server.Config.Args) > 0 {
			config["args"] = server.Config.Args
		}
		if server.Config.WorkingDir != "" {
			config["work_dir"] = server.Config.WorkingDir
		}
	} else {
		config["url"] = server.Config.URL
	}

	if server.Config.Timeout > 0 {
		config["timeout"] = server.Config.Timeout
	}

	status["config"] = config

	// Add tools information
	var toolsList []map[string]any
	for _, tool := range server.Tools {
		toolInfo := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
		}

		// Check if tool is disabled
		if server.Config.DisabledTools != nil {
			if _, disabled := server.Config.DisabledTools[tool.Name]; disabled {
				toolInfo["enabled"] = false
			} else {
				toolInfo["enabled"] = true
			}
		} else {
			toolInfo["enabled"] = true
		}

		toolsList = append(toolsList, toolInfo)
	}

	status["tools"] = toolsList
	status["tool_count"] = len(toolsList)

	jsonResult, _ := json.MarshalIndent(status, "", "  ")
	return tools.NewToolResult(string(jsonResult)), nil
}

// TestMCPServerTool tests connection to an MCP server
type TestMCPServerTool struct {
	mcpManager *MCPManager
}

func (t *TestMCPServerTool) Name() string {
	return "test_mcp_server"
}

func (t *TestMCPServerTool) Description() string {
	return "Test the connection to an MCP server and verify it's working correctly."
}

func (t *TestMCPServerTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the MCP server to test",
			},
		},
		"required": []string{"name"},
	}
}

func (t *TestMCPServerTool) Validate(params map[string]any) error        { return nil }
func (t *TestMCPServerTool) IsIdempotent() bool                          { return true }
func (t *TestMCPServerTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *TestMCPServerTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *TestMCPServerTool) RequiresPermission() []tools.Permission      { return nil }

func (t *TestMCPServerTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(errors.New("name is required")), nil
	}

	server := t.mcpManager.GetServer(name)
	if server == nil {
		return tools.NewErrorResult(fmt.Errorf("MCP server '%s' not found", name)), nil
	}

	// Test connection
	var result map[string]any
	if !server.Config.Enabled {
		result = map[string]any{
			"success": false,
			"message": "Server is disabled",
			"server":  name,
		}
	} else if !server.Connected {
		result = map[string]any{
			"success": false,
			"message": "Server is not connected",
			"server":  name,
			"error":   server.Error,
		}
	} else {
		result = map[string]any{
			"success":    true,
			"message":    "Server is connected and working",
			"server":     name,
			"tool_count": len(server.Tools),
			"tools":      getToolNames(server.Tools),
		}
	}

	jsonResult, _ := json.MarshalIndent(result, "", "  ")
	toolResult := tools.NewToolResult(string(jsonResult))
	toolResult.IsError = !result["success"].(bool)
	return toolResult, nil
}

// DeleteMCPServerTool deletes an MCP server
type DeleteMCPServerTool struct {
	mcpManager *MCPManager
}

func (t *DeleteMCPServerTool) Name() string {
	return "delete_mcp_server"
}

func (t *DeleteMCPServerTool) Description() string {
	return "Delete an MCP server configuration. This removes the server and disconnects it."
}

func (t *DeleteMCPServerTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the MCP server to delete",
			},
		},
		"required": []string{"name"},
	}
}

func (t *DeleteMCPServerTool) Validate(params map[string]any) error        { return nil }
func (t *DeleteMCPServerTool) IsIdempotent() bool                          { return true }
func (t *DeleteMCPServerTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *DeleteMCPServerTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *DeleteMCPServerTool) RequiresPermission() []tools.Permission      { return nil }

func (t *DeleteMCPServerTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(errors.New("name is required")), nil
	}

	err := t.mcpManager.DeleteServer(name)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("Failed to delete server: %v", err)), nil
	}

	return tools.NewToolResult(fmt.Sprintf("MCP server '%s' deleted successfully", name)), nil
}

// ListMCPServerToolsTool lists tools provided by a specific MCP server
type ListMCPServerToolsTool struct {
	mcpManager *MCPManager
}

func (t *ListMCPServerToolsTool) Name() string {
	return "list_mcp_server_tools"
}

func (t *ListMCPServerToolsTool) Description() string {
	return "List all tools provided by a specific MCP server with their descriptions and enabled status."
}

func (t *ListMCPServerToolsTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the MCP server",
			},
		},
		"required": []string{"name"},
	}
}

func (t *ListMCPServerToolsTool) Validate(params map[string]any) error        { return nil }
func (t *ListMCPServerToolsTool) IsIdempotent() bool                          { return true }
func (t *ListMCPServerToolsTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ListMCPServerToolsTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *ListMCPServerToolsTool) RequiresPermission() []tools.Permission      { return nil }

func (t *ListMCPServerToolsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(errors.New("name is required")), nil
	}

	server := t.mcpManager.GetServer(name)
	if server == nil {
		return tools.NewErrorResult(fmt.Errorf("MCP server '%s' not found", name)), nil
	}

	var toolsList []map[string]any
	for _, tool := range server.Tools {
		enabled := true
		if server.Config.DisabledTools != nil {
			if _, disabled := server.Config.DisabledTools[tool.Name]; disabled {
				enabled = false
			}
		}

		toolInfo := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"enabled":     enabled,
		}

		toolsList = append(toolsList, toolInfo)
	}

	result := map[string]any{
		"server":     name,
		"tools":      toolsList,
		"tool_count": len(toolsList),
	}

	jsonResult, _ := json.MarshalIndent(result, "", "  ")
	return tools.NewToolResult(string(jsonResult)), nil
}

// ConfigureMCPServerToolsTool enables/disables specific tools on an MCP server
type ConfigureMCPServerToolsTool struct {
	mcpManager *MCPManager
}

func (t *ConfigureMCPServerToolsTool) Name() string {
	return "configure_mcp_server_tools"
}

func (t *ConfigureMCPServerToolsTool) Description() string {
	return "Enable or disable specific tools provided by an MCP server."
}

func (t *ConfigureMCPServerToolsTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server_name": map[string]any{
				"type":        "string",
				"description": "Name of the MCP server",
			},
			"tool_name": map[string]any{
				"type":        "string",
				"description": "Name of the tool to configure",
			},
			"enabled": map[string]any{
				"type":        "boolean",
				"description": "Whether the tool should be enabled (true) or disabled (false)",
			},
		},
		"required": []string{"server_name", "tool_name", "enabled"},
	}
}

func (t *ConfigureMCPServerToolsTool) Validate(params map[string]any) error        { return nil }
func (t *ConfigureMCPServerToolsTool) IsIdempotent() bool                          { return true }
func (t *ConfigureMCPServerToolsTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ConfigureMCPServerToolsTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *ConfigureMCPServerToolsTool) RequiresPermission() []tools.Permission      { return nil }

func (t *ConfigureMCPServerToolsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	serverName, _ := params["server_name"].(string)
	toolName, _ := params["tool_name"].(string)
	enabled, _ := params["enabled"].(bool)

	if serverName == "" || toolName == "" {
		return tools.NewErrorResult(errors.New("server_name and tool_name are required")), nil
	}

	err := t.mcpManager.ConfigureTool(serverName, toolName, enabled)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("Failed to configure tool: %v", err)), nil
	}

	status := "enabled"
	if !enabled {
		status = "disabled"
	}

	return tools.NewToolResult(fmt.Sprintf("Tool '%s' on server '%s' %s successfully", toolName, serverName, status)), nil
}

// ReconnectMCPServerTool reconnects an MCP server
type ReconnectMCPServerTool struct {
	mcpManager *MCPManager
}

func (t *ReconnectMCPServerTool) Name() string {
	return "reconnect_mcp_server"
}

func (t *ReconnectMCPServerTool) Description() string {
	return "Reconnect to an MCP server. Useful if the connection was lost or the server was updated."
}

func (t *ReconnectMCPServerTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the MCP server to reconnect",
			},
		},
		"required": []string{"name"},
	}
}

func (t *ReconnectMCPServerTool) Validate(params map[string]any) error        { return nil }
func (t *ReconnectMCPServerTool) IsIdempotent() bool                          { return false }
func (t *ReconnectMCPServerTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ReconnectMCPServerTool) OptimizationHints() *tools.OptimizationHints { return nil }
func (t *ReconnectMCPServerTool) RequiresPermission() []tools.Permission      { return nil }

func (t *ReconnectMCPServerTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(errors.New("name is required")), nil
	}

	// ReconnectServer now blocks for OAuth/HTTP servers, so this will wait for the connection
	err := t.mcpManager.ReconnectServer(ctx, name)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("Failed to reconnect server: %v", err)), nil
	}

	// Check the connection status after reconnection attempt
	server := t.mcpManager.GetServer(name)
	if server == nil {
		return tools.NewErrorResult(fmt.Errorf("Server %s not found", name)), nil
	}

	if server.Connected {
		toolCount := len(server.Tools)
		return tools.NewToolResult(fmt.Sprintf("MCP server '%s' successfully reconnected with %d tools", name, toolCount)), nil
	} else {
		if server.Error != "" {
			return tools.NewErrorResult(fmt.Errorf("Failed to connect: %s", server.Error)), nil
		}
		return tools.NewToolResult(fmt.Sprintf("MCP server '%s' reconnection initiated (connecting in background)", name)), nil
	}
}

// Helper function to get tool names
func getToolNames(tools []*sdkmcp.MCPTool) []string {
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
