# Plan: map Swarm TUI/Forge to Pi plugins

Status: archived — produced `docs/architecture/swarm-tui-to-pi-map.md`; the
extension inventory in `AGENTS.md` is now authoritative.

## Goal

Create an evidence-backed architecture map for a 1:1 replication of the Swarm
Forge prompt and SwarmOS TUI harness behavior as a Pi plugin set. This phase
does not implement plugins.

## Low-level decomposition

### 1. Source boundary

- Need: canonical Swarm source, canonical Pi source/docs, and the empty
  Pi-Swarm destination.
- Expect: exact repository paths, branch/dirty-state evidence, and an explicit
  distinction between observed code and proposed Pi seams.
- Logic: enumerate entrypoints and registrations first; then read the
  implementations and tests that prove runtime reachability.
- Breakpoints: duplicate/stale worktrees, generated binaries, hidden config,
  and documentation that describes an older architecture.

### 2. Startup and prompt construction

- Need: `cmd/swarmos/main.go`, `internal/chat/app_init.go`,
  `app_initial_prompt.go`, system-prompt settings, context loading, and
  `harness.Plan`/`client.WithHarnessPlan`.
- Expect: a sequence from argv/config to effective provider, model, prompt,
  workspace, tools, permissions, hooks, and first user message.
- Logic: interactive mode builds a visible Bubble Tea shell first and creates
  the SDK behind the first frame; an initial prompt is consumed exactly once
  and routed through the normal send path. Harness mode replaces ambient
  configuration with a closed immutable plan.
- Breakpoints: prompt precedence, context injection, OAuth provider prefixes,
  resume IDs, yolo opt-in, and accidental ambient fallback.

### 3. Agent/tool/hook execution

- Need: `internal/agent`, `internal/tools`, `internal/hooks`, builtin hooks,
  Forge tool registrations, permission checks, MCP, subagents, and compaction.
- Expect: a per-turn event and data-flow map with tool input/output/error,
  hook ordering, permission posture, cancellation, and stream normalization.
- Logic: provider stream produces assistant/tool events; the agent executes
  selected tools; hooks gate/observe before and after execution; results return
  to the conversation and stream subscribers; compaction can alter the next
  request context.
- Breakpoints: hook payloads do not include assistant prose, sibling tool-call
  ordering, failed-result propagation, permission-vs-registration confusion,
  and token/cache double counting.

### 4. TUI state and rendering

- Need: `internal/chat` App state, Bubble Tea Init/Update/View, message types,
  tool-render registry, settings/commands, overlays, image support, and
  attach/control paths.
- Expect: a presentation map separating durable semantic events from
  Bubble-Tea-specific widgets, frame caching, viewport coordinates, and
  user-input routing.
- Logic: external/runtime events enter an update queue, the event loop reduces
  them into App/message state, caches are invalidated, and View composes the
  current screen plus modal overlays.
- Breakpoints: concurrent callbacks, stale frames, width-sensitive wrapping,
  ANSI/grapheme coordinates, native image anchors, and UI-only state being
  mistaken for execution state.

### 5. Harness/automation/persistence

- Need: `cmd/swarmos` harness CLI, headless printer, automation server, ACP,
  attach/serve contracts, conversation/session stores, and tests.
- Expect: machine-facing contracts for validate/explain, stream-json,
  attach/resume, control sockets, ACP, and restart-safe identity.
- Logic: compile/validate a plan, construct the runtime, subscribe to updates,
  execute `ChatCtx`, serialize output/events, and persist conversation/session
  state.
- Breakpoints: exact active conversation identity, workspace boundaries,
  control-socket publication races, output contamination, and process
  replacement.

### 6. Pi translation matrix

- Need: Pi extension API and event semantics from
  `packages/coding-agent/docs/extensions.md`.
- Expect: each Swarm behavior classified as exact Pi primitive, adapter
  implementation, Pi core dependency, or not yet supported.
- Logic: preserve semantic contracts first (prompt/tool/hook/permission/state),
  then map TUI-only rendering to Pi widgets/commands and runtime-only
  transports to explicit external adapters.
- Breakpoints: Pi has no direct equivalent for every Swarm transport, global
  daemon, A2A control plane, or Bubble Tea frame cache; those must be marked
  as adapters/gaps rather than silently omitted.

## Deliverable

Write `docs/architecture/swarm-tui-to-pi-map.md` with:

1. scope and repository evidence;
2. top-to-bottom execution flow;
3. subsystem ownership and key files;
4. Forge prompt/tool/hook/permission contracts;
5. TUI state/rendering and automation contracts;
6. Pi plugin package layout and registration matrix;
7. exact-copy vs adapter vs gap classification;
8. invariants and acceptance tests for parity;
9. unresolved implementation questions and recommended build order.

## Verification

- Re-run targeted symbol searches for every cited subsystem.
- Check all cited line ranges exist in the canonical source.
- Check every proposed Pi API is present in the Pi extension documentation or
  mark it as an assumption.
- Keep the destination worktree changes limited to the plan and mapping docs.
