# Workflow System - READY TO TEST ✅

## Commands (Corrected)

**Binary name:** `swarm` (not swarmos!)
**Location:** `/usr/local/bin/swarm` (installed) or `./swarm` (local build)
**Config:** `~/.config/swarmos/`

## Quick Test (NOW)

```bash
cd /home/rincon/swarm/TUI

# Option 1: Interactive TUI
./swarm
# Press 'w' for workflows
# Navigate to test_simple.yaml
# Press 'e' to EDIT (NEW!) or Space to launch

# Option 2: Headless test
./swarm -p "run the simple test workflow"
```

## Recent Fixes ✅

1. **Provider Fixed** - test_simple.yaml now uses "@current" → ClaudeCode
2. **Edit Key Fixed** - Press 'e' directly from list to edit (no need for Enter first)

## Current Status

✅ Build successful: `./swarm` binary created
✅ Headless mode works (tested with -p flag)
✅ 7 workflows loaded in `./workflows/`
✅ API keys configured (using ClaudeCode OAuth)
✅ Current provider/model set
✅ Update infrastructure in place

## What Workflows Do

1. **test_simple.yaml** - 1 agent, answers "What is 2+2?"
   - Perfect for testing
   - Should complete in ~30 seconds
   - Uses: claude-3-5-sonnet-20241022

2. **plan_and_execute.yaml** - Planning → Execution workflow
3. **multi_model_debate.yaml** - 3 models debate
4. **panel_of_experts.yaml** - Multiple expert analysis
5. **adversarial_review.yaml** - Create → Critique workflow
6. **research_synthesis.yaml** - Research multiple topics
7. **gated_code_review.yaml** - Review with quality gates

## Expected Behavior (Interactive Mode)

```
1. Launch: ./swarm
2. Press 'w' → Workflows screen appears
3. Shows 7 workflows with descriptions
4. Navigate with j/k or ↑/↓
5. Press Space on test_simple.yaml
6. → Creates new conversation
7. → Shows workflow header
8. → Polls every 500ms for updates
9. → After ~30s: Shows result "The answer is 4"
10. → Status: Completed ✓
```

## Debug Monitoring

```bash
# Watch real-time logs
tail -f swarmos_debug.log | grep -i "workflow\|group\|agent"

# Check for errors
grep -i "error\|failed" swarmos_debug.log | tail -20

# Verify workflow launched
grep "launchWorkflowInChat\|executeWorkflowInBackground" swarmos_debug.log | tail -10
```

## Known Issues

⚠️ **Polling delay** - Updates every 500ms (not real-time)
⚠️ **Hardcoded input** - Uses "Analyze this: What is 2+2?"
⚠️ **No cancel** - Can't stop mid-execution from TUI

These are acceptable for testing. Streaming optimization comes later.

## If It Doesn't Work

### Issue: "SDK not initialized"
Check: `grep "SDK.*init\|agentFactory" swarmos_debug.log | tail -10`

### Issue: "Failed to load workflow"
Check: `ls -la workflows/test_simple.yaml`

### Issue: Stuck on "Running..."
Check: `grep "ticker\|Poll\|Execute" swarmos_debug.log | tail -20`

### Issue: No API key
Check: Your ClaudeCode OAuth should be active
Try: `swarm -p "hello"` to verify basic chat works first

## Test Checklist

Before launching workflow:
- [ ] Build successful (`./swarm` exists)
- [ ] Workflows directory has YAML files
- [ ] Basic chat works (`swarm -p "test"`)

During workflow execution:
- [ ] Workflow selector shows 7 workflows
- [ ] Can select test_simple.yaml
- [ ] Creates new conversation
- [ ] Shows workflow header in chat
- [ ] Polling updates visible
- [ ] Completes without crashes

After completion:
- [ ] Final result appears in chat
- [ ] Status shows "Completed ✓"
- [ ] No error messages in log

## Next Steps After Test

If it works:
1. ✅ Celebrate - workflows are functional!
2. Add proper input modal (15 min)
3. Add cancel keybinding (15 min)
4. Add streaming (2-3 hours)

If it fails:
1. Check specific error in logs
2. Verify basic agent chat works
3. Test workflow CLI tool separately
4. Debug specific failure point

---

**Ready to test NOW!** 🚀

Run: `cd /home/rincon/swarm/TUI && ./swarm`

Then: Press 'w', select test_simple.yaml, press Space

Watch the magic happen! ✨
