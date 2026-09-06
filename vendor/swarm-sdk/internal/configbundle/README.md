# ConfigBundle - Unified Configuration System

ConfigBundle provides a unified configuration system for Swarm that consolidates all settings into a single config file with support for hierarchical layering (global + project).

## Overview

The config bundle system replaces the fragmented config file approach with a unified structure:

- **Before**: Multiple files (`config.json`, `providers.json`, `agent_profiles.json`, `hooks.json`, `mcp_servers.json`, `credentials.json`)
- **After**: Single unified `.swarm/config.json` with all settings

### Key Features

- **Unified Schema**: All config aspects in one file
- **Hierarchical Layering**: Global config can be overridden by project config
- **Config Switching**: Switch between global and project config at runtime
- **Auto-Detection**: Automatically detects project config when entering directories
- **Migration Support**: Migrates from legacy fragmented config files
- **Security-Conscious**: Project credentials cannot override global credentials

## Config File Locations

### Global Config
```
~/.swarmos/config.json
```

The global config contains:
- Default settings for all projects
- API keys and credentials (stored securely)
- Global agent profiles and providers

### Project Config
```
<project-directory>/.swarm/config.json
```

The project config contains:
- Project-specific settings
- Project-specific agents and tools
- Additional credentials (cannot override global)

## Schema Structure

```json
{
  "schemaVersion": 1,
  "name": "My Project Config",
  "description": "Project-specific configuration",
  
  "system": {
    "defaultProvider": "anthropic",
    "defaultModel": "claude-3-5-sonnet",
    "theme": "dark",
    "maxOutputLines": 200
  },
  
  "agents": {
    "defaultAgent": "developer",
    "definitions": [
      {
        "id": "developer",
        "name": "Developer Agent",
        "systemPrompt": "You are a helpful developer assistant.",
        "tools": ["edit", "bash", "read"]
      }
    ]
  },
  
  "profiles": {
    "mode": "inline",
    "inline": [
      {
        "id": "main",
        "name": "Main Profile",
        "provider": "anthropic",
        "model": "claude-3-5-sonnet",
        "temperature": 0.7
      }
    ]
  },
  
  "prompts": {
    "custom": {
      "review": "Review the following code changes..."
    }
  },
  
  "tools": {
    "enabled": ["edit", "bash", "read", "grep"],
    "disabled": ["websearch"]
  },
  
  "hooks": {
    "definitions": [
      {
        "id": "post-edit",
        "name": "Post Edit Hook",
        "event": "tool:edit:after",
        "command": "./scripts/format.sh"
      }
    ]
  },
  
  "skills": {
    "installed": [
      {"id": "test-runner", "name": "Test Runner", "path": "./skills/test-runner"}
    ]
  },
  
  "providers": {
    "mode": "inline",
    "inline": [
      {
        "id": "anthropic",
        "name": "Anthropic",
        "type": "anthropic",
        "models": [
          {"id": "claude-3-5-sonnet", "name": "Claude 3.5 Sonnet"}
        ]
      }
    ]
  },
  
  "credentials": {
    "inherit": true,
    "providerKeys": {
      "openai": "project-specific-key"
    }
  },
  
  "mcpServers": {
    "servers": [
      {
        "id": "filesystem",
        "name": "Filesystem MCP",
        "type": "stdio",
        "command": "mcp-filesystem",
        "args": ["/project/path"]
      }
    ]
  },
  
  "mergePolicy": {
    "system": "override",
    "agents": "merge",
    "hooks": "append"
  }
}
```

## Merge Modes

When a project config is active, it merges with the global config according to merge modes:

### `override`
Project section completely replaces global section.

### `merge`
Project values override global values with same keys, but other global values are preserved.

### `append`
Project items are appended to global list (used for hooks, skills, MCP servers).

## Security

### Credential Inheritance

Project credentials **cannot override** global credentials. This is a security feature to prevent:
- Accidental exposure of global API keys
- Unauthorized modification of shared credentials
- Project-specific config from affecting global settings

```json
// Global config
{
  "credentials": {
    "providerKeys": {
      "anthropic": "global-anthropic-key"
    }
  }
}

// Project config - this CANNOT override the anthropic key
{
  "credentials": {
    "inherit": true,
    "providerKeys": {
      "anthropic": "different-key",  // IGNORED - global key is used
      "openai": "project-openai-key" // ADDED - new key for project
    }
  }
}
```

## Usage

### Programmatic Usage

```go
import "github.com/Swarm-Code/mono/swarm-sdk/configbundle"

// Create manager
mgr, err := configbundle.NewManager(ctx, configbundle.Options{
    WorkDir:    "/path/to/project",
    AutoSwitch: true,
})

// Check if using project config
if mgr.IsUsingProject() {
    fmt.Println("Using project config:", mgr.ProjectName())
}

// Switch between configs
mgr.UseProject()  // Switch to project config
mgr.UseGlobal()   // Switch to global config
mgr.Toggle()      // Toggle between them

// Get active config
active := mgr.Active()
fmt.Println("Default provider:", active.System.DefaultProvider)

// Save changes
mgr.Save(ctx)
```

### TUI Integration

The config bundle system integrates with the swarm-tui:

1. **Config Source Indicator**: The tab bar shows "🌍 Global" or "📦 ProjectName"
2. **Settings Panel**: Switch between configs with `[S]` key
3. **Auto-Detection**: Prompts when entering a directory with project config

## Migration

To migrate from legacy fragmented config files:

```go
import "github.com/Swarm-Code/mono/swarm-sdk/configbundle"

migration := configbundle.NewMigration("/home/user/.swarmos")
bundle, err := migration.MigrateToBundle(ctx, "/home/user/.swarmos/config_bundle.json")
```

## API Reference

### Manager

```go
type Manager struct { ... }

func NewManager(ctx context.Context, opts Options) (*Manager, error)
func (m *Manager) Global() *ConfigBundle
func (m *Manager) Project() *ConfigBundle
func (m *Manager) Active() *ConfigBundle
func (m *Manager) IsUsingProject() bool
func (m *Manager) HasProjectConfig() bool
func (m *Manager) ProjectName() string
func (m *Manager) UseProject() error
func (m *Manager) UseGlobal()
func (m *Manager) Toggle() bool
func (m *Manager) Save(ctx context.Context) error
func (m *Manager) Refresh(ctx context.Context) error
func (m *Manager) CreateProject(path string) (*ConfigBundle, error)
```

### Detector

```go
type Detector struct { ... }

func NewDetector(opts DetectorOptions) *Detector
func (d *Detector) Start(ctx context.Context, workDir string)
func (d *Detector) Stop()
func (d *Detector) OnDetect(callback func(*ProjectConfigInfo))
func (d *Detector) OnLost(callback func())

func FindProjectConfig(startDir string) string
func DetectProjectConfig(workDir string) (*ProjectConfigInfo, error)
```

### Migration

```go
type Migration struct { ... }

func NewMigration(configDir string) *Migration
func (m *Migration) MigrateToBundle(ctx context.Context, outputPath string) (*ConfigBundle, error)
func (m *Migration) NeedsMigration() bool
```

## Best Practices

1. **Use Project Config for Project-Specific Settings**
   - Agent definitions specific to the project
   - Project-specific tools and hooks
   - Project-specific MCP servers

2. **Keep Global Config for Shared Settings**
   - API keys that are used across projects
   - Common agent profiles
   - Default settings

3. **Version Control**
   - Add `.swarm/config.json` to version control
   - Add `.swarm/credentials.json` to `.gitignore` if storing project-specific keys
   - Or use environment variables for sensitive data

4. **Git Ignore Pattern**
   ```
   # .gitignore
   .swarm/credentials.json
   .swarm/*.local.json
   ```

## Future Enhancements

- [ ] Environment-specific configurations (dev/staging/prod)
- [ ] Config inheritance chains (global → team → project)
- [ ] Config validation and linting
- [ ] Config diffing and visualization
- [ ] Remote config sync
