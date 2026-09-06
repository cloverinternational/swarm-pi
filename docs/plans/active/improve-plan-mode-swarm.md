# Improve Pi Plan Mode from Swarm semantics

## Overview

Bring the local Pi plan-mode adapter closer to the authoritative Swarm
behavior while preserving Pi's host-independent core and the existing
approval ceremony. The current adapter already has the basic lifecycle,
first-tool decomposition, workspace containment, persistence entry, and
approval/rejection flow. The main gaps are contract drift and incomplete
lifecycle semantics: the default plan filename differs from Swarm's documented
`PLAN.md`, plan identity/context is not fully represented in persisted state,
hydration does not re-establish lifecycle side effects, and approval results do
not explicitly preserve the approved/edited plan for subsequent enforcement.

## Files to modify

- `.pi/lib/context/swarm-plan-mode.ts`
  - Align the plan-file default and plan approval/snapshot contracts with
    Swarm's plan lifecycle.
  - Make state transitions and hydration restart-safe, including plan identity,
    interaction state, approval state, and the submitted/edited plan metadata.
  - Keep first-tool injection one-shot and ensure transition methods have clear
    invalid-state behavior.
  - Preserve workspace containment, symlink, regular-file, UTF-8, and size
    protections.
- `.pi/extensions/10-context/swarm-plan-mode.ts`
  - Persist complete lifecycle snapshots at the correct transition points.
  - Restore the latest valid snapshot without duplicating prompt guidance or
    incorrectly re-triggering first-tool behavior.
  - Return a stable, Swarm-compatible approval result for inline and file plans,
    including edited content and context-clearance intent.
  - Ensure rejected approval remains active and does not leave an approval
    state stranded after broker/UI failure.
- `.pi/lib/swarm-plan-mode.test.ts`
  - Add lifecycle, hydration, filename, edited-plan, invalid-transition,
    persistence, and repeated-entry regression coverage.
- `tests/swarm-plan-mode.test.ts`
  - Extend the Pi adapter tests for real event wiring, exact prompt
    idempotence, approval/rejection retry, and session restart behavior.
- `docs/plans/active/improve-plan-mode-swarm.md`
  - This implementation plan and acceptance record.

Do not modify `vendor/`; it is reference material only.

## Implementation steps

1. **Pin the contract before changing code.** Compare the local controller and
   adapter with Swarm's plan-mode definition and first-tool hook. Record exact
   differences in tests rather than relying on prose or startup tool catalogs.
   Keep tool authorization unchanged: plan mode is a phase/lifecycle contract,
   not a permission bypass.
2. **Align plan artifact semantics.** Use Swarm's default `PLAN.md` while
   retaining explicit `plan` and `plan_file` support. Validate mutual
   exclusion, workspace-local paths, symlink/traversal escapes, regular files,
   bounded bytes, and non-empty content. Make fallback lookup deterministic.
3. **Strengthen lifecycle state.** Ensure every entry mints a fresh plan ID,
   resets first-tool and interaction state, and records history. Ensure
   approval begins only from active state, rejection returns to active with
   feedback, and approval returns to idle while retaining enough finalized
   plan metadata for the result and persistence audit. Define behavior for
   repeated enter/exit calls and failed approval UI calls.
4. **Make restart hydration faithful.** Persist and restore the complete
   snapshot, validate malformed/unknown snapshots defensively, and preserve
   whether the first-tool prompt was already consumed. Re-establish the shared
   controller reference and avoid duplicate system-prompt or breakdown
   injections after session restart.
5. **Preserve approved-plan handoff.** Return the approved or edited Markdown
   exactly once in the structured result, with explicit `clear_context` intent.
   Keep rejection feedback actionable and ensure a second submission can be
   attempted without minting an accidental extra plan ID.
6. **Add focused tests first, then run the existing adapter suite.** Cover
   controller transitions, two entries and plan-ID history, snapshot round
   trips, malformed snapshots, first-tool/ask-user ordering, prompt
   idempotence, default `PLAN.md`, inline/file conflicts, approval edits,
   rejection retry, UI absence/headless approval, and all path-boundary cases.
7. **Verify integration and report any deliberate divergence.** Run the focused
   Vitest tests and affected TypeScript checks/builds, then inspect the diff for
   unrelated changes. If a Swarm behavior cannot be represented by Pi's host
   surface, document it as an explicit adapter exception rather than silently
   approximating it.

## Risks and considerations

- Changing the default from `plan.md` to `PLAN.md` may affect existing users;
  explicit paths remain supported, and the migration behavior must be tested.
- Persisted snapshot shape is session data. Add backward-compatible defaults
  for older entries rather than assuming every field exists.
- System prompt and first-tool guidance are model-visible; duplicate or
  reordered injection changes behavior, so tests must assert exact occurrence
  counts and ordering.
- Approval and context compaction are separate concerns. The adapter should
  report `clear_context`; it must not silently compact or alter conversation
  history itself.
- Workspace and symlink checks must remain fail-closed. No plan-file behavior
  should permit writes outside the active workspace.

## Acceptance criteria

- Local controller/adapter tests pass, including all existing tests.
- Default plan submission resolves `PLAN.md`; explicit relative and absolute
  contained paths continue to work, while traversal, symlink escape, directory,
  empty, and oversized files fail safely.
- Entering plan mode creates a fresh ID and exactly one first-tool breakdown;
  asking a question records interaction; repeated prompt hooks do not duplicate
  the active-plan guidance.
- Rejected approval leaves the controller active and retryable; approved or
  headless approval leaves it idle and returns the finalized plan plus explicit
  context-clearance state.
- A hydrated active snapshot preserves plan ID, history, interaction, and
  first-tool state without duplicate injections.
- No source under `vendor/` is changed, and any remaining Pi/Swarm mismatch
  is named as a bounded adapter limitation.
