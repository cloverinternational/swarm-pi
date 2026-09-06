# Pi-Swarm Agent Contract

This file is the repository's operating contract for every coding agent. Read
it before changing anything. If a change makes this file inaccurate, update
`AGENTS.md` in the same change. A task is not complete until the index,
architecture notes, and commands below still describe the repository.

## Index

1. [Mission and architecture](#mission-and-architecture)
2. [Repository map](#repository-map)
3. [How Pi is composed](#how-pi-is-composed)
4. [Extension inventory](#extension-inventory)
5. [Package boundaries](#package-boundaries)
6. [Change contract](#change-contract)
7. [Validation](#validation)
8. [Safety and upstream policy](#safety-and-upstream-policy)
9. [Capability locator](#capability-locator)
10. [AGENTS.md discovery](#agentsmd-discovery)

## Mission and architecture

Pi-Swarm is a Pi extension-based Swarm runtime. Pi owns the provider/agent
loop, session manager, native tools, and terminal UI. This repository adds
Swarm prompt assembly, policy, hooks, persistence, task/subagent workflows,
history, skills, MCP/vault adapters, parity behavior, and UI extensions.

The governing separation is:

```text
Pi AgentSession + SessionManager
        │ lifecycle, context, provider, native tools
        ▼
Pi ExtensionAPI (.pi/extensions)
        │ registration, gates, persistence, rendering
        ├── prompt/context      ── swarm-prompt + swarm-context
        ├── policy/governance   ── policy + hooks + upstream-readonly
        ├── Swarm tools         ── agents, tasks, history, fs, bash, MCP, vault
        ├── durable state       ── session entries + .swarm stores
        └── presentation        ── renderers, widgets, status, footer, themes
```

Semantic state, authorization, persistence, and cancellation must not depend
on whether a TUI frame rendered. Do not infer runtime state from assistant
prose or rendered text when a lifecycle event or persisted entry is available.

## Repository map

| Path | Purpose |
| --- | --- |
| `.pi/extensions/` | Pi entrypoints: tools, commands, lifecycle hooks, renderers, and adapters. |
| `.pi/lib/` | Shared extension/runtime helpers; keep reusable behavior here. |
| `.pi/themes/` | Local Pi theme definitions. |
| `agents/` | Agent/delegate execution and runner adapters. |
| `autogenskills/` | Generated-skill discovery and lifecycle integration. |
| `mcp/` | MCP manifest and tool adapter package. |
| `policy/` | Capability, workspace, mutation, network, and approval policy. |
| `runtime-contracts/` | Stable runtime/lifecycle contracts and control-plane types. |
| `schedule/` | Scheduling and recurring execution contracts. |
| `skills/` | Skill loading, registry, and builtins. |
| `swarm-contract/` | Shared Swarm protocol/schema contracts. |
| `swarm-core/` | Shared runtime identity, state, and lifecycle implementation. |
| `swarm-prompt/` | Prompt/context assembly and provenance. |
| `taskmanage/` | Durable task management and task hooks/tools. |
| `tests/` | Repository-level parity and integration tests. |
| `tools/` | Probes, dogfood, and integration helpers. |
| `docs/` and `*.md` | Architecture, parity, and operational design records. |
| `bridges/`, `infra/` | Go bridge and local integration infrastructure. |
| `upstream/` | Vendored read-only reference snapshots. |
| `.swarm/`, `.pi/agent-sessions/` | Local/generated runtime state; never source of truth for implementation. |

## How Pi is composed

1. Pi loads project extensions from `.pi/extensions/` and exposes the
   `ExtensionAPI`.
2. Extensions register tools with `pi.registerTool`, commands with
   `pi.registerCommand`, shortcuts with `pi.registerShortcut`, and lifecycle
   handlers with `pi.on`.
3. `session_start` establishes session-scoped state and rehydrates persisted
   entries. `before_agent_start` composes the effective prompt/context.
4. The agent loop requests the provider, handles assistant output and tool
   calls, then emits tool and turn lifecycle events.
5. Policy/hook extensions observe or gate calls at `tool_call`; execution
   outcomes are handled at `tool_result`/`tool_execution_end`.
6. Extensions persist bounded, non-secret state with `appendEntry`; they must
   discard stale session/context references after replacement or shutdown.
7. Pi renders tool calls/results through native renderers. Swarm presentation
   uses `renderCall`/`renderResult`, entry renderers, widgets, status, and
   footer APIs rather than writing transcript content for UI-only state.

Important UI locations:

- **Bottom/footer:** `.pi/extensions/conversation-metrics.ts`; it calls
  `ctx.ui.setFooter(...)`, shows running/idle walltime and output tokens, and
  refreshes with `requestRender()`.
- **Inline hook rows:** `.pi/hook-render-bridge.ts` and `.pi/hook-presenter.ts`.
- **Themes:** `.pi/themes/*.json`.

The detailed design references are `PI_MODULAR_ARCHITECTURE.md`,
`SWARM_TUI_TO_PI_MAP.md`, and `HOOKS_PROMPTS_TOOLS_PI_EQUIVALENCE.md`. Update
those documents when a change alters a documented architectural boundary.

## AGENTS.md discovery

Pi-Swarm now discovers instructions hierarchically through
`.pi/lib/swarm-context.ts` and injects them through `.pi/extensions/swarm-prompt.ts`.
The algorithm follows the current working directory's lexical path upward to
the nearest `.git` directory or `.git` file, then loads one instruction file
per directory from repository root to the working directory. If no Git root is
found, only the working directory is considered.

Within each directory, the precedence is:

1. `.swarm/AGENTS.md`
2. `.claude/AGENTS.md`
3. `AGENTS.md`

The selected files are concatenated root-first, so deeper instructions can
supplement or refine broader rules. Missing files are ignored. This is the
hierarchical ancestor model used by Swarm/OpenCode-style agents; it is **not**
a recursive scan of every sibling or descendant directory. A package-local
file becomes active when the agent's working directory is inside that package.

The discovery API is `discoverAgentsMdPaths(workDir)`. The context pipeline
uses it for `agentsMd`, records every discovered path in prompt provenance,
and applies the existing source/total byte budgets. `--no-context-files` /
`--no-project-memory` disables this source along with the other project memory
files. Do not replace this with a fixed root-only lookup or a recursive whole
repository scan without updating tests, budgets, and this contract.

When changing discovery, preserve these invariants:

- walk the logical path supplied as the workspace; do not silently canonicalize
  through symlinks before choosing ancestors;
- stop at the nearest Git root and never load parent-workspace instructions;
- preserve root-to-leaf ordering and one winning candidate per directory;
- keep instruction contents bounded and exclude secrets from provenance/logs;
- test Git directories, `.git` files, nested packages, no-Git fallback, and
  same-directory precedence.

## Extension inventory

| Area | Entrypoints |
| --- | --- |
| Runtime/prompt | `swarm-runtime`, `swarm-prompt`, `system-prompts`, `system-inspector` |
| Tools/files | `swarm-bash`, `swarm-fs-tools`, `swarm-agent-tools`, `control-task-tools`, `taskmanage` |
| Governance | `hooks`, `swarm-disk-hooks`, `upstream-readonly`, `swarm-plan-mode`, `annoyed/nudge` |
| State/history | `conversation-metrics`, `memory-history`, `history-search`, `cache-telemetry` |
| Skills/MCP/network | `swarm-skills`, `autogenskills`, `codemode`, `swarm-search`, `exa-search`, `research-tools` |
| Control/integration | `control-panel`, `swarm-transport-parity`, `schedule`, `pi-ask-user`, `swarm-history-vault-tools` |
| UI | `conversation-metrics`, `swarm-themes`, `swarm-thinking`, `system-inspector` |

An extension is an adapter, not a second agent runtime. Prefer Pi native
capabilities and shared policy over duplicate tools or parallel global state.

## Capability locator

Use this section when a user names a tool, skill, or hook. Start at the public
Pi extension, then follow its imported package/library. Keep the extension
thin: execution/domain logic belongs in the package or `.pi/lib/`, policy
belongs in `policy`, and rendering belongs in the extension/render bridge.

### Tools by user-facing name

| Tool(s) | Pi registration | Owning implementation / seam |
| --- | --- | --- |
| `bash` | `.pi/extensions/swarm-bash.ts` | `.pi/lib/swarm-bash.ts`; policy must gate execution separately. |
| `Read`, `apply_patch`, `Undo` | `.pi/extensions/swarm-fs-tools.ts` | `.pi/lib/swarm-apply-patch.ts`, `.pi/lib/swarm-read-image.ts`; filesystem boundary is an explicit decoupling seam. |
| `Agent`, `AgentControl` | `.pi/extensions/swarm-agent-tools.ts` | `agents/src/index.ts`, `agents/src/general-agent-adapter.ts`; child runner/session isolation lives in `agents`. |
| `task_create`, `task_update`, `task_get`, `task_list`, `task_delete`, `task_claim`, `task_note`, `task_plan`, `task_complete`, `task_reopen`, `task_block`, `task_unblock`, `task_focus`, `task_unfocus`, `task_status`, `run_status` | `.pi/extensions/taskmanage.ts`, `.pi/extensions/control-task-tools.ts` | `taskmanage/src/task-manage.ts`, `taskmanage/src/workflow.ts`, `taskmanage/src/persistence.ts`, `runtime-contracts/src/control-task.ts`; do not duplicate task state in extensions. |
| `HistorySearch`, `HistoryGet` | `.pi/extensions/swarm-history-vault-tools.ts` and `.pi/extensions/history-search.ts` | `.pi/lib/swarm-history-tools.ts`; history search/read is deliberately read-only, bounded, and redacted. |
| `memory_history` | `.pi/extensions/memory-history.ts` | Extension-local memory implementation; preserve workspace/session scoping and redaction. |
| `skills_list`, `skill_view` | `.pi/extensions/swarm-skills.ts` | `skills/src/index.ts` and `.pi/lib/swarm-skill-registry.ts`; skill bodies/support files stay on disk. |
| `Skill`, `SkillManage` | skill/autogen integration via `.pi/extensions/swarm-skills.ts`, `.pi/extensions/autogenskills.ts` | `autogenskills/src/index.ts`; mutate skills only through the vault/revision API. |
| `websearch` | `.pi/extensions/exa-search.ts` | Exa HTTP adapter; credentials/config must remain outside tool arguments. |
| `xai_web_search`, `x_search` | `.pi/extensions/swarm-search.ts` | xAI HTTP adapter; credential lookup is environment/vault mediated. |
| `web_fetch`, `deepwiki`, `browser_get_page` | `.pi/extensions/research-tools.ts` | Extension-local bounded evidence fetcher; provenance and network policy are coupled requirements. |
| `codemode` | `.pi/extensions/codemode/index.ts` | `.pi/extensions/codemode/src/` interpreter, schema, OpenAPI, and runtime; it composes registered tools and must not bypass policy. |
| `enter_plan_mode`, `exit_plan_mode` | `.pi/extensions/swarm-plan-mode.ts` | `.pi/lib/swarm-plan-mode.ts`; plan approval is separate from implementation. |
| `control_plane_status` | `.pi/extensions/control-panel.ts` | `runtime-contracts/src/control-plane.ts`, `control-plane-store.ts`; dashboard is read-only. |
| `daemon_status`, goal/task/run tools | `.pi/extensions/swarm-runtime.ts`, `.pi/extensions/control-task-tools.ts` | `runtime-contracts/src/daemon-rpc.ts`, `goal-loop.ts`, `control-task.ts`; unavailable daemon must fail closed. |
| `vault_add`, `vault_approve`, `vault_exec`, `vault_list`, `vault_two_person_status` | `.pi/extensions/swarm-history-vault-tools.ts` | `.pi/lib/swarm-vault-tools.ts`; never expose secret values. |
| `mcp__<server>__<tool>` | `.pi/extensions/swarm-runtime.ts` / `mcp/src/index.ts` | `mcp/src/index.ts`; manifests, allowlists, transport, and auth are the seam. |
| `annoyed` | `.pi/extensions/annoyed/index.ts` | `.pi/extensions/annoyed/store.ts`; issue persistence is separate from the nudge hook. |

Names may be filtered by active-tool policy. `swarm-tools-status` and
`system-inspector` show the runtime's actual registered/active surface; use
those instead of assuming every row above is enabled.

### Skills and their locations

| Skill source | Location | Owner / notes |
| --- | --- | --- |
| Builtin `loop` | `skills/builtins/loop/SKILL.md` | `skills/src/index.ts` loader; progressive disclosure. |
| Builtin `swarm-skill` | `skills/builtins/swarm-skill/SKILL.md` and `references/SKILL-AUTHORING.md` | Skill authoring contract. |
| Builtin `swarm-workflow` | `skills/builtins/swarm-workflow/SKILL.md` and `references/` | Workflow schema, profiles, examples, strategies, troubleshooting. |
| `ask-user` | `pi-ask-user/skills/ask-user/SKILL.md` and `references/ask-user-skill-extension-spec.md` | Separate nested package/submodule; UI interaction skill. |
| Autogenerated/project/user/managed/install skills | Runtime search paths resolved by `skills/src/index.ts` | Do not hard-code paths; `swarm-skills.ts` exposes source and support files. |
| Autogenerated skill lifecycle/revisions | `.pi/extensions/autogenskills.ts` + `autogenskills/src/index.ts` | Curator, locks, budgets, review/absorb/archive/pin. |
| Local harness skill | `.swarm/skills/pi-harness-engineering/SKILL.md` | Runtime state/configuration; do not mistake it for a builtin package skill. |

If changing skill discovery, loading, precedence, progressive disclosure, or
support-file safety, change `skills/src/index.ts` and its tests first; change
the Pi adapter only for registration/presentation concerns.

### Hooks and lifecycle locations

| Hook group / concern | Registration | Implementation / events |
| --- | --- | --- |
| Central hook state and ordering | `.pi/hook-state.ts` | Registration, enablement, persistence, and visibility. |
| Prompt hook `swarm-prompt` | `.pi/extensions/swarm-prompt.ts` | `before_agent_start`; prompt/context assembly in `swarm-prompt/src/index.ts` and `.pi/lib/swarm-context.ts`. |
| Disk hooks `disk-hooks` | `.pi/extensions/swarm-disk-hooks.ts` | Loads project/user hook config, executes bounded commands; maps tool/session/prompt/compact events. |
| Upstream guard | `.pi/extensions/upstream-readonly.ts` | `tool_call`; blocks mutation targeting `upstream/`. |
| Inline hook presentation | `.pi/extensions/hooks.ts`, `.pi/hook-render-bridge.ts`, `.pi/hook-presenter.ts` | UI only; never make governance depend on rendering. |
| Annoyance/nudge | `.pi/extensions/annoyed/nudge.ts` | `tool_result`, `turn_end`; persistence in `annoyed/store.ts`. |
| Task enforcement | `.pi/extensions/taskmanage.ts` | `taskmanage/src/task-hooks.ts`, `swarm-hook-runtime.ts`; task state is authoritative in taskmanage. |
| Metrics/cache telemetry | `.pi/extensions/conversation-metrics.ts`, `cache-telemetry.ts` | Agent/message/provider lifecycle; persisted telemetry is non-secret. |
| Theme/thinking/UI lifecycle | `.pi/extensions/swarm-themes.ts`, `swarm-thinking.ts` | Presentation/config only. |

For a hook bug, first identify the Pi event (`before_agent_start`, `tool_call`,
`tool_result`, `turn_start/end`, `session_start/shutdown`, or compaction), then
the hook group, then the domain owner. Hook execution, policy, and rendering
are separate seams and should remain decoupled.

### Package source locator

| Package | Primary source files | Change here when… |
| --- | --- | --- |
| `agents` | `agents/src/index.ts`, `general-agent-adapter.ts`, `worker-daemon.ts`, `absurd-control-plane.ts` | Agent identity, runner, cancellation, concurrency, or child sessions change. |
| `autogenskills` | `autogenskills/src/index.ts` | Skill curation, locking, budgets, revision history, or review policy changes. |
| `mcp` | `mcp/src/index.ts` | MCP manifests, transports, discovery, tool allowlists, or auth change. |
| `policy` | `policy/src/policy.ts`, `policy/src/index.ts` | Authorization, workspace/mutation/network boundaries, or fail-closed rules change. |
| `runtime-contracts` | `runtime-contracts/src/*.ts` | Control-plane, daemon, goal-loop, task, or stable runtime interfaces change. |
| `schedule` | `schedule/src/{cron,scheduler,store,tools,types}.ts` | Scheduling semantics, persistence, or schedule tools change. |
| `skills` | `skills/src/index.ts`, `skills/builtins/**` | Skill loading, precedence, builtins, metadata, or disclosure changes. |
| `swarm-contract` | `swarm-contract/src/index.ts` | Immutable profile, capability IDs, digest, or provenance contracts change. |
| `swarm-core` | `swarm-core/src/index.ts` | Session identity, event journal, or runtime replacement semantics change. |
| `swarm-prompt` | `swarm-prompt/src/index.ts`, `assets/*.txt`, `scripts/sync.mjs` | Canonical prompt assets or prompt provenance changes. |
| `taskmanage` | `taskmanage/src/*.ts` | Task persistence, hooks, workflows, interaction, or authoritative task lifecycle changes. |

Tests live beside each package in its `test/` directory. Pi adapter behavior
is tested primarily under `taskmanage/test/` and `.pi/lib/*.test.ts`; update
both sides when a public contract crosses the package/extension boundary.

## Package boundaries

- `swarm-prompt` owns prompt precedence, workspace context, and non-secret
  provenance; chain `event.systemPrompt` instead of overwriting blindly.
- `policy` owns final fail-closed authorization. Tool registration or a prompt
  instruction is not authorization.
- `taskmanage`, `agents`, `history-search`, and `memory-history` own their
  respective durable workflows; use stable IDs and bounded outputs.
- `swarm-core` owns shared runtime identity/lifecycle; extensions must not each
  invent global identity or duplicate lifecycle state.
- `skills`, `autogenskills`, `mcp`, and `schedule` provide opt-in capability
  layers; do not make ambient discovery silently widen a closed profile.
- `swarm-contract` and `runtime-contracts` define interfaces consumed by
  multiple packages; change them deliberately and update all consumers/tests.

## Change contract

Every agent must:

1. Read this file and inspect `git status --short` before editing.
2. Create/update a TaskManage task for multi-step work and keep its status
   current.
3. Identify the owning package/extension before changing code; avoid unrelated
   refactors and preserve existing user changes.
4. Update this file's index, inventory, package boundaries, or flow whenever
   files, ownership, commands, extension registration, or architecture change.
5. Add focused tests for behavior changes where practical, and document any
   intentional test gap.
6. Report files changed, validation run, and known limitations.

When adding a new top-level package, extension, persistent entry type, hook
group, or user-facing command, add it to the relevant tables above in the same
patch. This is a contract, not optional documentation.

## Validation

```sh
npm run build              # build all TypeScript packages
npm test                   # package test suites
npm run test:parity        # repository parity tests
npm run dogfood            # build then package tests
npm --prefix <package> run build
npm --prefix <package> test
```

Run the narrowest relevant check first, then broader checks when the change is
cross-package. Never claim a check passed unless it was actually run.

## Safety and upstream policy

- Preserve uncommitted user changes. Never reset, clean, or rewrite unrelated
  files.
- Do not expose credentials, API keys, session transcripts, generated state,
  or large dependency trees in source changes or output. Treat `.pi/config` as
  sensitive; use the credential vault for provided secrets.
- Do not edit, format, regenerate, delete, move, or otherwise mutate anything
  under `upstream/`. Read it for reference and implement local changes in
  packages, extensions, tools, or docs. If upstream modification appears
  necessary, stop and ask for explicit approval.
- The local `.pi/extensions/upstream-readonly.ts` guard exists to block unsafe
  upstream mutation; do not bypass it.
