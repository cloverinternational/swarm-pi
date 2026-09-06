# Pi ↔ Swarm Provider-Surface Parity Plan

## Goal and acceptance contract

Make matched `pi -p` and `swarm -p` runs act and work the same by converging the
JSON that reaches the model and the JSON lifecycle that comes back.

Parity is complete only when matched runs use the same:

- provider protocol, model, and maximum supported reasoning configuration;
- final system-prompt bytes and role placement;
- active tool names, descriptions, input schemas, ordering, and execution modes;
- injected skill metadata, selected skill set, and loaded instruction content;
- user/assistant/tool-result message shapes after runtime injection;
- hook decisions, permissions, errors, cancellation, and result semantics;
- observable action sequence and final result on the behavioral probe suite.

Request IDs, timestamps, transport-generated IDs, and token-usage counters may be
normalized. No prompt, schema, capability, permission, or error difference may be
normalized away.

## Known baseline mismatches

1. Pi currently defaults to `clover-plexus/luna` with medium thinking; installed
   Swarm defaults to `codex/gpt-5.6-sol` with auto reasoning.
2. Pi and Swarm assemble and place their system prompts differently.
3. Pi and Swarm expose different tool names and schemas. Swarm's startup
   preflight reports 48 tools while its request-time event exposes 50.
4. Pi's broad skill registry discovers hundreds of installed skills, but native
   Pi's existing `<available_skills>` block can suppress injection of that broad
   registry. Swarm does not discover `.pi/skills`.
5. Pi registers `goal_create` twice; the later control-task registration
   overwrites the reviewed goal-loop schema.
6. Registration catalogs, active tools, slash commands, and provider-visible
   tools are currently conflated by some diagnostics.
7. Swarm headless persistence and Pi `--no-session` behavior differ.
8. The installed Swarm binary and the available Swarm source checkout are not
   the same commit, so installed-binary probes remain the runtime authority.

## Phase 1 — Build the parity probes

1. Add a local, credential-free OpenAI-compatible capture server that records
   request bodies and returns deterministic assistant/tool-call responses.
2. Add launch adapters for Pi and Swarm using isolated temporary configuration,
   the same workspace, provider protocol, dummy model, prompt, reasoning mode,
   tool allowlist, and session policy.
3. Capture:
   - initial request JSON;
   - continuation request after a deterministic tool call;
   - tool call and tool result events;
   - final response;
   - runtime catalog and active request surface separately.
4. Add a canonicalizer that removes only volatile transport fields and emits:
   - `prompt.json` and prompt byte/hash report;
   - `tools.json` with full schemas and deterministic ordering;
   - `skills.json` with metadata and instruction hashes;
   - `messages.json`;
   - `actions.json`;
   - a JSON mismatch ledger with exact JSON-pointer paths.
5. Add tests proving the canonicalizer preserves semantic differences and does
   not hide missing tools, schema fields, prompt whitespace, role changes,
   permission outcomes, or error payloads.

Gate: one command must reproduce a baseline diff without external credentials,
mutating user configuration, or persisting conversations.

## Phase 2 — Converge provider/model and system prompt

1. Choose one common provider protocol/model route supported by both launchers
   and make maximum reasoning explicit rather than relying on defaults.
2. Move every shared prompt section to canonical assets in `swarm-prompt`.
3. Make Pi and Swarm consume those assets in the same order.
4. Separate dynamic workspace data from canonical prompt text and render it
   identically.
5. Capture at the final pre-wire boundary; detect any extension/provider layer
   that mutates the prompt afterward.
6. Add byte/hash parity tests for base prompt, context block, delegation,
   planning, task-management, skill guidance, and safety instructions.

Gate: the captured provider request has identical prompt bytes, roles, and
reasoning/model fields after normalizing transport-only IDs.

## Phase 3 — Converge skills

1. Define one canonical discovery-root and precedence policy used by both.
2. Ensure `.pi/skills`, `.swarm/skills`, installed, user, and autogen sources are
   either exposed by both or intentionally excluded from both.
3. Render one canonical, bounded skill metadata block; do not let native Pi's
   partial block suppress the complete registry.
4. Align metadata fields that affect model selection and invocation.
5. Ensure `Skill` returns byte-identical instructions and argument substitution.
6. Ensure ephemeral skill guidance is sent to the model but not compounded in
   durable user messages.

Gate: skill metadata lists and selected instruction hashes are identical, and
matched positive/negative trigger prompts invoke the same skill sequence.

## Phase 4 — Converge tools and actions

1. Use captured request schemas as authority, not `getAllTools()` or startup
   banners.
2. Establish one canonical tool manifest containing name, description, schema,
   execution mode, permission class, and adapter owner.
3. Resolve duplicate/colliding registrations, beginning with `goal_create`.
4. Implement host adapters so both provider requests expose the same canonical
   names and schemas. Hide host-only tools until an equivalent adapter exists.
5. Align file, shell, edit/patch, orchestration, search, history, scheduling,
   plan, task, vault, browser/MCP, and agent-control families.
6. Align unknown allowlist handling, disabled-tool behavior, validation errors,
   cancellation, timeout, truncation, and result envelopes.
7. Keep slash/TUI commands out of the provider tool manifest unless both expose
   them as model-callable tools.

Gate: canonical `tools.json` is identical with zero missing, extra, reordered,
or schema-different entries.

## Phase 5 — Converge hooks, permissions, and persistence

1. Map pre-tool, post-tool, user-prompt, turn, compaction, session, and
   annoyance/task/autogenskills hooks in execution order.
2. Align allow/block/modify semantics and the JSON returned to the model.
3. Align workspace boundaries, mutation/network policy, approval behavior, and
   protected paths.
4. Run sessions under isolated stores and compare what is persisted versus what
   is request-only.
5. Prevent runtime guidance and skill wrappers from accumulating across turns.

Gate: matched success, failure, denied, timeout, cancellation, and continuation
probes produce equivalent canonical message/action traces and persistence.

## Phase 6 — Behavioral dogfood loop

Run both agents with matched maximum reasoning through:

1. no-tool answer;
2. single file read;
3. search and bounded multi-file analysis;
4. safe edit in disposable fixtures;
5. failing tool call and recovery;
6. task creation/update/completion;
7. skill discovery and invocation;
8. plan-mode entry/approval/exit;
9. subagent/delegation;
10. web/search capability with mocked transport;
11. hook allow, modify, and block outcomes;
12. multi-turn continuation and compaction;
13. cancellation and timeout;
14. unknown/disabled tool request;
15. prompt-injection and protected-path adversarial cases.

For every case, diff initial request, each continuation request, tool calls,
tool results, hook events, persisted semantic messages, and final answer.
Fix the earliest divergence, rerun the focused case, then rerun the complete
matrix. Continue until the mismatch ledger is empty.

## Verification and completion

- Focused tests after each subsystem change.
- All affected package builds and tests.
- Root `npm run dogfood`.
- Fresh isolated parity capture against the installed Pi and Swarm binaries.
- Independent review of the final tool manifest, prompt hashes, skill hashes,
  action traces, and any adapter exceptions.
- Completion requires an empty mismatch ledger. Any upstream impossibility must
  be demonstrated by a minimal probe and surfaced as a blocker rather than
  silently accepted.

## Change safety and ripples

- Preserve the existing dirty worktree and do not edit `upstream/`.
- Keep probe outputs in disposable directories and redact request bodies from
  chat/logs because prompts may contain sensitive context.
- Prompt changes affect token/cache behavior and all model decisions.
- Tool schema/name changes affect hooks, CodeMode, policies, tests, and stored
  transcripts.
- Skill discovery changes affect prompt size, invocation policy, and
  autogenskills state.
- Provider/model alignment affects output determinism and must precede behavior
  comparisons.
- Installed Swarm behavior is authoritative when its source build differs from
  the checked-out source.
