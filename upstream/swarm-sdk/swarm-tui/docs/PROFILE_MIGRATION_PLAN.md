# Migration Plan: Deprecate headless/profile Legacy Package

## Objective
Replace all usage of `swarm-tui/headless/profile` types with canonical `swarm-sdk/profiles` types across 8 consumer files, then delete the legacy package.

## Legacy vs SDK Type Mapping

| Legacy Type (`headless/profile`) | SDK Type (`swarm-sdk/profiles`) | Compatibility Notes |
|---|---|---|
| `profile.ModelAlias` | `profiles.ModelAlias` | Same underlying `string`; SDK has 8 aliases vs legacy 6 |
| `profile.AliasMain` | `profiles.AliasMain` | Identical value `"main"` |
| `profile.AliasSteering` | `profiles.AliasSteering` | Identical value `"steering"` |
| `profile.AliasBackground` | `profiles.AliasBackground` | Identical value `"background"` |
| `profile.AliasSubAgent` | `profiles.AliasSubAgent` | Identical value `"sub_agent"` |
| `profile.AliasThinking` | `profiles.AliasThinking` | Identical value `"thinking"` (deprecated) |
| `profile.AliasLongContext` | `profiles.AliasLongContext` | Identical value `"long_context"` |
| `profile.ModelPointer` | `profiles.ModelPointer` | Same struct fields (Provider, Model, SystemPrompt, Capabilities, ReasoningLevel, DisableReasoning) |
| `profile.AgentProfile` | `profiles.AgentProfile` | Legacy: `Pointers map[ModelAlias]ModelPointer`. SDK: `Roles map[ModelAlias]RoleConfig`. Different field name! |
| `profile.ProfilesConfig` | `profiles.ProfilesConfig` | Same fields (DefaultProfile, Profiles) |
| `profile.ProfileStore` | `profiles.Manager` | Legacy: `NewProfileStore(configDir)`. SDK: `NewManager(configDir)`. Different API! |
| `profile.AgentCapabilities` | `profiles.AgentCapabilities` | Same struct fields |
| `profile.ValidationError` | `profiles.ValidationError` | Same struct |
| `profile.ProfileWarning` | `profiles.ProfileWarning` | Same struct |

## Critical Differences

### 1. AgentProfile field name: `Pointers` → `Roles`
Legacy profiles use `Pointers` (single model per alias). SDK uses `Roles` (chain-based RoleConfig).

The SDK's `Manager` has auto-migration that reads legacy JSON with `"pointers"` key and converts it to `Roles` internally. However, CODE that directly accesses `.Pointers` will break.

### 2. ProfileStore → Manager API differences
| Legacy (`ProfileStore`) | SDK (`Manager`) |
|---|---|
| `NewProfileStore(configDir)` | `NewManager(configDir)` |
| `store.GetConfig()` | `mgr.GetConfig()` |
| `store.GetActiveProfile()` | `mgr.GetActiveProfile()` |
| `store.GetProfile(id)` | `mgr.GetProfile(id)` |
| `store.ListProfiles()` | `mgr.ListProfiles()` |
| `store.CreateProfile(name, desc)` | `mgr.CreateProfile(name, desc)` |
| `store.UpdateProfile(p)` | `mgr.UpdateProfile(id, p)` |
| `store.DeleteProfile(id)` | `mgr.DeleteProfile(id)` |
| `store.SetDefaultProfile(id)` | `mgr.SetDefaultProfile(id)` |
| `store.SetPointer(profileID, alias, pointer)` | `mgr.SetRoleInProfile(profileID, alias, rc)` |
| `store.SaveConfig(config)` | `mgr.Save()` (no param, uses internal config) |

### 3. ModelAlias constants
SDK adds `AliasInference`, `AliasCompaction`, `AliasVision` that legacy doesn't have. The SDK `AllAliases()` returns 8 items; legacy returns 6.

## Per-File Migration Plan

### File 1: `headless/ipc/protocol.go`
**Legacy usage:**
```go
import "github.com/Swarm-Code/mono/swarm-tui/headless/profile"
```

**Analysis:** Read lines where `profile.` is used in protocol.go to determine if protocol types reference profile types, or if it's just a stray import.

**Expected action:** Remove import if unused, or replace types if referenced.

### File 2: `headless/ipc/server_config.go`
**Legacy usage:**
```go
import "github.com/Swarm-Code/mono/swarm-tui/headless/profile"
```

**Analysis:** This file likely references `profile.ProfileStore` in server configuration structs.

**Expected action:** Replace `*profile.ProfileStore` with `*profiles.Manager`.

### File 3: `headless/ipc/server.go`
**Legacy usage:**
```go
import "github.com/Swarm-Code/mono/swarm-tui/headless/profile"
profileStore *profile.ProfileStore
saveActiveProfile func(configDir, profileID string) error
```

**Expected action:**
- Change field `profileStore` type to `*profiles.Manager`
- Update constructor to accept `*profiles.Manager`
- Update all `profileStore.*` method calls to `mgr.*` equivalents
- The `saveActiveProfile` callback may be removable if Manager handles active profile tracking

### File 4: `headless/ipc/profile_rpc_test.go`
**Legacy usage:**
```go
import "github.com/Swarm-Code/mono/swarm-tui/headless/profile"
profile.NewProfileStore(tmpDir)
profile.ProfilesConfig{...}
profile.AgentProfile{...}
profile.ModelAlias
profile.ModelPointer
profile.AliasMain
```

**Expected action:**
- Replace with `profiles.NewManager(tmpDir)`
- Use SDK type names (`profiles.ProfilesConfig`, `profiles.AgentProfile`, etc.)
- The JSON test data uses `"pointers"` key — SDK Manager auto-migrates this on load
- May need to update test expectations if field access patterns change

### File 5: `headless/sdk/bridge.go`
**Legacy usage:**
```go
import "github.com/Swarm-Code/mono/swarm-tui/headless/profile"
profileStore *profile.ProfileStore
```

**Expected action:**
- Change `profileStore` to `*profiles.Manager`
- Update `WithProfileStore` option to accept `*profiles.Manager`
- Update all method calls

### File 6: `headless/sdk/bridge_test.go`
**Legacy usage:**
```go
import "github.com/Swarm-Code/mono/swarm-tui/headless/profile"
```

**Expected action:**
- Replace with SDK types
- Update test setup code

### File 7: `headless/cmd/ipc-server/main.go`
**Legacy usage:**
```go
import "github.com/Swarm-Code/mono/swarm-tui/headless/profile"
profile.NewProfileStore(configDir)
```

**Expected action:**
- Replace with `profiles.NewManager(configDir)`
- Pass `*profiles.Manager` to server constructor

### File 8: `headless/cmd/acp-server/main.go`
**Legacy usage:**
```go
import "github.com/Swarm-Code/mono/swarm-tui/headless/profile"
```

**Expected action:**
- Similar to ipc-server — replace `ProfileStore` with `Manager`

## Execution Sequence

1. **Phase A — IPC Protocol + Server Config** (low risk)
   - `protocol.go` — remove/update import
   - `server_config.go` — replace ProfileStore with Manager

2. **Phase B — Server Core** (medium risk)
   - `server.go` — replace field types, update method calls
   - This is the most complex file; tread carefully

3. **Phase C — SDK Bridge** (medium risk)
   - `bridge.go` — replace ProfileStore with Manager
   - `bridge_test.go` — update tests

4. **Phase D — Command Entry Points** (low risk)
   - `cmd/ipc-server/main.go`
   - `cmd/acp-server/main.go`

5. **Phase E — Tests** (medium risk)
   - `profile_rpc_test.go` — major rewrite needed due to Pointers→Roles

6. **Phase F — Delete Legacy Package**
   - Delete `headless/profile/types.go`
   - Delete `headless/profile/store.go`
   - Delete `headless/profile/role_selector.go`
   - Delete `headless/profile/*_test.go`

## Rollback Strategy
- All changes are additive type replacements
- Legacy package files can be restored from git if needed
- The `agent_profiles.json` format is compatible (SDK auto-migrates)

## Pre-Migration Verification
Before starting, run:
```bash
cd swarm-tui && go test ./headless/ipc/... ./headless/sdk/...
```
Baseline: must pass before any edits.

## Post-Migration Verification
After each phase:
```bash
cd swarm-tui && go build ./headless/...
cd swarm-tui && go test ./headless/ipc/... ./headless/sdk/...
```
