# Surgical Plan: Native Swarm-Style Hook Rows in Pi

## 0. Objective

Make hook execution render exactly like the Swarm TUI, around each individual tool call:

```text
  ✓ [pre-hook] plan-mode-first-tool-hook
  ✓ [pre-hook] task-enforcement-hook
  ✓ [pre-hook] protected-branch

  ⎿ Bash ...

  ✓ [post-hook] task-maintenance-reminder-hook
  ✓ [post-hook] autogenskills-budget-enforcement
```

Requirements:

- Rows appear before and after the matching individual tool call.
- Each registered hook gets its own row.
- Hook stdout/stderr appears below its own row when present.
- Successful hook status is not sent to the LLM.
- Intentional hook context injection remains possible.
- Pre/post order is deterministic.
- `tool_result` and `tool_execution_end` cannot duplicate execution or rows.
- Parallel tool calls cannot cross-contaminate rows.
- The implementation is modular and does not embed TUI logic in policy/runtime code.

Non-goals:

- Do not redesign Pi's general event system.
- Do not use ordinary chat messages as a permanent hook-row mechanism.
- Do not modify unrelated tools, prompts, themes, or task behavior.
- Do not change Swarm budget semantics as part of this rendering change.

---

## 1. Lowest-Level Problem Model

A tool call is a sequence of events. We must distinguish **execution**, **accounting**, and **display**.

```text
             EXECUTION                 ACCOUNTING                 DISPLAY

Pi emits       tool_call  ───────────────┐
                                         │
Hook runtime   run hook A                │
               run hook B                │
               return block/continue     │
                                         │
Pi executes    tool                      │
                                         │
Pi emits       tool_result ──────────────┤── record result
                                         │
Pi emits       tool_execution_end ──────┘

Renderer       receives the finished per-tool record
               and renders pre rows, tool row, result, post rows
```

The key invariant is:

```text
one toolCallId
  -> one execution record
  -> zero or more pre-hook observations
  -> one tool invocation
  -> zero or more post-hook observations
  -> one rendered tool row
```

The current implementation violates this separation because it tries to insert post-hook UI through `sendMessage()`. `sendMessage()` is a session message API, not a slot in Pi's existing tool component.

---

## 2. Facts Verified in Current Pi

Pi v0.85.0 provides:

```text
pi.on("tool_call", handler)
pi.on("tool_result", handler)
pi.on("tool_execution_start", handler)
pi.on("tool_execution_update", handler)
pi.on("tool_execution_end", handler)   // documented, but not reliably observed in current runner path
pi.registerTool(definition)
pi.registerMessageRenderer(customType, renderer)
pi.registerEntryRenderer(customType, renderer)
pi.appendEntry(customType, data)
pi.sendMessage(message, options)
```

Important semantics from Pi:

```text
tool_call:
  - can block
  - return shape is { block, reason, terminate? }
  - arbitrary { message } is not a display-only result

tool_result:
  - middleware
  - can patch content/details/isError/usage
  - returning content changes what the LLM receives

sendMessage:
  - custom message
  - participates in LLM context
  - display=true affects TUI visibility only

appendEntry + registerEntryRenderer:
  - TUI-only/durable custom entry
  - excluded from LLM context
  - replayed as transcript entries, not guaranteed to occupy live tool slots
```

Conclusion:

```text
Do not use sendMessage for the final implementation.
Do not use appendEntry for live positional hook rows.
The row must be attached to Pi's tool execution renderer/state.
```

---

## 3. Upstream Swarm Reference Model

Swarm has an explicit execution-update path:

```text
Hook manager
  -> Execute hook
  -> HookExecutionResult {
       HookName,
       Phase,
       Success,
       Blocked,
       Output,
       Error,
     }

Tool orchestration
  -> ToolCallRecord.PreHooks[]
  -> ToolCallRecord.PostHooks[]

TUI renderer
  -> render PreHooks before tool
  -> render tool call/result
  -> render PostHooks after tool
```

The critical design point is that Swarm does not make each hook result a chat message. It attaches hook results to the tool-call record consumed by the tool renderer.

Pi-Swarm must reproduce that shape locally.

---

## 4. Target Architecture

```text
+---------------------+
| Pi event adapters   |
| tool_call/result    |
+----------+----------+
           |
           v
+---------------------+
| Hook runtime        |
| ordering/filtering  |
| execution/policy    |
+----------+----------+
           |
           | HookExecutionObservation
           v
+---------------------+
| Hook observation    |
| store               |
| keyed by toolCallId |
+----------+----------+
           |
           +--------------------+
           |                    |
           v                    v
+---------------------+  +----------------------+
| Budget/task audit   |  | Pi tool render bridge|
| counters/persistence|  | pre/post lookup      |
+---------------------+  +----------+-----------+
                                      |
                                      v
                           +----------------------+
                           | Native tool renderer |
                           | or controlled wrapper |
                           +----------------------+
```

### Module responsibilities

#### A. Hook runtime

File:

```text
taskmanage/src/swarm-hook-runtime.ts
```

Owns:

- hook registration
- priority ordering
- event matching
- timeout handling
- block/failure policy
- per-hook execution result
- runtime audit

Must not own:

- `Text`, `Box`, colors, indentation
- Pi session messages
- TUI layout

#### B. Observation store

New file:

```text
.pi/hook-observations.ts
```

Owns:

- per-tool-call pre/post observations
- lifecycle state transitions
- terminal deduplication
- bounded retention
- lookup by `toolCallId`
- cleanup after final rendering

Suggested types:

```ts
export type HookPhase = "pre" | "post";
export type HookOutcome = "executed" | "blocked" | "failed" | "skipped";

export interface HookExecutionObservation {
  hookName: string;
  phase: HookPhase;
  outcome: HookOutcome;
  toolCallId: string;
  toolName: string;
  output?: string;
  error?: string;
  at: string;
}

export interface ToolHookObservations {
  pre: HookExecutionObservation[];
  post: HookExecutionObservation[];
  resultSeen: boolean;
  executionEndSeen: boolean;
}
```

The store must be independent of Pi TUI classes.

#### C. Presentation adapter

New or expanded file:

```text
.pi/hook-presenter.ts
```

Owns:

- converting observations to Swarm row text/components
- success/error markers
- `[pre-hook]` and `[post-hook]` labels
- output indentation
- width truncation
- theme colors

Must not own:

- hook execution
- budget decisions
- task decisions
- session persistence

#### D. Pi tool-render bridge

Preferred location:

```text
.pi/extensions/tool-render-bridge.ts
```

Responsibilities:

- connect observations to Pi tool rows
- preserve Pi built-in rendering where possible
- expose a narrow internal registration API
- associate `toolCallId` with rendering state
- render pre/post observations in the same component as the tool

This is the only module allowed to understand both Pi tool rendering and hook presentation.

---

## 5. Integration Strategy Decision

### Preferred implementation order

Implement in this order, stopping at the first option that supports exact placement without broad Pi changes.

```text
Option 1: Pi native renderer extension point
    |
    | unavailable?
    v
Option 2: shared wrapper around built-in tool definitions
    |
    | incomplete/unsafe for all tools?
    v
Option 3: small maintained Pi fork/patch to tool-execution.ts
```

### Option 1: Check existing native extension point

Inspect the installed Pi source, not only docs:

```text
/usr/lib/node_modules/@earendil-works/pi-coding-agent/dist/
```

Search specifically for:

```text
ToolRenderResultOptions
renderCall
renderResult
ToolExecution
tool-execution.ts
custom renderer composition
```

Question to answer:

```text
Can an extension register a renderer for an existing tool name
without replacing execution, and can that renderer access the same
row-local state before and after execution?
```

If yes:

- use that API
- preserve built-in execution
- add hook rows through shared row-local state

If no, continue to Option 2.

### Option 2: wrapper definitions

Create a wrapper factory:

```ts
wrapToolWithHookRendering(tool, observationStore)
```

Requirements:

- preserve original `execute`
- preserve original `renderCall` unless composition is required
- preserve original `renderResult` unless composition is required
- preserve schema and tool name
- preserve built-in result shapes
- never duplicate tool registration
- support built-ins: read, bash, edit, write, grep, find, ls, powershell
- support extension tools where possible

Risk:

- Pi extension tools may not expose all definitions to an extension
- replacing built-ins may alter source metadata or behavior
- parallel calls require strict row-local state

This option is acceptable only with tests for every wrapped tool class.

### Option 3: Pi renderer patch

If no extension-only mechanism can guarantee placement, patch Pi's tool-execution component.

The patch should be minimal and upstreamable:

```ts
interface ToolExecutionHookSlots {
  pre: HookExecution[];
  post: HookExecution[];
}

interface ToolRenderContext {
  ...existingContext;
  hookSlots?: ToolExecutionHookSlots;
}
```

The patch must:

- add an optional hook-slot field
- render slots before/after the existing tool content
- leave all existing tools unchanged when slots are absent
- avoid importing Pi-Swarm into Pi
- expose a generic callback/interface, not Swarm-specific names

Pi-Swarm then supplies the slots through a small integration bridge.

---

## 6. Exact Event/Data Flow

### Pre-hook flow

```text
1. Pi emits tool_call(toolCallId, toolName, input)
2. Pi-Swarm adapter receives event
3. Budget/task hooks execute in registered priority order
4. For each hook:
     observationStore.add({ phase: "pre", ... })
5. If a hook blocks:
     - record blocked observation
     - return { block: true, reason }
     - do not execute the tool
6. Tool renderer reads store.pre[toolCallId]
7. Renderer prints pre rows immediately before the tool row
```

### Post-hook flow

```text
1. Tool finishes
2. Pi emits tool_result(toolCallId, toolName, result)
3. Pi-Swarm adapter executes post hooks
4. For each hook:
     observationStore.add({ phase: "post", ... })
5. Pi's tool result middleware receives only legitimate patches:
     { content, details, isError, usage }
6. Tool renderer reads store.post[toolCallId]
7. Renderer prints post rows immediately after tool output
8. On finalization, store.finalize(toolCallId)
```

### Terminal deduplication

Use explicit event identity:

```text
key = toolCallId + ":" + phase + ":" + hookName
```

Do not use only `toolCallId`.

Do not assume `tool_result` and `tool_execution_end` are both emitted.

If both are emitted:

```text
execution work: run once
observation: record once
rendering: render once
```

If only `tool_result` is emitted:

```text
post rows still render
```

---

## 7. Hook Output Rules

For each observation:

```text
success, no output:
  ✓ [pre-hook] hook-name

success, output:
  ✓ [pre-hook] hook-name
      ⎿ output line 1
      ⎿ output line 2

blocked:
  ! [pre-hook] hook-name (BLOCKED: reason)

failed:
  × [post-hook] hook-name (FAILED: reason)
```

Rules:

- stdout and stderr are presentation data by default.
- Do not return output as `{ message }`.
- Do not inject output into LLM context unless a hook explicitly requests context injection.
- Truncate output for display using terminal width and a bounded line count.
- Preserve full output in diagnostics only if explicitly needed and safely bounded.

---

## 8. Accounting Separation

Autogen budget accounting must not be changed by the renderer work.

### Attempt phase

```text
tool_call
  -> determine exempt/non-exempt
  -> if non-exempt and focused: charge exactly once
  -> failed attempts remain charged
```

### Result phase

```text
tool_result
  -> increment completed tool/result metrics exactly once
  -> record success/failure
  -> successful Skill/SkillManage resets/refills budget
  -> never charge budget again
```

### Turn phase

```text
turn_end
  -> increment turns exactly once
  -> evaluate nudge cadence
  -> schedule curator after idle
```

The renderer must consume observations but never modify these counters.

---

## 9. Required File Changes

### New files

```text
.pi/hook-observations.ts
.pi/extensions/tool-render-bridge.ts       # only if native/wrapper bridge is needed
```

### Existing files to modify

```text
taskmanage/src/swarm-hook-runtime.ts       # emit normalized per-hook observations
.pi/hook-state.ts                          # accounting/adapter boundary only
.pi/hook-presenter.ts                       # presentation implementation
.pi/extensions/hooks.ts                    # register presenter/bridge
.pi/extensions/swarm-disk-hooks.ts         # publish individual disk-hook results
.pi/extensions/autogenskills.ts            # publish named individual observations
```

### Files that should not change unless required by Option 3

```text
upstream/swarm-sdk/**
Pi's global installation files
unrelated task management code
unrelated prompt/theme/search extensions
```

If a Pi fork is required, keep it outside this repository or document the exact patch and version pin. Do not silently modify `/usr/lib` source.

---

## 10. Tests to Add Before Completion

### Unit: observation store

```text
- adds pre observation by toolCallId
- adds post observation by toolCallId
- separates two simultaneous toolCallIds
- preserves hook ordering
- deduplicates same hook/phase/call
- permits same hook name across different calls
- finalizes and removes call state
- bounded retention prevents memory growth
```

### Unit: presenter

```text
- success marker is ✓
- blocked marker is !
- failed marker is ×
- phase before renders [pre-hook]
- phase after renders [post-hook]
- output uses ⎿ continuation rows
- multiline output is indented
- long output is bounded
- narrow terminal width does not overflow
```

### Unit: runtime

```text
- emits one observation per registered hook
- emits observations in priority order
- emits blocked observation before returning block
- emits failed observation on thrown hook
- includes output in observation
- does not render or import TUI
```

### Integration: Pi event contract

```text
- tool_call return contains only valid block fields
- tool_result return contains only valid result patch fields
- successful hook status is not returned as message
- intentional before_agent_start message still works
```

### Integration: tool placement

```text
- one Bash call: pre rows -> Bash row -> post rows
- two sequential calls: each has its own pre/post group
- parallel calls: rows stay attached to their own toolCallId
- failed tool: post rows still render
- blocked tool: pre block row renders, tool does not execute
- no tool_execution_end: post rows still render from tool_result
- duplicate terminal events: no duplicate rows
```

### Dogfood

Use a fresh process, never only `/reload`:

```bash
SESSION=pi-hook-final-proof
tmux kill-session -t "$SESSION" 2>/dev/null || true
tmux new-session -d -s "$SESSION" -x 140 -y 45 \
  'cd /home/swarm/Work/Pi-Swarm; pi --thinking low --no-session'
```

Prompt:

```text
Run exactly three separate Bash calls, one at a time:
printf FIRST
printf SECOND
printf THIRD
```

Expected proof:

```text
✓ [pre-hook] ...
$ printf FIRST
FIRST
✓ [post-hook] ...

✓ [pre-hook] ...
$ printf SECOND
SECOND
✓ [post-hook] ...

✓ [pre-hook] ...
$ printf THIRD
THIRD
✓ [post-hook] ...
```

Also inspect:

```bash
tail -50 ~/.swarm/logs/autogenskills.jsonl
```

Verify:

```text
attempt count = one per actual tool call
result count = one per actual tool call
turn count = one per completed turn
Bash read-only commands are charged when focused
```

---

## 11. Acceptance Criteria

The work is complete only when all statements are true:

```text
[ ] Every actual hook execution has one normalized observation.
[ ] Every observation carries hookName, phase, outcome, toolCallId.
[ ] Pre rows render before their own tool row.
[ ] Post rows render after their own tool output.
[ ] Rows do not move to the end of the transcript.
[ ] Rows do not become LLM context.
[ ] Hook output appears only under the owning hook row.
[ ] Multiple hook groups do not suppress one another.
[ ] Parallel calls do not cross-contaminate.
[ ] tool_result-only environments still show post rows.
[ ] Duplicate terminal events do not duplicate rows.
[ ] Runtime modules contain no TUI imports.
[ ] Presenter contains no policy/accounting logic.
[ ] Autogen budget counters are unchanged by rendering.
[ ] TaskManage counters are unchanged by rendering.
[ ] Fresh Pi v0.85.0 tmux dogfood proves the complete sequence.
[ ] Full test suite passes.
[ ] Only scoped files are committed.
```

---

## 12. Implementation Sequence

```text
Phase 1: Freeze and inspect
  1. Record current git status.
  2. Do not mix unrelated working-tree changes into this work.
  3. Inspect Pi's actual tool-render source and extension types.
  4. Decide Option 1, 2, or 3 based on executable evidence.

Phase 2: Build pure data layer
  5. Add hook observation types.
  6. Add per-tool-call observation store.
  7. Add deterministic dedupe/finalization tests.

Phase 3: Refactor runtime publication
  8. Make runtime emit one observation per hook.
  9. Keep runtime free of TUI imports.
  10. Adapt disk, task, autogen, and other hook families to publish observations.

Phase 4: Build presentation layer
  11. Make presenter consume only normalized observations.
  12. Add marker/phase/output/width tests.
  13. Remove group-level synthetic rows.

Phase 5: Integrate with Pi tool rendering
  14. Implement the selected bridge.
  15. Preserve built-in tool execution and result rendering.
  16. Attach observations by toolCallId.
  17. Handle blocked, failed, sequential, and parallel calls.

Phase 6: Remove incorrect paths
  18. Remove sendMessage-based hook rows.
  19. Remove appendEntry-based live hook rows.
  20. Keep appendEntry only for durable telemetry/audit.
  21. Ensure context filtering is not being used as a substitute for correct TUI placement.

Phase 7: Prove
  22. Run unit tests.
  23. Run full package tests.
  24. Start fresh Pi v0.85.0 tmux session.
  25. Run three sequential Bash calls.
  26. Capture pane.
  27. Inspect debug log.
  28. Verify acceptance checklist.
  29. Review diff for scope creep.
  30. Commit only after the proof passes.
```

---

## 13. Explicit Stop Conditions

Stop and reassess instead of adding another workaround if:

```text
- post rows still require sendMessage timing tricks
- a renderer cannot identify toolCallId
- a wrapper changes built-in tool behavior
- a hook result is being returned as model-visible prose
- an event is assumed to exist but is absent in the installed runtime
- parallel calls cannot be isolated
- the solution requires modifying unrelated extensions
```

The correct fix is a first-class per-tool hook-slot bridge, not another message, timer, or global queue.