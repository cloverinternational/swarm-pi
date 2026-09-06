package x11

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
)

// MoveMouse moves the cursor to the specified coordinates.
func (b *Backend) MoveMouse(x, y int) error {
	_, err := b.runCommand("xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y))
	if err != nil {
		return &computeruse.InputError{Operation: "mousemove", Err: err}
	}

	// Settle delay
	time.Sleep(time.Duration(b.opts.MoveSettleMs) * time.Millisecond)
	return nil
}

// Click performs a mouse click at the specified coordinates.
func (b *Backend) Click(x, y int, button computeruse.MouseButton, count computeruse.ClickCount, modifiers []string) error {
	// Move to position first
	if err := b.MoveMouse(x, y); err != nil {
		return err
	}

	// Map button to xdotool button number
	buttonNum := "1"
	switch button {
	case computeruse.MouseButtonLeft:
		buttonNum = "1"
	case computeruse.MouseButtonMiddle:
		buttonNum = "2"
	case computeruse.MouseButtonRight:
		buttonNum = "3"
	}

	// Build click command
	args := []string{"click", buttonNum}
	if count > 1 {
		args = append(args, "--repeat", strconv.Itoa(int(count)))
		args = append(args, "--delay", "200") // Delay between clicks
	}

	// Handle modifiers
	if len(modifiers) > 0 {
		// Press modifiers
		for _, mod := range modifiers {
			modKey := mapModifier(mod)
			if _, err := b.runCommand("xdotool", "keydown", modKey); err != nil {
				return &computeruse.InputError{Operation: "keydown", Err: err}
			}
		}

		// Ensure modifiers are released
		defer func() {
			for _, mod := range modifiers {
				modKey := mapModifier(mod)
				b.runCommand("xdotool", "keyup", modKey) //nolint:errcheck
			}
		}()
	}

	// Execute click
	_, err := b.runCommand("xdotool", args...)
	if err != nil {
		return &computeruse.InputError{Operation: "click", Err: err}
	}

	return nil
}

// MouseDown presses the left mouse button.
func (b *Backend) MouseDown() error {
	_, err := b.runCommand("xdotool", "mousedown", "1")
	if err != nil {
		return &computeruse.InputError{Operation: "mousedown", Err: err}
	}
	return nil
}

// MouseUp releases the left mouse button.
func (b *Backend) MouseUp() error {
	_, err := b.runCommand("xdotool", "mouseup", "1")
	if err != nil {
		return &computeruse.InputError{Operation: "mouseup", Err: err}
	}
	return nil
}

// GetCursorPosition returns the current mouse position.
func (b *Backend) GetCursorPosition() (*computeruse.Point, error) {
	output, err := b.runCommand("xdotool", "getmouselocation", "--shell")
	if err != nil {
		return nil, &computeruse.InputError{Operation: "getmouselocation", Err: err}
	}

	// Parse output: X=123 Y=456 SCREEN=0 WINDOW=...
	var x, y int
	for line := range strings.SplitSeq(output, "\n") {
		if after, ok := strings.CutPrefix(line, "X="); ok {
			x, _ = strconv.Atoi(after)
		} else if after, ok := strings.CutPrefix(line, "Y="); ok {
			y, _ = strconv.Atoi(after)
		}
	}

	return &computeruse.Point{X: x, Y: y}, nil
}

// Drag performs a drag operation from one point to another.
func (b *Backend) Drag(from, to *computeruse.Point) error {
	if to == nil {
		return fmt.Errorf("drag: destination point is required")
	}
	// Move to start position if specified
	if from != nil {
		if err := b.MoveMouse(from.X, from.Y); err != nil {
			return err
		}
	}

	// Press mouse button
	if err := b.MouseDown(); err != nil {
		return err
	}

	// Settle after press
	time.Sleep(time.Duration(b.opts.MoveSettleMs) * time.Millisecond)

	// Ensure release
	defer b.MouseUp() //nolint:errcheck

	// Move to end position (with animation if enabled)
	if b.opts.MouseAnimation {
		if err := b.animatedMove(to.X, to.Y); err != nil {
			return err
		}
	} else {
		if err := b.MoveMouse(to.X, to.Y); err != nil {
			return err
		}
	}

	return nil
}

// Scroll performs a scroll operation at the specified position.
func (b *Backend) Scroll(x, y int, dx, dy int) error {
	// Move to position first
	if err := b.MoveMouse(x, y); err != nil {
		return err
	}

	// Vertical scroll
	if dy != 0 {
		// xdotool uses button 4 for scroll up, 5 for scroll down
		// Positive dy = scroll up, negative dy = scroll down
		button := "4"
		if dy < 0 {
			button = "5"
		}

		// Number of scroll clicks
		clicks := abs(dy)
		for range clicks {
			if _, err := b.runCommand("xdotool", "click", button); err != nil {
				return &computeruse.InputError{Operation: "scroll_vertical", Err: err}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Horizontal scroll (button 6 = left, 7 = right)
	if dx != 0 {
		button := "7"
		if dx < 0 {
			button = "6"
		}

		clicks := abs(dx)
		for range clicks {
			if _, err := b.runCommand("xdotool", "click", button); err != nil {
				return &computeruse.InputError{Operation: "scroll_horizontal", Err: err}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	return nil
}

// animatedMove moves the mouse with smooth animation.
func (b *Backend) animatedMove(targetX, targetY int) error {
	current, err := b.GetCursorPosition()
	if err != nil {
		return b.MoveMouse(targetX, targetY) // Fallback to instant move
	}

	distance := computeruse.Distance(*current, computeruse.Point{X: targetX, Y: targetY})
	if distance < 2 {
		return nil // Already at target
	}

	frames := computeruse.AnimationFrames(distance, b.opts.AnimationFPS, 0.5)
	if frames < 2 {
		return b.MoveMouse(targetX, targetY) // Too short for animation
	}

	frameInterval := time.Second / time.Duration(b.opts.AnimationFPS)
	deltaX := float64(targetX - current.X)
	deltaY := float64(targetY - current.Y)

	for frame := 1; frame <= frames; frame++ {
		t := float64(frame) / float64(frames)
		eased := computeruse.EaseOutCubic(t)

		x := int(float64(current.X) + deltaX*eased)
		y := int(float64(current.Y) + deltaY*eased)

		if _, err := b.runCommand("xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y)); err != nil {
			return &computeruse.InputError{Operation: "animated_move", Err: err}
		}

		if frame < frames {
			time.Sleep(frameInterval)
		}
	}

	time.Sleep(time.Duration(b.opts.MoveSettleMs) * time.Millisecond)
	return nil
}

// mapModifier maps a modifier name to xdotool key name.
func mapModifier(mod string) string {
	switch strings.ToLower(mod) {
	case "ctrl", "control":
		return "ctrl"
	case "alt", "option":
		return "alt"
	case "shift":
		return "shift"
	case "super", "cmd", "command", "meta", "win", "windows":
		return "super"
	default:
		return mod
	}
}

// abs returns the absolute value of an integer.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
