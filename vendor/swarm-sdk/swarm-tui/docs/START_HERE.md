# Cache Debugging System - START HERE

## 🎯 What You Have

A **complete cache debugging system** that captures everything from raw Anthropic API through to your final message, exactly like Claude Code does.

## ⚡ Quick Start (5 minutes)

```bash
cd /home/swarm/SwarmCode/TUI
CACHE_DEBUG=1 go test -v ./internal/chat -run TestComprehensiveTrace
```

This will:
- Create 8 trace files showing the complete flow
- Create 1 NDJSON file with all events
- Output to: `/tmp/tui-trace/test-session-123/`

Then view the results:
```bash
cat /tmp/tui-trace/test-session-123/complete_trace_*.ndjson
```

## 📁 Files Created

**Code (ready to use):**
- `internal/chat/cache_debug.go` - Simple 3-format tracer
- `internal/chat/comprehensive_trace.go` - Complete event tracer ⭐ **USE THIS**
- `internal/chat/comprehensive_trace_test.go` - Working example

**Documentation (read in this order):**
1. `README_CACHE_DEBUGGING.md` - Overview & insights
2. `COMPLETE_TRACE_INTEGRATION.md` - How to integrate (4 lines of code)
3. `CACHE_DEBUG_GUIDE.md` - Detailed troubleshooting
4. `CACHE_DEBUG_QUICK_START.md` - Command reference

## 🔍 Your Cache Problem

**Symptom:** Both requests show `cache_creation_input_tokens` (should be: create then read)

```
Request 1: cache_creation: 19111, cache_read: 0        ✓ Expected
Request 2: cache_creation: 19309, cache_read: 0        ✗ Wrong! Should be: creation: 0, read: 19000+
```

**What the traces will show:**

Event 005 from Request 1 (creating cache):
```json
{
  "cache_creation_input_tokens": 15039,
  "cache_read_input_tokens": 0
}
```

Event 005 from Request 2 (should read cache):
```json
{
  "cache_creation_input_tokens": 0,
  "cache_read_input_tokens": 15039
}
```

If still wrong on Request 2, the traces will show you exactly which Anthropic response is wrong, and which header/config is missing.

## 🚀 Integration (10 minutes)

When ready to integrate into your code:

**File 1: `sdk/provider/anthropic/stream.go` (line ~225)**
```go
// Add this line before p.rawEventCallback
if trace != nil {
    trace.LogRawSSEEvent(event.Type, data)
}
```

**File 2: `sdk/provider/anthropic/translate.go` (line ~1000)**
```go
// Add these lines before return statement
if trace != nil {
    trace.LogCanonical(resp, nil)
    trace.LogFinalMessage(msg)
}
```

**Enable with:**
```bash
export CACHE_DEBUG=1
```

Full details in: `COMPLETE_TRACE_INTEGRATION.md`

## 📊 Understanding the Trace Output

Each trace creates numbered files showing the transformation stages:

```
001_raw_sse_message_start.json       ← Raw from Anthropic
002_raw_sse_content_block_start.json
003_raw_sse_content_block_delta.json
004_stream_chunk_.json               ← After initial processing
005_raw_sse_message_delta.json       ← FINAL metrics (key!)
006_after_message_delta_.json        ← Our extraction
007_canonical_.json                  ← Provider format
008_final_message_.json              ← Your application sees this
complete_trace_*.ndjson              ← All events in one file
```

**Key file:** `005_raw_sse_message_delta.json` - Shows what Anthropic actually returned

## 🔧 Debugging Strategy

1. **Run example test** - Confirm system works
2. **Integrate 4 lines of code** - Add logging to real code
3. **Run with real requests** - `CACHE_DEBUG=1 go run ./cmd/...`
4. **Compare traces** - Request 1 vs Request 2
5. **Find the issue** - Which event shows the problem?
6. **Fix & verify** - Run again, check Request 2 now uses cache_read

## ✅ Verification Checklist

After running the example test:

- ☐ Files created in `/tmp/tui-trace/test-session-123/`
- ☐ `001_raw_sse_*.json` through `008_final_message_*.json` exist
- ☐ `complete_trace_*.ndjson` has 8 lines (one per event)
- ☐ Cache metrics appear in events 005, 006, 007, 008
- ☐ Metrics are identical across all stages

## 🎓 What This Teaches You

You'll understand:
- Exact format Anthropic sends
- Exact transformations happening in your code
- Where cache metrics appear/disappear
- Token calculation at each step
- Whether headers/config are working

## 📚 Documentation Guide

| Document | Read When | Length |
|----------|-----------|--------|
| `README_CACHE_DEBUGGING.md` | Want overview | 5 min |
| `COMPLETE_TRACE_INTEGRATION.md` | Ready to integrate | 10 min |
| `CACHE_DEBUG_GUIDE.md` | Troubleshooting | 15 min |
| `CACHE_DEBUG_QUICK_START.md` | Need quick reference | 2 min |

## ⚙️ System Architecture

```
Raw Anthropic SSE Event
        ↓
    stream.py: trace.LogRawSSEEvent()
        ↓
Process as Stream Chunks
        ↓
    stream.go: trace.LogStreamChunk()
        ↓
Build Canonical Response
        ↓
    translate.go: trace.LogCanonical()
        ↓
Create Final Message
        ↓
    translate.go: trace.LogFinalMessage()
        ↓
Complete Trace File
```

Each step is logged with:
- Raw data at that stage
- Accumulated state so far
- Cache metrics if applicable

## 🎯 Expected Results

After proper integration and with cache working correctly:

**Request 1:**
```
cache_creation_input_tokens: 15039
cache_read_input_tokens: 0
```
→ Creating cache with system prompt

**Request 2 (< 5 min later):**
```
cache_creation_input_tokens: 0
cache_read_input_tokens: 15039
```
→ Cache hit! Reusing system prompt

**Cost savings:**
- Creation tokens cost 1.25x (writing to cache)
- Read tokens cost 0.1x (reading from cache)
- 10x cheaper on cache hits!

## 🚦 Next Steps

1. **This minute**: Run the example test
   ```bash
   CACHE_DEBUG=1 go test -v ./internal/chat -run TestComprehensiveTrace
   ```

2. **Next 5 minutes**: Examine `/tmp/tui-trace/test-session-123/`
   ```bash
   cat /tmp/tui-trace/test-session-123/complete_trace_*.ndjson
   ```

3. **Next 10 minutes**: Read `README_CACHE_DEBUGGING.md`

4. **Next 10 minutes**: Integrate 4 lines of code (COMPLETE_TRACE_INTEGRATION.md)

5. **Next 5 minutes**: Run with real request and `CACHE_DEBUG=1`

6. **Next 15 minutes**: Analyze traces to find your cache issue

## ❓ Common Questions

**Q: Do I need to modify production code?**
A: No, it's optional. Just add 4 lines and enable with env var.

**Q: Will this slow things down?**
A: No, only when `CACHE_DEBUG=1` is set. Otherwise no overhead.

**Q: How do I know it's working?**
A: Compare event 005 from two requests. Should show creation on first, read on second.

**Q: Where do trace files go?**
A: `/tmp/tui-trace/[sessionID]/`

**Q: How long are traces kept?**
A: Until you delete them manually.

## 📝 Summary

You now have **complete visibility** into your cache system:
- ✅ See exact Anthropic responses
- ✅ Track metrics through all stages
- ✅ Identify where cache isn't working
- ✅ Prove fix is working

Ready? Run the test!

```bash
cd /home/swarm/SwarmCode/TUI && CACHE_DEBUG=1 go test -v ./internal/chat -run TestComprehensiveTrace
```

Then read: `README_CACHE_DEBUGGING.md`
