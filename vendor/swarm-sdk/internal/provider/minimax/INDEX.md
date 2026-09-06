# INDEX.md — minimax

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./minimax`

---

## Scope

### `provider/minimax/` — Provider implements the provider.

---

## Árbol de Estructura

### `provider/minimax/` — Provider implements the provider.Provider interface for MiniMax's Anthropic-compatible API.
Config, ModelInfo, and helper functions manage model selection and validation, while Chat and Stream handle synchronous and streaming requests.
- `provider.go` — core Provider type, Config, and interface methods
- `translate.go` — request/response translation and model info lookup
- `stream.go` — streaming response processing
**Entry:** `provider.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar nuevo LLM provider | `engine/provider_identity.py` |

---

