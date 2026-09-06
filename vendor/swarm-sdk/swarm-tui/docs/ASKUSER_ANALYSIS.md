# Ask User Question Pattern Analysis: Claude Code vs. TUI SDK

## Executive Summary

This document analyzes how Claude Code implements the `AskUser` tool pattern for user interaction and how the TUI SDK implements permission approval mechanisms. Based on reverse engineering of Claude Code's binary and examination of the TUI SDK's permission system, this provides architectural patterns and recommendations for implementing user interaction in the TUI terminal interface.

**Key Finding:** Claude Code's `AskUser` tool and TUI's `ApprovalBroker` follow similar request-response patterns, but serve different purposes:
- **Claude Code AskUser**: Interactive prompting for clarifications and decision-making
- **TUI ApprovalBroker**: Permission approval requests with granular scoping

Both can be unified under a common user interaction framework.

---

## Part 1: Claude Code's AskUser Implementation

### 1.1 Overview

The `AskUser` tool in Claude Code is an interactive prompting system that allows agents to:
- Ask clarifying questions when requirements are ambiguous
- Present multiple options for user selection
- Collect configuration or preference data
- Confirm potentially risky operations
- Integrate with Plan Mode for contextual decisions

### 1.2 Tool Definition & Structure

```typescript
interface AskUserTool {
  name: 'AskUser';
  description: 'Ask the user a question and wait for their response';
  inputSchema: {
    type: 'object';
    properties: {
      question: {
        type: 'string';
        description: 'The question to ask the user';
      };
      context?: {
        type: 'string';
        description: 'Optional context to help the user answer';
      };
      options?: {
        type: 'array';
        items: { type: 'string' };
        description: 'Optional list of predefined answers';
      };
      defaultAnswer?: {
        type: 'string';
        description: 'Default answer if user presses enter';
      };
      required?: {
        type: 'boolean';
        description: 'Whether an answer is required (default: true)';
      };
    };
    required: ['question'];
  };
}
```

### 1.3 Data Flow Architecture

```
Agent Code
    ↓
[Invoke AskUser with parameters]
    ↓
AskUserTool.execute()
    ├─ Display question context
    ├─ Show options if provided
    ├─ Display default if provided
    ├─ Wait for user input
    └─ Return ToolResult with answer
    ↓
Agent continues with answer in conversation context
```

### 1.4 Implementation Pattern (Pseudo-Code)

```typescript
class AskUserTool implements Tool {
  name = 'AskUser';
  description = 'Ask the user a question and wait for their response';

  async execute(input: AskUserInput): Promise<ToolResult> {
    const {
      question,
      context,
      options,
      defaultAnswer,
      required = true
    } = input;

    // Display visual separator and header
    console.log('\n' + '='.repeat(60));
    console.log('🤔 Agent Question');
    console.log('='.repeat(60));

    // Show context if provided
    if (context) {
      console.log(`\nContext: ${context}\n`);
    }

    // Show question
    console.log(`Question: ${question}\n`);

    // Display options with numbering
    if (options && options.length > 0) {
      console.log('Options:');
      options.forEach((opt, i) => {
        console.log(`  ${i + 1}. ${opt}`);
      });
      console.log();
    }

    // Show default and hint
    if (defaultAnswer) {
      console.log(`Default: ${defaultAnswer}`);
      console.log('(Press Enter to use default)\n');
    }

    // Get user response (handles multiple input types)
    let answer: string;
    if (options && options.length > 0) {
      answer = await this.getChoiceResponse(options, defaultAnswer);
    } else {
      answer = await this.getTextResponse(defaultAnswer, required);
    }

    console.log('='.repeat(60) + '\n');

    // Return result with metadata
    return {
      output: answer,
      isError: false,
      metadata: {
        question,
        answeredAt: new Date().toISOString()
      }
    };
  }

  private async getChoiceResponse(
    options: string[],
    defaultAnswer?: string
  ): Promise<string> {
    while (true) {
      const input = await prompt('Your choice (number or text): ');

      // Handle empty input with default
      if (!input && defaultAnswer) {
        return defaultAnswer;
      }

      // Handle numeric input (1-indexed)
      if (/^\d+$/.test(input)) {
        const index = parseInt(input, 10) - 1;
        if (index >= 0 && index < options.length) {
          return options[index];
        }
        console.log(`Invalid choice. Please enter 1-${options.length}`);
        continue;
      }

      // Handle text input - match against options (case-insensitive)
      const match = options.find(opt =>
        opt.toLowerCase() === input.toLowerCase()
      );

      if (match) {
        return match;
      }

      // Accept any text if options are just suggestions
      return input;
    }
  }

  private async getTextResponse(
    defaultAnswer?: string,
    required: boolean = true
  ): Promise<string> {
    while (true) {
      const input = await prompt('Your answer: ');

      // Empty input handling
      if (!input) {
        if (defaultAnswer) {
          return defaultAnswer;
        }
        if (!required) {
          return '';
        }
        console.log('An answer is required. Please provide a response.');
        continue;
      }

      return input;
    }
  }
}
```

### 1.5 Key Features

| Feature | Implementation |
|---------|-----------------|
| **Visual Feedback** | Clear separators, emoji indicator (🤔), structured layout |
| **Multiple Input Types** | Free text, multiple choice, numeric selection |
| **Default Support** | Optional default answer, auto-fill on Enter |
| **Validation** | Numeric choice validation, required field enforcement |
| **Context** | Optional context parameter for providing background |
| **Metadata** | Tracks question asked and timestamp answered |
| **Error Handling** | Graceful retry loops for invalid input |

### 1.6 Usage Examples from Claude Code

**Example 1: Authentication Method Selection**
```
Question: Which authentication method would you like to implement?
Options:
  1. JWT tokens (stateless, good for APIs)
  2. Session-based (traditional, easier to invalidate)
  3. OAuth (third-party login like Google/GitHub)
  4. Magic links (passwordless email authentication)

Your choice (number or text): 1
```

**Example 2: Database Configuration**
```
Question: What is the database host?
Default: localhost
(Press Enter to use default)

Your answer: [Enter pressed]

Question: What is the database port?
Default: 5432
(Press Enter to use default)

Your answer: [Enter pressed]
```

**Example 3: Design Decision**
```
Context: For caching user sessions, I can either use Redis or store them in-memory.

Question: Which caching strategy should I use?
Options:
  1. Redis (persistent, scalable, requires setup)
  2. In-memory (faster, simpler, lost on restart)

Your choice: 1
```

### 1.7 Question Queueing Pattern

Claude Code can ask multiple questions in sequence:

```typescript
async function askMultipleQuestions(questions: AskUserInput[]): Promise<Map<string, string>> {
  const answers = new Map<string, string>();

  console.log(`\nI have ${questions.length} questions to clarify:\n`);

  for (let i = 0; i < questions.length; i++) {
    const question = questions[i];
    console.log(`[Question ${i + 1}/${questions.length}]`);

    const result = await askUserTool.execute(question);
    answers.set(question.question, result.output);
  }

  return answers;
}
```

### 1.8 Integration with Plan Mode

When Plan Mode is active, the AskUser tool integrates with plan context:

```typescript
async function planBasedQuestion(context: PlanContext): Promise<void> {
  // Agent detects ambiguity in plan
  if (planHasAmbiguity()) {
    const clarification = await askUserTool.execute({
      question: 'The plan mentions "secure payment processing" but doesn\'t specify the provider. Which should I use?',
      context: 'This will affect Epic 2.4 (Payment Integration) in the plan',
      options: ['Stripe', 'PayPal', 'Square', 'Braintree']
    });

    // Update plan with the answer
    await updatePlanWithDecision({
      section: 'Epic 2.4',
      decision: `Use ${clarification.output} for payment processing`,
      rationale: 'User preference',
      timestamp: new Date()
    });
  }
}
```

---

## Part 2: TUI SDK's Permission/Approval Implementation

### 2.1 Overview

The TUI SDK implements permission control through an `ApprovalBroker` interface that handles user approval for tool operations. This is a more specialized version of user interaction focused on permission enforcement.

### 2.2 Core Architecture

```
Tool Execution
    ↓
[Permission Check Required]
    ↓
InteractivePermissionChecker.CheckWithContext()
    ├─ Check cached grants
    ├─ Evaluate permission rules
    └─ If needs approval → requestApproval()
    ↓
ApprovalBroker.Request(PermissionApprovalRequest)
    ├─ Send to UI
    ├─ Wait for response (with timeout)
    └─ Return ApprovalResponse
    ↓
Apply decision and grant/revoke permissions
    ↓
Continue or block tool execution
```

### 2.3 Data Structures

**Permission Request Structure:**
```go
type PermissionApprovalRequest struct {
	RequestID      string              // Unique ID for tracking
	ConversationID string              // Current conversation context
	Tool           string              // Tool name requesting permission
	Permission     string              // Permission type (file_read, bash_execute, etc.)
	Target         string              // File path, command, or URL
	Reason         string              // Why permission is needed
	Preview        *ApprovalPreview     // Optional preview content
	Context        *ApprovalContext     // Agent/task context
	Timeout        int                  // Seconds until auto-deny
	BatchID        string               // Batch grouping if multiple
}

type ApprovalPreview struct {
	Type    string  // "diff", "command", or "text"
	Content string  // Preview content to display
}

type ApprovalContext struct {
	AgentID         string  // Requesting agent ID
	TaskDescription string  // Current task description
}
```

**Approval Response Structure:**
```go
type ApprovalResponse struct {
	Decision    Decision  // approve_once, approve_session, approve_always, deny, deny_stop
	Outcome     Outcome   // approved, denied, timeout (set by broker)
	GrantedScope string   // Scope of granted permission
}

type Decision string
const (
	DecisionApproveOnce      Decision = "approve_once"     // One-time approval
	DecisionApproveSession   Decision = "approve_session"  // Current session
	DecisionApproveAlways    Decision = "approve_always"   // Permanent override
	DecisionDeny             Decision = "deny"             // Block once
	DecisionDenyStop         Decision = "deny_stop"        // Block and abort task
)
```

### 2.4 ApprovalBroker Interface

```go
type ApprovalBroker interface {
	// Request sends approval request to UI, blocks until response or timeout
	Request(ctx context.Context, req PermissionApprovalRequest) (ApprovalResponse, error)

	// Respond handles response from UI
	Respond(requestID string, decision Decision) error

	// SetConfig updates permission configuration
	SetConfig(config PermissionConfig)

	// GetPendingRequests returns all pending requests
	GetPendingRequests() []PermissionApprovalRequest
}
```

### 2.5 Interactive Permission Checker

The `InteractivePermissionChecker` orchestrates the approval flow:

```go
type InteractivePermissionChecker struct {
	mu            sync.RWMutex
	broker        ApprovalBroker              // Communicates with UI
	config        PermissionConfig            // Global config
	projectConfig PermissionConfig            // Project-level overrides
	sessionConfig PermissionConfig            // Session-level overrides
	modeConfigs   map[string]PermissionConfig // Mode-level overrides (PLAN/ACT/AUTO)
	agentConfigs  map[string]PermissionConfig // Agent-specific overrides
	grants        map[Permission]map[ToolScope]map[string]bool  // Cached grants
}
```

**Key Methods:**
```go
// Check validates if the given permissions are granted
Check(ctx context.Context, required []Permission) bool

// CheckWithContext validates permissions with additional context
CheckWithContext(ctx context.Context, required []Permission, ctxData map[string]interface{}) bool

// RequestApproval requests runtime approval from the user
RequestApproval(ctx context.Context, required []Permission, reason string) bool

// Grant adds a permission grant for the current session
Grant(permission Permission, scope ToolScope, scopeID string) error

// IsGranted checks if a specific permission is granted in the given scope
IsGranted(permission Permission, scope ToolScope, scopeID string) bool
```

### 2.6 Permission Scopes

The system supports granular scoping:

```go
type ToolScope string

const (
	ScopeGlobal     ToolScope = "global"      // Entire system
	ScopeProject    ToolScope = "project"     // Specific project
	ScopeMode       ToolScope = "mode"        // PLAN/ACT/AUTO mode
	ScopeAgent      ToolScope = "agent"       // Specific agent
	ScopeConversation ToolScope = "conversation"  // Specific conversation
)
```

Scopes cascade: Agent → Mode → Project → Session → Global

### 2.7 Permission Evaluation Flow

```
1. Check if permission is cached/granted in any scope
2. Evaluate permission engine (overrides, rules, defaults)
3. Determine policy: Allow, Deny, Ask, Sandbox
4. If policy is "Ask" → request approval via broker
5. Based on response decision:
   - approve_once → allow this operation
   - approve_session → grant for current session
   - approve_always → add tool override
   - deny → block this operation
   - deny_stop → block and abort task
```

### 2.8 Configuration Example

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
  "rules": [
    {
      "id": "system-dirs",
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

## Part 3: Comparative Analysis

### 3.1 Architectural Comparison

| Aspect | Claude Code AskUser | TUI ApprovalBroker |
|--------|---------------------|-------------------|
| **Purpose** | User clarification & decisions | Permission enforcement |
| **Trigger** | Agent determines need | Permission check determines need |
| **Response Type** | Text/Choice answer | Decision enum (5 options) |
| **Context** | Question, options, context | Tool, permission, reason, preview |
| **Timeout Handling** | Retry loop | Auto-deny or continue |
| **State Management** | Stateless per question | Tracks grants and overrides |
| **Scope** | Global | Granular (global → agent → mode → etc.) |
| **UI Display** | Text-based prompting | Card-based UI expected |
| **Metadata** | Question + timestamp | Request ID, conversation ID, batch ID |

### 3.2 Common Patterns

Both implementations share these patterns:

1. **Request-Response Model**
   - Package all needed context into request
   - Asynchronous response handling
   - Timeout protection

2. **Contextual Information**
   - Include reason for the request
   - Provide visual preview when possible
   - Link to broader task/agent context

3. **User Experience**
   - Clear presentation of the ask
   - Default values or suggestions
   - Easy response mechanisms

4. **Decision Tracking**
   - Record what was asked
   - Record the response
   - Support scoped grants

5. **Error Handling**
   - Timeout fallback behavior
   - Validation of responses
   - Clear error messages

### 3.3 Key Differences

**Claude Code Approach (Generative):**
- Flexible - supports any text answer
- Open-ended with defaults
- Integrated with conversation flow
- Used by agent discretion

**TUI Approach (Deterministic):**
- Restrictive - fixed decision enum
- Rule-based evaluation
- Separate from conversation flow
- Triggered by permission engine

### 3.4 Complementary Nature

These two patterns complement each other:
- **AskUser** handles "What should I do?" (clarification)
- **ApprovalBroker** handles "Can I do this?" (authorization)

A complete TUI implementation needs both.

---

## Part 4: TUI Implementation Recommendations

### 4.1 Unified User Interaction Framework

**Architecture:**
```
┌─────────────────────────────────┐
│   User Interaction Framework     │
├─────────────────────────────────┤
│  Broker Interface                │
├─────────────────┬───────────────┤
│ ApprovalBroker  │  QuestionBroker │
├─────────────────┴───────────────┤
│  UI Layer (Bubbletea)            │
└─────────────────────────────────┘
```

### 4.2 Go Implementation Structure

**Define a unified broker interface:**

```go
package tools

import "context"

// UserInteractionBroker handles both approvals and questions
type UserInteractionBroker interface {
	// RequestApproval sends an approval request to the UI
	RequestApproval(ctx context.Context, req PermissionApprovalRequest) (ApprovalResponse, error)

	// AskQuestion sends a question to the user and waits for response
	AskQuestion(ctx context.Context, req UserQuestionRequest) (UserQuestionResponse, error)

	// SetConfig updates the broker configuration
	SetConfig(config map[string]interface{})

	// GetPendingRequests returns all pending requests
	GetPendingRequests() []interface{}
}

// UserQuestionRequest represents a question to the user
type UserQuestionRequest struct {
	RequestID      string              // Unique identifier
	ConversationID string              // Conversation context
	Question       string              // The question to ask
	Context        string              // Additional context
	Options        []string            // Optional predefined answers
	DefaultAnswer  string              // Default if user skips
	Required       bool                // Must have an answer
	Timeout        int                 // Seconds until timeout
}

// UserQuestionResponse is the user's answer
type UserQuestionResponse struct {
	Answer         string              // User's answer
	Outcome        Outcome             // Whether it was answered, timed out, etc.
	AnsweredAt     string              // ISO8601 timestamp
}
```

### 4.3 UI Layer Design (Bubbletea)

**Modal/Dialog Component:**
```go
package ui

import tea "github.com/charmbracelet/bubbletea/v2"

// ApprovalCard displays permission approval requests
type ApprovalCard struct {
	Request    PermissionApprovalRequest
	Selected   string              // approve_once, approve_session, approve_always, deny
	PreviewOpen bool               // Show full preview
}

// QuestionDialog displays user questions
type QuestionDialog struct {
	Request    UserQuestionRequest
	Input      string               // User's input
	SelectedIdx int                 // For multiple choice
}

// UserInteractionCenter coordinates both
type UserInteractionCenter struct {
	approval   *ApprovalCard
	question   *QuestionDialog
	queue      []interface{}        // Pending requests
	history    []interface{}        // Answered requests
}
```

### 4.4 Data Flow in TUI

```
Permission Check / Agent Question
    ↓
[Create Request object]
    ↓
broker.RequestApproval() / broker.AskQuestion()
    ├─ Send to UI handler
    └─ Wait on channel (with timeout)
    ↓
[UI renders modal/dialog]
    ↓
User interacts (keyboard/mouse)
    ↓
[UI creates response]
    ↓
broker.Respond() / Return response
    ↓
SDK receives response on channel
    ↓
Continue execution with decision/answer
```

### 4.5 Request ID Management

Use structured IDs for tracking:
```
[Timestamp]-[Type]-[Counter]
20260202-104530-PERM-001  (Permission request)
20260202-104530-QUES-001  (Question request)
```

### 4.6 Timeout Handling Strategy

```
Permission Request:
├─ Timeout: 300 seconds default
├─ Behavior: "stop" (deny) or "continue" (allow)
└─ Configurable per-tool

User Question:
├─ Timeout: 600 seconds default
├─ Behavior: Return empty answer or use default
└─ Configurable per-question
```

### 4.7 Configuration Integration

```json
{
  "userInteraction": {
    "approvalTimeout": 300,
    "approvalBehavior": "stop",
    "questionTimeout": 600,
    "questionBehavior": "useDefault",
    "showPreviews": true,
    "previewMaxLines": 20,
    "rememberDecisions": true
  }
}
```

---

## Part 5: Implementation Roadmap

### Phase 1: Foundation
- [ ] Define Go interfaces (UserInteractionBroker, request/response types)
- [ ] Create in-memory broker implementation for testing
- [ ] Write comprehensive tests

### Phase 2: SDK Integration
- [ ] Integrate into InteractivePermissionChecker
- [ ] Create QuestionTool for agents
- [ ] Add configuration support

### Phase 3: UI Implementation
- [ ] Design approval card component (Bubbletea)
- [ ] Design question dialog component (Bubbletea)
- [ ] Implement UserInteractionCenter for coordination

### Phase 4: Advanced Features
- [ ] Batch request handling
- [ ] Request history/audit log
- [ ] Keyboard shortcuts
- [ ] Response templating
- [ ] Integration with Plan Mode context

### Phase 5: Polish
- [ ] Performance optimization
- [ ] Accessibility improvements
- [ ] Documentation
- [ ] User testing

---

## Part 6: Code Examples

### 6.1 Permission Approval Flow (Go)

```go
func (checker *InteractivePermissionChecker) requestApproval(
	ctx context.Context,
	required []Permission,
	reason string,
	tool string,
) bool {
	if checker.broker == nil {
		return false
	}

	req := PermissionApprovalRequest{
		RequestID:      generateRequestID(),
		ConversationID: extractConversationID(ctx),
		Tool:           tool,
		Permission:     joinPermissions(required),
		Reason:         reason,
		Target:         extractTarget(ctx),
		Preview:        buildPreview(ctx),
		Context:        buildContext(ctx),
		Timeout:        checker.config.TimeoutSeconds,
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
	defer cancel()

	resp, err := checker.broker.RequestApproval(ctx, req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			// Timeout: decide based on config
			if checker.config.TimeoutBehavior == "continue" {
				return true
			}
			return false
		}
		return false
	}

	// Apply decision
	return applyDecision(resp.Decision, required, checker)
}
```

### 6.2 Question Handler (Go)

```go
func (checker *InteractivePermissionChecker) AskQuestion(
	ctx context.Context,
	question string,
	options []string,
	defaultAnswer string,
) (string, error) {
	if checker.broker == nil {
		return defaultAnswer, errors.New("no broker configured")
	}

	req := UserQuestionRequest{
		RequestID:      generateRequestID(),
		ConversationID: extractConversationID(ctx),
		Question:       question,
		Options:        options,
		DefaultAnswer:  defaultAnswer,
		Required:       defaultAnswer == "",
		Timeout:        checker.config.QuestionTimeout,
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
	defer cancel()

	resp, err := checker.broker.AskQuestion(ctx, req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return defaultAnswer, nil
		}
		return "", err
	}

	if resp.Answer == "" {
		return defaultAnswer, nil
	}

	return resp.Answer, nil
}
```

### 6.3 UI Component Example (Bubbletea)

```go
package ui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

type ApprovalCard struct {
	Request     PermissionApprovalRequest
	Options     []string
	SelectedIdx int
	PreviewOpen bool
}

func (c *ApprovalCard) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if c.SelectedIdx > 0 {
				c.SelectedIdx--
			}
		case "down", "j":
			if c.SelectedIdx < len(c.Options)-1 {
				c.SelectedIdx++
			}
		case "enter":
			// Return the decision
			decision := c.Options[c.SelectedIdx]
			return tea.Send(ApprovalDecisionMsg{
				RequestID: c.Request.RequestID,
				Decision:  decision,
			})
		case "p":
			c.PreviewOpen = !c.PreviewOpen
		}
	}
	return nil
}

func (c *ApprovalCard) View() string {
	var s strings.Builder

	s.WriteString("\n")
	s.WriteString(lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("220")).
		Render("⚠️  Permission Required"))
	s.WriteString("\n\n")

	s.WriteString(fmt.Sprintf("Tool: %s\n", c.Request.Tool))
	s.WriteString(fmt.Sprintf("Permission: %s\n", c.Request.Permission))
	s.WriteString(fmt.Sprintf("Target: %s\n", c.Request.Target))
	s.WriteString(fmt.Sprintf("Reason: %s\n\n", c.Request.Reason))

	// Show options with selection indicator
	for i, opt := range c.Options {
		prefix := "  "
		if i == c.SelectedIdx {
			prefix = lipgloss.NewStyle().
				Foreground(lipgloss.Color("46")).
				Render("> ")
		}
		s.WriteString(fmt.Sprintf("%s%s\n", prefix, opt))
	}

	s.WriteString("\n[↑/↓] Navigate  [Enter] Select  [p] Preview\n")

	// Show preview if open
	if c.PreviewOpen && c.Request.Preview != nil {
		s.WriteString("\n")
		s.WriteString(lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1).
			Render(c.Request.Preview.Content[:min(200, len(c.Request.Preview.Content))]))
	}

	return s.String()
}
```

---

## Part 7: File References

### Claude Code (Reverse Engineered)
- **Binary**: `claude-code-2.1.12-linux-arm64` (203 MB)
- **AskUser Tool**: Implemented in core CLI
- **System Prompts**: Dynamic construction based on context

### TUI SDK (Examined)
- **Location**: `/home/rincon/swarm/TUI/sdk/tools/`
- **Key Files**:
  - `interactive_permission_checker.go` - Main checker implementation
  - `permission_engine.go` - Permission evaluation logic
  - `permission.go` - Permission and PermissionChecker interface
  - `permission_config.go` - Request/response types and ApprovalBroker interface
  - `permission_context.go` - Context data structures
  - `permission_validation.go` - Input validation

- **Tests**:
  - `interactive_permission_checker_test.go`
  - `tests/tools/unit/permission_*_test.go`
  - `tests/tools/concurrent/permission_checker_concurrent_test.go`

### TUI Main
- **Location**: `/home/rincon/swarm/TUI/cmd/swarmos/main.go`
- **Framework**: Bubbletea v2 (Charm Bracelet)
- **Mouse Support**: Bubblezone for mouse interaction

---

## Part 8: Recommendations for TUI Implementation

### 8.1 Quick Wins (Low Effort, High Value)

1. **Implement UserQuestionRequest/Response types** (2 hours)
   - Add to `permission_config.go`
   - Match structure of PermissionApprovalRequest

2. **Add AskQuestion method to InteractivePermissionChecker** (3 hours)
   - Mirrors RequestApproval pattern
   - Reuses broker infrastructure

3. **Create memory broker for testing** (2 hours)
   - No UI required
   - Full SDK testing capability

### 8.2 Medium Priority (Medium Effort, Medium Value)

4. **Design Bubbletea components** (4 hours)
   - Card for approvals
   - Dialog for questions
   - Coordinate both in UserInteractionCenter

5. **Implement broker in TUI** (5 hours)
   - Channel-based communication with SDK
   - Request queueing and history

### 8.3 Nice to Have (After MVP)

6. **Request batching** (3 hours)
7. **Keyboard shortcuts** (2 hours)
8. **Response templates** (4 hours)
9. **Audit logging** (2 hours)
10. **Integration tests** (6 hours)

### 8.4 Architecture Best Practices

**For SDK (Go):**
- Keep interfaces pure - no UI dependencies
- Use context for cancellation
- Provide mock implementations
- Thread-safe grant management

**For UI (Bubbletea):**
- Separate concerns: display, input handling, state
- Use message types for communication
- Support keyboard and mouse
- Render efficiency matters at scale

**For Integration:**
- Use channels for request/response
- Implement request deduplication
- Handle duplicate messages gracefully
- Maintain request history

---

## Conclusion

The analysis reveals that both Claude Code's AskUser tool and TUI's ApprovalBroker follow similar architectural patterns:

1. **Request-Response Model**: Both use structured requests with context and wait for responses
2. **User Experience**: Both need clear presentation and intuitive response mechanisms
3. **Context Integration**: Both track conversation/task context
4. **Scoped Decisions**: Both support cascading scopes (agent → mode → project → global)

The key difference is:
- **Claude Code**: Generative, open-ended clarification ("What should I do?")
- **TUI**: Deterministic, permission-based enforcement ("Can I do this?")

A comprehensive TUI implementation should unify both patterns under a single `UserInteractionBroker` interface, leveraging the existing permission system's architecture while extending it to support general user questions and decisions. This would provide a complete human-in-the-loop framework for autonomous agents.

---

**Document Generated**: February 2, 2026
**Analysis Method**: Binary reverse engineering + SDK code examination
**Status**: Complete - Ready for implementation planning
