# INDEX.md — interaction

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./interaction`

---

## Scope

### `interaction/` — The `interaction` package provides a user interaction system centered around the `BrokerAdapter` type, which adapts a `UserInteractionBroker` to the legacy `tools.

---

## Árbol de Estructura

### `interaction/` — The `interaction` package provides a user interaction system centered around the `BrokerAdapter` type, which adapts a `UserInteractionBroker` to the legacy `tools.ApprovalBroker` interface.
- `adapter.go` — Adapts UserInteractionBroker to legacy ApprovalBroker interface
- `broker.go` — Core interaction broker and request/response functions
- `hosted.go` — Hosted environment interaction integration
**Entry:** `adapter.go:L1`

### `interaction/visual/` — Defines `VisualPrimitive` interface and concrete primitive types for the visual_choice question type
No rendering logic lives here; types are data-only with JSON serialization support.
- `primitives.go` — Tagged-union interface and primitive structs (Options, Cards, SplitCompare, Markdown, Mermaid)
**Entry:** `primitives.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/interaction/adapter.go`
- `swarm-sdk/interaction/broker.go`
