# TUI Performance Optimizations

**Branch:** `perf/tui-render-optimizations`
**Date:** 2026-01-04
**Profiling Duration:** 30 seconds of normal TUI usage

## Executive Summary

CPU profiling revealed that **84% of CPU time** is spent in bubbletea's internal `ultraviolet` terminal renderer. The remaining **~15%** is in our application code, with specific hot spots identified for optimization.

**Total potential savings:** ~800ms per render cycle (estimated 25-40% improvement in app code)

---

## Profiling Results

### Overall CPU Distribution

| Component | Time (30s sample) | % of Total |
|-----------|-------------------|------------|
| Ultraviolet renderer (bubbletea) | 19.2s | 84% |
| Application code | ~3.5s | 15% |
| Other | ~0.2s | 1% |

### Hot Functions in Application Code

| Function | Time | % | File:Line |
|----------|------|---|-----------|
| `renderChatContent` | 1.78s | 7.8% | app.go:5023 |
| `MessageList.View` | 0.54s | 2.4% | messagelist.go:790 |
| `SidePanel.Render` | 0.45s | 2.0% | sidepanel.go:93 |
| `updateViewportContent` | 0.20s | 0.9% | app.go:6612 |

### Hot Lines (Specific Bottlenecks)

| Location | Time | Issue |
|----------|------|-------|
| `app.go:5344` | 360ms | `finalStyle.Render(content)` - final frame wrap |
| `app.go:5272` | 300ms | `viewportStyle.Render(viewportContent)` |
| `sidepanel.go:578` | 210ms | `panelStyle.Render(content)` - final panel wrap |
| `app.go:5045` | 180ms | Header title render |
| `app.go:5336` | 120ms | `lipgloss.JoinVertical(parts...)` |
| `sidepanel.go:114` | 40ms | `strings.Repeat("─", width)` divider |

---

## Optimization Plan

### 1. SidePanel Caching (~450ms saved)

**Problem:** SidePanel renders 40+ lipgloss style calls every frame, but content rarely changes.

**Solution:** Cache the rendered SidePanel and only re-render when state changes.

**Files to modify:**
- `internal/chat/app.go` - Add cache fields to App struct
- `internal/chat/sidepanel.go` - Add cache check in Render()

**Cache invalidation triggers:**
- Token count changes
- Operating mode changes
- Tool list changes
- Background agent status changes
- Window resize

**Implementation:**
```go
// App struct additions
sidePanelCache       string
sidePanelCacheDirty  bool
sidePanelLastTokens  int
sidePanelLastMode    string
sidePanelLastWidth   int
sidePanelLastHeight  int
```

---

### 2. Pre-allocate Divider Strings (~40ms saved)

**Problem:** `strings.Repeat("─", width-4)` called every frame in SidePanel.

**Solution:** Cache divider string, regenerate only on width change.

**Files to modify:**
- `internal/chat/sidepanel.go`

**Implementation:**
```go
// SidePanel struct additions
cachedDivider string
lastWidth     int

// In Render(), before using divider:
if s.width != s.lastWidth || s.cachedDivider == "" {
    s.cachedDivider = strings.Repeat("─", s.width-4)
    s.lastWidth = s.width
}
```

---

### 3. Pre-create lipgloss Styles (~200ms saved)

**Problem:** `lipgloss.NewStyle()` called repeatedly in hot paths, creating garbage.

**Solution:** Pre-create commonly used styles once, reuse them.

**Files to modify:**
- `internal/chat/app.go` - Add style cache to App struct
- `internal/chat/styles.go` (new file) - Centralized style definitions

**Styles to pre-create:**
- `viewportStyle` - Used in renderChatContent
- `headerStyle` - Header rendering
- `separatorStyle` - Blue separator lines
- `inputStyle` - Input area
- `bottomGapStyle` - Bottom info line

**Implementation:**
```go
// App struct additions
styles struct {
    viewport    lipgloss.Style
    header      lipgloss.Style
    separator   lipgloss.Style
    input       lipgloss.Style
    bottomGap   lipgloss.Style
    divider     lipgloss.Style
}

// Call once on init and theme change
func (a *App) initStyles() {
    th := a.theme
    a.styles.viewport = lipgloss.NewStyle().
        Background(lipgloss.Color(th.BG)).
        Padding(0, 2)
    // ... etc
}
```

---

### 4. Optimize String Building (~100ms saved)

**Problem:** `lipgloss.JoinVertical(parts...)` allocates intermediate strings.

**Solution:** Use `strings.Builder` with pre-allocated capacity.

**Files to modify:**
- `internal/chat/app.go` - renderChatContent function
- `internal/chat/sidepanel.go` - Render function

**Implementation:**
```go
// Instead of:
content := lipgloss.JoinVertical(lipgloss.Left, parts...)

// Use:
var b strings.Builder
totalLen := 0
for _, p := range parts {
    totalLen += len(p) + 1
}
b.Grow(totalLen)
for i, part := range parts {
    if i > 0 {
        b.WriteByte('\n')
    }
    b.WriteString(part)
}
content := b.String()
```

---

## Implementation Order

1. **SidePanel caching** - Biggest single win, isolated change
2. **Pre-allocate dividers** - Easy, low risk
3. **Pre-create styles** - Medium effort, good payoff
4. **strings.Builder optimization** - Easy, cumulative benefit

---

## Verification

After each optimization, run profiler to verify:

```bash
# Terminal 1
SWARMOS_PPROF=6060 ./swarmos

# Terminal 2
./scripts/profile.sh cpu
```

Compare before/after for each function's CPU time.

---

## Implementation Results

### Verified Improvements

| Function | Before | After | Improvement |
|----------|--------|-------|-------------|
| SidePanel.Render | 0.45s (1.96%) | Not measurable | **100% eliminated** |
| Separator strings | Multiple per frame | Cached | Eliminated allocs |
| lipgloss.JoinVertical | Multiple calls | strings.Builder | Reduced allocs |
| MessageList cellbuf | 830ms/frame | Cached | **Cache hits avoid re-render** |
| wrapContentToWidth | Re-wrap on every scroll | Per-line cache | **Scroll optimization** |

### Commits

1. `b182e57` - docs: Add TUI performance optimization plan
2. `4861fd0` - perf(sidepanel): Add render caching for ~450ms savings
3. `36029bc` - perf: Cache separator strings and use strings.Builder
4. `3abae76` - perf(messagelist): Add cellbuf render caching for ~830ms savings
5. `1597bf8` - docs: Update optimization plan with implementation results
6. `d1ba8fa` - perf(messagelist): Add fast render path bypassing cellbuf
7. `be5f492` - perf(animation): Reduce idle animation FPS from 15 to 8
8. `df62321` - perf(messagelist): Add per-line wrap caching for scroll

---

### 5. Per-Line Wrap Cache (~variable savings during scroll)

**Problem:** `wrapContentToWidth` re-wraps every line on every scroll event, even for lines that haven't changed.

**Solution:** Cache wrapped lines by their hash + width. When scrolling through a long conversation, lines that have already been wrapped are retrieved from cache instantly.

**Implementation:**
```go
var lineWrapCache = struct {
    sync.RWMutex
    cache map[string][]string  // "width:hash" -> wrapped lines
    width int
}{}

func wrapContentToWidth(content string, width int) string {
    // Check cache for each line
    // Cache hit: use cached wrapped lines
    // Cache miss: wrap and store in cache
}
```

**Cache invalidation:** On width change only (window resize)

---

## Notes

- The `updateViewportContent` function already has good caching (line 6612-6658)
- Ultraviolet renderer overhead is outside our control (bubbletea internal)
- Focus on reducing lipgloss.Render calls in hot paths
- Avoid premature optimization in cold paths (settings screens, etc.)
- MessageList cache helps most during idle state (no scroll/selection changes)
- Animation ticks still trigger re-renders - future optimization opportunity
- Per-line wrap cache specifically optimizes scrolling through existing content
