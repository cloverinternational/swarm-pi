# INDEX.md — analytics

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./analytics`

---

## Scope

### `analytics/` — Dispatcher orchestrates analytics event spooling and dispatch to a remote endpoint.

---

## Árbol de Estructura

### `analytics/` — Dispatcher orchestrates analytics event spooling and dispatch to a remote endpoint.
- `dispatcher.go` — Core Dispatcher struct tying together client, spool, and artifact spool
- `artifact_spool.go` — ArtifactSpool for enqueuing, loading, and pruning file artifacts
- `spool.go` — Spool for buffering and batching analytics events

**Entry:** `dispatcher.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/analytics/dispatcher.go`
- `swarm-sdk/analytics/manual_dispatcher_test.go`
- `swarm-sdk/analytics/stats.go`
- `swarm-sdk/analytics/artifact_spool.go`
- `swarm-sdk/analytics/config.go`
- `swarm-sdk/analytics/spool.go`
- `swarm-sdk/analytics/tracker.go`
- `swarm-sdk/analytics/client.go`
- `swarm-sdk/analytics/config_test.go`
- `swarm-sdk/analytics/dispatcher_test.go`
