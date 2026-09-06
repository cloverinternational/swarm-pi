package plan

import (
	"context"
	"fmt"
)

// PlanModeSystemPrompt returns the system prompt injection to prepend when
// the agent is in plan mode. This mirrors the plan mode instructions that
// Claude Code injects into the system prompt when plan mode is active.
func PlanModeSystemPrompt(cfg Config) string {
	return fmt.Sprintf(`## Plan Mode - ACTIVE

You are in PLAN MODE. This is an approval ceremony for exploring a change,
resolving decisions, and presenting a concrete implementation plan.

Plan mode does not change tool authorization. Normal workspace, task, credential,
permission, and safety controls continue to apply.

### Your Only Job Right Now
Explore 	 Resolve decisions with the user 	 Document your plan 	 Call exit_plan_mode for approval.

### Requirement Discovery (MANDATORY)

Before writing your plan, you MUST resolve the decision tree with the user:

1. **Identify the decision tree**: From your exploration, list every decision that needs to be made, noting which decisions depend on others.
2. **Walk the tree depth-first**: Start with the most upstream decision. Resolve it before moving to decisions that depend on it.
3. **Ask one question at a time**: Use ask_user_question for each unresolved decision. Never batch multiple questions.
4. **Provide your recommended answer**: For each question, include your recommendation based on what you found in the codebase. The user reviews your draft — they don't write from scratch.
5. **Self-service where possible**: If a question can be answered by exploring the codebase, explore it instead of asking the user. Only ask the user questions that require their judgment.

A decision is "resolved" when you and the user agree on the answer. Do not write the plan until all critical decisions are resolved.

When all decisions are resolved, you have reached "shared understanding" — now write the plan.

### Plan File Workflow (IMPORTANT)
1. Explore the relevant code and resolve material decisions first
2. Write the agreed plan to a meaningful text or Markdown file inside the workspace
3. Call exit_plan_mode with plan_file set to that file's path
4. The tool copies the submitted content into conversation storage before approval

Workspace root: %s

### What to Include in Your Plan
- **Overview**: What is being built/changed and why
- **Files to modify**: Specific files and the changes needed
- **Implementation steps**: Ordered, concrete actions
- **Risks/considerations**: Edge cases, breaking changes, tests needed

### Visual Decisions During Planning

For UI / layout / design / architectural trade-offs, prefer ask_user_question with
type="visual_choice" over describing options in prose. When the plan itself contains
mermaid code fences or architecture diagrams, they will render natively in the user's
browser on plan approval — write them as fenced code blocks with language "mermaid".

### Tool Behavior
Plan mode itself neither grants nor removes tool permissions. Use tools according
to their ordinary schemas and the active workspace, task, permission, and safety
policies.

---
`, cfg.workspaceDir())
}

// NoopPlanBroker is a PlanBroker that auto-approves all plans.
// Useful for headless/testing environments where no UI is attached.
type NoopPlanBroker struct{}

var _ PlanBroker = (*NoopPlanBroker)(nil)

// EnterPlanMode is a no-op in headless mode.
func (n *NoopPlanBroker) EnterPlanMode(_ context.Context) error {
	return nil
}

// RequestPlanApproval auto-approves in headless mode.
func (n *NoopPlanBroker) RequestPlanApproval(_ context.Context, plan string) (ApprovalResponse, error) {
	return ApprovalResponse{
		Approved:     true,
		EditedPlan:   plan,
		ClearContext: false,
	}, nil
}
