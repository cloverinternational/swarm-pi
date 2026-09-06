# INDEX.md — envelope

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./envelope`

---

## Scope

### `internal/envelope/` — Polymorphic `Envelope` type and `Handler` for normalizing provider events into canonical form


---

## Árbol de Estructura

### `internal/envelope/` — Polymorphic `Envelope` type and `Handler` for normalizing provider events into canonical form
Defines `Envelope` with `CanonicalPayload` and related structs, plus a `Handler` that adapts via an injected `TransformRegistry` and `EnvelopeStore` to process and normalize events from multiple providers.
- `envelope.go` — Core `Envelope` struct, `CanonicalPayload`, and construction helpers
- `handler.go` — `Handler` struct with registry-driven transform and callback hooks
- `store.go` — `EnvelopeStore` persistence layer
**Entry:** `envelope.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/internal/envelope/store.go`
