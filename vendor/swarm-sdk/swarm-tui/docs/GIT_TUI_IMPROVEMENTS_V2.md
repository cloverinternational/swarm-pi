# Git TUI Improvements - Version 2

## Overview

Major enhancements to the Git TUI component including scrollable file lists, improved diff viewing, table-formatted commit history, and interactive commit detail panels.

## ✅ What's New

### 1. Fixed Files Tab (Tab 2) - Tree List Scrolling

The Files tab now fully supports scrolling and selection:

**Features:**
- Navigate through file list with `j/k` or arrow keys
- Automatic viewport adjustment keeping selection visible
- Visual scroll indicators ("↑ N more above", "↓ N more below")
- Page scrolling with `Ctrl+d/u`
- Jump to top/bottom with `g/G`

**Example:**
```
📁 Files
Press [j/k] to navigate, [Ctrl+u/d] for page, [g/G] for top/bottom

 src/main.go       ← Selected
 src/utils.go
 src/helpers.go
  ↓ 47 more below
```

### 2. Enhanced Diff Viewer

Improved diff viewing with better navigation:

**New Features:**
- Hunk navigation with `n` (next) and `N` (prev) keys
- Scroll through diffs with `j/k` or arrow keys
- Page scrolling with `Ctrl+d/u`
- Jump to top/bottom with `g/G`
- Exit with `q` or `Esc`

**Key Controls in Diff View:**
```
j/k          - Scroll line by line
Ctrl+d/u     - Page down/up
g / G        - Jump to top/bottom
n / N        - Jump to next/previous hunk
q / Esc      - Exit diff view
```

### 3. Table-Formatted Commit History (Tab 7)

Complete redesign of the history view with structured table layout:

**Table Format:**
```
📜 Commit History

SHA       DATE        AUTHOR           MESSAGE
abc1234   2024-01-30  John Doe         Add scrolling support
def5678   2024-01-29  Jane Smith       Fix diff viewer
ghi9101   2024-01-28  Bob Johnson      Improve UI
  ↓ 45 more below
```

**Features:**
- Aligned columns for easy scanning
- Author names truncated to prevent overflow
- Long messages truncated with ellipsis
- Scroll indicators showing available commits above/below
- Select commits with `j/k`

### 4. Commit Detail Panel

Interactive split-screen view showing commit details:

**Activation:**
- Press `Enter` on a commit in History tab to open detail panel

**Layout:**
```
Left Panel (60%): Commit List      │ Right Panel (40%): Details
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┿━━━━━━━━━━━━━━━━━━━━━━━━━━
SHA       MESSAGE              │ 📋 Details
abc1234   Add scrolling       │
def5678   Fix diff viewer     │ SHA:
ghi9101   Improve UI         │   abc1234567890...
jkl1121   Update docs         │
```

**Detail Panel Shows:**
- Full commit SHA
- Author name and email
- Commit timestamp
- Full commit message (with text wrapping)
- Statistics summary

**Navigation:**
- `j/k` - Navigate commits or scroll detail panel
- `Tab` - Switch focus between list and detail panels
- `Ctrl+d/u` - Page navigation
- `g/G` - Jump to top/bottom
- `Enter` - Toggle detail panel
- `q/Esc` - Exit without closing (in detail panel)

### 5. Improved Status Bar Help Text

Context-aware keybinding hints in the status bar:

**When viewing diffs:**
```
j/k:scroll n/p:hunk ↑/↓ Ctrl+u/d:page g/G:top/bottom q/esc:exit
```

**When in History tab:**
```
j/k:navigate ↑/↓ Ctrl+u/d:page g/G:top/bottom r:refresh
```

**When in Files tab:**
```
j/k:navigate Ctrl+u/d:page g/G:top/bottom
```

**When in Unstaged/Staged tabs:**
```
j/k:navigate s:stage u:unstage d/enter:diff a:stage-all x:unstage-all c:commit
```

## 📋 Complete Keybinding Reference

### Navigation Keys (Global)
| Key | Action |
|-----|--------|
| `j`, `↓` | Move down / scroll down |
| `k`, `↑` | Move up / scroll up |
| `g`, `Home` | Jump to top |
| `G`, `End` | Jump to bottom |
| `Ctrl+d`, `PgDn` | Page down |
| `Ctrl+u`, `PgUp` | Page up |
| `Tab` | Switch panels (in History detail view) |

### Tab Selection
| Key | Tab |
|-----|-----|
| `1` | Status |
| `2` | Files |
| `3` | Unstaged Changes |
| `4` | Staged Changes |
| `5` | Commit |
| `6` | Commit Graph |
| `7` | Commit History |

### File Operations (Unstaged/Staged)
| Key | Action |
|-----|--------|
| `s` | Stage selected file |
| `u` | Unstage selected file |
| `d`, `Enter` | View diff of selected file |
| `q`, `Esc` | Exit diff view |
| `a` | Stage all changes |
| `x` | Unstage all changes |
| `c` | Open commit dialog |

### Diff Viewer
| Key | Action |
|-----|--------|
| `j`, `↓` | Scroll down |
| `k`, `↑` | Scroll up |
| `n` | Jump to next hunk |
| `N` | Jump to previous hunk |
| `Ctrl+d`, `PgDn` | Page down |
| `Ctrl+u`, `PgUp` | Page up |
| `g`, `Home` | Jump to top |
| `G`, `End` | Jump to bottom |
| `q`, `Esc` | Exit diff view |

### History Tab
| Key | Action |
|-----|--------|
| `j`, `↓` | Select next commit or scroll detail |
| `k`, `↑` | Select prev commit or scroll detail |
| `Enter` | Toggle detail panel |
| `Tab` | Switch between list and detail focus |
| `Ctrl+d`, `PgDn` | Page down |
| `Ctrl+u`, `PgUp` | Page up |
| `g`, `Home` | Jump to top |
| `G`, `End` | Jump to bottom |
| `r` | Refresh history |

### Remote Operations
| Key | Action |
|-----|--------|
| `p` | Push to remote |
| `o` | Pull from remote |
| `f` | Fetch from remote |

## 🎯 Usage Examples

### Example 1: Review Changes Before Commit

```
1. Press 3 (go to Unstaged tab)
2. Use j/k to navigate files
3. Press Enter on a file to view diff
4. Use j/k to scroll through diff
5. Press n to jump to next hunk
6. Press q to exit diff
7. Press s to stage the file
8. Repeat for other files
9. Press c to commit
```

### Example 2: Explore Commit History

```
1. Press 7 (go to History tab)
2. Use j/k to navigate commits
3. Press Enter to open detail panel
4. Use j/k to scroll commit details
5. Press Tab to switch to list focus
6. Use Ctrl+d to jump down multiple commits
7. Press g to jump to newest commit
8. Press G to jump to oldest commit
9. Press q or Esc to close and go back
```

### Example 3: Quick Navigation

```
1. Press 2 (go to Files tab)
2. Use Ctrl+d to page down quickly
3. Find the file you need
4. Use g to jump back to top
5. Use G to jump to bottom
```

## 🏗️ Implementation Details

### New File: `history_table.go`

Created `HistoryTableView` struct to replace the simple line-based history:

**Key Components:**
- `HistoryTableView` - Main history view with table and detail panel
- `renderTableOnly()` - Renders just the table list
- `renderWithDetailPanel()` - Renders split-screen view
- `renderTableHeader()` - Column headers
- `renderTableRow()` - Individual table rows
- `renderDetailPanel()` - Commit details
- `wrapText()` - Text wrapping utility

**Features:**
- Adaptive column widths based on terminal width
- Text truncation with ellipsis for long content
- Scroll-aware rendering (only visible lines rendered)
- Split-screen layout management
- Focus tracking between panels

### Modified Files:

**component.go:**
- Updated `renderFilesTab()` to support selection and scrolling
- Fixed `maxSelection()` for Files tab
- Added hunk navigation key handlers (`n`/`N`)
- Added detail panel toggle (`Enter` on History)
- Added panel focus switching (`Tab` on History)
- Updated status bar keybinding hints
- Changed `historyView` type from `*HistoryView` to `*HistoryTableView`

**graph.go:**
- `HistoryView` kept for backward compatibility but not actively used
- `HistoryTableView` is new and replaces it in functionality

## 📊 Data Structures

### HistoryTableView Fields

```go
type HistoryTableView struct {
    theme              theme.Theme
    width              int
    height             int
    commits            []gitops.CommitInfo
    selectedIndex      int
    scrollOffset       int
    showDetailPanel    bool
    detailScrollOffset int
    focusedPanel       string // "list" or "detail"
}
```

## 🧪 Testing

All existing tests pass. Key test coverage:

- `TestScrolling` - File list scrolling
- `TestDiffScrolling` - Diff viewer scrolling  
- `TestPageUpDown` - Page navigation
- `TestEnsureVisible` - Viewport adjustment
- `TestStagedTabScrolling` - Staged files scrolling

## 🔄 Viewport Management

Both file lists and detail panels use consistent viewport logic:

1. **Selection Tracking**: Current selection index is tracked
2. **Scroll Offset**: Viewport position is managed separately
3. **Visible Range**: Only lines within viewport are rendered
4. **Auto-scroll**: Viewport adjusts to keep selection visible
5. **Indicators**: Visual hints show if more content exists

## 🎨 Styling

Consistent theme integration across all new views:

- **Selected lines**: Use `BackgroundLightColor()` for highlight
- **Headers**: Bold white text with accent color
- **Labels**: Dim text for metadata
- **Values**: Regular text for content
- **Indicators**: Dim text for scroll hints

## 🐛 Known Limitations

1. **Detail panel file changes** - Currently not showing specific file changes (future enhancement)
2. **Horizontal scrolling** - Long lines in diffs may wrap (can be improved)
3. **Full message display** - Limited to terminal width (text wrapping is basic)

## 🚀 Future Enhancements

1. **File changes in detail panel** - Show list of modified files with +/- stats
2. **Diff statistics** - Show insertion/deletion counts
3. **Mouse support** - Click to select and scroll
4. **Search/filter** - Find commits by message or author
5. **Blame view** - Show who changed each line
6. **Stash management** - View and apply stashes
7. **Branch management** - Create, delete, switch branches

## ✨ Performance Considerations

- **Viewport rendering**: Only visible lines are rendered (efficient)
- **Lazy loading**: History is fetched in batches
- **String operations**: Minimized for large diffs
- **Memory usage**: Scroll offsets are integers (minimal overhead)

## 📝 Notes

- All changes maintain backward compatibility
- Existing keybindings work as before
- New features are additive, no breaking changes
- Tests ensure reliability
- Code follows project conventions

---

**Version**: 2.0  
**Last Updated**: 2024-01-30  
**Status**: Stable
