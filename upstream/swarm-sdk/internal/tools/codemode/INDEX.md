# INDEX.md — codemode

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./codemode`

---

## Scope

### `tools/codemode/` — Package providing `CodeMode` execution, wrapping multiple tools into a single `run_code` tool for JavaScript-orchestrated tool calls.

---

## Árbol de Estructura

### `tools/codemode/` — Package providing `CodeMode` execution, wrapping multiple tools into a single `run_code` tool for JavaScript-orchestrated tool calls.
Uses a `Selector` interface to determine which tools are sandboxed inside the JavaScript runtime versus exposed as normal tools.
- `codemode.go` — Core `CodeMode` type, constructors, and registry functions
- `selector.go` — `Selector` interface for tool sandbox matching
- `trace.go` — Tracing support for code-mode execution
**Entry:** `codemode.go:L1`

### `tools/codemode/sandbox` — GojaSandbox provides a JavaScript execution environment using the goja runtime with promise/async-await support and REPL semantics
- `goja.go` — GojaSandbox implementation with event loop and persistent global state
- `interface.go` — Sandbox interface definition
**Entry:** `goja.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/codemode/codemode.go`
- `swarm-sdk/tools/codemode/tui_probe_test.go`
