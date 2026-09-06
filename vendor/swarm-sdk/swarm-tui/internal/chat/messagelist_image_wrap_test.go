package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/termimage"
)

// A Kitty Unicode placeholder cell is only bound to an image by the SGR
// foreground colour that carries the image ID. The ID is emitted ONCE at the
// start of the placeholder run, and every cell after it inherits it. That makes
// the run fragile under line wrapping: if a wrap starts a new line without
// re-emitting the foreground, the cells on that line still occupy their columns
// but reference no image, and the terminal paints empty space where the picture
// should be. That is the "image is blank but the gap is there" report.
//
// These tests pin the two properties that keep placeholders bound to their
// image through the viewport's wrapping path.

// imagePlaceholderLines builds the exact line shape the chat pipeline produces
// for a tool-result image: placeholder rows, indented, on a themed background.
func imagePlaceholderLines(t *testing.T, columns, rows int, imageID, placementID uint32) []string {
	t.Helper()

	placement := termimage.Placement{
		Source:      termimage.Source{Key: "test", PNG: []byte{1}, Width: 100, Height: 50},
		Occurrence:  "occ",
		Columns:     columns,
		Rows:        rows,
		ImageID:     imageID,
		PlacementID: placementID,
	}
	raw := termimage.PlaceholderLines(placement)
	if len(raw) != rows {
		t.Fatalf("PlaceholderLines returned %d rows, want %d", len(raw), rows)
	}

	// app_chat_render_tools.go indents image rows by six columns, then
	// app_chat_render.go prefixes two more and reapplies the background.
	out := make([]string, len(raw))
	for i, line := range raw {
		out[i] = "  " + reapplyBackground("      "+line, "#1a1a1a")
	}
	return out
}

// firstPlaceholderIsBound reports whether every wrapped line that paints
// placeholder cells also sets the image-ID foreground before the first cell.
func firstPlaceholderIsBound(line string) bool {
	idx := strings.IndexRune(line, kitty.Placeholder)
	if idx < 0 {
		return true // no placeholders on this line, nothing to bind
	}
	// Look for an SGR foreground (38;...) anywhere before the first cell.
	prefix := line[:idx]
	return strings.Contains(prefix, "\x1b[38;")
}

// TestPlaceholderRunNeverExceedsViewport guards the mapping that image
// publication depends on. publishTerminalImageFrame converts a raw line index
// to a wrapped y via preWrappedMapping and then tests that y against the scroll
// window. If a wrapped line is wider than the viewport, the terminal wraps it
// again on its own, the mapping under-counts the real rows, and the placement
// can be judged off-screen and dropped from the published frame — leaving the
// cells on screen with no image behind them.
func TestPlaceholderRunNeverExceedsViewport(t *testing.T) {
	// 40 image columns plus 8 of indent cannot fit a 30-column viewport.
	lines := imagePlaceholderLines(t, 40, 2, 0x00112233, 7)

	for _, width := range []int{20, 30, 48, 80} {
		wrapped := wrapLinesToWidth(lines, width)
		for i, line := range wrapped {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: wrapped placeholder line %d is %d columns wide; "+
					"an over-wide line corrupts preWrappedMapping and the image is dropped from the frame",
					width, i, got)
			}
		}
	}
}

// TestPlaceholderKeepsImageIDAcrossWrap is the direct test for the blank-image
// report: after wrapping, every line that still paints placeholder cells must
// re-declare the image ID, or those cells are orphaned.
func TestPlaceholderKeepsImageIDAcrossWrap(t *testing.T) {
	lines := imagePlaceholderLines(t, 40, 2, 0x00112233, 7)

	for _, width := range []int{20, 30, 48} {
		wrapped := wrapLinesToWidth(lines, width)
		for i, line := range wrapped {
			if !firstPlaceholderIsBound(line) {
				t.Errorf("width %d: wrapped line %d paints placeholder cells with no image-ID foreground; "+
					"the terminal renders these as blank space\n%q",
					width, i, line)
			}
		}
	}
}

// TestPlaceholderFittingLineIsUntouched makes sure the common case — an image
// narrow enough for the viewport — passes through wrapping unchanged.
func TestPlaceholderFittingLineIsUntouched(t *testing.T) {
	lines := imagePlaceholderLines(t, 10, 2, 0x00112233, 7)
	wrapped := wrapLinesToWidth(lines, 80)

	if len(wrapped) != len(lines) {
		t.Fatalf("a fitting image was re-flowed: %d lines in, %d out", len(lines), len(wrapped))
	}
	for i := range lines {
		if wrapped[i] != lines[i] {
			t.Errorf("line %d was modified despite fitting the viewport", i)
		}
	}
}
