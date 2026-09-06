# Builtin Tools

This package provides core built-in tools for the Go Agent SDK.

## Available Tools

### FileRead

Read file contents from the filesystem with safety checks.

**Features:**
- Read text and binary files
- File size limits (default 50MB)
- Path restrictions (optional)
- Binary file base64 encoding
- Context cancellation support
- Symlink support

**Example:**
```go
tool := builtin.NewFileReadTool()
content, err := tool.Execute(ctx, map[string]any{
    "path": "/path/to/file.txt",
})
```

### FileWrite

Write content to files with automatic directory creation.

**Features:**
- Create or overwrite files
- Automatic parent directory creation
- Path restrictions (optional)
- Context cancellation support
- Concurrent write safety

**Example:**
```go
tool := builtin.NewFileWriteTool()
result, err := tool.Execute(ctx, map[string]any{
    "path":    "/path/to/file.txt",
    "content": "Hello, World!",
})
```

### Bash

Execute shell commands with full feature support.

**Features:**
- Stdout and stderr capture
- Working directory control
- Environment variable injection
- Timeout support
- Context cancellation
- Pipes, redirections, and shell expansion
- Path restrictions for working directory (optional)

**Example:**
```go
tool := builtin.NewBashTool()
output, err := tool.Execute(ctx, map[string]any{
    "command": "echo 'Hello, World!'",
    "cwd":     "/tmp",
    "env": map[string]any{
        "MY_VAR": "value",
    },
})
```

## Configuration

All tools support custom configuration:

```go
// FileRead with size limit
fileRead := builtin.NewFileReadToolWithLimit(10 * 1024 * 1024) // 10MB

// FileRead with path restrictions
fileRead := builtin.NewFileReadToolWithConfig(builtin.FileReadToolConfig{
    MaxFileSize:  50 * 1024 * 1024,
    AllowedPaths: []string{"/workspace", "/tmp"},
})

// FileWrite with path restrictions
fileWrite := builtin.NewFileWriteToolWithConfig(builtin.FileWriteToolConfig{
    AllowedPaths: []string{"/workspace"},
    CreateDirs:   true,
    AtomicWrite:  false,
})

// Bash with custom shell and path restrictions
bash := builtin.NewBashToolWithConfig(builtin.BashToolConfig{
    Shell:        "/bin/bash",
    AllowedPaths: []string{"/workspace"},
})
```

## Security

All tools implement:
- Parameter validation
- Context cancellation support
- Error categorization (transient, permanent)
- Optional path restrictions
- Permission requirements

## Testing

All tools have comprehensive test coverage (>85%):
- Unit tests for normal operation
- Error path testing
- Concurrent execution testing (race detector)
- Context cancellation testing
- Integration scenarios

Run tests:
```bash
go test ./tests/tools/builtin/unit/ -v
go test -race ./tests/tools/builtin/unit/
go test -cover ./tests/tools/builtin/unit/ -coverpkg=./tools/builtin
```

## Tool Interface

All tools implement the standard `Tool` interface:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, params map[string]any) (string, error)
    IsIdempotent() bool
    RequiresPermission() []string
}
```

## Error Handling

Tools use the SDK error taxonomy:
- **Permanent errors**: Invalid parameters, file not found, permission denied
- **Transient errors**: Context cancelled, timeout, temporary I/O errors

Example error handling:
```go
result, err := tool.Execute(ctx, params)
if err != nil {
    if sdkerror.IsTransient(err) {
        // Retry possible
    } else {
        // Handle permanent error
    }
}
```
