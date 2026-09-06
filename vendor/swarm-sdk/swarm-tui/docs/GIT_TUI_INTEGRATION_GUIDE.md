# Integration Guide: Enhanced Commit History Table

## Quick Start

This guide shows how to integrate the new `EnhancedCommitHistoryWidget` into `git_diff_viewer.py`.

## Step 1: Import the New Components

**File:** `/octogit/git_diff_viewer.py`

Add at the top with other imports (around line 31):

```python
from octogit.commit_history_panel import (
    EnhancedCommitHistoryWidget,
    CommitHistoryTable,
    CommitDetailsPanel,
)
```

## Step 2: Add Widget Reference to GitDiffViewer Class

**File:** `/octogit/git_diff_viewer.py`

In the `__init__` method, add (around line 520):

```python
def __init__(self, repo_path: str | None = None):
    super().__init__()
    # ... existing code ...
    self.commit_history_widget = None  # NEW: Reference to history widget
```

## Step 3: Update populate_commit_history() Method

**File:** `/octogit/git_diff_viewer.py` (lines 1149-1172)

Replace the existing `populate_commit_history()` method with:

```python
@profile
def populate_commit_history(self) -> None:
    """Populate the commit history tab with enhanced table widget."""
    try:
        history_content = self.query_one("#history-content", VerticalScroll)
        history_content.remove_children()

        commits = self.git_sidebar.get_commit_history(max_count=200)
        
        if not commits:
            history_content.mount(
                Static("No commit history available", classes="info")
            )
            return

        # Create and mount the enhanced history widget
        self.commit_history_widget = EnhancedCommitHistoryWidget(commits)
        history_content.mount(self.commit_history_widget)
        
    except Exception as e:
        self.notify(f"Error loading commit history: {e}", severity="error")
```

## Step 4: Add Keyboard Navigation Handler

**File:** `/octogit/git_diff_viewer.py`

Add this new method to the `GitDiffViewer` class (around line 1820):

```python
def action_navigate_commits_up(self) -> None:
    """Navigate to previous commit in history."""
    if self.commit_history_widget is None:
        return
    
    new_idx = max(0, self.commit_history_widget.table.selected_index - 1)
    self.commit_history_widget.table.update_selection(new_idx)
    selected = self.commit_history_widget.get_selected_commit()
    self.commit_history_widget.details.update_commit(selected)

def action_navigate_commits_down(self) -> None:
    """Navigate to next commit in history."""
    if self.commit_history_widget is None:
        return
    
    new_idx = min(
        len(self.commit_history_widget.commits) - 1,
        self.commit_history_widget.table.selected_index + 1
    )
    self.commit_history_widget.table.update_selection(new_idx)
    selected = self.commit_history_widget.get_selected_commit()
    self.commit_history_widget.details.update_commit(selected)
```

## Step 5: Update BINDINGS (Optional)

**File:** `/octogit/git_diff_viewer.py` (lines 467-501)

You can add explicit bindings for commit navigation if desired:

```python
BINDINGS = [
    # ... existing bindings ...
    # For history tab navigation (add if you want explicit keys)
    # ("shift+up", "navigate_commits_up", "Previous Commit"),
    # ("shift+down", "navigate_commits_down", "Next Commit"),
]
```

Or let arrow keys work contextually (recommended - they already work in trees/lists).

## Step 6: Handle Global Key Events (if needed)

If you want to handle history navigation without explicit bindings, add to the main key handler:

**File:** `/octogit/git_diff_viewer.py`

In the `on_key` or `handle_key` method, add (around line 800-850):

```python
def on_key(self, event: Key) -> None:
    """Handle key events."""
    key = event.key
    
    # Get current active tab
    try:
        tabs = self.query_one("#main-tabs", TabbedContent)
        active_tab = tabs.active_tab.id if tabs.active_tab else None
    except:
        return

    # Handle history tab navigation
    if active_tab == "history-tab" and self.commit_history_widget:
        if key in ["up", "k"]:
            self.action_navigate_commits_up()
            return
        elif key in ["down", "j"]:
            self.action_navigate_commits_down()
            return
```

## Step 7: Add CSS for New Components (Optional)

**File:** `/octogit/style.tcss` (at the end)

Add styling for the new components:

```tcss
/* Enhanced Commit History */
CommitHistoryTable {
    width: 1fr;
    height: 1fr;
    background: #0f111e;
    color: #cdd6f4;
}

.commit-row {
    width: 100%;
    height: auto;
    padding: 0 1;
    background: transparent;
}

.commit-row.selected {
    background: #313244;
}

CommitDetailsPanel {
    width: 40;
    height: 1fr;
    border: solid #6c7086;
    background: #0f111e;
    padding: 1;
    color: #cdd6f4;
}

.detail-label {
    color: #9ece6a;
    text-style: bold;
}

.detail-value {
    color: #cdd6f4;
}

/* Container for table + panel */
#commit-table-container {
    width: 1fr;
    height: 1fr;
}
```

## Complete Code Example

Here's what the integration looks like in context:

```python
# At top of file
from octogit.commit_history_panel import EnhancedCommitHistoryWidget

class GitDiffViewer(App):
    """Main application."""
    
    def __init__(self, repo_path: str | None = None):
        super().__init__()
        # ... existing init code ...
        self.commit_history_widget = None  # NEW
    
    @profile
    def populate_commit_history(self) -> None:
        """Populate the commit history tab with enhanced table widget."""
        try:
            history_content = self.query_one("#history-content", VerticalScroll)
            history_content.remove_children()

            commits = self.git_sidebar.get_commit_history(max_count=200)
            
            if not commits:
                history_content.mount(
                    Static("No commit history available", classes="info")
                )
                return

            # Create and mount the enhanced history widget
            self.commit_history_widget = EnhancedCommitHistoryWidget(commits)
            history_content.mount(self.commit_history_widget)
            
        except Exception as e:
            self.notify(f"Error loading commit history: {e}", severity="error")

    def action_navigate_commits_up(self) -> None:
        """Navigate to previous commit in history."""
        if self.commit_history_widget is None:
            return
        
        new_idx = max(0, self.commit_history_widget.table.selected_index - 1)
        self.commit_history_widget.table.update_selection(new_idx)
        selected = self.commit_history_widget.get_selected_commit()
        self.commit_history_widget.details.update_commit(selected)

    def action_navigate_commits_down(self) -> None:
        """Navigate to next commit in history."""
        if self.commit_history_widget is None:
            return
        
        new_idx = min(
            len(self.commit_history_widget.commits) - 1,
            self.commit_history_widget.table.selected_index + 1
        )
        self.commit_history_widget.table.update_selection(new_idx)
        selected = self.commit_history_widget.get_selected_commit()
        self.commit_history_widget.details.update_commit(selected)
```

## Testing the Integration

1. **Start the application:**
   ```bash
   cd /home/swarm/SwarmCode/TUI/octogit
   python main.py
   ```

2. **Navigate to History tab:** Press `7`

3. **Test navigation:**
   - Press `↑` or `k` to go to previous commit
   - Press `↓` or `j` to go to next commit
   - Check that details panel updates

4. **Verify display:**
   - Table should show Date | Author | SHA | Message columns
   - Details panel should show selected commit's full information
   - Selected row should be highlighted

## Troubleshooting

### Import Error
```
ModuleNotFoundError: No module named 'octogit.commit_history_panel'
```

**Solution:** Ensure `commit_history_panel.py` exists in `/octogit/` directory.

### Widget Not Showing
```
Widget 'EnhancedCommitHistoryWidget' not displayed
```

**Solution:** Check that:
1. `populate_commit_history()` is called
2. `history_content.mount()` is executed
3. Commits list is not empty

### Navigation Not Working
```
Pressing arrow keys doesn't change selection
```

**Solution:** 
1. Ensure you're on History tab (press `7`)
2. Check that `commit_history_widget` is not None
3. Verify key handler is calling the action methods

### Performance Issues
```
Application lags with large commit history
```

**Solution:**
1. Reduce `max_count` in `get_commit_history()`:
   ```python
   commits = self.git_sidebar.get_commit_history(max_count=100)
   ```
2. Implement pagination (load more on demand)
3. Check git operation performance

## Next Steps

Once integrated, you can enhance further:

1. **Add commit filtering**
   ```python
   commits = git_sidebar.get_commit_history(
       max_count=200,
       grep="feature"  # Only commits with "feature" in message
   )
   ```

2. **Show file changes in details**
   ```python
   def update_commit(self, commit):
       # Get list of files changed in this commit
       files = git_sidebar.get_commit_files(commit.sha)
       # Display in details panel
   ```

3. **Preview diff in details panel**
   ```python
   def show_diff_preview(self, commit):
       # Show first few lines of diff
       # Allow expanding to full diff
   ```

4. **Copy SHA to clipboard**
   ```python
   def copy_sha_to_clipboard(self):
       import pyperclip
       sha = self.table.get_selected_commit().sha
       pyperclip.copy(sha)
       self.app.notify("SHA copied to clipboard")
   ```

## References

- **CommitInfo dataclass:** `/octogit/git_status_sidebar.py` (line 29)
- **Git operations:** `/octogit/git_status_sidebar.py`
- **New components:** `/octogit/commit_history_panel.py`
- **Main app:** `/octogit/git_diff_viewer.py`
- **Styling:** `/octogit/style.tcss`

---

## Summary

The integration requires minimal changes:

1. ✅ Add import statement
2. ✅ Store widget reference in class
3. ✅ Update `populate_commit_history()` method
4. ✅ Add navigation action methods
5. ✅ Optional: Add CSS and key handlers

The new widget integrates seamlessly with existing code and provides professional table-based commit history display with interactive side panel!
