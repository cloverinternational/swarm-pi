# swarm-context / Page-Index Memory Plan

## Outcome sought

Create a new `swarm-context` Pi extension that gives users and agents explicit,
scoped, inspectable long-term context without silently turning the transcript
into an opaque memory dump. Page-Index is the reasoning/tree-index reference,
not a runtime import: `vendor/` is read-only under `AGENTS.md`.

## Evidence and constraints

- Pi assembles context at `before_agent_start` in `.pi/extensions/10-context/swarm-prompt.ts:424-466`.
- Existing memory is append-only, redacted, and workspace/session-scoped in `.pi/extensions/40-state/memory-history.ts:4-145`, but has no correction/deletion/staleness model.
- Page-Index Markdown parsing is reusable at `vendor/page-index/pageindex/page_index_md.py:32-89,192-303`; it builds heading trees with line locations and optional LLM summaries.
- Page-Index persistence is atomic JSON with a manifest and per-document files at `vendor/page-index/pageindex/local_store.py:15-25,73-186`.
- Page-Index local retrieval is agentic tree browsing, not vector ranking, at `vendor/page-index/pageindex/agent_tools.py:901-1120` and `vendor/page-index/pageindex/local_chat.py:621-641,953-975`.
- Page-Index is Python and its local SDK submission path is PDF-oriented; embedding it in the TypeScript extension would require a Python runtime, dependency management, IPC, cancellation, and error translation.

## Decision gates

1. **Runtime boundary (upstream decision):** run Page-Index out-of-process behind a narrow adapter/MCP-like boundary (recommended), or port only the Markdown/tree subset into TypeScript. Do not silently commit to either path before the manual spike. The plan must measure startup, cancellation, provider absence, packaging, and citation fidelity.
2. **Capture policy:** explicit capture only for MVP; automatic transcript capture is a later opt-in experiment. This protects user trust and avoids indexing secrets/noise.
3. **Retrieval policy:** on-demand retrieval only for MVP; never inject all memory into every prompt. Explicit retrieval may inject bounded evidence into the next turn while preserving existing prompt budgets.
4. **Scope:** namespace + workspace + session, with deliberate promotion from session to workspace/global. No cross-workspace retrieval by default.
5. **Truth contract:** every result carries provenance and freshness; “not indexed,” “no result,” “stale,” and “backend unavailable” are distinct outcomes.

### Stage 0 spike decision record

- The vendored Page-Index package cannot be imported directly in the current
  environment because its runtime dependencies (`PyPDF2`, and separately
  `litellm`/`pypdfium2`) are not installed. This is an installation/runtime
  prerequisite, not evidence that the source is invalid.
- Isolated execution of the Markdown parser/tree functions confirmed the
  useful subset: heading-based nested nodes, stable zero-padded node IDs,
  source line numbers, and node text ranges. Editing source content changed a
  SHA-256 hash, while injection-like prose remained ordinary source text.
- The upstream Markdown test explicitly verifies a no-LLM CLI path:
  `vendor/page-index/tests/test_page_index_md.py:22-50`.
- **Recommended MVP boundary after the spike:** port the small, tested
  Markdown/tree/provenance subset into the TypeScript extension and keep the
  full Python Page-Index reasoning/PDF stack out of the Pi runtime. Revisit an
  out-of-process Python adapter only after a dependency-packaging spike proves
  startup, cancellation, and provider/error translation are acceptable.
- This recommendation is limited to the Markdown memory MVP; it does not
  claim parity with Page-Index's LLM summaries or agentic retrieval.

## User stories

- As a user, I can say “remember this decision” and see what was stored, where, and at what scope.
- As a user, I can ask “what do you know about X?” and receive concise evidence with source, section/line, content hash, and indexed-at time.
- As a user, I can inspect `/swarm-context` to see indexed sources, scopes, freshness, model/provider, cost/latency, and failures.
- As a user, I can correct, delete, disable, or re-index a source by stable ID; deletion remains effective after reload.
- As a user, I can choose offline/local-only behavior and understand when a provider is required.
- As a user, I am not surprised by silent transcript capture, prompt injection from stored text, or secrets persisted in memory.

## Agent stories

- The agent has explicit `context_capture`, `context_index`, `context_retrieve`, `context_inspect`, and `context_delete` operations with strict schemas.
- Retrieval returns bounded evidence plus machine-readable citations; the agent must not present unsupported conclusions as memory.
- The agent can distinguish backend failure from an empty result and can continue the primary task when retrieval is optional.
- Retrieved content is treated as untrusted data, not instructions; source text is delimited and injection warnings are preserved.
- Headless calls return deterministic JSON and never depend on UI confirmation or rendering.

## Manual orchestration first (Boris-Cherny harness)

Before writing an orchestrator or full extension:

1. Run a hand-driven indexing/retrieval spike against 3–4 real fixtures: a short Markdown decision log, a long Markdown technical document, a transcript-like file containing misleading instructions, and a changed/stale source.
2. Run the same user questions through the candidate Page-Index path and a simple exact/heading retrieval baseline.
3. Have an isolated critic judge each result independently using a fixed JSON rubric: citation correctness, section relevance, boundedness, latency, failure clarity, and injection resistance. Mechanical checks (schema, source hashes, line ranges, timeout behavior) run before the critic.
4. Record observed vocabulary and defects, then choose the runtime boundary. Only after the manual stages are repeatable should adapter/orchestrator code be written.

## Staged implementation choreography

### Stage 0 — Baseline and contract

- Preserve dirty `vendor/opencode`; do not edit vendor sources.
- Inspect package manifests, extension load order, session entry APIs, MCP seam, and test conventions.
- Define a versioned TypeScript contract for source metadata, scope, index status, citations, retrieval result, failure categories, and tombstones.
- Define budget limits: max source bytes, max result bytes, timeout, query count, and optional model spend.

**Breakpoints:** Pi version/API mismatch; accidental vendor import; leaking raw content in logs; scope path normalization causing collisions.

### Stage 1 — Provenance-safe capture and persistence

- Add explicit capture of user-selected text/file/session-entry references.
- Reuse/redesign redaction from `memory-history`, but make redaction and consent visible in result metadata.
- Persist append-only source/index/tombstone entries through Pi session persistence, with content hash and origin references.
- Implement idempotent deduplication and stable IDs.

**Acceptance:** same content + same scope does not duplicate; secrets are redacted; session reload preserves state; deletion tombstone prevents resurrection.

### Stage 2 — Backend spike and adapter

- Implement the smallest adapter boundary selected by Stage 0: index one Markdown source and retrieve one bounded answer/evidence set.
- Keep Page-Index storage separate from Pi semantic metadata; never make the model’s opaque tree the source of truth for deletion/scope.
- Supervise child processes if Python wins: explicit executable discovery, JSON-lines or equivalent bounded IPC, timeout, cancellation, exit classification, stderr redaction, and no shell interpolation.
- If TypeScript wins: port only the tested Markdown parser/tree shape and keep the Page-Index provenance note; do not recreate PDF logic.

**Acceptance:** unavailable backend is actionable and non-fatal; cancellation leaves no half-committed index; citations resolve to source hash + line/section; stale hash is detected.

### Stage 3 — Agent tools and explicit retrieval

- Register the five operations with structured schemas and native renderers.
- Default retrieval is opt-in and bounded; no automatic prompt injection.
- On explicit retrieval, return evidence to the agent and optionally provide a separate prompt-context injection path that preserves existing context budgets.
- Enforce scope and policy before backend calls; reject cross-workspace requests unless explicitly authorized.

**Acceptance:** headless and interactive outputs are semantically identical JSON; optional retrieval failure does not block the primary task; unsupported claims are not silently upgraded to facts.

### Stage 4 — Human inspection UX

- Add `/swarm-context` as an inspection command, not the semantic authority.
- Show source IDs, scope, status, freshness, provider/model, latency/cost, and safe error summaries.
- Add actions for re-index, stale marking, disable, correction, and deletion; destructive operations require confirmation interactively and explicit parameters headlessly.

**Acceptance:** a user can answer “what is indexed, why was it used, and how do I remove it?” without reading logs or raw JSON.

### Stage 5 — Verification and calibration

- Unit-test contracts, redaction, scopes, hashes, tombstones, stale detection, source line citations, and bounded outputs.
- Integration-test the adapter with provider missing, timeout, malformed response, cancellation, prompt-injection fixture, and changed source.
- Run the isolated critic on candidate retrieval outputs; compare against human judgments and tighten the rubric when the critic passes a result users reject.
- Run focused TypeScript tests, package checks, and the manual fixtures before broader validation.

## Non-goals for MVP

- Automatic capture of every conversation.
- Vector database or opaque embedding index.
- PDF/OCR/image support in the Pi extension.
- Cross-workspace/global retrieval by default.
- Silent background model calls or mandatory cloud storage.
- Treating Page-Index’s Python implementation as a runtime dependency without a packaging/installation plan.

## Verification report requirements

Every implementation phase must report exact files changed, commands run, fixture results, latency/cost observations, and unverified gaps. Do not claim memory quality from a successful index build alone; require citation-level evidence and isolated critique.
