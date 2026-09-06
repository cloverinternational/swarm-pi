# Code Mode Integration Summary

## Overview

Code mode is now integrated into swarm-sdk at `tools/codemode/`. It provides JavaScript-based tool orchestration with batching capabilities via `Promise.all()`, inspired by `pydantic/pydantic-ai-harness` CodeMode.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Agent/Mode Layer                            │
│    (uses CodeMode.Install() to wrap registry)                        │
└─────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      CodeMode (codemode.go)                          │
│  - Manages tool selection via Selector                              │
│  - Wraps Registry to hide sandboxed tools                           │
│  - Registers single run_code tool                                   │
│  - Maintains per-conversation sandbox instances                     │
└─────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    RunCodeTool (tool.go)                             │
│  - Implements tools.Tool interface                                  │
│  - Dynamic description with JS signatures                           │
│  - Routes code to sandbox.Eval()                                    │
└─────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    GojaSandbox (sandbox/goja.go)                     │
│  - Pure-Go JavaScript execution via goja                            │
│  - Promise support via goja_nodejs/eventloop                        │
│  - Console capture, REPL state persistence                          │
│  - Tool function injection as JS callables                          │
└─────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                 tools.Executor / Registry                            │
│    (existing swarm-sdk tool infrastructure)                          │
└─────────────────────────────────────────────────────────────────────┘
```

## Key Components

### 1. Selector (selector.go)
Determines which tools are sandboxed inside `run_code`:
- `AllTools{}` - All tools sandboxed (default)
- `NewByNames("tool_a", "tool_b")` - Only named tools
- `NewByPredicate(fn)` - Custom filter function
- `NewByMetadata(map[string]any{"code_mode": true})` - Match tool metadata

### 2. Signature Renderer (signature.go)
Converts JSON Schema to TypeScript/JSDoc-style function signatures:
- Type mapping: `string` → `string`, `integer` → `number`, `array` → `T[]`
- Enum → `Literal["a"|"b"]`
- Object → `TypedDict` or inline `{ prop: type }`
- Name sanitization for JS-safe identifiers

### 3. Dispatch Adapter (dispatch.go)
Bridges JS tool calls to `tools.Executor`:
- Resolves sanitized names back to original
- Routes through full validation/permission pipeline
- Converts `ToolResult` to JSON-serializable form for JS

### 4. Trace Collector (trace.go)
Captures nested tool call metadata:
- Unique call IDs for tracing
- Duration tracking
- Maps to `ToolResult.Metadata.tool_calls` / `tool_returns`

### 5. GojaSandbox (sandbox/goja.go)
Core execution engine:
- Uses `github.com/dop251/goja` for pure-Go JS
- `github.com/dop251/goja_nodejs/eventloop` for Promise support
- Console capture via injected `console.log`
- REPL state via `globals` map persisted across calls
- Timeout via context cancellation + `vm.Interrupt()`

## Usage

```go
import "github.com/Swarm-Code/mono/swarm-sdk/tools/codemode"

// Create code mode with default settings
cm := codemode.New(
    codemode.WithSelector(codemode.AllTools{}),
    codemode.WithTimeout(60 * time.Second),
)

// Install wraps the registry, hiding selected tools
wrappedRegistry, err := cm.Install(registry, executor)
if err != nil {
    // handle error
}

// Use wrappedRegistry with agent
agent := agent.New(wrappedRegistry, ...)
```

## Batching with Promise.all()

The model writes:
```javascript
const results = await Promise.all([
    tool_a({input: "x"}),
    tool_b({input: "y"}),
    tool_c({input: "z"})
]);
return results;
```

All three tools execute concurrently on the event loop, with Go-side goroutines resolving their Promises.

## REPL Semantics

State persists across `run_code` calls within a conversation:
```javascript
// First call
let x = 42;

// Second call (x is preserved)
return x + 8;  // Returns 50
```

Use `restart: true` to reset state:
```javascript
// Clears all variables
```

## Observability

Nested tool calls appear in `ToolResult.Metadata`:
```go
result.Metadata["tool_calls"]   // map[callID]ToolCallMeta
result.Metadata["tool_returns"]  // map[callID]ToolReturnMeta
result.Metadata["code_mode"]     // true
```

## Enabling code mode (TUI)

Toggle with `/codemode on` (aliases `/cm`, `/code-mode`) or `/codemode off`.
When enabled, all individual tools are hidden from the LLM tool list, only
`run_code` is offered, and a code-mode instruction block (`codemode.SystemPrompt`)
is appended to the system prompt so the model knows to orchestrate via `run_code`.
The flip is applied **in place** on the shared tool registry — the running agent
picks up the new tool list and instruction on its next request; no agent
recreation is needed.

## Limitations

1. **No class definitions** - goja is ES5.1+ with limited ES6 support
2. **No external modules** - Only injected tools and stdlib-like helpers (`Math`, `console`, `JSON`)
3. **One sandbox per conversation** - a Sandbox is not safe for concurrent `Eval`; access is serialized

## Fixed since v1

- **Promise resolution** now polls on the event-loop goroutine (via
  `loop.SetTimeout`) instead of a separate goroutine, eliminating a data race on
  goja's non-goroutine-safe `Promise.State()`.
- **Sync tool dispatch** now uses the eval context (honoring timeout/cancellation)
  instead of `context.Background()`.
- **Model steering**: an explicit `run_code` instruction block is injected while
  code mode is enabled, so the model reliably uses `run_code` instead of falling
  back to individual tool calls.

## Future Enhancements

1. **Starlark backend** - Python syntax alternative behind same Sandbox interface
2. **Type checking** - Pre-run type validation against tool signatures
3. **Stream support** - Yield intermediate results during long execution

## Files

```
tools/codemode/
├── codemode.go          # CodeMode struct, Install(), registry wrapper
├── tool.go              # RunCodeTool implementation
├── selector.go          # Tool selection logic
├── signature.go        # JSON Schema → JSDoc conversion
├── dispatch.go         # JS → tools.Executor bridge
├── trace.go            # Nested call metadata
└── sandbox/
    ├── interface.go     # Sandbox interface
    └── goja.go          # Goja-based implementation
```

## Dependencies

- `github.com/dop251/goja` - Pure-Go JavaScript runtime
- `github.com/dop251/goja_nodejs/eventloop` - Promise/event loop support
