# INDEX.md — vault

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./vault`

---

## Scope

### `vault/` — Encrypted credential storage built on age encryption, centered on `Vault` and `Storage`


---

## Árbol de Estructura

### `vault/` — Encrypted credential storage built on age encryption, centered on `Vault` and `Storage`
`AgeStorage` implements the `Storage` interface for persisting secrets on disk with age-based encryption, while `Identity` types manage encryption keys.
- `storage.go` — Core encrypted storage implementation using age
- `identity.go` — Identity and key management for age encryption
- `provider.go` — Vault construction and credential resolution helpers
**Entry:** `storage.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/vault/fuzz_test.go`
