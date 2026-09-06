# INDEX.md — skills

> Modo: incremental | Directorios indexados: 4
> Para regenerar: `idx generate --path ./skills`

---

## Scope

### `skills/` — PluginDatabase and CommandHandler for skill discovery, installation, and execution


---

## Árbol de Estructura

### `skills/` — PluginDatabase and CommandHandler for skill discovery, installation, and execution
Provides PluginDatabase for managing plugin/skill discovery from marketplaces, and CommandHandler for invoking skills with positional and named argument substitution.
- `database.go` — PluginDatabase and marketplace index types
- `registry.go` — CommandHandler and skill registry
- `loader.go` — skill loading and argument substitution
**Entry:** `database.go:L1`

### `skills/builtins/swarm-skill/` — Documentation for Swarm Skill authoring
Covers skill types and the process of creating a skill for the Swarm framework.

### `skills/builtins/swarm-workflow/` — Documentation for Swarm Workflow authoring
Covers minimal working examples and execution strategies for building swarm-based workflows.
**Entry:** (none)

### `skills/examples/code-review/` — Documentation for the Code Review Skill example
Describes capabilities and usage instructions for the code-review skill.
**Entry:** (none)

---

## Señales de Cambio Reciente

- `swarm-sdk/skills/parser.go`
- `swarm-sdk/skills/defaults.go`
- `swarm-sdk/skills/loader.go`
- `swarm-sdk/skills/registry.go`
- `swarm-sdk/skills/skills_test.go`
- `swarm-sdk/skills/telemetry.go`
- `swarm-sdk/skills/telemetry_test.go`
- `swarm-sdk/skills/types.go`
- `swarm-sdk/skills/watcher.go`
- `swarm-sdk/skills/watcher_test.go`
