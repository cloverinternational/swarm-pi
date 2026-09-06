package chat

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// autoModeVerdicts caches the classifier outcome for prompt-level requests so
// the modal can render the risk banner without re-running the classifier at
// render time. Keyed by request ID; entries are dropped in
// handleApprovalDecision / removeApprovalRequest.
//
// NOTE: accessed only from the UI goroutine (same as approvalQueue), so no
// locking is needed.
type autoModeVerdict struct {
	risk       string
	reason     string
	confidence float64
}

// enqueueApprovalRequest adds a new permission request to the queue.
// If no modal is currently shown it immediately shows the first request.
//
// When auto-mode is opted in (SWARM_AUTO_MODE_OPT_IN=1 or config flag), the
// request is first classified by the AutoModeHook's rule-based classifier:
//   - "approve" → short-circuit with DecisionApproveOnce (no modal)
//   - "deny"    → short-circuit with DecisionDeny (no modal)
//   - "prompt"  → queue as normal, stash verdict for the modal banner
func (a *App) enqueueApprovalRequest(req tools.PermissionApprovalRequest) {
	logDebug("[PERMISSIONS] Approval request enqueued for tool: %s, permission: %s", req.Tool, req.Permission)

	if a.maybeAutoHandleApproval(req) {
		return
	}

	a.approvalQueue = append(a.approvalQueue, req)
	if a.approvalModal == nil {
		logDebug("[PERMISSIONS] No modal showing, displaying approval request")
		a.showNextApproval()
		return
	}
	// Update the queue-depth indicator on the currently visible modal.
	// The shown item is always queue[0], so its index is 1/N.
	logDebug("[PERMISSIONS] Modal already showing, adding to queue (position: 1 of %d)", len(a.approvalQueue))
	a.approvalModal.SetQueueInfo(1, len(a.approvalQueue))
}

// maybeAutoHandleApproval consults the auto-mode classifier. Returns true
// when the request was fully resolved without user interaction (either
// auto-approved or auto-denied), so the caller should skip queuing.
//
// For "prompt" verdicts the classification is stashed in
// a.autoModeVerdicts so the modal can render a risk banner; returns false so
// the normal queue path runs.
func (a *App) maybeAutoHandleApproval(req tools.PermissionApprovalRequest) bool {
	if a.sdk == nil {
		return false
	}
	hm := a.sdk.GetHooksManager()
	if hm == nil {
		return false
	}
	ctx := context.Background()
	// Synthesize a tool-input map from fields that reach the approval layer.
	// The request does not carry the raw tool parameters, but Target and
	// Permission are enough for the rule-based classifier's allow/soft-deny
	// matching on tool names and simple string rules.
	toolInput := map[string]any{
		"target":     req.Target,
		"permission": req.Permission,
		"reason":     req.Reason,
	}
	res, ok := hm.ClassifyToolForAutoMode(ctx, req.Tool, toolInput)
	if !ok {
		return false // auto-mode not opted in — fall through
	}
	switch res.Recommendation {
	case "approve":
		logDebug("[PERMISSIONS] auto-mode approve tool=%s risk=%s conf=%.2f", req.Tool, res.Risk, res.Confidence)
		if a.permissionBroker != nil {
			if err := a.permissionBroker.RespondWithContext(req.RequestID, tools.DecisionApproveOnce, ""); err != nil {
				logDebug("[PERMISSIONS] auto-mode approve failed, falling back to modal: %v", err)
				return false
			}
		}
		// Inject the approval decision into the agent's context so it knows
		// this was auto-approved by rule — not by the user explicitly.
		// Without this, the agent may hallucinate that the user gave approval.
		approvalMsg := fmt.Sprintf("[auto-mode] %s was automatically approved (risk: %s, rule-based — not user approval). Reason: %s",
			req.Tool, res.Risk, res.Reason)
		hm.AddPendingToolMessage(approvalMsg)
		a.addNotification("info", i18n.T("classic_chat_3.permissions.auto_approved", req.Tool, res.Risk))
		return true
	case "deny":
		logDebug("[PERMISSIONS] auto-mode deny tool=%s reason=%s", req.Tool, res.Reason)
		if a.permissionBroker != nil {
			if err := a.permissionBroker.RespondWithContext(req.RequestID, tools.DecisionDeny, "auto-mode: "+res.Reason); err != nil {
				logDebug("[PERMISSIONS] auto-mode deny failed, falling back to modal: %v", err)
				return false
			}
		}
		// Inject the denial so the agent knows why the tool was blocked.
		denyMsg := fmt.Sprintf("[auto-mode] %s was automatically DENIED. Reason: %s. Do not retry this tool without user approval.",
			req.Tool, res.Reason)
		hm.AddPendingToolMessage(denyMsg)
		a.addNotification("warning", i18n.T("classic_chat_3.permissions.auto_denied", req.Tool, res.Reason))
		return true
	default:
		// "prompt" (or unknown) → keep the modal, but remember the verdict so
		// showNextApproval can surface the risk banner.
		if a.autoModeVerdicts == nil {
			a.autoModeVerdicts = make(map[string]autoModeVerdict)
		}
		a.autoModeVerdicts[req.RequestID] = autoModeVerdict{
			risk:       res.Risk,
			reason:     res.Reason,
			confidence: res.Confidence,
		}
		return false
	}
}

// removeApprovalRequest removes a request by ID (called on timeout / external cancel).
// If the currently displayed request is removed a new one is shown immediately.
func (a *App) removeApprovalRequest(requestID string, outcome tools.Outcome) {
	if len(a.approvalQueue) == 0 {
		return
	}

	idx := -1
	for i, req := range a.approvalQueue {
		if req.RequestID == requestID {
			idx = i
			break
		}
	}
	if idx == -1 {
		// Already handled (e.g. user responded just before timeout fired).
		return
	}

	a.approvalQueue = append(a.approvalQueue[:idx], a.approvalQueue[idx+1:]...)
	delete(a.autoModeVerdicts, requestID)

	if idx == 0 {
		// The visible item was removed externally — close the modal and show next.
		a.approvalModal = nil
		a.showNextApproval()
	} else if a.approvalModal != nil {
		// A queued (non-visible) item was removed; update the count indicator.
		a.approvalModal.SetQueueInfo(1, len(a.approvalQueue))
	}

	if outcome == tools.OutcomeTimeout {
		a.addNotification("warning", i18n.T("classic_chat_3.permissions.timed_out"))
	}
}

// showNextApproval creates an ApprovalModal for queue[0], or clears the modal if
// the queue is empty.
func (a *App) showNextApproval() {
	if len(a.approvalQueue) == 0 {
		a.approvalModal = nil
		return
	}
	req := a.approvalQueue[0]
	a.approvalModal = NewApprovalModal(req, 1, len(a.approvalQueue), func(decision tools.Decision, userContext string) {
		a.handleApprovalDecision(decision, userContext)
	})
	if v, ok := a.autoModeVerdicts[req.RequestID]; ok {
		a.approvalModal.SetAutoModeAnnotation(v.risk, v.reason, v.confidence)
	}
}

// handleApprovalDecision is called by the modal's onSelect callback when the
// user makes a decision.  It responds to the broker, applies any side-effects,
// removes queue[0] and advances to the next queued request (if any).
func (a *App) handleApprovalDecision(decision tools.Decision, userContext string) {
	if len(a.approvalQueue) == 0 {
		a.approvalModal = nil
		return
	}

	req := a.approvalQueue[0]

	// Remove from the queue *before* responding so that a concurrent timeout
	// firing for the same requestID in removeApprovalRequest finds idx==-1 and
	// does nothing (avoiding a double-remove / stale-waiter scenario).
	a.approvalQueue = a.approvalQueue[1:]
	a.approvalModal = nil
	delete(a.autoModeVerdicts, req.RequestID)

	// Respond to the broker (unblocks the SDK goroutine waiting for the result).
	// Track whether the response was delivered so we only apply side-effects on
	// success.  On failure we log/notify and advance the queue so the UI doesn't
	// stall, but we do NOT run stopAgent() or emit success notifications.
	if a.permissionBroker != nil {
		if err := a.permissionBroker.RespondWithContext(req.RequestID, decision, userContext); err != nil {
			logDebug("failed to respond to approval request %s: %v", req.RequestID, err)
			a.addNotification("error", i18n.T("classic_chat_3.permissions.response_failed", err))
			a.showNextApproval()
			return
		}
	}

	// Side-effects / notifications.
	switch decision {
	case tools.DecisionDenyStop:
		a.stopAgent()
	case tools.DecisionSaveProject:
		a.addNotification("success", i18n.T("classic_chat_3.permissions.saved_project"))
	case tools.DecisionApproveSession:
		a.addNotification("info", i18n.T("classic_chat_3.permissions.approved_session"))
	case tools.DecisionApproveAlways:
		a.addNotification("info", i18n.T("classic_chat_3.permissions.always_allowed"))
	}

	// Show the next queued request (if any).
	a.showNextApproval()
}
