# Implementation Plan: TPS and Cache Metrics Bars

## Overview

Adding two new dynamic metric bars to the side panel:
1. **Tokens Per Second (TPS) Bar** - Real-time token generation rate tracking per model
2. **Cache Metrics Bar** - Cache read/write operation statistics

---

## 1. Data Structure Design

### New Package: `internal/metrics/`

#### `internal/metrics/types.go` - Core Data Structures

```go
package metrics

import (
    "sync"
    "time"
)

// TPSMetrics tracks tokens-per-second for streaming sessions
type TPSMetrics struct {
    mu sync.RWMutex
    
    // Current streaming session
    StreamStartTime time.Time      // When streaming began
    LastUpdateTime  time.Time      // Last token update time
    TokensAtStart   int            // Token count at stream start
    CurrentTokens   int            // Current token count
    Snapshots       []TPSSnapshot  // Rolling window of TPS measurements
    
    // Computed values (updated in real-time)
    CurrentTPS      float64        // Current tokens/sec (smoothed with EMA)
    PeakTPS         float64        // Highest TPS seen this session
    AverageTPS      float64        // Average TPS this session
    
    // Configuration
    SnapshotWindowSize int          // Number of snapshots for rolling average (10)
    SnapshotInterval   time.Duration // Debounce interval (100ms)
}

// TPSSnapshot represents a point-in-time measurement
type TPSSnapshot struct {
    Timestamp   time.Time
    TokensDelta int
    TPS         float64
}

// CacheMetrics tracks cache operation statistics
type CacheMetrics struct {
    mu sync.RWMutex
    
    // Current session metrics
    TotalReads       int64   // Total cache read attempts
    TotalWrites      int64   // Total cache write attempts
    SuccessfulReads  int64   // Successful cache reads (cache hits)
    SuccessfulWrites int64   // Successful cache writes
    FailedReads      int64   // Failed cache read attempts
    FailedWrites     int64   // Failed cache write attempts
    
    // Computed values
    HitRate          float64 // (SuccessfulReads / TotalReads) * 100
    SuccessRate      float64 // ((SuccessfulReads + SuccessfulWrites) / (TotalReads + TotalWrites)) * 100
    
    // For animation (per-second deltas)
    ReadsThisSecond  int64
    WritesThisSecond int64
    LastResetTime    time.Time
}

// ModelMetrics stores per-model historical statistics
type ModelMetrics struct {
    ModelID              string
    ModelName            string
    
    // TPS history
    HistoricalPeakTPS    float64  // Best TPS ever recorded for this model
    HistoricalAvgTPS     float64  // Running average TPS across all sessions
    TotalTokensGenerated int64    // All tokens ever generated with this model
    TotalStreamingSecs   float64  // Total seconds spent streaming
    SessionCount         int      // Number of streaming sessions
    
    // Cache history
    LifetimeCacheHits    int64
    LifetimeCacheMisses  int64
    LifetimeCacheReads   int64
    LifetimeCacheWrites  int64
    LifetimeFailedOps    int64
    
    LastUpdated          time.Time
}

// MetricsStore is the main metrics storage container
type MetricsStore struct {
    mu sync.RWMutex
    
    // Current session (in-memory only)
    CurrentModel  string
    TPS           *TPSMetrics
    Cache         *CacheMetrics
    
    // Per-model historical data (persisted to disk)
    ModelStats    map[string]*ModelMetrics
    
    // File path for persistence
    PersistPath   string
}

// GetCurrentTPS returns current metrics atomically
func (t *TPSMetrics) GetMetrics() (current, peak, avg float64) {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return t.CurrentTPS, t.PeakTPS, t.AverageTPS
}

// GetCacheMetrics returns cache metrics atomically
func (c *CacheMetrics) GetMetrics() (hits, reads, writes, failed int64, rate float64) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.SuccessfulReads, c.TotalReads, c.TotalWrites, c.FailedReads + c.FailedWrites, c.HitRate
}
```

#### `internal/metrics/tps_tracker.go` - TPS Calculation

```go
package metrics

import (
    "time"
)

// StartStreaming initializes tracking for a new streaming session
func (t *TPSMetrics) StartStreaming(initialTokens int) {
    t.mu.Lock()
    defer t.mu.Unlock()
    
    t.StreamStartTime = time.Now()
    t.LastUpdateTime = t.StreamStartTime
    t.TokensAtStart = initialTokens
    t.CurrentTokens = initialTokens
    t.Snapshots = t.Snapshots[:0] // Clear previous snapshots
    t.CurrentTPS = 0
    t.PeakTPS = 0
    t.AverageTPS = 0
}

// UpdateTokens records new token count and calculates TPS
// Should be called whenever tokens are received during streaming
func (t *TPSMetrics) UpdateTokens(newTokenCount int) {
    t.mu.Lock()
    defer t.mu.Unlock()
    
    now := time.Now()
    elapsed := now.Sub(t.LastUpdateTime)
    
    // Debounce: only calculate if interval has passed
    if elapsed < t.SnapshotInterval {
        t.CurrentTokens = newTokenCount
        return
    }
    
    tokensDelta := newTokenCount - t.CurrentTokens
    if tokensDelta <= 0 {
        t.CurrentTokens = newTokenCount
        return
    }
    
    // Calculate instantaneous TPS
    instantTPS := float64(tokensDelta) / elapsed.Seconds()
    
    // Create snapshot
    snapshot := TPSSnapshot{
        Timestamp:   now,
        TokensDelta: tokensDelta,
        TPS:         instantTPS,
    }
    
    // Add to rolling window
    t.Snapshots = append(t.Snapshots, snapshot)
    if len(t.Snapshots) > t.SnapshotWindowSize {
        t.Snapshots = t.Snapshots[1:] // Remove oldest
    }
    
    // Update current TPS with exponential moving average (smoothing)
    // This reduces jitter while still responding to changes
    alpha := 0.3 // Smoothing factor (0.3 = 30% new, 70% previous)
    if t.CurrentTPS == 0 {
        t.CurrentTPS = instantTPS
    } else {
        t.CurrentTPS = alpha*instantTPS + (1-alpha)*t.CurrentTPS
    }
    
    // Update peak TPS
    if t.CurrentTPS > t.PeakTPS {
        t.PeakTPS = t.CurrentTPS
    }
    
    // Calculate rolling average from snapshot window
    t.AverageTPS = t.calculateRollingAverage()
    
    // Update state
    t.CurrentTokens = newTokenCount
    t.LastUpdateTime = now
}

// calculateRollingAverage computes average of snapshot window (must be locked)
func (t *TPSMetrics) calculateRollingAverage() float64 {
    if len(t.Snapshots) == 0 {
        return 0
    }
    
    var sum float64
    for _, snap := range t.Snapshots {
        sum += snap.TPS
    }
    return sum / float64(len(t.Snapshots))
}

// StopStreaming finalizes the streaming session
func (t *TPSMetrics) StopStreaming() {
    t.mu.Lock()
    defer t.mu.Unlock()
    
    // Calculate final session average
    totalElapsed := time.Since(t.StreamStartTime).Seconds()
    totalTokens := float64(t.CurrentTokens - t.TokensAtStart)
    
    if totalElapsed > 0 && totalTokens > 0 {
        finalAvg := totalTokens / totalElapsed
        // Blend with existing average (don't override completely)
        alpha := 0.5
        t.AverageTPS = alpha*finalAvg + (1-alpha)*t.AverageTPS
    }
}

// IsStreaming returns whether we're currently tracking a stream
func (t *TPSMetrics) IsStreaming() bool {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return !t.StreamStartTime.IsZero() && time.Since(t.LastUpdateTime) < 2*time.Second
}
```

#### `internal/metrics/cache_tracker.go` - Cache Metrics

```go
package metrics

import (
    "time"
)

// RecordCacheRead records a cache read attempt
func (c *CacheMetrics) RecordCacheRead(success bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    c.TotalReads++
    c.ReadsThisSecond++
    
    if success {
        c.SuccessfulReads++
    } else {
        c.FailedReads++
    }
    
    c.updateHitRate()
}

// RecordCacheWrite records a cache write attempt
func (c *CacheMetrics) RecordCacheWrite(success bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    c.TotalWrites++
    c.WritesThisSecond++
    
    if success {
        c.SuccessfulWrites++
    } else {
        c.FailedWrites++
    }
    
    c.updateSuccessRate()
}

// updateHitRate calculates hit rate (must be locked)
func (c *CacheMetrics) updateHitRate() {
    if c.TotalReads == 0 {
        c.HitRate = 0
    } else {
        c.HitRate = float64(c.SuccessfulReads) / float64(c.TotalReads) * 100.0
    }
}

// updateSuccessRate calculates overall success rate (must be locked)
func (c *CacheMetrics) updateSuccessRate() {
    totalOps := c.TotalReads + c.TotalWrites
    if totalOps == 0 {
        c.SuccessRate = 0
    } else {
        successOps := c.SuccessfulReads + c.SuccessfulWrites
        c.SuccessRate = float64(successOps) / float64(totalOps) * 100.0
    }
}

// ResetPerSecondMetrics resets the per-second counters (call once per second)
func (c *CacheMetrics) ResetPerSecondMetrics() {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    c.ReadsThisSecond = 0
    c.WritesThisSecond = 0
    c.LastResetTime = time.Now()
}

// GetCacheMetrics returns metrics atomically
func (c *CacheMetrics) GetMetrics() (hits, reads, writes, failures int64, rate float64) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    
    return c.SuccessfulReads, c.TotalReads, c.TotalWrites, c.FailedReads + c.FailedWrites, c.HitRate
}
```

#### `internal/metrics/persistence.go` - File Storage

```go
package metrics

import (
    "encoding/json"
    "os"
    "path/filepath"
    "sync"
    "time"
)

const metricsFileName = "model_metrics.json"

type PersistenceData struct {
    Version    int                      `json:"version"`
    ModelStats map[string]*ModelMetrics `json:"model_stats"`
    UpdatedAt  time.Time                `json:"updated_at"`
}

// NewMetricsStore creates a new metrics store and loads persisted data
func NewMetricsStore(persistPath string) *MetricsStore {
    store := &MetricsStore{
        TPS: &TPSMetrics{
            SnapshotWindowSize: 10,
            SnapshotInterval:   100 * time.Millisecond,
            Snapshots:          make([]TPSSnapshot, 0, 20),
        },
        Cache: &CacheMetrics{
            LastResetTime: time.Now(),
        },
        ModelStats:  make(map[string]*ModelMetrics),
        PersistPath: persistPath,
    }
    
    // Load existing metrics from disk
    store.loadFromDisk()
    
    return store
}

// LoadFromDisk reads metrics from disk
func (s *MetricsStore) loadFromDisk() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    path := filepath.Join(s.PersistPath, metricsFileName)
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil // No existing data, start fresh
        }
        return err
    }
    
    var persisted PersistenceData
    if err := json.Unmarshal(data, &persisted); err != nil {
        return err
    }
    
    s.ModelStats = persisted.ModelStats
    return nil
}

// SaveToDisk persists metrics to disk
func (s *MetricsStore) SaveToDisk() error {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    persisted := PersistenceData{
        Version:    1,
        ModelStats: s.ModelStats,
        UpdatedAt:  time.Now(),
    }
    
    data, err := json.MarshalIndent(persisted, "", "  ")
    if err != nil {
        return err
    }
    
    path := filepath.Join(s.PersistPath, metricsFileName)
    
    // Create directory if needed
    if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
        return err
    }
    
    return os.WriteFile(path, data, 0644)
}

// SetCurrentModel switches to tracking a different model
func (s *MetricsStore) SetCurrentModel(modelID, modelName string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // Flush previous model's session metrics
    if s.CurrentModel != "" && s.CurrentModel != modelID {
        s.flushCurrentSessionLocked()
    }
    
    s.CurrentModel = modelID
    
    // Ensure model entry exists in stats
    if _, exists := s.ModelStats[modelID]; !exists {
        s.ModelStats[modelID] = &ModelMetrics{
            ModelID:   modelID,
            ModelName: modelName,
        }
    }
}

// FlushCurrentSession saves session metrics to model history
func (s *MetricsStore) FlushCurrentSession() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.flushCurrentSessionLocked()
}

// flushCurrentSessionLocked updates model stats with current session data (must be locked)
func (s *MetricsStore) flushCurrentSessionLocked() {
    if s.CurrentModel == "" {
        return
    }
    
    stats, exists := s.ModelStats[s.CurrentModel]
    if !exists || stats == nil {
        return
    }
    
    // Update TPS history
    if s.TPS.PeakTPS > stats.HistoricalPeakTPS {
        stats.HistoricalPeakTPS = s.TPS.PeakTPS
    }
    
    // Update streaming duration and average
    sessionDuration := time.Since(s.TPS.StreamStartTime).Seconds()
    if sessionDuration > 0 {
        sessionTokens := float64(s.TPS.CurrentTokens - s.TPS.TokensAtStart)
        
        // Update historical data
        oldTotal := stats.TotalTokensGenerated
        oldSecs := stats.TotalStreamingSecs
        
        stats.TotalTokensGenerated += int64(sessionTokens)
        stats.TotalStreamingSecs += sessionDuration
        stats.SessionCount++
        
        // Recalculate running average
        if stats.TotalStreamingSecs > 0 {
            stats.HistoricalAvgTPS = float64(stats.TotalTokensGenerated) / stats.TotalStreamingSecs
        }
    }
    
    // Update cache history (cumulative)
    stats.LifetimeCacheReads += s.Cache.TotalReads
    stats.LifetimeCacheWrites += s.Cache.TotalWrites
    stats.LifetimeCacheHits += s.Cache.SuccessfulReads
    stats.LifetimeCacheMisses += s.Cache.FailedReads
    stats.LifetimeFailedOps += s.Cache.FailedReads + s.Cache.FailedWrites
    
    stats.LastUpdated = time.Now()
}

// GetModelMetrics returns historical metrics for a model
func (s *MetricsStore) GetModelMetrics(modelID string) *ModelMetrics {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    return s.ModelStats[modelID]
}
```

---

## 2. Side Panel Integration

### `internal/chat/sidepanel_metrics.go` - UI Rendering

```go
package chat

import (
    "fmt"
    "github.com/charmbracelet/lipgloss/v2"
)

// renderTPSBar renders the tokens-per-second metric bar
func (s *SidePanel) renderTPSBar(app *App, th Theme) []string {
    var lines []string
    
    // Get current TPS metrics
    currentTPS, peakTPS, avgTPS := app.metrics.TPS.GetMetrics()
    
    // Title
    title := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.Text)).
        Bold(true).
        Render("TPS")
    lines = append(lines, title)
    
    // Progress bar (0-200 t/s scale, or use peak as max)
    maxTPS := peakTPS * 1.2 // Show some headroom
    if maxTPS < 100 {
        maxTPS = 100 // Minimum scale
    }
    
    percentage := (currentTPS / maxTPS) * 100
    if percentage > 100 {
        percentage = 100
    }
    
    barWidth := s.width - 4
    filledWidth := int(float64(barWidth) * percentage / 100)
    
    // Color coding based on TPS
    var barColor string
    if currentTPS > peakTPS*0.8 {
        barColor = th.Success // Green if close to peak (good performance)
    } else if currentTPS > peakTPS*0.5 {
        barColor = th.Warning // Yellow if moderate
    } else {
        barColor = th.TextDim // Gray if low
    }
    
    filledBar := lipgloss.NewStyle().
        Foreground(lipgloss.Color(barColor)).
        Render("|" + repeatStr("▮", filledWidth-2) + "|")
    emptyBar := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.Border)).
        Render(repeatStr("░", barWidth-filledWidth))
    
    progressBar := filledBar + emptyBar
    lines = append(lines, progressBar)
    
    // Metrics detail line: "125.3 | Peak: 150.2 | Avg: 118.5"
    details := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.TextMuted)).
        Render(fmt.Sprintf("%.1f | P:%.1f | A:%.1f", currentTPS, peakTPS, avgTPS))
    lines = append(lines, details)
    
    return lines
}

// renderCacheBar renders the cache metrics bar
func (s *SidePanel) renderCacheBar(app *App, th Theme) []string {
    var lines []string
    
    // Get cache metrics
    hits, reads, writes, failures, hitRate := app.metrics.Cache.GetMetrics()
    
    // Title
    title := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.Text)).
        Bold(true).
        Render("Cache")
    lines = append(lines, title)
    
    // Progress bar showing hit rate
    barWidth := s.width - 4
    filledWidth := int(float64(barWidth) * hitRate / 100)
    
    // Color based on hit rate
    var barColor string
    if hitRate >= 80 {
        barColor = th.Success // Green if high hit rate
    } else if hitRate >= 50 {
        barColor = th.Warning // Yellow
    } else {
        barColor = th.TextDim // Gray
    }
    
    filledBar := lipgloss.NewStyle().
        Foreground(lipgloss.Color(barColor)).
        Render(repeatStr("▮", filledWidth))
    emptyBar := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.Border)).
        Render(repeatStr("░", barWidth-filledWidth))
    
    progressBar := filledBar + emptyBar
    lines = append(lines, progressBar)
    
    // Metrics detail: "Hits: 45 | Reads: 50 | Fails: 3"
    hitRateStr := fmt.Sprintf("%.0f%%", hitRate)
    details := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.TextMuted)).
        Render(fmt.Sprintf("%s | H:%d | F:%d", hitRateStr, hits, failures))
    lines = append(lines, details)
    
    return lines
}
```

### Cache Invalidation in `sidepanel.go` Render

```go
// In SidePanel.Render() cache validation section (around line 124):

// Add to cache validation checks:
cache.tpsCurrentValue == currentTPS &&
cache.tpsPeakValue == peakTPS &&
cache.tpsAvgValue == avgTPS &&
cache.cacheHitRate == hitRate &&
cache.cacheHits == hits &&
cache.cacheFailures == failures &&
```

### Updated `sidePanelCache` struct in `app.go`

```go
// Add these fields to the sidePanelCache struct (line 491):
tpsCurrentValue float64
tpsPeakValue    float64
tpsAvgValue     float64
cacheHitRate    float64
cacheHits       int64
cacheFailures   int64
```

---

## 3. Integration with App

### `internal/chat/app.go` - Add Metrics Field

```go
// Add to App struct (around line 547):
metrics *metrics.MetricsStore  // New metrics tracking
```

### Initialize Metrics in `newApp()` 

```go
// In newApp() function:
metricsPath := getMetricsPath() // ~/.config/swarm-tui/metrics or similar
a.metrics = metrics.NewMetricsStore(metricsPath)

// Set initial model if available
if currentModel != "" {
    a.metrics.SetCurrentModel(currentModel, currentModel)
}
```

### Update Token Handler

```go
// In existing token update handlers (around line 2520, 3070, 3702):

// When tokens are received during streaming:
func (a *App) updateTokensAndMetrics(newTokenCount int) {
    a.tokenCount = newTokenCount
    
    // Update TPS metrics (new)
    a.metrics.TPS.UpdateTokens(newTokenCount)
    
    // Invalidate side panel cache
    a.sidePanelCache.valid = false
}
```

### Start/Stop Streaming

```go
// When streaming begins (around line 3063):
func (a *App) startStreaming() {
    // ... existing code ...
    a.metrics.TPS.StartStreaming(a.tokenCount)
}

// When streaming completes (around line 3600):
func (a *App) stopStreaming() {
    // ... existing code ...
    a.metrics.TPS.StopStreaming()
    a.metrics.FlushCurrentSession()
    a.metrics.SaveToDisk()
}
```

### Model Change Handler

```go
// When user changes model (in /model command or similar):
func (a *App) setCurrentModel(modelID, modelName string) {
    // ... existing code ...
    
    // Update metrics tracking for new model
    a.metrics.SetCurrentModel(modelID, modelName)
    a.sidePanelCache.valid = false
}
```

---

## 4. UI Layout

### Current Side Panel (38 chars wide)

```
┌──────────────────────────────────┐
│ Swarm v0.3.3                     │
│                                  │
│ Config                           │
│ anthropic - Claude 3.5 Sonnet    │
│ ✓ Caching (1h) - 92% hits        │
│   ↳ Read: 2.3k | Created: 1.2k   │
│ ✓ Micro-compact (keep 5)         │
│ ──────────────────────────────────│
│ Conversation                     │
│ ▮▮▮▮▮░░░░░░░░░░░░░░░░░░░░░░░░░░░│
│ 25.5% (149,234 remaining)        │
│ ──────────────────────────────────│
│ Audit Log                        │
│ ✓ main.go                        │
│ ✓ app.go                         │
│                                  │
│ Background Agents                │
│ ● Task 1: Analyzing code...      │
│ ──────────────────────────────────│
│ Tools                            │
│ • swarm_Read (1-10 of 23)        │
│ • swarm_Grep                     │
│ • swarm_Bash                     │
│ • swarm_Task                     │
│ ⬤ More tools... (13)             │
└──────────────────────────────────┘
```

### Updated Side Panel WITH New Bars

```
┌──────────────────────────────────┐
│ Swarm v0.3.3                     │
│                                  │
│ Config                           │
│ anthropic - Claude 3.5 Sonnet    │
│ ✓ Caching (1h) - 92% hits        │
│   ↳ Read: 2.3k | Created: 1.2k   │
│ ✓ Micro-compact (keep 5)         │
│ ──────────────────────────────────│
│ Conversation                     │
│ ▮▮▮▮▮░░░░░░░░░░░░░░░░░░░░░░░░░░░│
│ 25.5% (149,234 remaining)        │
│ ──────────────────────────────────│
│ TPS                    ← NEW      │
│ ▮▮▮▮░░░░░░░░░░░░░░░░░░░░░░░░░░░░│
│ 125.3|P:150.2|A:118.5  ← NEW     │
│ ──────────────────────────────────│
│ Cache                  ← NEW      │
│ ▮▮▮▮▮▮▮▮░░░░░░░░░░░░░░░░░░░░░░░░│
│ 92% | H:45 | F:1       ← NEW     │
│ ──────────────────────────────────│
│ Audit Log                        │
│ ✓ main.go                        │
│ ✓ app.go                         │
│                                  │
│ Background Agents                │
│ ● Task 1: Analyzing code...      │
│ ──────────────────────────────────│
│ Tools                            │
│ • swarm_Read (1-10 of 23)        │
│ • swarm_Grep                     │
│ • swarm_Bash                     │
│ • swarm_Task                     │
│ ⬤ More tools... (13)             │
└──────────────────────────────────┘
```

---

## 5. Implementation Checklist

### Phase 1: Core Metrics Infrastructure
- [ ] Create `internal/metrics/types.go` with data structures
- [ ] Create `internal/metrics/tps_tracker.go` with TPS calculation
- [ ] Create `internal/metrics/cache_tracker.go` with cache tracking
- [ ] Create `internal/metrics/persistence.go` for disk storage
- [ ] Add `metrics` field to `App` struct in `app.go`
- [ ] Initialize metrics store in `newApp()`

### Phase 2: Streaming Integration
- [ ] Find all token update locations in `app.go` (lines 2520, 3070, 3702, etc.)
- [ ] Hook `metrics.TPS.UpdateTokens()` to token updates
- [ ] Add `metrics.TPS.StartStreaming()` at streaming start
- [ ] Add `metrics.TPS.StopStreaming()` at streaming end
- [ ] Add model change handler for `metrics.SetCurrentModel()`
- [ ] Add periodic save: `metrics.SaveToDisk()` (e.g., every 5 sec or on exit)

### Phase 3: Cache Integration  
- [ ] Find cache operation points in SDK/provider code
- [ ] Hook `metrics.Cache.RecordCacheRead/Write()` calls
- [ ] Ensure failure cases call the methods with `success=false`
- [ ] Validate cache metrics are updating correctly

### Phase 4: UI Rendering
- [ ] Create `internal/chat/sidepanel_metrics.go`
- [ ] Implement `renderTPSBar()` function
- [ ] Implement `renderCacheBar()` function
- [ ] Update `SidePanel.Render()` to call new bar functions
- [ ] Add new bars to sections slice (after Conversation section, before Audit Log)
- [ ] Update cache validation in side panel

### Phase 5: Testing & Polish
- [ ] Add unit tests for TPS calculations (mock token updates)
- [ ] Add unit tests for cache metrics (mock operations)
- [ ] Test persistence (write, exit, restart, verify data loads)
- [ ] Manual testing: stream a message and verify TPS bar updates
- [ ] Manual testing: verify metrics persist per model
- [ ] Verify cache operations are recorded correctly
- [ ] Performance test: ensure metrics don't slow down rendering

### Phase 6: Documentation
- [ ] Update README with metrics feature description
- [ ] Add comments to metrics package
- [ ] Document data persistence format
- [ ] Document model metrics file location

---

## 6. Performance Considerations

### TPS Calculation
- **Debounce Interval**: 100ms (only calculate every 100ms max)
- **Snapshot Window**: 10 snapshots (rolling average)
- **EMA Smoothing**: Alpha=0.3 (reduces jitter)
- **Lock Contention**: Use RWMutex, short critical sections

### Cache Metrics
- **Per-operation Recording**: O(1) operation
- **Atomic Reads**: Use GetMetrics() for thread-safe reads
- **Per-second Reset**: Call once per second from main loop

### Side Panel Rendering
- **Cache Hit Rate**: Cached render returned when metrics unchanged
- **Memory Footprint**: ~500 bytes for side panel cache
- **Compute Cost**: ~10ms for full render (with new bars)

---

## 7. File Structure

```
internal/
├── metrics/
│   ├── types.go              (data structures)
│   ├── tps_tracker.go        (TPS calculation)
│   ├── cache_tracker.go      (cache metrics)
│   └── persistence.go        (disk I/O)
└── chat/
    ├── app.go               (metrics field + initialization)
    ├── sidepanel.go         (cache invalidation integration)
    └── sidepanel_metrics.go (NEW: bar rendering)
```

---

## 8. Data Persistence

### File Location
```
~/.config/swarm-tui/metrics/model_metrics.json
```

### File Format
```json
{
  "version": 1,
  "model_stats": {
    "gpt-4": {
      "model_id": "gpt-4",
      "model_name": "GPT-4",
      "historical_peak_tps": 150.2,
      "historical_avg_tps": 118.5,
      "total_tokens_generated": 245000,
      "total_streaming_seconds": 2048.3,
      "session_count": 23,
      "lifetime_cache_hits": 1250,
      "lifetime_cache_misses": 145,
      "lifetime_cache_reads": 1395,
      "lifetime_cache_writes": 453,
      "lifetime_failed_ops": 8,
      "last_updated": "2024-01-15T14:30:45Z"
    },
    "claude-opus": {
      "model_id": "claude-opus",
      "model_name": "Claude 3 Opus",
      "historical_peak_tps": 175.8,
      ...
    }
  },
  "updated_at": "2024-01-15T14:30:45Z"
}
```

---

## 9. Color Scheme

### TPS Bar
- **Green**: Current TPS >= 80% of peak (good performance)
- **Yellow**: Current TPS 50-80% of peak (moderate)
- **Gray**: Current TPS < 50% of peak (low)

### Cache Bar  
- **Green**: Hit rate >= 80% (excellent cache performance)
- **Yellow**: Hit rate 50-80%
- **Gray**: Hit rate < 50%

---

## 10. Example Usage Flow

```
User starts streaming message with Claude 3.5 Sonnet:

1. App calls metrics.TPS.StartStreaming(initialTokens=100)
   → StreamStartTime = now, TokensAtStart = 100

2. First token arrives, app calls metrics.TPS.UpdateTokens(105)
   → Elapsed = 50ms, TokensDelta = 5, TPS = 100 t/s

3. More tokens arrive, called every 100-200ms:
   → Snapshots accumulated: [100, 95, 110, 105, ...]
   → CurrentTPS smoothed with EMA
   → PeakTPS updated if CurrentTPS > previous peak
   → AverageTPS calculated from snapshot window

4. Side panel renders, calls renderTPSBar():
   → Displays progress bar width proportional to CurrentTPS
   → Shows "125.3 | P:150.2 | A:118.5"
   → Color changes based on performance

5. Stream ends, app calls metrics.TPS.StopStreaming():
   → Finalizes AverageTPS
   → Calls metrics.FlushCurrentSession()
   → Updates ModelMetrics with historical data
   → Calls metrics.SaveToDisk()
   → Loads on next model selection for that model

6. User switches model, calls metrics.SetCurrentModel("gpt-4", "GPT-4"):
   → Previous session flushed
   → New model stats loaded from disk
   → UI shows new model's historical metrics
```

---

## 11. Testing Strategy

### Unit Tests

```go
// Test TPS calculation accuracy
func TestTPSCalculation(t *testing.T) {
    tps := &TPSMetrics{
        SnapshotWindowSize: 5,
        SnapshotInterval:   100 * time.Millisecond,
        Snapshots:          make([]TPSSnapshot, 0, 10),
    }
    
    tps.StartStreaming(0)
    
    // Simulate 100ms passing, 10 tokens received
    // Should calculate ~100 t/s
    // ... (mock time, call UpdateTokens)
}

// Test cache metrics accuracy
func TestCacheMetrics(t *testing.T) {
    cache := &CacheMetrics{}
    
    cache.RecordCacheRead(true)  // Hit
    cache.RecordCacheRead(false) // Miss
    cache.RecordCacheRead(true)  // Hit
    
    // Should have 2 hits, 1 miss, 66.67% hit rate
}

// Test persistence round-trip
func TestPersistence(t *testing.T) {
    store := NewMetricsStore(tmpDir)
    store.SetCurrentModel("gpt-4", "GPT-4")
    store.TPS.PeakTPS = 150.2
    
    store.SaveToDisk()
    
    // Load in new store
    store2 := NewMetricsStore(tmpDir)
    
    // Verify metrics loaded
}
```

### Integration Tests

```go
// Test with actual streaming
func TestMetricsWithStreaming(t *testing.T) {
    // Start app, begin streaming, verify metrics update
}
```

---

## 12. Migration Path

### Safe Rollout
1. Deploy metrics infrastructure (doesn't affect UI)
2. Deploy streaming integration (collects data silently)
3. Deploy UI rendering (shows new bars)
4. Verify metrics accuracy
5. Monitor for performance issues

### Backward Compatibility
- Metrics are optional (can function without them)
- Existing side panel works if metrics unavailable
- Old metrics file format supports version field

---

## Next Steps

1. **Review & Approve** this plan with team
2. **Create new package**: `internal/metrics/`
3. **Implement Phase 1**: Core infrastructure
4. **Test Phase 1**: Unit tests for calculations
5. **Implement Phase 2**: Streaming integration
6. **Integration testing**: End-to-end flow
7. **Implement Phase 3-4**: Cache and UI
8. **Manual testing**: Full feature validation
9. **Optimize**: Performance tuning if needed
10. **Deploy**: Roll out with monitoring

