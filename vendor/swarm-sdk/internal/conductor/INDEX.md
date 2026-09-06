# INDEX.md — conductor

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./conductor`

---

## Scope

### `conductor/` — `Conductor` struct for Symphony-compatible issue orchestration


---

## Árbol de Estructura

### `conductor/` — `Conductor` struct for Symphony-compatible issue orchestration
Wires together a WORKFLOW.md definition, an IssueTracker, a PeerPool, the client.Event bus, and a PeerDiscoveryPoller to coordinate multi-agent workflows.
- `conductor.go` — defines `Conductor` and its functional option constructors
- `orchestrator.go` — provides orchestration and task dispatch logic
- `tracker_forgejo.go` — Forgejo issue tracker integration
**Entry:** `conductor.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/conductor/conductor.go`
- `swarm-sdk/conductor/conductor_test.go`
- `swarm-sdk/conductor/doc.go`
- `swarm-sdk/conductor/export_test.go`
- `swarm-sdk/conductor/orchestrator.go`
- `swarm-sdk/conductor/orchestrator_probe_test.go`
- `swarm-sdk/conductor/pool_live.go`
- `swarm-sdk/conductor/tracker.go`
- `swarm-sdk/conductor/tracker_forgejo.go`
- `swarm-sdk/conductor/tracker_github.go`
