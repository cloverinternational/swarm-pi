# Agent Edit Menu Improvements

## Changes Made

### 1. Tab/Shift+Tab Navigation ✅
**Problem:** Tab and Shift+Tab keys didn't work to navigate between fields when creating or editing agents.

**Solution:** Added proper Tab/Shift+Tab key handling in `workflow_editor.go`:
- **Tab**: Move to next field (wraps around from last to first)
- **Shift+Tab**: Move to previous field (wraps around from first to last)
- Works in both agent edit and group edit modes
- Respects model selector dropdown state

**Code Location:** `internal/chat/workflow_editor.go` lines 351-378

### 2. Permissions Section ✅
**New Feature:** Added a dedicated Permissions section to the agent edit form.

**Fields:**
- **Bash Execution**: Allow/Restrict running shell commands
- **File Write**: Allow/Restrict creating/modifying files  
- **Network Access**: Allow/Restrict network operations
- **Sub-Agents**: Allow/Restrict spawning sub-agents

**Visual Indicators:**
- ✓ Green text for "Allowed" permissions
- ✗ Red text for "Restricted" permissions
- Highlight selected permission with background color
- Show hint: "press 't' to toggle" for selected field

**Code Location:** `internal/chat/workflow_editor.go` lines 1326-1404

### 3. Permission Toggle Functionality ✅
**Feature:** Press 't' key to toggle permissions on/off for the selected agent.

**How it works:**
- When agent has `Tools: ["*"]` (wildcard = all tools allowed)
  - Toggling OFF a permission: Removes wildcard, adds all tools EXCEPT the disabled ones
- When agent has restricted tool list
  - Toggling ON: Adds the specific tools for that permission
  - Toggling OFF: Removes the specific tools for that permission
- Automatically converts back to wildcard if all major tools are re-enabled

**Tool Mappings:**
- Bash Execution → `bash`, `swarm_bash`
- File Write → `file_write`, `write`
- Network Access → `network`, `http`
- Sub-Agents → `Task`, `BackgroundTask`

**Code Locations:**
- Key handler: `internal/chat/workflow_editor.go` line 373-376
- Toggle action: `internal/chat/app.go` line 5017-5028
- Toggle logic: `internal/chat/app.go` lines 11167-11289

## Field Navigation Updates

**Previous field count:** 11 fields (0-10)
- 0-6: Main fields (ID, Name, Description, Provider/Model, SystemPrompt, Tools, ContextSources)
- 7-10: Capabilities (MaxTokens, Temperature, MaxTurns, Timeout)

**New field count:** 15 fields (0-14)
- 0-6: Main fields
- 7-10: Capabilities
- **11-14: Permissions** (NEW)

## User Experience Improvements

### Before
- Had to use arrow keys (↑/↓) only for navigation
- No visual indication of permissions
- Couldn't easily toggle tool access

### After
- **Tab/Shift+Tab** for quick navigation (familiar keyboard shortcut)
- **Visual permission status** with color-coded indicators
- **One-key toggle** ('t') for permissions
- **Wrapping navigation** - Tab at last field goes to first field
- **Contextual hints** show keyboard shortcuts for current field

## Testing

Test the new features:
```bash
# Launch swarm
./swarm

# Navigate to Settings > Agents
# Create or edit an agent

# Try these:
1. Press Tab to move forward through fields
2. Press Shift+Tab to move backward
3. Navigate to Permissions section (fields 11-14)
4. Press 't' to toggle a permission
5. See the visual indicator change from ✓ to ✗ or vice versa
6. Save the agent (Ctrl+S)
```

## Files Modified

1. **internal/chat/workflow_editor.go**
   - Added Tab/Shift+Tab cases (lines 351-378)
   - Added 't' toggle key (lines 373-376)
   - Added Permissions section rendering (lines 1326-1404)
   - Updated maxFields from 10 to 14

2. **internal/chat/app.go**
   - Added toggle_permission case handler (lines 5017-5028)
   - Added toggleAgentPermission() function (lines 11167-11289)

3. **Backup files created:**
   - `internal/chat/workflow_editor.go.backup`
   - `internal/chat/app.go` (toggleAgentPermission appended to end)

## Related to Permission Inheritance Fix

This permission UI complements the sub-agent permission inheritance fix:
- **Sub-agent fix**: Ensures sub-agents inherit parent's permission policies
- **Permission UI**: Allows easy configuration of those permissions

Together, these give full control over agent tool access at both parent and sub-agent levels.

---

**Status**: ✅ Implemented and Tested  
**Build**: Successful  
**Date**: 2026-01-28
