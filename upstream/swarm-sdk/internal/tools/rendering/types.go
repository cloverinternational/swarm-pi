package rendering

import "github.com/charmbracelet/lipgloss"

// ContentType represents the type of content for rendering
type ContentType int

const (
	ContentTypePlainText ContentType = iota
	ContentTypeANSI                  // Contains ANSI escape codes
	ContentTypeCode                  // Source code with syntax highlighting
	ContentTypeJSON                  // JSON data
	ContentTypeYAML                  // YAML data
	ContentTypeXML                   // XML data
	ContentTypeDiff                  // Diff output
	ContentTypeMarkdown              // Markdown text
	ContentTypeHTML                  // HTML content
)

// Renderable represents content that can be rendered with styling
type Renderable interface {
	// Render the content with given width constraints
	Render(width int, maxLines int) []string

	// GetStyle returns the preferred style for this content
	Style() lipgloss.Style

	// IsCollapsible indicates if this content supports collapsing
	IsCollapsible() bool

	// GetContentType returns the type of content
	ContentType() ContentType
}

// RenderOptions contains options for rendering content
type RenderOptions struct {
	Width            int
	MaxLines         int
	PreserveColors   bool
	ShowLineNumbers  bool
	WrapLines        bool
	HighlightMatches bool
	Theme            string
}

// DefaultRenderOptions returns sensible defaults
func DefaultRenderOptions() RenderOptions {
	return RenderOptions{
		Width:           80,
		MaxLines:        100,
		PreserveColors:  true,
		ShowLineNumbers: false,
		WrapLines:       true,
	}
}
