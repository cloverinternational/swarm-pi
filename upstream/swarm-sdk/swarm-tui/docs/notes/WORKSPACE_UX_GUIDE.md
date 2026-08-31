# Workspace Mode - Simple UX Guide

## What is Workspace Mode?

Workspace mode lets you view and interact with **multiple conversations side-by-side**. It's perfect for:
- Comparing different approaches
- Working on multiple tasks in parallel
- Keeping context visible while starting new chats

---

## Getting Started

### Enter Workspace Mode
Press `Alt+1`, `Alt+2`, or `Alt+3` to switch between layouts:
- **Alt+1**: Single pane (standard chat)
- **Alt+2**: Two panes side-by-side (dual view)
- **Alt+3**: Three panes (triple view)

### Exit Workspace Mode
Press `Esc` from anywhere to return to normal chat view

---

## Simple Keybinds (Easy to Remember!)

### Navigation (Works Everywhere)

| Key | Action |
|-----|--------|
| `Tab` | Move to next pane (cycles: Sidebar → Pane 1 → Pane 2 → Sidebar) |
| `Shift+Tab` | Move to previous pane (cycles backward) |
| `Esc` | Exit workspace mode |

**Think of it like browser tabs** - Tab moves forward, Shift+Tab moves back!

---

### When Sidebar is Focused (Conversation List)

| Key | Action |
|-----|--------|
| `↑` or `k` | Move up in conversation list |
| `↓` or `j` | Move down in conversation list |
| `Enter` | Open selected conversation in current pane |
| `Tab` | Move to first chat pane |

**Visual clue:** The sidebar has a **bright blue border** when focused

---

### When Chat Pane is Focused

| Key | Action |
|-----|--------|
| `↑` or `k` | Scroll up one line |
| `↓` or `j` | Scroll down one line |
| `Page Up` | Scroll up one page (fast) |
| `Page Down` | Scroll down one page (fast) |
| `Home` | Jump to top of conversation |
| `End` | Jump to bottom of conversation |
| `Enter` | Send message |
| `Tab` | Move to next pane |
| Type normally | Input goes to message box |

**Visual clues:**
- Focused pane has **bright blue border**
- Title shows **● (filled dot)** when focused, **○ (circle)** when not focused
- Bottom shows current keybinds

---

### Special Actions (Work Everywhere)

| Key | Action |
|-----|--------|
| `Ctrl+C` | Stop running agent |
| `n` | New chat in current pane |
| `Alt+1` | Switch to single pane layout |
| `Alt+2` | Switch to dual pane layout |
| `Alt+3` | Switch to triple pane layout |

---

## Visual Feedback (Always Clear!)

### How to Tell Which Pane is Active

1. **Border Color**
   - **Bright blue**: This pane/sidebar is focused
   - **Gray**: Not focused (click or Tab to focus)

2. **Title Indicator**
   - **● Active** (filled dot): This pane is focused
   - **○ Inactive** (empty circle): Not focused

3. **Bottom Status Line**
   - Shows **context-aware** keybinds
   - Focused pane: "Tab:next ↑↓:scroll PgUp/PgDn:page Enter:send Esc:exit"
   - Unfocused pane: "Tab/Shift+Tab:navigate Click:focus Esc:exit"
   - Sidebar: "↑↓:navigate Enter:open Tab:next Esc:exit"

---

## Common Workflows

### Opening Multiple Conversations

1. Press `Alt+2` to enter dual pane mode
2. Use `Tab` to focus the sidebar
3. Use `↑`/`↓` to select a conversation
4. Press `Enter` to open it
5. Press `Tab` to move to the next pane
6. Repeat to open another conversation

### Comparing Two Chats

1. Press `Alt+2` for dual pane layout
2. Open conversation A in left pane
3. Press `Tab` to focus right pane
4. Open conversation B in right pane
5. Use `Tab`/`Shift+Tab` to switch between them
6. Scroll independently with `↑`/`↓`

### Starting Fresh Chat While Keeping Context

1. Already in workspace mode with a conversation open
2. Press `Tab` to focus an empty pane
3. Press `n` to start new chat
4. Type your message and press `Enter`
5. Previous conversation stays visible in other pane!

---

## Mouse Support

**Click anywhere** to focus that pane - no keyboard needed!
- Click sidebar → Focus sidebar
- Click pane → Focus that pane
- Click input → Focus and start typing

---

## Tips for Comfortable Use

### Memory Aids

- **Tab = Next** (like browser tabs)
- **Arrows = Navigation** (sidebar OR scroll, depending on focus)
- **Enter = Do It** (open conversation OR send message)
- **Esc = Go Back** (always exits workspace)

### Muscle Memory

After a few uses, you'll naturally remember:
1. `Tab` to move around
2. `↑`/`↓` to navigate/scroll
3. `Enter` to act
4. `Esc` to exit

### Pro Tips

- Use `Alt+1/2/3` to quickly switch layouts
- Use `Page Up`/`Page Down` for fast scrolling
- Use `Home`/`End` to jump to conversation ends
- The **status line at the bottom** always shows available keys!

---

## Troubleshooting

**Q: I pressed a key and nothing happened**
- Make sure the correct pane is focused (check the blue border)
- Check the status line at the bottom for available keys

**Q: I can't type my message**
- Press `Tab` until a chat pane is focused (blue border)
- Make sure you're not in the sidebar (conversation list)

**Q: Keys 1, 2, 3 don't switch workspaces**
- Use `Alt+1`, `Alt+2`, `Alt+3` instead
- Plain 1, 2, 3 are for typing (no conflicts!)

**Q: How do I know which pane is active?**
- Look for the **bright blue border**
- Look for the **● filled dot** in the title
- Check the **status line** at the bottom

---

## Design Philosophy

This UX was designed to be:

1. **Predictable**: Same key always does same thing
2. **Natural**: Tab for switching (like browser), arrows for movement
3. **Clear**: Visual feedback shows exactly what's active
4. **Comfortable**: No awkward finger positions
5. **Discoverable**: Status line shows available keys
6. **Simple**: Minimal mental overhead

**You don't need to memorize anything!** The visual feedback and status lines guide you.

---

## Summary: The Only Keys You Need

```
Tab         → Next pane
Shift+Tab   → Previous pane
↑/↓         → Navigate or scroll (context-aware)
Enter       → Open or send (context-aware)
Esc         → Exit workspace
```

**That's it!** Everything else is optional shortcuts.

Enjoy your productive multi-pane workspace! 🎉
