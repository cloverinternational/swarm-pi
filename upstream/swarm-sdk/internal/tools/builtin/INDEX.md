# INDEX.md — builtin

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./builtin`

---

## Scope

### `tools/builtin/` — Builtin tool implementations including `VaultExecParams` and `AgentBrowserTool`


---

## Árbol de Estructura

### `tools/builtin/` — Builtin tool implementations including `VaultExecParams` and `AgentBrowserTool`
Provides concrete tool implementations such as the vault execution tool and agent browser tool, along with a sleep-blocker filter for agent events.
- `vault_exec.go` — Vault credential execution tool with `VaultExecParams`
- `subagent.go` — Agent browser tool (`AgentBrowserTool`) and related params
- `delegate_task.go` — Task delegation logic
**Entry:** `vault_exec.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/builtin/delegate_task.go`
- `swarm-sdk/tools/builtin/delegate_task_id_test.go`
- `swarm-sdk/tools/builtin/fuzz_test.go`
- `swarm-sdk/tools/builtin/bash.go`
- `swarm-sdk/tools/builtin/bash_overflow_probe_test.go`
- `swarm-sdk/tools/builtin/agent_browser.go`
- `swarm-sdk/tools/builtin/annoyed_test.go`
- `swarm-sdk/tools/builtin/ask_parent.go`
- `swarm-sdk/tools/builtin/cron_scheduler.go`
- `swarm-sdk/tools/builtin/delegate.go`
