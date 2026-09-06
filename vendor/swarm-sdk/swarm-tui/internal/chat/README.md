# Chat UI Components - Refactored Architecture

## Overview

This package provides a modern, flicker-free chat UI built with bubbletea v2 and lipgloss v2. The architecture is inspired by the bubbles library but adapted for bubbletea v2's API changes.

## Key Components

### 1. Viewport (`viewport.go`)
Scrollable content area with mouse wheel support.

```go
vp := NewViewport(width, height)
vp.SetContent("Your content here")
vp.MouseWheelEnabled = true
vp.MouseWheelDelta = 3

// In Update:
cmd := vp.Update(msg)

// In View:
content := vp.View()
// Or with scrollbar:
content := vp.ViewWithScrollbar(theme)
```

**Features:**
- Mouse wheel scrolling (works with bubbletea v2)
- Keyboard navigation (pgup/pgdown, ctrl+u/d, j/k)
- Scroll position tracking (AtTop, AtBottom, ScrollPercent)
- Horizontal scrolling support
- Customizable style

### 2. TextArea (`input.go`)
Multi-line text input with cursor and editing.

```go
ta := NewTextArea()
ta.Placeholder = "Type a message..."
ta.Prompt = "› "
ta.Focus()

// In Update:
cmd := ta.Update(msg)

// In View:
content := ta.View()

// Get/Set value:
text := ta.Value()
ta.SetValue("new text")
ta.Reset()
```

**Features:**
- Multi-line editing
- Cursor movement (arrows, home/end, word navigation)
- Text manipulation (backspace, delete, word delete)
- Configurable max height and char limit
- Focus/blur states with visual feedback

### 3. SideMenu (`sidemenu.go`)
Collapsible navigation panel with sections.

```go
menu := NewSideMenu(width, height, theme)
menu.Title = "Chats"
menu.SetSections([]MenuSection{
    {Title: "Recent", Items: []MenuItem{
        {ID: "1", Label: "Chat 1", Icon: "●"},
        {ID: "2", Label: "Chat 2", Icon: "○"},
    }},
})

// Toggle collapse:
menu.Toggle()

// Get selected:
id := menu.SelectedID()
```

**Features:**
- Collapsible (icons-only mode)
- Sections with titles
- Item icons and badges
- Keyboard navigation (j/k, enter)
- Mouse click support
- Mouse wheel scrolling

### 4. Progress (`progress.go`)
Animated progress bar.

```go
prog := NewProgress(width, theme)
prog.SetPercent(0.5) // 50%
prog.ShowPercentage = true

// Determinate:
content := prog.View()

// Indeterminate (animated):
content := prog.ViewIndeterminate(frameCount)
```

**Features:**
- Solid or gradient fill
- Percentage display
- Indeterminate animation mode

## Animation Manager (`animation.go`)

Reusable animation state machine for UI elements (tracks + sequences + event triggers).

```go
manager := NewAnimationManager()
track := NewAnimationTrack("widget", []AnimationSequence{
  {Name: "idle", Length: 1},
  {Name: "pulse", Length: 12, Loop: true},
}, "idle")
track.MapEvent(AnimationEventWake, "pulse")
manager.AddTrack(track)

manager.Trigger("widget", AnimationEventWake, nil)
manager.Tick()
```

## Main Application (`chatapp.go`)

The `ChatApp` combines all components into a complete chat interface.

```go
// Create with defaults:
app := NewChatApp()

// Or with custom config:
config := DefaultChatAppConfig()
config.SideMenuWidth = 30
config.ShowScrollbar = true
app := NewChatAppWithConfig(config)

// Run with bubbletea:
p := tea.NewProgram(app)
p.Run()
```

### Focus Management

The app manages focus between three areas:
- `FocusSideMenu` - Navigate conversations
- `FocusMessages` - Scroll through chat history
- `FocusInput` - Type messages

Use `Tab` to cycle focus, `Ctrl+B` to toggle side menu.

### Mouse Support

Mouse is enabled via `tea.MouseModeCellMotion`:
- Click to focus areas
- Scroll wheel in viewport and side menu
- Click menu items to select

## Anti-Flicker Techniques

1. **Stable dimensions**: Components maintain fixed sizes to avoid layout shifts
2. **Content caching**: Only re-render when content actually changes
3. **Alt screen**: Uses alternate screen buffer (`v.AltScreen = true`)
4. **Efficient updates**: Components return `nil` cmd when no state change

## Bubbletea v2 Differences

Key API changes from v1:
- `View() string` → `View() tea.View` (struct with SetContent, AltScreen, MouseMode)
- Mouse events split into `tea.MouseMsg`, `tea.MouseClickMsg`, `tea.MouseWheelMsg`
- Use `msg.String()` for wheel direction ("wheelup"/"wheeldown")
- `tea.Mouse(msg)` to convert MouseClickMsg for coordinates

## Usage Example

```bash
# Run with new refactored UI:
./swarmos --new

# Run legacy UI:
./swarmos
```

## File Structure

```
internal/chat/
├── app.go          # Legacy app (original)
├── chatapp.go      # NEW: Refactored main application
├── viewport.go     # NEW: Scrollable viewport component
├── input.go        # NEW: TextArea component
├── sidemenu.go     # NEW: Side menu component  
├── progress.go     # NEW: Progress bar component
├── components.go   # Legacy UI primitives
├── messagelist.go  # Legacy message list
└── debug.go        # Debug logging utilities
```

## Styling

All components use the `Theme` struct from `components.go`:

```go
type Theme struct {
    Primary    string  // Main accent color
    PrimaryDim string
    Secondary  string
    Accent     string
    Success    string
    Warning    string
    Error      string
    Info       string
    BG         string  // Background
    BGLight    string
    BGLighter  string
    Border     string
    Text       string
    TextDim    string
    TextMuted  string
}
```

Use `DefaultTheme` or create custom themes.
