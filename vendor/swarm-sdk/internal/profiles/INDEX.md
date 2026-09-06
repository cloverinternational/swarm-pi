# INDEX.md — profiles

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./profiles`

---

## Scope

### `profiles/` — Provides the canonical agent profile system centered around the `Manager` type


---

## Árbol de Estructura

### `profiles/` — Provides the canonical agent profile system centered around the `Manager` type
Manages named sets of per-role model fallback chains with persistence to `agent_profiles.json`.
- `manager.go` — Implements `Manager` and profile CRUD methods
- `types.go` — Defines `ProfilesConfig` and role configuration types
- `builtin.go` — Provides built-in profile generation
**Entry:** `manager.go:L1`

### `profiles/bridge/` — Profile-to-execution bridge providing functions like RoleTypeToModelAlias and BuildRoleModelSelector
Avoids import cycles between the profiles and builtin packages by encapsulating role-to-model mapping and selection logic.
- `role_selector.go` — Maps RoleType to ModelAlias and resolves role chains
**Entry:** `role_selector.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/profiles/bridge/CONTRACT.md`
- `swarm-sdk/profiles/bridge/role_selector.go`
- `swarm-sdk/profiles/bridge/role_selector_test.go`
- `swarm-sdk/profiles/manager.go`
- `swarm-sdk/profiles/types.go`
