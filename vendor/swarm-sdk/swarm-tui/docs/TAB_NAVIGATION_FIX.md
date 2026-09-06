# Tab Navigation Fix for Agent Settings

## Problem
When editing an agent in the Settings > Agents section, pressing Tab would switch focus from the content area back to the sidebar menu, instead of cycling through the different tabs within the agent edit form (Metadata → Tools → Hooks → Capabilities).

## Root Cause
The `Manager.HandleKey()` method in `internal/chat/settings/manager.go` was handling the Tab key globally at the top of the function, before delegating to section-specific handlers. This meant that Tab was always consumed by the manager to switch between sidebar and content, preventing section-specific handlers from using Tab for their own navigation.

## Solution
The fix implements a two-stage Tab handling approach:

1. **Sidebar Focus**: When focus is on the sidebar, Tab switches to content (existing behavior)

2. **Content Focus**: When focus is on content:
   - First, delegate the key to the content-specific handler
   - The handler returns a boolean indicating if it handled the key
   - If the handler handled Tab (returns true), don't switch focus
   - If the handler didn't handle Tab (returns false), switch focus to sidebar

## Implementation Details

### Modified Functions
- `Manager.HandleKey()`: Now checks content handler response before switching focus
- Added new handler methods that return (tea.Cmd, bool):
  - `handleAgentsKeyWithResult()`
  - `handleProfilesKeyWithResult()`
  - `handleHooksKeyWithResult()`
  - `handleSkillsKeyWithResult()`
  - `handlePluginsKeyWithResult()`
  - `handleSystemPromptKeyWithResult()`
  - `handleContextKeyWithResult()`

### Key Flow
```
User presses Tab while editing agent
  ↓
Manager.HandleKey("tab")
  ↓
Focus is on Content → delegate to section handler
  ↓
handleAgentsKeyWithResult("tab")
  ↓
AgentsSettings.HandleKey("tab")
  ↓
State is "edit" → handleFormKey("tab")
  ↓
Tab cycles through form tabs, returns true
  ↓
Manager sees handled=true, doesn't switch to sidebar ✓
```

## Testing
When editing an agent:
- Tab cycles: Metadata → Tools → Hooks → Capabilities → Metadata
- Shift+Tab cycles in reverse
- When in agent list view, Tab switches to sidebar (as expected)
- From sidebar, Tab switches back to content (as expected)

## Files Modified
- `internal/chat/settings/manager.go`
  - Modified `HandleKey()` method
  - Added `*WithResult()` handler methods

## Backward Compatibility
This fix maintains backward compatibility:
- Sections that don't handle Tab will still use it for sidebar switching
- All existing keyboard shortcuts continue to work
- The sidebar/content focus switching is preserved for non-Tab-consuming contexts
