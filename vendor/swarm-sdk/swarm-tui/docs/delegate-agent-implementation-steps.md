# Delegate Agent Streaming - Implementation Steps

## Summary

We've created the `DelegateAgentCmp` component. Now we need to integrate it into the message list to replace `ToolCallCmp` for agent tool calls.

## Files Created

✅ `example/crush/internal/tui/components/chat/messages/delegate.go` - New delegate agent component

## Files to Modify

### 1. `example/crush/internal/tui/components/chat/chat.go`

#### Change 1: Replace `handleChildSession` function (Line 237-303)

**Current code:**
```go
// handleChildSession handles messages from child sessions (agent tools).
func (m *messageListCmp) handleChildSession(event pubsub.Event[message.Message]) tea.Cmd {
	var cmds []tea.Cmd
	if len(event.Payload.ToolCalls()) == 0 && len(event.Payload.ToolResults()) == 0 {
		return nil
	}

	// Check if this is an agent tool session and parse it
	childSessionID := event.Payload.SessionID
	parentMessageID, toolCallID, ok := m.app.Sessions.ParseAgentToolSessionID(childSessionID)
	if !ok {
		return nil
	}
	items := m.listCmp.Items()
	toolCallInx := NotFound
	var toolCall messages.ToolCallCmp
	for i := len(items) - 1; i >= 0; i-- {
		if msg, ok := items[i].(messages.ToolCallCmp); ok {
			if msg.ParentMessageID() == parentMessageID && msg.GetToolCall().ID == toolCallID {
				toolCallInx = i
				toolCall = msg
			}
		}
	}
	if toolCallInx == NotFound {
		return nil
	}
	// ... rest of nested tool call handling ...
}
```

**Replace with:**
```go
// handleChildSession handles messages from child sessions (delegate agents).
func (m *messageListCmp) handleChildSession(event pubsub.Event[message.Message]) tea.Cmd {
	// Check if this is an agent tool session and parse it
	childSessionID := event.Payload.SessionID
	parentMessageID, toolCallID, ok := m.app.Sessions.ParseAgentToolSessionID(childSessionID)
	if !ok {
		return nil
	}

	items := m.listCmp.Items()

	// Find the delegate agent component
	delegateIndex := m.findDelegateAgentByToolCall(items, parentMessageID, toolCallID)
	
	if delegateIndex != NotFound {
		// This is a delegate agent - handle its messages
		return m.handleDelegateAgentMessage(delegateIndex, event)
	}

	// Fallback to old behavior for non-delegate child sessions (agentic fetch, etc)
	return m.handleLegacyChildSession(event, parentMessageID, toolCallID)
}
```

#### Change 2: Add new helper functions (after handleChildSession)

Add these 4 new functions after `handleChildSession`:

```go
// findDelegateAgentByToolCall finds a delegate agent component by parent message and tool call ID
func (m *messageListCmp) findDelegateAgentByToolCall(items []list.Item, parentMessageID, toolCallID string) int {
	for i := len(items) - 1; i >= 0; i-- {
		if delegate, ok := items[i].(messages.DelegateAgentCmp); ok {
			if delegate.ParentMessageID() == parentMessageID && delegate.GetToolCall().ID == toolCallID {
				return i
			}
		}
	}
	return NotFound
}

// handleDelegateAgentMessage handles messages for a delegate agent
func (m *messageListCmp) handleDelegateAgentMessage(delegateIndex int, event pubsub.Event[message.Message]) tea.Cmd {
	items := m.listCmp.Items()
	delegateAgent := items[delegateIndex].(messages.DelegateAgentCmp)

	switch event.Type {
	case pubsub.CreatedEvent:
		// New message in delegate conversation
		delegateAgent.AddDelegateMessage(event.Payload)

	case pubsub.UpdatedEvent:
		// Update existing message in delegate conversation
		delegateAgent.UpdateDelegateMessage(event.Payload)
	}

	// Update the component in the list
	m.listCmp.UpdateItem(delegateAgent.ID(), delegateAgent)

	return nil
}

// handleLegacyChildSession handles old-style nested tool calls (for backwards compatibility)
func (m *messageListCmp) handleLegacyChildSession(event pubsub.Event[message.Message], parentMessageID, toolCallID string) tea.Cmd {
	var cmds []tea.Cmd
	if len(event.Payload.ToolCalls()) == 0 && len(event.Payload.ToolResults()) == 0 {
		return nil
	}

	items := m.listCmp.Items()
	toolCallIndex := m.findToolCallByIDAndParent(items, parentMessageID, toolCallID)
	
	if toolCallIndex == NotFound {
		return nil
	}
	
	toolCall := items[toolCallIndex].(messages.ToolCallCmp)
	nestedToolCalls := toolCall.GetNestedToolCalls()

	for _, tc := range event.Payload.ToolCalls() {
		found := false
		for existingInx, existingTC := range nestedToolCalls {
			if existingTC.GetToolCall().ID == tc.ID {
				nestedToolCalls[existingInx].SetToolCall(tc)
				found = true
				break
			}
		}
		if !found {
			nestedCall := messages.NewToolCallCmp(
				event.Payload.ID,
				tc,
				m.app.Permissions,
				messages.WithToolCallNested(true),
			)
			cmds = append(cmds, nestedCall.Init())
			nestedToolCalls = append(nestedToolCalls, nestedCall)
		}
	}

	for _, tr := range event.Payload.ToolResults() {
		for nestedInx, nestedTC := range nestedToolCalls {
			if nestedTC.GetToolCall().ID == tr.ToolCallID {
				nestedToolCalls[nestedInx].SetToolResult(tr)
				break
			}
		}
	}

	toolCall.SetNestedToolCalls(nestedToolCalls)
	m.listCmp.UpdateItem(toolCall.ID(), toolCall)

	return tea.Batch(cmds...)
}

// findToolCallByIDAndParent finds a tool call by parent message ID and tool call ID
func (m *messageListCmp) findToolCallByIDAndParent(items []list.Item, parentMessageID, toolCallID string) int {
	for i := len(items) - 1; i >= 0; i-- {
		if tc, ok := items[i].(messages.ToolCallCmp); ok {
			if tc.ParentMessageID() == parentMessageID && tc.GetToolCall().ID == toolCallID {
				return i
			}
		}
	}
	return NotFound
}
```

#### Change 3: Modify `updateOrAddToolCall` (around line 500-515)

**Current code:**
```go
func (m *messageListCmp) updateOrAddToolCall(msg message.Message, tc message.ToolCall, existingToolCalls map[int]messages.ToolCallCmp) tea.Cmd {
	// Try to find existing tool call
	for _, existingTC := range existingToolCalls {
		if tc.ID == existingTC.GetToolCall().ID {
			existingTC.SetToolCall(tc)
			if msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonCanceled {
				existingTC.SetCancelled()
			}
			m.listCmp.UpdateItem(tc.ID, existingTC)
			return nil
		}
	}

	// Add new tool call if not found
	return m.listCmp.AppendItem(messages.NewToolCallCmp(msg.ID, tc, m.app.Permissions))
}
```

**Replace with:**
```go
func (m *messageListCmp) updateOrAddToolCall(msg message.Message, tc message.ToolCall, existingToolCalls map[int]messages.ToolCallCmp) tea.Cmd {
	// Try to find existing tool call
	for _, existingTC := range existingToolCalls {
		if tc.ID == existingTC.GetToolCall().ID {
			existingTC.SetToolCall(tc)
			if msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonCanceled {
				existingTC.SetCancelled()
			}
			m.listCmp.UpdateItem(tc.ID, existingTC)
			return nil
		}
	}

	// Check if this is a delegate agent tool call
	if tc.Name == agent.AgentToolName {
		// Create delegate agent component instead
		return m.listCmp.AppendItem(messages.NewDelegateAgentCmp(msg.ID, tc, m.app.Permissions))
	}

	// Regular tool call - use ToolCallCmp
	return m.listCmp.AppendItem(messages.NewToolCallCmp(msg.ID, tc, m.app.Permissions))
}
```

#### Change 4: Modify `handleNewAssistantMessage` (around line 517-538)

**Current code:**
```go
// handleNewAssistantMessage processes new assistant messages and their tool calls.
func (m *messageListCmp) handleNewAssistantMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd

	// Add assistant message if it should be displayed
	if m.shouldShowAssistantMessage(msg) {
		cmd := m.listCmp.AppendItem(
			messages.NewMessageCmp(
				msg,
			),
		)
		cmds = append(cmds, cmd)
	}

	// Add tool calls
	for _, tc := range msg.ToolCalls() {
		cmd := m.listCmp.AppendItem(messages.NewToolCallCmp(msg.ID, tc, m.app.Permissions))
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}
```

**Replace with:**
```go
// handleNewAssistantMessage processes new assistant messages and their tool calls.
func (m *messageListCmp) handleNewAssistantMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd

	// Add assistant message if it should be displayed
	if m.shouldShowAssistantMessage(msg) {
		cmd := m.listCmp.AppendItem(
			messages.NewMessageCmp(
				msg,
			),
		)
		cmds = append(cmds, cmd)
	}

	// Add tool calls
	for _, tc := range msg.ToolCalls() {
		var cmd tea.Cmd
		if tc.Name == agent.AgentToolName {
			// Create delegate agent component
			cmd = m.listCmp.AppendItem(messages.NewDelegateAgentCmp(msg.ID, tc, m.app.Permissions))
		} else {
			// Regular tool call
			cmd = m.listCmp.AppendItem(messages.NewToolCallCmp(msg.ID, tc, m.app.Permissions))
		}
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}
```

#### Change 5: Modify `convertAssistantMessage` (around line 600-635)

**Current code (the section that handles agent tools):**
```go
	// Add tool calls with their results and status
	for _, tc := range msg.ToolCalls() {
		options := m.buildToolCallOptions(tc, msg, toolResultMap)
		uiMessages = append(uiMessages, messages.NewToolCallCmp(msg.ID, tc, m.app.Permissions, options...))
		// If this tool call is the agent tool or agentic fetch, fetch nested tool calls
		if tc.Name == agent.AgentToolName || tc.Name == tools.AgenticFetchToolName {
			agentToolSessionID := m.app.Sessions.CreateAgentToolSessionID(msg.ID, tc.ID)
			nestedMessages, _ := m.app.Messages.List(context.Background(), agentToolSessionID)
			nestedToolResultMap := m.buildToolResultMap(nestedMessages)
			nestedUIMessages := m.convertMessagesToUI(nestedMessages, nestedToolResultMap)
			nestedToolCalls := make([]messages.ToolCallCmp, 0, len(nestedUIMessages))
			for _, nestedMsg := range nestedUIMessages {
				if toolCall, ok := nestedMsg.(messages.ToolCallCmp); ok {
					toolCall.SetIsNested(true)
					nestedToolCalls = append(nestedToolCalls, toolCall)
				}
			}
			uiMessages[len(uiMessages)-1].(messages.ToolCallCmp).SetNestedToolCalls(nestedToolCalls)
		}
	}
```

**Replace with:**
```go
	// Add tool calls with their results and status
	for _, tc := range msg.ToolCalls() {
		if tc.Name == agent.AgentToolName {
			// Create delegate agent component and load its conversation
			delegateAgent := messages.NewDelegateAgentCmp(msg.ID, tc, m.app.Permissions)
			m.loadDelegateConversation(delegateAgent, msg.ID, tc.ID, toolResultMap)
			uiMessages = append(uiMessages, delegateAgent)
		} else if tc.Name == tools.AgenticFetchToolName {
			// Agentic fetch still uses old nested tool call approach
			options := m.buildToolCallOptions(tc, msg, toolResultMap)
			toolCallCmp := messages.NewToolCallCmp(msg.ID, tc, m.app.Permissions, options...)
			agentToolSessionID := m.app.Sessions.CreateAgentToolSessionID(msg.ID, tc.ID)
			nestedMessages, _ := m.app.Messages.List(context.Background(), agentToolSessionID)
			nestedToolResultMap := m.buildToolResultMap(nestedMessages)
			nestedUIMessages := m.convertMessagesToUI(nestedMessages, nestedToolResultMap)
			nestedToolCalls := make([]messages.ToolCallCmp, 0, len(nestedUIMessages))
			for _, nestedMsg := range nestedUIMessages {
				if toolCall, ok := nestedMsg.(messages.ToolCallCmp); ok {
					toolCall.SetIsNested(true)
					nestedToolCalls = append(nestedToolCalls, toolCall)
				}
			}
			toolCallCmp.SetNestedToolCalls(nestedToolCalls)
			uiMessages = append(uiMessages, toolCallCmp)
		} else {
			// Regular tool call
			options := m.buildToolCallOptions(tc, msg, toolResultMap)
			uiMessages = append(uiMessages, messages.NewToolCallCmp(msg.ID, tc, m.app.Permissions, options...))
		}
	}
```

#### Change 6: Add new helper function (after `convertAssistantMessage`)

Add this new function:

```go
// loadDelegateConversation loads the full conversation for a delegate agent
func (m *messageListCmp) loadDelegateConversation(delegateAgent messages.DelegateAgentCmp, parentMessageID, toolCallID string, toolResultMap map[string]message.ToolResult) {
	childSessionID := m.app.Sessions.CreateAgentToolSessionID(parentMessageID, toolCallID)
	childMessages, _ := m.app.Messages.List(context.Background(), childSessionID)

	for _, childMsg := range childMessages {
		delegateAgent.AddDelegateMessage(childMsg)
	}

	// Set tool result if available
	if tr, ok := toolResultMap[toolCallID]; ok {
		delegateAgent.SetToolResult(tr)
	}
}
```

## Testing Steps

1. **Build the project:**
   ```bash
   cd /home/rincon/Swarm/swarmos
   go build -o crush ./example/crush
   ```

2. **Test delegate agent:**
   - Run crush
   - Send a message that triggers the agent tool
   - Verify you see the delegate agent component (collapsed by default)
   - Press Ctrl+A to expand and see the full conversation
   - Verify streaming works in real-time

3. **Test backwards compatibility:**
   - Verify agentic fetch still works with nested tool calls
   - Verify regular tool calls still work

## Expected Behavior

### Collapsed State
```
✓ Agent ▶
   Task: Analyze the codebase...
   Status: working...
   (Ctrl+A to toggle)
   
   [Task Agent animation spinner]
```

### Expanded State
```
✓ Agent ▼
   Task: Analyze the codebase...
   Status: complete
   (Ctrl+A to toggle)
   
   Delegate conversation:
   
     👤 Analyze the codebase...
     
     🤖 [Thinking] Let me explore the structure...
     
        I'll start by checking...
        [streaming text]
     
     🔧 bash(command: "ls -la")
        ✓ Output: [files...]
   
   Final Result:
   Analysis complete. Found 3 key components: ...
```

## Rollback

If something goes wrong:
```bash
cd /home/rincon/Swarm/swarmos
mv example/crush/internal/tui/components/chat/chat.go.backup example/crush/internal/tui/components/chat/chat.go
rm example/crush/internal/tui/components/chat/messages/delegate.go
go build -o crush ./example/crush
```
