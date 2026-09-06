# INDEX.md — steeringtools

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./steeringtools`

---

## Scope

### `tools/steeringtools` — Steering tools for influencing agent behavior via system prompt injection, user interaction, and tool blocking


---

## Árbol de Estructura

### `tools/steeringtools` — Steering tools for influencing agent behavior via system prompt injection, user interaction, and tool blocking
- `inject.go` — InjectSystemNoteTool prepends steering notes into an agent's system prompt
- `ask_user.go` — AskUserTool prompts the user for input with sync/fallback support
- `block.go` — BlockNextToolTool blocks the next tool invocation
**Entry:** `inject.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/steeringtools/wired_test.go`
- `swarm-sdk/tools/steeringtools/ask_user.go`
- `swarm-sdk/tools/steeringtools/ask_user_phase4_test.go`
- `swarm-sdk/tools/steeringtools/block.go`
- `swarm-sdk/tools/steeringtools/halt_peer.go`
- `swarm-sdk/tools/steeringtools/inject.go`
- `swarm-sdk/tools/steeringtools/log_concern.go`
- `swarm-sdk/tools/steeringtools/observe.go`
- `swarm-sdk/tools/steeringtools/tools.go`
- `swarm-sdk/tools/steeringtools/tools_test.go`
