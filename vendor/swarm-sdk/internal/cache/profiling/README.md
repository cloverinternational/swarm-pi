# Cache Profiling Package

## Overview

The cache profiling package provides comprehensive detection and analysis of prompt cache breaks in the Swarm SDK. It identifies when and where cached messages are being mutated, helping developers pinpoint the root cause of cache invalidation.

## Key Features

✅ **Hash-Based Cache Validation**: Detects changes in message content between API calls  
✅ **Automatic Break Detection**: Identifies when messages differ from previous turns  
✅ **Cache Metrics Tracking**: Aggregates cache hit/miss rates and token usage  
✅ **Structured Logging**: Writes cache events to `~/.swarmos/logs/cache_profiling.log` in JSONL format  
✅ **Zero Overhead When Disabled**: Uses no-op profiler with minimal performance impact  
✅ **Comprehensive Reporting**: Generates actionable reports with recommendations  

## Quick Start

### Enable Cache Profiling

```bash
# Enable profiling
export ENABLE_CACHE_PROFILING=1

# Run your SDK-based application
your_app
```

Cache events will be logged to `~/.swarmos/logs/cache_profiling.log`.

### View Cache Breaks in Real-Time

```bash
# Tail the cache profiling log
tail -f ~/.swarmos/logs/cache_profiling.log | jq '.'

# Filter for only HIGH severity breaks
cat ~/.swarmos/logs/cache_profiling.log | jq 'select(.severity == "HIGH")'

# Find which code locations break cache most
cat ~/.swarmos/logs/cache_profiling.log | \
  jq -s 'group_by(.location.file) | 
         map({file: .[0].location.file, breaks: length}) | 
         sort_by(.breaks) | reverse'
```

### Use in Tests

```go
package main

import (
	"context"
	"testing"
	"github.com/Swarm-Code/mono/swarm-sdk/cache/profiling"
)

func TestWithCacheProfiling(t *testing.T) {
	// Create a profiler for this conversation
	profiler := profiling.NewValidator()
	
	// Use the profiler to track messages
	messages := buildMessages()
	profiler.RecordMessageHash("conv-123", 1, messages)
	
	// Later, compare turns
	newMessages := buildNewMessages()
	err := profiler.CompareTurns("conv-123", 2, newMessages)
	if err != nil {
		t.Fatalf("CompareTurns failed: %v", err)
	}
	
	// Get metrics
	metrics := profiler.GetMetrics("conv-123")
	t.Logf("Cache breaks: %d, Cache hit rate: %.1f%%", 
		metrics.CacheBreaks, metrics.CacheHitPercent)
	
	// Generate report
	report := profiler.GenerateReport("conv-123")
	t.Logf("Recommendations: %d", len(report.Recommendations))
	for _, rec := range report.Recommendations {
		t.Logf("  [%s] %s: %s", rec.Priority, rec.Title, rec.Description)
	}
}
```

## Architecture

### Components

**1. Types** (`types.go`)
- `CacheBreakEvent`: Represents a detected cache break with full context
- `CacheMetrics`: Aggregated statistics across a conversation
- `CacheBreakReport`: Analysis report with recommendations

**2. Profiling Interface** (`profiling.go`)
- `CacheProfiler`: Main interface for profiling operations
- `BeforeTranslation()`: Records message state before provider translation
- `AfterTranslation()`: Validates translated JSON stability
- `RecordAPIResponse()`: Captures cache metrics from API responses

**3. Validator** (`validator.go`)
- `ComputeMessageHash()`: SHA256 hash of canonical message JSON
- `CompareTurns()`: Detects cache breaks between consecutive turns
- `GenerateReport()`: Creates comprehensive analysis with recommendations

**4. Logging** (`logger.go`)
- `LoggingProfiler`: Wraps another profiler and logs all events
- Writes to `~/.swarmos/logs/cache_profiling.log` in JSONL format
- Automatic file rotation and flushing

**5. Factory** (`factory.go`)
- `NewProfiler()`: Creates profiler based on `ENABLE_CACHE_PROFILING` env var
- `NoOpProfiler`: Zero-overhead implementation when disabled
- Global registry for managing per-conversation profilers

## Usage Patterns

### Pattern 1: Enable for a Single Conversation

```go
import "github.com/Swarm-Code/mono/swarm-sdk/cache/profiling"

// Get profiler for this conversation
profiler := profiling.GetProfiler("my-conversation-id")

// Use it transparently with your agent
agent.Execute(ctx, req)

// Later, generate a report
report := profiler.GenerateReport("my-conversation-id")
```

### Pattern 2: Custom Profiling in Tests

```go
func TestCacheStability(t *testing.T) {
	validator := profiling.NewValidator()
	
	for turn := 1; turn <= 10; turn++ {
		messages := buildMessages(turn)
		
		// Compare with previous turn
		err := validator.CompareTurns("test-conv", turn, messages)
		if err != nil {
			t.Fatalf("Turn %d failed: %v", turn, err)
		}
	}
	
	// Check results
	metrics := validator.GetMetrics("test-conv")
	if metrics.CacheBreaks > 0 {
		t.Fatalf("Unexpected cache breaks: %d", metrics.CacheBreaks)
	}
}
```

### Pattern 3: Minimal Overhead (Production)

```go
// By default, ENABLE_CACHE_PROFILING is not set
// This uses NoOpProfiler with virtually zero overhead

profiler := profiling.NewProfiler() // Returns NoOp

// All calls are instant no-ops
profiler.BeforeTranslation(ctx, messages) // Returns immediately
profiler.RecordAPIResponse(ctx, metrics)   // Returns immediately
```

## Log Format

Cache events are logged as JSONL (JSON Lines) to `~/.swarmos/logs/cache_profiling.log`:

### Cache Break Event

```json
{
  "ts": "2026-03-25T14:30:45.123456Z",
  "event_type": "CACHE_BREAK_DETECTED",
  "id": "break-1234567890",
  "conversation_id": "conv-abc123",
  "turn_number": 5,
  "break_type": "SYSTEM_PROMPT_CHANGE",
  "severity": "HIGH",
  "description": "System prompt modified between turns",
  "location": {
    "file": "swarm-tui/internal/chat/sdk_integration_execution.go",
    "function": "buildSystemPrompt",
    "line": 581,
    "column": 0
  },
  "before_hash": "sha256:abc123...",
  "after_hash": "sha256:def456...",
  "diff_type": "MODIFIED",
  "diff_size": 342,
  "stack_trace": [
    {
      "function": "SetSystemPrompt",
      "file": "swarm-sdk/agent/agent.go",
      "line": 145
    }
  ]
}
```

### API Response Event

```json
{
  "ts": "2026-03-25T14:30:46.000000Z",
  "event_type": "API_RESPONSE",
  "provider": "anthropic",
  "model": "claude-3-sonnet",
  "request_tokens": 1000,
  "output_tokens": 500,
  "cache_creation_tokens": 1000,
  "cache_read_tokens": 0,
  "cache_hit": false,
  "message_count": 3,
  "response_time_ms": 1234
}
```

## Analysis Commands

### Find All Cache Breaks

```bash
cat ~/.swarmos/logs/cache_profiling.log | \
  jq 'select(.event_type == "CACHE_BREAK_DETECTED")'
```

### Count Breaks by Type

```bash
cat ~/.swarmos/logs/cache_profiling.log | \
  jq -s 'map(select(.event_type == "CACHE_BREAK_DETECTED")) | 
         group_by(.break_type) | 
         map({type: .[0].break_type, count: length})'
```

### Find Hotspot Locations (Most Breaks)

```bash
cat ~/.swarmos/logs/cache_profiling.log | \
  jq -s 'map(select(.event_type == "CACHE_BREAK_DETECTED") | .location.file + ":" + (.location.line | tostring)) | 
         group_by(.) | 
         map({location: .[0], breaks: length}) | 
         sort_by(.breaks) | 
         reverse'
```

### Get Cache Hit Rate

```bash
cat ~/.swarmos/logs/cache_profiling.log | \
  jq -s 'map(select(.event_type == "API_RESPONSE")) | 
         {
           total: length,
           hits: map(select(.cache_hit == true)) | length,
           hit_rate: (map(select(.cache_hit == true)) | length / length * 100)
         }'
```

### Timeline of Events for a Conversation

```bash
cat ~/.swarmos/logs/cache_profiling.log | \
  jq "select(.conversation_id == \"conv-abc123\")" | \
  jq -s 'sort_by(.ts) | .[]'
```

## Data Structures

### CacheBreakEvent

Complete representation of a single cache break:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique event ID |
| `ts` | timestamp | When break was detected |
| `conversation_id` | string | Which conversation |
| `turn_number` | int | Which turn |
| `event_type` | string | Always "CACHE_BREAK_DETECTED" |
| `break_type` | string | Type of break (SYSTEM_PROMPT_CHANGE, MESSAGE_FORMAT_CHANGE, etc.) |
| `severity` | string | HIGH, MEDIUM, or LOW |
| `description` | string | Human-readable description |
| `location` | object | Code location where detected |
| `before_hash` | string | SHA256 of previous state |
| `after_hash` | string | SHA256 of current state |
| `diff_type` | string | ADDED, REMOVED, MODIFIED, REORDERED |
| `diff_size` | int | Byte count difference |

### CacheMetrics

Aggregated statistics:

| Field | Type | Description |
|-------|------|-------------|
| `total_api_calls` | int | Number of API calls made |
| `cache_hits` | int | Number that reused cache |
| `cache_misses` | int | Number that created new cache |
| `cache_hit_percent` | float | Hit rate percentage (0-100) |
| `cache_breaks` | int | Detected cache invalidations |
| `breaks_by_type` | map | Count of each break type |
| `system_prompt_changes` | int | Times system prompt changed |
| `cache_created_tokens` | int | Tokens spent on cache creation |
| `cache_read_tokens` | int | Tokens saved via cache hits |
| `cache_savings_percent` | float | Savings percentage (0-100) |

## Integration Points

The cache profiling system can be integrated at:

1. **Provider Translation** (`swarm-sdk/provider/anthropic/translate.go`)
   - Call `BeforeTranslation()` before message processing
   - Call `AfterTranslation()` after translation

2. **Agent Execution** (`swarm-sdk/agent/agent_execute.go`)
   - Record API responses with cache metrics
   - Track conversation state

3. **Conversation Storage** (`swarm-sdk/conversation/storage/`)
   - Track message loads/saves
   - Detect mutations during serialization

4. **Compaction** (`swarm-sdk/compaction/`)
   - Monitor message changes during compaction
   - Log compression artifacts

## Performance

### Overhead Measurement

- **When Disabled** (default): < 1µs per call (no-op)
- **When Enabled** (with logging): ~10-50µs per call
- **Logging I/O**: Buffered writes, flushed on each event
- **Memory**: ~1KB per conversation tracked

### Best Practices

1. **Production**: Leave `ENABLE_CACHE_PROFILING` unset (uses no-op)
2. **Testing**: Enable for integration tests where cache is critical
3. **Debugging**: Enable temporarily when investigating cache issues
4. **Analysis**: Use `jq` to query logs without rerunning

## Future Enhancements

- Phase 3: Mutation tracking at all modification points
- Phase 4: Comprehensive reporting and dashboard integration
- Phase 5: Automated recommendations and fixes
- Phase 6: OpenTelemetry metric exports

## Testing

Run the full test suite:

```bash
go test -v ./swarm-sdk/cache/profiling
```

All 11 unit tests verify:
- Hash stability and correctness
- Cache break detection
- Metrics aggregation
- Report generation
- Zero-overhead no-op implementation
- Factory and registry functionality
