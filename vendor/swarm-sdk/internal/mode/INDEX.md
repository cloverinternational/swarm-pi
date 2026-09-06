# INDEX.md — mode

> Modo: incremental | Directorios indexados: 4
> Para regenerar: `idx generate --path ./mode`

---

## Scope

### `mode/` — Defines the `OperatingMode`, `GroupCoordinator`, and `ContextBuilder` types for managing multi-agent workflows and tool permissions


---

## Árbol de Estructura

### `mode/` — Defines the `OperatingMode`, `GroupCoordinator`, and `ContextBuilder` types for managing multi-agent workflows and tool permissions
- `mode.go` — Core `OperatingMode` type and tool gating logic
- `workflow_factory.go` — `GroupCoordinator` execution engine and `ContextBuilder` 
- `registry.go` — Registry for builtin operating modes
**Entry:** `mode.go:L1`

### `mode/consensus/` — ConsensusEvaluator implementations for evaluating agent agreement across voting, semantic, and LLM synthesis strategies
- `evaluator.go` — defines ConsensusEvaluator interface, VotingConsensusEvaluator, SemanticConsensusEvaluator, LLMSynthesisEvaluator, and ConsensusEvaluatorRegistry
**Entry:** `evaluator.go:L1`

### `mode/gates/` — Workflow control framework built around the `Gate` interface and `ConsensusGate` implementation
Provides gating primitives for workflow execution, including the `Gate` interface, `BaseGate` embeddable type, `GateEvaluator` interface, and `ConsensusGate` with consensus-based evaluation.
- `gate.go` — Core `Gate` interface and `ConsensusGate` type
- `manager.go` — Gate management and evaluation orchestration
- `resource.go` — Resource-related gate configuration types
**Entry:** `gate.go:L1`

### `mode/steering/` — Steering modes for agent, group, and global coordination
Provides DefaultAgentSteering, DefaultGlobalSteering, and DefaultGroupSteering with pluggable rules, policies, and coordination strategies.
- `agent_steering.go` — Agent-level response and tool validation rules
- `global_steering.go` — Global routing and policy enforcement
- `group_steering.go` — Group coordination with pluggable CoordinationStrategy
**Entry:** `agent_steering.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/mode/builtin.go`
