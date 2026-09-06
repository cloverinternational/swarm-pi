# Workflow Completion Signals: The Core of Workflow Coordination

## Overview

The **completion signal system** is the fundamental mechanism that allows SwarmOS workflows to determine when agents have truly completed their assigned work. This document explains how the system works, why it's critical, and how agents emit completion signals.

## The Problem We Solved

**Before this fix**, workflows would fail with "completion criteria not met" errors even when agents appeared to succeed. The root cause: there was no way for the system to distinguish between:
- An agent producing intermediate output (thinking, brainstorming, calculations)
- An agent producing a **final, completed summary** that's ready for the next stage

Without this distinction, the coordinator couldn't reliably determine if a group of agents had truly finished their work.

## The Solution: `IsWorkComplete` Flag

Every agent execution now carries an **`IsWorkComplete` boolean flag** that signals whether the agent has finished producing its final result.

### Architecture

```
Agent Execution → ExecuteResponse {
    Message: "final output",
    IsWorkComplete: true/false,  ← THE SIGNAL
    Tokens, Cost, Duration, ...
}
    ↓
Coordinator's AgentResult {
    AgentID, AgentName, Output,
    IsWorkComplete: true/false,  ← CAPTURED HERE
    Error, ...
}
    ↓
GroupCoordinator.isComplete() {
    // Checks: agentResult.Error == nil AND agentResult.IsWorkComplete == true
}
```

## When Is `IsWorkComplete` Set?

### Default Behavior (SDK Automatic)

When an agent completes execution **without error**, the SDK automatically sets `IsWorkComplete = true`:

```go
response := &ExecuteResponse{
    Message:        finalMessage,
    IsWorkComplete: true,  // ← Set by default on successful completion
    // ... other fields
}
```

This means:
- ✅ Agent finished executing → `IsWorkComplete = true`
- ❌ Agent returned an error → `IsWorkComplete = false`

### How Completion Criteria Works Now

The `isComplete()` function in the coordinator now checks **both conditions**:

```go
// For "all" completion type:
for _, agentResult := range result.Results {
    // BOTH conditions must be true for success:
    if agentResult.Error != nil {
        return false  // Agent errored
    }
    if !agentResult.IsWorkComplete {
        return false  // Agent didn't signal completion
    }
}
```

For different completion types:

| Type | Logic |
|------|-------|
| **`all`** | Every agent: no error AND `IsWorkComplete == true` |
| **`majority`** | > 50% of agents: no error AND `IsWorkComplete == true` |
| **`first`** | At least 1 agent: no error AND `IsWorkComplete == true` |
| **`consensus`** | Majority of agents complete + consensus threshold met |

## For Agent SDK Users: What to Know

### System Prompts Should Encourage Final Summaries

Agents should be instructed to provide a **clear, final summary** at the end of their work. This is already the best practice for any agent system.

Example system prompt:

```
You are a research assistant. Complete your analysis and provide a concise final summary.
At the end of your response, summarize the key findings.
```

### How System Prompts Map to Completion

- Agent finishes execution and provides summary → SDK sets `IsWorkComplete = true`
- SDK returns response with `IsWorkComplete = true` → Coordinator captures it
- Coordinator sees `IsWorkComplete = true` → Marks agent as successfully completed
- All agents marked complete → Group completion criteria met → Workflow advances

### If an Agent Needs to Signal Incomplete Work

If an agent execution completes but the agent realizes it **hasn't finished its work** (rare, but possible), it could store a signal in the response metadata. However, this is **not recommended** as it breaks the normal flow.

## How Fixed Result Handling Prevents Silent Failures

### The Parallel Execution Fix

In `executeParallel()`, agent creation failures are now stored as failed results:

```go
agentInstance, err := gc.createAgent(ctx, def, factory)
if err != nil {
    // NEW: Store failed result instead of silently returning
    failedResult := &AgentResult{
        AgentID:        def.ID,
        AgentName:      def.Name,
        Error:          err,
        IsWorkComplete: false,  // ← Explicitly mark incomplete
    }
    result.Results[def.ID] = failedResult  // ← Now in the map!
    return
}
```

**Why this matters:**
- Before: 3 agents, 1 creation failed → Results map had 2 entries → `len(Results) != len(Agents)` → "completion criteria not met"
- After: 3 agents, 1 creation failed → Results map has 3 entries → Coordinator properly evaluates all 3 → Clear error message

## Workflow Examples

### Example 1: Simple Parallel Analysis (Should Work)

**YAML:**
```yaml
name: parallel-analysis
config:
  execution: parallel
  completion:
    type: all  # All agents must complete
groups:
  - agents:
      - id: analyst1
        name: Financial Analyst
        model: claude-3-5-sonnet
      - id: analyst2
        name: Technical Analyst
        model: gpt-4o
```

**What happens:**
1. Both agents execute in parallel
2. Each produces output and completes normally
3. SDK sets `IsWorkComplete = true` for both
4. Coordinator sees both with `Error == nil` and `IsWorkComplete == true`
5. ✅ Workflow advances

### Example 2: Checking Majority Consensus (Should Work)

**YAML:**
```yaml
name: consensus-check
config:
  execution: parallel
  completion:
    type: majority  # 50%+ must complete
groups:
  - agents:
      - id: agent1
        model: claude-3-5-sonnet
      - id: agent2
        model: gpt-4o
      - id: agent3
        model: claude-3-opus
```

**What happens:**
1. All 3 agents execute
2. At least 2 agents: no error, `IsWorkComplete == true`
3. 2/3 > 0.5 (majority) ✅
4. Workflow advances

### Example 3: One Agent Fails (Should Fail Clearly)

**YAML:**
```yaml
name: strict-all
config:
  execution: parallel
  completion:
    type: all  # All must succeed
groups:
  - agents:
      - id: good-agent
        model: claude-3-5-sonnet
      - id: bad-agent
        provider: fake-provider  # Invalid provider
```

**What happens:**
1. good-agent: executes, returns `IsWorkComplete = true` ✅
2. bad-agent: creation fails, stores `AgentResult{ Error: "...", IsWorkComplete: false }` ❌
3. Coordinator evaluates: 1 complete, 1 incomplete → type is "all" → fails
4. Clear error: "completion criteria not met" + logs show agent creation error
5. User can see in logs: `agent creation failed` for bad-agent

## Debugging Completion Issues

### Checklist When Workflows Fail

1. **Check the logs** - Look for `"completion criteria not met"` error
2. **Check individual agent results** - Did each agent produce output?
3. **Verify IsWorkComplete is true** - Check if agents signaled completion
4. **Check for errors** - Are there agent creation or execution errors?
5. **Count the results** - Does `len(results) == len(agents)`?

### Log Patterns to Look For

```
// Success pattern:
agent.execute.completed: agent_id=analyst1, turns=3, tokens=1250

// Failure pattern (agent creation):
group.parallel.agent.error: error="failed to create agent..."

// Failure pattern (agent execution):
group.sequential.agent.error: error="timeout" / "api error" / "..."
```

## For Developers: Internal Implementation

### Key Changes Made

1. **`ExecuteResponse`** - Added `IsWorkComplete bool` field
2. **`AgentResult`** - Added `IsWorkComplete bool` field
3. **`executeAgent()`** - Captures `resp.IsWorkComplete` into `result.IsWorkComplete`
4. **`executeParallel()`** - Stores failed agent creations instead of returning early
5. **`isComplete()`** - Checks both `Error == nil` AND `IsWorkComplete == true`

### Testing the System

Create a simple 2-agent workflow:

```yaml
name: test-completion
config:
  execution: parallel
  completion:
    type: all
groups:
  - agents:
      - id: test1
        model: claude-3-5-sonnet
      - id: test2
        model: gpt-4o
```

Run with:
```bash
swarmos -f test-completion.yaml "Summarize the benefits of AI"
```

**Expected behavior:**
- Both agents execute and return output
- Both show `IsWorkComplete = true`
- Workflow completes successfully
- ✅ "Group status: completed"

## Migration Notes

### Backward Compatibility

- The SDK automatically sets `IsWorkComplete = true` for successful agent executions
- Existing workflows should continue to work
- No changes needed to YAML files or system prompts
- The only breaking change is the stricter completion criteria checking

### Future Enhancements

1. **Partial completion signals** - Agents could emit intermediate completion markers
2. **Completion metadata** - Agents could include data about what they completed
3. **Progressive completion** - Workflows could monitor partial completion during execution
4. **Custom completion evaluators** - Users could define custom completion logic

## Summary

The completion signal system is now the **single source of truth** for determining if agents have finished their work. The system:

- ✅ Automatically sets completion signals on successful agent execution
- ✅ Properly handles agent creation failures
- ✅ Provides clear coordinator logic for evaluating completion
- ✅ Maintains backward compatibility
- ✅ Enables reliable workflow orchestration

With this system in place, workflows now reliably detect when agents have produced their final results and can confidently advance through multi-stage execution pipelines.
