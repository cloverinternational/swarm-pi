package chat

import (
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plan"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// ============================================================================
// PLAN MODE – BubbleTea update handlers
//
// Plan Mode follows the same lifecycle as Dream but is interactive:
//
//   Agent calls enter_plan_mode
//         │
//         ▼  planEnterMsg
//   TUI sets inPlanMode=true, shows PLAN badge
//         │
//   Agent explores, resolves decisions, and writes a workspace-local plan …
//         │
//   Agent calls exit_plan_mode with plan content
//         │
//         ▼  planApprovalRequestMsg
//   TUI adds plan to chat as a scrollable assistant message
//   TUI shows plan question bar (planQuestionModal) at bottom
//         │
//   User presses Y / C / N in the bar
//         │
//         ├── Y → planApprovedMsg{ClearContext:false}
//         ├── C → planApprovedMsg{ClearContext:true}
//         └── N → text input bar → planRejectedMsg{Feedback:"…"}
//                        │
//         PlanBroker.Respond*() unblocks the tool goroutine
// ============================================================================

// createPlanChoiceModal sets up the plan approval choice bar (y / c / n).
// It can be called again to restore the choice bar after cancelling a
// rejection feedback input.
func (a *App) createPlanChoiceModal() {
	req := interaction.QuestionRequest{
		ID:       "plan-approval",
		Question: "Review the plan above and choose an action:",
		Type:     interaction.QuestionTypeChoice,
		Choices: []string{
			"y: Approve",
			"c: Approve + Clear Context",
			"n: Reject / Revise",
		},
		Default: "y: Approve",
		Metadata: interaction.QuestionMetadata{
			Title:    "Plan Ready for Review",
			Priority: "high",
		},
	}

	onSubmit := func(resp interaction.QuestionResponse) {
		switch resp.Answer {
		case "y: Approve":
			plan := a.planContent
			a.planQuestionModal = nil
			a.pendingAnimCmd = func() tea.Msg {
				return planApprovedMsg{Plan: plan, ClearContext: false}
			}
		case "c: Approve + Clear Context":
			plan := a.planContent
			a.planQuestionModal = nil
			a.pendingAnimCmd = func() tea.Msg {
				return planApprovedMsg{Plan: plan, ClearContext: true}
			}
		case "n: Reject / Revise":
			// Switch to a text input bar for rejection feedback.
			a.createPlanRejectionModal()
		}
	}

	onCancel := func() {
		// Esc on the choice bar – treat as rejection with no feedback.
		a.planQuestionModal = nil
		a.pendingAnimCmd = func() tea.Msg {
			return planRejectedMsg{Feedback: "Please revise the plan."}
		}
	}

	a.planQuestionModal = NewQuestionModal(req, 1, 1, onSubmit, onCancel)
}

// createPlanRejectionModal replaces the choice bar with a text input bar
// so the user can type rejection feedback before confirming.
func (a *App) createPlanRejectionModal() {
	textReq := interaction.QuestionRequest{
		ID:       "plan-rejection-feedback",
		Question: "Rejection feedback (press Enter to send, Esc to go back):",
		Type:     interaction.QuestionTypeText,
		Metadata: interaction.QuestionMetadata{
			Title:    "Reject / Revise Plan",
			Priority: "high",
		},
	}

	onSubmit := func(resp interaction.QuestionResponse) {
		feedback := strings.TrimSpace(resp.Answer)
		if feedback == "" {
			feedback = "Please revise the plan."
		}
		a.planQuestionModal = nil
		a.pendingAnimCmd = func() tea.Msg {
			return planRejectedMsg{Feedback: feedback}
		}
	}

	onCancel := func() {
		// Esc on the feedback bar – go back to the choice bar.
		a.createPlanChoiceModal()
	}

	a.planQuestionModal = NewQuestionModal(textReq, 1, 1, onSubmit, onCancel)
}

// handlePlanApproved is called when the user approves the plan.
func (a *App) handlePlanApproved(msg planApprovedMsg) (tea.Model, tea.Cmd) {
	// Clear the approval bar and plan viewer
	a.planQuestionModal = nil
	a.planViewer = nil
	a.inPlanMode = false

	// Restore operating mode to "act" (implementation mode)
	a.operatingMode = "act"
	a.lastCommittedMode = "act"

	// Unblock the tool goroutine via the broker
	if a.planBroker != nil {
		plan := msg.Plan
		if plan == "" {
			plan = a.planContent
		}
		a.planBroker.RespondApproved(plan, msg.ClearContext)
	}
	a.planContent = ""

	// Add system message confirming approval
	statusMsg := Message{
		Role:      "system",
		Content:   "✓ **Plan approved.** Transitioning to implementation mode.",
		Timestamp: time.Now(),
	}
	a.messages = append(a.messages, statusMsg)
	a.invalidateViewportCache()
	a.updateViewportContent()
	a.sidePanelCache.valid = false

	// Trigger context compaction if:
	//   (a) user explicitly pressed "C" (ClearContext from modal keyboard), OR
	//   (b) the "Auto-compact Context After Approval" setting is enabled.
	//
	// The setting acts as an implicit "C" for every "Y" approval so users who
	// always want a clean slate don't have to remember to press C each time.
	shouldCompact := msg.ClearContext
	if !shouldCompact && a.settingsManager != nil {
		if planSettings := a.settingsManager.GetPlanSettings(); planSettings != nil {
			if planSettings.GetAutoClear() {
				shouldCompact = true
				logDebug("[PLAN] Auto-compact triggered by PlanSettings.AutoClear setting")
			}
		}
	}

	var cmds []tea.Cmd
	if shouldCompact {
		cmds = append(cmds, a.triggerCompactionCmd("plan_approval"))
	}

	return a, tea.Batch(cmds...)
}

// handlePlanRejected is called when the user rejects the plan.
func (a *App) handlePlanRejected(msg planRejectedMsg) (tea.Model, tea.Cmd) {
	// Clear the approval bar and plan viewer but keep plan mode active (agent will revise)
	a.planQuestionModal = nil
	a.planViewer = nil

	feedback := strings.TrimSpace(msg.Feedback)
	if feedback == "" {
		feedback = "Please revise the plan and try again."
	}

	// Unblock the tool goroutine with rejection
	if a.planBroker != nil {
		a.planBroker.RespondRejected(feedback)
	}
	a.planContent = ""

	// Add system message with the feedback
	sysMsg := Message{
		Role: "system",
		Content: "✗ **Plan rejected.** Feedback sent to Claude:\n\n> " +
			strings.ReplaceAll(feedback, "\n", "\n> "),
		Timestamp: time.Now(),
	}
	a.messages = append(a.messages, sysMsg)
	a.invalidateViewportCache()
	a.updateViewportContent()
	a.sidePanelCache.valid = false

	return a, nil
}

// triggerCompactionCmd returns a tea.Cmd that fires context compaction after
// plan approval.  Sends a CompactRequestMsg so the normal compact pipeline
// handles summarisation, model selection, and error reporting.
func (a *App) triggerCompactionCmd(_ string) tea.Cmd {
	return func() tea.Msg {
		logDebug("[PLAN] Triggering compaction after plan approval")
		return commands.CompactRequestMsg{
			Strategy: compaction.StrategyStandard,
			Manual:   true,
		}
	}
}

// planSessionFilePath returns the absolute path to this session's plan file.
func (a *App) planSessionFilePath() string {
	if a.rootSessionID == "" {
		return ""
	}
	return plan.PlanFilePath(plan.Config{SessionID: a.rootSessionID})
}

// readActivePlan reads the plan content from the session-scoped plan file.
// Returns "" if the file doesn't exist or the session dir is not configured.
func (a *App) readActivePlan() string {
	path := a.planSessionFilePath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
