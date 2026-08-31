# Git TUI - Quick Visual Guide

## Layout Overview

### Files Tab (Tab 2) - Improved Scrolling

```
1 Status  2 Files  3 Unstaged  4 Staged  5 Commit  6 Graph  7 History

📁 Files
Press [j/k] to navigate, [Ctrl+u/d] for page, [g/G] for top/bottom

  src/
  src/main.go              ← Currently selected (highlighted)
  src/utils.go
  src/handlers.go
  tests/
  ↓ 28 more below

j/k:navigate Ctrl+u/d:page g/G:top/bottom
```

### Unstaged/Staged Tabs - With Diff View

```
Without Diff (press 'd' to view):
📝 Unstaged Changes
Press [s] to stage, [d] or [enter] to view diff, [q] to exit diff

  M main.go           ← Selected
  M utils.go
  A new_file.txt

With Diff (shows after pressing 'd'):
main.go

-5      func oldFunction() {
+5      func newFunction() {
 6          return value
 7      }
-10     // Old comment
+10     // New comment

j/k:scroll n/p:hunk ↑/↓ Ctrl+u/d:page g/G:top/bottom q/esc:exit
```

### History Tab - Table View

```
📜 Commit History

SHA       DATE        AUTHOR           MESSAGE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
a1b2c3d   2024-01-30  Alice           Add new feature ← Selected
d4e5f6g   2024-01-29  Bob             Fix bug
h7i8j9k   2024-01-28  Carol           Update docs
↓ 42 more below

j/k:navigate ↑/↓ Ctrl+u/d:page g/G:top/bottom r:refresh
```

### History Tab - With Detail Panel (Press Enter)

```
SHA       DATE        AUTHOR           │ 📋 Details
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━│━━━━━━━━━━━━━━━━━━━
a1b2c3d   Add feature  ← Selected     │ SHA:
d4e5f6g   Fix bug                    │   a1b2c3d7890abcd...
h7i8j9k   Update docs                │
                                      │ Author:
                                      │   Alice <alice@example.com>
                                      │
                                      │ Date:
                                      │   2024-01-30 14:23:45
                                      │
                                      │ Message:
                                      │   Add new feature to improve
                                      │   user experience with better
                                      │   navigation support

Tab: Switch between list/detail focus
```

## Keybinding Cheat Sheet

### Movement
```
┌─ Basic Movement ─┬─────────────────────┐
│ j / ↓            │ Down                 │
│ k / ↑            │ Up                   │
│ Ctrl+d / PgDn    │ Page Down (faster)   │
│ Ctrl+u / PgUp    │ Page Up (faster)     │
│ g / Home         │ Jump to Top          │
│ G / End          │ Jump to Bottom       │
└──────────────────┴─────────────────────┘
```

### Tab Switching
```
1 = Status          5 = Commit
2 = Files           6 = Graph
3 = Unstaged        7 = History
4 = Staged
```

### File Operations
```
s = Stage file          u = Unstage file
d/Enter = View diff     q/Esc = Exit diff
a = Stage all           x = Unstage all
c = Open commit
```

### History-Specific
```
Enter = Toggle detail panel
Tab = Switch panels (in detail view)
n = Next hunk (in diff)
N = Previous hunk (in diff)
```

### Remote
```
p = Push       o = Pull       f = Fetch       r = Refresh
```

## Common Workflows

### Workflow: Stage and Commit Changes

```
1. Press 3                  → Go to Unstaged tab
2. j/k navigate            → Find file to stage
3. Press Enter             → View diff preview
4. j/k scroll              → Review changes
5. Press q                 → Exit diff
6. Press s                 → Stage file
7. Repeat 2-6             → For other files
8. Press c                 → Open commit
9. Type message            → Enter commit message
10. Press Enter            → Commit!
```

### Workflow: Review Commit History

```
1. Press 7                 → Go to History tab
2. j/k navigate            → Find commit
3. Press Enter             → Open detail panel
4. j/k scroll              → Read full message
5. Press Tab               → Switch to list
6. Ctrl+d                  → Jump to older commits
7. Press g                 → Jump to newest
```

### Workflow: Find and Stage Specific File

```
1. Press 2                 → Go to Files tab
2. Ctrl+d page down        → Find section
3. j/k navigate            → Locate file
4. Note filename           → Remember the path
5. Press 3                 → Go to Unstaged
6. j/k navigate            → Find same file
7. Press s                 → Stage it
```

## Color Coding

```
═══════════════════════════════════════════════════════════════

🔵 Accent (Bright):     SHA hashes, headers, key info
🟢 Success (Green):     Added files, staged changes  
🟡 Warning (Orange):    Modified files, in-progress
🔴 Error (Red):         Deleted files, errors
⚪ Normal (White):      Regular text, content
⚫ Dim (Gray):          Helper text, secondary info

═══════════════════════════════════════════════════════════════
```

## Status Bar Examples

```
Different contexts show different hints:

[In regular file list]
j/k:navigate s:stage u:unstage d/enter:diff a:stage-all x:unstage-all c:commit

[In diff view]
j/k:scroll n/p:hunk ↑/↓ Ctrl+u/d:page g/G:top/bottom q/esc:exit

[In history]
j/k:navigate ↑/↓ Ctrl+u/d:page g/G:top/bottom r:refresh

[In files tab]
j/k:navigate Ctrl+u/d:page g/G:top/bottom
```

## Scroll Indicators

```
↑ 5 more above       ← More commits above current view
Content here         
↓ 15 more below      ← More commits below current view
```

## Tips & Tricks

### Fast Navigation
- Use `Ctrl+d/u` (page half) to jump quickly through long lists
- Use `g` / `G` for instant top/bottom jumps
- Use number keys 1-7 to instantly switch tabs

### Efficient Staging
- Press `a` to stage all changes at once
- Then use `u` on specific files to unstage unwanted changes
- This is faster than staging files individually

### Code Review
- Open detail panel in History to read full commit messages
- Use diff view to understand what each commit changed
- Switch between list and detail with Tab key

### Large Repositories
- Use Ctrl+u/d to jump through commits faster
- Search by scrolling is linear, so jump strategically
- The history auto-refreshes when you switch tabs

---

**Pro Tip**: Most operations can be chained together:
1. Navigate to file (j/k)
2. View diff (d)
3. Scroll diff (j/k)
4. Exit diff (q)
5. Stage file (s)

All with keyboard only - no mouse needed!
