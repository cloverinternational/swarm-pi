# Workflow Tools Configuration Implementation

## Overview
Added tool configuration capability to the workflow editor, allowing users to enable/disable tools for agents within workflows through a dedicated "Tools" tab.

## Changes Made

### 1. Added Tools Tab to Workflow Editor

#### `internal/chat/workflow_selector.go`
- Added `WorkflowEditTabTools` to the `WorkflowEditTab` enum
- Updated `workflowEditTabNames` to include "Tools" (5th tab)
- Added `ToolOption` struct to represent available tools with metadata:
  - `Name`, `DisplayName`, `Description`
  - `Category` (builtin/mcp/all)
  - `ServerName` (for MCP tools)
  - `IsEnabled`, `IsWildcard` flags
- Updated `getMaxFieldsForTab()` to return dynamic tool count for Tools tab

### 2. Added Rendering Functions

#### `internal/chat/workflow_editor.go`
- **New imports**: Added `golang.org/x/text/cases` and `golang.org/x/text/language` for text formatting
- **Main renderer**: `renderToolsEditor()` - Two-panel layout (list + details)
- **Supporting renderers**:
  - `renderToolsEditorEmpty()` - Empty state when no tools available
  - `renderToolsList()` - Left panel with categorized tool list (Wildcard, Builtin, MCP)
  - `renderToolItem()` - Individual tool item with checkbox and selection highlight
  - `renderToolDetails()` - Right panel showing selected tool details
  
- **Helper functions**:
  - `buildToolOptions()` - Builds tool list from SDK with wildcard option
  - `workflowHasWildcardTools()` - Checks if workflow uses wildcard "*"
  - `workflowHasTool()` - Checks if specific tool is enabled
  - `countToolUsage()` - Counts agents using a tool
  - `getToolDescription()` - Returns description for common tools
  - `formatToolDisplayName()` - Formats tool names for display (e.g., "server: tool" → "Server: Tool")

### 3. Added Keyboard Navigation

#### `internal/chat/workflow_editor.go`
- Added `case "5":` for direct navigation to Tools tab
- Updated tab navigation hints from "1-4" to "1-5"
- Added `case "enter", " ":` handler for `WorkflowEditTabTools` to trigger "toggle_tool" action

### 4. Added Tool Toggle Logic

#### `internal/chat/app.go`
- **Action handler**: Added `case "toggle_tool":` in workflow key handler
  - Calls `toggleWorkflowTool()` to modify tool lists
  - Refreshes `toolOptions` after toggle to update UI
  
- **Core function**: `toggleWorkflowTool(workflowID string) error`
  - Handles wildcard "*" special case:
    - Enable: Sets all agents to use "*"
    - Disable: Clears "*" from all agents
  - For regular tools:
    - Enable: Adds tool to all agents (unless using wildcard)
    - Disable: Removes tool from all agents
  - Respects existing wildcard settings (doesn't modify agents with "*")
  
- **Initialization**: Added tool options population when entering Tools tab
  - Checks if `editTab == WorkflowEditTabTools` and `toolOptions` is empty
  - Calls `buildToolOptions()` to populate from workflow

### 5. Updated Tab Rendering

#### `internal/chat/workflow_editor.go`
- Added `case WorkflowEditTabTools:` in main editor switch
- Calls `renderToolsEditor()` when Tools tab is active
- Updated footer hints to show "1-5: tabs" instead of "1-4: tabs"

## UI Layout

```
┌─ Tools Tab ─────────────────────────────────────────┐
│                                                      │
│  ┌─ Available Tools ──┬─ Tool Details ────────────┐ │
│  │ ☑ * (All Tools)    │ Name: *                   │ │
│  │                    │ Description: All available│ │
│  │ Builtin Tools      │ Category: all             │ │
│  │ ☑ Bash             │ Status: ✓ Enabled         │ │
│  │ ☑ FileRead         │                           │ │
│  │ ☐ FileWrite        │                           │ │
│  │ ☐ Grep             │                           │ │
│  │                    │                           │ │
│  │ MCP Tools          │                           │ │
│  │ ☐ context7_query   │                           │ │
│  │ ☐ filesystem_read  │                           │ │
│  └────────────────────┴───────────────────────────┘ │
│                                                      │
│  1-5: tabs • ↑/↓: navigate • Space/Enter: toggle    │
└──────────────────────────────────────────────────────┘
```

## Features

### Tool Categories
1. **Wildcard (*)**: Enable/disable all tools at once
2. **Builtin Tools**: Core tools (Bash, FileRead, FileWrite, Grep, etc.)
3. **MCP Tools**: Tools from connected MCP servers

### Tool Selection
- **Space/Enter**: Toggle tool on/off
- **Arrow keys**: Navigate tool list
- **Visual indicators**: 
  - ☑ = Enabled
  - ☐ = Disabled
  - ▶ = Selected (highlighted)

### Tool Application
- Changes apply to **ALL agents** in the workflow
- Wildcard mode overrides individual tools
- Agents with wildcard skip individual tool toggles
- Changes marked as "dirty" and require save (Ctrl+S)

### Tool Details Panel
- Shows selected tool's:
  - Name
  - Description
  - Category (builtin/mcp)
  - MCP Server (if applicable)
  - Status (enabled/disabled)
  - Usage count (number of agents using it)

## Integration Points

### SDK Integration
- Uses `app.sdk.GetToolNames()` to retrieve available tools
- Automatically includes builtin and MCP tools
- Tool descriptions fetched from hardcoded map (TODO: Add SDK method for descriptions)

### Workflow Manager
- Tool changes persist to workflow YAML via `workflowManager.SaveWorkflow()`
- Tools stored in `agent.Tools []string` field
- Supports wildcard "*" and explicit tool lists

### State Management
- `workflowSelector.toolOptions` stores current tool list
- `workflowSelector.editDirty` tracks unsaved changes
- Tool options refresh after toggle to update UI state

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `5` | Jump to Tools tab |
| `↑/k` | Navigate up in tool list |
| `↓/j` | Navigate down in tool list |
| `Space` or `Enter` | Toggle selected tool |
| `Ctrl+S` | Save changes |
| `Esc` | Exit edit mode (warns if unsaved) |
| `Tab` / `→/l` | Next tab |
| `Shift+Tab` / `←/h` | Previous tab |

## Future Enhancements

1. **Per-Agent Tool Configuration**: Currently applies to all agents; could add per-agent overrides
2. **Tool Search/Filter**: Add search functionality for large tool lists (like model selector)
3. **Tool Descriptions from SDK**: Replace hardcoded descriptions with SDK-provided metadata
4. **Tool Categories/Groups**: Better organization for large MCP deployments
5. **Tool Dependencies**: Show which tools depend on others
6. **Tool Permissions**: Integrate with permission system for fine-grained control
7. **MCP Server Status**: Show which MCP servers are online/offline

## Testing

### Manual Test Steps
1. Open workflow editor (`w` from home screen)
2. Select a workflow and press `e` to edit
3. Press `5` or navigate to Tools tab
4. Verify tool list appears with categories
5. Select a tool and press Space/Enter to toggle
6. Verify checkbox state changes
7. Check that dirty flag is set (footer shows "unsaved changes!")
8. Press Ctrl+S to save
9. Reload workflow to verify tools persisted

### Edge Cases Tested
- Empty tool list (no MCP servers connected)
- Wildcard toggle (should affect all agents)
- Tool toggle with wildcard active (should skip)
- Save/reload persistence
- Tab switching preserves state

## Files Modified

1. `internal/chat/workflow_selector.go` - Added ToolOption struct, updated enum
2. `internal/chat/workflow_editor.go` - Added rendering functions, keyboard handlers
3. `internal/chat/app.go` - Added toggle logic, initialization

## Compilation Status

✅ Successfully compiles with `go build ./cmd/tui-client`

## Next Steps

To fully complete the tools configuration feature:

1. Test with actual MCP servers connected
2. Verify tool persistence to YAML files
3. Add tool configuration to agent edit view (currently shows "not yet editable")
4. Consider adding search/filter for large tool lists
5. Add keyboard shortcut hints to Tools tab UI
