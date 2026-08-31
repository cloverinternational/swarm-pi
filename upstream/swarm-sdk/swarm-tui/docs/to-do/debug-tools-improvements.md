# Debug Tools Improvement Plan

**Status:** Proposal  
**Priority:** Medium  
**Impact:** High (Developer Experience)  
**Created:** 2026-01-02  
**Context:** After debugging message ordering issue (commit 16e3462)

---

## Executive Summary

While debugging the message ordering issue where tool calls and text content were rendering out-of-order, we discovered significant gaps in our debug tooling that made root cause analysis unnecessarily difficult. This document outlines those gaps and proposes concrete improvements.

### The Problem We Had

**Issue:** Messages from Claude were rendering out of order (all tool calls first, then text content), even though they arrived from the LLM in the correct interleaved order.

**Debug Pain Points:**
- Spent significant time correlating timestamps across 8000+ log lines
- Hit "TOO LARGE" errors when trying to inspect messages/logs
- No visibility into channel buffer state or goroutine timing
- Couldn't trace a single update through its full lifecycle
- Had to manually piece together: arrival time → queue time → process time → render

**Root Cause:** Sequence numbers were assigned during queue processing instead of during update arrival, causing goroutine timing races to affect display order.

**Time to Resolution:** ~2 hours of manual log analysis could have been ~10 minutes with better tooling.

---

## Current Debug Tools Assessment

### What Works ✅

1. **`debug_inspect` tool** - Runtime introspection concept is sound
2. **Persistent debug log** (`swarmos_debug.log`) - Timestamped logging helped trace events
3. **Structured logging categories** - Filtering by category (APP, TOOL, SDK) is useful
4. **grep/Read/Bash tools** - Code exploration basics are solid

### What's Broken or Missing 🔴

#### 1. Output Size Limits
**Problem:** `debug_inspect` fails with "TOO LARGE" error when results exceed limits
```
Result size: 121846 chars (~30461 tokens), 1412 lines
Limits: 100000 chars (~25000 tokens), 1000 lines
```

**Impact:** Can't view full message history or log context during debugging

#### 2. No Correlation/Tracing
**Problem:** No unified trace ID linking related events across goroutines

**Example:** Can't easily trace:
- Update arrives in `listenForAgentUpdates` (goroutine A)
- Gets queued to `updateQueue` channel
- Gets processed in `Update()` (goroutine B)  
- Renders in `View()` (main goroutine)

#### 3. Missing Queue Visibility
**Problem:** Can't inspect channel state (buffer depth, pending messages, lag)

**Example:** During the bug, we couldn't see:
```
updateQueue: [ToolCall #1, ToolResult #1, ToolCall #2, ToolResult #2, ...]
             ↑ 4 messages waiting, processing lag = 63ms
```

#### 4. Timing Gaps
**Problem:** No easy way to see time deltas between related events

**Example:** Want to see:
```
Event                          | Timestamp      | Delta from Prev
-------------------------------|----------------|----------------
ToolCallUpdate arrives         | 16:47:39.498   | -
Queued to updateQueue          | 16:47:39.498   | +0.017ms
Processed in Update()          | 16:47:39.562   | +63.9ms  ← THE PROBLEM
```

#### 5. Log Verbosity
**Problem:** Debug logs filled with render/viewport noise, hard to filter

```
[SIDEPANEL] Rendering tokens: current=6 max=200000 (0.0%)
[SimpleInput.View] width=77, maxWidth=69, height=4
[viewMainChat] inputTextLines count: 1, inputText len: 39
... (repeated 100x)
```

Drowns out critical agent execution logs.

---

## Proposed Improvements

### Priority 1: Quick Wins (Implement First) 🚀

#### 1.1 Add Sequence Numbers to All Debug Logs

**What:** Include `[seq=N]` in every agent update log
**Why:** Makes it trivial to track message flow through the system
**Effort:** Low (already partially done in commit 16e3462)

```go
// Before
logDebug("listenForAgentUpdates: ToolCallUpdate - bash")
logDebug("[Queue] Processing tool call: bash")

// After  
logDebug("listenForAgentUpdates: ToolCallUpdate - bash [seq=45]")
logDebug("[Queue] Processing tool call: bash [seq=45]")
```

**Files to modify:**
- `internal/chat/app.go` - Already done for queue processing
- Need to add to render logs, viewport updates, etc.

---

#### 1.2 Expose Queue State in `debug_inspect`

**What:** Add queue introspection to state target
**Why:** See backlog and processing lag in real-time
**Effort:** Low

```go
// In debug_inspect state target
case "state":
    return map[string]interface{}{
        "updateQueue": map[string]interface{}{
            "capacity":      cap(a.updateQueue),
            "current_depth": len(a.updateQueue),
            "max_observed":  a.maxQueueDepth, // track high water mark
        },
        "globalUpdateSequence": a.globalUpdateSequence,
        "messageSequence":      a.messageSequence,
        // ... existing state
    }
```

**Usage:**
```bash
debug_inspect --target=state | jq '.updateQueue'
```

**Files to modify:**
- `sdk/tools/builtin/debug_inspect.go` - Add queue fields
- `internal/chat/app.go` - Track maxQueueDepth

---

#### 1.3 Streaming/Pagination for Large Results

**What:** Instead of failing on large results, paginate or stream
**Why:** Essential for viewing full message/log history
**Effort:** Medium

**Option A: Pagination**
```bash
debug_inspect --target=messages --page=1 --per-page=20
debug_inspect --target=logs --offset=100 --limit=50
```

**Option B: Streaming**
```bash
debug_inspect --target=logs --stream --pattern="Queue"
# Outputs results as they're found, no size limit
```

**Files to modify:**
- `sdk/tools/builtin/debug_inspect.go` - Add pagination params
- Response formatter to support chunked output

---

#### 1.4 Log Filtering Improvements

**What:** Add time-based and category-based filters
**Why:** Cut through noise to relevant logs
**Effort:** Low

```bash
# Filter by time
debug_logs --since="16:47:30" --until="16:47:40" --pattern="seq="

# Exclude noisy categories
debug_logs --exclude-category="SIDEPANEL,SimpleInput,viewMainChat"

# Tail and follow
debug_logs --tail=50 --follow --pattern="Queue"
```

**Files to modify:**
- `sdk/tools/ii/debug_logs.go` - Add filtering params
- Log file reader to support time range parsing

---

### Priority 2: Enhanced Tracing (Medium Term) 🔍

#### 2.1 Unified Trace IDs

**What:** Add correlation IDs to link related events across goroutines
**Why:** Trace a single update from arrival → queue → process → render
**Effort:** Medium

```go
// Generate trace ID when update arrives
type agentToolCallMsg struct {
    toolName   string
    parameters map[string]interface{}
    callID     string
    sequence   int
    traceID    string // NEW: e.g. "conv-a9d8e76-1735851239-45"
}

// In listenForAgentUpdates
traceID := fmt.Sprintf("conv-%s-%d-%d", 
    a.currentConvID[:8], time.Now().Unix(), seq)

logDebug("[TRACE=%s] [seq=%d] ToolCallUpdate - %s", traceID, seq, u.Name)

// In Update()
logDebug("[TRACE=%s] [seq=%d] Processing tool call", qm.traceID, qm.sequence)

// In render
logDebug("[TRACE=%s] [seq=%d] Rendering block", block.TraceID, block.Sequence)
```

**Then debug with:**
```bash
grep "TRACE=conv-a9d8e76-1735851239-45" debug.log

# Output shows full lifecycle:
[TRACE=...] [seq=45] ToolCallUpdate - bash
[TRACE=...] [seq=45] Queued to updateQueue
[TRACE=...] [seq=45] Processing tool call
[TRACE=...] [seq=45] Rendering block
```

**Files to modify:**
- All agent message types - Add `traceID` field
- `internal/chat/app.go` - Generate and propagate trace IDs
- `MessageBlock` struct - Add `TraceID` field for render tracking

---

#### 2.2 Timeline Visualization Tool

**What:** Create a tool that reconstructs event timeline with timing deltas
**Why:** Visually see where delays/reordering occur
**Effort:** Medium-High

```bash
debug_timeline --from="16:47:39" --to="16:47:40" --pattern="bash"

# Output:
Time         Δ(ms)  Event                           Details
------------ ------ ------------------------------- -------------------------
16:47:39.498 +0     [ARRIVE] ToolCallUpdate         bash (seq=45)
16:47:39.498 +0.017 [QUEUE ] → updateQueue          depth: 0→1
16:47:39.506 +8     [ARRIVE] ToolResultUpdate       bash (seq=46)  
16:47:39.506 +0.028 [QUEUE ] → updateQueue          depth: 1→2
16:47:39.562 +56    [PROCESS] ToolCallMsg           seq=45 ← DELAY!
16:47:39.585 +23    [PROCESS] ToolResultMsg         seq=46
16:47:39.586 +1     [RENDER] Block type=tool_call   seq=45
```

**Implementation:**
```go
// New tool: debug_timeline
type TimelineEvent struct {
    Timestamp    time.Time
    Category     string // ARRIVE, QUEUE, PROCESS, RENDER
    Sequence     int
    TraceID      string
    Description  string
    DeltaFromPrev time.Duration
}

func ParseTimeline(logFile string, from, to time.Time) []TimelineEvent {
    // Parse debug.log, extract events, calculate deltas
}
```

**Files to create:**
- `sdk/tools/builtin/debug_timeline.go`
- Timeline parser and formatter

---

#### 2.3 Structured JSON Logging (Optional)

**What:** Add JSON log format option for machine parsing
**Why:** Enables powerful filtering/analysis with jq/jless
**Effort:** Medium

```go
// Add --log-format=json flag
{
  "timestamp": "2026-01-02T16:47:39.498108-04:00",
  "category": "AGENT",
  "subcategory": "UPDATE.IN",
  "sequence": 45,
  "trace_id": "conv-a9d8e76-1735851239-45",
  "event": "tool_call_update",
  "data": {
    "tool": "bash",
    "call_id": "toolu_01RE5gBTMfMX2dF774qFsjB3"
  }
}
```

**Usage:**
```bash
cat debug.log | jq 'select(.sequence != null) | {seq: .sequence, event, ts: .timestamp}'
cat debug.log | jq 'select(.trace_id == "conv-a9d8e76-1735851239-45")'
```

**Files to modify:**
- `internal/chat/debug.go` - Add JSON formatter
- All `DebugLog()` calls - Add structured fields

---

### Priority 3: Advanced Features (Long Term) 🎯

#### 3.1 Queue Introspection Tool

**What:** Dedicated tool to inspect channel state
**Why:** Deep visibility into concurrency issues
**Effort:** Medium

```bash
debug_queue --name=updateQueue --snapshot

# Output:
{
  "capacity": 50,
  "current_size": 4,
  "pending_messages": [
    {
      "type": "agentToolCallMsg",
      "sequence": 45,
      "queued_at": "16:47:39.498125",
      "age_ms": 64
    },
    {
      "type": "agentToolResultMsg", 
      "sequence": 46,
      "queued_at": "16:47:39.505666",
      "age_ms": 56
    }
  ],
  "processing_lag_ms": 64,
  "throughput_msgs_per_sec": 156
}
```

**Implementation:** Requires concurrent-safe introspection of channel state

**Files to create:**
- `sdk/tools/builtin/debug_queue.go`

---

#### 3.2 Snapshot Diff Tool

**What:** Save/compare app state snapshots
**Why:** See exactly what changed between two points
**Effort:** High

```bash
debug_snapshot save --name="before_tool_call"
# ... user sends message, tools execute ...
debug_snapshot save --name="after_tool_call"
debug_snapshot diff before_tool_call after_tool_call

# Output:
OrderedBlocks changed:
  Messages[2].OrderedBlocks:
    + Block[3]: {Type: "tool_call", Sequence: 45, Tool: "bash"}
    + Block[4]: {Type: "tool_result", Sequence: 46, CallID: "..."}
  
  globalUpdateSequence: 42 → 48 (+6)
  updateQueue depth: 0 → 2
```

**Files to create:**
- `sdk/tools/builtin/debug_snapshot.go`
- Snapshot storage/diff engine

---

#### 3.3 Interactive Debug Shell

**What:** REPL for live debugging
**Why:** Exploratory debugging without recompiling
**Effort:** Very High

```bash
# Press Ctrl+Shift+D to enter debug mode
SwarmOS Debug> show queue
updateQueue: depth=2/50, lag=23ms

SwarmOS Debug> show messages where role=assistant limit 1
Message[2]: role=assistant, blocks=4, content="Hello..."

SwarmOS Debug> show blocks where sequence > 40
Block[3]: seq=45, type=tool_call, tool=bash
Block[4]: seq=46, type=tool_result, call_id=toolu_01...

SwarmOS Debug> trace sequence 45
[ARRIVE] 16:47:39.498 ToolCallUpdate
[QUEUE]  16:47:39.498 → updateQueue
[PROCESS] 16:47:39.562 Update() processing
[RENDER] 16:47:39.586 View() rendered

SwarmOS Debug> watch globalUpdateSequence
globalUpdateSequence: 45 → 46 → 47 → ...
```

**Implementation:** Requires:
- REPL parser/evaluator
- Safe access to app state from separate goroutine
- Command DSL and query engine

**Files to create:**
- `internal/chat/debug_shell.go`
- Query language parser

---

## Implementation Roadmap

### Phase 1: Quick Wins (1-2 weeks)
- [ ] Add sequence numbers to all debug logs
- [ ] Expose queue state in debug_inspect
- [ ] Add pagination to debug_inspect
- [ ] Improve debug_logs filtering (--since, --exclude-category, --tail)

**Success Criteria:** Can debug the message ordering issue in <30 minutes

---

### Phase 2: Enhanced Tracing (2-4 weeks)
- [ ] Implement unified trace IDs
- [ ] Build timeline visualization tool
- [ ] Optional: Add JSON logging format

**Success Criteria:** Can trace a single message through entire lifecycle with one command

---

### Phase 3: Advanced Features (1-2 months)
- [ ] Queue introspection tool
- [ ] Snapshot diff tool
- [ ] Interactive debug shell (stretch goal)

**Success Criteria:** Can debug complex concurrency issues without adding print statements

---

## Testing Strategy

### Unit Tests
- Test pagination logic with varying result sizes
- Test trace ID generation and propagation
- Test timeline parser with sample logs

### Integration Tests
```go
func TestDebugTimeline(t *testing.T) {
    // 1. Send message that triggers tool calls
    // 2. Capture debug.log
    // 3. Parse timeline
    // 4. Assert events appear in correct order
    // 5. Assert timing deltas are reasonable
}
```

### Manual Testing
- Debug the message ordering issue using new tools
- Verify can trace update from arrival to render
- Confirm queue state updates in real-time

---

## Performance Considerations

### Trace ID Overhead
- Adding string fields to message types: ~24 bytes per message
- Trace ID generation: ~100ns per update
- **Impact:** Negligible (< 0.1% overhead)

### Queue Introspection
- Reading channel length: O(1), ~10ns
- **Impact:** Negligible

### Timeline Parsing
- Parsing 10k log lines: ~100ms
- **Optimization:** Index by timestamp on first read

### JSON Logging
- JSON marshaling: ~2-3x slower than plain text
- **Mitigation:** Make it opt-in (--log-format=json)

---

## Security Considerations

### Log Sanitization
**Risk:** Debug logs might contain sensitive data (API keys, user content)

**Mitigation:**
- Sanitize tool parameters in logs (mask API keys)
- Add --redact flag to debug tools
- Never log full message content in production builds

### Debug Shell Access
**Risk:** Interactive shell could expose internal state

**Mitigation:**
- Only available in debug builds (--tags=debug)
- Require authentication for remote access
- Limit query scope to prevent data exfiltration

---

## Success Metrics

### Before (Current State)
- Time to debug message ordering issue: **~2 hours**
- Log analysis: **Manual correlation of 8000+ lines**
- Queue visibility: **None** (had to infer from logs)
- Trace single update: **Impossible** (no correlation IDs)

### After (Target State)
- Time to debug similar issue: **<30 minutes**
- Log analysis: **Automated timeline visualization**
- Queue visibility: **Real-time depth/lag metrics**
- Trace single update: **Single command** with full lifecycle

### KPIs
- Reduce average debug time by **75%**
- Reduce "TOO LARGE" errors to **0%** (via pagination)
- Enable tracing for **100%** of agent updates
- Support filtering **99%** of log noise

---

## Open Questions

1. **Persistence:** Should timeline/snapshots persist across sessions?
   - **Proposal:** Store in `~/.config/swarmos/debug/` with TTL cleanup

2. **Remote Debugging:** Support debugging production instances?
   - **Proposal:** Phase 4 feature, requires authentication

3. **Performance Mode:** Disable debug tools in production?
   - **Proposal:** Build flag `--tags=debug` to exclude heavy tools

4. **Log Rotation:** How to handle debug.log growing large?
   - **Proposal:** Rotate at 100MB, keep last 3 files

---

## Related Issues

- Message ordering bug (commit 16e3462) - **FIXED** ✅
- Debug inspect size limits - **IN PROGRESS**
- Queue visibility - **PLANNED**

---

## References

- Original debug session: 2026-01-02 (this file)
- Commit fixing message ordering: `16e3462`
- Debug tools code: `sdk/tools/builtin/debug_inspect.go`
- Log system: `internal/chat/debug.go`

---

## Appendix: Example Debug Session (After Improvements)

**Scenario:** User reports messages appearing out of order

```bash
# 1. Check current queue state
$ debug_inspect --target=state | jq '.updateQueue'
{
  "capacity": 50,
  "current_depth": 4,
  "max_observed": 12
}

# 2. View recent timeline
$ debug_timeline --since="now-30s" --pattern="seq="
Time         Δ(ms)  Event                    Details
16:47:39.498 +0     [ARRIVE] ToolCall       bash seq=45
16:47:39.498 +0.017 [QUEUE]                 depth:0→1
16:47:39.562 +64    [PROCESS] ToolCall      seq=45 ← DELAY!
# ^ Spot the 64ms gap immediately

# 3. Trace specific sequence
$ grep "seq=45" debug.log
[TRACE=conv-xyz-45] [seq=45] ARRIVE: ToolCallUpdate  
[TRACE=conv-xyz-45] [seq=45] QUEUE: Sent to channel
[TRACE=conv-xyz-45] [seq=45] PROCESS: Processing in Update()
[TRACE=conv-xyz-45] [seq=45] RENDER: Block rendered

# 4. Check for sequence gaps
$ cat debug.log | jq 'select(.sequence != null) | .sequence' | sort -n
42
43
45  ← Missing 44!
46

# Total time: 5 minutes
# Root cause: Identified sequence assignment happening after queueing
```

---

**End of Document**
