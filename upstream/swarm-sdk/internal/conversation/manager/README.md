# ConversationManager

Complete conversation lifecycle management for the Go Agent SDK. Provides conversation creation, message handling, context window optimization, and multi-format export/import capabilities.

## Features

- 🎯 **Conversation Lifecycle**: Create, resume, complete, archive, and delete conversations
- 📝 **Message Management**: Add, retrieve, and filter messages with flexible options
- 🪟 **Context Window Strategies**: Automatic context trimming with LRU, Sliding Window, or Priority strategies
- 📤 **Multi-Format Export**: JSON, Markdown, HTML, and JSONL formats
- 📥 **Multi-Format Import**: Round-trip JSON and JSONL support
- 🔍 **Flexible Filtering**: Filter messages by role, time range, with pagination support
- 📊 **Token Tracking**: Automatic token counting and context window management
- 🔒 **Thread-Safe**: Concurrent-safe operations
- 📍 **Observable**: Comprehensive logging and distributed tracing
- ✅ **Well-Tested**: 22 unit and integration tests with race detection

## Installation

```go
import "github.com/Swarm-Code/mono/swarm-sdk/conversation/manager"
```

## Quick Start

```go
package main

import (
    "context"
    "log"
    
    "github.com/Swarm-Code/mono/swarm-sdk/conversation"
    "github.com/Swarm-Code/mono/swarm-sdk/conversation/manager"
    "github.com/Swarm-Code/mono/swarm-sdk/conversation/storage"
    "github.com/Swarm-Code/mono/swarm-sdk/observability"
)

func main() {
    // Create manager with in-memory storage
    mgr, err := manager.NewManager(manager.Config{
        Storage: storage.NewMemoryStorage(),
        Logger:  observability.NewLogger(),
        Tracer:  observability.NewTracer(),
        MaxContextTokens: 100000,
    })
    if err != nil {
        log.Fatal(err)
    }

    ctx := context.Background()

    // Create a new conversation
    conv, err := mgr.Create(ctx, manager.CreateOptions{
        Mode: "planning",
        Metadata: &conversation.ConversationMetadata{
            UserID:    "user123",
            ProjectID: "proj456",
            Tags:      []string{"important"},
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    // Add a message
    msg := &conversation.Message{
        ID:        "msg1",
        Timestamp: time.Now(),
        Role:      conversation.RoleUser,
        Content:   "What is machine learning?",
        Tokens: &conversation.TokenUsage{
            Input:  10,
            Output: 0,
            Total:  10,
        },
    }

    err = mgr.AddMessage(ctx, conv.ID, msg)
    if err != nil {
        log.Fatal(err)
    }

    // Retrieve messages
    messages, err := mgr.GetMessages(ctx, conv.ID, manager.GetMessagesOptions{
        Role: conversation.RoleUser,
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Retrieved %d messages\n", len(messages))

    // Export conversation
    data, err := mgr.Export(ctx, conv.ID, manager.ExportFormatJSON)
    if err != nil {
        log.Fatal(err)
    }

    // Complete the conversation
    err = mgr.Complete(ctx, conv.ID)
    if err != nil {
        log.Fatal(err)
    }
}
```

## API Documentation

### Manager Configuration

```go
type Config struct {
    Storage          storage.Storage           // Required: storage backend
    ContextStrategy  ContextWindowStrategy     // Optional: defaults to LRU
    Logger           observability.Logger      // Required: for structured logging
    Tracer           observability.Tracer      // Required: for distributed tracing
    MaxContextTokens int                       // Optional: defaults to 100,000
}
```

### Create Conversation

```go
conv, err := mgr.Create(ctx, manager.CreateOptions{
    Mode:     "planning",                  // Required
    Metadata: &conversation.ConversationMetadata{
        UserID:    "user123",
        ProjectID: "proj456",
        Tags:      []string{"tag1", "tag2"},
    },
    TraceID: "trace-123",                  // Optional
})
```

### Resume Conversation

```go
conv, err := mgr.Resume(ctx, conversationID)
if err != nil {
    // Handle conversation not found
}
```

### Add Message

```go
msg := &conversation.Message{
    ID:        "msg1",
    Timestamp: time.Now(),
    Role:      conversation.RoleUser,
    Content:   "Hello assistant",
    Tokens: &conversation.TokenUsage{
        Input:  5,
        Output: 0,
        Total:  5,
    },
}

err := mgr.AddMessage(ctx, conversationID, msg)
```

### Get Messages with Filtering

```go
// Get all messages
messages, err := mgr.GetMessages(ctx, conversationID, manager.GetMessagesOptions{})

// Filter by role
messages, err := mgr.GetMessages(ctx, conversationID, manager.GetMessagesOptions{
    Role: conversation.RoleAssistant,
})

// Filter by time range
after := time.Now().Add(-1 * time.Hour)
messages, err := mgr.GetMessages(ctx, conversationID, manager.GetMessagesOptions{
    After: &after,
})

// Pagination
messages, err := mgr.GetMessages(ctx, conversationID, manager.GetMessagesOptions{
    Offset: 10,
    Limit:  20,
})
```

### Complete/Archive/Delete

```go
// Mark as completed
err := mgr.Complete(ctx, conversationID)

// Archive for later reference
err := mgr.Archive(ctx, conversationID)

// Permanently delete
err := mgr.Delete(ctx, conversationID)
```

### Export Conversation

```go
// JSON format (canonical)
data, err := mgr.Export(ctx, conversationID, manager.ExportFormatJSON)

// Markdown (human-readable)
data, err := mgr.Export(ctx, conversationID, manager.ExportFormatMarkdown)

// HTML (rich presentation)
data, err := mgr.Export(ctx, conversationID, manager.ExportFormatHTML)

// JSONL (streaming)
data, err := mgr.Export(ctx, conversationID, manager.ExportFormatJSONL)
```

### Import Conversation

```go
// From JSON
imported, err := mgr.Import(ctx, jsonData, manager.ExportFormatJSON)

// From JSONL
imported, err := mgr.Import(ctx, jsonlData, manager.ExportFormatJSONL)
```

## Context Window Strategies

### LRU Strategy (Default)

Removes oldest messages when context window is exceeded.

```go
strategy := manager.NewLRUContextStrategy()
mgr, _ := manager.NewManager(manager.Config{
    ContextStrategy: strategy,
    // ... other config
})
```

**Use case**: General-purpose conversations where older context is less important.

### Sliding Window Strategy

Keeps only the N most recent messages.

```go
strategy := manager.NewSlidingWindowStrategy(50) // Keep 50 recent messages
mgr, _ := manager.NewManager(manager.Config{
    ContextStrategy: strategy,
    // ... other config
})
```

**Use case**: Fixed conversation length requirements.

### Priority Strategy

Keeps system messages and recent messages, removes older user messages.

```go
strategy := manager.NewPriorityContextStrategy(20) // Keep 20 recent messages
mgr, _ := manager.NewManager(manager.Config{
    ContextStrategy: strategy,
    // ... other config
})
```

**Use case**: Important system context must be preserved.

## Export Formats

### JSON
- **Use case**: Data processing, storage, archival
- **Properties**: Complete structure, round-trip compatible
- **Size**: Larger but human-readable with indentation

### Markdown
- **Use case**: Documentation, sharing, reports
- **Properties**: Formatted transcript, includes metadata
- **Size**: Medium, human-friendly

### HTML
- **Use case**: Web display, styled presentation
- **Properties**: Syntax highlighting, CSS styling
- **Size**: Larger, browser-friendly

### JSONL
- **Use case**: Streaming, append-only logs
- **Properties**: One message per line, efficient
- **Size**: Compact

## Error Handling

All operations return categorized errors:

```go
// Permanent errors - fail fast
"manager.invalid_config"        // Configuration error
"manager.invalid_options"       // Invalid create options
"manager.conversation_not_found"  // Conversation not found
"manager.invalid_format"        // Unsupported format

// Transient errors - retry with backoff
"manager.storage_failed"        // Storage operation failed
"manager.trim_failed"           // Context trimming failed
```

## Observability

### Logging

Every operation logs structured events:

```
manager.conversation_created
  - conversation_id: conv_1234567890
  - mode: planning

manager.message_added
  - conversation_id: conv_1234567890
  - message_id: msg_1
  - role: user
  - total_tokens: 45
```

### Tracing

Distributed tracing with span attributes:

```
manager.create
├── conversation_id: conv_xxx
├── mode: planning
└── [0.5ms]

manager.add_message
├── conversation_id: conv_xxx
├── message_id: msg_1
├── total_tokens: 45
└── [1.2ms]
```

## Performance

| Operation | Complexity | Notes |
|-----------|-----------|-------|
| Create | O(1) | Direct storage write |
| Resume | O(1) | Direct storage read |
| AddMessage | O(n) | Where n = context trimming (if needed) |
| GetMessages | O(m) | Where m = total messages (filtered) |
| Export | O(m) | Where m = message count |
| Import | O(m) | Where m = message count |

## Testing

The implementation includes comprehensive test coverage:

### Unit Tests (14 tests)
- Create with/without metadata
- Resume existing and non-existent conversations
- Add messages with token tracking
- Filter messages by role and time range
- Complete/Archive/Delete operations
- Export to all formats
- Import from all formats
- Error handling

### Integration Tests (8 tests)
- Complete conversation lifecycle
- Multi-turn conversations
- Context window trimming
- Export/import round-trips
- Storage persistence
- Error recovery

### Run Tests

```bash
# Run all tests
go test -v ./...

# Run with race detector
go test -race ./...

# Run specific test
go test -v -run TestManagerCreate ./...
```

## Thread Safety

The Manager is thread-safe for concurrent operations. The underlying storage implementation determines actual concurrency guarantees.

- **MemoryStorage**: Thread-safe with RWMutex
- **SQLiteStorage**: Thread-safe (SQLite handles locking)
- **PostgresStorage**: Concurrent-safe (database handles locking)

## Best Practices

1. **Always provide Logger and Tracer**: Required for observability
2. **Use appropriate ContextStrategy**: Choose based on use case
3. **Set MaxContextTokens**: Reasonable default is 100k, adjust per model
4. **Handle errors properly**: Use error.Category() to categorize responses
5. **Export before deleting**: Keep backups of important conversations
6. **Monitor token usage**: Watch for context window threshold approaches

## Examples

See the `examples/` directory for complete working examples:
- Basic conversation workflow
- Multi-turn conversation
- Context window management
- Export/import with different formats
- Error handling patterns

## Contributing

Contributions welcome! Please:
1. Follow the TDD pattern (tests first)
2. Add tests for new features
3. Update documentation
4. Run tests with race detector: `go test -race ./...`

## License

Same as the Go Agent SDK.
