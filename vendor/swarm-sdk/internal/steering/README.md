# Steering Package

The steering package provides intelligent oversight for agent execution, monitoring tool calls and making decisions about whether to allow, block, or modify them.

## Features

- **Mode-based steering**: Block write tools in PLAN mode
- **Efficiency analysis**: Detect redundant reads, doc bloat, stalling
- **LLM-based evaluation**: Use a cheap model for intelligent decisions
- **Model profile integration**: Works seamlessly with account manager
- **Zero-config**: Works out of the box with sensible defaults
- **Fail-open**: Never breaks your agent on errors

## Quick Start

### Option 1: Mode-Only (Lightest Weight)

No LLM calls, just mode-based rules:

```go
import "github.com/Swarm-Code/mono/swarm-sdk/steering"

s, _ := steering.SetupModeOnly()
```

### Option 2: With Model Profile

```go
import (
    "github.com/Swarm-Code/mono/swarm-sdk/steering"
    "github.com/Swarm-Code/mono/swarm-sdk/provider"
)

prov := provider.NewAntheticProvider("your-api-key")
s, _ := steering.SetupWithModel("claude-3-haiku-20240307", prov)
```

### Option 3: From Account Manager

```go
import (
    "github.com/Swarm-Code/mono/swarm-sdk/account"
    "github.com/Swarm-Code/mono/swarm-sdk/steering"
)

mgr := account.NewManager()
mgr.LoadProfiles("profiles.json")

s, _ := steering.SetupFromManager(mgr, prov)
```

## Integration with Agent

### Method 1: Hook Registration

```go
import (
    "github.com/Swarm-Code/mono/swarm-sdk/agent"
    "github.com/Swarm-Code/mono/swarm-sdk/hooks"
    "github.com/Swarm-Code/mono/swarm-sdk/hooks/builtin"
    "github.com/Swarm-Code/mono/swarm-sdk/steering"
)

// Create steering
s, _ := steering.SetupModeOnly()

// Create hook
hook := builtin.NewSteeringHook(s)

// Register with agent
hookExecutor := hooks.NewExecutor()
hookExecutor.Register(hooks.EventToolBeforeExecute, hook)

ag := agent.NewAgent(
    agent.WithProvider(prov),
    agent.WithHooks(hookExecutor),
)
```

### Method 2: Agent Option (If Supported)

```go
ag := agent.NewAgent(
    agent.WithProvider(prov),
    agent.WithSteering(s),
)
```

## How It Works

### Decision Flow

```
Tool Call
    ↓
SteeringHook.OnEvent()
    ↓
┌─────────────────────────────────┐
│ 1. Check if steering enabled    │
│ 2. Check mode (PLAN blocks)     │
│ 3. Analyze efficiency          │
│ 4. LLM evaluation (if config)  │
└─────────────────────────────────┘
    ↓
Decision: APPROVE | BLOCK | MODIFY
```

### Decision Types

| Decision | Effect |
|----------|--------|
| `APPROVE` | Tool executes normally |
| `BLOCK` | Tool execution prevented |
| `MODIFY` | Tool input modified |
| `RETRY` | Tool should be retried |

## Efficiency Analysis

The steering package includes pattern detection:

| Pattern | Detection |
|---------|-----------|
| Redundancy | Same file read >3 times |
| Doc Bloat | Docs before implementation |
| Stalling | Many operations without writes |
| Priority | Operations in wrong order |

## Configuration

```go
cfg := &steering.Config{
    Enabled:          true,
    ModelProfile:     profile,  // Optional
    Provider:         prov,     // Optional
    ModeBased:        true,
    EnableEfficiency: true,
    MaxFileReads:     3,
    LogDecisions:     true,
}

s, _ := steering.New(cfg)
```

## Runtime Control

```go
// Disable steering temporarily
s.SetEnabled(false)

// Re-enable
s.SetEnabled(true)

// Get efficiency score (1-5)
score := s.GetEfficiencyScore()

// Get decision history
decisions := s.GetDecisions()

// Reset for new task
s.Reset()
```

## Model Profile Setup

Add to your `profiles.json`:

```json
{
  "profiles": {
    "steering": {
      "name": "claude-3-haiku-20240307",
      "max_tokens": 500,
      "temperature": 0.3
    }
  }
}
```

The steering package will automatically use this profile when configured via `SetupFromManager()`.

## Best Practices

1. **Use mode-only for simple cases**: No LLM cost, still useful
2. **Use haiku for LLM evaluation**: Cheap and fast
3. **Check efficiency score**: Monitor agent performance
4. **Reset between tasks**: Start fresh for new conversations

## Architecture

```
swarm-sdk/
├── steering/
│   ├── steering.go         # Main steering implementation
│   ├── config.go           # Configuration helpers
│   ├── llm_evaluator.go    # LLM-based evaluation
│   └── efficiency/
│       └── analyzer.go     # Pattern detection
└── hooks/builtin/
    └── steering_hook.go    # Hook integration
```

## Examples

See `example_test.go` for complete working examples.

## License

MIT
