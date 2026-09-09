# Pi exact-session tmux resurrection

## Decision

Use Pi's native JSONL session files as the source of truth. Do not use the
generic cwd-based Pi matching from `tmux-assistant-resurrect`; do not install
`jq` unless an upstream component proves necessary. Use tmux-resurrect and
tmux-continuum for tmux layout persistence, plus a small Pi-aware mapping and
restore layer for exact session paths.

## Low-level decomposition

### Pi session source of truth

**Need:** Pi 0.85.0, its session directory, and the `session_start` context.
Pi creates JSONL files whose first record contains the session ID and cwd; the
session manager exposes the exact session file.

**Expect:** On startup, the extension records `{pane identity, cwd, session
file, session id}`. It never copies or rewrites conversation data.

**Logic:** `session_start` → read `ctx.sessionManager.getSessionFile()` →
resolve to an absolute path → write an atomic mapping record. If no tmux pane
exists, skip mapping rather than inventing an identity.

### tmux persistence

**Need:** tmux-resurrect and tmux-continuum, TPM or equivalent installation,
and additive entries in `~/.tmux.conf`.

**Expect:** Manual save and restore work; continuum periodically saves and can
auto-restore the layout after a new tmux server starts.

**Logic:** tmux saves session/window/pane layout → restore recreates panes and
working directories → the Pi-aware restore hook finds the restored pane and
launches Pi with the exact recorded session path.

### Exact pane association

**Need:** A stable key that survives tmux server restart. Numeric pane IDs do
not survive, so the mapping must use session name/window/pane indexes plus cwd
and an assistant marker, with deterministic fallback behavior.

**Expect:** Multiple Pi sessions in one cwd do not silently resume one another.
Ambiguous mappings are reported and left untouched rather than guessed.

**Logic:** Save the tmux logical pane coordinates and cwd alongside the exact
Pi file. During restore, match coordinates first, then validate cwd. If the
match is not unique, do not launch Pi automatically.

### Safe test

**Need:** An isolated tmux socket, a temporary test session, a known Pi JSONL
file, and no production session termination.

**Expect:** Save artifact exists, tmux server can be killed, restore recreates
the pane, and the restored command references the same Pi session file.

**Logic:** start isolated server → create shell/Pi marker pane → invoke save →
inspect artifacts → kill isolated server → restore from saved state → inspect
pane command and mapping → clean up temporary socket/session.

## Choreography

1. Inspect Pi's installed API/session behavior and upstream tmux plugin hooks.
2. Implement the smallest exact-session mapping extension/helper with atomic
   writes and no guessing on ambiguity.
3. Add focused tests for mapping, same-cwd ambiguity, stale files, and shell
   quoting.
4. Install TPM and tmux-resurrect/continuum only after verifying prerequisites;
   add the custom integration without overwriting unrelated tmux settings.
5. Start an isolated tmux server and run save/kill/restore tests.
6. Run syntax/tests and inspect the real user-level configuration and artifacts.
7. Document usage, rollback, manual save/restore keys, and the limitation that
   unsaved layout changes since the last save cannot be recovered.

## Breaking points and mitigations

- A power loss before a tmux save loses only recent layout metadata; Pi JSONL
  remains recoverable with `pi --resume`.
- Pane indexes can change during complex restores; require coordinate + cwd
  validation and refuse ambiguous matches.
- A stale mapping may point to a deleted session; verify file existence before
  launching and fall back to an explicit diagnostic.
- Shell quoting can turn a session path into command injection; use tmux
  `send-keys` with safely quoted arguments and test spaces/metacharacters.
- Auto-restore may duplicate a running server; test only when no server exists
  for the production socket and keep the isolated test separate.
- Existing uncommitted repository changes must remain untouched.

## Verification

- `tmux -f /dev/null -L <test-socket> ...` isolated lifecycle test.
- Mapping unit tests, shell syntax checks, and `git diff --check`.
- Confirm tmux config has no `@resurrect-processes` Pi entry.
- Confirm a restored Pi command uses `--session` and the original absolute file.
- Confirm an ambiguous same-cwd case refuses automatic restore.
