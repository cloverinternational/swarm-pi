# Bash Tool Terminal-Style Rendering

## Summary
Transformed the Bash tool output rendering from plain text to a terminal-within-terminal aesthetic with ANSI color preservation and hacker-style visual design.

## Changes Made

### 1. New Terminal Renderer
- **File:** `internal/chat/bash_terminal_render.go` (new file)
- Created `BashTerminalRenderer` with terminal-style box rendering
- Features:
  - Matrix green borders (#00FF41) for normal execution
  - Error state: Red borders (palette.Error)
  - Running state: Yellow/warning borders (palette.Warning)
  - Terminal box characters: ┌─┐│├┤└─┘
  - Prompt line with $ symbol and command display
  - ANSI color code preservation in output
  - Automatic line wrapping with ANSI support
  - Running indicator: cursor block (▊)

### 2. ANSI Color Handling
- `visualLength()`: Calculates visible string length ignoring ANSI escape codes
- `wrapLinePreservingANSI()`: Wraps long lines while preserving color codes
- Output displays exactly as it would in a real terminal (colors, formatting preserved)

### 3. Visual Design
```
┌─[ bash ]──────────────────────────────────┐
│ $ ls -la --color=always
├────────────────────────────────────────────┤
│ drwxr-xr-x 5 user group 4096 Jan 28 14:30 │
│ -rw-r--r-- 1 user group  123 Jan 28 14:29 │
│ -rwxr-xr-x 1 user group 4567 Jan 28 14:28 │
└────────────────────────────────────────────┘
```

Running state:
```
┌─[ bash (running) ]────────────────────────┐
│ $ npm test
├────────────────────────────────────────────┤
│ ▊                                          │
└────────────────────────────────────────────┘
```

Error state (red borders):
```
┌─[ bash (error) ]──────────────────────────┐
│ $ invalid-command
├────────────────────────────────────────────┤
│ bash: invalid-command: command not found   │
└────────────────────────────────────────────┘
```

### 4. Integration Points

#### App Struct
- **File:** `internal/chat/app.go` (line 597)
- Added `bashTerminalRenderer *BashTerminalRenderer` field

#### Initialization
- **File:** `internal/chat/app.go` (line 918)
- Initialize renderer: `app.bashTerminalRenderer = NewBashTerminalRenderer(120)`

#### Rendering Logic
- **File:** `internal/chat/app.go` (lines 10359-10383)
- Check if tool is "Bash"
- Extract command from tool parameters
- Detect running/error state
- Update renderer width dynamically
- Render with terminal style

```go
if toolName == "Bash" {
    // Find command from tool parameters
    var command string
    for _, prevBlock := range msg.OrderedBlocks {
        if prevBlock.Type == "tool_call" && prevBlock.ToolCall != nil {
            if prevBlock.ToolCall.ID == tr.CallID {
                if cmd, ok := prevBlock.ToolCall.Parameters["command"].(string); ok {
                    command = cmd
                }
                break
            }
        }
    }
    
    // Check if bash is currently running
    isBashRunning := a.streamingMessage && tr.Output == "" && tr.Error == ""
    hasError := tr.Error != ""
    
    // Update width for terminal renderer
    a.bashTerminalRenderer.width = width
    
    // Render in terminal style
    bashLines := a.bashTerminalRenderer.RenderTerminal(command, tr.Output, isBashRunning, hasError)
    msgLines = append(msgLines, bashLines...)
}
```

## Color Palette

### Terminal Colors
- **Border (normal):** #00FF41 (Matrix green)
- **Prompt:** #00FF41 (Matrix green, bold)
- **Cursor:** #00FF41 (Matrix green, bold)
- **Background:** #0A0E14 (Dark terminal background)
- **Text:** #C5C9DE (Light gray)

### State Colors
- **Normal:** Matrix green borders
- **Running:** Warning yellow borders (palette.Warning)
- **Error:** Red borders (palette.Error)

## Benefits

1. **Visual Appeal:** Professional terminal-within-terminal aesthetic
2. **Color Preservation:** ANSI colors from actual terminal output are displayed
3. **Status Clarity:** Immediate visual feedback (green/yellow/red borders)
4. **Command Visibility:** Shows the executed command prominently
5. **Streaming Support:** Animated cursor for running commands
6. **Error Handling:** Clear error state indication

## Testing

Built successfully:
```bash
go build -o /tmp/tui-test3 ./cmd/tui-client
```

## Example Use Cases

### Colored Output
```bash
# Command with colors
$ ls --color=always
```
→ Colors are preserved and displayed in terminal box

### Long Running Commands
```bash
# Running npm test
$ npm test
```
→ Shows cursor block (▊) while running
→ Yellow borders during execution

### Build Commands
```bash
# Build with output
$ go build ./...
```
→ Compilation output in terminal box
→ Green borders on success, red on error

### Directory Listings
```bash
# With file permissions
$ ls -la
```
→ Formatted output with proper alignment
→ Wrapped lines preserve color codes

## Files Modified

1. `internal/chat/bash_terminal_render.go` - New terminal renderer
2. `internal/chat/app.go` - Integration and rendering logic

## Future Enhancements

Potential improvements:
1. Exit code display in terminal footer
2. Execution time display in terminal title
3. Scroll support for very long outputs
4. Syntax highlighting for common commands
5. Auto-detection of color support
6. Terminal size indicator
7. Copy command button/shortcut
