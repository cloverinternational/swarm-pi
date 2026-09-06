# Git TUI Scrolling Improvements

## Overview
This document describes the improvements made to enable keyboard scrolling for commit messages in the Git TUI application.

## Message Rendering and Highlighting Logic

### Key Files

#### 1. `/octogit/commit_graph.py`
- **CommitGraphLine class (lines 1431-1472)**: Renders individual commit lines with hover highlighting
  - CSS hover effect changes background to `#1a1b26` (dark gray) on mouse hover
  - Displays commit information in a formatted single line

- **GitGraphRenderer class (line 20+)**: Core rendering logic
  - `render_commit_line()` method (lines 720-819): Formats commit messages
  - Calculates colors based on branch depth and commit type
  - Indicates HEAD commits with bright green color (➤ symbol)
  - Differentiates merge commits with diamond symbol (◆)

- **CommitGraphWidget class (line 1475+)**: Main graph widget
  - `_render_graph()` method: Mounts rendered commit lines into scroll container
  - VerticalScroll container (line 1542) with `overflow-y: auto` CSS styling

#### 2. `/octogit/git_diff_viewer.py`
- **GitDiffViewer class (line 469+)**: Main application controller
  - Manages tab switching and overall application state
  - Updated help documentation with new keyboard shortcuts

#### 3. `/octogit/style.tcss`
- CSS styling for the entire application
- Defines hover effects, colors, and scroll behavior

## Implemented Scrolling Features

### Keyboard Bindings Added (to CommitGraphWidget)

| Key(s) | Action | Description |
|--------|--------|-------------|
| `j` | `scroll_down` | Scroll down one page |
| `↓` | `scroll_down` | Scroll down one page |
| `k` | `scroll_up` | Scroll up one page |
| `↑` | `scroll_up` | Scroll up one page |
| `g` | `scroll_home` | Jump to top of commits |
| `Home` | `scroll_home` | Jump to top of commits |
| `Shift+G` | `scroll_end` | Jump to bottom of commits |
| `End` | `scroll_end` | Jump to bottom of commits |
| `/` | `focus_search` | Focus search input (existing) |
| `f` | `toggle_filter` | Toggle filters (existing) |

### Code Changes Made

#### 1. Added BINDINGS to CommitGraphWidget (commit_graph.py, line 1517)
```python
BINDINGS = [
    Binding("/", "focus_search", "Search", show=True),
    Binding("f", "toggle_filter", "Filter", show=True),
    # Keyboard navigation
    Binding("j", "scroll_down", "Scroll Down", show=True),
    Binding("down", "scroll_down", "Scroll Down", show=True),
    Binding("k", "scroll_up", "Scroll Up", show=True),
    Binding("up", "scroll_up", "Scroll Up", show=True),
    Binding("g", "scroll_home", "Home", show=True),
    Binding("shift+g", "scroll_end", "End", show=True),
    Binding("home", "scroll_home", "Home", show=True),
    Binding("end", "scroll_end", "End", show=True),
]
```

#### 2. Implemented Action Methods (commit_graph.py, line 1639+)
```python
def action_scroll_down(self) -> None:
    """Scroll down in the commit graph."""
    try:
        scroll = self.query_one("#graph-scroll", VerticalScroll)
        scroll.scroll_down(animate=False)
    except Exception:
        pass

def action_scroll_up(self) -> None:
    """Scroll up in the commit graph."""
    try:
        scroll = self.query_one("#graph-scroll", VerticalScroll)
        scroll.scroll_up(animate=False)
    except Exception:
        pass

def action_scroll_home(self) -> None:
    """Scroll to the top of the commit graph."""
    try:
        scroll = self.query_one("#graph-scroll", VerticalScroll)
        scroll.scroll_home(animate=False)
    except Exception:
        pass

def action_scroll_end(self) -> None:
    """Scroll to the bottom of the commit graph."""
    try:
        scroll = self.query_one("#graph-scroll", VerticalScroll)
        scroll.scroll_end(animate=False)
    except Exception:
        pass
```

#### 3. Added Focus Management (commit_graph.py, line 1544)
```python
def on_focus(self) -> None:
    """Handle focus event - ensure scroll is focused."""
    try:
        # Focus the scroll container when the widget receives focus
        scroll = self.query_one("#graph-scroll", VerticalScroll)
        scroll.focus()
    except Exception:
        pass
```

#### 4. Updated Help Documentation (git_diff_viewer.py, line 330)
Added new section for Commit Graph Navigation:
```
[help-section-title]📜 Commit Graph Navigation (Tab 6)[/help-section-title]
[help-key]j/Down[/help-key]     Scroll down in commit graph
[help-key]k/Up[/help-key]       Scroll up in commit graph
[help-key]g/Home[/help-key]     Scroll to top of commits
[help-key]Shift+G/End[/help-key] Scroll to bottom of commits
[help-key]/[/help-key]          Search commits by message/SHA
[help-key]f[/help-key]          Toggle filter options
```

## Usage

### Basic Navigation
1. Switch to the Graph tab using `6` key
2. Use arrow keys or `j`/`k` to scroll through commits
3. Use `Home`/`End` or `g`/`Shift+G` for jumping to top/bottom
4. Press `h` to see all keyboard shortcuts

### Mouse Interaction
- Hover over commit lines to see them highlighted
- Use scroll wheel or mouse drag to scroll

### Search Functionality (Existing)
- Press `/` to search commits by message or SHA
- Search results are filtered in real-time

## Technical Details

### How Scrolling Works

1. **VerticalScroll Container**: The commit graph uses a `VerticalScroll` widget with ID `"#graph-scroll"`
2. **Keyboard Events**: The Textual framework captures keyboard events and routes them to action methods
3. **Focus Management**: The `on_focus()` method ensures the scroll container receives focus when the graph tab is active
4. **Scroll Methods**: Textual's built-in `scroll_down()`, `scroll_up()`, `scroll_home()`, and `scroll_end()` methods handle the actual scrolling
5. **Non-animated Scrolling**: We use `animate=False` for instant, responsive scrolling

### Highlighting Mechanism

1. **CSS Hover**: Commit lines use CSS `:hover` pseudo-class for visual highlighting
2. **Static Widget**: Each commit is a `Static` widget subclass (`CommitGraphLine`)
3. **Color Coding**: Different commit types and branch depths get different colors
   - HEAD commits: Bright green (➤)
   - Merge commits: Red (◆)
   - Regular commits: Depth-based colors (blue, green, yellow, purple, cyan)

## Troubleshooting

### Scrolling Not Working
- Ensure you're on the Graph tab (press `6`)
- Check if the scroll container has focus (try clicking on it with mouse)
- Restart the application if keyboard shortcuts aren't responding

### Key Conflicts
- The `g` key in the graph tab scrolls to top (instead of generating AI commit messages)
- For AI commit generation, use tabs other than the graph tab, or use the UI button

### Performance Issues
- If scrolling is slow, try reducing `max_commits` parameter in CommitGraphWidget
- Large repositories may have reduced performance with many commits

## Future Enhancements

Potential improvements for scrolling functionality:
- [ ] Support for Page Up/Down keys
- [ ] Smooth scrolling animation option
- [ ] Vertical scrollbar visibility toggle
- [ ] Commit count display (showing "Showing X of Y commits")
- [ ] Jump to specific commit number
- [ ] Persistent scroll position when switching tabs

## Files Modified

1. `/octogit/commit_graph.py` - Added keyboard bindings and action methods
2. `/octogit/git_diff_viewer.py` - Updated help documentation
3. `/docs/SCROLLING_IMPROVEMENTS.md` - This documentation file

## Testing

To test the scrolling functionality:
1. Navigate to the Graph tab (`6`)
2. Test all keyboard shortcuts (j/k/up/down/g/Shift+g/Home/End)
3. Test mouse hover highlighting
4. Test search functionality with `/` key
5. Verify help modal shows all shortcuts correctly

## Conclusion

The Git TUI now supports full keyboard navigation through commit messages, making it much easier to browse and work with commit history without relying on mouse interactions, especially for users familiar with vim-style navigation.
