package rendering

import (
	"github.com/charmbracelet/lipgloss"
)

// CreateRenderable creates the appropriate Renderable based on content type
func CreateRenderable(content string, contentType ContentType, style lipgloss.Style, options RenderOptions) Renderable {
	switch contentType {
	case ContentTypeANSI:
		return NewANSIRenderable(content, options)
	case ContentTypePlainText:
		return NewPlainTextRenderable(content, style, options)
	// TODO: Add more renderable types as we implement them
	// case ContentTypeCode:
	//     return NewCodeRenderable(content, options)
	// case ContentTypeJSON:
	//     return NewJSONRenderable(content, options)
	// case ContentTypeDiff:
	//     return NewDiffRenderable(content, options)
	default:
		// Fallback to plain text
		return NewPlainTextRenderable(content, style, options)
	}
}

// AutoDetectAndRender is a convenience function that detects content type and renders
func AutoDetectAndRender(content string, toolName string, metadata map[string]any,
	style lipgloss.Style, options RenderOptions) []string {

	contentType := DetectContentType(content, toolName, metadata)
	renderable := CreateRenderable(content, contentType, style, options)
	return renderable.Render(options.Width, options.MaxLines)
}
