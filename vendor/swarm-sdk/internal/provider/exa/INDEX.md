# INDEX.md — exa

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./exa`

---

## Scope

### `provider/exa/` — Exa search-only Provider implementing provider.

---

## Árbol de Estructura

### `provider/exa/` — Exa search-only Provider implementing provider.Provider
The `Provider` type wraps the Exa API for search and answer queries, exposing `Chat()` and `Stream()` methods that return formatted results as assistant responses.
- `provider.go` — Provider type with Chat and Stream methods
- `exa_api.go` — Exa API types and Search/Answer functions
- `config.go` — Config struct and DefaultConfig
**Entry:** `provider.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar nuevo LLM provider | `engine/provider_identity.py` |

---

