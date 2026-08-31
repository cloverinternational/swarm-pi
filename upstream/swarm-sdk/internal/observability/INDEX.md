# INDEX.md — observability

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./observability`

---

## Scope

### `observability/` — LocalTracer and supporting observability types for tracing, auditing, and logging


---

## Árbol de Estructura

### `observability/` — LocalTracer and supporting observability types for tracing, auditing, and logging
Provides `NewLocalTracer` and `NewLocalJSONLTracer` constructors, span lifecycle functions (`StartSpan`, `StartSpanWithOptions`, `SpanFromContext`, `InjectContext`), and interfaces for `Sink`, `Exporter`, `Auditor`, `Redactor`, and `LookupStore`.
- `local_tracer.go` — LocalTracer implementation and span context propagation
- `noop_tracer.go` — No-op tracer implementations
- `nop_logger.go` — No-op logger implementations
**Entry:** `local_tracer.go:L1`

### `observability/noop/` — No-op implementations of the `observability.Logger` and `observability.Tracer` interfaces
All operations discard data, making these suitable for tests or contexts where observability is not needed.
- `noop.go` — No-op logger and tracer implementations
**Entry:** `noop.go:L1`

---

