# Annoyed — local product-friction board

A Pi extension that records actionable defects locally instead of publishing
anything remotely. Its automatic annoyance-nudge hook observes failed tool
results and gives the model one structured prompt to decide whether to record
real product friction. It stores the source of truth in `~/.pi/annoyed/annoyed.sqlite`
and maintains an atomic, human-readable export at `~/.pi/annoyed/issues.json`.

## Tool

`annoyed` accepts `issue`, `title`, `category`, `severity`, `observed`,
`expected`, `evidence`, `acceptance_tests`, and `tags`. Repeated fingerprints are
merged into one card and increment `occurrences`; every observation is retained
in `issue_events`.

## Board

- `backlog` → `triage` → `accepted` → `in_progress` → `verified`
- terminal alternatives: `wont_fix`, `duplicate`

Commands:

- `/annoyed` — board summary
- `/annoyed all` — all cards
- `/annoyed backlog` — one column
- `/annoyed move <id> <status>`
- `/annoyed severity <id> <low|medium|high|critical>`

The directory is created with mode `0700`; database and JSON export use `0600`.
SQLite WAL/foreign keys/busy timeout are enabled. Transcript capture is bounded
to the last 80 session entries and remains local.

## Runtime note

This uses Node's built-in `node:sqlite`, avoiding native npm dependencies. It
requires the Node runtime bundled with current Pi releases (Node 22.5+).
