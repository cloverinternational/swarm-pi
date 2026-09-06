package viewport

import (
	"hash/fnv"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
)

// MaxMessageCacheSize is the maximum number of messages to cache
// before evicting oldest entries.
const MaxMessageCacheSize = 500

// RenderCache provides hash-based caching for viewport content.
// This avoids re-rendering when the visible content hasn't changed.
type RenderCache struct {
	content       string
	contentHash   uint64
	offsetHash    uint64
	selectionHash uint64
	valid         bool
}

// NewRenderCache creates a new render cache.
func NewRenderCache() *RenderCache {
	return &RenderCache{}
}

// IsValid checks if the cache matches the current state.
func (c *RenderCache) IsValid(lines []string, offset int, selection types.Selection) bool {
	if !c.valid {
		return false
	}

	contentHash := hashLines(lines)
	offsetHash := hashOffset(offset)
	selectionHash := hashSelection(selection)

	return c.contentHash == contentHash &&
		c.offsetHash == offsetHash &&
		c.selectionHash == selectionHash
}

// Get retrieves the cached content.
func (c *RenderCache) Get() string {
	return c.content
}

// Set updates the cache with new content and state hashes.
func (c *RenderCache) Set(content string, lines []string, offset int, selection types.Selection) {
	c.content = content
	c.contentHash = hashLines(lines)
	c.offsetHash = hashOffset(offset)
	c.selectionHash = hashSelection(selection)
	c.valid = true
}

// Invalidate marks the cache as invalid.
func (c *RenderCache) Invalidate() {
	c.valid = false
}

// hashLines computes a hash of the content lines.
func hashLines(lines []string) uint64 {
	h := fnv.New64a()
	for _, line := range lines {
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	return h.Sum64()
}

// hashOffset computes a hash of the scroll offset.
func hashOffset(offset int) uint64 {
	h := fnv.New64a()
	// Use all 8 bytes to support 64-bit integers
	h.Write([]byte{
		byte(offset >> 56),
		byte(offset >> 48),
		byte(offset >> 40),
		byte(offset >> 32),
		byte(offset >> 24),
		byte(offset >> 16),
		byte(offset >> 8),
		byte(offset),
	})
	return h.Sum64()
}

// hashSelection computes a hash of the selection state.
func hashSelection(sel types.Selection) uint64 {
	if !sel.Active {
		return 0
	}

	h := fnv.New64a()
	writeInt := func(n int) {
		// Use all 8 bytes to support 64-bit integers
		h.Write([]byte{
			byte(n >> 56),
			byte(n >> 48),
			byte(n >> 40),
			byte(n >> 32),
			byte(n >> 24),
			byte(n >> 16),
			byte(n >> 8),
			byte(n),
		})
	}

	writeInt(sel.StartLine)
	writeInt(sel.StartCol)
	writeInt(sel.EndLine)
	writeInt(sel.EndCol)

	return h.Sum64()
}

// MessageCache provides per-message render caching for large conversations.
// This allows caching individual message renders to avoid re-rendering
// the entire conversation when only one message changes.
type MessageCache struct {
	entries map[string]*MessageCacheEntry
}

// MessageCacheEntry holds cached render data for a single message.
type MessageCacheEntry struct {
	Lines        []string
	ContentHash  uint64
	BlockCount   int
	LastModified int64 // Unix timestamp of last modification
}

// NewMessageCache creates a new message cache.
func NewMessageCache() *MessageCache {
	return &MessageCache{
		entries: make(map[string]*MessageCacheEntry),
	}
}

// Get retrieves a cached message render.
func (c *MessageCache) Get(messageID string, contentHash uint64, blockCount int) ([]string, bool) {
	entry, ok := c.entries[messageID]
	if !ok {
		return nil, false
	}

	if entry.ContentHash != contentHash || entry.BlockCount != blockCount {
		return nil, false
	}

	return entry.Lines, true
}

// Set stores a message render in the cache.
func (c *MessageCache) Set(messageID string, lines []string, contentHash uint64, blockCount int) {
	// Evict oldest entries if cache is full
	if len(c.entries) >= MaxMessageCacheSize {
		c.evictOldest()
	}

	c.entries[messageID] = &MessageCacheEntry{
		Lines:        lines,
		ContentHash:  contentHash,
		BlockCount:   blockCount,
		LastModified: time.Now().Unix(),
	}
}

// evictOldest removes the oldest 10% of entries from the cache.
func (c *MessageCache) evictOldest() {
	if len(c.entries) == 0 {
		return
	}

	// Find oldest entries to evict (10% or at least 1)
	evictCount := max(len(c.entries)/10, 1)

	// Find entries with oldest LastModified
	type entryAge struct {
		id           string
		lastModified int64
	}
	var ages []entryAge
	for id, entry := range c.entries {
		ages = append(ages, entryAge{id, entry.LastModified})
	}

	// Simple sort: find the N oldest
	for i := 0; i < evictCount && i < len(ages); i++ {
		minIdx := i
		for j := i + 1; j < len(ages); j++ {
			if ages[j].lastModified < ages[minIdx].lastModified {
				minIdx = j
			}
		}
		if minIdx != i {
			ages[i], ages[minIdx] = ages[minIdx], ages[i]
		}
		delete(c.entries, ages[i].id)
	}
}

// Invalidate removes a specific message from the cache.
func (c *MessageCache) Invalidate(messageID string) {
	delete(c.entries, messageID)
}

// InvalidateAll clears the entire cache.
func (c *MessageCache) InvalidateAll() {
	c.entries = make(map[string]*MessageCacheEntry)
}

// Size returns the number of cached entries.
func (c *MessageCache) Size() int {
	return len(c.entries)
}
