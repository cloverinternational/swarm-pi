# INDEX.md — http

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./http`

---

## Scope

### `provider/http/` — HTTP client with configurable authentication strategies and retry logic


---

## Árbol de Estructura

### `provider/http/` — HTTP client with configurable authentication strategies and retry logic
Implements the `AuthStrategy` interface with `NoAuth`, `APIKeyAuth`, and `CustomHeaderAuth` concrete strategies, backed by a client supporting exponential backoff retries.
- `client.go` — HTTP client with retry and backoff logic
- `auth.go` — `AuthStrategy` interface and authentication implementations
- `request.go` — request construction helpers
**Entry:** `client.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar nuevo LLM provider | `engine/provider_identity.py` |

---

## Señales de Cambio Reciente

- `swarm-sdk/provider/http/client.go`
