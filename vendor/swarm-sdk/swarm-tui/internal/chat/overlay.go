package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Overlay places the top string over the bottom string, centered.
func Overlay(bottom, top string, width, height int, bgColor string) string {
	// Center overlay by default
	topLines := strings.Split(top, "\n")
	topH := len(topLines)
	topW := lipgloss.Width(top)

	startY := (height - topH) / 2
	startX := (width - topW) / 2

	return overlayAt(bottom, top, width, height, startX, startY, bgColor)
}

// overlayAutocomplete positions autocomplete directly above the input box
// width is the chat width (excluding side panel), so overlays position correctly
func overlayAutocomplete(base, autocomplete string, width, height, inputStartY int) string {
	if autocomplete == "" {
		return base
	}

	autocompleteHeight := len(strings.Split(autocomplete, "\n"))

	// Position directly above the rendered input area
	startY := inputStartY - autocompleteHeight - 1
	if startY < 0 {
		startY = 0
	}

	// Left align with the input prompt (use width-based positioning, not fixed value)
	// The width parameter already accounts for side panel, so we position at a proportional startX
	startX := 2

	return overlayAt(base, autocomplete, width, height, startX, startY, "")
}

// overlayModal positions modal centered in the middle of the screen
func overlayModal(base, modal string, width, height int) string {
	if modal == "" {
		return base
	}

	modalHeight := len(strings.Split(modal, "\n"))
	modalWidth := lipgloss.Width(modal)

	startY := (height - modalHeight) / 2
	startX := (width - modalWidth) / 2

	return overlayAt(base, modal, width, height, startX, startY, "")
}

// overlayAt composes overlay on top of base at an explicit position using
// ANSI-aware string operations to preserve escape sequences.
func overlayAt(base, overlay string, width, height, startX, startY int, bgColor string) string {
	if width <= 0 || height <= 0 || overlay == "" {
		return base
	}

	// Pad base vertically to the expected height without altering existing lines
	baseLines := strings.Split(base, "\n")
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}

	// Optionally tint the background without touching the underlying content width
	if bgColor != "" {
		lineStyle := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
		for i := range baseLines {
			baseLines[i] = lineStyle.Render(baseLines[i])
		}
	}

	// Ensure all base lines have at least 'width' cells for proper overlay
	for i := range baseLines {
		lineWidth := ansi.StringWidth(baseLines[i])
		if lineWidth < width {
			baseLines[i] = baseLines[i] + strings.Repeat(" ", width-lineWidth)
		}
	}

	overlayLines := strings.Split(overlay, "\n")

	// Clamp overlay origin so it stays on screen
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	// Overlay the content using ANSI-aware Cut
	for i, overlayLine := range overlayLines {
		y := startY + i
		if y >= 0 && y < len(baseLines) {
			baseLine := baseLines[y]
			overlayLineWidth := ansi.StringWidth(overlayLine)

			// Use ansi.Cut to extract portions while preserving ANSI codes
			// Left part: from cell 0 to startX
			leftPart := ""
			if startX > 0 {
				leftPart = ansi.Cut(baseLine, 0, startX)
			}

			// Right part: from startX + overlayWidth to end
			rightStart := startX + overlayLineWidth
			rightPart := ""
			baseLineWidth := ansi.StringWidth(baseLine)
			if rightStart < baseLineWidth {
				rightPart = ansi.Cut(baseLine, rightStart, baseLineWidth)
			}

			// Pad left part if needed
			leftWidth := ansi.StringWidth(leftPart)
			if leftWidth < startX {
				leftPart = leftPart + strings.Repeat(" ", startX-leftWidth)
			}

			// Compose the line: left + overlay + right
			baseLines[y] = leftPart + overlayLine + rightPart
		}
	}

	// Trim to height
	if len(baseLines) > height {
		baseLines = baseLines[:height]
	}

	return strings.Join(baseLines, "\n")
}
