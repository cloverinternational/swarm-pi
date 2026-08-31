package computeruse

import (
	"math"
)

// CoordinateConverter handles conversion between coordinate systems.
type CoordinateConverter struct {
	displayWidth  int
	displayHeight int
}

// NewCoordinateConverter creates a new coordinate converter for a display.
func NewCoordinateConverter(width, height int) *CoordinateConverter {
	return &CoordinateConverter{
		displayWidth:  width,
		displayHeight: height,
	}
}

// NormalizedToPixels converts normalized coordinates (0-1000) to pixels.
// Normalized coordinates are used by some APIs for display-independent positioning.
func (c *CoordinateConverter) NormalizedToPixels(normX, normY int) (int, int) {
	pixelX := (normX * c.displayWidth) / 1000
	pixelY := (normY * c.displayHeight) / 1000
	return pixelX, pixelY
}

// PixelsToNormalized converts pixel coordinates to normalized (0-1000).
func (c *CoordinateConverter) PixelsToNormalized(pixelX, pixelY int) (int, int) {
	normX := (pixelX * 1000) / c.displayWidth
	normY := (pixelY * 1000) / c.displayHeight
	return normX, normY
}

// ClampPoint ensures a point is within display bounds.
func (c *CoordinateConverter) ClampPoint(x, y int) (int, int) {
	if x < 0 {
		x = 0
	} else if x >= c.displayWidth {
		x = c.displayWidth - 1
	}
	if y < 0 {
		y = 0
	} else if y >= c.displayHeight {
		y = c.displayHeight - 1
	}
	return x, y
}

// RectInBounds checks if a rectangle is within display bounds.
func (c *CoordinateConverter) RectInBounds(r Rect) bool {
	return r.X >= 0 && r.Y >= 0 &&
		r.X+r.W <= c.displayWidth &&
		r.Y+r.H <= c.displayHeight
}

// ClampRect ensures a rectangle is within display bounds.
func (c *CoordinateConverter) ClampRect(r Rect) Rect {
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	if r.X+r.W > c.displayWidth {
		r.W = c.displayWidth - r.X
	}
	if r.Y+r.H > c.displayHeight {
		r.H = c.displayHeight - r.Y
	}
	return r
}

// TargetImageSize calculates the target image dimensions for API transmission.
// Matches the sizing logic from Claude Code's computer-use-mcp package.
func TargetImageSize(physW, physH int, maxDim int) (int, int) {
	if physW <= maxDim && physH <= maxDim {
		return physW, physH
	}

	// Scale down to fit within maxDim
	scale := float64(maxDim) / math.Max(float64(physW), float64(physH))
	newW := int(math.Round(float64(physW) * scale))
	newH := int(math.Round(float64(physH) * scale))

	// Ensure we don't exceed maxDim
	if newW > maxDim {
		newW = maxDim
	}
	if newH > maxDim {
		newH = maxDim
	}

	return newW, newH
}

// ScaleCoord scales a coordinate from one image size to another.
// This is used when the screenshot was resized before sending to the API.
func ScaleCoord(x, y, srcW, srcH, dstW, dstH int) (int, int) {
	scaleX := float64(dstW) / float64(srcW)
	scaleY := float64(dstH) / float64(srcH)
	return int(math.Round(float64(x) * scaleX)), int(math.Round(float64(y) * scaleY))
}

// Distance calculates the Euclidean distance between two points.
func Distance(p1, p2 Point) float64 {
	dx := float64(p2.X - p1.X)
	dy := float64(p2.Y - p1.Y)
	return math.Sqrt(dx*dx + dy*dy)
}

// EaseOutCubic applies ease-out cubic easing for smooth animation.
// Used for animated mouse movement.
func EaseOutCubic(t float64) float64 {
	return 1 - math.Pow(1-t, 3)
}

// AnimationFrames calculates the number of frames for animated movement.
// Based on distance with a maximum duration.
func AnimationFrames(distance float64, fps int, maxDurationSec float64) int {
	// 2000 px/sec speed, capped at maxDurationSec
	durationSec := math.Min(distance/2000, maxDurationSec)
	return int(math.Round(durationSec * float64(fps)))
}
