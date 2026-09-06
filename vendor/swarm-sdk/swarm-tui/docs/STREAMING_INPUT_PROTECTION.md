# Streaming Input Protection Fix

## Problem

When streaming responses from the LLM, the TUI would frequently update the viewport to show new content chunks. These rapid viewport updates (potentially hundreds per second) caused the entire UI to re-render, including the input component. This led to:

1. **Cursor position disruption** - The cursor would jump or disappear while typing
2. **Input state corruption** - Characters could be lost or duplicated
3. **Poor user experience** - Users couldn't reliably type while the agent was responding

## Root Cause

The issue occurred because:

1. `updateStreamingMessageIncremental()` was called on every streaming chunk
2. Each call triggered a full UI re-render via the `View()` function
3. The input component's state (cursor position, text buffer) was being reconstructed on each render
4. Rapid re-renders interfered with user input events being processed

## Solution

The fix implements a **debounced viewport update** system with three key components:

### 1. Input Protection State

Added to the `App` struct:

```go
inputProtection struct {
    lastUpdateTime time.Time     // Last time viewport was updated
    debounceDelay  time.Duration  // Minimum time between updates (50ms)
    pendingUpdate  bool           // True if update is pending
}
```

### 2. Debounced Update Logic

Modified `updateStreamingMessageIncremental()` to:

- Track the time since the last viewport update
- Skip updates that occur within the debounce window (50ms)
- Mark updates as "pending" when debounced
- Allow updates to proceed once the debounce window has passed

```go
now := time.Now()
timeSinceLastUpdate := now.Sub(a.inputProtection.lastUpdateTime)

// If we updated recently, mark as pending and skip
if timeSinceLastUpdate < a.inputProtection.debounceDelay {
    a.inputProtection.pendingUpdate = true
    return
}

// Proceed with update
a.inputProtection.lastUpdateTime = now
a.inputProtection.pendingUpdate = false
// ... render viewport ...
```

### 3. Pending Update Flushing

Two mechanisms ensure pending updates are eventually applied:

**a) Animation Tick Handler**
```go
case AnimationTickMsg:
    // Check for pending updates and flush if debounce delay has passed
    if a.inputProtection.pendingUpdate && a.streamingInProgress {
        if timeSinceLastUpdate >= a.inputProtection.debounceDelay {
            a.updateStreamingMessageIncremental()
        }
    }
```

**b) Stream End Handler**
```go
case streamDoneMsg:
    // Flush any pending updates before final render
    if a.inputProtection.pendingUpdate {
        a.inputProtection.lastUpdateTime = time.Time{}
        a.updateStreamingMessageIncremental()
    }
```

## Performance Impact

The debouncing provides significant benefits:

- **Update frequency**: Reduced from unlimited to max 20 FPS (50ms debounce)
- **Test results**: 8.33x reduction in viewport updates (25 attempts → 3 actual updates)
- **User experience**: Smooth streaming with no input disruption
- **CPU usage**: Lower due to fewer re-renders

## Configuration

The debounce delay is set in `NewAppWithOptions()`:

```go
app.inputProtection.debounceDelay = 50 * time.Millisecond // 20 FPS max
```

This can be adjusted if needed:
- **Lower values** (e.g., 25ms): More responsive, but more CPU usage
- **Higher values** (e.g., 100ms): Less CPU usage, but less smooth streaming
- **Recommended**: 50ms provides the best balance

## Testing

Three comprehensive tests verify the implementation:

1. **TestInputProtectionDebouncing** - Verifies basic debouncing behavior
2. **TestInputProtectionFlushOnStreamEnd** - Ensures pending updates are flushed
3. **TestInputProtectionUpdateFrequency** - Measures update frequency reduction

All tests pass successfully.

## Migration Notes

This fix is **backward compatible** - no changes required to existing code. The debouncing is automatic and applies only during streaming.

## Future Improvements

Potential enhancements:

1. **Adaptive debouncing** - Adjust delay based on streaming speed
2. **Input-aware rendering** - Skip viewport updates when user is actively typing
3. **Separate render cycles** - Completely isolate input rendering from viewport
4. **Virtual scrolling** - Only render visible portions of large conversations

## References

- **Implementation**: `internal/chat/app.go` (lines 420-432, 843-848, 2934-2959, 9838-9895)
- **Tests**: `internal/chat/streaming_input_protection_test.go`
- **Related**: Viewport rendering (`updateViewportContent`), Animation system (`AnimationTickMsg`)
