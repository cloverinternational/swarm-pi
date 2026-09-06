# Rendering Performance Optimization Plan

**Date:** 2026-03-16  
**Author:** Stress Testing Analysis  
**Status:** Ready for Implementation

---

## Executive Summary

After running comprehensive stress tests under extreme conditions (16MB memory, 2% CPU), we identified the **View() rendering pipeline** as the primary bottleneck, consuming **93-96% of CPU time** with frame times spiking to **170ms** (target: <16.6ms for 60fps).

This document presents a surgical plan with 12 targeted optimizations to achieve a **10x improvement** in rendering performance.

---

## Stress Testing Infrastructure

### Test Suite Location
```
internal/stress/
├── benchmark.go      # Core benchmark harness
├── generator.go      # Message/content generation
├── report.go         # Report generation
├── types.go          # Test types and constants
└── extreme.go        # Extreme condition testing

cmd/stress_test/
└── main.go           # CLI entry point
```

### Running Tests
```bash
# Build the stress tester
go build -o bin/stress_test ./cmd/stress_test

# Standard extreme test
./bin/stress_test -extreme -low-mem -low-cpu -duration 30s -v

# Ultra extreme (finds breaking points)
./bin/stress_test \
  -extreme \
  -mem-limit 16 \
  -cpu-limit 2 \
  -duration 60s \
  -messages 15000 \
  -rate 100us \
  -max-size 40000 \
  -resize-freq 3ms \
  -resize-amp 300 \
  -tool-density 0.9 \
  -stream \
  -chunk-rate 200us \
  -chunk-size 1000 \
  -v
```

### Key Metrics to Track
| Metric | Current (Stress) | Target | Priority |
|--------|------------------|--------|----------|
| `render_cpu_percent` | 93-96% | <50% | CRITICAL |
| `max_frame_time` | 170ms | <33ms | CRITICAL |
| `avg_frame_time` | 28-32ms | <16.6ms | HIGH |
| `p99_frame_time` | 52-95ms | <33ms | HIGH |
| `messages_per_sec` | 33-70 | >200 | MEDIUM |
| `frame_time_stddev` | 14-15ms | <5ms | MEDIUM |

---

## Root Cause Analysis

### Call Graph (Simplified)
```
App.Update()
└── App.View()                          [93% CPU]
    ├── Panel.View()
    │   └── Viewport.View()             [HOT SPOT]
    │       ├── cache.IsValid()         [O(n) hash every call]
    │       ├── getVisibleLines()       [slice alloc]
    │       ├── strings.Join()          [alloc + copy]
    │       └── applySelection()        [O(n) selection]
    └── Viewport.View() (x panels)
```

### Primary Bottlenecks Identified

#### 1. Cache Invalidation Strategy (CRITICAL)
**Location:** `internal/chatui/viewport/cache.go`

```go
// CURRENT: IsValid recomputes hash EVERY call - O(n)
func (c *RenderCache) IsValid(lines []string, offset int, selection types.Selection) bool {
    if !c.valid {
        return false
    }
    contentHash := hashLines(lines)    // O(n) allocation + computation
    offsetHash := hashOffset(offset)    // Small but unnecessary
    selectionHash := hashSelection(selection)  // O(1) but called always
    return c.contentHash == contentHash &&
           c.offsetHash == offsetHash &&
           c.selectionHash == selectionHash
}
```

**Problem:** We compute the hash to check if we need to render, which is almost as expensive as rendering.

#### 2. Double Hash Computation (HIGH)
**Location:** `internal/chatui/viewport/viewport.go` + `cache.go`

```go
// View() calls IsValid() which hashes, then Set() which hashes again
func (v *Viewport) View() string {
    if v.cache.IsValid(v.lines, ...) {  // HASH #1
        return v.cache.Get()
    }
    // ... render ...
    v.cache.Set(result, v.lines, ...)   // HASH #2
    return result
}
```

#### 3. No Dirty Tracking (HIGH)
**Current flow:**
- `SetContent()` → Invalidate cache → Next View() → Always hash check
- Even when we KNOW content changed, we still verify via hash

**Better flow:**
- `SetContent()` → Mark dirty → Next View() → Skip hash, render immediately

#### 4. String Allocation in View() (MEDIUM)
**Location:** `internal/chatui/viewport/viewport.go:View()`

```go
// strings.Join allocates new string every time
result = strings.Join(visibleLines, "\n")
```

#### 5. No Resize Debouncing (HIGH)
**Location:** `internal/chatui/viewport/viewport.go:Resize()`

```go
func (v *Viewport) Resize(width, height int) {
    v.state.Width = width
    v.state.Height = height
    v.cache.Invalidate()  // Every resize invalidates
}
```

Under stress tests with 3ms resize frequency, this causes constant re-renders.

#### 6. Selection Hash Always Computed (LOW)
**Location:** `internal/chatui/viewport/cache.go:hashSelection()`

Selection is inactive 99% of the time but we still compute a hash for it.

---

## Surgical Optimization Plan

### Phase 1: Cache Architecture (Highest Impact)

#### Change 1.1: Dirty Flag Pattern
**File:** `internal/chatui/viewport/cache.go`

```go
type RenderCache struct {
    content       string
    contentHash   uint64
    offsetHash    uint64
    selectionHash uint64
    valid         bool
    dirty         bool  // NEW: Explicit dirty tracking
}

// NEW: Mark dirty when content changes
func (c *RenderCache) MarkDirty() {
    c.dirty = true
    c.valid = false
}

// OPTIMIZED: Skip hash if we know we're dirty
func (c *RenderCache) IsValid(lines []string, offset int, selection types.Selection) bool {
    if c.dirty {
        return false  // Fast path: we know content changed
    }
    if !c.valid {
        return false
    }
    // Only hash if not dirty and valid
    // ... hash comparison ...
}
```

**Impact:** Eliminates hash computation after SetContent(), saves O(n) per render

#### Change 1.2: Pre-computed Hash Storage
**File:** `internal/chatui/viewport/viewport.go`

```go
type Viewport struct {
    lines         []string
    linesHash     uint64  // NEW: Store hash when content set
    linesDirty    bool    // NEW: Track if lines changed
    // ...
}

func (v *Viewport) SetContent(content string) {
    v.lines = strings.Split(content, "\n")
    v.linesHash = hashLines(v.lines)  // Compute ONCE
    v.linesDirty = true
    v.buildLineOffsets()
    v.cache.MarkDirty()
    // ...
}

func (v *Viewport) View() string {
    // Pass pre-computed hash
    if v.cache.IsValidWithHash(v.linesHash, v.state.YOffset, v.state.Selection) {
        return v.cache.Get()
    }
    // ...
}
```

**Impact:** Eliminates double hash computation

#### Change 1.3: Optimized IsValid
**File:** `internal/chatui/viewport/cache.go`

```go
// NEW: Accept pre-computed hash
func (c *RenderCache) IsValidWithHash(contentHash uint64, offset int, selection types.Selection) bool {
    if c.dirty || !c.valid {
        return false
    }
    
    // Fast offset check (inline)
    if c.offsetHash != uint64(offset) {  // Direct comparison if hash is just the value
        return false
    }
    
    // Skip selection hash if inactive
    selHash := uint64(0)
    if selection.Active {
        selHash = hashSelection(selection)
    }
    
    return c.contentHash == contentHash &&
           c.selectionHash == selHash
}
```

### Phase 2: String Allocation Reduction

#### Change 2.1: strings.Builder Pool
**File:** `internal/chatui/viewport/viewport.go`

```go
var builderPool = sync.Pool{
    New: func() interface{} {
        return &strings.Builder{}
    },
}

func (v *Viewport) View() string {
    // ... cache check ...
    
    visibleLines := v.getVisibleLines()
    
    builder := builderPool.Get().(*strings.Builder)
    builder.Reset()
    defer builderPool.Put(builder)
    
    // Pre-grow if needed
    totalLen := 0
    for _, line := range visibleLines {
        totalLen += len(line) + 1
    }
    builder.Grow(totalLen)
    
    for i, line := range visibleLines {
        if i > 0 {
            builder.WriteByte('\n')
        }
        builder.WriteString(line)
    }
    
    result := builder.String()
    v.cache.Set(result, v.linesHash, v.state.YOffset, v.state.Selection)
    return result
}
```

**Impact:** Reduces allocations by ~50%

#### Change 2.2: Slice Reuse in SetContent
**File:** `internal/chatui/viewport/viewport.go`

```go
func (v *Viewport) SetContent(content string) {
    // Estimate line count to pre-allocate
    estimatedLines := strings.Count(content, "\n") + 1
    
    // Reuse existing slice if possible
    if cap(v.lines) >= estimatedLines {
        v.lines = v.lines[:0]
    } else {
        v.lines = make([]string, 0, estimatedLines)
    }
    
    // Manual split to reuse slice
    start := 0
    for i := 0; i < len(content); i++ {
        if content[i] == '\n' {
            v.lines = append(v.lines, content[start:i])
            start = i + 1
        }
    }
    if start < len(content) {
        v.lines = append(v.lines, content[start:])
    }
    
    // ... rest of method
}
```

**Impact:** Reduces allocations during content updates

### Phase 3: Resize Debouncing

#### Change 3.1: Debounced Resize
**File:** `internal/chatui/viewport/viewport.go`

```go
type Viewport struct {
    // ... existing fields ...
    
    // Resize debouncing
    pendingResize    bool
    pendingWidth     int
    pendingHeight    int
    lastResizeTime   time.Time
    resizeDebounce   time.Duration  // Default: 16ms
}

func (v *Viewport) Resize(width, height int) {
    now := time.Now()
    
    // If same size, skip entirely
    if !v.pendingResize && v.state.Width == width && v.state.Height == height {
        return
    }
    
    // Debounce rapid resizes
    if now.Sub(v.lastResizeTime) < v.resizeDebounce {
        v.pendingResize = true
        v.pendingWidth = width
        v.pendingHeight = height
        return
    }
    
    v.applyResize(width, height)
    v.pendingResize = false  // Clear pending flag after applying
    v.lastResizeTime = now
}

func (v *Viewport) applyResize(width, height int) {
    v.state.Width = width
    v.state.Height = height
    v.cache.MarkDirty()
    v.clampScroll()
}

// Call this before View() to flush pending resizes
func (v *Viewport) FlushPendingResize() {
    if v.pendingResize {
        v.applyResize(v.pendingWidth, v.pendingHeight)
        v.pendingResize = false
    }
}
```

**Impact:** Eliminates render thrashing during rapid viewport changes

### Phase 4: Selection Optimization

#### Change 4.1: Lazy Selection Hash
**File:** `internal/chatui/viewport/cache.go`

```go
type RenderCache struct {
    // ... existing fields ...
    selectionActive bool  // NEW: Track if selection is active
}

func (c *RenderCache) Set(content string, contentHash uint64, offset int, selection types.Selection) {
    c.content = content
    c.contentHash = contentHash
    c.offsetHash = uint64(offset)  // Direct value if offset < 2^64
    c.selectionActive = selection.Active
    if selection.Active {
        c.selectionHash = hashSelection(selection)
    } else {
        c.selectionHash = 0
    }
    c.valid = true
    c.dirty = false
}

func (c *RenderCache) IsValidWithHash(contentHash uint64, offset int, selection types.Selection) bool {
    if c.dirty || !c.valid {
        return false
    }
    
    if c.contentHash != contentHash {
        return false
    }
    
    if c.offsetHash != uint64(offset) {
        return false
    }
    
    // Skip selection comparison if both inactive
    if !c.selectionActive && !selection.Active {
        return true
    }
    
    // Only hash if selection is active and different
    if selection.Active {
        return c.selectionHash == hashSelection(selection)
    }
    
    return false
}
```

**Impact:** Eliminates selection hash for 99% of frames

---

## Implementation Order

```
Phase 1 (Critical - 60% of improvement)
├── 1.1 Dirty flag pattern
├── 1.2 Pre-computed hash storage
└── 1.3 Optimized IsValid

Phase 2 (High - 25% of improvement)
├── 2.1 strings.Builder pool
└── 2.2 Slice reuse in SetContent

Phase 3 (Medium - 10% of improvement)
└── 3.1 Debounced resize

Phase 4 (Low - 5% of improvement)
└── 4.1 Lazy selection hash
```

---

## Expected Results

| Metric | Before | After (Expected) | Improvement |
|--------|--------|------------------|-------------|
| `render_cpu_percent` | 93-96% | 30-40% | ~60% reduction |
| `avg_frame_time` | 28-32ms | 5-8ms | 4-6x faster |
| `max_frame_time` | 170ms | 25-35ms | 5-7x faster |
| `messages_per_sec` | 33-70 | 300-500 | 7-10x faster |
| `allocations_per_frame` | ~50 | ~5-10 | 5-10x reduction |

---

## Files to Modify

| File | Changes | Complexity |
|------|---------|------------|
| `internal/chatui/viewport/cache.go` | Dirty tracking, optimized IsValid, lazy hash | Medium |
| `internal/chatui/viewport/viewport.go` | Pre-computed hash, builder pool, slice reuse, debounce | High |
| `internal/chatui/types/state.go` | Add dirty flag to ViewportState if needed | Low |

---

## Testing Checklist

After implementing changes, run these tests to verify:

```bash
# 1. Basic functionality
go test ./internal/chatui/... -v

# 2. Standard stress test
./bin/stress_test -duration 60s -messages 10000 -v

# 3. Low memory stress
./bin/stress_test -extreme -mem-limit 32 -duration 30s -v

# 4. CPU starvation
./bin/stress_test -extreme -cpu-limit 10 -duration 30s -v

# 5. Viewport chaos
./bin/stress_test -resize-freq 5ms -resize-amp 500 -duration 30s -v

# 6. Combined extreme
./bin/stress_test -extreme -low-mem -low-cpu -mem-limit 16 -cpu-limit 2 -duration 60s -v

# 7. Benchmark comparison
go test ./internal/chatui/viewport/... -bench=. -benchmem -count=5
```

### Success Criteria
- [ ] All existing tests pass
- [ ] `render_cpu_percent` < 50% under stress
- [ ] `max_frame_time` < 50ms under stress
- [ ] `messages_per_sec` > 150 under stress
- [ ] No new allocations in hot paths (verify with `-benchmem`)

---

## Appendix: Current Code Analysis

### Hot Path Flame Graph Data
```
View() .............................. 93.2%
├── cache.IsValid() .................. 35.1%
│   └── hashLines() .................. 32.4%
├── strings.Join() ................... 25.3%
├── getVisibleLines() ................. 8.2%
├── applySelection() .................. 6.1%
└── cache.Set() ....................... 4.8%
    └── hashLines() ................... 3.9%
```

### Allocation Profile
```
Total: 2.4 GB during 30s stress test
├── strings.Split() ................. 42%
├── strings.Join() .................. 28%
├── hash computation ................ 15%
├── slice operations ................. 8%
└── other ............................ 7%
```

---

## Contact

For questions about this optimization plan:
1. See stress test implementation: `internal/stress/extreme.go`
2. See current caching: `internal/chatui/viewport/cache.go`
3. See viewport rendering: `internal/chatui/viewport/viewport.go`

---

*Generated by stress testing analysis on 2026-03-16*
