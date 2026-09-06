# Message Rendering Architecture Analysis

## Executive Summary

The TUI has **3 DISTINCT RENDERING CACHES** that operate at different levels:

1. **Per-Message Cache** (`MessageRenderCache` in `message_cache.go`) - HIGHEST LEVEL
2. **MessageList View Cache** (`cachedView` in `messagelist.go`) - MIDDLE LEVEL  
3. **MessageList Selection Cache** (`cachedSelectionView` in `messagelist.go`) - MIDDLE LEVEL

These caches work at different layers and serve different optimization purposes.

---

## The Three Rendering Paths

### Path 1: Per-Message Cache (Highest Level - NOT CURRENTLY USED)
**File**: `internal/chat/message_cache.go`
**Status**: ⚠️ **ALLOCATED BUT NOT ACTIVELY USED IN RENDERING**

```go
// Initialized in app_init.go:309
messageCache: NewMessageRenderCache(), // Per-message render cache (Crush technique)
```

**Purpose**: Cache individual message rendering to avoid re-rendering unchanged messages
**Data Structure**:
- Maps message index → rendered output
- Stores: view string, height, line positions, content hash, focus state, width
- Thread-safe with RWMutex
- Evicts old messages when cache exceeds 500 entries

**Problem**: Despite being initialized, grep searches show **NO USAGE** of `messageCache.Get()` or `messageCache.Set()` in the actual rendering pipeline!

---

### Path 2: MessageList Fast Render Cache (Middle Level)
**File**: `internal/chat/messagelist.go:895-937`
**Trigger**: When `Selection.Active == false` (no text selection)

```go
func (m *MessageList) View() string {
    // FAST PATH: When no selection is active, skip expensive cellbuf rendering
    if !m.Selection.Active {
        return m.renderFast(h)  // ← Uses cachedView
    }
    // ...
}

func (m *MessageList) renderFast(h int) string {
    // Check fast-path cache using content hash for validation
    if !m.cachedViewDirty &&
        m.cachedView != "" &&
        m.cachedViewOffset == m.YOffset &&
        m.cachedViewWidth == w &&
        m.cachedViewHeight == h &&
        !m.Selection.Active &&
        m.cachedContentHash == wrapped.contentHash {
        return m.cachedView  // ← CACHE HIT
    }
    
    // ULTRA-FAST PATH: During rapid scrolling, skip wrapping
    if m.IsFastScrolling() {
        return m.renderUltraFast(h, w)  // ← Even faster, no wrapping
    }
    
    // Full render and cache result
    result := wrapped.content
    m.cachedView = result
    m.cachedViewOffset = m.YOffset
    m.cachedViewWidth = w
    m.cachedViewHeight = h
    m.cachedContentHash = wrapped.contentHash
    m.cachedViewDirty = false
    return result
}
```

**Cache Invalidation**:
- Content changes: `cachedViewDirty = true`
- Scroll position changes: check `cachedViewOffset`
- Window resize: check `cachedViewWidth/Height`
- Content hash mismatch: check `cachedContentHash`

---

### Path 3: MessageList Selection Render Cache (Middle Level)
**File**: `internal/chat/messagelist.go:988-1056`
**Trigger**: When `Selection.Active == true` (user selecting text)

```go
func (m *MessageList) View() string {
    if !m.Selection.Active {
        return m.renderFast(h)
    }
    // SLOW PATH: Use cellbuf for selection highlighting
    return m.renderWithCellbufSelection(h)  // ← Uses cachedSelectionView
}

func (m *MessageList) renderWithCellbufSelection(h int) string {
    // Use dedicated selection cache to prevent incorrect cache hits
    cacheValid := !m.cachedSelectionDirty &&
        m.cachedSelectionView != "" &&
        m.cachedSelectionOffset == m.YOffset &&
        m.cachedSelectionWidth == w &&
        m.cachedSelectionHeight == h &&
        m.cachedSelectionState == m.Selection &&
        m.cachedSelectionContentHash == wrapped.contentHash
    
    if cacheValid {
        return m.cachedSelectionView  // ← CACHE HIT
    }
    
    // Full cellbuf render with selection highlighting
    buf := cellbuf.NewBuffer(w, h)
    cellbuf.NewStyledString(content).Draw(buf, area)
    buf.ApplySelection(selRect, selStyle, getSelectionIgnoreMap())
    result := buf.Render()
    
    // Update separate selection cache
    m.cachedSelectionView = result
    m.cachedSelectionOffset = m.YOffset
    // ...
    return result
}
```

**Why Separate Cache?**
The comment explains it clearly:
```go
// Use dedicated selection cache to prevent incorrect cache hits from fast path
```

Selection rendering uses expensive cellbuf operations, so it needs its own cache to avoid re-rendering when only selection state changes.

---

## The Flow: From Messages to Screen

```
1. App.updateViewportContent() [app_chat_render.go:1528]
   ├─> Checks: viewportContentDirty, message count, last message length
   ├─> Cache Hit: Use viewportCachedLines (string[])
   └─> Cache Miss: Render all messages
       ├─> renderMessageListWithPositions() [app_chat_render.go:1495]
       │   └─> renderMessageListWithContext() [app_chat_render.go:517]
       │       └─> Individual message rendering (tools, blocks, etc.)
       └─> Store in viewportCachedLines

2. App.renderChatContent() [app_chat_render.go:27]
   ├─> Calls: updateViewportContent()
   └─> Gets viewport view: msgViewport.View()

3. MessageList.View() [messagelist.go:872]
   ├─> Decision: Selection.Active?
   ├─> NO  → renderFast() → uses cachedView
   └─> YES → renderWithCellbufSelection() → uses cachedSelectionView
```

---

## Why Multiple Caches Exist

### 1. Per-Message Cache (MessageRenderCache)
**Optimization Target**: Avoid re-rendering individual messages
**Use Case**: When scrolling through long conversations, unchanged messages shouldn't be re-rendered
**Status**: **CURRENTLY UNUSED** - This is the potential optimization opportunity!

### 2. Viewport Content Cache (viewportCachedLines)
**Optimization Target**: Avoid rebuilding entire message list
**Use Case**: When viewport updates but messages haven't changed
**Location**: `App.updateViewportContent()`

### 3. MessageList View Caches (cachedView + cachedSelectionView)
**Optimization Target**: Avoid wrapping/cellbuf operations on every frame
**Use Case**: 
- `cachedView`: Fast scrolling without selection
- `cachedSelectionView`: Selection rendering without re-computing cellbuf

---

## The "Click Fixes Everything" Bug

**Observed Behavior**: When you click on the screen, everything renders properly

**Root Cause Analysis**:

Looking at `messagelist.go:863`:
```go
func (ml *MessageList) handleMouseDrag(mouse tea.MouseMsg) tea.Cmd {
    // ...
    m.cachedSelectionDirty = true // Invalidate selection cache
    return nil
}
```

And in `messagelist.go:270` and `353`:
```go
func (m *MessageList) SetContent(s string) {
    // ...
    m.cachedViewDirty = true
}

func (m *MessageList) GotoBottom() {
    // ...
    m.cachedViewDirty = true // Mark view cache as dirty
}
```

**Hypothesis**: 
1. During scrolling or content updates, one of the caches gets stale
2. The cache validation logic misses some edge case
3. When clicking, mouse event handlers mark caches dirty
4. Next render does a full rebuild and shows correct content

**Likely Culprit**: 
The `cachedContentHash` validation in `renderFast()` and `renderWithCellbufSelection()`. The hash might not be detecting certain content changes (like streaming updates, tool results, etc.)

---

## Recommendations

### Immediate Fix
1. **Add aggressive cache invalidation** on any message content change:
   ```go
   func (a *App) markViewportDirty() {
       a.viewportContentDirty = true
       a.msgViewport.cachedViewDirty = true
       a.msgViewport.cachedSelectionDirty = true
   }
   ```

2. **Debug the content hash** - add logging to see when it's changing:
   ```go
   oldHash := m.cachedContentHash
   newHash := wrapped.contentHash
   if oldHash != newHash {
       logDebug("[RENDER] Content hash changed: %d → %d", oldHash, newHash)
   }
   ```

### Long-term Optimization
1. **Actually use the MessageRenderCache**:
   - Modify `renderMessageListWithContext()` to check per-message cache
   - Cache individual message render output
   - Only re-render messages that changed

2. **Better cache invalidation**:
   - Track message modification timestamps
   - Use more sophisticated content hashing (include tool results, streaming state)
   - Add explicit "dirty ranges" instead of all-or-nothing

3. **Unified cache strategy**:
   - Consider if we need both viewport-level and messagelist-level caches
   - Might be redundant - pick one level and optimize it well

---

## Files to Investigate

1. `internal/chat/message_cache.go` - Unused per-message cache
2. `internal/chat/messagelist.go:872-1056` - View rendering with dual caches
3. `internal/chat/app_chat_render.go:1528` - Viewport content management
4. `internal/chat/app_update.go` - Where cache invalidation should happen

## Next Steps

1. Add debug logging to track cache hits/misses
2. Reproduce the "click fixes it" bug with logging enabled
3. Identify which cache is serving stale content
4. Fix the validation logic or make invalidation more aggressive
