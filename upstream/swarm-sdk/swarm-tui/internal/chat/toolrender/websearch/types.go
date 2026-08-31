package websearch

import (
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// WebSearchResult stores pre-processed web search tool output.
// It implements toolrender.CachedResult so it can be cached on a message and
// re-used across render cycles without re-parsing JSON.
type WebSearchResult struct {
	Lines       []string // Pre-formatted styled lines for rendering
	RawContent  string   // Original raw JSON content (for re-rendering at different widths)
	RenderWidth int      // Width the content was rendered for
	Language    i18n.Language
}

// Compile-time interface check.
var _ toolrender.CachedResult = (*WebSearchResult)(nil)

// GetRenderedLines returns the pre-styled lines for rendering.
func (r *WebSearchResult) GetRenderedLines() []string {
	if r == nil || len(r.Lines) == 0 {
		width := 0
		if r != nil {
			width = r.RenderWidth
		}
		// The placeholder is a fixed i18n string wider than a narrow pane.
		return []string{shared.FitLine("    \x1b[38;2;147;153;178m\u23bf\x1b[0m \x1b[38;2;140;140;140m"+i18n.T("toolrender.websearch.no_results")+"\x1b[0m", width)}
	}
	return r.Lines
}

// NeedsRerender returns true if the result needs to be re-rendered at the given width.
func (r *WebSearchResult) NeedsRerender(width int) bool {
	if r == nil {
		return true
	}
	return r.RenderWidth != width || r.Language != i18n.CurrentLanguage()
}
