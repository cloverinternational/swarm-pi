# TaskManage reference contract

This package ports the upstream `internal/tools/ii` task contract into a Pi-safe,
standalone TypeScript adapter.

## Compatibility slice

- Task IDs are stable strings and are journaled through Pi session entries.
- Task status is `pending`, `in_progress`, `completed`, or `deleted`; at most one
  task is active/focused.
- Priorities match upstream `TodoPriority`: `low`, `medium`, and `high`; omitted
  priority defaults to `medium` and is included in task reads/lists.
- Categories, dependencies, parent links, typed notes, audit history, ordered
  batches, sequential/atomic rollback, and operation-key references are preserved.
- Hook enforcement defaults to advisory mode. Blocking is opt-in, and read-only
  exploration plus subagents remain exempt as in the upstream enforcement hook.

The implementation is intentionally offline: Pi's structural API is injected and
all state is rehydrated from journal entries. No provider credentials or network
services are required for tests.

## Deliberate boundaries

This slice does not implement upstream's Go hook registry priority ordering or
compaction-time Todo tool. Pi extensions are registered in load order, so
`TaskHooksCoordinator` is the deterministic lifecycle coordinator for this
adapter. Future work can add explicit hook priority metadata without changing the
TaskManage wire schema.
