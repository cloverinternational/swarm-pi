# Agent Configuration Bundles

## Overview

Agent Configuration Bundles provide a way to export, import, share, and quickly switch between complete agent configuration sets. A bundle packages together:

- **Agent Profiles**: Role-to-model mappings (main, steering, background, sub_agent, thinking, long_context)
- **Custom Agents**: Concrete agent definitions with tools, hooks, and capabilities
- **Active selections**: Which profile/agent is currently active

## Features

✅ **Export** current configuration as a shareable JSON file  
✅ **Import** configuration bundles from JSON files  
✅ **Apply** bundles to merge them into your current setup  
✅ **Delete** bundles you no longer need  
✅ **List** all available bundles with metadata  

## Usage

### Access the Config Bundles Screen

1. Open settings (`Ctrl+,` or settings command)
2. Navigate to "Config Bundles" section (📦)

### Export Your Current Configuration

1. Press `e` in the bundles list
2. Enter a bundle name (e.g., "Team Config", "Production Setup")
3. Enter a description (optional)
4. Press `Enter` to export

Your bundle will be saved to `~/.swarmos/config_bundles/[name]-[timestamp].json`

### Import a Bundle

1. Press `i` in the bundles list
2. Enter the bundle filename or full path
3. Press `Enter` to import

The bundle will be validated and added to your bundles list.

### Apply a Bundle

1. Use arrow keys to select a bundle
2. Press `Enter`
3. Confirm with `y`

The bundle's profiles and agents will be merged into your current configuration:
- Existing configs with matching IDs will be updated
- New configs will be added
- Active profile/agent will be switched

### Delete a Bundle

1. Use arrow keys to select a bundle
2. Press `d`
3. Confirm with `y`

### Keyboard Shortcuts

- `↑/↓` or `j/k`: Navigate bundles
- `Enter`: Apply selected bundle
- `e`: Export current config
- `i`: Import bundle
- `d`: Delete bundle
- `r`: Refresh list
- `Esc`: Back to settings

## Bundle File Format

Bundles are JSON files with the following structure:

```json
{
  "version": "1.0",
  "name": "My Config",
  "description": "Configuration for XYZ project",
  "created_at": "2026-02-07T...",
  "author": "username",
  "active_profile": "balanced",
  "active_agent": "general-assistant",
  "profiles": {
    "default_profile": "balanced",
    "profiles": [...]
  },
  "custom_agents": {
    "default_agent": "general-assistant",
    "agents": [...]
  },
  "tags": ["development", "team"],
  "metadata": {}
}
```

## Use Cases

### Team Collaboration
Export your config and share the JSON file with team members. They can import and apply it to match your setup.

### Environment Switching
Create bundles for different contexts:
- `development-config`: Fast, cheap models for rapid iteration
- `production-config`: High-quality models for production
- `review-config`: Specialized agents for code review

### Backup & Migration
Export configs before making changes, or when migrating to a new machine.

### A/B Testing
Create bundles with different model configurations and switch between them to compare results.

## File Locations

- **Bundle storage**: `~/.swarmos/config_bundles/`
- **Agent profiles**: `~/.swarmos/agent_profiles.json`
- **Custom agents**: `~/.swarmos/custom_agents.json`

## API Usage (Programmatic)

```go
import "github.com/Swarm-Code/mono/swarmos-tui/internal/chat/settings"

// Create bundle manager
bundleMgr := settings.NewBundleManager()

// Export current config
bundle, _ := bundleMgr.CreateBundle("My Config", "Description", profiles, agents)
bundleMgr.ExportBundle(bundle, "my-config")

// Import bundle
bundle, _ := bundleMgr.ImportBundle("my-config")

// Apply bundle
bundleMgr.ApplyBundle(bundle, profileMgr, agentSettings)

// List bundles
bundles, _ := bundleMgr.ListBundles()
```

## Tips

1. **Naming**: Use descriptive names that indicate the bundle's purpose
2. **Tags**: Add tags for easy searching (future feature)
3. **Versioning**: Include version info in the description
4. **Security**: Review bundles before applying them (no secrets are exported)
5. **Backups**: Export before making major changes

## Implementation Details

### Architecture

```
BundleManager (agent_config_bundle.go)
    ├─> CreateBundle()    # Package current config
    ├─> ExportBundle()    # Save to JSON
    ├─> ImportBundle()    # Load from JSON
    ├─> ApplyBundle()     # Merge into system
    └─> ListBundles()     # Enumerate available

ConfigBundlesSettings (config_bundles.go)
    ├─> UI state machine (list/export/import/confirm)
    ├─> Form handling
    └─> Integration with Manager

Settings Manager (manager.go)
    ├─> Section: SectionConfigBundles
    ├─> Render integration
    └─> Key handling
```

### Validation

Bundles are validated on import:
- Version compatibility
- Profile structure integrity
- Agent definition completeness
- Required fields presence

### Atomic Operations

- Exports use temp files + rename for atomic writes
- Imports validate before applying
- Applies update configs individually (partial success possible)

## Future Enhancements

- [ ] Bundle versioning and migration
- [ ] Selective import (choose which parts to import)
- [ ] Bundle diff/comparison
- [ ] Cloud sync for bundles
- [ ] Bundle marketplace/sharing platform
- [ ] Automatic bundle creation on config changes
- [ ] Bundle templates for common setups

## Troubleshooting

**Bundle import fails with validation error**
- Check bundle JSON syntax
- Ensure version compatibility
- Verify all required fields are present

**Bundle not appearing in list**
- Check `~/.swarmos/config_bundles/` directory
- Verify file has `.json` extension
- Press `r` to refresh list

**Apply fails partway through**
- Some configs may have been applied
- Check error message for details
- Re-export to capture current state

## Related Documentation

- [Agent Profiles](./agent_profiles.md) - Role-based model configuration
- [Custom Agents](./custom_agents.md) - Agent definition format
- [Settings System](./README.md) - General settings architecture
