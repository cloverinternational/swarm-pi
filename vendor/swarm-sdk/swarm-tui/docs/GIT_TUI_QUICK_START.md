# Quick Reference: Git TUI Features

## Tree List Scrolling ✅

**Location:** Tab `2` (Files)

| Key | Action |
|-----|--------|
| ↑ or k | Move up in tree |
| ↓ or j | Move down in tree |
| → or l | Expand folder |
| ← or h | Collapse folder |
| Home/g | First item |
| End/G | Last item |
| Mouse wheel | Scroll viewport |
| Click file | Select file |

**Visual:**
```
📁 src/
  ├─ 🐍 main.py              [staged ✓]
  ├─ 🐍 utils.py              [unstaged ●]
  └─ 🐍 config.py             [both ◐]
📁 tests/
  ├─ 🐍 test_main.py          [clean]
  └─ ...
```

**Status Indicators:**
- ✓ Green = staged
- ● Red = unstaged  
- ◐ Purple = both staged and unstaged
- (none) = unmodified

---

## Diff View ✅

**Location:** Tab `3` (Unstaged) or Tab `4` (Staged)

**Colors:**
- 🟢 Green background = added line
- 🔴 Red/pink background = removed line
- ⚪ White = unchanged context

**Actions:**
- Click `[Stage]` → Stage this hunk
- Click `[Discard]` → Discard this hunk
- Click `[Unstage]` → Unstage this hunk

**Example:**
```
@@ -10,5 +10,8 @@
 def hello():                     ← context (unchanged)
-    print("old")               ← removed (red)
+    print("new")               ← added (green)
+    return True
 
 def world():                     ← context (unchanged)
```

---

## Commit History Table ✅ (NEW)

**Location:** Tab `7` (History)

**Layout:**
```
┌──────────────┬─────────────────┬──────────┬──────────────────────┐
│ Date         │ Author          │ SHA      │ Message              │
├──────────────┼─────────────────┼──────────┼──────────────────────┤
│ 2024-01-30   │ Alice Smith     │ abc1def2 │ Fix authentication...│ ← selected (highlighted)
│ 2024-01-29   │ Bob Jones       │ def3ghi4 │ Add new feature      │
│ 2024-01-28   │ Carol White     │ ghi5jkl6 │ Update dependencies  │
└──────────────┴─────────────────┴──────────┴──────────────────────┘

Commit Details
──────────────────────────
SHA
  abc1def2f5e8d7c6b5a4f3e2d1c0b9a8

Author
  Alice Smith

Date
  2024-01-30 15:23:45

Message
  Fix authentication bug in OAuth
  handler. Added proper timeout...
```

| Key | Action |
|-----|--------|
| ↑ or k | Previous commit |
| ↓ or j | Next commit |
| Mouse wheel | Scroll table |
| Click row | Select commit |

**Auto-update:**
- Select a commit row
- Details panel updates automatically
- Shows full SHA, author, date, message

---

## Global Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `1` | Go to Status tab |
| `2` | Go to Files tab (tree) |
| `3` | Go to Unstaged tab (diff) |
| `4` | Go to Staged tab (diff) |
| `5` | Go to Commit tab |
| `6` | Go to Graph tab |
| `7` | Go to History tab |
| `8` | Go to Settings tab |
| `h` | Show help |
| `q` | Quit application |
| `r` | Refresh branches |
| `b` | Switch branch |
| `s` | Stage selected file |
| `u` | Unstage selected file |
| `a` | Stage all changes |
| `x` | Unstage all changes |
| `e` | Edit file in external editor |
| `p` | Push changes |
| `o` | Pull changes |
| `c` | Show commit tab |
| `g` | GAC: Generate commit message |
| `Ctrl+G` | GAC: Configure AI provider |

---

## File Status Indicators

### In Tree View
```
✓ Green  = Staged for commit
● Red    = Modified (unstaged)
◐ Purple = Both staged and modified
(none)   = Unmodified
```

### In Commit History
```
Date: YYYY-MM-DD format
Author: Author name
SHA: First 8 chars of commit hash
Message: First ~40 chars (truncated)
```

---

## Common Workflows

### Stage and Commit a File

1. Press `2` (Files tab)
2. Navigate with ↑↓ to find file
3. Press `s` (Stage selected file)
4. Press `5` (Commit tab)
5. Enter commit message
6. Click `[Commit]` button

### View What Changed

1. Press `3` (Unstaged tab)
2. See all unstaged changes with diff
3. Click `[Stage]` to stage hunks
4. Click `[Discard]` to discard hunks

### Review Commit History

1. Press `7` (History tab)
2. Navigate with ↑↓ or j/k
3. Side panel shows selected commit details
4. View date, author, message

### Push to Remote

1. Make changes and stage
2. Press `5` and commit with message
3. Press `p` (Push changes)
4. Follow any prompts

---

## Tips & Tricks

### Tree Navigation
- Press `Home` to go to top of tree
- Press `End` to go to bottom
- Press `→` to expand all folders
- Scroll wheel works when tree is focused

### Diff Review
- Hover over buttons to see what they do
- Each hunk can be staged/discarded separately
- Colors make it easy to spot changes

### History Review
- Use j/k for faster commit navigation
- Details panel shows full commit info
- Selected commit highlighted in reverse video

### File Editing
- Press `e` to open file in your editor
- Set editor in Settings tab (Tab `8`)
- Common editors: `code`, `vim`, `nano`, `gedit`

---

## Troubleshooting

### Tree Not Scrolling
- Make sure tree is focused (click on it)
- Use scroll wheel or arrow keys
- Tree auto-scrolls to keep selection visible

### Diff Not Showing
- Check that files have changes
- "No changes to display" = file is unchanged
- Refresh with `r` key

### Commit History Empty
- Repository might not have commits yet
- Try `git log` in terminal to verify
- Check current branch has commits

### Buttons Not Responding
- Click directly on button text
- Use Enter key after selecting button with Tab
- Refresh with `r` key

---

## Color Scheme

**Dark Tokyo Night Theme:**
- 🟣 Purple (`#bb9af7`) - Primary accent, headings
- 🟢 Green (`#9ece6a`) - Success, staged files
- 🔴 Red (`#f7768e`) - Changes, unstaged files
- 🟡 Yellow (`#f9e2af`) - Warnings, important
- 🔵 Blue (`#89b4fa`) - Info, commit SHA
- ⚪ White (`#cdd6f4`) - Text, content
- ⚫ Dark (`#0f111e`) - Background

---

## Getting Help

- Press `h` to show keybindings help
- Check Settings tab (Tab `8`) for options
- All buttons show hover tooltips
- Each section has hint text

---

## Next: Integration Steps

To use the new commit history table:

1. Open `/docs/GIT_TUI_INTEGRATION_GUIDE.md`
2. Follow the 7 step integration process
3. Test with: `python main.py`
4. Navigate to Tab `7` (History)
5. Enjoy the new table view!

---

## File Locations

```
octogit/
├── git_diff_viewer.py           ← Main app (update this)
├── commit_history_panel.py       ← New component (add this)
├── diff_markdown.py              ← Syntax highlighting
├── commit_graph.py               ← Graph visualization
├── git_status_sidebar.py         ← Git operations
├── style.tcss                    ← Styling
└── ...

docs/
├── IMPROVEMENTS_SUMMARY.md       ← What changed (start here)
├── GIT_TUI_IMPROVEMENTS.md       ← Details & architecture
├── GIT_TUI_INTEGRATION_GUIDE.md  ← How to integrate
├── GIT_TUI_TECHNICAL_REFERENCE.md ← Technical deep-dive
└── GIT_TUI_QUICK_START.md        ← This file
```

---

**Everything is ready to use! 🚀**

Start with the IMPROVEMENTS_SUMMARY.md for a complete overview of what was done.
