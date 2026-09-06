# Pi-Swarm live parity acceptance

Recorded on 2026-09-04 against:

- Pi 0.74.2 at `/usr/bin/pi`
- SwarmOS 1.28.0 at `/home/swarm/.local/bin/swarm`

## Host keymap

The normal Pi profile uses [.pi/keybindings.json](../.pi/keybindings.json):

```json
{
  "app.thinking.toggle": "ctrl+alt+t",
  "app.tree.filter.noTools": "ctrl+alt+y"
}
```

The repository file and `~/.pi/agent/keybindings.json` were byte-identical
during acceptance.

## Live Ctrl+T capture

Normal-profile Pi startup loaded `swarm-thinking.ts` without a shortcut
conflict. Pressing `Ctrl+T` rendered:

```text
Thinking settings · choose reasoning level

→ ○ Off
  ○ Minimal
  ○ Low
  ● Medium  · current
  ○ High
  ○ Xhigh
```

## Live TaskManage capture

The model was instructed to create one active task and one task blocked by it
in a single atomic TaskManage call. The settled Pi pane rendered:

```text
TaskManage Managing tasks…
    ⎿ ◉ #1 Widget Active
      ○ #2 Widget Blocked

WIDGET_READY

Tasks   0/2 done
  ● [A] Widget Active
  ⧗ [A] Widget Blocked
```

The widget remained visible after the model turn completed. Routine successful
hook events did not produce transcript messages.

## Real headless comparison

Both commands received the same atomic dependency prompt.

```text
REAL swarm -p: exit=0 output=PARITY_OK total=2 beta_depends_on=1
REAL pi -p:    exit=0 output=PARITY_OK total=2 beta_depends_on=1
```

### Fresh evaluator rerun

The real installed binaries were launched again from the project root with the
same prompt on 2026-09-04. Swarm exposed the successful atomic result:

```text
TaskManage
{"status":"succeeded","results":[
  {"key":"alpha","op":"create","status":"succeeded",
   "data":{"task":{"active":false,"id":"1","status":"pending","subject":"Alpha"}}},
  {"key":"beta","op":"create","status":"succeeded",
   "data":{"task":{"active":false,"id":"2","status":"pending","subject":"Beta"}}},
  {"key":"all","op":"list","status":"succeeded",
   "data":{"tasks":[
     {"blocks":["2"],"id":"1","status":"pending","subject":"Alpha"},
     {"depends_on":["1"],"id":"2","status":"pending","subject":"Beta"}
   ],"pagination":{"total":2,"offset":0,"limit":50,"more":false}}}
]}
PARITY_OK total=2 beta_depends_on=1
SWARM_PROCESS_EXIT=0
```

The independent Pi launch returned:

```text
PARITY_OK total=2 beta_depends_on=1
PI_PROCESS_EXIT=0
```

The normal-profile interactive Pi session then made the corresponding atomic
TaskManage call. A delayed pane capture after model completion proved that the
tool result and persistent widget remained visible:

```text
TaskManage Managing tasks…
    ⎿ ◉ #1 Widget Active
      ○ #2 Widget Blocked

 WIDGET_READY

Tasks   0/2 done
  ● [A] Widget Active
  ⧗ [A] Widget Blocked
```

## Visual artifacts

The reproducible source is [pi-swarm-parity.tape](pi-swarm-parity.tape).

| Artifact | Size | SHA-256 |
|---|---:|---|
| [pi-swarm-parity.gif](pi-swarm-parity.gif) | 375,575 bytes | `698aa24e5c834a897c62ea69ce0fa34e154d367e2a9303992e57eed9b97678c8` |
| [pi-swarm-parity.mp4](pi-swarm-parity.mp4) | 615,254 bytes | `6cc490f0cc746cb89bab645e8bdee070b4a0144ba6421fbcd63e970cfd1ee0f7` |
| [pi-swarm-parity.png](pi-swarm-parity.png) | 274,521 bytes | `eea5b680a0151d66fa03aabce56b2488344113f021c3440369d0165c6db7c153` |

The regenerated recording duration is 40.84 seconds.

## Automated acceptance

- Autogenskills: 38/38 tests.
- TaskManage and Pi integration: 82/82 tests.
- All repository package tests passed.
- All TypeScript package builds passed.
- `git diff --check` passed.
- Independent final review passed.

## Targeted autogenskills and curator parity

The repository's `upstream/swarm-sdk` snapshot contains the authoritative
autogenskills package and tests, but intentionally omits
`tests/agent/mocks`, so that mirror cannot compile its test package in
isolation. The targeted Go checks were therefore run from the complete local
Swarm checkout at `/home/swarm/Work/mono/swarm-sdk`. Before running them, the
six relevant test files were SHA-256 compared with the repository mirror:

```text
history_test.go             07d7b93afa0917f1a857fe33d7d12e63aa5a370b3b173e73de6311c5c55ca5de
curator_test.go             9abc529fb0509ec5fc9d8468e51f8329398eed7d00f94dc43b0353920e44da3f
skillmanage_test.go         d373b449c6fb141951695daad947a4e765303b5ffea5c22f133f22c89e02afc9
skillmanage_usage_test.go   c32c5f310375e4e9ef7bca2ace3799e62abc203cb8ba311a7762ed5bc201bb0d
budget_enforcement_test.go  779d5d29b2b30bf5be73e149a47c351d69ff01d44b286b5f4dd7782870318206
hook_test.go                2a973dd3d3bc36769a90016ac5cc867976b1696487ae15e408aecdfd7e347fa5
```

Each hash matched byte-for-byte in both locations.

### Complete upstream and port suites

The entire authoritative upstream Go package was run with the test cache
disabled:

```sh
cd /home/swarm/Work/mono/swarm-sdk
GOWORK=off go test -count=1 ./internal/skills/autogenskills
```

```text
ok github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills 2.751s
UPSTREAM_AUTOGENSKILLS_FULL_SUITE_EXIT=0
```

A second uncached run used Go's JSON event stream to expose exact test counts:

```text
tests_started: 339
tests_passed: 339
tests_failed: 0
tests_skipped: 0
package_passed: true
UPSTREAM_AUTOGENSKILLS_JSON_SUITE_EXIT=0
```

The complete Pi port suite and build were then run:

```sh
cd /home/swarm/Work/Pi-Swarm/autogenskills
npm test -- --reporter=dot
npm run build
```

```text
Test Files  1 passed (1)
Tests       38 passed (38)
PI_AUTOGENSKILLS_FULL_SUITE_EXIT=0
PI_AUTOGENSKILLS_BUILD_EXIT=0
```

### Upstream Swarm command and result

Run from `/home/swarm/Work/mono/swarm-sdk`:

```sh
GOWORK=off go test -v ./internal/skills/autogenskills \
  -run 'Test(SkillHistoryPatchAndUndoRestoresExactPackageAndRegistry|ApplyRetainsRecoveryBackupWhenRollbackFails|StagedSupportWriteRetainsBackupWhenRollbackFails|SkillHistoryRejectsOrphanUndoAndCorruption|SkillManagePreviewDeniesWritesAndDoesNotBumpUsage|SkillManageTool_AutonomousWriteRequiresView|Curator_PinnedSkillNeverArchived|Curator_ShouldRun_freshCurator|Curator_ShouldRun_immediatelyAfterRun|Curator_ShouldRun_after25h|Curator_RunDoesNotConsumeCadenceBeforeOrchestratorSuccess|Curator_RunPreviewDoesNotMutateStateOrFilesystem|LifecycleHook_MarkSkillUsed_SkillManage|BudgetEnforcementHook_ClockPausedBeforeTaskFocused)$'
```

Literal result:

```text
PASS TestBudgetEnforcementHook_ClockPausedBeforeTaskFocused
PASS TestCurator_PinnedSkillNeverArchived
PASS TestCurator_ShouldRun_freshCurator
PASS TestCurator_ShouldRun_immediatelyAfterRun
PASS TestCurator_ShouldRun_after25h
PASS TestCurator_RunDoesNotConsumeCadenceBeforeOrchestratorSuccess
PASS TestCurator_RunPreviewDoesNotMutateStateOrFilesystem
PASS TestSkillHistoryPatchAndUndoRestoresExactPackageAndRegistry
PASS TestSkillHistoryRejectsOrphanUndoAndCorruption
PASS TestApplyRetainsRecoveryBackupWhenRollbackFails
PASS TestStagedSupportWriteRetainsBackupWhenRollbackFails
PASS TestLifecycleHook_MarkSkillUsed_SkillManage
PASS TestSkillManageTool_AutonomousWriteRequiresView
PASS TestSkillManagePreviewDeniesWritesAndDoesNotBumpUsage
PASS
ok github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills 0.283s
```

The lifecycle/accounting follow-up was:

```sh
GOWORK=off go test -v ./internal/skills/autogenskills \
  -run 'Test(Curator_StatePersistence|Curator_PinUnpin|Curator_AutomaticTransitions_ProtectsPinnedAndExternal|SkillManageTool_IsIdempotent|SkillManageViewMarksSkillUsed|BudgetEnforcementHook_ReadOnlyToolsExemptFromBudget|BudgetEnforcementHook_ExemptSkillTools|BudgetEnforcementHook_ExemptTaskTools|BudgetEnforcementHook_PlanModeToolsExemptFromBudget|BudgetEnforcementHook_BashCommandsCountTowardBudgetUnlessReadOnly)$'
```

```text
PASS TestBudgetEnforcementHook_ReadOnlyToolsExemptFromBudget
PASS TestBudgetEnforcementHook_ExemptSkillTools
PASS TestBudgetEnforcementHook_ExemptTaskTools
PASS TestBudgetEnforcementHook_PlanModeToolsExemptFromBudget
PASS TestBudgetEnforcementHook_BashCommandsCountTowardBudgetUnlessReadOnly
PASS TestCurator_PinUnpin
PASS TestCurator_StatePersistence
PASS TestCurator_AutomaticTransitions_ProtectsPinnedAndExternal
PASS TestSkillManageTool_IsIdempotent
PASS TestSkillManageViewMarksSkillUsed
PASS
ok github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills 0.024s
```

### Pi port command and result

Run from `autogenskills`:

```sh
npm test -- --reporter=verbose \
  -t 'serializes skills across processes|merges curator metadata under lock|rolls an undo swap back|rolls back a staged support write|rejects a tampered revision manifest|curates stale skills on cadence|rechecks persisted pin state|enforces read-only curator previews|shares pinned lifecycle protection|shares skill usage counters and timestamps|runs semantic preview without mutation|suppresses duplicate curator runs and arms|suppresses curator runs shared|preserves fresh lifecycle metadata|keeps curator maintenance and explicitly exempt|wires curator preview to an isolated|shares the concrete default autogen directory'
```

Literal result:

```text
PASS rolls an undo swap back when revert revision publication fails
PASS curates stale skills on cadence while preserving pinned skills
PASS rechecks persisted pin state before stale archival
PASS rejects a tampered revision manifest even when its filename and HEAD are unchanged
PASS rolls back a staged support write when revision publication fails
PASS serializes skills across processes and recovers a dead lock owner
PASS merges curator metadata under lock instead of losing another process update
PASS enforces read-only curator previews and view-before-write apply sessions
PASS shares pinned lifecycle protection with isolated curator processes
PASS shares skill usage counters and timestamps across lifecycle observers
PASS runs semantic preview without mutation and preserves cadence on runner failure
PASS suppresses duplicate curator runs and arms maintenance only while idle
PASS suppresses curator runs shared by managers and a live external owner
PASS preserves fresh lifecycle metadata while seeding automatic cadence
PASS keeps curator maintenance and explicitly exempt child sessions outside the parent budget
PASS wires curator preview to an isolated SkillManage-only pi child
PASS shares the concrete default autogen directory with curator children

Test Files  1 passed (1)
Tests       17 passed | 21 skipped (38)
```

### Behavior matrix

| Behavior | Upstream Swarm evidence | Pi port evidence | Result |
|---|---|---|---|
| Revision identity, ancestry, corruption rejection | history patch/undo and orphan/corruption tests | tampered-manifest and undo tests | Same observable safety contract |
| Transaction rollback | apply and staged-support rollback tests | undo and support-write rollback tests | Same observable rollback contract |
| Preview/apply isolation | preview read-only and autonomous view-before-write tests | preview read-only and view-before-write test | Same |
| Curator cadence | fresh/immediate/25h and failed-run cadence tests | cadence, runner-failure, and atomic seed tests | Same, with atomic multi-manager seed hardening in Pi |
| Pin/lifecycle protection | pinned-never-archived, state persistence, pin/unpin tests | persisted pin recheck and isolated-child lifecycle tests | Same |
| Skill usage lifecycle | SkillManage usage tests | explicit cross-observer `uses`, `lastUsed`, and active-state test | Same |
| Budget/task focus/exempt tools | focused-clock and exemption tests | curator/child accounting exemption test plus full budget suite | Same parent-session behavior |
| Duplicate suppression | upstream idempotent SkillManage test | process-local and cross-process curator-run tests | Pi adds explicit orchestration-level suppression coverage |
| Cross-process mutation locking | upstream uses OS file locks in `history.go` / `history_lock_unix.go`; no named contention regression exists | real child-process contention and dead-owner recovery test | Same exclusivity goal; Pi's directory-lock stale-owner recovery is port-specific |
| Default directory propagation | upstream path accessors establish `~/.swarm/skills/autogen`; no host-to-child propagation test exists | concrete adapter-to-child propagation regression | Pi adapter boundary covered explicitly |

“Same” in this table means matching externally visible lifecycle or safety
semantics. It does not claim identical locking primitives: upstream uses
kernel-released file locks, while the TypeScript port uses owned lock
directories with PID/start-time validation and stale-owner recovery.
