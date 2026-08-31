# Chat UI Module

A modular, testable chat UI implementation for the Swarm TUI.

## Architecture Overview

The `chatui` package follows a clean architecture pattern with clear separation of concerns:

```
internal/chatui/
├── chatui.go          # Public API and Panel wrapper
├── doc.go             # Package documentation
├── types/             # Core domain types
│   ├── message.go     # Message, MessageBlock, ToolCallDisplay
│   ├── state.go       # PanelState, ViewportState, MultiPanelState
│   ├── events.go      # Bubbletea message types
│   └── conversation.go # Conversation metadata
├── theme/             # Styling concerns
│   ├── theme.go       # Theme interface, DefaultTheme, LightTheme
│   ├── styles.go      # Pre-allocated StyleSet
│   └── icons.go       # Tool icons, file icons, border chars
├── renderer/          # Rendering logic
│   ├── message.go     # MessageRenderer (orchestrator)
│   ├── tool.go        # ToolRenderer
│   ├── thinking.go    # ThinkingRenderer
│   └── markdown.go    # MarkdownRenderer
├── viewport/          # Viewport management
│   ├── viewport.go    # Scrolling, selection, view
│   └── cache.go       # RenderCache, MessageCache
├── layout/            # Layout calculations
│   ├── layout.go      # Calculator, Rect
│   └── split.go       # SplitRenderer for multi-panel
├── state/             # State management
│   └── panel.go       # Panel (Bubbletea Model)
└── components/        # Reusable UI components
    ├── input.go       # Input with history
    └── statusbar.go   # Status bar
```

## Usage

### Basic Panel

```go
import "github.com/Swarm-Code/mono/swarmos-tui/internal/chatui"

// Create a new panel
panel := chatui.NewPanel(80, 24,
    chatui.WithShowThinking(true),
    chatui.WithShowFullToolOutput(false),
)

// Use as Bubbletea Model
p := tea.NewProgram(panel)
p.Run()
```

### Adding Messages

```go
// Add a user message
panel.SetMessages([]chatui.Message{
    {
        Role:      "user",
        Content:   "Hello!",
        Timestamp: time.Now(),
    },
})

// Add an assistant message with tool calls
panel.AppendMessage(chatui.Message{
    Role: "assistant",
    OrderedBlocks: []chatui.MessageBlock{
        {Type: chatui.BlockContent, Content: "Let me check that file."},
        {Type: chatui.BlockToolCall, ToolCall: &chatui.ToolCallDisplay{
            ID:   "1",
            Name: "Read",
            Parameters: map[string]interface{}{"file_path": "/path/to/file.go"},
        }},
    },
})
```

### Streaming

```go
// Start streaming
cmd := panel.StreamChunk("Hello", &chatui.MessageBlock{
    Type:    chatui.BlockContent,
    Content: "Hello",
})

// End streaming
cmd := panel.StreamDone(time.Second * 5)
```

### Loading Conversations

```go
cmd := panel.LoadMessages("conv-123", messages)
```

## Feature Flag

Enable the new chat UI with the `SWARM_USE_CHATUI=1` environment variable.

## Key Design Decisions

### Separation of Concerns

- **types/**: Pure data types with no dependencies
- **theme/**: Styling only, no rendering logic
- **renderer/**: State → strings transformation
- **viewport/**: Scrolling, selection, caching
- **state/**: Bubbletea Model implementation

### Performance Optimizations

1. **Pre-allocated StyleSet**: Styles created once, reused every frame
2. **Render caching**: Hash-based invalidation
3. **Per-message caching**: Only re-render changed messages
4. **Viewport culling**: Only render visible lines

### Multi-Panel Support

The architecture supports multi-panel layouts:

```go
// Layout calculator for split panels
calc := layout.NewCalculator(width, height)
rects := calc.CalculateSplit(multiPanelState)

// Render split panels
renderer := layout.NewSplitRenderer(theme)
output := renderer.RenderSplit(panelViews, rects, state)
```

## Testing

```bash
go test ./internal/chatui/...
```

## Migration Path

1. **Feature flag**: Enable with `SWARM_USE_CHATUI=1`
2. **Parallel testing**: Run both implementations side-by-side
3. **Gradual rollout**: Make default when stable
4. **Deprecation**: Remove old code after verification
