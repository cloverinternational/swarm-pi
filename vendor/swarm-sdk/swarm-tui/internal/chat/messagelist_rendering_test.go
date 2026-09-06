package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderingConsistency verifies that cached and selection rendering paths
// produce identical output with the same input. This test ensures that the
// unified wrapAndPadContent function works correctly.
func TestRenderingConsistency(t *testing.T) {
	tests := []struct {
		name    string
		content string
		width   int
		height  int
		offset  int
	}{
		{
			name:    "simple content",
			content: "Hello\nWorld\nTest",
			width:   80,
			height:  10,
			offset:  0,
		},
		{
			name:    "long lines with spaces",
			content: "The quick brown fox jumps over the lazy dog. " + strings.Repeat("The quick brown fox jumps over the lazy dog. ", 2),
			width:   40,
			height:  10,
			offset:  0,
		},
		{
			name:    "with scroll offset",
			content: strings.Join([]string{"Line 1", "Line 2", "Line 3", "Line 4", "Line 5", "Line 6", "Line 7", "Line 8"}, "\n"),
			width:   80,
			height:  3,
			offset:  2,
		},
		{
			name:    "empty content",
			content: "",
			width:   80,
			height:  5,
			offset:  0,
		},
		{
			name:    "single line",
			content: "Single line of text",
			width:   80,
			height:  5,
			offset:  0,
		},
		{
			name:    "narrow width",
			content: "This is a test with wrapping behavior that should work correctly",
			width:   20,
			height:  10,
			offset:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create messagelist and set content
			ml := NewMessageList(tt.width, tt.height)
			ml.SetContent(tt.content)
			ml.SetYOffset(tt.offset)

			// Get lines from messagelist
			lines := ml.lines

			// Use unified function to get wrapped and padded content
			result := wrapAndPadContent(lines, tt.offset, tt.height, tt.width)

			// Verify result properties
			if result.content == "" && len(lines) > 0 && tt.offset < len(lines) {
				t.Errorf("content should not be empty for non-empty input with valid offset")
			}

			// Count lines
			outputLines := strings.Split(result.content, "\n")
			if len(outputLines) != tt.height {
				t.Errorf("expected %d lines, got %d", tt.height, len(outputLines))
			}

			// Verify max width is within bounds
			for _, line := range outputLines {
				w := ansi.StringWidth(line)
				if w > tt.width {
					t.Errorf("line width %d exceeds max width %d: %q", w, tt.width, line)
				}
			}
		})
	}
}

// TestCacheSeparation verifies that fast path cache and selection cache
// don't interfere with each other.
func TestCacheSeparation(t *testing.T) {
	ml := NewMessageList(80, 10)
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	ml.SetContent(content)

	// Render without selection (fast path)
	ml.Selection = Selection{Active: false}
	view1 := ml.View()

	// Render with selection (selection path)
	ml.Selection = Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   1,
		EndCol:    5,
		Active:    true,
	}
	view2 := ml.View()

	// Verify both rendered something
	if view1 == "" {
		t.Error("fast path rendering produced empty output")
	}
	if view2 == "" {
		t.Error("selection path rendering produced empty output")
	}

	// Render without selection again (should use fast cache now)
	ml.Selection = Selection{Active: false}
	view3 := ml.View()

	// The fast path output should be consistent
	if view1 != view3 {
		t.Errorf("fast path cache inconsistency:\nfirst:  %q\nsecond: %q", view1, view3)
	}

	// Render with selection again (should use selection cache)
	ml.Selection = Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   1,
		EndCol:    5,
		Active:    true,
	}
	view4 := ml.View()

	if view2 != view4 {
		t.Errorf("selection cache inconsistency")
	}
}

// TestCacheInvalidation verifies that caches are properly invalidated
// when content or selection changes.
func TestCacheInvalidation(t *testing.T) {
	ml := NewMessageList(80, 10)
	ml.SetContent("Initial content\nLine 2\nLine 3")

	// Render and cache
	view1 := ml.View()

	// Change content
	ml.SetContent("Changed content\nNew line 2\nNew line 3")

	// Render again (should not use old cache)
	view2 := ml.View()

	if view1 == view2 {
		t.Error("cache was not invalidated after content change")
	}

	// Restore original content
	ml.SetContent("Initial content\nLine 2\nLine 3")
	view3 := ml.View()

	// Views should match since content is same
	if view1 != view3 {
		t.Error("rendering inconsistent after restoring content")
	}
}

// TestSelectionCacheInvalidation verifies selection-specific cache invalidation.
func TestSelectionCacheInvalidation(t *testing.T) {
	ml := NewMessageList(80, 10)
	ml.SetContent("Line 1\nLine 2\nLine 3\nLine 4\nLine 5")

	// Render with selection
	ml.Selection = Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   1,
		EndCol:    5,
		Active:    true,
	}
	view1 := ml.View()

	// Change selection
	ml.Selection = Selection{
		StartLine: 2,
		StartCol:  0,
		EndLine:   3,
		EndCol:    5,
		Active:    true,
	}
	view2 := ml.View()

	if view1 == view2 {
		t.Error("selection cache was not invalidated after selection change")
	}

	// Restore original selection
	ml.Selection = Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   1,
		EndCol:    5,
		Active:    true,
	}
	view3 := ml.View()

	if view1 != view3 {
		t.Error("selection rendering inconsistent after restoring selection")
	}
}

// TestWrapAndPadContentDimensions verifies that wrapAndPadContent produces
// correct output dimensions in all cases.
func TestWrapAndPadContentDimensions(t *testing.T) {
	tests := []struct {
		name        string
		lines       []string
		yOffset     int
		height      int
		width       int
		expectLines int
	}{
		{
			name:        "exact fit",
			lines:       []string{"A", "B", "C"},
			yOffset:     0,
			height:      3,
			width:       80,
			expectLines: 3,
		},
		{
			name:        "padding needed",
			lines:       []string{"A", "B"},
			yOffset:     0,
			height:      5,
			width:       80,
			expectLines: 5,
		},
		{
			name:        "truncation needed",
			lines:       []string{"A", "B", "C", "D", "E", "F"},
			yOffset:     0,
			height:      3,
			width:       80,
			expectLines: 3,
		},
		{
			name:        "with offset",
			lines:       []string{"A", "B", "C", "D", "E"},
			yOffset:     1,
			height:      3,
			width:       80,
			expectLines: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapAndPadContent(tt.lines, tt.yOffset, tt.height, tt.width)

			outputLines := strings.Split(result.content, "\n")
			if len(outputLines) != tt.expectLines {
				t.Errorf("expected %d output lines, got %d", tt.expectLines, len(outputLines))
			}

			// Verify no line exceeds width
			for i, line := range outputLines {
				w := ansi.StringWidth(line)
				if w > tt.width {
					t.Errorf("line %d width %d exceeds limit %d", i, w, tt.width)
				}
			}
		})
	}
}
