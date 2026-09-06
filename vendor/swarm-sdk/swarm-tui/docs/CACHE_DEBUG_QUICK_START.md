# Quick Reference: Using the Cache Debugger

## Files Created

1. **`internal/chat/cache_debug.go`** - CacheDebugger utility class
2. **`internal/chat/cache_debug_test.go`** - Example test showing how to use it
3. **`CACHE_DEBUG_GUIDE.md`** - Comprehensive debugging guide
4. **`debug-cache.sh`** - Bash script to run debugging
5. **`/tmp/cache-debug/`** - Output directory with formatted JSON files

## How to Use in Your Code

### Option 1: Direct Integration into Stream Handler

In `sdk/provider/anthropic/stream.go`, add logging after line 363:

```go
case "message_delta":
    if event.Usage != nil {
        // Existing code...
        
        // ADD THIS for debugging:
        if os.Getenv("CACHE_DEBUG") != "" {
            debugger := NewCacheDebugger()
            debugger.LogRawAnthropicResponse(event.Usage, "message_delta")
        }
```

### Option 2: After Translation (translate.go)

Around line 1000, after building cache_metrics:

```go
// NEW: Log canonical format
if os.Getenv("CACHE_DEBUG") != "" {
    debugger := NewCacheDebugger()
    debugger.LogCanonicalFormat(&provider.ChatResponse{
        Message:      msg,
        Usage:        usage,
        FinishReason: finishReason,
    })
    debugger.LogTransformedFormat(msg)
}
```

### Option 3: In Tests (Recommended to Start)

```go
func TestCachePipeline(t *testing.T) {
    debugger := NewCacheDebugger()
    
    // 1. Capture raw Anthropic response
    rawResp := getAnthropicResponse()
    debugger.LogRawAnthropicResponse(rawResp, "message_delta")
    
    // 2. After translation
    translated := translateAnthropicToCanonicl(rawResp)
    debugger.LogCanonicalFormat(translated)
    
    // 3. Final message
    debugger.LogTransformedFormat(translated.Message)
    
    // 4. Compare the three files
    t.Log(debugger.ComparisonReport())
}
```

## Reading the Debug Output

After running with `CACHE_DEBUG=1`:

```bash
# See all debug files
ls -lh /tmp/cache-debug/

# Compare formats side-by-side
diff -y /tmp/cache-debug/1_raw_* /tmp/cache-debug/2_canonical_*

# Pretty print individual files
jq . /tmp/cache-debug/1_raw_*.json
jq . /tmp/cache-debug/2_canonical_*.json
jq . /tmp/cache-debug/3_transformed_*.json
```

## What Each File Shows

| File | Purpose | What to Check |
|------|---------|---------------|
| `1_raw_anthropic_*.json` | Raw API response | `cache_creation_input_tokens`, `cache_read_input_tokens` |
| `2_canonical_*.json` | After provider translation | `message.metadata.cache_metrics`, `usage.input` calculation |
| `3_transformed_*.json` | Final application format | Metadata preserved? Missing fields? |

## Debugging Cache Not Working

If you're seeing `cache_creation_tokens: 19309` on BOTH requests:

1. **Check raw response** - Is Anthropic actually returning cache metrics?
   ```bash
   jq '.usage' /tmp/cache-debug/1_raw_*.json
   ```

2. **Verify canonical translation** - Are cache_metrics in metadata?
   ```bash
   jq '.message.metadata.cache_metrics' /tmp/cache-debug/2_canonical_*.json
   ```

3. **Check cache-control headers** - Look for this in stream.go around line 95:
   ```go
   headers["anthropic-beta"] = "prompt-caching-2024-07-16"
   ```

4. **Verify system prompt has cache_control** - Check request formation in translate.go:
   ```json
   {
     "type": "text",
     "text": "system prompt...",
     "cache_control": { "type": "ephemeral" }
   }
   ```

## Running the Example Test

```bash
cd /home/swarm/SwarmCode/TUI

# Run the test (creates example debug files)
go test -v ./internal/chat -run TestCacheDebugger

# View results
cat /tmp/cache-debug/ANALYSIS_SUMMARY.md
```

## Environment Variables

```bash
export CACHE_DEBUG=1          # Enable all cache debugging
export LOG_LEVEL=debug        # Verbose logging
export CACHE_DEBUG_DIR=/custom/path  # Custom debug directory
```

## Integration Checklist

- [ ] Added `cache_debug.go` to internal/chat
- [ ] Created test file `cache_debug_test.go`
- [ ] Ran test to confirm debug files are created
- [ ] Integrated logging into stream.go (optional, for production)
- [ ] Integrated logging into translate.go (optional, for production)
- [ ] Set `CACHE_DEBUG=1` environment variable
- [ ] Ran real request and checked /tmp/cache-debug/
- [ ] Compared the three format files
- [ ] Verified cache_metrics preserved through transformation
