# Auto Compaction Specification (Headless + IDE)

## 1) Scope and non-goals

Scope
- Headless auto-compaction in the inference pipeline (pre-message execution).
- Manual compaction via IPC (`compact`, `getCompactionStats`).
- IDE UI alignment for settings and progress indicators.

Non-goals
- Changing compaction prompts or algorithms in `sdk/compaction`.
- TUI behavior changes or new TUI commands.
- Cloud sync or settings schema expansion beyond current allowlists.

## 2) Configuration semantics

Authoritative config keys (headless `config.json`)
- `enableCompaction` (bool): master switch for auto-compaction.
- `compactionThreshold` (int): threshold to trigger auto-compaction.
- `preserveRecentMessages` (int): count of most recent messages to keep unmodified.

Backwards compatibility and IDE alignment
- IDE uses `autoCompact` and `compactionThreshold` in settings. `autoCompact` is treated as an alias for `enableCompaction` when received over IPC or in config payloads.
- `compactionThreshold` in the IDE can be percent (e.g., 92 in General Settings) or absolute tokens (e.g., 40000/60000/80000 in Compaction Settings). The headless core must interpret it as:
  - `<= 100`: percentage of the current model context window.
  - `> 100`: absolute token count.
- `compactionStrategy` is a request-level field for manual compaction. It is not an authoritative config key in headless config, but the IPC `compact` call may accept it.

Defaults (current)
- `enableCompaction`: true.
- `compactionThreshold`: 100000 tokens (headless default).
- `preserveRecentMessages`: 10.
- IDE defaults remain valid: `enableCompaction: true`, `compactionThreshold: 92` (percent) in General Settings, or 80000 in Compaction Settings.

Validation and fallback
- Missing or invalid values must not crash. Fallback to defaults when values are missing or out of range.
- If `compactionThreshold` is `<= 0`, treat it as missing and use the default.
- If the model context window is unknown, percent thresholds are treated as "not ready" and auto-compaction is skipped for that request.

## 3) Context window source of truth

Model context window
- The model context window is sourced from the selected provider model config (`ProviderConfig.Models[].ContextWindow`).
- When provider or model changes, `AppState.ModelContextWindow` must be updated from that config.
- If the provider/model cannot be resolved or the context window is missing, `ModelContextWindow` is set to 0 and auto-compaction is skipped.

Current context size
- `AppState.CurrentContextSize` is updated from the token usage payload input tokens.
- `TokenCountPayload.InputTokens` is the source of truth for current context size.
- If input tokens are unavailable, keep the last known `CurrentContextSize` and optionally fall back to estimated tokens from message history.

## 4) Auto-compaction trigger and flow

Trigger timing
- Auto-compaction is evaluated before executing a new user message, after the active provider/model and conversation are known.

Trigger conditions
- `enableCompaction` is true.
- `ModelContextWindow` is known when using percent thresholds.
- `CurrentContextSize` (or a best-effort estimate) meets or exceeds the effective threshold.

Effective threshold
- If `compactionThreshold <= 100`, `effectiveThresholdTokens = floor(ModelContextWindow * compactionThreshold / 100)`.
- If `compactionThreshold > 100`, `effectiveThresholdTokens = compactionThreshold`.

Budget-aware compaction (no truncation)
- Before compaction, compute the available input budget for the compaction model:
  - `availableTokens = ModelContextWindow - (compactionPromptTokens + reservedOutputTokens)`
- If the full history does not fit within `availableTokens`, use hierarchical chunked summarization:
  - Preserve the most recent `preserveRecentMessages` verbatim.
  - Split the remaining older messages into chunks that fit `availableTokens`.
  - Summarize each chunk, then summarize the summaries into a single compact summary message.
  - Store a rolling summary message so future compactions are cheaper.
- If the first hierarchical pass still exceeds budget, reduce chunk size and retry once.
- Raw truncation of message history is not allowed as a fallback.

Success flow
- Compaction uses `sdk/compaction` to summarize and create a new conversation.
- The new conversation becomes active; the old conversation is archived.
- State is updated with new conversation summary and updated token counts.
- A compaction progress update is emitted (see Section 6).

Failure flow
- If direct compaction fails due to budget, attempt hierarchical chunked summarization first.
- If compaction still fails after the one retry, proceed with the original conversation without compaction.
- Emit a visible notification and an error update event.
- No crash, no hard failure; the user can continue.

## 5) Manual compaction API

IPC: `compact`
- Request fields:
  - `strategy` (string, optional): summarize/hybrid. If `truncate` is requested, it is treated as summarize; no raw truncation is allowed.
  - `threshold` (int, optional): overrides config threshold for this invocation; same percent/tokens semantics.
  - `preserveRecentMessages` (int, optional): overrides config for this invocation.
- Behavior:
  - Always attempts compaction when an active conversation exists.
  - Uses the same compaction service and conversation flow as auto-compaction.
- Response fields:
  - `beforeTokens` (int)
  - `afterTokens` (int)
  - `status` (string): `compacted`, `no_change`, or `failed`
  - `approach` (string, optional): `direct` or `hierarchical`
  - `compactionCount` (int)
  - `thresholdTokens` (int)
  - `thresholdPercent` (int, optional when a percent threshold was used)

IPC: `getCompactionStats`
- Response fields:
  - `currentTokens` (int): `CurrentContextSize`
  - `maxTokens` (int): `ModelContextWindow`
  - `compactionCount` (int)
  - `thresholdTokens` (int)
  - `thresholdPercent` (int, optional)
  - `enableCompaction` (bool)

Error handling
- If there is no active conversation or no provider/model, return a structured error and emit an update notification. Do not crash or return partial data.

## 6) State and event updates

New update types
- `compaction_start`: emitted when compaction begins.
- `compaction_complete`: emitted on success.
- `compaction_error`: emitted on failure.

Compaction payload
- `mode`: `auto` or `manual`
- `status`: `started`, `completed`, `failed`
- `beforeTokens`, `afterTokens`
- `thresholdTokens`, `thresholdPercent` (optional)
- `approach`: `direct` or `hierarchical` (optional)
- `compactionCount`
- `message` (short status text for UI)

UI behavior
- The IDE shows a non-blocking progress indicator when `compaction_start` is received.
- On `compaction_complete`, the indicator resolves and a success toast or inline status message is shown.
- On `compaction_error`, the indicator resolves and a visible error message is shown.

## 7) Security and robustness

- Do not log tokens, summaries, or message content. Logs must redact or omit sensitive data.
- Compaction output is untrusted model output; it is stored as messages only and never executed as code.
- Guard against missing providers/models and empty conversations; fail gracefully with a visible notification.
- Do not drop message history without summarizing it; raw truncation is not allowed.
- Avoid accepting or applying unsafe settings from sync payloads; compaction settings must stay within allowlisted keys.
- Use TLS-only endpoints; compaction must not accept custom base URLs from synced settings.

## 8) Acceptance criteria and test plan

Spec-driven development protocol
- Tests MUST be written before implementation in later phases.
- Implementation work only begins after the relevant tests exist and fail.

Headless core
- Auto-compaction triggers before message execution when `enableCompaction` is true and threshold is exceeded.
- Percent thresholds use model context window; absolute thresholds use token count directly.
- `CurrentContextSize` is updated from `TokenCountPayload.InputTokens`.
- On success, a new conversation is created and the old conversation is archived.
- On failure, the conversation proceeds without compaction and emits `compaction_error`.
- If the conversation exceeds the compaction budget, hierarchical chunked summarization runs and completes without raw truncation.
- If hierarchical compaction still exceeds budget after one retry, the engine continues without compaction and emits `compaction_error`.

SDK bridge
- Token usage input tokens are propagated into `TokenCountPayload`.
- Compaction path uses the existing compaction service and preserves `preserveRecentMessages`.

IPC
- `compact` returns accurate before/after tokens, status, compactionCount, and thresholds.
- `compact` reports `approach=hierarchical` when chunked summarization is used.
- `getCompactionStats` returns current tokens, max tokens, compactionCount, thresholds, and enablement.
- Errors are structured and do not crash the process.

IDE
- `autoCompact` toggles map to `enableCompaction`.
- Threshold values render correctly in both percent and absolute modes.
- Compaction progress is shown non-blocking and resolves on completion or error.

Pass/fail conditions
- All tests pass (`go test ./...` and `npx vitest run`) after implementation.
- No regressions in existing behavior or settings compatibility.
