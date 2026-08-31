# INDEX.md — claude

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./claude`

---

## Scope

### `converters/claude/` — Provides the `ClaudeCodeConverter` for importing and exporting Claude Code conversations.

---

## Árbol de Estructura

### `converters/claude/` — Provides the `ClaudeCodeConverter` for importing and exporting Claude Code conversations.
Bridges the SDK's `conversation` types with Claude Code's JSONL session formats.
- `converter.go` — Core converter implementation and session structures
- `jsonl.go` — JSONL serialization and deserialization logic
**Entry:** `converter.go:L1`

---

