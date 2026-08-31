# Terminal Image Rendering Research for Bubble Tea TUI

## Overview

This document summarizes research on rendering images in terminal-based TUI applications built with Bubble Tea (Go).

## Terminal Image Protocols

There are several protocols for displaying images in terminals:

### 1. Sixel Graphics
- **Standard**: DEC VT300 series standard from 1980s
- **Support**: xterm, mlterm, foot, mintty, Windows Terminal (preview)
- **Pros**: Wide support, standardized
- **Cons**: Limited color palette (typically 256 colors)

### 2. Kitty Graphics Protocol
- **Standard**: Kitty terminal's native protocol
- **Support**: Kitty terminal, WezTerm, Konsole (partial)
- **Pros**: High quality, supports animations, PNG/JPEG direct display
- **Cons**: Limited terminal support

### 3. iTerm2 Inline Images Protocol
- **Standard**: iTerm2's proprietary protocol
- **Support**: iTerm2, WezTerm, mintty
- **Pros**: Easy to use, base64 encoded
- **Cons**: macOS-centric

### 4. Unicode Block Characters (Braille/Half-blocks)
- **Standard**: Uses Unicode characters to approximate images
- **Support**: Any Unicode-capable terminal
- **Pros**: Universal support
- **Cons**: Lower resolution, ASCII-art style

## Go Libraries for Terminal Images

### Primary Options

#### 1. pixterm (github.com/eliukblau/pixterm)
- **Stars**: ~1000
- **Description**: Draw images in ANSI terminal with true color
- **Features**:
  - Converts images to ANSI escape sequences
  - Uses Unicode half-block characters (U+2580, U+2584)
  - True color (24-bit) support
  - Supports PNG, JPEG, GIF
- **Usage**:
```go
import "github.com/eliukblau/pixterm/v2/pkg/ansimage"

// Load and display image
img, _ := ansimage.NewFromFile(filename, 80, 24, ansimage.ScaleModeResize, ansimage.NoDithering)
fmt.Print(img.Render())
```

#### 2. imgcat (github.com/danielgatis/imgcat)
- **Stars**: ~360
- **Description**: Display images and gifs in terminal
- **Features**:
  - Supports iTerm2 protocol
  - Supports Kitty protocol
  - GIF animation support
- **Usage**:
```go
import "github.com/danielgatis/imgcat/pkg/imgcat"

// Display image using detected protocol
imgcat.CatFile(filename, os.Stdout)
```

#### 3. chafa-go / go-chafa (bindings to libchafa)
- **Description**: Go bindings for libchafa
- **Features**:
  - High-quality character-based image rendering
  - Multiple output formats (Sixel, Kitty, symbols)
- **Note**: Requires libchafa C library

### Bubble Tea Integration Considerations

Bubble Tea uses a string-based View() method for rendering. To integrate images:

1. **String-based rendering**: Convert image to ANSI escape sequences (pixterm approach)
```go
func (m model) View() string {
    // Render image as ANSI string
    img, _ := ansimage.NewFromFile(m.imagePath, m.width, m.height, ansimage.ScaleModeResize, ansimage.NoDithering)
    return img.Render()
}
```

2. **Direct terminal output**: Use tea.Println or raw escape sequences for protocol-specific output
```go
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    // For Kitty/iTerm2 protocols, may need to output directly
    return m, tea.Printf("%s", kittyImageSequence)
}
```

### Bubble Tea v2 Considerations

Bubble Tea v2 (beta) introduces:
- **Layer interface**: `Draw(s Screen, r Rectangle)` for direct screen manipulation
- **Screen type**: Canvas for rendering components
- **Rectangle type**: Bounds for component rendering

These new APIs may enable more sophisticated image rendering but are still in development.

## Recommended Approach for SwarmCode TUI

### Phase 1: Unicode Half-Block Rendering (pixterm)
- Use `github.com/eliukblau/pixterm/v2` for universal compatibility
- Convert images to ANSI strings using half-block characters
- Works in any true-color terminal

### Phase 2: Protocol Detection and Enhancement
- Detect terminal capabilities (Sixel, Kitty, iTerm2)
- Use protocol-specific rendering when available
- Fallback to half-block rendering

### Implementation Steps

1. **Add pixterm dependency**:
```bash
go get github.com/eliukblau/pixterm/v2
```

2. **Create image component**:
```go
package components

import (
    "github.com/eliukblau/pixterm/v2/pkg/ansimage"
)

type ImageModel struct {
    path   string
    width  int
    height int
    img    *ansimage.ANSImage
}

func NewImageModel(path string, w, h int) (*ImageModel, error) {
    img, err := ansimage.NewFromFile(path, w, h, 
        ansimage.ScaleModeResize, 
        ansimage.NoDithering)
    if err != nil {
        return nil, err
    }
    return &ImageModel{path: path, width: w, height: h, img: img}, nil
}

func (m *ImageModel) View() string {
    if m.img == nil {
        return "[Image loading error]"
    }
    return m.img.Render()
}
```

3. **Terminal capability detection** (future enhancement):
```go
package terminal

import (
    "os"
    "strings"
)

type ImageProtocol int

const (
    ProtocolHalfBlock ImageProtocol = iota
    ProtocolSixel
    ProtocolKitty
    ProtocolITerm2
)

func DetectImageProtocol() ImageProtocol {
    term := os.Getenv("TERM")
    termProgram := os.Getenv("TERM_PROGRAM")
    
    // Kitty detection
    if os.Getenv("KITTY_WINDOW_ID") != "" {
        return ProtocolKitty
    }
    
    // iTerm2 detection
    if termProgram == "iTerm.app" {
        return ProtocolITerm2
    }
    
    // Sixel detection (check for xterm with sixel support)
    if strings.Contains(term, "xterm") {
        // Could query terminal for sixel support via DA1
        // For now, default to half-block
    }
    
    return ProtocolHalfBlock
}
```

## Charmbracelet Crush Analysis (Reference Implementation)

**Source**: https://github.com/charmbracelet/crush

Crush is Charmbracelet's official AI coding assistant TUI. It provides an excellent reference implementation for image rendering in Bubble Tea.

### Key Files

```
/internal/tui/components/image/
├── image.go    # Bubble Tea model for image display
└── load.go     # Image loading and rendering logic
```

### Dependencies Used by Crush

```go
// From go.mod
github.com/disintegration/imageorient v0.0.0-20180920195336-8147d86e83ec  // EXIF orientation
github.com/disintegration/imaging v1.6.2                                   // Image processing
github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646                  // Lanczos3 resize
github.com/lucasb-eyer/go-colorful v1.3.0                                  // Color conversion
github.com/muesli/termenv v0.16.0                                          // Terminal colors
github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c                // SVG parsing
github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef              // SVG rasterization
```

### Core Algorithm: `imageToString()`

```go
// From /internal/tui/components/image/load.go
func imageToString(width, height uint, img image.Image) (string, error) {
    // Resize image - height*2 because each char = 2 vertical pixels
    img = resize.Thumbnail(width, height*2-4, img, resize.Lanczos3)
    b := img.Bounds()
    w := b.Max.X
    h := b.Max.Y
    p := termenv.ColorProfile()
    str := strings.Builder{}
    
    // Process 2 rows at a time (top pixel = foreground, bottom = background)
    for y := 0; y < h; y += 2 {
        // Padding for centering
        for x := w; x < int(width); x = x + 2 {
            str.WriteString(" ")
        }
        // Render each column
        for x := range w {
            c1, _ := colorful.MakeColor(img.At(x, y))      // Top pixel
            color1 := p.Color(c1.Hex())
            c2, _ := colorful.MakeColor(img.At(x, y+1))    // Bottom pixel
            color2 := p.Color(c2.Hex())
            
            // Use UPPER HALF BLOCK with fg=top, bg=bottom
            str.WriteString(termenv.String("▀").
                Foreground(color1).
                Background(color2).
                String())
        }
        str.WriteString("\n")
    }
    return str.String(), nil
}
```

### Bubble Tea Model Pattern

```go
// From /internal/tui/components/image/image.go
type Model struct {
    url    string
    image  string   // Pre-rendered ANSI string
    width  uint
    height uint
    err    error
}

func New(width, height uint, url string) Model {
    return Model{width: width, height: height, url: url}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
    switch msg := msg.(type) {
    case errMsg:
        m.err = msg
    case redrawMsg:
        m.width, m.height, m.url = msg.width, msg.height, msg.url
        return m, loadURL(m.url)  // Async load
    case loadMsg:
        return handleLoadMsg(m, msg)  // Convert to ANSI string
    }
    return m, nil
}

func (m Model) View() string {
    if m.err != nil {
        return fmt.Sprintf("couldn't load image(s): %v", m.err)
    }
    return m.image  // Return pre-rendered ANSI string
}

// Trigger image reload with new dimensions/URL
func (m Model) Redraw(width, height uint, url string) tea.Cmd {
    return func() tea.Msg {
        return redrawMsg{width: width, height: height, url: url}
    }
}
```

### Features Supported

1. **File Formats**: PNG, JPEG, SVG (via rasterization)
2. **Sources**: Local files, HTTP URLs, Base64-encoded data
3. **SVG Support**: Uses oksvg + rasterx to convert SVG → PNG → ANSI
4. **EXIF Orientation**: Automatic handling via imageorient
5. **Async Loading**: Non-blocking image loading via tea.Cmd
6. **Dynamic Resizing**: Redraw() method for responsive layouts

### Base64 Image Support

```go
// From /internal/tui/components/image/load.go
func ImageFromBase64(width, height uint, data, mediaType string) (string, error) {
    decoded, err := base64.StdEncoding.DecodeString(data)
    if err != nil {
        return "", err
    }
    r := bytes.NewReader(decoded)
    
    if strings.Contains(mediaType, "svg") {
        return svgToImage(width, height, r)
    }
    
    img, _, err := imageorient.Decode(r)
    if err != nil {
        return "", err
    }
    return imageToString(width, height, img)
}
```

### Integration Example (File Picker)

```go
// From /internal/tui/components/dialogs/filepicker/filepicker.go
type model struct {
    filePicker  filepicker.Model
    image       image.Model  // Image preview component
    // ...
}

func (m *model) Update(msg tea.Msg) (util.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        // Handle resize
    case tea.KeyPressMsg:
        // When file selection changes, trigger image reload
        w, h := m.imagePreviewSize()
        cmd = m.image.Redraw(uint(w-2), uint(h-2), m.currentImage())
    }
    m.image, cmd = m.image.Update(msg)
    return m, cmd
}

func (m *model) View() string {
    // Include image preview in layout
    return lipgloss.JoinVertical(lipgloss.Left,
        m.imagePreview(),       // Image component view
        m.filePicker.View(),
        m.help.View(m.keyMap),
    )
}

func (m *model) imagePreview() string {
    if m.currentImage() == "" {
        return emptyPreviewStyle.Render()
    }
    return m.imagePreviewStyle().Render(m.image.View())
}
```

## References

- [pixterm](https://github.com/eliukblau/pixterm) - ANSI image rendering
- [imgcat](https://github.com/danielgatis/imgcat) - Multi-protocol image display
- [Kitty Graphics Protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/)
- [iTerm2 Inline Images](https://iterm2.com/documentation-images.html)
- [Sixel Graphics](https://en.wikipedia.org/wiki/Sixel)
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI framework
- [charmbracelet/x](https://github.com/charmbracelet/x) - Extended utilities (cellbuf, vt)
- **[charmbracelet/crush](https://github.com/charmbracelet/crush)** - Reference implementation

## Next Steps

1. [ ] Add image dependencies to go.mod:
   - `github.com/nfnt/resize`
   - `github.com/disintegration/imageorient`
   - `github.com/lucasb-eyer/go-colorful`
   - `github.com/muesli/termenv`
   - `github.com/srwiley/oksvg` (for SVG support)
   - `github.com/srwiley/rasterx` (for SVG support)
2. [ ] Create ImageModel component based on Crush's implementation
3. [ ] Integrate with existing Read tool output display
4. [ ] Add Base64 image rendering for LLM image responses
5. [ ] Add terminal capability detection (future: Kitty/iTerm2)
6. [ ] Test across different terminal emulators
