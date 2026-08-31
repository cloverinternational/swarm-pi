# Context Sources

This document describes SwarmOS “Context Sources”: a configurable set of real-time inputs that are injected into the model system prompt.

## Overview

Context Sources let SwarmOS include:
- **Stable file instructions** (project/global `CLAUDE.md`, `SWARM.md`, `AGENTS.md`)
- **Dynamic environment context** (git status, current date, etc.)
- **Live MCP-backed context** (resources and prompt outputs)

Sources are rendered as XML `<context name="…">…</context>` blocks and injected into the system prompt. For caching efficiency, SwarmOS splits context into two injected sections:
- `<swarmos_cached_context>…</swarmos_cached_context>` for **cacheable** sources
- `<swarmos_context>…</swarmos_context>` for **ephemeral** sources

## Supported Sources

### Built-in File Sources
- **CLAUDE.md**: Project instructions and guidelines (searches `.claude/CLAUDE.md` then `./CLAUDE.md`)
- **SWARM.md**: SwarmOS-specific project configuration (searches `.swarm/SWARM.md` then `./SWARM.md`)  
- **AGENTS.md**: Agent-specific definitions and rules (searches `.swarm/AGENTS.md`, `.claude/AGENTS.md`, then `./AGENTS.md`)
- **Global CLAUDE.md**: User-level instructions from `~/.claude/CLAUDE.md`
- **Global SWARM.md**: User-level SwarmOS config from `~/.swarm/SWARM.md`

### Built-in Dynamic Sources
- **Git Status**: Current branch and working directory status
- **Project Name**: Current directory name
- **Current Date**: Current date in `YYYY-MM-DD` format

### MCP Sources
- **Resources**: Read live resources from connected MCP servers (URI-backed)
- **Prompts**: Execute MCP prompt templates with arguments and inject results

## Configuration

Context sources can be configured in **Settings → Context Sources**.

SwarmOS supports **global configuration + per-project overrides**:
- Global config: `~/.swarmos/context_config.json`
- Project config (optional): `<workspaceRoot>/.swarm/context_config.json`

SwarmOS loads both, merges them, and uses the merged result.

### Merge / Override Semantics

- The merged config uses:
  - **Project defaults** overriding global defaults (when set)
  - **Project sources** overriding global sources by `id` (when the same `id` exists)
  - Project sources can also add new sources that do not exist globally

This makes it easy to keep a personal global baseline while tailoring context to a repo.

### Config Schema (`sources[]`)

Config is stored as:
- `defaults`: shared defaults applied when a source uses `inherit`
- `sources`: a unified list of built-in and MCP sources

Example:

```json
{
  "defaults": {
    "refresh_mode": "every_message",
    "ttl_seconds": 60,
    "cache_policy": "inherit",
    "mcp_timeout_ms": 2000,
    "mcp_concurrency": 4
  },
  "sources": [
    {"id": "project_swarm_md", "enabled": true, "refresh_mode": "on_change", "cache_policy": "cached"},
    {"id": "git_status", "enabled": true, "refresh_mode": "ttl", "ttl_seconds": 10, "cache_policy": "ephemeral"},
    {"id": "mcp_0123456789abcdef01234567", "kind": "mcp_prompt", "server_name": "jira", "prompt_name": "ticket_context",
     "prompt_args": {"key": "PROJ-123"}, "enabled": true, "refresh_mode": "ttl", "ttl_seconds": 60, "cache_policy": "ephemeral"}
  ]
}
```

Notes:
- Built-in sources are identified by stable `id` values (see below); their paths are derived (you do not configure a file path).
- MCP sources are user-defined and include `server_name` plus `uri` (resource) or `prompt_name` + `prompt_args` (prompt).

### Built-in Source IDs

These IDs are stable and can be overridden per-project:
- `global_claude_md`, `global_swarm_md`
- `project_claude_md`, `project_swarm_md`, `agents_md`
- `git_status`, `project_name`, `current_date`

### Refresh Modes (per-source)

Each source has `refresh_mode`:
- `inherit`: use `defaults.refresh_mode`
- `every_message`: refresh once per user message and reuse across tool turns (grouped by `context_run_id`)
- `every_turn`: refresh on every model call (including tool loops)
- `ttl`: refresh when cached source content is older than `ttl_seconds` (per-source or `defaults.ttl_seconds`)
- `on_change`: **files only**. SwarmOS checks file stat + hashes on change; if the file disappears, its content is dropped immediately.

### Cache Policy and Prompt Caching

Each source has `cache_policy`:
- `inherit`: use `defaults.cache_policy`
- `cached`: inject into `<swarmos_cached_context>` (intended to remain stable and cacheable)
- `ephemeral`: inject into `<swarmos_context>` (expected to change frequently)

**Anthropic prompt caching:** SwarmOS sends the system prompt to Anthropic as multiple system blocks:
- Base system prompt (cache-controlled when enabled)
- Cached context block (cache-controlled when enabled)
- Ephemeral context block (not cache-controlled)

This means stable file guidance can benefit from prompt caching even when volatile sources refresh frequently.

## Context Format

Injected context appears like this (cached + ephemeral shown):

```xml
<swarmos_cached_context>
As you answer the user's questions, you can use the following context:
<context name="claudeMd">…</context>
</swarmos_cached_context>

<swarmos_context>
As you answer the user's questions, you can use the following context:
<context name="gitStatus">…</context>
</swarmos_context>
```

The model can reference this context while answering and while running tool loops.
