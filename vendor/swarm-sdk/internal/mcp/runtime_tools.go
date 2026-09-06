package mcp

import (
	"context"
	"fmt"
	"regexp"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

type mcpToolWrapper struct {
	inner      tools.Tool
	uniqueName string
	serverName string
}

func (w *mcpToolWrapper) Name() string {
	return w.uniqueName
}

func (w *mcpToolWrapper) Description() string {
	return w.inner.Description()
}

func (w *mcpToolWrapper) Parameters() any {
	return w.inner.Parameters()
}

func (w *mcpToolWrapper) Validate(params map[string]any) error {
	if vt, ok := w.inner.(tools.ValidatableTool); ok {
		return vt.Validate(params)
	}
	return nil
}

func (w *mcpToolWrapper) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	return w.inner.Execute(ctx, params)
}

func (w *mcpToolWrapper) IsIdempotent() bool {
	if it, ok := w.inner.(tools.IdempotentTool); ok {
		return it.IsIdempotent()
	}
	return false
}

func (w *mcpToolWrapper) RequiresPermission() []tools.Permission {
	if pt, ok := w.inner.(tools.PermissionedTool); ok {
		return pt.RequiresPermission()
	}
	return nil
}

func (w *mcpToolWrapper) SupportedContentTypes() []tools.ContentType {
	if ct, ok := w.inner.(tools.ContentTypedTool); ok {
		return ct.SupportedContentTypes()
	}
	return nil
}

func (w *mcpToolWrapper) OptimizationHints() *tools.OptimizationHints {
	if ht, ok := w.inner.(tools.HintedTool); ok {
		return ht.OptimizationHints()
	}
	return nil
}

func (w *mcpToolWrapper) ToolMetadata() *tools.ToolMetadata {
	return &tools.ToolMetadata{
		Source:     tools.ToolSourceMCP,
		ServerName: w.serverName,
		Category:   "external",
	}
}

var toolNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeToolName(name string) string {
	sanitized := toolNameSanitizer.ReplaceAllString(name, "_")
	if len(sanitized) > 128 {
		sanitized = sanitized[:128]
	}
	if sanitized == "" {
		sanitized = "tool"
	}
	return sanitized
}

func (w *mcpToolWrapper) String() string {
	return fmt.Sprintf("%s (%s)", w.uniqueName, w.serverName)
}
