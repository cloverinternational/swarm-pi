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
8. [Safety and vendor policy](#safety-and-vendor-policy)
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
Pi ExtensionAPI (.pi/extensions/<NN-layer>/)
        │ registration, gates, persistence, rendering
        ├── 00-runtime   ── integration boundary, hook engine, tool surface, parity
        ├── 10-context   ── swarm-prompt + swarm-context, plan mode, skills
        ├── 20-policy    ── policy + disk hooks + policy nudges
        ├── 30-tools     ── agents, tasks, history, fs, bash, MCP, vault, research
        ├── 40-state     ── session entries + .swarm stores
        └── 50-ui        ── renderers, widgets, status, footer, themes
```

Semantic state, authorization, persistence, and cancellation must not depend
on whether a TUI frame rendered. Do not infer runtime state from assistant
prose or rendered text when a lifecycle event or persisted entry is available.

## Repository map

Everything is filed by one rule — *what does it primarily register or
export?* — into six layers. The same six names are used for Pi extensions,
shared libraries, their tests, and the npm packages, so a concept has one
address in each tree.

| # | Layer | Owns |
| --- | --- | --- |
| 00 | `runtime` | integration boundary, hook engine, tool surface/gating, transport parity, telemetry |
| 10 | `context` | system prompt, prompt/context config, plan mode, thinking, skills, inspector |
| 20 | `policy` | disk hooks, sleep blocker, nudges |
| 30 | `tools` | every `registerTool` surface: bash, fs, search, agents, tasks, schedule, history, vault, research, MCP, ask-user, annoyed, codemode |
| 40 | `state` | durable session entries: memory history, conversation metadata |
| 50 | `ui` | control panel, metrics widgets, themes, tools-status command |

| Path | Purpose |
| --- | --- |
| `.pi/extensions/<NN-layer>/` | Pi entrypoints. Each layer directory has a `package.json` whose `pi.extensions` list is the load order; layers load in numeric-prefix order. Pi discovers only one level deep, so a new extension must be added to its layer manifest. |
| `.pi/lib/<layer>/` | Shared extension/runtime helpers, filed by the same rule. Hook infrastructure (`hook-state`, `hook-observations`, `hook-presenter`, `hook-render-bridge`) lives in `.pi/lib/runtime/`. |
| `.pi/test/<layer>/` | Tests for `.pi` code. A test lives in the layer of the module it primarily imports. |
| `.pi/config/` | Project configuration: `prompt-context.json`, `swarm-settings.json`, and the ignored `api-keys.json`. |
| `.pi/themes/` | Local Pi theme definitions. |
| `packages/<layer>/<name>/` | npm workspaces (`packages/*/*`). Directory name equals the npm name suffix: `packages/runtime/core` is `@pi-swarm/core`. |
| `packages/runtime/` | `core` (session identity, event journal), `contract` (immutable profile/capability contracts), `runtime-contracts` (control-plane, daemon, goal-loop, task interfaces). |
| `packages/context/` | `prompt` (canonical prompt assets and provenance), `skills` (loader, registry, builtins), `autogenskills` (generated-skill lifecycle). |
| `packages/policy/` | `policy` (capability, workspace, mutation, network, approval). |
| `packages/tools/` | `agents`, `taskmanage`, `mcp`, `schedule`, `codemode`. |
| `tests/parity/` | Cross-runtime parity suite (`node --test`). |
| `tools/parity/` | Pi ↔ Swarm probes, fixtures, generators; `plexus/` holds the A/B harness. |
| `tools/integration/` | Postgres integration runner and cache dogfood. |
| `tools/install/` | `doctor.mjs`: global-install health check (`npm run doctor`). |
| `tools/repo/` | Repository maintenance: `rewrite-imports.mjs` re-targets relative specifiers after moves and has a `--check` mode. |
| `docs/architecture/` | Living design descriptions. |
| `docs/reference/` | Contracts, inventories, and operational references. |
| `docs/parity/` | Parity plan, acceptance record, and VHS media. |
| `docs/plans/{active,archive}/` | Plans still driving work; executed plans headed with what superseded them. |
| `infra/` | `postgres/` compose stack, `bridges-go/` Go bridge. |
| `vendor/` | Read-only references: `pi-mono`, `swarm-sdk`, and the `opencode` and `page-index` submodules. Never edited and never imported at runtime. |
| `artifacts/` | Ignored. Probe outputs (`artifacts/parity/<run>/`), baselines, recordings. |
| `.swarm/`, `.pi/agent-sessions/` | Ignored runtime state; never a source of truth for implementation. |

Naming rules: directories and Markdown files are `kebab-case`; the only
capitalised files are `AGENTS.md`, per-package `README.md`, and skill
`SKILL.md`/reference files whose names are contractual. `dist/`,
`node_modules/`, `artifacts/`, `.pi/agent-sessions/`, and `.swarm/` are never
tracked. Generated files say so in their header and name their generator.

## How Pi is composed

1. Pi loads project extensions from `.pi/extensions/` one level deep: each
   `<NN-layer>/package.json` declares its entry files under `pi.extensions`,
   and Pi exposes the `ExtensionAPI` to each of them.
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

- **Bottom/footer:** `.pi/extensions/50-ui/conversation-metrics.ts`; it calls
  `ctx.ui.setFooter(...)`, shows running/idle walltime and output tokens, and
  refreshes with `requestRender()`. Pi has exactly one footer slot, so this
  extension is its sole owner: other extensions must not call `setFooter`.
  They contribute a text segment through the process-wide registry
  `globalThis[Symbol.for("pi-swarm-footer-segments")]` (a `Map<name, () =>
  string | undefined>`; `autogenskills` registers `"autogen"` there). Extension
  factories must guard re-entry per `pi` instance (WeakSet/WeakMap), never with
  a process-wide boolean: `/reload` re-evaluates modules with a fresh `pi`
  while `globalThis` survives, and a boolean guard silently skips registration.
- **Inline hook rows:** `.pi/lib/runtime/hook-render-bridge.ts` and `.pi/lib/runtime/hook-presenter.ts`.
- **Themes:** `.pi/themes/*.json`.

The detailed design references are
`docs/architecture/modular-pi-architecture.md`,
`docs/architecture/swarm-tui-to-pi-map.md`, and
`docs/architecture/hooks-prompts-tools-pi-equivalence.md`. Update those
documents when a change alters a documented architectural boundary.

## AGENTS.md discovery

Pi-Swarm now discovers instructions hierarchically through
`.pi/lib/context/swarm-context.ts` and injects them through `.pi/extensions/10-context/swarm-prompt.ts`.
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

| Layer (load order) | Entrypoints |
| --- | --- |
| `00-runtime` | `cache-telemetry`, `hooks`, `swarm-runtime`, `swarm-transport-parity` |
| `10-context` | `autogenskills`, `prompt-context-configure`, `swarm-plan-mode`, `swarm-prompt`, `swarm-skills`, `swarm-thinking`, `system-inspector`, `system-prompts` |
| `20-policy` | `swarm-disk-hooks` |
| `30-tools` | `annoyed/`, `codemode`, `control-task-tools`, `exa-search`, `history-search`, `ask-user/`, `research-tools`, `schedule`, `swarm-agent-tools`, `swarm-background-bash`, `swarm-bash`, `swarm-fs-tools`, `swarm-history-vault-tools`, `swarm-search`, `taskmanage`, `vault` |
| `40-state` | `memory-history`, `swarm-conversation-metadata` |
| `50-ui` | `control-panel`, `conversation-metrics`, `swarm-themes`, `swarm-tools-status` |

The list in each layer's `package.json` is authoritative; this table mirrors
it. Adding an extension means adding the file *and* its manifest entry.

An extension is an adapter, not a second agent runtime. Prefer Pi native
capabilities and shared policy over duplicate tools or parallel global state.

## Capability locator

Use this section when a user names a tool, skill, or hook. Start at the public
Pi extension, then follow its imported package/library. Keep the extension
thin: execution/domain logic belongs in the package or `.pi/lib/`, policy
belongs in `packages/policy/policy`, and rendering belongs in the extension/render bridge.

### Tools by user-facing name

| Tool(s) | Pi registration | Owning implementation / seam |
| --- | --- | --- |
| `bash` | `.pi/extensions/30-tools/swarm-bash.ts` | `.pi/lib/tools/swarm-bash.ts`; policy must gate execution separately. |
| `Read`, `apply_patch`, `Undo` | `.pi/extensions/30-tools/swarm-fs-tools.ts` | `.pi/lib/tools/swarm-apply-patch.ts`, `.pi/lib/tools/swarm-read-image.ts`; filesystem boundary is an explicit decoupling seam. |
| `Agent`, `AgentControl` | `.pi/extensions/30-tools/swarm-agent-tools.ts` | `packages/tools/agents/src/index.ts`, `packages/tools/agents/src/general-agent-adapter.ts`; child runner/session isolation lives in `packages/tools/agents`. |
| `task_create`, `task_update`, `task_get`, `task_list`, `task_delete`, `task_claim`, `task_note`, `task_plan`, `task_complete`, `task_reopen`, `task_block`, `task_unblock`, `task_focus`, `task_unfocus`, `task_status`, `run_status` | `.pi/extensions/30-tools/taskmanage.ts`, `.pi/extensions/30-tools/control-task-tools.ts` | `packages/tools/taskmanage/src/task-manage.ts`, `packages/tools/taskmanage/src/workflow.ts`, `packages/tools/taskmanage/src/persistence.ts`, `packages/runtime/runtime-contracts/src/control-task.ts`; do not duplicate task state in extensions. |
| `HistorySearch`, `HistoryGet` | `.pi/extensions/30-tools/swarm-history-vault-tools.ts` and `.pi/extensions/30-tools/history-search.ts` | `.pi/lib/tools/swarm-history-tools.ts`; history search/read is deliberately read-only, bounded, and redacted. |
| `memory_history` | `.pi/extensions/40-state/memory-history.ts` | Extension-local memory implementation; preserve workspace/session scoping and redaction. |
| `skills_list`, `skill_view` | `.pi/extensions/10-context/swarm-skills.ts` | `packages/context/skills/src/index.ts` and `.pi/lib/context/swarm-skill-registry.ts`; skill bodies/support files stay on disk. |
| `Skill`, `SkillManage` | skill/autogen integration via `.pi/extensions/10-context/swarm-skills.ts`, `.pi/extensions/10-context/autogenskills.ts` | `packages/context/autogenskills/src/index.ts`; mutate skills only through the vault/revision API. |
| `websearch` | `.pi/extensions/30-tools/exa-search.ts` | Exa HTTP adapter; credentials/config must remain outside tool arguments. |
| `xai_web_search`, `x_search` | `.pi/extensions/30-tools/swarm-search.ts` | xAI HTTP adapter; credential lookup is environment/vault mediated. |
| `web_fetch`, `deepwiki`, `browser_get_page` | `.pi/extensions/30-tools/research-tools.ts` | Extension-local bounded evidence fetcher; provenance and network policy are coupled requirements. |
| `codemode` | `.pi/extensions/30-tools/codemode.ts` | `packages/tools/codemode/src/` interpreter, schema, OpenAPI, and runtime (`@pi-swarm/codemode`, consumed as TypeScript source); it composes registered tools and must not bypass policy. |
| `enter_plan_mode`, `exit_plan_mode` | `.pi/extensions/10-context/swarm-plan-mode.ts` | `.pi/lib/context/swarm-plan-mode.ts`; plan approval is separate from implementation. |
| `ask_user_question` | `.pi/extensions/30-tools/ask-user/index.ts` | Fork of `edlsh/pi-ask-user` v0.15.0 (MIT, `LICENSE` beside it); local edits are marked `pi-swarm:`. Upstream `bun:test` suite not carried; `.pi/test/tools/ask-user-layout.test.ts` covers the layout helper. |
| `control_plane_status` | `.pi/extensions/50-ui/control-panel.ts` | `packages/runtime/runtime-contracts/src/control-plane.ts`, `control-plane-store.ts`; dashboard is read-only. |
| `daemon_status`, goal/task/run tools | `.pi/extensions/00-runtime/swarm-runtime.ts`, `.pi/extensions/30-tools/control-task-tools.ts` | `packages/runtime/runtime-contracts/src/daemon-rpc.ts`, `goal-loop.ts`, `control-task.ts`; unavailable daemon must fail closed. |
| `vault_add`, `vault_approve`, `vault_exec`, `vault_list`, `vault_two_person_status` | `.pi/extensions/30-tools/swarm-history-vault-tools.ts` | `.pi/lib/tools/swarm-vault-tools.ts`; never expose secret values. |
| `vault` | `.pi/extensions/30-tools/vault.ts` | `.pi/lib/tools/swarm-vault-tools.ts`; transparent global credential storage, with explicit user-risk warning. |
| `mcp__<server>__<tool>` | `.pi/extensions/00-runtime/swarm-runtime.ts` / `packages/tools/mcp/src/index.ts` | `packages/tools/mcp/src/index.ts`; manifests, allowlists, transport, and auth are the seam. |
| `annoyed` | `.pi/extensions/30-tools/annoyed/index.ts` | `.pi/extensions/30-tools/annoyed/store.ts`; issue persistence is separate from the nudge hook. |

Names may be filtered by active-tool policy. `swarm-tools-status` and
`system-inspector` show the runtime's actual registered/active surface; use
those instead of assuming every row above is enabled.

### Skills and their locations

| Skill source | Location | Owner / notes |
| --- | --- | --- |
| Builtin `loop` | `packages/context/skills/builtins/loop/SKILL.md` | `packages/context/skills/src/index.ts` loader; progressive disclosure. |
| Builtin `swarm-skill` | `packages/context/skills/builtins/swarm-skill/SKILL.md` and `references/SKILL-AUTHORING.md` | Skill authoring contract. |
| Builtin `swarm-workflow` | `packages/context/skills/builtins/swarm-workflow/SKILL.md` and `references/` | Workflow schema, profiles, examples, strategies, troubleshooting. |
| `ask-user` | `.pi/extensions/30-tools/ask-user/skills/ask-user/SKILL.md` and `references/` | Carried with the forked `ask_user_question` extension; deliberately not registered as a builtin so the parity-compared `<available_skills>` block is unchanged. |
| Autogenerated/project/user/managed/install skills | Runtime search paths resolved by `packages/context/skills/src/index.ts` | Do not hard-code paths; `swarm-skills.ts` exposes source and support files. |
| Autogenerated skill lifecycle/revisions | `.pi/extensions/10-context/autogenskills.ts` + `packages/context/autogenskills/src/index.ts` | Curator, locks, budgets, review/absorb/archive/pin. |
| Local harness skill | `.swarm/skills/pi-harness-engineering/SKILL.md` | Runtime state/configuration; do not mistake it for a builtin package skill. |

If changing skill discovery, loading, precedence, progressive disclosure, or
support-file safety, change `packages/context/skills/src/index.ts` and its tests first; change
the Pi adapter only for registration/presentation concerns.

### Hooks and lifecycle locations

| Hook group / concern | Registration | Implementation / events |
| --- | --- | --- |
| Central hook state and ordering | `.pi/lib/runtime/hook-state.ts` | Registration, enablement, persistence, and visibility. |
| Prompt hook `packages/context/prompt` | `.pi/extensions/10-context/swarm-prompt.ts` | `before_agent_start`; prompt/context assembly in `packages/context/prompt/src/index.ts` and `.pi/lib/context/swarm-context.ts`. |
| Disk hooks `disk-hooks` | `.pi/extensions/20-policy/swarm-disk-hooks.ts` | Loads project/user hook config, executes bounded commands; maps tool/session/prompt/compact events. |
| Inline hook presentation | `.pi/extensions/00-runtime/hooks.ts`, `.pi/lib/runtime/hook-render-bridge.ts`, `.pi/lib/runtime/hook-presenter.ts` | UI only; never make governance depend on rendering. |
| Annoyance/nudge | `.pi/extensions/30-tools/annoyed/nudge.ts` | `tool_result`, `turn_end`; persistence in `annoyed/store.ts`. |
| Task enforcement | `.pi/extensions/30-tools/taskmanage.ts` | `packages/tools/taskmanage/src/task-hooks.ts`, `swarm-hook-runtime.ts`; task state is authoritative in taskmanage. |
| Metrics/cache telemetry | `.pi/extensions/50-ui/conversation-metrics.ts`, `cache-telemetry.ts` | Agent/message/provider lifecycle; persisted telemetry is non-secret. |
| Theme/thinking/UI lifecycle | `.pi/extensions/50-ui/swarm-themes.ts`, `swarm-thinking.ts` | Presentation/config only. |

For a hook bug, first identify the Pi event (`before_agent_start`, `tool_call`,
`tool_result`, `turn_start/end`, `session_start/shutdown`, or compaction), then
the hook group, then the domain owner. Hook execution, policy, and rendering
are separate seams and should remain decoupled.

### Package source locator

| Package | Primary source files | Change here when… |
| --- | --- | --- |
| `packages/tools/agents` | `packages/tools/agents/src/index.ts`, `general-agent-adapter.ts`, `worker-daemon.ts`, `absurd-control-plane.ts` | Agent identity, runner, cancellation, concurrency, or child sessions change. |
| `packages/context/autogenskills` | `packages/context/autogenskills/src/index.ts` | Skill curation, locking, budgets, revision history, or review policy changes. |
| `packages/tools/mcp` | `packages/tools/mcp/src/index.ts` | MCP manifests, transports, discovery, tool allowlists, or auth change. |
| `packages/policy/policy` | `packages/policy/policy/src/policy.ts`, `packages/policy/policy/src/index.ts` | Authorization, workspace/mutation/network boundaries, or fail-closed rules change. |
| `packages/runtime/runtime-contracts` | `packages/runtime/runtime-contracts/src/*.ts` | Control-plane, daemon, goal-loop, task, or stable runtime interfaces change. |
| `packages/tools/schedule` | `packages/tools/schedule/src/{cron,scheduler,store,tools,types}.ts` | Scheduling semantics, persistence, or schedule tools change. |
| `packages/tools/codemode` | `packages/tools/codemode/src/{codemode,tool,tool-runtime,tool-schema}.ts`, `interpreter/`, `openapi/`, `stdlib/` | The confined CodeMode interpreter, tool-schema bridge, or OpenAPI import changes. Its `build` is a `noEmit` typecheck; the extension imports its `src/` directly. |
| `packages/context/skills` | `packages/context/skills/src/index.ts`, `packages/context/skills/builtins/**` | Skill loading, precedence, builtins, metadata, or disclosure changes. |
| `packages/runtime/contract` | `packages/runtime/contract/src/index.ts` | Immutable profile, capability IDs, digest, or provenance contracts change. |
| `packages/runtime/core` | `packages/runtime/core/src/index.ts` | Session identity, event journal, or runtime replacement semantics change. |
| `packages/context/prompt` | `packages/context/prompt/src/index.ts`, `assets/*.txt`, `scripts/sync.mjs` | Canonical prompt assets or prompt provenance changes. |
| `packages/tools/taskmanage` | `packages/tools/taskmanage/src/*.ts` | Task persistence, hooks, workflows, interaction, or authoritative task lifecycle changes. |

Tests live beside each package in its `test/` directory and under
`.pi/test/<layer>/` for extension and library behaviour; one root
`vitest.config.ts` runs both. Update both sides when a public contract crosses
the package/extension boundary.

## Package boundaries

- `packages/context/prompt` owns prompt precedence, workspace context, and non-secret
  provenance; chain `event.systemPrompt` instead of overwriting blindly.
- `packages/policy/policy` owns final fail-closed authorization. Tool registration or a prompt
  instruction is not authorization.
- `packages/tools/taskmanage`, `packages/tools/agents`, `history-search`, and `memory-history` own their
  respective durable workflows; use stable IDs and bounded outputs.
- `packages/runtime/core` owns shared runtime identity/lifecycle; extensions must not each
  invent global identity or duplicate lifecycle state.
- `packages/context/skills`, `packages/context/autogenskills`, `packages/tools/mcp`, and `packages/tools/schedule` provide opt-in capability
  layers; do not make ambient discovery silently widen a closed profile.
- `packages/runtime/contract` and `packages/runtime/runtime-contracts` define interfaces consumed by
  multiple packages; change them deliberately and update all consumers/tests.
- Import direction is fixed by who runs the code. `.pi/` imports package
  sources by relative path (`../../../packages/<layer>/<name>/src/index.ts`):
  Pi loads extensions through jiti with plain Node resolution and no build
  step, so a bare `@pi-swarm/<name>` import there would silently depend on a
  stale or missing `dist/`. Package-to-package imports use the bare workspace
  name (`@pi-swarm/core`, `@pi-swarm/runtime-contracts`) against that
  package's `dist` exports so `tsc` `rootDir` boundaries hold. Do not mix the
  two; `tools/repo/rewrite-imports.mjs --check` verifies the relative side.

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
npm install                       # one workspace install; single lockfile
npm run build                     # tsc for every package, dependency order
npm test                          # vitest: packages/**/test, .pi/test, tests/
npx vitest run .pi/test/context   # one layer, or any path/file
npm run build -w @pi-swarm/prompt # one package
npm run test:parity               # Pi ↔ Swarm wire parity (node --test)
npm run parity:probe              # capture into artifacts/parity/default
npm run doctor                    # global-install health: PATH, dist, deps, models.json, theme
node tools/repo/rewrite-imports.mjs --check   # every relative specifier resolves
npm run dogfood                   # build + test + parity
```

### Installing globally

The repository is itself a Pi package (root `package.json` `pi` manifest:
the six extension layers and `.pi/themes`). Never copy `.pi/extensions/`
anywhere — the extensions import `.pi/lib`, `packages/*/src`, and
`tools/parity/fixtures` by relative path and need the hoisted `node_modules`.

```sh
# dev box: link this checkout (no copy; dedupes against .pi/ by absolute path)
pi install /home/swarm/Work/Pi-Swarm
# any other machine (pin a tag or commit; `pi update` reconciles the ref)
pi install git:github.com/cloverinternational/swarm-pi@<tag>
# from the installed checkout: PATH, versions, dist, deps, models.json, theme, packages[]
npm run doctor
```

What `pi install` does (vendor/pi-mono `package-manager.ts`): `git clone`
(no submodules) into `~/.pi/agent/git/<host>/<path>`, then
`npm install --omit=dev`, never `npm run build`. Consequences that are
contracts here:

- the root `prepare` script is the only build a global install gets; every
  package consumed from `dist/` at runtime (`@pi-swarm/core`,
  `@pi-swarm/runtime-contracts`) must be in `build:runtime`;
- anything imported at runtime is in root or package `dependencies`, never
  `devDependencies` (`typescript` is a runtime dependency of codemode);
- nothing under `vendor/` is imported at runtime (submodules are empty in a
  package clone); `ask_user_question` is the in-tree fork for that reason;
- on a machine that also opens this repo as a project, use the local-path
  form — a `git:` install has a different absolute path and would register
  every tool twice.

Prove a change against the package path, not just the project boot:
`node tools/parity/probe.mjs --profile project --workspace <foreign dir>
--package <checkout>` boots Pi with `settings.packages` pointing at the
checkout and no `--extension` injection. Host-level items (node on the
non-interactive `PATH`, `models.json` `maxTokens`, tmux launch shell,
provider key scope) are outside the package; `npm run doctor` reports the
first two.

Run the narrowest relevant check first, then broader checks when the change is
cross-package. Never claim a check passed unless it was actually run. After
moving files, run the import rewriter with a move map and then `--check`
rather than editing specifiers by hand.

## Safety and vendor policy

- Preserve uncommitted user changes. Never reset, clean, or rewrite unrelated
  files.
- Do not expose credentials, API keys, session transcripts, generated state,
  or large dependency trees in source changes or output. Treat `.pi/config` as
  sensitive; use the credential vault for provided secrets.
- Do not edit, format, regenerate, delete, move, or otherwise mutate anything
  under `vendor/` (formerly `upstream/`). Read it for reference and implement
  local changes in packages, extensions, tools, or docs. If a vendored change
  appears necessary, stop and ask for explicit approval.
