# TUI AskUser Implementation - Architecture & File Mapping

## 1. Complete Architecture Diagram

```
┌──────────────────────────────────────────────────────────────────┐
│                      Agent SDK (Go)                              │
├──────────────────────────────────────────────────────────────────┤
│                                                                   │
│  ┌─────────────────────────┐        ┌──────────────────────┐    │
│  │  Agent Tool Execution   │        │  Permission System   │    │
│  │                         │        │                      │    │
│  │ askUserTool.execute()   │◄──────►│ permissionChecker    │    │
│  │                         │        │ .CheckWithContext()  │    │
│  └────────┬────────────────┘        └──────┬───────────────┘    │
│           │                                 │                    │
│           │ Need user input                 │ Needs approval     │
│           ▼                                 ▼                    │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │      UserInteractionBroker Interface                    │    │
│  │  ┌───────────────────────────────────────────────────┐  │    │
│  │  │ AskQuestion(ctx, UserQuestionRequest)             │  │    │
│  │  │ RequestApproval(ctx, PermissionApprovalRequest)   │  │    │
│  │  │ SetConfig(config)                                 │  │    │
│  │  │ GetPendingRequests()                              │  │    │
│  │  └───────────────────────────────────────────────────┘  │    │
│  └────────────────────────┬─────────────────────────────────┘    │
│                           │                                      │
└───────────────────────────┼──────────────────────────────────────┘
                            │
                   (Channel-based IPC)
                            │
┌───────────────────────────┼──────────────────────────────────────┐
│                           ▼                                      │
│                      TUI Application                             │
│                   (Bubbletea Framework)                          │
│                                                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │        UserInteractionCenter                            │   │
│  │  ┌──────────────────┐  ┌──────────────────────────────┐ │   │
│  │  │ ApprovalCard     │  │ QuestionDialog              │ │   │
│  │  │                  │  │                             │ │   │
│  │  │ Shows request    │  │ Shows question + options   │ │   │
│  │  │ Buttons:         │  │ Input field                │ │   │
│  │  │  • Approve Once  │  │ Buttons:                   │ │   │
│  │  │  • Approve Sess. │  │  • Submit                  │ │   │
│  │  │  • Approve Alw.  │  │  • Use Default             │ │   │
│  │  │  • Deny          │  │  • Cancel                  │ │   │
│  │  │  • [p] Preview   │  └──────────────────────────────┘ │   │
│  │  └──────────────────┘                                    │   │
│  │  RequestQueue        ResponseHistory                       │   │
│  │  [pending...]       [answered...]                        │   │
│  └──────────────────────────────────────────────────────────┘   │
│                           │                                      │
└───────────────────────────┼──────────────────────────────────────┘
                            │
                      (Response via broker)
                            │
```

---

## 2. Component Interaction Sequence

### Approval Flow
```
SDK                          Broker                    UI
│                            │                         │
├─ RequestApproval()────────►│                         │
│                            ├─ Queue request────────►│
│                            │                         ├─ Render ApprovalCard
│                            │                         │
│                            │                         ├─ User presses arrow keys
│                            │                         │
│                            │                         ├─ User presses enter
│                            │                         │
│                            │◄────── Create response ─┤
│                            │                         │
│◄───── Return response ─────┤                         │
│                            │                         │
├─ Apply decision            │                         │
│  ├─ Grant/Revoke           │                         │
│  └─ Continue/Block tool    │                         │
```

### Question Flow
```
Agent                        Broker                    UI
│                            │                         │
├─ AskQuestion()────────────►│                         │
│                            ├─ Queue request────────►│
│                            │                         ├─ Render QuestionDialog
│                            │                         │
│                            │                         ├─ User types answer
│                            │                         │
│                            │                         ├─ User presses enter
│                            │                         │
│                            │◄───── Create response ──┤
│                            │                         │
│◄────── Return answer ──────┤                         │
│                            │                         │
├─ Continue with answer      │                         │
```

---

## 3. File Structure & Implementation Map

### Current Files (To Understand)

```
/home/rincon/swarm/TUI/

sdk/tools/
├── permission.go
│   ├── Permission constants (file_read, file_write, bash_execute, etc.)
│   ├── PermissionChecker interface
│   │   └── Methods: Check, CheckWithContext, RequestApproval, Grant, Revoke, IsGranted
│   ├── PermissionPolicy (allow, deny, ask, sandbox)
│   └── PermissionRequest struct
│
├── permission_config.go
│   ├── PermissionConfig struct (level, timeoutSeconds, defaults, overrides, rules)
│   ├── Decision enum (approve_once, approve_session, approve_always, deny, deny_stop)
│   ├── PermissionApprovalRequest struct ◄── Model for approval requests
│   │   ├── RequestID
│   │   ├── ConversationID
│   │   ├── Tool, Permission, Target
│   │   ├── Reason, Preview, Context
│   │   ├── Timeout, BatchID
│   │   └── ApprovalPreview (Type, Content)
│   ├── ApprovalResponse struct
│   │   ├── Decision
│   │   ├── Outcome
│   │   └── GrantedScope
│   ├── ApprovalBroker interface ◄── What to extend
│   │   ├── Request(ctx, req) response
│   │   ├── Respond(requestID, decision)
│   │   ├── SetConfig(config)
│   │   └── GetPendingRequests()
│   └── DefaultPermissionConfig()
│
├── interactive_permission_checker.go
│   ├── InteractivePermissionChecker struct
│   │   ├── broker ApprovalBroker
│   │   ├── config PermissionConfig
│   │   ├── grants (cached permissions)
│   │   └── modeConfigs, agentConfigs
│   ├── Check methods
│   ├── RequestApproval implementation ◄── Shows approval pattern
│   │   └── Builds PermissionApprovalRequest
│   │   └── Calls broker.Request()
│   │   └── Applies decision
│   └── Grant/Revoke methods
│
├── permission_engine.go
│   ├── PermissionEngine (evaluation logic)
│   ├── Evaluate() - applies rules & defaults
│   ├── PermissionEvaluationRequest struct
│   │   ├── Permissions, Tool, Operations
│   │   ├── Paths, Commands, URLs
│   │   ├── Scope, ScopeID, AgentID, Mode
│   │   └── Dangerous flag
│   ├── PermissionDecision struct
│   │   ├── Policy (allow/deny/ask/sandbox)
│   │   ├── NeedsApproval
│   │   ├── Reason, RuleID, Source
│   │   └── BuildPermissionEvaluationRequest()
│   └── Priority-based rule sorting
│
└── permission_validation.go
    ├── Input validation helpers
    └── Dangerous action detection
```

### New Files to Create

```
sdk/tools/
├── user_question.go ◄────────── NEW: Question request/response types
│   ├── UserQuestionRequest struct
│   │   ├── RequestID
│   │   ├── ConversationID
│   │   ├── Question, Context
│   │   ├── Options[]
│   │   ├── DefaultAnswer
│   │   ├── Required
│   │   └── Timeout
│   ├── UserQuestionResponse struct
│   │   ├── Answer
│   │   ├── Outcome (answered, timeout, cancelled)
│   │   └── AnsweredAt
│   └── QuestionBroker interface
│       ├── AskQuestion(ctx, req) response
│       ├── Respond(requestID, answer)
│       ├── SetConfig(config)
│       └── GetPendingRequestions()
│
├── user_interaction_broker.go ◄── NEW: Unified broker
│   ├── UserInteractionBroker interface
│   │   ├── RequestApproval(ctx, req)
│   │   ├── AskQuestion(ctx, req)
│   │   ├── SetConfig(config)
│   │   └── GetPendingRequests()
│   └── Composition of ApprovalBroker + QuestionBroker
│
├── question_tool.go ◄────────── NEW: Agent question tool
│   ├── QuestionTool implements Tool interface
│   ├── Name(), Description(), Parameters()
│   ├── Execute(ctx, params)
│   └── Integration with broker
│
└── tests/
    ├── user_question_test.go ◄── NEW
    ├── user_interaction_broker_test.go ◄── NEW
    └── question_tool_test.go ◄── NEW
```

### TUI UI Files to Create

```
cmd/swarmos/
├── main.go
│   └── Setup UserInteractionCenter
│
internal/chat/
├── interaction.go ◄─────────── NEW: Broker implementation
│   ├── TUIBroker struct
│   │   ├── requestQueue chan Request
│   │   ├── responseMap  map[string]chan Response
│   │   ├── history      []Request
│   │   └── config       Configuration
│   ├── RequestApproval() - sends PermissionApprovalRequest
│   ├── AskQuestion() - sends UserQuestionRequest
│   ├── Respond() - receives response
│   └── GetPendingRequests()
│
├── components/approval_card.go ◄─ NEW: Approval UI
│   ├── ApprovalCard struct
│   │   ├── request PermissionApprovalRequest
│   │   ├── selectedIdx int
│   │   ├── previewOpen bool
│   │   └── options []string
│   ├── Init()
│   ├── Update(msg tea.Msg)
│   │   ├── Handle arrow keys (↑/↓)
│   │   ├── Handle enter (select)
│   │   ├── Handle 'p' (preview toggle)
│   │   └── Send ApprovalDecisionMsg
│   └── View() string
│       ├── Show request details
│       ├── Show numbered options
│       ├── Show preview if open
│       └── Show help text
│
├── components/question_dialog.go ◄ NEW: Question UI
│   ├── QuestionDialog struct
│   │   ├── request UserQuestionRequest
│   │   ├── input string
│   │   ├── selectedIdx int (for multiple choice)
│   │   └── errorMsg string
│   ├── Init()
│   ├── Update(msg tea.Msg)
│   │   ├── Handle text input (typing)
│   │   ├── Handle arrow keys (↑/↓ for options)
│   │   ├── Handle enter (submit)
│   │   ├── Handle esc (cancel/default)
│   │   └── Send QuestionAnswerMsg
│   └── View() string
│       ├── Show question
│       ├── Show context
│       ├── Show input field or options
│       └── Show help text
│
└── components/interaction_center.go ◄ NEW: Coordinator
    ├── UserInteractionCenter struct
    │   ├── approval *ApprovalCard
    │   ├── question *QuestionDialog
    │   ├── queue []interface{}
    │   └── history []interface{}
    ├── Init()
    ├── Update(msg tea.Msg) tea.Cmd
    │   ├── Route to active component
    │   ├── Handle ApprovalDecisionMsg
    │   ├── Handle QuestionAnswerMsg
    │   └── Pop next from queue
    └── View() string
        ├── Show active card/dialog
        ├── Show queue indicator
        └── Show history indicator
```

### Messages (Bubbletea)

```go
// messages.go
type ApprovalDecisionMsg struct {
  RequestID string
  Decision  Decision  // approve_once, approve_session, etc.
}

type QuestionAnswerMsg struct {
  RequestID string
  Answer    string
}

type RequestReceivedMsg struct {
  RequestID   string
  RequestType string  // "approval" or "question"
}

type RequestTimeoutMsg struct {
  RequestID string
}

type RequestHistoryMsg struct {
  Request interface{}  // PermissionApprovalRequest or UserQuestionRequest
}
```

---

## 4. Implementation Phases with File Dependencies

### Phase 1: Data Structures (Week 1, Day 1-2)

**Files to create:**
- `sdk/tools/user_question.go`
- `sdk/tools/user_interaction_broker.go`

**Key types:**
```go
// user_question.go
type UserQuestionRequest struct { /* ... */ }
type UserQuestionResponse struct { /* ... */ }
type QuestionBroker interface { /* ... */ }

// user_interaction_broker.go (extends permission_config.go)
type UserInteractionBroker interface {
  RequestApproval(ctx context.Context, req PermissionApprovalRequest) (ApprovalResponse, error)
  AskQuestion(ctx context.Context, req UserQuestionRequest) (UserQuestionResponse, error)
  // ...
}
```

**Dependencies:**
- Must read: `permission_config.go`
- No external dependencies

**Tests:**
- Unit tests for struct marshaling

### Phase 2: SDK Integration (Week 1, Day 3-4)

**Files to create:**
- `sdk/tools/question_tool.go`
- Updates to `interactive_permission_checker.go`

**Key additions:**
```go
// question_tool.go
type QuestionTool struct { /* ... */ }
func (t *QuestionTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error)

// Updates to interactive_permission_checker.go
func (c *InteractivePermissionChecker) AskQuestion(ctx context.Context, q UserQuestionRequest) (UserQuestionResponse, error)
```

**Dependencies:**
- Phase 1 files
- `permission.go` (Tool interface)
- `tool.go` (ToolResult)

**Tests:**
- Unit tests for question tool
- Integration tests with checker

### Phase 3: Mock Broker (Week 1, Day 5)

**Files to create:**
- `sdk/tools/mock_broker.go`

**Implementation:**
```go
type MockBroker struct {
  responses map[string]interface{}
  requests []interface{}
}

func (m *MockBroker) RequestApproval(ctx context.Context, req PermissionApprovalRequest) (ApprovalResponse, error)
func (m *MockBroker) AskQuestion(ctx context.Context, req UserQuestionRequest) (UserQuestionResponse, error)
```

**Purpose:**
- Enable full SDK testing without UI
- Support headless mode
- Deterministic testing

**Tests:**
- Full end-to-end tests without UI

### Phase 4: UI Foundation (Week 2, Day 1-3)

**Files to create:**
- `cmd/swarmos/messages.go`
- `internal/chat/interaction.go`
- `internal/chat/components/approval_card.go`
- `internal/chat/components/question_dialog.go`

**Key components:**
- Message types
- TUIBroker implementation
- UI cards

**Dependencies:**
- Bubbletea (charmbracelet/bubbletea)
- SDK (Phase 1-2 files)
- Lipgloss (styling)

**Tests:**
- Component rendering tests
- Keyboard input tests
- State management tests

### Phase 5: Integration (Week 2, Day 4-5)

**Files to create:**
- `internal/chat/components/interaction_center.go`
- Updates to `cmd/swarmos/main.go`

**Key additions:**
- Center coordinator
- Queue management
- History tracking
- Main TUI integration

**Dependencies:**
- Phase 4 files
- Main.go

**Tests:**
- Integration tests
- Multi-request tests
- Timeout tests

### Phase 6: Polish & Testing (Week 3)

**Files to update:**
- All SDK files (documentation)
- All UI files (edge cases)
- README/guides

**Tasks:**
- Performance testing
- Load testing (queue handling)
- Accessibility audit
- Documentation
- User testing

---

## 5. Key Integration Points

### 1. SDK to Broker Communication

```go
// In interactive_permission_checker.go
func (c *InteractivePermissionChecker) requestApproval(...) bool {
  // ... existing code ...

  req := PermissionApprovalRequest{
    RequestID: generateRequestID(),
    // ... populate request ...
  }

  // Call broker (already does this)
  resp, err := c.broker.Request(ctx, req)

  // NEW: Also handle questions
  if needsQuestionFirst {
    qReq := UserQuestionRequest{
      Question: "What should I do here?",
    }
    qResp, err := c.broker.AskQuestion(ctx, qReq)
    // Use answer to inform approval
  }

  return applyDecision(resp.Decision)
}
```

### 2. Broker to UI Communication

```go
// In TUIBroker (new)
func (b *TUIBroker) RequestApproval(ctx context.Context, req PermissionApprovalRequest) (ApprovalResponse, error) {
  // Create unique request ID
  id := req.RequestID

  // Create response channel
  responseChan := make(chan ApprovalResponse, 1)
  b.responseMap[id] = responseChan

  // Queue request for UI
  b.requestQueue <- RequestQueuedMsg{
    RequestID: id,
    Payload: req,
    Type: "approval",
  }

  // Wait for response (with timeout)
  select {
  case resp := <-responseChan:
    return resp, nil
  case <-ctx.Done():
    return ApprovalResponse{}, ctx.Err()
  case <-time.After(timeout):
    // Timeout handling
  }
}

// In InteractionCenter.Update()
case msg tea.KeyMsg:
  if msg.String() == "enter" {
    // User selected decision
    decision := selectedOption
    center.broker.Respond(requestID, decision)
  }
```

### 3. Initialization Chain

```go
// main.go
func main() {
  // 1. Create SDK components
  config := loadPermissionConfig()
  broker := NewTUIBroker()  // NEW
  checker := NewInteractivePermissionChecker(config, broker)

  // 2. Create UI components
  approvalCard := NewApprovalCard()
  questionDialog := NewQuestionDialog()
  center := NewUserInteractionCenter(approvalCard, questionDialog, broker)

  // 3. Create chat model with broker reference
  chat := NewChatModel(checker, broker, center)

  // 4. Run TUI
  p := tea.NewProgram(chat)
  p.Run()
}
```

---

## 6. Error Handling Strategy

### Permission Approval Errors

```go
// In interactive_permission_checker.go
if err := c.broker.Request(ctx, req); err != nil {
  switch err.(type) {
  case *TimeoutError:
    // Handle timeout based on config
    if c.config.TimeoutBehavior == "stop" {
      return false  // Deny
    }
    return true   // Allow

  case *BrokerError:
    // Broker unavailable - fallback behavior
    return false  // Conservative default

  default:
    // Unknown error
    return false
  }
}
```

### Question Answer Errors

```go
// In question_tool.go
if resp.Answer == "" && q.Required {
  return ToolError{
    Code: "ANSWER_REQUIRED",
    Message: "Question requires an answer",
  }
}

if !isValidChoice(resp.Answer, q.Options) {
  return ToolError{
    Code: "INVALID_CHOICE",
    Message: fmt.Sprintf("Expected one of: %v", q.Options),
  }
}
```

### UI Component Errors

```go
// In approval_card.go
case tea.KeyMsg:
  if msg.String() == "enter" {
    if c.selectedIdx < 0 || c.selectedIdx >= len(c.options) {
      c.errorMsg = "Invalid selection"
      return nil
    }
    // Valid selection
    return tea.Send(ApprovalDecisionMsg{...})
  }
```

---

## 7. Configuration Integration

### SDK Config Extension

```json
{
  "version": 1,
  "level": "balanced",
  "userInteraction": {
    "enableApprovals": true,
    "enableQuestions": true,
    "approvalTimeout": 300,
    "approvalBehavior": "stop",
    "questionTimeout": 600,
    "questionBehavior": "useDefault",
    "maxQueueSize": 100,
    "rememberDecisions": true
  }
}
```

### UI Config Extension

```json
{
  "ui": {
    "userInteraction": {
      "showPreview": true,
      "previewMaxLines": 30,
      "defaultDecision": "deny",
      "keyBindings": {
        "selectUp": "up",
        "selectDown": "down",
        "confirm": "enter",
        "cancel": "esc",
        "preview": "p"
      }
    }
  }
}
```

---

## 8. Testing Structure

### Unit Tests

```go
// tests/tools/unit/user_question_test.go
func TestUserQuestionRequest(t *testing.T) { }
func TestUserQuestionResponse(t *testing.T) { }

// tests/tools/unit/question_tool_test.go
func TestQuestionToolExecute(t *testing.T) { }
func TestQuestionToolValidation(t *testing.T) { }

// tests/tools/unit/user_interaction_broker_test.go
func TestBrokerRequestApproval(t *testing.T) { }
func TestBrokerAskQuestion(t *testing.T) { }
```

### Integration Tests

```go
// tests/tools/integration/interaction_test.go
func TestApprovalFlow(t *testing.T) {
  // Create checker with mock broker
  // Simulate permission check
  // Verify broker receives request
  // Simulate user response
  // Verify decision applied
}

func TestQuestionFlow(t *testing.T) {
  // Create tool with broker
  // Execute with question
  // Verify broker receives request
  // Simulate user answer
  // Verify tool returns answer
}

func TestBatchRequests(t *testing.T) {
  // Queue multiple requests
  // Verify ordering
  // Verify timeout handling
}
```

### UI Component Tests

```go
// internal/chat/components/approval_card_test.go
func TestApprovalCardRender(t *testing.T) { }
func TestApprovalCardKeyboard(t *testing.T) { }

// internal/chat/components/question_dialog_test.go
func TestQuestionDialogRender(t *testing.T) { }
func TestQuestionDialogInput(t *testing.T) { }

// internal/chat/components/interaction_center_test.go
func TestInteractionCenterQueue(t *testing.T) { }
func TestInteractionCenterHistory(t *testing.T) { }
```

---

## Summary

This architecture provides:

1. **Clear separation of concerns:**
   - SDK: Logic & request/response handling
   - UI: Display & user interaction
   - Broker: Communication bridge

2. **Extensibility:**
   - New question types easy to add
   - Custom UI implementations possible
   - Alternative brokers (CLI, web) feasible

3. **Testability:**
   - Mock broker for SDK testing
   - Component tests for UI
   - Integration tests for full flow

4. **Maintainability:**
   - Well-defined interfaces
   - Minimal cross-dependencies
   - Clear file organization

5. **Scalability:**
   - Queue-based request handling
   - Timeout protection
   - Batch request support
   - History tracking

---

**Architecture Document**: February 2, 2026
**Status**: Ready for Implementation
**Estimated Effort**: 3-4 weeks (all phases)
