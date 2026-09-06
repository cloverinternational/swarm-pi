# User Interaction System (Ring 0/1)

This package provides a unified interface for user interactions including permission approvals and question prompts. It bridges the SDK core with UI implementations in the TUI, IDE, or other frontends.

## Architecture

```
┌─────────────────────────────────────┐
│ Frontend (TUI, IDE, etc)            │
│ Implements UserInteractionBroker    │
└────────────┬────────────────────────┘
             │ implements
             ↓
┌─────────────────────────────────────┐
│ UserInteractionBroker (interface)   │
│ - AskQuestion()                     │
│ - AskApproval()                     │
│ - Notify()                          │
└────────────┬────────────────────────┘
             │ adapted by
             ↓
┌─────────────────────────────────────┐
│ BrokerAdapter                       │
│ Adapts to tools.ApprovalBroker      │
└────────────┬────────────────────────┘
             │ used by
             ↓
┌─────────────────────────────────────┐
│ SDK Permission System               │
│ (permission_engine.go, etc)         │
└─────────────────────────────────────┘
```

## Core Components

### 1. UserInteractionBroker Interface

The primary interface for user interactions.

```go
type UserInteractionBroker interface {
    // Ask the user a question (text, choice, confirm, number)
    AskQuestion(ctx context.Context, request QuestionRequest) (QuestionResponse, error)

    // Ask for permission approval
    AskApproval(ctx context.Context, request ApprovalRequest) (ApprovalResponse, error)

    // Send a non-blocking notification
    Notify(ctx context.Context, notification Notification) error
}
```

### 2. Question Types

- **Text**: Free-form text input
- **Choice**: Select one option from a list
- **MultiChoice**: Select multiple options
- **Confirm**: Yes/No confirmation
- **Number**: Numeric input
- **Visual Choice**: Browser-rendered visual picker

#### Visual Choice (Browser-rendered picker)

`QuestionTypeVisualChoice` presents a visual picker via a local HTTP server.
User clicks come back as the `Answer` field (selected key).

```go
result, err := registry.Execute(ctx, "ask_user_question", map[string]any{
    "question": "Which layout?",
    "type":     "visual_choice",
    "visual": map[string]any{
        "kind": "cards",
        "items": []any{
            map[string]any{"key": "single", "title": "Single Column",
                "body": map[string]any{"kind": "mermaid", "diagram": "flowchart TD\nHeader-->Content-->Footer"}},
            map[string]any{"key": "sidebar", "title": "Sidebar + Content",
                "body": map[string]any{"kind": "mermaid", "diagram": "flowchart LR\nSidebar-->Content"}},
        },
    },
})
// result.Content is the selected key: "single" or "sidebar"
```

Supported primitive kinds: `options`, `cards`, `split`, `markdown`, `mermaid`, `raw_html`.
See `swarm-sdk/interaction/visual/primitives.go` for field-level documentation.

The visual server lazy-starts per session. URL is announced via the broker's
Notify channel on first use.

### 3. Approval Responses

Approval decisions can have different scopes:

- **Once**: Allow this operation once
- **Session**: Allow for the current session
- **Always**: Add a permanent tool override

## Usage Examples

### In Frontend Code (Implementing UserInteractionBroker)

```go
// TUI example
type TUIBroker struct {
    modal *ApprovalModal
    app   *App
}

func (b *TUIBroker) AskQuestion(ctx context.Context, req QuestionRequest) (QuestionResponse, error) {
    // Send message to Bubble Tea
    b.app.showQuestionModal(req)

    // Wait for user response (via channel)
    select {
    case resp := <-b.responseChan:
        return resp, nil
    case <-ctx.Done():
        return QuestionResponse{}, ctx.Err()
    case <-time.After(time.Duration(req.Timeout) * time.Second):
        return QuestionResponse{Timeout: true}, nil
    }
}
```

### In Agent Code (Using ask_user_question Tool)

```go
// Agent asks user a question
result, err := registry.Execute(ctx, "ask_user_question", map[string]interface{}{
    "question": "Which auth method should I use?",
    "type":     "choice",
    "choices":  []string{"JWT", "Sessions", "OAuth"},
    "timeout":  300,
})

if err != nil {
    // Handle error (timeout, canceled, etc)
    log.Printf("Question failed: %v", err)
} else {
    // Use the answer
    authMethod := result.Content
    log.Printf("User chose: %s", authMethod)
}
```

### With YOLO Mode

```go
// Create normal permission checker
normalChecker := NewInteractivePermissionChecker(config, broker)

// Wrap with YOLO
yoloChecker := NewYOLOPermissionChecker(YOLOConfig{
    Policy: &YOLOPolicy{
        Enabled: true,
        ExcludeTools: []string{"bash"},  // Still require approval for bash
        DangerousOnly: true,               // Only bypass dangerous actions
        ExpiresAt: &futureTime,            // Auto-disable after time
    },
    UnderlyingChecker: normalChecker,
})

// Use in registry
registry.SetPermissionChecker(yoloChecker)
```

## Testing with MockBroker

```go
// Create mock broker
mockBroker := NewMockBroker()

// Configure responses
mockBroker.SetQuestionResponse("q1", QuestionResponse{
    Answer: "test answer",
})

// Use in tests
tool := NewAskUserQuestionTool(mockBroker)
result, err := tool.Execute(ctx, params)

// Verify interactions
questions := mockBroker.GetQuestionsAsked()
if len(questions) > 0 && questions[0].Type == QuestionTypeChoice {
    t.Log("Choice question was asked")
}
```

## Integration Steps for TUI

### 1. Implement UserInteractionBroker

Create `tui/internal/interaction/broker.go`:

```go
package interaction

type TUIBroker struct {
    app *chat.App
}

func (b *TUIBroker) AskQuestion(ctx context.Context, req interaction.QuestionRequest) (interaction.QuestionResponse, error) {
    // Send to Bubble Tea modal queue
    // Block until user responds
}

func (b *TUIBroker) AskApproval(ctx context.Context, req interaction.ApprovalRequest) (interaction.ApprovalResponse, error) {
    // Send to Bubble Tea approval modal queue
    // Block until user responds
}
```

### 2. Create Question Modal Component

```go
type QuestionModal struct {
    request   interaction.QuestionRequest
    input     textinput.Model
    choices   []string
    selected  int
    theme     Theme
}

func (m *QuestionModal) Update(msg tea.Msg) tea.Cmd {
    // Handle keyboard input
    // Return response via channel
}

func (m *QuestionModal) View() string {
    // Render question UI based on type
}
```

### 3. Wire Into App Initialization

```go
// In app.go or main.go
broker := &interaction.TUIBroker{app: app}
adapter := interaction.NewBrokerAdapter(broker)

// Pass to SDK
sdk.CreateConversation(ctx, mode, agent.ConversationOptions{
    ApprovalBroker: adapter,
})
```

### 4. Register ask_user_question Tool

```go
// In tool registration
questionTool := builtin.NewAskUserQuestionTool(broker)
registry.Register(questionTool)
```

## YOLO Mode

YOLO mode bypasses permission checks with optional constraints:

### Configuration

```go
policy := &YOLOPolicy{
    Enabled:       true,
    ExcludeTools:  []string{"bash", "file_delete"},  // Still require approval
    DangerousOnly: true,                              // Only bypass dangerous
    ExpiresAt:     &futureTime,                       // Auto-disable
    CreatedBy:     "user@example.com",                // Audit trail
}

checker := NewYOLOPermissionChecker(YOLOConfig{
    Policy:            policy,
    UnderlyingChecker: normalChecker,
    Logger:            logger,
})
```

### Audit Logging

YOLO mode optionally logs all grants for compliance:

```go
// Get statistics
grantCount, denials := checker.GetStats()
log.Printf("YOLO granted %d permissions (would have denied %d)", grantCount, denials)

// Get audit log
auditLog := checker.GetAuditLog()
for _, entry := range auditLog {
    log.Printf("YOLO granted %v for %s at %s", entry.Permissions, entry.Tool, entry.Timestamp)
}
```

## Best Practices

1. **Always set timeouts** for user questions to prevent indefinite blocking
2. **Log YOLO grants** for compliance and debugging
3. **Exclude dangerous tools** from YOLO mode (bash, file_delete)
4. **Set expiry times** for temporary YOLO mode
5. **Validate user input** when using free-text questions
6. **Use descriptive questions** that clearly explain what's needed
7. **Handle timeouts gracefully** with sensible defaults

## Security Considerations

- YOLO mode bypasses ALL permission checks - use with caution
- Only enable YOLO in development or trusted environments
- Set short expiry times for temporary YOLO mode
- Always log YOLO grants for audit trail
- Consider excluding high-risk tools from YOLO
- Use per-scope YOLO for more granular control

## Performance

- **Question asking**: Blocking operation, waits for user response
- **YOLO checking**: O(n) where n = number of excluded tools, negligible overhead
- **Audit logging**: Optional, minimal memory impact with configurable limits

## Troubleshooting

### Questions not appearing in TUI

- Verify `UserInteractionBroker` is properly implemented
- Check that `BrokerAdapter` is created and passed to SDK
- Ensure `ask_user_question` tool is registered in tool registry

### YOLO mode not working

- Verify `YOLOPolicy.Enabled` is true
- Check `ExpiresAt` hasn't passed
- Verify tool is not in `ExcludeTools`
- For `DangerousOnly`, ensure request has `dangerous: true` in context

### Approval timeouts

- Increase timeout value in `ApprovalRequest.Timeout`
- Verify context deadline isn't shorter than timeout
- Check that broker implementation properly handles timeouts
