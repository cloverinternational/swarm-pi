# INDEX.md — gates

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./gates`

---

## Scope

### `mode/gates/` — Workflow control framework built around the `Gate` interface and `ConsensusGate` implementation


---

## Árbol de Estructura

### `mode/gates/` — Workflow control framework built around the `Gate` interface and `ConsensusGate` implementation
Provides gating primitives for workflow execution, including the `Gate` interface, `BaseGate` embeddable type, `GateEvaluator` interface, and `ConsensusGate` with consensus-based evaluation.
- `gate.go` — Core `Gate` interface and `ConsensusGate` type
- `manager.go` — Gate management and evaluation orchestration
- `resource.go` — Resource-related gate configuration types
**Entry:** `gate.go:L1`

---

