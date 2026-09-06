package x11

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
)

// displayRegex matches xrandr output lines like:
// eDP-1 connected primary 1920x1080+0+0 (normal left inverted right x axis y axis) 344mm x 193mm
// HDMI-1 connected 1920x1080+1920+0 (normal left inverted right x axis y axis) 527mm x 296mm
var displayRegex = regexp.MustCompile(`^(\S+)\s+connected\s+(primary\s+)?(\d+)x(\d+)\+(\d+)\+(\d+)`)

// GetDisplaySize returns the geometry of a specific display.
func (b *Backend) GetDisplaySize(displayID int) (*computeruse.DisplayGeometry, error) {
	b.displaysMux.RLock()
	defer b.displaysMux.RUnlock()

	if len(b.displays) == 0 {
		return nil, computeruse.ErrDisplayNotFound
	}

	// displayID 0 means primary or first display
	if displayID < 0 {
		return nil, computeruse.ErrDisplayNotFound
	}
	if displayID == 0 || displayID > len(b.displays) {
		// Find primary display
		for _, d := range b.displays {
			if d.Primary {
				return &d, nil
			}
		}
		// Return first display if no primary
		return &b.displays[0], nil
	}

	// Return specific display (1-indexed in API, 0-indexed internally)
	idx := displayID - 1
	if idx >= len(b.displays) {
		return nil, computeruse.ErrDisplayNotFound
	}

	return &b.displays[idx], nil
}

// ListDisplays returns all connected displays.
func (b *Backend) ListDisplays() ([]computeruse.DisplayGeometry, error) {
	b.displaysMux.RLock()
	defer b.displaysMux.RUnlock()

	// Return cached displays
	if len(b.displays) > 0 {
		result := make([]computeruse.DisplayGeometry, len(b.displays))
		copy(result, b.displays)
		return result, nil
	}

	return nil, computeruse.ErrNoDisplay
}

// listDisplaysInternal parses xrandr output to get display information.
func (b *Backend) listDisplaysInternal() ([]computeruse.DisplayGeometry, error) {
	if !b.hasXrandr {
		return nil, computeruse.NewToolNotAvailableError("xrandr")
	}

	output, err := b.runCommand("xrandr", "--query")
	if err != nil {
		return nil, fmt.Errorf("xrandr query failed: %w", err)
	}

	return parseXrandrOutput(output)
}

// parseXrandrOutput parses the output of xrandr --query.
func parseXrandrOutput(output string) ([]computeruse.DisplayGeometry, error) {
	var displays []computeruse.DisplayGeometry

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		matches := displayRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		// Parse values
		name := matches[1]
		primary := matches[2] != ""
		width, _ := strconv.Atoi(matches[3])
		height, _ := strconv.Atoi(matches[4])
		x, _ := strconv.Atoi(matches[5])
		y, _ := strconv.Atoi(matches[6])

		// X11 typically uses scale factor of 1 (no HiDPI auto-detection)
		scaleFactor := 1.0

		displays = append(displays, computeruse.DisplayGeometry{
			ID:          len(displays) + 1,
			Name:        name,
			Width:       width,
			Height:      height,
			ScaleFactor: scaleFactor,
			Primary:     primary,
			X:           x,
			Y:           y,
		})
	}

	if len(displays) == 0 {
		return nil, fmt.Errorf("no connected displays found in xrandr output")
	}

	return displays, nil
}

// RefreshDisplays refreshes the cached display information.
func (b *Backend) RefreshDisplays() error {
	displays, err := b.listDisplaysInternal()
	if err != nil {
		return err
	}

	b.displaysMux.Lock()
	b.displays = displays
	b.displaysMux.Unlock()

	return nil
}

// FindWindowDisplays finds which displays have windows for the given app IDs.
// On Linux, app IDs are window class names.
func (b *Backend) FindWindowDisplays(appIDs []string) (map[string][]int, error) {
	result := make(map[string][]int)

	for _, appID := range appIDs {
		// Find windows with this class
		output, err := b.runCommand("xdotool", "search", "--class", appID)
		if err != nil {
			continue // No windows found for this app
		}

		// Get window list
		windowIDs := strings.Split(output, "\n")
		if len(windowIDs) == 0 {
			continue
		}

		// Check which displays these windows are on
		displaySet := make(map[int]bool)
		for _, winID := range windowIDs {
			if winID == "" {
				continue
			}

			// Get window geometry
			geom, err := b.getWindowGeometry(winID)
			if err != nil {
				continue
			}

			// Find which display contains this window
			displayID := b.findDisplayForPoint(geom.X+geom.W/2, geom.Y+geom.H/2)
			if displayID > 0 {
				displaySet[displayID] = true
			}
		}

		// Convert set to slice
		var displayIDs []int
		for id := range displaySet {
			displayIDs = append(displayIDs, id)
		}
		result[appID] = displayIDs
	}

	return result, nil
}

// windowGeom represents window geometry.
type windowGeom struct {
	X, Y, W, H int
}

// getWindowGeometry gets the geometry of a window.
func (b *Backend) getWindowGeometry(windowID string) (*windowGeom, error) {
	output, err := b.runCommand("xdotool", "getwindowgeometry", "--shell", windowID)
	if err != nil {
		return nil, err
	}

	var geom windowGeom
	for line := range strings.SplitSeq(output, "\n") {
		if after, ok := strings.CutPrefix(line, "X="); ok {
			geom.X, _ = strconv.Atoi(after)
		} else if after, ok := strings.CutPrefix(line, "Y="); ok {
			geom.Y, _ = strconv.Atoi(after)
		} else if after, ok := strings.CutPrefix(line, "WIDTH="); ok {
			geom.W, _ = strconv.Atoi(after)
		} else if after, ok := strings.CutPrefix(line, "HEIGHT="); ok {
			geom.H, _ = strconv.Atoi(after)
		}
	}

	return &geom, nil
}

// findDisplayForPoint finds which display contains a point.
func (b *Backend) findDisplayForPoint(x, y int) int {
	b.displaysMux.RLock()
	defer b.displaysMux.RUnlock()

	for _, d := range b.displays {
		if x >= d.X && x < d.X+d.Width && y >= d.Y && y < d.Y+d.Height {
			return d.ID
		}
	}

	return 0
}
