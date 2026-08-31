package cellbuf

import (
	"fmt"
	"image/color"
	"strings"
)

// Render converts the buffer back to an ANSI-styled string.
// This matches ultraviolet's Buffer.Render method used by Crush's selectionView.
func (b *Buffer) Render() string {
	if b.height == 0 || b.width == 0 {
		return ""
	}

	var sb strings.Builder
	// Pre-allocate: roughly 4 bytes per cell average (content + styles)
	sb.Grow(b.width * b.height * 4)

	var lastStyle *Style

	for y := 0; y < b.height; y++ {
		// Add newline between lines (not before first line)
		if y > 0 {
			sb.WriteByte('\n')
		}

		for x := 0; x < b.width; {
			cell := b.CellAt(x, y)
			if cell == nil {
				x++
				continue
			}

			// Skip placeholder cells (from wide characters)
			if cell.Width == 0 {
				x++
				continue
			}

			// Emit style change if needed
			if lastStyle == nil || !stylesEqual(lastStyle, &cell.Style) {
				sb.WriteString(styleDiff(lastStyle, &cell.Style))
				lastStyle = &cell.Style
			}

			// Write content
			if cell.Content != "" {
				sb.WriteString(cell.Content)
			} else {
				sb.WriteByte(' ')
			}

			x += max(1, cell.Width)
		}
	}

	// Reset at end
	sb.WriteString("\x1b[0m")

	return sb.String()
}

// String is an alias for Render
func (b *Buffer) String() string {
	return b.Render()
}

// stylesEqual checks if two styles are equal
func stylesEqual(a, b *Style) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Bold == b.Bold &&
		a.Dim == b.Dim &&
		a.Italic == b.Italic &&
		a.Under == b.Under &&
		a.Reverse == b.Reverse &&
		a.Strike == b.Strike &&
		colorEqual(a.Fg, b.Fg) &&
		colorEqual(a.Bg, b.Bg)
}

// colorEqual checks if two colors are equal
func colorEqual(a, b color.Color) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// styleDiff generates ANSI escape codes to transition from one style to another
func styleDiff(from, to *Style) string {
	if to == nil {
		return "\x1b[0m"
	}

	// If no previous style or styles are very different, emit full reset and new style
	if from == nil {
		return styleToANSI(to)
	}

	// For simplicity, always emit full style change
	// A more optimized version would compute the minimal diff
	return styleToANSI(to)
}

// styleToANSI converts a style to ANSI escape sequence
func styleToANSI(s *Style) string {
	if s == nil {
		return "\x1b[0m"
	}

	var codes []string

	// Reset first to clear any inherited styles
	codes = append(codes, "0")

	if s.Bold {
		codes = append(codes, "1")
	}
	if s.Dim {
		codes = append(codes, "2")
	}
	if s.Italic {
		codes = append(codes, "3")
	}
	if s.Under {
		codes = append(codes, "4")
	}
	if s.Reverse {
		codes = append(codes, "7")
	}
	if s.Strike {
		codes = append(codes, "9")
	}

	if s.Fg != nil {
		codes = append(codes, fgColorCode(s.Fg))
	}
	if s.Bg != nil {
		codes = append(codes, bgColorCode(s.Bg))
	}

	if len(codes) == 0 {
		return ""
	}

	return fmt.Sprintf("\x1b[%sm", strings.Join(codes, ";"))
}

// fgColorCode returns the ANSI foreground color code for a color
func fgColorCode(c color.Color) string {
	if c == nil {
		return "39"
	}

	r, g, b, _ := c.RGBA()
	// Convert from 16-bit to 8-bit
	r8 := uint8(r >> 8)
	g8 := uint8(g >> 8)
	b8 := uint8(b >> 8)

	return fmt.Sprintf("38;2;%d;%d;%d", r8, g8, b8)
}

// bgColorCode returns the ANSI background color code for a color
func bgColorCode(c color.Color) string {
	if c == nil {
		return "49"
	}

	r, g, b, _ := c.RGBA()
	// Convert from 16-bit to 8-bit
	r8 := uint8(r >> 8)
	g8 := uint8(g >> 8)
	b8 := uint8(b >> 8)

	return fmt.Sprintf("48;2;%d;%d;%d", r8, g8, b8)
}
