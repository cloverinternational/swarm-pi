# OpenAI Provider

Production-ready OpenAI provider for the SwarmOS SDK with full observability and error handling.

## Features

✅ **Chat Completions** - Synchronous chat with full message history  
✅ **Tool Calling** - Function calling with JSON schema validation  
✅ **System Prompts** - Native system message support  
✅ **Error Handling** - Automatic categorization and retry logic  
✅ **Observability** - Structured logging, tracing, and metrics  
✅ **Thread-Safe** - Safe for concurrent requests  

🚧 **Streaming** - SSE streaming support (Phase 2)  
🚧 **Vision** - Multimodal image support (Phase 2)  

## Quick Start

```go
import (
    "github.com/Swarm-Code/mono/swarm-sdk/provider/openai"
    "github.com/Swarm-Code/mono/swarm-sdk/conversation"
)

// Create provider
provider, err := openai.New(openai.Config{
    APIKey: "sk-...",
    Logger: logger,
    Tracer: tracer,
})

// Make request
resp, err := provider.Chat(ctx, provider.ChatRequest{
    Model: "gpt-4o",
    Messages: []*conversation.Message{
        {Role: conversation.RoleUser, Content: "Hello!"},
    },
})
```

## Configuration

```go
type Config struct {
    APIKey         string                // Required: OpenAI API key
    BaseURL        string                // Optional: Custom API endpoint (default: api.openai.com/v1)
    OrganizationID string                // Optional: OpenAI organization ID
    Logger         observability.Logger  // Required: Structured logger
    Tracer         observability.Tracer  // Required: Distributed tracer
}
```

## Registry Integration

```go
import (
    "github.com/Swarm-Code/mono/swarm-sdk/provider"
    "github.com/Swarm-Code/mono/swarm-sdk/provider/openai"
)

// Create registry
registry := provider.NewSimpleRegistry(logger)

// Register OpenAI provider
openai.Register(registry)

// Create provider from config
prov, err := registry.Create(provider.Config{
    Name:   "openai",
    APIKey: "sk-...",
    Custom: map[string]interface{}{
        "logger": logger,
        "tracer": tracer,
    },
})
```

## Built-in Provider Profiles

The OpenAI provider includes pre-configured profiles for OpenAI-compatible providers:

| Provider | Profile Name | Base URL | Features |
|----------|--------------|----------|----------|
| OpenAI | `openai` | https://api.openai.com/v1 | Chat, Tools, Vision, Streaming |
| Wafer.ai | `wafer` | https://pass.wafer.ai/v1 | Chat, Tools, Vision, Streaming |
| DeepSeek | `deepseek` | https://api.deepseek.com/v1 | Chat, Tools, Streaming |
| Groq | `groq` | https://api.groq.com/openai/v1 | Chat, Tools, Streaming |
| Cerebras | `cerebras` | https://api.cerebras.ai/v1 | Chat, Tools, Streaming |
| Fireworks | `fireworks` | https://api.fireworks.ai/inference/v1 | Chat, Tools, Vision, Streaming |
| Together AI | `together` | https://api.together.xyz/v1 | Chat, Tools, Vision, Streaming |
| Perplexity | `perplexity` | https://api.perplexity.ai | Chat, Streaming (no tools) |
| OpenRouter | `openrouter` | https://openrouter.ai/api/v1 | Multi-provider aggregator |
| Azure OpenAI | `azure` | https://{resource}.openai.azure.com | Enterprise OpenAI service |
| Z.AI GLM | `glm` | https://api.z.ai/api/coding/paas/v4 | GLM coding models |
| Z.AI Vision | `glm-vision` | https://api.z.ai/api/paas/v4 | GLM vision models |

### Using a Profile

```go
// Use the OpenAI provider factory with a profile
factory := openai.NewFactory()

// Create provider using a profile
prov, err := factory.Create(provider.Config{
    Name:   "wafer",      // Use wafer.ai profile
    APIKey: "wfr-...",    // Your Wafer Pass API key
    Model:  "claude-sonnet-4", // Any model supported by Wafer
})
```

Profiles automatically configure the correct base URL, authentication headers, and feature flags for each provider.

## Tool Calling

```go
resp, err := provider.Chat(ctx, provider.ChatRequest{
    Model: "gpt-4o",
    Messages: messages,
    Tools: []provider.Tool{{
        Name:        "get_weather",
        Description: "Get current weather",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "location": map[string]interface{}{
                    "type": "string",
                    "description": "City name",
                },
            },
            "required": []string{"location"},
        },
    }},
})

// Check for tool calls
if resp.FinishReason == provider.FinishReasonToolCalls {
    for _, toolCall := range resp.Message.ToolCalls {
        // Execute tool with toolCall.Parameters
        // Add tool result to conversation
    }
}
```

## Error Handling

The provider automatically categorizes errors:

**Transient Errors** (automatic retry with exponential backoff):
- Rate limits (429)
- Server errors (500+)
- Network timeouts

**Permanent Errors** (fail fast):
- Invalid API key (401)
- Bad request (400)
- Not found (404)

**Steering Errors** (delegate to steering system):
- Content policy violations
- Low quality responses

```go
resp, err := provider.Chat(ctx, req)
if err != nil {
    if sdkerror.IsRetryable(err) {
        // Will be retried automatically
    } else if sdkerror.IsPermanent(err) {
        // Fix config and retry manually
    }
}
```

## Token Usage

```go
resp, err := provider.Chat(ctx, req)
if resp.Usage != nil {
    fmt.Printf("Input: %d tokens\n", resp.Usage.Input)
    fmt.Printf("Output: %d tokens\n", resp.Usage.Output)
    fmt.Printf("Total: %d tokens\n", resp.Usage.Total)
}
```

## Model Support

Models are configured externally, not hardcoded. The provider supports:
- GPT-4 family (gpt-4, gpt-4-turbo, gpt-4o)
- GPT-3.5 family (gpt-3.5-turbo)
- Custom/fine-tuned models

Model-specific capabilities (context window, output limits) will be loaded from configuration.

## Architecture

The provider follows the SDK's Ring architecture:

**Translation Layer** (`translate.go`)
- Canonical format ↔ OpenAI API format
- Handles OpenAI quirks (content can be string or array)
- Tool call parameter serialization

**HTTP Client** (`provider/http/`)
- Automatic retry with exponential backoff
- Request/response observability
- Thread-safe connection pooling

**Error Categorization** (`error/`)
- Transient vs Permanent vs Steering
- Automatic retry eligibility
- Context preservation

## Testing

```bash
# Run unit tests
go test ./tests/provider/openai/unit/...

# Run with coverage
go test -cover ./tests/provider/openai/unit/...

# Run specific test
go test -run TestChat_Success ./tests/provider/openai/unit/...
```

## Observability

Every operation emits:
- **Structured logs** (request, response, errors)
- **Distributed traces** (spans with attributes)
- **Metrics** (token usage, latency, error rates)

```go
// Logs include:
// - openai.chat.request
// - openai.chat.success
// - openai.chat.request_failed

// Spans include attributes:
// - provider: "openai"
// - model: "gpt-4o"
// - tokens: 1234
// - duration_ms: 567
```

## Examples

See `example_test.go` for working examples:
- Basic chat
- Tool calling
- System prompts
- Error handling
- Temperature control

## Limitations

- **Streaming**: Not yet implemented (Phase 2)
- **Vision**: Basic support, full multimodal in Phase 2
- **Embeddings**: Not implemented (use separate package)
- **DALL-E**: Not implemented (use separate package)

## License

Part of the SwarmOS SDK - see repository LICENSE
