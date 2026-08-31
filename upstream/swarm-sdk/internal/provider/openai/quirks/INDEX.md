# INDEX.md — quirks

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./quirks`

---

## Scope

### `provider/openai/quirks/` — Provides the `Adapter` interface and `Registry` for managing provider-specific API transformations.

---

## Árbol de Estructura

### `provider/openai/quirks/` — Provides the `Adapter` interface and `Registry` for managing provider-specific API transformations.
- `adapter.go` — Defines the `Adapter` interface and `PassthroughAdapter`
- `registry.go` — Implements the `Registry` for adapter lookup
- `zai.go` — Implements the `ZAIAdapter`
**Entry:** `adapter.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar nuevo LLM provider | `engine/provider_identity.py` |

---

