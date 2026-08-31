# Claude Code Import/Export for Swarm SDK

This package provides bidirectional conversion between Claude Code and Swarm SDK conversation formats, enabling seamless data exchange between the two systems.

## Features

- **Import from Claude Code**: Convert Claude conversations to Swarm SDK format
- **Export to Claude Code**: Convert Swarm conversations to Claude format
- **Batch Operations**: Import/export entire projects with multiple conversations
- **Path Resolution**: Automatic conversion between Claude project IDs and filesystem paths
- **Data Preservation**: Maintains all conversation metadata, messages, and tool usage

## Installation

The Claude Code converter is included in the Swarm SDK. No additional installation is required.

## Usage

### Command Line Interface

The easiest way to use the import/export functionality is through the `headless` CLI:

#### Import from Claude Code

```bash
# Import a single conversation file
headless import-claude -file ~/.claude/projects/-home-user-project/conversations/conv-123.json

# Import all conversations from a Claude project
headless import-claude -project /home/user/myproject

# List available Claude projects
headless import-claude
```

#### Export to Claude Code

```bash
# Export a single conversation
headless export-claude -id <conversation-id> -output my-conv.json

# Export all conversations for a workspace
headless export-claude -workspace /home/user/myproject

# List available conversations to export
headless export-claude
```

### Programmatic Usage

You can also use the converter programmatically in your Go code:

```go
import (
    "github.com/Swarm-Code/mono/swarm-sdk/conversation/manager"
    "github.com/Swarm-Code/mono/swarm-sdk/converters/claude"
)

// Create converter
converter := claude.NewClaudeCodeConverter()

// Import from Claude format
claudeConv := &claude.ClaudeConversation{...}
swarmConv, err := converter.ImportConversation(claudeConv)

// Export to Claude format
swarmConv := &conversation.Conversation{...}
claudeConv, err := converter.ExportConversation(swarmConv)

// Batch operations
conversations, err := converter.ImportProjectConversations("/path/to/project")
err = converter.ExportToProject(conversations, "/path/to/project")
```

### Using with Manager

The conversation manager supports Claude format natively:

```go
// Export using manager
data, err := manager.Export(ctx, conversationID, manager.ExportFormatClaudeCode)

// Import using manager
conv, err := manager.Import(ctx, data, manager.ExportFormatClaudeCode)
```

## Data Mapping

### Core Fields

| Claude Code | Swarm SDK | Notes |
|-------------|-----------|-------|
| `ID` | `id` | Direct mapping |
| `Title` | `metadata.custom["title"]` | Stored as custom metadata |
| `Status` | `status` | Automatic enum conversion |
| `Mode` | `mode` | Direct mapping |
| `TokenCount` | `total_tokens` | Direct mapping |
| `CreatedAt` | `created_at` | Milliseconds to RFC3339 |
| `UpdatedAt` | `updated_at` | Milliseconds to RFC3339 |
| `ProjectID` | `workspace_path` | Automatic path conversion |

### Message Fields

| Claude Code | Swarm SDK | Notes |
|-------------|-----------|-------|
| `Role` | `role` | Direct mapping |
| `Content` | `content` | Direct mapping |
| `ToolUse[]` | `tool_calls[]` + `tool_results[]` | Split into separate arrays |
| `TokenCount` | `tokens.total` | Nested structure |
| `Timestamp` | `timestamp` | Milliseconds to RFC3339 |

### Path Conversion

Claude Code uses URL-safe project IDs, while Swarm SDK uses filesystem paths:

- `/home/user/project` ↔ `-home-user-project`
- `/var/data/app` ↔ `-var-data-app`

The converter handles this transformation automatically.

## File Organization

### Claude Code Structure
```
~/.claude/
└── projects/
    └── -home-user-project/
        ├── sessions-index.json
        └── memory/
            └── conversations/
                ├── conv-123.json
                └── conv-456.json
```

### Swarm SDK Structure
```
~/.swarmos/
└── conversations/
    ├── conv-123.json
    ├── conv-456.json
    └── conv-789/
        └── metadata/
            └── task.json
```

## Examples

### Import Claude Conversation with Error Handling

```go
converter := claude.NewClaudeCodeConverter()

// Import single file
conv, err := converter.ImportSingleConversation("path/to/conv.json")
if err != nil {
    log.Fatalf("Import failed: %v", err)
}

fmt.Printf("Imported conversation %s with %d messages\n", conv.ID, len(conv.Messages))
```

### Export with Title Preservation

```go
// Ensure title is preserved
conv.Metadata.Custom["title"] = "Important Discussion"

// Export to Claude format
claudeConv, err := converter.ExportConversation(conv)
if err != nil {
    log.Fatalf("Export failed: %v", err)
}

// claudeConv.Title will be "Important Discussion"
```

### Batch Import with Progress

```go
conversations, err := converter.ImportProjectConversations("/home/user/project")
if err != nil {
    log.Printf("Import completed with errors: %v", err)
}

for _, conv := range conversations {
    fmt.Printf("Imported: %s (%d messages)\n", conv.ID, len(conv.Messages))
}
```

## Testing

Run the tests with:

```bash
# Unit tests
go test ./converters/claude/

# Integration tests
go test -tags=integration ./converters/claude/

# With coverage
go test -cover ./converters/claude/
```

## Limitations

1. **Streaming**: Claude's streaming format is not currently supported
2. **Attachments**: Binary attachments are converted to base64 in tool results
3. **Custom Fields**: Unknown Claude fields are ignored during import
4. **Large Files**: Very large conversation files may require streaming (future enhancement)

## Troubleshooting

### Common Issues

1. **Project not found**: Ensure the Claude project directory exists at `~/.claude/projects/<project-id>/`
2. **Permission denied**: Check file permissions on Claude and Swarm directories
3. **Invalid format**: Ensure JSON files are properly formatted
4. **Missing conversations**: Check the correct project ID or workspace path

### Debug Mode

Enable debug logging for detailed conversion information:

```bash
SWARM_LOG_LEVEL=debug headless import-claude -file conv.json
```

## Future Enhancements

- Streaming import/export for large conversation sets
- Incremental sync to transfer only changed conversations
- Knowledge graph integration
- Conflict resolution for duplicate IDs
- GUI tool for visual import/export

## Contributing

Contributions are welcome! Please ensure:

1. All tests pass
2. New features include tests
3. Documentation is updated
4. Code follows project style guidelines

## License

This package is part of the Swarm SDK and follows the same license terms.