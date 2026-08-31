# INDEX.md — ollama

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./ollama`

---

## Scope

### `provider/ollama/` — Implements the Ollama provider via the `Provider` struct, supporting both cloud and local modes.

---

## Árbol de Estructura

### `provider/ollama/` — Implements the Ollama provider via the `Provider` struct, supporting both cloud and local modes.
Includes configuration helpers (`Config`) and model definitions (`ModelDefinition`).
- `provider.go` — Core provider implementation and interface methods
- `config.go` — Configuration defaults and validation
- `types.go` — Model definitions and lookups
**Entry:** `provider.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar nuevo LLM provider | `engine/provider_identity.py` |

---

