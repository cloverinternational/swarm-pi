# INDEX.md — anthropic

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./anthropic`

---

## Scope

### `provider/anthropic/` — Anthropic provider for the SwarmOS SDK, implementing message processing, tool workflows, web search, and response quality validation


---

## Árbol de Estructura

### `provider/anthropic/` — Anthropic provider for the SwarmOS SDK, implementing message processing, tool workflows, web search, and response quality validation
- `provider.go` — core provider implementation and exported functions
- `models.go` — Anthropic Messages API request/response types
- `device_identity.go` — device identity support
**Entry:** `provider.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar nuevo LLM provider | `engine/provider_identity.py` |

---

## Señales de Cambio Reciente

- `swarm-sdk/provider/anthropic/translate.go`
