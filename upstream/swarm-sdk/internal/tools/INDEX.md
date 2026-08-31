# INDEX.md — tools

> Modo: incremental | Directorios indexados: 25
> Para regenerar: `idx generate --path ./tools`

---

## Scope

### `tools/` — SimpleRegistry and BaseTool implementations for the tool system


---

## Árbol de Estructura

### `tools/` — SimpleRegistry and BaseTool implementations for the tool system
Includes ChangeTracker for recording tool configuration changes and an interactive permission checker.
- `registry_impl.go` — SimpleRegistry implementing Registry and ObservableRegistry interfaces
- `history.go` — ChangeTracker and TrackedChange for tracking tool mutations
- `interactive_permission_checker.go` — Interactive permission checking for tool execution
**Entry:** `registry_impl.go:L1`

### `tools/advanced/` — `ToolBuilder` fluent API for constructing tools with advanced metadata
Produces a `BuiltTool` implementing `tools.Tool` plus optional `Deferrable`, `tools.ToolWithExamples`, `tools.ParallelCapable`, and `tools.MetadataProvider` interfaces.
- `builder.go` — `ToolBuilder` type and fluent construction methods
- `state.go` — internal state management for built tools
- `deferred.go` — deferred execution support
**Entry:** `builder.go:L1`

### `tools/anthropic_web_search` — MCPServer providing Anthropic's server-side web search tool integration via the web-search beta flag.
Wraps the main Anthropic Chat provider with web search enabled, using OAuth tokens and automatic refresh.
- `tool.go` — MCPServer definition, NewMCPServer constructor, and Start/Stop lifecycle
- `web_search.go` — SearchRequest struct and web search request/response parsing
- `stats.go` — Rate limiting and usage tracking
**Entry:** `tool.go:L1`

### `tools/browser/` — Registry for managing browser process lifecycles via `Registry` and `ProcessInfo`
Provides a global `Registry` to register, track, and clean up browser processes (`ProcessInfo`) by session, with helpers for signal handling and cancellation.
- `registry.go` — Process registry with registration, lookup, and cleanup operations
**Entry:** `registry.go:L1`

### `tools/builtin/` — Builtin tool implementations including `VaultExecParams` and `AgentBrowserTool`
Provides concrete tool implementations such as the vault execution tool and agent browser tool, along with a sleep-blocker filter for agent events.
- `vault_exec.go` — Vault credential execution tool with `VaultExecParams`
- `subagent.go` — Agent browser tool (`AgentBrowserTool`) and related params
- `delegate_task.go` — Task delegation logic
**Entry:** `vault_exec.go:L1`

### `tools/codemode/` — Package providing `CodeMode` execution, wrapping multiple tools into a single `run_code` tool for JavaScript-orchestrated tool calls.
Uses a `Selector` interface to determine which tools are sandboxed inside the JavaScript runtime versus exposed as normal tools.
- `codemode.go` — Core `CodeMode` type, constructors, and registry functions
- `selector.go` — `Selector` interface for tool sandbox matching
- `trace.go` — Tracing support for code-mode execution
**Entry:** `codemode.go:L1`

### `tools/deepwiki/` — Wiki generation pipeline with CompilerAgent, EmbedderAgent, and PipelineAgent
Orchestrates LLM-driven wiki compilation, embedding, and page regeneration using configurable agents.
- `engine.go` — Agent construction, pipeline orchestration, and option functions
- `types.go` — Request/result types (GenerateWikiRequest, CompilerProgress, EmbedderResult, etc.)
- `llm.go` — LLM HTTP client and streaming response handling
**Entry:** `engine.go:L1`

### `tools/forge` — Package forge implements multi-language symbol indexing tools including call hierarchy, completion, and signature help.
Exports `Complete`, `CallHierarchyItem`, `SnapshotContext`, and `IndexDaemonHandler` as core symbols for language-aware tooling.
- `tools.go` — main tool implementations and package entry point
- `go_symbol_index.go` — Go-specific symbol indexing
- `type_bootstrap.go` — type bootstrapping utilities
**Entry:** `tools.go:L1`

### `tools/ii/` — Provides the `ApplyPatchTool` and `Parser` for parsing and applying unified diff patches.
- `shared_state.go` — Shared state and todo list management for productivity tools
- `browser_manager.go` — Browser manager functionality
- `browser_input.go` — Browser input handling
**Entry:** `shared_state.go:L1`

### `tools/mcp/` — Adapters wrapping MCP server tools and resources as `tools.Tool` implementations.
- `adapter.go` — defines `MCPToolAdapter`, `MCPResourceWrapper`, and `MCPPromptAdapter` adapting MCP primitives to the SDK tool interface
- `client.go` — MCP client for communicating with MCP servers
- `types.go` — MCP protocol type definitions
**Entry:** `adapter.go:L1`

### `tools/migrate_conversations/` — One-shot CLI that migrates legacy flat conversation files into a workspace-partitioned layout using `DirectoryFileStorage`.
Idempotent and safe to run multiple times.
- `main.go` — CLI entry point that triggers the storage migration
**Entry:** `main.go:L1`

### `tools/projectmemory/` — Project memory system backed by a SQLite database
Exports `Database`, `ViewProjectContext`, `UpdateProjectContext`, and tool functions for managing persistent project context entries.
- `tools.go` — tool registration and context view/update handlers
- `database.go` — SQLite-backed `Database` type with CRUD operations
**Entry:** `tools.go:L1`

### `tools/rendering/` — Renderable types for ANSI, HTML, and plain-text output formats
Provides `ANSIRenderable`, `PlainTextRenderable`, and `RenderableHTML` structs along with factory functions like `CreateRenderable` and `AutoDetectAndRender` for content-type detection and rendering.
- `types.go` — Core interfaces, shared types, and factory functions
- `ansi.go` — ANSI escape-code–aware rendering via `ANSIRenderable`
- `html.go` — HTML rendering via `RenderableHTML`
**Entry:** `types.go:L1`

### `tools/skilltools/` — Provides the `SkillTool` tool and `FormatSkillsWithinBudget` function for LLM skill invocation
`NewSkillTool` constructs the tool, which resolves skills from the registry and applies argument and variable substitution before returning content.
- `skill_tool.go` — Defines `SkillTool` struct and its tool interface methods
- `prompt.go` — Implements `FormatSkillsWithinBudget`
**Entry:** `skill_tool.go:L1`

### `tools/steeringtools` — Steering tools for influencing agent behavior via system prompt injection, user interaction, and tool blocking
- `inject.go` — InjectSystemNoteTool prepends steering notes into an agent's system prompt
- `ask_user.go` — AskUserTool prompts the user for input with sync/fallback support
- `block.go` — BlockNextToolTool blocks the next tool invocation
**Entry:** `inject.go:L1`

### `tools/swarm/` — Provides `NewSwarmTool` for creating Agent-to-Agent swarm management tools
Supports operations like create, join, list, send, broadcast, status, and sync across swarm peers.
- `swarm.go` — Defines `SwarmTool`, `SwarmParams`, `SwarmCommunicator` interface, and peer types
**Entry:** `swarm.go:L1`

#### `tools/swarmtools/` — Package swarmtools exports func FuzzParseLineRef, func FuzzHashFileContent, func FuzzLineHash.

### `tools/web_fetch/` — `Tool` type for fetching web pages and converting HTML to markdown
Implements the tool contract with `New` constructor, HTTPS upgrade, same-origin redirect handling, a 15-minute LRU cache, and 100k-character truncation.
- `web_fetch.go` — core `Tool` struct, `New`, `Execute`, and redirect/HTTPS logic
- `cache.go` — LRU content cache with 50 MB cap
- `html_to_markdown.go` — HTML-to-markdown conversion
**Entry:** `web_fetch.go:L1`

### `tools/websearch` — Package websearch provides a unified web search tool supporting Anthropic and Exa backends.
The primary API surface includes `GetWebSearchTools`, `GetWebSearchToolsWithConfig`, and auth-detection helpers like `IsAuthConfigured`.
- `tool.go` — Package documentation, tool construction, and backend selection
- `web_search.go` — Web search tool implementations and configuration
- `stats.go` — Search statistics and observability helpers
**Entry:** `tool.go:L1`

### `tools/codemode/sandbox` — GojaSandbox provides a JavaScript execution environment using the goja runtime with promise/async-await support and REPL semantics
- `goja.go` — GojaSandbox implementation with event loop and persistent global state
- `interface.go` — Sandbox interface definition
**Entry:** `goja.go:L1`

### `tools/forge/indexd/` — Client-server index daemon communicating over Unix domain sockets
Exports `Server` for running the daemon and client functions `Connect`, `Spawn`, `Query`, `IsAvailable` for interacting with it.
- `server.go` — Server type and daemon lifecycle (NewServer, Run)
- `client.go` — Client functions (Connect, Spawn, Query, IsAvailable)
- `protocol.go` — Request/Response types and SocketPath utility
**Entry:** `server.go:L1`

### `tools/mcp/oauth/` — Provides OAuth callback handling via `CallbackServer` and `SingletonCallbackServer`, with persistent token storage through `TokenStorage`.
The `RunCallbackFlow` function orchestrates the full OAuth authorization code flow, while `SingletonCallbackServer` allows multiple concurrent auth requests to share a single callback listener.
- `flow.go` — OAuth callback server implementation and authorization flow orchestration
- `storage.go` — `TokenStorage` interface for persisting and loading OAuth tokens
- `types.go` — Shared OAuth data types and configuration structs
**Entry:** `flow.go:L1`

### `tools/swarmtools/scope/` — Scope detection package providing language-aware structural hashing via `BraceDetector` and `CDetector`
The package detects structural scopes (functions, types, etc.) so line hashes remain stable across insertions and deletions elsewhere in the file.
- `detector.go` — core scope detection interface and language registration
- `brace_base.go` — brace-based scope detectors for C-style languages
- `cache.go` — file hash caching with `FileHashCache` for scope lookups
**Entry:** `detector.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/skilltools/skill_tool.go`
- `swarm-sdk/tools/skilltools/skill_tool_test.go`
- `swarm-sdk/tools/skilltools/prompt.go`
- `swarm-sdk/tools/steeringtools/wired_test.go`
- `swarm-sdk/tools/builtin/delegate_task.go`
- `swarm-sdk/tools/builtin/delegate_task_id_test.go`
- `swarm-sdk/tools/steeringtools/ask_user.go`
- `swarm-sdk/tools/steeringtools/ask_user_phase4_test.go`
- `swarm-sdk/tools/steeringtools/block.go`
- `swarm-sdk/tools/steeringtools/halt_peer.go`
