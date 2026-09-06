# Git TUI Scroll Improvements

## Overview
This document describes the comprehensive scroll functionality improvements made to the Git TUI component to enable navigation through commit messages, file lists, and diffs.

## Changes Made

### 1. **Viewport Scrolling for File Lists**

All file list tabs now support viewport scrolling with visual indicators:

- **Unstaged Tab (Tab 3)**: Scroll through unstaged and untracked files
- **Staged Tab (Tab 4)**: Scroll through staged files  
- **Files Tab (Tab 2)**: Scroll through repository file tree

**Features:**
- Automatic viewport adjustment as you navigate
- Visual indicators showing "↑ N more above" and "↓ N more below"
- Selection stays visible within the viewport

### 2. **Diff Viewer Scrolling**

When viewing a file diff, you can now scroll through the entire diff:

- Press `d` or `Enter` on a file to view its diff
- Use `j/k` or arrow keys to scroll through the diff
- Use `Ctrl+d/Ctrl+u` or `PgDn/PgUp` for page-wise scrolling
- Press `q` or `Esc` to exit the diff view
- Use `g/G` to jump to top/bottom of diff

### 3. **Enhanced Navigation Keys**

#### Basic Navigation
- `j` or `↓`: Move down / scroll down
- `k` or `↑`: Move up / scroll up
- `g` or `Home`: Jump to top
- `G` or `End`: Jump to bottom

#### Fast Navigation (NEW)
- `Ctrl+d` or `PgDn`: Page down (half screen)
- `Ctrl+u` or `PgUp`: Page up (half screen)

These work in all scrollable contexts:
- File lists (Unstaged, Staged, Files tabs)
- Diff viewer
- Commit history
- Commit graph

### 4. **Improved Graph & History Views**

The existing Graph (Tab 6) and History (Tab 7) tabs now integrate with the enhanced navigation system:

- Same consistent keybindings across all tabs
- Page up/down support added
- Better scroll indicators

### 5. **Context-Aware Status Bar**

The status bar now shows relevant keybindings based on current state:

**When viewing a diff:**
```
j/k:scroll ↑/↓ Ctrl+u/d:page g/G:top/bottom q/esc:exit diff
```

**When navigating Graph/History:**
```
j/k:navigate ↑/↓ Ctrl+u/d:page g/G:top/bottom r:refresh
```

**When in file lists (Unstaged/Staged):**
```
j/k:navigate s:stage u:unstage d/enter:diff a:stage-all x:unstage-all c:commit
```

**Default (Status tab):**
```
r:refresh s:stage u:unstage a:stage-all x:unstage-all c:commit p:push o:pull
```

## Technical Implementation

### New Fields in Model
```go
type Model struct {
    // ...
    scrollOffset int      // Viewport scroll position for file lists
    viewingDiff  bool     // Track if currently viewing a diff
    // ...
}
```

### New Methods

#### `ensureVisible()`
Automatically adjusts `scrollOffset` to keep the selected item visible within the viewport:
```go
func (m Model) ensureVisible() Model {
    visibleLines := m.height - 6
    if m.selectedIndex >= m.scrollOffset+visibleLines {
        m.scrollOffset = m.selectedIndex - visibleLines + 1
    }
    if m.selectedIndex < m.scrollOffset {
        m.scrollOffset = m.selectedIndex
    }
    return m
}
```

### Updated Rendering
File list tabs now render only visible items based on `scrollOffset`:
- Calculate visible range: `[scrollOffset, scrollOffset + visibleLines]`
- Show scroll indicators when there's more content above/below
- Highlight selected item even when scrolled

## Usage Examples

### Scrolling Through Unstaged Files
1. Press `3` to switch to Unstaged tab
2. Use `j/k` to navigate through files
3. The viewport automatically scrolls to keep selection visible
4. See indicators like "↓ 15 more below" when more files exist

### Viewing and Scrolling a Diff
1. Navigate to a file in Unstaged (Tab 3) or Staged (Tab 4)
2. Press `d` or `Enter` to view the diff
3. Use `j/k` to scroll line by line
4. Use `Ctrl+d/Ctrl+u` for faster scrolling
5. Press `q` to exit back to file list

### Navigating Commit History
1. Press `7` to switch to History tab
2. Use `j/k` to navigate commits
3. Use `Ctrl+d` to jump down half a page
4. Use `g` to jump to the newest commit
5. Use `G` to jump to the oldest commit

## Testing

Comprehensive tests have been added:

- `TestScrolling`: Tests viewport scrolling in unstaged files
- `TestDiffScrolling`: Tests scrolling within diff viewer
- `TestPageUpDown`: Tests page navigation
- `TestEnsureVisible`: Tests viewport adjustment logic
- `TestStagedTabScrolling`: Tests scrolling in staged files

Run tests:
```bash
go test ./internal/chatui/components/gitpanel -v
```

## Keybinding Reference

### Global Navigation (All Tabs)
| Key | Action |
|-----|--------|
| `1-7` | Switch to tab 1-7 |
| `j`, `↓` | Move/scroll down |
| `k`, `↑` | Move/scroll up |
| `g`, `Home` | Jump to top |
| `G`, `End` | Jump to bottom |
| `Ctrl+d`, `PgDn` | Page down |
| `Ctrl+u`, `PgUp` | Page up |
| `r` | Refresh status |

### File Operations (Unstaged/Staged Tabs)
| Key | Action |
|-----|--------|
| `s` | Stage selected file |
| `u` | Unstage selected file |
| `d`, `Enter` | View diff of selected file |
| `q`, `Esc` | Exit diff view (when viewing diff) |
| `a` | Stage all files |
| `x` | Unstage all files |
| `c` | Open commit dialog |

### Remote Operations
| Key | Action |
|-----|--------|
| `p` | Push to remote |
| `o` | Pull from remote |
| `f` | Fetch from remote |

## Performance Considerations

- Only visible items are rendered, improving performance with large file lists
- Scroll offset is clamped to valid ranges to prevent errors
- Viewport calculation accounts for UI chrome (header, status bar)

## Future Enhancements

Potential improvements for future iterations:

1. **Mouse wheel support** for scrolling
2. **Search/filter** functionality in file lists
3. **Collapsible sections** in diff viewer
4. **Horizontal scrolling** for long lines
5. **Bookmarks/marks** for quick navigation
6. **Split diff view** (side-by-side comparison)

## Compatibility

These changes maintain backward compatibility:
- Existing keybindings continue to work
- No breaking changes to the API
- All existing tests pass
- New functionality is additive

## Conclusion

The Git TUI now provides a smooth, intuitive scrolling experience across all views. Users can efficiently navigate through:
- Long file lists
- Large diffs
- Extensive commit history
- Complex commit graphs

The consistent keybindings and visual feedback make the interface more user-friendly and productive.
