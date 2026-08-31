package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// renderChatContent used to wrap the already-rendered, already-width-correct
// cached separator in a SECOND lipgloss Style with Width(a.width) on every
// frame. That re-Render was 16% of CPU during scrolling. This asserts the
// wrapper was a genuine no-op, so dropping it cannot change what is drawn.
func TestSeparatorReRenderIsNoOp(t *testing.T) {
	for _, width := range []int{1, 2, 20, 80, 120, 200} {
		// Exactly how app_chat_render.go builds a.cachedSeparator.rendered.
		cached := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5FAFFF")).
			Width(width).
			Render(strings.Repeat("─", width))

		// The wrapper that was removed.
		reRendered := lipgloss.NewStyle().Width(width).Render(cached)

		if cached != reRendered {
			t.Errorf("width=%d: re-Render is NOT a no-op\n cached: %q\n re-ren: %q",
				width, cached, reRendered)
		}
	}
}

// countLines(topSeparatorRendered) feeds a.inputOverlayY, which positions the
// input overlay. Line count must be identical or the overlay shifts.
func TestSeparatorLineCountUnchanged(t *testing.T) {
	for _, width := range []int{1, 40, 80, 200} {
		cached := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5FAFFF")).
			Width(width).
			Render(strings.Repeat("─", width))
		reRendered := lipgloss.NewStyle().Width(width).Render(cached)

		got := strings.Count(cached, "\n") + 1
		want := strings.Count(reRendered, "\n") + 1
		if got != want {
			t.Errorf("width=%d: line count drift, cached=%d re-rendered=%d", width, got, want)
		}
	}
}
