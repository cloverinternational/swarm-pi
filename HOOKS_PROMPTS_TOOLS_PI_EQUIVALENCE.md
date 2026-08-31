# Swarm hooks, prompts, and tools: Pi equivalence matrix

This is the focused porting guide for the three surfaces most likely to reuse
Pi primitives instead of being rebuilt. It is based on the local source mirrors
and official Pi documentation:

- <https://pi.dev/docs/latest/extensions>
- <https://pi.dev/docs/latest/usage>
- <https://pi.dev/docs/latest/containerization>
- <https://pi.dev/docs/latest/rpc>

The local Pi source is pinned in
[`upstream/pi-mono`](/home/swarm/Work/Pi-Swarm/upstream/pi-mono), and the
Swarm source is pinned in
[`upstream/swarm-sdk`](/home/swarm/Work/Pi-Swarm/upstream/swarm-sdk).

## Bottom line

Do not rebuild the event loop, provider stream machinery, session manager, or
tool renderer framework. Pi already has the correct extension seams for these.

Build only the Swarm semantic layer around them:

1. a canonical Forge prompt loader/provenance recorder;
2. one centralized capability/policy classifier;
3. thin tool definitions that delegate to that policy;
4. an ordered hook coordinator;
5. durable Swarm-specific session entries;
6. external bridges for A2A, ACP, daemon, and attach behavior.

## 1. System prompts

### Swarm behavior

Swarm’s Forge prompt is the `forgeSwarmSystemPrompt` documented in
`upstream/swarm-sdk/swarm-tui/docs/FORGE_SWARM_SYSTEM_PROMPT.md:7–19` and
implemented in
`upstream/swarm-sdk/swarm-tui/internal/chat/settings/system_prompt.go`.
The runtime can prepend workspace facts through `RenderWorkspaceContext`.

Non-harness TUI startup can apply an active user prompt, provider-specific
instructions, and context files such as `CLAUDE.md`, `SWARM.md`, and
`AGENTS.md`; see
`upstream/swarm-sdk/swarm-tui/internal/chat/app_init.go:1850–1895`.

Closed harness mode is different. `harness.Compile` resolves and hashes the
prompt into an immutable plan, and `client.WithHarnessPlan` disables ambient
prompt/config/tool/skill/plugin discovery:

- `upstream/swarm-sdk/harness/plan.go:20–220`
- `upstream/swarm-sdk/client/harness_option.go:57–83`

### Pi equivalent

Use one `swarm-prompt.ts` extension:

```ts
pi.on("before_agent_start", async (event, ctx) => {
  const prompt = assembleForgePrompt({
    basePrompt: event.systemPrompt,
    cwd: event.systemPromptOptions.cwd,
    selectedTools: event.systemPromptOptions.selectedTools,
    contextFiles: event.systemPromptOptions.contextFiles,
    skills: event.systemPromptOptions.skills,
  });

  return {
    systemPrompt: prompt.text,
    message: {
      customType: "swarm-prompt-provenance",
      content: prompt.redactedProvenance,
      display: false,
    },
  };
});
```

Pi explicitly exposes the chained `event.systemPrompt` and structured
`systemPromptOptions`; later handlers see earlier prompt changes. This is the
direct replacement for Swarm’s prompt assembly callback.

Use `context` only for message-level transformations. Use
`before_provider_request` only when the provider payload itself must change.
Do not append the Forge prompt in multiple extensions or it will duplicate
instructions and alter cache behavior.

### Prompt parity rules

- Forge body must be loaded once and versioned/hash-recorded.
- Workspace context must be generated from the active `cwd`, not copied from a
  stale session.
- Prompt source, context-file names, selected tools, and hashes may be recorded;
  raw secrets and full sensitive context must not enter diagnostics.
- In closed mode, explicitly disable or filter ambient Pi context/skills/tools.
- A user-selected prompt may override Forge only under the configured posture.
- Every `before_agent_start` handler must preserve prior handlers’ changes.

## 2. Tools

### Swarm behavior

Swarm separates tool registration, active exposure, authorization, invocation,
outcome, rendering, and persistence. The core tool permission interface is
`upstream/swarm-sdk/internal/tools/tool.go:80–86`; the permission engine is
`internal/tools/permission_engine.go:1–90`.

Forge filesystem tools and their registration live under
`upstream/swarm-sdk/internal/tools/forge`. TUI render registration is separate
from execution in
`upstream/swarm-sdk/swarm-tui/internal/chat/app_init.go:899–911`.

### Pi equivalent

Use Pi’s native `ToolDefinition`:

- typed TypeBox `parameters`;
- `prepareArguments` for compatibility normalization;
- `execute(toolCallId, params, signal, onUpdate, ctx)`;
- `onUpdate` for progress;
- `renderCall` and `renderResult` for TUI output.

Evidence:

- local type contract:
  `upstream/pi-mono/packages/coding-agent/src/core/extensions/types.ts:431–490`;
- public registration/runtime methods:
  `.../types.ts:1118–1253`;
- loader implementation:
  `.../core/extensions/loader.ts:180–305`;
- official examples and renderer rules:
  `upstream/pi-mono/packages/coding-agent/docs/extensions.md:1251–1308`,
  `:1703–1710`, and `:1882–1888`.

Recommended tool shape:

```ts
pi.registerTool({
  name: "swarm_forge_read",
  label: "Forge Read",
  description: "Read a bounded file range in the authorized workspace.",
  parameters: Type.Object({
    path: Type.String(),
    offset: Type.Optional(Type.Integer({ minimum: 1 })),
    limit: Type.Optional(Type.Integer({ minimum: 1 })),
  }),
  async execute(toolCallId, params, signal, onUpdate, ctx) {
    const decision = policy.authorize({
      tool: "forge.read",
      input: params,
      cwd: ctx.cwd,
    });
    if (!decision.allowed) {
      throw new Error(decision.reason);
    }
    return forgeRead(params, { signal, onUpdate });
  },
  renderCall(args, theme, context) {
    return renderForgeReadCall(args, theme, context);
  },
  renderResult(result, options, theme, context) {
    return renderForgeReadResult(result, options, theme, context);
  },
});
```

The policy check belongs in a shared module or hook as well as at the tool
boundary for defense in depth. It must use one classifier, not separate
implementations in every tool.

### What Pi already gives us

- Dynamic registration after startup.
- Active-tool allowlists through `getActiveTools`, `getAllTools`, and
  `setActiveTools`.
- Tool-specific prompt snippets and guidelines.
- Tool argument normalization.
- Abort signals and progress updates.
- Built-in renderer inheritance when wrapping a built-in tool.
- Custom call/result components.
- Session-visible custom messages and UI-only persisted entries.

### What still needs Swarm code

- Forge path semantics and bounded reads/writes/patches.
- Swarm capability IDs and never-default acknowledgement rules.
- Workspace-boundary and mutation policy.
- Credential/network/database permission classes.
- Redacted audit events and stable tool outcome taxonomy.
- Background process handles and full-output references.
- MCP naming/allowlist compatibility.

## 3. Hooks

### Swarm behavior

Swarm has a typed event vocabulary beginning with `BeforeAgent`,
`ToolBeforeExecute`, and `ToolAfterExecute`:

- event definitions:
  `upstream/swarm-sdk/internal/hooks/hook_events.go:1–90`;
- registration and policy:
  `.../internal/hooks/manager.go:15–165` and `:235–410`;
- event execution:
  `.../internal/hooks/manager.go:490–530`;
- tool bridge:
  `.../internal/hooks/agentbridge/bridge.go:49–90`;
- tool execution call sites:
  `.../internal/agent/agent_tools.go:215–361` and `:504–631`.

Swarm hook registration supports enable/disable state, per-hook permission
policy, scope, filtering, priority, shell execution, timeouts, and structured
tool outcomes.

### Pi equivalent

| Swarm hook purpose | Pi event | Port shape |
|---|---|---|
| session initialization | `session_start` | Rehydrate policy/state and register dynamic resources |
| user prompt gate/rewrite | `before_agent_start` | Modify chained prompt or inject custom message |
| context transformation | `context` | Modify messages before provider request |
| provider request observation | `before_provider_request` | Audit/normalize request payload |
| provider response observation | `after_provider_response` | Record status/headers/usage metadata |
| pre-tool gate | `tool_call` | Block or mutate `event.input` |
| tool progress | `tool_execution_update` | Update durable/event state |
| post-tool observation/rewrite | `tool_result` | Inspect/modify result before completion |
| turn accounting | `turn_start`, `turn_end` | Budgets, nudge counters, metrics |
| compaction | `session_before_compact`, `session_compact` | Separate decision from execution |
| lifecycle cleanup | `session_shutdown` | Close bridges and discard stale runtime objects |

Pi’s local `ExtensionAPI` event declarations are in
`upstream/pi-mono/packages/coding-agent/src/core/extensions/types.ts:1125–1160`.
Pi’s agent session installs tool interception in
`upstream/pi-mono/packages/coding-agent/src/core/agent-session.ts:390–435`.

### Important Pi semantics

According to the official contract and local implementation:

- `tool_call` runs after `tool_execution_start` but before execution.
- Returning `{ block: true, reason }` blocks the call.
- `event.input` is mutable.
- Later handlers see earlier input mutations.
- Pi does not revalidate after hook mutation.
- In default parallel mode, sibling calls may be preflighted sequentially and
  executed concurrently.
- Sibling tool results are not guaranteed to be visible to a later sibling’s
  `tool_call` handler.
- `tool_result` runs after execution and can modify the result.
- Hook handlers run in extension/load order.

Therefore `swarm-hooks.ts` should be the only extension registering enforcement
handlers. Other plugins may emit facts, but they should not independently block
tools or mutate prompts.

### Hook payload limitation

Swarm’s tool hook payload does not include assistant prose, thinking, model name,
or turn ordinal. Pi’s `tool_call` similarly gives tool identity/input rather
than the complete assistant message. Prose-aware governance must run in
`before_agent_start`, message events, or session/transcript state.

## 4. Authorization: the major non-equivalence

Pi’s extension API is an interception surface, not a complete permission
engine. Official Pi documentation states that Pi runs with the launching
process’s permissions by default and recommends containerization or routing.

Swarm has more structure:

```text
required capabilities
  → permission engine
  → workspace/path/operation context
  → approval posture
  → hook policy
  → tool invocation
  → typed outcome/audit
```

The Pi port must preserve this with two layers:

1. `swarm-policy.ts` performs deterministic classification and `tool_call`
   blocking.
2. A sandbox/routing layer enforces filesystem/process/network boundaries
   outside prompt text and UI confirmation.

For high-risk tools, the extension should route execution to a supervised
process or sandbox. A prompt saying “do not write outside the workspace” is not
an authorization boundary.

## 5. Minimal implementation that reuses Pi

The first vertical slice should contain only:

```text
swarm-prompt.ts
  └─ before_agent_start → Forge prompt + workspace context + provenance

swarm-policy.ts
  └─ shared classifier → allowlist + workspace + mutation decision

swarm-tools.ts
  └─ swarm_forge_read → native Pi ToolDefinition

swarm-hooks.ts
  └─ tool_call/tool_result → central gate + audit event

swarm-state.ts
  └─ appendEntry + session_start rehydration

swarm-render.ts
  └─ renderCall/renderResult + custom entry renderer
```

Do not begin with A2A, the global daemon, full TUI replacement, or all
providers. Those are transport/product surfaces, not prerequisites for proving
prompt/tool/hook parity.

## 6. Acceptance tests

1. `before_agent_start` produces the Forge prompt exactly once and preserves
   prior extension modifications.
2. Closed mode exposes only the declared tool names and does not inherit
   ambient tools, skills, or context files.
3. A harmless bounded read is allowed.
4. A path outside the workspace is blocked.
5. A write is blocked when mutation is disabled.
6. A dangerous Bash command is blocked before execution.
7. A hook timeout follows the configured fail-closed/fail-open policy.
8. `tool_result` sees and records the real terminal outcome, including
   timeout/error metadata.
9. Two sibling tool calls do not rely on sibling result visibility.
10. Tool progress and result rendering remain correct when the result is
    partial, truncated, expanded, or an error.
11. Session restart rehydrates policy and prompt provenance without reusing a
    stale session manager/context object.
12. RPC/stdout output remains machine-readable; diagnostics stay off stdout.

## Current recommendation

Use Pi’s existing mechanisms for hooks, system prompts, tools, rendering,
session persistence, and compaction callbacks. Reimplement only Swarm’s
capability model, closed harness compiler/loader, prompt provenance, stable
event taxonomy, and external transport bridges. This is the smallest path to
behavioral parity with the least duplicated infrastructure.
