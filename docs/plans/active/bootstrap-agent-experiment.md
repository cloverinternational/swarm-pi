# First-call bootstrap experiment

## Goal
Test whether specialized startup agents improve relevant memory and skill use and initial task quality. Do not enable mandatory bootstrap or change production memory scope during this experiment.

## Consultation
Four tool-disabled `pi -p --model clover-plexus/claude-fable-5` consultations completed successfully: architecture, safety/budgets, evaluation, then adversarial synthesis. Recommendations conflicted on task-drafter value; these are design opinions, not performance evidence. Measure instead of assuming.

## Experimental arms (counts exclude main agent)
0. Main-agent-only baseline.
1. Combined memory/skill selector; main agent drafts initial tasks.
2. Combined memory/skill selector, then separate task drafter.
3. Parallel memory and skill selectors, then separate task drafter consuming both results.

Use the same Fable model, request, evidence budgets, output limits, and corpus for each arm. Arm 1 is the provisional simplest candidate, not a predetermined winner.

## Sequence and dependencies
1. Inspect repository contracts, agent runner, context retrieval, skill activation/accounting, task persistence and hooks. Verify actual interfaces before implementing the experiment.
2. Build frozen, explicitly synthetic repository/worktree/global memory fixtures and a skill catalog. Include applicable and irrelevant entries, stale branch facts, conflicting evidence, empty recall, and instruction-injection text. Global examples must be deliberately shareable, not private data from other projects.
3. Implement a bounded opt-in runner using real `pi -p` Fable consultations. Leaf agents cannot spawn agents or modify files/stores. Resolve returned IDs against the supplied corpus; unknown IDs fail validation. Selected text remains untrusted evidence. Each subprocess has cancellation, timeout and output limits.
4. Run a small screening set across all arms, then repeat promising arms on a broader fixed set. Report evidence relevance, unsupported claims, missing constraints, task usefulness, wall time, token usage where available and errors. Count all nested calls. Do not report pricing without authoritative prices.
5. Inspect results and recommend topology from measured tradeoffs. Main agent owns judgment; separate task drafter outputs are proposals. No production tasks are created by the fixture benchmark.
6. If screening supports adoption, exercise an explicit opt-in bootstrap first-call integration in an isolated Pi session. This returns bounded evidence, skill recommendations and proposed tasks. Actual main-session Skill loading must use the existing activation path; recommendation alone cannot unlock its budget. One coordinator commits validated tasks, never selector agents.
7. Present measured results and production integration gaps before proposing default enforcement.

## Runtime invariants to verify
- No recursively triggered bootstrap; leaf tools structurally restrict spawning and writes.
- Parent cancellation terminates workers; partial results are labeled, not fabricated.
- Cache keys include task/session identity, scope, request and memory/skill revisions. Task commit identity is separate so evidence refresh does not duplicate existing tasks.
- Bootstrap has bounded separately reported accounting; it cannot fabricate skill activation or reset main-agent budgets. No skill creation required simply to unlock the experiment.
- First-call behavior is explicit, not interception of a pending mutation.
- Skills are trusted through the normal registry/loading path; memory cannot expand permissions.
- No blanket bypass for single-file work. Explicit no-context cases and no useful candidates may avoid spawning, with the reason recorded.
- Repository/worktree/global scopes appear in fixtures; production cross-session persistence is not claimed from fixtures. Current session-entry stores require a separately verified durable scope implementation before production rollout.

## Breaking points and impact
Existing spawn APIs may differ from assumptions; verify before integrating. Hooks may reset or block bootstrap workers; isolated benchmark disables extensions, while integration probes deliberately exercise real hooks. Task drafting may add latency without quality gain; arm comparison detects this. Retry may duplicate tasks; test actual persisted state. Model usage may be unavailable; report unknown rather than estimate silently. Shared repository identity and global promotion remain required production work, not implicitly solved by this experiment.

## Acceptance evidence
Keep exact prompts, model identity, bounded outputs, timing, scoring rubric, and failures. Include a real isolated session probe for skill activation and task persistence if integration reaches that stage. Unit tests or in-memory append stubs are not live persistence dogfood. Do not enable mandatory first-call enforcement without a subsequent approved production plan.
