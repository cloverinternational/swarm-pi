# Complete Trace Integration Guide

This shows you how to capture the COMPLETE flow like Claude Code does, so you can recreate JSON 1:1.

## Key Insight

Your logs show that Claude Code logs EVERY event in sequence as NDJSON. Each event includes:
- Raw Anthropic SSE data
- Intermediate transformations
- Final message state
- Accumulated metadata at each step

This allows **perfect reconstruction** because you have every intermediate step.

## Integration Points

### 1. In `stream.go` - Log Raw SSE Events (Line ~225)

**Current code:**
```go
// Around line 225-230
data := strings.TrimPrefix(line, "data: ")

// Log raw API response before transformation
if p.rawEventCallback != nil {
    p.rawEventCallback.OnRawEvent("sse_data", data)
}
```

**Add comprehensive tracing:**
```go
data := strings.TrimPrefix(line, "data: ")

// NEW: Log raw SSE with complete trace
if trace != nil {  // trace is your ComprehensiveTrace instance
    trace.LogRawSSEEvent(event.Type, data)
}

if p.rawEventCallback != nil {
    p.rawEventCallback.OnRawEvent("sse_data", data)
}
```

### 2. When Building Stream Chunks (Line ~440)

**Add this:**
```go
// Before sending chunk
if trace != nil {
    accumulatedState := map[string]interface{}{
        "accumulated_text":      accumulatedText.String(),
        "accumulated_thinking":  accumulatedThinking.String(),
        "tool_calls":           len(toolCalls),
        "cache_metrics":        cacheMetrics,
    }
    trace.LogStreamChunk(chunk, accumulatedState)
}

chunks <- chunk
```

### 3. After Translation (translate.go - Line ~1000)

**Add this:**
```go
// Before returning ChatResponse
if trace != nil {
    trace.LogCanonical(&provider.ChatResponse{
        Message:      msg,
        FinishReason: finishReason,
        Usage:        usage,
    }, nil)
}

return &provider.ChatResponse{...}, nil
```

### 4. When Returning Final Message

**Add this:**
```go
if trace != nil {
    trace.LogFinalMessage(msg)
    traceFile := trace.WriteCompleteTrace()
    // Log where trace was written
}
```

## What This Gives You

**Individual event files** (numbered for sequence):
```
001_raw_sse_message_start.json
002_raw_sse_content_block_start.json
003_raw_sse_content_block_delta.json
...
008_final_message_.json
```

**NDJSON file** (like Claude Code):
```json
{"sequence":1,"stage":"raw_sse","type":"message_start","raw_sse":{...}}
{"sequence":2,"stage":"raw_sse","type":"content_block_start","raw_sse":{...}}
{"sequence":3,"stage":"raw_sse","type":"content_block_delta","raw_sse":{...}}
...
{"sequence":8,"stage":"final_message","type":"","final_message":{...}}
```

## Reconstruction Recipe

With this complete trace, you can **recreate everything**:

```python
# Read complete_trace_*.ndjson
events = []
with open('complete_trace_*.ndjson') as f:
    for line in f:
        events.append(json.loads(line))

# Reconstruct:
# 1. Get all raw SSE events (stage == 'raw_sse')
# 2. Process in sequence order
# 3. Accumulate text/thinking/tool_calls
# 4. Final message in last event has everything

raw_events = [e for e in events if e['stage'] == 'raw_sse']
# Now you have the original API stream

# Get final state
final_event = [e for e in events if e['stage'] == 'final_message'][0]
final_message = final_event['final_message']
# This is what your application sees
```

## Debugging Cache Issues

With the complete trace:

1. **Find cache creation event:**
   ```bash
   grep cache_creation_input_tokens complete_trace_*.ndjson | head -1
   ```
   Should show `cache_creation_input_tokens: 19111` on first request

2. **Find cache read event:**
   ```bash
   grep cache_read_input_tokens complete_trace_*.ndjson | head -1
   ```
   On second request within 5 minutes, should show `cache_read_input_tokens > 0`

3. **Track token calculation:**
   ```bash
   grep -A2 -B2 total_input_tokens complete_trace_*.ndjson
   ```
   Shows: `input_tokens + cache_read_tokens + cache_creation_tokens`

4. **Verify metadata preservation:**
   ```bash
   jq '.final_message.metadata.cache_metrics' 008_final_message_*.json
   ```
   Must show cache metrics in final message

## Environment Variable

Enable tracing:
```bash
export TRACE_ENABLED=1
# OR
export CACHE_DEBUG=1  # Also enables cache debugging
```

## Example Usage in Code

```go
// At the start of streaming
sessionID := req.SessionID
messageID := resp.Message.ID
trace := NewComprehensiveTrace(sessionID, messageID)

// In processSSEStream
for scanner.Scan() {
    // ...
    if trace != nil {
        trace.LogRawSSEEvent(event.Type, data)
    }
}

// At the end
if trace != nil {
    traceFile := trace.WriteCompleteTrace()
    fmt.Printf("Trace written to: %s\n", traceFile)
}
```

## Benefits

✅ **Perfect reconstruction** - Every step recorded
✅ **Cache debugging** - See exactly when cache created/read
✅ **Metadata tracking** - Follow data through all transforms
✅ **Performance analysis** - See token counts at each stage
✅ **Format validation** - Verify all fields preserved
✅ **Error diagnosis** - Trace where things went wrong

## Test Output Location

Traces go to: `/tmp/tui-trace/[sessionID]/`

View all events:
```bash
ls -lh /tmp/tui-trace/*/
cat /tmp/tui-trace/*/complete_trace_*.ndjson
```
