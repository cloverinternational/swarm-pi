# INDEX.md — plugins

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./plugins`

---

## Scope

### `plugins/` — Plugin lifecycle manager exposing `Loader` for installing, loading, and querying plugins


---

## Árbol de Estructura

### `plugins/` — Plugin lifecycle manager exposing `Loader` for installing, loading, and querying plugins
Provides `NewLoader` to create a `Loader` instance that handles discovery, installation, enabling, and command/agent resolution from local and marketplace sources.
- `loader.go` — core `Loader` type and plugin loading logic
- `marketplace.go` — marketplace index fetching and search
- `registry.go` — plugin registry and metadata tracking
**Entry:** `loader.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar plugin OAuth | `engine/plugins/` |

---

