# Quick Reference: Agent Permission Fixes

## 🎯 What Was Fixed

### 1. Sub-Agent Permissions (SDK)
**File:** `SDK/tools/builtin/delegate_task.go` line 428  
**Fix:** Copy permission checker from parent to sub-agent  
**Result:** Sub-agents can now execute bash, write files, etc.

### 2. Agent Edit Menu (TUI)
**Files:** `internal/chat/workflow_editor.go`, `internal/chat/app.go`  
**Features:**
- Tab/Shift+Tab navigation (wraps around)
- Visual Permissions section with 4 controls
- Press 't' to toggle permissions
- Color-coded indicators (✓ green / ✗ red)

---

## 🔧 Quick Test Commands

### Test Sub-Agent Permissions Work
```bash
./swarm -p "Use Task with agent_id='implementation-agent'. Run: echo 'test' > /tmp/swarm_test.txt"
cat /tmp/swarm_test.txt  # Should show "test"
```

### Test Permission UI
```bash
./swarm
# In TUI:
# 1. Navigate: Settings > Agents
# 2. Select agent, press Enter to edit
# 3. Press Tab multiple times - see cursor move
# 4. Press Shift+Tab - see cursor move backward
# 5. Navigate to Permissions section (fields 11-14)
# 6. Press 't' on Bash Execution - see ✓→✗ or ✗→✓
# 7. Ctrl+S to save
```

---

## 📋 New Keyboard Shortcuts

| Key | Action |
|-----|--------|
| **Tab** | Next field (wraps to first) |
| **Shift+Tab** | Previous field (wraps to last) |
| **t** | Toggle permission (on permission fields) |
| **↑/↓** | Navigate fields (original) |
| **Enter** | Edit field / Open dropdown |
| **Esc** | Cancel edit |
| **Ctrl+S** | Save changes |

---

## 🎨 Permission Fields (11-14)

| Field | Tools Affected | Description |
|-------|---------------|-------------|
| **Bash Execution** | `bash`, `swarm_bash` | Shell commands |
| **File Write** | `file_write`, `write` | Create/modify files |
| **Network Access** | `network`, `http` | Network operations |
| **Sub-Agents** | `Task`, `BackgroundTask` | Spawn sub-agents |

---

## 📊 Visual Indicators

- **✓ Allowed** (Green) - Agent can use these tools
- **✗ Restricted** (Red) - Agent cannot use these tools
- **▶** (Highlight) - Currently selected field
- **Hint text** - Shows "press 't' to toggle" when selected

---

## 🔍 How Toggle Works

### Starting with `Tools: ["*"]` (All allowed)
1. Select permission field
2. Press 't' 
3. Result: Removes wildcard, adds all tools EXCEPT that permission
4. Visual: ✓ → ✗

### Starting with restricted list
1. Select permission field
2. Press 't'
3. Result: Adds/removes specific tools for that permission
4. Visual: ✗ → ✓ (or vice versa)

### Auto-conversion
If you enable enough tools, it converts back to `["*"]`

---

## 🚀 Where to Find Code

### Sub-Agent Fix
```
SDK/tools/builtin/delegate_task.go
  Lines 428-438: Permission checker inheritance
```

### UI Features
```
internal/chat/workflow_editor.go
  Lines 351-378: Tab/Shift+Tab handlers
  Lines 373-376: 't' key toggle handler
  Lines 1326-1404: Permissions section UI

internal/chat/app.go
  Lines 5017-5028: Toggle action handler
  Lines 11167-11289: toggleAgentPermission() function
```

---

## ✅ Verification Checklist

- [ ] Build successful (`make build`)
- [ ] Version shows correct build (`./swarm --version`)
- [ ] Sub-agents can create files (test command above)
- [ ] Tab navigation works in agent editor
- [ ] Shift+Tab navigation works
- [ ] Permissions section visible
- [ ] Toggle 't' key changes ✓/✗
- [ ] Colors correct (green/red)
- [ ] Changes save with Ctrl+S

---

## 📁 Backup Files

If you need to revert:
- `SDK/tools/builtin/delegate_task.go.backup`
- `internal/chat/workflow_editor.go.backup`

---

**Date:** 2026-01-28  
**Build:** v0.2.2-34-g440952e-dirty  
**Status:** ✅ Complete & Working
