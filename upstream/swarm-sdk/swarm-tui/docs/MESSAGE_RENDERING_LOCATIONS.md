# Message Rendering and Highlighting Logic - Location Reference

## Quick Reference

### Where Messages Are Rendered
**File**: `octogit/commit_graph.py`
- **Class**: `GitGraphRenderer` (line 20+)
- **Method**: `render_commit_line()` (lines 720-819)

### Where Messages Are Highlighted
**File**: `octogit/commit_graph.py`
- **Class**: `CommitGraphLine` (lines 1431-1472)
- **CSS Rule**: Lines 1445-1447

### Where Messages Are Mounted/Displayed
**File**: `octogit/commit_graph.py`
- **Class**: `CommitGraphWidget` (lines 1475+)
- **Method**: `_render_graph()` (lines 1582-1649)

---

## Detailed Breakdown

### 1. GitGraphRenderer Class (`octogit/commit_graph.py`, line 20)

**Purpose**: Core rendering engine that formats commit messages with colors and branch indicators

**Key Components**:

#### Color Definitions (lines 70-95)
```python
self.depth_colors = [
    "#89b4fa",  # L0: Blue (main)
    "#a6e3a1",  # L1: Green (first branch level)
    "#f9e2af",  # L2: Yellow (second branch level)
    "#cba6f7",  # L3: Purple (third branch level)
    "#94e2d5",  # L4+: Cyan (deeper levels)
]

self.merge_color = "#f38ba8"  # Red for merges
self.HEAD_INDICATOR = "➤"  # Distinctive pointer
self.HEAD_COLOR = "#a6e3a1"  # Bright green for HEAD
```

#### Main Rendering Method: `render_commit_line()` (lines 720-819)

This method formats a single commit line with:
- **Label column** (left side): Branch/tag names
- **Graph part**: ASCII art tree visualization
- **Commit info**: SHA, message, author

**Key Logic**:
```python
def render_commit_line(self, commit: CommitNode, max_width: int = 200) -> str:
    """Render a clean single-line commit visualization"""
    
    # 1. Validate commit data
    if not commit or not hasattr(commit, 'sha'):
        return " " * LABEL_COLUMN_WIDTH + "[error] Invalid commit data"
    
    # 2. Determine commit type and color
    is_head = self._is_head_commit(commit)
    if is_head:
        commit_symbol = self.HEAD_INDICATOR  # ➤
        commit_color = self.HEAD_COLOR  # Bright green
    elif commit.is_merge():
        commit_symbol = self.MERGE_DOT  # ◆
        commit_color = self.merge_color  # Red
    else:
        commit_symbol = self.COMMIT_DOT  # ●
        # Color based on branch depth
    
    # 3. Format and return the final rendered line
    result = f"{label_column}{graph_part} {commit_info}"
    return result
```

**Important Helper Methods**:
- `_calculate_commit_depth()` - Determines branch depth level
- `_is_head_commit()` - Checks if commit is HEAD
- `_format_label_column()` - Formats branch/tag labels
- `_build_graph_columns()` - Creates ASCII graph visualization
- `_format_commit_info()` - Formats SHA, message, author

---

### 2. CommitGraphLine Widget (`octogit/commit_graph.py`, lines 1431-1472)

**Purpose**: Individual commit line widget that displays the rendered content and provides hover highlighting

**CSS Highlighting** (lines 1445-1447):
```css
CommitGraphLine:hover {
    background: #1a1b26;  /* Dark gray background on hover */
}
```

**Widget Definition**:
```python
class CommitGraphLine(Static):
    """A single line in the commit graph."""
    
    DEFAULT_CSS = """
    CommitGraphLine {
        width: 100%;
        height: auto;
        padding: 0 1;
        margin: 0;
        background: transparent;
        overflow: hidden;
        color: #cdd6f4;
    }
    
    CommitGraphLine:hover {
        background: #1a1b26;  # Highlighting on hover
    }
    """
    
    def __init__(self, commit: CommitNode, content: str, **kwargs):
        """Initialize with commit object and pre-formatted content"""
        super().__init__(content, **kwargs)
        self.commit = commit
```

---

### 3. CommitGraphWidget (`octogit/commit_graph.py`, lines 1475+)

**Purpose**: Main container widget that manages the commit graph display and handles user interaction

**Key Structure**:
```python
class CommitGraphWidget(Widget):
    """Git log --graph style commit graph widget."""
    
    # Container IDs:
    # - #graph-toolbar: Search box and refresh button
    # - #graph-scroll: VerticalScroll container for commits
    # - #graph-search: Input field for searching
    # - #refresh-graph: Refresh button
```

**Main Method: `_render_graph()`** (lines 1582-1649)

This method:
1. Gets the VerticalScroll container
2. Clears previous content
3. Filters commits based on search
4. For each commit:
   - Validates commit data
   - Renders using `GitGraphRenderer.render_commit_line()`
   - Creates a `CommitGraphLine` widget
   - Mounts it into the scroll container

**Code Flow**:
```python
def _render_graph(self) -> None:
    """Render the graph to the scroll container."""
    scroll = self.query_one("#graph-scroll", VerticalScroll)
    scroll.remove_children()  # Clear previous
    
    commits = self.graph.get_commits_in_order()
    filtered_commits = [c for c in commits if self.filter.matches(c)]
    
    for commit in filtered_commits[:self.filter.max_commits]:
        # Render the commit line
        content = self.renderer.render_commit_line(commit)
        
        # Create widget
        line = CommitGraphLine(commit, content)
        
        # Mount into scroll container
        scroll.mount(line)
```

---

### 4. CSS Styling (`octogit/style.tcss`)

**Scroll Container Styling** (implied in commit_graph.py CSS):
```css
CommitGraphWidget #graph-scroll {
    width: 100%;
    height: 1fr;
    border: solid #6c7086;
    background: transparent;
    overflow-y: auto;
}
```

---

## Color Scheme

### Branch Depth Colors
- **L0 (Main)**: Blue (`#89b4fa`)
- **L1 (First branch)**: Green (`#a6e3a1`)
- **L2 (Second branch)**: Yellow (`#f9e2af`)
- **L3 (Third branch)**: Purple (`#cba6f7`)
- **L4+ (Deeper)**: Cyan (`#94e2d5`)

### Special Commits
- **HEAD**: Bright green (`#a6e3a1`) with ➤ indicator
- **Merge**: Red (`#f38ba8`) with ◆ indicator
- **Regular**: Depth-based color with ● indicator

---

## Rendering Flow Diagram

```
1. User opens Graph tab (Tab 6)
   ↓
2. CommitGraphWidget.on_mount()
   ↓
3. refresh_graph() builds commit graph
   ↓
4. _render_graph() iterates through commits
   ↓
5. For each commit:
   - GitGraphRenderer.render_commit_line() formats it
   - CommitGraphLine widget is created with formatted content
   - Widget is mounted into #graph-scroll container
   ↓
6. User moves mouse over a commit line
   ↓
7. CSS :hover pseudo-class triggers
   ↓
8. CommitGraphLine background changes to #1a1b26
```

---

## Keyboard Scrolling Integration

**File**: `octogit/commit_graph.py`
**Method**: `key()` (lines 1660-1698)

This method:
1. Intercepts keyboard events (j/k/up/down/home/end/pageup/pagedown)
2. Gets the VerticalScroll widget
3. Calls appropriate scroll methods
4. Returns True to consume the event

**Supported Keys**:
- `k` or `↑`: Scroll up 3 lines
- `j` or `↓`: Scroll down 3 lines
- `Home`: Jump to top
- `End`: Jump to bottom
- `PageUp`: Scroll up one screen
- `PageDown`: Scroll down one screen

---

## File Organization Summary

| File | Purpose | Key Classes/Methods |
|------|---------|-------------------|
| `commit_graph.py` | Core graph rendering | `GitGraphRenderer`, `CommitGraphWidget`, `CommitGraphLine` |
| `git_diff_viewer.py` | Main TUI app | Help documentation updated with keyboard shortcuts |
| `style.tcss` | Styling | CSS rules for scrolling and highlighting |
| `graph_data.py` | Data structures | `CommitGraph`, `CommitNode`, `GraphFilter` |
| `graph_layout.py` | Layout engine | `GraphLayoutEngine` for building commit graph |

---

## How to Modify Rendering

### Change Colors
Edit lines 70-95 in `commit_graph.py`:
```python
self.depth_colors = [
    "#YOUR_COLOR_1",
    "#YOUR_COLOR_2",
    # etc.
]
```

### Change Symbols
Edit lines 40-46 in `commit_graph.py`:
```python
self.COMMIT_DOT = "●"  # Change to any unicode character
self.MERGE_DOT = "◆"   # Change to any unicode character
self.HEAD_INDICATOR = "➤"  # Change to any unicode character
```

### Change Highlight Color
Edit line 1446 in `commit_graph.py`:
```python
CommitGraphLine:hover {
    background: #1a1b26;  # Change this color
}
```

### Change Scroll Speed
Edit line 1675 or 1679 in `commit_graph.py`:
```python
scroll.scroll_up(lines=3, animate=False)  # Change 3 to different number
```

---

## Testing Rendering

To verify rendering is working:

1. Open Graph tab: Press `6`
2. Check that:
   - Commits appear with correct colors
   - HEAD commit is highlighted in green
   - Merge commits appear in red
   - Hover highlighting works (background changes on mouse over)
   - Scrolling works with keyboard (j/k/arrows/home/end)
   - Search filters commits (press `/`)

For debugging:
- Add print statements to `render_commit_line()` to see formatted output
- Use browser dev tools to inspect CSS if styling issues occur
- Check console for rendering errors in Textual logs

