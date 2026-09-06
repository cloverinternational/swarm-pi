# INDEX.md — lint

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./lint`

---

## Scope

### `lint/` — Config and ProcessMessage implement the swarmlint static analyzer for Swarm SDK contract enforcement.

---

## Árbol de Estructura

### `lint/` — Config and ProcessMessage implement the swarmlint static analyzer for Swarm SDK contract enforcement.
Detects violations of ordered-blocks access, message construction, custom client, and documentation contracts at compile time.
- `swarmlint.go` — Core linter logic and analyzer entry point
- `swarmlint_settings.go` — Configuration and settings

**Entry:** `swarmlint.go:L1`

### `lint/cmd/swarmlint` — Command swarmlint runs the Swarm SDK contract linter.
- `main.go` — CLI entry point
**Entry:** `main.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/lint/README.md`
- `swarm-sdk/lint/cmd/swarmlint/main.go`
- `swarm-sdk/lint/swarmlint.go`
- `swarm-sdk/lint/swarmlint_settings.go`
- `swarm-sdk/lint/swarmlint_test.go`
- `swarm-sdk/lint/testdata/src/testpkg/testpkg.go`
