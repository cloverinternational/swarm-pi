# Delegate Agent Streaming - Quick Start Guide

## What This Does

Shows you what delegate agents are doing in real-time, with full conversation streaming, while keeping the main conversation clean with only final results.

## Status

✅ **Component Created**: `delegate.go` - Ready to use
📝 **Integration Needed**: `chat.go` - 6 surgical changes required

## Quick Apply (For Experienced Devs)

```bash
cd /home/rincon/Swarm/swarmos
# See detailed steps in: docs/delegate-agent-implementation-steps.md
# Apply 6 changes to example/crush/internal/tui/components/chat/chat.go
go build -o crush ./example/crush
./crush
```

## What Changes

In `chat.go`, replace agent tool handling with delegate component:

1. **handleChildSession** - Route to delegate vs legacy
2. **Add 4 helper functions** - Find/handle delegate components  
3. **updateOrAddToolCall** - Check if agent tool → create delegate
4. **handleNewAssistantMessage** - Create delegate for agent tools
5. **convertAssistantMessage** - Load delegate conversation
6. **Add loadDelegateConversation** - Load child messages

## Expected Result

### Before (Current)
```
✓ Agent
   Task: Analyze codebase...
   
   ├─ bash(ls -la)
   │  ✓ Output: files...
   └─ view(main.go)
      ✓ Content: code...
   
   Result: Analysis complete...
```
*Shows nested tool calls only*

### After (With Delegate Streaming)
```
✓ Agent ▶ (Ctrl+A to expand)
   Task: Analyze codebase...
   Status: working...
   
   [Task Agent spinner...]
```

Press Ctrl+A:

```
✓ Agent ▼ (Ctrl+A to collapse)
   Task: Analyze codebase...
   Status: complete
   
   Delegate conversation:
   
     👤 Analyze codebase...
     
     🤖 [Thinking] Let me explore...
        
        [streaming text...]
     
     🔧 bash(ls -la)
        ✓ Output: files...
     
     🔧 view(main.go)
        ✓ Content: code...
   
   Final Result:
   Analysis complete. Found 3 components...
```

## Key Features

- ✅ **Real-time streaming** - See delegate thinking/working live
- ✅ **Ctrl+A toggle** - Expand/collapse full conversation
- ✅ **Clean context** - Only final result in parent conversation
- ✅ **Full transparency** - See all tool calls, thinking, reasoning
- ✅ **Backward compatible** - Other tools (agentic fetch) unchanged

## Context Separation

**Parent sees:**
```
Tool: "Analysis complete. Found 3 components..." ← Only this!
```

**You see (when expanded):**
```
Full delegate conversation with streaming!
├─ Thinking
├─ Tool calls
├─ Results
└─ Final answer
```

## Files

**Read for details:**
- `docs/delegate-agent-implementation-steps.md` - Exact code changes
- `docs/delegate-agent-summary.md` - Full explanation
- `docs/message-streaming-rendering.md` - How streaming works

**Component:**
- `example/crush/internal/tui/components/chat/messages/delegate.go` ✅ Created

**To modify:**
- `example/crush/internal/tui/components/chat/chat.go` 📝 6 changes needed

## Test Plan

1. Apply changes to `chat.go`
2. Build: `go build -o crush ./example/crush`
3. Run and send message that uses agent tool
4. Verify collapsed view shows status
5. Press Ctrl+A and verify full conversation visible
6. Watch streaming updates in real-time
7. Verify final result only in parent context

## Rollback

```bash
cd /home/rincon/Swarm/swarmos
mv example/crush/internal/tui/components/chat/chat.go.backup \
   example/crush/internal/tui/components/chat/chat.go
rm example/crush/internal/tui/components/chat/messages/delegate.go
go build -o crush ./example/crush
```

## Help

If stuck, check:
1. `delegate-agent-implementation-steps.md` for line-by-line changes
2. `delegate-agent-streaming.md` for architecture details
3. `message-streaming-rendering.md` for streaming mechanics

The implementation is surgical and well-documented. You got this! 🚀
