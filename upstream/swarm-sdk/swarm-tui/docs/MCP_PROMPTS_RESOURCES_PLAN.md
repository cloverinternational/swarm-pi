# MCP Prompts & Resources Implementation Plan

## Overview

This plan adds comprehensive MCP support for:
1. **Resources** - Exposed as `@resource` mentions (like file `@` mentions)
2. **Prompts** - Exposed as `/prompt` commands (like skill commands)

Plus an extensible architecture for future plugins to add their own @ mentions and / commands.

## Current State

### Existing Infrastructure
- **SDK MCP Client** (`sdk/tools/mcp/client.go`):
  - `GetPrompts()`, `GetPrompt(name)` - List/get prompts
  - `GetResources()`, `GetResource(uri)`, `ReadResource(uri)` - List/read resources
  - Already discovers prompts and resources on connect

- **TUI MCP Manager** (`internal/chat/mcp_manager.go`):
  - Manages MCP server connections
  - Currently only registers tools, not prompts/resources

- **File Autocomplete** (`internal/chat/file_autocomplete.go`):
  - Triggered by `@` character
  - Shows file/directory matches
  - Not extensible for other @ providers

- **Slash Commands** (`internal/chat/slash_commands.go`):
  - Triggered by `/` character
  - Hardcoded command lookup
  - Not extensible for other / providers

## Architecture Design

### 1. Mention Provider Interface (@ mentions)

```go
// MentionProvider provides @ mention completions
type MentionProvider interface {
    // ID returns unique provider identifier (e.g., "file", "mcp_resource")
    ID() string

    // Trigger returns the trigger character(s) after @ (empty = just @)
    // e.g., "" for @file, "r:" for @r:resource
    Trigger() string

    // Icon returns the icon for this provider's section
    Icon() string

    // Label returns the label for this provider's section
    Label() string

    // GetMatches returns matches for the given fragment
    GetMatches(ctx context.Context, fragment string) []MentionMatch

    // Resolve returns the content for a selected match
    Resolve(ctx context.Context, match MentionMatch) (string, error)
}

type MentionMatch struct {
    ID          string  // Unique ID for this match
    Display     string  // Display text in autocomplete
    Description string  // Optional description
    Icon        string  // Icon to show
    Provider    string  // Provider ID
    Data        any     // Provider-specific data
}
```

### 2. Command Provider Interface (/ commands)

```go
// CommandProvider provides / command completions
type CommandProvider interface {
    // ID returns unique provider identifier
    ID() string

    // Prefix returns the prefix for this provider (e.g., "mcp:", "skill:")
    // Empty string = no prefix required
    Prefix() string

    // Icon returns the icon for this provider's section
    Icon() string

    // Label returns the label for this provider's section
    Label() string

    // GetCommands returns available commands
    GetCommands(ctx context.Context) []CommandMatch

    // GetMatches returns matches for the given fragment
    GetMatches(ctx context.Context, fragment string) []CommandMatch

    // Execute executes the command and returns a tea.Cmd
    Execute(ctx context.Context, name string, args string) tea.Cmd
}

type CommandMatch struct {
    Name        string   // Command name
    Description string   // Description
    Icon        string   // Icon
    Args        []string // Expected arguments
    Provider    string   // Provider ID
    Data        any      // Provider-specific data
}
```

### 3. Unified Autocomplete Manager

```go
type AutocompleteManager struct {
    mentionProviders []MentionProvider
    commandProviders []CommandProvider

    // State
    active      bool
    triggerType string  // "mention" or "command"
    fragment    string
    matches     []AutocompleteItem
    selected    int
}

type AutocompleteItem struct {
    Type        string  // "mention" or "command"
    Display     string
    Description string
    Icon        string
    Section     string  // Section header (provider label)
    Data        any
}
```

## Implementation Phases

### Phase 1: Create Provider Interfaces
**Files to create:**
- `internal/chat/autocomplete/provider.go` - Interfaces
- `internal/chat/autocomplete/manager.go` - Unified manager

### Phase 2: Migrate File Autocomplete
**Files to modify:**
- `internal/chat/file_autocomplete.go` → `internal/chat/autocomplete/file_provider.go`
- Implement MentionProvider interface

### Phase 3: MCP Resource Provider
**Files to create:**
- `internal/chat/autocomplete/mcp_resource_provider.go`
- Queries MCPManager for resources
- Reads resource content on resolve

### Phase 4: Migrate Built-in Commands
**Files to modify:**
- `internal/chat/slash_commands.go` → `internal/chat/autocomplete/builtin_command_provider.go`
- Implement CommandProvider interface

### Phase 5: MCP Prompt Provider
**Files to create:**
- `internal/chat/autocomplete/mcp_prompt_provider.go`
- Queries MCPManager for prompts
- Executes prompts and inserts messages

### Phase 6: Skills Command Provider
**Files to create:**
- `internal/chat/autocomplete/skill_command_provider.go`
- Wraps existing skills as commands

### Phase 7: Integrate with Chat Input
**Files to modify:**
- `internal/chat/app.go` - Wire up new autocomplete manager
- `internal/chat/components.go` - Update input handling

## Detailed Component Design

### MCP Resource Provider

```go
type MCPResourceProvider struct {
    mcpManager *MCPManager
}

func (p *MCPResourceProvider) ID() string { return "mcp_resource" }
func (p *MCPResourceProvider) Trigger() string { return "" }  // Just @
func (p *MCPResourceProvider) Icon() string { return "󰒍" }  // MCP icon
func (p *MCPResourceProvider) Label() string { return "MCP Resources" }

func (p *MCPResourceProvider) GetMatches(ctx context.Context, fragment string) []MentionMatch {
    var matches []MentionMatch

    for _, state := range p.mcpManager.servers {
        if !state.Connected {
            continue
        }

        resources := state.Client.GetResources()
        for _, res := range resources {
            // Filter by fragment
            if !matchesFragment(res.Name, fragment) && !matchesFragment(res.URI, fragment) {
                continue
            }

            matches = append(matches, MentionMatch{
                ID:          res.URI,
                Display:     fmt.Sprintf("%s:%s", state.Config.Name, res.Name),
                Description: res.Description,
                Icon:        getResourceIcon(res.MimeType),
                Provider:    "mcp_resource",
                Data:        res,
            })
        }
    }

    return matches
}

func (p *MCPResourceProvider) Resolve(ctx context.Context, match MentionMatch) (string, error) {
    res := match.Data.(*mcp.MCPResource)

    // Find the client that owns this resource
    for _, state := range p.mcpManager.servers {
        if r, ok := state.Client.GetResource(res.URI); ok {
            contents, err := state.Client.ReadResource(ctx, r.URI)
            if err != nil {
                return "", err
            }
            return formatResourceContent(res, contents), nil
        }
    }

    return "", fmt.Errorf("resource not found: %s", res.URI)
}
```

### MCP Prompt Provider

```go
type MCPPromptProvider struct {
    mcpManager *MCPManager
    registry   *commands.Registry
}

func (p *MCPPromptProvider) ID() string { return "mcp_prompt" }
func (p *MCPPromptProvider) Prefix() string { return "" }  // No prefix needed
func (p *MCPPromptProvider) Icon() string { return "󰒍" }
func (p *MCPPromptProvider) Label() string { return "MCP Prompts" }

func (p *MCPPromptProvider) GetCommands(ctx context.Context) []CommandMatch {
    var commands []CommandMatch

    for _, state := range p.mcpManager.servers {
        if !state.Connected {
            continue
        }

        prompts := state.Client.GetPrompts()
        for _, prompt := range prompts {
            commands = append(commands, CommandMatch{
                Name:        fmt.Sprintf("%s:%s", state.Config.Name, prompt.Name),
                Description: prompt.Description,
                Icon:        "󰒍",
                Args:        getPromptArgNames(prompt),
                Provider:    "mcp_prompt",
                Data:        promptData{Server: state.Config.Name, Prompt: prompt},
            })
        }
    }

    return commands
}

func (p *MCPPromptProvider) Execute(ctx context.Context, name string, args string) tea.Cmd {
    // Parse server:prompt from name
    // Parse arguments
    // Call MCP server to get prompt
    // Return command that inserts prompt messages
}
```

## UI/UX Design

### @ Mention Autocomplete
```
┌─────────────────────────────────────────┐
│ Files                                    │
│   📄 src/main.go                        │
│   📁 internal/chat/                      │
├─────────────────────────────────────────┤
│ 󰒍 MCP Resources                         │
│   󰈙 filesystem:config.json              │
│   󰗀 database:users/schema               │
│   󰌷 github:repo/readme                  │
└─────────────────────────────────────────┘
```

### / Command Autocomplete
```
┌─────────────────────────────────────────┐
│ Commands                                 │
│   /help - Show help                      │
│   /clear - Clear chat                    │
│   /compact - Compact context             │
├─────────────────────────────────────────┤
│ 󱜚 Skills                                 │
│   /commit - Create git commit            │
│   /review-pr - Review pull request       │
├─────────────────────────────────────────┤
│ 󰒍 MCP Prompts                           │
│   /fs:analyze-code [path]                │
│   /db:query-builder [table]              │
└─────────────────────────────────────────┘
```

## File Structure

```
internal/chat/
├── autocomplete/
│   ├── provider.go           # Interfaces
│   ├── manager.go            # Unified autocomplete manager
│   ├── file_provider.go      # File @ mentions (migrated)
│   ├── mcp_resource_provider.go  # MCP resource @ mentions
│   ├── builtin_command_provider.go  # Built-in / commands
│   ├── mcp_prompt_provider.go    # MCP prompt / commands
│   └── skill_provider.go     # Skill / commands
├── mcp_manager.go            # Modified to expose prompts/resources
└── app.go                    # Wire up autocomplete manager
```

## Migration Strategy

1. Create new `autocomplete/` package with interfaces
2. Implement file provider (copy from file_autocomplete.go)
3. Test file autocomplete still works
4. Add MCP resource provider
5. Test resource mentions
6. Implement command providers
7. Test command completions
8. Remove old file_autocomplete.go
9. Update app.go to use new system

## API Additions to MCPManager

```go
// Add to MCPManager

// GetAllResources returns resources from all connected servers
func (m *MCPManager) GetAllResources() map[string][]*mcp.MCPResource {
    result := make(map[string][]*mcp.MCPResource)
    for name, state := range m.servers {
        if state.Connected && state.Client != nil {
            result[name] = state.Client.GetResources()
        }
    }
    return result
}

// GetAllPrompts returns prompts from all connected servers
func (m *MCPManager) GetAllPrompts() map[string][]*mcp.MCPPrompt {
    result := make(map[string][]*mcp.MCPPrompt)
    for name, state := range m.servers {
        if state.Connected && state.Client != nil {
            result[name] = state.Client.GetPrompts()
        }
    }
    return result
}

// ReadResource reads a resource from a specific server
func (m *MCPManager) ReadResource(ctx context.Context, serverName, uri string) (*mcp.ResourceContents, error)

// GetPrompt gets a prompt with arguments from a specific server
func (m *MCPManager) GetPrompt(ctx context.Context, serverName, promptName string, args map[string]string) (*mcp.PromptResult, error)
```

## Next Steps

1. Start with Phase 1: Create provider interfaces
2. Implement in order of phases
3. Test each phase before moving to next
4. Document new APIs
