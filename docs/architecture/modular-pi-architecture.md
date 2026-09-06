# Modular Pi architecture for Swarm

This document turns the Swarm SDK/TUI source snapshot in `upstream/swarm-sdk`
into a staged Pi extension architecture. It is a design boundary document, not
yet an implementation.

## Research conclusions

Official Pi documentation describes a deliberately small core. Workflow-specific
behavior belongs in extensions, skills, prompt templates, or external tools:

- Extensions can subscribe to lifecycle events, register tools and commands,
  persist custom session entries, and provide custom rendering:
  <https://pi.dev/docs/latest/extensions>
- Pi supports session persistence, exact session selection, tool allowlists,
  extension loading, and project trust:
  <https://pi.dev/docs/latest/usage>
- RPC mode exposes JSON commands/events over stdin/stdout for embedding:
  <https://pi.dev/docs/latest/rpc>
- Pi does not provide a built-in permission system; sandboxing belongs in the
  process/container or a routing extension:
  <https://pi.dev/docs/latest/containerization>

The local Pi snapshot confirms these contracts:

- `upstream/pi-mono/packages/coding-agent/src/core/extensions/types.ts:431–490`
  defines typed tool schemas, execution, progress, and `renderCall` /
  `renderResult`.
- `.../types.ts:1118–1253` defines the ExtensionAPI lifecycle, registration,
  messaging, persistence, and active-tool controls.
- `.../core/agent-session.ts:390–435` installs `tool_call` and `tool_result`
  interception on the agent.
- `.../core/agent-session.ts:1088–1120` applies `before_agent_start` prompt and
  message modifications.
- `.../core/extensions/loader.ts:180–305` shows registrations delegating into
  the active runtime and stale-context invalidation.
- `.../core/sdk.ts` creates the session, resource loader, model runtime,
  session manager, and active-tool policy.

## Target architecture

```text
Pi AgentSession / SessionManager
             │
             ▼
    swarm-runtime-contract
             │
   ┌─────────┼─────────┬──────────┬──────────┐
   ▼         ▼         ▼          ▼          ▼
 prompt   policy     tools    persistence  transport
   │         │         │          │          │
   └─────────┴─────────┴──────────┴──────────┘
             │
             ▼
       Pi ExtensionAPI
             │
   ┌─────────┼──────────┬──────────┐
   ▼         ▼          ▼          ▼
 commands  widgets   renderers  RPC bridge
```

The `swarm-runtime-contract` layer is the key seam. It owns stable IDs,
normalized tool outcomes, prompt provenance, capability policy, and lifecycle
state. Individual plugins call this layer; they do not independently recreate
policy or infer state from rendered text.

## Package boundaries

### `swarm-core`

Owns the shared runtime object and lifecycle state:

- workspace and project identity;
- session/conversation identity;
- immutable parity configuration;
- event journal and stable event IDs;
- redacted diagnostics;
- cancellation and stale-session handling.

Registers `session_start`, `session_shutdown`, `agent_start`, `agent_end`,
`turn_start`, and `turn_end`. It is the only package allowed to own global
Swarm state.

### `swarm-prompt`

Owns Forge prompt assembly:

1. load the canonical Forge prompt;
2. resolve workspace context;
3. apply explicit user prompt configuration;
4. apply selected tool snippets/guidelines;
5. inject context files only when policy allows;
6. record non-secret provenance and a prompt hash.

Uses `before_agent_start`. It must treat `event.systemPrompt` as chained state,
not replace another extension’s changes blindly. Prompt content and context
files are sensitive and must not enter command metadata, logs, or diagnostics.

### `swarm-policy`

Owns the closed harness posture and approval rules:

- exact active-tool allowlist;
- mutation/read-only classification;
- workspace boundary checks;
- credential/network restrictions;
- never-default capability acknowledgement;
- yolo as a separate host-level opt-in;
- hook timeout and failure behavior.

Uses `tool_call` for the final fail-closed gate. Since Pi’s `tool_call` mutation
is not revalidated automatically, policy must either normalize arguments before
returning or avoid mutation and block malformed calls. All other plugins
delegate classification to this package.

Pi’s default process permissions are broader than Swarm’s harness posture.
Strong enforcement therefore requires a sandbox/routing adapter, not only a
prompt instruction or UI confirmation.

### `swarm-tools`

Reuses Pi's native filesystem, search, edit, and shell tools. Do not register a
parallel `forge_*` catalog: duplicate read/write/grep/bash tools clutter the
model-facing surface, drift from Pi's maintained contracts, and create a second
authorization path.

Swarm-specific tools remain appropriate only where Pi has no native equivalent,
including task management, history/conversation access, subagent operations,
and optional MCP/vault capabilities. Their definitions use typed schemas,
progress updates, and custom rendering, and activation remains controlled by
`pi.setActiveTools`.

Tool registration is not authorization. `swarm-policy` remains the mandatory
pre-execution gate.

### `swarm-hooks`

Ports the Swarm hook manager and builtin governance hooks:

| Swarm lifecycle | Pi event |
|---|---|
| session start/end | `session_start`, `session_shutdown` |
| user prompt submit | `before_agent_start` |
| provider request | `before_provider_request`, `after_provider_response` |
| pre-tool gate | `tool_call` |
| tool outcome | `tool_result`, `tool_execution_end` |
| streaming activity | `message_update`, `tool_execution_update` |
| turn accounting | `turn_start`, `turn_end` |
| compaction | `session_before_compact`, `session_compact` |
| context mutation | `context` |

Hook handlers must be registered centrally and ordered deterministically. A
hook must not assume assistant prose is present in `tool_call`; use
`before_agent_start`, message events, or persisted transcript state.

### `swarm-persistence`

Uses Pi’s `SessionManager` and `appendEntry` for durable state:

- `swarm_identity`;
- `swarm_policy`;
- `swarm_prompt_provenance`;
- `swarm_tool_event`;
- `swarm_agent_binding`;
- `swarm_compaction`;
- `swarm_workflow`.

The extension must rehydrate state in `session_start` and discard captured
runtime/context objects after session replacement or reload. Pi’s loader marks
stale extension contexts; a captured old `SessionManager` must never be reused.

### `swarm-compaction`

Owns the Swarm distinction between pressure observation and compaction
execution. It listens to context/turn events, invokes Pi compaction through the
documented session controls, records the compaction entry, and resets live
token counters after successful compaction.

It must not trigger compaction merely because a pressure event was observed.
The next query boundary and in-agent continuation rules must be explicit.

### `swarm-subagents`

Provides `Task`/delegate tools and background execution. Every child receives:

- deterministic agent ID;
- explicit parent session ID;
- workspace binding;
- exact tool/policy profile;
- cancellation signal;
- bounded result preview plus durable full result reference.

Child completion is delivered as a custom persisted message and an event, not
only as a UI notification.

Pi does not include built-in subagents, so this package requires either
`pi.exec`/child processes or direct `AgentSession` construction.

### `swarm-workflows`

Ports declarative profiles, fallback, schedules, and workflows only after the
core turn contract is stable. Compilation/validation must be side-effect free;
runtime construction and scheduling are separate phases.

Use commands and session events for local workflows. Long-lived schedules and
cross-process execution belong in an external daemon/bridge.

### `swarm-render`

Ports semantic presentation only:

- Forge read/write/edit/patch/bash result renderers;
- collapsed/expanded result state;
- subagent progress;
- policy/approval status;
- footer/status/widget indicators;
- custom messages for background completions.

Pi supports tool-local `renderCall` and `renderResult`, custom message
renderers, widgets, status, and full custom UI. Do not port Bubble Tea frame
cache internals, raw coordinate arithmetic, or native Kitty transport until a
visual parity test demonstrates a need.

### `swarm-bridge`

Owns features outside the Pi process:

- Swarm serve RPC/SSE/WebSocket;
- ACP;
- A2A peer discovery and messaging;
- attach/control sockets;
- global daemon lifecycle.

The bridge communicates through stable session/agent IDs and normalized events.
It must not reach into renderer state or infer the active conversation from the
most recent global record.

## Event and data invariants

1. Tool registration, active exposure, authorization, invocation, outcome, and
   rendering remain separate states.
2. Every tool call/result is correlated by a stable `toolCallId`.
3. Every session replacement invalidates old extension contexts and rehydrates
   state through the new `session_start`.
4. Prompt provenance is hashed/redacted; raw secrets never enter explain output.
5. Hook failure behavior is explicit per policy: block, observe, or fail-open
   only where the dependency is unavailable and the policy permits it.
6. Sibling tool calls cannot assume sibling results are visible; default
   parallelism must be treated as concurrent.
7. The active conversation ID is assigned before the first turn is executed.
8. Compaction resets live counters and emits a durable compaction record.
9. Machine/RPC output is isolated from human UI and diagnostics.
10. UI state is derived from events and may lag; it is never the source of truth.

## Commit-sized implementation sequence

1. `swarm-core` contracts, event IDs, identity, and session rehydration tests.
2. `swarm-prompt` Forge prompt, workspace context, provenance, and precedence
   tests.
3. `swarm-policy` closed-mode allowlist and dangerous-tool blocking tests.
4. `swarm-tools` read-only Forge tools with Pi schemas and result renderers.
5. `swarm-hooks` lifecycle registry and ordering/failure tests.
6. `swarm-persistence` custom entries, resume, fork, reload, and stale-context
   tests.
7. `swarm-compaction` pressure/execute/reset tests.
8. `swarm-subagents` deterministic child identity and cancellation tests.
9. `swarm-render` visual fixtures and custom widgets.
10. `swarm-workflows` and then `swarm-bridge`.

Each step should be one or more surgical commits with focused checks. The
upstream snapshot commit is intentionally separate from these implementation
commits.

## Initial acceptance matrix

| Area | Required proof |
|---|---|
| Prompt | Forge prompt hash/provenance and workspace facts match expected output |
| Closed mode | undeclared tool/context/skill/provider cannot reach the model |
| Policy | dangerous mutation is blocked; harmless read remains allowed |
| Hooking | before/after lifecycle order and timeout posture are deterministic |
| Persistence | resume/fork/reload preserve exact IDs and rehydrate state |
| Streaming | partial text/tool progress/result reconcile by stable IDs |
| Compaction | observation and execution are separate; counters reset |
| Rendering | tool call/result renderers handle partial and expanded states |
| Subagents | parent/child binding, cancellation, preview, and full-result path |
| RPC | stdout remains machine-readable and diagnostics stay separate |
| Sandbox | filesystem/process/network boundary is enforced outside prompts |

