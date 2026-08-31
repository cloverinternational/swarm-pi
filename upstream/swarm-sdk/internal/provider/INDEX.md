# INDEX.md — provider

> Modo: incremental | Directorios indexados: 12
> Para regenerar: `idx generate --path ./provider`

---

## Scope

### `provider/` — Provider registration and orchestration for LLM backends


---

## Árbol de Estructura

### `provider/` — Provider registration and orchestration for LLM backends
Defines `Provider` registration via `RegisterCompatible` and its aliases, along with `OrchestratorBuilder` for fluently constructing an `Orchestrator` that manages multiple provider entries.
- `provider.go` — Core `Provider` type and registration functions
- `orchestrator.go` — `Orchestrator` type for provider routing
- `orchestrator_builder.go` — `OrchestratorBuilder` fluent API
**Entry:** `provider.go:L1`

### `provider/anthropic/` — Anthropic provider for the SwarmOS SDK, implementing message processing, tool workflows, web search, and response quality validation
- `provider.go` — core provider implementation and exported functions
- `models.go` — Anthropic Messages API request/response types
- `device_identity.go` — device identity support
**Entry:** `provider.go:L1`

### `provider/codex` — Provider implementation for the Codex API
Defines the `Provider` struct and `New` constructor to support `Chat` and `Stream` capabilities with integrated observability.
- `provider.go` — Defines Provider, Config, and streaming logic
**Entry:** `provider.go:L1`

### `provider/exa/` — Exa search-only Provider implementing provider.Provider
The `Provider` type wraps the Exa API for search and answer queries, exposing `Chat()` and `Stream()` methods that return formatted results as assistant responses.
- `provider.go` — Provider type with Chat and Stream methods
- `exa_api.go` — Exa API types and Search/Answer functions
- `config.go` — Config struct and DefaultConfig
**Entry:** `provider.go:L1`

### `provider/gemini/` — Gemini provider with `Config`, cache management, and API translation for the Gemini backend
Provides `Config` with `Validate`/`MethodURL`, full cache CRUD via `CreateCache`/`Cache`/`ListCaches`/`DeleteCache`/`UpdateCache`, and `ParseGeminiError` for translating Gemini API errors.
- `translate.go` — Request/response type translation and error parsing
- `cache.go` — Cached content CRUD operations
- `register.go` — Provider configuration and registration
**Entry:** `register.go:L1`

### `provider/http/` — HTTP client with configurable authentication strategies and retry logic
Implements the `AuthStrategy` interface with `NoAuth`, `APIKeyAuth`, and `CustomHeaderAuth` concrete strategies, backed by a client supporting exponential backoff retries.
- `client.go` — HTTP client with retry and backoff logic
- `auth.go` — `AuthStrategy` interface and authentication implementations
- `request.go` — request construction helpers
**Entry:** `client.go:L1`

### `provider/minimax/` — Provider implements the provider.Provider interface for MiniMax's Anthropic-compatible API.
Config, ModelInfo, and helper functions manage model selection and validation, while Chat and Stream handle synchronous and streaming requests.
- `provider.go` — core Provider type, Config, and interface methods
- `translate.go` — request/response translation and model info lookup
- `stream.go` — streaming response processing
**Entry:** `provider.go:L1`

### `provider/mock/` — Mock implementation of provider.Provider for testing
Exposes `Provider` struct with configurable responses and recorded requests via `AddResponse`, `GetRequests`, and `Reset`.
- `provider.go` — Mock provider with response injection and request tracking
**Entry:** `provider.go:L1`

### `provider/ollama/` — Implements the Ollama provider via the `Provider` struct, supporting both cloud and local modes.
Includes configuration helpers (`Config`) and model definitions (`ModelDefinition`).
- `provider.go` — Core provider implementation and interface methods
- `config.go` — Configuration defaults and validation
- `types.go` — Model definitions and lookups
**Entry:** `provider.go:L1`

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
- `swarm-sdk/provider/fuzz_test.go`
- `swarm-sdk/provider/anthropic/translate.go`
- `swarm-sdk/provider/openai/translate.go`
- `swarm-sdk/provider/schema.go`
- `swarm-sdk/provider/compatibility.go`
- `swarm-sdk/provider/compatibility_test.go`
- `swarm-sdk/provider/openai/README.md`
- `swarm-sdk/provider/openai/profiles/loader_test.go`
