# Delegate Agent Streaming Implementation

## Overview

This document describes how to implement real-time streaming for delegate agents (sub-agents spawned by the `agent` tool), allowing users to see their conversations, tool uses, and thinking in real-time while ensuring only the final output gets added to the parent conversation context.

## Current Implementation

### How Delegate Agents Work Now

```go
// internal/agent/agent_tool.go
fantasy.NewParallelAgentTool(
    AgentToolName,
    description,
    func(ctx context.Context, params AgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
        // 1. Create a child session
        agentToolSessionID := c.sessions.CreateAgentToolSessionID(agentMessageID, call.ID)
        session, _ := c.sessions.CreateTaskSession(ctx, agentToolSessionID, sessionID, "New Agent Session")
        
        // 2. Run agent (blocks until complete)
        result, _ := agent.Run(ctx, SessionAgentCall{
            SessionID: session.ID,
            Prompt:    params.Prompt,
            // ... config ...
        })
        
        // 3. Return only final text to parent
        return fantasy.NewTextResponse(result.Response.Content.Text()), nil
    }
)
```

**Current Behavior:**
- ✅ Creates child session with special ID format: `{parentMessageID}:{toolCallID}`
- ✅ Child messages are stored in database
- ✅ Child messages publish pubsub events
- ⚠️ UI shows only nested tool calls (not full conversation)
- ❌ No real-time streaming visibility
- ✅ Only final text returned to parent context

### Current UI Handling

```go
// internal/tui/components/chat/chat.go
func (m *messageListCmp) handleChildSession(event pubsub.Event[message.Message]) tea.Cmd {
    // Parse child session ID
    childSessionID := event.Payload.SessionID
    parentMessageID, toolCallID, ok := m.app.Sessions.ParseAgentToolSessionID(childSessionID)
    
    // Find parent tool call component
    toolCall := findToolCallByID(parentMessageID, toolCallID)
    
    // Extract nested tool calls from child messages
    nestedToolCalls := extractNestedToolCalls(event.Payload)
    
    // Update parent tool call with nested calls
    toolCall.SetNestedToolCalls(nestedToolCalls)
    m.listCmp.UpdateItem(toolCall.ID(), toolCall)
}
```

**What's Shown:**
- Parent tool call component
- Nested tool calls from delegate agent
- Tool results from nested calls

**What's NOT Shown:**
- Delegate agent's thinking/reasoning
- Delegate agent's text responses
- Full conversation flow

## Proposed Streaming Implementation

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                     Parent Agent                                 │
│  - Runs in main session                                          │
│  - Makes tool call to "agent" tool                              │
└────────────────────────┬────────────────────────────────────────┘
                         │ Spawns delegate
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Delegate Agent (Task Agent)                    │
│  - Runs in child session (ID: parentMsgID:toolCallID)           │
│  - Messages stored in DB with child sessionID                    │
│  - Publishes pubsub events (CreatedEvent, UpdatedEvent)         │
│  - STREAMING: Updates happen in real-time                       │
└────────────────────────┬────────────────────────────────────────┘
                         │ Pubsub events
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                        TUI Message List                          │
│  - Receives all pubsub events                                    │
│  - Routes child session events to delegate UI component         │
│  - Shows expandable/collapsible delegate conversation           │
└─────────────────────────────────────────────────────────────────┘
```

### Key Design Decisions

#### 1. **Context Separation**

**Parent Context (Main Conversation):**
- Contains user messages
- Contains parent agent messages
- Contains tool calls (including `agent` tool)
- Contains tool results with **only final output** from delegate

**Delegate Context (Hidden from Parent):**
- Full delegate conversation (user prompt + assistant responses)
- Delegate's tool calls and results
- Delegate's thinking/reasoning
- Stored separately with child session ID

#### 2. **UI Rendering**

**Expandable Delegate Component:**
```
┌─────────────────────────────────────────────────────────────────┐
│ 🤖 agent(prompt: "Analyze the codebase...")                     │
│    ▶ Task agent working... (Click to expand)                    │
└─────────────────────────────────────────────────────────────────┘

                     ↓ (User expands)

┌─────────────────────────────────────────────────────────────────┐
│ 🤖 agent(prompt: "Analyze the codebase...")                     │
│    ▼ Task agent conversation:                                   │
│                                                                  │
│    ┌──────────────────────────────────────────────────────────┐│
│    │ 👤 Analyze the codebase...                               ││
│    └──────────────────────────────────────────────────────────┘│
│                                                                  │
│    ┌──────────────────────────────────────────────────────────┐│
│    │ 🤖 [Thinking] Let me explore the project structure...   ││
│    │                                                           ││
│    │    I'll analyze the main components by:                  ││
│    │    1. Checking the directory structure                   ││
│    │    2. Reading key files                                  ││
│    │    [streaming text here...]                              ││
│    └──────────────────────────────────────────────────────────┘│
│                                                                  │
│    ┌──────────────────────────────────────────────────────────┐│
│    │ 🔧 bash(command: "ls -la")                               ││
│    │    ✓ Output: [files listed...]                          ││
│    └──────────────────────────────────────────────────────────┘│
│                                                                  │
│    Result: Analysis complete. Found 3 key components...        │
└─────────────────────────────────────────────────────────────────┘
```

## Implementation Plan

### Phase 1: Data Model Updates

No changes needed! The current architecture already supports this:

```go
// Child session messages already have their own session ID
childMessage := message.Message{
    ID:        "msg-123",
    SessionID: "parentMsgID:toolCallID",  // ← Identifies it as child
    Role:      message.Assistant,
    Parts:     []{/* content */},
    // ... other fields ...
}
```

### Phase 2: New UI Component - DelegateAgentCmp

Create a new component to render delegate agent conversations:

```go
// internal/tui/components/chat/messages/delegate.go

type DelegateAgentCmp interface {
    list.Item
    layout.Sizeable
    
    GetToolCall() message.ToolCall
    SetToolCall(message.ToolCall)
    SetToolResult(message.ToolResult)
    
    // Delegate-specific methods
    AddDelegateMessage(message.Message)
    UpdateDelegateMessage(message.Message)
    SetExpanded(bool)
    IsExpanded() bool
    GetDelegateMessages() []message.Message
}

type delegateAgentCmp struct {
    width  int
    height int
    
    // Tool call info (from parent)
    toolCall   message.ToolCall
    toolResult *message.ToolResult
    
    // Delegate conversation
    delegateMessages []message.Message  // Full child conversation
    expanded         bool                // Expand/collapse state
    
    // Child session ID (for filtering events)
    childSessionID string
    
    // Nested message components
    messageComponents []messages.MessageCmp
}

func NewDelegateAgentCmp(parentMessageID string, toolCall message.ToolCall) DelegateAgentCmp {
    return &delegateAgentCmp{
        toolCall:       toolCall,
        childSessionID: fmt.Sprintf("%s:%s", parentMessageID, toolCall.ID),
        delegateMessages: []message.Message{},
        messageComponents: []messages.MessageCmp{},
        expanded:       false,
    }
}
```

### Phase 3: Update Message List to Handle Delegates

```go
// internal/tui/components/chat/chat.go

func (m *messageListCmp) handleChildSession(event pubsub.Event[message.Message]) tea.Cmd {
    // Parse child session ID
    childSessionID := event.Payload.SessionID
    parentMessageID, toolCallID, ok := m.app.Sessions.ParseAgentToolSessionID(childSessionID)
    if !ok {
        return nil
    }
    
    // Find the delegate agent component
    items := m.listCmp.Items()
    delegateIndex := m.findDelegateAgentByToolCall(items, parentMessageID, toolCallID)
    
    if delegateIndex == NotFound {
        // This shouldn't happen, but handle gracefully
        return nil
    }
    
    delegateAgent := items[delegateIndex].(messages.DelegateAgentCmp)
    
    switch event.Type {
    case pubsub.CreatedEvent:
        // Add new message to delegate conversation
        delegateAgent.AddDelegateMessage(event.Payload)
        
    case pubsub.UpdatedEvent:
        // Update existing message in delegate conversation
        delegateAgent.UpdateDelegateMessage(event.Payload)
    }
    
    // Update the component in the list
    m.listCmp.UpdateItem(delegateAgent.ID(), delegateAgent)
    
    return nil
}
```

### Phase 4: Rendering Delegate Conversations

```go
// internal/tui/components/chat/messages/delegate.go

func (d *delegateAgentCmp) View() string {
    t := styles.CurrentTheme()
    
    // Render tool call header
    header := d.renderToolCallHeader()
    
    if !d.expanded {
        // Collapsed view - just show status
        status := d.renderCollapsedStatus()
        return lipgloss.JoinVertical(lipgloss.Left, header, status)
    }
    
    // Expanded view - show full conversation
    parts := []string{header}
    
    // Render each delegate message
    for _, msg := range d.delegateMessages {
        msgCmp := messages.NewMessageCmp(msg)
        msgCmp.SetSize(d.width-4, 0)  // Indent delegate messages
        parts = append(parts, 
            t.S().Base.PaddingLeft(2).Render(msgCmp.View()),
        )
    }
    
    // Show final result if complete
    if d.toolResult != nil {
        parts = append(parts, d.renderResult())
    }
    
    return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (d *delegateAgentCmp) renderToolCallHeader() string {
    t := styles.CurrentTheme()
    
    icon := "🤖"
    toolName := t.S().Code.Render(d.toolCall.Name)
    
    expandIcon := "▶"
    if d.expanded {
        expandIcon = "▼"
    }
    
    status := "working..."
    if d.toolResult != nil {
        if d.toolResult.IsError {
            status = "❌ error"
        } else {
            status = "✓ complete"
        }
    }
    
    return fmt.Sprintf("%s %s %s (%s)", 
        expandIcon, icon, toolName, status)
}

func (d *delegateAgentCmp) renderCollapsedStatus() string {
    t := styles.CurrentTheme()
    
    // Show summary of what's happening
    if len(d.delegateMessages) == 0 {
        return t.S().Subtle.Render("  Starting task agent...")
    }
    
    // Find last assistant message
    for i := len(d.delegateMessages) - 1; i >= 0; i-- {
        if d.delegateMessages[i].Role == message.Assistant {
            content := d.delegateMessages[i].Content().Text
            if len(content) > 50 {
                content = content[:50] + "..."
            }
            return t.S().Subtle.Render("  " + content)
        }
    }
    
    return t.S().Subtle.Render("  Working...")
}

func (d *delegateAgentCmp) renderResult() string {
    t := styles.CurrentTheme()
    
    label := t.S().Base.Foreground(t.GreenDark).Render("Result:")
    content := d.toolResult.Content
    
    if len(content) > 200 {
        content = content[:200] + "..."
    }
    
    return fmt.Sprintf("\n%s %s", label, content)
}
```

### Phase 5: Handle Tool Call → Delegate Component Creation

```go
// internal/tui/components/chat/chat.go

func (m *messageListCmp) handleUpdateAssistantMessage(msg message.Message) tea.Cmd {
    var cmds []tea.Cmd
    items := m.listCmp.Items()
    
    // ... existing message update logic ...
    
    // Handle tool calls
    for _, tc := range msg.ToolCalls() {
        if tc.Name == agent.AgentToolName {
            // This is a delegate agent call - create DelegateAgentCmp
            cmds = append(cmds, m.createDelegateAgentComponent(msg, tc))
        } else {
            // Regular tool call - create ToolCallCmp
            cmds = append(cmds, m.createRegularToolCall(msg, tc))
        }
    }
    
    return tea.Batch(cmds...)
}

func (m *messageListCmp) createDelegateAgentComponent(msg message.Message, tc message.ToolCall) tea.Cmd {
    // Check if delegate already exists
    items := m.listCmp.Items()
    if m.findDelegateAgentByToolCall(items, msg.ID, tc.ID) != NotFound {
        return nil  // Already exists
    }
    
    // Create new delegate component
    delegateAgent := messages.NewDelegateAgentCmp(msg.ID, tc)
    return m.listCmp.AppendItem(delegateAgent)
}
```

### Phase 6: Interactive Expand/Collapse

```go
// internal/tui/components/chat/messages/delegate.go

func (d *delegateAgentCmp) Update(msg tea.Msg) (util.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyPressMsg:
        // Toggle expand/collapse on Enter or Space
        if key.Matches(msg, key.NewBinding(key.WithKeys("enter", " "))) {
            d.expanded = !d.expanded
            return d, nil
        }
        
    case tea.MouseClickMsg:
        // Also support mouse click to expand/collapse
        if msg.Button == tea.MouseLeft {
            d.expanded = !d.expanded
            return d, nil
        }
    }
    
    return d, nil
}
```

### Phase 7: Streaming Updates

The beauty is that **streaming already works** with this architecture! Here's why:

```go
// When delegate agent streams (in child session):

// 1. Agent callback fires
OnTextDelta: func(id string, text string) error {
    currentAssistant.AppendContent(text)
    return a.messages.Update(ctx, *currentAssistant)  // SessionID = childSessionID
}

// 2. Message service publishes event
s.Publish(pubsub.UpdatedEvent, message)  // Event includes childSessionID

// 3. TUI receives event
case pubsub.Event[message.Message]:
    if event.Payload.SessionID != m.session.ID {
        return m.handleChildSession(event)  // ← Routes to delegate handler
    }

// 4. Delegate component updates
delegateAgent.UpdateDelegateMessage(event.Payload)
m.listCmp.UpdateItem(delegateAgent.ID(), delegateAgent)

// 5. UI re-renders with updated content
```

**Result:** Users see delegate agent's conversation streaming in real-time!

## Context Separation: Parent vs Delegate

### What Goes into Parent Context

```go
// Only the tool result with final output

// Parent conversation:
// User: "Analyze the codebase"
// Assistant: [tool_call: agent(prompt="Analyze the codebase")]
// Tool: "Analysis complete. Found 3 key components: ..." ← Only this!
```

### What Stays in Delegate Context

```go
// Full delegate conversation (stored, but separate)

// Delegate conversation (session: parentMsgID:toolCallID):
// User: "Analyze the codebase" 
// Assistant: [thinking] "Let me explore..."
// Assistant: "I'll start by checking the structure..."
// Assistant: [tool_call: bash("ls -la")]
// Tool: [result: files...]
// Assistant: [tool_call: view("main.go")]
// Tool: [result: file contents...]
// Assistant: "Analysis complete. Found 3 key components: X, Y, Z"
```

### Implementation in Agent Tool

The current implementation already does this correctly:

```go
// internal/agent/agent_tool.go
result, err := agent.Run(ctx, SessionAgentCall{
    SessionID: session.ID,  // ← Child session, separate context
    Prompt:    params.Prompt,
})

// Only return final text to parent
return fantasy.NewTextResponse(result.Response.Content.Text()), nil
```

**Perfect!** The delegate's full conversation is stored in the child session, but only the final text response gets returned to the parent.

## Benefits of This Approach

### ✅ Full Transparency
- Users see exactly what the delegate agent is doing
- All tool calls, thinking, and reasoning visible
- Builds trust in agent behavior

### ✅ Real-Time Streaming
- Delegate agent streams just like main agent
- Users see progress in real-time
- No changes to underlying streaming architecture needed

### ✅ Clean Context Separation
- Parent context stays clean with only final results
- Delegate context fully preserved for debugging
- Token efficiency maintained

### ✅ Expandable/Collapsible UI
- Collapsed: Shows brief status, doesn't clutter
- Expanded: Shows full conversation for transparency
- User controls level of detail

### ✅ Debugging & Development
- Full delegate conversations stored in DB
- Easy to debug what delegates did
- Can replay or analyze delegate behavior

## Advanced Features (Future)

### 1. Nested Delegates
If a delegate spawns another delegate:

```
Parent Agent
  └─ Delegate 1: "Analyze code"
      └─ Delegate 2: "Search documentation"
          └─ Delegate 3: "Find examples"
```

The same pattern works recursively!

### 2. Parallel Delegates

```go
// Multiple delegates running concurrently
fantasy.NewParallelAgentTool(...)  // ← Already parallel!

// UI shows all delegates expanding independently
┌─ 🤖 agent(task1) - working...
├─ 🤖 agent(task2) - working...
└─ 🤖 agent(task3) - complete ✓
```

### 3. Delegate Summary View

Show aggregate stats when collapsed:
- Tool calls made: 5
- Tokens used: 2.5K
- Duration: 12s
- Status: Complete ✓

### 4. Delegate Abort/Cancel

Allow users to cancel individual delegates:
```
[Cancel] button on delegate component → cancels child session
```

## Migration Path

### Step 1: Implement DelegateAgentCmp
- Create new component
- Test rendering static delegate conversations

### Step 2: Update Message List Routing  
- Route child session events to delegate component
- Test with real streaming

### Step 3: Add Expand/Collapse
- Implement toggle interaction
- Add keyboard/mouse handlers

### Step 4: Polish UI
- Add icons, status indicators
- Improve collapsed summary
- Add loading states

### Step 5: Test Edge Cases
- Multiple concurrent delegates
- Deeply nested delegates
- Large delegate conversations
- Delegate errors/cancellations

## File Changes Required

### New Files
- `internal/tui/components/chat/messages/delegate.go` - DelegateAgentCmp

### Modified Files
- `internal/tui/components/chat/chat.go` - Route to delegate component
- `internal/tui/components/chat/messages/tool.go` - May need minor adjustments

### No Changes Needed
- ✅ `internal/agent/agent_tool.go` - Already works correctly
- ✅ `internal/agent/agent.go` - Streaming already works
- ✅ `internal/message/message.go` - Pubsub already works
- ✅ Database schema - Child sessions already stored

## Summary

The proposed implementation:

1. **Leverages existing streaming** - No changes to agent layer needed
2. **Uses current pubsub events** - Just routes them differently in UI
3. **Separates contexts cleanly** - Child sessions independent from parent
4. **Provides full transparency** - Users see everything delegates do
5. **Maintains token efficiency** - Only final output in parent context
6. **Scales naturally** - Handles nested and parallel delegates

The key insight: **The infrastructure already exists!** We just need to build the UI component to visualize delegate conversations that are already being streamed and stored.
