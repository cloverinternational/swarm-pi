# INDEX.md — account

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./account`

---

## Scope

### `account/` — Credential broker and account fallback chain management


---

## Árbol de Estructura

### `account/` — Credential broker and account fallback chain management
Provides the `AccountFallbackChain` interface and `StandardAccountFallbackChain` implementation for ordered account selection with automatic fallback on failure, along with `CredentialBroker` and `CredentialBrokerProvider` interfaces for credential resolution.
- `fallback_chain.go` — Fallback chain interface and standard implementation
- `profile_store_impl.go` — Credential broker and profile storage
**Entry:** `fallback_chain.go:L1`

---

