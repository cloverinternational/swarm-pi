# Unified Message Rendering System

## Overview

The TUI now uses a **single source of truth** for rendering messages across all views. Both the main chat view and conversation history preview use the same `renderMessageList()` function, ensuring pixel-perfect identical styling.

## The Problem (Before)

Previously, there were **two separate rendering implementations**:

### Main Chat View (`updateViewportContent`)
- Full-featured rendering with ~800 lines of code
- Supported OrderedBlocks, tool calls/results, markdown, thinking, sub-agents, hooks
- Used proper display modes (compact/verbose/minimal/hidden)
- Had streaming animations with spinners

### Conversation Preview (`renderConversationPreview`)
- Primitive ~75 lines of basic rendering
- Only rendered plain text content
- **Missing:** tool calls, tool results, markdown, thinking, sub-agents, hooks
- Messages looked completely different from the main view

This caused:
- Inconsistent UX - same messages looked different in different views
- Code duplication - similar logic in two places
- Maintenance burden - fixes needed in multiple locations
- Missing features - preview couldn't show tools even if enabled

## The Solution

### Single Unified Renderer

```go
func (a *App) renderMessageList(messages []Message, width int) []string
```

This function is now the **only place** where messages are rendered. It handles:

| Feature | Supported |
|---------|-----------|
| User messages with `│` border | ✅ |
| Assistant messages with `▌` border | ✅ |
| System messages with `◆` prefix | ✅ |
| Tool calls with parameters | ✅ |
| Tool results with display modes | ✅ |
| Markdown with syntax highlighting | ✅ |
| Thinking blocks | ✅ |
| Sub-agent nested activity | ✅ |
| Hook execution indicators | ✅ |
| Bash commands with `!` prefix | ✅ |
| File attachments | ✅ |
| Focus highlighting (navigation) | ✅ |
| Streaming spinners | ✅ |

### How It Works

```
┌─────────────────────────────────────────────────────────────┐
│                    renderMessageList()                       │
│                  (Single Source of Truth)                    │
└─────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              │               │               │
              ▼               ▼               ▼
     ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
     │   Main      │  │ Conversation│  │  Streaming  │
     │  Chat View  │  │   Preview   │  │  Incremental│
     └─────────────┘  └─────────────┘  └─────────────┘
```

### Usage Examples

**Main Chat View:**
```go
func (a *App) updateViewportContent() {
    // ... caching logic ...
    
    msgLines := a.renderMessageList(a.messages, a.msgViewport.Width)
    
    // ... set viewport content ...
}
```

**Conversation Preview:**
```go
func (a *App) renderConversationPreview(width int) string {
    // ... load messages from SDK ...
    
    msgLines := a.renderMessageList(previewMessages, width-4)
    
    // ... handle scrolling, add header/footer ...
}
```

**Single Message (for streaming):**
```go
func (a *App) renderSingleMessage(index int, msg *Message) []string {
    messages := []Message{*msg}
    return a.renderMessageList(messages, a.msgViewport.Width)
}
```

## Message Conversion

When loading messages for preview, SDK messages are now fully converted:

```go
msg := Message{
    Role:      string(sdkMsg.Role),
    Content:   sdkMsg.Content,
    Timestamp: sdkMsg.Timestamp,
    Model:     sdkMsg.Model,
    Thinking:  sdkMsg.Thinking,
}

// Convert tool calls
for _, tc := range sdkMsg.ToolCalls {
    msg.ToolCalls = append(msg.ToolCalls, ToolCallDisplay{
        ID:         tc.ID,
        Name:       tc.Name,
        Parameters: tc.Parameters,
    })
}

// Convert tool results
for _, tr := range sdkMsg.ToolResults {
    msg.ToolResults = append(msg.ToolResults, ToolResultDisplay{
        CallID: tr.CallID,
        Output: tr.Output,
        Error:  tr.Error.Message,
    })
}
```

## Rendering Features

### Tool Calls
```
● bash [ls -la]
● Read (src/main.go)
● Grep (pattern → "func.*Test")
```

With spinner during execution:
```
◐ bash [running command...]
```

### Tool Results
```
● bash [echo hello]
  ⎿ hello
```

With truncation in compact mode:
```
● Read (large_file.go)
  ⎿ package main
  ⎿ 
  ⎿ import (
  ⎿     "fmt"
  ⎿     "os"
      ... (247 more lines, press Ctrl+O to expand)
```

### Thinking Blocks
```
◆ Thinking
  ⎿ I need to analyze this code to understand...
```

### Sub-Agent Activity
```
      ◆ Sub-Agent: code-reviewer
      ○ Read src/main.go
      ⎿ [file contents...]
```

### System Messages
```
  ◆ You are a helpful coding assistant...
  ────────────────────────────────────────
```

## Configuration

The renderer respects all existing settings:

| Setting | Effect |
|---------|--------|
| `a.showThinking` | Show/hide thinking blocks |
| `a.showFullToolOutput` | Verbose vs compact tool output |
| `a.renderSettings` | Per-tool display modes and max lines |
| `a.streamingMessage` | Enable spinner animations |
| `a.messageNavMode` | Enable focus highlighting |

## Performance

- **Caching preserved:** `updateViewportContent()` still uses `viewportCachedLines`
- **Incremental updates:** Streaming still uses `renderSingleMessage()` for efficiency
- **No regression:** Same rendering logic, just consolidated

## Files Changed

| File | Change |
|------|--------|
| `internal/chat/app.go` | Added `renderMessageList()`, updated callers |

## Stats

- **Lines added:** 765
- **Lines removed:** 931
- **Net reduction:** 166 lines
- **Duplicate code eliminated:** ~75 lines of primitive rendering
