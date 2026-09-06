# Swarm SDK Findings System - Technical Documentation

## Overview

The Findings System implements the AAR-style local knowledge persistence pattern for the Swarm SDK. It captures tool execution results, analyzes them for insights, and stores them in a searchable local database.

## Architecture

### Hook Chain Integration

```
Priority 100: SteeringHook           (pre-execution evaluation)
Priority  95: TaskEnforcementHook    (blocks without tasks)
Priority  90: ToolResultAnalysisHook (NEW - post-execution analysis) ⭐
Priority  85: LocalFindingsHook      (NEW - persistence) ⭐
Priority  80: PostActingHook         (workflow guidance)
Priority   0: DreamHook              (conversation consolidation)
```

### Two-Hook Design

**Why two separate hooks?**

1. **ToolResultAnalysisHook (P90)** - Decides WHAT to capture
   - Analyzes tool output
   - Sets metadata flags
   - Triggers task creation for errors

2. **LocalFindingsHook (P85)** - Decides HOW to capture
   - Checks metadata flags
   - Writes to cache
   - Updates semantic index

This separation enables:
- Independent enable/disable
- Easier testing
- Custom analysis without custom storage

## How Analysis Works

### The ResultAnalyzer Interface

```go
type ResultAnalyzer interface {
    Analyze(ctx context.Context, toolName string, input, output map[string]any) (AnalysisResult, error)
    Name() string
}
```

### Analysis Flow

```
Tool Event
    ↓
ToolResultAnalysisHook.OnEvent()
    ↓
For each registered analyzer:
    ├─ DefaultAnalyzer.Analyze()
    ├─ ErrorAnalyzer.Analyze()
    └─ InsightAnalyzer.Analyze()
    ↓
Aggregate results:
    ├─ ShouldCapture = OR of all analyzers
    ├─ ShouldCreateTask = OR of all analyzers
    ├─ Tags = UNION of all tags
    ├─ Priority = MAX of all priorities
    └─ Insights = CONCAT of all insights
    ↓
Store in event.Metadata
    ↓
Continue to LocalFindingsHook
    ↓
(if capture_finding=true) Write to cache
```

### Built-in Analyzers

#### 1. DefaultAnalyzer

**Purpose:** General-purpose capture decisions

**Logic:**
```go
// Capture "interesting" tools by default
interestingTools := ["evaluate", "run_test", "build", "deploy", "benchmark"]

// Check success flag
if output.success == true {
    tags = append(tags, "success")
} else {
    tags = append(tags, "failure")
    priority = 70
}
```

**Example:**
```json
// Input: evaluate tool with success=true, pgr=0.87
{
  "should_capture": true,
  "tags": ["evaluate", "automated", "success"],
  "priority": 50
}
```

#### 2. ErrorAnalyzer

**Purpose:** Detect and escalate errors

**Logic:**
```go
// Check for error indicators:
// - output.error (string)
// - output.error_message (string)
// - output.stderr (string)
// - output.success == false

if has_error {
    should_capture = true
    should_create_task = true
    priority = 90
    tags = ["error", "needs-attention"]
    task_description = "Investigate {tool} failure: {error}"
}
```

**Example:**
```json
// Input: build tool with success=false, error_message="undefined variable"
{
  "should_capture": true,
  "should_create_task": true,
  "task_description": "Investigate build failure: undefined variable",
  "tags": ["build", "automated", "error", "needs-attention"],
  "priority": 90
}
```

#### 3. InsightAnalyzer

**Purpose:** Extract metrics and insights from successful results

**Logic:**
```go
// Look for interesting metrics:
metrics := ["duration_ms", "tokens_used", "accuracy", "score", "pgr", "coverage"]

for metric in metrics:
    if output[metric] exists:
        should_capture = true
        insights = append(insights, "{metric}: {value}")

// Special handling for PGR (Performance Gap Recovery)
if output.pgr exists:
    tags = append(tags, "evaluation", "pgr")
    priority = 75
```

**Example:**
```json
// Input: evaluate tool with pgr=0.87, accuracy=0.95, duration_ms=1234
{
  "should_capture": true,
  "tags": ["evaluate", "automated", "success", "evaluation", "pgr"],
  "insights": ["pgr: 0.87", "accuracy: 0.95", "duration_ms: 1234"],
  "priority": 75
}
```

### Analysis Aggregation

When multiple analyzers run, their results are merged:

```go
result.ShouldCapture = analyzer1.ShouldCapture OR analyzer2.ShouldCapture OR ...
result.ShouldCreateTask = analyzer1.ShouldCreateTask OR analyzer2.ShouldCreateTask OR ...
result.Tags = union(analyzer1.Tags, analyzer2.Tags, ...)
result.Priority = max(analyzer1.Priority, analyzer2.Priority, ...)
result.Insights = concat(analyzer1.Insights, analyzer2.Insights, ...)
```

**Example Aggregation:**

```
DefaultAnalyzer:     capture=true,  tags=["evaluate", "automated"],     priority=50
ErrorAnalyzer:       capture=true,  tags=["error", "needs-attention"], priority=90, create_task=true
InsightAnalyzer:     capture=true,  tags=["evaluation", "pgr"],      priority=75, insights=["pgr: 0.87"]

─────────────────────────────────────────────────────────────────────
Aggregated Result:   capture=true,  tags=["evaluate", "automated", "error", "needs-attention", "evaluation", "pgr"],
                     priority=90, create_task=true, insights=["pgr: 0.87"]
```

## How Findings Capture Works

### Event Flow

```
1. Tool Executes
   ↓
2. Event Created: EventToolAfterExecute
   {
     "tool_name": "evaluate",
     "tool_input": {...},
     "tool_output": {...}
   }
   ↓
3. ToolResultAnalysisHook (P90)
   - Runs analyzers
   - Sets metadata:
     {
       "capture_finding": true,
       "finding_tags": ["w2s", "evaluation"],
       "finding_priority": 75,
       "finding_insights": ["pgr: 0.87"]
     }
   ↓
4. LocalFindingsHook (P85)
   - Checks capture_finding flag
   - Extracts from event:
     * tool_name, tool_input, tool_output
     * agent_id, conversation_id
     * tags, priority from metadata
   - Creates Finding struct
   - Writes to cache
   - Async updates index
   ↓
5. Finding Persisted
   File: ~/.swarmos/findings/cache/2026/04/14/finding_<uuid>.json
```

### Storage Format

Each finding is stored as a JSON file:

```json
{
  "finding_id": "550e8400-e29b-41d4-a716-446655440000",
  "tool_name": "evaluate",
  "tool_input": {
    "dataset": "chat",
    "weak_model": "qwen3-4b",
    "strong_model": "qwen3-32b"
  },
  "tool_output": {
    "success": true,
    "accuracy": 0.95,
    "pgr": 0.87
  },
  "timestamp": "2026-04-14T16:42:00Z",
  "agent_id": "agent-123",
  "conversation_id": "conv-456",
  "context_summary": "Executed evaluate: Evaluating W2S approach on chat dataset",
  "tags": ["w2s", "evaluation", "automated"],
  "metadata": {
    "duration_ms": 1234,
    "tokens_used": 500,
    "success": true,
    "priority": 75,
    "source": "local"
  }
}
```

## Usage Examples

### Example 1: Successful Evaluation

```go
// Tool execution
event := hooks.Event{
    Type: hooks.EventToolAfterExecute,
    Data: map[string]any{
        "tool_name": "evaluate",
        "tool_input": map[string]any{"dataset": "chat"},
        "tool_output": map[string]any{
            "success":  true,
            "accuracy": 0.95,
            "pgr":      0.87,
        },
    },
    Metadata: make(map[string]any),
}

// Run analysis hook
analysisHook.OnEvent(ctx, event)

// Metadata after analysis:
// event.Metadata = {
//     "capture_finding":  true,
//     "finding_tags":     ["evaluate", "automated", "success", "evaluation", "pgr"],
//     "finding_priority": 75,
//     "finding_insights": ["pgr: 0.87", "accuracy: 0.95"],
// }

// Run findings hook
findingsHook.OnEvent(ctx, event)

// Result: Finding written to cache with high priority
```

### Example 2: Failed Build

```go
// Tool execution
event := hooks.Event{
    Type: hooks.EventToolAfterExecute,
    Data: map[string]any{
        "tool_name": "build",
        "tool_input": map[string]any{"command": "go build ./..."},
        "tool_output": map[string]any{
            "success":       false,
            "error_message": "undefined: someVariable",
        },
    },
    Metadata: make(map[string]any),
}

// Run analysis hook
analysisHook.OnEvent(ctx, event)

// Metadata after analysis:
// event.Metadata = {
//     "capture_finding":  true,
//     "create_task":      true,
//     "task_description": "Investigate build failure: undefined: someVariable",
//     "finding_tags":     ["build", "automated", "error", "needs-attention"],
//     "finding_priority": 90,
//     "finding_insights": ["Tool build failed with error"],
// }

// Run findings hook
findingsHook.OnEvent(ctx, event)

// Result: High-priority finding with task creation flag
```

### Example 3: Boring Tool (Not Captured)

```go
// Tool execution
event := hooks.Event{
    Type: hooks.EventToolAfterExecute,
    Data: map[string]any{
        "tool_name": "read_file",
        "tool_input": map[string]any{"file_path": "/tmp/test.txt"},
        "tool_output": map[string]any{"content": "hello world"},
    },
    Metadata: make(map[string]any),
}

// Run analysis hook
analysisHook.OnEvent(ctx, event)

// Metadata after analysis:
// event.Metadata = {}  // No capture_finding flag!

// Filter check in LocalFindingsHook
shouldCapture := hook.Filter(event)  // Returns FALSE

// Result: Finding NOT written to cache
```

## Querying Findings

### By Tool Name

```go
results, _ := cache.Query(ctx, findings.FindingQuery{
    ToolName: "evaluate",
})
```

### By Tags

```go
results, _ := cache.Query(ctx, findings.FindingQuery{
    Tags: []string{"w2s", "evaluation"},
})
```

### By Agent

```go
results, _ := cache.Query(ctx, findings.FindingQuery{
    AgentID: "agent-123",
})
```

### By Time Range

```go
results, _ := cache.Query(ctx, findings.FindingQuery{
    TimeRange: &findings.TimeRange{
        Start: time.Now().Add(-24 * time.Hour),
        End:   time.Now(),
    },
})
```

### Text Search

```go
results, _ := cache.Query(ctx, findings.FindingQuery{
    TextQuery: "W2S approach",
})
```

## Extending with Custom Analyzers

### Example: Performance Regression Analyzer

```go
type PerformanceAnalyzer struct {
    baselinePGR float64
}

func (a *PerformanceAnalyzer) Name() string {
    return "performance"
}

func (a *PerformanceAnalyzer) Analyze(ctx context.Context, toolName string, input, output map[string]any) (findings.AnalysisResult, error) {
    result := findings.AnalysisResult{
        ShouldCapture: false,
        Priority:      50,
    }

    // Only analyze evaluation tools
    if toolName != "evaluate" {
        return result, nil
    }

    // Check for PGR regression
    pgr, ok := output["pgr"].(float64)
    if !ok {
        return result, nil
    }

    if pgr < a.baselinePGR * 0.9 {  // 10% regression
        result.ShouldCapture = true
        result.ShouldCreateTask = true
        result.Priority = 95
        result.Tags = []string{"performance", "regression", "needs-attention"}
        result.TaskDescription = fmt.Sprintf(
            "PGR regression detected: %.2f (baseline: %.2f)",
            pgr, a.baselinePGR,
        )
        result.Insights = []string{
            fmt.Sprintf("PGR dropped to %.2f (%.1f%% below baseline)",
                pgr, (1-pgr/a.baselinePGR)*100),
        }
    }

    return result, nil
}
```

### Registering Custom Analyzer

```go
analysisHook := builtin.NewToolResultAnalysisHook()
analysisHook.RegisterAnalyzer(&PerformanceAnalyzer{baselinePGR: 0.85})
```

## Configuration

### Config File Location

`~/.swarmos/findings/config.yaml`

### Example Configuration

```yaml
local_cache_dir: ~/.swarmos/findings
max_cache_size_mb: 1000
retention_days: 90
enable_semantic_index: true
sync_endpoint: https://findings.swarm.example.com
sync_enabled: true
auto_capture_tools:
  - evaluate
  - benchmark
  - experiment
```

## Performance Considerations

### Write Performance

- **Synchronous writes:** ~2ms per finding (local SSD)
- **Background writes:** ~0.1ms per finding (buffered)
- **Async indexing:** Non-blocking, ~5ms latency acceptable

### Query Performance

- **By ID:** O(1) - direct file read
- **By tool/agent:** O(n) - directory scan
- **By tags:** O(n) - filter scan
- **Full-text search:** O(n) - naive implementation
- **Semantic search:** O(log n) - FAISS approximate nearest neighbor

### Storage Requirements

- **Average finding size:** ~2KB
- **Daily usage (1000 findings):** ~2MB
- **90-day retention:** ~180MB

## Testing

### Unit Tests

```bash
cd swarm-sdk/findings
go test -v ./...

cd swarm-sdk/hooks/builtin
go test -v -run "TestToolResult|TestLocalFinding"
```

### Integration Demo

```bash
cd swarm-sdk/tests/integration
go run findings_demo.go
```

This demonstrates the complete flow with simulated tool executions.

## Troubleshooting

### Findings not being captured

1. Check if `capture_finding` flag is set in metadata
2. Verify LocalFindingsHook priority (should be 85)
3. Check cache directory permissions
4. Review analyzer logic for the specific tool

### High disk usage

1. Adjust `retention_days` in config
2. Lower `max_cache_size_mb`
3. Run manual cleanup: `find ~/.swarmos/findings -mtime +30 -delete`

### Query performance issues

1. Enable semantic index for similarity queries
2. Use more specific filters (tool_name, agent_id)
3. Add time range to limit scan scope

## Future Enhancements (P1/P2)

1. **Semantic Index:** FAISS-based vector similarity search
2. **Sync Engine:** Bidirectional sync with remote findings server
3. **TUI Panel:** Findings explorer with search and visualization
4. **Autonomous Mode:** Self-improving agent that queries prior findings

---

## TUI Configuration

### Settings Panel

The findings system can be configured via the TUI at **Settings > Findings**:

**Available Options:**

| Setting            | Description                                          | Default            |
|-------------------|-----------------------------------------------------|--------------------|
| Server URL        | HTTP endpoint of the findings server                  | http://localhost:8081 |
| Capture           | Enable/disable findings capture                    | Enabled           |
| Auto-capture      | Which tools to auto-capture (all or specific)      | All tools         |

### Keyboard Shortcuts (Findings Panel)

When viewing the findings panel in the TUI:

**Navigation:**
- `↑/j` - Move up
- `↓/k` - Move down
- `enter/space` - View finding detail
- `esc/q` - Back to list / close detail

**Search:**
- `/` - Start search
- `enter` - Execute search
- `esc` - Clear search and return to list
- `backspace` - Delete character

**Actions:**
- `r` - Refresh findings list
- `?` - Toggle keyboard shortcuts help

### Configuration File

Settings are persisted to `~/.swarm/config.json`:

```json
{
  "findings": {
    "serverUrl": "http://localhost:8081",
    "enabled": true,
    "autoCaptureTools": []
  }
}
```

- `serverUrl`: The findings server URL
- `enabled`: Whether findings capture is active
- `autoCaptureTools`: List of tools to auto-capture (empty = all tools)

### Cache Directory

Findings are stored in:

- **With workspace:** `~/.swarm/projects/<hash>/findings/`
- **Without workspace:** `~/.swarmos/findings/` (backward compatibility)

The `<hash>` is the first 16 characters of the SHA256 hash of the workspace directory path.
