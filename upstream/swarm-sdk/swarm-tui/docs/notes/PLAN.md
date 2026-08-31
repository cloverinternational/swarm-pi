# Plan: Unified Message Renderer + MCP Tool Rendering

## Goal
Replace the dual-path rendering system (streaming vs. completed) with a single unified renderer, and add proper MCP tool output rendering.

---

## Part 1: Unified Renderer (`updateViewport()`)

### Problem
Two separate render paths produce visually different output:
- `updateStreamingMessageIncremental()` — renders only last message, `IsStreaming: true`
- `updateViewportContent()` — rebuilds all messages, `IsStreaming: false`

### Solution
Single `updateViewport()` function with per-message dirty tracking.

### Changes

#### 1.1 — `render_context.go`: Replace `IsStreaming` with `IsActiveMessage`

- Rename `IsStreaming bool` → `IsActiveMessage bool` in `MessageRenderContext`
- `NewMessageRenderContext()` → sets `IsActiveMessage: false` (used by full render)
- `NewStreamingMessageContext()` → REMOVE entirely
- `NewPreviewMessageContext()` → keeps `IsActiveMessage: false`
- Add new `NewSingleMessageContext(msgIdx, width int, isActive bool)` that sets `IsActiveMessage` per-message

#### 1.2 — `app_types.go`: Replace caching state

**Remove:**
- `viewportCachedLines []string`
- `viewportLastMsgCount int`
- `viewportLastMsgLen int`
- `streamingPreviousLines []string`

**Add:**
- `perMessageCache [][]string` — rendered lines per message
- `perMessageDirty []bool` — dirty flag per message

**Keep:**
- `viewportContentDirty bool` — global "invalidate everything" flag
- `streamingInProgress bool` — controls debouncing + active message detection
- `streamingMessage bool` — used by spinner, input, scroll (NOT rendering)
- `inputProtection` struct — debouncing

#### 1.3 — `app_chat_render.go`: New unified `updateViewport()`

**Remove functions:**
- `updateStreamingMessageIncremental()`
- `renderSingleMessage()`
- `renderAllMessagesExceptLast()`
- `renderMessageListWithPositions()`

**Replace with single `updateViewport()`:**
```
1. If no messages → set empty content, return
2. Debounce during streaming (same logic as old incremental)
3. Save scroll position
4. Ensure per-message cache arrays match message count
5. If viewportContentDirty → mark ALL messages dirty, clear flag
6. During streaming → always mark last message dirty
7. For each dirty message:
   - Compute isActive = streamingInProgress && idx == lastMsgIdx
   - Render with per-message context (IsActiveMessage = isActive)
   - Store in perMessageCache[idx]
   - Clear dirty flag
8. If nothing was dirty → return (no SetContent call)
9. Combine all per-message caches into allLines + compute positions
10. Build string → SetContent()
11. Restore scroll (same logic: userScrolledAway → preserve, else → GotoBottom)
```

#### 1.4 — `app_chat_render.go`: Update `renderMessageListWithContext()`

Replace all `ctx.IsStreaming` with `ctx.IsActiveMessage`:
- Line 760: sub-agent renderer `ctx.IsActiveMessage`
- Line 802: `isRunning := ctx.IsActiveMessage && !hasResult`
- Line 1044: `isBashRunning := ctx.IsActiveMessage && trOutput == "" && tr.Error == ""`

#### 1.5 — `app_update.go`: Replace ALL dual-path blocks

Every instance of:
```go
if a.streamingInProgress {
    a.updateStreamingMessageIncremental()
} else {
    wasAtBottom := a.msgViewport.AtBottom()
    a.updateViewportContent()
    if wasAtBottom { a.msgViewport.GotoBottom() }
}
```
Becomes:
```go
a.updateViewport()
```

Approximately 13 call sites to update.

#### 1.6 — `app_keyboard.go`: Same replacement

~5 call sites that call `invalidateViewportCache()` then `updateViewportContent()`:
Change to `invalidateViewportCache()` then `updateViewport()`.

#### 1.7 — `app_messaging.go`: Remove streaming cache setup

Remove:
```go
if len(a.messages) > 1 {
    a.streamingPreviousLines = a.renderAllMessagesExceptLast()
}
```

Update `invalidateViewportCache()`:
```go
func (a *App) invalidateViewportCache() {
    a.viewportContentDirty = true
    a.perMessageCache = nil
    a.perMessageDirty = nil
    if a.msgViewport != nil {
        a.msgViewport.cachedViewDirty = true
        a.msgViewport.cachedSelectionDirty = true
        a.msgViewport.wrappedLineCache = nil
    }
}
```

#### 1.8 — Cleanup streaming end handlers

In `agentResponseMsg`, `streamDoneMsg`, `streamErrorMsg` handlers:
- Remove `a.streamingPreviousLines = nil`
- Keep `a.streamingInProgress = false`
- Call `a.markLastMessageDirty()` + `a.updateViewport()`

---

## Part 2: MCP Tool Classification & Rendering

### Problem
MCP tools fall through to generic plain-text rendering. No syntax highlighting, no diff view, no structured output.

### Solution
Add semantic tool classification that maps MCP tools to built-in tool renderers.

### Changes

#### 2.1 — `tool_renderer.go`: Add `ToolCategory` and `ClassifyTool()`

```go
type ToolCategory int
const (
    ToolCategoryUnknown ToolCategory = iota
    ToolCategoryRead
    ToolCategoryEdit
    ToolCategoryPatch
    ToolCategoryBash
    ToolCategoryGrep
    ToolCategoryWebSearch
    ToolCategoryTodo
)

func ClassifyTool(toolName string) ToolCategory {
    // Check built-in tools first (fast path)
    if IsReadTool(toolName) { return ToolCategoryRead }
    if IsPatchTool(toolName) { return ToolCategoryPatch }
    if IsEditTool(toolName) { return ToolCategoryEdit }
    if toolName == "Bash" { return ToolCategoryBash }
    if IsGrepTool(toolName) { return ToolCategoryGrep }
    if IsWebSearchTool(toolName) { return ToolCategoryWebSearch }
    if IsTodoTool(toolName) { return ToolCategoryTodo }

    // MCP tool heuristic classification
    if strings.HasPrefix(toolName, "mcp_") {
        return classifyMCPTool(toolName)
    }
    return ToolCategoryUnknown
}

func classifyMCPTool(toolName string) ToolCategory {
    lower := strings.ToLower(toolName)
    // Read-like: read_file, read-file, get_file, file_content, etc.
    // Edit-like: write_file, edit_file, update_file, etc.
    // Grep-like: search, grep, find_in, etc.
    // Bash-like: run_command, execute, terminal, shell, etc.
    ...
}
```

#### 2.2 — `app_chat_render.go` (lines 932-1137): Refactor tool rendering cascade

Replace the nested if/else chain with a switch on `ClassifyTool()`:
```go
category := ClassifyTool(toolName)
switch category {
case ToolCategoryRead:    // syntax highlighting path
case ToolCategoryEdit:    // diff rendering path
case ToolCategoryPatch:   // patch rendering path
case ToolCategoryBash:    // terminal rendering path
case ToolCategoryGrep:    // grep formatting path
case ToolCategoryWebSearch: // search card path
case ToolCategoryTodo:    // checklist path
default:                  // generic output
}
```

#### 2.3 — `app_chat_render.go` (processToolResultForRendering): Extend for MCP

Use `ClassifyTool()` instead of individual `IsXTool()` checks so MCP tools get pre-processed too:
```go
category := ClassifyTool(toolName)
switch category {
case ToolCategoryRead:
    filePath := GetFilePath(params)
    result := a.toolResultProcessor.ProcessReadResult(filePath, output, width)
    msg.ReadResults[callID] = result
case ToolCategoryEdit:
    // ... same as current IsEditTool path
// etc.
}
```

---

## Part 3: Helper Functions

### 3.1 — Per-message dirty tracking helpers

```go
func (a *App) ensurePerMessageCache() { ... }
func (a *App) markMessageDirty(idx int) { ... }
func (a *App) markLastMessageDirty() { ... }
```

---

## Files Modified

| File | Changes |
|------|---------|
| `render_context.go` | `IsStreaming` → `IsActiveMessage`, remove `NewStreamingMessageContext` |
| `app_types.go` | Replace cache fields, add `perMessageCache`/`perMessageDirty` |
| `app_chat_render.go` | New `updateViewport()`, remove 4 old functions, refactor tool rendering |
| `app_update.go` | Replace ~13 dual-path blocks with `updateViewport()` |
| `app_keyboard.go` | Replace ~5 `updateViewportContent()` calls |
| `app_messaging.go` | Remove streaming cache setup, update `invalidateViewportCache()` |
| `tool_renderer.go` | Add `ToolCategory`, `ClassifyTool()`, `classifyMCPTool()` |
| `render_context_test.go` | Update tests for `IsActiveMessage` |

## Files NOT Modified
- `streamingMessage` field — kept for spinner, input, scroll (non-rendering uses)
- `collapse_widget.go` — rendering functions unchanged, just called via different paths
- `tool_result_processor.go` — processing functions unchanged, just called for MCP tools too
- `messagelist.go` — viewport internals unchanged

## Build Order
1. `render_context.go` + `render_context_test.go` (IsActiveMessage)
2. `app_types.go` (new cache fields)
3. `tool_renderer.go` (ClassifyTool)
4. `app_chat_render.go` (unified renderer + tool classification)
5. `app_messaging.go` (invalidation + streaming setup)
6. `app_update.go` (all callers)
7. `app_keyboard.go` (remaining callers)
8. Build + test
