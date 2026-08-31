# INDEX.md — agent

> Modo: incremental | Directorios indexados: 3
> Para regenerar: `idx generate --path ./agent`

---

## Scope

### `agent/` — Package agent implements the agent runtime layer, executing tasks using providers, tools, and memory while maintaining full observability.

---

## Árbol de Estructura

### `agent/` — Package agent implements the agent runtime layer, executing tasks using providers, tools, and memory while maintaining full observability.
It defines the `IntermediateUpdate` interface and various update types such as `ToolCallUpdate` and `ContentUpdate` for tracking agent execution.
- `agent.go` — Agent runtime and update types
- `agent_config.go` — Agent configuration
- `steering_target.go` — Steering target logic
**Entry:** `agent.go:L1`

### `agent/agenttest` — MockProvider test double for scripting agent responses without real LLM calls
Provides `MockProvider` and helper functions (`RespondWith`, `ThenCallTool`, `ThenRespondWith`) to pre-script agent chat responses and inspect call history via `ToolCallCount`, `CallLog`, and `TotalCalls`.
- `mock.go` — MockProvider and scripted response helpers
**Entry:** `mock.go:L1`

### `agent/optimized/` — OptimizedAgentExecutor provides pooled, performance-optimized agent execution
Currently under development and disabled due to build errors; wraps agent execution with sync pooling and fast JSON handling.
- `executor.go` — OptimizedAgentExecutor and batch execution with metrics
**Entry:** `executor.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Cambiar loop de ejecución | `analyzer.py` |

---

## Señales de Cambio Reciente

- `swarm-sdk/agent/audit_log.go`
- `swarm-sdk/agent/audit_log_test.go`
- `swarm-sdk/agent/steering_stream.go`
- `swarm-sdk/agent/steering_stream_audit_test.go`
- `swarm-sdk/agent/background_agent.go`
- `swarm-sdk/agent/background_agent_probe_test.go`
- `swarm-sdk/agent/drift_tracker.go`
- `swarm-sdk/agent/drift_tracker_test.go`
- `swarm-sdk/agent/steering_ask_adapter.go`
- `swarm-sdk/agent/steering_ask_adapter_test.go`
