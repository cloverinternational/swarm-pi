# INDEX.md — scope

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./scope`

---

## Scope

### `tools/swarmtools/scope/` — Scope detection package providing language-aware structural hashing via `BraceDetector` and `CDetector`


---

## Árbol de Estructura

### `tools/swarmtools/scope/` — Scope detection package providing language-aware structural hashing via `BraceDetector` and `CDetector`
The package detects structural scopes (functions, types, etc.) so line hashes remain stable across insertions and deletions elsewhere in the file.
- `detector.go` — core scope detection interface and language registration
- `brace_base.go` — brace-based scope detectors for C-style languages
- `cache.go` — file hash caching with `FileHashCache` for scope lookups
**Entry:** `detector.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/swarmtools/scope/demo_compare.go`
