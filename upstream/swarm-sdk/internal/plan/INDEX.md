# INDEX.md — plan

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./plan`

---

## Scope

### `plan/` — Plan mode broker and tooling for the Swarm SDK


---

## Árbol de Estructura

### `plan/` — Plan mode broker and tooling for the Swarm SDK
Defines the `PlanBroker` interface and `Config` for managing plan mode, along with `EnterPlanModeTool`/`ExitPlanModeTool` implementations and plan file I/O helpers.
- `plan.go` — PlanBroker interface, Config, and plan file operations
- `tools.go` — EnterPlanModeTool and ExitPlanModeTool
- `prompt.go` — Prompt construction for plan approval
**Entry:** `plan.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/plan/prompt.go`
- `swarm-sdk/plan/tools.go`
