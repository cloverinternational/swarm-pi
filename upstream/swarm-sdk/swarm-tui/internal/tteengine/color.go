// Source: terminaltexteffects/utils/graphics.py
// Covers: Color, ColorPair, Gradient (with Direction + BuildCoordinateColorMapping)
package tteengine

import (
	"fmt"
	"math"
	"strings"
)

// ---------------------------------------------------------------------------
// Color
// ---------------------------------------------------------------------------

// Color is an RGB terminal colour.  It can be constructed from a 6-hex-digit
// string (with or without a leading '#') or from individual R/G/B bytes.
type Color struct {
	R, G, B uint8
}

// Common named colours — mirrors the Python engine's palette.
var (
	ColorWhite   = Color{255, 255, 255}
	ColorBlack   = Color{0, 0, 0}
	ColorRed     = Color{255, 0, 0}
	ColorGreen   = Color{0, 255, 0}
	ColorBlue    = Color{0, 0, 255}
	ColorYellow  = Color{255, 255, 0}
	ColorCyan    = Color{0, 255, 255}
	ColorMagenta = Color{255, 0, 255}
	ColorOrange  = Color{255, 165, 0}
	ColorPurple  = Color{128, 0, 128}
)

// NewColor constructs a Color from individual R/G/B byte values.
func NewColor(r, g, b uint8) Color { return Color{r, g, b} }

// MustColorFromHex parses a hex string and panics on error.  Useful for
// compile-time constant colours.
func MustColorFromHex(hex string) Color {
	c, err := ColorFromHex(hex)
	if err != nil {
		panic(err)
	}
	return c
}

// ColorFromHex parses a 6-digit RGB hex string ("#rrggbb" or "rrggbb").
func ColorFromHex(hex string) (Color, error) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return Color{}, fmt.Errorf("tteengine: invalid hex colour %q (want 6 hex digits)", hex)
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b); err != nil {
		return Color{}, fmt.Errorf("tteengine: parse hex colour %q: %w", hex, err)
	}
	return Color{r, g, b}, nil
}

// Hex returns the colour as a lowercase 6-digit hex string without '#'.
func (c Color) Hex() string { return fmt.Sprintf("%02x%02x%02x", c.R, c.G, c.B) }

// ANSI returns the 24-bit ANSI foreground escape sequence for text.
// Source: colorterm.fg
func (c Color) ANSI(text string) string {
	return fmt.Sprintf("\033[38;2;%d;%d;%dm%s\033[0m", c.R, c.G, c.B, text)
}

// ANSIBg returns the 24-bit ANSI background escape sequence for text.
func (c Color) ANSIBg(text string) string {
	return fmt.Sprintf("\033[48;2;%d;%d;%dm%s\033[0m", c.R, c.G, c.B, text)
}

// Lerp linearly interpolates between c and other by t ∈ [0,1].
func (c Color) Lerp(other Color, t float64) Color {
	if t <= 0 {
		return c
	}
	if t >= 1 {
		return other
	}
	lerp := func(a, b uint8) uint8 {
		return uint8(float64(a) + float64(int(b)-int(a))*t)
	}
	return Color{lerp(c.R, other.R), lerp(c.G, other.G), lerp(c.B, other.B)}
}

// AdjustBrightness multiplies the HSL lightness of c by factor and returns the
// resulting colour.  Values >1 brighten, <1 darken.
// Source: Animation.adjust_color_brightness in Python engine.
func (c Color) AdjustBrightness(factor float64) Color {
	h, s, l := c.toHSL()
	l = math.Max(0, math.Min(1, l*factor))
	return fromHSL(h, s, l)
}

func (c Color) toHSL() (h, s, l float64) {
	r := float64(c.R) / 255
	g := float64(c.G) / 255
	b := float64(c.B) / 255
	mx := math.Max(math.Max(r, g), b)
	mn := math.Min(math.Min(r, g), b)
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	case b:
		h = (r-g)/d + 4
	}
	h /= 6
	return h * 360, s, l
}

func fromHSL(h, s, l float64) Color {
	h /= 360
	if s == 0 {
		v := uint8(l * 255)
		return Color{v, v, v}
	}
	hue2rgb := func(p, q, t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		default:
			return p
		}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	return Color{
		R: uint8(hue2rgb(p, q, h+1.0/3) * 255),
		G: uint8(hue2rgb(p, q, h) * 255),
		B: uint8(hue2rgb(p, q, h-1.0/3) * 255),
	}
}

// ---------------------------------------------------------------------------
// ColorPair — foreground + background colour together
// Source: graphics.py — ColorPair
// ---------------------------------------------------------------------------

// ColorPair holds an optional foreground and an optional background colour.
// nil pointer means "no colour" (terminal default).
type ColorPair struct {
	FG *Color
	BG *Color
}

// NewColorPair creates a ColorPair.  Pass nil for either field to leave it unset.
func NewColorPair(fg, bg *Color) ColorPair { return ColorPair{FG: fg, BG: bg} }

// FGOnly returns a ColorPair with only a foreground colour set.
func FGOnly(fg Color) ColorPair { return ColorPair{FG: &fg} }

// BGOnly returns a ColorPair with only a background colour set.
func BGOnly(bg Color) ColorPair { return ColorPair{BG: &bg} }

// ---------------------------------------------------------------------------
// GradientDirection
// Source: graphics.py — Gradient.Direction
// ---------------------------------------------------------------------------

// GradientDirection controls how a Gradient is mapped to canvas coordinates.
type GradientDirection int

const (
	GradientVertical   GradientDirection = iota // same colour per row
	GradientHorizontal                          // same colour per column
	GradientDiagonal                            // row+column combined
	GradientRadial                              // distance from centre
)

// ---------------------------------------------------------------------------
// Gradient
// Source: graphics.py — Gradient
// ---------------------------------------------------------------------------

// Gradient is a list of Colors linearly interpolated between a set of stops.
// The Spectrum slice can be iterated directly.
type Gradient struct {
	Spectrum []Color
}

// NewGradient builds a gradient that walks through stops in order.
// steps can be a single int (applied between every pair) or one int per gap.
// Source: Gradient.__init__ + Gradient._generate
func NewGradient(stops []Color, steps []int) *Gradient {
	if len(stops) == 0 {
		return &Gradient{}
	}
	if len(stops) == 1 {
		n := 1
		if len(steps) > 0 {
			n = steps[0]
		}
		spec := make([]Color, n)
		for i := range spec {
			spec[i] = stops[0]
		}
		return &Gradient{Spectrum: spec}
	}

	// Normalise steps slice to len(stops)-1
	pairs := len(stops) - 1
	normalised := make([]int, pairs)
	for i := range normalised {
		if i < len(steps) {
			normalised[i] = steps[i]
		} else if len(steps) > 0 {
			normalised[i] = steps[len(steps)-1]
		} else {
			normalised[i] = 1
		}
		if normalised[i] < 1 {
			normalised[i] = 1
		}
	}

	spectrum := make([]Color, 0)
	for i := range pairs {
		start := stops[i]
		end := stops[i+1]
		n := normalised[i]
		from := 0
		if i > 0 {
			from = 1 // skip first to avoid duplicating the shared stop
		}
		for step := from; step <= n; step++ {
			t := float64(step) / float64(n)
			spectrum = append(spectrum, start.Lerp(end, t))
		}
	}
	return &Gradient{Spectrum: spectrum}
}

// At returns the colour at position t ∈ [0,1] along the gradient.
func (g *Gradient) At(t float64) Color {
	if len(g.Spectrum) == 0 {
		return ColorWhite
	}
	if t <= 0 {
		return g.Spectrum[0]
	}
	if t >= 1 {
		return g.Spectrum[len(g.Spectrum)-1]
	}
	idx := int(t * float64(len(g.Spectrum)-1))
	return g.Spectrum[idx]
}

// BuildCoordinateColorMapping returns a map of every Coord in the rectangle
// [minCol..maxCol] × [minRow..maxRow] to its gradient colour based on direction.
// Source: graphics.py — Gradient.build_coordinate_color_mapping
func (g *Gradient) BuildCoordinateColorMapping(
	minRow, maxRow, minCol, maxCol int,
	direction GradientDirection,
) map[Coord]Color {
	out := make(map[Coord]Color, (maxCol-minCol+1)*(maxRow-minRow+1))
	rowOff := minRow - 1
	colOff := minCol - 1

	for row := minRow; row <= maxRow; row++ {
		for col := minCol; col <= maxCol; col++ {
			var t float64
			switch direction {
			case GradientVertical:
				t = float64(row-rowOff) / float64(maxRow-rowOff)
			case GradientHorizontal:
				t = float64(col-colOff) / float64(maxCol-colOff)
			case GradientDiagonal:
				t = float64((row-rowOff)*2+(col-colOff)) /
					float64((maxRow-rowOff)*2+(maxCol-colOff))
			case GradientRadial:
				t = FindNormalizedDistanceFromCenter(minRow, maxRow, minCol, maxCol,
					Coord{Column: col, Row: row})
			}
			out[Coord{Column: col, Row: row}] = g.At(t)
		}
	}
	return out
}
