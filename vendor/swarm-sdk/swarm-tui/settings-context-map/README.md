# Settings Context Map

This directory contains comprehensive documentation of all settings screens in the SwarmCode TUI.

## Overview

The TUI settings system is organized into **6 major groups** with **20+ individual settings screens**:

### 1. AI Config
- Model & Provider
- System Prompts
- Agents
- Agent Profiles
- Compaction

### 2. Tools & Integrations
- Tools and MCP (Model Context Protocol)
- Hooks
- Skills
- Plugins
- Context Sources

### 3. Appearance
- Display

### 4. Security & Auth
- Security
- Authentication
- Cloud
- Proxies

### 5. Performance
- Cache Statistics
- Reliability

### 6. Advanced
- General
- Config Bundles
- Advanced

## Documentation Files

Each settings screen has detailed documentation including:
- Functionality overview
- Screen states and navigation
- Interactive elements (buttons, inputs, toggles)
- Data flow and state management
- Mermaid diagrams showing relationships

## Files in this Directory

- `00-architecture.md` - Overall settings system architecture
- `01-ai-config/` - AI configuration screens
- `02-tools-integrations/` - Tools and integration screens
- `03-appearance/` - Display and UI customization
- `04-security-auth/` - Security and authentication
- `05-performance/` - Performance optimization screens
- `06-advanced/` - Advanced configuration
- `state-management.md` - Comprehensive state management guide
- `navigation-flow.md` - Navigation and interaction patterns

## Quick Reference

| Screen | Primary Function | Key Features |
|--------|-----------------|--------------|
| Model & Provider | Configure AI model | Provider selection, model switching, API config |
| System Prompts | Manage prompts | Create, edit, activate prompts |
| Agents | Custom agent management | Create agents with specific configurations |
| Agent Profiles | Role-based model assignment | Assign different models to agent roles |
| MCP | Tool & server management | Enable/disable tools, configure MCP servers |
| Hooks | Event-driven automation | Configure hooks for tool events |
| Security | Permission controls | Tool access rules, security levels |
| Display | UI customization | Rendering options, animations, themes |
| Context Sources | Project context | Configure context loading sources |

## State Management Overview

The settings system uses a centralized `State` struct (defined in `types.go`) with:
- **373 lines** of state definitions
- Per-screen state management
- Navigation state tracking
- Form editing state
- Modal and dialog state

See `state-management.md` for complete details.