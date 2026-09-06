# Diff View Implementation Plan

## Overview
Implement a clean, well-architected diff view system for displaying Edit, Write, and Patch tool outputs with green/red background highlighting, syntax highlighting, auto-layout switching, and collapse support.

## Requirements
- **Tools**: Edit, Write, and Patch file modification tools
- **Display modes**: Auto-switch between unified and split view based on terminal width
- **Collapse**: Integrates with existing 3-level collapse system (Collapsed/Compact/Full)
- **Syntax highlighting**: Layered on top of diff colors

## Architecture

### Component Diagram
```
internal/chat/diffview/
├── diffview.go          # Main DiffView component (orchestrator)
├── computer.go          # DiffComputer (pure diff logic, no rendering)
├── layout.go            # DiffLayout (unified/split switching)
├── highlighter.go       # SyntaxHighlighter (chroma integration + caching)
├── styler.go            # DiffStyler (apply diff colors)
├── renderer.go          # DiffRenderer (final assembly)
├── types.go             # Shared types (ComputedDiff, DiffLine, etc.)
├── styles.go            # Style definitions (dark theme colors)
└── diffview_test.go     # Unit tests
```

### Data Flow
```
Tool Execution (Edit/Write/Patch)
  └─> ToolCallDisplay.RenderDiff()
       └─> DiffView.New(beforePath, afterPath, before, after)
            ├─> DiffComputer.Compute() → ComputedDiff
            ├─> DiffLayout.ShouldUseSplit(width) → bool
            ├─> SyntaxHighlighter.Highlight(line, bgcolor) → styled string
            ├─> DiffStyler.StyleLine(line, kind) → StyledLine
            └─> DiffRenderer.Render() → []string
```

## Phase 1: Core Types & Diff Computation

### types.go
```go
package diffview

// DiffLineKind represents the type of diff line
type DiffLineKind int

const (
    DiffLineEqual DiffLineKind = iota  // Unchanged line (context)
    DiffLineInsert                     // Added line
    DiffLineDelete                     // Removed line
)

// DiffLine represents a single line in the diff
type DiffLine struct {
    Kind       DiffLineKind
    BeforeNum  int    // Line number in original (0 if insert)
    AfterNum   int    // Line number in new (0 if delete)
    Content    string // The actual line content
}

// DiffHunk represents a group of changes with context
type DiffHunk struct {
    BeforeStart int        // Starting line in original
    AfterStart  int        // Starting line in new
    Lines       []DiffLine // Lines in this hunk
}

// ComputedDiff holds the complete diff result
type ComputedDiff struct {
    BeforePath  string
    AfterPath   string
    Hunks       []DiffHunk
    Additions   int
    Deletions   int
    TotalLines  int
}
```

### computer.go
```go
package diffview

import (
    "strings"
    "github.com/sergi/go-diff/diffmatchpatch"
)

type DiffComputer struct {
    ContextLines int // Default: 3 lines of context
}

func NewDiffComputer() *DiffComputer {
    return &DiffComputer{ContextLines: 3}
}

func (dc *DiffComputer) Compute(beforePath, afterPath, before, after string) *ComputedDiff
```

**Key algorithm**:
1. Split both texts into lines
2. Use `diffmatchpatch.DiffMain()` to compute character-level diffs
3. Convert to line-level diffs
4. Group into hunks with context lines
5. Count additions/deletions

## Phase 2: Layout & Styling

### layout.go
```go
package diffview

type LayoutType int

const (
    LayoutUnified LayoutType = iota
    LayoutSplit
    LayoutAuto // Auto-switch based on width
)

type DiffLayout struct {
    Type           LayoutType
    AutoSplitWidth int // Minimum width for split (default: 120)
}

func (dl *DiffLayout) ShouldUseSplit(width, maxCodeWidth int) bool {
    if dl.Type == LayoutUnified {
        return false
    }
    if dl.Type == LayoutSplit {
        return true
    }
    // Auto: need room for both sides + line numbers + separator
    minRequired := (maxCodeWidth * 2) + 16 // ~16 chars for line nums + separator
    return width >= minRequired && width >= dl.AutoSplitWidth
}
```

### styles.go
```go
package diffview

import "github.com/charmbracelet/lipgloss/v2"

type DiffStyle struct {
    // Background colors (dark theme)
    AdditionBG string // "#303a30" - dark green
    DeletionBG string // "#3a3030" - dark red
    ContextBG  string // transparent

    // Line number styles
    AdditionLineNum lipgloss.Style
    DeletionLineNum lipgloss.Style
    ContextLineNum  lipgloss.Style

    // Symbol styles (+/-)
    AdditionSymbol lipgloss.Style
    DeletionSymbol lipgloss.Style

    // Code content styles
    AdditionCode lipgloss.Style
    DeletionCode lipgloss.Style
    ContextCode  lipgloss.Style
}

func DefaultDarkStyle() DiffStyle {
    return DiffStyle{
        AdditionBG: "#303a30",
        DeletionBG: "#3a3030",

        AdditionLineNum: lipgloss.NewStyle().
            Foreground(lipgloss.Color("#4AD7A5")).
            Background(lipgloss.Color("#293229")),
        DeletionLineNum: lipgloss.NewStyle().
            Foreground(lipgloss.Color("#FF5555")).
            Background(lipgloss.Color("#332929")),
        ContextLineNum: lipgloss.NewStyle().
            Foreground(lipgloss.Color("#5C5C5C")),

        AdditionSymbol: lipgloss.NewStyle().
            Foreground(lipgloss.Color("#4AD7A5")).
            Bold(true),
        DeletionSymbol: lipgloss.NewStyle().
            Foreground(lipgloss.Color("#FF5555")).
            Bold(true),

        AdditionCode: lipgloss.NewStyle().
            Background(lipgloss.Color("#303a30")),
        DeletionCode: lipgloss.NewStyle().
            Background(lipgloss.Color("#3a3030")),
        ContextCode: lipgloss.NewStyle(),
    }
}
```

### styler.go
```go
package diffview

type DiffStyler struct {
    Style DiffStyle
}

type StyledLine struct {
    LineNum string // Formatted line number(s)
    Symbol  string // + or - or space
    Code    string // Styled code content
}

func (ds *DiffStyler) StyleLine(line DiffLine) StyledLine
func (ds *DiffStyler) StyleLineNum(num int, kind DiffLineKind) string
func (ds *DiffStyler) StyleSymbol(kind DiffLineKind) string
func (ds *DiffStyler) StyleCode(content string, kind DiffLineKind) string
```

## Phase 3: Syntax Highlighting

### highlighter.go
```go
package diffview

import (
    "github.com/alecthomas/chroma/v2"
    "github.com/alecthomas/chroma/v2/lexers"
)

type SyntaxHighlighter struct {
    lexer     chroma.Lexer
    style     *chroma.Style
    bgColor   string // Background color to preserve
    cache     map[string]string // content hash -> highlighted
    enabled   bool
}

func NewSyntaxHighlighter(filePath string) *SyntaxHighlighter
func (sh *SyntaxHighlighter) Highlight(content string, bgColor string) string
func (sh *SyntaxHighlighter) SetEnabled(enabled bool)
```

**Key approach**:
1. Use chroma to tokenize code
2. Apply syntax colors as foreground
3. Preserve background color from diff styling
4. Cache results by hash to avoid re-highlighting identical lines

## Phase 4: Rendering

### renderer.go
```go
package diffview

type DiffRenderer struct {
    Width         int
    LineNumbers   bool
    NumWidth      int // Width of line number column
    UseSplit      bool
}

func (dr *DiffRenderer) RenderUnified(diff *ComputedDiff, styler *DiffStyler, highlighter *SyntaxHighlighter) []string
func (dr *DiffRenderer) RenderSplit(diff *ComputedDiff, styler *DiffStyler, highlighter *SyntaxHighlighter) []string
func (dr *DiffRenderer) padLine(content string, width int) string
```

**Unified layout format**:
```
  123  456  + added line with green background
  124       - removed line with red background
  125  457    context line (no background)
```

**Split layout format**:
```
 OLD                  │  NEW
 123  - removed line  │  456  + added line
 124    context       │  457    context
```

## Phase 5: Main Component

### diffview.go
```go
package diffview

type DiffView struct {
    // Input
    BeforePath string
    AfterPath  string
    Before     string
    After      string

    // Sub-components
    computer    *DiffComputer
    layout      *DiffLayout
    styler      *DiffStyler
    highlighter *SyntaxHighlighter
    renderer    *DiffRenderer

    // State
    diff        *ComputedDiff
    width       int
    maxLines    int

    // Integration
    CollapseLevel CollapseLevel // From tool_state.go
}

func New(beforePath, afterPath, before, after string) *DiffView
func (dv *DiffView) SetWidth(w int) *DiffView
func (dv *DiffView) SetMaxLines(n int) *DiffView
func (dv *DiffView) SetCollapseLevel(level CollapseLevel) *DiffView
func (dv *DiffView) Render() []string
func (dv *DiffView) GetStats() (additions, deletions int)
func (dv *DiffView) GetTitle() string // "file.go (+3, -2)"
```

## Phase 6: Integration with Tool Rendering

### Modify: internal/chat/debug_render.go

Add new scenario for diff rendering:
```go
func (d *DebugRenderApp) injectScenarioEdit() {
    // Create Edit tool with before/after content
    toolCall := ToolCallDisplay{
        ID:   "call_edit_001",
        Name: "Edit",
        Parameters: map[string]interface{}{
            "file_path":  "internal/chat/app.go",
            "old_string": "func main() {\n\tfmt.Println(\"old\")\n}",
            "new_string": "func main() {\n\tfmt.Println(\"new\")\n\tlogDebug(\"added\")\n}",
        },
    }
    // ... create message with diff rendering
}
```

### Modify: Tool rendering in app.go or collapse_widget.go

Detect Edit/Write/Patch tools and use DiffView:
```go
func renderToolResult(tc *ToolCallState, result *ToolResultDisplay, width int) []string {
    switch tc.ToolAPIName {
    case "Edit", "file_edit", "edit":
        before := extractParam(tc.Parameters, "old_string")
        after := extractParam(tc.Parameters, "new_string")
        filePath := extractParam(tc.Parameters, "file_path")

        dv := diffview.New(filePath, filePath, before, after)
        dv.SetWidth(width)
        dv.SetCollapseLevel(tc.CollapseLevel)
        return dv.Render()

    case "Write", "file_write", "write":
        // For new files, before is empty
        filePath := extractParam(tc.Parameters, "file_path")
        content := extractParam(tc.Parameters, "content")

        dv := diffview.New("", filePath, "", content)
        dv.SetWidth(width)
        dv.SetCollapseLevel(tc.CollapseLevel)
        return dv.Render()

    default:
        // Fall back to existing rendering
        return renderRegularToolOutput(result.Output, width)
    }
}
```

## Implementation Order

1. **types.go** - Define all types (15 min)
2. **styles.go** - Define color scheme (15 min)
3. **computer.go** - Implement diff algorithm (1 hour)
4. **layout.go** - Layout switching logic (30 min)
5. **styler.go** - Style application (30 min)
6. **highlighter.go** - Syntax highlighting (1 hour)
7. **renderer.go** - Unified and split rendering (1.5 hours)
8. **diffview.go** - Main orchestrator (1 hour)
9. **diffview_test.go** - Unit tests (1 hour)
10. **Integration** - Wire into tool rendering (1 hour)
11. **Debug scenario** - Add test scenario (30 min)
12. **Polish** - Edge cases, colors, alignment (1 hour)

**Total estimated time: ~10 hours**

## Dependencies to Add

```bash
go get github.com/sergi/go-diff/diffmatchpatch
go get github.com/alecthomas/chroma/v2
```

## Success Criteria

1. Edit tool shows diff with green/red backgrounds
2. Write tool shows all content as additions (green)
3. Collapse levels work: Collapsed shows "file.go (+3 -2)", Compact shows first 10 lines, Full shows all
4. Auto-switches to split view at width >= 120
5. Syntax highlighting visible on top of diff colors
6. Smooth scrolling through long diffs
7. No visual glitches or alignment issues

## Visual Example (Unified View)
```
  ● Edit(internal/chat/app.go)
    Added 2 lines, removed 1 line
  ┌─────────────────────────────────────────
  │  3849       return a, nil
  │  3850   }
  │  3851
  │  3852 - // Export current render + metadata (Ctrl+E)
  │  3853 - if key == "ctrl+e" {
  │  3852 + // Export current render + metadata (Ctrl+Shift+D)
  │  3853 + if key == "ctrl+shift+d" {
  │  3854       if err := a.exportRenderSnapshot(); err != nil
  └─────────────────────────────────────────
```

## Questions Resolved
- Tools: Edit, Write, Patch (all file modifications)
- Layout: Auto (unified < 120 chars, split >= 120)
- Collapse: Yes, using existing 3-level system
- Syntax: Yes, layered on diff backgrounds
