# Key Findings: Message Rendering Investigation

## 🔍 What We Found

### 1. **THREE SEPARATE RENDERING CACHES** exist (not just one!)

```
┌─────────────────────────────────────────────────────────────┐
│                    RENDERING PIPELINE                        │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌────────────────────────────────────────────────────┐     │
│  │ Level 1: Per-Message Cache (MessageRenderCache)    │     │
│  │ Location: internal/chat/message_cache.go           │     │
│  │ Status: ⚠️ ALLOCATED BUT NEVER USED! ⚠️              │     │
│  │ - Has Get() method → NEVER CALLED                  │     │
│  │ - Has Set() method → NEVER CALLED                  │     │
│  │ - Only MarkDirty() is called (2 places)            │     │
│  └────────────────────────────────────────────────────┘     │
│                          ↓                                   │
│  ┌────────────────────────────────────────────────────┐     │
│  │ Level 2: Viewport Content Cache                    │     │
│  │ Location: App.viewportCachedLines                  │     │
│  │ Status: ✅ ACTIVELY USED                            │     │
│  │ - Caches entire rendered message list              │     │
│  │ - Invalidated on message changes                   │     │
│  └────────────────────────────────────────────────────┘     │
│                          ↓                                   │
│  ┌────────────────────────────────────────────────────┐     │
│  │ Level 3a: MessageList Fast View Cache              │     │
│  │ Location: MessageList.cachedView                   │     │
│  │ Status: ✅ USED when Selection.Active = false       │     │
│  │ - Skips expensive cellbuf rendering                │     │
│  │ - Uses content hash for validation                 │     │
│  └────────────────────────────────────────────────────┘     │
│                                                              │
│  ┌────────────────────────────────────────────────────┐     │
│  │ Level 3b: MessageList Selection View Cache         │     │
│  │ Location: MessageList.cachedSelectionView          │     │
│  │ Status: ✅ USED when Selection.Active = true        │     │
│  │ - Caches cellbuf-rendered selection highlighting   │     │
│  │ - Separate to avoid cache conflicts                │     │
│  └────────────────────────────────────────────────────┘     │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## 🐛 The "Click Fixes Rendering" Bug

### Symptoms
- During normal operation, some content doesn't render correctly
- Clicking on the screen suddenly makes everything render properly
- Suggests cached content is stale

### Root Cause
Looking at mouse event handling:

```go
// messagelist.go:863
func (ml *MessageList) handleMouseDrag(mouse tea.MouseMsg) tea.Cmd {
    // ...
    m.cachedSelectionDirty = true // ← Invalidates cache
    return nil
}

// messagelist.go:270
func (m *MessageList) SetContent(s string) {
    // ...
    m.cachedViewDirty = true  // ← Invalidates cache
}
```

**Analysis**: Mouse events aggressively invalidate caches, forcing a full re-render. This suggests normal operations are NOT properly invalidating caches.

---

## 🎯 Likely Culprits

### 1. Content Hash Validation
File: `internal/chat/messagelist.go:911`

```go
if !m.cachedViewDirty &&
    m.cachedView != "" &&
    m.cachedViewOffset == m.YOffset &&
    m.cachedViewWidth == w &&
    m.cachedViewHeight == h &&
    !m.Selection.Active &&
    m.cachedContentHash == wrapped.contentHash {  // ← PROBLEM HERE
    return m.cachedView  // Returns stale cache
}
```

**Issue**: The content hash might not be detecting all types of content changes:
- Streaming message updates
- Tool result additions
- Dynamic content like spinners
- Sub-agent activity updates

### 2. Missing Cache Invalidation
File: `internal/chat/app_update.go`

When messages are updated (streaming, tool results, etc.), the code calls:
```go
a.viewportContentDirty = true  // ✅ Sets viewport dirty
```

But it does NOT call:
```go
a.msgViewport.cachedViewDirty = true  // ❌ MessageList cache still valid!
```

This means:
1. `App.updateViewportContent()` rebuilds content (cache miss)
2. Sets new content via `SetContent()`
3. But `MessageList.View()` might still return OLD cached view if hash matches!

---

## 🔧 Immediate Fix

Add this helper function:

```go
// app_update.go
func (a *App) invalidateAllRenderCaches() {
    a.viewportContentDirty = true
    if a.msgViewport != nil {
        a.msgViewport.cachedViewDirty = true
        a.msgViewport.cachedSelectionDirty = true
    }
}
```

Then call it whenever message content changes:
- Streaming message updates
- Tool result additions  
- Message edits
- Any content mutation

---

## 📊 Performance Opportunity: Unused MessageRenderCache

The `MessageRenderCache` in `message_cache.go` is a fully-implemented per-message cache that's **NEVER USED**!

### Evidence
```bash
# Grep results show:
messageCache.Get()  → 0 usages
messageCache.Set()  → 0 usages  
messageCache.MarkDirty() → 2 usages (but cache never read!)
```

### Potential Benefit
If implemented, it could provide:
- **Granular caching**: Only re-render messages that changed
- **Better scrolling**: Unchanged messages reuse cached output
- **Streaming efficiency**: Only last message re-renders during streaming

### Implementation Path
Modify `renderMessageListWithContext()` to:

```go
func (a *App) renderMessageListWithContext(messages []Message, ctx MessageRenderContext) []string {
    var result []string
    
    for i, msg := range messages {
        msgIdx := ctx.ActualMessageIndex(i)
        isFocused := ctx.IsFocused(i)
        
        // Try cache first
        if cached := a.messageCache.Get(msgIdx, isFocused, ctx.Width); cached != nil {
            // Cache hit - reuse rendered lines
            result = append(result, cached.lines...)
            continue
        }
        
        // Cache miss - render message
        lines := a.renderSingleMessage(msgIdx, &msg)
        
        // Store in cache
        a.messageCache.Set(msgIdx, &renderedMessage{
            view:        strings.Join(lines, "\n"),
            height:      len(lines),
            isFocused:   isFocused,
            width:       ctx.Width,
            contentHash: computeMessageHash(&msg),
        })
        
        result = append(result, lines...)
    }
    
    return result
}
```

---

## 🧪 Debug Strategy

### Step 1: Add Cache Hit/Miss Logging

```go
// messagelist.go:911
func (m *MessageList) renderFast(h int) string {
    // ...
    if !m.cachedViewDirty && /* ... other checks */ {
        logDebug("[CACHE] Fast view HIT - offset:%d hash:%d", m.YOffset, m.cachedContentHash)
        return m.cachedView
    }
    
    logDebug("[CACHE] Fast view MISS - dirty:%v offset:%d→%d hash:%d→%d", 
        m.cachedViewDirty, m.cachedViewOffset, m.YOffset, 
        m.cachedContentHash, wrapped.contentHash)
    // ... render ...
}
```

### Step 2: Reproduce the Bug
1. Start the app
2. Trigger streaming/tool results
3. Observe cache hit/miss patterns
4. Click on screen
5. See cache invalidation in logs

### Step 3: Identify Root Cause
Look for patterns like:
- Cache hit with old hash (hash not updating)
- Cache dirty flag not being set
- Content changes not triggering invalidation

---

## 📝 Summary

### What Exists
- ✅ 3 different rendering caches at different levels
- ✅ Fast/slow path rendering based on selection state
- ✅ Content hashing for cache validation

### What's Broken
- ❌ Per-message cache allocated but never used
- ❌ Cache invalidation not comprehensive enough
- ❌ Content hash might not detect all changes
- ❌ Clicking forces cache invalidation (workaround, not fix)

### What to Fix
1. **Short-term**: Add aggressive cache invalidation on all content changes
2. **Medium-term**: Debug content hash to detect all change types
3. **Long-term**: Implement per-message cache for better performance

### Files to Modify
1. `internal/chat/app_update.go` - Add invalidateAllRenderCaches()
2. `internal/chat/messagelist.go` - Add cache debug logging
3. `internal/chat/app_chat_render.go` - Use per-message cache
4. `internal/chat/message_cache.go` - Already implemented, just needs usage!
