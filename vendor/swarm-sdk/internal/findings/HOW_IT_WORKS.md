# How the Analysis System Works

## Quick Answer

The analysis system uses a **chain of responsibility pattern** where multiple specialized analyzers examine tool output, each looking for different patterns. Results are aggregated to decide:

1. **Should we capture this as a finding?** (yes/no)
2. **Should we create a follow-up task?** (yes/no for errors)
3. **What metadata applies?** (tags, priority, insights)

## Visual Flow

```
┌─────────────────────────────────────────────────────────────────┐
│  TOOL EXECUTION RESULT                                          │
│  {                                                             │
│    "tool_name": "evaluate",                                     │
│    "output": {                                                 │
│      "success": true,                                          │
│      "pgr": 0.87,                                              │
│      "accuracy": 0.95                                          │
│    }                                                           │
│  }                                                             │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  TOOLRESULTANALYSISHOOK (Priority 90)                          │
│  Runs ALL registered analyzers in parallel                    │
└─────────────────────────────────────────────────────────────────┘
                              ↓
          ┌──────────────────┼──────────────────┐
          ↓                  ↓                  ↓
┌────────────────┐  ┌────────────────┐  ┌────────────────┐
│  DEFAULT       │  │  ERROR         │  │  INSIGHT       │
│  ANALYZER      │  │  ANALYZER      │  │  ANALYZER      │
│                │  │                │  │                │
│ "evaluate is   │  │ success=true?  │  │ Look for:      │
│  interesting"  │  │ ├─ No error    │  │ • pgr          │
│                │  │ └─ No action   │  │ • accuracy     │
│ capture=true   │  │                │  │ • duration_ms  │
│ tags=[evaluate]│  │ capture=false  │  │                │
│                │  │                │  │ capture=true   │
└────────────────┘  └────────────────┘  │ insights=[...] │
          └──────────────────┼──────────────────┘
                             ↓
┌─────────────────────────────────────────────────────────────────┐
│  AGGREGATION (OR logic)                                        │
│  ─────────────────────────────────────────────────────────────  │
│  capture = true OR false OR true          = true               │
│  create_task = false OR false OR false    = false             │
│  priority = max(50, 50, 75)               = 75                │
│  tags = union([eval], [], [pgr, eval])    = [eval, pgr]       │
│  insights = [] + [] + ["pgr: 0.87"]        = ["pgr: 0.87"]     │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  EVENT METADATA (set by analysis hook)                         │
│  {                                                             │
│    "capture_finding": true,                                   │
│    "finding_tags": ["evaluate", "pgr"],                        │
│    "finding_priority": 75,                                     │
│    "finding_insights": ["pgr: 0.87"]                          │
│  }                                                             │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  LOCALFINDINGSHOOK (Priority 85)                              │
│  Only runs if capture_finding=true                             │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│  FINDING CREATED                                               │
│  • Written to JSON file                                        │
│  • Async indexed                                               │
│  • Available for search                                        │
└─────────────────────────────────────────────────────────────────┘
```

## Detailed Analyzer Behavior

### 1. Default Analyzer - "The Gatekeeper"

**Question:** Is this tool generally interesting?

```go
interestingTools := {
    "evaluate":   true,  // Evaluations are always interesting
    "benchmark":  true,  // Benchmarks are always interesting
    "build":      true,  // Builds are interesting
    "run_test":   true,  // Test runs are interesting
    "read_file":  false, // Reading files is boring
    "write_file": false, // Writing files is boring
}
```

**Example Outputs:**

| Tool | Success | Result |
|------|---------|--------|
| evaluate | true | capture=true, tags=[evaluate, automated, success] |
| evaluate | false | capture=true, tags=[evaluate, automated, failure], priority=70 |
| read_file | true | capture=false |
| read_file | false | capture=false |

### 2. Error Analyzer - "The Sentry"

**Question:** Did something go wrong?

**Error Detection Logic:**
```go
hasError = (
    output["error"] != "" ||
    output["error_message"] != "" ||
    output["stderr"] != "" ||
    output["success"] == false
)
```

**Action on Error:**
```go
if hasError {
    capture = true              // Always capture errors
    create_task = true          // Create follow-up task
    priority = 90               // High priority
    tags = ["error", "needs-attention"]
    task_description = "Investigate {tool} failure: {error}"
}
```

**Example:**

```json
// Input
{
  "tool_name": "build",
  "output": {
    "success": false,
    "error_message": "undefined: someVariable in main.go:42"
  }
}

// Analysis Result
{
  "should_capture": true,
  "should_create_task": true,
  "task_description": "Investigate build failure: undefined: someVariable in main.go:42",
  "priority": 90,
  "tags": ["build", "automated", "error", "needs-attention"],
  "insights": ["Tool build failed with error"]
}
```

### 3. Insight Analyzer - "The Detective"

**Question:** What can we learn from this result?

**Metrics it looks for:**
- `pgr` (Performance Gap Recovery) - main metric for W2S
- `accuracy` - model accuracy
- `duration_ms` - execution time
- `tokens_used` - LLM token consumption
- `score` - generic score metric
- `coverage` - test coverage

**Action:**
```go
for each metric in output:
    capture = true
    insights = append("{metric}: {value}")
    
    if metric == "pgr":
        tags = append("evaluation", "pgr")
        priority = 75
```

**Example:**

```json
// Input
{
  "tool_name": "evaluate",
  "output": {
    "success": true,
    "pgr": 0.87,
    "accuracy": 0.95,
    "duration_ms": 1234
  }
}

// Analysis Result
{
  "should_capture": true,
  "priority": 75,
  "tags": ["evaluate", "automated", "success", "evaluation", "pgr"],
  "insights": [
    "pgr: 0.87",
    "accuracy: 0.95",
    "duration_ms: 1234"
  ]
}
```

## Aggregation Examples

### Example 1: Successful Evaluation

```
┌─────────────────────────────────────────────────────────────┐
│ TOOL: evaluate                                              │
│ OUTPUT: {success: true, pgr: 0.87}                         │
└─────────────────────────────────────────────────────────────┘

DefaultAnalyzer:
  → capture=true, tags=[evaluate, automated, success]

ErrorAnalyzer:
  → capture=false (no error)

InsightAnalyzer:
  → capture=true, tags=[evaluation, pgr], insights=["pgr: 0.87"]

───────────────────────────────────────────────────────────────
AGGREGATED:
  capture=true          (true OR false OR true)
  create_task=false     (false OR false OR false)
  priority=75           (max of 50, 50, 75)
  tags=[evaluate, automated, success, evaluation, pgr]
  insights=["pgr: 0.87"]
```

### Example 2: Failed Build with Error

```
┌─────────────────────────────────────────────────────────────┐
│ TOOL: build                                                 │
│ OUTPUT: {success: false, error_message: "compile failed"}  │
└─────────────────────────────────────────────────────────────┘

DefaultAnalyzer:
  → capture=true, tags=[build, automated, failure]

ErrorAnalyzer:
  → capture=true, create_task=true, priority=90,
    tags=[error, needs-attention]
    task_description="Investigate build failure: compile failed"

InsightAnalyzer:
  → capture=false (no interesting metrics)

───────────────────────────────────────────────────────────────
AGGREGATED:
  capture=true
  create_task=true
  priority=90           (max of 50, 90, 50)
  tags=[build, automated, failure, error, needs-attention]
  task_description="Investigate build failure: compile failed"
```

### Example 3: Edge Case - Partial Success with Metrics

```
┌─────────────────────────────────────────────────────────────┐
│ TOOL: evaluate                                              │
│ OUTPUT: {success: false, error: "partial", pgr: 0.65}      │
└─────────────────────────────────────────────────────────────┘

DefaultAnalyzer:
  → capture=true, tags=[evaluate, automated, failure]

ErrorAnalyzer:
  → capture=true, create_task=true, priority=90,
    tags=[error, needs-attention]

InsightAnalyzer:
  → capture=true, tags=[evaluation, pgr], insights=["pgr: 0.65"]

───────────────────────────────────────────────────────────────
AGGREGATED:
  capture=true          (all three say yes)
  create_task=true      (error analyzer says yes)
  priority=90           (error takes precedence)
  tags=[evaluate, automated, failure, error, needs-attention,
        evaluation, pgr]
  insights=["pgr: 0.65"]
```

## How Capture Works

After analysis sets the metadata, the LocalFindingsHook:

1. **Checks the flag:** `if event.Metadata["capture_finding"] != true { skip }`

2. **Creates a Finding struct:**
   ```go
   finding := Finding{
       FindingID:      generateUUID(),
       ToolName:       event.Data["tool_name"],
       ToolInput:      event.Data["tool_input"],
       ToolOutput:     event.Data["tool_output"],
       Timestamp:      time.Now(),
       AgentID:        event.AgentID,
       ConversationID: event.ConversationID,
       ContextSummary: buildSummary(toolName, event),
       Tags:           extractTags(event.Metadata),
       Metadata: FindingMetadata{
           Priority:  extractPriority(event.Metadata),
           Success:   checkSuccess(event.Data["tool_output"]),
       },
   }
   ```

3. **Writes to cache:**
   - File: `~/.swarmos/findings/cache/2026/04/14/finding_<uuid>.json`
   - Also appends to: `findings.jsonl` (for fast scanning)

4. **Async indexes:**
   - Background goroutine updates semantic index
   - Non-blocking (failures are logged but don't stop execution)

## Why This Design?

### Separation of Concerns

| Hook | Responsibility | Why Separate? |
|------|---------------|---------------|
| ToolResultAnalysisHook | Decides WHAT to capture | Logic can be customized without changing storage |
| LocalFindingsHook | Decides HOW to capture | Storage can be swapped (file, DB, cloud) without changing analysis |

### Fail-Open Philosophy

- Analysis errors don't block tool execution
- Storage errors are logged but don't stop the chain
- Missing metadata = conservative default (don't capture)

### Extensibility

You can add custom analyzers:

```go
type MyAnalyzer struct{}

func (a *MyAnalyzer) Analyze(ctx context.Context, toolName string, input, output map[string]any) (findings.AnalysisResult, error) {
    // Your custom logic
    return findings.AnalysisResult{
        ShouldCapture: true,
        Tags:          []string{"custom"},
    }, nil
}

func (a *MyAnalyzer) Name() string {
    return "my-analyzer"
}

// Register it:
hook := builtin.NewToolResultAnalysisHook()
hook.RegisterAnalyzer(&MyAnalyzer{})
```

## Testing the System

### Run the Integration Demo

```bash
cd swarm-sdk/tests/integration
go run findings_demo.go
```

This shows:
1. Successful evaluation → captured with PGR insight
2. Failed build → captured with error, task created
3. Boring read → not captured

### Run Unit Tests

```bash
cd swarm-sdk/hooks/builtin
go test -v -run "TestToolResult"
```

Tests verify:
- Event filtering (only AfterTool events)
- Each analyzer's logic
- Aggregation behavior
- Metadata flag setting

## Summary

**Analysis is a multi-stage pipeline:**

1. **Tool executes** → Hook system fires event
2. **Analysis hook** (P90) runs analyzers → sets metadata flags
3. **Findings hook** (P85) checks flags → writes to storage
4. **Finding available** for search and discovery

**Key insight:** The system captures what matters (errors, evaluations, metrics) and ignores what doesn't (routine file operations), enabling agents to learn from their execution history without drowning in noise.
