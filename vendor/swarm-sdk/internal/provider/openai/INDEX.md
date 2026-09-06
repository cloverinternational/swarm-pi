# INDEX.md — openai

> Modo: incremental | Directorios indexados: 3
> Para regenerar: `idx generate --path ./openai`

---

## Scope

### `provider/openai/` — OpenAI API provider implementation with Config and Factory for chat completions


---

## Árbol de Estructura

### `provider/openai/` — OpenAI API provider implementation with Config and Factory for chat completions
Supports model listing, OAuth authentication, and translation of requests to OpenAI's chat completion format including streaming and tool use.
- `models.go` — Chat completion request/response types and API structures
- `models_list.go` — Model catalog and profile definitions
- `oauth.go` — OAuth and environment-based authentication
**Entry:** `models.go:L1`

### `provider/openai/profiles/` — Registry and Profile types for OpenAI-compatible provider configuration
Defines the `Profile` struct and its configuration sub-types (`AuthConfig`, `ModelConfig`, `FeatureFlags`, `TransformConfig`, etc.), along with a `Registry` for loading, querying, and merging provider profiles.
- `profile.go` — Core `Profile` struct and all configuration type definitions
- `loader.go` — `Registry` type and profile loading/management functions
**Entry:** `profile.go:L1`

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

## Señales de Cambio Reciente

- `swarm-sdk/provider/openai/stream.go`
- `swarm-sdk/provider/openai/stream_test.go`
- `swarm-sdk/provider/openai/translate.go`
- `swarm-sdk/provider/openai/README.md`
- `swarm-sdk/provider/openai/profiles/loader_test.go`
- `swarm-sdk/provider/openai/profiles/profile.go`
- `swarm-sdk/provider/openai/profiles/profiles.toml`
- `swarm-sdk/provider/openai/register.go`
