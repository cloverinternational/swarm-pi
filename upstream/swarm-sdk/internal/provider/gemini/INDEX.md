# INDEX.md — gemini

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./gemini`

---

## Scope

### `provider/gemini/` — Gemini provider with `Config`, cache management, and API translation for the Gemini backend


---

## Árbol de Estructura

### `provider/gemini/` — Gemini provider with `Config`, cache management, and API translation for the Gemini backend
Provides `Config` with `Validate`/`MethodURL`, full cache CRUD via `CreateCache`/`Cache`/`ListCaches`/`DeleteCache`/`UpdateCache`, and `ParseGeminiError` for translating Gemini API errors.
- `translate.go` — Request/response type translation and error parsing
- `cache.go` — Cached content CRUD operations
- `register.go` — Provider configuration and registration
**Entry:** `register.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar nuevo LLM provider | `engine/provider_identity.py` |

---

## Señales de Cambio Reciente

- `swarm-sdk/provider/gemini/translate.go`
- `swarm-sdk/provider/gemini/translate_test.go`
