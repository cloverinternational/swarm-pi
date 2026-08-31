package chat

import (
	"strings"
)

// ============================================================================
// VIEW - RENDERING
// ============================================================================

// View renders the viewport.
// Three-path rendering:
//  1. Fast cache: identical geometry + content -> return cached string (O(1))
//  2. Pre-wrapped: background worker already wrapped all lines -> slice directly
//  3. Sync fallback: first frame or width-change -> wrap synchronously
func (m *MessageList) View() string {
	if len(m.lines) == 0 {
		result := strings.Repeat("\n", m.Height-1)
		return result
	}

	h := m.Height - m.Style.GetVerticalFrameSize()
	if h <= 0 {
		return ""
	}

	w := m.Width - m.Style.GetHorizontalFrameSize()
	if w <= 0 {
		w = m.Width
	}

	// Fast cache path: skip expensive wrapping entirely when nothing could have changed.
	if !m.cachedDirty &&
		m.cachedView != "" &&
		m.cachedOffset == m.YOffset &&
		m.cachedWidth == w &&
		m.cachedHeight == h &&
		m.cachedSelection == m.Selection {
		return m.cachedView
	}

	// Pre-wrapped path: background worker already wrapped the full content.
	// This path renders preWrappedLines[YOffset:YOffset+h], i.e. YOffset is a
	// WRAPPED (visual) line index — the same space maxYOffset()/SetYOffset use
	// when pre-wrap is active, and the same space ScreenToContent maps into.
	// We use it for BOTH selection and non-selection frames so that activating a
	// selection never reinterprets YOffset (which previously flipped the frame to
	// raw-line space and made clicks/highlights land on the wrong line).
	if m.preWrappedWidth == w &&
		m.preWrappedHash == m.lastContentHash &&
		len(m.preWrappedLines) > 0 &&
		len(m.preWrappedMapping) == len(m.lines)+1 {
		if m.Selection.Active {
			result := m.renderPreWrappedWithSelection(w, h)
			return result
		}
		result := m.renderFromPreWrappedLines(w, h)
		return result
	}

	// Fallback: synchronous wrap.
	// When the viewport is pinned to the bottom (the common case during
	// streaming, before the background pre-wrap result arrives), anchor the
	// wrapped window to the bottom. This makes the intermediate frame match the
	// post-prewrap frame and eliminates the per-chunk vertical "snap"/bounce.
	wrapped := wrapAndPadContentAnchored(m.lines, m.YOffset, h, w, m.AtBottom())
	result := wrapped.content

	// Apply selection highlighting when a range is active.
	if m.Selection.Active {
		result = applySelectionHighlight(result, m.Selection, m)
	}

	// Apply style if needed
	if m.Style.GetHorizontalFrameSize() > 0 || m.Style.GetVerticalFrameSize() > 0 {
		result = m.Style.Render(result)
	}

	// Update cache
	m.cachedView = result
	m.cachedOffset = m.YOffset
	m.cachedWidth = w
	m.cachedHeight = h
	m.cachedSelection = m.Selection
	m.cachedDirty = false

	return result
}

// renderFromPreWrappedLines renders the viewport from the pre-wrapped line cache.
// No text wrapping occurs: we slice preWrappedLines for the current scroll offset.
func (m *MessageList) renderFromPreWrappedLines(w, h int) string {
	top := m.YOffset
	if top < 0 {
		top = 0
	}
	bottom := top + h
	if bottom > len(m.preWrappedLines) {
		bottom = len(m.preWrappedLines)
	}

	var visibleLines []string
	if top < len(m.preWrappedLines) {
		visibleLines = m.preWrappedLines[top:bottom]
	}

	// Pad to exact height so output always receives a full frame.
	padded := make([]string, 0, h)
	padded = append(padded, visibleLines...)
	for len(padded) < h {
		padded = append(padded, "")
	}
	content := strings.Join(padded, "\n")

	if m.Style.GetHorizontalFrameSize() > 0 || m.Style.GetVerticalFrameSize() > 0 {
		content = m.Style.Render(content)
	}

	// Populate the view cache so that the next frame (same scroll position) hits
	// the fast path and does zero work.
	m.cachedView = content
	m.cachedOffset = m.YOffset
	m.cachedWidth = w
	m.cachedHeight = h
	m.cachedSelection = m.Selection
	m.cachedDirty = false

	return content
}

// renderPreWrappedWithSelection renders the pre-wrapped frame (wrapped-line
// space, sliced by a WRAPPED YOffset) with selection highlighting applied in
// that same wrapped space. This keeps the selection-active frame in EXACTLY the
// same coordinate space as the non-selection pre-wrapped frame and as
// ScreenToContent's hit-testing, so activating or dragging a selection never
// shifts the frame under the cursor.
func (m *MessageList) renderPreWrappedWithSelection(w, h int) string {
	top := m.YOffset
	if top < 0 {
		top = 0
	}
	bottom := top + h
	if bottom > len(m.preWrappedLines) {
		bottom = len(m.preWrappedLines)
	}

	var visibleLines []string
	if top < len(m.preWrappedLines) {
		visibleLines = m.preWrappedLines[top:bottom]
	}
	padded := make([]string, 0, h)
	padded = append(padded, visibleLines...)
	for len(padded) < h {
		padded = append(padded, "")
	}
	content := strings.Join(padded, "\n")

	// Highlight using wrapped-line rectangles, in the same wrapped-line space
	// that ScreenToContent maps clicks into.
	content = m.applyWrappedSelectionHighlight(content, m.Selection, w, h, top)

	if m.Style.GetHorizontalFrameSize() > 0 || m.Style.GetVerticalFrameSize() > 0 {
		content = m.Style.Render(content)
	}

	// Cache so the next identical frame hits the fast path.
	m.cachedView = content
	m.cachedOffset = m.YOffset
	m.cachedWidth = w
	m.cachedHeight = h
	m.cachedSelection = m.Selection
	m.cachedDirty = false

	return content
}
