# Hook Toggle Architecture Design

## Overview

This document outlines the improved architecture for hook toggling functionality in the Swarm SDK and TUI, incorporating patterns from claude-code-source while maintaining the existing thread-safe runtime toggle capabilities.

## Design Goals

1. **Unified Hook Management**: Single source of truth for hook states across SDK and TUI
2. **Persistent Configuration**: Save hook enable/disable preferences
3. **Categorization**: Group hooks by type/category for bulk operations
4. **System Hook Control**: Ability to toggle builtin/system hooks
5. **Runtime Safety**: Thread-safe toggling during active agent execution

## Architecture Components

### 1. SDK Layer Enhancements

#### Hook Categories
```go
// hooks/category.go
type HookCategory string

const (
    CategoryValidation   HookCategory = "validation"
    CategoryAnalysis     HookCategory = "analysis"  
    CategorySecurity     HookCategory = "security"
    CategoryPerformance  HookCategory = "performance"
    CategoryDebug        HookCategory = "debug"
    CategorySystem       HookCategory = "system"
    CategoryCustom       HookCategory = "custom"
)

type HookMetadata struct {
    Name        string
    Category    HookCategory
    Description string
    IsBuiltin   bool
    IsSystem    bool  // System hooks that are critical
}
```

#### Enhanced Hook Interface
```go
// hooks/hook.go
type Hook interface {
    Execute(context.Context, Event) (Result, error)
    Filter(Event) bool
    Priority() int
    Name() string
    
    // New methods
    Metadata() HookMetadata
    SetEnabled(enabled bool)
    IsEnabled() bool
}
```

#### Hook Configuration
```go
// hooks/config.go
type HookConfig struct {
    // Global toggle
    DisableAllHooks bool `json:"disableAllHooks,omitempty"`
    
    // Category toggles
    DisabledCategories []HookCategory `json:"disabledCategories,omitempty"`
    
    // Individual hook states
    HookStates map[string]bool `json:"hookStates,omitempty"`
    
    // Policy overrides (cannot be changed at runtime)
    PolicyOverrides map[string]bool `json:"policyOverrides,omitempty"`
}

type ConfigSource string

const (
    SourceDefault  ConfigSource = "default"
    SourceUser     ConfigSource = "user"
    SourceProject  ConfigSource = "project"
    SourceRuntime  ConfigSource = "runtime"
    SourcePolicy   ConfigSource = "policy"
)
```

#### Enhanced Manager
```go
// hooks/manager.go
func (m *Manager) SetEnabled(name string, enabled bool) error
func (m *Manager) SetCategoryEnabled(category HookCategory, enabled bool) error
func (m *Manager) SetAllEnabled(enabled bool) error
func (m *Manager) GetHookMetadata(name string) (*HookMetadata, error)
func (m *Manager) ListByCategory(category HookCategory) []HookRegistration
func (m *Manager) LoadConfig(config *HookConfig, source ConfigSource) error
func (m *Manager) SaveConfig() (*HookConfig, error)
```

### 2. Configuration Persistence

#### Configuration File Structure
```yaml
# ~/.swarm/hooks.yaml
version: "1.0"
hooks:
  disableAllHooks: false
  disabledCategories:
    - debug
    - performance
  hookStates:
    "auto-mode-hook": false
    "task-nudge-hook": true
    "recap-hook": false

# Project-specific: .swarm/hooks.yaml
hooks:
  hookStates:
    "project-specific-hook": true
```

#### Configuration Loading Priority
1. Default built-in states
2. User configuration (~/.swarm/hooks.yaml)
3. Project configuration (.swarm/hooks.yaml)  
4. Runtime modifications
5. Policy overrides (highest priority, immutable)

### 3. TUI Integration Enhancements

#### Hook Dashboard View
```go
// internal/chat/hooks/dashboard.go
type HookDashboard struct {
    manager  *HooksManager
    config   *HookConfig
    
    // UI state
    selectedCategory HookCategory
    searchQuery     string
    showDisabled    bool
}

// Keybindings:
// - 'h' or F9: Toggle hooks panel
// - 'space': Toggle selected hook
// - 'c': Toggle category
// - 'a': Toggle all hooks
// - '/': Search hooks
// - 's': Save configuration
```

#### Settings Integration
```go
// internal/chat/settings/hooks.go
type HooksSettings struct {
    BaseSettings
    config *hooks.HookConfig
}

func (s *HooksSettings) RenderContent() []SettingItem {
    items := []SettingItem{
        {
            Key:         "disable_all_hooks",
            Label:       "Disable All Hooks",
            Type:        SettingTypeBool,
            Value:       s.config.DisableAllHooks,
            Description: "Globally disable all hooks (including system hooks)",
        },
    }
    
    // Add category toggles
    for _, cat := range hooks.AllCategories {
        items = append(items, SettingItem{
            Key:   fmt.Sprintf("category_%s", cat),
            Label: fmt.Sprintf("%s Hooks", strings.Title(string(cat))),
            Type:  SettingTypeBool,
            Value: !contains(s.config.DisabledCategories, cat),
        })
    }
    
    return items
}
```

### 4. Implementation Plan

#### Phase 1: SDK Foundation
1. Add HookMetadata and categories to existing hooks
2. Implement configuration types and loading
3. Enhance Manager with category-based operations
4. Add configuration persistence

#### Phase 2: Builtin Hook Updates
1. Update all builtin hooks to implement new interface
2. Assign appropriate categories
3. Mark system-critical hooks
4. Add descriptions for UI display

#### Phase 3: TUI Integration
1. Create hooks dashboard component
2. Integrate with settings system
3. Add keybindings for quick toggle
4. Implement configuration save/load

#### Phase 4: Testing & Polish
1. Unit tests for toggle operations
2. Integration tests for persistence
3. UI/UX refinements
4. Documentation

## Key Decisions

### 1. Category-based Organization
Following claude-code-source's pattern of organizing by event types, but adapted for hook purposes rather than events.

### 2. Multiple Configuration Sources
Hierarchical configuration similar to claude-code-source, but simplified to user/project/runtime/policy levels.

### 3. Runtime Toggle Safety
Maintaining the existing thread-safe SetEnabled pattern while adding persistence.

### 4. System Hook Protection
Some hooks (like security or core functionality) can be marked as system hooks with optional policy protection.

## Migration Path

1. Existing hook states will be preserved
2. Default categories will be assigned based on hook names/purposes
3. Configuration files will be auto-generated on first use
4. Backward compatibility maintained for existing Toggle APIs

## Example Usage

### SDK Usage
```go
// Toggle individual hook
mgr.SetEnabled("auto-mode-hook", false)

// Toggle category
mgr.SetCategoryEnabled(hooks.CategoryDebug, false)

// Save configuration
config, _ := mgr.SaveConfig()
configBundle.SetHookConfig(config)
```

### TUI Usage
```
# In chat interface
/hooks                  # Open hooks dashboard
/hooks toggle auto-mode # Toggle specific hook
/hooks category debug   # Toggle debug category
/hooks save            # Save current configuration
```

## Benefits

1. **Unified Experience**: Same toggle behavior across SDK and TUI
2. **Persistence**: Hook preferences survive restarts
3. **Granular Control**: Toggle individual, category, or all
4. **Policy Compliance**: Respect organizational policies
5. **Developer Friendly**: Clear categorization and descriptions