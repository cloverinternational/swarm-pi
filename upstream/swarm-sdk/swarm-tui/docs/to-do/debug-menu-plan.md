# Debug Menu - Analysis & Improvement Plan

## Overview

The Debug Menu is an overlay screen accessible via `Ctrl+D` that provides API request inspection and application logging. It's designed for developers to troubleshoot LLM interactions, token usage, and application behavior.

---

## File References

| File | Purpose |
|------|---------|
| `internal/chat/debug_screen.go` | Main debug screen component (DebugScreen struct, View, Update, all tab renderers) |
| `internal/chat/debug_logs.go` | Log infrastructure (LogEntry, CircularLogBuffer, LogFilter, EnhancedLogsView) |
| `internal/chat/debug.go` | Global logging functions (DebugLog, InitDebugLog, globalDebugScreen) |
| `internal/chat/sdk_integration.go:876` | SetDebugScreen - connects SDK to debug screen for request capture |
| `internal/chat/app.go:2486-2492` | Debug screen rendering in main View() - takes priority when visible |

---

## Architecture

### Core Data Structures

**DebugScreen** (`debug_screen.go:100-116`)
```go
type DebugScreen struct {
    visible          bool
    width, height    int
    theme            Theme
    requests         []DebugRequest     // API request history
    selectedReq      int                // Selected request index
    activeTab        DebugTab           // Current tab (0-4)
    scrollOffset     int                // Scroll position
    enhancedLogsView *EnhancedLogsView  // Log viewer component
    jsonPretty       bool               // Pretty-print toggle
    showSanitized    bool               // Sanitize API keys
    autoScroll       bool               // Auto-select latest request
    viewMode         string             // "split" or "full" for JSON tab
}
```

**DebugRequest** (`debug_screen.go:24-54`)
```go
type DebugRequest struct {
    ID, MessageIndex, Timestamp, Method, URL
    Headers, Body, Response           // Request/response data
    CanonicalJSON, ProviderJSON       // For diff view
    ResponseCode, Error, Duration
    TokensInput, TokensOutput, Model
    ConversationVersion, MessageCount
    MessageHashes, MessagesAdded, MessagesRemoved
    ConversationState *DebugConversationState
}
```

**LogEntry** (`debug_logs.go:45-52`)
```go
type LogEntry struct {
    Timestamp time.Time
    Level     LogLevel    // TRACE, DEBUG, INFO, WARN, ERROR, FATAL
    Category  string      // SDK, MCP, STATE, API, etc.
    Message   string
    Raw       string      // Original unparsed message
}
```

### Tab System

| Tab | Enum Value | Render Function | Purpose |
|-----|------------|-----------------|---------|
| Requests | `DebugTabRequests` (0) | `renderRequestsList()` | List all API calls |
| JSON | `DebugTabJSON` (1) | `renderJSONView()` | Request/Response bodies |
| Diff | `DebugTabDiff` (2) | `renderDiffView()` | Canonical vs Provider comparison |
| Details | `DebugTabDetails` (3) | `renderDetailsView()` | Full request breakdown |
| Logs | `DebugTabLogs` (4) | `renderLogsView()` | Application logs with filtering |

### Data Flow

```
API Request Flow:
SDK.SendMessage() → captureDebugRequest() → debugScreen.AddRequest()
                                                    ↓
                                         requests = append(requests, req)
                                                    ↓
                                         if autoScroll: selectedReq = latest

Log Flow:
DebugLog("CATEGORY", "msg") → globalDebugScreen.AddLog()
                                        ↓
                              enhancedLogsView.AddLog()
                                        ↓
                              buffer.Add(ParseLogMessage())
```

---

## Current Keyboard Shortcuts

### Global (All Tabs)
- `Ctrl+D` - Toggle debug screen
- `q` / `Esc` - Close debug screen
- `Tab` / `Shift+Tab` - Cycle tabs
- `1-5` - Jump to specific tab
- `y` - Copy current tab content
- `Y` - Export all logs to file

### Tab-Specific

**Requests Tab:**
- `j/k` - Select request (changes selectedReq)
- `g/G` - First/last request
- `c` - Clear all requests

**JSON/Diff/Details Tabs:**
- `j/k` / `↑/↓` - Scroll content
- `Ctrl+u/d` - Page up/down
- `g/G` - Top/bottom
- `p` - Toggle pretty JSON
- `s` - Toggle API key sanitization
- `v` - Toggle split/full view (JSON only)

**Logs Tab:**
- `j/k` / `↑/↓` - Scroll logs
- `f` - Open filter panel
- `e` - Show errors only
- `w` - Show warnings and above
- `i` - Show info and above
- `r` - Reset all filters
- `l` - Clear logs
- `+/-` - Adjust buffer capacity

---

## Problems Identified

### 1. Shared State Across Tabs

**Location:** `debug_screen.go:107-109`

```go
selectedReq      int    // SHARED - used by all tabs
scrollOffset     int    // SHARED - used by all tabs
```

**Problem:** `selectedReq` makes sense for Requests tab (selection) but is used by JSON/Diff/Details to know which request to display. `scrollOffset` is shared but each tab has different content length.

**Symptom:** 
- Scroll down in JSON tab
- Switch to Requests tab
- Scroll position is wrong for the content
- Switch to Details tab
- Content starts mid-way

**Fix:** Store per-tab scroll offsets:
```go
tabScrollOffsets map[DebugTab]int
```

---

### 2. Tab Purpose Overlap

**Problem:** Content overlap between tabs:

| Content | JSON Tab | Details Tab |
|---------|----------|-------------|
| Request body | ✓ | ✓ |
| Response body | ✓ | ✓ |
| Headers | ✗ | ✓ |
| Token info | ✗ | ✓ |
| Metadata | ✗ | ✓ |

Diff tab is only useful when debugging provider transformations (rare use case).

**Suggestion:** 
- Merge JSON into Details as collapsible sections
- Make Diff a toggle within Details rather than separate tab
- Reduce from 5 tabs to 3: Requests, Inspector, Logs

---

### 3. Inconsistent j/k Semantics

**Location:** `debug_screen.go:694-719`

| Tab | j/k Action |
|-----|------------|
| Requests | **Select** different request |
| Others | **Scroll** content |

**Problem:** Same keys do fundamentally different operations. User builds muscle memory for "j = down" but it means different things.

**Suggestion:** Use consistent model:
- `j/k` always scrolls
- `n/p` or `]/[` for next/prev request selection
- Or: show request selector in all tabs (top bar)

---

### 4. Logs Tab is a Separate Component

**Location:** `debug_screen.go:110`, `debug_logs.go:607-648`

The Logs tab embeds `EnhancedLogsView` which has:
- Its own `scrollOffset`
- Its own `Update()` method
- Its own filter panel (modal overlay)
- Its own caching system

**Problem:** Logs tab behaves differently from other tabs. The filter panel (`f`) opens a modal overlay, unlike any other tab functionality.

**Suggestion:** Either:
- Extract Logs to its own screen (`Ctrl+L`)
- Or unify the UX pattern (make other tabs also use panel overlays for options)

---

### 5. Footer Says "1-4" But There Are 5 Tabs

**Location:** `debug_screen.go:2036`

```go
"1-4: jump to tab",  // BUG: should be "1-5"
```

---

### 6. No Request Filtering

**Problem:** Can filter logs by level/category, but cannot filter requests by:
- Error status (show only failed requests)
- Model name
- Duration (find slow requests)
- Token count (find expensive requests)

**Location to add:** Would need new filter state in DebugScreen and filter UI in `renderRequestsList()`

---

### 7. Ambiguous Copy Behavior

**Location:** `debug_screen.go:196-282` (copyCurrentTabContent)

| Tab | 'y' copies |
|-----|------------|
| Requests | All request summaries |
| JSON | All request/response JSONs |
| Diff | Selected request's diff only |
| Details | Selected request's details only |
| Logs | All filtered logs |

**Problem:** Inconsistent scope - sometimes "all", sometimes "selected".

**Suggestion:** 
- `y` = copy selected/visible
- `Y` = copy all (currently only exports logs)

---

### 8. No Visual autoScroll Indicator

**Location:** `debug_screen.go:113`

```go
autoScroll       bool   // Toggle with 'a', but no visual indicator
```

**Problem:** User toggles autoScroll off to investigate, forgets, wonders why new requests aren't appearing.

**Suggestion:** Show in header: `[AUTO-SCROLL: ON]` or `[AUTO-SCROLL: OFF]`

---

### 9. Token Growth Analysis Buried

**Location:** `debug_screen.go:300-358` (exportAllLogs has growth analysis)

Token growth analysis is excellent debugging info but only visible:
- In Details tab (partial)
- In exported file (full analysis)

**Suggestion:** Add a "Token Growth" quick view or chart in Requests tab showing growth trend.

---

## Improvement Priorities

### High Priority (UX Bugs)
1. Fix "1-4" → "1-5" in footer
2. Separate scrollOffset per tab
3. Add autoScroll visual indicator

### Medium Priority (Usability)
4. Add request filtering (error/model/duration)
5. Consistent j/k behavior across tabs
6. Consistent copy scope

### Low Priority (Refactoring)
7. Consider consolidating tabs (5 → 3)
8. Extract Logs to separate screen or unify UX
9. Surface token growth analysis more prominently

---

## Notes

- Debug screen is activated with `Ctrl+D` and takes over entire screen (`app.go:2486-2492`)
- Uses bubblezone for mouse support (`zone.Scan()` in View)
- Theme is passed from main app (`debugScreen.theme = app.theme`)
- Global `globalDebugScreen` variable enables `DebugLog()` to work anywhere
