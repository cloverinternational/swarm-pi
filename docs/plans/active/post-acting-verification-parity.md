# Post-acting verification/documentation parity

## Goal

Dogfood Pi until completed acting work produces the same model-visible
verification/documentation workflow as Swarm, without duplicate nudges.

## Choreography

1. **Map the current event path**
   - Confirm `TaskManage` successful batch results update the authoritative
     `TaskManager` before hook handling.
   - Confirm the visible runtime is `.pi/lib/swarm-builtin-hooks-runtime.ts`
     while `taskmanage/src/task-hooks.ts` is currently registered silently.
   - Confirm duplicate terminal events are deduplicated before outcome logic.
2. **Implement the parity state machine**
   - Track successful completed `acting` tasks in the owning visible hook
     pipeline.
   - At two completed acting tasks, inspect the authoritative task snapshot.
   - If no `verifying` or `documenting` task exists, emit the exact Swarm
     post-acting reminder and require no blocking behavior.
   - Make the trigger idempotent per workflow streak/session and reset the
     counter when a verifying/documenting follow-up exists, matching Swarm.
3. **Add focused regression tests**
   - Exact two-acting-task trigger and exact reminder text.
   - One acting task does not trigger.
   - Existing verifying/documenting tasks suppress the reminder.
   - Failed/partial TaskManage batches do not count.
   - Duplicate Pi terminal events count once.
   - Non-acting categories and near-miss ordinary tasks remain allowed.
4. **Dogfood and iterate**
   - Run focused taskmanage tests and build.
   - Run package tests, repository build, repository test suite, and parity
     probe.
   - Inspect failures, patch only parity-related defects, and repeat until
     clean; report any unrelated pre-existing failures separately.

## Breaking points and safeguards

- **Duplicate delivery:** keep visible guidance in the existing Swarm runtime;
  do not enable the silent coordinator as a second model-message source.
- **Batch semantics:** only successful result rows whose task category is
  `acting` count; partial and failed batches must not trigger.
- **Snapshot timing:** read tasks after the TaskManage result has committed,
  not from raw operation category fields alone.
- **Persistence/reload:** use existing hook state/session boundaries so a Pi
  reload cannot replay the same reminder unexpectedly.
- **Subagents:** retain existing subagent bypass behavior.

## Acceptance gates

- Pi emits the exact Swarm heading: `THE WORK IS NOT DONE UNTIL IT IS VERIFIED AND DOCUMENTED`.
- Pi creates the same verifying/documenting task guidance as Swarm after the
  second completed acting task.
- Read-only work and a single completed acting task do not emit the reminder.
- Focused tests, build, package tests, and parity probe pass.
