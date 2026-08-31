# Keyboard Scrolling Fix for Commit Graph

## Problem
The initial scrolling implementation in the commit graph was not properly handling keyboard events. The issue was that we were using Textual action bindings (`action_*` methods with BINDINGS list), but this approach doesn't work well for continuous scrolling widgets like `VerticalScroll`.

## Solution
Implemented a custom `key()` event handler in `CommitGraphWidget` that directly handles keyboard input and calls the appropriate scroll methods on the `VerticalScroll` widget.

## Implementation Details

### File: `octogit/commit_graph.py`

#### Key Changes:

1. **Added scroll tracking attributes** (lines 1535-1536):
```python
self.scroll_offset = 0  # Tracking scroll position for keyboard navigation
self.selected_index = 0  # Currently selected commit index
```

2. **Implemented custom `key()` event handler** (lines 1660-1698):
```python
def key(self, event) -> bool:
    """Handle keyboard events for scrolling through commits."""
    key_name = event.key if hasattr(event, 'key') else ""
    
    try:
        scroll = self.query_one("#graph-scroll", VerticalScroll)
    except Exception:
        return False
    
    if key_name in ("up", "k"):
        # Scroll up 3 lines
        scroll.scroll_up(lines=3, animate=False)
        return True
    elif key_name in ("down", "j"):
        # Scroll down 3 lines
        scroll.scroll_down(lines=3, animate=False)
        return True
    elif key_name == "home":
        # Jump to top
        scroll.scroll_home(animate=False)
        return True
    elif key_name == "end":
        # Jump to bottom
        scroll.scroll_end(animate=False)
        return True
    elif key_name == "pageup":
        # Page up - scroll by screen height
        scroll.scroll_up(lines=scroll.size.height - 3, animate=False)
        return True
    elif key_name == "pagedown":
        # Page down - scroll by screen height
        scroll.scroll_down(lines=scroll.size.height - 3, animate=False)
        return True
    
    return False
```

3. **Kept focus management** (lines 1550-1557):
```python
def on_focus(self) -> None:
    """Handle focus event - ensure scroll is focused."""
    try:
        scroll = self.query_one("#graph-scroll", VerticalScroll)
        scroll.focus()
    except Exception:
        pass
```

## Keyboard Shortcuts

### Scrolling Controls:
| Key | Action |
|-----|--------|
| `k` or `↑` | Scroll up 3 lines |
| `j` or `↓` | Scroll down 3 lines |
| `Home` | Jump to top of commits |
| `End` | Jump to bottom of commits |
| `PageUp` | Scroll up one screen |
| `PageDown` | Scroll down one screen |
| `/` | Search commits |
| `f` | Toggle filter |

## Why This Approach Works

1. **Event Handler Method**: The `key()` method is called by the Textual framework whenever a key event occurs on the widget. This bypasses the BINDINGS system which is better for widgets that need event forwarding.

2. **Direct Scroll API**: Instead of trying to trigger actions, we directly call the VerticalScroll widget's scroll methods:
   - `scroll_up(lines=N, animate=False)` - Scroll up by N lines
   - `scroll_down(lines=N, animate=False)` - Scroll down by N lines
   - `scroll_home(animate=False)` - Jump to top
   - `scroll_end(animate=False)` - Jump to bottom

3. **Non-animated**: We use `animate=False` for instant, responsive scrolling without delay

4. **Return Value**: Returning `True` from the `key()` method tells Textual that we've handled the event and it should not be propagated further

5. **Fallback**: Returning `False` for unhandled keys allows other handlers to process them

## How It Mirrors the Debug Menu Pattern

This implementation follows the same pattern as the debug menu in the main TUI:
- The GO code uses `switch` statements to check `key_name`
- Increment/decrement indices or adjust offsets
- Re-render the content based on new scroll position
- Return `true` to consume the event

Our Python implementation:
- Uses `if/elif` chain to check `key_name`
- Calls Textual's built-in scroll methods
- VerticalScroll handles the re-rendering
- Return `True` to consume the event

## Testing

To verify scrolling works:

1. **Open Git TUI**:
   ```bash
   cd /home/swarm/SwarmCode/TUI/octogit
   python3 main.py
   ```

2. **Navigate to Graph tab**: Press `6`

3. **Test scrolling**:
   - Press `k` or `↑` to scroll up
   - Press `j` or `↓` to scroll down
   - Press `Home` to jump to top
   - Press `End` to jump to bottom
   - Press `PageUp`/`PageDown` for screen-size scrolling

4. **Test search**: Press `/` to search commits

## Files Modified

1. `octogit/commit_graph.py` - Added key() handler and scroll tracking attributes
2. `octogit/git_diff_viewer.py` - Updated help documentation
3. `docs/KEYBOARD_SCROLLING_FIX.md` - This documentation

## Compatibility

- ✓ Vim-style navigation (j/k for down/up)
- ✓ Arrow keys (↑/↓)
- ✓ Standard keys (Home/End/PageUp/PageDown)
- ✓ Mouse scrolling (built-in to VerticalScroll)
- ✓ Search functionality (/)

## Future Enhancements

Potential improvements:
- [ ] Highlight currently selected commit
- [ ] Show scroll position indicator
- [ ] Custom scroll amount configuration
- [ ] Smooth scrolling option
- [ ] Jump to specific commit by index

## Troubleshooting

### Scrolling still not working
1. Verify you're on the Graph tab (Tab 6)
2. Click on the graph area to ensure the widget has focus
3. Check if other widgets are consuming key events
4. Restart the application

### Keys not recognized
- Make sure terminal supports the key (some terminals don't support all keys)
- Try using j/k/home/end instead of arrow keys
- Check Textual version compatibility

### Performance issues
- If scrolling is slow with many commits, try reducing max_commits parameter
- Use search (/) to filter commits instead of scrolling
- Consider using a more powerful terminal emulator
