package web_fetch

import (
	"sync"
	"time"
)

const (
	cacheTTL          = 15 * time.Minute
	maxCacheSizeBytes = 50 * 1024 * 1024 // 50 MB
)

type cacheEntry struct {
	content         string
	bytes           int
	code            int
	codeText        string
	contentType     string
	fetchedAt       time.Time
	bodyTruncated   bool
	outputTruncated bool
}

// urlCache is a simple TTL-based in-memory cache for fetched URL content.
// It evicts the oldest entries when the total size exceeds maxCacheSizeBytes.
type urlCache struct {
	mu        sync.Mutex
	entries   map[string]*cacheEntry
	order     []string // insertion order for LRU eviction
	totalSize int
}

func newURLCache() *urlCache {
	return &urlCache{
		entries: make(map[string]*cacheEntry),
		order:   make([]string, 0, 64),
	}
}

func (c *urlCache) get(url string) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[url]
	if !ok {
		return nil, false
	}
	if time.Since(entry.fetchedAt) > cacheTTL {
		c.evictLocked(url)
		return nil, false
	}
	return entry, true
}

func (c *urlCache) set(url string, entry *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict existing entry for this URL if present.
	if old, ok := c.entries[url]; ok {
		c.totalSize -= old.bytes
	} else {
		c.order = append(c.order, url)
	}

	c.entries[url] = entry
	c.totalSize += entry.bytes

	// Evict expired and oversized entries.
	c.sweepLocked()
}

// evictLocked removes a single URL entry. Caller must hold c.mu.
func (c *urlCache) evictLocked(url string) {
	if entry, ok := c.entries[url]; ok {
		c.totalSize -= entry.bytes
		delete(c.entries, url)
	}
	for i, u := range c.order {
		if u == url {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// sweepLocked evicts expired entries and, if still over budget, oldest entries.
// Caller must hold c.mu.
func (c *urlCache) sweepLocked() {
	now := time.Now()

	// First pass: evict expired.
	for url, entry := range c.entries {
		if now.Sub(entry.fetchedAt) > cacheTTL {
			c.totalSize -= entry.bytes
			delete(c.entries, url)
		}
	}

	// Rebuild order slice.
	clean := c.order[:0]
	for _, url := range c.order {
		if _, ok := c.entries[url]; ok {
			clean = append(clean, url)
		}
	}
	c.order = clean

	// Second pass: evict oldest until under budget.
	for c.totalSize > maxCacheSizeBytes && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		if entry, ok := c.entries[oldest]; ok {
			c.totalSize -= entry.bytes
			delete(c.entries, oldest)
		}
	}
}

// globalCache is the shared cache instance.
var globalCache = newURLCache()
