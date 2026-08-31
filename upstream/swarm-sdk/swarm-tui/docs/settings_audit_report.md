# TUI Settings System Audit Report

## Executive Summary

A comprehensive audit of the TUI settings system identified **8 critical issues** affecting settings save/propagation. The issues range from silent save failures to architectural inconsistencies that could cause settings to be lost or not propagate correctly.

---

## Critical Issues Found

### 1. Inconsistent Save Mechanisms (HIGH PRIORITY)

Different settings use completely different save patterns:

| Setting File | Save Method | Return Type | Issue |
|--------------|-------------|-------------|-------|
| `steering.go` | Config Bundle via IPC | `tea.Cmd` | ✅ Correct - Async pattern |
| `dream.go` | ConfigManager direct | `void` | ❌ Synchronous, no error handling |
| `plan.go` | ConfigManager direct | `void` | ❌ Synchronous, no error handling |
| `websearch_settings.go` | ConfigManager direct | `void` | ❌ Synchronous, no error handling |
| `findings_settings.go` | ConfigManager direct | `tea.Cmd/void` | ❌ Two different patterns |
| `compaction.go` | Via picker callback | `void` | ⚠️ Complex callback chain |
| `reliability.go` | ConfigManager via updateConfig | `error` | ⚠️ OK but inconsistent |
| `general.go` | ConfigManager direct | `void` | ❌ Silent failures |
| `computer_use.go` | ConfigManager direct | `void` | ❌ Callback may not fire |
| `agents.go` | ConfigManager direct | `error` | ⚠️ Returns error but ignored |
| `security.go` | Via callbacks | `tea.Cmd` | ✅ Uses external callbacks |
| `display.go` | Via onDisplaySettingsSync | `tea.Cmd` | ✅ Uses external callback |

**Root Cause**: No standardized save pattern. Each setting invents its own approach.

---

### 2. Duplicate ConfigManager Instances (HIGH PRIORITY)

**Location**: Multiple files create their own ConfigManager:

- `dream.go:38` - `cm, _ := commands.NewConfigManager()`
- `plan.go:26` - `cm, _ := commands.NewConfigManager()`
- `websearch_settings.go:41` - `cm, _ := commands.NewConfigManager()`
- `findings_settings.go:45` - `cm, _ := commands.NewConfigManager()`
- `compaction.go:27` - `cm, err := commands.NewConfigManager()`
- `reliability.go:56` - `cm, _ := commands.NewConfigManager()`
- `general.go:24` - `cm, _ := commands.NewConfigManager()`
- `computer_use.go:23` - `cm, _ := commands.NewConfigManager()`

**Problem**: Multiple ConfigManagers can race when saving to the same config.json file, causing:
- Lost updates
- Corrupted config
- Inconsistent state between settings

**Solution**: Pass ConfigManager from App/Manager to all settings.

---

### 3. State.Dirty Flag Not Propagated (MEDIUM PRIORITY)

**Location**: `types.go:192`
```go
type State struct {
    Dirty bool  // true when any setting has been changed since last reset
}
```

**Problem**: The `Dirty` flag is set in the State struct but:
- Never checked by the parent App
- Never triggers any notification
- Never prompts user to restart/reload
- Reset mechanism unclear

**Impact**: Users make settings changes but the app doesn't know it might need to reload or notify about pending changes.

---

### 4. Steering Save May Fail Silently (MEDIUM PRIORITY)

**Location**: `steering.go:106-112`
```go
func (s *SteeringSettings) save() tea.Cmd {
    if s.onConfigChange == nil {
        return nil  // ❌ Silently does nothing
    }
    cfg := s.toConfig()
    return s.onConfigChange(s.scope, cfg)
}
```

**Problem**: 
- If `SetSteeringCallbacks()` wasn't called, all steering changes are lost
- No error notification to user
- UI shows changes but they don't persist

**Location**: `steering.go:194-367`
```go
func (s *SteeringSettings) HandleKey(key string, state *State) tea.Cmd {
    // ... toggle enabled ...
    return s.save()  // Returns tea.Cmd
}
```

**Problem**: While it returns `tea.Cmd`, if the callback is nil, it returns `nil` and the change is lost.

---

### 5. Findings Has Two Different Save Functions (MEDIUM PRIORITY)

**Location**: `findings_settings.go`

Function 1 (lines 164-190):
```go
func (f *FindingsSettings) save() tea.Cmd {
    // ... saves findings config
    // ... fires onChange callback
}
```

Function 2 (lines 192-215):
```go
func (f *FindingsSettings) saveAnalysis() {
    // ... saves analysis settings only
    // ... fires onAnalysisChange callback
}
```

**Problem**: Inconsistent API. One returns `tea.Cmd`, other returns nothing. Both have different callback patterns.

---

### 6. ComputerUse Callback/State Mismatch (LOW PRIORITY)

**Location**: `computer_use.go:44-62`
```go
func (s *ComputerUseSettings) SetEnabled(enabled bool) {
    s.enabled = enabled
    
    // Persist to config
    if s.configManager != nil {
        config, err := s.configManager.LoadConfig()
        if err != nil {
            config = &commands.SwarmOSConfig{}
        }
        config.ComputerUseEnabled = enabled
        _ = s.configManager.SaveConfig(config)  // ❌ Error ignored
    }
    
    // Notify callback
    if s.onEnabledChange != nil {
        s.onEnabledChange(enabled)  // ❌ Fires even if save failed
    }
}
```

**Problem**: 
- Save error ignored (line 55)
- Callback fires regardless of save success
- UI state may not match persisted state

---

### 7. No Centralized Settings Coordinator (ARCHITECTURAL)

**Current State**: Each setting manages its own persistence independently.

**Problems**:
- No atomic transactions (if one setting fails, others may succeed)
- No rollback capability
- No batch updates
- No cross-setting consistency checks
- Duplicate save logic across files

**Example of duplication**: Every file has this same pattern:
```go
cfg, err := cm.LoadConfig()
if err != nil {
    cfg = &commands.SwarmOSConfig{}
}
// ... modify cfg ...
_ = cm.SaveConfig(cfg)  // ❌ Error ignored everywhere
```

---

### 8. Silent Save Failures (HIGH PRIORITY)

**Location**: Multiple files ignore save errors:

- `dream.go:712` - `_ = d.configManager.SaveConfig(config)`
- `plan.go:149` - `_ = p.configManager.SaveConfig(cfg)`
- `websearch_settings.go:147` - `_ = w.configManager.SaveConfig(cfg)`
- `general.go:74` - `_ = g.configManager.SaveConfig(cfg)`
- `computer_use.go:55` - `_ = s.configManager.SaveConfig(config)`

**Problem**: All save errors are silently ignored. User toggles a setting, UI updates, but if save fails:
- No notification
- No error message
- Setting reverts on restart

---

## Files Requiring Attention

| File | Issues | Priority |
|------|--------|----------|
| `internal/chat/settings/dream.go` | Sync save (line 695), own ConfigManager (line 38), silent save (line 712) | HIGH |
| `internal/chat/settings/plan.go` | Sync save (line 138), own ConfigManager (line 26), silent save (line 149) | HIGH |
| `internal/chat/settings/websearch_settings.go` | Sync save (line 133), own ConfigManager (line 41), silent save (line 147) | HIGH |
| `internal/chat/settings/findings_settings.go` | Dual save functions (lines 164, 192), own ConfigManager (line 45) | MEDIUM |
| `internal/chat/settings/compaction.go` | Complex callback chain (line 64), own ConfigManager (line 27) | MEDIUM |
| `internal/chat/settings/general.go` | Silent save (line 74), own ConfigManager (line 24) | MEDIUM |
| `internal/chat/settings/computer_use.go` | Callback pattern issue (line 59), own ConfigManager (line 23) | MEDIUM |
| `internal/chat/settings/reliability.go` | Own ConfigManager (line 56) | LOW |
| `internal/chat/settings/steering.go` | Needs callback verification (lines 106-112, 514) | MEDIUM |
| `internal/chat/settings/types.go` | Dirty flag unused (line 192) | LOW |
| `internal/chat/settings/manager.go` | Needs dirty flag propagation (lines 48-106) | MEDIUM |

---

## Recommended Solutions

### Solution 1: Centralized Settings Coordinator

Create a `SettingsCoordinator` that manages all saves:

```go
// internal/chat/settings/coordinator.go
type SettingsCoordinator struct {
    configManager *commands.ConfigManager
    dirtySections map[Section]bool
    onChange      func(Section) tea.Cmd
    onError       func(error)
}

func (c *SettingsCoordinator) Save(section Section, update func(*commands.SwarmOSConfig)) tea.Cmd {
    return func() tea.Msg {
        cfg, err := c.configManager.LoadConfig()
        if err != nil {
            cfg = &commands.SwarmOSConfig{}
        }
        
        update(cfg)
        
        if err := c.configManager.SaveConfig(cfg); err != nil {
            c.onError(fmt.Errorf("%s: %w", section, err))
            return SettingsSaveFailedMsg{Section: section, Error: err}
        }
        
        c.dirtySections[section] = true
        if c.onChange != nil {
            return c.onChange(section)
        }
        return SettingsSavedMsg{Section: section}
    }
}
```

### Solution 2: Dependency Injection for ConfigManager

Modify all settings constructors to receive ConfigManager:

```go
// Before (dream.go:37-59)
func NewDreamSettings() *DreamSettings {
    cm, _ := commands.NewConfigManager()  // ❌ Creates new instance
    ...
}

// After
func NewDreamSettings(cm *commands.ConfigManager) *DreamSettings {
    // Use provided ConfigManager
    return &DreamSettings{
        configManager: cm,
        ...
    }
}
```

In `manager.go:124-169`, pass the same ConfigManager instance:
```go
func NewManager(...) *Manager {
    cm, _ := commands.NewConfigManager()  // Create once
    
    manager := &Manager{
        dream: NewDreamSettings(cm),      // Pass to all
        plan: NewPlanSettings(cm),
        webSearch: NewWebSearchSettings(cm),
        ...
    }
}
```

### Solution 3: Standardize Save Pattern

All settings should use async save with proper error handling:

```go
// Standard pattern for all settings
func (s *XSettings) ToggleEnabled() tea.Cmd {
    s.enabled = !s.enabled
    return s.coordinator.Save(SectionX, func(cfg *commands.SwarmOSConfig) {
        cfg.XEnabled = s.enabled
    })
}
```

### Solution 4: Wire Dirty Flag to App

Add callback in Manager:

```go
// manager.go
func (m *Manager) SetOnSettingsDirty(fn func(Section) tea.Cmd) {
    m.onSettingsDirty = fn
}

// In each setting's save:
func (s *XSettings) save() tea.Cmd {
    return func() tea.Msg {
        // ... save ...
        if m.onSettingsDirty != nil {
            return m.onSettingsDirty(SectionX)
        }
        return nil
    }
}
```

---

## Related Files

- `internal/chat/app_init.go` (lines 2055-2094) - Steering callback wiring example
- `internal/chat/commands/config.go` (lines 120-200) - Config persistence layer
- `internal/chat/settings/manager.go` (lines 504-517) - SetSteeringCallbacks pattern

---

## Testing Recommendations

1. **Add integration tests** for settings save/load roundtrip
2. **Add race condition tests** for concurrent settings changes
3. **Add failure injection tests** for save errors
4. **Verify** that all settings properly reload on app restart

---

*Audit completed: All findings documented with line numbers and specific code examples.*
