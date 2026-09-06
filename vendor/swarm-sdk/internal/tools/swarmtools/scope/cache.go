package scope

import (
	"fmt"
	"sync"
)

// CachedFileState holds the hashed-line snapshot from the last Read/Grep
// for a specific file. This represents what the agent last saw.
type CachedFileState struct {
	Lines []HashedLine
}

// FileHashCache is a thread-safe in-memory cache mapping file paths to their
// most recently computed hashed-line states. This allows the Edit tool to fall
// back to content-based matching when scope/ordinal shifts cause hash mismatches
// on lines whose actual content hasn't changed.
//
// Lifecycle: lives in memory for the duration of the process. No disk I/O.
type FileHashCache struct {
	mu    sync.RWMutex
	files map[string]*CachedFileState
}

// NewFileHashCache creates a new empty cache.
func NewFileHashCache() *FileHashCache {
	return &FileHashCache{
		files: make(map[string]*CachedFileState),
	}
}

// Store saves the hashed-line state for a file path.
// Replaces any previously cached state. The slice is copied defensively.
func (c *FileHashCache) Store(absPath string, lines []HashedLine) {
	cp := make([]HashedLine, len(lines))
	copy(cp, lines)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.files[absPath] = &CachedFileState{Lines: cp}
}

// Get retrieves the cached hashed-line state for a file path.
// Returns nil if no cached state exists.
func (c *FileHashCache) Get(absPath string) *CachedFileState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.files[absPath]
}

// Delete removes the cached state for a file path.
func (c *FileHashCache) Delete(absPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.files, absPath)
}

// --- Package-level singleton ---

var (
	globalCache     *FileHashCache
	globalCacheOnce sync.Once
)

// GlobalFileHashCache returns the package-level singleton cache.
func GlobalFileHashCache() *FileHashCache {
	globalCacheOnce.Do(func() {
		globalCache = NewFileHashCache()
	})
	return globalCache
}

// ResetGlobalFileHashCache resets the singleton for testing.
func ResetGlobalFileHashCache() {
	globalCacheOnce = sync.Once{}
	globalCache = nil
}

// --- Cache-aware resolution wrappers ---

// FindLineByHashWithCache resolves a line by hash, falling back to the
// cached state for content-based matching if the hash is not found.
//
// Resolution strategy (ordered):
//  1. Try FindLineByHash on the current hashed lines (pure, no cache).
//  2. If hash not found AND a cached state exists for the file:
//     a. Look up the hash in the cached state to find the original content.
//     b. Search the current file for that exact content near the hinted position.
//  3. Return error only if both strategies fail.
//
// The cache parameter may be nil, in which case this is equivalent to FindLineByHash.
func FindLineByHashWithCache(
	currentLines []HashedLine,
	hintLine int,
	hash string,
	cache *FileHashCache,
	absPath string,
) (*ResolveResult, error) {
	// Strategy 1+2: pure hash resolution (exact + spiral)
	result, err := FindLineByHash(currentLines, hintLine, hash)
	if err == nil {
		return result, nil
	}

	// Strategy 3: cache fallback
	if cache == nil {
		return nil, err
	}

	cached := cache.Get(absPath)
	if cached == nil {
		return nil, err
	}

	// Find the hash in the cached state to determine original content
	content, found := lookupContentByHash(cached.Lines, hintLine, hash)
	if !found {
		return nil, err
	}

	// Search current file for that exact content near the hint
	contentResult := findLineByContent(currentLines, hintLine, content)
	if contentResult == nil {
		return nil, fmt.Errorf(
			"hash %q not found in file, and cached content %q no longer exists (%d lines)",
			hash, truncateForError(content, 60), len(currentLines),
		)
	}

	return contentResult, nil
}

// FindLineByHashInRangeWithCache is like FindLineByHashWithCache but for
// resolving the end of a range, only accepting matches at or after afterLine.
func FindLineByHashInRangeWithCache(
	currentLines []HashedLine,
	hintLine int,
	hash string,
	afterLine int,
	cache *FileHashCache,
	absPath string,
) (*ResolveResult, error) {
	// Strategy 1+2: pure hash resolution
	result, err := FindLineByHashInRange(currentLines, hintLine, hash, afterLine)
	if err == nil {
		return result, nil
	}

	// Strategy 3: cache fallback
	if cache == nil {
		return nil, err
	}

	cached := cache.Get(absPath)
	if cached == nil {
		return nil, err
	}

	content, found := lookupContentByHash(cached.Lines, hintLine, hash)
	if !found {
		return nil, err
	}

	contentResult := findLineByContentInRange(currentLines, hintLine, content, afterLine)
	if contentResult == nil {
		return nil, fmt.Errorf(
			"hash %q not found at or after line %d, and cached content %q not found either",
			hash, afterLine, truncateForError(content, 60),
		)
	}

	return contentResult, nil
}

// --- Internal helpers ---

// lookupContentByHash finds the content string for a given hash in a
// hashed-line slice. Uses the hint line for fast lookup, then spirals outward.
func lookupContentByHash(lines []HashedLine, hintLine int, hash string) (string, bool) {
	if len(lines) == 0 {
		return "", false
	}

	if hintLine < 1 {
		hintLine = 1
	}
	if hintLine > len(lines) {
		hintLine = len(lines)
	}

	idx := hintLine - 1
	if lines[idx].Hash == hash {
		return lines[idx].Content, true
	}

	for delta := 1; delta <= len(lines); delta++ {
		above := idx - delta
		if above >= 0 && lines[above].Hash == hash {
			return lines[above].Content, true
		}
		below := idx + delta
		if below < len(lines) && lines[below].Hash == hash {
			return lines[below].Content, true
		}
		if above < 0 && below >= len(lines) {
			break
		}
	}

	return "", false
}

// findLineByContent searches for a line with exactly matching content
// near the hinted position using an outward spiral.
func findLineByContent(currentLines []HashedLine, hintLine int, content string) *ResolveResult {
	if len(currentLines) == 0 {
		return nil
	}

	if hintLine < 1 {
		hintLine = 1
	}
	if hintLine > len(currentLines) {
		hintLine = len(currentLines)
	}

	idx := hintLine - 1

	if currentLines[idx].Content == content {
		return &ResolveResult{Line: hintLine, Shift: 0, Method: "content_exact"}
	}

	for delta := 1; delta <= len(currentLines); delta++ {
		above := idx - delta
		if above >= 0 && currentLines[above].Content == content {
			return &ResolveResult{Line: above + 1, Shift: -delta, Method: "content_nearby"}
		}
		below := idx + delta
		if below < len(currentLines) && currentLines[below].Content == content {
			return &ResolveResult{Line: below + 1, Shift: delta, Method: "content_nearby"}
		}
		if above < 0 && below >= len(currentLines) {
			break
		}
	}

	return nil
}

// findLineByContentInRange is like findLineByContent but only accepts
// matches at or after afterLine.
func findLineByContentInRange(currentLines []HashedLine, hintLine int, content string, afterLine int) *ResolveResult {
	if len(currentLines) == 0 {
		return nil
	}

	if hintLine < 1 {
		hintLine = 1
	}
	if hintLine > len(currentLines) {
		hintLine = len(currentLines)
	}

	idx := hintLine - 1
	minIdx := afterLine - 1

	if idx >= minIdx && currentLines[idx].Content == content {
		return &ResolveResult{Line: hintLine, Shift: 0, Method: "content_exact"}
	}

	for delta := 1; delta <= len(currentLines); delta++ {
		above := idx - delta
		if above >= minIdx && above >= 0 && currentLines[above].Content == content {
			return &ResolveResult{Line: above + 1, Shift: -delta, Method: "content_nearby"}
		}
		below := idx + delta
		if below < len(currentLines) && currentLines[below].Content == content {
			return &ResolveResult{Line: below + 1, Shift: delta, Method: "content_nearby"}
		}
		if above < 0 && below >= len(currentLines) {
			break
		}
	}

	return nil
}

// truncateForError truncates a string for use in error messages.
func truncateForError(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
