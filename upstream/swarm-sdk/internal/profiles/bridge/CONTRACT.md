# Profile-Execution Bridge Contract

## Package: swarm-sdk/profiles/bridge/

**Purpose**: Centralise the TUI-specific profile-to-execution glue so that downstream consumers (CLI, headless servers, custom agents) can resolve profile roles to executable model configurations without duplicating swarm-tui/internal/chat/sdk_integration.go logic.

**Rationale for sub-package**: Avoids import cycle.
```
profiles → builtin (RoleType) → agent → profiles  (CYCLE)
bridge  imports both profiles + builtin; nothing imports bridge  (BREAKS CYCLE)
```

---

## 1. NEED (Inputs)

### Required preconditions
| # | Input | Type | Source | Nullable? |
|---|-------|------|--------|-----------|
| 1 | Profile manager | `*profiles.Manager` | `profiles.NewManager("")` | Yes (nil → nil output) |
| 2 | Role type | `builtin.RoleType` | SADD workflow constants | No |

### Implicit dependencies (must be true at call time)
- Manager has loaded or generated built-in profiles (at least `balanced`)
- `agent_profiles.json` exists or will be created with defaults
- The `builtin` package's `RoleModelConfig` and `RoleModelSelector` types are stable

---

## 2. EXPECT (Outputs)

### Exported signatures
```go
// Maps SADD role to profile ModelAlias
func RoleTypeToModelAlias(role builtin.RoleType) profiles.ModelAlias

// Creates a selector function for runtime role resolution
func BuildRoleModelSelector(mgr *profiles.Manager) builtin.RoleModelSelector

// Returns a provider-name getter for sub-agent spawning
func BuildCurrentProviderGetter(mgr *profiles.Manager) func() string

// Resolves the full fallback chain for a role (convenience)
func ResolveRoleChain(mgr *profiles.Manager, role builtin.RoleType) *fallback.Chain
```

### Consumer in delegate_task.go
The `builtin.RoleModelSelector` output is consumed by:
- `agent.New(agent.Config{RoleModelSelector: selector})` — configures the agent's sub-agent routing
- `builtin.DelegateTaskTool` — when spawning sub-agents, calls `selector(role)` to get `RoleModelConfig`

The `RoleModelConfig` fields used:
- `Provider` — passed to `provider.New()` or registry lookup
- `Model` — passed as the model parameter in provider.Config
- `Chain` — used by fallback.Execute() for automatic failover

---

## 3. LOGIC (Lowest-level data flow)

### Step-by-step transformation

| Step | From | Transformation | To | Type Invariant | Failure Mode |
|------|------|----------------|----|----------------|--------------|
| 1 | `builtin.RoleType` (e.g. `"supervisor"`) | `RoleTypeToModelAlias` | `profiles.ModelAlias` (`"steering"`) | String-to-string mapping | Unknown role → `AliasSubAgent` |
| 2 | `*profiles.Manager` + `ModelAlias` | `mgr.ResolveChain(alias)` | `*fallback.Chain` | Chain has Primary provider/model | Alias not configured → fallback to `AliasMain` |
| 3 | `*fallback.Chain` | `BuildRoleModelSelector` closure | `builtin.RoleModelSelector` | Function value | nil Manager → nil selector |
| 4 | `builtin.RoleType` + selector | `selector(role)` | `*builtin.RoleModelConfig` | Non-nil when chain resolved | Both alias and AliasMain missing → nil |
| 5 | `RoleModelConfig.Provider/Model` | `agent.New(Config{...})` | Running sub-agent | Provider registered in registry | Provider not registered → spawn error |

### Hand-off boundaries

**Boundary A: profiles → bridge**
- `mgr.ResolveChain(alias)` returns `(*fallback.Chain, error)`
- Invariant: if error == nil, `chain.Primary.Provider != ""` and `chain.Primary.Model != ""`
- Failure: `err != nil` triggers fallback to `AliasMain`

**Boundary B: bridge → builtin (delegate_task.go)**
- `selector(role)` returns `*builtin.RoleModelConfig`
- Invariant: if non-nil, `cfg.Provider != ""` and `cfg.Model != ""`
- Failure: `nil` return → runtime uses default model (not an error)

**Boundary C: builtin → agent runner**
- `agent.New(Config{Provider: cfg.Provider, Model: cfg.Model})`
- Invariant: provider must be registered in the agent's registry
- Failure: `provider not registered` error at spawn time

---

## 4. Invariants

1. **nil Manager** → all helpers return nil / empty values (graceful degradation)
2. **Missing role alias** → automatic fallback to `AliasMain` (ensures sub-agents can always spawn)
3. **AliasMain also missing** → nil config (runtime uses default model)
4. **Chain always non-nil when config non-nil** — guaranteed by ResolveChain contract
5. **No panics** — all error paths return values, never panic

---

## 5. Test Coverage

| Test | Stage Covered | Invariant Verified |
|------|---------------|-------------------|
| `TestRoleTypeToModelAlias` | Step 1 | All 4 roles + unknown map correctly |
| `TestBuildRoleModelSelector` | Steps 1-4 | Selector returns non-nil config with Provider/Model |
| `TestBuildCurrentProviderGetter` | Step 2 (provider only) | Getter returns non-empty provider string |
| `TestResolveRoleChain` | Steps 1-2 | Chain has valid Primary provider/model |
| `TestSelectorWithNilManager` | Invariant #1 | nil input → nil output |
| `TestSelectorFallbackToMain` | Invariant #2 | Missing alias → falls back to AliasMain |

---

## 6. Migration Path for TUI

After this package is stable, swarm-tui/internal/chat/sdk_integration.go will be modified:

**Before (TUI-specific):**
```go
func buildProfileRoleModelSelector(mgr *settings.ProfileManager) builtin.RoleModelSelector { ... }
func buildCurrentProviderGetter(mgr *settings.ProfileManager) func() string { ... }
func roleTypeToModelAlias(role builtin.RoleType) settings.ModelAlias { ... }
```

**After (delegates to SDK):**
```go
import bridge "github.com/Swarm-Code/mono/swarm-sdk/profiles/bridge"

func buildProfileRoleModelSelector(mgr *settings.ProfileManager) builtin.RoleModelSelector {
    return bridge.BuildRoleModelSelector(mgr)
}
// ... etc
```

The local implementations in sdk_integration.go (lines 558–644) become thin wrappers or are deleted.
