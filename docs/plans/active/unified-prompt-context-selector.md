# Unified Prompt and Context Selector Plan

## Objective

Add a unified `/configure` TUI experience for selecting the four prompt/runtime
categories already represented in Pi-Swarm:

1. System-prompt profiles
2. Context sources and workspace files
3. Model-visible skills
4. Model-visible tools

The selection state will be workspace-scoped, persisted in
`.pi/config/prompt-context.json`, and honored by both TUI and headless runs. `/sp`
will remain a compatibility entry point.

## Decisions

- `/configure` opens a category menu, followed by category-specific overlays.
- Existing `/sp` remains available and uses the consolidated store.
- `.pi/config/prompt-context.json` becomes the source of truth for prompt profiles and
  selections. Existing `.pi/system-prompts.json` is migrated non-destructively;
  the new store wins conflicts and imports only missing legacy profiles.
- Missing categories preserve current behavior. Explicit empty skills, tools,
  and context selections are valid. A system prompt always resolves to a valid
  profile.
- A selected prompt is a base; selected context, skills, and tool configuration
  are composed on top of it.
- Context consists of built-in sources, bounded known candidates, and explicit
  workspace files. Discovery is not recursive. Explicit paths are accepted
  permissively only when their canonical targets remain inside the workspace;
  symlink escapes are rejected. Persist paths, never contents.
- Skills use `default` or `allowlist` mode and filter the model-visible catalog;
  the registry remains available for inspection and validation.
- Tools enumerate `pi.getAllTools()` and use `setActiveTools()` as the supported
  implementation of strict deregistration. Registered definitions remain
  available for restoration. No physical unregister API will be added.
- `/configure` remains usable independently of the model tool set. A Restore
  defaults action restores the full active tool set.
- Confirmed changes apply immediately to runtime tool state and persist first;
  prompt/context changes take effect at the next model/provider boundary. A
  failed application rolls back persisted and runtime state.
- Invalid configuration is repaired field-by-field, preserving valid state.
  A timestamped recovery copy is written before repair and the user is warned.
- Configuration changes are one full transaction, not independent category
  commits. Current turns retain their existing prompt; the next turn reloads
  committed state.
- Tool deregistration is strict per the requested behavior: no automatic
  required-tool additions. UI commands remain outside the model tool registry.

## Choreographed implementation sequence

1. **Re-read current seams and establish baseline.** Inspect the existing
   system-prompt store, `/sp` command, context source contracts, skill catalog,
   active-tool APIs, selector rendering, and prompt/provider lifecycle. Run the
   focused existing tests before editing. This step verifies assumptions and
   identifies exact host APIs; every later step depends on it.
2. **Define the durable selection contract.** Add typed presence-aware state,
   versioning, normalization, validation, migration, atomic writes, recovery
   backups, and workspace scoping. Keep prompt bodies and selection data in the
   consolidated store, with legacy import and new-store conflict precedence.
3. **Add context selection plumbing.** Extend the context builder with selected
   source IDs and explicit file inputs. Preserve declaration order, cache policy,
   budgets, deduplication, canonical containment, regular-file checks, and
   provenance. Make absent state exactly match current defaults.
4. **Add skill and tool filtering.** Filter the catalog from the persisted skill
   mode without disabling registry inspection. Build a normalized registered-tool
   list and apply active tools through `setActiveTools()`. Snapshot active state,
   support Restore defaults, and make failure rollback exhaustive.
5. **Unify prompt assembly.** Make `swarm-prompt.ts` load the workspace state at
   the `before_agent_start` boundary, resolve the selected base prompt, compose
   selected context and skills, and preserve headless/TUI mode differences that
   are unrelated to selection. Remove the current custom-prompt bypass for the
   shared selected layers while preserving CLI isolation semantics.
6. **Implement `/configure` and compatibility flows.** Add the category menu,
   category-specific overlays, keyboard navigation, apply/cancel semantics,
   notifications, migration/repair warnings, and Restore defaults. Keep UI
   selection/persistence separate from prompt mutation. Update `/sp` to the new
   store while retaining its existing subcommands.
7. **Add lifecycle and transaction wiring.** Load/repair state at session start,
   apply tool changes after confirmation, and ensure next-turn prompt refresh.
   On any failure, restore the prior file and active-tool snapshot and report a
   bounded actionable error.
8. **Verify progressively.** Run package-local unit tests, TypeScript checks,
   prompt/context tests, and then parity probes. Add a real TUI scenario that
   changes selections and proves the subsequent provider system prompt/tool set
   changes. Add a headless scenario proving the same persisted state is honored
   without opening UI.

## Breaking points and mitigations

- **Host API mismatch:** `setActiveTools()` exists; physical unregister does not.
  Confirm the runtime contract before implementation and do not invent a host
  unregister layer.
- **State ambiguity:** absent versus explicit false/empty must use presence-aware
  decoding and round-trip tests.
- **Custom prompt regression:** current custom prompts bypass common assembly;
  add migration and composition tests before changing that branch.
- **Tool recovery deadlock:** strict selection could remove model tools needed
  for recovery; keep `/configure` as a command/UI path and test Restore defaults
  with an empty tool selection.
- **Partial transaction:** persistence and active-tool updates can disagree;
  write/backup first, apply with a snapshot, and restore both on failure.
- **File escape:** permissive selection can cross symlinks; resolve canonical
  targets and reject escapes while never persisting contents.
- **Prompt-size growth:** explicit files and selected catalogs must retain
  existing per-source and total budgets; test oversized and missing inputs.
- **Cross-mode drift:** both modes load persisted state, but headless never opens
  UI; test TUI/headless with identical workspace state.
- **Stale current turn:** do not mutate an active provider request; test that
  changes become visible at the next lifecycle boundary only.
- **Legacy conflict:** import only missing profiles and make the new file win;
  test both absent-new-file migration and both-files conflict cases.
- **Dirty working tree:** preserve unrelated existing modifications and avoid
  rewriting generated artifacts unless the repository's existing build contract
  requires it.

## Planned file scope

- `.pi/extensions/10-context/system-prompts.ts`: consolidated profile compatibility,
  migration, and `/sp` behavior.
- `.pi/extensions/10-context/swarm-prompt.ts`: selection-aware prompt composition and
  next-boundary loading.
- `.pi/lib/context/swarm-context.ts`: source/file selection and bounded resolution.
- `.pi/extensions/00-runtime/swarm-transport-parity.ts` or the owning tool-exposure seam:
  selected active-tool handling, only if required by the actual host contract.
- New focused library/module only if an existing owner cannot safely hold the
  persisted selection contract; avoid duplicate stores.
- Existing `/codemode` selector patterns may be reused, but its unrelated state
  and semantics must not be coupled to prompt configuration.
- Focused tests under `.pi/test/<layer>/` and relevant package tests.
- `tools/parity/probe.mjs` / `tools/parity/tui-probe.mjs` and fixtures for the
  selected-context/provider-boundary scenarios.

## Acceptance criteria

- A fresh workspace behaves byte-for-byte like current behavior with no config.
- `/configure` exposes all four categories and category overlays support
  navigation, explicit apply, and cancel-without-mutation.
- `/sp` continues to work and legacy profiles migrate without data loss.
- Custom profiles compose selected context and skills as specified.
- Context source defaults, explicit empty state, file ordering, containment,
  symlink rejection, deduplication, and budgets are tested.
- Skill allowlists affect the model-visible catalog but not registry inspection.
- Tool selection uses `setActiveTools()`, supports empty selection and Restore
  defaults, and never requires a physical unregister API.
- Configuration writes are atomic; repair preserves valid fields and leaves a
  timestamped recovery copy; invalid state cannot activate unsafe paths.
- Changes persist before application, roll back on failure, and affect prompt
  composition only at the next model boundary.
- TUI and headless runs honor the same persisted state without headless UI.
- Provider-boundary parity probes demonstrate changed prompt/tool output for a
  changed selection and unchanged output for an unconfigured workspace.
- Focused tests, TypeScript/build checks, and real TUI/headless probes pass;
  unrelated pre-existing failures are reported separately.

## Explicitly deferred

- Agent selection
- Broader runtime settings
- Recursive repository-wide file scanning
- Physical tool unregistration in the Pi host API
- Full `ask_user_question` schema unification and `visual_choice` work
- Moving or deleting `.pi/system-prompts.json` after migration
