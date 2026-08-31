// Package chat provides a chat-focused terminal UI
package chat

import (
	"strings"
	"sync"
)

// ============================================================================
// RENDERING PERFORMANCE CONSTANTS (Crush technique)
// ============================================================================

const (
	// maxCachedMessages is the maximum number of messages to keep in cache
	// Older messages beyond this limit are evicted
	maxCachedMessages = 500
)

// ============================================================================
// RENDERED MESSAGE CACHE (Crush technique)
// ============================================================================

// renderedMessage caches a single message's rendered output with position metadata
type renderedMessage struct {
	// height is the number of lines in the rendered output
	height int

	// startLine is the first line index in the full viewport
	startLine int

	// endLine is the last line index in the full viewport
	endLine int

	// isFocused tracks whether this was rendered with focus styling
	isFocused bool

	// width is the width at which this was rendered (for invalidation on resize)
	width int
}

// MessageRenderCache provides efficient caching for message rendering
type MessageRenderCache struct {
	mu sync.RWMutex

	// cache maps message index to rendered output
	cache map[int]*renderedMessage

	// lineToMessage maps line numbers to message indices for O(1) lookup
	lineToMessage []int

	// totalLines is the total number of lines across all cached messages
	totalLines int

	// lastWidth is the width at which the cache was last built
	lastWidth int

	// dirty tracks which message indices need re-rendering
	dirty map[int]bool
}

// NewMessageRenderCache creates a new message render cache
func NewMessageRenderCache() *MessageRenderCache {
	return &MessageRenderCache{
		cache:         make(map[int]*renderedMessage),
		lineToMessage: nil,
		dirty:         make(map[int]bool),
	}
}

// Get retrieves a cached rendered message, returns nil if not found or dirty
func (c *MessageRenderCache) Get(msgIdx int, isFocused bool, width int) *renderedMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rm, ok := c.cache[msgIdx]
	if !ok {
		return nil
	}

	// Check if cache is still valid
	if c.dirty[msgIdx] || rm.isFocused != isFocused || rm.width != width {
		return nil
	}

	return rm
}

// Set stores a rendered message in the cache
func (c *MessageRenderCache) Set(msgIdx int, rm *renderedMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[msgIdx] = rm
	delete(c.dirty, msgIdx)

	// Evict old entries if cache is too large
	if len(c.cache) > maxCachedMessages {
		// Find and remove oldest entries (lowest indices)
		minIdx := msgIdx
		for idx := range c.cache {
			if idx < minIdx {
				minIdx = idx
			}
		}
		if minIdx < msgIdx {
			delete(c.cache, minIdx)
		}
	}
}

// MarkDirty marks a specific message as needing re-render
func (c *MessageRenderCache) MarkDirty(msgIdx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dirty[msgIdx] = true
}

// MarkAllDirty marks all cached messages as needing re-render
func (c *MessageRenderCache) MarkAllDirty() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for idx := range c.cache {
		c.dirty[idx] = true
	}
}

// Clear removes all cached entries
func (c *MessageRenderCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[int]*renderedMessage)
	c.lineToMessage = nil
	c.dirty = make(map[int]bool)
	c.totalLines = 0
}

// InvalidateFrom marks all messages from the given index as dirty
// Used when a message changes and all subsequent messages might shift
func (c *MessageRenderCache) InvalidateFrom(msgIdx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for idx := range c.cache {
		if idx >= msgIdx {
			c.dirty[idx] = true
		}
	}
}

// UpdateLineMapping rebuilds the line-to-message mapping
func (c *MessageRenderCache) UpdateLineMapping(messages int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Calculate total lines and build mapping
	c.lineToMessage = nil
	c.totalLines = 0

	for msgIdx := range messages {
		rm, ok := c.cache[msgIdx]
		if !ok {
			continue
		}

		// Update the message's line positions
		rm.startLine = c.totalLines
		rm.endLine = c.totalLines + rm.height - 1

		// Add mapping entries for each line
		for i := 0; i < rm.height; i++ {
			c.lineToMessage = append(c.lineToMessage, msgIdx)
		}

		c.totalLines += rm.height
	}
}

// GetMessageAtLine returns the message index at a given line number
func (c *MessageRenderCache) GetMessageAtLine(line int) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if line < 0 || line >= len(c.lineToMessage) {
		return -1
	}
	return c.lineToMessage[line]
}

// GetVisibleMessages returns indices of messages visible in the viewport
func (c *MessageRenderCache) GetVisibleMessages(viewportStart, viewportEnd int) []int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	seen := make(map[int]bool)
	var visible []int

	for line := viewportStart; line <= viewportEnd && line < len(c.lineToMessage); line++ {
		if line < 0 {
			continue
		}
		msgIdx := c.lineToMessage[line]
		if !seen[msgIdx] {
			seen[msgIdx] = true
			visible = append(visible, msgIdx)
		}
	}

	return visible
}

// TotalLines returns the total number of rendered lines
func (c *MessageRenderCache) TotalLines() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totalLines
}

// SetLastWidth updates the width at which the cache was built
func (c *MessageRenderCache) SetLastWidth(width int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastWidth != width {
		c.lastWidth = width
		// Width change invalidates all cached entries
		for idx := range c.cache {
			c.dirty[idx] = true
		}
	}
}

// ============================================================================
// OPTIMIZED STRING BUILDING (Crush technique)
// ============================================================================

// MessageBuilder provides efficient string building for viewport content
type MessageBuilder struct {
	b            strings.Builder
	lineCount    int
	lineOffsets  []int
	messageEnds  []int // Line indices where each message ends
	currentWidth int
}

// NewMessageBuilder creates a new message builder with pre-allocation
func NewMessageBuilder(estimatedMessages, estimatedLinesPerMsg, width int) *MessageBuilder {
	mb := &MessageBuilder{
		lineOffsets:  make([]int, 0, estimatedMessages*estimatedLinesPerMsg),
		messageEnds:  make([]int, 0, estimatedMessages),
		currentWidth: width,
	}

	// Pre-allocate string builder capacity
	// Estimate: width chars per line * estimated total lines
	estimatedSize := width * estimatedMessages * estimatedLinesPerMsg
	mb.b.Grow(estimatedSize)

	// First line always starts at offset 0
	mb.lineOffsets = append(mb.lineOffsets, 0)

	return mb
}

// WriteMessage appends a message's rendered lines to the builder
func (mb *MessageBuilder) WriteMessage(lines []string) {
	for i, line := range lines {
		if mb.lineCount > 0 || i > 0 {
			mb.b.WriteByte('\n')
			mb.lineOffsets = append(mb.lineOffsets, mb.b.Len())
		}
		mb.b.WriteString(line)
		mb.lineCount++
	}

	// Record where this message ends
	mb.messageEnds = append(mb.messageEnds, mb.lineCount-1)
}

// WriteGap writes spacing between messages
func (mb *MessageBuilder) WriteGap(lines int) {
	if lines <= 0 {
		return
	}

	for range lines {
		mb.b.WriteByte('\n')
		mb.lineOffsets = append(mb.lineOffsets, mb.b.Len())
		mb.lineCount++
	}
}

// String returns the built content
func (mb *MessageBuilder) String() string {
	return mb.b.String()
}

// LineCount returns the total number of lines
func (mb *MessageBuilder) LineCount() int {
	return mb.lineCount
}

// LineOffsets returns the byte offsets for each line
func (mb *MessageBuilder) LineOffsets() []int {
	return mb.lineOffsets
}

// MessageEnds returns the line indices where each message ends
func (mb *MessageBuilder) MessageEnds() []int {
	return mb.messageEnds
}
