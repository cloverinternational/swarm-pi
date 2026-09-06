# AskUser Pattern - Quick Reference Guide

## 1. Data Flow Comparison

### Claude Code - AskUser Flow
```
Agent Logic
    ↓
askUserTool.execute({
  question: "Which auth method?",
  options: ["JWT", "Sessions", "OAuth"],
  defaultAnswer: "JWT"
})
    ↓
Display question in terminal
    ↓
User enters: "1" or "JWT"
    ↓
Validate & return answer
    ↓
Answer in conversation context
    ↓
Agent uses answer to make decisions
```

### TUI SDK - ApprovalBroker Flow
```
Tool Execution
    ↓
permissionChecker.CheckWithContext({
  tool: "bash",
  permission: "bash_execute",
  ctxData: { command: "rm -rf /" }
})
    ↓
Evaluate rules/overrides/defaults
    ↓
Decision: "ask" required
    ↓
broker.RequestApproval({
  Tool: "bash",
  Permission: "bash_execute",
  Reason: "Dangerous command detected",
  Target: "rm -rf /",
  Preview: { Type: "command", Content: "rm -rf /" }
})
    ↓
Send to UI, wait for response
    ↓
User selects: "approve_once" / "deny" / "approve_always"
    ↓
Apply decision (grant/revoke/override)
    ↓
Continue or block tool execution
```

---

## 2. Request/Response Structures

### Claude Code AskUser

**Request (Tool Input):**
```json
{
  "question": "Which caching strategy?",
  "context": "For user session storage",
  "options": ["Redis", "In-memory"],
  "defaultAnswer": "Redis",
  "required": true
}
```

**Response (Tool Output):**
```json
{
  "output": "Redis",
  "isError": false,
  "metadata": {
    "question": "Which caching strategy?",
    "answeredAt": "2026-02-02T10:45:30Z"
  }
}
```

### TUI ApprovalBroker

**Request (Go struct):**
```go
PermissionApprovalRequest{
  RequestID:      "20260202-104530-PERM-001",
  ConversationID: "conv_123456",
  Tool:           "bash",
  Permission:     "bash_execute",
  Target:         "rm -rf /",
  Reason:         "Dangerous command detected",
  Preview: &ApprovalPreview{
    Type:    "command",
    Content: "rm -rf /",
  },
  Context: &ApprovalContext{
    AgentID:         "agent_main",
    TaskDescription: "Cleanup old files",
  },
  Timeout: 300,
  BatchID: "",
}
```

**Response (Go struct):**
```go
ApprovalResponse{
  Decision:    "approve_once",  // or approve_session, approve_always, deny, deny_stop
  Outcome:     "approved",       // or denied, timeout
  GrantedScope: "global",
}
```

---

## 3. Permission Levels & Policies

### TUI Permission Levels
```
LevelAlwaysAsk   - Every tool needs approval (most restrictive)
LevelBalanced    - First-time and dangerous actions need approval
LevelPermissive  - Only dangerous actions need approval
```

### TUI Permission Policies
```
PolicyAllow    - Grant permission automatically
PolicyDeny     - Deny permission automatically
PolicyAsk      - Request user approval
PolicySandbox  - Allow with restrictions (e.g., path limits)
```

### TUI Decision Outcomes
```
DecisionApproveOnce    - Allow this operation once
DecisionApproveSession - Grant for current session
DecisionApproveAlways  - Add permanent tool override
DecisionDeny           - Block this operation
DecisionDenyStop       - Block and abort current task
```

---

## 4. Key Classes & Methods

### Claude Code AskUserTool

| Method | Purpose | Example |
|--------|---------|---------|
| `execute(input)` | Ask question and wait for answer | Returns ToolResult with answer |
| `getChoiceResponse(options, default)` | Handle multiple-choice input | Validates 1-N or text match |
| `getTextResponse(default, required)` | Handle free-form input | Validates non-empty if required |

### TUI InteractivePermissionChecker

| Method | Purpose | Signature |
|--------|---------|-----------|
| `Check()` | Simple permission check | `Check(ctx, permissions) bool` |
| `CheckWithContext()` | Check with context data | `CheckWithContext(ctx, permissions, ctxData) bool` |
| `RequestApproval()` | Request user approval | `RequestApproval(ctx, permissions, reason) bool` |
| `Grant()` | Add a permission grant | `Grant(permission, scope, scopeID) error` |
| `IsGranted()` | Check if granted | `IsGranted(permission, scope, scopeID) bool` |

### TUI ApprovalBroker Interface

| Method | Purpose | Returns |
|--------|---------|---------|
| `Request()` | Send approval request to UI | `ApprovalResponse, error` |
| `Respond()` | Receive response from UI | `error` |
| `SetConfig()` | Update configuration | `void` |
| `GetPendingRequests()` | Get all pending requests | `[]PermissionApprovalRequest` |

---

## 5. Scope Hierarchy (TUI)

```
Scope Hierarchy (highest to lowest priority):
Agent-specific      (e.g., agent_main)
    ↓
Mode-specific       (e.g., PLAN, ACT, AUTO)
    ↓
Project-specific    (e.g., project_abc)
    ↓
Session             (e.g., current session)
    ↓
Global              (e.g., system-wide)
```

**Example:**
- User denies file_write in mode "ACT"
- ACT mode config: `file_write: "deny"`
- Other modes still allow file_write

---

## 6. Configuration Examples

### Claude Code (Implicit)
```typescript
// Built into system prompt and tool definitions
// No explicit configuration file
// Behavior determined by agent logic
```

### TUI Configuration
```json
{
  "version": 1,
  "level": "balanced",
  "timeoutSeconds": 300,
  "timeoutBehavior": "stop",
  "defaults": {
    "policies": {
      "file_read": "allow",
      "file_write": "ask",
      "bash_execute": "ask"
    }
  },
  "overrides": {
    "tools": {
      "read": "always_allow",
      "bash": "always_ask"
    },
    "permissions": {
      "file_write": "always_ask"
    }
  },
  "rules": [
    {
      "id": "protect-system",
      "priority": 100,
      "when": {
        "operation": "write",
        "path": "/etc/**"
      },
      "then": {
        "policy": "deny",
        "reason": "System directories protected"
      }
    }
  ]
}
```

---

## 7. UI Display Patterns

### Claude Code Output Format

```
═══════════════════════════════════════════════════════
🤔 Agent Question
═══════════════════════════════════════════════════════

Context: For implementing user authentication

Question: Which authentication method would you prefer?

Options:
  1. JWT tokens (stateless, scalable)
  2. Sessions (traditional, easier to revoke)
  3. OAuth (third-party providers)
  4. Magic links (passwordless)

Your choice (number or text):
```

### TUI Approval Display (Expected)

```
┌─────────────────────────────────────────┐
│ ⚠️  Permission Required                  │
├─────────────────────────────────────────┤
│ Tool: bash                               │
│ Permission: bash_execute                 │
│ Target: rm -rf /                         │
│ Reason: Dangerous command detected       │
│                                          │
│ > Approve Once                          │
│   Approve Session                       │
│   Approve Always                        │
│   Deny                                  │
│                                          │
│ [↑/↓] Navigate  [Enter] Select [p] More │
└─────────────────────────────────────────┘
```

---

## 8. Implementation Checklist

### Phase 1: Foundation (Go SDK)
- [ ] Add UserQuestionRequest struct
- [ ] Add UserQuestionResponse struct
- [ ] Define QuestionBroker interface
- [ ] Implement mock broker for testing
- [ ] Write unit tests

### Phase 2: Permission Integration
- [ ] Extend InteractivePermissionChecker
- [ ] Add AskQuestion method
- [ ] Add question timeout handling
- [ ] Integrate with approval flow

### Phase 3: UI (Bubbletea)
- [ ] Design ApprovalCard component
- [ ] Design QuestionDialog component
- [ ] Implement UserInteractionCenter
- [ ] Add keyboard/mouse handling
- [ ] Add to main chat UI

### Phase 4: Integration
- [ ] Connect SDK broker to UI
- [ ] Add request history tracking
- [ ] Add configuration UI
- [ ] Test end-to-end flows

### Phase 5: Polish
- [ ] Performance testing
- [ ] Accessibility review
- [ ] Documentation
- [ ] User acceptance testing

---

## 9. Common Patterns to Implement

### Pattern 1: Multi-Question Sequence
```go
questions := []UserQuestionRequest{
  {Question: "What framework?", Options: ["React", "Vue", "Svelte"]},
  {Question: "What build tool?", Options: ["Vite", "Webpack", "esbuild"]},
  {Question: "What testing?", Options: ["Jest", "Vitest", "Mocha"]},
}

for _, q := range questions {
  answer, _ := broker.AskQuestion(ctx, q)
  answers = append(answers, answer)
}
```

### Pattern 2: Contextual Approval
```go
req := PermissionApprovalRequest{
  Tool: "bash",
  Permission: "bash_execute",
  Target: "npm install",
  Context: &ApprovalContext{
    TaskDescription: "Installing project dependencies from package.json",
  },
  Preview: &ApprovalPreview{
    Type: "command",
    Content: "npm install --save-dev @types/node",
  },
}

resp, _ := broker.RequestApproval(ctx, req)
```

### Pattern 3: Scoped Permissions
```go
// Allow in PLAN mode
checker.SetModeConfig("PLAN", PermissionConfig{
  Overrides: PermissionOverrides{
    Permissions: map[Permission]OverridePolicy{
      PermissionFileRead: OverrideAlwaysAllow,
    },
  },
})

// Require approval in ACT mode
checker.SetModeConfig("ACT", PermissionConfig{
  Overrides: PermissionOverrides{
    Permissions: map[Permission]OverridePolicy{
      PermissionBashExecute: OverrideAlwaysAsk,
    },
  },
})
```

---

## 10. File Structure Reference

```
TUI SDK:
  /sdk/tools/
    ├── permission.go                    # Permission constants & interfaces
    ├── permission_config.go             # Config, Request/Response structs
    ├── permission_engine.go             # Evaluation logic
    ├── interactive_permission_checker.go # Main checker implementation
    ├── permission_validation.go         # Input validation
    └── tests/
        └── unit/
            ├── permission_engine_test.go
            └── permission_checker_concurrent_test.go

TUI CLI:
  /cmd/swarmos/
    └── main.go                         # Bubbletea TUI entry point

TUI Internal:
  /internal/chat/
    ├── components/
    │   ├── approval_card.go            # [TODO] Approval UI
    │   └── question_dialog.go          # [TODO] Question UI
    └── interaction.go                  # [TODO] Broker implementation
```

---

## 11. Quick Tips

### For Claude Code Pattern
- Questions are **generative** - accept any text answer
- Use **options** to guide without restricting
- Provide **context** for better answers
- Include **defaults** to speed up responses
- Support **batching** for multiple questions

### For TUI Pattern
- Permissions are **deterministic** - fixed decision set
- Use **rules** for complex logic
- Leverage **scopes** for granular control
- Implement **timeout** behavior clearly
- Cache **grants** to avoid re-asking

### For Unified Approach
- Both use **request-response** model
- Both include **context** for decision-making
- Both track **identity** (request ID, agent ID)
- Both support **timeouts**
- Both can **batch** requests

---

## 12. Testing Strategy

### Unit Tests (SDK)
```go
func TestApprovalRequest(t *testing.T) {
  // Create request
  // Mock broker
  // Verify request structure
  // Verify timeout handling
}

func TestQuestionResponse(t *testing.T) {
  // Create question
  // Mock broker
  // Test answer validation
  // Test timeout behavior
}
```

### Integration Tests (UI)
```go
func TestApprovalCardUI(t *testing.T) {
  // Create component
  // Send keyboard input
  // Verify rendered output
  // Verify decision message sent
}
```

### E2E Tests
```go
func TestCompleteFlow(t *testing.T) {
  // Create SDK instance with broker
  // Create UI with broker
  // Simulate user approving/denying
  // Verify SDK receives response
  // Verify tool execution continues/stops
}
```

---

**Quick Reference Created**: February 2, 2026
**For**: TUI Implementation Planning
