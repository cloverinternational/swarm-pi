package cellbuf

// Position represents a point in the buffer
type Position struct {
	X, Y int
}

// Pos creates a new Position
func Pos(x, y int) Position {
	return Position{X: x, Y: y}
}

// Rectangle represents a rectangular area in the buffer.
// This matches ultraviolet's Rectangle structure.
type Rectangle struct {
	Min Position // Top-left corner (inclusive)
	Max Position // Bottom-right corner (exclusive)
}

// Rect creates a new Rectangle from coordinates
func Rect(x0, y0, x1, y1 int) Rectangle {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return Rectangle{
		Min: Pos(x0, y0),
		Max: Pos(x1, y1),
	}
}

// Dx returns the width of the rectangle
func (r Rectangle) Dx() int {
	return r.Max.X - r.Min.X
}

// Dy returns the height of the rectangle
func (r Rectangle) Dy() int {
	return r.Max.Y - r.Min.Y
}

// Empty returns true if the rectangle has zero area
func (r Rectangle) Empty() bool {
	return r.Min.X >= r.Max.X || r.Min.Y >= r.Max.Y
}

// Canon returns the canonical version of the rectangle.
// The returned rectangle has minimum and maximum coordinates swapped if necessary
// so that Min.X <= Max.X and Min.Y <= Max.Y.
func (r Rectangle) Canon() Rectangle {
	if r.Max.X < r.Min.X {
		r.Min.X, r.Max.X = r.Max.X, r.Min.X
	}
	if r.Max.Y < r.Min.Y {
		r.Min.Y, r.Max.Y = r.Max.Y, r.Min.Y
	}
	return r
}

// Contains returns true if the point is within the rectangle
func (r Rectangle) Contains(p Position) bool {
	return p.X >= r.Min.X && p.X < r.Max.X &&
		p.Y >= r.Min.Y && p.Y < r.Max.Y
}

// Intersect returns the largest rectangle contained by both r and s.
// If the two rectangles do not overlap, the zero rectangle is returned.
func (r Rectangle) Intersect(s Rectangle) Rectangle {
	if r.Min.X < s.Min.X {
		r.Min.X = s.Min.X
	}
	if r.Min.Y < s.Min.Y {
		r.Min.Y = s.Min.Y
	}
	if r.Max.X > s.Max.X {
		r.Max.X = s.Max.X
	}
	if r.Max.Y > s.Max.Y {
		r.Max.Y = s.Max.Y
	}
	if r.Empty() {
		return Rectangle{}
	}
	return r
}

// Union returns the smallest rectangle that contains both r and s.
func (r Rectangle) Union(s Rectangle) Rectangle {
	if r.Empty() {
		return s
	}
	if s.Empty() {
		return r
	}
	if r.Min.X > s.Min.X {
		r.Min.X = s.Min.X
	}
	if r.Min.Y > s.Min.Y {
		r.Min.Y = s.Min.Y
	}
	if r.Max.X < s.Max.X {
		r.Max.X = s.Max.X
	}
	if r.Max.Y < s.Max.Y {
		r.Max.Y = s.Max.Y
	}
	return r
}

// Overlaps returns true if r and s have a non-empty intersection
func (r Rectangle) Overlaps(s Rectangle) bool {
	return !r.Empty() && !s.Empty() &&
		r.Min.X < s.Max.X && s.Min.X < r.Max.X &&
		r.Min.Y < s.Max.Y && s.Min.Y < r.Max.Y
}
