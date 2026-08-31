# Tab Navigation Test Plan

## Test Scenarios

### 1. Agent Settings - Edit Form
**Context**: Settings > Agents > Select agent > Edit

**Test Steps**:
1. Navigate to Settings
2. Select "Agents" section
3. Select an existing agent
4. Choose "Edit" action
5. Press Tab multiple times

**Expected Behavior**:
- First Tab: Switch from Metadata → Tools tab
- Second Tab: Switch from Tools → Hooks tab
- Third Tab: Switch from Hooks → Capabilities tab
- Fourth Tab: Wrap around from Capabilities → Metadata tab
- Tab should NOT switch focus to sidebar while editing

**Alternative**: Press Shift+Tab to cycle in reverse order

### 2. Agent Settings - List View
**Context**: Settings > Agents (list view)

**Test Steps**:
1. Navigate to Settings > Agents
2. Press Tab

**Expected Behavior**:
- Tab should switch focus to sidebar
- This allows user to navigate to different settings sections

### 3. Plugins Settings - Detail View
**Context**: Settings > Plugins > Select plugin

**Test Steps**:
1. Navigate to Settings > Plugins
2. Select a plugin to view details
3. Press Tab multiple times

**Expected Behavior**:
- Tab cycles through plugin detail tabs: Overview → README → Config → Files
- Tab should NOT switch focus to sidebar while viewing plugin details

### 4. Skills Settings - Detail View
**Context**: Settings > Skills > Select skill

**Test Steps**:
1. Navigate to Settings > Skills
2. Select a skill to view details
3. Press Tab multiple times

**Expected Behavior**:
- Tab cycles through skill detail tabs: Overview → README → Config
- Tab should NOT switch focus to sidebar while viewing skill details

### 5. Hooks Settings - Edit Form
**Context**: Settings > Hooks > Select hook > Edit

**Test Steps**:
1. Navigate to Settings > Hooks
2. Select a hook and enter edit mode
3. Press Tab multiple times

**Expected Behavior**:
- Tab navigates through form fields
- Tab should NOT switch focus to sidebar while editing

### 6. General Settings
**Context**: Settings > General

**Test Steps**:
1. Navigate to Settings > General
2. Press Tab

**Expected Behavior**:
- Tab should switch focus to sidebar
- General section doesn't have internal tabs, so Tab is used for sidebar switching

## Key Behaviors

### Tab Key Handling Priority
1. **Content Handler First**: When focus is on content, the section-specific handler gets first chance to process Tab
2. **Sidebar Fallback**: If content handler doesn't consume Tab, it switches focus to sidebar
3. **Sidebar to Content**: When focus is on sidebar, Tab always switches to content

### Return Value Semantics
- `true`: Handler consumed the key (don't use for sidebar switching)
- `false`: Handler didn't consume the key (use for sidebar switching if key is Tab)

## Success Criteria
✅ All internal tab navigation works within forms and detail views
✅ Tab can still be used to switch between sidebar and content when appropriate
✅ Shift+Tab works for reverse navigation where applicable
✅ No regression in other keyboard shortcuts

## Testing Commands
```bash
# Build and run
cd /home/swarm/SwarmCode/TUI
make build
./swarm
```

## Manual Test Checklist
- [ ] Agent edit form tab cycling
- [ ] Plugin detail tab cycling
- [ ] Skill detail tab cycling
- [ ] Hook edit form field navigation
- [ ] Sidebar ↔ Content switching with Tab (when not in edit/detail views)
- [ ] Shift+Tab reverse cycling
- [ ] All existing keyboard shortcuts still work
