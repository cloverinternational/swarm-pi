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
Pi ExtensionAPI (extensions/<extension>/index.ts)
        │ registration, gates, persistence, rendering
        ├── 00-runtime   ── integration boundary, hook engine, tool surface, parity
        ├── 10-context   ── swarm-prompt + swarm-context, plan mode, skills
        ├── 20-policy    ── policy + disk hooks + structure guard + policy nudges
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
| 20 | `policy` | disk hooks, project structure guard and `/init`, sleep blocker, nudges |
| 30 | `tools` | every Pi-Swarm `registerTool` surface: bash, fs, search, agents, tasks, schedule, history, vault, research, MCP, ask-user, annoyed; CodeMode is provided by Pi |
| 40 | `state` | durable session entries: memory history, conversation metadata |
| 50 | `ui` | control panel, metrics widgets, themes, tools-status command, Pi-Swarm-owned `/btw` side-question overlay |

The root `extensions/trebol-toggle/index.ts` is always loaded and remains resident
when the project extension pack is disabled. Each layer manifest points to its
own `.trebol-guards/` wrapper, which dynamically imports exactly one original
factory when the process-local `Symbol.for("pi-swarm-trebol-mode")` state is
`on`. These nested directories are intentionally not autodiscovered. Ctrl+N
directly invokes reload through the shape-checked `trebol-shortcut.ts` runner
adapter; it never sends a user message. `/trebol-toggle` shares the same handler.
Reload is allowed only while idle with no queued messages. No per-press config
write occurs. Keep the root and local discovery paths deduplicated by resolved
path, and preserve native tools and unrelated extensions while disabled.

| Path | Purpose |
| --- | --- |
| `extensions/<name>/` | One extension per folder. Root package.json lists enabled index.ts entrypoints explicitly. extension.ts holds implementation; index.ts preserves whole-suite toggle behavior. No numbered layers or umbrella runtime. |
| `.pi/lib/<layer>/` | Shared extension/runtime helpers, filed by the same rule. Hook infrastructure (`hook-state`, `hook-observations`, `hook-presenter`, `hook-render-bridge`) lives in `.pi/lib/runtime/`. |
| `.pi/test/<layer>/` | Tests for `.pi` code. A test lives in the layer of the module it primarily imports. |
| `.pi/config/` | Project configuration: `prompt-context.json`, `swarm-settings.json`, and the ignored `api-keys.json`. |
| `.pi/themes/` | Local Pi theme definitions. |
| `packages/<layer>/<name>/` | npm workspaces (`packages/*/*`). Directory name equals the npm name suffix: `packages/runtime/core` is `@pi-swarm/core`. |
| `packages/runtime/` | `core` (session identity, event journal), `contract` (immutable profile/capability contracts), `runtime-contracts` (control-plane, daemon, goal-loop, task interfaces), `bootstrap` (bounded combined/parallel selector orchestration and settings persistence). |
| `packages/context/` | `prompt` (canonical prompt assets and provenance), `skills` (loader, registry, builtins), `autogenskills` (generated-skill lifecycle). |
| `packages/policy/` | `policy` (capability, workspace, mutation, network, approval). |
| `packages/tools/` | `agents`, `taskmanage`, `mcp`, `schedule`. |
| `tools/experiments/bootstrap-agent/` | Opt-in bootstrap experiments; `planning-contract.mjs` provides experimental prompt variants and strict plan validation. Private history replays stay in ignored artifacts; planning scores do not prove execution or memory retrieval. |
| `tools/integration/` | Postgres integration runner and cache dogfood. |
| `tools/install/` | `doctor.mjs`: global-install health check (`npm run doctor`). |
| `tools/repo/` | Repository maintenance: `rewrite-imports.mjs` re-targets relative specifiers after moves and has a `--check` mode. |
| `docs/architecture/` | Living design descriptions. |
| `docs/reference/` | Contracts, inventories, and operational references. |
| `docs/parity/` | Parity plan, acceptance record, and VHS media. |
| `docs/plans/{active,archive}/` | Plans still driving work; executed plans headed with what superseded them. |
| `infra/` | `postgres/` compose stack, `bridges-go/` Go bridge. |
| `vendor/` | Read-only references: `pi-mono`, `swarm-sdk`, and the `opencode` and `page-index` submodules. Never edited and never imported at runtime. |
| `artifacts/` | Ignored. Recordings and experiment outputs (e.g. `artifacts/tool-cards/`). |
| `.swarm/`, `.pi/agent-sessions/` | Ignored runtime state; never a source of truth for implementation. |

Naming rules: directories and Markdown files are `kebab-case`; the only
capitalised files are `AGENTS.md`, per-package `README.md`, and skill
`SKILL.md`/reference files whose names are contractual. `dist/`,
`node_modules/`, `artifacts/`, `.pi/agent-sessions/`, and `.swarm/` are never
tracked. Generated files say so in their header and name their generator.

## How Pi is composed

1. Pi loads the root package manifest, which explicitly lists `extensions/<name>/index.ts`. The project `.pi/settings.json` points to the root package.
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

- **Bottom/footer:** `extensions/conversation-metrics/extension.ts`; it calls
  `ctx.ui.setFooter(...)`, shows workspace/branch and model/state above context
  pressure, reported output tokens and accumulated active time. It uses semantic
  success/accent colors on the terminal's default background; narrow panes drop the
  provider/gauge and wrap groups. Unknown context shows a dash. Conditional
  extension segments occupy detail rows; expanded running-work remains available.
  Idle status shows the earliest live session loop/schedule countdown through the
  read-only `.pi/lib/runtime/schedule-status.ts` bridge; WORKING takes precedence.
  It refreshes with `requestRender()`. Pi has exactly one footer slot, so this
  extension is its sole owner: other extensions must not call `setFooter`.
  They contribute a text segment through the process-wide registry
  `globalThis[Symbol.for("pi-swarm-footer-segments")]` (a `Map<name, () =>
  string | undefined>`; `autogenskills` registers `"autogen"` there). Extension
  factories must guard re-entry per `pi` instance (WeakSet/WeakMap), never with
  a process-wide boolean: `/reload` re-evaluates modules with a fresh `pi`
  while `globalThis` survives, and a boolean guard silently skips registration.
- **Inline hook rows:** `.pi/lib/runtime/hook-render-bridge.ts` and `.pi/lib/runtime/hook-presenter.ts`.
- **Windows image paste:** `extensions/swarm-image-paste/extension.ts` with
  `.pi/lib/ui/windows-clipboard.ts`. It registers `ctrl+v` and `/paste-image`
  on win32 only, because Pi binds `app.clipboard.pasteImage` to `alt+v` there
  and its clipboard reader falls back to PowerShell only under WSL. Extension
  shortcuts are dispatched before built-in keybindings and `ctrl+v` is not a
  reserved binding, so Pi's `alt+v` keeps working. Do not register on other
  platforms: Pi's own `ctrl+v` is correct there.
- **Themes:** `.pi/themes/*.json`.

The detailed design references are
`docs/architecture/modular-pi-architecture.md`,
`docs/architecture/swarm-tui-to-pi-map.md`, and
`docs/architecture/hooks-prompts-tools-pi-equivalence.md`. Update those
documents when a change alters a documented architectural boundary.

## AGENTS.md discovery

Pi-Swarm now discovers instructions hierarchically through
`.pi/lib/context/swarm-context.ts` and injects them through `extensions/swarm-prompt/extension.ts`.
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
| `00-runtime` | `cache-telemetry`, `swarm-update`, `bootstrap`, `hooks`, `swarm-runtime`, `swarm-transport-parity` |
| `10-context` | `autogenskills`, `prompt-context-configure`, `swarm-plan-mode`, `swarm-prompt`, `swarm-auto`, `swarm-skills`, `swarm-thinking`, `system-inspector`, `system-prompts`, `mandatory-compaction` |
| `20-policy` | `swarm-disk-hooks`, `project-init` |
| `30-tools` | `annoyed/`, `control-task-tools`, `history-search`, `ask-user/`, `paseo`, `research-tools`, `swarm-goal`, `swarm-agent-tools`, `swarm-background-bash`, `swarm-bash`, `swarm-fs-tools`, `swarm-history-vault-tools`, `taskmanage`, `vault` |
| `40-state` | `memory-history`, `task-candidate-capture`, `knowledge-enrichment`, `candidate-memory-review`, `jev-knowledge-audit`, `memory-maintenance`, `swarm-conversation-metadata` |
| `50-ui` | `control-panel`, `supervisor`, `conversation-metrics`, `swarm-themes`, `swarm-tools-status`, `swarm-btw`, `swarm-image-paste` (through `.trebol-guards/` wrappers) |

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
| `bash` | `extensions/swarm-bash/extension.ts` | `.pi/lib/tools/swarm-bash.ts`; policy must gate execution separately. |
| `Read`, `apply_patch`, `Undo` | `extensions/swarm-fs-tools/extension.ts` | `.pi/lib/tools/swarm-apply-patch.ts`, `.pi/lib/tools/swarm-read-image.ts`; filesystem boundary is an explicit decoupling seam. |
| `Agent`, `AgentControl` | `extensions/swarm-agent-tools/extension.ts` | `packages/tools/agents/src/index.ts`, `packages/tools/agents/src/general-agent-adapter.ts`; child runner/session isolation lives in `packages/tools/agents`. |
| `task_create`, `task_update`, `task_get`, `task_list`, `task_delete`, `task_claim`, `task_note`, `task_plan`, `task_complete`, `task_reopen`, `task_block`, `task_unblock`, `task_focus`, `task_unfocus`, `task_status`, `run_status` | `extensions/taskmanage/extension.ts`, `extensions/control-task-tools/extension.ts` | `packages/tools/taskmanage/src/task-manage.ts`, `packages/tools/taskmanage/src/workflow.ts`, `packages/tools/taskmanage/src/persistence.ts`, `packages/runtime/runtime-contracts/src/control-task.ts`; do not duplicate task state in extensions. |
| `HistorySearch`, `HistoryGet` | `extensions/swarm-history-vault-tools/extension.ts` and `extensions/history-search/extension.ts` | `.pi/lib/tools/swarm-history-tools.ts` with shared `history-reader.ts`, `history-matching.ts`, and `history-admission.ts`; streaming history is read-only, resource-budgeted, redacted, and reports incomplete scans. See `docs/reference/history-search.md`. |
| `memory_history` | `extensions/memory-history/extension.ts` | `.pi/lib/state/shared-memory.ts`: repository-default immutable records under `~/.swarm/memory` (override `PI_SWARM_MEMORY_DIR`), Git-common-dir repository identity, worktree overlay and explicit global scope. Legacy session scope remains readable. Redact before writes. |
| `memory_history recall` | `extensions/memory-history/extension.ts` | `.pi/lib/context/knowledge-pageindex.ts` projects scoped records into read-only PageIndex sections; `.pi/lib/context/memory-agent.ts` calls an isolated Pi model whose only tools are `memory_browse`, `memory_read`, and `memory_select` from `.pi/lib/state/memory-retrieval-worker.ts`. The parent validates selected IDs against its snapshot. Candidate results are leads, never verified recall; writes still use explicit store operations. |
| `memory_history offer` | `extensions/memory-history/extension.ts` | `.pi/lib/context/memory-handoff.ts` accepts bounded explicit text, source and evidence; tool-less Pi consultation judges durable project knowledge, then the parent writes only a candidate through `KnowledgeStore`. A content/source-derived ID deduplicates retries. A read-only PageIndex projection acknowledges the exact saved revision as `stored-indexed`, otherwise `stored-index-pending` with a retry hint. No global or verified write. |
| `project_init`, `/init` | `extensions/project-init/extension.ts` | `.pi/lib/policy/project-scaffold.ts`, `.pi/lib/policy/project-structure.ts`. `/init` injects an interview brief via `sendUserMessage`; the agent interviews with `ask_user_question` and calls `project_init` (`inspect`/`plan`/`apply`). There is no modal wizard. `apply` never clobbers existing files. Like `memory_history`, this is an intentional model-facing extension tool, so `swarmSurfaceFor` must keep `project_init` on the surface — otherwise it registers but is gated out and the agent reports it as missing. |
| `bootstrap` tool, `/bootstrap`, native `/settings` → Bootstrap model | `extensions/bootstrap/extension.ts` | `packages/runtime/bootstrap/src/`; parallel/combined/off strategies, read-only model consultations, streaming tool renderer and startup guidance. Model inherits the current session unless overridden. Settings use Git common dir with non-Git `.swarm` fallback. Handoff invokes active Skill/TaskManage definitions through the registered policy hooks; both modes propose task reconciliation and load selected skills through the registered handoff; task changes require `commitTasks=true`, with `pi-swarm-bootstrap-task` recording retry dedup. Returned `loadedSkills` contains the instructions; `next.loadedSkillNames` identifies already-loaded skills, not work to invoke again. Plans remain proposals requiring repository verification. `.pi/lib/ui/bootstrap-settings.ts` augments the native SettingsList through an isolated, shape-checked compatibility adapter; no replacement `/settings` command or host-file edits. Recheck this adapter against Pi UI upgrades. |
| `skills_list`, `skill_view` | `extensions/swarm-skills/extension.ts` | `packages/context/skills/src/index.ts` and `.pi/lib/context/swarm-skill-registry.ts`; skill bodies/support files stay on disk. |
| Final Mermaid response repair and follow-up | `extensions/mermaid-response/extension.ts` | `.pi/lib/context/mermaid-response.ts` and `mermaid-followup.ts`; `message_end` replaces only successful finalized assistant text before persistence/final redraw. The streaming preview may already have shown the original. Mermaid fences are processed independently (up to 16); only one narrowly recognized flowchart arrow or sequence-message colon per diagram is changed after successful revalidation. Other syntax and validator failures preserve the original. For conceptual/explicit-diagram user requests, `turn_end` queues one model-visible `followUp` if a substantial final explanation lacks a Mermaid fence; it does not replace the previous response, and a second omission does not loop. Pi's terminal Markdown renderer displays Mermaid as code, not SVG connectors; connector color belongs in the actual diagram-rendering client. |
| `Skill`, `SkillManage` | skill/autogen integration via `extensions/swarm-skills/extension.ts`, `extensions/autogenskills/extension.ts` | `packages/context/autogenskills/src/index.ts`; mutate skills only through the vault/revision API. |
| `web_fetch`, `deepwiki`, `browser_get_page` | `extensions/research-tools/extension.ts` | Extension-local bounded evidence fetcher; provenance and network policy are coupled requirements. |
| `enter_plan_mode`, `exit_plan_mode` | `extensions/swarm-plan-mode/extension.ts` | `.pi/lib/context/swarm-plan-mode.ts`; plan approval is separate from implementation. |
| `/auto on|off|status` | `extensions/swarm-auto/extension.ts` | Opt-in session-local autonomous continuation. Contributes `auto:on/off` through the existing `pi-swarm-footer-segments` registry; it never replaces the footer slot. Ask-user now uses the replacement interactive questionnaire; the former ask tool’s automatic consultation is not supported. `before_agent_start` supplies bounded branch and tracked-worktree context; settled `agent_end` follows an explicit progress marker only while idle. No global provider limits or policy gates are changed. |
| `ask_user_question` | `extensions/ask-user/extension.ts` | Ask-user uses `eko24ive/pi-ask` at commit `49482b7e5d0d57be8af1db81f490f6e860792cfb` (MIT; LICENSE beside it). Private implementation, docs and tests live within `extensions/ask-user/`. The canonical tool name remains `ask_user_question` with the new questionnaire schema. |
| `control_plane_status` | `extensions/control-panel/extension.ts` | `packages/runtime/runtime-contracts/src/control-plane.ts`, `control-plane-store.ts`; dashboard is read-only. |
| `daemon_status`, task/run tools | `extensions/control-task-tools/extension.ts` | `packages/runtime/runtime-contracts/src/daemon-rpc.ts`, `control-task.ts`; unavailable daemon must fail closed. Daemon goal/loop APIs remain available to non-TUI consumers, but are not registered in the TUI. |
| `scheduler`, `/goal`, `/loop` | `extensions/swarm-goal/extension.ts` | `.pi/lib/tools/swarm-goal.ts`; session-local scheduling and evidence-based goal continuation without a turn cap. Replaces the separate CronCreate/List/Delete and ScheduleWakeup TUI tools. |
| Session wake-up delivery | `.pi/lib/runtime/session-wakeup.ts` | Background-agent completion and swarm-goal share custom-message delivery with triggerTurn, generation guards, and shutdown invalidation. No runtime imports from vendor. |
| `vault_add`, `vault_approve`, `vault_exec`, `vault_list`, `vault_two_person_status` | `extensions/swarm-history-vault-tools/extension.ts` | `.pi/lib/tools/swarm-vault-tools.ts`; never expose secret values. |
| `vault` | `extensions/vault/extension.ts` | `.pi/lib/tools/swarm-vault-tools.ts`; transparent global credential storage, with explicit user-risk warning. |
| `mcp__<server>__<tool>` | `extensions/mcp-fallback/extension.ts`, `extensions/swarm-websearch/extension.ts` / `packages/tools/mcp/src/index.ts` | Provider-neutral declared MCP bridge; manifests, allowlists, transport, and auth are the seam. |
| `annoyed` | `extensions/annoyed/extension.ts` | `extensions/annoyed/store.ts`, `.pi/lib/tools/swarm-annoyed-routing.ts`, and `swarm-annoyed-publish.ts`; a tool-less ownership judge routes project reports only to a verified active-workspace GitHub remote and Trebol triages upstream/non-actionable/uncertain/judge-failure/missing-remote cases. Publication receipts are SQLite metadata with a stable remote marker; ambiguous results are never blindly retried and SQLite/GitHub is not exactly-once. Transcript stays local; nudge behavior remains separate. |

Model-facing workflow wording is adapted by
`packages/context/prompt/src/workflow-guidance.ts` in prompt presets, Forge
assembly, and tool-description loading. Upstream prompt assets and tool captures
remain reference snapshots; local wording intentionally differs where blanket
workflow restrictions would prevent useful work. Preserve tool limits, approval
requirements, and data-integrity rules when changing this adapter.

Names may be filtered by active-tool policy. `swarm-tools-status` and
`system-inspector` show the runtime's actual registered/active surface; use
those instead of assuming every row above is enabled.

Paseo integration is currently Linux-only for managed process ownership checks
(`.pi/lib/tools/paseo-setup.ts`, adapted by `extensions/paseo/extension.ts`). New launches default to loopback and use
per-user state under `$XDG_STATE_HOME/pi-swarm/paseo` (default
`~/.local/state/pi-swarm/paseo`). Existing owned PID records are checked before
launch, subprocesses are bounded, and daemon health includes HTTP validation.
`/paseo setup inspect` only inspects Tailscale. Explicit `/paseo setup apply` (or a tool
setup call with `apply: true`) discovers the machine hostname, merges `daemon.hostnames`,
hot-reloads the daemon, and creates an unoccupied persistent tailnet-only Serve
route. Session startup only starts/checks the daemon, never changes network
exposure. `/paseo setup apply` retries the same operation. Existing conflicting routes or
Funnel exposure fail closed; matching routes are reverified without replacement.
Success requires HTTPS health plus a WebSocket hello/status/pong handshake.
`PASEO_HOME` is injected into managed launches. Config writes are atomic and mode
0600, anchored to verified Linux directory descriptors; setup/start/stop/update/build
share a lock. Update/build refuse live daemon records; stop retains records until
termination is verified. Interrupted locks require inspection, not age-only
automatic stealing. This is not yet an unattended cross-platform
installer or boot-time service. Fresh global package clones do not include a
built Paseo submodule. Validate with `npx vitest run .pi/test/tools/paseo.test.ts`;
those tests do not prove process supervision or cross-device connectivity.

The PageIndex-style context adapter registers `context_index`, `context_remember`,
`context_reindex`, `context_search`, `context_outline`, `context_read`,
`context_inspect`, and `context_delete`; these are retained by the default tool
surface. Session reload accepts Pi custom-entry envelopes. Retrieval consultations
use tool-disabled Pi subprocesses; selected sections are materialized by the
parent. `context-consult.ts` limits elapsed time and accepted output, but Pi's
exec API buffers child output internally (peak memory is not streaming-bounded).
The context index still has session scope; cross-session knowledge integration
is unfinished. `.pi/lib/state/knowledge-store.ts` and
`.pi/lib/context/knowledge-capture.ts` are tested integration components. Bootstrap now uses
`knowledge-recall.ts` to project verified durable records into cited PageIndex
reads; candidate records are excluded, storage failure is distinct from no match,
and recall now ranks all current scoped records from a single event-log projection before applying the response cap. `memory_history` supports `get` by scoped ID and status-filtered search for
candidate review. Review instructions require source evidence and later
corrections before revision-checked verification; extraction alone is not proof.
New non-session `memory_history` writes now use this store; legacy records remain
readable and labeled unverified. Correction/deletion require revision checks.
Agent global writes fail closed. `/memory-promote repository|worktree ID [namespace]`
requires interactive confirmation of a verified record and rechecks its revision
before making an explicitly approved global copy; the project record stays intact. Lifecycle candidate capture is registered in `knowledge-enrichment.ts`: on
`agent_settled` and `session_before_compact`, it processes new visible evidence,
persists a `pi-swarm-knowledge-enrichment` cursor, and saves cited candidates.
It excludes reasoning/runtime reminders and suppresses child-agent capture.
`/memory-capture on|off|status` controls this per session;
`PI_SWARM_MEMORY_CAPTURE=off` disables it initially. Consultations are awaited
and may add up to their timeout to turn completion. `candidate-memory-review.ts`
reviews at most one pending record per settled turn (or `/memory-review`) using
`PI_SWARM_MEMORY_REVIEW_MODEL` (default `clover-plexus/luna`). It reads only cited
Pi session entries, requires a bounded model verdict with a source-matching
quote, and corrects the existing record by exact revision; failed review leaves
it candidate. This is a model judgment, not independent proof, and it never
writes global scope. `task-candidate-capture.ts` projects TaskManage's current
task snapshot into worktree-scoped candidates: each record includes every task
question, answered or unanswered, plus answer/evidence when present. Repeated
snapshots are idempotent and changed answers create revisions; neither capture
nor a task's completion verifies a memory. Unverified candidates remain excluded
from bootstrap recall. `context_search` can report candidate-leads on a session
index miss, with Q&A projected as Markdown sections rather than raw JSON.
`memory_history` operation `recall` invokes a bounded read-only memory agent
using `PI_SWARM_MEMORY_RETRIEVAL_MODEL` or the session model (Luna fallback),
and returns selected source cards, not the child agent's prose. The sibling
`memory_history` operation `ask` uses the same scoped PageIndex projection and
isolated browse/read/select child, but requires the child to submit a bounded
answer citing its read/selected card keys. The parent validates the receipt
and returns the answer alongside original record IDs, revisions, statuses and
citations. An empty index or unsupported answer returns no answer; a failed or
malformed child is reported distinctly. Candidate cards remain unverified
leads; `ask` neither writes nor changes bootstrap's verified-only recall.
When the isolated child submits a receipt but later exits nonzero or times out,
the parent still validates that committed receipt; without one, it returns
`unavailable`. Abort and session-generation checks still reject stale results.
The `ask`/`recall` model-visible text now leads with outcome and answer (or
explicit no-answer), then bounded source-card IDs, status, excerpts and
citations; structured `details` retain the full prior retrieval result.
Other memory operations retain their JSON text. The tool's TUI-only renderer
shows a compact outcome/answer and an expanded source view without repeating
the raw JSON or changing retrieval state. Reload to update an active session.
The opt-in synthetic `live-answer.mjs` run enables child-only metadata tracing
(`PI_SWARM_MEMORY_RETRIEVAL_TRACE=1`) and saves offered cards, ordered
browse/read/select events, receipt, exit and final tool response under ignored
`artifacts/memory-retrieval/live-answer-*/results.jsonl`. Normal retrieval
does not emit these traces; they are not added to model-visible results.
`offer` lets the main agent hand explicit “remember that” content to a
tool-less reviewer for a durable candidate and indexed acknowledgment. Existing
`get` and other write operations retain their contracts. `search` now accepts
reordered queries when every distinctive term occurs in the same record (as
well as exact phrase matches); it does not synonym-expand or automatically
include another scope. `recall` ranks strict PageIndex section matches first
and fills unused candidate slots with partial matches containing at least two
distinctive terms; the read-only agent still decides whether they support the
query. A typo or synonym without lexical overlap may still return no result.
Historical backlog review is incremental. Store path checks reject
existing symlinks; hostile concurrent ancestor replacement is outside its current
filesystem guarantees. No live backfill is implied by these library tests.

Memory/skill semantics are shared in `.pi/lib/context/memory-guidance.ts`.
Memory stores project/domain knowledge, decisions and rationale, preferences,
and contextual facts (including how a project operates); skills store reusable
agent procedures. Mixed observations must be separated, not copied wholesale.
Repository is the shared-memory default; worktree holds unmerged facts, session
holds local context, and global requires explicitly shareable cross-project
knowledge. Do not automatically duplicate facts across scopes. The PageIndex-style
context extension currently retains its namespace/workspace/session boundary.

Bootstrap selects at most two distinct skills (preferring one), enforces the cap
in selector validation and orchestration, and invokes them before task drafting.
The planner receives bounded, explicitly truncated instruction excerpts; the
main agent receives up to 12,000 characters per skill with explicit truncation
and an exact full-output spill file for further reads, without another invocation.
This preview limit does not cap orchestrator tool calls or continued investigation.

Bootstrap shared-memory recall uses task-token ranking across text and tags before
its candidate cap (`recallShared`); explicit memory search retains substring
semantics. Bootstrap handoff asks for evidence-backed knowledge enrichment after
verification, not automatic transcript ingestion. No new learning is a valid
outcome. Historical backfills must be reviewed, scoped, and redacted before
promotion; experimental stores use `PI_SWARM_MEMORY_DIR`.

`/mem on|off|status` persists bootstrap enforcement per repository. On adds a
memory ceremony to the final assembled system prompt and blocks ordinary tool
calls until bootstrap succeeds in the current session. Bootstrap and user/plan
interaction remain available; `/mem off` is the recovery escape. Bootstrap is
exempt from skill-budget gates, but selected skills still require real invocation
to activate the working budget. Reload resets readiness, not the persisted mode.

### Skills and their locations

| Skill source | Location | Owner / notes |
| --- | --- | --- |
| Builtin `loop` | `packages/context/skills/builtins/loop/SKILL.md` | `packages/context/skills/src/index.ts` loader; progressive disclosure. |
| Builtin `swarm-skill` | `packages/context/skills/builtins/swarm-skill/SKILL.md` and `references/SKILL-AUTHORING.md` | Skill authoring contract. |
| Builtin `swarm-workflow` | `packages/context/skills/builtins/swarm-workflow/SKILL.md` and `references/` | Workflow schema, profiles, examples, strategies, troubleshooting. |
| `ask-user` | `extensions/ask-user/skills/ask-user/SKILL.md` and `references/` | Carried with the forked `ask_user_question` extension; deliberately not registered as a builtin so the parity-compared `<available_skills>` block is unchanged. |
| Autogenerated/project/user/managed/install skills | Runtime search paths resolved by `packages/context/skills/src/index.ts` | Do not hard-code paths; `swarm-skills.ts` exposes source and support files. |
| Autogenerated skill lifecycle/revisions | `extensions/autogenskills/extension.ts` + `packages/context/autogenskills/src/index.ts` | Curator, locks, budgets, review/absorb/archive/pin. |
| Local harness skill | `.swarm/skills/pi-harness-engineering/SKILL.md` | Runtime state/configuration; do not mistake it for a builtin package skill. |

If changing skill discovery, loading, precedence, progressive disclosure, or
support-file safety, change `packages/context/skills/src/index.ts` and its tests first; change
the Pi adapter only for registration/presentation concerns.

### Hooks and lifecycle locations

| Hook group / concern | Registration | Implementation / events |
| --- | --- | --- |
| Central hook state and ordering | `.pi/lib/runtime/hook-state.ts` | Registration, enablement, persistence, and visibility. |
| Same-session hook correction | `.pi/lib/runtime/hook-correction.ts` | Authoritative tool_call denial with same-step feedback and bounded repeat escalation. No speculative abort or settlement wake. |
| Prompt hook `packages/context/prompt` | `extensions/swarm-prompt/extension.ts` | `before_agent_start`; prompt/context assembly in `packages/context/prompt/src/index.ts` and `.pi/lib/context/swarm-context.ts`. |
| Disk hooks `disk-hooks` | `extensions/swarm-disk-hooks/extension.ts` | Loads project/user hook config, executes bounded commands; maps tool/session/prompt/compact events. |
| Structure guard `structure-guard` | `extensions/project-init/extension.ts` | `tool_call`; denies `write`/`edit`/`apply_patch` *creations* that violate `.project/structure.json` before the tool runs. Policy and classification in `.pi/lib/policy/project-structure.ts`, tool-argument extraction in `structure-guard.ts`. Existing paths are never blocked; `PI_SWARM_NO_STRUCTURE_GUARD=1` disables it like `PI_SWARM_NO_HOOKS`. |
| Inline hook presentation | `extensions/hooks/extension.ts`, `.pi/lib/runtime/hook-render-bridge.ts`, `.pi/lib/runtime/hook-presenter.ts` | UI only; never make governance depend on rendering. |
| Annoyance/nudge | `extensions/annoyed/nudge.ts` | `tool_result`, `turn_end`; persistence in `annoyed/store.ts`. |
| Task enforcement | `extensions/taskmanage/extension.ts` | `packages/tools/taskmanage/src/task-hooks.ts`, `swarm-hook-runtime.ts`; task state is authoritative in taskmanage. |
| Metrics/cache telemetry | `extensions/conversation-metrics/extension.ts`, `cache-telemetry.ts` | Agent/message/provider lifecycle; persisted telemetry is non-secret. |
| Theme/thinking/UI lifecycle | `extensions/swarm-themes/extension.ts`, `swarm-thinking.ts` | Presentation/config only. |

Task stop-time reconciliation lives in `.pi/lib/runtime/swarm-builtin-hooks-runtime.ts`.
After observed work and a normal stop, it may request one follow-up per external
input for an unowned, focused, dependency-ready task. Errors, questions, interaction
tools, observed background dispatch, queued messages, plan mode, subagents, and
shutdown suppress the wake. It never completes or deletes tasks automatically;
pending/backlog work is not a reason to restart. The allowance resets on session
start or interactive/RPC input, not on automatic continuations.

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
npm run doctor                    # global-install health: PATH, dist, deps, models.json, theme
node tools/repo/rewrite-imports.mjs --check   # every relative specifier resolves
npm run dogfood                   # build + test
```

### Installing globally

The repository is itself a Pi package (root `package.json` `pi` manifest:
individual extension entrypoints and `.pi/themes`). Never copy `extensions/`
anywhere — the extensions import `.pi/lib` and `packages/*/src` by relative
path (tests import `tests/fixtures`) and need the hoisted `node_modules`.

```sh
# dev box: link this checkout (no copy; dedupes against .pi/ by absolute path)
pi install /home/swarm/Work/Pi-Swarm
# any other machine (pin a tag or commit; `pi update` reconciles the ref)
pi install git:github.com/cloverinternational/trebol@<tag>
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
  `devDependencies`;
- nothing under `vendor/` is imported at runtime (submodules are empty in a
  package clone); `ask_user_question` is the in-tree fork for that reason;
- on a machine that also opens this repo as a project, use the local-path
  form — a `git:` install has a different absolute path and would register
  every tool twice.

Prove a change against the package path, not just the project boot.
Host-level items (node on the
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

### Compact TaskManage completion questions

Tasks may carry 1–12 `questions: [{id,text}]` (IDs ≤64, text ≤240).
Complete them with `answers: [{question,answer,evidence}]` in the same update;
answers are ≤240 characters, evidence references ≤512. Use workspace-local
`file.md#heading` or `file#Lx-Ly` references for detailed proof. Missing answers,
unknown IDs and unavailable evidence reject completion. Reference availability
is not semantic verification. Legacy questionless tasks remain compatible.
Bootstrap carries questions into task proposals and committed operations. No
separate question lifecycle or follow-up tool-call limit is introduced.

## Jev first-finder knowledge audit (opt-in)

`/jev-audit on|off|status|now` controls the session audit; environment
`PI_SWARM_JEV_AUDIT=on` enables it initially. `TYPESAFE_API_KEY` is resolved from the environment first, then the exact vault
credential `typesafe-api-key` through the existing internal resolver (never tool
parameters). `PI_SWARM_JEV_CREDENTIAL_ID` overrides the ID;
`PI_SWARM_JEV_VAULT=off` disables fallback. Values are never put in audit evidence. The state-layer adapter
`extensions/jev-knowledge-audit/extension.ts` counts completed Pi turns and
audits at the next turn start after five new turns, and on agent_end for final evidence. Stop audits process one bounded batch and never wake the agent; remaining backlog is retained. Domain collection/API/review
logic is in `.pi/lib/context/jev-knowledge-audit.ts`; mode coordination lives in
`jev-audit-mode.ts`. Persisted `pi-swarm-jev-audit` entries hold cursor/count and
bounded redacted pending review; `pi-swarm-jev-review` context messages are
untrusted proposals, injected once without a wake-up turn.

Jev is the first classifier, not a generator or verifier. At most three bounded
new excerpts per audit are routed as memory, procedure, mixed, temporary, unresolved,
or noise. Backlog remains for later audits. Main agent review and existing
revision-checked memory_history/SkillManage tools own all actual writes. Skills
are audited only when their visible invocation/view output is encountered; no
whole-library automatic scan or rewrite. Mandatory instructions are not removed.

Existing generative knowledge-enrichment capture defers while Jev is enabled;
off preserves its old behavior. A three-second deadline, session cancellation,
child suppression, exact citations and bounded request/response protect the loop.
API failure keeps evidence retryable and never blocks ordinary work. Missing
compacted cursors pause audit rather than guessing. `/jev-audit now` queues review
for the next model context; it does not start a new turn. Reload/new session is
required to load newly installed extension code.

Focused validation: `npx vitest run .pi/test/context/jev-knowledge-audit.test.ts
.pi/test/state/jev-knowledge-audit.test.ts .pi/test/state/knowledge-enrichment.test.ts`.
Live API test is opt-in: `PI_SWARM_JEV_LIVE_TEST=1 npx vitest run
.pi/test/context/jev-live.test.ts` with the key in the environment.

## Budgeted Jev-triggered memory maintenance

Enable `/jev-audit on` then `/memory-worker on`. `/memory-worker off|status`
controls the parent-session queue. Defaults: one worker at a time, up to four
queued Jev packets, six new child model turns, 90-second wall deadline. The main
agent remains free to work. No Harbor or global memory/skill edits are involved.

`extensions/memory-maintenance/extension.ts` owns queue/cancellation/receipts;
`.pi/lib/context/memory-maintenance-runner.ts` snapshots the active visible branch
and invokes actual Pi `--fork` into a separate child session. Snapshots are
redacted, omit reasoning, preserve source IDs, are capped at 2MB, and remain under
ignored `.pi/agent-sessions/` for evidence links. No parent branch switch occurs.
The child loads only `.pi/lib/state/memory-maintenance-worker.ts`, with no normal
extensions/tools. Its allowlist is `memory_evidence` and restricted `memory_history`.

The worker must search first, read cited snapshot evidence before verified writes,
and use expectedRevision for corrections. Repository/worktree only; global/session
writes, deletion, nested agents, shell, code edits and SkillManage are unavailable.
Verified status is attributed agent judgment, not proof from Jev. Procedure findings
remain unresolved skill proposals rather than autonomous skill edits. Cancellation
revokes a write lease and terminates the subprocess; completed prior writes remain
valid history. Interrupted/failed runs may contain partial writes: inspect before
retrying. A filesystem lease check is not a cross-process transactional barrier.

`.pi/lib/context/memory-query-budget.ts` tracks successful repository/worktree
`memory_history` search/get/replay calls by ID and result. Successful empty lookup
counts; failures, writes and other scopes/tools do not. After ten completed main
turns without lookup, one soft reminder is injected. It rearms on successful lookup
or a new interactive/RPC input; it never blocks tools or starts a new turn.

Worker completion is a bounded operation receipt at the next natural context
boundary, not an unsolicited continuation. Jev packets are reviewed by the worker
when enabled, otherwise by the main agent. Session replacement cancels stale work.
Reload may restart an interrupted queued item; store dedup/CAS limit repeated writes.

Validation: focused `memory-maintenance*` and `memory-query-budget` Vitest tests;
`node tools/experiments/jev-audit/memory-worker-smoke.mjs` exercises real installed
Pi fork/evidence/write/query against a local scripted provider and isolated store.
It does not establish unscripted reviewer accuracy or quality improvements.

### Staged historical review mapping

`tools/experiments/jev-backfill/project_mapping.py` uses the existing Git-common-dir
repository/worktree identity; missing workspaces remain unresolved. Explicit alias
files are reviewable mapping evidence, never guessed from names. `review_staged.py`
uses the restricted memory worker with an explicit staging store, fixed six-turn/
90-second limits, and durable max-ten-job reservation ledger. No live promotion.
Memory maintenance uses namespace `default` only so bootstrap can discover results;
project isolation is provided by repository/worktree identity, not model-invented
namespaces. See `docs/reference/jev-backfill-project-review.md` for references,
current pilot counts, provenance, and unresolved coverage limits.

### Operational supervisor (initial opt-in slice)

`/jev-supervisor on|off|status` adds bounded work-freshness and review-need questions
to Jev's existing call. Findings go to the independent memory worker when enabled.
Worker tools now additionally expose `supervisor_review` (confirm/reject/insufficient
with read evidence) and `supervisor_task_proposal` (proposal only). Parent-owned
reconciliation in `.pi/lib/context/supervisor-task-reconcile.ts` requires
`PI_SWARM_SUPERVISOR_TASK_APPLY=on`, current snapshot validation, active TaskManage,
and existing policy hooks. No completion/deletion or vault permission changes.

Jev and maintenance adapters share mode/dispatch by exact session identity, not
ExtensionAPI object identity. Autogen writes a fresh state entry on session_compact;
this does not reset budgets. Full live compaction/reviewer reconciliation still
needs dogfood. Skill cleanup remains an isolated preview, not a global migration.
See `docs/architecture/jev-operational-supervisor.md` for explicit unfinished seams.

Credential references for memory are projected by
`.pi/lib/context/vault-memory-evidence.ts` only from explicit vault-list results.
Only ID, purpose/name, scope and kind are eligible; get/add/exec values and opaque
message details are excluded from Jev evidence and maintenance snapshots. A listed
credential is not proof of project usage or execution authorization. No new vault
permissions or autonomous credential inventory scans are introduced.

Maintenance review policy is loaded from
`.pi/lib/context/prompts/memory-review-policy.md` (adapted evidence-prefix review
from Dream-RSI Appendix B.2, not a replay simulator). The runner records its hash.
Unscripted dogfood entry: `tools/experiments/jev-audit/dogfood-integrated.mjs`, using
configured provider authentication and isolated fixture/memory/skill state.
A headless parent can finish before maintenance completion is reconciled; persisted
`running` alone is not proof of live work. Inspect actual receipts/store on recovery.

## Unified Supervisor surface (supersedes earlier slash-command instructions)

Use only `/supervisor` for Jev audit, operational review and memory-worker controls.
`/jev-audit`, `/jev-supervisor`, `/memory-worker` registrations are removed, not aliases.
`/supervisor status` reports sanitized settings and adapter status. The compact
setWidget projection does not replace the single metrics-owned footer.

UI adapter: `extensions/supervisor/extension.ts`; shared session control:
`.pi/lib/context/supervisor-control.ts`; validated settings: `supervisor-settings.ts`.
Global defaults: `~/.swarm/config/supervisor.json`. Per-project overrides:
Git-common-dir `pi-swarm/supervisor.json`, or non-Git `.swarm/supervisor.json`.
Explicit project false overrides global true. No secret values accepted in settings.

Jev credential auto resolution: configured TypeSafe provider via Pi model registry,
then named environment variable, then named vault credential. Explicit provider/env/
vault modes do not fall back to other sources. Default references: `typesafe`,
`TYPESAFE_API_KEY`, `typesafe-api-key`. Endpoint remains fixed TypeSafe HTTPS.
Reviewer inherits current session model or configured provider/model override.

Panel changes apply to loaded audit/worker adapters. Pause cancels active work and
clears pending proposals. Worker caps remain at most6turns/90s; cadence1–50turns.
Connection test is explicit paid bounded call; credential presence is not health.
Skill mutation remains preview-only in this panel; no fake enable switch.
Existing session enabled entries no longer override persisted settings. Legacy
PI_SWARM_JEV_AUDIT=on boot flag applies only when no global/project master setting
exists. New code needs reload. Tests: supervisor-settings, jev-credential, UI
supervisor, and audit/maintenance lifecycle suites.

### Task enforcement correction

The canonical TaskManage hook pipeline now defaults to `block`, not `off`:
acting tools require a focused in-progress task; TaskManage, planning, interaction,
skill and read-only recovery paths remain available. The secondary coordinator
stays silent to avoid duplicate hooks. Explicit hook-off policy remains honored.
`node tools/experiments/jev-audit/task-enforcement-smoke.mjs` proves actual Pi
blocked write -> TaskManage create -> successful write in a temporary workspace.

Supervisor operational-review settings now also control reviewed parent task
application; the hidden PI_SWARM_SUPERVISOR_TASK_APPLY flag is no longer required.
Independent confirmation, live snapshot checks and owning TaskManage hooks remain
mandatory. Project `enabled:false` still disables audit/worker, not the normal
task-enforcement hooks. Reload is required for already-running sessions.

### Durable skill-budget enforcement

Autogen `gateTool` enforcement no longer depends on `modelContext`: disabling
manager-generated prose in the Pi adapter does not disable execution gating.
In auto mode, after task focus, the durable manager charges non-exempt calls
(including Bash), blocks the next call at5 onboarding/90 working calls, and
resets through actual successful Skill invocation. SkillManage list/view alone
cannot refill this budget. Recovery/task/interaction/read tools remain exempt.
The built-in skill-budget hook now reads the durable manager's gate and status,
so its model-visible block names `Skill` and explains that `SkillManage` create,
patch, view, or review does not refill: create if appropriate, then invoke.
Read-only Bash still counts, while a blocked call is not charged. The builtin
lifecycle skill-review reminder remains separate from the execution budget;
explicit autogen manual/off modes remain distinct.
Verify real host: `node tools/experiments/jev-audit/skill-budget-smoke.mjs`.

### Human handoff on unfinished stop

ask_user_question uses the ask-user questionnaire schema and explicit submit/cancel
flow. The old timeout argument and dialog fallback are not part of this schema.
Run `npm run test:ask-user` for the extension-local Node test suite.

After observed work and a normal stop, taskmanage's canonical turn_end hook now
considers unowned pending/in-progress tasks (including blocked tasks), reconciles
actual progress, and instructs one ask_user_question call about remaining work.
A prose question alone does not replace the tool. The once-per-external-request
allowance, abort/error/user-interaction/background-work/shutdown and plan/child
suppression remain. No auto-completion, deletion, or infinite continuation.
Headless unavailable interaction must be reported with pending tasks retained.

### Bootstrap task-first recovery

Bootstrap now establishes an active task before fallible model consultation, or
reuses the existing active task. It commits validated task reconciliation by default;
legacy commitTasks is accepted but no longer disables task creation. Deterministic
request keys avoid duplicate seed tasks on retry. Model failure does not prove
bootstrap readiness; it leaves the created task and a visible error for recovery.

The memory ceremony exempts TaskManage and normalized ask_user_question/ask_user
interaction names, plus bootstrap and plan controls. Other gates still apply.
Bootstrap renderer falls back for empty details rather than printing undefined.
This renderer fix alone does not diagnose why a particular provider consultation
failed. Reload required; inspect the returned explicit bootstrap failure if it persists.

### Temporary runtime review logs
Bootstrap and Jev write bounded metadata under the active Git worktree root's
`.swarmpi/execution/` and `.swarmpi/jev/` (cwd for non-Git projects). Bootstrap
records correlated starts/stages/results/failure stage, selected memory IDs and
skills/task mappings. Jev records attempt/outcome counts, not raw evidence or
credentials. Best-effort writes must never break execution. Seven-day retention
on next write, 500 files/category, 32KiB/file. Gitignored, safe to delete; logs are
not memory and do not backfill earlier runs. Reload required for instrumentation.

### Memory capture quality, first curation batch
Lifecycle capture supplies project identity and a bounded sample of existing
records to discourage semantic duplicates. Its prompt excludes transient execution
history and asks for explicit project ownership. These are model guidance, not a
semantic enforcement guarantee. Saved candidates emit metadata receipts under
.swarmpi/execution. Restricted reviewer policy uses the same durability filter.
First code-reviewed worktree records document verified-only recall and the exact
supervisor configuration location; tools/experiments/memory-curation/save-reviewed.ts
writes them through the canonical store and checks production recall. This does not
bulk-clean existing candidates, enable supervisor, or certify unrelated records.

## Installed MCP adapter ownership

When `pi-mcp-adapter` is installed (its `/pi-mcp` command is present), it owns
`/mcp`, MCP connection negotiation, resources, and prompts. Pi-Swarm skips its
fallback startup discovery; the fallback diagnostic command is `/swarm-mcp`.
Pi-Swarm's surface filter preserves already-selected registered MCP tools,
including adapter definitions labelled `MCP:`, but does not reactivate tools
excluded by the user's selection. Adapter configs use literal `headers` (or
its explicit credential-reference syntax), not Pi-Swarm's custom `apiKey` fields.
Never print header values during diagnostics. Vendor example configs are not
runtime configuration sources.

### Install recovery diagnostics

`tools/install/doctor.mjs` checks runtime dependencies including `re2-wasm`
and all three prepare-built runtime packages. Its package-source helper
(`tools/install/package-sources.mjs`) resolves local settings paths and flags
historical Trebol/Swarm Git installs alongside a development checkout. A Git
clone is not the same resource identity as the local checkout: keep only one
registration to avoid duplicate tools. Run `npm install` in a fresh checkout
before opening Pi; local package registration alone does not prove dependencies
or dist outputs exist. Regression tests: `tests/install-package-sources.test.ts`.

Installation layout is documented in `docs/reference/install-layout.md`.
`npm run check:install` uses `tools/install/check-load.mjs` to load the package
with isolated temporary settings, without starting a session or invoking a model.
`tools/install/pi-host.mjs` resolves the SDK belonging to the PATH executable
(or `PI_EXECUTABLE`); the toggle session probe shares this resolver. No hardcoded
global npm prefix. This executes extension factories, not a security sandbox;
it does not certify individual tools or real-user resource filters.

### Flat extension migration (worktree)

Public entrypoints are `extensions/<name>/index.ts`; implementations and private
assets live beside them. Root manifest ordering is explicit for compatibility,
not a dependency injection mechanism. `control-task-tools` owns its daemon
client and registrations; `mcp-fallback` owns fallback MCP and the legacy
`/swarm-runtime` diagnostic; `swarm-agent-tools` owns both Agent/AgentControl and
legacy agent tools with one manager. Only `swarm-prompt` registers prompt hooks.
Only `vault` registers the vault tool; history does not silently enable it.
No legacy layer manifests remain. Existing user resource filters require the
path migration in `tools/repo/extension-moves.json`; personal settings are not
automatically rewritten. Shared `.pi/lib/runtime` helpers remain and are not
claimed to be eliminated by this migration.

Installation verification supports `npm run check:install -- --extension
extensions/<name>/index.ts` for isolated entry loading. The preview-only settings
migration command is `node tools/install/migrate-extension-paths.mjs
<settings.json>`; it never writes configuration and flags legacy glob filters for
manual review. See the independent-extensions architecture record for latest
verification and unresolved macOS shell / task-correction smoke failures.

Headless task correction uses the normal Pi tool_call rejection/result path;
stream-time speculative abort is TUI-only. Validation:
`.pi/test/runtime/hook-correction-headless.test.ts` and
`node tools/experiments/jev-audit/task-enforcement-smoke.mjs` (isolated local
scripted provider; requires no provider credentials).

Native settlement now owns final notification: `.pi/lib/runtime/agent-settled.ts`
retains the last attempt payload but schedules no timers. Do not reintroduce a
quiet-period completion heuristic. Bootstrap tool calls prefer ctx.executeTool;
lifecycle supervisor dispatch retains the compatibility path explicitly.
macOS background-shell streaming uses `.pi/lib/tools/pty-runner.py` with optional
Python3 and a stdbuf/direct fallback; Linux uses util-linux script. Test the real
shell suite after any runner change. Local complete-suite validation on macOS
uses canonical TMPDIR=/private/tmp to avoid /var symlink-path fixture failures.

Auto and goal continuation use `agent_before_settle` drafts + continue:true;
final settlement is observational. Preserve existing boundary drafts and do not
require pre-draft canContinue: adding a custom message establishes valid context.
Real-host check: `node tools/e2e/auto-boundary.mjs` (loopback scripted provider,
isolated settings, no paid API). Scheduler timers still use session wakeup.

Correction now uses authoritative tool_call rejection in every mode; speculative
streaming abort previews and settled wakeups are removed. Bootstrap handoff
compatibility registrations are session-scoped and disposed on shutdown; native
tool-context calls use Pi executeTool. Supervisor's non-tool-context fallback
still sees only Trebol-registered hooks and remains explicit compatibility debt.
Bootstrap settings patch disposal restores owned render methods and injected rows.

Fallback hook dispatch is session-scoped, preserves multiple handlers per group,
and removes owner registrations on shutdown. It rejects missing session identity.
Ctrl+N adapter installation returns a release lease; last owner restores its
patch, without clobbering later adapters. Tests: session-hook-dispatch and
 trebol-shortcut under `.pi/test/runtime/`, plus actual-host toggle smoke.

PR 136 review dispositions and validation are recorded in
`docs/architecture/pr-136-review.md`, including intentionally fail-closed session
identity and preserved host-adapter constraints. Install source parsing shares
one Git identity parser with doctor; migration preview covers top-level extension
paths as well as package filters. Host lookup supports Windows npm shim layout
with suffix tests; live Windows verification remains separate.
