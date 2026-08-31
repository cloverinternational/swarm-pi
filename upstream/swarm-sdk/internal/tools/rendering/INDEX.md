# INDEX.md — rendering

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./rendering`

---

## Scope

### `tools/rendering/` — Renderable types for ANSI, HTML, and plain-text output formats


---

## Árbol de Estructura

### `tools/rendering/` — Renderable types for ANSI, HTML, and plain-text output formats
Provides `ANSIRenderable`, `PlainTextRenderable`, and `RenderableHTML` structs along with factory functions like `CreateRenderable` and `AutoDetectAndRender` for content-type detection and rendering.
- `types.go` — Core interfaces, shared types, and factory functions
- `ansi.go` — ANSI escape-code–aware rendering via `ANSIRenderable`
- `html.go` — HTML rendering via `RenderableHTML`
**Entry:** `types.go:L1`

---

