# Chat Message Highlighting and Scrolling Improvements

## Overview

This document describes the improvements made to message highlighting and keyboard scrolling functionality for the chat application.

## Message Rendering and Highlighting Logic

### Key Files

#### 1. `/internal/chat/app.go`
**Primary file containing message rendering and navigation logic:**

- **Line 10138**: `renderMessageList()` - Renders chat messages with styling
- **Line 11033**: `renderMessageListWithPositions()` - NEW: Renders messages with accurate line position tracking
- **Line 463**: `messageNavMode` - Flag enabling keyboard navigation mode
- **Line 464**: `focusedMessageIdx` - Tracks which message is currently highlighted
- **Line 11160-11180**: Updated viewport rendering to use accurate line positions
- **Lines 6640-6790**: Keyboard event handling for scrolling

#### 2. `/internal/chat/chat_state.go`
**Navigation and focus management:**

- **Line 976**: `updateFocusedMessageFromScroll()` - Updates highlight based on scroll position
- **Line 1029**: `findNextAssistantMessage()` - Jump to next assistant message
- **Line 1043**: `findPreviousAssistantMessage()` - Jump to previous assistant message
- **Line 1057**: `scrollToFocusedMessage()` - Scroll viewport to show focused message

#### 3. `/internal/chat/messagelist.go`
**Message list viewport implementation:**

- **Line 112**: `MessageList` struct - Custom viewport with scrolling support
- **Line 265**: `ScrollDown()` - Scroll down by N lines
- **Line 274**: `ScrollUp()` - Scroll up by N lines

## Problem Identified

### Original Issue
The message line position tracking (lines 11119-11129 in app.go) was using an **incorrect heuristic**:
```go
// OLD INCORRECT CODE:
for i := range a.messages {
    messageStartLine := lineCount
    if i > 0 {
        lineCount++ // spacing line
    }
    messageEndLine := lineCount
    a.messageLinePositions = append(..., MessageLinePosition{
        MessageIdx: i,
        StartLine:  messageStartLine,
        EndLine:    messageEndLine,
    })
    lineCount++  // Only increments by 1 line per message!
}
```

This assumed each message was only **1 line long**, which was completely inaccurate for:
- Multi-line user messages
- Formatted assistant responses (markdown, code blocks)
- Tool calls with parameters
- Tool results
- Thinking sections

### Impact
- Message highlighting didn't follow scroll position correctly
- Keyboard navigation (`Tab` + arrows) didn't work properly
- Messages appeared "off" when scrolling through chat history

## Solution Implemented

### 1. New Function: `renderMessageListWithPositions()`

Created at **line 11033** in `app.go`, this function:
- Renders messages **with accurate line counting**
- Tracks the **exact start and end line positions** for each message
- Returns **both formatted lines AND position data**

**Key Features:**
```go
func (a *App) renderMessageListWithPositions(messages []Message, width int) ([]string, []MessageLinePosition) {
    var msgLines []string
    var positions []MessageLinePosition
    currentLine := 0
    
    for i, msg := range messages {
        messageStartLine := currentLine
        
        // [Render message content with line counting]
        // Every time we append a line: msgLines = append(msgLines, line); currentLine++
        
        // Record actual start/end positions
        positions = append(positions, MessageLinePosition{
            MessageIdx: i,
            StartLine:  messageStartLine,
            EndLine:    currentLine - 1,  // Accurate end position
        })
    }
    
    return msgLines, positions
}
```

### 2. Updated Viewport Rendering (line 11160)

Modified `updateViewportContent()` to use the new accurate tracking:
```go
// Legacy rendering - get accurate line positions from renderMessageListWithPositions
msgLines, accuratePositions := a.renderMessageListWithPositions(a.messages, a.msgViewport.Width)
a.messageLinePositions = accuratePositions
```

### 3. Highlighting Logic (Existing)

Message highlighting is implemented in `renderMessageList()` at **line 10166**:
```go
// Check if this message is focused in navigation mode
isFocused := a.messageNavMode && i == a.focusedMessageIdx

// Apply background highlight if focused
if isFocused {
    highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight))
    for i, line := range renderedLines {
        renderedLines[i] = highlightStyle.Render(line)
    }
}
```

The theme background color (`th.BGLight`) is used to highlight focused messages.

## Keyboard Navigation Features

### Available Shortcuts

| Key(s) | Action | Description |
|--------|--------|-------------|
| `Tab` | Toggle navigation mode | Enable/disable line-by-line scrolling |
| `↑` | Scroll up (in nav mode) | Move up line-by-line with message tracking |
| `↓` | Scroll down (in nav mode) | Move down line-by-line with message tracking |
| `Esc` | Exit nav mode | Disable navigation mode |

### Navigation Mode Behavior

1. **Press `Tab`** to enable message navigation mode
2. **Arrow keys** scroll line-by-line, and:
   - Message highlighting **automatically follows** scroll position
   - The message with most viewport overlap gets highlighted
3. **Press `Tab` again** or `Esc` to exit

### Algorithm: updateFocusedMessageFromScroll()

Located at **line 976** in `chat_state.go`:

```go
func (a *App) updateFocusedMessageFromScroll() {
    // Calculate the center line of the viewport
    currentOffset := a.msgViewport.YOffset
    viewportHeight := a.msgViewport.Height
    centerLine := currentOffset + (viewportHeight / 2)
    
    // Find which message has the most overlap with viewport
    bestMatch := a.focusedMessageIdx  // Keep current if no better match
    bestOverlap := 0
    
    for _, pos := range a.messageLinePositions {
        // Calculate overlap between message and viewport
        overlapStart := max(pos.StartLine, visibleTop)
        overlapEnd := min(pos.EndLine, visibleBottom)
        overlap := overlapEnd - overlapStart
        
        if overlap > bestOverlap {
            bestOverlap = overlap
            bestMatch = pos.MessageIdx
        }
    }
    
    // Update focused message if changed
    if bestMatch != a.focusedMessageIdx {
        a.focusedMessageIdx = bestMatch
    }
}
```

This algorithm:
- Calculates which message is **most visible** in the viewport
- Prefers messages containing the **center line** of the viewport
- Updates `focusedMessageIdx` to trigger re-rendering with highlight

## Message Types Handled

The `renderMessageListWithPositions()` function accurately tracks lines for:

### User Messages
- Regular text messages (multi-line wrapping)
- Bash commands (with `!` prefix)
- File attachments with size information

### Assistant Messages
- **Bash Results**: Multi-line terminal output
- **Markdown Content**: Formatted text, code blocks
- **Thinking Sections**: Collapsible reasoning text
- **Tool Calls**: Function name and parameters
- **Tool Results**: Return values from tools
- **Sub-agent Activity**: Nested agent work

Each type contributes the correct number of lines to the position tracking.

## Examples

### Before Fix
```
Messages: 3 (user: "Hello", assistant: long response, user: "Thanks")
Lines tracked: [0-0], [1-1], [2-2]  // WRONG! Each message marked as 1 line
```

### After Fix
```
Messages: 3 (user: "Hello", assistant: long response, user: "Thanks")
Lines tracked: [0-0], [1-25], [26-27]  // CORRECT! Accurate line counts
```

## Testing

### Manual Testing Steps

1. **Start chat application**
2. **Create a conversation** with multiple messages of varying lengths
3. **Press `Tab`** to enter navigation mode
4. **Use ↑/↓ arrows** to scroll through messages
5. **Verify** that message highlighting follows scroll position smoothly
6. **Test with different message types:** code blocks, tool results, etc.

### Expected Behavior

- ✅ Message highlighting changes smoothly as you scroll
- ✅ Long messages (10+ lines) are tracked correctly
- ✅ Jumping to top/bottom shows correct highlight
- ✅ Multi-line code blocks don't break positioning
- ✅ Empty messages or messages with only attachments work correctly

### Debug Logging

Enable debug logs to see line position updates:
```go
logDebug("[updateFocusedMessageFromScroll] Focus changed to message %d (center line: %d)", bestMatch, centerLine)
```

## Performance Considerations

### Optimization: Crush Technique (line 201 in app.go)

The application uses cached rendering:
```go
// Per-message render cache for high-performance scrolling (Crush technique)
messageCache map[int][]string  // Cache rendered lines per message
```

- Messages are rendered **once and cached**
- Line position tracking is calculated only when content changes
- Scrolling uses viewport offset without re-rendering

### Memory Usage

- `messageLinePositions` stores ~12 bytes per message (3 ints)
- For 1000 messages: ~12KB
- Negligible memory overhead

## Future Enhancements

Potential improvements:

1. **Page Up/Down Keys**: Add support for jumping multiple pages at once
2. **Home/End Keys**: Jump to first/last message directly
3. **Smooth Scrolling**: Add animation for scroll transitions
4. **Focus Mode Persist**: Remember navigation mode across conversations
5. **Keyboard Shortcuts**: Add `n`/`p` for next/previous message jump
6. **Selection Mode**: Allow selecting text from highlighted messages
7. **Visual Indicators**: Show line numbers in navigation mode
8. **Mouse Interaction**: Click on message to jump to it

## Files Modified

1. **`/internal/chat/app.go`**
   - Added `renderMessageListWithPositions()` function (line 11033)
   - Updated `updateViewportContent()` to use accurate tracking (line 11160)

2. **`/internal/chat/chat_state.go`**
   - No changes (existing functionality used as-is)

3. **`/internal/chat/messagelist.go`**
   - No changes (existing scrolling methods used)

## Related Code

- **Line 251**: `MessageLinePosition` struct definition
- **Line 465**: `messageLinePositions` array in App struct
- **Line 6640-6790**: Keyboard event handlers
- **Line 10138-11030**: Original `renderMessageList()` function
- **Line 976-1074**: Navigation and focus management functions

## Troubleshooting

### Message highlighting not updating
- Check if `messageNavMode` is `true` (press `Tab`)
- Verify `messageLinePositions` has correct data
- Check viewport offset is within bounds

### Scrolling feels "jumpy"
- Ensure line position tracking is accurate (check console logs)
- Large messages may cause visual jumps - this is expected
- Try toggling nav mode off and on (press `Esc` then `Tab`)

### Performance issues with many messages
- Cached rendering should prevent slowdowns
- Check `messageCache` is being used properly
- Consider reducing conversation history size

## Conclusion

The fix ensures that message highlighting provides smooth, accurate feedback during keyboard navigation. Accurate line position tracking is essential for:
- Proper message highlighting during scrolling
- Keyboard navigation with visual feedback
- Future features like message selection, jumping, etc.

The implementation uses efficient caching and adds negligible performance overhead while significantly improving user experience.
