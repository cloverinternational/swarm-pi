package diffview

// DiffLayout handles layout decisions for diff rendering
type DiffLayout struct {
	Type           LayoutType
	AutoSplitWidth int // Minimum terminal width for split view (default: 120)
}

// NewDiffLayout creates a layout with auto-switching behavior
func NewDiffLayout() *DiffLayout {
	return &DiffLayout{
		Type:           LayoutAuto,
		AutoSplitWidth: 120, // Switch to split at 120+ chars
	}
}

// NewUnifiedLayout creates a layout that always uses unified view
func NewUnifiedLayout() *DiffLayout {
	return &DiffLayout{
		Type: LayoutUnified,
	}
}

// NewSplitLayout creates a layout that always uses split view
func NewSplitLayout() *DiffLayout {
	return &DiffLayout{
		Type: LayoutSplit,
	}
}

// ShouldUseSplit determines whether to use split layout based on:
// - Layout type setting
// - Available terminal width
// - Maximum code line width in the diff
func (dl *DiffLayout) ShouldUseSplit(width int, maxCodeWidth int) bool {
	switch dl.Type {
	case LayoutUnified:
		return false
	case LayoutSplit:
		return true
	case LayoutAuto:
		// For auto mode, we need enough width for:
		// - Two code panes side by side
		// - Line numbers on each side (~8 chars each)
		// - Separator (~3 chars)
		// - Some padding (~4 chars)
		minRequired := (maxCodeWidth * 2) + 8 + 8 + 3 + 4

		// But also respect absolute minimum
		return width >= dl.AutoSplitWidth && width >= minRequired
	default:
		return false
	}
}

// GetLayoutType returns the effective layout type for the given width
func (dl *DiffLayout) GetLayoutType(width int, maxCodeWidth int) LayoutType {
	if dl.ShouldUseSplit(width, maxCodeWidth) {
		return LayoutSplit
	}
	return LayoutUnified
}

// CalculateColumnWidths calculates widths for split view columns
// Returns: leftCodeWidth, rightCodeWidth
func (dl *DiffLayout) CalculateColumnWidths(totalWidth, lineNumWidth int) (int, int) {
	// Layout for split view:
	// [lineNum] [code] | [lineNum] [code]
	// lineNumWidth includes padding (e.g., " 123 " = 5 chars)
	separatorWidth := 3 // " | "

	// Available width for both code columns
	availableForCode := totalWidth - (lineNumWidth * 2) - separatorWidth

	// Split evenly
	halfWidth := max(availableForCode/2,
		// Minimum readable width
		10)

	return halfWidth, halfWidth
}

// CalculateUnifiedWidth calculates width for unified view code column
func (dl *DiffLayout) CalculateUnifiedWidth(totalWidth, lineNumWidth int) int {
	// Layout for unified view:
	// [beforeNum] [afterNum] [+/-] [code]
	symbolWidth := 3 // " + " or " - " or "   "

	codeWidth := max(totalWidth-(lineNumWidth*2)-symbolWidth,
		// Minimum readable width
		20)

	return codeWidth
}
