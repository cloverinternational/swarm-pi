# Settings Responsive Rendering Guide

## Overview

Every settings page MUST render correctly at all viewport tiers (XS 40-59, SM 60-79, MD 80-99, LG 100+).
This guide defines the exact patterns every settings Render() method must follow.

## The Viewport Contract

The settings Manager receives `(width, height int)` from the home screen or standalone view.
At **MD+ (80+)**, the Manager splits into sidebar + content (two-pane).
At **XS/SM (<80)**, the Manager renders single-pane: content only, with a compact section selector at top.

Each individual settings page Render() receives `(width, height int)` where `width` is the
**content area width** (already computed by the Manager). Pages must NEVER assume a minimum
width greater than 36 (the smallest possible content width at XS with single-pane).

## Critical Rules

### Rule 1: No Raw Subtraction
NEVER write `width - N` without a floor guard.

```go
// BAD
innerWidth := width - 8

// GOOD
innerWidth := maxInt(20, width - 8)
// OR use SafeSub from viewport.go:
innerWidth := chat.SafeSub(width, 8, 20)
```

### Rule 2: No Hardcoded Minimums Above 36
Any `if width < N` guard must use N <= 36. The content area at XS single-pane can be as small as 36.

```go
// BAD - breaks at XS
if sidebarWidth < 25 { sidebarWidth = 25 }

// GOOD - responsive
if showTwoPane {
    sidebarWidth = clampInt(width/4, 20, 40)
}
```

### Rule 3: Container Pattern
Every settings page follows this structure:

```go
func (s *FooSettings) Render(width, height int, state *State, theme interface{}) string {
    th := theme.(Theme)

    // Safe inner dimensions (accounts for border + padding)
    // Border = 1 per side, padding = 1 per side = total 4 horizontal
    const chrome = 4
    innerWidth := maxInt(20, width - chrome)
    innerHeight := maxInt(5, height - chrome - 4) // 4 = title(2) + hints(2)

    title := s.renderTitle(innerWidth, th)
    content := s.renderContent(innerWidth, innerHeight, state, th)
    hints := s.renderHintBar(innerWidth, th)

    body := lipgloss.JoinVertical(lipgloss.Left, title, content, hints)

    containerStyle := lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color(th.Border)).
        Background(lipgloss.Color(th.BG)).
        Width(maxInt(20, width-2)).
        Padding(0, 1)

    return containerStyle.Render(body)
}
```

### Rule 4: Responsive Content Density

Settings items should adapt their layout based on available width:

| Width Range | Layout |
|------------|--------|
| < 50       | Stacked: label on one line, value below, description hidden |
| 50-70      | Inline: `[x] Label` with short description |
| 70+        | Full: `[x] Label  ............  description text` |

```go
func renderToggle(width int, label, desc string, enabled bool, selected bool, th Theme) string {
    indicator := "[ ]"
    if enabled { indicator = "[x]" }
    if selected { indicator = ">" + indicator[1:] }

    if width < 50 {
        // Stacked: just the toggle + label
        return fmt.Sprintf("  %s %s", indicator, truncate(label, width-8))
    }
    if width < 70 {
        // Inline, no description
        return fmt.Sprintf("  %s %s", indicator, label)
    }
    // Full: label + dots + description
    labelPart := fmt.Sprintf("  %s %s", indicator, label)
    remaining := width - lipgloss.Width(labelPart) - 2
    if remaining > len(desc)+4 {
        dots := strings.Repeat(".", remaining-len(desc)-2)
        return labelPart + " " + dots + " " + desc
    }
    return labelPart
}
```

### Rule 5: Dividers Scale
Hardcoded divider strings like `"---------------------"` break at narrow widths.

```go
// BAD
divider := "-------------------------------------"

// GOOD
divider := strings.Repeat("-", maxInt(5, innerWidth-4))
```

### Rule 6: Badge Rows Wrap
Multiple badges on one line overflow at narrow widths. Badges should wrap or be omitted.

```go
func renderBadges(width int, badges []string, th Theme) string {
    if width < 50 {
        // Show at most 1 badge
        if len(badges) > 0 {
            return badges[0]
        }
        return ""
    }
    // Show all badges, letting lipgloss handle overflow
    return lipgloss.JoinHorizontal(lipgloss.Center, badges...)
}
```

### Rule 7: Title Simplification
At narrow widths, drop subtitles and status badges.

```go
func renderTitle(width int, title, subtitle string, th Theme) string {
    titleStyle := lipgloss.NewStyle().Bold(true).
        Foreground(lipgloss.Color(th.Primary)).
        Width(width).Align(lipgloss.Center)

    if width < 50 {
        return titleStyle.Render(title)
    }
    sub := lipgloss.NewStyle().
        Foreground(lipgloss.Color(th.TextMuted)).
        Width(width).Align(lipgloss.Center).
        Render(subtitle)
    return lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(title), sub)
}
```

### Rule 8: Scrollable Content Height
Always compute available height for the scrollable section, never let it go negative.

```go
// Available lines for scrollable items
scrollH := maxInt(3, innerHeight)
// Apply scroll offset
visible := items[state.ScrollOffset:]
if len(visible) > scrollH {
    visible = visible[:scrollH]
}
```

### Rule 9: Spinner/Preview Sections
Animated previews (spinner types, theme previews) should be hidden or minimized at XS.

```go
if width >= 60 {
    // Show spinner preview
    lines = append(lines, renderSpinnerPreview(width, th, animationFrame))
}
```

### Rule 10: Hint Bars
Hint bars at the bottom must abbreviate or hide at narrow widths.

```go
func renderHints(width int, th Theme) string {
    if width < 45 {
        return ""  // No room for hints
    }
    if width < 65 {
        return styledHint("esc:back  enter:toggle  /search", th)
    }
    return styledHint("esc: back | enter: toggle | tab: section | /: search", th)
}
```

## Manager Layout Rules

### Two-Pane vs Single-Pane

The Manager.Render() and renderSettingsInline() must use the Viewport to decide layout:

```go
func (m *Manager) Render(width, height int, theme interface{}, animationFrame int) string {
    th := ConvertTheme(theme)
    vp := chat.NewViewport(width, height)

    if vp.ShowTwoPane() {
        // Two-pane: sidebar + content
        sidebarWidth := vp.SidebarWidth()
        contentWidth := maxInt(30, width - sidebarWidth - 1)
        contentHeight := maxInt(5, height - 2)

        sidebar := m.renderSidebar(sidebarWidth, contentHeight, th)
        content := m.renderContentForSection(contentWidth, contentHeight, th, animationFrame)
        combined := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, content)
        hints := m.renderHints(width, th)
        return lipgloss.JoinVertical(lipgloss.Left, combined, hints)
    }

    // Single-pane: section selector row + content
    selectorHeight := 2
    contentHeight := maxInt(5, height - selectorHeight - 2)
    contentWidth := maxInt(30, width)

    selector := m.renderSectionSelector(width, th)
    content := m.renderContentForSection(contentWidth, contentHeight, th, animationFrame)
    hints := m.renderHints(width, th)
    return lipgloss.JoinVertical(lipgloss.Left, selector, content, hints)
}
```

### Single-Pane Section Selector

At XS/SM, replace the sidebar with a compact horizontal section indicator:

```
< General >           (arrows show prev/next, tab cycles)
```

Or a dropdown-style current section name:

```
[v] General Settings    esc:back  tab:next
```

### Sidebar Responsive Indent

At MD the sidebar should use 2-char indent ("  "). At LG use 4-char ("    ").

## Validation Checklist

Before any settings page is complete, verify:

- [ ] `go vet ./internal/chat/...` passes
- [ ] No `width - N` without `maxInt()` or `SafeSub()` floor guard
- [ ] No hardcoded minimum width > 36
- [ ] No hardcoded string dividers — use `strings.Repeat()` with width
- [ ] Title adapts at width < 50 (drop subtitle/badges)
- [ ] Toggle items render in stacked mode at width < 50
- [ ] Hint bar abbreviates at width < 65, hides at width < 45
- [ ] Container border uses `Width(maxInt(20, width-2))`
- [ ] innerHeight is never negative (use `maxInt(5, ...)`)
- [ ] Spinner/preview sections hidden at width < 60
