# Workflow System Fix Plan

## Status: IN PROGRESS
**Started:** 2024-02-05
**Goal:** Make workflows fully functional with real-time streaming in TUI

---

## Phase 1: Assessment & Testing ✅ COMPLETE
- [x] Audit current workflow implementation
- [x] Identify critical gaps
- [x] Test workflow execution headlessly
- [x] Document what works vs what's broken

**Results:**
- ✅ SDK workflow engine WORKS PERFECTLY
- ✅ Workflow CLI can validate and dry-run workflows
- ✅ Workflow YAML loading works correctly
- ❌ TUI integration uses polling (500ms) instead of events
- ❌ No real-time streaming to user
- ❌ WorkflowChatState.updateChan is NEVER populated

## Phase 2: Core Fixes (IN PROGRESS - REVISED APPROACH)

**PRIORITY CHANGE:** Get workflows working FIRST, optimize streaming LATER

### 2.1 Immediate Fixes (DONE ✅)
- [x] Verify SDK workflow engine works (TESTED - WORKS PERFECTLY)
- [x] Verify TUI integration exists (EXISTS - NEEDS TESTING)
- [x] Build TUI successfully (BUILDS CLEAN)
- [x] Document current state (SEE WORKFLOW_STATUS.md)

### 2.2 Testing & Bug Fixes (READY FOR TESTING ✅)
- [x] Add basic input (hardcoded for testing)
- [x] Build TUI successfully
- [x] Create testing guide (see WORKFLOW_TESTING.md)
- [ ] **TEST WORKFLOW EXECUTION** ← NEXT STEP
- [ ] Verify execution completes
- [ ] Check results display in chat
- [ ] Fix any runtime errors discovered
- [ ] Add proper input prompt modal
- [ ] Test cancel functionality

**Status:** Build is clean, ready to test with real API key.
**Next Action:** Launch TUI, navigate to workflows, test test_simple.yaml

### 2.3 Basic UX Improvements
- [ ] Add pre-flight cost estimate
- [ ] Add cancel keybinding
- [ ] Improve progress display
- [ ] Add error messages

### 2.2 Streaming (DEFERRED TO PHASE 3)
- [ ] Add event callback to SDK WorkflowEngine
- [ ] Connect SDK events to TUI updates
- [ ] Implement real-time agent output streaming
- [ ] Add proper error propagation

**Rationale:** User wants workflows to "work" - that means execute and show results.
Streaming is nice-to-have. Get it working first, optimize later.

## Phase 3: Missing Features
- [ ] Input modal for workflow launch
- [ ] DryRun cost estimate before execution
- [ ] Gate editor UI implementation
- [ ] Execution history viewer
- [ ] Workflow graph visualization

## Phase 4: Testing & Validation
- [ ] Test simple 1-group workflow
- [ ] Test multi-group parallel workflow
- [ ] Test sequential workflow with context passing
- [ ] Test adversarial workflow
- [ ] Test workflow with gates
- [ ] Test workflow cancellation
- [ ] Test error scenarios

## Phase 5: Polish
- [ ] Add loading indicators
- [ ] Improve error messages
- [ ] Add keyboard shortcuts help
- [ ] Document workflow usage
- [ ] Add example workflows

---

## Technical Implementation Details

### Critical Files to Modify:
1. `internal/chat/workflow_execution.go` - Add event streaming
2. `internal/chat/workflow_manager.go` - Connect to SDK events
3. `internal/chat/app.go` - Update workflow launch flow
4. `internal/chat/workflow_chat_renderer.go` - Add real-time rendering
5. `sdk/mode/workflow.go` - Ensure events are emitted

### Event Flow Architecture:
```
SDK WorkflowEngine
  ├─> GroupCoordinator
  │    ├─> Agent.Execute()
  │    │    └─> StreamingCallback ──┐
  │    └─> GroupComplete ──────────┐│
  └─> WorkflowComplete ────────────┘│
                                     │
                                     ▼
                              Event Channel
                                     │
                                     ▼
                         WorkflowChatState
                                     │
                                     ▼
                              TUI Update
                                     │
                                     ▼
                           Chat Message Append
```

### Key Changes:
1. WorkflowEngine needs to expose event channel
2. WorkflowManager.Execute() streams events to updateChan
3. App.listenForWorkflowUpdates() processes events in real-time
4. Chat messages update live as agents stream

---

## Testing Strategy

### Headless Test Suite:
```bash
# Test 1: Simple workflow (1 agent)
./swarmos-ipc workflow execute simple_test.yaml

# Test 2: Parallel workflow (3 agents)
./swarmos-ipc workflow execute parallel_test.yaml

# Test 3: Sequential workflow (context passing)
./swarmos-ipc workflow execute sequential_test.yaml

# Test 4: Workflow with gates
./swarmos-ipc workflow execute gated_test.yaml
```

### TUI Test Cases:
1. Launch workflow from selector
2. View real-time progress
3. See streaming agent outputs
4. Cancel mid-execution
5. Handle errors gracefully

---

## Success Criteria

### Must Have:
- ✅ Workflows execute without errors
- ✅ Real-time progress visible in chat
- ✅ Agent outputs stream live
- ✅ Cancel works immediately
- ✅ Errors show helpful messages
- ✅ Cost/tokens tracked accurately

### Nice to Have:
- ⭐ Beautiful progress visualization
- ⭐ Graph view of DAG
- ⭐ Execution history
- ⭐ Workflow templates

---

## Current Issues (From Audit)

### CRITICAL:
1. ❌ Polling instead of streaming (500ms ticker)
2. ❌ updateChan never populated
3. ❌ No real-time agent output
4. ❌ No progress visualization
5. ❌ No cancel keybinding

### MAJOR:
6. ❌ No input prompt for workflows
7. ❌ Gate editor not implemented
8. ❌ No cost estimate before launch
9. ❌ No execution history

### MINOR:
10. ⚠️ Magic numbers everywhere
11. ⚠️ Inconsistent error handling
12. ⚠️ Poor state management

---

## Next Steps (Immediate)

1. **Test current workflows headlessly** - Verify SDK works
2. **Create simple test workflow** - 1 agent, minimal config
3. **Fix event streaming** - Connect SDK to TUI
4. **Add progress rendering** - Show live updates
5. **Test in TUI** - Verify fixes work

---

## Notes

- SDK workflow engine is EXCELLENT - don't touch it
- Focus on TUI integration layer
- Reuse existing chat message rendering
- Keep it simple, make it work first
- Polish later

**Current Priority:** Phase 2.1 - Fix event streaming bridge
