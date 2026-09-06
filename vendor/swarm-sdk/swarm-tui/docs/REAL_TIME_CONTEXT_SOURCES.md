# Real-Time Context Sources (Architecture Note)

This document captures the current “real-time context sources” design as implemented in SwarmOS. It focuses on injection points, refresh semantics, and prompt-caching behavior.

## Goals / Constraints

- Per-source refresh semantics: `inherit | on_change | every_message | every_turn | ttl`
- Date format: `YYYY-MM-DD`
- Prefer MCP/API-backed sources (resources and prompts)
- Freshness + caching efficiency: stable sources should not be forced into an uncached “dynamic blob”
- **No timestamps in the injected prompt** (freshness metadata should be UI-only)

## Config Model (Global + Project Overrides)

SwarmOS loads two config files and merges them:
- Global: `~/.swarmos/context_config.json`
- Project (optional): `<workspaceRoot>/.swarm/context_config.json`

The schema is:
- `defaults`: shared defaults used when a source is set to `inherit`
- `sources[]`: unified list of built-in and MCP sources, keyed by stable `id`

Merge semantics:
- Project `defaults.*` overrides global `defaults.*` when set
- Project `sources[]` entries override global entries by `id`
- Project can add new sources not present globally

Legacy config files (booleans + `mcp_sources`) are auto-migrated to the unified `sources[]` format.

## Injection Point

Injection is done in a chat-layer provider wrapper:
- `internal/chat/context_injecting_provider.go`

For each provider call, the wrapper:
1) Gets a fresh context block from the orchestrator (see below)
2) Strips any prior injected blocks from `req.SystemPrompt`
3) Injects cached context first, then ephemeral context

## Orchestration and Refresh

The orchestrator lives in:
- `internal/chat/context/orchestrator.go`

It maintains per-source state and decides when to refresh each source.

### Run grouping (`every_message`)

SwarmOS creates a `context_run_id` per user message execution:
- `internal/chat/sdk_integration.go` sets `req.Context["context_run_id"] = ...`

When a source uses `every_message`, it refreshes once per `context_run_id` and is reused across tool turns inside that run.

### Refresh modes

- `every_turn`: refresh each provider call
- `every_message`: refresh once per `context_run_id`
- `ttl`: refresh when `now - last_refresh >= ttl_seconds`
- `on_change` (files only): stat the file; when it changes, re-hash and re-read. If the file disappears, its content is dropped immediately.
- `inherit`: use `defaults.refresh_mode`

### MCP fetch behavior

MCP sources are refreshed concurrently with:
- Per-fetch timeout (`defaults.mcp_timeout_ms`, default 2000ms)
- Concurrency limit (`defaults.mcp_concurrency`, default 4)

On MCP errors, SwarmOS injects a short warning string as the source content (best-effort, non-fatal).

## Cached vs Ephemeral Context

Each source has a `cache_policy`:
- `cached`: injected into `<swarmos_cached_context>…</swarmos_cached_context>`
- `ephemeral`: injected into `<swarmos_context>…</swarmos_context>`
- `inherit`: use `defaults.cache_policy`

The orchestrator renders **two separate injected blocks** (cached first, ephemeral second). This preserves stability for caching while still allowing “live” sources to refresh freely.

## Anthropic Prompt Caching

Anthropic request translation splits the system prompt into multiple system blocks:
- Base system prompt
- Cached context (`<swarmos_cached_context>…`)
- Ephemeral context (`<swarmos_context>…`)

When prompt caching is enabled, SwarmOS applies `system_cache_control` to:
- Base prompt block
- Cached context block

The ephemeral context block is not cache-controlled, which avoids invalidating the cached prefix when volatile sources change.

Implementation:
- `sdk/systemprompt/split.go` (provider-agnostic splitter)
- `sdk/provider/anthropic/translate.go` (applies cache control to base + cached blocks)
