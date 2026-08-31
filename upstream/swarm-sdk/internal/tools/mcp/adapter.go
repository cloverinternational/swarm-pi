package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// MCPToolAdapter wraps an MCP server tool as a tools.Tool
type MCPToolAdapter struct {
	mcpTool *MCPTool
	client  *Client
}

// NewMCPToolAdapter creates a new adapter for an MCP tool
func NewMCPToolAdapter(mcpTool *MCPTool, client *Client) *MCPToolAdapter {
	return &MCPToolAdapter{
		mcpTool: mcpTool,
		client:  client,
	}
}

func (a *MCPToolAdapter) Name() string {
	return a.mcpTool.Name
}

func (a *MCPToolAdapter) Description() string {
	if a.mcpTool.Description != "" {
		return a.mcpTool.Description
	}
	return fmt.Sprintf("MCP tool: %s", a.mcpTool.Name)
}

func (a *MCPToolAdapter) Parameters() any {
	return a.mcpTool.InputSchema
}

func (a *MCPToolAdapter) Validate(params map[string]any) error {
	// TODO: Implement JSON Schema validation using official SDK
	// For now, just check if parameters is a map
	if params == nil {
		return fmt.Errorf("parameters cannot be nil")
	}
	return nil
}

func (a *MCPToolAdapter) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	start := time.Now()

	result, err := a.client.CallTool(ctx, a.mcpTool.Name, params)
	if err != nil {
		return &tools.ToolResult{
			Error:      err,
			IsError:    true,
			DurationMS: time.Since(start).Milliseconds(),
		}, err
	}

	result.DurationMS = time.Since(start).Milliseconds()
	return result, nil
}

func (a *MCPToolAdapter) IsIdempotent() bool {
	// MCP tools are not idempotent by default
	// This could be configured per tool via annotations
	return false
}

func (a *MCPToolAdapter) RequiresPermission() []tools.Permission {
	// MCP tools run in their own process/server
	// Permission is handled by the MCP server
	return []tools.Permission{}
}

func (a *MCPToolAdapter) SupportedContentTypes() []tools.ContentType {
	// MCP supports all content types
	return []tools.ContentType{
		tools.ContentTypeText,
		tools.ContentTypeImage,
		tools.ContentTypeAudio,
		tools.ContentTypePDF,
		tools.ContentTypeResource,
	}
}

func (a *MCPToolAdapter) OptimizationHints() *tools.OptimizationHints {
	// Default hints for MCP tools
	// Could be customized based on tool metadata/annotations
	return &tools.OptimizationHints{
		PreferSequential:  false,
		EstimatedDuration: 100 * time.Millisecond,
		CanBatch:          false,
		BatchSize:         0,
		Priority:          50,
		MinimalLatency:    false,
		Cacheable:         false,
		CacheTTL:          0,
	}
}

// WrapMCPTools wraps all tools from an MCP client as SDK tools
func WrapMCPTools(client *Client) []tools.Tool {
	mcpTools := client.ListTools()
	wrapped := make([]tools.Tool, 0, len(mcpTools))

	for _, mcpTool := range mcpTools {
		adapter := NewMCPToolAdapter(mcpTool, client)
		wrapped = append(wrapped, adapter)
	}

	return wrapped
}

// WrapMCPResources wraps all resources from an MCP client as readable resources
func WrapMCPResources(client *Client) []*MCPResourceWrapper {
	resources := client.Resources()
	wrapped := make([]*MCPResourceWrapper, 0, len(resources))

	for _, resource := range resources {
		wrapper := &MCPResourceWrapper{
			resource: resource,
			client:   client,
		}
		wrapped = append(wrapped, wrapper)
	}

	return wrapped
}

// MCPResourceWrapper wraps an MCP resource for reading
type MCPResourceWrapper struct {
	resource *MCPResource
	client   *Client
}

// URI returns the resource URI
func (w *MCPResourceWrapper) URI() string {
	return w.resource.URI
}

// Name returns the resource name
func (w *MCPResourceWrapper) Name() string {
	return w.resource.Name
}

// Description returns the resource description
func (w *MCPResourceWrapper) Description() string {
	return w.resource.Description
}

// MimeType returns the resource MIME type
func (w *MCPResourceWrapper) MimeType() string {
	return w.resource.MimeType
}

// Read reads the resource contents
func (w *MCPResourceWrapper) Read(ctx context.Context) (*ResourceContents, error) {
	return w.client.ReadResource(ctx, w.resource.URI)
}

// MCPPromptAdapter wraps an MCP prompt as an executable prompt
type MCPPromptAdapter struct {
	prompt *MCPPrompt
	client *Client
}

// NewMCPPromptAdapter creates a new adapter for an MCP prompt
func NewMCPPromptAdapter(prompt *MCPPrompt, client *Client) *MCPPromptAdapter {
	return &MCPPromptAdapter{
		prompt: prompt,
		client: client,
	}
}

// Name returns the prompt name
func (a *MCPPromptAdapter) Name() string {
	return a.prompt.Name
}

// Description returns the prompt description
func (a *MCPPromptAdapter) Description() string {
	return a.prompt.Description
}

// Arguments returns the prompt arguments
func (a *MCPPromptAdapter) Arguments() []PromptArgument {
	return a.prompt.Arguments
}

// Execute runs the prompt with the given arguments
func (a *MCPPromptAdapter) Execute(ctx context.Context, args map[string]string) (*PromptResult, error) {
	return a.client.PromptContent(ctx, a.prompt.Name, args)
}

// WrapMCPPrompts wraps all prompts from an MCP client
func WrapMCPPrompts(client *Client) []*MCPPromptAdapter {
	prompts := client.Prompts()
	wrapped := make([]*MCPPromptAdapter, 0, len(prompts))

	for _, prompt := range prompts {
		adapter := NewMCPPromptAdapter(prompt, client)
		wrapped = append(wrapped, adapter)
	}

	return wrapped
}
