# INDEX.md — silver

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./silver`

---

## Scope

### `silver/` — SilverStore and Bronze file reading utilities for session trees


---

## Árbol de Estructura

### `silver/` — SilverStore and Bronze file reading utilities for session trees
The primary type is SilverStore, which persists Silver tree indices on disk, and the package exposes functions for reading and classifying Bronze event files and building session trees.
- `store.go` — SilverStore definition and disk persistence
- `bronze_reader.go` — Bronze file listing and reading functions
- `types.go` — shared types and configuration
**Entry:** `store.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/silver/store.go`
- `swarm-sdk/silver/bronze_reader.go`
- `swarm-sdk/silver/domain_detector.go`
- `swarm-sdk/silver/retrieval.go`
- `swarm-sdk/silver/session_detector.go`
- `swarm-sdk/silver/silver_test.go`
- `swarm-sdk/silver/tree_builder.go`
- `swarm-sdk/silver/tree_verifier.go`
- `swarm-sdk/silver/types.go`
