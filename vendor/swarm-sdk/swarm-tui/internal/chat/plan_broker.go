package chat

import (
	"context"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plan"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/visual"
)

// ─── Plan Mode Messages ────────────────────────────────────────────────────────

// planEnterMsg is dispatched to the BubbleTea Update loop when the agent
// calls the enter_plan_mode tool.
type planEnterMsg struct{}

// planApprovalRequestMsg is dispatched to the BubbleTea Update loop when the
// agent calls exit_plan_mode. The UI must eventually call
// PlanBroker.Respond() with the user's decision.
type planApprovalRequestMsg struct {
	requestID string
	plan      string // The plan content submitted by the agent
}

// ─── PlanBroker ───────────────────────────────────────────────────────────────

// planApprovalWaiter tracks a single in-flight plan approval request.
type planApprovalWaiter struct {
	requestID string
	agentID   string
	plan      string
	respCh    chan planApprovalResult
}

type planApprovalResult struct {
	response plan.ApprovalResponse
	err      error
}

// PlanBroker bridges SDK plan mode requests into the BubbleTea TUI.
// It implements plan.PlanBroker so the SDK plan tools can communicate
// with the UI layer using the same blocking-channel pattern as QuestionBroker.
type PlanBroker struct {
	mu           sync.Mutex
	waiter       *planApprovalWaiter // at most one in-flight approval at a time
	dispatch     func(tea.Msg)
	visualReg    *visual.Registry
	sessionID    string
	currentPlans map[string]string // agentID → planID
}

func (b *PlanBroker) CurrentPlanID(_ context.Context, agentID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentPlans[agentID]
}

func (b *PlanBroker) SetCurrentPlan(agentID, planID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentPlans == nil {
		b.currentPlans = map[string]string{}
	}
	if planID == "" {
		delete(b.currentPlans, agentID)
		return
	}
	b.currentPlans[agentID] = planID
}

func (b *PlanBroker) pushPlanScreen(agentID, planID, title, markdown string) {
	b.mu.Lock()
	visualReg := b.visualReg
	sessionID := b.sessionID
	b.mu.Unlock()
	if visualReg == nil || sessionID == "" {
		return
	}
	srv, err := visualReg.Ensure(sessionID)
	if err != nil {
		return
	}
	srv.PushScreen(&visual.Screen{
		ID:        planID,
		AgentID:   agentID,
		Kind:      visual.ScreenKindPlan,
		Title:     title,
		Primitive: nil,
		CreatedAt: time.Now(),
		PlanState: visual.PlanStateProposed,
	})
}

func (b *PlanBroker) closePlanScreen(agentID, decision string) {
	b.mu.Lock()
	visualReg := b.visualReg
	sessionID := b.sessionID
	planID := b.currentPlans[agentID]
	b.mu.Unlock()
	if visualReg == nil || sessionID == "" || planID == "" {
		return
	}
	srv, err := visualReg.Ensure(sessionID)
	if err != nil {
		return
	}
	_ = srv.TransitionPlan(planID, decision)
}

// SetVisualRegistry wires a visual picker registry for plan rendering.
func (b *PlanBroker) SetVisualRegistry(r *visual.Registry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.visualReg = r
}

// SetSessionID sets the session ID used when Ensure()ing the visual server.
func (b *PlanBroker) SetSessionID(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessionID = id
}

// NewPlanBroker creates a new PlanBroker.
func NewPlanBroker() *PlanBroker {
	return &PlanBroker{}
}

// SetDispatcher wires the broker to the BubbleTea runtime.
// Must be called before any tool invocations.
func (b *PlanBroker) SetDispatcher(dispatch func(tea.Msg)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dispatch = dispatch
}

// EnterPlanMode implements plan.PlanBroker.
// It dispatches a planEnterMsg to the BubbleTea loop (non-blocking) and
// returns immediately once the message is queued.
func (b *PlanBroker) EnterPlanMode(ctx context.Context) error {
	b.mu.Lock()
	dispatch := b.dispatch
	b.mu.Unlock()

	agentID, _ := ctx.Value("agent_id").(string)
	planID := fmt.Sprintf("plan-%d", time.Now().UnixNano())
	b.SetCurrentPlan(agentID, planID)
	b.pushPlanScreen(agentID, planID, i18n.T("classic_chat_3.plan.in_progress"), "")

	if dispatch != nil {
		dispatch(planEnterMsg{})
	}
	return nil
}

// RequestPlanApproval implements plan.PlanBroker.
// It dispatches the plan content to the TUI and blocks until the user
// approves, rejects, or the context is cancelled.
//
// The caller detaches ordinary tool deadlines while preserving context values.
// A direct caller may still provide a cancellable context, and SDK shutdown
// cancels pending approval explicitly through CancelPending.
func (b *PlanBroker) RequestPlanApproval(ctx context.Context, planContent string) (plan.ApprovalResponse, error) {
	const reqID = "plan-approval" // Only one in-flight at a time
	agentID, _ := ctx.Value("agent_id").(string)

	waiter := &planApprovalWaiter{
		requestID: reqID,
		agentID:   agentID,
		plan:      planContent,
		respCh:    make(chan planApprovalResult, 1),
	}

	b.mu.Lock()
	if b.waiter != nil {
		b.mu.Unlock()
		return plan.ApprovalResponse{}, fmt.Errorf("plan approval already pending")
	}
	b.waiter = waiter
	dispatch := b.dispatch
	b.mu.Unlock()

	if dispatch == nil {
		// Headless: auto-approve
		b.mu.Lock()
		if b.waiter == waiter {
			b.waiter = nil
		}
		b.mu.Unlock()
		return plan.ApprovalResponse{
			Approved:   true,
			EditedPlan: planContent,
		}, nil
	}

	dispatch(planApprovalRequestMsg{requestID: reqID, plan: planContent})

	select {
	case result := <-waiter.respCh:
		return result.response, result.err
	case <-ctx.Done():
		b.mu.Lock()
		if b.waiter == waiter {
			b.waiter = nil
		}
		b.mu.Unlock()
		b.closePlanScreen(waiter.agentID, "reject")
		b.SetCurrentPlan(waiter.agentID, "")
		return plan.ApprovalResponse{}, ctx.Err()
	}
}

// RespondApproved sends an approval response to the waiting tool goroutine.
// Called by the TUI when the user approves the plan.
func (b *PlanBroker) RespondApproved(editedPlan string, clearContext bool) {
	b.mu.Lock()
	w := b.waiter
	b.waiter = nil
	b.mu.Unlock()

	if w == nil {
		return
	}
	b.closePlanScreen(w.agentID, "approve")
	b.SetCurrentPlan(w.agentID, "")
	select {
	case w.respCh <- planApprovalResult{response: plan.ApprovalResponse{
		Approved: true, EditedPlan: editedPlan, ClearContext: clearContext,
	}}:
	default:
	}
}

// RespondRejected sends a rejection response to the waiting tool goroutine.
// Called by the TUI when the user rejects the plan.
func (b *PlanBroker) RespondRejected(feedback string) {
	b.mu.Lock()
	w := b.waiter
	b.waiter = nil
	b.mu.Unlock()

	if w == nil {
		return
	}
	b.closePlanScreen(w.agentID, "reject")
	b.SetCurrentPlan(w.agentID, "")
	select {
	case w.respCh <- planApprovalResult{response: plan.ApprovalResponse{
		Approved: false, Feedback: feedback,
	}}:
	default:
	}
}

// CancelPending releases an in-flight approval during session/workspace
// shutdown. It is safe to call when no approval is pending.
func (b *PlanBroker) CancelPending(err error) {
	if err == nil {
		err = context.Canceled
	}
	b.mu.Lock()
	w := b.waiter
	b.waiter = nil
	b.mu.Unlock()
	if w == nil {
		return
	}
	b.closePlanScreen(w.agentID, "reject")
	b.SetCurrentPlan(w.agentID, "")
	select {
	case w.respCh <- planApprovalResult{err: err}:
	default:
	}
}

// HasPendingApproval returns true if a plan is awaiting user approval.
func (b *PlanBroker) HasPendingApproval() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.waiter != nil
}

// PendingPlan returns the plan content awaiting approval, or "" if none.
func (b *PlanBroker) PendingPlan() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiter == nil {
		return ""
	}
	return b.waiter.plan
}
