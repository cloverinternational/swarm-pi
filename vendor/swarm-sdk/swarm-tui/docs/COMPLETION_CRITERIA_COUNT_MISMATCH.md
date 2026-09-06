# Why You Get "Completion Criteria Not Met" With Parallel Agents That Actually Run

## The Real Problem

Even if agents **run and produce output**, you still fail completion criteria because the code checks:

```go
len(result.Results) == len(gc.group.Agents)
```

This means: **Total results stored must equal total agents defined**

---

## What Causes Missing Results

### Reason 1: Agent Creation Fails

**In executeParallel() - lines 197-200:**

```go
agentInstance, err := gc.createAgent(ctx, def, factory)
if err != nil {
    errors <- fmt.Errorf("failed to create agent %s: %w", def.Name, err)
    return  // ← EXITS WITHOUT STORING RESULT
}
```

**If agent fails to CREATE:**
- Error is logged
- **NO result is stored** in `result.Results`
- Result count < agent count
- Completion fails

**What could cause creation to fail:**
- Invalid provider (typo in provider name)
- Invalid model name
- Provider not available/configured
- Authentication failure
- Factory unable to resolve provider

---

### Reason 2: Agent Execution Crashes/Panics

If `executeAgent()` panics (unhandled exception), the goroutine dies without storing result

---

### Reason 3: Agent Times Out (Context Cancelled)

If context is cancelled mid-execution, agent might not store result

---

## The Count Mismatch Issue

**Scenario:**
```yaml
agents:
  - id: agent1
  - id: agent2  
  - id: agent3  # ← This one fails to create
```

**Execution:**
1. agent1 → creates ✅ → executes ✅ → stores result ✅
2. agent2 → creates ✅ → executes ✅ → stores result ✅
3. agent3 → creates ❌ → returns (no result stored) ❌

**Result:**
- `len(result.Results)` = 2
- `len(gc.group.Agents)` = 3
- 2 ≠ 3 → "completion criteria not met"

Even though agents 1 and 2 succeeded! ❌

---

## How to Debug This

### Check 1: Look for "failed to create agent" errors

The code logs:
```go
gc.logger.Error(ctx, "group.parallel.agent.error",
    observability.Field{Key: "error", Value: err.Error()})
```

**Look for**: `"failed to create agent X"`

This means the agent definition is invalid.

### Check 2: Verify Agent Configuration

Each agent needs:
- `id:` - unique identifier
- `name:` - display name
- `provider:` - valid provider name
- `model:` - valid model for that provider
- `system_prompt:` - instructions
- `tools:` - list of tools

**Common mistakes:**
```yaml
agents:
  - id: agent1
    name: Agent 1
    provider: openai_typo     # ❌ Wrong provider name
    model: gpt-4
    system_prompt: "..."
    tools: [Bash, Read]
```

---

## The Real Completion Check Logic

For `type: all` (default):

```go
// Step 1: Check for errors
for _, agentResult := range result.Results {
    if agentResult.Error != nil && criteria.MaxFailures == 0 {
        return false
    }
}

// Step 2: CHECK RESULT COUNT ← THIS IS WHERE IT FAILS
if gc.group.Execution == ExecutionAdversarial {
    return len(result.Results) >= len(gc.group.Agents)
}
return len(result.Results) == len(gc.group.Agents)  // ← COUNT MUST MATCH
```

**Both conditions must pass:**
1. No agent errors (or MaxFailures allows them)
2. **Result count must equal agent count**

If ANY agent fails to create, condition 2 fails.

---

## The Fix

### Solution 1: Use `type: consensus` or `type: majority`

These don't require exact count match:

```yaml
completion:
  type: majority  # > 50% must succeed (doesn't care about missing results)
```

**How it works:**
```go
case "majority":
    successCount := 0
    for _, agentResult := range result.Results {
        if agentResult.Error == nil {
            successCount++
        }
    }
    return float64(successCount)/float64(len(gc.group.Agents)) > 0.5
```

Even if 1 agent fails to create:
- `result.Results` has 2 agents
- 2 succeeded (errors == nil)
- 2 / 3 = 66.7% > 50% ✅ PASS

---

### Solution 2: Fix the Agent Configurations

Make sure EVERY agent can be created:

✅ **Valid config:**
```yaml
agents:
  - id: agent1
    name: Agent 1
    provider: openai          # ← Valid
    model: gpt-4              # ← Valid
    system_prompt: "..."
    tools: [Bash, Read]
```

❌ **Invalid config:**
```yaml
agents:
  - id: agent1
    name: Agent 1
    provider: open_ai         # ← Typo! Should be "openai"
    model: gpt-4
    system_prompt: "..."
    tools: [Bash, Read]
```

---

### Solution 3: Use `type: first`

Only needs ONE agent to succeed:

```yaml
completion:
  type: first  # At least one agent must succeed
```

```go
case "first":
    for _, agentResult := range result.Results {
        if agentResult.Error == nil && agentResult.Output != "" {
            return true
        }
    }
    return false
```

If 1 agent creates and succeeds → PASS ✅

---

## What to Do Right Now

### Step 1: Change completion type

```yaml
# ❌ WRONG
completion:
  type: all

# ✅ RIGHT
completion:
  type: majority
```

### Step 2: Verify agent config

Check EVERY agent has:
- Valid `provider:` name
- Valid `model:` for that provider
- Non-empty `system_prompt:`
- Non-empty `tools:` list

### Step 3: Test with 1 agent first

```yaml
agents:
  - id: single_agent
    name: Single Agent
    provider: openai
    model: gpt-4
    system_prompt: "Test this works"
    tools: [Bash, Read]
```

If 1 agent works with `type: all`, add more agents.

---

## Summary

**The real issue:** Even if agents RUN, they might fail to CREATE, causing missing results.

**The completion check:** Requires `len(result.Results) == len(gc.group.Agents)`

**The fix:** Use `type: majority` or `type: first` which don't require exact count match.

---

## Code Flow Diagram

```
Parallel Execution:
├─ For each agent:
│  ├─ createAgent()     ← Can fail here (returns, no result stored)
│  ├─ executeAgent()    ← Can fail here (result stored with error)
│  └─ Store result
│
└─ After all complete:
   ├─ Check: len(result.Results) == len(gc.group.Agents)  ← THIS FAILS
   │         ↑
   │         Missing agent → result not stored
   │
   └─ If not equal → "completion criteria not met"
```

Agent creation failure = no result stored = count mismatch = completion fails

Even if the 2 agents that DID create, produced good output, got stored successfully!
