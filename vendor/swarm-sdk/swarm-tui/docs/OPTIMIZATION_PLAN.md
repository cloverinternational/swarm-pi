# Optimization Plan: Decoupled Architecture

## Problem Analysis (CS Intro Style)

### Current Architecture Issues

1. **Synchronous Blocking Chain**
   ```
   User types → Process → Marshal JSON → Write File → Update UI → Render
   ```
   Each operation blocks the next. If any step is slow, UI freezes.

2. **Tight Coupling**
   - UI code calls storage directly
   - Storage calls JSON marshaling directly
   - No separation of concerns

3. **Resource Contention**
   - UI thread competes with I/O operations
   - JSON marshaling happens on critical path
   - No pre-computation or caching

## Solution: Producer-Consumer Decoupling

### Core Concept
Think of it like a restaurant:
- **Kitchen (Processing Thread)**: Takes orders, cooks food, rings bell when ready
- **Waiter (UI Thread)**: Takes orders to kitchen, delivers ready food to tables
- **Order Queue**: Written orders go in
- **Ready Queue**: Completed dishes come out

### Components Needed

#### 1. Message Queues (Channels)
```go
type WorkItem struct {
    ID        string
    Type      WorkType  // MESSAGE, TOOL_CALL, STORAGE_SAVE
    Priority  int       // Higher priority processed first
    Payload   interface{}
    Timestamp time.Time
}

type StateUpdate struct {
    Type      UpdateType // NEW_MESSAGE, TOOL_RESULT, SAVE_COMPLETE
    Data      interface{}
    Version   uint64     // For ordering updates
}
```

#### 2. Processing Thread
```go
// Pseudocode for clarity
func ProcessingLoop() {
    for {
        work := <-workQueue
        
        switch work.Type {
        case MESSAGE:
            // Heavy processing here
            result := processMessage(work)
            stateUpdates <- StateUpdate{NEW_MESSAGE, result}
            
        case STORAGE_SAVE:
            // Async save, don't block
            go saveAsync(work)
        }
    }
}
```

#### 3. UI Thread  
```go
func UILoop() {
    ticker := time.NewTicker(16 * time.Millisecond) // 60 FPS
    
    for {
        select {
        case <-ticker.C:
            // Always render at 60 FPS
            render(currentState)
            
        case update := <-stateUpdates:
            // Apply updates between frames
            currentState.Apply(update)
        }
    }
}
```

#### 4. Pre-computation Cache
```go
type RenderCache struct {
    // Pre-rendered message HTML/markdown
    messageCache map[string]RenderedContent
    
    // Pre-computed view layouts
    layoutCache  map[ViewportSize]Layout
    
    // Pre-marshaled JSON for common objects
    jsonCache    map[string][]byte
}
```

## Implementation Steps

### Phase 1: Decouple Storage (Quick Win)
1. Make all storage operations async
2. Use a dedicated storage worker pool
3. Return immediately with a "pending" status

### Phase 2: Optimize JSON (Biggest Impact)
1. Pre-marshal common message formats
2. Use Sonic properly with buffer pools
3. Cache marshaled output

### Phase 3: Full Architecture (Complete Solution)
1. Separate UI and processing threads
2. Implement message queues
3. Add pre-rendering cache
4. Make UI purely reactive

## Measuring Success

### Metrics to Track
1. **Frame Time Consistency**: Std deviation should be < 1ms
2. **P99 Frame Time**: Should be < 16.67ms (60 FPS)
3. **Message Processing Latency**: Can be 100-200ms (not on UI thread)
4. **Memory Usage**: Should stay under 128MB for Cloudflare

### Expected Improvements
- UI latency: 487μs → <100μs (80% reduction)
- Frame drops: 10% → <1%
- Goroutines: 14 → 8-10 (further reduction)
- User perception: Instant response even during heavy processing