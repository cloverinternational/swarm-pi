# INDEX.md — profiling

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./profiling`

---

## Scope

### `cache/profiling/` — Cache profiling package providing the `LoggingProfiler` type for tracking cache breaks, mutations, and API responses


---

## Árbol de Estructura

### `cache/profiling/` — Cache profiling package providing the `LoggingProfiler` type for tracking cache breaks, mutations, and API responses
Includes `Factory` for profiler creation and `Registry` for global profiler management.
- `types.go` — Core types for cache break categorization and severity
- `logger.go` — `LoggingProfiler` implementation with reporting and metrics
- `validator.go` — Cache break validation and detection logic
**Entry:** `logger.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Cambiar modo incremental | `cache.py` |

---

