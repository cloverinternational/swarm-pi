package rendering

import "github.com/charmbracelet/lipgloss"

// RenderableHTML wraps raw HTML content. Rendering is a no-op — the content
// is served as-is to browser clients. Terminal consumers should not use this.
type RenderableHTML struct {
	Content string
}

func (r RenderableHTML) Render(width int, maxLines int) []string {
	return []string{r.Content}
}

func (r RenderableHTML) Style() lipgloss.Style { return lipgloss.NewStyle() }

func (r RenderableHTML) IsCollapsible() bool { return false }

func (r RenderableHTML) ContentType() ContentType { return ContentTypeHTML }
