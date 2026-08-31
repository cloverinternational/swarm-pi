package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	mcpsdk "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
)

const (
	resourceListToolName = "resources_list"
	resourceReadToolName = "resources_read"

	maxResourceListEntries = 50
	maxBlobPreviewChars    = 64
)

type resourceFetcher interface {
	ListResources(ctx context.Context, serverName, cursor string, limit int) ([]*mcpsdk.MCPResource, string, error)
	ReadResource(ctx context.Context, serverName, uri string) (*mcpsdk.ResourceContents, error)
}

type mcpResourcesListTool struct {
	serverName string
	uniqueName string
	fetcher    resourceFetcher
}

type mcpResourcesReadTool struct {
	serverName string
	uniqueName string
	fetcher    resourceFetcher
}

func newMCPResourcesListTool(serverName string, fetcher resourceFetcher) *mcpResourcesListTool {
	return &mcpResourcesListTool{
		serverName: serverName,
		uniqueName: uniqueToolName(serverName, resourceListToolName),
		fetcher:    fetcher,
	}
}

func newMCPResourcesReadTool(serverName string, fetcher resourceFetcher) *mcpResourcesReadTool {
	return &mcpResourcesReadTool{
		serverName: serverName,
		uniqueName: uniqueToolName(serverName, resourceReadToolName),
		fetcher:    fetcher,
	}
}

func (m *RuntimeManager) registerResourceTools(ctx context.Context, state *ServerState) {
	if m.registry == nil {
		return
	}
	state.mu.RLock()
	client := state.Client
	state.mu.RUnlock()
	if client == nil {
		return
	}

	resourceTools := []struct {
		name string
		tool tools.Tool
	}{
		{
			name: resourceListToolName,
			tool: newMCPResourcesListTool(state.Config.Name, m),
		},
		{
			name: resourceReadToolName,
			tool: newMCPResourcesReadTool(state.Config.Name, m),
		},
	}

	for _, entry := range resourceTools {
		if err := m.registry.Register(entry.tool); err != nil {
			if m.logger != nil {
				m.logger.Warn(ctx, "mcp.resource_tool_register_failed",
					observability.F("tool", entry.tool.Name()),
					observability.F("error", err.Error()))
			}
			continue
		}
		state.mu.Lock()
		state.Tools[entry.name] = &ToolState{
			Name:         entry.name,
			RegistryName: entry.tool.Name(),
			Enabled:      true,
		}
		state.toolMap[entry.name] = entry.tool.Name()
		state.mu.Unlock()
	}
}

func (t *mcpResourcesListTool) Name() string {
	return t.uniqueName
}

func (t *mcpResourcesListTool) Description() string {
	return "List resources exposed by an MCP server. Returns a compact summary and resource links for later reads."
}

func (t *mcpResourcesListTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cursor": map[string]any{
				"type":        "string",
				"description": "Optional cursor for pagination.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     1000,
				"description": "Optional limit for pagination.",
			},
		},
		"additionalProperties": false,
	}
}

func (t *mcpResourcesListTool) Validate(params map[string]any) error {
	if params == nil {
		return nil
	}
	for key, value := range params {
		switch key {
		case "cursor":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("cursor must be a string")
			}
		case "limit":
			switch limit := value.(type) {
			case int:
				if limit < 1 {
					return fmt.Errorf("limit must be at least 1")
				}
			case float64:
				if limit < 1 {
					return fmt.Errorf("limit must be at least 1")
				}
			default:
				return fmt.Errorf("limit must be a number")
			}
		default:
			return fmt.Errorf("unknown parameter: %s", key)
		}
	}
	return nil
}

func (t *mcpResourcesListTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if err := t.Validate(params); err != nil {
		return tools.NewErrorResult(err), nil
	}

	cursor := ""
	limit := 0
	if params != nil {
		if value, ok := params["cursor"]; ok {
			cursor, _ = value.(string)
		}
		if value, ok := params["limit"]; ok {
			switch v := value.(type) {
			case int:
				limit = v
			case float64:
				limit = int(v)
			}
		}
	}

	resources, nextCursor, err := t.fetcher.ListResources(ctx, t.serverName, cursor, limit)
	if err != nil {
		return tools.NewErrorResult(err), nil
	}

	if len(resources) == 0 {
		return tools.NewToolResult(fmt.Sprintf("No resources available from %s.", t.serverName)), nil
	}

	builder := &strings.Builder{}
	fmt.Fprintf(builder, "Resources from %s (%d resources):", t.serverName, len(resources))

	count := min(len(resources), maxResourceListEntries)
	for i := range count {
		resource := resources[i]
		if resource.Name != "" {
			fmt.Fprintf(builder, "\n- %s (%s)", resource.Name, resource.URI)
		} else {
			fmt.Fprintf(builder, "\n- %s", resource.URI)
		}
	}
	if len(resources) > count {
		fmt.Fprintf(builder, "\n... and %d more", len(resources)-count)
	}
	if nextCursor != "" {
		fmt.Fprintf(builder, "\nNext cursor: %s", nextCursor)
	}

	result := tools.NewToolResult(builder.String())
	for i := range count {
		resource := resources[i]
		result.AddResource(resource.URI, resource.Name, resource.Description)
	}
	if nextCursor != "" {
		result.WithMetadata("next_cursor", nextCursor)
	}

	return result, nil
}

func (t *mcpResourcesListTool) IsIdempotent() bool {
	return false
}

func (t *mcpResourcesListTool) RequiresPermission() []tools.Permission {
	return nil
}

func (t *mcpResourcesListTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{
		tools.ContentTypeText,
		tools.ContentTypeResource,
	}
}

func (t *mcpResourcesListTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

func (t *mcpResourcesListTool) ToolMetadata() *tools.ToolMetadata {
	return &tools.ToolMetadata{
		Source:     tools.ToolSourceMCP,
		ServerName: t.serverName,
		Category:   "external",
	}
}

func (t *mcpResourcesReadTool) Name() string {
	return t.uniqueName
}

func (t *mcpResourcesReadTool) Description() string {
	return "Read a resource from an MCP server by URI. Returns text for text resources or a resource reference for binary data."
}

func (t *mcpResourcesReadTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"uri": map[string]any{
				"type":        "string",
				"description": "Resource URI to read.",
			},
		},
		"required":             []string{"uri"},
		"additionalProperties": false,
	}
}

func (t *mcpResourcesReadTool) Validate(params map[string]any) error {
	if params == nil {
		return fmt.Errorf("uri parameter is required")
	}
	for key := range params {
		if key != "uri" {
			return fmt.Errorf("unknown parameter: %s", key)
		}
	}
	uriValue, ok := params["uri"]
	if !ok {
		return fmt.Errorf("uri parameter is required")
	}
	uri, ok := uriValue.(string)
	if !ok || uri == "" {
		return fmt.Errorf("uri must be a non-empty string")
	}
	return nil
}

func (t *mcpResourcesReadTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if err := t.Validate(params); err != nil {
		return tools.NewErrorResult(err), nil
	}

	uri := params["uri"].(string)
	resource, err := t.fetcher.ReadResource(ctx, t.serverName, uri)
	if err != nil {
		return tools.NewErrorResult(err), nil
	}

	if resource == nil {
		return tools.NewToolResult("Resource contents are empty."), nil
	}

	if resource.Blob != "" {
		preview := resource.Blob
		if len(preview) > maxBlobPreviewChars {
			preview = preview[:maxBlobPreviewChars] + "..."
		}
		summary := fmt.Sprintf(
			"Binary resource %s (%s). Base64 length: %d. Preview: %s",
			resource.URI,
			resource.MimeType,
			len(resource.Blob),
			preview,
		)
		result := tools.NewToolResult(summary)
		result.AddResource(resource.URI, resourceDisplayName(resource.URI), "Binary MCP resource")
		return result, nil
	}

	return tools.NewToolResult(resource.Text), nil
}

func (t *mcpResourcesReadTool) IsIdempotent() bool {
	return false
}

func (t *mcpResourcesReadTool) RequiresPermission() []tools.Permission {
	return nil
}

func (t *mcpResourcesReadTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{
		tools.ContentTypeText,
		tools.ContentTypeResource,
	}
}

func (t *mcpResourcesReadTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

func (t *mcpResourcesReadTool) ToolMetadata() *tools.ToolMetadata {
	return &tools.ToolMetadata{
		Source:     tools.ToolSourceMCP,
		ServerName: t.serverName,
		Category:   "external",
	}
}

func resourceDisplayName(uri string) string {
	if uri == "" {
		return "resource"
	}
	trimmed := strings.TrimSuffix(uri, "/")
	if trimmed == "" {
		return uri
	}
	lastSlash := strings.LastIndex(trimmed, "/")
	if lastSlash == -1 {
		return trimmed
	}
	return trimmed[lastSlash+1:]
}
