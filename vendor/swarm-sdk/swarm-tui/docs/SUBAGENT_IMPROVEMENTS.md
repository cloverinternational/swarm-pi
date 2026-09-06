# SubAgent and BackgroundTask Visibility Improvements

## Summary
Enhanced visibility for both Task (subagent) and BackgroundTask tools to make their operations more transparent and readable.

## Changes Made

### 1. Task Tool (SubAgent) Improvements

#### Data Structure Changes
- **File:** `headless/core/message.go`
  - Added `TaskInstruction string` field to `SubAgentDisplay` struct
  - Captures the original task sent to the subagent

- **File:** `internal/chat/app.go`
  - Added `TaskInstruction string` field to local `SubAgentDisplay` struct
  - Modified subagent block creation logic (lines 2645-2670) to:
    - Search backwards through OrderedBlocks for the parent Task tool call
    - Extract the "task" parameter
    - Store it in the SubAgentDisplay for rendering

#### Rendering Improvements
- **File:** `internal/chat/subagent_render.go`
  - Added "Task:" section at top of subagent box (lines 122-146)
    - Shows truncated task instruction (max width-aware)
    - Styled in italics with muted color
  - Added "Output:" label before content blocks (lines 180-186)
    - Clearly separates final output from thinking/intermediate content
    - Styled in success color (green) and bold
  - Improved visual hierarchy with indentation

#### Visual Result
```
┌─ task-agent [3 tools] ─────────────────────┐
│ Task:                                       │
│   Search for files containing "TODO"...    │
│                                             │
│ ~ thinking about search strategy...        │
│                                             │
│ Output:                                     │
│   Found 5 files with TODO markers          │
│   - file1.go: 3 TODOs                       │
│   - file2.go: 2 TODOs                       │
│                                             │
│ ─── Tools (3) ───                           │
│   ✓ Grep  pattern="TODO"                    │
│   ✓ Read  file.go                           │
│   ✓ Write summary.md                        │
└─────────────────────────────────────────────┘
```

### 2. BackgroundTask Tool Improvements

#### Tool Result Formatting
- **File:** `sdk/tools/builtin/check_background_agent.go`
  - Added `strings` and `time` imports
  - Completely rewrote `getResult()` function (lines 250-327) to provide structured, readable output
  - New format includes:
    - Boxed header with ASCII art
    - Clear metadata section (Agent ID, Status, Timing, Tokens, Cost)
    - Separated "AGENT OUTPUT" section with borders
    - Summary footer with key statistics
    - Better formatting for different status states (completed, failed, running, cancelled)

#### Visual Result
```
╔═══════════════════════════════════════════════════════════════╗
║         BACKGROUND AGENT RESULT
╚═══════════════════════════════════════════════════════════════╝

Agent ID:     bg-1234567890
Status:       completed
Start Time:   2024-01-28 14:30:00
End Time:     2024-01-28 14:35:23
Duration:     5m23s
Turns:        12
Tokens Used:  45234
Cost (USD):   $0.1245

───────────────────────────────────────────────────────────────
AGENT OUTPUT:
───────────────────────────────────────────────────────────────

Successfully analyzed 150 files and generated comprehensive report.
Key findings:
- 45 performance issues identified
- 12 security vulnerabilities found
- 230 optimization opportunities

Full report saved to analysis_report.md

───────────────────────────────────────────────────────────────
Completed in 12 turns using 45234 tokens
───────────────────────────────────────────────────────────────
```

## Benefits

### Task Tool (SubAgent)
1. **Transparency:** Users can now see what task was assigned to the subagent
2. **Context:** Better understanding of what the subagent is supposed to accomplish
3. **Output Clarity:** Clear distinction between thinking, intermediate work, and final output
4. **Debugging:** Easier to verify if subagent received correct instructions

### BackgroundTask Tool
1. **Readability:** Structured, formatted output instead of raw JSON
2. **Professionalism:** Boxed output with clear sections
3. **Information:** Complete metadata about execution (timing, tokens, cost)
4. **Usability:** Main agent can easily read and understand what background agent accomplished

## Testing

Built successfully:
```bash
go build -o /tmp/tui-test ./cmd/tui-client
```

## Files Modified

1. `headless/core/message.go` - SubAgentDisplay data structure
2. `internal/chat/app.go` - SubAgentDisplay creation logic
3. `internal/chat/subagent_render.go` - Rendering improvements
4. `sdk/tools/builtin/check_background_agent.go` - Result formatting

## Backward Compatibility

All changes are additive and backward compatible:
- New `TaskInstruction` field is optional (omitempty tag)
- Existing code that doesn't populate it will continue to work
- Rendering gracefully handles empty task instructions
- BackgroundTask tool result format is internal to tool output

## Future Enhancements

Potential future improvements:
1. Add intermediate update streaming for BackgroundTask (requires SDK changes)
2. Add collapsible task instruction (Ctrl+E to expand full text)
3. Show tool call details for background agents (requires activity log collection in SDK)
4. Add progress indicators for long-running background tasks
