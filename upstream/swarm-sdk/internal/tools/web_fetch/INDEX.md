# INDEX.md — web_fetch

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./web_fetch`

---

## Scope

### `tools/web_fetch/` — `Tool` type for fetching web pages and converting HTML to markdown


---

## Árbol de Estructura

### `tools/web_fetch/` — `Tool` type for fetching web pages and converting HTML to markdown
Implements the tool contract with `New` constructor, HTTPS upgrade, same-origin redirect handling, a 15-minute LRU cache, and 100k-character truncation.
- `web_fetch.go` — core `Tool` struct, `New`, `Execute`, and redirect/HTTPS logic
- `cache.go` — LRU content cache with 50 MB cap
- `html_to_markdown.go` — HTML-to-markdown conversion
**Entry:** `web_fetch.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/web_fetch/html_to_markdown.go`
