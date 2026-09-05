---
name: pi-harness-engineering
description: Engineering guidance for building Pi coding-agent harness extensions, especially event contracts, hook execution, TUI-only rendering, model context boundaries, and Swarm-style hook presentation.
---

# Pi Harness Engineering

Use this skill when extending Pi or porting behavior from the Swarm SDK into Pi.

## Core event contracts

Pi already provides the integration interface through `ExtensionAPI`:

- `pi.on("tool_call", handler)`: runs before tool execution; return only blocking control such as `{ block: true, reason }`. Do not return diagnostic messages from this event.
- `pi.on("tool_result", handler)`: runs after execution; may return partial result patches such as `{ content, details, isError }`.
- `pi.on("before_agent_start", handler)`: may return `{ message, systemPrompt }` for intentional model-context injection.
- `pi.on("tool_execution_start|update|end", handler)`: observe tool lifecycle and progress.
- Use `ctx.signal` for abort-aware nested work.

Always treat handler return values as Pi protocol data, not generic hook output. Returning `{ message: ... }` from tool events can create model-visible context or synthetic prose.

## Context versus TUI presentation

Pi has two distinct channels:

### Model-visible context

Use only when the agent must receive the information:

- `before_agent_start` return `{ message: { customType, content, display } }`
- `tool_result` return `{ content, details, isError }`
- `pi.sendMessage()` — custom messages participate in LLM context even when `display: true`.

### TUI-only durable presentation

For hook telemetry, status rows, and diagnostics that should not reach the model:

```ts
pi.appendEntry("my-entry", data);
pi.registerEntryRenderer("my-entry", (entry, options, theme) => {
  return new Text("...", options.outputPad, 0);
});
```

Custom entries do not participate in LLM context. Prefer this channel for Swarm-style hook execution rows.

## Swarm-style hook rendering

Swarm displays each tool-bound hook inline around the tool execution:

```text
  ✓ [pre-hook] plan-mode-first-tool-hook
  ✓ [pre-hook] task-enforcement-hook
  ✓ [pre-hook] protected-branch

  ⎿ Bash ...

  ✓ [post-hook] task-maintenance-reminder-hook
```

Rules:

- Render only tool-bound hook executions in the normal transcript.
- Keep lifecycle and prompt-hook observations in telemetry unless they block or fail.
- Successful hook rows must not emit prose such as “hook completed successfully”.
- Put hook stdout/stderr beneath the row only when output exists:

```text
  ✓ [pre-hook] custom-hook
      ⎿ hook output
```

- Use `✓` for success, `!` or `✗` for blocked/failed outcomes.
- Display the actual hook name, not the adapter group or tool name.
- Preserve ordering: pre-hooks before the tool, post-hooks after the tool.
- Deduplicate terminal events because Pi can emit both `tool_result` and `tool_execution_end`.

## Swarm SDK alignment

The Swarm SDK separates execution from presentation:

1. Hook manager executes registered hooks and records individual outputs.
2. Tool orchestration attaches `PreHooks` and `PostHooks` to the tool-call record.
3. The TUI consumes hook execution updates and renders them inline.
4. Hook output is available for the UI, while only intentional `AdditionalContext` is injected into the agent.

Mirror this separation in Pi. Do not model every hook observation as a Pi custom message.

## Disk hooks

For command hooks:

- Capture stdout and stderr from the child process.
- Return output through internal runtime data for UI rendering.
- Convert blocking failures into Pi’s `{ block: true, reason }` contract on `tool_call`.
- Do not return `{ message }` for ordinary successful execution.
- Apply timeouts and bounded output buffers.
- Persist audit records with `appendEntry`, but keep audit records out of model context.

## Verification checklist

Before considering a harness change complete:

1. Inspect Pi’s current extension docs and type definitions.
2. Confirm each event’s allowed return shape.
3. Verify whether data is model-visible (`sendMessage`, event return) or TUI-only (`appendEntry` + entry renderer).
4. Dogfood in tmux and inspect the captured pane.
5. Test successful, blocked, failed, and output-producing hooks.
6. Confirm `/reload` does not duplicate registrations or erase telemetry incorrectly.
7. Run the full package test suite.

## Common mistakes

- Using `pi.sendMessage()` for visual-only hook rows.
- Returning `{ message }` from `tool_call` or `tool_result` merely to report hook status.
- Rendering adapter group names instead of hook names.
- Showing every lifecycle hook inline, creating transcript noise.
- Treating `tool_result` and `tool_execution_end` as two executions.
- Dropping hook stdout/stderr instead of rendering it beneath the hook row.
