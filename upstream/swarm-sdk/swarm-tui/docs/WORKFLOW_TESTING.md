# Workflow Testing Guide

## Quick Test (5 minutes)

### Prerequisites
- ClaudeCode OAuth configured (or API keys set)
- Build is clean: `./swarm` exists

### Test Steps

1. **Launch TUI**
```bash
cd /home/rincon/swarm/TUI
./swarm
```

2. **Navigate to Workflows**
- Press `w` from home screen
- You should see list of 7 workflows

3. **Launch Simple Workflow**
- Navigate to `test_simple.yaml` (use ↑/↓ or j/k)
- Press `Space` or `Enter` to launch
- Workflow will use input: "Analyze this: What is 2+2?"

4. **Observe Execution**
- Should switch to chat screen
- Should show workflow header with structure
- Watch for polling updates (every 500ms)
- Final results should appear when complete

5. **Check Debug Log**
```bash
tail -f swarmos_debug.log
```
Look for:
- `[launchWorkflowInChat]` - Launch initiated
- `[executeWorkflowInBackground]` - Execution started  
- `[listenForWorkflowUpdates]` - Update listener active
- Group/agent completions

### Expected Behavior

**Working Case:**
```
Workflow: Simple Test Workflow
Status: Running ● (0/1 groups completed)

Layer 0: Simple Analysis  
  └─ [ ] Simple Analyzer (pending)

[Updates every 500ms]

[After ~30-60 seconds]

Layer 0: Simple Analysis ✓
  └─ [✓] Simple Analyzer (completed)

Status: Completed ✓

[Final output appears]
```

**Failure Case:**
```
Error: SDK not initialized
OR
Error: Failed to load workflow
OR
[No updates, stuck on "Running..."]
```

### Debugging

If workflow doesn't execute:

1. **Check API Key**
```bash
echo $ANTHROPIC_API_KEY
# Should show: sk-ant-...
```

2. **Check Current Provider/Model**
```bash
grep -A5 "currentProvider\|currentModel" swarmos_debug.log | tail -20
```

3. **Check WorkflowManager Init**
```bash
grep "workflow.*manager\|Loaded.*workflows" swarmos_debug.log | tail -10
```

4. **Check SDK AgentFactory**
```bash
grep "agentFactory\|CreateAgent" swarmos_debug.log | tail -20
```

### Success Criteria

✅ Workflow launches (new conversation created)
✅ Shows workflow structure
✅ Polls and shows updates
✅ Completes successfully
✅ Shows final output in chat

### Known Issues

⚠️ **Polling is slow** - Updates every 500ms (not real-time)
⚠️ **No streaming** - Agent output appears all at once
⚠️ **Fixed input** - Currently hardcoded, need input modal

### Next Steps After Test

If it works:
1. Add input prompt modal
2. Add streaming (Phase 2)
3. Improve progress visualization

If it doesn't work:
1. Check logs for specific error
2. Verify API key and provider config
3. Test simple agent chat first (to verify SDK works)
4. Debug workflow manager initialization

---

## Advanced Testing

### Test Different Workflows

```bash
# Test parallel execution
# Navigate to: multi_model_debate.yaml
# This will run 3 agents in parallel

# Test sequential execution  
# Navigate to: plan_and_execute.yaml
# This will run agents one after another
```

### Monitor Execution State

```bash
# Watch workflow state updates
grep "UpdateGroup\|UpdateAgent\|UpdateWorkflow" swarmos_debug.log | tail -f

# Check polling ticker
grep "ticker\|Poll" swarmos_debug.log | tail -f
```

### Verify Results

After workflow completes:
- Check final message in chat has all agent outputs
- Verify token counts and costs are shown
- Confirm workflow status shows "Completed ✓"

---

## Troubleshooting

### Issue: "SDK not initialized"
**Fix:** Verify TUI started with proper config, check SDK initialization logs

### Issue: "Failed to load workflow"
**Fix:** Check `workflows/test_simple.yaml` exists and is valid YAML

### Issue: No updates during execution
**Fix:** Check if `listenForWorkflowUpdates` goroutine started, verify updateChan is connected

### Issue: Workflow stuck "Running..."
**Fix:** Check if polling ticker is active, verify WorkflowManager.Execute succeeded

### Issue: "Context cancelled" 
**Fix:** Workflow may have timed out (3min default for test_simple), check timeout settings

---

## Code Checkpoints

### Where Workflow Launches
`internal/chat/app.go:6455` - `launchWorkflowInChat()`

### Where Execution Starts
`internal/chat/app.go:6543` - `executeWorkflowInBackground()`

### Where Updates Are Listened
`internal/chat/app.go:6733` - `listenForWorkflowUpdates()`

### Where Polling Happens
`internal/chat/app.go:6577` - Ticker loop every 500ms

### Where State Updates
`internal/chat/app.go:6627` - `updateWorkflowChatFromExecution()`

---

Ready to test! 🚀
