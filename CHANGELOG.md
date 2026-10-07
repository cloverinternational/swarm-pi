# Changelog

Release metadata is kept in `package.json`, `update-manifest.json`, and this file together.

## [Unreleased]

## [0.6.0] - 2026-10-07

- Add the independently selectable `ask-user` extension with structured
  questionnaires, replay/settings UI, documented interaction contracts, and
  205 focused tests. Include its dedicated test suite in CI.
- Separate mandatory-compaction behavior and tests into the current extension
  organization.
- Add the prompt-context and autogenskills compatibility corrections included
  in this release.

- Flatten Pi extensions into individually selectable entries, preserve the tool surface, adopt native settlement/continuation, scope compatibility handlers, and repair macOS PTY execution and installation diagnostics.

- Add an open issue triage script under `tools/experiments` for batching repository issue review.
- Accept live tool results as TaskManage evidence, so in-session tool output can back completion answers.
- Rely on Pi's native CodeMode tool instead of the custom codemode registration.
- Improve the conversation footer: workspace/branch and model/state reporting plus schedule status countdowns.
- Add cited answers to scoped memory lookup: `memory_history ask`/`recall` return validated source cards alongside bounded answers.
- Correct blocked tool choices within the same session through the shared hook correction coordinator, with escalation after repeated attempts.
- Compact sessions at a fixed token threshold with mandatory compaction instead of waiting for pressure.
- Add a runtime switch (`/trebol-toggle`, Ctrl+N) for the Trebol extension pack with shape-checked reload and no per-press config writes.
- Reconcile TaskManage #38 review evidence and guard validation error decoration.
- Add `tools/repo/list-env-keys.py` to print environment variable names from a
  JSON array without exposing values, with optional prefix filtering.

- Accept canonical numeric task references in TaskManage: integer `taskId`,
  `parentTaskId`, `addBlocks`/`addBlockedBy` entries and `{ref}` targets are
  coerced to the decimal-string IDs returned by `op:list`/`op:create`, and
  `{"ref":"<task id>"}` now resolves a literal task ID when no key binding
  matches (cloverinternational/trebol#33, #35, #36).
- Match TaskManage evidence headings with punctuation (?, parentheses, commas)
  punctuation-insensitively on both sides, while exact and dash-slug matches
  keep working (cloverinternational/trebol#36).
- Repair bootstrap planner replies automatically: fenced JSON with trailing
  prose, unwrapped JSON followed by commentary, and payloads with trailing
  text no longer degrade bootstrap; truncated JSON still fails with a clear
  error.
- Split TaskManage evidence reference lists on `;` and `,` even when no
  whitespace follows the separator, so `a.md#X;b.md#Y` resolves both files
  like `a.md#X; b.md#Y` already did; separators inside headings that do not
  start a path-like reference remain unsplit.
- Resolve string task references in `addBlocks`/`addBlockedBy` against the
  persistent key ledger as well as the current batch, matching the
  `{"ref":…}` form so dependency arrays no longer fail with not-found when
  they name a task created in an earlier batch.
- Redesign the interactive Bash tool card to the Oh My Pi-style framed layout: framed call card, status header with success/error/timeout icons, labeled Output section with the same tail-window collapse, and a dim Wall/Timeout/Exit stats line.
- Add the reusable framed-card drawing helper for tool cards and let `renderShell: "self"` tools own their own collapse in the default renderer wrapper.
- Add a dedicated Read tool renderer (image-aware result summary and call card) and register it with the Read tool.
- Correct stale validation and parity documentation in AGENTS.md (removed `test:parity`/`parity:probe` references, fixed global-install import notes).
- Document global `pi install` usage in the README.

## [0.5.0] - 2026-09-28

- Require Mermaid diagrams in substantial explanations through all system-prompt paths, repair eligible Mermaid syntax errors, and add a one-shot follow-up hook.
- Improve history search resource handling and matching; refine tool gating and lifecycle behavior.
- Add renderer audit and capture tooling, documentation, and cleanup of accidentally tracked build/debug artifacts.
- Update task-management contracts and validation guidance.
- Check out submodules in the CI build-and-test job so the import-resolution check sees pinned vendor sources.

## [0.4.0] - 2026-09-23

- Add evidence-backed memory candidate capture, explicit offer handoff, bounded
  review, PageIndex source discovery, cited retrieval, and read-only memory-agent
  lookup. Keep unverified candidates distinct from verified knowledge; expose
  `/memory-capture`, `/memory-review`, and interactive `/memory-promote` controls.
- Capture TaskManage questions and evidence-backed answers; tighten task
  execution and bootstrap guidance, restore task state, and add the read-only
  `/tasks` browser.
- Add `/init` project interviews and inspect → plan → approved apply scaffolding,
  with workspace structure and Bash/path safeguards; improve tool argument
  contracts and diagnostics across history, MCP, vault, and CodeMode.
- Add `/supervisor` controls for optional Jev knowledge and operational audits,
  bounded memory maintenance, and reviewer status; include staged backfill and
  skill-curation tooling for manual review rather than automatic promotion.
- Add Clover startup notices and improve TUI tool renderers, hook lifecycle,
  agent settling, and prompt reassembly across workspaces and settings.
- Surface Paseo auto-start failures in startup notices without changing the
  explicit `/paseo setup apply` authorization introduced in v0.3.0. Retire the obsolete
  Swarm parity probes and fixtures; document retrieval and review limitations.
- Read the installed package version for update checks instead of a stale
  hard-coded value; keep release metadata, tests, and import checks consistent.

## [0.3.0] - 2026-09-17

- Add a provider-neutral long-running monitor agent with bounded retries,
  persisted observations, and pause/resume/cancel controls.

- Use Tailscale LocalAPI ETag/If-Match conditional writes to reject concurrent
  Serve changes, preserve JSON responses up to an explicit 8 MiB bound, and
  revalidate process start-time identity immediately before stopping a daemon.
- Require explicit `/paseo setup apply` or tool `apply: true` before modifying hostname
  configuration or Tailscale Serve; ordinary session startup does not expose the daemon.
- Retain daemon records on failed stops, clean up failed launches, support legacy
  stop records, and serialize update/build with lifecycle operations. Reject
  non-target Serve routes and anchor Linux atomic writes to directory descriptors.

- Automate per-machine Paseo Tailscale setup: preserve nested hostname config,
  hot-reload without restarting agents, configure nonconflicting persistent
  tailnet-only HTTPS routes, and verify HTTP plus WebSocket readiness.
- Use installation-relative Paseo paths, per-user daemon state, loopback defaults,
  bounded asynchronous subprocesses, and shared setup/start locking. Add focused
  regression tests and document installation prerequisites and limitations.

- Harden tool reliability, history argument normalization, and TaskManage validation.

- Add optional `/mem on|off|status` bootstrap memory enforcement with prompt and tool-call safeguards.

- Align HistorySearch field normalization and segment filtering with case, runtime, sorting, and ordering options.

- Add the paseo tool extension and register it in the 30-tools layer.
- Auto-start Paseo sessions with Tailscale-aware listening and add mobile relay pairing.
- Add a bootstrap-settings adapter that augments the native /settings panel.
- Mark the paseo vendor submodule as shallow.

- Add a repository-backed update checker with hourly checks (exposed as
  `/trebol-update` in v0.4.0).
- Allow overriding the update manifest URL with `PI_SWARM_UPDATE_URL` for testing.
- Add CI: mandatory changelog gate, release-metadata consistency, build and test.
- Add the Plexus OpenCodeReview (luna) advisory PR review workflow.
- Refine bootstrap selector evidence, shared-memory namespaces, and renderer output.
- Register the agent-mcp and oh-my-pi vendor submodules in `.gitmodules` so CI checkout succeeds.
- Sync `package-lock.json` with the `@pi-swarm/bootstrap` workspace so `npm ci` succeeds.
- Make tests CI-safe: honor explicit `headless: false` in InteractionBroker and derive the project name from the checkout path.

## [0.2.1] - 2026-09-15

- Accept the Kitty keyboard protocol CSI-u encoding of Enter in the running-work
  footer so inspection opens when Pi enables enhanced key reporting.
- Strip ANSI/OSC escapes and hard-wrap inspection text so a wide child transcript
  line cannot trip the editor's width validation and terminate the TUI.
- Treat a missing child transcript as an expected cleanup race: fall back to
  buffered output instead of surfacing a raw filesystem path, and catch rejected
  editor promises so a closing child view cannot raise an uncaught rejection.
- Add an optional per-user systemd unit installer for reboot-safe Paseo startup
  on Linux.
- Write goal-evaluation transcripts with 0600 permissions and a bounded size, and
  reject model-supplied regexes that can backtrack catastrophically.
- Treat combining marks and ZWJ sequences as zero-width in the metrics footer so
  accented text and emoji are no longer truncated early.

## [0.2.0] - 2026-09-11

- Harden bootstrap task handoff so generated guidance is attached through valid TaskManage operations.
- Make Forge prompt transparency explicit for debugging and testing.
- Clarify vault tool and command descriptions for stored connection entries.

## [0.1.0] - 2026-01-01

- Initial Pi-Swarm extension pack release.

To publish a release, update the root package version, this changelog, and
`update-manifest.json` in one commit, then tag the commit. The checker only
notifies; `/trebol-update install` delegates installation to Pi with
`pi update --extensions`.
