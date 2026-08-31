# Message Streaming and Rendering in Crush

## Overview

Crush uses a real-time streaming architecture where messages are rendered immediately as they arrive from the LLM provider. This creates a responsive, ChatGPT-like experience where users see the agent "thinking" and responding in real-time.

## Core Concepts

### 1. Streaming vs Batching

**Batching (Traditional Approach)**
```
LLM generates → Wait for complete response → Store message → Render UI
```
- User sees nothing until response is complete
- Simple to implement
- Poor user experience for long responses

**Streaming (Crush Approach)**
```
Token arrives → Update message → Publish event → Update UI component → Render
     ↑________________________________________________________________|
                   (repeats for every token/delta)
```
- User sees response character-by-character
- More complex to implement
- Excellent user experience

### 2. Message State Management

Messages in Crush have multiple states during their lifecycle:

```go
// Message lifecycle states
Created       → Empty assistant message, shows spinner
Thinking      → Reasoning content streaming (for extended thinking models)
Streaming     → Text content streaming character by character
Tool Calling  → LLM requests tool execution
Tool Running  → Tool executing, showing progress
Finished      → Complete message with final content and metadata
Error/Cancel  → Message terminated with error or user cancellation
```

### 3. The Pubsub Event System

The core of streaming is the pubsub broker:

```go
// Message service publishes events
type Service interface {
    pubsub.Subscriber[Message]
    Create(ctx, sessionID, params) (Message, error)  // → CreatedEvent
    Update(ctx, message) error                        // → UpdatedEvent
    Delete(ctx, id) error                            // → DeletedEvent
}
```

**Event Flow:**
```
Agent callback fires
    ↓
Message updated in memory
    ↓
messages.Update(ctx, message)
    ↓
Database write
    ↓
s.Publish(pubsub.UpdatedEvent, message)
    ↓
All subscribers notified (including TUI)
```

## Streaming Implementation

### Agent Layer

The agent uses Fantasy's streaming API with callbacks:

```go
// internal/agent/agent.go
result, err := agent.Stream(ctx, fantasy.AgentStreamCall{
    // Reasoning/thinking callbacks (Claude, Gemini extended thinking)
    OnReasoningStart: func(id string, reasoning fantasy.ReasoningContent) error {
        currentAssistant.AppendReasoningContent(reasoning.Text)
        return a.messages.Update(ctx, *currentAssistant)
    },
    
    OnReasoningDelta: func(id string, text string) error {
        currentAssistant.AppendReasoningContent(text)
        return a.messages.Update(ctx, *currentAssistant)  // Immediate update!
    },
    
    OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
        currentAssistant.FinishThinking()
        return a.messages.Update(ctx, *currentAssistant)
    },
    
    // Text streaming (main content)
    OnTextDelta: func(id string, text string) error {
        currentAssistant.AppendContent(text)
        return a.messages.Update(ctx, *currentAssistant)  // Immediate update!
    },
    
    // Tool execution
    OnToolInputStart: func(id string, toolName string) error {
        toolCall := message.ToolCall{
            ID:       id,
            Name:     toolName,
            Finished: false,
        }
        currentAssistant.AddToolCall(toolCall)
        return a.messages.Update(ctx, *currentAssistant)
    },
    
    OnToolCall: func(tc fantasy.ToolCallContent) error {
        toolCall := message.ToolCall{
            ID:       tc.ToolCallID,
            Name:     tc.ToolName,
            Input:    tc.Input,
            Finished: true,
        }
        currentAssistant.AddToolCall(toolCall)
        return a.messages.Update(ctx, *currentAssistant)
    },
    
    OnToolResult: func(result fantasy.ToolResultContent) error {
        // Create a new tool message with the result
        toolResult := a.convertToToolResult(result)
        _, err := a.messages.Create(ctx, currentAssistant.SessionID, 
            message.CreateMessageParams{
                Role: message.Tool,
                Parts: []message.ContentPart{toolResult},
            })
        return err
    },
    
    OnStepFinish: func(stepResult fantasy.StepResult) error {
        currentAssistant.AddFinish(finishReason, "", "")
        return a.messages.Update(ctx, *currentAssistant)
    },
})
```

**Key Points:**
- Every callback calls `messages.Update()` immediately
- No buffering or batching
- Each update triggers a pubsub event

### Message Service Layer

```go
// internal/message/message.go
func (s *service) Update(ctx context.Context, message Message) error {
    // 1. Serialize message parts
    parts, err := marshallParts(message.Parts)
    if err != nil {
        return err
    }
    
    // 2. Update database
    err = s.q.UpdateMessage(ctx, db.UpdateMessageParams{
        ID:    message.ID,
        Parts: string(parts),
    })
    if err != nil {
        return err
    }
    
    // 3. Update timestamp
    message.UpdatedAt = time.Now().Unix()
    
    // 4. Publish event to all subscribers
    s.Publish(pubsub.UpdatedEvent, message)
    
    return nil
}
```

**Important:** Both DB write AND event publish happen together, ensuring UI is always in sync with persisted state.

### TUI Message List Component

The message list subscribes to all message events:

```go
// internal/tui/components/chat/chat.go
func (m *messageListCmp) Update(msg tea.Msg) (util.Model, tea.Cmd) {
    switch msg := msg.(type) {
    
    case pubsub.Event[message.Message]:
        // Handle all message events
        return m.handleMessageEvent(msg), tea.Batch(cmds...)
        
    // ... other events ...
    }
}

func (m *messageListCmp) handleMessageEvent(event pubsub.Event[message.Message]) tea.Cmd {
    switch event.Type {
    case pubsub.CreatedEvent:
        // New message - add to list
        if event.Payload.SessionID != m.session.ID {
            return m.handleChildSession(event)  // Delegate/sub-agent
        }
        return m.handleNewMessage(event.Payload)
        
    case pubsub.UpdatedEvent:
        // Message updated - update existing component
        if event.Payload.SessionID != m.session.ID {
            return m.handleChildSession(event)  // Delegate/sub-agent
        }
        
        switch event.Payload.Role {
        case message.Assistant:
            return m.handleUpdateAssistantMessage(event.Payload)
        case message.Tool:
            return m.handleToolMessage(event.Payload)
        }
        
    case pubsub.DeletedEvent:
        // Message deleted - remove from list
        return m.handleDeleteMessage(event.Payload)
    }
    return nil
}
```

### Updating Assistant Messages

When an assistant message is updated (most common during streaming):

```go
func (m *messageListCmp) handleUpdateAssistantMessage(msg message.Message) tea.Cmd {
    items := m.listCmp.Items()
    
    // Find the existing message component in the list
    assistantIndex, existingToolCalls := m.findAssistantMessageAndToolCalls(items, msg.ID)
    
    if assistantIndex != NotFound {
        // Get the existing UI component
        uiMsg := items[assistantIndex].(messages.MessageCmp)
        
        // Update its message data
        uiMsg.SetMessage(msg)
        
        // Tell the list to re-render this item
        m.listCmp.UpdateItem(items[assistantIndex].ID(), uiMsg)
    }
    
    // Also update any tool calls associated with this message
    return m.updateToolCalls(msg, existingToolCalls)
}
```

**Flow:**
1. Receive updated message from pubsub
2. Find the UI component displaying this message
3. Update the component's internal message state
4. Trigger list re-render for that item
5. The component's `View()` method renders the updated content

### Message Component Rendering

Individual message components render based on their current state:

```go
// internal/tui/components/chat/messages/messages.go
func (m *messageCmp) View() string {
    // Show spinner while waiting for first content
    if m.spinning && m.message.ReasoningContent().Thinking == "" {
        return m.style().PaddingLeft(1).Render(m.anim.View())
    }
    
    // Render based on role
    switch m.message.Role {
    case message.User:
        return m.renderUserMessage()
    default:
        return m.renderAssistantMessage()
    }
}

func (m *messageCmp) renderAssistantMessage() string {
    t := styles.CurrentTheme()
    parts := []string{}
    
    // Get current content (may be partial during streaming)
    content := strings.TrimSpace(m.message.Content().String())
    thinking := m.message.IsThinking()
    thinkingContent := strings.TrimSpace(m.message.ReasoningContent().Thinking)
    finished := m.message.IsFinished()
    
    // Show thinking content if present
    if thinking || thinkingContent != "" {
        m.anim.SetLabel("Thinking")
        thinkingContent = m.renderThinkingContent()
        parts = append(parts, thinkingContent)
    }
    
    // Show text content (streams in character by character)
    if content != "" {
        if thinkingContent != "" {
            parts = append(parts, "")  // Add spacing
        }
        parts = append(parts, m.toMarkdown(content))
    }
    
    // Join all parts vertically
    joined := lipgloss.JoinVertical(lipgloss.Left, parts...)
    return m.style().Render(joined)
}
```

**Rendering Stages:**
1. **Empty**: Shows spinner animation
2. **Thinking**: Shows reasoning content streaming in
3. **Content**: Shows text content streaming in (may include thinking above)
4. **Finished**: Shows complete content with footer (model, timing)

### Tool Call Components

Tool calls are separate components that render alongside messages:

```go
type ToolCallCmp interface {
    list.Item
    GetToolCall() message.ToolCall
    SetToolCall(message.ToolCall)
    SetToolResult(message.ToolResult)
    SetPermissionRequested()
    SetPermissionGranted()
    // ... more methods ...
}

func (m *toolCallCmp) View() string {
    // Render based on tool state
    switch {
    case !m.toolCall.Finished:
        return m.renderPending()      // Tool call streaming
    case m.toolResult == nil:
        return m.renderExecuting()    // Waiting for result
    case m.toolResult.IsError:
        return m.renderError()        // Tool failed
    default:
        return m.renderComplete()     // Tool succeeded
    }
}
```

## Performance Considerations

### 1. Update Frequency

During streaming, updates can arrive very frequently (potentially every token). Optimizations:

- **Database writes**: SQLite handles rapid updates well
- **Pubsub**: In-memory event bus is very fast
- **UI updates**: Bubble Tea's update loop is efficient

### 2. Rendering Optimization

```go
// Markdown rendering is cached
func (m *messageCmp) toMarkdown(content string) string {
    r := styles.GetMarkdownRenderer(m.textWidth())  // Cached renderer
    rendered, _ := r.Render(content)
    return strings.TrimSuffix(rendered, "\n")
}
```

### 3. List Virtualization

The message list uses a virtualized viewport:
- Only visible items are rendered
- Off-screen items are not computed
- Scrolling is efficient even with long conversations

### 4. Search Optimization

```go
// Search backwards - new messages are at the end
for i := len(items) - 1; i >= 0; i-- {
    if msg, ok := items[i].(messages.MessageCmp); ok {
        if msg.GetMessage().ID == messageID {
            return true
        }
    }
}
```

## Message State Transitions

### Complete Lifecycle

```
┌─────────────────────────────────────────────────────────────┐
│                     ASSISTANT MESSAGE                        │
└─────────────────────────────────────────────────────────────┘

1. PrepareStep (before LLM call)
   ├─ Create empty assistant message
   ├─ Event: CreatedEvent
   └─ UI: Show spinner animation

2. Reasoning Phase (optional, extended thinking models)
   ├─ OnReasoningStart
   │  ├─ Event: UpdatedEvent
   │  └─ UI: Show "Thinking" label with spinner
   │
   ├─ OnReasoningDelta (many times)
   │  ├─ Event: UpdatedEvent (each time)
   │  └─ UI: Thinking content streams in
   │
   └─ OnReasoningEnd
      ├─ Event: UpdatedEvent
      └─ UI: Thinking complete, may show signature

3. Text Phase
   ├─ OnTextDelta (many times)
   │  ├─ Event: UpdatedEvent (each time)
   │  └─ UI: Text content streams in character by character
   │
   └─ Complete text
      └─ UI: Full text rendered with markdown

4. Tool Phase (if LLM requests tools)
   ├─ OnToolInputStart
   │  ├─ Event: UpdatedEvent
   │  └─ UI: Tool call component appears (pending)
   │
   ├─ OnToolCall
   │  ├─ Event: UpdatedEvent
   │  └─ UI: Tool call shows input/arguments
   │
   └─ OnToolResult
      ├─ Event: CreatedEvent (new tool message)
      └─ UI: Tool result shown (success/error)

5. Finish
   ├─ OnStepFinish
   ├─ Event: UpdatedEvent
   └─ UI: Show footer with model name, response time, tokens
```

### Error Handling

```
Error occurs during streaming
   ├─ currentAssistant.FinishThinking()  // Close reasoning
   ├─ currentAssistant.AddFinish(FinishReasonError, title, details)
   ├─ messages.Update(ctx, *currentAssistant)
   │  └─ Event: UpdatedEvent
   └─ UI: Show error state with details
```

### Cancellation

```
User cancels (Ctrl+C)
   ├─ Context cancelled
   ├─ currentAssistant.AddFinish(FinishReasonCanceled, ...)
   ├─ Mark incomplete tool calls as cancelled
   ├─ messages.Update(ctx, *currentAssistant)
   │  └─ Event: UpdatedEvent
   └─ UI: Show "*Canceled*" state
```

## Key Files Reference

| File | Responsibility |
|------|----------------|
| `internal/agent/agent.go` | Streaming callbacks, message updates |
| `internal/message/message.go` | Message service, pubsub publishing |
| `internal/tui/components/chat/chat.go` | Message list, event handling |
| `internal/tui/components/chat/messages/messages.go` | Message component rendering |
| `internal/tui/components/chat/messages/tool.go` | Tool call component |
| `internal/pubsub/pubsub.go` | Event broker implementation |

## Debugging Streaming

### Enable Debug Logging

```bash
export CRUSH_DEBUG=1
crush
```

This logs:
- HTTP requests/responses to LLM providers
- Message updates
- Event publications

### Watch Message Updates

```go
// Add logging to message service
func (s *service) Update(ctx context.Context, message Message) error {
    slog.Debug("updating message", 
        "id", message.ID, 
        "role", message.Role,
        "content_length", len(message.Content().Text))
    // ... rest of update ...
}
```

### Monitor Events

```go
// Add subscriber to log events
messages.Subscribe(func(event pubsub.Event[message.Message]) {
    slog.Debug("message event", 
        "type", event.Type, 
        "id", event.Payload.ID,
        "role", event.Payload.Role)
})
```

## Common Patterns

### Pattern 1: Streaming Text Only

```go
OnTextDelta: func(id string, text string) error {
    currentAssistant.AppendContent(text)
    return a.messages.Update(ctx, *currentAssistant)
}
```
Use when: Simple text-only responses

### Pattern 2: Streaming with Thinking

```go
OnReasoningDelta: func(id string, text string) error {
    currentAssistant.AppendReasoningContent(text)
    return a.messages.Update(ctx, *currentAssistant)
}
```
Use when: Extended thinking models (Claude, Gemini)

### Pattern 3: Streaming with Tools

```go
OnToolCall: func(tc fantasy.ToolCallContent) error {
    currentAssistant.AddToolCall(/* ... */)
    return a.messages.Update(ctx, *currentAssistant)
}

OnToolResult: func(result fantasy.ToolResultContent) error {
    _, err := a.messages.Create(ctx, sessionID, /* ... */)
    return err
}
```
Use when: Agent needs to execute tools

## Summary

Crush's streaming architecture provides:

✅ **Real-time feedback**: Users see responses as they're generated
✅ **Transparent tool execution**: Tool calls and results are visible
✅ **Thinking visibility**: Extended thinking/reasoning is shown
✅ **Reliable state**: Database and UI always in sync
✅ **Efficient rendering**: Only visible content is rendered
✅ **Error handling**: Cancellations and errors are handled gracefully

The key insight: **Every token/delta triggers an immediate update**, creating a responsive, ChatGPT-like experience in the terminal.
