# TUI Overflow Issues - Analysis & Detector

## Overflow Detector Tool

A static analysis tool for detecting potential TUI layout overflow issues in Go code.

### Features

- **Smart False Positive Detection**: Analyzes variable types, function context, and code patterns to identify safe operations
- **Multiple Detection Categories**: ANSI corruption, width calculations, padding, borders, truncation
- **Fix Suggestions**: Provides actionable fix recommendations
- **Report Generation**: Markdown reports for documentation
- **Filtering**: By severity (high/medium/low/info) and category

### Usage

```bash
# Build
go build -o overflow-detector ./cmd/overflow-detector/

# Basic scan
./overflow-detector -dir ./internal/chat

# Filter by severity
./overflow-detector -dir ./internal/chat -severity high

# Filter by category
./overflow-detector -dir ./internal/chat -category ansi-corruption

# Verbose mode (show code and fixes)
./overflow-detector -dir ./internal/chat -v

# Show likely false positives
./overflow-detector -dir ./internal/chat -show-fp

# Generate detailed report
./overflow-detector -dir ./internal/chat -report > report.md

# List all categories
./overflow-detector -list-categories
```

### Categories

| Category | Description | Severity |
|----------|-------------|----------|
| `ansi-corruption` | Operations that corrupt ANSI escape sequences | High |
| `width-calculation` | Incorrect terminal width calculations | High |
| `truncation-unsafe` | Truncation that may corrupt text | Medium |
| `width-mismatch` | Width variables used inconsistently | Medium |
| `padding-mismatch` | Padding not accounted for | Medium |
| `border-mismatch` | Border dimensions not accounted for | Low |
| `height-mismatch` | Height calculations that may overflow | Info |
| `hardcoded-dimension` | Hardcoded width/height values | Low |

## Current Status

### Scan Results (internal/chat)

| Severity | Count | Status |
|----------|-------|--------|
| 🔴 High | 0 | All false positives (properly filtered) |
| 🟡 Medium | 84 | Truncation patterns, width mismatches |
| 🔵 Low | 187 | Border warnings, hardcoded widths |
| ⚪ Info | 87 | Height calculations, horizontal joins |
| ⬜ False+ | 24 | Properly identified as safe |

### Issues Fixed in This Session

| File | Function/Line | Issue | Fix |
|------|---------------|-------|-----|
| `overlay.go` | `overlayAt` | Rune replacement corrupts ANSI | `ansi.Cut()` |
| `app.go` | `renderNotifications` | Rune truncation corrupts ANSI | `ansi.Truncate()` |
| `app.go` | Modal width | Overlays use full width not chat width | Use `chatWidth` |
| `viewer.go` | `truncateVisible` | Lost ANSI styling on truncation | `ansi.Truncate()` |
| `commands/model.go` | `joinLeftRight` | Rune truncation on styled text | `ansi.Truncate()` |
| `components.go` | `wordWrapText` (×2) | `len([]rune())` for width | `lipgloss.Width()` |

## Best Practices for TUI Styling

### DO ✅

```go
// Use ANSI-aware functions for styled strings
width := ansi.StringWidth(styledText)
width := lipgloss.Width(styledText)

// Use ANSI-safe truncation
truncated := ansi.Truncate(text, maxWidth, "...")

// Use ANSI-safe substring extraction
left := ansi.Cut(text, 0, startX)
right := ansi.Cut(text, endX, width)

// Account for borders and padding
contentWidth := width - 2  // borders
contentHeight := height - 2  // vertical padding
```

### DON'T ❌

```go
// Don't use []rune() on styled strings
runes := []rune(styledText)  // Corrupts ANSI codes!

// Don't use len([]rune()) for terminal width
width := len([]rune(text))  // Wrong for wide chars & ANSI

// Don't slice styled strings directly
truncated := text[:maxLen]  // Corrupts ANSI codes!

// Don't forget border/padding in calculations
style.Width(width).Border(...)  // Border adds 2 to actual width!
```

### Safe Patterns (False Positives)

These patterns are safe even though they use `[]rune()`:

```go
// String literals (no ANSI possible)
chars := []rune("0123456789")

// After ANSI stripping
plain := stripANSI(text)
runes := []rune(plain)  // Safe

// Plain text variables (user input, search queries)
query := userInput
runes := []rune(query)  // Safe - user input has no ANSI

// Before styling is applied
label := "Button"
runes := []rune(label)  // Safe - will be styled after
styled := style.Render(string(runes))
```

## Architecture Notes

The detector uses two analysis passes:

1. **Variable Type Analysis**: First pass identifies which variables are plain text vs styled
   - Tracks assignments from `stripANSI()`, `Render()`, string literals
   - Maintains safe/styled variable maps per file

2. **Pattern Analysis**: Second pass checks for dangerous patterns
   - AST analysis for `[]rune()` and `len([]rune())` calls
   - Regex patterns for truncation, width mismatches, padding issues
   - Context checks to filter false positives

## Files Modified

- `cmd/overflow-detector/main.go` - Static analysis tool
- `internal/chat/overlay.go` - ANSI-aware overlay
- `internal/chat/app.go` - Notification truncation, modal width
- `internal/chat/viewer.go` - ANSI-safe truncation
- `internal/chat/commands/model.go` - ANSI-safe truncation
- `internal/chat/components.go` - Proper width calculation
