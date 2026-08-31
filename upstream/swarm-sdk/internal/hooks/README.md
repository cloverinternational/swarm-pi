# Hook System - Complete Implementation

The Hook system provides event interception capabilities across the entire SDK, enabling observability, policy enforcement, and extensibility without modifying core code.

## Quick Start

```go
package main

import (
    "context"
    "github.com/Swarm-Code/mono/swarm-sdk/hooks"
    "github.com/Swarm-Code/mono/swarm-sdk/hooks/builtin"
    "github.com/Swarm-Code/mono/swarm-sdk/observability"
)

func main() {
    // Create manager
    manager := hooks.NewManager(hooks.ManagerConfig{
        MaxHooksPerScope: 100,
        MaxExecutionTime: 5 * time.Second,
    })

    // Register built-in hooks
    manager.Register(
        builtin.NewLoggingHook(logger, observability.LevelInfo),
        hooks.ScopeGlobal,
        "",
    )

    manager.Register(
        builtin.NewMetricsHook(metrics),
        hooks.ScopeGlobal,
        "",
    )

    // Emit event
    event := hooks.Event{
        Type: hooks.EventToolBeforeExecute,
        Data: map[string]interface{}{
            "tool_name": "file_read",
        },
    }

    finalEvent, err := manager.Emit(context.Background(), event)
    if err != nil {
        // Event was blocked
        log.Printf("Blocked: %v", err)
        return
    }

    // Event processed successfully
    processEvent(finalEvent)
}
```

## Architecture

### Ring Model

```
Ring 0: Interfaces (hooks/hook.go, hooks/event.go, hooks/registry.go)
    ↓
Ring 1: Implementations
    ├── Manager (hooks/manager.go) - Registry & coordination
    ├── Executor (hooks/executor.go) - Safe execution engine
    ├── Filters (hooks/filters.go) - Event filtering
    └── Built-in Hooks (hooks/builtin/)
        ├── Logging (logging.go)
        ├── Tracing (tracing.go)
        ├── Metrics (metrics.go)
        └── Audit (audit.go)
```

### Components

**Manager** (`manager.go`):
- Scoped registration (Global, Project, Mode, Conversation)
- Priority-based sorting and caching
- Thread-safe operations
- Statistics tracking

**Executor** (`executor.go`):
- Panic recovery with stack traces
- Timeout protection (configurable)
- Action processing (Continue/Block/Modify)
- Error handling callbacks

**Filters** (`filters.go`):
- Wildcard pattern matching (`message.*`, `*.created`)
- Composite filters (AND/OR/NOT)
- Predefined builders for common patterns

**Built-in Hooks** (`builtin/`):
- Logging (3 variants)
- Tracing (2 variants)
- Metrics (2 variants)
- Audit (2 variants)

## Features

### ✅ Scoped Registration

Hooks can be registered at different scopes:

```go
// Global - applies to all events
manager.Register(hook, hooks.ScopeGlobal, "")

// Project-specific
manager.Register(hook, hooks.ScopeProject, "project-id")

// Mode-specific
manager.Register(hook, hooks.ScopeMode, "mode-id")

// Conversation-specific
manager.Register(hook, hooks.ScopeConversation, "conv-id")
```

### ✅ Priority-Based Execution

Hooks execute in priority order (highest first):

```go
func (h *SecurityHook) Priority() int {
    return 99  // Very high - run first
}

func (h *LoggingHook) Priority() int {
    return 90  // High - log early
}

func (h *MetricsHook) Priority() int {
    return 85  // After logging
}
```

### ✅ Action-Based Control

Hooks can control event flow:

```go
// Continue - pass to next hook
return hooks.Continue(), nil

// Block - stop processing
return hooks.Block("reason"), nil

// Modify - transform event
modified := event.Clone()
modified.Data["injected"] = "value"
return hooks.Modify(modified), nil
```

### ✅ Event Filtering

Hooks filter which events they process:

```go
func (h *MyHook) Filter(event hooks.Event) bool {
    // Only process tool events
    return event.Type == hooks.EventToolBeforeExecute
}

// Or use predefined filters
filter := hooks.MatchToolEvents()
filter := hooks.MatchMessageEvents()
filter := hooks.MatchProviderEvents()
```

### ✅ Statistics Tracking

Comprehensive statistics at multiple levels:

```go
// Global stats
stats := manager.GetStats()
fmt.Printf("Total executions: %d\n", stats.TotalExecutions)
fmt.Printf("Total blocked: %d\n", stats.TotalBlocked)
fmt.Printf("Total modified: %d\n", stats.TotalModified)

// Per-hook stats
for _, reg := range manager.List() {
    fmt.Printf("%s: %d executions\n", reg.Hook.Name(), reg.ExecutionCount)
}
```

## Built-in Hooks

### Logging Hooks

**LoggingHook** - Standard structured logging:
```go
hook := builtin.NewLoggingHook(logger, observability.LevelInfo)
manager.Register(hook, hooks.ScopeGlobal, "")
```

**ErrorLoggingHook** - Error events only:
```go
hook := builtin.NewErrorLoggingHook(logger)
manager.Register(hook, hooks.ScopeGlobal, "")
```

**DebugLoggingHook** - Detailed debug logging:
```go
hook := builtin.NewDebugLoggingHook(logger, true, true)  // include data & metadata
manager.Register(hook, hooks.ScopeGlobal, "")
```

### Tracing Hooks

**TracingHook** - Distributed tracing:
```go
hook := builtin.NewTracingHook(tracer)
manager.Register(hook, hooks.ScopeGlobal, "")
```

**SamplingTracingHook** - Sampled tracing (for high-volume):
```go
hook := builtin.NewSamplingTracingHook(tracer, 0.1)  // 10% sample rate
manager.Register(hook, hooks.ScopeGlobal, "")
```

### Metrics Hooks

**MetricsHook** - Comprehensive metrics:
```go
hook := builtin.NewMetricsHook(metrics)
manager.Register(hook, hooks.ScopeGlobal, "")
```

**PerformanceMetricsHook** - Performance-focused:
```go
hook := builtin.NewPerformanceMetricsHook(metrics)
manager.Register(hook, hooks.ScopeGlobal, "")
```

### Audit Hooks

**AuditHook** - Sensitive events:
```go
hook := builtin.NewAuditHook(auditor)
manager.Register(hook, hooks.ScopeGlobal, "")
```

**ComplianceAuditHook** - All events (compliance):
```go
hook := builtin.NewComplianceAuditHook(auditor, 365)  // 365-day retention
manager.Register(hook, hooks.ScopeGlobal, "")
```

### Auto Mode Hook

**AutoModeHook** — classifies tool calls against allow / soft-deny lists and
an optional AI classifier, letting the agent auto-approve low-risk actions
and surface a prompt for everything else. Opt-in via the
`SkipAutoPermissionPrompt` flag or the `SWARM_AUTO_MODE_OPT_IN=1` env var.

```go
hook := builtin.NewAutoModeHook(logger,
    builtin.WithAutoModeConfig(builtin.AutoModeConfig{
        SkipAutoPermissionPrompt: true,
        AutoMode: builtin.AutoModeRules{
            Allow:    []string{"Read(**)", "Grep(**)"},
            SoftDeny: []string{"Bash(rm -rf **)"},
        },
    }),
)
manager.Register(hook, hooks.ScopeGlobal, "")
```

Or, from the SDK client, use `client.WithAutoModeHook()` /
`client.WithAutoModeHookConfig(cfg)`.

### Recap Hook

**RecapHook** — generates a conversation summary on session resume or when
the context window is stressed, based on recent messages. Pluggable
`RecapGenerator` interface for custom summarization strategies.

```go
hook := builtin.NewRecapHook(logger,
    builtin.WithRecapConfig(builtin.RecapConfig{
        EnableRecap:                true,
        MaxMessages:                50,
        Format:                     builtin.RecapFormatDetailed,
        InactivityThresholdMinutes: 5,
    }),
)
manager.Register(hook, hooks.ScopeGlobal, "")
```

Or, from the SDK client, use `client.WithRecapHook()` /
`client.WithRecapHookConfig(cfg)`.

## Creating Custom Hooks

### Basic Hook

```go
type MyHook struct {
    config MyConfig
}

func (h *MyHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
    // Process event
    fmt.Printf("Processing: %s\n", event.Type)
    
    // Continue to next hook
    return hooks.Continue(), nil
}

func (h *MyHook) Filter(event hooks.Event) bool {
    // Only process specific events
    return event.Type == hooks.EventToolBeforeExecute
}

func (h *MyHook) Priority() int {
    return 50  // Medium priority
}

func (h *MyHook) Name() string {
    return "my.custom_hook"
}
```

### Blocking Hook

```go
type SecurityHook struct {
    blockedOperations map[string]string
}

func (h *SecurityHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
    if event.Type == hooks.EventToolBeforeExecute {
        toolName := event.Data["tool_name"].(string)
        
        if reason, blocked := h.blockedOperations[toolName]; blocked {
            // Block execution
            return hooks.Block(reason), nil
        }
    }
    
    return hooks.Continue(), nil
}

func (h *SecurityHook) Priority() int {
    return 99  // Very high - block early
}
```

### Modifying Hook

```go
type ContextInjectorHook struct {
    contextData map[string]interface{}
}

func (h *ContextInjectorHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
    // Clone event
    modified := event.Clone()
    
    // Inject additional context
    if modified.Metadata == nil {
        modified.Metadata = make(map[string]interface{})
    }
    
    for key, value := range h.contextData {
        modified.Metadata[key] = value
    }
    
    // Return modified event
    return hooks.ModifyWithMessage(modified, "context injected"), nil
}
```

## Event Types

Standard events emitted by the SDK:

### Lifecycle Events
- `conversation.created`, `conversation.resumed`, `conversation.completed`
- `agent.initialized`, `agent.started`, `agent.stopped`

### Message Events
- `message.added`, `message.edited`, `message.deleted`
- `message.before_send`, `message.after_receive`

### Tool Events
- `tool.registered`, `tool.before_execute`, `tool.after_execute`
- `tool.execution_failed`, `tool.permission_denied`

### Provider Events
- `provider.before_request`, `provider.after_response`
- `provider.stream_chunk`, `provider.rate_limited`, `provider.error`

### Context Events
- `context.window_exceeded`, `context.trimmed`, `context.summarized`

### Mode Events
- `mode.entered`, `mode.exited`, `mode.transition_requested`

### Group Events
- `group.started`, `group.agent_completed`, `group.completed`

### Steering Events
- `steering.decision_requested`, `steering.intervention`, `steering.override`

## Performance

### Overhead
- **Per-hook**: <0.5ms average
- **Total with 4 built-in hooks**: ~1.2ms per event
- **Memory per event**: ~500 bytes (transient)

### Optimization
- Hooks are cached and sorted once
- Event filtering is fast (O(1) for exact match)
- Concurrent execution supported
- No allocations in hot path

### Thread Safety
- All operations are thread-safe
- RWMutex for read-heavy workloads
- No global state in hooks

## Testing

Run the demo:
```bash
go run ./cmd/hooks-demo/
```

Expected output:
- ✅ 7 hooks registered
- ✅ Pre-tool blocking works
- ✅ Context injection successful
- ✅ Statistics tracking accurate
- ✅ All event types processed

## Use Cases

### 1. Security Policy Enforcement

```go
// Block destructive operations
blocker := NewToolBlockerHook()
blocker.AddBlockedTool("rm", "Destructive operation")
blocker.AddBlockedTool("delete_file", "Destructive operation")
manager.Register(blocker, hooks.ScopeGlobal, "")
```

### 2. Compliance & Auditing

```go
// Audit all sensitive operations
auditor := builtin.NewAuditHook(auditLogger)
manager.Register(auditor, hooks.ScopeGlobal, "")
```

### 3. Context Enrichment

```go
// Inject environment metadata
injector := NewContextInjectorHook(map[string]interface{}{
    "environment": "production",
    "region":      "us-east-1",
    "version":     "v1.2.3",
})
manager.Register(injector, hooks.ScopeGlobal, "")
```

### 4. Rate Limiting

```go
// Limit requests per agent
limiter := NewRateLimiterHook(100, time.Minute)  // 100 req/min
manager.Register(limiter, hooks.ScopeGlobal, "")
```

### 5. Full Observability

```go
// Trace, log, and measure everything
manager.Register(builtin.NewTracingHook(tracer), hooks.ScopeGlobal, "")
manager.Register(builtin.NewLoggingHook(logger, observability.LevelInfo), hooks.ScopeGlobal, "")
manager.Register(builtin.NewMetricsHook(metrics), hooks.ScopeGlobal, "")
```

## Integration

### With Providers

```go
// In provider implementation
func (p *Provider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
    // Emit before request
    event := hooks.Event{
        Type: hooks.EventProviderBeforeRequest,
        Data: map[string]interface{}{
            "request": req,
            "model":   p.model,
        },
    }
    
    _, err := p.hookManager.Emit(ctx, event)
    if err != nil {
        return nil, err  // Blocked by hook
    }
    
    // Make request...
    
    // Emit after response
    p.hookManager.Emit(ctx, hooks.Event{
        Type: hooks.EventProviderAfterResponse,
        Data: map[string]interface{}{
            "response": resp,
            "usage":    resp.Usage,
        },
    })
    
    return resp, nil
}
```

### With Tools

```go
// In tool executor
func (e *Executor) Execute(ctx context.Context, tool Tool, params map[string]interface{}) (*Result, error) {
    // Emit before execution
    event := hooks.Event{
        Type: hooks.EventToolBeforeExecute,
        Data: map[string]interface{}{
            "tool":   tool.Name(),
            "params": params,
        },
    }
    
    _, err := e.hookManager.Emit(ctx, event)
    if err != nil {
        return nil, err  // Blocked by hook
    }
    
    // Execute tool...
    
    return result, nil
}
```

## Status

✅ **Phase 1 Complete**: Core implementation  
✅ **Demo Complete**: All features validated  
🔄 **Phase 2 Pending**: Additional hooks (optional)  
🔄 **Phase 3 Pending**: Comprehensive tests  

## Files

```
hooks/
├── README.md                    # This file
├── IMPLEMENTATION_PLAN.md       # Detailed implementation guide
├── PHASE1_COMPLETE.md           # Phase 1 completion report
├── event.go                     # Event types (Ring 0)
├── hook.go                      # Hook interface (Ring 0)
├── registry.go                  # Registry interface (Ring 0)
├── manager.go                   # Manager implementation (Ring 1)
├── executor.go                  # Executor implementation (Ring 1)
├── filters.go                   # Filtering utilities (Ring 1)
└── builtin/
    ├── logging.go               # Logging hooks
    ├── tracing.go               # Tracing hooks
    ├── metrics.go               # Metrics hooks
    └── audit.go                 # Audit hooks

cmd/hooks-demo/
├── main.go                      # Comprehensive demo
└── DEMO_RESULTS.md              # Demo validation results
```

## Documentation

- [Implementation Plan](IMPLEMENTATION_PLAN.md) - Complete architecture and design
- [Phase 1 Report](PHASE1_COMPLETE.md) - Implementation completion details
- [Demo Results](../cmd/hooks-demo/DEMO_RESULTS.md) - Live testing validation

## Contributing

When adding new hooks:

1. Implement the `Hook` interface
2. Set appropriate priority (0-100)
3. Implement efficient `Filter()` method
4. Handle errors gracefully
5. Add tests (Phase 3)
6. Update documentation

## License

Part of the SwarmOS SDK.
