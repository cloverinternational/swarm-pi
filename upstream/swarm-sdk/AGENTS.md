# AGENTS.md - AI Agent Development Guide

This document provides comprehensive guidance for AI agents working on the Go Agent SDK codebase.

---

## Core Philosophy

This SDK is built on the principle of the **"99% Environment"** - a system where complete observability enables autonomous improvement. Every operation is traceable, measurable, and auditable.

### Design Principles

**1. Trace Everything**
- Every conversation has a trace ID
- Every agent execution is a span
- Every tool call is instrumented
- Parent-child relationships preserved
- No operation goes unmeasured

**2. Errors Are Expected**
- Categorize all errors (transient, permanent, steering, critical)
- Automatic retry with exponential backoff
- Circuit breakers prevent cascading failures
- Graceful degradation when services fail
- User-friendly error messages

**3. Modularity First**
- Every component implements an interface
- Swappable providers, tools, hooks, storage
- Ring architecture (0-4) with clear boundaries
- No tight coupling between layers

**4. Observable by Default**
- Structured JSON logging (no unstructured text)
- Metrics for every operation
- Distributed tracing with correlation IDs
- Audit trail for all decisions

**5. Interoperability**
- Multiple providers (OpenAI, Anthropic, Gemini, etc.)
- Native tools + MCP server integration
- Canonical message format with provider translation
- Export to any format

**6. Security First**
- Explicit permissions at every level
- Sandboxed tool execution
- Workspace isolation
- Audit logging for compliance

---

## Project Structure

```
sdk/
├── provider/          # Provider interface & implementations
│   ├── interface.go       # Provider interface (Ring 0)
│   ├── anthropic/         # Anthropic implementation (Ring 1)
│   ├── openai/            # OpenAI implementation (Ring 1)
│   ├── gemini/            # Gemini implementation (Ring 1)
│   └── registry.go        # Provider registry
│
├── conversation/      # Conversation & message management
│   ├── conversation.go    # Core conversation type (Ring 0)
│   ├── message.go         # Message types (Ring 0)
│   ├── manager.go         # Conversation manager (Ring 1)
│   └── storage/           # Storage implementations
│       ├── interface.go   # Storage interface (Ring 0)
│       ├── memory.go      # In-memory store (Ring 1)
│       ├── file.go        # File-based store (Ring 1)
│       ├── dir_storage.go  # Directory storage (Ring 1)
│       └── async_storage.go  # Async storage wrapper (Ring 1)
│
├── hooks/            # Hook system & registry
│   ├── interface.go       # Hook interface (Ring 0)
│   ├── registry.go        # Hook registry (Ring 1)
│   ├── event.go           # Event types (Ring 0)
│   └── builtin/           # Built-in hooks
│
├── tools/            # Tool framework
│   ├── interface.go       # Tool interface (Ring 0)
│   ├── registry.go        # Tool registry (Ring 1)
│   ├── permission.go      # Permission system (Ring 1)
│   ├── mcp/               # MCP integration (Ring 1)
│   └── builtin/           # Built-in tools
│       ├── file.go
│       ├── bash.go
│       └── grep.go
│
├── agent/            # Agent layer
│   ├── agent.go           # Agent runtime (Ring 2)
│   ├── definition.go      # Agent definition (Ring 2)
│   ├── memory.go          # Agent memory (Ring 2)
│   └── collaboration.go   # Collaboration protocols (Ring 2)
│
├── mode/             # Mode orchestration
│   ├── mode.go            # Mode definition (Ring 3)
│   ├── group.go           # Agent group coordination (Ring 3)
│   ├── workflow.go        # Workflow engine (Ring 3)
│   ├── steering/          # Steering system (Ring 3)
│   └── loader.go          # Mode loader (YAML/config)
│
├── workspace/        # Workspace abstraction
│   ├── interface.go       # Workspace interface (Ring 0)
│   ├── filesystem.go      # Filesystem workspace (Ring 1)
│   ├── remote.go          # Remote workspace (Ring 1)
│   ├── container.go       # Container workspace (Ring 1)
│   └── database.go        # Database workspace (Ring 1)
│
├── cache/            # Multi-layer caching
│   ├── interface.go       # Cache interface (Ring 0)
│   ├── response.go        # Response cache (Ring 1)
│   ├── tool.go            # Tool result cache (Ring 1)
│   └── prompt.go          # Prompt cache (Ring 1)
│
├── observability/    # Observability system
│   ├── logger.go          # Structured logger (Ring 0)
│   ├── metrics.go         # Metrics collector (Ring 0)
│   ├── tracer.go          # Distributed tracer (Ring 0)
│   └── audit.go           # Audit logger (Ring 0)
│
├── sdkerr/            # Error handling
│   ├── constructors.go     # Error constructors (Ring 0)
│   ├── error.go            # Error taxonomy (Ring 0)
│   ├── helpers.go          # Error helpers (Ring 0)
│   └── context.go          # Error context (Ring 0)
│
├── config/           # Configuration
│   ├── config.go          # Config structures (Ring 0)
│   ├── loader.go          # Config loader (Ring 1)
│   └── validator.go       # Config validator (Ring 1)
│
├── serve/            # Server layer
│   ├── serve.go            # Serve entry point
│   └── ... 
│
└── scripts/          # Build & dev scripts
```

---

## Development Commands

### Building

```bash
# Build the SDK
go build ./...

# Build with race detection
go build -race ./...

# Build specific package
go build ./provider/anthropic
```

### Testing

**WE PRACTICE TEST-DRIVEN DEVELOPMENT (TDD)**

Every feature implementation MUST include tests. Tests are not optional—they are part of the definition of "done."

#### Test Structure

All tests live in `tests/` directory, organized by package and category:

```
tests/
├── runner.go                      # Unified test runner
├── provider/
│   └── http/
│       ├── unit/                  # Fast, isolated unit tests
│       ├── integration/           # Integration tests with real servers
│       ├── concurrent/            # Thread safety tests (race detector)
│       └── mocks/                 # Shared test mocks
│           ├── logger.go
│           └── tracer.go
```

#### Running Tests

```bash
# Use the test runner (RECOMMENDED)
go run tests/runner.go                    # Run all tests
go run tests/runner.go -category=unit     # Run only unit tests
go run tests/runner.go -category=concurrent  # Run concurrent tests with race detector
go run tests/runner.go -v                 # Verbose output
go run tests/runner.go -cover             # Generate coverage reports
go run tests/runner.go -run=TestClient    # Run specific test pattern

# Or use go test directly
go test ./tests/provider/http/unit           # Unit tests
go test -race ./tests/provider/http/concurrent  # Concurrent tests with race detector
go test ./...                                 # All tests (legacy)
```

#### Test-Driven Development Workflow

**REQUIRED PROCESS** for all new features:

1. **Write the test FIRST** (Red phase)
   ```go
   func TestNewFeature(t *testing.T) {
       // Arrange
       logger := mocks.NewLogger()
       tracer := mocks.NewTracer()
       
       // Act
       result, err := NewFeature()
       
       // Assert
       if err != nil {
           t.Fatalf("NewFeature() error = %v", err)
       }
       // ... more assertions
   }
   ```

2. **Run the test** (should fail)
   ```bash
   go run tests/runner.go -run=TestNewFeature
   ```

3. **Implement the feature** (Green phase)
   - Write minimal code to make test pass
   - Follow interfaces and patterns

4. **Run the test again** (should pass)
   ```bash
   go run tests/runner.go -run=TestNewFeature
   ```

5. **Refactor** if needed (Refactor phase)
   - Improve code quality
   - Tests still pass

6. **Add concurrent tests** for shared state
   ```go
   func TestNewFeatureConcurrent(t *testing.T) {
       const numGoroutines = 100
       var wg sync.WaitGroup
       
       for i := 0; i < numGoroutines; i++ {
           wg.Add(1)
           go func() {
               defer wg.Done()
               // Test concurrent access
           }()
       }
       wg.Wait()
   }
   ```

7. **Run race detector**
   ```bash
   go run tests/runner.go -category=concurrent
   ```

#### Test Categories

**Unit Tests** (`tests/*/unit/`)
- Fast (<100ms per test)
- No external dependencies
- Use mocks for all dependencies
- Test single functions/methods
- MUST be written for every public function

**Integration Tests** (`tests/*/integration/`)
- Test component interactions
- May use real HTTP servers (httptest)
- Test end-to-end flows
- MUST be written for public APIs

**Concurrent Tests** (`tests/*/concurrent/`)
- Test thread safety
- Use 50-300 goroutines
- MUST run with race detector
- REQUIRED for any shared state

#### Using Mocks

Always use shared mocks from `tests/*/mocks/`:

```go
import (
    httplib "github.com/Swarm-Code/mono/swarm-sdk/provider/http"
    "github.com/Swarm-Code/mono/swarm-sdk/tests/provider/http/mocks"
)

func TestWithMocks(t *testing.T) {
    logger := mocks.NewLogger()
    tracer := mocks.NewTracer()
    
    client, err := httplib.NewClient(httplib.ClientConfig{
        Logger: logger,
        Tracer: tracer,
    })
    
    // Test implementation...
    
    // Verify interactions
    if tracer.SpanCount() == 0 {
        t.Error("Expected spans to be created")
    }
}
```

#### Test Naming Conventions

```go
// Unit test
func TestFunctionName(t *testing.T) { }

// Table-driven test
func TestFunctionName_EdgeCases(t *testing.T) {
    tests := []struct {
        name string
        input string
        want string
    }{ /* ... */ }
}

// Concurrent test
func TestFunctionNameConcurrent(t *testing.T) { }

// Integration test
func TestFunctionNameIntegration(t *testing.T) { }
```

#### Test Requirements Checklist

Every implementation MUST include:

- [ ] Unit tests for all public functions
- [ ] Unit tests for error paths
- [ ] Unit tests for edge cases (nil, empty, invalid)
- [ ] Concurrent tests for shared state
- [ ] Integration tests for public APIs
- [ ] All tests pass: `go run tests/runner.go`
- [ ] Zero race conditions: `go run tests/runner.go -category=concurrent`
- [ ] Coverage ≥80%: `go run tests/runner.go -cover`

#### Test Documentation

Document complex test scenarios:

```go
// TestClientTimeout verifies that the client properly handles timeouts
// in the following scenarios:
// 1. Parent context timeout before client timeout
// 2. Client timeout before parent context timeout
// 3. Context cancellation during active request
func TestClientTimeout(t *testing.T) {
    // Implementation...
}
```

#### Common Test Patterns

**Arrange-Act-Assert:**
```go
func TestPattern(t *testing.T) {
    // Arrange
    input := "test"
    expected := "result"
    
    // Act
    actual := Process(input)
    
    // Assert
    if actual != expected {
        t.Errorf("got %v, want %v", actual, expected)
    }
}
```

**Table-Driven:**
```go
func TestTableDriven(t *testing.T) {
    tests := []struct {
        name string
        input string
        want string
    }{
        {"empty", "", ""},
        {"normal", "test", "result"},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := Process(tt.input)
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

**Concurrent:**
```go
func TestConcurrent(t *testing.T) {
    var wg sync.WaitGroup
    results := make(chan string, 100)
    
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            results <- Process("test")
        }()
    }
    
    wg.Wait()
    close(results)
    
    // Verify results
}
```

#### Test Failure Protocol

When tests fail:

1. **STOP** implementing new features
2. **FIX** failing tests immediately
3. **VERIFY** fix with: `go run tests/runner.go`
4. **CHECK** for race conditions: `go run tests/runner.go -category=concurrent`
5. **ONLY THEN** continue with new work

**NEVER commit code with failing tests.**

#### Performance Testing

Add benchmarks for performance-critical code:

```go
func BenchmarkCriticalPath(b *testing.B) {
    setup := PrepareTest()
    b.ResetTimer()
    
    for i := 0; i < b.N; i++ {
        CriticalFunction(setup)
    }
}
```

Run benchmarks:
```bash
go test -bench=. -benchmem ./tests/provider/http/unit
```

#### Test Coverage

Aim for ≥80% coverage:

```bash
# Generate coverage report
go run tests/runner.go -cover

# View coverage
go tool cover -html=coverage-tests-provider-http-unit.out
```

Areas requiring 100% coverage:
- Error handling paths
- Security-critical code
- Data validation
- Permission checks

### Linting & Formatting

```bash
# Format code (ALWAYS before committing)
go fmt ./...

# Run linter
golangci-lint run

# Run static analysis
go vet ./...
```

---

## Coding Conventions

### Go Style

1. **Follow standard Go conventions**: [Effective Go](https://go.dev/doc/effective_go)
2. **Package comments required**: Every package MUST have a doc comment
   ```go
   // Package provider defines the interface and implementations for LLM providers.
   package provider
   ```
3. **Exported identifiers**: Document all exported types, functions, constants
4. **Run `go fmt` before committing** (non-negotiable)
5. **Use descriptive names**: No single-letter variables except in very short scopes

### Error Handling

**ALWAYS** categorize errors using the error taxonomy:

```go
import "github.com/Swarm-Code/mono/swarm-sdk/sdkerr"

// Transient error - will be retried
if err := provider.Chat(ctx, messages); err != nil {
    return sdkerr.Transient("provider.timeout", err.Error(), 
        sdkerr.WithRetryAfter(5*time.Second))
}

// Permanent error - fail fast
if apiKey == "" {
    return sdkerr.Permanent("provider.invalid_api_key", 
        "API key not configured")
}

// Steering error - let steering decide
if quality < threshold {
    return sdkerr.Steering("agent.low_quality_response", 
        "Response quality below threshold",
        sdkerr.WithContext("quality", quality))
}
```

### Observability

**EVERY** operation must emit logs, metrics, and traces:

```go
import "github.com/Swarm-Code/mono/swarm-sdk/observability"

func (p *Provider) Chat(ctx context.Context, messages []Message) error {
    // Start span
    ctx, span := observability.StartSpan(ctx, "provider.chat")
    defer span.End()
    
    // Add span attributes
    span.SetAttribute("provider", p.Name())
    span.SetAttribute("model", p.Model())
    span.SetAttribute("message_count", len(messages))
    
    // Structured log
    observability.Log(ctx, observability.LevelInfo, "provider.request", 
        observability.Field("provider", p.Name()),
        observability.Field("model", p.Model()))
    
    // Emit metric
    defer observability.Histogram("provider.request.duration", 
        time.Since(start))
    
    // Implementation...
    
    return nil
}
```

### Interface Design

**ALL** core abstractions MUST be interfaces:

```go
// Ring 0: Define interface
type Provider interface {
    // Name returns the provider name (e.g., "anthropic")
    Name() string
    
    // Chat sends a chat request and returns the response
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    
    // Stream sends a chat request and streams the response
    Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
    
    // Capabilities returns the provider's capabilities
    Capabilities() Capabilities
}

// Ring 1: Implement
type AnthropicProvider struct {
    apiKey string
    client *http.Client
}

func (p *AnthropicProvider) Name() string {
    return "anthropic"
}

// ... implement other methods
```

### Configuration

Use structured configuration with validation:

```go
type Config struct {
    Provider   ProviderConfig   `toml:"provider" validate:"required"`
    Cache      CacheConfig      `toml:"cache"`
    Observability ObsConfig     `toml:"observability"`
}

func (c *Config) Validate() error {
    if c.Provider.APIKey == "" {
        return sdkerr.Permanent("config.invalid", 
            "provider API key required")
    }
    return nil
}
```

### Testing Patterns

```go
func TestProviderChat(t *testing.T) {
    // Arrange
    provider := NewMockProvider()
    ctx := context.Background()
    messages := []Message{
        {Role: "user", Content: "test"},
    }
    
    // Act
    resp, err := provider.Chat(ctx, messages)
    
    // Assert
    assert.NoError(t, err)
    assert.NotNil(t, resp)
    assert.NotEmpty(t, resp.Content)
}

// Table-driven tests
func TestErrorCategories(t *testing.T) {
    tests := []struct {
        name     string
        err      error
        category error.Category
        retryable bool
    }{
        {
            name:     "rate limit is transient",
            err:      sdkerr.Transient("provider.rate_limited", nil),
            category: sdkerr.CategoryTransient,
            retryable: true,
        },
        {
            name:     "invalid key is permanent",
            err:      sdkerr.Permanent("provider.invalid_api_key", ""),
            category: sdkerr.CategoryPermanent,
            retryable: false,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            assert.Equal(t, tt.category, sdkerr.GetCategory(tt.err))
            assert.Equal(t, tt.retryable, sdkerr.IsRetryable(tt.err))
        })
    }
}
```

---

## Common Patterns

### 1. Provider Implementation

When adding a new provider:

```go
// 1. Define in provider/{name}/provider.go
type Provider struct {
    apiKey string
    baseURL string
    client *http.Client
}

// 2. Implement provider.Provider interface
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
    // Translate canonical request to provider format
    providerReq := p.translateRequest(req)
    
    // Make API call with observability
    ctx, span := observability.StartSpan(ctx, "provider.api_call")
    defer span.End()
    
    resp, err := p.client.Do(providerReq)
    if err != nil {
        // Categorize error
        return nil, p.categorizeError(err)
    }
    
    // Translate provider response to canonical format
    return p.translateResponse(resp), nil
}

// 3. Register in provider/registry.go
func init() {
    registry.Register("newprovider", func(config Config) (Provider, error) {
        return newprovider.New(config)
    })
}
```

### 2. Tool Implementation

When adding a new tool:

```go
// 1. Implement tools.Tool interface
type MyTool struct {}

func (t *MyTool) Name() string {
    return "my_tool"
}

func (t *MyTool) Description() string {
    return "Does something useful"
}

func (t *MyTool) Parameters() JSONSchema {
    return JSONSchema{
        Type: "object",
        Properties: map[string]JSONSchema{
            "param1": {Type: "string"},
        },
        Required: []string{"param1"},
    }
}

func (t *MyTool) Execute(ctx context.Context, params map[string]any) (string, error) {
    // Validate
    if err := t.Validate(params); err != nil {
        return "", sdkerr.Permanent("tool.invalid_parameters", err.Error())
    }
    
    // Check permissions (automatic via framework)
    
    // Execute with observability
    ctx, span := observability.StartSpan(ctx, "tool.execute")
    span.SetAttribute("tool", t.Name())
    defer span.End()
    
    // Implementation...
    
    return result, nil
}

func (t *MyTool) IsIdempotent() bool {
    return true // or false
}

func (t *MyTool) RequiresPermission() []tools.Permission {
    return []tools.Permission{
        tools.PermissionFileRead,
    }
}
```

### 3. Hook Implementation

When adding a hook:

```go
type MyHook struct {}

func (h *MyHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
    // Filter events we care about
    if event.Type != "message.before_send" {
        return hooks.Continue(), nil
    }
    
    // Process event
    // ...
    
    // Options:
    // 1. Continue (pass to next hook)
    return hooks.Continue(), nil
    
    // 2. Block (stop processing)
    return hooks.Block("reason"), nil
    
    // 3. Modify (change event)
    modified := event.Clone()
    modified.Data["custom"] = "value"
    return hooks.Modify(modified), nil
}

func (h *MyHook) Priority() int {
    return 50 // 0-100, higher = runs first
}

func (h *MyHook) Filter(event hooks.Event) bool {
    return event.Type == "message.before_send"
}
```

### 4. Workspace Implementation

When adding a workspace type:

```go
type MyWorkspace struct {
    root string
}

func (w *MyWorkspace) Type() string {
    return "myworkspace"
}

func (w *MyWorkspace) Root() string {
    return w.root
}

func (w *MyWorkspace) Read(path string) (string, error) {
    // Resolve path
    absPath, err := w.Resolve(path)
    if err != nil {
        return "", err
    }
    
    // Check permissions
    if !w.IsAllowed("read", absPath) {
        return "", sdkerr.Permanent("workspace.permission_denied", 
            fmt.Sprintf("read not allowed: %s", path))
    }
    
    // Implement read logic...
    
    return content, nil
}
```

---

## Working with Modes

Modes define multi-agent workflows. They are defined in YAML and loaded at runtime.

### Mode File Structure

```yaml
name: "example_mode"
description: "Example workflow"
version: "1.0"

config:
  max_duration: "30m"
  allow_human_intervention: true

steering:
  type: "hybrid"
  llm_meta_agent:
    provider: "anthropic"
    model: "claude-3-5-sonnet"

groups:
  - name: "planning"
    execution: "parallel"
    agents:
      - name: "agent1"
        provider: "openai"
        model: "gpt-4"
        tools: ["file_read", "grep"]
```

### Loading Modes

```go
// Load mode from file
mode, err := mode.LoadFromFile("path/to/mode.yaml")
if err != nil {
    return err
}

// Validate mode
if err := mode.Validate(); err != nil {
    return err
}

// Register mode
mode.Registry.Register(mode)
```

---

## Storage & Persistence

All conversations are stored using the Storage interface:

```go
// Save conversation
if err := storage.Save(conversation); err != nil {
    return sdkerr.Transient("storage.save_failed", err)
}

// Load conversation
conv, err := storage.Load(conversationID)
if err != nil {
    return sdkerr.Permanent("conversation.not_found", err.Error())
}

// Query conversations
filter := storage.Filter{
    Status: "active",
    Tags:   []string{"bug-fix"},
}
conversations, err := storage.Query(filter)
```

---

## Caching Strategy

The SDK implements multi-layer caching. **ALWAYS** check cache before expensive operations:

```go
// L1: Prompt cache (automatic at provider level)
// L2: Response cache
cacheKey := cache.GenerateKey(model, messages, tools)
if cached, ok := cache.Get(cacheKey); ok {
    observability.Metric("cache.hit", 1)
    return cached, nil
}

// L3: Tool result cache (for idempotent tools only)
if tool.IsIdempotent() {
    toolCacheKey := cache.GenerateToolKey(tool.Name(), params)
    if result, ok := cache.GetToolResult(toolCacheKey); ok {
        return result, nil
    }
}
```

---

## MCP Integration

MCP servers are first-class citizens:

```go
// Connect to MCP server
mcpClient, err := mcp.Connect(mcp.Config{
    Name:    "filesystem",
    Command: "mcp-server-filesystem",
    Args:    []string{"--root", "/workspace"},
})

// List available tools
tools, err := mcpClient.ListTools()

// Register MCP tools in tool registry
for _, tool := range tools {
    toolRegistry.Register(mcp.WrapTool(tool))
}

// Tools are now available to agents transparently
```

---

## Security & Permissions

**NEVER** bypass the permission system:

```go
// Check permission before tool execution
if !permissions.Check(ctx, agent, tool, params) {
    // Request runtime permission
    if !permissions.RequestApproval(ctx, tool, params) {
        return sdkerr.Permanent("tool.permission_denied", 
            fmt.Sprintf("%s denied for %s", tool.Name(), agent.ID))
    }
}

// Execute tool
result, err := tool.Execute(ctx, params)
```

Permission levels (most to least restrictive):
1. Runtime (user approval)
2. Agent (per-agent config)
3. Mode (per-mode config)
4. Project (project-level config)
5. Global (global config)

---

## Performance Considerations

### 1. Context Windows

**ALWAYS** respect context window limits:

```go
// Check token count
tokens := conversation.TokenCount()
if tokens > model.ContextWindow() {
    // Trigger context trimming
    conversation.Trim(strategy.LRU)
}
```

### 2. Streaming

**PREFER** streaming for long-running operations:

```go
// Streaming chat
chunks, err := provider.Stream(ctx, req)
for chunk := range chunks {
    // Process chunk immediately
    // Don't buffer entire response
}
```

### 3. Concurrency

Use goroutines for parallel operations:

```go
// Parallel agent execution
var wg sync.WaitGroup
results := make(chan AgentResult, len(agents))

for _, agent := range agents {
    wg.Add(1)
    go func(a Agent) {
        defer wg.Done()
        result, err := a.Execute(ctx)
        results <- AgentResult{Agent: a, Result: result, Error: err}
    }(agent)
}

wg.Wait()
close(results)
```

---

## Debugging

### Enable Debug Logging

```bash
export SDK_LOG_LEVEL=debug
export SDK_TRACE=true
```

### View Traces

All operations emit trace IDs. Follow the trace to see full execution:

```bash
# Find trace ID in logs
grep "trace_id: abc-123" logs.json

# View all events in trace
jq 'select(.trace_id == "abc-123")' logs.json
```

### Common Issues

**Problem**: Provider rate limited
- **Cause**: Too many requests
- **Solution**: Check rate limit config, enable caching, use backup provider

**Problem**: Context window exceeded
- **Cause**: Conversation too long
- **Solution**: Enable context trimming, increase summary frequency

**Problem**: Tool permission denied
- **Cause**: Insufficient permissions
- **Solution**: Update permission config or request runtime approval

---

## Adding New Features

### Checklist

When adding any new feature:

- [ ] Define interface in Ring 0
- [ ] Implement in appropriate Ring
- [ ] Add comprehensive logging
- [ ] Add metrics
- [ ] Add distributed tracing
- [ ] Categorize all errors
- [ ] Write unit tests
- [ ] Write integration tests
- [ ] Update documentation
- [ ] Add example usage
- [ ] Consider caching strategy
- [ ] Consider performance impact
- [ ] Run `go fmt ./...`
- [ ] Run `golangci-lint run`
- [ ] Run all tests

---

## Documentation Standards

### Code Comments

```go
// Provider defines the interface for LLM providers.
// All providers must implement this interface to be compatible
// with the SDK.
type Provider interface {
    // Name returns the unique identifier for this provider.
    // Examples: "anthropic", "openai", "gemini"
    Name() string
    
    // Chat sends a synchronous chat request and returns the complete response.
    // The request is in canonical format and will be translated to the provider's
    // native format automatically.
    //
    // Returns an error categorized as:
    // - Transient: Network errors, rate limits, timeouts
    // - Permanent: Invalid API key, unsupported model
    // - Steering: Low quality response, content policy violation
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}
```

### README Files

Each package should have a README explaining:
- Purpose
- Key types/interfaces
- Usage examples
- Common patterns

---

## Release Process

### Versioning

Use semantic versioning:
- **MAJOR**: Breaking changes to interfaces
- **MINOR**: New features, backward compatible
- **PATCH**: Bug fixes, no API changes

### Before Release

- [ ] All tests passing
- [ ] Documentation updated
- [ ] CHANGELOG.md updated
- [ ] Version bumped
- [ ] Examples tested
- [ ] Performance benchmarks run
- [ ] Breaking changes documented

---

## Getting Help

### Resources

- Architecture: `core-ideals.md`
- Examples: `examples/`
- Tests: `*_test.go` files throughout codebase

### Common Questions

**Q: How do I add a new provider?**
A: See "Provider Implementation" pattern above

**Q: How do I add a new tool?**
A: See "Tool Implementation" pattern above

**Q: How do error retries work?**
A: Transient errors retry with exponential backoff (see `error/retry.go`)

**Q: How do I test with multiple providers?**
A: Use mock providers in tests (see `provider/mock/`)

**Q: How do I trace a conversation?**
A: Every conversation has a trace_id. Grep logs for that ID.

---

## Summary

This SDK is designed for:
- **Modularity**: Everything is swappable
- **Observability**: Everything is measured
- **Resilience**: Errors are handled gracefully
- **Interoperability**: Multiple providers, formats, tools
- **Performance**: Caching, streaming, concurrency
- **Security**: Permissions at every level

When in doubt, follow these principles:
1. **Trace it** - Add logging, metrics, tracing
2. **Interface it** - Define clean interfaces
3. **Categorize errors** - Use error taxonomy
4. **Cache it** - Check cache before expensive ops
5. **Test it** - Write comprehensive tests
6. **Document it** - Clear comments and examples

---

**Remember**: This codebase enables AI to improve itself. Every trace, every metric, every log entry contributes to the 99% environment where autonomous improvement is possible. Write code with that in mind.

# Explicit Typing in Go Application

## Objective
Ensure the application uses explicit typing throughout to improve code reliability and maintainability.

## Key Points
- **Use explicit type declarations** for all variables and function parameters
- **Avoid relying on type inference** where clarity might be compromised
- **Define custom types** for domain-specific concepts rather than using primitives
- **Specify return types clearly** on all functions
- **Benefits:**
  - Easier to understand code intent
  - Reduces bugs from type-related issues
  - Improves IDE support and autocomplete
  - Makes code reviews more straightforward
  - Facilitates easier refactoring

## Implementation Strategy
- Code review checklist: verify all declarations have explicit types
- Document type definitions in relevant package files
- Consider creating a style guide for the team if not already present

---

*Last Updated: [timestamp]*

_Added on 12/1/2025, 4:14:57 PM GMT-4_

# Context Documentation Review

## Task
Use `context7` to examine and reference documentation alongside related questions.

## Purpose
- Review official documentation in context
- Cross-reference questions with documented information
- Ensure answers align with established docs

## How to Use
1. Access `context7` for documentation lookup
2. Compare against outstanding questions
3. Validate responses against documented standards

---

*This note can be expanded with specific documentation links or question categories as needed.*

_Added on 12/1/2025, 8:15:59 PM GMT-4_
