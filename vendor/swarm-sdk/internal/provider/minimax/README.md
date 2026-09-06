# MiniMax Provider Implementation

## Overview

The MiniMax provider extends SwarmOS with support for MiniMax's conversational AI models through their **Anthropic-compatible endpoint**.

MiniMax offers an endpoint at `https://api.minimax.io/anthropic` that implements a subset of Anthropic's Messages API, allowing seamless integration with the Swarm SDK without requiring custom API parsing logic.

## Architecture

The MiniMax provider is located in `swarm-sdk/provider/minimax/` and consists of:

### Core Files

- **`config.go`**: Configuration schema for MiniMax
  - API key management
  - Base URL configuration (defaults to `https://api.minimax.io/anthropic`)
  - Model selection
  - Timeout settings
  - Token limit configuration

- **`models.go`**: MiniMax model definitions
  - Default model: `MiniMax-M2.5`
  - Model-specific capabilities (context window, max tokens, etc.)

- **`capabilities.go`**: Capability registry
  - Declares support for streaming, function calling, vision, thinking blocks
  - Specifies model-specific limits

- **`provider.go`**: Core provider interface implementation
  - Implements `provider.Provider` interface
  - HTTP client setup with Bearer token auth
  - Token estimation
  - Lifecycle management (Close, etc.)

- **`register.go`**: Provider registration
  - Factory function for creating provider instances
  - Integration with SDK's provider registry

- **`translate.go`**: Message translation logic
  - Converts SDK canonical messages to Anthropic/MiniMax format
  - Handles interleaved thinking blocks
  - Tool call and tool result translation
  - Response parsing from MiniMax API

- **`chat.go`**: Synchronous chat completions
  - `Chat()` method for request-response interactions
  - Full message translation and response parsing

- **`stream.go`**: Server-Sent Events (SSE) streaming
  - `Stream()` method for real-time streaming
  - SSE event parsing and chunk delivery
  - Token counting during streaming

### Test Files

- **`translate_test.go`**: Translation layer tests
  - Message to block conversion
  - Block to message conversion
  - Round-trip translation verification
  - Response parsing tests

- **`provider_test.go`**: Provider interface tests
  - Configuration validation
  - Model capabilities
  - Token estimation

## Integration Points

### SDK Integration

1. **Provider Registry**: Registered in `swarm-sdk/provider/minimax/register.go`
2. **IPC Server**: Integrated into `swarm-tui/headless/cmd/ipc-server/main.go`
   - Imported from `github.com/Swarm-Code/mono/swarm-sdk/provider/minimax`
   - Registered with `providerRegistry.Register("minimax", ...)`

### Configuration

Users can configure MiniMax through `~/.swarmos/providers.json`:

```json
{
  "custom_models": [
    {
      "model_display_name": "MiniMax-M2.5",
      "model": "MiniMax-M2.5",
      "base_url": "https://api.minimax.io/anthropic",
      "api_key": "<MINIMAX_API_KEY>",
      "provider": "anthropic",
      "max_tokens": 64000
    }
  ]
}
```

Or through environment variables:

```bash
export MINIMAX_API_KEY="your-api-key"
```

## Features

### Supported Capabilities

- ✅ **Streaming**: Real-time token-by-token response streaming via SSE
- ✅ **Function Calling**: Full tool/function call support
- ✅ **Vision**: Image input support (via Anthropic-compatible format)
- ✅ **Interleaved Thinking**: Extended thinking with reasoning blocks
- ✅ **Prompt Caching**: Cache metrics tracking
- ✅ **Tool Calls**: Full request-response cycle for tool invocation

### Message Format

MiniMax uses Anthropic's block-based format:

```go
type Message struct {
    Role    string      // "user" or "assistant"
    Content interface{} // string or []ContentBlock
}

type ContentBlock struct {
    Type      string                 // "text", "tool_use", "tool_result", "thinking"
    Text      string                 // For text blocks
    Thinking  string                 // For thinking blocks
    ID        string                 // For tool_use blocks
    Name      string                 // For tool_use blocks
    Input     map[string]interface{} // For tool_use blocks
    ToolUseID string                 // For tool_result blocks
    Content   interface{}            // For tool_result blocks
    IsError   bool                   // For tool_result blocks
    Signature string                 // For thinking blocks (required for history)
}
```

## Translation Logic

The provider performs bidirectional translation:

### SDK → MiniMax

```
SDK Message                 →  Anthropic Format
├── thinking                →  thinking block
├── content                 →  text block
├── tool_calls[]            →  tool_use blocks
└── metadata                →  preserved in signature
```

### MiniMax → SDK

```
Anthropic Response          →  SDK Message
├── thinking blocks         →  Message.Thinking
├── text blocks             →  Message.Content
├── tool_use blocks         →  Message.ToolCalls
└── tool_result blocks      →  (in next user message)
```

## API Compatibility

MiniMax's Anthropic-compatible endpoint implements:

- **Messages API**: POST `/anthropic/messages`
  - Request format: Anthropic Messages API v2023-06-01
  - Response format: Anthropic Messages API response
  - Streaming: Server-Sent Events (compatible with Anthropic's format)

### Authentication

MiniMax uses Bearer token authentication:

```
Authorization: Bearer <MINIMAX_API_KEY>
anthropic-version: 2023-06-01
Content-Type: application/json
```

## Testing

### Unit Tests

Run tests with:

```bash
cd /home/rincon/swarm
go test ./swarm-sdk/provider/minimax/... -v
```

Tests cover:

- Translation layer (message ↔ block conversion)
- Message translation pipeline
- Response parsing
- Configuration validation
- Model capabilities
- Token estimation

### Integration Testing

For live API testing:

```bash
# Set API key
export MINIMAX_API_KEY="your-key"

# Run IPC server
./swarm-tui/headless/cmd/ipc-server/ipc-server

# Send request via JSON-RPC
# See swarm-app or other clients for examples
```

## Known Limitations

1. **Vision Support**: Images must be sent in Anthropic format (base64 + media type)
2. **Models**: Currently supports `MiniMax-M2.5` model
3. **Streaming**: Fully supported but inherits Anthropic's SSE format
4. **Rate Limiting**: Respects `Retry-After` headers from MiniMax API

## Error Handling

The provider includes robust error handling for:

- Invalid API keys
- Rate limit errors (HTTP 429)
- Server errors (HTTP 5xx)
- Malformed responses
- Network timeouts
- Incomplete SSE streams

Errors are wrapped with context:

```go
if err != nil {
    return nil, fmt.Errorf("minimax: failed to parse response: %w", err)
}
```

## Performance Considerations

### Token Estimation

Uses 4 characters per token approximation (configurable):

```go
tokens := len(text) / 4
```

### Streaming

- Buffered SSE parsing (4KB chunks)
- Token counting during streaming
- Efficient memory usage for large responses

### Caching

- Supports prompt caching tracking
- Cache metrics included in response
- Used for compaction decisions

## Future Enhancements

1. **Custom Models**: Support additional MiniMax models as they're released
2. **Vision Improvements**: Native image support without Anthropic format wrapping
3. **Advanced Thinking**: Support for extended thinking budgets
4. **Cost Optimization**: Cache usage tracking and reporting
5. **Batch API**: Batch processing support if MiniMax adds it

## References

- [MiniMax API Documentation](https://platform.minimaxi.com/)
- [Anthropic API Documentation](https://docs.anthropic.com/)
- [Messages API Format](https://docs.anthropic.com/claude/reference/messages-api)
- [Extended Thinking](https://docs.anthropic.com/claude/guides/extended-thinking)

## Contributing

To extend the MiniMax provider:

1. Add new models to `models.go`
2. Update capabilities in `capabilities.go`
3. Add tests for new features
4. Update documentation

## License

Part of SwarmOS - refer to main project license.
