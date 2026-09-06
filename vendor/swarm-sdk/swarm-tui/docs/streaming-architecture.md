# Agent Message Streaming Architecture in Crush TUI

## Overview

The Crush TUI uses a **real-time streaming architecture** where agent messages are rendered immediately as they arrive, rather than being batched. This is accomplished through a pubsub event system that updates the UI in real-time.

## Architecture Components

### 1. **Message Service Layer** (`internal/message/message.go`)

The message service acts as the central hub for message state:

```go
type Service interface {
    pubsub.Subscriber[Message]
    Create(ctx context.Context, sessionID string, params CreateMessageParams) (Message, error)
    Update(ctx context.Context, message Message) error
    // ...
}
```

**Key behaviors:**
- When messages are created or updated, the service **publishes events immediately** via the pubsub broker:
  - `s.Publish(pubsub.CreatedEvent, message)` on creation
  - `s.Publish(pubsub.UpdatedEvent, message)` on updates
- These events are broadcast to all subscribers (including the TUI)

### 2. **Agent Streaming Layer** (`internal/agent/agent.go`)

The agent uses Fantasy's `agent.Stream()` method with callback handlers that update messages in real-time:

```go
result, err := agent.Stream(genCtx, fantasy.AgentStreamCall{
    // ... configuration ...
    
    OnReasoningDelta: func(id string, text string) error {
        currentAssistant.AppendReasoningContent(text)
        return a.messages.Update(genCtx, *currentAssistant)  // ← Updates DB + publishes event
    },
    
    OnTextDelta: func(id string, text string) error {
        currentAssistant.AppendContent(text)
        return a.messages.Update(genCtx, *currentAssistant)  // ← Updates DB + publishes event
    },
    
    OnToolCall: func(tc fantasy.ToolCallContent) error {
        toolCall := message.ToolCall{...}
        currentAssistant.AddToolCall(toolCall)
        return a.messages.Update(genCtx, *currentAssistant)  // ← Updates DB + publishes event
    },
    // ... more callbacks ...
})
```

**Flow:**
1. LLM streams tokens → Fantasy SDK captures them
2. Callback handlers fire (e.g., `OnTextDelta`)
3. Message is updated in memory
4. `messages.Update()` saves to DB **AND publishes a `pubsub.UpdatedEvent`**
5. Event is broadcast to all subscribers

### 3. **TUI Message List Component** (`internal/tui/components/chat/chat.go`)

The message list subscribes to message events and updates the UI immediately:

```go
func (m *messageListCmp) Update(msg tea.Msg) (util.Model, tea.Cmd) {
    switch msg := msg.(type) {
    
    case pubsub.Event[message.Message]:
        cmds = append(cmds, m.handleMessageEvent(msg))
        return m, tea.Batch(cmds...)
    
    // ... other cases ...
    }
}
```

**Event Handling:**

```go
func (m *messageListCmp) handleMessageEvent(event pubsub.Event[message.Message]) tea.Cmd {
    switch event.Type {
    case pubsub.CreatedEvent:
        return m.handleNewMessage(event.Payload)
        
    case pubsub.UpdatedEvent:
        switch event.Payload.Role {
        case message.Assistant:
            return m.handleUpdateAssistantMessage(event.Payload)  // ← Real-time updates
        case message.Tool:
            return m.handleToolMessage(event.Payload)
        }
    }
}
```

**Update Flow:**

```go
func (m *messageListCmp) handleUpdateAssistantMessage(msg message.Message) tea.Cmd {
    items := m.listCmp.Items()
    
    // Find existing message component
    assistantIndex, existingToolCalls := m.findAssistantMessageAndToolCalls(items, msg.ID)
    
    if assistantIndex != NotFound {
        uiMsg := items[assistantIndex].(messages.MessageCmp)
        uiMsg.SetMessage(msg)  // ← Updates the component with new content
        m.listCmp.UpdateItem(items[assistantIndex].ID(), uiMsg)  // ← Triggers re-render
    }
    
    // Handle tool calls similarly...
}
```

### 4. **Message Component Rendering** (`internal/tui/components/chat/messages/messages.go`)

Individual message components render their content based on the current message state:

```go
func (m *messageCmp) View() string {
    if m.spinning && m.message.ReasoningContent().Thinking == "" {
        return m.style().PaddingLeft(1).Render(m.anim.View())  // Spinner while waiting
    }
    
    switch m.message.Role {
    case message.User:
        return m.renderUserMessage()
    default:
        return m.renderAssistantMessage()  // ← Renders current content
    }
}

func (m *messageCmp) renderAssistantMessage() string {
    content := strings.TrimSpace(m.message.Content().String())
    thinkingContent := strings.TrimSpace(m.message.ReasoningContent().Thinking)
    
    // Shows thinking content if present
    if thinkingContent != "" {
        parts = append(parts, thinkingContent)
    }
    
    // Shows text content as it streams in
    if content != "" {
        parts = append(parts, m.toMarkdown(content))
    }
    
    return m.style().Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}
```

## Data Flow Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                         LLM Provider                             │
│                    (Anthropic/OpenAI/etc)                        │
└─────────────────────────┬───────────────────────────────────────┘
                          │ Streaming tokens
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Fantasy SDK Stream                           │
│                  (agent.Stream callbacks)                        │
└─────────────────────────┬───────────────────────────────────────┘
                          │ OnTextDelta(text)
                          │ OnReasoningDelta(text)
                          │ OnToolCall(tc)
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Session Agent                                 │
│            (internal/agent/agent.go)                             │
│                                                                   │
│  currentAssistant.AppendContent(text)                           │
│  messages.Update(ctx, *currentAssistant) ────────┐              │
└──────────────────────────────────────────────────┼──────────────┘
                                                    │
                                                    ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Message Service                               │
│            (internal/message/message.go)                         │
│                                                                   │
│  1. Save to database (SQLite)                                   │
│  2. Publish event: s.Publish(UpdatedEvent, message) ────┐       │
└──────────────────────────────────────────────────────────┼───────┘
                                                            │
                                                            ▼
                                    ┌─────────────────────────────────┐
                                    │      Pubsub Broker              │
                                    │  Broadcasts to subscribers      │
                                    └──────────────┬──────────────────┘
                                                   │
                                                   ▼
┌─────────────────────────────────────────────────────────────────┐
│                  TUI Message List Component                      │
│      (internal/tui/components/chat/chat.go)                      │
│                                                                   │
│  Update(pubsub.Event[message.Message]) {                        │
│      handleUpdateAssistantMessage(msg)                          │
│          ↓                                                       │
│      uiMsg.SetMessage(msg)      // Update component state       │
│      listCmp.UpdateItem(uiMsg)  // Trigger re-render            │
│  }                                                               │
└─────────────────────────┬───────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Individual Message Component                   │
│      (internal/tui/components/chat/messages/messages.go)         │
│                                                                   │
│  View() string {                                                │
│      return renderAssistantMessage()  // Shows latest content   │
│  }                                                               │
└─────────────────────────┬───────────────────────────────────────┘
                          │
                          ▼
                   ┌──────────────┐
                   │  Terminal    │
                   │  (rendered)  │
                   └──────────────┘
```

## Key Differences from Batch Rendering

### Before (Batch Mode - Not Used)
```
Agent generates full response → Store complete message → Update UI once
```

### Now (Streaming Mode - Current)
```
Token arrives → Update message → Publish event → Update UI component → Render
     ↑_______________________________________________________________|
                   (repeats for every token/delta)
```

## Performance Optimizations

1. **Backward Search**: Lists are searched backwards since new messages/updates are likely at the end
2. **Viewport Management**: The list component uses a virtualized viewport that only renders visible items
3. **Markdown Caching**: The markdown renderer caches parsed content (see `styles.GetMarkdownRenderer()`)
4. **Mouse Event Throttling**: Mouse events are throttled to 15ms to prevent overload:

```go
func MouseEventFilter(m tea.Model, msg tea.Msg) tea.Msg {
    switch msg.(type) {
    case tea.MouseWheelMsg, tea.MouseMotionMsg:
        now := time.Now()
        if now.Sub(lastMouseEvent) < 15*time.Millisecond {
            return nil
        }
        lastMouseEvent = now
    }
    return msg
}
```

## Message State Transitions

### Assistant Message Lifecycle
1. **Created**: Empty assistant message created in `PrepareStep`
   - Event: `pubsub.CreatedEvent`
   - UI: Shows spinner animation

2. **Streaming Content**: Text/reasoning arrives via deltas
   - Event: `pubsub.UpdatedEvent` (many times)
   - UI: Content updates character by character

3. **Tool Calls**: LLM requests tools
   - Event: `pubsub.UpdatedEvent` with tool calls
   - UI: Tool call components appear

4. **Finished**: Message marked complete
   - Event: `pubsub.UpdatedEvent` with finish reason
   - UI: Shows footer with model name, timing

### Thinking/Reasoning Flow
```go
OnReasoningStart  → Message shows "Thinking" spinner
     ↓
OnReasoningDelta  → Thinking content streams in (updated continuously)
     ↓
OnReasoningEnd    → Signature/metadata added, thinking section finalized
```

## Critical Code Paths

### Immediate Update Trigger
**File**: `internal/agent/agent.go:304`
```go
OnTextDelta: func(id string, text string) error {
    currentAssistant.AppendContent(text)
    return a.messages.Update(genCtx, *currentAssistant)  // ← KEY: Updates immediately
}
```

### Event Broadcast
**File**: `internal/message/message.go:127`
```go
func (s *service) Update(ctx context.Context, message Message) error {
    // ... update database ...
    message.UpdatedAt = time.Now().Unix()
    s.Publish(pubsub.UpdatedEvent, message)  // ← KEY: Broadcasts to TUI
    return nil
}
```

### UI Component Update
**File**: `internal/tui/components/chat/chat.go:460`
```go
uiMsg := items[assistantIndex].(messages.MessageCmp)
uiMsg.SetMessage(msg)              // ← KEY: Updates component state
m.listCmp.UpdateItem(               // ← KEY: Triggers re-render
    items[assistantIndex].ID(),
    uiMsg,
)
```

## Summary

The streaming architecture ensures **immediate rendering** by:

1. ✅ **No Batching**: Every token delta triggers an immediate update
2. ✅ **Pubsub Events**: Database updates trigger UI events automatically
3. ✅ **Component State**: Message components hold latest state and re-render on demand
4. ✅ **Bubble Tea Update Loop**: The TUI framework handles the message → update → view cycle efficiently

The result is a **responsive, real-time chat interface** where users see the agent's response character-by-character as it's generated, similar to ChatGPT or Claude web interfaces.
