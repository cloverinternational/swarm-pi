# Cache Format Debugging Guide

## Overview
This guide helps you capture and compare the three cache formats:
1. **Raw Anthropic JSON** - Direct from the API
2. **Canonical Format** - provider.ChatResponse with translated fields
3. **Transformed Format** - conversation.Message ready for use

## Quick Start

### Option 1: Test with Unit Tests
```bash
cd /home/swarm/SwarmCode/TUI
go test -v ./internal/chat -run TestCacheDebugger
ls -la /tmp/cache-debug/
```

### Option 2: Real Integration with Stream Events

Edit `sdk/provider/anthropic/stream.go` and add this logging after line 363:

```go
// Around line 363 in message_delta case
if event.Usage != nil {
    // ADD THIS LOGGING:
    if os.Getenv("CACHE_DEBUG") != "" {
        rawJSON, _ := json.MarshalIndent(event.Usage, "", "  ")
        filename := fmt.Sprintf("/tmp/cache-debug/stream_usage_%d.json", time.Now().UnixNano())
        os.WriteFile(filename, rawJSON, 0644)
    }
    
    // ... rest of code
}
```

## What to Look For

### Raw Anthropic Format
**Expected fields:**
```json
{
  "type": "message_delta",
  "delta": { "stop_reason": "end_turn" },
  "usage": {
    "input_tokens": 7,
    "output_tokens": 204,
    "cache_creation_input_tokens": 19111,
    "cache_read_input_tokens": 0
  }
}
```

**What to check:**
- Are `cache_creation_input_tokens` and `cache_read_input_tokens` present?
- On first request: `cache_creation > 0, cache_read = 0`
- On subsequent requests: `cache_creation = 0, cache_read > 0`

### Canonical Format (provider.ChatResponse)
**Expected structure:**
```json
{
  "message": {
    "role": "assistant",
    "content": "...",
    "metadata": {
      "cache_metrics": {
        "cache_creation_tokens": 19111,
        "cache_read_tokens": 0
      }
    }
  },
  "finish_reason": "end_turn",
  "usage": {
    "input": 19118,
    "output": 204
  },
  "cache_metrics": { ... }
}
```

**What to check:**
- Is `cache_metrics` properly populated in `Message.Metadata`?
- Are token counts correctly extracted?
- Is the structure preserved from the raw response?

### Transformed Format (conversation.Message)
**Expected structure:**
```json
{
  "role": "assistant",
  "content": "Hi! 👋",
  "metadata": {
    "cache_metrics": {
      "cache_creation_tokens": 19111,
      "cache_read_tokens": 0
    }
  },
  "thinking": "",
  "citations": []
}
```

**What to check:**
- Is `cache_metrics` preserved in metadata?
- Are any cache fields lost during transformation?
- Is the message content clean and readable?

## Analysis Steps

1. **Compare Raw → Canonical**
   ```bash
   diff /tmp/cache-debug/1_raw_anthropic_*.json /tmp/cache-debug/2_canonical_*.json
   ```
   Check if cache fields are properly extracted

2. **Compare Canonical → Transformed**
   ```bash
   diff /tmp/cache-debug/2_canonical_*.json /tmp/cache-debug/3_transformed_*.json
   ```
   Check if cache metrics survive the transformation

3. **Check Token Calculations**
   ```bash
   jq '.usage' /tmp/cache-debug/2_canonical_*.json | head -20
   # Expected: input should be (input_tokens + cache_read + cache_creation)
   ```

## Common Issues & Fixes

### Issue: `cache_creation_input_tokens` always high on second request

**Root cause:** Cache headers not being sent in follow-up requests

**Check in stream.go around line 95:**
```go
// Verify cache_control headers are being set
if len(p.config.BetaHeaders) > 0 {
    // Check anthropic-beta header includes cache control
}
```

### Issue: Cache metrics missing in metadata

**Root cause:** translate.go not storing cache_metrics correctly

**Check translate.go lines 990-1020:**
```go
// Ensure this code is running:
if anthropicResp.Usage.CacheCreationInputTokens != nil || ... {
    cacheMetrics := make(map[string]int)
    // ... populate cacheMetrics ...
    msg.Metadata["cache_metrics"] = cacheMetrics
}
```

### Issue: Fields lost during transformation

**Root cause:** conversation.Message not preserving metadata

**Check if metadata is being copied:**
```go
msg.Metadata = chatResp.Message.Metadata  // Should preserve all fields
```

## Integration Points

1. **Stream processing** (`stream.go` line 363)
   - Extract cache metrics from `event.Usage`
   - Store in `cacheMetrics` map

2. **Translation** (`translate.go` line 1000)
   - Convert `cacheMetrics` to message metadata
   - Ensure both legacy and new formats are handled

3. **Final message** (conversation.Message)
   - Metadata should include `cache_metrics`
   - No fields should be lost

## Debug Environment Variables

Add to your shell or test:
```bash
export CACHE_DEBUG=1
export LOG_LEVEL=debug
export CACHE_DEBUG_DIR=/tmp/cache-debug
```

## Testing Sequential Requests

Run this test sequence to verify cache is working:

```bash
# Request 1 - Should create cache
claude code "hi"  # Check cache_creation_tokens > 0

# Wait 2 seconds
sleep 2

# Request 2 - Should read cache
claude code "hi"  # Check cache_read_tokens > 0, cache_creation = 0

# Request 3 - After 5+ min, may recreate cache
sleep 300
claude code "hi"  # Check cache behavior (should still read if < 5 min)
```

Compare the three debug files for each request to see the caching flow.
