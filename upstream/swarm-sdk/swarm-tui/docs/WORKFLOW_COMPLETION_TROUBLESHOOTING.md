# Troubleshooting Workflow Completion Criteria Failures

## Quick Diagnosis

If you see the error: **"completion criteria not met"**, follow this checklist:

### Step 1: Check Agent Count in Results

```
Log pattern: group.completed: group=my-workflow, status=failed, 
             agent_count=2
```

Compare `agent_count` (completed results) vs `agents_defined` (in YAML):
- ✅ They match → Proceed to Step 2
- ❌ They don't match → Agent creation/initialization failed (see "Agent Creation Failures" below)

### Step 2: Check Agent Output

Look at each agent's result:
```
analyst1: status=completed, output_length=245 bytes
analyst2: status=completed, output_length=189 bytes
```

- ✅ All agents have output → Proceed to Step 3
- ❌ Some agents missing output → Check agent execution errors

### Step 3: Check Individual Agent Completion Signals

The new system looks for **two conditions** on each agent:
1. `Error == nil` (agent didn't crash)
2. `IsWorkComplete == true` (agent finished and returned summary)

View the detailed result:
```
AgentResult {
  AgentID: "analyst1",
  Error: nil,
  IsWorkComplete: true,   ← THIS MUST BE TRUE
  Output: "My analysis...",
}
```

- ✅ Both true → Agent properly completed
- ❌ IsWorkComplete = false → Even if output exists, system sees it as incomplete

---

## Common Issues & Fixes

### Issue 1: "Agent Creation Failed"

**Symptom:**
```
group.parallel.agent.error: error="failed to create agent..."
group.completed: status=failed, agent_count=2 (expected 3)
```

**Causes:**
- Invalid provider name (typo in YAML)
- Invalid model name
- Missing API keys/credentials for that provider
- Agent definition validation failed

**Fix:**
1. **Check provider name** - Must match installed providers:
   - `anthropic`
   - `openai`
   - `gemini`
   - `@current` (uses currently active provider)

2. **Check model name** - Must be valid for the provider:
   - Claude models: `claude-3-opus`, `claude-3-5-sonnet`, `claude-3-haiku`
   - GPT models: `gpt-4o`, `gpt-4-turbo`, `gpt-4`
   - Gemini models: `gemini-2.0-flash`, `gemini-1.5-pro`

3. **Check credentials**:
   ```bash
   # Verify API key is set
   echo $ANTHROPIC_API_KEY   # For Claude
   echo $OPENAI_API_KEY      # For OpenAI
   echo $GOOGLE_API_KEY      # For Gemini
   ```

**Example Fix:**
```yaml
# BEFORE (broken):
agents:
  - id: analyst1
    provider: claude        # ❌ Wrong name
    model: claude-opus      # ❌ Wrong name

# AFTER (correct):
agents:
  - id: analyst1
    provider: anthropic     # ✅ Correct
    model: claude-3-opus    # ✅ Correct
```

---

### Issue 2: "Agent Execution Timed Out"

**Symptom:**
```
group.sequential.agent.error: error="context deadline exceeded"
```

**Causes:**
- Agent took too long to respond (network delay, complex task)
- API provider is slow
- Model is under heavy load

**Fix:**
1. **Increase timeout** in agent definition:
   ```yaml
   agents:
     - id: analyst1
       model: gpt-4o
       capabilities:
         timeout: 60  # seconds (increase from default)
   ```

2. **Use simpler prompt** - Reduce complexity:
   ```yaml
   input: |
     Quickly summarize: What is 2+2?
     (Much simpler than before)
   ```

3. **Try different model** - Faster models:
   ```yaml
   # Instead of:
   model: gpt-4
   # Try:
   model: gpt-4o  # Usually faster
   ```

---

### Issue 3: "Agent Returned Error"

**Symptom:**
```
AgentResult {
  Error: "API rate limit exceeded",
  IsWorkComplete: false,
}
```

**Causes:**
- API rate limits
- Invalid API key
- Network error
- Provider service down
- Malformed request

**Fix:**
1. **Check API key validity**:
   ```bash
   # Test OpenAI API key
   curl -H "Authorization: Bearer $OPENAI_API_KEY" \
        https://api.openai.com/v1/models
   ```

2. **Wait and retry** - Rate limit is temporary
3. **Use different provider** - Switch to an available one
4. **Check provider status** - Visit provider's status page:
   - Anthropic: https://status.anthropic.com
   - OpenAI: https://status.openai.com
   - Google: https://status.cloud.google.com

---

### Issue 4: "IsWorkComplete is False but No Error"

**Symptom:**
```
AgentResult {
  AgentID: "analyst1",
  Error: nil,
  IsWorkComplete: false,  ← Unexpected!
  Output: "Here's my analysis...",
}
```

**This should NOT happen** (bug indicator):
- The SDK should set `IsWorkComplete = true` when agent execution completes without error
- If you see this, it indicates a problem in the agent SDK

**Workaround for now:**
1. **Report this as a bug** - Include the full agent output
2. **Use different completion type** in the meantime:
   ```yaml
   # Change from:
   completion:
     type: all
   
   # To:
   completion:
     type: majority  # More lenient
   ```

---

## Debugging by Completion Type

### Completion Type: `all`

**What it requires:**
- Every agent must complete WITHOUT error
- Every agent must set `IsWorkComplete = true`
- Number of results must equal number of agents

**Fix priority:**
1. Check agent count (step 1 above)
2. Check for agent creation errors
3. Check for agent execution errors
4. Check IsWorkComplete flags

**If one agent is problematic:**
- Remove/disable that agent
- Or change to `majority` type if you can tolerate 1 failure

---

### Completion Type: `majority`

**What it requires:**
- More than 50% of agents must complete successfully
- Success = `Error == nil` AND `IsWorkComplete == true`
- For 2 agents: both needed (2/2 > 0.5)
- For 3 agents: 2+ needed (2/3 > 0.5)
- For 4 agents: 3+ needed (3/4 > 0.5)

**Debug example with 3 agents:**
```
Agent 1: ✅ Complete
Agent 2: ❌ Timeout
Agent 3: ✅ Complete

Result: 2/3 = 0.67 > 0.5 ✅ PASSES
```

---

### Completion Type: `first`

**What it requires:**
- At least ONE agent must complete successfully
- Success = `Error == nil` AND `IsWorkComplete == true`

**Fastest to complete** - Use for "give me any good answer"

---

### Completion Type: `consensus`

**What it requires:**
- Majority of agents succeed
- System analyzes output similarity (consensus)
- Threshold must be met (usually 0.5 = 50% confidence)

**Most reliable** - Use for "I need agreement from agents"

---

## Logging Deep Dive

### Enable Verbose Logging

To see detailed completion information:

```bash
# Set log level
export LOG_LEVEL=debug
swarmos -f my-workflow.yaml "my prompt"
```

### Look for These Log Patterns

**Successful completion:**
```
agent.execute.completed: agent_id=analyst1, turns=2, tokens=1204
group.completed: group=my-workflow, status=completed, agent_count=3
```

**Failed agent creation:**
```
group.parallel.agent.error: error="failed to create agent bad-agent: invalid provider 'fake'"
result.Results[bad-agent] = AgentResult { Error: "...", IsWorkComplete: false }
```

**Failed agent execution:**
```
group.sequential.agent.error: agent=analyst1, error="context deadline exceeded"
result.Results[analyst1] = AgentResult { Error: "timeout", IsWorkComplete: false }
```

**Failed completion criteria check:**
```
group.execute.parallel: executed without error
isComplete(type="all") returned false
  └─ Checking: len(results)==len(agents) → 2==3 ❌
  └─ OR some agent has IsWorkComplete=false
group.completed: status=failed, error="completion criteria not met"
```

---

## Testing Your Workflow

### Simple Test Workflow

Create `test.yaml`:
```yaml
name: simple-test
config:
  execution: parallel
  completion:
    type: first  # Easiest to pass
groups:
  - agents:
      - id: test
        provider: '@current'
        model: '@current'
```

Run with:
```bash
swarmos -f test.yaml "Hello, write a one-sentence summary"
```

**Expected:**
- ✅ "Group status: completed"
- ✅ Agent output shows clearly

### Progressive Complexity

1. **Test with 1 agent** → Verify basic execution
2. **Add 2nd agent** → Test parallel execution
3. **Add 3rd agent with `majority`** → Test completion logic
4. **Use `all`** → Test strict completion

---

## Quick Reference

| Error | Likely Cause | Check |
|-------|-------------|-------|
| `completion criteria not met` | Agent count mismatch | Step 1 above |
| `failed to create agent` | Invalid provider/model | Agent Creation Failures section |
| `context deadline exceeded` | Timeout | Issue 2 above |
| `API rate limit` | Too many requests | Issue 3 above |
| `invalid API key` | Credentials wrong | Issue 3 above |

---

## When All Else Fails

1. **Reduce to single agent**:
   ```yaml
   groups:
     - agents:
         - id: single
           provider: anthropic
           model: claude-3-haiku  # Fast, cheap
   ```

2. **Use simplest completion type**:
   ```yaml
   completion:
     type: first  # Will complete ASAP
   ```

3. **Add verbose output in system prompt**:
   ```yaml
   input: |
     [System: Be concise and clear. End with a summary.]
     Task: ...
   ```

4. **Report issue** if still failing:
   - Include full error message
   - Include workflow YAML
   - Include log output
   - Include what provider/model you're using

---

## Advanced: Manual Debugging

### Inspect AgentResult Structure

When debugging, understand what the system checks:

```go
// For "all" type:
if agentResult.Error != nil {
    return false  // Agent failed
}
if !agentResult.IsWorkComplete {
    return false  // Agent didn't signal completion
}

// For "majority" type:
successCount := 0
for each agent:
    if agentResult.Error == nil AND agentResult.IsWorkComplete {
        successCount++
    }
return float64(successCount)/float64(totalAgents) > 0.5
```

**To debug:** Add logging to see what each agent's result looks like.

### Test with Mock Workflow

Create a YAML that should definitely work:

```yaml
name: guaranteed-pass
config:
  execution: parallel
  completion:
    type: first  # Only needs 1 to pass
groups:
  - agents:
      - id: agent1
        provider: anthropic
        model: claude-3-haiku  # Fast
      - id: agent2
        provider: anthropic
        model: claude-3-opus   # Slow (but 1st will pass first)
```

If this passes → Your system is OK, issue is with specific workflow
If this fails → System-level issue, check credentials/API keys

---

## Next Steps

- ✅ Workflow passed? → Move to production
- ❌ Still failing? → Review specific issue section above
- ❓ Unclear? → Check logs with `LOG_LEVEL=debug`
- 🐛 Suspected bug? → Report with full details

