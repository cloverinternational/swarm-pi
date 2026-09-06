# Hook Ordering: First-Principles Implementation Plan

## Status

Planning only. No implementation changes are authorized until the observations below are verified by tests.

## 1. Problem Definition

Observed output:

```text
codemode result

✓ [pre-hook] autogenskills-budget-enforcement
✓ [pre-hook] disk-hooks
✓ [pre-hook] task-enforcement-hook
✓ [post-hook] annoyance-nudge
✓ [post-hook] autogenskills-budget-enforcement
✓ [post-hook] disk-hooks
✓ [post-hook] task-maintenance-reminder-hook
```

Required output:

```text
✓ [pre-hook] autogenskills-budget-enforcement
✓ [pre-hook] disk-hooks
✓ [pre-hook] task-enforcement-hook

codemode call/result

✓ [post-hook] annoyance-nudge
✓ [post-hook] autogenskills-budget-enforcement
✓ [post-hook] disk-hooks
✓ [post-hook] task-maintenance-reminder-hook
```

The word `pre` describes the hook's execution phase. It does not prove the row was rendered before the tool. We must prove both independently.

## 2. Basic Terms

```text
Event       A notification from Pi, such as tool_call or tool_result.
Handler     A function registered to receive an event.
Observation The durable in-memory record of one hook execution.
Tool row    The TUI component representing one tool call.
Renderer    The function that converts tool state into TUI components.
Queue       Deferred custom messages waiting to be displayed.
Identity    The toolCallId that connects all data to one tool invocation.
```

## 3. Actual Pi Lifecycle

From the installed Pi source:

```text
assistant message_update
  -> ToolExecutionComponent is created
  -> getRegisteredToolDefinition(toolName) is evaluated
  -> component.renderCall() may run

agent.beforeToolCall
  -> ExtensionRunner.emitToolCall({ toolCallId, toolName, input })
  -> tool_call handlers execute
  -> block result may stop execution

actual tool execute

agent.afterToolCall
  -> ExtensionRunner.emitToolResult({ toolCallId, toolName, content, details, isError })
  -> tool_result handlers execute
  -> content/details/isError patches are applied
  -> tool result is returned

agent event forwarding
  -> tool_execution_start/update/end are delivered to TUI subscribers

TUI
  -> ToolExecutionComponent.updateResult()
  -> tool renderer renderResult()
```

Important consequence:

```text
tool_call handler is not a TUI insertion point.
tool_result handler is not a TUI insertion point.
sendMessage is a queued custom transcript message.
```

## 4. Current Pi-Swarm Data Path

```text
Pi tool_call
  -> hook-state registerHook wrapper
  -> actual hook handler
  -> recordHook()
  -> addHookObservation()
  -> setHookPresenter()
  -> pi.sendMessage(display=true)
  -> queued custom message
  -> appears after tool output
```

This explains the observed ordering. The label is `pre`, but the display operation is deferred.

Current code facts:

```text
.pi/hook-state.ts
  recordHook() calls addHookObservation()
  recordHook() calls shared.present()

.pi/extensions/hooks.ts
  setHookPresenter() calls pi.sendMessage()

.pi/extensions/hooks.ts
  registerHookRenderers() is intentionally empty

codemode
  registers its own tool definition
  has its own renderSummary result rendering
  is not wrapped by any active hook renderer
```

Therefore the current implementation cannot guarantee positional pre rows.

## 5. Required Invariants

### I1: Hook execution ordering

For one `toolCallId`, hooks execute in registration/priority order.

```text
pre execution A < pre execution B < tool execution
post execution A < post execution B
```

### I2: Display ordering

For one `toolCallId`:

```text
pre display rows < tool display < post display rows
```

Execution order and display order must both be recorded and tested.

### I3: Identity

Every observation has:

```text
toolCallId
hookName
phase
sequence
```

No global queue may be used for tool-bound rows.

### I4: No cross-talk

For calls A and B:

```text
rows(A) never render inside tool B
```

This includes parallel calls and completion-order differences.

### I5: No duplicates

The same logical hook execution must render once even if Pi emits both:

```text
tool_result
and
tool_execution_end
```

Deduplication key:

```text
toolCallId + phase + hookName + execution sequence
```

### I6: No routine context leakage

Successful hook rows are TUI data only.

```text
LLM context contains neither row text nor routine hook stdout/stderr.
```

Intentional system reminders are separate and explicitly model-visible.

## 6. Data Structures

Create a pure data module:

```text
.pi/hook-observations.ts
```

Required state:

```ts
interface HookObservation {
  toolCallId: string;
  toolName: string;
  hookName: string;
  phase: "pre" | "post";
  outcome: "executed" | "blocked" | "failed" | "skipped";
  output?: string;
  reason?: string;
  executionSequence: number;
  observedAt: number;
}

interface ToolCallHookState {
  pre: HookObservation[];
  post: HookObservation[];
  preClosed: boolean;
  postClosed: boolean;
  renderedPre: boolean;
  renderedPost: boolean;
}
```

The store must expose:

```text
begin(toolCallId)
add(observation)
get(toolCallId)
subscribe(toolCallId, listener)
markResult(toolCallId)
markEnd(toolCallId)
finalize(toolCallId)
```

## 7. Presentation Strategy

### Rejected strategy

```text
recordHook() -> sendMessage()
```

Reason: queued display is not positional.

### Required strategy

Use Pi's `renderCall` and `renderResult` composition.

For every tool definition that can render:

```text
renderCall(args, theme, context)
  -> read observations[context.toolCallId].pre
  -> render pre rows
  -> render original call component

renderResult(result, options, theme, context)
  -> render original result component
  -> read observations[context.toolCallId].post
  -> render post rows
```

The renderer must invalidate when observations arrive after the component is first created.

### Critical Pi detail

The wrapper cannot rely on a single snapshot during `renderCall`, because `tool_call` hooks execute after the component may already exist.

Required behavior:

```text
renderCall first pass:
  subscribe(toolCallId, context.invalidate)
  render current pre rows, possibly empty

hook executes:
  add observation
  listener invokes context.invalidate()

renderCall second pass:
  render pre rows
```

The same applies to `renderResult` and post rows.

## 8. Tool Coverage

The bridge must cover all tools, not only built-ins:

```text
read
bash
edit
write
grep
find
ls
powershell
codemode
Skill
SkillManage
SwarmSkill
Agent
AgentControl
MCP tools
research tools
memory/history tools
schedule tools
```

Coverage must be determined from `pi.getToolDefinition(name)` or an explicit registration wrapper. Do not assume built-in tools are the complete registry.

## 9. Codemode Requirement

`codemode` is a top-level Pi tool whose result contains nested calls. It must receive an outer hook row:

```text
pre hooks
$ codemode
codemode summary
post hooks
```

Nested tools inside CodeMode are separate executions with generated IDs. They must either:

```text
A. be deliberately excluded from outer TUI rows and documented, or
B. receive their own hook observations and render in CodeMode's nested view.
```

Do not silently mix nested IDs with the outer `codemode` ID.

## 10. Prompt/Context Separation

Hook presentation and model reminders are different channels:

```text
presentation:
  renderCall/renderResult components
  TUI only
  not persisted as messages

model reminder:
  tool_result content patch or explicit hidden custom message
  intentionally enters next request context
  wrapped in <system-reminder>
```

Do not use the presentation path for nudges.

## 11. Required Tests Before Implementation Is Complete

### Pure store tests

```text
1. One call receives three pre observations.
2. One call receives four post observations.
3. A and B remain isolated.
4. Duplicate observation is ignored.
5. Different hook groups with same call ID are retained.
6. Listener fires on each new observation.
7. Finalization removes listeners and bounded state.
```

### Fake renderer tests

Use fake components implementing:

```text
render(width): string[]
invalidate(): void
```

Assert:

```text
renderCall output starts with pre rows
renderResult output ends with post rows
invalidation occurs after late observation
```

### Pi contract test

Register a fake tool with:

```text
renderCall
renderResult
execute
```

Run:

```text
tool_call -> hook A/B -> execute -> tool_result -> hook C/D
```

Assert exact event log:

```text
pre-execute:A
pre-execute:B
render-call:pre:A
render-call:pre:B
tool-execute
post-execute:C
post-execute:D
render-result:post:C
render-result:post:D
```

### Codemode test

Assert outer codemode renderer is wrapped and receives rows.

### Context test

Run context transformation after rendering and assert:

```text
no swarm-hook-event
no routine hook output
system-reminder remains only when explicitly intended
```

## 12. Probe Instrumentation

Add temporary or opt-in diagnostics:

```text
SWARM_HOOK_TRACE=1
```

Record JSONL fields:

```json
{
  "at": 0,
  "stage": "execution|observation|renderer|display",
  "toolCallId": "...",
  "toolName": "codemode",
  "hookName": "...",
  "phase": "pre|post",
  "sequence": 0,
  "component": "..."
}
```

The probe must make it impossible to confuse:

```text
execution timestamp
observation timestamp
renderer timestamp
actual screen insertion timestamp
```

## 13. Implementation Order

```text
1. Freeze current behavior in a failing ordering test.
2. Remove sendMessage from recordHook presentation.
3. Complete the pure observation store.
4. Build a composable renderer wrapper.
5. Prove wrapper behavior with fake components.
6. Integrate built-ins.
7. Integrate codemode and extension tools.
8. Add invalidation for late pre/post observations.
9. Remove obsolete dedupe/global queue code.
10. Add context isolation test.
11. Run full tests.
12. Run fresh Pi tmux probe.
13. Capture exact screen output and trace log.
14. Commit only after output matches the required sequence.
```

## 14. Stop Conditions

Stop implementation and report the blocker if:

```text
- only sendMessage can display a row
- no tool definition can be obtained or wrapped
- context.toolCallId is unavailable
- renderCall cannot invalidate
- codemode registration conflicts with the bridge
- post rows require timers or sleeps
- a successful routine hook row enters model context
```

## 15. Definition of Done

```text
[ ] Pre rows execute before the tool.
[ ] Pre rows display before the tool.
[ ] Post rows execute after the tool.
[ ] Post rows display after the tool.
[ ] Codemode is covered.
[ ] Parallel calls are isolated.
[ ] Every hook group is represented independently.
[ ] No duplicate rows occur.
[ ] Hook stdout/stderr is attached to its hook row.
[ ] Routine rows never enter LLM context.
[ ] Intentional nudges enter context exactly once.
[ ] Pi theme reload remains healthy.
[ ] Full tests pass.
[ ] Fresh tmux proof passes.
```