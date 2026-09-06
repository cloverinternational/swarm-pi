# Workflow Completion Criteria - How It Actually Works

This document explains how completion criteria work in the workflow system, based on the actual code.

---

## The Core Logic

The system checks if a group meets completion criteria in the `isComplete()` function in `coordinator.go` (line 415).

**The group fails if `isComplete()` returns `false`** → "completion criteria not met" error

---

## Completion Types

### 1. `"all"` (Default)

**Requirement**: All agents must complete successfully

```yaml
completion:
  type: all
  threshold: 1.0
  max_failures: 0
```

**Logic** (from code line 419-431):
```go
case "all":
  // All agents must complete (errors allowed if partial failure permitted)
  for _, agentResult := range result.Results {
    if agentResult.Error != nil && criteria.MaxFailures == 0 {
      return false
    }
  }
  // Check result count matches agent count
  return len(result.Results) == len(gc.group.Agents)
```

**What this means:**
- Every agent must produce a result
- If `MaxFailures == 0` (default), ANY agent error fails the group
- Result count MUST equal agent count

**Example that WORKS:**
```yaml
groups:
  - id: parallel_analysis
    execution: parallel
    completion:
      type: all
    agents:
      - id: agent1
        name: Agent 1
        # ... config
      - id: agent2
        name: Agent 2
        # ... config
```
✅ Works if BOTH agents succeed

**Example that FAILS:**
```yaml
groups:
  - id: parallel_analysis
    execution: parallel
    completion:
      type: all        # ALL must succeed
    agents:
      - id: agent1
        name: Agent 1
        # ...
      - id: agent2
        name: Agent 2
        # ...
```
❌ Fails if agent1 errors OR agent2 errors

---

### 2. `"consensus"`

**Requirement**: Agents must reach agreement (threshold %)

```yaml
completion:
  type: consensus
  threshold: 0.7          # 70% agreement
```

**Logic** (from code line 433-436):
```go
case "consensus":
  // Check if consensus threshold is met
  consensus := gc.analyzeConsensus(context.Background(), result)
  return consensus.Reached && consensus.Confidence >= criteria.Threshold
```

**How consensus is calculated** (from code line 469-503):
```go
// Count successful agents
successCount := 0
for _, agentResult := range result.Results {
  if agentResult.Error == nil && agentResult.Output != "" {
    successCount++
  }
}
// Calculate confidence: successful / total agents
consensus.Confidence = float64(successCount) / float64(totalAgents)
// Consensus reached if >= 50% (hardcoded minimum)
consensus.Reached = consensus.Confidence >= 0.5
```

**What this means:**
- Agents that error DON'T count as success
- Agents with empty output DON'T count as success
- Confidence = (agents that succeeded) / (total agents)
- Your threshold must be met AND consensus.Reached must be true (>= 50%)

**Example:**
```yaml
groups:
  - id: review
    execution: parallel
    completion:
      type: consensus
      threshold: 0.7     # Need 70% agreement
    agents:
      - id: expert1
      - id: expert2
      - id: expert3
```
- If 2/3 succeed: 66.7% < 70% → FAILS
- If 3/3 succeed: 100% >= 70% → WORKS

---

### 3. `"first"`

**Requirement**: At least one agent succeeds

```yaml
completion:
  type: first
```

**Logic** (from code line 438-445):
```go
case "first":
  // At least one agent succeeded
  for _, agentResult := range result.Results {
    if agentResult.Error == nil && agentResult.Output != "" {
      return true
    }
  }
  return false
```

**What this means:**
- Only ONE agent needs to succeed
- Agent MUST have no error AND non-empty output
- Rest can fail without issue

**Example:**
```yaml
completion:
  type: first
agents:
  - id: agent1   # Could fail
  - id: agent2   # Could fail
  - id: agent3   # Only needs ONE to work
```

---

### 4. `"majority"`

**Requirement**: More than 50% of agents succeed

```yaml
completion:
  type: majority
```

**Logic** (from code line 447-455):
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

**What this means:**
- Need MORE than 50% success (not equal)
- 2/3 = 66.7% ✅ (> 50%)
- 1/2 = 50% ❌ (not > 50%)
- 2/4 = 50% ❌ (not > 50%)
- 3/4 = 75% ✅ (> 50%)

**Example:**
```yaml
completion:
  type: majority
agents:
  - id: agent1
  - id: agent2
  - id: agent3
```
- Need at least 2 to succeed (2/3 = 66.7% > 50%)
- 1 success is NOT enough

---

## Common Mistakes That Cause "Completion Criteria Not Met"

### Mistake 1: Agent Errors

```yaml
agents:
  - id: worker
    system_prompt: "You are..." 
    model: invalid-model    # ← This will error!
```

If agent has error → `agentResult.Error != nil` → fails for "all" and "majority"

**Fix**: Use valid model names

---

### Mistake 2: Empty Output

```yaml
system_prompt: |
  You should output nothing when:
  - The sun is shining
  - The moon is out
  # ← Might produce empty output in some cases
```

For `"consensus"` and `"first"`: Empty output = failure

**Fix**: Ensure system prompt always produces output

---

### Mistake 3: Too Many Agents for "all"

```yaml
completion:
  type: all           # ALL must succeed
agents:
  - id: agent1
  - id: agent2
  - id: agent3
  - id: agent4        # If even ONE fails...
  - id: agent5        # ...entire group fails
```

**Fix**: Use `consensus`, `majority`, or `first` for multiple agents

---

### Mistake 4: Wrong Threshold for Consensus

```yaml
completion:
  type: consensus
  threshold: 1.0      # ← Requires 100% perfect agreement
agents:
  - id: agent1
  - id: agent2
  - id: agent3
```

3/3 agents = 100% → Only works if ALL succeed perfectly

**Fix**: Use lower threshold like 0.7 (70%)

---

### Mistake 5: Max Failures Misunderstanding

```yaml
completion:
  type: all
  max_failures: 1     # Allows 1 failure
agents:
  - id: agent1
  - id: agent2
```

If agent1 errors and you have `max_failures: 0` → FAILS
If agent1 errors and you have `max_failures: 1` → Still checks result count

**Current code** (line 422-424):
```go
if agentResult.Error != nil && criteria.MaxFailures == 0 {
  return false
}
```

This only checks `MaxFailures == 0`. With `MaxFailures: 1`, it still requires `len(result.Results) == len(agents)`

---

## Field Meanings

| Field | Type | Default | Meaning |
|---|---|---|---|
| `type` | string | "all" | Completion type: all, consensus, first, majority |
| `threshold` | float | 1.0 | For consensus: agreement % (0.0-1.0) |
| `min_agents` | int | 0 | Minimum agents required (currently unused) |
| `max_failures` | int | 0 | Max allowed failures (only affects "all" type) |

---

## Why You're Getting Errors

### Scenario 1: Parallel with "all"
```yaml
execution: parallel
completion:
  type: all    # ALL must succeed
agents:
  - agent1
  - agent2
```

If agent1 succeeds and agent2 errors → FAILS

**Solution**: Use `type: consensus` or `type: majority`

---

### Scenario 2: Sequential with One Agent Failing
```yaml
execution: sequential
completion:
  type: all
agents:
  - agent1  # Succeeds
  - agent2  # Errors
```

Result: agent1 succeeded, agent2 had error → Not all succeeded → FAILS

**Solution**: 
- Fix agent2's configuration
- Or use `type: first`
- Or use `max_failures: 1`

---

### Scenario 3: Consensus Not Met
```yaml
completion:
  type: consensus
  threshold: 0.8    # Need 80%
agents:
  - agent1
  - agent2
  - agent3
  - agent4
  - agent5
```

If only 3/5 succeed (60%) → 60% < 80% → FAILS

**Solution**: Lower threshold to 0.6 or 0.5

---

## Best Practice Configurations

### For Single Agent
```yaml
completion:
  type: all
```
Simple - one agent, must succeed

---

### For Multiple Independent Experts
```yaml
completion:
  type: consensus
  threshold: 0.7    # 70% agreement
```
Flexible - allows some agents to fail

---

### For Sequential Pipeline
```yaml
completion:
  type: all
  max_failures: 0   # Each step must work
```
Strict - each step in pipeline must succeed

---

### For "At Least One Works"
```yaml
completion:
  type: first
```
Lenient - only needs one success

---

### For Debate/Adversarial
```yaml
completion:
  type: consensus
  threshold: 0.8    # Strong agreement
```
Requires high consensus since agents are debating

---

## Testing Your Completion Logic

Create a simple workflow to test:

```yaml
id: test_completion
name: Test Completion
version: 1.0.0

config:
  max_duration: "5m"

groups:
  - id: test_group
    execution: parallel
    timeout: "3m"
    completion:
      type: consensus
      threshold: 0.5
    agents:
      - id: agent1
        name: Agent 1
        provider: openai
        model: gpt-4
        system_prompt: "Say: SUCCESS"
        tools: [Bash, Read]
        capabilities:
          max_tokens: 100
          temperature: 0.5
```

This SHOULD work because:
- 1 agent produces output
- consensus requires >= 50% success (1/1 = 100%)
- 100% >= 50% ✅

---

## Key Takeaway

**Agents with errors or empty output = failure**

The system counts:
- Agents with `error == nil` AND `output != ""` as **success**
- Agents with `error != nil` OR `output == ""` as **failure**

Then checks if completion criteria is met based on success count.

---

## Summary Table

| Type | Requirement | Best For |
|---|---|---|
| `all` | 100% agents succeed | Sequential pipelines, single agent |
| `consensus` | ≥ threshold % succeed | Multiple experts with voting |
| `first` | ≥ 1 agent succeeds | "At least one source" tasks |
| `majority` | > 50% agents succeed | Quorum-based decisions |

---

Start with `consensus` + `threshold: 0.7` for parallel agents. It's the most forgiving.
