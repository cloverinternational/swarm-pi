# TUI Cache Debugging System - Complete Setup

## What You Have

A **three-tier debugging system** that captures everything from raw Anthropic API through to final message:

### Tier 1: Simple Cache Debug (cache_debug.go)
- Quick format comparison
- Good for: Understanding format transformation
- Output: 3 JSON files (raw, canonical, transformed)

### Tier 2: Comprehensive Trace (comprehensive_trace.go) ⭐ **USE THIS**
- Complete event-by-event logging
- Good for: Production debugging, seeing exact Anthropic responses
- Output: 8+ numbered files + NDJSON file
- Exactly like Claude Code's event logging

### Tier 3: Integration Guide (COMPLETE_TRACE_INTEGRATION.md)
- How to add tracing to your real code
- Just 4 lines of code changes needed

## Quick Start

### Test 1: See the example trace
```bash
cd /home/swarm/SwarmCode/TUI
CACHE_DEBUG=1 go test -v ./internal/chat -run TestComprehensiveTrace
```

### Test 2: View the trace files
```bash
# See directory structure
ls -lh /tmp/tui-trace/test-session-123/

# View individual event
cat /tmp/tui-trace/test-session-123/001_raw_sse_message_start.json

# View complete flow
cat /tmp/tui-trace/test-session-123/complete_trace_*.ndjson
```

### Test 3: Follow cache metrics through pipeline
```bash
# Raw from Anthropic
jq '.cache_metrics' /tmp/tui-trace/test-session-123/006_after_message_delta_*.json

# In canonical response
jq '.canonical_response.message.metadata.cache_metrics' /tmp/tui-trace/test-session-123/007_canonical_*.json

# In final message
jq '.final_message.metadata.cache_metrics' /tmp/tui-trace/test-session-123/008_final_message_*.json
```

## Files Created

| File | Purpose | Use Case |
|------|---------|----------|
| `internal/chat/cache_debug.go` | Simple 3-format comparison | Quick format checks |
| `internal/chat/comprehensive_trace.go` | Complete event tracing | Production debugging ⭐ |
| `internal/chat/comprehensive_trace_test.go` | Example showing full flow | Reference implementation |
| `CACHE_DEBUG_GUIDE.md` | Detailed debugging guide | When stuck |
| `COMPLETE_TRACE_INTEGRATION.md` | Integration into your code | When ready to integrate |

## The Problem You're Trying to Solve

**Question**: Why does both requests create cache instead of reading it?
```
Request 1: cache_creation_input_tokens: 19111, cache_read: 0 ✓ (expected)
Request 2: cache_creation_input_tokens: 19309, cache_read: 0 ✗ (wrong! should be read > 0)
```

**Root Causes** (to be discovered with traces):
1. Cache-control headers not being sent to Anthropic
2. System prompt doesn't have cache_control block
3. Cache TTL expired between requests (unlikely if < 5 min)
4. Something is changing the prompt hash between requests

**How to Debug with Traces**:
1. Run request 1 with `CACHE_DEBUG=1`
2. Check event 005 (message_delta) - look at `cache_creation_input_tokens` value
3. Check event 006 (after_message_delta) - confirm it extracted correctly
4. Check event 007 (canonical) - verify it's in metadata
5. Check event 008 (final_message) - verify it survived

Then repeat for request 2 - should see `cache_read_input_tokens` instead of creation.

## Integration Checklist

When you're ready to integrate into real code:

- [ ] Read `COMPLETE_TRACE_INTEGRATION.md`
- [ ] Open `sdk/provider/anthropic/stream.go`
- [ ] Add 4 trace logging calls (see integration guide)
- [ ] Enable with `export TRACE_ENABLED=1`
- [ ] Run real request
- [ ] Check `/tmp/tui-trace/[sessionID]/`
- [ ] Analyze trace files
- [ ] Compare request 1 vs request 2 traces
- [ ] Identify where cache metrics are lost (if they are)

## Key Insights

### What Each Event Shows

**001_raw_sse_message_start.json**
- What Anthropic tells you about the message
- First place to check token counts
- Shows if cache already being used

**005_raw_sse_message_delta.json**
- Final token metrics from Anthropic
- `cache_creation_input_tokens`: Tokens written to cache
- `cache_read_input_tokens`: Tokens read from cache
- `input_tokens`: Tokens after cache breakpoint

**006_after_message_delta_.json** (our calculation)
- Shows: `total_input_tokens = input + read + creation`
- This is what you should display to users

**007_canonical_.json**
- Provider-neutral format
- Cache metrics in `message.metadata`
- Token usage normalized

**008_final_message_.json**
- What your application sees
- Must have cache_metrics in metadata

### Token Calculation Formula

When cache is involved:
```
total_input_tokens = input_tokens + cache_read_input_tokens + cache_creation_input_tokens
displayed_to_user = total_input_tokens  (the full context size)
cost = (creation_tokens * 1.25) + (read_tokens * 0.1) + (generation_tokens)
```

## Debugging Commands

Find all cache metrics in traces:
```bash
grep -r cache_creation_input_tokens /tmp/tui-trace/*/
grep -r cache_read_input_tokens /tmp/tui-trace/*/
```

Compare two requests:
```bash
diff /tmp/tui-trace/session1/complete_trace_*.ndjson /tmp/tui-trace/session2/complete_trace_*.ndjson
```

Count events by type:
```bash
jq '.type' /tmp/tui-trace/test-session-123/complete_trace_*.ndjson | sort | uniq -c
```

Show just cache metrics over time:
```bash
jq 'select(.cache_metrics) | {sequence, cache_metrics}' /tmp/tui-trace/test-session-123/complete_trace_*.ndjson
```

## Next Actions

1. **Understand the system** (5 min)
   - Run the example test
   - Browse the trace files
   - Read the JSON structures

2. **Find your cache issue** (15 min)
   - Integrate logging into real code
   - Run with CACHE_DEBUG=1
   - Compare request 1 vs request 2 traces
   - Find where cache metrics change

3. **Fix the issue** (depends on root cause)
   - If cache_metrics missing: Fix translate.go
   - If always creating: Check cache-control headers
   - If not sent to API: Check stream.go

## Support Files

- **COMPLETE_TRACE_INTEGRATION.md** - How to integrate (4 lines of code)
- **CACHE_DEBUG_GUIDE.md** - Detailed troubleshooting
- **CACHE_DEBUG_QUICK_START.md** - Quick reference

## Summary

You now have **complete visibility** into the cache pipeline, exactly like Claude Code does. Every event is logged, every transformation is recorded, and you can replay the exact Anthropic API responses.

**Status**: ✅ Ready to use
**Next**: Run the test, then integrate into real code
**Goal**: Find and fix your cache issue with full visibility
