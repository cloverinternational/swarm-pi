# INDEX.md — cloud

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./cloud`

---

## Scope

### `cloud/` — Cloud authentication via `LoginWithHostedUI` and token management with `GetValidTokens`


---

## Árbol de Estructura

### `cloud/` — Cloud authentication via `LoginWithHostedUI` and token management with `GetValidTokens`
Also provides catalog caching through `CatalogCacheManager`.
- `auth.go` — Hosted UI login and login options
- `sessions.go` — Token refresh and validation
- `catalog.go` — Catalog cache management and persistence
**Entry:** `auth.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/cloud/auth.go`
- `swarm-sdk/cloud/auth_refresh_test.go`
- `swarm-sdk/cloud/catalog.go`
- `swarm-sdk/cloud/catalog_document.go`
- `swarm-sdk/cloud/catalog_test.go`
- `swarm-sdk/cloud/client.go`
- `swarm-sdk/cloud/cloud_agents_integration_test.go`
- `swarm-sdk/cloud/config.go`
- `swarm-sdk/cloud/config_test.go`
- `swarm-sdk/cloud/device_id.go`
