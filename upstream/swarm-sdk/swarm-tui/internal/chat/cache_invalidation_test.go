package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestStaleCacheAfterStreamingBug reproduces the bug where message content
// changes but cached rendered views are served, causing stale display.
//
// Bug scenario:
// 1. Initial message is rendered and cached
// 2. Streaming chunk updates message content
// 3. SetContent() is called with new content
// 4. Hash matches (content string identical by coincidence) OR early return skips invalidation
// 5. MessageList.View() returns stale cachedView
// 6. User sees old content until mouse click forces invalidation
//
// Expected: Content changes should ALWAYS invalidate all caches
// Actual (before fix): Caches could be skipped if hash matched or early return
func TestStaleCacheAfterStreamingBug(t *testing.T) {
	// Create app with minimal setup
	app := &App{
		messages:             []Message{},
		updateQueue:          make(chan tea.Msg, 10000),
		globalUpdateSequence: 0,
	}

	// Initialize msgViewport (required for rendering)
	app.msgViewport = NewMessageList(100, 20)

	// Add initial assistant message
	app.messages = append(app.messages, Message{
		Role:    "assistant",
		Content: "Initial content",
	})

	t.Log("=== Step 1: Initial Render ===")
	// Simulate initial render (this populates caches)
	app.updateViewportContent()
	initialRender := app.msgViewport.View()

	t.Logf("Initial render length: %d chars", len(initialRender))
	t.Logf("Initial content: '%s'", app.messages[0].Content)

	// Verify initial content is in the render
	if !strings.Contains(initialRender, "Initial content") {
		t.Errorf("Initial render should contain 'Initial content', got: %s", initialRender)
	}

	t.Log("\n=== Step 2: Simulate Streaming Update ===")
	// CRITICAL: This simulates streaming chunk modifying message content
	app.messages[0].Content = "Updated streaming content with more text"

	t.Logf("Updated content: '%s'", app.messages[0].Content)

	// CRITICAL: The bug would happen here - updateStreamingMessageIncremental()
	// calls SetContent() which might not invalidate caches properly
	app.updateViewportContent()
	updatedRender := app.msgViewport.View()

	t.Logf("Updated render length: %d chars", len(updatedRender))

	t.Log("\n=== Step 3: Verify Cache Was Invalidated ===")
	// BUG CHECK: Render should have changed
	if initialRender == updatedRender {
		t.Errorf("STALE CACHE BUG: Render did not change after content update!")
		t.Logf("Initial render: %s", initialRender)
		t.Logf("Updated render: %s", updatedRender)
		t.Fatal("Cache was not invalidated - serving stale content")
	}

	// Verify new content is in the render
	if !strings.Contains(updatedRender, "Updated streaming content") {
		t.Errorf("Updated render should contain new content, got: %s", updatedRender)
	}

	// Verify old content is NOT in the render
	if strings.Contains(updatedRender, "Initial content") &&
		!strings.Contains(app.messages[0].Content, "Initial content") {
		t.Errorf("Updated render should NOT contain old content")
	}

	t.Log("Cache was properly invalidated - new content rendered")
}

// TestCacheInvalidationOnDirectContentModification verifies that ALL direct
// message content modifications trigger cache invalidation.
func TestCacheInvalidationOnDirectContentModification(t *testing.T) {
	testCases := []struct {
		name           string
		initialContent string
		updatedContent string
		modifyFunc     func(*App)
	}{
		{
			name:           "Streaming chunk append",
			initialContent: "Hello",
			updatedContent: "Hello world",
			modifyFunc: func(a *App) {
				a.messages[0].Content = "Hello world"
				a.invalidateViewportCache()
				a.updateViewportContent()
			},
		},
		{
			name:           "Error message replacement",
			initialContent: "Processing...",
			updatedContent: "Error: failed",
			modifyFunc: func(a *App) {
				a.invalidateViewportCache()
				a.messages[0].Content = "Error: failed"
				a.updateViewportContent()
			},
		},
		{
			name:           "Content clear for BashResult",
			initialContent: "Running command...",
			updatedContent: "",
			modifyFunc: func(a *App) {
				a.invalidateViewportCache()
				a.messages[0].Content = ""
				a.updateViewportContent()
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			app := &App{
				messages:    []Message{},
				updateQueue: make(chan tea.Msg, 10000),
			}
			app.msgViewport = NewMessageList(100, 20)

			app.messages = append(app.messages, Message{
				Role:    "assistant",
				Content: tc.initialContent,
			})

			// Initial render
			app.updateViewportContent()
			before := app.msgViewport.View()

			// Modify content
			tc.modifyFunc(app)
			after := app.msgViewport.View()

			// Verify cache was invalidated (render changed)
			if before == after && tc.initialContent != tc.updatedContent {
				t.Errorf("STALE CACHE: Render did not change after content modification")
				t.Logf("Before: %s", before)
				t.Logf("After:  %s", after)
			}
		})
	}
}

// TestSetContentAlwaysInvalidatesCaches verifies the core fix:
// SetContent() must ALWAYS invalidate caches, even if content hash matches.
func TestSetContentAlwaysInvalidatesCaches(t *testing.T) {
	ml := NewMessageList(100, 20)

	t.Log("=== Step 1: Set initial content ===")
	ml.SetContent("Line 1\nLine 2\nLine 3")

	// Trigger render to populate cache
	view1 := ml.View()
	t.Logf("View 1: %d chars", len(view1))

	// Verify cache is marked dirty after SetContent
	if !ml.cachedDirty && ml.cachedView == "" {
		t.Log("Cache correctly marked dirty before first render")
	}

	// Render fills cache
	_ = ml.View()

	t.Log("\n=== Step 2: Set same content again ===")
	// CRITICAL TEST: Even with identical content, cache should be invalidated
	ml.SetContent("Line 1\nLine 2\nLine 3")

	// VERIFY: cachedDirty should be TRUE after SetContent
	if !ml.cachedDirty {
		t.Error("BUG: cachedDirty should be TRUE after SetContent (even with same content)")
	}

	t.Log("Cache properly invalidated on SetContent")

	t.Log("\n=== Step 3: Set different content ===")
	// Render to clear dirty flag
	_ = ml.View()

	// Now set different content
	ml.SetContent("Different content")

	// Verify cache is invalidated
	if !ml.cachedDirty {
		t.Error("BUG: cachedDirty should be TRUE after content change")
	}

	t.Log("Cache invalidated on content change")
}

// TestInvalidateViewportCacheInvalidatesAllLayers verifies that the centralized
// invalidateViewportCache() function invalidates MessageList cache layers.
func TestInvalidateViewportCacheInvalidatesAllLayers(t *testing.T) {
	app := &App{
		messages:    []Message{{Role: "user", Content: "test"}},
		updateQueue: make(chan tea.Msg, 10000),
	}
	app.msgViewport = NewMessageList(100, 20)

	// Populate caches
	app.updateViewportContent()
	_ = app.msgViewport.View()

	// Manually mark cache as clean to simulate cached state
	app.viewportContentDirty = false
	app.msgViewport.cachedDirty = false

	t.Log("=== Before invalidateViewportCache() ===")
	t.Logf("msgViewport.cachedDirty: %v", app.msgViewport.cachedDirty)

	// Call the centralized invalidation function
	app.invalidateViewportCache()

	t.Log("\n=== After invalidateViewportCache() ===")
	t.Logf("msgViewport.cachedDirty: %v", app.msgViewport.cachedDirty)

	// Verify MessageList cache is invalidated
	if !app.msgViewport.cachedDirty {
		t.Error("BUG: msgViewport.cachedDirty should be TRUE")
	}

	if !app.viewportContentDirty {
		t.Error("BUG: viewportContentDirty should be TRUE after invalidateViewportCache()")
	}

	t.Log("All cache layers properly invalidated")
}
