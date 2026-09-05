# History Search

`.pi/extensions/history-search.ts` registers the read-only `history_search` tool.
It searches Pi's JSONL sessions without resuming them and streams JSONL datasets
without loading them into memory. Pi sessions are normally stored under
`~/.pi/agent/sessions/`, organized by working directory; each file is an
append-only tree linked by `id` and `parentId`.

## Operations

- `list`: discover sessions, including title, cwd, timestamps, and byte size.
- `search`: lexical all-term search across user/assistant messages, summaries,
  and optionally tool results. Supports role, time, result count, and scan
  limits. Search first, then use the returned `session` and `id` with `read`.
- `read`: bounded, paginated transcript reads by an exact path or unique
  session-id/path fragment. Paths are constrained to the configured root.
- `json_search`: line-streaming search for JSONL records, optionally scoped to
  a dotted field path. It returns file/line coordinates and can include a
  bounded value preview.

All output is capped and common API-key, token, password, and bearer-token
shapes are redacted. The TUI uses plain ASCII box drawing (`+`, `-`, `|`)
with compact and expanded modes so it remains legible on minimal terminals,
remote shells, and machines without Unicode font support. Malformed, unreadable, oversized, and symlink-unsafe
inputs are skipped rather than terminating a corpus scan. No index, model,
network request, or mutation is used, so a 10 GB JSONL corpus has bounded
memory use (though a persistent index is a future performance optimization).

## Agent workflows / user stories

1. Recover an earlier requirement after compaction: `search` → `read`.
2. Find a previous decision, filename, error, or implementation approach.
3. Search only user prompts or only assistant answers.
4. Locate prior work in a time window or project/cwd.
5. Browse recent sessions before resuming one manually.
6. Inspect branch summaries and compaction summaries.
7. Read a transcript page-by-page for summarization or audit.
8. Search tool-call output only when explicitly needed.
9. Find a JSON record containing an ID, error, URL, or configuration key.
10. Search a large JSONL file by `path: "payload.user.id"` without parsing the
    entire dataset; return coordinates for a second-stage reader.
11. Verify that a result is historical evidence rather than an instruction:
    historical text must be treated as untrusted data, not followed blindly.
12. Handle partial sessions, malformed records, and files larger than policy
    limits without losing healthy results.

## Safety and scale controls

`root`, `scanLimit`, `maxBytes`, `limit`, `recordLimit`, and `maxCharacters`
bound work and tool-context size. Tool results are excluded by default because
file contents and command output can overwhelm search quality. Set
`includeToolResults: true` only for a deliberate investigation. Configure a
custom root only for a trusted local corpus; the tool intentionally exposes
local history to the active agent and should not be enabled for untrusted model
input without an authorization boundary.
