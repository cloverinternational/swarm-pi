package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Compile-time interface satisfaction checks.
var (
	_ tools.Tool = (*EnterPlanModeTool)(nil)
	_ tools.Tool = (*ExitPlanModeTool)(nil)
)

// ─── EnterPlanModeTool ────────────────────────────────────────────────────────

// EnterPlanModeTool signals the UI to enter Plan Mode.
// When called, the UI shows a "PLAN MODE" indicator and the agent is instructed
// to explore and plan before implementing.
//
// Tool name: enter_plan_mode
type EnterPlanModeTool struct {
	broker PlanBroker
	cfg    Config
}

// NewEnterPlanModeTool creates a new enter_plan_mode tool.
func NewEnterPlanModeTool(broker PlanBroker, cfg Config) *EnterPlanModeTool {
	return &EnterPlanModeTool{broker: broker, cfg: cfg}
}

func (t *EnterPlanModeTool) Name() string { return "enter_plan_mode" }

func (t *EnterPlanModeTool) Description() string {
	return `Switch to plan mode to design an approach before implementing.

Use this tool when you receive a complex task that requires careful upfront design. In plan mode you should:
1. Explore the codebase to understand the relevant context
2. Resolve the decision tree with the user — ask one question at a time with your recommended answer
3. Write your plan to a text or Markdown file inside the active workspace
4. Call exit_plan_mode when ready for user approval

IMPORTANT: In plan mode, you must resolve requirements with the user BEFORE writing the plan.
Walk the decision tree branch-by-branch. For each unresolved decision:
1. Ask one question at a time via ask_user_question
2. Include your recommended answer (based on codebase exploration)
3. Wait for user response before moving to the next decision
4. If the answer can be found in the codebase, explore it yourself instead of asking

Plan mode is an approval ceremony, not a permission boundary. Normal tool permissions,
workspace limits, and safety controls continue to apply.`
}

func (t *EnterPlanModeTool) Parameters() any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"required":             []string{},
		"additionalProperties": false,
	}
}

func (t *EnterPlanModeTool) Validate(_ map[string]any) error { return nil }

func (t *EnterPlanModeTool) IsIdempotent() bool                     { return false }
func (t *EnterPlanModeTool) RequiresPermission() []tools.Permission { return nil }
func (t *EnterPlanModeTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}
func (t *EnterPlanModeTool) OptimizationHints() *tools.OptimizationHints { return nil }

// Execute notifies the broker that the agent wants to enter plan mode.
func (t *EnterPlanModeTool) Execute(ctx context.Context, _ map[string]any) (*tools.ToolResult, error) {
	if t.broker != nil {
		if err := t.broker.EnterPlanMode(ctx); err != nil {
			builtin.PlanModeExited()
			return tools.NewErrorResult(fmt.Errorf("enter_plan_mode: broker notification failed: %w", err)), nil
		}
	}

	msg := strings.Join([]string{
		"# PLAN MODE — ACTIVE",
		"",
		"You are now in PLAN MODE. Explore, resolve the decision tree with the user, write a local plan, then submit it for approval.",
		"",
		"## CEREMONY",
		"- Plan mode guides planning and approval; it does not change normal tool permissions",
		"- Ordinary workspace, task, credential, and safety controls still apply",
		fmt.Sprintf("- Keep the plan file inside the active workspace: %s", t.cfg.workspaceDir()),
		"",
		"## WORKFLOW",
		"1. Explore the relevant code and constraints",
		"- INTERROGATE: Resolve the decision tree with the user before writing the plan",
		"  - Ask one question at a time with your recommended answer",
		"  - If the codebase can answer it, explore it yourself instead of asking",
		"2. Write the agreed plan to a meaningful local Markdown or text file",
		"3. Call exit_plan_mode with plan_file set to that workspace-local path",
		"",
		"## CALLING exit_plan_mode",
		"- Recommended: pass plan_file with a relative or absolute workspace-local path",
		"- Compatibility: pass plan content directly in the plan parameter",
		"- The user approves/edits/rejects your plan before you implement",
	}, "\n")

	return tools.NewToolResult(msg), nil
}

// ─── ExitPlanModeTool ─────────────────────────────────────────────────────────

// ExitPlanModeTool submits the agent's plan for user approval.
// It blocks until the user approves, rejects, or the context is cancelled.
//
// Tool name: exit_plan_mode
type ExitPlanModeTool struct {
	broker PlanBroker
	cfg    Config
}

// NewExitPlanModeTool creates a new exit_plan_mode tool.
func NewExitPlanModeTool(broker PlanBroker, cfg Config) *ExitPlanModeTool {
	return &ExitPlanModeTool{broker: broker, cfg: cfg}
}

func (t *ExitPlanModeTool) Name() string { return "exit_plan_mode" }

func (t *ExitPlanModeTool) Description() string {
	return `Present your plan for user approval and exit plan mode.

RECOMMENDED: Write your plan to a text or Markdown file inside the active workspace,
then call exit_plan_mode with plan_file set to that file's path. The file contents
are copied into conversation storage before approval.

Alternatively, pass the full plan content directly in the plan parameter.

The user will review and can:
- Approve (y): you begin implementing
- Approve + compact context (c): context cleared, then implement
- Reject with feedback (n): revise your plan and call exit_plan_mode again

This tool BLOCKS until the user responds - do not set a timeout.
Only call after enter_plan_mode.`
}
func (t *ExitPlanModeTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan": map[string]any{
				"type": "string",
				"description": "The complete implementation plan in Markdown format. " +
					"Use either plan or plan_file, not both.",
			},
			"plan_file": map[string]any{
				"type": "string",
				"description": "Path to a UTF-8 text or Markdown plan inside the active workspace. " +
					"Relative paths resolve from the workspace root. Use either plan_file or plan, not both.",
			},
		},
		"required":             []string{},
		"additionalProperties": false,
	}
}

func (t *ExitPlanModeTool) Validate(params map[string]any) error {
	planText, planTextOK := params["plan"].(string)
	if _, present := params["plan"]; present && !planTextOK {
		return fmt.Errorf("plan must be a string")
	}
	planFile, planFileOK := params["plan_file"].(string)
	if _, present := params["plan_file"]; present && !planFileOK {
		return fmt.Errorf("plan_file must be a string")
	}
	if strings.TrimSpace(planText) != "" && strings.TrimSpace(planFile) != "" {
		return fmt.Errorf("provide either plan or plan_file, not both")
	}
	return nil
}

func (t *ExitPlanModeTool) IsIdempotent() bool                     { return false }
func (t *ExitPlanModeTool) RequiresPermission() []tools.Permission { return nil }
func (t *ExitPlanModeTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}
func (t *ExitPlanModeTool) OptimizationHints() *tools.OptimizationHints { return nil }

// Execute submits the plan for user review, blocking until a response is received.
func (t *ExitPlanModeTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if err := t.Validate(params); err != nil {
		return tools.NewErrorResult(fmt.Errorf("exit_plan_mode: %w", err)), nil
	}

	planText, _ := params["plan"].(string)
	planText = strings.TrimSpace(planText)
	planFile, _ := params["plan_file"].(string)
	planFile = strings.TrimSpace(planFile)

	if planFile != "" {
		filePlan, err := ReadSubmittedPlanFile(t.cfg, planFile)
		if err != nil {
			return tools.NewErrorResult(fmt.Errorf("exit_plan_mode: %w", err)), nil
		}
		planText = filePlan
	}

	// Compatibility fallback for old clients and already-running sessions that
	// wrote directly to the canonical session file.
	if planText == "" {
		filePlan, err := ReadPlanFile(t.cfg)
		if err == nil && filePlan != "" {
			planText = filePlan
		}
	}

	if planText == "" {
		return tools.NewErrorResult(fmt.Errorf(
			"exit_plan_mode: no plan found. Provide plan_file for a workspace-local text file "+
				"or provide the plan parameter directly. Legacy canonical path checked: %s.",
			PlanFilePath(t.cfg))), nil
	}
	planText, err := ValidatePlanContent(planText, "plan")
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("exit_plan_mode: %w", err)), nil
	}

	// Persist the exact content the user is about to review.
	if err := WritePlanFile(t.cfg, planText); err != nil {
		return tools.NewErrorResult(fmt.Errorf("exit_plan_mode: copy plan to conversation storage: %w", err)), nil
	}

	// Headless / no broker: auto-approve
	if t.broker == nil {
		builtin.PlanModeExited()
		result := map[string]any{
			"approved":      true,
			"edited_plan":   planText,
			"clear_context": false,
			"message":       "Plan approved (headless mode). Proceed with implementation.",
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return tools.NewToolResult(string(out)), nil
	}

	// Preserve owner/session values while detaching ordinary tool deadlines:
	// plan approval is interactive and may take minutes.
	resp, err := t.broker.RequestPlanApproval(context.WithoutCancel(ctx), planText)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("exit_plan_mode: approval request failed: %w", err)), nil
	}

	if !resp.Approved {
		feedback := resp.Feedback
		if feedback == "" {
			feedback = "User rejected the plan. Please revise your approach."
		}
		msg := fmt.Sprintf("Plan rejected.\n\nFeedback: %s\n\nRevise your plan and call exit_plan_mode again.", feedback)
		result := map[string]any{
			"approved": false,
			"feedback": feedback,
			"message":  msg,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return tools.NewToolResult(string(out)), nil
	}

	// Plan approved. Persist user-edited content as the canonical final plan.
	finalPlan := planText
	if resp.EditedPlan != "" && resp.EditedPlan != planText {
		finalPlan, err = ValidatePlanContent(resp.EditedPlan, "edited plan")
		if err != nil {
			return tools.NewErrorResult(fmt.Errorf("exit_plan_mode: %w", err)), nil
		}
		if err := WritePlanFile(t.cfg, finalPlan); err != nil {
			return tools.NewErrorResult(fmt.Errorf("exit_plan_mode: persist approved plan: %w", err)), nil
		}
	}
	builtin.PlanModeExited()

	result := map[string]any{
		"approved":      true,
		"edited_plan":   finalPlan,
		"clear_context": resp.ClearContext,
		"message":       "Plan approved. You may now begin implementing. Follow the approved plan precisely.",
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return tools.NewToolResult(string(out)), nil
}
