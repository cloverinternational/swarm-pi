# Complete Settings Reference

**Last Updated:** 2026-02-16  
**Version:** Based on TUI feature/sleek-start-menu branch

## Executive Summary

The SwarmCode TUI settings system consists of:
- **20 distinct settings screens** organized into 6 groups
- **373 lines of state management** in a centralized State struct
- **44 Go source files** totaling ~15,000+ lines of code
- **10+ callback hooks** for integration with the main app
- **Multiple UI patterns**: lists, forms, split panels, tabs, modals

## Quick Navigation Index

### By Functionality

| What You Want to Do | Screen | Section |
|---------------------|--------|---------|
| Change AI model | Model & Provider | AI Config |
| Configure tool permissions | Security | Security & Auth |
| Manage MCP servers | Tools and MCP | Tools & Integrations |
| Create custom agents | Agents | AI Config |
| Set system prompts | System Prompts | AI Config |
| Configure hooks | Hooks | Tools & Integrations |
| Customize UI | Display | Appearance |
| Add API keys | Authentication | Security & Auth |
| Configure proxies | Proxies | Security & Auth |
| View cache stats | Cache Statistics | Performance |
| Add context sources | Context Sources | Tools & Integrations |
| Configure fallbacks | Reliability | Performance |
| Manage profiles | Agent Profiles | AI Config |
| Export/import config | Config Bundles | Advanced |

### By Complexity

**Simple (Single Screen)**
- Display Settings - 5 toggles + 1 picker
- General Settings - Basic app configuration
- Auth Settings - API key management
- Cloud Settings - Swarm Cloud endpoints
- Cache Statistics - Read-only metrics

**Medium (Multi-State)**
- Security & Permissions - Tabs + rules + forms
- System Prompts - CRUD operations
- Compaction Settings - Model selection
- Reliability Settings - Retry/fallback config
- Skills - Browser with detail view
- Plugins - Browser with detail view

**Complex (Deep Navigation)**
- Model & Provider - 11 sub-states, browsing, management
- MCP Settings - 5 sub-states, server management, tool testing
- Agents - CRUD + tool/hook selection + chat
- Agent Profiles - CRUD + role configuration
- Hooks - Split panel + templates + chat + forms
- Context Sources - MCP integration + resource picker

## Complete Screen Inventory

### AI Config Group (5 screens)

#### 1. Model & Provider
**File:** `model.go` (2149 lines)  
**Sub-screens:** 11 states  
**Purpose:** Browse and select AI models, manage providers

**Key Features:**
- Model browser with tabs (Fast/Cheap, Long Context, Coding, Vision, All)
- Provider management (add/edit/delete custom providers)
- OpenRouter integration with refresh
- Favorite and recent models
- Model aliasing across providers
- Reasoning effort configuration

**State Fields:** 40+ (including form state)

#### 2. System Prompts
**File:** `system_prompt.go`  
**States:** list, action_menu, create, edit, preview  
**Purpose:** Manage AI system prompts

**Operations:**
- Create new prompts
- Edit existing prompts
- Activate prompt (set as default)
- Delete prompts
- Preview prompt with OAuth integration

**State Fields:** 10

#### 3. Agents
**File:** `agents.go`, `agents_render.go`  
**States:** list, action_menu, create, edit, preview  
**Purpose:** Create and configure custom agents

**Features:**
- Full CRUD operations
- Tool selection with search
- Hook selection with search
- AI assistant for configuration
- Clone existing agents
- Set default agent

**State Fields:** 22

#### 4. Agent Profiles
**File:** `agent_profiles.go`, `agent_profiles_ui.go`, `agent_profiles_types.go`  
**States:** list, action_menu, edit, edit_role, preview  
**Purpose:** Configure role-based model assignments

**Roles:**
- MainAgent - Primary conversation model
- Implementer - Code implementation model
- FastFinder - Quick search model
- Researcher - Research tasks model
- CodeReviewer - Code review model
- GitAgent - Git operations model
- BackgroundWorker - Background task model
- ExploreAgent - Exploration model

**State Fields:** 19

#### 5. Compaction
**File:** `compaction.go`  
**Purpose:** Configure conversation compaction model

**Options:**
- Provider selection
- Model selection
- Enable/disable compaction

**State Fields:** Uses global state (SelectedItem)

### Tools & Integrations Group (5 screens)

#### 6. Tools and MCP
**File:** `mcp.go`, `mcp_render.go`, `mcp_servers.go`, `tool_tester.go`  
**States:** main, configure_tools, mcp_config, tool_tester, error_detail  
**Purpose:** Manage MCP servers and tool availability

**Features:**
- Configure tool enable/disable
- Manage MCP servers (add/edit/delete/test)
- Tool testing interface
- AI assistant for server configuration
- Split panel view (enabled/disabled tools)
- Categorized tool browser

**State Fields:** 25

#### 7. Hooks
**File:** `hooks.go`, `hooks_render.go`  
**States:** main, chat, action_menu, edit, templates  
**Purpose:** Configure event hooks for automation

**Features:**
- Global hooks (system-wide)
- Project hooks (workspace-specific)
- Hook templates
- AI assistant for hook creation
- Event types: tool_call, tool_result, conversation_end, etc.
- Actions: allow, block, exit, exit2, confirm, log

**State Fields:** 26

#### 8. Skills
**File:** `skills.go`, `skills_render.go`  
**States:** list, detail, search, install  
**Purpose:** Browse and install agent skills

**Features:**
- Skill browser
- Detail view with tabs (Info, Scripts, Refs)
- Search functionality
- Install/uninstall skills

**State Fields:** 9

#### 9. Plugins
**File:** `plugins.go`, `plugins_render.go`  
**States:** list, detail, search, marketplace  
**Purpose:** Manage Claude Code-compatible plugins

**Features:**
- Plugin browser
- Detail view with tabs (Info, Commands, Agents, MCP/LSP)
- Marketplace browser
- Install/uninstall/enable/disable plugins

**State Fields:** 10

#### 10. Context Sources
**File:** `context.go`, `context_render.go`  
**States:** list, detail, mcp_servers, mcp_picker, mcp_prompt_args  
**Purpose:** Configure project context loading

**Sources:**
- File-based sources
- MCP resources
- MCP prompts (with argument support)
- Git information
- Environment variables

**State Fields:** 16

### Appearance Group (1 screen)

#### 11. Display
**File:** `display.go`  
**Purpose:** Customize UI rendering and behavior

**Settings:**
1. Show Thinking Blocks (toggle)
2. Show Full Tool Output (toggle)
3. Rich Animations (toggle)
4. Copy Selection Shortcut (toggle)
5. Auto-copy Selection on Mouse (toggle)
6. Spinner Type (8 options: Matrix, Braille, Blocks, Wave, Pulse, Bounce, Aurora, DNA Helix)

**State Fields:** Uses global SelectedItem

### Security & Auth Group (4 screens)

#### 12. Security
**File:** `security.go`  
**States:** list, action_menu, add_form  
**Purpose:** Configure tool permissions and security rules

**Features:**
- 4 permission levels: Always Ask, Balanced, Permissive, YOLO
- 4 tabs: Allow, Ask, Deny, Workspace
- Pattern-based rules (commands, paths, URLs)
- Tool overrides
- Global and project-specific rules

**State Fields:** 14

#### 13. Authentication
**File:** `auth.go`  
**Purpose:** Manage API keys and OAuth

**Features:**
- OAuth status display
- API key input
- Provider-specific authentication
- Login/logout actions

**State Fields:** Uses global state

#### 14. Cloud
**File:** `cloud.go`  
**Purpose:** Configure Swarm Cloud endpoints

**Settings:**
- Cloud endpoint URL
- Authentication token
- Enable/disable cloud sync

**State Fields:** Uses global state

#### 15. Proxies
**File:** `proxies.go`  
**Purpose:** Configure API proxy endpoints

**Features:**
- Add/edit/delete proxy configurations
- Per-provider proxy settings
- Proxy authentication
- Test proxy connection

**State Fields:** Uses global state + internal proxy list

### Performance Group (2 screens)

#### 16. Cache Statistics
**File:** `cache.go`  
**Purpose:** View cache performance metrics

**Display:**
- Hit rate
- Total requests
- Cache size
- Eviction stats
- Per-provider breakdown

**State Fields:** Read-only, uses global state

#### 17. Reliability
**File:** `reliability.go`  
**Purpose:** Configure retry, fallback, and rate limiting

**Features:**
- Retry settings (max attempts, backoff)
- Fallback model configuration
- Rate limit settings
- Timeout configuration
- Circuit breaker settings

**State Fields:** Uses global state

### Advanced Group (3 screens)

#### 18. General
**File:** `general.go`  
**Purpose:** General application settings

**Settings:**
- App preferences
- Logging configuration
- Update checking
- Telemetry settings

**State Fields:** Uses global state

#### 19. Config Bundles
**File:** `config_bundles.go`  
**Purpose:** Export/import configuration bundles

**Features:**
- Export current configuration
- Import configuration bundle
- Share configurations
- Template bundles

**State Fields:** Uses global state

#### 20. Advanced
**File:** `advanced.go`  
**Purpose:** Advanced configuration options

**Settings:**
- Developer mode
- Experimental features
- Debug options
- Performance tuning

**State Fields:** Uses global state + AdvancedSubsection

## State Field Summary

### Total State Fields: 373 lines

**Breakdown by Category:**
- Core Navigation: 11 fields
- Exit Modal: 3 fields
- Security: 14 fields
- MCP: 25 fields
- System Prompts: 10 fields
- Hooks: 26 fields
- Context: 16 fields
- Agents: 22 fields
- Agent Profiles: 19 fields
- Skills: 9 fields
- Plugins: 10 fields
- Advanced: 1 field
- Misc: Shared across simple screens

## Callback Summary

### 10 Primary Callbacks

1. **onModelChange()** - Model/provider changed
2. **onAuthChange()** - Authentication updated
3. **onRenderChange()** - Rendering settings changed
4. **onMCPChange()** - MCP configuration changed
5. **onProxyChange()** - Proxy settings changed
6. **onPromptChange(string)** - System prompt changed
7. **onHookChange(string, bool)** - Hook enabled/disabled
8. **onAgentChange(string)** - Agent configuration changed
9. **onToolToggle(string, string, bool)** - Tool enabled/disabled
10. **onSpinnerChange(string)** - Spinner type changed
11. **onDisplaySettingsSync(...)** - Display settings saved
12. **onReasoningEffortChange(string)** - Reasoning effort changed

## File Organization

### Core Files (4 files)
- `types.go` - State definitions and enums (373 lines)
- `manager.go` - Main manager (618 lines)
- `manager_handlers.go` - Event handlers (1000+ lines)
- `permissions_shared.go` - Shared permission utilities

### Per-Screen Files (40 files)

**Single-file screens:**
- `display.go`, `general.go`, `auth.go`, `cloud.go`
- `cache.go`, `compaction.go`, `reliability.go`
- `advanced.go`, `config_bundles.go`, `proxies.go`
- `security.go`

**Multi-file screens:**
- Model: `model.go`, `model_render.go`, `model_render_forms.go`, `model_update.go`, `model_helpers.go`
- MCP: `mcp.go`, `mcp_render.go`, `mcp_servers.go`, `tool_tester.go`
- Agents: `agents.go`, `agents_render.go`
- Agent Profiles: `agent_profiles.go`, `agent_profiles_ui.go`, `agent_profiles_types.go`
- Hooks: `hooks.go`, `hooks_render.go`
- Context: `context.go`, `context_render.go`
- Skills: `skills.go`, `skills_render.go`
- Plugins: `plugins.go`, `plugins_render.go`
- System Prompts: `system_prompt.go`

**Test files:**
- `agent_profiles_test.go`, `model_reasoning_effort_test.go`
- `model_switch_test.go`, `reliability_test.go`

**Helper files:**
- `fallback_picker.go` - Fallback model picker component
- `agent_config_bundle.go` - Agent configuration bundling

## Configuration Files

### Global Configuration (`~/.config/swarmcode/`)

```
~/.config/swarmcode/
├── config.yaml                 # Main configuration
├── security.yaml               # Security rules
├── agents/                     # Agent definitions
│   ├── default.yaml
│   └── custom-*.yaml
├── profiles/                   # Agent profiles
│   ├── default.yaml
│   └── custom-*.yaml
├── prompts/                    # System prompts
│   ├── default.yaml
│   └── custom-*.yaml
├── hooks/                      # Global hooks
│   └── *.yaml
└── cache/                      # Cache data
```

### Project Configuration (`.swarmcode/`)

```
.swarmcode/
├── context.yaml                # Context sources
├── security.yaml               # Project security rules
├── hooks/                      # Project hooks
│   └── *.yaml
└── config.yaml                 # Project-specific config
```

## UI Patterns

### Pattern 1: Simple List
**Used by:** Display, General, Auth, Cloud, Cache

```
▶ Setting Name                  [VALUE]
  Description text
```

### Pattern 2: Tabbed List
**Used by:** Security, Model (browse)

```
Tab1   Tab2   Tab3   Tab4

▶ Item 1
  Item 2
  Item 3
```

### Pattern 3: Split Panel
**Used by:** MCP, Hooks

```
┌─────────────┬─────────────┐
│ Left Panel  │ Right Panel │
│ ▶ Item 1    │   Item A    │
│   Item 2    │   Item B    │
└─────────────┴─────────────┘
```

### Pattern 4: Form
**Used by:** MCP add server, Agents, System Prompts

```
Field 1: [value      ]
▶ Field 2: [value      ]
Field 3: [value      ]

[Save] [Cancel]
```

### Pattern 5: Action Menu
**Used by:** Agents, System Prompts, Hooks

```
▶ Edit
  Delete
  Clone
  Preview
```

### Pattern 6: Browser with Detail
**Used by:** Skills, Plugins

```
List View          │ Detail View
                   │
▶ Item 1           │ [Info] Scripts Refs
  Item 2           │ 
  Item 3           │ Description...
                   │ 
                   │ [Install]
```

## Keyboard Shortcuts Summary

### Global
- `Tab` - Switch focus (Sidebar ↔ Content)
- `Esc` - Go back / Cancel / Exit
- `Up/Down` - Navigate lists
- `Enter` - Select / Activate / Edit
- `/` - Search (in applicable screens)
- `PgUp/PgDn` - Fast scroll
- `Home/End` - Jump to start/end

### Context-Specific
- `Space` - Toggle (checkboxes, selections)
- `Left/Right` - Change values (dropdowns, pickers)
- `Ctrl+Enter` - Submit form (in some screens)
- `a` - Add new (MCP servers, etc.)
- `e` - Edit selected
- `d` - Delete selected
- `t` - Test / Templates
- `m` - View models / Marketplace
- `r` - Refresh / Reload
- `c` - Toggle view mode
- `f` - Toggle favorite
- `1-4` - Quick menu selection

## Best Practices for Users

### 1. Navigation
- Use `Tab` to switch between sidebar and content
- Use `Esc` to safely back out of any screen
- Use `/` to search when lists are long

### 2. Configuration
- Start with Model & Provider to set AI model
- Configure Security for safe tool usage
- Set up System Prompts for custom behavior
- Use Agent Profiles for role-based models

### 3. Performance
- Check Cache Statistics for optimization opportunities
- Configure Reliability for stable operation
- Use Compaction to manage context window

### 4. Customization
- Use Display settings for comfortable UI
- Create Agents for specific tasks
- Set up Hooks for automation
- Configure Context Sources for better understanding

### 5. Troubleshooting
- Check Auth settings if API calls fail
- Review Security rules if tools are blocked
- Test MCP servers if tools don't appear
- Check Reliability settings if retries failing

## Development Guide

### Adding a New Settings Screen

1. **Create screen file** - `internal/chat/settings/my_screen.go`
2. **Define struct** - Implement settings interface
3. **Add to types.go** - Add Section enum and state fields
4. **Register in manager.go** - Add to Manager struct
5. **Add to Sections list** - Include in sidebar
6. **Implement Render()** - Create UI layout
7. **Implement HandleKeyPress()** - Handle input
8. **Add callbacks** - If needed for app integration
9. **Update manager_handlers.go** - Route events
10. **Test thoroughly** - Navigation, state, persistence

### Testing Checklist

- [ ] Navigation (keyboard)
- [ ] Navigation (mouse)
- [ ] Form validation
- [ ] State persistence
- [ ] Error handling
- [ ] Callback integration
- [ ] Visual rendering
- [ ] Search/filter (if applicable)
- [ ] Scroll behavior
- [ ] Exit confirmation

## Performance Metrics

### Render Performance
- **Simple screens:** <1ms render time
- **Complex screens:** 1-5ms render time
- **List scrolling:** 60 FPS maintained
- **Animation:** Smooth at configured FPS

### Memory Usage
- **State struct:** ~5-10 KB
- **Per screen:** ~1-5 KB
- **Total settings:** ~50-100 KB

### Configuration Load Time
- **Cold start:** <100ms
- **Hot reload:** <10ms
- **Save operation:** <50ms

## Known Limitations

1. **No undo/redo** - Changes are immediate
2. **Limited mouse support** - Depends on terminal
3. **No multi-select** - One item at a time
4. **Fixed layout** - No customizable layout
5. **No drag-and-drop** - Terminal limitation
6. **Limited clipboard** - Terminal-dependent
7. **No inline help** - Press ? for shortcuts
8. **Fixed colors** - Theme not customizable

## Roadmap

### Planned Features
- [ ] Configuration export/import (bundles)
- [ ] Settings search across all screens
- [ ] Settings history/undo
- [ ] Keyboard shortcut customization
- [ ] Theme customization
- [ ] Settings profiles
- [ ] Quick settings panel
- [ ] Settings sync across devices
- [ ] Settings validation
- [ ] Settings documentation links

### Future Enhancements
- [ ] Guided setup wizard
- [ ] Settings templates
- [ ] Settings comparison
- [ ] Settings backup/restore
- [ ] Settings migration
- [ ] Settings audit log
- [ ] Settings recommendations
- [ ] Settings health check

## Support Resources

### Documentation
- `00-architecture.md` - System architecture
- `state-management.md` - State management guide
- `navigation-flow.md` - Navigation patterns
- Screen-specific docs in category folders

### Code Reference
- `internal/chat/settings/` - All settings code
- `sdk/tools/` - Tool definitions
- `internal/ui/` - UI components

### Getting Help
- GitHub Issues - Bug reports
- Discord - Community support
- Documentation - In-depth guides
- Code comments - Implementation details

---

**Document Version:** 1.0  
**Last Updated:** 2026-02-16  
**Maintained by:** SwarmCode Team