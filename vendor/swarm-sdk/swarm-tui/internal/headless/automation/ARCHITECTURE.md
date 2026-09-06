# Headless TUI Automation System

A Playwright-like automation system for testing and debugging the SwarmOS TUI.

## Overview

This system enables:
- Running the TUI headlessly without a real terminal (like tmux but deeper)
- High-performance networking via gnet for real-time interaction
- **Deep state introspection** - access actual Go model state, not just rendered output
- Sending commands (keys, resize, navigation)
- Querying rendered state and component boundaries
- Source code tracing (map rendered lines to Go files)
- Screen configuration definitions for automated testing

## Two Modes of Operation

### 1. Direct Mode (In-Process)
Use the `Driver` directly in Go tests for fast, synchronous automation:

```go
driver := automation.NewDriver(panel, automation.WithSize(80, 24))
driver.Start()
defer driver.Stop()

driver.SendKey("j")
frame := driver.GetFrame()
```

### 2. Server Mode (Remote/IPC)
Run a gnet server that clients can connect to over Unix sockets or TCP:

```go
// Server
srv := server.New(panel, server.Config{
    Address: "unix:///tmp/tui.sock",
    Width:   80,
    Height:  24,
})
srv.Start(ctx)

// Client
cli := client.New("unix:///tmp/tui.sock")
cli.Connect()
cli.SendKey("j")
state, _ := cli.GetState()  // Deep introspection!
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          AUTOMATION SYSTEM                               │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                     Virtual Terminal Driver                      │    │
│  │                                                                  │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │    │
│  │  │   Options    │  │   Lifecycle  │  │  Operations  │          │    │
│  │  │  - Trace     │  │  - Start()   │  │  - SendKey   │          │    │
│  │  │  - Config    │  │  - Stop()    │  │  - Navigate  │          │    │
│  │  │  - Size      │  │  - Resize()  │  │  - Query     │          │    │
│  │  └──────────────┘  └──────────────┘  └──────────────┘          │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                                                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌────────────┐  │
│  │   Capture    │  │   Inject     │  │    Trace     │  │   Query    │  │
│  │              │  │              │  │              │  │            │  │
│  │ - Frame      │  │ - Events     │  │ - Stack      │  │ - Selector │  │
│  │ - Parser     │  │ - Keyboard   │  │ - Location   │  │ - Matcher  │  │
│  │ - Buffer     │  │ - Navigation │  │ - Instrument │  │ - Engine   │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  └────────────┘  │
│                                                                          │
│  ┌──────────────┐  ┌──────────────────────────────────────────────┐    │
│  │   Config     │  │              Testing Helpers                  │    │
│  │              │  │                                               │    │
│  │ - Screen     │  │  - TestDriver                                │    │
│  │ - Region     │  │  - Assertions (AssertText, AssertScreen)     │    │
│  │ - Loader     │  │  - Recorder (Record/Replay)                  │    │
│  │ - Validator  │  │  - Screenshot                                │    │
│  └──────────────┘  └──────────────────────────────────────────────┘    │
│                                                                          │
└───────────────────────────────┬──────────────────────────────────────────┘
                                │
                                ▼
        ┌────────────────────────────────────────────┐
        │          Bubbletea Program                  │
        │                                             │
        │  tea.NewProgram(model,                     │
        │    tea.WithInput(injector.reader),         │
        │    tea.WithOutput(capture.writer),         │
        │    tea.WithoutRenderer())                  │
        └────────────────────────────────────────────┘
                                │
                                ▼
        ┌────────────────────────────────────────────┐
        │             App/Panel Model                 │
        │                                             │
        │  - Update(msg) → (Model, Cmd)              │
        │  - View() → string (intercepted)           │
        └────────────────────────────────────────────┘
```

## Package Structure

```
internal/headless/automation/
├── driver.go           # Main driver implementation
├── capture/
│   └── frame.go        # Frame, Cell types, ANSI parsing
├── inject/
│   └── events.go       # Event injection (keys, mouse)
├── state/
│   └── inspector.go    # Deep state introspection interfaces
├── protocol/
│   └── protocol.go     # Binary wire protocol
├── server/
│   └── server.go       # gnet high-performance server
├── client/
│   └── client.go       # Client library
├── config/             # (planned)
│   ├── screen.go       # Screen configuration types
│   └── loader.go       # YAML config loader
└── query/              # (planned)
    └── engine.go       # Query engine

configs/
└── screens.yaml        # Screen definitions
```

## Deep State Introspection

Unlike tmux which only shows rendered output, this system provides **deep access to Go model state**.

Models implement inspector interfaces to expose their internals:

```go
// inspector.go - interfaces that models can implement

type Inspector interface {
    GetScreen() string
}

type MessageInspector interface {
    GetMessages() []MessageState
    GetMessageCount() int
}

type InputInspector interface {
    GetInputText() string
    GetCursorPosition() int
    IsInputFocused() bool
}

type ScrollInspector interface {
    GetScrollOffset() int
    GetScrollMax() int
    GetVisibleRange() (first, last int)
}

type FocusInspector interface {
    GetFocusedComponent() string
    IsFocused(componentID string) bool
}

type ModalInspector interface {
    IsModalOpen() bool
    GetModalID() string
    GetModalData() interface{}
}
```

### Client Usage

```go
cli := client.New("unix:///tmp/tui.sock")
cli.Connect()

// Get full state - actual Go structs, not rendered text!
state, _ := cli.GetState()
fmt.Println(state.Screen)           // "chat"
fmt.Println(state.Messages[0].Role) // "user"
fmt.Println(state.Input.Text)       // Current input text
fmt.Println(state.Scroll.Offset)    // Scroll position

// Or get individual values
messages, _ := cli.GetMessages()
inputText, _ := cli.GetInputText()
```

### Wire Protocol

Fast binary protocol with JSON payloads:

```
[4 bytes: length][1 byte: type][JSON payload...]

Message types:
- 0x01-0x0F: Commands (SendKey, Resize, Click, etc.)
- 0x10-0x1F: Queries (GetFrame, GetState, GetField)
- 0x20-0x2F: Subscriptions (Subscribe, Unsubscribe)
- 0x80-0x8F: Responses (OK, Error, Frame, State)
- 0x90-0x9F: Pushed events (FrameUpdate, StateChange)
```

## Core Components

### 1. Virtual Terminal Driver

The main entry point for automation.

```go
type Driver struct {
    program   *tea.Program
    model     tea.Model
    output    *frameCapture
    input     *eventInjector
    trace     *sourceTracer
    config    *ScreenConfig
}

// Create and start driver
driver := automation.NewDriver(model,
    automation.WithSize(80, 24),
    automation.WithConfig("configs/screens.yaml"),
    automation.WithTrace(true),
)
driver.Start()
defer driver.Stop()

// Send input
driver.SendKey("down")
driver.SendKeys("H", "e", "l", "l", "o")
driver.Resize(120, 40)

// Get current frame
frame := driver.GetFrame()

// Navigate using screen config
driver.Navigate("chat")
driver.WaitForScreen("chat", 5*time.Second)

// Query state
result := driver.Query("#input_box")
```

### 2. Frame Capture

Captures and parses rendered output.

```go
type Frame struct {
    Width      int
    Height     int
    Cells      [][]Cell
    Timestamp  time.Time
    RenderTime time.Duration
}

type Cell struct {
    Rune       rune
    Style      Style
    SourceInfo *SourceLocation
}

type Style struct {
    Foreground Color
    Background Color
    Bold       bool
    Italic     bool
    Underline  bool
}
```

### 3. Screen Configuration

YAML-based screen definitions.

```yaml
screens:
  home:
    id: "home"
    type: "ScreenHome"
    regions:
      - id: "new_chat_button"
        bounds: { x: 0, y: 5, width: 20, height: 3 }
        clickable: true
        navigation_target: "chat"
      - id: "settings_button"
        bounds: { x: 0, y: 11, width: 20, height: 3 }
        clickable: true
    navigation:
      to_chat: ["n"]
      to_settings: ["s"]
      to_workflows: ["w"]

  chat:
    id: "chat"
    type: "ScreenChat"
    regions:
      - id: "message_list"
        bounds: { x: 0, y: 2, width: -38, height: -6 }  # Negative = from edge
        scrollable: true
      - id: "input_box"
        bounds: { x: 2, y: -3, width: -40, height: 3 }
        input: true
      - id: "side_panel"
        bounds: { x: -38, y: 2, width: 38, height: -6 }
        optional: true
    navigation:
      to_home: ["esc"]
      toggle_sidepanel: ["ctrl+b"]
      submit: ["enter"]

  settings:
    id: "settings"
    type: "ScreenSettings"
    regions:
      - id: "settings_list"
        bounds: { x: 0, y: 2, width: 80, height: -4 }
        scrollable: true
    navigation:
      to_home: ["esc"]
```

### 4. Query Engine

CSS-like selectors for state inspection.

```go
// By region ID
result := driver.Query("#input_box")

// By text content
result := driver.Query("text:Hello")

// By bounds
result := driver.Query("bounds:0,0,20,5")

// By attribute
results := driver.QueryAll("[scrollable]")

// QueryResult
type QueryResult struct {
    Region  *RegionDef
    Cells   [][]Cell
    Text    string
    Bounds  Bounds
    Visible bool
}
```

### 5. Source Tracer

Maps rendered output to source code.

```go
// Enable tracing
driver := automation.NewDriver(model, automation.WithTrace(true))

// Get source location for a cell
frame := driver.GetFrame()
loc := frame.GetLocationForCell(5, 23)

// SourceLocation
type SourceLocation struct {
    File      string  // "internal/chat/components.go"
    Line      int     // 1234
    Function  string  // "SimpleInput.View"
    Component string  // "input_box"
}
```

## Usage Examples

### Basic Test

```go
func TestChatInputRendering(t *testing.T) {
    app := chat.NewApp()
    driver := automation.NewTestDriver(t, app)
    driver.Start()
    defer driver.Stop()

    // Navigate to chat
    driver.Navigate("chat")

    // Verify input box
    driver.AssertRegionVisible("#input_box")

    // Type message
    driver.SendKeys("Hello, world!")

    // Check input content
    driver.AssertText("#input_box", "Hello, world!")
}
```

### Navigation Test

```go
func TestScreenNavigation(t *testing.T) {
    driver := automation.NewTestDriver(t, chat.NewApp())
    driver.Start()
    defer driver.Stop()

    // Start at home
    driver.AssertScreen("home")

    // Navigate through screens
    driver.Navigate("settings")
    driver.AssertScreen("settings")

    driver.Navigate("home")
    driver.AssertScreen("home")

    driver.Navigate("chat")
    driver.AssertScreen("chat")
}
```

### Viewport Size Testing

```go
func TestResponsiveLayout(t *testing.T) {
    driver := automation.NewTestDriver(t, chat.NewApp())
    driver.Start()
    defer driver.Stop()

    driver.Navigate("chat")

    // Test at different sizes
    sizes := []struct{ w, h int }{
        {80, 24},   // Minimal
        {120, 40},  // Medium
        {200, 60},  // Large
    }

    for _, size := range sizes {
        driver.Resize(size.w, size.h)

        frame := driver.GetFrame()
        if frame.Height != size.h {
            t.Errorf("Frame height %d != expected %d", frame.Height, size.h)
        }

        // Side panel should appear at width >= 90
        result := driver.Query("#side_panel")
        if size.w >= 90 && !result.Visible {
            t.Errorf("Side panel should be visible at width %d", size.w)
        }
    }
}
```

### Source Tracing

```go
func TestRenderTracing(t *testing.T) {
    driver := automation.NewDriver(
        chat.NewApp(),
        automation.WithTrace(true),
        automation.WithSize(80, 24),
    )
    driver.Start()
    defer driver.Stop()

    driver.Navigate("chat")
    frame := driver.GetFrame()

    // Find where input box is rendered
    inputRegion := driver.Query("#input_box")
    loc := frame.GetLocationForCell(inputRegion.Bounds.X, inputRegion.Bounds.Y)

    t.Logf("Input box rendered at %s:%d in %s",
        loc.File, loc.Line, loc.Function)
}
```

## Build Phases

### Phase 1: Foundation
- Basic driver with Start/Stop
- Frame capture with ANSI parsing
- Event injection (keys, resize)
- Simple test with chatui.Panel

### Phase 2: Configuration
- Screen config types and YAML loader
- Navigation using config paths
- WaitForScreen with detection

### Phase 3: Query Engine
- Selector parsing
- Region matching
- Text extraction

### Phase 4: Source Tracing
- Runtime stack capture
- Source location mapping
- Optional instrumentation

### Phase 5: Testing Helpers
- TestDriver wrapper
- Assertions
- Screenshots

### Phase 6: CI Integration
- Full test suite
- Performance optimization
- Documentation

## Design Principles

1. **Non-invasive**: No changes to existing App/Panel code required
2. **Familiar API**: Playwright-like patterns for ease of use
3. **Testable**: Clean interfaces for unit testing
4. **Performant**: Minimal overhead, cached frames
5. **Traceable**: Source mapping for debugging
