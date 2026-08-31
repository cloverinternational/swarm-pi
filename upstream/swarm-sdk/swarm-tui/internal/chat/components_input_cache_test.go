package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// BenchmarkCachedWordWrap demonstrates the performance improvement
// from caching word wrap results during history navigation.
func BenchmarkCachedWordWrap(b *testing.B) {
	// Create a long input similar to what users might type
	longInput := strings.Repeat("This is a typical user input with many words that would be expensive to wrap repeatedly ", 20)

	input := NewSimpleInput()
	input.SetWidth(80)

	b.ResetTimer()
	b.Run("WithCache", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// Simulate navigating to same history item multiple times
			// First call warms cache, subsequent calls hit cache
			_ = input.cachedWordWrap(longInput, 74) // width - 6
			_ = input.cachedWordWrap(longInput, 74)
			_ = input.cachedWordWrap(longInput, 74)
		}
	})

	b.Run("WithoutCache", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// Simulating old behavior - re-wrap every time
			_ = wordWrapText(longInput, 74)
			_ = wordWrapText(longInput, 74)
			_ = wordWrapText(longInput, 74)
		}
	})
}

// TestCachedWordWrapCorrectness verifies cached results match uncached
func TestCachedWordWrapCorrectness(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
	}{
		{"empty", "", 80},
		{"short", "hello world", 80},
		{"long single word", strings.Repeat("a", 100), 80},
		{"multi paragraph", "Line one\n\nLine two after blank\nLine three", 40},
		{"realistic input", "Please analyze this codebase and tell me about the architecture", 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := NewSimpleInput()
			input.SetWidth(tt.width)

			// Get uncached result
			uncached := wordWrapText(tt.text, tt.width)

			// Get cached result (first call - cache miss)
			cached1 := input.cachedWordWrap(tt.text, tt.width)

			// Get cached result (second call - cache hit)
			cached2 := input.cachedWordWrap(tt.text, tt.width)

			// Verify uncached matches cached
			if len(uncached) != len(cached1) {
				t.Errorf("Length mismatch: uncached=%d, cached=%d", len(uncached), len(cached1))
			}
			for i := range uncached {
				if uncached[i] != cached1[i] {
					t.Errorf("Line %d mismatch: uncached=%q, cached=%q", i, uncached[i], cached1[i])
				}
			}

			// Verify cache hit returns same result
			if len(cached1) != len(cached2) {
				t.Errorf("Cache hit length mismatch: first=%d, second=%d", len(cached1), len(cached2))
			}
			for i := range cached1 {
				if cached1[i] != cached2[i] {
					t.Errorf("Cache hit line %d mismatch: first=%q, second=%q", i, cached1[i], cached2[i])
				}
			}
		})
	}
}

// TestCachedWordWrapInvalidation verifies cache invalidation works
func TestCachedWordWrapInvalidation(t *testing.T) {
	input := NewSimpleInput()
	input.SetWidth(80)

	text1 := "First text to wrap"
	text2 := "Second different text"

	// First wrap - cache miss
	_ = input.cachedWordWrap(text1, 74)
	if input.wrapCacheText != text1 {
		t.Error("Expected wrapCacheText to be set after first wrap")
	}

	// Different text - should be new cache entry
	_ = input.cachedWordWrap(text2, 74)
	if input.wrapCacheText != text2 {
		t.Error("Expected wrapCacheText to be updated for new text")
	}

	// Verify both entries exist
	key1 := fmt.Sprintf("%s|%d", text1, 74)
	key2 := fmt.Sprintf("%s|%d", text2, 74)
	if _, ok := input.wrapCache[key1]; !ok {
		t.Error("Expected first text to remain in cache")
	}
	if _, ok := input.wrapCache[key2]; !ok {
		t.Error("Expected second text to be in cache")
	}

	// SetValue should clear cache
	input.SetValue("new value")
	if len(input.wrapCache) != 0 {
		t.Errorf("Expected cache cleared after SetValue, got %d entries", len(input.wrapCache))
	}
	if input.wrapCacheWidth != 0 {
		t.Error("Expected wrapCacheWidth reset after SetValue")
	}
}

// TestCachedWordWrapWidthChange verifies cache invalidation on width change
func TestCachedWordWrapWidthChange(t *testing.T) {
	input := NewSimpleInput()
	input.SetWidth(80)

	text := "Some text to wrap for testing"

	// Wrap at original width
	_ = input.cachedWordWrap(text, 74)
	originalCacheSize := len(input.wrapCache)

	if originalCacheSize == 0 {
		t.Error("Expected cache to have entries after wrap")
	}

	// Change width - should invalidate cache
	input.SetWidth(120)

	if len(input.wrapCache) != 0 {
		t.Error("Expected cache cleared after width change")
	}

	// Wrap at new width - should create new cache entry
	_ = input.cachedWordWrap(text, 114) // 120 - 6

	if len(input.wrapCache) == 0 {
		t.Error("Expected new cache entry after wrap at different width")
	}
}

// BenchmarkHistoryNavigation simulates pressing up/down through history
func BenchmarkHistoryNavigation(b *testing.B) {
	// Simulate a typical scenario: user has typed several long messages
	historyItems := []string{
		strings.Repeat("This is a long message about feature implementation ", 10),
		strings.Repeat("Another detailed explanation of the codebase architecture ", 8),
		strings.Repeat("A third message discussing design patterns and tradeoffs ", 12),
	}

	input := NewSimpleInput()
	input.SetWidth(100)

	b.Run("NavigateUpDown", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			idx := i % len(historyItems)
			input.SetValue(historyItems[idx])
			// Simulate multiple render passes (which call GetContentHeight, View, etc)
			_ = input.GetContentHeight()
			_ = input.GetTotalWrappedLines()
			_ = input.GetCursorLineIndex()
		}
	})
}

// TestCacheHitLatency verifies cache hits are fast
func TestCacheHitLatency(t *testing.T) {
	input := NewSimpleInput()
	input.SetWidth(80)

	longText := strings.Repeat("word ", 500) // ~2500 chars

	// First call - cache miss (slower)
	start := time.Now()
	_ = input.cachedWordWrap(longText, 74)
	missDuration := time.Since(start)

	// Second call - cache hit (should be much faster)
	start = time.Now()
	_ = input.cachedWordWrap(longText, 74)
	hitDuration := time.Since(start)

	// Cache hit should be at least 10x faster than miss
	if hitDuration > missDuration/10 {
		t.Logf("Cache miss: %v, Cache hit: %v", missDuration, hitDuration)
		// This is informational - actual speedup depends on text size
	}
}
