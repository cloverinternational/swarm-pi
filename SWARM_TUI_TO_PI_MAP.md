# Swarm Forge/TUI → Pi plugin map

Status: architecture map only. No Pi implementation exists in this repository
yet. The canonical Swarm implementation is the clean `main` worktree at
`/home/swarm/Work/mono/swarm-sdk`; the Pi implementation reference is
`/home/swarm/Work/buzz/pi-mono`. The active destination
`/home/swarm/Work/Pi-Swarm` started with only `.swarm/project_memory.db`.

## 1. The complete runtime in one pass

```text
argv / project files
  ├─ swarm                         interactive TUI
  ├─ swarm -p / swarm run          one-shot headless agent
  └─ swarm -p --harness PATH       closed harness execution
        │
        ▼
config migration + provider/model resolution + workspace binding
        │
        ├─ ambient TUI construction
        │    └─ chat.NewAsyncAppWithOptions
        │          └─ first Bubble Tea frame
        │                └─ App.Init → newAppWithOptions
        │                      ├─ SDK/provider/agent
        │                      ├─ tool registry + permissions
        │                      ├─ context/memory/skills
        │                      ├─ hooks + MCP + subagents
        │                      └─ conversation/settings
        │
        └─ closed harness construction
             └─ harness.Compile
                   └─ immutable Plan (digest/provenance/diagnostics)
                         └─ client.New(WithHarnessPlan(...))
                               └─ exactly declared runtime

user prompt
  └─ normal send path / ChatCtx
       └─ provider stream
            ├─ assistant text/thinking
            ├─ tool call
            │    ├─ permission check
            │    ├─ before-tool hooks
            │    ├─ tool execution/progress
            │    └─ after-tool hooks
            ├─ tool result → next model request
            └─ turn end / compaction / persistence
                  │
                  ├─ SDK update subscribers
                  ├─ TUI updateQueue → Bubble Tea Update → View
                  ├─ headless printer → text/json/stream-json
                  └─ serve/SSE/WebSocket/ACP/attach observers
```

The interactive entrypoint, subcommand dispatch, headless dispatch, harness
dispatch, control socket, native serve lifecycle, and Bubble Tea program are
all in [`cmd/swarmos/main.go`](/home/swarm/Work/mono/swarm-sdk/swarm-tui/cmd/swarmos/main.go:308).
The same file makes `swarm run <prompt>` and `swarm -p <prompt>` converge on
`dispatchHeadlessExecution` (roughly lines 642–710 and 1124–1680), while
`runTUI` owns the interactive process (roughly lines 1695–2070).

## 2. Ownership layers

| Layer | Swarm source of truth | Responsibility | Pi destination |
|---|---|---|---|
| CLI/process | `swarm-tui/cmd/swarmos` | argv, mode selection, startup, shutdown, daemon/attach | Pi extension commands plus a thin launcher only where Pi cannot own the process |
| Prompt/config | `swarm-tui/internal/chat/settings`, `internal/systemprompt`, `harness` | prompt precedence, context, provider/model, plan compilation | `before_agent_start`, `systemPromptOptions`, project `.pi` resources |
| Agent loop | `internal/agent`, `client` | provider request, stream normalization, tool loop, cancellation, usage | Pi core agent loop; plugin observes and modifies at documented events |
| Capabilities | `internal/tools`, `internal/tools/forge`, `internal/mcp` | registration, exact selection, schemas, permission requirements | `pi.registerTool`, active-tool filtering, external command/MCP adapters |
| Governance | `internal/hooks`, `internal/hooks/builtin` | lifecycle gates, nudges, audits, task/plan policy, hook execution | `pi.on(...)`, primarily `before_agent_start`, `tool_call`, `tool_result`, `context`, `turn_end` |
| Semantic state | `internal/conversation`, client session state, task stores | messages, tool calls/results, active conversation, durable identity | Pi session entries plus extension state; never infer identity from “latest” |
| Presentation | `swarm-tui/internal/chat` | Bubble Tea MVU, viewport, modals, tool renderers, sidebars, images | Pi TUI renderCall/renderResult, widgets, commands, status/footer |
| Remote control | `serve`, A2A, attach/automation, ACP | live session observation/control and peer identity | explicit external adapter; no direct Bubble Tea equivalent |

The important boundary is semantic state versus presentation. The TUI is an
observer/reducer of SDK events; tool correctness, authorization, persistence,
and cancellation must not depend on whether a frame rendered.

## 3. Startup and prompt contract

### Interactive path

1. `main` migrates legacy data, initializes telemetry/error reporting, handles
   subcommands, validates incompatible flags, and calls `runTUI`
   (`cmd/swarmos/main.go:308–622`).
2. `runTUI` resolves workspace/project roots, session identity, peer handle,
   updater, image manager, and `chat.AppOptions`; it creates
   `chat.NewAsyncAppWithOptions` and a Bubble Tea program before expensive SDK
   initialization (`main.go:1695–1955`).
3. `NewAsyncAppWithOptions` creates a bootstrap shell and stores
   `InitialPrompt`, `ResumeConversationID`, and optional `HarnessPath`
   (`internal/chat/app_init.go:75–127`; `internal/chat/app.go:44–102`).
4. `App.Init` batches terminal probes, intro/bootstrap work, and
   `buildRuntimeCmd`; the full `newAppWithOptions` construction happens behind
   the first visible frame (`internal/chat/app_update.go:17–102`).
5. `newAppWithOptions` loads settings/provider catalogs, resolves the workspace
   binding, creates SDK integration, restores conversation/settings, wires
   callbacks, registers renderers, and defers enabled custom hooks until the
   event loop is running (`internal/chat/app_init.go:141–220` and
   `840–930`).
6. If a harness is selected, `harnessPlanForAppOptions` compiles it and passes
   it into `NewSDKIntegrationWithOptions`; otherwise normal ambient TUI
   construction is used (`internal/chat/app_harness.go:1–35`).
7. Initial prompt delivery is exactly-once: `takeInitialPrompt` clears the
   stored value, `consumeInitialPrompt` rejects repeats/empty values, and
   `handleInitialPrompt` starts a fresh chat then calls the ordinary send path
   (`internal/chat/app_initial_prompt.go:1–53`).

### Effective prompt precedence

The Forge prompt is documented as `forgeSwarmSystemPrompt` in
`internal/chat/settings/system_prompt.go` (documented at
`swarm-tui/docs/FORGE_SWARM_SYSTEM_PROMPT.md:7–19`). Runtime workspace facts
are prepended by `RenderWorkspaceContext`. Non-harness TUI settings can replace
or augment the prompt and inject `CLAUDE.md`, `SWARM.md`, `AGENTS.md`, and
related context; harness mode deliberately blocks that ambient path.

Observed prompt decisions in `internal/chat/app_init.go:1850–1895`:

- Codex-backed providers merge canonical provider instructions with the user
  prompt.
- Other providers set the active prompt and then inject configured context.
- Harness-governed sessions do not accept the normal settings override.
- OAuth providers add provider-specific canonical prefixes.

### Closed harness contract

`harness.Document` is a strict v1 document with typed runtime/provider/agent/
permissions/interfaces plus optional skills, hooks, MCP, profiles, fallback,
agents, budgets, schedules, and workflows
(`harness/types.go:1–180`). `harness.Compile` reads and validates without
runtime side effects, resolves manifest-relative paths, redacts secrets from
the immutable `Plan`, and produces a digest/provenance surface
(`harness/plan.go:20–220`).

`client.WithHarnessPlan` switches `client.New` to a closed construction:
declared provider, credential, prompt, workspace, limits, permissions, and
exact tools only; no ambient config, default tools, hooks, skills, MCP
discovery, plugins, or implicit fallback (`client/harness_option.go:39–83`).
`WithHarnessAllowYolo` is a separate host gesture; a manifest cannot authorize
itself (`harness_option.go:84–105`).

Pi equivalent: a project-local extension should load one canonical prompt
definition and use `before_agent_start` to enforce the effective prompt. A
closed Pi profile needs an explicit allowlist and startup validation; do not
silently inherit Pi’s ambient tools/skills/context when parity mode is active.

## 4. The turn, tools, hooks, and permissions

### Swarm execution

`app_messaging.go:337–746` is the TUI send bridge. It creates/chooses the
active conversation, starts loading indicators, subscribes to SDK intermediate
updates (including updates that may have happened before subscription), invokes
the agent, and queues updates for the event loop. `internal/agent` owns the
actual provider/tool loop. Tool execution calls
`EmitToolBeforeExecute` and `EmitToolAfterExecute` in
`internal/agent/agent_tools.go:215–361` and `:504–631`.

The tool contract is layered:

1. catalog registration and schema;
2. active-tool filtering;
3. reachability/provider exposure;
4. permission requirements;
5. invocation;
6. outcome/error conversion;
7. rendering and persistence.

Do not collapse these into one “tool exists” boolean.

Forge tools are under `internal/tools/forge` and include filesystem read/write/
patch behavior. Other relevant builtins include Bash/background processes,
todo/task management, web search, history, subagents, MCP, vault, and
debug/logging. The TUI’s display registry is separate from execution:
`app_init.go:899–911` registers specialized renderers for bash/read/write/edit/
patch/grep/history/websearch/todo/subagent/read-background plus a generic
fallback.

Permissions are explicit and tool-specific (`internal/tools/tool.go:80–86`,
`permission_engine.go`, `permission_context.go`). Harness permissions include
approval mode, workspace boundary, mutation allowance, and per-capability
never-default acknowledgements (`harness/types.go:121–170`).

### Hook lifecycle

The unified hook mapper is `internal/hooks/event_mapper.go:18–60`; the manager
and shell execution path are `internal/hooks/manager.go`, `executor.go`, and
`integration.go`. Config hierarchy is merged in
`internal/hooks/config_hierarchy.go:80–100`. The TUI loads project/user/system
hooks and registers enabled custom hooks after startup
(`internal/chat/app_update.go:136–170`; headless registration is
`cmd/swarmos/main.go:1320–1375`).

The event payload limitation matters for a faithful port: Swarm PreToolUse
contains tool call ID/name/input/params, and PostToolUse adds output/result/
error/outcome, but neither carries assistant prose, thinking, model name, or
turn ordinal. A Pi plugin must use session/transcript state or the appropriate
agent events for prose-aware policies.

### Pi event mapping

Pi’s documented lifecycle is in
`packages/coding-agent/docs/extensions.md:280–330`:

| Swarm concern | Pi primitive | Notes |
|---|---|---|
| prompt submit / prompt rewrite | `before_agent_start` | Can inspect chained system prompt/options and inject a stored message |
| session start/end | `session_start`, `session_shutdown`, `agent_start`, `agent_end` | Use session reason/identity, not process-local guesses |
| provider request/context | `context`, `before_provider_request`, `after_provider_response` | Preserve closed prompt/tool policy |
| pre-tool gate | `tool_call` | Can mutate input or return `{block:true, reason}` |
| tool progress/result | `tool_execution_update`, `tool_result`, `tool_execution_end` | Pi tool result is mutable at the result event |
| compaction | `session_before_compact`, `session_compact` | Map policy and reset token telemetry after compaction |
| turn accounting | `turn_start`, `turn_end` | Re-anchor nudges/budgets to lifecycle events |
| tool registration | `pi.registerTool` | Dynamic registration is supported and immediately visible |
| commands | `pi.registerCommand` | Use for settings, plan mode, attach/status, and parity controls |

Pi documents `tool_call` ordering and mutation guarantees at
`extensions.md:694–735`: sibling tool calls may be preflighted sequentially
and executed concurrently, and sibling results are not guaranteed to be in
session state. Any Swarm policy that depends on order must explicitly
serialize or use stable IDs.

## 5. TUI state and rendering

`chat.App` is a large stateful MVU model defined in
`internal/chat/app_types.go:455–953`. It contains:

- semantic messages with text, thinking, tool calls/results, images, sections,
  and structured activity (`:73–221`);
- conversation/workspace/screen/pane state (`:297–415`);
- approval queues, active tool activity, subagent progress, voice, A2A,
  background process state, settings, and modal state;
- render caches, pre-rendered width-specific lines, image anchors, and
  viewport cache state (`:1121–1218`).

Runtime callbacks do not mutate Bubble Tea state directly. They enqueue
messages through `sendToRuntime`, `wakeRuntimeAsync`, and the bounded
`updateQueue` (`app_messaging.go:819–980`). `Update` reduces stream chunks,
completion, tool activity, approvals, key/mouse commands, and timers
(`app_update_handlers.go:27–252` plus the command-handling files).

`View` composes a cached frame, bootstrap/chat/attach/debug screens, side
panels, autocompletion, modals, approval UI, and image output
(`app_view.go:34–451`). Rendering is not merely string formatting: it includes
ANSI-aware wrapping, stable message/tool IDs, collapsed tool outputs, syntax
highlighting/diffs, viewport anchoring, and native image coordinates.

Pi equivalent:

- semantic message/tool data should remain in session entries or extension
  state;
- `registerTool.renderCall`/`renderResult` handles tool-local presentation
  (`extensions.md:1251–1308`);
- `registerCommand`, widgets, status, footer, and custom UI handle global
  controls;
- do not port Bubble Tea frame caches or internal coordinate tricks unless a
  visual parity requirement proves they are needed.

## 6. Persistence, attach, automation, and machine output

Conversation persistence is shared SDK state, while the TUI adds active
conversation, branch/workspace, and session-launch records. The client’s
`SendMessage` sets the authoritative active conversation before execution and
mints a workspace-scoped conversation if needed
(`client/messaging.go:32–120`). This prevents a first message from being
persisted into another workspace’s latest conversation.

Interactive startup also:

- registers a peer handle and control socket;
- starts a local native `serve.Mux` exposing RPC/SSE/WebSocket;
- optionally ensures the global daemon;
- publishes cleanup atomically with peer presence
(`cmd/swarmos/main.go:1880–2005`).

The automation server is constructed in
`internal/headless/automation/server`; ACP protocol/session/approval are under
`headless/acp` and `headless/approval`. These are runtime control surfaces,
not TUI widgets.

Harness CLI behavior:

- `swarm harness validate PATH` compiles and prints `OK <digest>`;
- `swarm harness explain PATH` prints redacted plan/build identity;
- invalid manifests print structured diagnostics;
- `runHeadlessHarness` compiles, constructs a closed client, subscribes to
  updates, invokes `ChatCtx`, and prints the selected output format
  (`cmd/swarmos/harness_cli.go:1–125`,
  `cmd/swarmos/harness_run.go:1–82`).

Pi mapping:

- session entries are the primary durable transcript;
- extension state must be namespaced and restart-safe;
- use Pi commands for local inspection/control;
- keep remote attach/ACP/A2A in a separate bridge process or adapter;
- machine output must never share stdout with diagnostics or UI banners.

## 7. Proposed Pi plugin set

The following split preserves Swarm ownership boundaries and permits
incremental verification:

```text
.pi/
  extensions/
    swarm-core.ts             prompt/context/identity/policy coordinator
    swarm-tools.ts            Forge filesystem + task/tool registrations
    swarm-hooks.ts            hook lifecycle gates and event translation
    swarm-compaction.ts       micro/auto compaction policy and token reset
    swarm-plan-mode.ts        PLAN/ACT/AUTO and plan approval controls
    swarm-subagents.ts        Task/delegate/background-agent bridge
    swarm-workflows.ts        workflow/profile/schedule adapters
    swarm-settings.ts         model/provider/tool/hook settings commands
    swarm-attach.ts           optional control/serve/ACP bridge client
    swarm-render.ts           tool renderers, status, footer, widgets
    swarm-compat.ts            parity manifest validation and feature flags
  skills/
    ...                       only if the skill is intentionally Pi-loaded
  prompts/
    forge-swarm.md            canonical Forge body, versioned and hashed
```

Registration matrix:

| Plugin | Registers | Main events |
|---|---|---|
| core | commands, identity/state, prompt policy | `session_start`, `before_agent_start`, `agent_end` |
| tools | Forge tools and schemas | `tool_call`, `tool_result` |
| hooks | governance hooks | `before_agent_start`, `tool_call`, `tool_result`, `turn_end` |
| compaction | compact/summarize commands and policy | `session_before_compact`, `session_compact`, `turn_end` |
| plan-mode | mode commands and active-tool sets | `input`, `before_agent_start`, `tool_call` |
| subagents | Task/delegate tools and result messages | `tool_call`, `tool_result`, session events |
| workflows | workflow commands/launch adapters | `session_start`, commands, `turn_end` |
| settings | `/swarm-*` commands and status | commands, model/session events |
| attach | bridge commands only | session events; external transport callbacks |
| render | custom tool renderers/widgets | tool render callbacks and UI APIs |
| compat | closed manifest validation | `session_start`, `before_agent_start` |

## 8. Exact, adapter, and gap classification

### Exact semantic ports

- Forge prompt body and workspace-context policy.
- Tool schemas and tool names where Pi can represent the same operation.
- Before/after tool governance and fail-closed dangerous-command checks.
- Prompt injection/rewriting through `before_agent_start`.
- Initial prompt as a one-shot command/startup input.
- Plan/ACT mode as active-tool and hook policy.
- Tool result previews, progress, and custom rendering where Pi’s API supports
  it.
- Conversation identity and session-local durable state.

### Adapters required

- Swarm harness YAML compiler/digest/provenance → TypeScript manifest loader
  with strict validation and a closed-mode Pi session.
- Go shell hooks → TypeScript event handlers or explicitly supervised child
  processes; preserve timeout, environment, output, and failure semantics.
- MCP and A2A → Pi tools plus external bridge processes.
- Background bash/subagents → `pi.registerTool` + `exec`/child-process bridge
  with stable IDs and completion messages.
- Swarm serve/attach/ACP/control socket → a separate local bridge, never a
  fake TUI widget.
- Bubble Tea viewport/sidebar/modal rendering → Pi widgets/status/footer and
  tool render callbacks.
- SQLite conversation/session stores → Pi session manager plus an extension
  state store where Pi entries cannot carry the required metadata.

### Gaps that must remain explicit

- A global Swarm daemon that survives UI exit.
- Full A2A peer presence and atomic control-socket publication.
- Exact Bubble Tea frame caching, native Kitty image anchoring, and all
  coordinate behavior.
- Go hook manager’s full built-in hook catalog and ordering semantics.
- Exact provider catalog/OAuth/account rotation behavior unless Pi core or a
  provider plugin exposes the same contract.
- Harness runtime bindings for schedules/workflows unless a bridge implements
  them.

## 9. Parity invariants and acceptance tests

1. Prompt: Forge body plus workspace facts are present once; user/system
   overrides follow documented precedence; no secret appears in diagnostics.
2. Closed mode: undeclared ambient tool, skill, hook, MCP server, provider
   fallback, or context file is unavailable.
3. Tool policy: a blocked dangerous call produces no execution; a read-only
   call remains allowed; input mutation is validated before execution.
4. Hook order: before-tool gate precedes invocation, after-tool observes the
   terminal outcome, and hook timeout/failure follows the selected posture.
5. Stream identity: text, thinking, tool call, result, progress, and completion
   reconcile by stable call/message ID; late text cannot overwrite structured
   activity.
6. Initial prompt: startup text is submitted once, and an empty/repeated
   startup event does nothing.
7. Persistence: the first message creates/selects the workspace-scoped
   conversation before execution; restart/resume uses the exact ID.
8. Compaction: pressure observation does not itself trigger an unintended
   turn-end compaction; after compaction token counters re-anchor.
9. Machine output: stream-json contains only the contract stream on stdout;
   diagnostics go to stderr.
10. Concurrency: sibling tool calls are tested with stable IDs and no
    assumptions about sibling result visibility.
11. Near-miss safety tests: explicit `--initial-prompt=<dash-prefixed text>`,
    a harmless read tool, and a non-harness Pi session remain allowed while
    bare ambiguous flags, undeclared mutation, and unknown manifest keys fail.

## 10. Recommended implementation order

1. Port the Forge prompt, workspace context, strict parity manifest, and
   closed-mode validation.
2. Port Forge read/write/patch plus permission and `tool_call` gates.
3. Port task management, plan mode, and structured tool-result persistence.
4. Port compaction and conversation identity tests.
5. Add specialized tool renderers and status/widgets.
6. Add subagent/background-process adapters.
7. Add MCP/workflow/schedule adapters.
8. Add attach/ACP/A2A bridge only after local semantic parity is green.
9. Run a live comparison matrix against Swarm headless `stream-json` and Pi
   session transcripts; treat visual comparison as a separate gate.
