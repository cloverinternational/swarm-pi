# Agent Profile Pool System — Comprehensive Plan

> **Status:** Planned — ready to implement  
> **Branch:** version-0.7  
> **Created:** 2026-02-17  
> **Context:** Written before compaction to preserve full design intent

---

## 1. Problem Statement

### What Sentry Told Us

Pulling live production errors (token: `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`) revealed the following:

| Error | Count | Root Cause |
|-------|-------|------------|
| `agent.invalid_state` — agent stuck in `executing` | **17 hits** | Race condition: TUI fires `Execute()` on same agent instance from two goroutines concurrently. Queue drain + new message dispatch both fire. `streamingMessage` flag is stale. |
| `all providers exhausted: rate_limited` | **11 hits** | `retried: false` on every single event. The failover system exists globally but is **never invoked for sub-agents**. `DelegateTaskTool.Execute()` swallows errors as tool results. |
| `HTTP 402 Payment Required` (Cerebras, 16 hits) | **16 hits** | User configured `zai-glm-4.7`, ran out of credits. No way to disable without full reconfigure. TUI kept hammering for 2+ hours. |
| `adaptive thinking not supported on model` | **4 hits** | Profile points to model that doesn't support thinking. No validation at save time. |
| `model not found: claude-opus-4.6` | **2 hits** | Typo in model ID in profile config. No validation. |
| `tool_result blocks only in user messages` (Gemini) | **1 hit** | `repairOrphanedToolCalls` not Gemini-aware; sub-agents don't inherit repair logic. |

### What Users Experience

- Configure an agent → get rate limited → **no way to disable it** → have to delete and recreate
- Agents and Profiles are **two disconnected systems** in the sidebar
- Compaction is a **mystery separate section** nobody finds
- Changing a model requires navigating 3 levels deep
- No visibility into which model is actually running for which role
- Sub-agents silently fail when rate limited — no retry, no fallback, just error

---

## 2. Current Architecture

### Settings Sidebar (Current — Fragmented)

```
AI Config
  ◆ Model & Provider       ← API keys + current model
  ◆ System Prompts
  ◆ Agents                 ← custom agent definitions (disconnected)
  ◆ Agent Profiles         ← role→model mapping (disconnected)
  ◆ Compaction             ← compaction model (mystery section)

Performance
  ◆ Reliability            ← global fallback chain + retry
```

### Data Model (Current)

```go
// One model per role — no pool, no fallback
type ModelPointer struct {
    Provider     string             `json:"provider"`
    Model        string             `json:"model"`
    SystemPrompt string             `json:"system_prompt,omitempty"`
    Capabilities *AgentCapabilities `json:"capabilities,omitempty"`
    // NO: Enabled, NO: Fallbacks
}

type AgentProfile struct {
    ID        string
    Name      string
    Pointers  map[ModelAlias]ModelPointer  // single model per role
    IsDefault bool
    // NO: Enabled, NO: per-role pool
}
```

### Execution Flow (Current — Broken)

```
Main agent execution:
  sdk_integration_execution.go → sdk.agent.Execute()
  → On 429: "all providers exhausted" (retried: false)
  → Error surfaced to user with no recovery

Sub-agent execution (Task tool):
  delegate_task.go → getProviderConfig() → RoleModelSelector
  → RoleModelSelector resolves ONE model from profile
  → On 429: error swallowed as tool result string, no retry
```

### Existing Primitives We Reuse

The `sdk/fallback` package already has everything we need:

```go
// sdk/fallback/chain.go — already exists, battle-tested
type Chain struct {
    Primary   ModelRef   `json:"primary"`
    Fallbacks []ModelRef `json:"fallbacks,omitempty"`
}

// sdk/fallback/executor.go — already exists
func Execute(ctx context.Context, chain *Chain, fn ExecutionFunc, opts ...ExecuteOptions) Result
// Tries primary → fallbacks in order, records all attempts, returns Result
```

**We don't build new retry logic. We wire the existing `fallback.Chain` into every role in every profile.**

---

## 3. New Architecture

### Settings Sidebar (New — Unified)

```
AI Config
  ◆ Providers              ← was "Model & Provider" — API keys, provider setup only
  ◆ Agents                 ← THE unified hub: profiles + models + retry (see below)
  ◆ System Prompts

Tools & Integrations
  ◆ Tools and MCP
  ◆ Hooks
  ◆ Skills
  ◆ Plugins
  ◆ Context Sources

Performance
  ◆ Reliability            ← global fallback chain + global rate limits (kept)

Advanced
  ◆ Config Bundles
  ◆ Advanced
```

**Removed from sidebar:** `SectionAgentProfiles`, `SectionCompaction` — both absorbed into `SectionAgents`.

---

### New Data Model

```go
// RoleConfig replaces ModelPointer — now wraps a full fallback pool
type RoleConfig struct {
    // Chain is the ordered pool of models for this role.
    // Primary = first tried. Fallbacks = tried in order on error.
    // Example: Main role: opus → gemini-3-pro → codex
    Chain   *fallback.Chain `json:"chain"`

    // Enabled lets you disable a role without deleting its config.
    // When false, the role falls through to the global fallback chain.
    // This is the fix for the 402/rate-limit "no escape" problem.
    Enabled bool `json:"enabled"`

    // SystemPrompt overrides the default system prompt for this role.
    SystemPrompt string `json:"system_prompt,omitempty"`

    // Capabilities overrides defaults for this role.
    Capabilities *AgentCapabilities `json:"capabilities,omitempty"`
}

// AgentProfile — updated to use RoleConfig pools
type AgentProfile struct {
    ID          string                    `json:"id"`
    Name        string                    `json:"name"`
    Description string                    `json:"description,omitempty"`
    Icon        string                    `json:"icon,omitempty"`
    Color       string                    `json:"color,omitempty"`
    IsDefault   bool                      `json:"is_default"`

    // Roles maps each agent role to its model pool + enabled state.
    // Replaces the old Pointers map[ModelAlias]ModelPointer.
    Roles       map[ModelAlias]RoleConfig `json:"roles"`

    // RetryPolicy is per-profile retry configuration.
    // Overrides global reliability settings for agents using this profile.
    RetryPolicy *ProfileRetryPolicy       `json:"retry_policy,omitempty"`

    CreatedAt   time.Time                 `json:"created_at"`
    UpdatedAt   time.Time                 `json:"updated_at"`
}

// ProfileRetryPolicy controls when to rotate to the next model in a pool.
type ProfileRetryPolicy struct {
    // RotateOn defines which error types trigger pool rotation.
    RotateOnRateLimit  bool `json:"rotate_on_rate_limit"`   // 429
    RotateOnPayment    bool `json:"rotate_on_payment"`      // 402
    RotateOnAuthError  bool `json:"rotate_on_auth_error"`   // 401
    RotateOnAnyError   bool `json:"rotate_on_any_error"`    // catch-all

    // CooldownSeconds before retrying a failed model in the pool.
    // After cooldown, the model becomes available again.
    CooldownSeconds int `json:"cooldown_seconds"`
}
```

### Role Aliases (Updated)

```go
const (
    AliasMain        ModelAlias = "main"        // Primary conversation agent
    AliasSubAgent    ModelAlias = "sub_agent"    // Task tool sub-agents (implementer)
    AliasInference   ModelAlias = "inference"    // Fast inference (NEW — replaces AliasThinking for speed)
    AliasBackground  ModelAlias = "background"   // BackgroundTask tool agents
    AliasSteering    ModelAlias = "steering"     // Supervisor/reviewer agents
    AliasLongContext ModelAlias = "long_context" // Long context operations
    AliasCompaction  ModelAlias = "compaction"   // Context compaction (moved from separate section)
)
```

**`AliasThinking` removed** — replaced by `AliasInference` which is clearer. Thinking is a model capability, not a separate role.

---

## 4. UX Design — Three-State Agents Screen

### State 1: Profile Overview (Landing)

The first thing you see when you open `Agents` in settings.

```
┌─ Agents ──────────────────────────────────────────────────────────┐
│                                                                     │
│  ⚖️  Balanced                                            [ACTIVE]  │
│  Optimal balance of speed and quality for everyday coding          │
│                                                                     │
│  ▶ [Configure Profile]         [Switch Profile]                    │
│                                                                     │
│  ─── Role Pools ────────────────────────────────────────────────   │
│                                                                     │
│  Main          claude-opus-4 → gemini-2.5-pro → codex  ✓ 3 models │
│  Sub Agent     claude-haiku-4 → llama-3.3-70b           ✓ 2 models │
│  Fast          llama-3.3-70b (Cerebras)                 ✓ 1 model  │
│  Background    claude-sonnet-4                          ✓ 1 model  │
│  Steering      claude-opus-4 → gemini-2.5-pro           ✓ 2 models │
│  Compaction    claude-haiku-4 → claude-3-haiku          ✓ 2 models │
│                                                                     │
│  ─── Retry Policy ──────────────────────────────────────────────   │
│  Auto-rotate:  Rate limit ✓  Payment ✓  Auth error ✓               │
│  Cooldown:     30s                                                  │
│                                                                     │
│  ↑/↓ navigate  Enter configure role  c configure profile           │
│  s switch profile  e enable/disable role  Esc back                 │
└─────────────────────────────────────────────────────────────────────┘
```

**Key interactions:**
- `c` → go to State 2 (Configure Profile form)
- `s` → open State 3 (Switch Profile overlay)
- `e` on a role row → instantly toggle enabled/disabled **without opening a form**
- `Enter` on a role row → go directly to that role's pool editor in State 2
- Roles show their full pool summary inline — no hunting required

---

### State 2: Configure Profile

```
┌─ Configure Profile: Balanced ──────────────────────────────────────┐
│                                                                      │
│  Name:  [Balanced                                               ]    │
│                                                                      │
│  ─── Model Pools ─────────────────────────────────────────────────  │
│                                                                      │
│  Main                                                                │
│    #1 ★ ClaudeCode  / claude-opus-4          [✓ enabled]            │
│    #2   Gemini      / gemini-2.5-pro         [✓ enabled]            │
│    #3   OpenAI      / codex                  [✓ enabled]            │
│    [+ Add model to Main pool]                                        │
│                                                                      │
│  Sub Agent                                                           │
│  ▶  #1 ★ ClaudeCode  / claude-haiku-4        [✓ enabled]            │
│       #2   Cerebras    / llama-3.3-70b        [✓ enabled]            │
│       [+ Add model to Sub Agent pool]                                │
│                                                                      │
│  Fast                                                                │
│    #1 ★ Cerebras    / llama-3.3-70b          [✓ enabled]            │
│    [+ Add model to Fast pool]                                        │
│                                                                      │
│  Background                                                          │
│    #1 ★ ClaudeCode  / claude-sonnet-4        [✓ enabled]            │
│    [+ Add model to Background pool]                                  │
│                                                                      │
│  Steering                                                            │
│    #1 ★ ClaudeCode  / claude-opus-4          [✓ enabled]            │
│    [+ Add model to Steering pool]                                    │
│                                                                      │
│  Compaction                                                          │
│    #1 ★ ClaudeCode  / claude-haiku-4         [✓ enabled]            │
│    #2   ClaudeCode  / claude-3-haiku          [✓ enabled]            │
│    [+ Add model to Compaction pool]                                  │
│                                                                      │
│  ─── Retry Policy ─────────────────────────────────────────────── │
│                                                                      │
│  Auto-rotate on:  [✓] Rate limit (429)   [✓] Payment (402)          │
│                   [✓] Auth error (401)   [ ] Any error              │
│  Cooldown before retry:  [30]s                                       │
│                                                                      │
│  ▶ [Save Profile]    [Save as New Profile]    [Cancel]               │
│                                                                      │
│  ↑/↓ navigate • ←/→ cycle provider/model • a add • d delete         │
│  ↑/↓ reorder pool • e enable/disable entry • Enter edit • Esc back  │
└──────────────────────────────────────────────────────────────────────┘
```

**Key interactions:**
- `↑/↓` moves between role sections and pool entries within them
- `←/→` on a pool entry cycles through available providers/models for that slot
- `a` appends a new model to the currently selected role's pool
- `d` removes the selected pool entry (can't remove last one)
- `↑/↓` within a pool section reorders priority (drag-free)
- `★` marker auto-moves to entry #1 (primary is always position 1)
- `e` on a pool entry toggles that specific model enabled/disabled
- Retry Policy inline — no separate Reliability section needed

---

### State 3: Switch Profile (Overlay)

```
┌─ Switch Profile ──────────────────────────┐
│                                            │
│  ▶ ⚖️  Balanced              [ACTIVE]     │
│     ⭐ Quality                             │
│     🚀 Performance                        │
│     💰 Cost-Optimized                     │
│     ─────────────────────────             │
│     + New Profile                         │
│     + Clone Current                       │
│                                            │
│  Enter switch • n new • k clone • Esc cancel│
└────────────────────────────────────────────┘
```

---

## 5. Runtime Auto-Rotation Flow

### Main Agent Execution

```
User sends message
  ↓
sdk_integration_execution.go → ExecuteMessage()
  ↓
Resolve Main role pool from active profile:
  Pool: [claude-opus-4 (ClaudeCode), gemini-2.5-pro (Gemini), codex (OpenAI)]
  ↓
fallback.Execute(ctx, pool, fn) where fn = sdk.agent.Execute()
  ↓
  Try #1: claude-opus-4 → 429 rate limited
  Try #2: gemini-2.5-pro → success ✓
  ↓
TUI banner: "⚡ Rate limited on ClaudeCode, switched to Gemini / gemini-2.5-pro"
  ↓
After 30s cooldown: ClaudeCode available again
TUI banner: "✓ ClaudeCode / claude-opus-4 restored"
```

### Sub-Agent Execution (Task Tool)

```
Main agent calls Task tool
  ↓
delegate_task.go → getProviderConfig(role=implementer)
  ↓
buildProfileRoleModelSelector() → resolves Sub Agent pool:
  Pool: [claude-haiku-4 (ClaudeCode), llama-3.3-70b (Cerebras)]
  ↓
fallback.Execute(ctx, subAgentPool, fn) where fn = factory.CreateSubAgent()
  ↓
  Try #1: claude-haiku-4 → 402 Payment Required
  Try #2: llama-3.3-70b → success ✓
  ↓
Sub-agent result returned to main agent as tool result
  ↓
TUI shows in sub-agent panel: "⚡ Used fallback: Cerebras / llama-3.3-70b"
```

### Disable Flow (The 402 Escape Hatch)

```
User is out of ClaudeCode credits
  ↓
Settings → Agents → profile overview
  ↓
Press e on "Main" role row → [DISABLED]
  ↓
Main pool now uses first ENABLED entry:
  Pool: [claude-opus-4 ✗ disabled, gemini-2.5-pro ✓, codex ✓]
  First enabled = gemini-2.5-pro → used immediately
  ↓
OR: User disables entire profile → Switch Profile → pick another
```

---

## 6. Files to Change

### Settings Package (`internal/chat/settings/`)

| File | Change |
|------|--------|
| `agent_profiles_types.go` | Replace `ModelPointer` + `map[ModelAlias]ModelPointer` with `RoleConfig` + `map[ModelAlias]RoleConfig`. Add `ProfileRetryPolicy`. Add `AliasInference`, `AliasCompaction`. Remove `AliasThinking`. |
| `agent_profiles.go` | Update `GenerateBuiltinProfiles()` with chains per role. Update `ResolveAlias()` to return first enabled entry in chain. Update `ProfileManager` CRUD. |
| `agent_profiles_ui.go` | Full rewrite: 3-state UI (Overview → Configure → Switch). Pool editor per role. Inline retry policy. |
| `agents.go` | Remove `CustomAgentConfig` complexity (agents just reference profiles). Add `Enabled` field to `CustomAgentEntry`. |
| `agents_render.go` | Add enabled/disabled badge on custom agents. |
| `types.go` | Remove `SectionAgentProfiles`, `SectionCompaction` from `Sections`. Rename `SectionModel` → `SectionProviders`. Add pool editor state fields to `State`. |
| `manager.go` | Remove `compaction`, `profiles` fields. Wire `agents` to handle all three states. |
| `manager_handlers.go` | Route key events for new unified Agents section. |
| `reliability.go` / `reliability_render.go` | Remove per-profile retry UI (now inline in profile). Keep global rate limits section only. |

### SDK Integration (`internal/chat/`)

| File | Change |
|------|--------|
| `sdk_integration.go` | `buildProfileRoleModelSelector()` → returns entire `RoleConfig.Chain`, not single `ModelPointer`. Update `NewSDKIntegrationWithOptions` to pass chain resolver. |
| `sdk_integration_execution.go` | `ExecuteMessage()` → wrap `sdk.agent.Execute()` in `fallback.Execute()` using Main role's chain. Handle 429/402/401 as rotation triggers. Emit `providerEventMsg` on rotation. |
| `sdk_integration_profile.go` | `CreateAgentForRole()` → use `RoleConfig.Chain` + `fallback.Execute()`. `ResolveAlias()` → return first enabled model in chain. |
| `reliability_provider_stack.go` | Update to understand per-profile pools vs global fallback. |

### SDK (`sdk/`)

| File | Change |
|------|--------|
| `sdk/tools/builtin/delegate_task.go` | `getProviderConfig()` → accept `*fallback.Chain` instead of single `RoleModelConfig`. Use `fallback.Execute()` to try chain entries. |
| `sdk/fallback/chain.go` | No change needed — already works. |
| `sdk/fallback/executor.go` | No change needed — already works. |

---

## 7. State Fields Added to `types.go`

```go
// In State struct — new fields for Agents section pool editor
AgentsView              string // "overview", "configure", "switch"

// Pool editor state
AgentsPoolRole          string // Which role's pool is being edited ("main", "sub_agent", etc.)
AgentsPoolSelected      int    // Selected index within the role's pool (0 = primary, 1+ = fallback)
AgentsPoolAddMode       bool   // Whether in add-model mode
AgentsPoolEditProvider  string // Draft provider for new pool entry
AgentsPoolEditModel     string // Draft model for new pool entry

// Switch profile overlay
AgentsSwitchSelected    int    // Selected profile index in switch overlay
```

---

## 8. Built-in Profile Defaults (Updated)

### Balanced Profile

```
Main:        [ClaudeCode/claude-opus-4, Gemini/gemini-2.5-pro]
Sub Agent:   [ClaudeCode/claude-haiku-4, Cerebras/llama-3.3-70b]
Fast:        [Cerebras/llama-3.3-70b, Groq/llama-3.1-70b]
Background:  [ClaudeCode/claude-sonnet-4]
Steering:    [ClaudeCode/claude-opus-4]
Compaction:  [ClaudeCode/claude-haiku-4, ClaudeCode/claude-3-haiku]
RetryPolicy: RotateOnRateLimit=true, RotateOnPayment=true, Cooldown=30s
```

### Quality Profile

```
Main:        [ClaudeCode/claude-opus-4, Gemini/gemini-3-opus]
Sub Agent:   [ClaudeCode/claude-sonnet-4]
Fast:        [ClaudeCode/claude-haiku-4]
Background:  [ClaudeCode/claude-opus-4]
Steering:    [ClaudeCode/claude-opus-4, Gemini/gemini-3-opus]
Compaction:  [ClaudeCode/claude-haiku-4]
RetryPolicy: RotateOnRateLimit=true, RotateOnPayment=true, Cooldown=60s
```

### Performance Profile

```
Main:        [Cerebras/llama-3.3-70b, Groq/llama-3.1-70b]
Sub Agent:   [Cerebras/llama-3.3-70b]
Fast:        [Cerebras/llama-3.3-70b]
Background:  [Groq/llama-3.1-70b]
Steering:    [ClaudeCode/claude-haiku-4]
Compaction:  [Cerebras/llama-3.3-70b]
RetryPolicy: RotateOnRateLimit=true, RotateOnPayment=true, Cooldown=10s
```

### Cost-Optimized Profile

```
Main:        [ClaudeCode/claude-haiku-4, Cerebras/llama-3.3-70b]
Sub Agent:   [Cerebras/llama-3.3-70b]
Fast:        [Cerebras/llama-3.3-70b]
Background:  [ClaudeCode/claude-haiku-4]
Steering:    [ClaudeCode/claude-haiku-4]
Compaction:  [ClaudeCode/claude-haiku-4]
RetryPolicy: RotateOnRateLimit=true, RotateOnPayment=true, Cooldown=30s
```

---

## 9. Sentry Error Fixes Addressed by This Plan

| Error | Fix |
|-------|-----|
| `agent.invalid_state` (17x) | Not in this plan — separate fix: add mutex guard in `handleSendMessage` to prevent double-dispatch |
| `all providers exhausted: rate_limited` (11x) | ✅ Per-role pool + `fallback.Execute()` in sub-agent path |
| `HTTP 402 Payment Required` (16x) | ✅ `Enabled` toggle on pool entries + `RotateOnPayment` policy |
| `adaptive thinking not supported` (4x) | ✅ Model validation at profile save (check model caps against provider catalog) |
| `model not found: claude-opus-4.6` (2x) | ✅ Model ID validation at profile save — validate against known model IDs |
| `tool_result in user messages` (1x) | Not in this plan — separate fix in conversation repair logic |

---

## 10. Key Decisions & Rationale

### Why `fallback.Chain` per role instead of a separate pool type?

`fallback.Chain` already exists, already has `Execute()`, already has validation, already serializes cleanly to JSON. We reuse it verbatim. No new primitives needed.

### Why merge Compaction into Agents?

Compaction is just another agent role. Users shouldn't need to know what "compaction" is or find it in a separate settings section. It's `Compaction` in the pool list, same as `Main` or `Sub Agent`.

### Why remove `SectionAgentProfiles` from the sidebar?

The profile IS the agents config. Having both "Agents" and "Agent Profiles" as separate sections confuses users. One section, one screen, three states.

### Why keep global Reliability?

Global rate limits and global retry settings still make sense for users who want coarse-grained control without managing per-profile policies. Per-profile retry policy is an OVERRIDE on top of global defaults.

### Why `AliasInference` instead of `AliasThinking`?

"Thinking" describes a model feature (extended thinking / chain-of-thought). "Inference" describes the use case: fast inference for quick tasks. Users understand "fast agent" better than "thinking agent."

---

## 11. Implementation Order

1. **Data model** — `agent_profiles_types.go`: Add `RoleConfig`, `ProfileRetryPolicy`, update `AgentProfile.Roles`
2. **Built-in profiles** — `agent_profiles.go`: Update `GenerateBuiltinProfiles()` with chains
3. **Profile Manager** — `agent_profiles.go`: Update `ResolveAlias()`, add `ResolveChain()`
4. **Settings sidebar** — `types.go`: Merge sections, add state fields
5. **Agents UI** — `agent_profiles_ui.go`: 3-state screen (Overview → Configure → Switch)
6. **SDK integration** — `sdk_integration_profile.go`: Wire `fallback.Execute()` for role resolution
7. **Main execution** — `sdk_integration_execution.go`: Wrap `Execute()` with pool fallback
8. **Sub-agent execution** — `sdk/tools/builtin/delegate_task.go`: Pass chain to `getProviderConfig()`
9. **Manager wiring** — `manager.go` + `manager_handlers.go`: Route unified Agents section
