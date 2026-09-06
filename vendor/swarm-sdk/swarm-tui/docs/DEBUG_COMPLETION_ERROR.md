# Debugging Your Completion Error: Root Cause Analysis

## The Evidence

From your error output, I can see:

```
Groups: 1/3 completed
Error: group Parallel Research failed: completion criteria not met
```

BUT you can clearly see in the agent outputs:
- ✓ Query Agent: `[WORK COMPLETE]` 
- ✓ RAG Context Agent: `[WORK COMPLETE]`
- ✓ Email Search Agent: (output truncated, but activity logs show it's running)

## The Real Problem I've Identified

The injection and detection code **exists and is in the binary**, but here's what I think is happening:

### Most Likely Issue: The Email Search Agent

The Email Search Agent appears to be **timing out or not completing properly**. Notice:
- Query Agent: Shows full output with `[WORK COMPLETE]`
- RAG Context Agent: Shows final line with `[WORK COMPLETE]`
- Email Search Agent: **Only shows activity logs, no final output**

This agent is probably:
1. ❌ Still running when group evaluation happens
2. ❌ Timing out before producing output
3. ❌ Returning an error we don't see
4. ❌ Not producing output with marker

## New Debugging Added

I've added **aggressive logging** to the coordinator that will show:

```
agent.completion_marker_not_detected
├─ agent_id: [which agent]
├─ agent_name: [agent name]
├─ output_length: [bytes of output]
└─ output_preview: [first 200 chars]

agent.execution.completed
├─ agent_id: [which agent]
├─ is_work_complete: [true/false]
├─ has_error: [true/false]
└─ output_length: [bytes]

group.isComplete.agent_not_complete
├─ group_name: "Parallel Research"
├─ agent_id: [which agent failed]
├─ agent_name: [name]
├─ is_work_complete: [false - this is why it failed]
└─ output_length: [bytes]
```

## What To Do Now

### Step 1: Run With Debug Logging

```bash
export LOG_LEVEL=debug
swarmos -f clover-workflow.yaml "your prompt" 2>&1 | tee workflow.log
```

### Step 2: Search the Log For

Look for:
```
agent.completion_marker_not_detected
agent.execution.error
group.isComplete.agent_not_complete
```

### Step 3: Check Your Workflow YAML

The issue is probably one of these:

**A) Email Search Agent is timing out**
```yaml
- id: email-search-agent
  model: ...
  capabilities:
    timeout_seconds: 120  # ← Maybe too short?
```

**B) Email Search Agent has no system prompt**
```yaml
- id: email-search-agent
  model: ...
  system_prompt: ""  # ← This means NO INJECTION!
```

**C) Email Search Agent's model is slow**
```yaml
- id: email-search-agent
  model: some-slow-model  # ← Takes forever
```

## The Fix I've Implemented

The code now has **multi-layered debugging**:

1. **At execution time**: Logs every agent's completion status
2. **At marker detection**: Logs when markers are found/not found
3. **At group evaluation**: Logs why each agent passed/failed
4. **With output preview**: Shows first 200 chars of output for diagnosis

## Hypothesis

My best guess based on the output you showed:

**The Parallel Research group has 3 agents:**
1. ✓ Query Agent - **COMPLETED** (shows `[WORK COMPLETE]`)
2. ✓ RAG Context Agent - **COMPLETED** (shows `[WORK COMPLETE]`)
3. ❌ Email Search Agent - **DID NOT COMPLETE** (only shows activity, no final output)

**Why Email Search Agent failed:**
- Probably timing out
- Or hitting an error in Gmail API search
- Or hitting rate limits
- Or still executing when group timeout occurs

## Next Steps

1. **Rebuild with the new logging**:
   ```bash
   cd /home/swarm/SwarmCode/TUI
   go build ./cmd/swarmos
   ```

2. **Run your workflow with debug logging**:
   ```bash
   export LOG_LEVEL=debug
   swarmos -f clover-workflow.yaml "your query" 2>&1 | grep -E "completion|Error|agent\." | tail -100
   ```

3. **Share the logs showing**:
   - Which agent failed
   - Why it failed (error/timeout/no output)
   - What output it produced (if any)

## The Real Fix Will Be

Once we identify which agent is failing:

**If timing out:**
```yaml
capabilities:
  timeout_seconds: 300  # Increase timeout
```

**If error in Gmail API:**
```yaml
system_prompt: |
  When searching Gmail, handle rate limits gracefully.
  If search fails, provide summary of available data.
  [Include completion marker anyway]
```

**If no system prompt:**
```yaml
system_prompt: |
  [Get your existing prompt]
  [Injection will be added automatically]
  [Make sure agent provides output]
```

## Technical Details

The completion signal system works as designed, but **it can't detect completion from an agent that doesn't finish executing**. 

If an agent times out or errors:
- ❌ No output produced
- ❌ No marker to detect
- ❌ IsWorkComplete stays false
- ❌ Group fails

---

**Let's get those logs and identify exactly which agent is failing!**

