# Settings State Management Guide

## Overview

The settings system uses a centralized `State` struct (defined in `types.go`) that contains **373 lines** of state definitions. This document maps all state fields to their purpose and usage.

## State Structure

```go
type State struct {
    // ===== CORE NAVIGATION (11 fields) =====
    SelectedSection Section        // Which settings screen is active
    SelectedItem    int            // Selected item within screen
    ScrollOffset    int            // Scroll position in lists
    MaxVisible      int            // Items visible at once
    EditingItem     bool           // Whether editing text field
    EditBuffer      string         // Text editing buffer
    Focus           Focus          // FocusSidebar or FocusContent
    
    // Sidebar grouping & search
    CollapsedGroups map[string]bool // Which groups are collapsed
    SearchQuery     string          // Sidebar search filter
    SearchActive    bool            // Whether search input is active
    
    // ===== EXIT MODAL (3 fields) =====
    ShowExitModal   bool           // Show save confirmation dialog
    ExitModalChoice int            // 0=Save&Exit, 1=Exit, 2=Cancel
    
    // ===== SECURITY SETTINGS (14 fields) =====
    SecuritySelected      int      // Selected rule index
    SecurityScrollOffset  int      // Scroll position
    SecurityLevelSelected int      // 0=AlwaysAsk, 1=Balanced, 2=Permissive, 3=YOLO
    SecurityTab           int      // 0=Allow, 1=Ask, 2=Deny, 3=Workspace
    SecuritySearchQuery   string   // Search filter text
    SecurityRulesState    string   // "list", "action_menu", "add_form"
    SecurityRuleActionChoice int   // 0=Delete
    SecurityAddFormField  int      // 0=tool, 1=pattern
    SecurityAddFormTool   string   // Tool name for new rule
    SecurityAddFormPattern string  // Glob pattern for new rule
    SecurityAddCursorPos  int      // Cursor position in pattern input
    
    // ===== ADVANCED SUBSECTION (1 field - legacy) =====
    AdvancedSubsection AdvancedSubsection // AdvancedMain or AdvancedCache
    
    // ===== MCP SETTINGS (25 fields) =====
    MCPState                string  // "main", "configure_tools", "mcp_config", "tool_tester", "error_detail"
    MCPSelectedButton       int     // 0=ConfigureTools, 1=MCPConfig, 2=ToolTester, 3=AIAssistant
    MCPFocusedPanel         string  // "buttons", "enabled", "disabled"
    MCPScrollOffset         int     // Scroll position in main view
    MCPScrollOffsetEnabled  int     // Scroll in enabled tools panel
    MCPScrollOffsetDisabled int     // Scroll in disabled tools panel
    MCPCurrentServer        *commands.MCPServerState  // Selected server
    MCPCurrentTool          *mcp.MCPTool              // Selected tool
    MCPErrorDetailScroll    int     // Scroll in error detail view
    
    // Configure Tools screen state
    MCPConfigureToolsSelected           int             // Selected tool index
    MCPConfigureToolsScrollOffset       int             // Scroll offset for tool list
    MCPConfigureToolsExpandedCategories map[string]bool // Expanded categories
    MCPConfigureToolsViewMode           string          // "flat" or "categorized"
    
    // MCP Servers screen state
    MCPServersSelected     int   // Selected server index
    MCPServersScrollOffset int   // Scroll offset for server list
    MCPServersAddMode      bool  // Whether in add server mode
    MCPServersEditMode     bool  // Whether in edit server mode
    
    // Add server form state (9 fields)
    AddFormField    int
    AddFormName     string
    AddFormType     string  // "stdio", "sse", "http", "oauth"
    AddFormCommand  string
    AddFormArgs     string
    AddFormURL      string
    AddFormHeaders  string
    AddFormClientID string
    AddFormScopes   string
    AddFormEnv      string
    AddFormWorkDir  string
    AddFormTimeout  string
    
    // MCP Chat state (4 fields)
    MCPChatInput        string
    MCPChatHistory      []string
    MCPChatScrollOffset int
    MCPChatWaiting      bool
    
    // ===== SYSTEM PROMPT SETTINGS (10 fields) =====
    SystemPromptState        string   // "list", "action_menu", "create", "edit", "preview"
    SystemPromptSelected     int      // Selected prompt index (-1 = "New Prompt")
    SystemPromptActionChoice int      // 0=Edit, 1=Activate, 2=Delete, 3=Preview
    SystemPromptEditingField int      // 0=name, 1=content
    SystemPromptFormName     string   // Form name field
    SystemPromptFormContent  string   // Form content field
    SystemPromptCursorPos    int      // Cursor position
    SystemPromptScrollOffset int      // Scroll position
    SystemPromptContentLines []string // Multi-line content
    
    // ===== HOOKS SETTINGS (26 fields) =====
    HooksState        string  // "main", "chat", "action_menu", "edit", "templates"
    HooksSelected     int     // Selected hook index
    HooksActionChoice int     // Action menu selection
    HooksEditingField int     // 0=name, 1=event, 2=command, etc.
    HooksEditingMode  string  // "create" or "edit"
    HooksEditingScope string  // "global" or "project"
    HooksFormName     string
    HooksFormEvent    string
    HooksFormCommand  string
    HooksCursorPos    int
    
    // Hooks panel navigation (MCP-style split view)
    HooksFocusedPanel        string  // "buttons", "global", "project"
    HooksSelectedButton      int     // 0=Templates, 1=AIAssistant, 2=Reload, 3=NewHook
    HooksScrollOffsetGlobal  int
    HooksScrollOffsetProject int
    HooksSelectedGlobal      int
    HooksSelectedProject     int
    
    // Hooks template picker
    HooksTemplateIdx int
    
    // Hooks form fields (8 fields)
    HooksFormToolMatcher   string
    HooksFormPathAllowlist string
    HooksFormPathDenylist  string
    HooksFormAction        string
    HooksFormTimeout       string
    HooksFormDescription   string
    HooksFormEnabled       bool
    
    // Hooks chat state (4 fields)
    HooksChatInput        string
    HooksChatHistory      []string
    HooksChatScrollOffset int
    HooksChatWaiting      bool
    
    // ===== CONTEXT SETTINGS (16 fields) =====
    ContextSelectedItem   int     // Which source is selected
    ContextState          string  // "list", "detail", "mcp_servers", "mcp_picker", "mcp_prompt_args"
    ContextEditTarget     string  // "global" or "project"
    ContextTTLEditing     bool
    ContextTTLInput       string
    ContextTTLSourceID    string
    ContextDetailSourceID string
    
    // Context MCP picker state (9 fields)
    ContextMCPServerIndex    int
    ContextMCPSelectedServer string
    ContextMCPTab            int  // 0=resources, 1=prompts
    ContextMCPResourceIndex  int
    ContextMCPPromptIndex    int
    ContextMCPFilter         string
    ContextMCPFilterMode     bool
    ContextMCPArgsServer     string
    ContextMCPArgsPromptName string
    ContextMCPArgsPromptArgs []mcp.PromptArgument
    ContextMCPArgsValues     map[string]string
    ContextMCPArgsSelected   int
    
    // ===== AGENTS SETTINGS (22 fields) =====
    AgentsState        string  // "list", "action_menu", "create", "edit", "preview"
    AgentsSelected     int     // Selected agent index (-1 = "New Agent")
    AgentsActionChoice int     // 0=Edit, 1=SetDefault, 2=Clone, 3=Delete, 4=Preview
    AgentsFormTab      int     // DEPRECATED
    AgentsFormField    int     // Current field (0-8)
    AgentsCursorPos    int     // Cursor position
    AgentsScrollOffset int     // Scroll offset
    AgentsFormEditing  bool    // Whether editing text field
    
    // Agents form data (12 fields)
    AgentsFormID           string
    AgentsFormName         string
    AgentsFormDescription  string
    AgentsFormProvider     string
    AgentsFormModel        string
    AgentsFormSystemPrompt string
    AgentsFormTools        []string
    AgentsFormHooks        []string
    AgentsFormMaxTokens    int
    AgentsFormTemperature  float64
    AgentsFormMaxTurns     int
    AgentsFormTimeout      int
    
    // Agents tool/hook selection state (6 fields)
    AgentsToolsScrollOffset int
    AgentsToolsSelected     int
    AgentsHooksScrollOffset int
    AgentsHooksSelected     int
    AgentsToolsFilter       string
    AgentsHooksFilter       string
    
    // Agents chat state (4 fields)
    AgentsChatInput        string
    AgentsChatHistory      []string
    AgentsChatScrollOffset int
    AgentsChatWaiting      bool
    
    // ===== AGENT PROFILES SETTINGS (19 fields) =====
    ProfilesState        string  // "list", "action_menu", "edit", "edit_role", "preview"
    ProfilesSelected     int     // Selected profile index (-1 = "New Profile")
    ProfilesActionChoice int     // 0=Edit, 1=SetDefault, 2=Clone, 3=Delete, 4=Preview
    ProfilesFormField    int     // 0=ID, 1=Name, 2=Desc, 3=Icon, 4=Color, 5=ConfigureRoles, 6=Save
    ProfilesRoleEditing  string  // Which role is being edited (ModelAlias)
    ProfilesRoleField    int     // Current field in role edit (0-7)
    ProfilesCursorPos    int     // Cursor position
    ProfilesScrollOffset int     // Scroll offset
    ProfilesFormEditing  bool    // Whether editing text field
    
    // Profile form data (5 fields)
    ProfilesFormID          string
    ProfilesFormName        string
    ProfilesFormDescription string
    ProfilesFormIcon        string
    ProfilesFormColor       string
    
    // Profile role configuration data (7 fields)
    ProfilesRoleProvider     string
    ProfilesRoleModel        string
    ProfilesRoleSystemPrompt string
    ProfilesRoleMaxTokens    int
    ProfilesRoleTemperature  float64
    ProfilesRoleMaxTurns     int
    ProfilesRoleTimeout      int
    
    // Profile feedback messages (3 fields)
    ProfilesErrorMessage   string
    ProfilesSuccessMessage string
    ProfilesMessageTimeout int
    
    // ===== SKILLS SETTINGS (9 fields) =====
    SkillsState          string  // "list", "detail", "search", "install"
    SkillsSelected       int
    SkillsScrollOffset   int
    SkillsSearchQuery    string
    SkillsSearchSelected int
    SkillsSearchOffset   int
    SkillsDetailTab      int     // 0=Info, 1=Scripts, 2=Refs
    SkillsCursorPos      int
    
    // ===== PLUGINS SETTINGS (10 fields) =====
    PluginsState               string  // "list", "detail", "search", "marketplace"
    PluginsSelected            int
    PluginsScrollOffset        int
    PluginsSearchQuery         string
    PluginsSearchSelected      int
    PluginsSearchOffset        int
    PluginsDetailTab           int     // 0=Info, 1=Commands, 2=Agents, 3=MCP/LSP
    PluginsCursorPos           int
    PluginsMarketplaceSelected int
}
```

## State Field Reference

### Core Navigation (11 fields)

| Field | Type | Purpose | Used By |
|-------|------|---------|---------|
| SelectedSection | Section | Active settings screen | All screens |
| SelectedItem | int | Current item in list | All list-based screens |
| ScrollOffset | int | Scroll position | All scrollable screens |
| MaxVisible | int | Items per page | List rendering |
| EditingItem | bool | Text editing active | Form screens |
| EditBuffer | string | Text editing buffer | Text inputs |
| Focus | Focus | Sidebar vs Content | Navigation |
| CollapsedGroups | map[string]bool | Sidebar group state | Sidebar |
| SearchQuery | string | Sidebar search | Sidebar |
| SearchActive | bool | Search input active | Sidebar |
| ShowExitModal | bool | Confirmation dialog | Exit flow |
| ExitModalChoice | int | Modal selection | Exit flow |

### Per-Screen State Breakdown

| Screen | Field Count | Primary State Fields | Notes |
|--------|-------------|---------------------|-------|
| Security | 14 | SecurityTab, SecurityRulesState, SecuritySelected | Complex tab-based UI |
| MCP | 25 | MCPState, MCPFocusedPanel, MCPServersAddMode | Most complex screen |
| System Prompts | 10 | SystemPromptState, SystemPromptSelected | CRUD operations |
| Hooks | 26 | HooksState, HooksFocusedPanel, HooksEditingScope | Split global/project |
| Context | 16 | ContextState, ContextMCPTab | MCP integration |
| Agents | 22 | AgentsState, AgentsFormField | Full CRUD with chat |
| Agent Profiles | 19 | ProfilesState, ProfilesRoleEditing | Role-based config |
| Skills | 9 | SkillsState, SkillsDetailTab | Simple browser |
| Plugins | 10 | PluginsState, PluginsDetailTab | Similar to Skills |

## State Initialization

### NewState() Function

Creates default state with sane defaults:

```go
func NewState() *State {
    return &State{
        SelectedSection:  SectionModel,  // Start on Model screen
        SelectedItem:     0,
        ScrollOffset:     0,
        MaxVisible:       10,
        Focus:            FocusSidebar,
        CollapsedGroups:  make(map[string]bool),
        
        // Security defaults
        SecuritySelected:       -1,  // "Add new rule"
        SecurityTab:            0,   // Allow tab
        SecurityRulesState:     "list",
        SecurityAddFormTool:    "Bash",
        
        // MCP defaults
        MCPState:                     "main",
        MCPFocusedPanel:              "buttons",
        MCPConfigureToolsViewMode:    "categorized",
        MCPConfigureToolsExpandedCategories: make(map[string]bool),
        
        // System Prompts defaults
        SystemPromptState:    "list",
        SystemPromptSelected: -1,  // "New Prompt"
        
        // ... (continues for all screens)
    }
}
```

## State Patterns

### Pattern 1: List Navigation

Used by: Security, Agents, System Prompts, Skills, Plugins

```go
// Fields
Selected     int     // -1 or 0..N
ScrollOffset int     // Scroll position
State        string  // "list", "action_menu", "edit", etc.

// Navigation
Up/Down → Adjust Selected
Enter → Change State (e.g., "list" → "edit")
Esc → Return to previous State
```

### Pattern 2: Form Editing

Used by: MCP Servers, Agents, System Prompts, Hooks

```go
// Fields
FormField     int     // Current field index
FormEditing   bool    // Whether editing
FormFieldName string  // Current field value
CursorPos     int     // Cursor position

// Navigation
Tab → Next field
Enter → Start/stop editing
Type → Update field value
```

### Pattern 3: Split Panel

Used by: MCP, Hooks

```go
// Fields
FocusedPanel         string  // "buttons", "left", "right"
SelectedLeft         int     // Selection in left panel
SelectedRight        int     // Selection in right panel
ScrollOffsetLeft     int     // Scroll in left
ScrollOffsetRight    int     // Scroll in right

// Navigation
Left/Right → Switch panels
Up/Down → Navigate within panel
Enter → Action on selected item
```

### Pattern 4: Multi-State Screen

Used by: MCP, Context, Skills, Plugins

```go
// Fields
State string  // Multiple possible states

// State machine
"main" → "detail" → "edit" → "main"
  ↓
"search" → "results" → "detail"

// Navigation
State-specific keyboard shortcuts
Esc → Previous state
```

### Pattern 5: Chat Integration

Used by: MCP, Agents, Hooks

```go
// Fields
ChatInput        string
ChatHistory      []string
ChatScrollOffset int
ChatWaiting      bool

// Usage
User types → ChatInput
Press Enter → Send to AI, set ChatWaiting=true
AI responds → Append to ChatHistory, set ChatWaiting=false
Scroll chat → Adjust ChatScrollOffset
```

## State Synchronization

### Screen-Specific State Access

Each screen handler accesses state through the shared State struct:

```go
func (s *SecuritySettings) Render(width, height int, state *State, theme Theme) string {
    tab := state.SecurityTab
    selected := state.SecuritySelected
    scrollOffset := state.SecurityScrollOffset
    // ...
}

func (s *SecuritySettings) HandleKeyPress(key tea.KeyMsg, state *State) tea.Cmd {
    switch key.String() {
    case "up":
        state.SecuritySelected--
    case "down":
        state.SecuritySelected++
    case "enter":
        state.SecurityRulesState = "add_form"
    }
}
```

### State Persistence

State is **not persisted** across sessions. Only configuration data is saved.

**Ephemeral state:**
- Current selection
- Scroll positions
- Form editing state
- Modal state

**Persisted configuration:**
- Model selection
- Security rules
- Agent definitions
- System prompts
- Hooks

## State Validation

### Boundary Checks

All screens validate state before rendering:

```go
// Security example
selected := state.SecuritySelected
if selected < -1 {
    selected = -1
}
if selected > maxItems-1 {
    selected = maxItems-1
}
state.SecuritySelected = selected

// Scroll bounds
if scrollOffset < 0 {
    scrollOffset = 0
}
if scrollOffset > maxScroll {
    scrollOffset = maxScroll
}
```

### State Transitions

State machines validate transitions:

```go
switch state.MCPState {
case "main":
    // Can go to: configure_tools, mcp_config, tool_tester
case "configure_tools":
    // Can only go back to: main
case "tool_tester":
    // Can go to: error_detail, main
}
```

## Memory Usage

### State Size Estimation

Approximate memory per field:
- `int` - 8 bytes
- `bool` - 1 byte
- `string` - 16 bytes (header) + content
- `[]string` - 24 bytes (header) + elements
- `map[string]bool` - 24 bytes (header) + entries

**Total State struct:** ~5-10 KB (depending on string content)

### Optimization Opportunities

1. **Lazy initialization** - Only allocate maps when needed
2. **State compression** - Pack boolean flags into bitfields
3. **Separate state structs** - Per-screen state structs instead of monolithic

## Best Practices

### 1. Always Validate State

```go
// Bad
selected := state.Selected
items[selected].Name  // Panic if out of bounds

// Good
selected := state.Selected
if selected >= 0 && selected < len(items) {
    items[selected].Name
}
```

### 2. Reset State on Screen Exit

```go
// When leaving screen, reset edit state
state.SecurityRulesState = "list"
state.SecurityAddFormField = 0
state.SecurityAddFormPattern = ""
```

### 3. Use Descriptive State Values

```go
// Good
state.MCPState = "configure_tools"

// Bad
state.MCPState = "2"
```

### 4. Document State Transitions

```go
// State flow: list → action_menu → list
//             list → add_form → list
switch state.SecurityRulesState {
case "list":
    // ...
case "action_menu":
    // ...
case "add_form":
    // ...
}
```

## Debugging State

### Debug Logging

```go
logDebug("Security state: tab=%d, selected=%d, state=%s", 
    state.SecurityTab, state.SecuritySelected, state.SecurityRulesState)
```

### State Inspection

Add debug screen to display current state:

```go
func renderDebugState(state *State) string {
    return fmt.Sprintf(`
Section: %d
Focus: %d
Selected: %d
Scroll: %d
SecurityTab: %d
SecurityState: %s
MCPState: %s
`, state.SelectedSection, state.Focus, state.SelectedItem, 
   state.ScrollOffset, state.SecurityTab, state.SecurityRulesState, state.MCPState)
}
```

## Future Improvements

### 1. State Isolation

Move per-screen state to dedicated structs:

```go
type State struct {
    Core     CoreState
    Security SecurityState
    MCP      MCPState
    Agents   AgentsState
    // ...
}
```

### 2. State History

Implement undo/redo for form editing:

```go
type State struct {
    Current  StateSnapshot
    History  []StateSnapshot
    Future   []StateSnapshot
}
```

### 3. State Serialization

Persist UI state across sessions:

```yaml
# ~/.config/swarmcode/ui_state.yaml
last_section: model
last_scroll_positions:
  security: 5
  mcp: 10
```

### 4. State Validation Layer

Automatic state validation on every change:

```go
func (s *State) SetSecuritySelected(value int) {
    if value < -1 || value > s.getMaxSecurityItems() {
        panic("Invalid security selection")
    }
    s.SecuritySelected = value
}
```