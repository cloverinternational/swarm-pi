# INDEX.md — deepwiki

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./deepwiki`

---

## Scope

### `tools/deepwiki/` — Wiki generation pipeline with CompilerAgent, EmbedderAgent, and PipelineAgent


---

## Árbol de Estructura

### `tools/deepwiki/` — Wiki generation pipeline with CompilerAgent, EmbedderAgent, and PipelineAgent
Orchestrates LLM-driven wiki compilation, embedding, and page regeneration using configurable agents.
- `engine.go` — Agent construction, pipeline orchestration, and option functions
- `types.go` — Request/result types (GenerateWikiRequest, CompilerProgress, EmbedderResult, etc.)
- `llm.go` — LLM HTTP client and streaming response handling
**Entry:** `engine.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/deepwiki/llm.go`
- `swarm-sdk/tools/deepwiki/engine.go`
- `swarm-sdk/tools/deepwiki/smart_cache.go`
- `swarm-sdk/tools/deepwiki/types.go`
