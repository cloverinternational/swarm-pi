package chat

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ============================================================================
// MESSAGE LIST
// ============================================================================

// Selection represents a text selection range within the MessageList viewport.
// StartLine/EndLine are zero-based line indices; StartCol/EndCol are byte offsets.
// When Active is false the selection is ignored and the fast-path cache is used.
type Selection struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	Active    bool
	Selecting bool // true while mouse button is held down
}

// IsEmpty returns true if the selection has no content.
func (s Selection) IsEmpty() bool {
	return !s.Active || (s.StartLine == s.EndLine && s.StartCol == s.EndCol)
}

// Normalize ensures start is before end.
func (s Selection) Normalize() Selection {
	if s.StartLine > s.EndLine || (s.StartLine == s.EndLine && s.StartCol > s.EndCol) {
		return Selection{
			StartLine: s.EndLine,
			StartCol:  s.EndCol,
			EndLine:   s.StartLine,
			EndCol:    s.StartCol,
			Active:    s.Active,
			Selecting: s.Selecting,
		}
	}
	return s
}

// MessageList is a viewport component
type MessageList struct {
	Width   int
	Height  int
	OriginX int
	OriginY int

	// Scroll position
	YOffset int
	xOffset int

	// Selection state — when Active, the selection path is used instead of the
	// fast-path cache so that highlighted ranges are rendered correctly.
	Selection Selection

	// Internal state
	initialized      bool
	content          string   // Raw content string
	lines            []string // Line slices (references into content)
	lineOffsets      []int    // Byte offsets for each line (Crush technique)
	longestLineWidth int

	// Performance: content caching (Crush techniques)
	lastContentHash uint64 // Hash of last content to detect changes
	cachedLineCount int    // Cached line count for quick access

	// View cache
	cachedView      string    // Cached rendered view
	cachedOffset    int       // YOffset when view was cached
	cachedDirty     bool      // Whether cache needs refresh
	cachedWidth     int       // Width when view was cached
	cachedHeight    int       // Height when view was cached
	cachedSelection Selection // Selection state when view was cached

	// Pre-wrapped lines: the entire content string wrapped to a specific width,
	// computed off the UI thread by a tea.Cmd. When valid, View() slices this
	// array for the current scroll offset instead of calling wrapContentToWidth,
	// making scroll a zero-wrapping-work operation.
	preWrappedLines []string // All content lines wrapped to preWrappedWidth
	preWrappedWidth int      // Width used when pre-wrapping
	preWrappedHash  uint64   // lastContentHash value at time pre-wrap completed
	// preWrappedMapping maps raw line index -> cumulative wrapped-line count.
	// mapping[i] = number of wrapped (visual) lines produced by m.lines[0:i].
	// len(mapping) == len(m.lines)+1. Used by ScreenToContent to convert a
	// wrapped/visual line index (which is what YOffset means in the pre-wrapped
	// render path) back into a raw line + segment. Kept in lock-step with
	// preWrappedLines so mouse hit-testing mirrors exactly what is on screen.
	preWrappedMapping []int

	// Native image anchors use raw transcript line coordinates. They are
	// projected through preWrappedMapping at frame time so image placement uses
	// the exact same visual-line space as custom scrolling.
	imageAnchors []nativeImageAnchor

	// Style support
	Style lipgloss.Style
}

// NewMessageList creates a new message list viewport
func NewMessageList(width, height int) *MessageList {
	m := &MessageList{}
	m.Width = width
	m.Height = height
	m.setInitialValues()
	return m
}

func (m *MessageList) setInitialValues() {
	m.initialized = true
	m.OriginX = 0
	m.OriginY = 0
}

// ============================================================================
// SCROLL METHODS
// ============================================================================

// AtTop returns whether viewport is at very top position
func (m MessageList) AtTop() bool {
	return m.YOffset <= 0
}

// AtBottom returns whether viewport is at or past very bottom position
func (m MessageList) AtBottom() bool {
	return m.YOffset >= m.maxYOffset()
}

// PastBottom returns whether viewport is scrolled beyond last line
func (m MessageList) PastBottom() bool {
	return m.YOffset > m.maxYOffset()
}

// ScrollPercent returns amount scrolled as a float between 0 and 1
func (m MessageList) ScrollPercent() float64 {
	maxOffset := m.maxYOffset()
	if maxOffset <= 0 {
		return 1.0
	}
	v := float64(m.YOffset) / float64(maxOffset)
	if v < 0.0 {
		return 0.0
	}
	if v > 1.0 {
		return 1.0
	}
	return v
}

// SetScrollPercent restores a viewport position after content or geometry
// changes. YOffset can represent either raw or wrapped lines depending on
// which render cache is active, so callers that preserve position across a
// rebuild must use this normalized coordinate instead of copying YOffset.
func (m *MessageList) SetScrollPercent(percent float64) {
	if percent < 0 {
		percent = 0
	}
	if percent > 1 {
		percent = 1
	}
	m.SetYOffset(int(float64(m.maxYOffset()) * percent))
}

func (m MessageList) maxYOffset() int {
	// Use pre-wrapped line count when the pre-wrapped path is active —
	// renderFromPreWrappedLines indexes into preWrappedLines with YOffset,
	// so the scroll boundary must be based on that slice's length, not m.lines.
	lineCount := len(m.lines)
	if len(m.preWrappedLines) > 0 && m.preWrappedHash == m.lastContentHash {
		lineCount = len(m.preWrappedLines)
	}
	max := lineCount - m.Height + m.Style.GetVerticalFrameSize()
	if max < 0 {
		return 0
	}
	return max
}

// SetYOffset sets the Y offset
func (m *MessageList) SetYOffset(n int) {
	oldOffset := m.YOffset
	maxOffset := m.maxYOffset()
	if n < 0 {
		m.YOffset = 0
	} else if n > maxOffset {
		m.YOffset = maxOffset
	} else {
		m.YOffset = n
	}
	// Mark cache dirty if offset changed
	if m.YOffset != oldOffset {
		m.cachedDirty = true
		// Selection survives scroll — it's stored in raw content coordinates.
		// The render path will recompute visible coordinates from the current YOffset.
	}
}

// ScrollDown moves view down by given number of lines
func (m *MessageList) ScrollDown(n int) {
	if m.AtBottom() || n == 0 || len(m.lines) == 0 {
		return
	}

	m.SetYOffset(m.YOffset + n)
}

// ScrollUp moves view up by given number of lines
func (m *MessageList) ScrollUp(n int) {
	if m.AtTop() || n == 0 || len(m.lines) == 0 {
		return
	}

	m.SetYOffset(m.YOffset - n)
}

// PageDown moves view down by viewport height
func (m *MessageList) PageDown() {
	if m.AtBottom() {
		return
	}
	m.ScrollDown(m.Height)
}

// PageUp moves view up by viewport height
func (m *MessageList) PageUp() {
	if m.AtTop() {
		return
	}
	m.ScrollUp(m.Height)
}

// HalfPageDown moves view down by half viewport height
func (m *MessageList) HalfPageDown() {
	if m.AtBottom() {
		return
	}
	m.ScrollDown(m.Height / 2)
}

// HalfPageUp moves view up by half viewport height
func (m *MessageList) HalfPageUp() {
	if m.AtTop() {
		return
	}
	m.ScrollUp(m.Height / 2)
}

// GotoTop scrolls to top
func (m *MessageList) GotoTop() {
	if m.AtTop() {
		return
	}
	m.SetYOffset(0)
}

// GotoBottom scrolls to bottom
func (m *MessageList) GotoBottom() {
	m.SetYOffset(m.maxYOffset())
}

// ============================================================================
// CONTENT METHODS
// ============================================================================

// SetImageAnchors replaces the native image metadata associated with content.
// Callers provide raw transcript line coordinates.
func (m *MessageList) SetImageAnchors(anchors []nativeImageAnchor) {
	m.imageAnchors = append(m.imageAnchors[:0], anchors...)
}

// SetContent sets the viewport's text content
func (m *MessageList) SetContent(s string) {
	// CRITICAL: ALWAYS invalidate cache first to prevent stale cache bugs
	// Even if content hash matches, viewport state may have changed or hash collision occurred
	m.cachedDirty = true

	// Normalize line endings first so the comparison below is against the same
	// form we store. strings.ReplaceAll returns s untouched (no allocation)
	// when there is no "\r\n", which is the normal case for rendered output.
	s = strings.ReplaceAll(s, "\r\n", "\n")

	// Identity check instead of a hash. lastContentHash is only ever compared
	// for equality (prewrap dispatch/validation, render cache guards), never
	// used as a hash of specific bytes — so a monotonic counter is a drop-in
	// replacement and cannot collide.
	//
	// quickHash was FNV-1a over the ENTIRE viewport string on every SetContent,
	// i.e. once per frame: a byte-at-a-time loop with a multiply. A Go string
	// compare checks length first and then uses memequal, which short-circuits
	// on equal data pointers (the common cache-hit case) and is SIMD otherwise.
	if s == m.content && len(m.lines) > 0 {
		// Content unchanged, just ensure scroll position is valid
		// NOTE: Do NOT call GotoBottom() here — let the caller handle scroll restoration
		// to avoid double-GotoBottom() calls that cause scroll jank
		logDebug("[CACHE] SetContent: content unchanged, skipping line reprocessing (caches invalidated)")
		return
	}
	// Bump the content generation. Consumers only test this for equality.
	m.lastContentHash++
	logDebug("[CACHE] SetContent: content changed, full reprocessing")

	m.content = s

	// Build line offsets for O(1) line access (Crush technique)
	// This avoids strings.Split allocation and allows direct slicing
	m.lineOffsets = make([]int, 0, strings.Count(s, "\n")+1)
	m.lineOffsets = append(m.lineOffsets, 0)

	offset := 0
	for {
		idx := strings.IndexByte(s[offset:], '\n')
		if idx == -1 {
			break
		}
		offset += idx + 1
		m.lineOffsets = append(m.lineOffsets, offset)
	}

	// Also keep lines slice for compatibility (but as slices into content)
	newLines := strings.Split(s, "\n")

	// Clear the selection only when content changed in a way that actually
	// invalidates it. Unconditionally clearing on every content-hash mismatch
	// (the previous behavior) broke the common case of a response streaming
	// into the transcript while the user has text selected further up: every
	// incremental append fired this path and wiped the selection, making it
	// look like the selection "disappeared" while streaming (reported live).
	// Selection coordinates are raw line/byte-column indices into m.lines,
	// and only the currently-streaming (last) message is re-rendered each
	// tick — everything above it reuses cached, byte-identical lines — so
	// appending new lines/text after the selected range does not perturb
	// anything the selection actually points at.
	if m.Selection.Active && !selectionSurvivesContentChange(m.lines, newLines, m.Selection) {
		m.ClearSelection()
	}

	m.lines = newLines
	m.cachedLineCount = len(m.lines)

	// Only calculate longest line width if horizontal scrolling is needed
	// This is expensive for large content, so we defer it
	m.longestLineWidth = 0 // Reset - will be calculated lazily if needed

	// CRITICAL: After updating content, clamp the current offset to ensure it's still valid.
	// Use SetYOffset to apply proper clamping, which will mark cache dirty if offset changed.
	// This ensures that if content shrank, we don't stay scrolled past the end.
	// The caller (updateViewportIncremental/updateViewport) will then restore scroll position
	// via explicit GotoBottom() or SetYOffset() calls, avoiding duplicate calls.
	oldOffset := m.YOffset
	m.SetYOffset(m.YOffset) // This clamps to new valid range [0, maxYOffset]

	// Log if clamping happened to help debug scroll issues
	if m.YOffset != oldOffset {
		logDebug("[SCROLL] SetContent clamped offset from %d to %d (new maxOffset=%d, lineCount=%d)",
			oldOffset, m.YOffset, m.maxYOffset(), len(m.lines))
	}
}

// ============================================================================
// PRE-WRAP INTERFACE (used by App.Update via tea.Cmd)
// ============================================================================

// NeedsPrewrap returns true when the pre-wrapped cache is absent or stale.
// The App checks this at the end of every Update() call to decide whether to
// dispatch a background prewrapViewportCmd.
func (m *MessageList) NeedsPrewrap(width int) bool {
	return len(m.content) > 0 &&
		(len(m.preWrappedLines) == 0 ||
			m.preWrappedHash != m.lastContentHash ||
			m.preWrappedWidth != width)
}

// LinesForPrewrap returns the already-split lines slice and its hash for the
// background prewrapViewportCmd. The caller captures the slice header; because
// SetContent always replaces m.lines with a new allocation, the goroutine
// safely reads the old backing array even after a concurrent SetContent call.
func (m *MessageList) LinesForPrewrap() (lines []string, hash uint64) {
	return m.lines, m.lastContentHash
}

// SetPreWrappedLines stores the result delivered by viewportPrewrappedMsg.
// If the hash no longer matches the current content the result is stale and
// discarded; the next NeedsPrewrap check will dispatch a fresh cmd.
func (m *MessageList) SetPreWrappedLines(lines []string, mapping []int, width int, hash uint64) {
	if hash != m.lastContentHash {
		return // Content changed while cmd was in flight — discard stale result.
	}

	// CANONICAL SPACE CONVERSION: YOffset means a RAW m.lines index while the
	// fallback synchronous-wrap renderer is active, but a WRAPPED
	// preWrappedLines index once the pre-wrapped renderer takes over — see
	// prewrapActive(). Any width or content change invalidates the previous
	// pre-wrap match, which forces View() onto the fallback (raw-space)
	// renderer for the frames leading up to THIS call — the only exception is
	// a redundant re-application of data that was already current. Detect
	// that one exception; in every other case convert the offset via the
	// raw->wrapped mapping instead of re-clamping the raw number as if it
	// were already wrapped-space. Skipping this conversion is what produced
	// the visible flash/jitter whenever a background pre-wrap landed while
	// the user was scrolled away — confirmed live via SWARM_SCROLL_DEBUG on a
	// Tab (side-panel) width change: raw=1428 lines became wrapped=1667 lines,
	// and the same numeric offset silently pointed at different content.
	wasAlreadyWrapped := m.preWrappedWidth == width && m.preWrappedHash == hash && len(m.preWrappedLines) > 0

	m.preWrappedLines = lines
	m.preWrappedMapping = mapping
	m.preWrappedWidth = width
	m.preWrappedHash = hash
	m.cachedDirty = true

	newOffset := m.YOffset
	if !wasAlreadyWrapped && len(mapping) > 0 {
		idx := newOffset
		if idx < 0 {
			idx = 0
		}
		if idx >= len(mapping) {
			idx = len(mapping) - 1
		}
		newOffset = mapping[idx]
	}
	m.SetYOffset(newOffset)
}

// quickHash generates a fast hash for content change detection
func quickHash(s string) uint64 {
	// FNV-1a hash - fast and good distribution
	var hash uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		hash ^= uint64(s[i])
		hash *= 1099511628211
	}
	return hash
}

// SetSize sets viewport dimensions
func (m *MessageList) SetSize(width, height int) {
	if m.Width != width || m.Height != height {
		m.cachedDirty = true
	}
	m.Width = width
	m.Height = height
}

// SetOrigin sets the viewport's top-left screen position for mouse mapping.
func (m *MessageList) SetOrigin(x, y int) {
	m.OriginX = x
	m.OriginY = y
}

// TotalLineCount returns total number of lines
func (m MessageList) TotalLineCount() int {
	return len(m.lines)
}

// VisibleLineCount returns number of visible lines
func (m MessageList) VisibleLineCount() int {
	return len(m.visibleLines())
}

func (m MessageList) visibleLines() []string {
	h := m.Height - m.Style.GetVerticalFrameSize()
	if h < 0 {
		h = 0
	}

	if len(m.lines) == 0 {
		return nil // Avoid allocation
	}

	top := m.YOffset
	if top < 0 {
		top = 0
	}
	if top > len(m.lines) {
		top = len(m.lines)
	}

	bottom := m.YOffset + h
	if bottom > len(m.lines) {
		bottom = len(m.lines)
	}
	if bottom < top {
		bottom = top
	}

	// Return slice of existing array - no allocation
	// Only allocate if horizontal scrolling is active
	if m.xOffset > 0 {
		w := m.Width - m.Style.GetHorizontalFrameSize()
		if w > 0 && m.longestLineWidth > w {
			cutLines := make([]string, bottom-top)
			for i, line := range m.lines[top:bottom] {
				if m.xOffset < len(line) {
					end := m.xOffset + w
					if end > len(line) {
						end = len(line)
					}
					cutLines[i] = line[m.xOffset:end]
				}
				// Empty string is zero value, no need to set
			}
			return cutLines
		}
	}

	return m.lines[top:bottom]
}

// ============================================================================
// UPDATE - MESSAGE HANDLING
// ============================================================================

// Update handles bubbletea messages
func (m *MessageList) Update(msg tea.Msg) tea.Cmd {
	// No mouse/selection handling — keyboard scrolling is handled by the parent App
	_ = msg
	return nil
}
