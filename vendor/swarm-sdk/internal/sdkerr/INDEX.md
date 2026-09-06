# INDEX.md — sdkerr

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./sdkerr`

---

## Scope

### `sdkerr/` — SDK error type with categorization, wrapping, and tracing support


---

## Árbol de Estructura

### `sdkerr/` — SDK error type with categorization, wrapping, and tracing support
Provides the `Error` type with retry categories (Transient, Permanent, Steering, Critical), functional-option constructors (`Wrap`, `Capture`), and attribute/tracing helpers for structured error propagation.
- `error.go` — core error type and category definitions
- `constructors.go` — error construction and option functions
- `helpers.go` — error inspection utilities
**Entry:** `error.go:L1`

---

