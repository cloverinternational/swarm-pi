# Technical Reference: Tree Scrolling, Diff View, and Commit History

## Architecture Overview

```
GitDiffViewer (Main App)
├── GitDiffHistoryTabs (Container with tabs)
│   ├── Status Tab
│   ├── Files Tab (Tree with VerticalScroll)
│   ├── Unstaged Tab (Diff view)
│   ├── Staged Tab (Diff view)
│   ├── Commit Tab
│   ├── Graph Tab
│   ├── History Tab (NEW: EnhancedCommitHistoryWidget)
│   │   ├── CommitHistoryTable (table with columns)
│   │   └── CommitDetailsPanel (side panel)
│   └── Settings Tab
└── Status Bar
```

---

## File: Tree Scrolling

### Location
- **Primary:** `/octogit/git_diff_viewer.py` (lines 118-122)
- **Styling:** `/octogit/style.tcss` (lines 400-420)

### Components

#### VerticalScroll Container
```python
with TabPane("Files", id="tree-tab"):
    yield VerticalScroll(
        Static("[dim italic]Hint: press [bold]e[/bold] to open your configured editor...[/dim italic]", 
               classes="hint-text"),
        Tree("Files", id="file-tree"),
        id="tree-content"
    )
```

**Key Properties:**
- `height: 1fr` - Fills available vertical space
- `overflow-y: auto` - Automatic vertical scrolling
- Contains hint text and Tree widget

#### Tree Widget
```python
Tree("Files", id="file-tree")
```

**Features:**
- Hierarchical file/folder display
- Expand/collapse with arrow keys
- Keyboard navigation: ↑↓ for movement, → to expand, ← to collapse
- Mouse scroll support

#### CSS Styling
```tcss
#tree-content {
    width: 100%;
    height: 1fr;              /* Fill available space */
}

#file-tree {
    width: 100%;
    height: auto;             /* Auto size, scroll if needed */
    min-height: 10;           /* Minimum height for scrolling */
    background: transparent;
    padding: 1;
}
```

### How Scrolling Works

1. **Container Size:** VerticalScroll set to `height: 1fr` (flexible)
2. **Content Size:** Tree has `height: auto` (grows with content)
3. **Overflow:** When content > container, scrollbar appears
4. **Navigation:** 
   - Keyboard: ↑↓ keys move cursor
   - Mouse: Scroll wheel moves viewport
   - `ensureVisible()` auto-scrolls to keep cursor visible

### Data Flow

```
populate_file_tree()
  ↓
git_sidebar.collect_file_data()
  ├─ staged_files: Set[str]
  ├─ unstaged_files: Set[str]
  └─ untracked_files: Set[str]
  ↓
Build tree structure
  ├─ Directory nodes (with folder icon)
  ├─ File nodes (with status indicator)
  │   ├─ Green ✓ = staged
  │   ├─ Red ● = unstaged
  │   └─ Purple ◐ = both
  ↓
Tree.add_leaf(label, data=file_path)
```

### Keyboard Controls

| Key | Action |
|-----|--------|
| ↑/k | Move up in tree |
| ↓/j | Move down in tree |
| →/l | Expand folder |
| ←/h | Collapse folder |
| Home/g | Go to first item |
| End/G | Go to last item |
| Mouse wheel | Scroll viewport |

---

## File: Diff View

### Location
- **Primary:** `/octogit/git_diff_viewer.py` (lines 1176-1272)
- **Markdown:** `/octogit/diff_markdown.py`
- **Styling:** `/octogit/style.tcss` (lines 49-52, 278-342)

### Components

#### DiffMarkdown Widget
```python
markdown_widget = DiffMarkdown(
    file_path=file_path,
    hunks=[hunk],
    config=markdown_config,
)
```

**Features:**
- Syntax highlighting for code blocks
- Language-specific lexer selection
- Line-by-line formatting
- Colored additions/removals

#### Hunk Structure
```python
@dataclass
class Hunk:
    header: str          # "@@ -10,5 +10,8 @@"
    lines: List[str]     # Individual diff lines
```

**Line Format:**
```
 context line (no change)
-removed line
+added line
```

#### Hunk Rendering Pipeline

```python
for i, hunk in enumerate(hunks):
    # 1. Create header widget
    hunk_header = Static(hunk.header, classes="hunk-header")
    
    # 2. Create markdown widget for syntax highlighting
    markdown_widget = DiffMarkdown(
        file_path=file_path,
        hunks=[hunk],
        config=markdown_config,
    )
    
    # 3. Create action buttons
    if is_staged:
        buttons = [Button("Unstage", ...)]
    else:
        buttons = [Button("Stage", ...), Button("Discard", ...)]
    
    # 4. Mount as container
    hunk_container = Container(
        hunk_header,
        markdown_widget,
        *buttons,
        classes="hunk-container"
    )
    
    diff_content.mount(hunk_container)
```

### CSS Styling

```tcss
.hunk-header {
    background: transparent;
    color: $text;
    text-style: bold;
    padding: 0;
}

.added {
    background: #9ece6a 60%;   /* Green with transparency */
    color: black;
    width: 100%;
    padding: 0 1;
    height: auto;
}

.removed {
    background: #d3869b 60%;   /* Red/pink with transparency */
    color: white;
    width: 100%;
    padding: 0 1;
    height: auto;
}

.unchanged {
    background: transparent;
    width: 100%;
    padding: 0 1;
    height: auto;
}

.hunk-buttons {
    width: 100%;
    height: auto;
    align: center middle;
    padding: 1 0;
}

.stage-button, .unstage-button {
    width: 12;
    margin: 0 1;
    height: auto;
    border: solid #6c7086;
    background: transparent;
}

.stage-button:hover, .unstage-button:hover {
    background: #9ece6a 60%;
}

.discard-button {
    width: 12;
    margin: 0 1;
    height: auto;
    border: solid #6c7086;
    background: transparent;
}

.discard-button:hover {
    background: #d3869b 60%;
}
```

### Rendering Flow

```
display_file_diff(file_path, is_staged)
  ↓
Clear previous content
  ↓
Get diff hunks
  hunks = git_sidebar.get_diff_hunks(file_path, staged=is_staged)
  ↓
For each hunk:
  ├─ Create hunk_header Static widget
  ├─ Create DiffMarkdown with syntax highlighting
  ├─ Create action buttons
  └─ Mount all in Container
  ↓
VerticalScroll automatically handles overflow
```

### Data Structures

#### DiffMarkdownConfig
```python
@dataclass
class DiffMarkdownConfig:
    repo_root: Path                    # Root directory
    prefer_diff_language: bool = False # Use diff syntax highlighting
    show_headers: bool = False         # Show hunk headers
```

#### CommitLine (unused but available)
```python
class CommitLine(Static):
    """Widget for displaying a commit line."""
    DEFAULT_CSS = """
    CommitLine {
        width: 100%;
        height: 1;
        overflow: hidden hidden;
    }
    """
```

---

## File: Commit History Table (NEW)

### Location
- **Primary:** `/octogit/commit_history_panel.py`
- **Integration:** `/octogit/git_diff_viewer.py` (lines ~1149+)

### Class Hierarchy

```
EnhancedCommitHistoryWidget (Container)
├── CommitHistoryTable (Static)
│   └── renders table with columns
└── CommitDetailsPanel (Static)
    └── renders commit metadata
```

### CommitHistoryTable

#### Data Structure
```python
class CommitHistoryTable(Static):
    commits: List[CommitInfo]      # From git_status_sidebar
    selected_index: int            # Currently selected row
    table_width: int               # Width in characters
    columns: List[TableColumn]     # Column definitions
```

#### Column Definition
```python
@dataclass
class TableColumn:
    name: str              # Display name ("Date", "Author", etc.)
    width: Optional[int]   # Fixed width or None for flexible
    style: str            # Color style (cyan, green, yellow, white)
```

#### Rendering

**Header:**
```
Date        │ Author          │ SHA      │ Message
```

**Row (normal):**
```
2024-01-30  │ Alice Smith     │ abc1def2 │ Fix authentication bug
```

**Row (selected):**
```
[reverse]2024-01-30  │ Alice Smith     │ abc1def2 │ Fix auth...[/reverse]
```

#### Key Methods

```python
def render(self) -> str:
    """Render the complete table with all rows."""
    # Returns formatted string with table content
    
def update_selection(self, index: int) -> None:
    """Update selected row and refresh."""
    
def get_selected_commit(self) -> CommitInfo:
    """Get the currently selected commit object."""
```

### CommitDetailsPanel

#### Display Format

```
Commit Details
──────────────────────────────────

SHA
  abc1def2f5e8d7c6b5a4f3e2d1c0b9a8

Author
  Alice Smith

Date
  2024-01-30 15:23:45

Message
  Fix authentication bug in
  OAuth handler

─ End of Details
```

#### Key Methods

```python
def render(self) -> str:
    """Render commit details."""
    # Returns formatted string with commit info
    
def update_commit(self, commit: CommitInfo) -> None:
    """Update displayed commit and refresh."""
```

### EnhancedCommitHistoryWidget

#### Layout Structure

```
Horizontal Layout
├─ Container(width=1fr)
│  └─ CommitHistoryTable
└─ CommitDetailsPanel(width=40)
```

#### Key Methods

```python
def __init__(self, commits: List[CommitInfo]):
    """Initialize with list of commits."""
    
def compose(self) -> ComposeResult:
    """Create the Horizontal layout."""
    
def handle_key(self, key: str) -> None:
    """Handle up/down/j/k navigation."""
    # Updates table selection and details panel
    
def get_selected_commit(self) -> CommitInfo:
    """Get currently selected commit."""
```

### Integration Points

#### Data Source
```python
# From git_status_sidebar.py
@dataclass
class CommitInfo:
    sha: str              # Full commit hash
    message: str          # Commit message
    author: str           # Author name
    date: datetime        # Commit datetime
```

#### Usage in git_diff_viewer.py
```python
def populate_commit_history(self) -> None:
    commits = self.git_sidebar.get_commit_history(max_count=200)
    self.commit_history_widget = EnhancedCommitHistoryWidget(commits)
    history_content.mount(self.commit_history_widget)

def action_navigate_commits_up(self) -> None:
    self.commit_history_widget.handle_key("up")
```

---

## Navigation Flow

### Unified Navigation Model

```
User Input (Keyboard/Mouse)
  ↓
GitDiffViewer.on_key() or TabPane.key()
  ↓
Action Router (based on active tab)
  ├─ Tree Tab → Tree navigation
  ├─ Diff Tab → Scroll/button focus
  ├─ History Tab → Table navigation
  └─ Other Tab → Default handling
  ↓
Widget State Update
  ├─ Tree: cursor position
  ├─ Diff: scroll offset
  └─ History: selected_index
  ↓
Widget Refresh (redraw)
  ├─ Tree: View() called
  ├─ Diff: auto-scroll container
  └─ History: render() called + details.render()
  ↓
Terminal Output Updated
```

### Keyboard Binding Resolution

```
Raw Key Event
  ↓
BINDINGS lookup (if any)
  └─ action_* method called
  ↓
Fallback: on_key() handler
  ├─ Check active tab
  ├─ Route to widget-specific handler
  └─ Update state + refresh
```

---

## Performance Characteristics

### Tree Scrolling
- **Memory:** ~1-10 MB for 10,000 files
- **Render:** O(visible_height) - only shows visible rows
- **Navigation:** O(1) - cursor position lookup
- **Bottleneck:** Git operation (git status)

### Diff View
- **Memory:** ~1-50 MB per file (depends on file size)
- **Render:** O(hunk_count) - renders all hunks
- **Navigation:** O(1) - scroll container handles
- **Bottleneck:** Syntax highlighting for large files

### Commit History Table
- **Memory:** ~1-2 MB per 1000 commits
- **Render:** O(visible_rows) - only shows visible rows
- **Navigation:** O(1) - index lookup
- **Bottleneck:** Git operation (commit fetch)

### Optimization Tips

```python
# 1. Reduce commit history
commits = git_sidebar.get_commit_history(max_count=100)

# 2. Lazy-load file tree
def populate_file_tree(self):
    # Load directories on-demand, not all files

# 3. Cache diff rendering
self.diff_cache = {}
def display_file_diff(self, file_path):
    if file_path in self.diff_cache:
        return self.diff_cache[file_path]

# 4. Pagination for large histories
def load_more_commits(self):
    older_commits = git_sidebar.get_commit_history(
        since=self.oldest_commit.sha,
        max_count=100
    )
    self.commits.extend(older_commits)
    self.table.update(older_commits)
```

---

## Error Handling

### Tree Population
```python
try:
    populate_file_tree()
except Exception as e:
    tree.root.add_leaf(f"Error: {e}")
    notify(f"Error loading file tree: {e}")
```

### Diff Display
```python
try:
    display_file_diff(file_path)
except Exception as e:
    diff_content.mount(Static(f"Error: {e}", classes="error"))
    notify(f"Error displaying diff: {e}")
```

### Commit History
```python
try:
    populate_commit_history()
except Exception as e:
    history_content.mount(Static(f"Error: {e}", classes="error"))
    notify(f"Error loading history: {e}")
```

---

## Testing Checklist

- [ ] Tree scrolling with 100+ files
- [ ] Diff view with various file types (.py, .js, .go, .txt, etc.)
- [ ] Commit history with 1000+ commits
- [ ] Navigation in each tab
- [ ] Keyboard shortcuts
- [ ] Mouse scrolling
- [ ] Window resizing
- [ ] Copy/paste in diff view
- [ ] Stress test: large files (>1MB)
- [ ] Edge cases: empty directories, binary files, etc.

---

## References

- **CommitInfo:** `/octogit/git_status_sidebar.py` (line 29-40)
- **GitStatusSidebar:** `/octogit/git_status_sidebar.py` (line 43+)
- **DiffMarkdown:** `/octogit/diff_markdown.py`
- **CommitGraphWidget:** `/octogit/commit_graph.py`
- **Styling:** `/octogit/style.tcss`
- **Main App:** `/octogit/git_diff_viewer.py`
- **New Components:** `/octogit/commit_history_panel.py`

