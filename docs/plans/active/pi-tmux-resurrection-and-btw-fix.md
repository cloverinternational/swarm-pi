# Pi tmux resurrection and `/btw` fix

## Goal

Make Pi sessions in tmux recover the exact Pi JSONL session after a reboot or
tmux-server restart, and fix the local `/btw` command failure:
`appendSources.map is not a function`.

## Low-level breakdown

### `/btw` bug

**Need:** the Pi `DefaultResourceLoader` contract for `appendSystemPrompt`.
The current vendored API declares it as `string[]`; the local extension passes a
single joined string. During loader reload Pi calls `.map()` on that value.

**Expected:** the side session loads the main prompt plus the side-agent prompt
without throwing, while retaining read-only tools and main-session context.

**Data flow:** command → `ctx.getSystemPromptOptions()` → construct loader →
`loader.reload()` → loader maps each append source → create isolated session.
The invariant is that `appendSystemPrompt` is always an array of strings.

### Exact Pi resurrection

**Need:** `pi-tmux-session-map` records the exact JSONL session file for each
stable tmux pane key; `tmux-assistant-resurrect` saves that mapping and restores
the pane layout/session. The restore command must be a wrapper that resolves the
pane mapping and runs `pi --session <file>`.

**Expected:** after a reboot, tmux starts, continuum restores panes, and each Pi
continues its own previous conversation. If a mapping is unavailable, the
wrapper falls back to `pi --continue` rather than failing silently.

**Data flow:** Pi lifecycle event → mapping file → tmux-resurrect save → reboot
→ tmux-continuum restore → assistant restore hook → `pi-tmux-resume` → exact
session file. The invariant is that Pi is not also listed in
`@resurrect-processes`, preventing duplicate launches.

## Choreography

1. **Fix the loader shape.** Change `appendSystemPrompt` from a joined string to
   an array. This is independent of tmux work and directly addresses the
   reported exception.
2. **Add focused regression coverage.** Add a small source-level/contract test
   or validation that the loader receives an array and that the `/btw` manifest
   remains registered. This depends on step 1.
3. **Install the Pi mapping extension.** Use the official npm package and reload
   Pi. Verify its mapping directory and extension registration. This depends on
   the live-install decision and does not modify repository vendor code.
4. **Install assistant-resurrect through TPM.** Clone TPM if absent, add
   `tmux-resurrect`, `tmux-continuum`, and `timvw/tmux-assistant-resurrect` to
   `~/.tmux.conf`, and initialize/reload tmux. Preserve the existing prefix and
   unrelated user settings. Do not add Pi to `@resurrect-processes`.
5. **Install the exact-session wrapper.** Add `pi-tmux-resume` to `~/bin` using
   the documented stable pane-key/hash lookup and `pi --continue` fallback.
   Verify it is executable and on `PATH`.
6. **Exercise save/restore safely.** Check tmux plugin status, manually save,
   inspect the generated assistant/mapping state, and perform a non-destructive
   restore check. Do not kill an active production Pi session without explicit
   confirmation.
7. **Document the setup.** Add a repository-owned operations note with commands,
   rollback steps, prerequisites, and known caveats. Update `AGENTS.md` only if
   repository ownership/inventory changes.

## Breaking points and mitigations

- Pi versions before the mapping extension's required version may lack
  `agent_settled`; check the installed Pi version before enabling it.
- `appendSystemPrompt` is an array in the current Pi API; passing a string causes
  the exact reported `.map` exception.
- Continuum depends on tmux status hooks; preserve the existing status line and
  confirm the plugin is loaded.
- Multiple Pis in one directory can otherwise collide; the mapping extension's
  stable pane key avoids this, but pane reordering can still associate a mapping
  with a sibling pane. Document this caveat.
- Resurrecting Pi both through `@resurrect-processes` and the assistant hook
  causes duplicate launches; explicitly avoid that configuration.
- A live install can fail due to network, missing TPM, or unavailable npm. Use
  idempotent checks and verify side effects before retrying.
- Existing uncommitted repository changes and unrelated vendor changes must not
  be reset or overwritten.

## Ripple analysis

- The loader fix affects only `.pi/extensions/50-ui/swarm-btw.ts` and the side
  session's prompt construction.
- The Pi extension install affects global Pi state, not repository source.
- tmux configuration affects all tmux sessions, so additions must be isolated,
  ordered after existing settings, and reversible by removing only added lines.
- The wrapper affects only resurrected Pi panes; its fallback preserves normal
  `pi --continue` behavior.

## Verification

- `git diff --check`.
- Focused source/contract test for `appendSystemPrompt: string[]`.
- Pi `/reload` followed by `/btw test question` in an interactive session.
- `tmux show-options -g` confirms continuum/resurrect hooks and no Pi process
  entry in `@resurrect-processes`.
- Wrapper shellcheck-equivalent syntax check and controlled mapping/fallback
  tests.
- Manual save/restore evidence without terminating the user's active work.
