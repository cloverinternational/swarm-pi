# Tool Rendering & Streaming Architecture - Context Document

## Executive Summary

This document analyzes how tools are called, executed, and rendered in the SwarmOS TUI, with focus on streaming behavior and identified issues for improvement.

---

## 1. Current Architecture Overview

### 1.1 The Complete Data Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           TOOL EXECUTION FLOW                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  User Message                                                                │
│       ↓                                                                      │
│  SDKIntegration.ExecuteMessage()  [sdk_integration.go:1172]                  │
│       ↓                                                                      │
│  Agent.Execute()  [agent.go]                                                 │
│       ↓                                                                      │
│  Provider.Stream() → StreamChunk channel  [anthropic/stream.go]              │
│       ↓                                                                      │
│  Agent processes chunks, detects tool_calls                                  │
│       ↓                                                                      │
│  executeTools()  [agent.go:761-962]                                          │
│       │                                                                      │
│       ├─→ intermediateCallback(ToolCallUpdate)  ←── IMMEDIATE                │
│       │        ↓                                                             │
│       │   updateChan → listenForAgentUpdates() → agentToolCallMsg            │
│       │        ↓                                                             │
│       │   App.Update() drains updateQueue → OrderedBlocks.append()           │
│       │        ↓                                                             │
│       │   updateViewportContent() → UI shows "● bash [command]"              │
│       │                                                                      │
│       ├─→ hooksManager.EmitToolBeforeExecute()                               │
│       │        ↓                                                             │
│       │   intermediateCallback(HookExecutionUpdate)  ←── IMMEDIATE           │
│       │                                                                      │
│       ├─→ tool.Execute(ctx, params)  ←── BLOCKING UNTIL COMPLETE             │
│       │        ↓                                                             │
│       │   (For bash: cmd.Run() captures stdout+stderr to buffer)             │
│       │                                                                      │
│       ├─→ intermediateCallback(ToolResultUpdate)  ←── AFTER COMPLETION       │
│       │        ↓                                                             │
│       │   updateChan → agentToolResultMsg → OrderedBlocks.append()           │
│       │        ↓                                                             │
│       │   UI shows "⎿ [full output]"                                         │
│       │                                                                      │
│       └─→ hooksManager.EmitToolAfterExecute()                                │
│                ↓                                                             │
│            intermediateCallback(HookExecutionUpdate)                         │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 1.2 Key Files and Line References

| Component | File | Lines | Purpose |
|-----------|------|-------|---------|
| **IntermediateUpdate types** | `sdk/agent/agent.go` | 52-102 | Update type definitions |
| **executeTools()** | `sdk/agent/agent.go` | 761-962 | Tool execution orchestration |
| **callIntermediateCallback()** | `sdk/agent/agent.go` | 292-297 | Sends updates to UI |
| **BashTool.Execute()** | `sdk/tools/builtin/bash.go` | 89-177 | Bash command execution |
| **ExecuteMessage()** | `internal/chat/sdk_integration.go` | 1172-1719 | SDK-TUI bridge |
| **listenForAgentUpdates()** | `internal/chat/app.go` | 4708-4763 | Update channel listener |
| **Update() queue drain** | `internal/chat/app.go` | 1376-1520 | Process queued messages |
| **OrderedBlocks rendering** | `internal/chat/app.go` | 4962-5180 | Tool result display |
| **Message struct** | `internal/chat/app.go` | 107-122 | TUI message model |
| **RenderSettings** | `internal/chat/render_config.go` | 51-74 | Display configuration |

---

## 2. ISSUE: Tool Results Are Batched, Not Streamed

### 2.1 Current Behavior

For a command like `npm install` that runs for 30 seconds:

```
Time 0s:   ● bash [npm install]     ← Shows immediately (ToolCallUpdate)
Time 0-30s: <nothing visible>       ← User waits with no feedback
Time 30s:  ⎿ added 1234 packages... ← ENTIRE output appears at once (ToolResultUpdate)
```

### 2.2 Root Cause Analysis

**Location: `sdk/tools/builtin/bash.go:139-145`**
```go
// Capture stdout and stderr
var stdout, stderr bytes.Buffer
cmd.Stdout = &stdout
cmd.Stderr = &stderr

// Run command - BLOCKS until complete
err = cmd.Run()
```

The bash tool uses `bytes.Buffer` to capture all output, then returns it as a single `ToolResult` only after `cmd.Run()` completes.

**Location: `sdk/agent/agent.go:946-950`**
```go
// Emit tool result update for real-time UI BEFORE post-hooks
a.callIntermediateCallback(ctx, ToolResultUpdate{
    ID:     toolCall.ID,
    Output: result.Output,  // ← Full output, all at once
    Error:  nil,
})
```

The `ToolResultUpdate` is sent **once** after the tool finishes, containing the complete output.

### 2.3 What Users Expect

```
Time 0s:   ● bash [npm install]
Time 1s:   ⎿ npm WARN deprecated...
Time 2s:   ⎿ npm WARN deprecated...
Time 5s:   ⎿ added 100 packages...
Time 10s:  ⎿ added 500 packages...
Time 30s:  ⎿ added 1234 packages in 30s ✓
```

---

## 3. HOW TO ADD: Incremental Streaming for Tool Output

### 3.1 New IntermediateUpdate Type

**File: `sdk/agent/agent.go` (add after line 102)**
```go
// ToolOutputChunk represents incremental output during tool execution.
type ToolOutputChunk struct {
    ID      string // Tool call ID
    Chunk   string // Incremental output
    Stream  string // "stdout" or "stderr"
    IsFinal bool   // True if this is the last chunk
}

func (u ToolOutputChunk) UpdateType() string { return "tool_output_chunk" }
```

### 3.2 Streaming Tool Interface

**File: `sdk/tools/tool.go` (new interface)**
```go
// StreamingTool is an optional interface for tools that support incremental output.
type StreamingTool interface {
    Tool
    // ExecuteStreaming executes the tool and streams output via callback.
    // The callback is called for each chunk of output.
    ExecuteStreaming(ctx context.Context, params map[string]interface{},
        onOutput func(chunk string, stream string)) (*ToolResult, error)
}
```

### 3.3 Modified Bash Tool

**File: `sdk/tools/builtin/bash.go` (new method)**
```go
// ExecuteStreaming runs the command with incremental output streaming
func (t *BashTool) ExecuteStreaming(ctx context.Context, params map[string]any,
    onOutput func(chunk string, stream string)) (*tools.ToolResult, error) {

    // ... validation code same as Execute() ...

    // Create command
    cmd := exec.CommandContext(ctx, t.shell, "-c", command)
    if cwd != "" {
        cmd.Dir = cwd
    }

    // Create pipes for stdout and stderr
    stdoutPipe, err := cmd.StdoutPipe()
    if err != nil {
        return nil, sdkerror.Permanent("bash.pipe_failed", err.Error())
    }
    stderrPipe, err := cmd.StderrPipe()
    if err != nil {
        return nil, sdkerror.Permanent("bash.pipe_failed", err.Error())
    }

    // Collect full output for final result
    var fullOutput strings.Builder
    var mu sync.Mutex

    // Start command
    if err := cmd.Start(); err != nil {
        return nil, sdkerror.Permanent("bash.start_failed", err.Error())
    }

    // Stream stdout in goroutine
    var wg sync.WaitGroup
    wg.Add(2)

    go func() {
        defer wg.Done()
        scanner := bufio.NewScanner(stdoutPipe)
        for scanner.Scan() {
            line := scanner.Text() + "\n"
            mu.Lock()
            fullOutput.WriteString(line)
            mu.Unlock()
            onOutput(line, "stdout")
        }
    }()

    go func() {
        defer wg.Done()
        scanner := bufio.NewScanner(stderrPipe)
        for scanner.Scan() {
            line := scanner.Text() + "\n"
            mu.Lock()
            fullOutput.WriteString(line)
            mu.Unlock()
            onOutput(line, "stderr")
        }
    }()

    // Wait for output goroutines
    wg.Wait()

    // Wait for command to complete
    err = cmd.Wait()

    output := strings.TrimSpace(fullOutput.String())
    if output == "" {
        output = "Command completed successfully (no output)"
    }

    if err != nil {
        return tools.NewToolResult(output), sdkerror.Permanent("bash.command_failed", err.Error())
    }

    return tools.NewToolResult(output), nil
}
```

### 3.4 Agent executeTools() Modification

**File: `sdk/agent/agent.go` (modify executeTools around line 850)**
```go
// Execute tool - check if it supports streaming
if streamingTool, ok := tool.(tools.StreamingTool); ok {
    // Use streaming execution
    result, err = streamingTool.ExecuteStreaming(ctx, toolCall.Parameters,
        func(chunk string, stream string) {
            // Send incremental output to UI
            a.callIntermediateCallback(ctx, ToolOutputChunk{
                ID:      toolCall.ID,
                Chunk:   chunk,
                Stream:  stream,
                IsFinal: false,
            })
        })
} else {
    // Fall back to regular execution
    result, err = tool.Execute(ctx, toolCall.Parameters)
}
```

### 3.5 TUI Updates

**File: `internal/chat/app.go` (add new message type after line 1292)**
```go
// agentToolOutputChunkMsg carries incremental tool output
type agentToolOutputChunkMsg struct {
    callID  string
    chunk   string
    stream  string // "stdout" or "stderr"
    isFinal bool
}
```

**File: `internal/chat/app.go` (add to listenForAgentUpdates after line 4757)**
```go
case agent.ToolOutputChunk:
    // Incremental tool output
    logDebug("listenForAgentUpdates: ToolOutputChunk - ID: %s, %d chars", u.ID, len(u.Chunk))
    a.sendToRuntime(agentToolOutputChunkMsg{
        callID:  u.ID,
        chunk:   u.Chunk,
        stream:  u.Stream,
        isFinal: u.IsFinal,
    })
```

**File: `internal/chat/app.go` (add to Update() queue drain after line 1422)**
```go
case agentToolOutputChunkMsg:
    logDebug("[Queue] Processing tool output chunk: %s", qm.callID)
    if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
        lastMsg := &a.messages[len(a.messages)-1]
        // Find or create the tool result block for this callID
        found := false
        for i := len(lastMsg.OrderedBlocks) - 1; i >= 0; i-- {
            block := &lastMsg.OrderedBlocks[i]
            if block.Type == "tool_result" && block.ToolResult != nil &&
               block.ToolResult.CallID == qm.callID {
                // Append chunk to existing result
                block.ToolResult.Output += qm.chunk
                found = true
                break
            }
        }
        if !found {
            // Create new streaming result block
            toolResult := ToolResultDisplay{
                CallID: qm.callID,
                Output: qm.chunk,
            }
            lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
                Type:       "tool_result",
                ToolResult: &toolResult,
            })
        }
        a.updateViewportContent()
        a.msgViewport.GotoBottom()
    }
```

---

## 4. Exact Rendering Code Analysis

### 4.1 The OrderedBlocks Pattern

**File: `internal/chat/app.go:119-131`**
```go
// Ordered blocks for proper interleaved rendering
// This tracks the sequence: text → tool call → result → text → etc.
OrderedBlocks []MessageBlock

type MessageBlock struct {
    Type          string // "content", "tool_call", "tool_result", "thinking", "hook_execution"
    Content       string
    ToolCall      *ToolCallDisplay
    ToolResult    *ToolResultDisplay
    HookExecution *HookExecutionDisplay
}
```

This structure ensures content appears in the **exact order** it was generated:
1. Text content
2. Tool call announcement
3. (Optional) Pre-hook execution
4. Tool result
5. (Optional) Post-hook execution
6. More text content

### 4.2 Tool Call Rendering

**File: `internal/chat/app.go:5006-5046`**
```go
case "tool_call":
    if block.ToolCall != nil {
        tc := block.ToolCall
        toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FAB387"))  // Orange
        dimToolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))

        var toolLine string
        // Smart parameter display
        if cmdParam, ok := tc.Parameters["command"].(string); ok && len(tc.Parameters) == 1 {
            toolLine = fmt.Sprintf("● %s [%s]", tc.Name, cmdParam)
        } else if pathParam, ok := tc.Parameters["file_path"].(string); ok {
            toolLine = fmt.Sprintf("● %s (%s)", tc.Name, pathParam)
        } else if !a.showFullToolOutput {
            toolLine = fmt.Sprintf("● %s (%d params)", tc.Name, len(tc.Parameters))
        } else {
            // Show all params
            var parts []string
            for key, val := range tc.Parameters {
                parts = append(parts, fmt.Sprintf("%s → %v", key, val))
            }
            toolLine = fmt.Sprintf("● %s (%s)", tc.Name, strings.Join(parts, ", "))
        }

        msgLines = append(msgLines, "")
        msgLines = append(msgLines, "        "+toolStyle.Render(toolLine))
    }
```

**Visual Output:**
```
        ● bash [npm install]
```

### 4.3 Tool Result Rendering

**File: `internal/chat/app.go:5048-5133`**
```go
case "tool_result":
    if block.ToolResult != nil {
        tr := block.ToolResult

        // Find tool name by matching CallID to previous tool_call
        var toolName string
        for _, prevBlock := range msg.OrderedBlocks {
            if prevBlock.Type == "tool_call" && prevBlock.ToolCall != nil {
                if prevBlock.ToolCall.ID == tr.CallID {
                    toolName = prevBlock.ToolCall.Name
                    break
                }
            }
        }

        // Get display settings from RenderSettings
        displayMode := a.renderSettings.GetDisplayModeEnum(toolName)
        maxLines := a.renderSettings.GetMaxLines(toolName)

        resultStyle := lipgloss.NewStyle().
            Foreground(lipgloss.Color(a.renderSettings.GetToolColor(toolName)))
        connectorStyle := lipgloss.NewStyle().
            Foreground(lipgloss.Color(a.renderSettings.Colors.Connector))
        errorStyle := lipgloss.NewStyle().
            Foreground(lipgloss.Color(a.renderSettings.Colors.ToolError))

        // Skip if hidden
        if displayMode == DisplayHidden {
            break
        }

        if tr.Error != "" {
            // Error rendering with truncation
            errWrapped := wrapTextWithIndent(tr.Error, a.msgViewport.Width-15, "              ")
            if displayMode == DisplayMinimal {
                // Show only first line
                msgLines = append(msgLines, "            "+connectorStyle.Render("⎿")+" "+errorStyle.Render("Error: "+errWrapped[0]))
            } else if !a.showFullToolOutput && len(errWrapped) > maxLines {
                // Compact mode: show maxLines then truncation notice
                for idx := 0; idx < maxLines; idx++ {
                    if idx == 0 {
                        msgLines = append(msgLines, "            "+connectorStyle.Render("⎿")+" "+errorStyle.Render("Error: "+errWrapped[idx]))
                    } else {
                        msgLines = append(msgLines, errorStyle.Render(errWrapped[idx]))
                    }
                }
                truncatedStyle := lipgloss.NewStyle().Italic(true)
                msgLines = append(msgLines, "              "+truncatedStyle.Render(
                    fmt.Sprintf("... (%d more lines, press Ctrl+O to expand)", len(errWrapped)-maxLines)))
            }
        } else {
            // Normal output rendering (same truncation logic)
            outputWrapped := wrapTextWithIndent(tr.Output, a.msgViewport.Width-15, "              ")
            // ... similar truncation logic ...
        }
    }
```

**Visual Output:**
```
            ⎿ added 1234 packages in 30s

              37 packages are looking for funding
                run `npm fund` for details
              ... (15 more lines, press Ctrl+O to expand)
```

### 4.4 Display Modes

**File: `internal/chat/render_config.go:12-20`**
```go
type ToolDisplayMode string

const (
    DisplayVerbose ToolDisplayMode = "verbose" // Show full output
    DisplayCompact ToolDisplayMode = "compact" // Show truncated (default)
    DisplayMinimal ToolDisplayMode = "minimal" // Only show tool name + 1 line summary
    DisplayHidden  ToolDisplayMode = "hidden"  // Don't show output, only name
    DisplayDiff    ToolDisplayMode = "diff"    // Special diff view for file operations
)
```

**Default Tool Settings (render_config.go:173-203):**
| Tool | Display Mode | Max Lines |
|------|--------------|-----------|
| bash | compact | 6 |
| file_read | compact | 10 |
| file_write | compact | 10 |
| grep | compact | 15 |

---

## 5. Hook Execution Display

### 5.1 Hook Update Flow

**File: `sdk/agent/agent.go:812-826`**
```go
if a.hooksManager != nil {
    hookResults, hookErr := a.hooksManager.EmitToolBeforeExecute(ctx, toolCall.Name, toolCall.Parameters)

    // Emit hook execution updates for UI
    for _, hr := range hookResults {
        a.callIntermediateCallback(ctx, HookExecutionUpdate{
            HookName: hr.HookName,
            ToolName: toolCall.Name,
            Phase:    "before",
            Success:  hr.Success,
            Output:   hr.Output,
            Blocked:  hr.Blocked,
            Error:    hr.Error,
        })
    }
}
```

### 5.2 Hook Rendering

**File: `internal/chat/app.go:5135-5178`**
```go
case "hook_execution":
    if block.HookExecution != nil {
        he := block.HookExecution

        // Subtle grey styling
        hookStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
        successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
        errorHookStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AA4444"))

        // Icon: ✓ for success, ✗ for failure
        icon := "✓"
        if !he.Success || he.Blocked {
            icon = "✗"
        }

        // Phase: "pre" or "post"
        phaseStr := "pre"
        if he.Phase == "after" {
            phaseStr = "post"
        }

        hookLine := fmt.Sprintf("%s [%s-hook] %s", icon, phaseStr, he.HookName)

        if he.Blocked {
            msgLines = append(msgLines, "        "+errorHookStyle.Render(hookLine+" (BLOCKED)"))
            if he.Error != "" {
                msgLines = append(msgLines, "          "+errorHookStyle.Render("⎿ "+he.Error))
            }
        } else if !he.Success {
            msgLines = append(msgLines, "        "+errorHookStyle.Render(hookLine))
        } else {
            msgLines = append(msgLines, "        "+hookStyle.Render(hookLine))
            if he.Output != "" {
                msgLines = append(msgLines, "          "+successStyle.Render("⎿ "+he.Output))
            }
        }
    }
```

**Visual Output (success):**
```
        ✓ [pre-hook] security-check
          ⎿ Command validated
        ● bash [rm -rf /tmp/cache]
        ✓ [post-hook] audit-log
          ⎿ Logged to /var/log/audit.log
```

**Visual Output (blocked):**
```
        ✗ [pre-hook] security-check (BLOCKED)
          ⎿ Dangerous command detected: rm -rf /
```

---

## 6. Summary of Changes Required for Streaming

### 6.1 Files to Modify

| File | Changes |
|------|---------|
| `sdk/agent/agent.go` | Add `ToolOutputChunk` type, modify `executeTools()` |
| `sdk/tools/tool.go` | Add `StreamingTool` interface |
| `sdk/tools/builtin/bash.go` | Add `ExecuteStreaming()` method |
| `internal/chat/app.go` | Add `agentToolOutputChunkMsg`, update queue processing, update `listenForAgentUpdates()` |

### 6.2 New Types Summary

```go
// sdk/agent/agent.go
type ToolOutputChunk struct {
    ID      string
    Chunk   string
    Stream  string // "stdout" or "stderr"
    IsFinal bool
}

// sdk/tools/tool.go
type StreamingTool interface {
    Tool
    ExecuteStreaming(ctx context.Context, params map[string]interface{},
        onOutput func(chunk string, stream string)) (*ToolResult, error)
}

// internal/chat/app.go
type agentToolOutputChunkMsg struct {
    callID  string
    chunk   string
    stream  string
    isFinal bool
}
```

### 6.3 Backward Compatibility

- Tools that don't implement `StreamingTool` continue working as before
- `ToolResultUpdate` still sent for final result (ensures complete output in conversation history)
- `ToolOutputChunk` is optional enhancement for real-time feedback

---

## 7. Testing Considerations

### 7.1 Test Commands
```bash
# Long-running with frequent output
npm install

# Mixed stdout/stderr
make 2>&1

# Slow output (one line per second)
for i in {1..10}; do echo "Line $i"; sleep 1; done

# Large output
find / -name "*.go" 2>/dev/null
```

### 7.2 Edge Cases
- Command with no output
- Command that only writes to stderr
- Command killed by timeout
- Command that writes binary data
- Very long single lines (>2000 chars)

---

## 8. References

- `sdk/agent/agent.go` - Agent orchestration, IntermediateUpdate system
- `sdk/tools/builtin/bash.go` - Current blocking implementation
- `sdk/tools/tool.go` - Tool interface definitions
- `internal/chat/app.go` - TUI message handling, OrderedBlocks rendering
- `internal/chat/sdk_integration.go` - SDK-TUI bridge, ExecuteMessage()
- `internal/chat/render_config.go` - Display modes and tool settings
