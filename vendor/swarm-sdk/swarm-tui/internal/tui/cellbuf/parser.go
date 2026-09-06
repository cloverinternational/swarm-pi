package cellbuf

import (
	"image/color"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// StyledString is a string that can be decomposed into a series of styled
// cells. It is used to disassemble a rendered string with ANSI escape codes
// into a series of cells that can be used in a Buffer.
// This matches ultraviolet's StyledString.
type StyledString struct {
	Text string
}

// NewStyledString creates a new StyledString from an ANSI-styled string
func NewStyledString(str string) *StyledString {
	return &StyledString{Text: str}
}

// Draw parses the styled string and draws it into the buffer at the given area.
// This matches ultraviolet's StyledString.Draw method used by Crush.
func (s *StyledString) Draw(buf *Buffer, area Rectangle) {
	if s.Text == "" || buf == nil {
		return
	}

	// Current position in buffer
	x := area.Min.X
	y := area.Min.Y

	// Current style state
	var currentStyle Style

	// Parse the string
	p := ansi.GetParser()
	defer ansi.PutParser(p)

	var state byte // the initial state is always zero (NormalState)
	data := []byte(s.Text)
	for len(data) > 0 {
		seq, width, n, newState := ansi.DecodeSequence(data, state, p)
		if n == 0 {
			break
		}

		state = newState

		switch {
		case ansi.HasCsiPrefix(seq):
			// CSI sequence - likely SGR (style)
			if len(seq) > 2 && seq[len(seq)-1] == 'm' {
				// SGR sequence - parse parameters
				parseCSI(seq, &currentStyle)
			}

		case ansi.HasOscPrefix(seq):
			// OSC sequence - ignore for now (hyperlinks, etc)

		case seq[0] == '\n':
			// Newline
			y++
			x = area.Min.X

		case seq[0] == '\r':
			// Carriage return
			x = area.Min.X

		case seq[0] == '\t':
			// Tab - advance to next tab stop (every 8 columns)
			tabStop := ((x-area.Min.X)/8 + 1) * 8
			x = area.Min.X + tabStop

		case seq[0] < 0x20:
			// Other control characters - ignore

		default:
			// Regular text - process grapheme clusters
			text := string(seq)
			gr := uniseg.NewGraphemes(text)
			for gr.Next() {
				cluster := gr.Str()
				clusterWidth := gr.Width()

				// Skip if out of bounds
				if y >= area.Max.Y {
					return
				}
				if x >= area.Max.X {
					// Move to next line if beyond right edge
					continue
				}

				// Create cell
				cell := Cell{
					Content: cluster,
					Width:   clusterWidth,
					Style:   currentStyle,
				}

				// Set cell in buffer
				buf.SetCell(x, y, &cell)

				// For wide characters, set placeholder in next cell
				if clusterWidth > 1 && x+1 < area.Max.X {
					placeholder := Cell{
						Content: "",
						Width:   0,
						Style:   currentStyle,
					}
					buf.SetCell(x+1, y, &placeholder)
				}

				x += clusterWidth
				_ = width // unused but from decoder
			}
		}

		data = data[n:]
	}
}

// parseCSI parses a CSI sequence and updates the style
func parseCSI(seq []byte, style *Style) {
	// Skip CSI prefix (ESC [) and final byte (m)
	if len(seq) < 3 {
		return
	}

	params := seq[2 : len(seq)-1] // Skip "ESC[" and "m"
	if len(params) == 0 {
		// ESC[m is reset
		*style = Style{}
		return
	}

	// Parse semicolon-separated parameters
	values := parseParams(params)

	for i := 0; i < len(values); i++ {
		v := values[i]
		switch v {
		case 0: // Reset
			*style = Style{}
		case 1: // Bold
			style.Bold = true
		case 2: // Dim
			style.Dim = true
		case 3: // Italic
			style.Italic = true
		case 4: // Underline
			style.Under = true
		case 7: // Reverse
			style.Reverse = true
		case 9: // Strikethrough
			style.Strike = true
		case 21, 22: // Not bold/dim
			style.Bold = false
			style.Dim = false
		case 23: // Not italic
			style.Italic = false
		case 24: // Not underline
			style.Under = false
		case 27: // Not reverse
			style.Reverse = false
		case 29: // Not strikethrough
			style.Strike = false
		case 30, 31, 32, 33, 34, 35, 36, 37: // Standard foreground
			style.Fg = ansiColor(v - 30)
		case 38: // Extended foreground
			if i+1 < len(values) {
				switch values[i+1] {
				case 5: // 256 color
					if i+2 < len(values) {
						style.Fg = ansi256Color(values[i+2])
						i += 2
					}
				case 2: // True color RGB
					if i+4 < len(values) {
						style.Fg = color.RGBA{
							R: uint8(values[i+2]),
							G: uint8(values[i+3]),
							B: uint8(values[i+4]),
							A: 255,
						}
						i += 4
					}
				}
			}
		case 39: // Default foreground
			style.Fg = nil
		case 40, 41, 42, 43, 44, 45, 46, 47: // Standard background
			style.Bg = ansiColor(v - 40)
		case 48: // Extended background
			if i+1 < len(values) {
				switch values[i+1] {
				case 5: // 256 color
					if i+2 < len(values) {
						style.Bg = ansi256Color(values[i+2])
						i += 2
					}
				case 2: // True color RGB
					if i+4 < len(values) {
						style.Bg = color.RGBA{
							R: uint8(values[i+2]),
							G: uint8(values[i+3]),
							B: uint8(values[i+4]),
							A: 255,
						}
						i += 4
					}
				}
			}
		case 49: // Default background
			style.Bg = nil
		case 90, 91, 92, 93, 94, 95, 96, 97: // Bright foreground
			style.Fg = ansiBrightColor(v - 90)
		case 100, 101, 102, 103, 104, 105, 106, 107: // Bright background
			style.Bg = ansiBrightColor(v - 100)
		}
	}
}

// parseParams parses semicolon-separated integer parameters
func parseParams(data []byte) []int {
	if len(data) == 0 {
		return nil
	}

	// Count semicolons to pre-allocate
	count := 1
	for _, b := range data {
		if b == ';' {
			count++
		}
	}

	result := make([]int, 0, count)
	current := 0
	hasDigit := false

	for _, b := range data {
		switch {
		case b >= '0' && b <= '9':
			current = current*10 + int(b-'0')
			hasDigit = true
		case b == ';' || b == ':':
			if hasDigit {
				result = append(result, current)
			} else {
				result = append(result, 0)
			}
			current = 0
			hasDigit = false
		}
	}

	// Append last value
	if hasDigit {
		result = append(result, current)
	} else if len(data) > 0 && data[len(data)-1] == ';' {
		result = append(result, 0)
	}

	return result
}

// Standard ANSI colors (0-7)
var ansiColors = []color.RGBA{
	{R: 0, G: 0, B: 0, A: 255},       // 0: Black
	{R: 128, G: 0, B: 0, A: 255},     // 1: Red
	{R: 0, G: 128, B: 0, A: 255},     // 2: Green
	{R: 128, G: 128, B: 0, A: 255},   // 3: Yellow
	{R: 0, G: 0, B: 128, A: 255},     // 4: Blue
	{R: 128, G: 0, B: 128, A: 255},   // 5: Magenta
	{R: 0, G: 128, B: 128, A: 255},   // 6: Cyan
	{R: 192, G: 192, B: 192, A: 255}, // 7: White
}

// Bright ANSI colors (8-15)
var ansiBrightColors = []color.RGBA{
	{R: 128, G: 128, B: 128, A: 255}, // 8: Bright Black (Gray)
	{R: 255, G: 0, B: 0, A: 255},     // 9: Bright Red
	{R: 0, G: 255, B: 0, A: 255},     // 10: Bright Green
	{R: 255, G: 255, B: 0, A: 255},   // 11: Bright Yellow
	{R: 0, G: 0, B: 255, A: 255},     // 12: Bright Blue
	{R: 255, G: 0, B: 255, A: 255},   // 13: Bright Magenta
	{R: 0, G: 255, B: 255, A: 255},   // 14: Bright Cyan
	{R: 255, G: 255, B: 255, A: 255}, // 15: Bright White
}

func ansiColor(n int) color.Color {
	if n >= 0 && n < len(ansiColors) {
		return ansiColors[n]
	}
	return nil
}

func ansiBrightColor(n int) color.Color {
	if n >= 0 && n < len(ansiBrightColors) {
		return ansiBrightColors[n]
	}
	return nil
}

// ansi256Color returns a color for 256-color palette index
func ansi256Color(n int) color.Color {
	if n < 0 || n > 255 {
		return nil
	}

	// Standard colors (0-15)
	if n < 8 {
		return ansiColors[n]
	}
	if n < 16 {
		return ansiBrightColors[n-8]
	}

	// 216 color cube (16-231)
	if n < 232 {
		n -= 16
		r := (n / 36) * 51
		g := ((n / 6) % 6) * 51
		b := (n % 6) * 51
		return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
	}

	// Grayscale (232-255)
	gray := uint8((n-232)*10 + 8)
	return color.RGBA{R: gray, G: gray, B: gray, A: 255}
}
