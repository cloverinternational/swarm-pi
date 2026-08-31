// Package tteengine is a Go port of the Python TerminalTextEffects engine,
// designed to integrate with bubbletea for use in swarm-tui.
//
// Source: terminaltexteffects/utils/geometry.py
//
// Coord uses (Column, Row) — Column is the X axis (1=left), Row is the Y axis
// (1=bottom).  This matches the Python engine's convention.  In the terminal
// output buffer rows are rendered top-to-bottom, so Row=1 is the lowest line.
package tteengine

import (
	"math"
)

// ---------------------------------------------------------------------------
// Coord — integer grid position (Column=X, Row=Y)
// ---------------------------------------------------------------------------

// Coord is an immutable integer 2-D position on the canvas.
// Column maps to the horizontal axis, Row to the vertical axis.
// Row 1 is the bottom of the canvas; higher row numbers are higher up.
type Coord struct {
	Column int
	Row    int
}

// Equal reports whether two Coords are identical.
func (c Coord) Equal(other Coord) bool {
	return c.Column == other.Column && c.Row == other.Row
}

// Add returns c + other.
func (c Coord) Add(other Coord) Coord {
	return Coord{Column: c.Column + other.Column, Row: c.Row + other.Row}
}

// Sub returns c - other.
func (c Coord) Sub(other Coord) Coord {
	return Coord{Column: c.Column - other.Column, Row: c.Row - other.Row}
}

// ---------------------------------------------------------------------------
// FloatCoord — sub-pixel position used internally by Motion
// ---------------------------------------------------------------------------

// FloatCoord is a floating-point 2-D position used for smooth sub-pixel motion.
type FloatCoord struct {
	Column float64
	Row    float64
}

// ToCoord rounds a FloatCoord to the nearest integer Coord.
func (fc FloatCoord) ToCoord() Coord {
	return Coord{
		Column: int(math.Round(fc.Column)),
		Row:    int(math.Round(fc.Row)),
	}
}

// Add returns fc + other.
func (fc FloatCoord) Add(other FloatCoord) FloatCoord {
	return FloatCoord{Column: fc.Column + other.Column, Row: fc.Row + other.Row}
}

// Sub returns fc - other.
func (fc FloatCoord) Sub(other FloatCoord) FloatCoord {
	return FloatCoord{Column: fc.Column - other.Column, Row: fc.Row - other.Row}
}

// Scale multiplies both components by factor.
func (fc FloatCoord) Scale(factor float64) FloatCoord {
	return FloatCoord{Column: fc.Column * factor, Row: fc.Row * factor}
}

// Distance returns the Euclidean distance to other.
func (fc FloatCoord) Distance(other FloatCoord) float64 {
	dc := fc.Column - other.Column
	dr := fc.Row - other.Row
	return math.Sqrt(dc*dc + dr*dr)
}

// Lerp linearly interpolates between fc and other by t ∈ [0,1].
func (fc FloatCoord) Lerp(other FloatCoord, t float64) FloatCoord {
	return FloatCoord{
		Column: fc.Column + (other.Column-fc.Column)*t,
		Row:    fc.Row + (other.Row-fc.Row)*t,
	}
}

// Normalize returns a unit-length vector; returns zero vector if length == 0.
func (fc FloatCoord) Normalize() FloatCoord {
	length := math.Sqrt(fc.Column*fc.Column + fc.Row*fc.Row)
	if length == 0 {
		return FloatCoord{}
	}
	return FloatCoord{Column: fc.Column / length, Row: fc.Row / length}
}

// FromCoord converts an integer Coord to a FloatCoord.
func FromCoord(c Coord) FloatCoord {
	return FloatCoord{Column: float64(c.Column), Row: float64(c.Row)}
}

// ---------------------------------------------------------------------------
// Bezier curve helpers
// Source: geometry.py — find_coord_on_bezier_curve, find_length_of_bezier_curve
// ---------------------------------------------------------------------------

// QuadraticBezier returns the point at t ∈ [0,1] on a quadratic Bézier curve
// defined by p0 (start), p1 (control), p2 (end).
func QuadraticBezier(p0, p1, p2 FloatCoord, t float64) FloatCoord {
	mt := 1 - t
	return FloatCoord{
		Column: mt*mt*p0.Column + 2*mt*t*p1.Column + t*t*p2.Column,
		Row:    mt*mt*p0.Row + 2*mt*t*p1.Row + t*t*p2.Row,
	}
}

// CubicBezier returns the point at t ∈ [0,1] on a cubic Bézier curve
// defined by p0, p1, p2 (controls), p3 (end).
func CubicBezier(p0, p1, p2, p3 FloatCoord, t float64) FloatCoord {
	mt := 1 - t
	mt2 := mt * mt
	mt3 := mt2 * mt
	t2 := t * t
	t3 := t2 * t
	return FloatCoord{
		Column: mt3*p0.Column + 3*mt2*t*p1.Column + 3*mt*t2*p2.Column + t3*p3.Column,
		Row:    mt3*p0.Row + 3*mt2*t*p1.Row + 3*mt*t2*p2.Row + t3*p3.Row,
	}
}

// BezierAtControls dispatches to Quadratic or Cubic based on the number of
// control points (1 = quadratic, 2 = cubic).  Returns linear interpolation
// for any other control-point count.
func BezierAtControls(start FloatCoord, controls []FloatCoord, end FloatCoord, t float64) FloatCoord {
	switch len(controls) {
	case 1:
		return QuadraticBezier(start, controls[0], end, t)
	case 2:
		return CubicBezier(start, controls[0], controls[1], end, t)
	default:
		return start.Lerp(end, t)
	}
}

// LengthOfBezierCurve approximates the arc-length of a Bézier curve by
// sampling 10 segments (matching the Python implementation).
func LengthOfBezierCurve(start FloatCoord, controls []FloatCoord, end FloatCoord) float64 {
	length := 0.0
	prev := start
	for i := 1; i <= 10; i++ {
		pt := BezierAtControls(start, controls, end, float64(i)/10.0)
		length += LengthOfLine(prev, pt, true)
		prev = pt
	}
	return length
}

// ---------------------------------------------------------------------------
// Line helpers
// Source: geometry.py — find_length_of_line, find_coord_on_line
// ---------------------------------------------------------------------------

// LengthOfLine returns the Euclidean distance between two FloatCoords.
// When doubleRowDiff is true the row difference is doubled to account for the
// typical terminal cell aspect ratio (Python: double_row_diff=True).
func LengthOfLine(a, b FloatCoord, doubleRowDiff bool) float64 {
	dc := b.Column - a.Column
	dr := b.Row - a.Row
	if doubleRowDiff {
		dr *= 2
	}
	return math.Sqrt(dc*dc + dr*dr)
}

// CoordOnLine returns the FloatCoord at t ∈ [0,1] along the line from start to end.
func CoordOnLine(start, end FloatCoord, t float64) FloatCoord {
	return start.Lerp(end, t)
}

// ExtrapolateAlongRay returns the point that is offsetFromTarget units past
// 'target' along the ray from 'origin' through 'target'.
// Source: geometry.py — extrapolate_along_ray
func ExtrapolateAlongRay(origin, target FloatCoord, offsetFromTarget float64) FloatCoord {
	totalDist := LengthOfLine(origin, target, false) + offsetFromTarget
	baseDist := LengthOfLine(origin, target, false)
	if baseDist == 0 || origin == target {
		return target
	}
	t := totalDist / baseDist
	return FloatCoord{
		Column: (1-t)*origin.Column + t*target.Column,
		Row:    (1-t)*origin.Row + t*target.Row,
	}
}

// ---------------------------------------------------------------------------
// Geometric coordinate sets
// Source: geometry.py — find_coords_on_circle, find_coords_in_circle,
//                        find_coords_in_rect, find_normalized_distance_from_center
// ---------------------------------------------------------------------------

// FindCoordsOnCircle returns up to coordsLimit evenly-spaced integer points on
// a circle with the given origin and radius.  Duplicate points are removed when
// unique=true.  The column distance is doubled (terminal aspect ratio).
func FindCoordsOnCircle(origin Coord, radius int, coordsLimit int, unique bool) []Coord {
	if radius == 0 {
		return nil
	}
	if coordsLimit == 0 {
		coordsLimit = int(math.Round(2 * math.Pi * float64(radius)))
	}
	seen := make(map[Coord]struct{})
	points := make([]Coord, 0, coordsLimit)
	angleStep := 2 * math.Pi / float64(coordsLimit)
	for i := 0; i < coordsLimit; i++ {
		angle := angleStep * float64(i)
		x := float64(origin.Column) + float64(radius)*math.Cos(angle)
		// double the column distance from origin to account for terminal aspect ratio
		xDiff := x - float64(origin.Column)
		x += xDiff
		y := float64(origin.Row) + float64(radius)*math.Sin(angle)
		pt := Coord{Column: int(math.Round(x)), Row: int(math.Round(y))}
		if unique {
			if _, ok := seen[pt]; ok {
				continue
			}
			seen[pt] = struct{}{}
		}
		points = append(points, pt)
	}
	return points
}

// FindCoordsInCircle returns all integer coordinates inside an ellipse whose
// major axis has the given diameter, centred on center.  The elliptical shape
// produces a visually circular region in a typical terminal.
func FindCoordsInCircle(center Coord, diameter int) []Coord {
	if diameter == 0 {
		return nil
	}
	h, k := center.Column, center.Row
	aSquared := float64(diameter * diameter)
	bSquared := float64(diameter/2) * float64(diameter/2)
	coords := make([]Coord, 0)
	for x := h - diameter; x <= h+diameter; x++ {
		xComp := float64((x-h)*(x-h)) / aSquared
		maxYOff := int(math.Sqrt(bSquared * (1 - xComp)))
		for y := k - maxYOff; y <= k+maxYOff; y++ {
			coords = append(coords, Coord{Column: x, Row: y})
		}
	}
	return coords
}

// FindCoordsInRect returns all integer coordinates within a rectangular region
// centred on origin, extending 'distance' cells in each direction.
func FindCoordsInRect(origin Coord, distance int) []Coord {
	if distance == 0 {
		return nil
	}
	coords := make([]Coord, 0, (2*distance+1)*(2*distance+1))
	for col := origin.Column - distance; col <= origin.Column+distance; col++ {
		for row := origin.Row - distance; row <= origin.Row+distance; row++ {
			coords = append(coords, Coord{Column: col, Row: row})
		}
	}
	return coords
}

// FindNormalizedDistanceFromCenter returns a value in [0,1] representing how
// far coord is from the centre of the rectangle defined by
// (minRow..maxRow, minCol..maxCol).
// Source: geometry.py — find_normalized_distance_from_center
func FindNormalizedDistanceFromCenter(minRow, maxRow, minCol, maxCol int, coord Coord) float64 {
	yOff := minRow - 1
	xOff := minCol - 1
	right := maxCol - xOff
	top := maxRow - yOff
	centerX := float64(right) / 2
	centerY := float64(top) / 2
	maxDist := math.Sqrt(float64(right*right) + float64(top*2)*float64(top*2))
	cx := float64(coord.Column-xOff) - centerX
	cy := float64(coord.Row-yOff) - centerY
	dist := math.Sqrt(cx*cx + (cy*2)*(cy*2))
	if maxDist == 0 {
		return 0
	}
	return dist / (maxDist / 2)
}
