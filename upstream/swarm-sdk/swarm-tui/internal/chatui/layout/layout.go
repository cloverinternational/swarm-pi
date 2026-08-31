// Package layout provides layout calculations for the chat UI.
//
// The layout package handles terminal size adaptation, multi-panel
// split calculations, and responsive design concerns.
package layout

import (
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
)

// Rect represents a rectangular region on screen.
type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Calculator provides layout calculations.
type Calculator struct {
	width  int
	height int

	// Minimum dimensions
	minPanelWidth  int
	minPanelHeight int

	// Side panel configuration
	sidePanelWidth int
	showSidePanel  bool
}

// NewCalculator creates a layout calculator with the given dimensions.
func NewCalculator(width, height int) *Calculator {
	return &Calculator{
		width:          width,
		height:         height,
		minPanelWidth:  40,
		minPanelHeight: 10,
		sidePanelWidth: 38,
		showSidePanel:  width >= 90,
	}
}

// Resize updates the calculator dimensions.
func (c *Calculator) Resize(width, height int) {
	c.width = width
	c.height = height
	c.showSidePanel = width >= 90
}

// Width returns the total width.
func (c *Calculator) Width() int {
	return c.width
}

// Height returns the total height.
func (c *Calculator) Height() int {
	return c.height
}

// ShowSidePanel returns whether the side panel should be shown.
func (c *Calculator) ShowSidePanel() bool {
	return c.showSidePanel
}

// SetShowSidePanel overrides the side panel visibility.
func (c *Calculator) SetShowSidePanel(show bool) {
	c.showSidePanel = show
}

// ContentArea returns the main content area bounds.
func (c *Calculator) ContentArea() Rect {
	width := c.width
	if c.showSidePanel {
		width -= c.sidePanelWidth
	}
	return Rect{
		X:      0,
		Y:      0,
		Width:  width,
		Height: c.height,
	}
}

// SidePanelArea returns the side panel bounds.
func (c *Calculator) SidePanelArea() Rect {
	if !c.showSidePanel {
		return Rect{}
	}
	return Rect{
		X:      c.width - c.sidePanelWidth,
		Y:      0,
		Width:  c.sidePanelWidth,
		Height: c.height,
	}
}

// MessageAreaHeight returns the height available for messages.
// Subtracts space for input area (3 lines) and status bar (1 line).
func (c *Calculator) MessageAreaHeight() int {
	return c.height - 4
}

// InputAreaHeight returns the height of the input area.
func (c *Calculator) InputAreaHeight() int {
	return 3
}

// CalculateSplit calculates panel bounds for a split layout.
func (c *Calculator) CalculateSplit(state *types.MultiPanelState) []Rect {
	content := c.ContentArea()

	if len(state.Panels) == 1 {
		return []Rect{content}
	}

	if len(state.Panels) == 2 {
		return c.calculateTwoPanel(content, state.SplitDirection, state.SplitRatio)
	}

	// For more panels, use recursive splitting
	return c.calculateMultiPanel(content, state)
}

// calculateTwoPanel calculates bounds for two panels.
func (c *Calculator) calculateTwoPanel(area Rect, direction types.SplitDirection, ratio float64) []Rect {
	if direction == types.SplitHorizontal {
		// Left/Right split
		firstWidth := max(int(float64(area.Width)*ratio), c.minPanelWidth)
		if area.Width-firstWidth < c.minPanelWidth {
			firstWidth = area.Width - c.minPanelWidth
		}

		return []Rect{
			{X: area.X, Y: area.Y, Width: firstWidth - 1, Height: area.Height},
			{X: area.X + firstWidth, Y: area.Y, Width: area.Width - firstWidth, Height: area.Height},
		}
	}

	// Top/Bottom split
	firstHeight := max(int(float64(area.Height)*ratio), c.minPanelHeight)
	if area.Height-firstHeight < c.minPanelHeight {
		firstHeight = area.Height - c.minPanelHeight
	}

	return []Rect{
		{X: area.X, Y: area.Y, Width: area.Width, Height: firstHeight - 1},
		{X: area.X, Y: area.Y + firstHeight, Width: area.Width, Height: area.Height - firstHeight},
	}
}

// calculateMultiPanel handles more than 2 panels.
func (c *Calculator) calculateMultiPanel(area Rect, state *types.MultiPanelState) []Rect {
	// Simple grid layout for 3+ panels
	numPanels := len(state.Panels)

	// Calculate grid dimensions
	cols := 2
	rows := (numPanels + cols - 1) / cols

	panelWidth := area.Width / cols
	panelHeight := area.Height / rows

	rects := make([]Rect, numPanels)
	for i := range numPanels {
		col := i % cols
		row := i / cols

		rects[i] = Rect{
			X:      area.X + col*panelWidth,
			Y:      area.Y + row*panelHeight,
			Width:  panelWidth - 1,
			Height: panelHeight - 1,
		}
	}

	return rects
}

// CanSplit returns true if there's room for another panel.
func (c *Calculator) CanSplit(state *types.MultiPanelState, direction types.SplitDirection) bool {
	content := c.ContentArea()

	if direction == types.SplitHorizontal {
		// Need at least 2x minimum width
		return content.Width >= 2*c.minPanelWidth
	}

	// Vertical split
	return content.Height >= 2*c.minPanelHeight
}

// OptimalSplitRatio returns the recommended split ratio for equal panels.
func (c *Calculator) OptimalSplitRatio(numPanels int) float64 {
	if numPanels <= 1 {
		return 1.0
	}
	return 1.0 / float64(numPanels)
}
